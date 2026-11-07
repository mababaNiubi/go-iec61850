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

func TestSetpointAndSettingActivationProduceChangeReports(t *testing.T) {
	raw, err := os.ReadFile("../examples/ied/settings/device.scd")
	if err != nil {
		t.Fatal(err)
	}
	x := string(raw)
	for _, change := range [][2]string{
		{`<DataSet name="dsEvents">`, `<DataSet name="dsEvents"><FCDA ldInst="CTRL" lnClass="PTOC" lnInst="1" doName="OpDlTmms" daName="setVal" fc="SP"/><FCDA ldInst="CTRL" lnClass="PTOC" lnInst="1" doName="StrVal" daName="setMag.f" fc="SG"/>`},
		{`name="events" datSet="dsPos"`, `name="events" datSet="dsEvents"`},
		{`name="setVal" bType="INT32" fc="SP"`, `name="setVal" bType="INT32" fc="SP" dchg="true"`},
		{`name="setMag" bType="Struct" type="SettingAnalogue" fc="SG"`, `name="setMag" bType="Struct" type="SettingAnalogue" fc="SG" dchg="true"`},
	} {
		if strings.Count(x, change[0]) != 1 {
			t.Fatalf("fixture does not match %s", change[0])
		}
		x = strings.Replace(x, change[0], change[1], 1)
	}
	d := device(t, x)
	delay := lookup(t, d, "PTOC1", "SP", "OpDlTmms.setVal")
	active := lookup(t, d, "PTOC1", "SG", "StrVal.setMag.f")
	edit := lookup(t, d, "PTOC1", "SE", "StrVal.setMag.f")
	if err := d.OnSetpoint(delay, func(ctx context.Context, v mms.Value) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	ctx, _, c := serviceRuntime(t, d)
	s, err := c.SubscribeURCB(ctx, mms.DomainName(delay.Domain, "LLN0$RP$events01"), ied.ReportOptions{GI: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nextReport(t, ctx, s)
	check := func(n mms.ObjectName, want mms.Value) {
		t.Helper()
		r := nextReport(t, ctx, s)
		if len(r.Entries) != 1 || r.Entries[0].Variable.Name != n || !r.Entries[0].Value.Equal(want) {
			t.Fatalf("change report: %+v", r)
		}
		raw, _ := r.Entries[0].Reason.Bytes()
		if len(raw) != 1 || raw[0] != 0x40 {
			t.Fatalf("not data-change: %x", raw)
		}
	}
	if err := c.Write(ctx, delay, mms.Integer(250)); err != nil {
		t.Fatal(err)
	}
	check(delay, mms.Integer(250))
	// An inactive edit must not change or publish the active SG view.
	if _, err := c.EditSettings(ctx, active.Domain, 2, map[mms.ObjectName]mms.Value{edit: mms.Float32(25)}, false); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-s.Events():
		t.Fatalf("inactive group published: %+v", e)
	case <-time.After(30 * time.Millisecond):
	}
	if err := c.Write(ctx, mms.DomainName(active.Domain, "LLN0$SP$SGCB$ActSG"), mms.Unsigned(2)); err != nil {
		t.Fatal(err)
	}
	check(active, mms.Float32(25))
}

func TestEnhancedFailureRetainsAcceptedEvidence(t *testing.T) {
	d := device(t, strings.Replace(fixture(t), "<Val>direct-with-normal-security</Val>", "<Val>direct-with-enhanced-security</Val>", 1))
	oper := lookup(t, d, "XCBR1", "CO", "Pos.Oper")
	if err := d.OnControl(oper, func(context.Context, scl.Command) error { return mms.HardwareFault }); err != nil {
		t.Fatal(err)
	}
	ctx, _, c := serviceRuntime(t, d)
	result, err := c.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", mms.Boolean(true), ied.ControlOptions{Test: true})
	if !result.Acknowledged || result.Terminated || !errors.Is(err, ied.ErrRejected) {
		t.Fatal(result, err)
	}
}

func TestUnboundReportCannotEnableUntilStaticDatasetSelected(t *testing.T) {
	d := device(t, strings.Replace(fixture(t), `name="events" datSet="dsPos"`, `name="events" datSet=""`, 1))
	ctx, _, c := serviceRuntime(t, d)
	block := mms.DomainName("BayIEDCTRL", "LLN0$RP$events01")
	if err := c.Write(ctx, mms.DomainName(block.Domain, block.Item+"$RptEna"), mms.Boolean(true)); !errors.Is(err, mms.ObjectValueInvalid) {
		t.Fatal(err)
	}
	if err := c.Write(ctx, mms.DomainName(block.Domain, block.Item+"$DatSet"), mms.VisibleString("BayIEDCTRL/LLN0$dsPos")); err != nil {
		t.Fatal(err)
	}
	s, err := c.SubscribeURCB(ctx, block, ied.ReportOptions{GI: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := nextReport(t, ctx, s)
	if len(r.Entries) != 3 || r.ConfRev != 2 {
		t.Fatal(r)
	}
}

func TestEnhancedControlAcknowledgesBeforeExecutionAndTermination(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "execution-failure"}[fails], func(t *testing.T) {
			d := device(t, strings.Replace(fixture(t), "<Val>direct-with-normal-security</Val>", "<Val>direct-with-enhanced-security</Val>", 1))
			oper := lookup(t, d, "XCBR1", "CO", "Pos.Oper")
			entered, release := make(chan struct{}), make(chan struct{})
			if err := d.OnControl(oper, func(ctx context.Context, c scl.Command) error {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if fails {
					return mms.HardwareFault
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, err := simulator.Start(ctx, d, simulator.Options{Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEOff})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			c, err := mms.Dial(ctx, r.Address(), mms.Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			spec, err := c.GetVariableAccessAttributes(ctx, oper)
			if err != nil {
				t.Fatal(err)
			}
			command, err := ied.BuildCommand(spec, mms.Boolean(true), ied.Origin{Category: 2}, 7, ied.ControlOptions{Test: true, Time: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			// This must complete while the actuator is still deliberately blocked.
			if err := c.Write(ctx, oper, command); err != nil {
				t.Fatal("acceptance waited for execution:", err)
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case report := <-c.Reports():
				t.Fatalf("premature termination: %+v", report)
			default:
			}
			close(release)
			select {
			case report := <-c.Reports():
				if len(report.Variables) == 0 || report.Variables[0].Name != oper || !report.Results[0].Value.Equal(command) {
					t.Fatalf("uncorrelated termination: %+v", report)
				}
				if (len(report.Variables) == 2) != fails {
					t.Fatalf("failure evidence: %+v", report)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestUTF8BOMAndServiceTrackingFC(t *testing.T) {
	x := fixture(t)
	system, err := scl.Parse(strings.NewReader("\ufeff" + x))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := system.Device("BayIED", "S1"); err != nil {
		t.Fatal(err)
	}
	if _, err := scl.Parse(strings.NewReader("x\ufeff" + x)); err == nil {
		t.Fatal("non-XML prefix accepted")
	}
	x = strings.Replace(x, `<DA name="stVal" bType="Dbpos" fc="ST"`, `<DA name="audit" bType="INT32" fc="SR"/><DA name="stVal" bType="Dbpos" fc="ST"`, 1)
	d := device(t, x)
	if _, err := d.Lookup("CTRL", "XCBR1", "SR", "Pos.audit"); err != nil {
		t.Fatal(err)
	}
}
