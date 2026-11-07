// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

func TestCatalogEquipmentPartialModelAndOnlineOrder(t *testing.T) {
	x := strings.Replace(fixture(t), `<DO name="TotW" type="Power"/>`, `<DO name="TotW" type="Power"/><DO name="Unknown" type="Missing"/>`, 1)
	system, err := scl.Parse(strings.NewReader(x))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := system.Catalog("BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Diagnostics) != 1 || len(catalog.Equipment) != 1 {
		t.Fatal(catalog.Diagnostics, catalog.Equipment)
	}
	found := false
	for _, p := range catalog.Points {
		if p.Reference == "BayIEDCTRL/XCBR1.Pos.stVal" {
			found = len(p.Equipment) == 1 && p.Equipment[0] == "Demo/E1/Q01/Q0" && p.Quality.Item == "XCBR1$ST$Pos$q" && p.Timestamp.Item == "XCBR1$ST$Pos$t"
		}
	}
	if !found {
		t.Fatal("equipment/value/quality/time binding missing")
	}
	d := device(t, fixture(t))
	ctx, _, c := serviceRuntime(t, d)
	system, err = scl.Parse(strings.NewReader(fixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = system.Catalog("BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	issues, err := catalog.CheckOnline(ctx, c.MMS())
	if err != nil || len(issues) != 0 {
		t.Fatal(issues, err)
	}
	set, err := d.DataSet("CTRL", "LLN0", "dsPos")
	if err != nil {
		t.Fatal(err)
	}
	set.Variables[0], set.Variables[1] = set.Variables[1], set.Variables[0]
	if err := scl.CheckDataSet(ctx, c.MMS(), set); err == nil {
		t.Fatal("reordered engineering dataset accepted")
	}
}

func TestSPRangeAndReadOnlySettingArePreserved(t *testing.T) {
	raw, err := os.ReadFile("../examples/ied/settings/device.scd")
	if err != nil {
		t.Fatal(err)
	}
	x := strings.Replace(string(raw), `<LN lnClass="PTOC" inst="1" lnType="Protection"/>`, `<LN lnClass="PTOC" inst="1" lnType="Protection"><DOI name="StrVal"><SDI name="setMag"><DAI name="f" valKind="RO"/></SDI></DOI></LN>`, 1)
	d := device(t, x)
	delay := lookup(t, d, "PTOC1", "SP", "OpDlTmms.setVal")
	if err := d.OnSetpoint(delay, func(ctx context.Context, v mms.Value) error {
		value, err := v.Int()
		if err != nil || value > 5000 || value < 0 {
			return mms.ObjectValueInvalid
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, _, c := serviceRuntime(t, d)
	if err := c.Write(ctx, delay, mms.Integer(10000)); !errors.Is(err, mms.ObjectValueInvalid) {
		t.Fatal(err)
	}
	v, err := c.Read(ctx, delay)
	value, _ := v.Int()
	if err != nil || value != 100 {
		t.Fatal(v, err)
	}
	edit := lookup(t, d, "PTOC1", "SE", "StrVal.setMag.f")
	result, err := c.EditSettings(ctx, edit.Domain, 2, map[mms.ObjectName]mms.Value{edit: mms.Float32(25)}, false)
	if !errors.Is(err, mms.ObjectAccessDenied) || result.Committed {
		t.Fatal("read-only setting edited", result, err)
	}
	groups, err := c.SettingGroups(ctx, edit.Domain)
	if err != nil || groups.Editing != 0 || groups.Active != 1 {
		t.Fatal("failed edit reservation retained", groups, err)
	}
}
func TestSegmentedReportAssembledBeforeDelivery(t *testing.T) {
	x := strings.Replace(fixture(t), `reasonCode="true" dataSet="true"`, `reasonCode="true" dataRef="true" dataSet="true"`, 1)
	d := device(t, x)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := simulator.Start(ctx, d, simulator.Options{Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEOff, MMS: mms.Config{MaxPDUSize: 192}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c, err := ied.Dial(ctx, r.Address(), ied.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.SubscribeURCB(ctx, mms.DomainName("BayIEDCTRL", "LLN0$RP$events01"), ied.ReportOptions{GI: true})
	if err != nil {
		t.Fatal(err)
	}
	report := nextReport(t, ctx, s)
	if len(report.Entries) != 3 || report.Segments < 2 {
		t.Fatal("partial report exposed", report)
	}
}
func TestBRCBOverflowAndUnknownCursor(t *testing.T) {
	d := device(t, fixture(t))
	if err := d.SetReportBufferCapacity(2); err != nil {
		t.Fatal(err)
	}
	ctx, _, c := serviceRuntime(t, d)
	block := mms.DomainName("BayIEDCTRL", "LLN0$BR$history01")
	s, err := c.SubscribeBRCB(ctx, block, ied.ReportOptions{PurgeBuffer: true, GI: true})
	if err != nil {
		t.Fatal(err)
	}
	baseline := nextReport(t, ctx, s)
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	for _, state := range []scl.Position{scl.Closed, scl.Open, scl.Closed} {
		v, _ := state.MMSValue()
		if err := d.Update(ctx, mms.Assignment{Name: pos, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.SubscribeBRCB(ctx, block, ied.ReportOptions{ResumeEntryID: baseline.EntryID}); !errors.Is(err, mms.ObjectValueInvalid) {
		t.Fatal("expired history cursor", err)
	}
	s, err = c.SubscribeBRCB(ctx, block, ied.ReportOptions{ResumeEntryID: make([]byte, 8)})
	if err != nil {
		t.Fatal(err)
	}
	if report := nextReport(t, ctx, s); !report.BufferOverflow || !report.Reset {
		t.Fatal("overflow history gap hidden", report)
	}
}
func TestExplicitSelectionCancelAndExpiry(t *testing.T) {
	x := strings.Replace(fixture(t), "<Val>direct-with-normal-security</Val>", "<Val>sbo-with-normal-security</Val>", 1)
	x = strings.Replace(x, `<DA name="Oper"`, `<DA name="SBO" bType="VisString129" fc="CO"/><DA name="Cancel" bType="Struct" type="Operate" fc="CO"/><DA name="sboTimeout" bType="INT32U" fc="CF"><Val>40</Val></DA><DA name="Oper"`, 1)
	d := device(t, x)
	if err := d.OnControl(lookup(t, d, "XCBR1", "CO", "Pos.Oper"), func(context.Context, scl.Command) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, _, c := serviceRuntime(t, d)
	control, err := c.Control(ctx, "BayIEDCTRL/XCBR1.Pos")
	if err != nil {
		t.Fatal(err)
	}
	selection, err := control.Select(ctx, mms.Boolean(true), ied.ControlOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = selection.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = selection.Operate(ctx); !errors.Is(err, ied.ErrRejected) {
		t.Fatal(err)
	}
	selection, err = control.Select(ctx, mms.Boolean(true), ied.ControlOptions{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err = selection.Operate(ctx); !errors.Is(err, ied.ErrRejected) {
		t.Fatal("expired selection executed", err)
	}
}
