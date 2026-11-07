// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

func serviceRuntime(t *testing.T, d *scl.IED) (context.Context, *simulator.Runtime, *ied.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	r, err := simulator.Start(ctx, d, simulator.Options{Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEOff})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	c, err := ied.Dial(ctx, r.Address(), ied.Config{MMS: d.ClientConfig()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return ctx, r, c
}
func nextReport(t *testing.T, ctx context.Context, s *ied.ReportSubscription) ied.Report {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok || e.Err != nil {
			t.Fatalf("report closed=%v err=%v", !ok, e.Err)
		}
		return e.Report
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ied.Report{}
	}
}
func TestEngineeredReportsQualityGIAndBufferedResume(t *testing.T) {
	d := device(t, fixture(t))
	ctx, r, c := serviceRuntime(t, d)
	block := mms.DomainName("BayIEDCTRL", "LLN0$RP$events01")
	s, err := c.SubscribeURCB(ctx, block, ied.ReportOptions{GI: true})
	if err != nil {
		t.Fatal(err)
	}
	if report := nextReport(t, ctx, s); len(report.Entries) != 3 {
		t.Fatal(report)
	}
	q := lookup(t, d, "XCBR1", "ST", "Pos.q")
	bad, _ := ied.Quality(ied.OldData).MMSValue()
	if err := d.Update(ctx, mms.Assignment{Name: q, Value: bad}); err != nil {
		t.Fatal(err)
	}
	if report := nextReport(t, ctx, s); len(report.Entries) != 1 || report.Entries[0].Variable.Name != q {
		t.Fatal(report)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	history := mms.DomainName("BayIEDCTRL", "LLN0$BR$history01")
	b, err := c.SubscribeBRCB(ctx, history, ied.ReportOptions{PurgeBuffer: true, GI: true})
	if err != nil {
		t.Fatal(err)
	}
	baseline := nextReport(t, ctx, b)
	if len(baseline.EntryID) != 8 {
		t.Fatal(baseline)
	}
	if err := c.Conclude(ctx); err != nil {
		t.Fatal(err)
	}
	position := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	stamp := lookup(t, d, "XCBR1", "ST", "Pos.t")
	sourceTime := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	for i, state := range []scl.Position{scl.Closed, scl.Open} {
		v, _ := state.MMSValue()
		if err := d.Update(ctx, mms.Assignment{Name: position, Value: v}, mms.Assignment{Name: stamp, Value: mms.UTC(sourceTime.Add(time.Duration(i) * time.Second))}); err != nil {
			t.Fatal(err)
		}
	}
	c2, err := ied.Dial(ctx, r.Address(), ied.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	b2, err := c2.SubscribeBRCB(ctx, history, ied.ReportOptions{ResumeEntryID: baseline.EntryID})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []scl.Position{scl.Closed, scl.Open} {
		report := nextReport(t, ctx, b2)
		if len(report.Entries) != 1 {
			t.Fatal(report)
		}
		leaves, err := report.Leaves()
		if err != nil || len(leaves) != 3 {
			t.Fatal(leaves, err)
		}
		got, err := scl.ReadPosition(leaves[0].Value)
		if err != nil || got != want {
			t.Fatal(got, err)
		}
		when, err := leaves[2].Value.Time()
		if err != nil || !when.Equal(sourceTime.Add(time.Duration(i)*time.Second)) {
			t.Fatal("source event time lost", when, err)
		}
	}
}

func TestSBOEnhancedOwnershipAndTermination(t *testing.T) {
	x := strings.Replace(fixture(t), "<Val>direct-with-normal-security</Val>", "<Val>sbo-with-enhanced-security</Val>", 1)
	x = strings.Replace(x, `<DA name="Oper"`, `<DA name="SBOw" bType="Struct" type="Operate" fc="CO"/><DA name="Cancel" bType="Struct" type="Operate" fc="CO"/><DA name="Oper"`, 1)
	d := device(t, x)
	op := lookup(t, d, "XCBR1", "CO", "Pos.Oper")
	if err := d.OnControl(op, func(ctx context.Context, c scl.Command) error {
		if c.Test {
			return mms.ObjectAccessDenied
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, r, c := serviceRuntime(t, d)
	other, err := ied.Dial(ctx, r.Address(), ied.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	command, _ := (scl.Command{Value: true, OriginCategory: 2, OriginID: []byte("owner"), Number: 9, Time: time.Now()}).MMSValue()
	selectName := lookup(t, d, "XCBR1", "CO", "Pos.SBOw")
	if err := c.Write(ctx, selectName, command); err != nil {
		t.Fatal(err)
	}
	if err := other.Write(ctx, selectName, command); !errors.Is(err, mms.TemporarilyUnavailable) {
		t.Fatal(err)
	}
	if err := c.Write(ctx, lookup(t, d, "XCBR1", "CO", "Pos.Cancel"), command); err != nil {
		t.Fatal(err)
	}
	result, err := other.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", mms.Boolean(true), ied.ControlOptions{})
	if err != nil || !result.Selected || !result.Terminated {
		t.Fatal(result, err)
	}
}

func TestSettingGroupsEditIsolationAndActivate(t *testing.T) {
	x := strings.Replace(fixture(t), `<LN0 lnClass="LLN0" inst="" lnType="CommonLLN0">`, `<LN0 lnClass="LLN0" inst="" lnType="CommonLLN0"><SettingControl numOfSGs="2" actSG="1"/>`, 1)
	x = strings.Replace(x, `<DO name="TotW" type="Power"/>`, `<DO name="TotW" type="Power"/><DO name="Limit" type="SettingLimit"/>`, 1)
	x = strings.Replace(x, "</DataTypeTemplates>", `<DOType id="SettingLimit" cdc="ASG"><DA name="setMag" bType="FLOAT32" fc="SG"><Val sGroup="1">10</Val><Val sGroup="2">20</Val></DA></DOType></DataTypeTemplates>`, 1)
	d := device(t, x)
	ctx, _, c := serviceRuntime(t, d)
	sg := lookup(t, d, "MMXU1", "SG", "Limit.setMag")
	se := lookup(t, d, "MMXU1", "SE", "Limit.setMag")
	base := "LLN0$SP$SGCB$"
	selected := mms.VariableAccess{Name: mms.DomainName(sg.Domain, "MMXU1"), Selection: mms.Selection{Component: "SE$Limit$setMag"}}
	if _, err := c.MMS().ReadAccess(ctx, selected); !errors.Is(err, mms.TemporarilyUnavailable) {
		t.Fatal("alternate access bypassed edit reservation", err)
	}
	write := func(field string, v mms.Value) {
		t.Helper()
		if err := c.Write(ctx, mms.DomainName(sg.Domain, base+field), v); err != nil {
			t.Fatal(err)
		}
	}
	write("EditSG", mms.Unsigned(2))
	if err := c.Write(ctx, se, mms.Float32(25)); err != nil {
		t.Fatal(err)
	}
	editValue, err := c.MMS().ReadAccess(ctx, selected)
	edited, _ := editValue.Float()
	if err != nil || edited != 25 {
		t.Fatal("alternate access returned a stale edit view", editValue, err)
	}
	v, err := c.Read(ctx, sg)
	number, _ := v.Float()
	if err != nil || number != 10 {
		t.Fatal(v, err)
	}
	write("CnfEdit", mms.Boolean(true))
	write("ActSG", mms.Unsigned(2))
	v, err = c.Read(ctx, sg)
	number, _ = v.Float()
	if err != nil || number != 25 {
		t.Fatal(v, err)
	}
}
