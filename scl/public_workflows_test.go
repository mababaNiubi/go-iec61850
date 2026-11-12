// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func stationXML(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../examples/scd/station/station.scd")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMMSBatchReadUsesOneAcquisition(t *testing.T) {
	d := device(t, stationXML(t))
	ctx, _, client := serviceRuntime(t, d)
	signal, err := d.Signal("CTRL", "XCBR1", "ST", "Pos", "stVal")
	if err != nil {
		t.Fatal(err)
	}
	update := func(n int64) error {
		position := scl.Open
		quality := ied.Quality(0)
		if n%2 == 1 {
			position = scl.Closed
			quality = ied.OldData
		}
		v, _ := position.MMSValue()
		q, _ := quality.MMSValue()
		return d.Update(ctx, signal.Sample(v, q, mms.UTC(time.Unix(n, 0)))...)
	}
	if err = update(100); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		for n := int64(101); ; n++ {
			if run.Err() != nil {
				done <- nil
				return
			}
			if e := update(n); e != nil {
				done <- e
				return
			}
			runtime.Gosched()
		}
	}()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	missing := mms.Access(mms.DomainName(signal.Value.Domain, "missing"))
	for i := 0; i < 100; i++ {
		results, e := client.ReadMultiple(ctx, mms.Access(signal.Value), mms.Access(signal.Quality), mms.Access(signal.Time), missing)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range results[:3] {
			if r.Error != nil {
				t.Fatal(r.Error)
			}
		}
		if results[3].Error == nil {
			t.Fatal("bad member hid its own access error")
		}
		position, e := scl.ReadPosition(results[0].Value)
		if e != nil {
			t.Fatal(e)
		}
		q, e := ied.DecodeQuality(results[1].Value)
		if e != nil {
			t.Fatal(e)
		}
		at, e := results[2].Value.Time()
		if e != nil {
			t.Fatal(e)
		}
		odd := at.Unix()%2 == 1
		if (position == scl.Closed) != odd || q.Has(ied.OldData) != odd {
			t.Fatalf("mixed acquisition: %s q=%v time=%v", position, q, at)
		}
	}
}

func TestGOOSELayoutRetainsSelectedArrayChannel(t *testing.T) {
	x := strings.Replace(stationXML(t),
		`<DO name="TotW" type="Power"/>`,
		`<DO name="TotW" type="Power"/><DO name="Harm" type="Power" count="2"/>`, 1)
	x = strings.Replace(x, `appID="BayPosition" datSet="dsPos"`, `appID="BayPosition" datSet="dsHarm"`, 1)
	build := func(index string) *scl.Stream {
		set := `<DataSet name="dsHarm"><FCDA ldInst="CTRL" lnClass="MMXU" lnInst="1" doName="Harm" fc="MX" ix="` + index + `"/></DataSet><DataSet name="dsPos">`
		d := device(t, strings.Replace(x, `<DataSet name="dsPos">`, set, 1))
		g, e := d.Stream("CTRL", "gcbPos")
		if e != nil {
			t.Fatal(e)
		}
		return g
	}
	a, b := build("0"), build("1")
	if a.Layout().CheckCompatible(b.Layout()) == nil {
		t.Fatal("different channels compared equal")
	}
	layout := a.Layout()
	*layout.Members[0].Selection.Index = 99
	if *a.Layout().Members[0].Selection.Index != 0 {
		t.Fatal("caller mutated the model selection")
	}
}

func TestEngineeringDiagnosticsAndReportSettings(t *testing.T) {
	x := strings.Replace(stationXML(t), `<LNodeType id="Meter" lnClass="MMXU">`, `<LNodeType id="Meter" lnClass="MMXN">`, 1)
	s, e := scl.Parse(strings.NewReader(x))
	if e != nil {
		t.Fatal(e)
	}
	catalog, e := s.Catalog("BayIED", "S1")
	if e != nil || len(catalog.Diagnostics) == 0 {
		t.Fatal("catalog accepted a mismatched LN type", e)
	}
	if _, e = s.Device("BayIED", "S1"); e == nil {
		t.Fatal("device accepted mismatched LN type")
	}
	x = strings.Replace(stationXML(t), `<IED name="BayIED">`, `<IED name="BayIED"><Services><ReportSettings rptID="Dyn"/></Services>`, 1)
	d := device(t, x)
	ctx, _, client := serviceRuntime(t, d)
	base := "LLN0$RP$events01$"
	if e = client.Write(ctx, mms.DomainName("BayIEDCTRL", base+"RptID"), mms.VisibleString("new-id")); e != nil {
		t.Fatal(e)
	}
	if e = client.Write(ctx, mms.DomainName("BayIEDCTRL", base+"BufTm"), mms.Unsigned(1)); !errors.Is(e, mms.ObjectAccessDenied) {
		t.Fatal("fixed BufTm was writable", e)
	}
}

func TestOnlineDiscoveryAndIndependentCurrentSnapshot(t *testing.T) {
	d := device(t, stationXML(t))
	ctx, _, client := serviceRuntime(t, d)
	directory, e := client.Discover(ctx, ied.DiscoveryOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(directory.Reports) != 2 || len(directory.DataSets) != 2 || len(directory.Objects) == 0 {
		t.Fatalf("incomplete discovery: %+v", directory)
	}
	for _, block := range directory.Reports {
		if block.Error != "" {
			t.Fatal(block)
		}
	}
	block := mms.DomainName("BayIEDCTRL", "LLN0$BR$history01")
	first, e := client.SubscribeBRCB(ctx, block, ied.ReportOptions{GI: true})
	if e != nil {
		t.Fatal(e)
	}
	oldGI := nextReport(t, ctx, first)
	if e = first.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	signal, e := d.Signal("CTRL", "XCBR1", "ST", "Pos", "stVal")
	if e != nil {
		t.Fatal(e)
	}
	q, _ := ied.Quality(0).MMSValue()
	for _, position := range []scl.Position{scl.Open, scl.Closed} {
		v, _ := position.MMSValue()
		if e = d.Update(ctx, signal.Sample(v, q, mms.UTC(time.Now()))...); e != nil {
			t.Fatal(e)
		}
	}
	second, e := client.SubscribeBRCB(ctx, block, ied.ReportOptions{ResumeEntryID: make([]byte, 8), GI: true})
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	snapshot, e := second.ReadSnapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	leaves, e := snapshot.Leaves()
	if e != nil {
		t.Fatal(e)
	}
	seen := false
	for _, leaf := range leaves {
		if leaf.Variable.Name == signal.Value {
			p, e := scl.ReadPosition(leaf.Value)
			if e != nil || p != scl.Closed {
				t.Fatal(p, e)
			}
			seen = true
		}
	}
	if !seen {
		t.Fatal("confirmed snapshot lost FCD leaves")
	}
	for i := 0; i < 3; i++ {
		r := nextReport(t, ctx, second)
		if string(r.EntryID) == string(oldGI.EntryID) {
			t.Fatal("older GI remained after new GI request")
		}
	}
}
