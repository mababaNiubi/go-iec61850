// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"encoding/binary"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/sv"
)

func TestSVEngineeringLayoutsAndWidths(t *testing.T) {
	system, err := ParseFile("testdata/sv-lab.scd")
	if err != nil {
		t.Fatal(err)
	}
	p, err := system.SVStream("MUA/AP1/LD0/MSVCB01")
	if err != nil {
		t.Fatal(err)
	}
	if p.Layout.Size != 64 || p.ASDUCount != 2 || p.APPID != 0x4001 || len(p.Layout.Members) != 16 {
		t.Fatal(p)
	}
	field, err := p.Layout.Field("TCTR1$MX$Amp$instMag", "i")
	if err != nil || field.Offset != 0 {
		t.Fatal(field, err)
	}
	quality, err := p.Layout.QualityFor(field)
	if err != nil || quality == nil || quality.Offset != 4 {
		t.Fatal(quality, err)
	}
	other, err := system.SVStream("MUB/AP1/LD0/MSVCB01")
	if err != nil || !p.Layout.Compatible(other.Layout) {
		t.Fatal(err)
	}
	for _, hz := range []uint16{50, 60} {
		cfg, err := p.SubscriptionConfig(sv.SubscriptionOptions{NominalFrequency: hz})
		if err != nil || cfg.CounterModulus != uint32(hz)*80 {
			t.Fatal(cfg, err)
		}
	}
	system, err = ParseFile("testdata/sv-types.scd")
	if err != nil {
		t.Fatal(err)
	}
	p, err = system.SVStream("TypedMU/AP1/LD0/SV1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Layout.Size != 24 || p.Layout.Members[0].Fields[0].Width != 4 {
		t.Fatal(p.Layout)
	}
	raw := make([]byte, 24)
	binary.BigEndian.PutUint32(raw, 0xfffffffe)
	binary.BigEndian.PutUint64(raw[16:], math.MaxInt64)
	for _, tc := range []struct {
		path  string
		value data.Value
	}{{"mode", data.Integer(-2)}, {"large", data.Integer(math.MaxInt64)}} {
		f, err := p.Layout.Field("0", tc.path)
		if err != nil {
			t.Fatal(err)
		}
		value, err := f.Decode(raw)
		if err != nil || !value.Equal(tc.value) {
			t.Fatal(value, err)
		}
		if err := f.Encode(raw, tc.value); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSVRejectsBadProfilesAndPreservesDeclarationOrder(t *testing.T) {
	model, err := os.ReadFile("testdata/sv-lab.scd")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range [][2]string{{`smpRate="80"`, `smpRate="0"`}, {`nofASDU="2"`, `nofASDU="255"`}, {`multicast="true"`, `multicast="false"`}, {`security="false"`, `security="true"`}, {`type="APPID">4001`, `type="APPID">0001`}, {`type="VLAN-ID">007`, `type="VLAN-ID">FFF`}, {`bType="INT32"`, `bType="VisString255"`}} {
		s, err := Parse(strings.NewReader(strings.Replace(string(model), change[0], change[1], 1)))
		if err == nil {
			_, err = s.SVStream("MUA/AP1/LD0/MSVCB01")
		}
		if err == nil {
			t.Fatal("accepted invalid profile", change)
		}
	}
	// A DA before an SDO must stay before it in seqData, regardless of the
	// separate indexed slices used by the MMS model compiler.
	s, err := Parse(strings.NewReader(`<SCL xmlns="http://www.iec.ch/61850/2003/SCL"><IED name="I"><AccessPoint name="A"/></IED><DataTypeTemplates><DOType id="A"><DA name="first" bType="INT8" fc="MX"/><SDO name="nested" type="B"/><DA name="last" bType="INT16" fc="MX"/></DOType><DOType id="B"><DA name="value" bType="INT32" fc="MX"/></DOType></DataTypeTemplates></SCL>`))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := (svCompiler{system: s}).flatten(s.doTypes["A"].order, nil, "MX", "", 0)
	if err != nil || len(fields) != 3 || fields[0].Path != "first" || fields[1].Path != "nested.value" || fields[2].Path != "last" {
		t.Fatal(fields, err)
	}
}
