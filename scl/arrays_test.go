// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"strings"
	"testing"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
)

func TestSCLArraysInitialValuesIndexedDatasetAndTriggers(t *testing.T) {
	x := fixture(t)
	x = strings.Replace(x, `<DO name="TotW" type="Power"/>`, `<DO name="TotW" type="Power"/><DO name="Harm" type="Power" count="2"/>`, 1)
	x = strings.Replace(x, `<LN lnClass="MMXU" inst="1" lnType="Meter"/>`, `<LN lnClass="MMXU" inst="1" lnType="Meter"><DOI name="Harm" ix="1"><SDI name="mag"><DAI name="f"><Val>12.5</Val></DAI></SDI><DAI name="samples" ix="2"><Val>7</Val></DAI></DOI></LN>`, 1)
	x = strings.Replace(x, `<DA name="mag" bType="Struct" type="Analogue" fc="MX"`, `<DA name="samples" bType="INT32" count="3" fc="MX" dchg="true"><Val>3</Val></DA><DA name="mag" bType="Struct" type="Analogue" fc="MX"`, 1)
	x = strings.Replace(x, `<DataSet name="dsPos">`, `<DataSet name="dsHarm"><FCDA ldInst="CTRL" lnClass="MMXU" lnInst="1" doName="Harm" fc="MX" ix="0"/><FCDA ldInst="CTRL" lnClass="MMXU" lnInst="1" doName="Harm" fc="MX" ix="1"/></DataSet><DataSet name="dsPos">`, 1)
	x = strings.Replace(x, `name="events" datSet="dsPos"`, `name="events" datSet="dsHarm"`, 1)
	d := device(t, x)
	n := lookup(t, d, "MMXU1", "MX", "Harm")
	ctx, _, c := serviceRuntime(t, d)
	spec, err := c.GetVariableAccessAttributes(ctx, n)
	if err != nil || spec.Kind != mms.KindArray || spec.Length != 2 {
		t.Fatal(spec, err)
	}
	value, err := c.Read(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]mms.Value{"1.mag.f": mms.Float32(12.5), "1.samples.2": mms.Integer(7), "0.samples.2": mms.Integer(3)} {
		_, indices, err := data.ResolveComponent(spec, path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := data.ValueAt(value, indices)
		if err != nil || !got.Equal(want) {
			t.Fatal(path, got, err)
		}
	}
	s, err := c.SubscribeURCB(ctx, mms.DomainName(n.Domain, "LLN0$RP$events01"), ied.ReportOptions{GI: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if report := nextReport(t, ctx, s); len(report.Entries) != 2 {
		t.Fatal(report)
	}
	_, indices, err := data.ResolveComponent(spec, "1.mag.f")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := data.WithComponent(value, indices, mms.Float32(99))
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Update(ctx, mms.Assignment{Name: n, Value: changed}); err != nil {
		t.Fatal(err)
	}
	report := nextReport(t, ctx, s)
	if len(report.Entries) != 1 || report.Entries[0].Variable.Selection.Index == nil || *report.Entries[0].Variable.Selection.Index != 1 {
		t.Fatalf("array element isolation: %+v", report)
	}
	_, indices, err = data.ResolveComponent(spec, "1.q")
	if err != nil {
		t.Fatal(err)
	}
	quality, _ := ied.Quality(ied.OldData).MMSValue()
	changed, err = data.WithComponent(changed, indices, quality)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Update(ctx, mms.Assignment{Name: n, Value: changed}); err != nil {
		t.Fatal(err)
	}
	report = nextReport(t, ctx, s)
	reason, _ := report.Entries[0].Reason.Bytes()
	if len(report.Entries) != 1 || len(reason) != 1 || reason[0] != 0x20 {
		t.Fatalf("quality-only array event lost/misclassified: %+v", report)
	}
	outside := mms.Access(n)
	index := uint32(2)
	outside.Selection.Index = &index
	results, err := c.ReadMultiple(ctx, outside)
	if err == nil && (len(results) != 1 || results[0].Error == nil) {
		t.Fatal("out-of-range array access accepted", results)
	}
}
