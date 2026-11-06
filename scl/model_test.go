// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "examples", "scd", "breaker", "bay.scd"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func device(t *testing.T, xml string) *scl.IED {
	t.Helper()
	s, err := scl.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Device("BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func lookup(t *testing.T, d *scl.IED, ln, fc, path string) mms.ObjectName {
	t.Helper()
	n, err := d.Lookup("CTRL", ln, fc, path)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEngineeredNamesAndFCDSubscription(t *testing.T) {
	// A custom LD name, LN prefix, nested instance override and whole-DO FCDA
	// distinguish model-derived settings from hard-coded names or member indices.
	x := strings.Replace(fixture(t), `<LDevice inst="CTRL">`, `<LDevice inst="CTRL" ldName="FeedOne">`, 1)
	x = strings.ReplaceAll(x, `lnClass="XCBR" lnInst=`, `prefix="B" lnClass="XCBR" lnInst=`)
	x = strings.ReplaceAll(x, `<LN lnClass="XCBR"`, `<LN prefix="B" lnClass="XCBR"`)
	x = strings.Replace(x, `<LN lnClass="MMXU" inst="1" lnType="Meter"/>`, `<LN lnClass="MMXU" inst="1" lnType="Meter"><DOI name="TotW"><SDI name="mag"><DAI name="f"><Val>42.5</Val></DAI></SDI></DOI></LN>`, 1)
	first := strings.Index(x, "<FCDA ")
	end := strings.Index(x, "</DataSet>")
	x = x[:first] + `<FCDA ldInst="CTRL" lnClass="MMXU" lnInst="1" doName="TotW" daName="mag.f" fc="MX"/>
              <FCDA ldInst="CTRL" prefix="B" lnClass="XCBR" lnInst="1" doName="Pos" fc="ST"/>` + x[end:]
	x = strings.Replace(x, `<P type="IP">`, `<P type="OSI-TSEL">00A3</P><P type="IP">`, 1)
	d := device(t, x)
	ctx := context.Background()
	if d.MMSAddress() != "127.0.0.1:8102" || !bytes.Equal(d.ClientConfig().ISO.RemoteTSelector, []byte{0, 0xa3}) {
		t.Fatal("wrong MMS endpoint")
	}
	p := lookup(t, d, "MMXU1", "MX", "TotW.mag.f")
	if p.String() != "FeedOne/MMXU1$MX$TotW$mag$f" {
		t.Fatal(p)
	}
	v, err := d.Read(ctx, mms.Access(p))
	if err != nil {
		t.Fatal(err)
	}
	watts, err := v.Float()
	if err != nil || watts != 42.5 {
		t.Fatal(watts, err)
	}
	if _, err := d.Resolve("FeedOne/MMXU1.TotW.mag.f", "ST"); !errors.Is(err, mms.ObjectNonExistent) {
		t.Fatal(err)
	}
	in, err := d.Input("StationHMI", "S1", "breakerPosition")
	if err != nil {
		t.Fatal(err)
	}
	info := in.Stream.Info()
	if in.Index != 1 || in.Name.String() != "FeedOne/BXCBR1$ST$Pos$stVal" || info.Reference != "FeedOne/LLN0$GO$gcbPos" || info.DataSet != "FeedOne/LLN0$dsPos" || info.APPID != 0x1001 || info.VLAN.ID != 123 || info.VLAN.Priority != 4 {
		t.Fatalf("unexpected binding: %+v %+v", in, info)
	}
	set, err := d.DataSet("CTRL", "LLN0", "dsPos")
	if err != nil || set.Variables[1].Name != info.Members[1] {
		t.Fatal(set, err)
	}
	fd, err := d.Read(ctx, set.Variables[1])
	if err != nil {
		t.Fatal(err)
	}
	event := goose.Event{Kind: goose.StateChanged, Frame: goose.Frame{PDU: goose.PDU{GoCBRef: info.Reference, DataSet: info.DataSet, ConfRev: info.ConfRev, Values: []mms.Value{v, fd}}}}
	value, err := in.Value(event)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := scl.ReadPosition(value)
	if err != nil || pos != scl.Intermediate {
		t.Fatal(pos, err)
	}
	event.Kind = goose.Expired
	if _, err = in.Value(event); err == nil {
		t.Fatal("expired data delivered")
	}
}

func TestInvalidEngineeringFailsAtTheRelevantBoundary(t *testing.T) {
	original := fixture(t)
	for _, tc := range []struct{ name, from, to string }{
		{"missing DAType", `type="Analogue"`, `type="NoSuchType"`},
		{"cyclic DAType", `<BDA name="f" bType="FLOAT32"/>`, `<BDA name="f" bType="Struct" type="Analogue"/>`},
		{"indexed member", `daName="stVal" fc="ST"`, `daName="stVal" ix="1" fc="ST"`},
		{"missing dataset attribute", `daName="stVal" fc="ST"`, `daName="missing" fc="ST"`},
		{"missing GSE route", `cbName="gcbPos"`, `cbName="other"`},
		{"reserved VLAN", `>07B</P>`, `>FFF</P>`},
		{"secured GOOSE", `appID="BayPosition"`, `appID="BayPosition" securityEnable="Signature"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := scl.Parse(strings.NewReader(strings.Replace(original, tc.from, tc.to, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Device("BayIED", "S1"); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	for _, x := range []string{original + original, strings.Replace(original, "http://www.iec.ch/61850/2003/SCL", "urn:other", 1), strings.Replace(original, "<SCL ", "<!DOCTYPE x><SCL ", 1)} {
		if _, err := scl.Parse(strings.NewReader(x)); err == nil {
			t.Fatal("invalid XML accepted")
		}
	}
	// A vendor extension with the same local element name must not change the route.
	x := strings.Replace(original, `<P type="APPID">`, `<ext:P xmlns:ext="urn:vendor" type="APPID">FFFF</ext:P><P type="APPID">`, 1)
	if _, err := device(t, x).Input("StationHMI", "S1", "missing"); err == nil {
		t.Fatal("missing ExtRef accepted")
	}
}

func TestDiscoverInputsOnClientOnlyAccessPoint(t *testing.T) {
	x := fixture(t)
	start := strings.Index(x, `<IED name="StationHMI">`)
	end := start + strings.Index(x[start:], "</IED>") + len("</IED>")
	client := `<IED name="StationHMI"><AccessPoint name="Client"><LN lnClass="MMXU" inst="1" lnType="Meter"><Inputs>
	<ExtRef iedName="BayIED" ldInst="CTRL" lnClass="XCBR" lnInst="1" doName="Pos" daName="stVal" intAddr="remotePosition" serviceType="GOOSE" srcLDInst="CTRL" srcLNClass="LLN0" srcCBName="gcbPos"/>
	</Inputs></LN></AccessPoint></IED>`
	x = x[:start] + client + x[end:]
	s, err := scl.Parse(strings.NewReader(x))
	if err != nil {
		t.Fatal(err)
	}
	aps := s.AccessPoints()
	if len(aps) != 2 || !aps[0].HasServer || aps[1].HasServer || aps[1].AP != "Client" {
		t.Fatal(aps)
	}
	refs, err := s.GOOSEInputs("StationHMI", "Client")
	if err != nil || len(refs) != 1 || refs[0].DataPath != "Pos.stVal" || refs[0].IntAddr != "remotePosition" {
		t.Fatal(refs, err)
	}
	d, err := s.Device("BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	in, err := d.Input("StationHMI", "Client", refs[0].IntAddr)
	if err != nil || in.Index != 0 || in.Name.Item != "XCBR1$ST$Pos$stVal" {
		t.Fatal(in, err)
	}
	if _, err = s.Device("StationHMI", "Client"); err == nil {
		t.Fatal("client-only AP instantiated as a server")
	}
}

func TestLocalAcquisitionIsAtomicAndNetworkAccessIsReadOnly(t *testing.T) {
	d := device(t, fixture(t))
	ctx := context.Background()
	pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	q := lookup(t, d, "XCBR1", "ST", "Pos.q")
	closed, _ := scl.Closed.MMSValue()
	err := d.Update(ctx, mms.Assignment{Name: pos, Value: closed}, mms.Assignment{Name: q, Value: mms.Boolean(true)})
	if !errors.Is(err, mms.TypeInconsistent) {
		t.Fatal(err)
	}
	v, err := d.Read(ctx, mms.Access(pos))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := scl.ReadPosition(v)
	if got != scl.Intermediate {
		t.Fatal("first change survived failed batch")
	}
	if err := d.Write(ctx, mms.Access(pos), closed); !errors.Is(err, mms.ObjectAccessDenied) {
		t.Fatal(err)
	}
}
