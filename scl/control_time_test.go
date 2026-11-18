// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

// timeActivatedFixture declares operTm between ctlNum and T, which is the
// seven-field Oper layout used by time-activated control.
func timeActivatedFixture(t *testing.T) string {
	t.Helper()
	xml := fixture(t)
	old := `<BDA name="ctlNum" bType="INT8U"/>`
	if !strings.Contains(xml, old) {
		t.Fatal("fixture changed: ctlNum declaration missing")
	}
	xml = strings.Replace(xml, old, old+`<BDA name="operTm" bType="Timestamp"/>`, 1)
	// Time activation requires enhanced security (ctlModel 3).
	return strings.Replace(xml, `<DOI name="Pos"><DAI name="ctlModel"><Val>direct-with-normal-security</Val></DAI></DOI>`,
		`<DOI name="Pos"><DAI name="ctlModel"><Val>direct-with-enhanced-security</Val></DAI></DOI>`, 1)
}

// T-CTL-01 (time activation): acceptance is immediate, execution happens at
// operTm and the enhanced-security termination follows it.
func TestTimeActivatedControl(t *testing.T) {
	d := device(t, timeActivatedFixture(t))
	op := lookup(t, d, "XCBR1", "CO", "Pos.Oper")
	pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	var mu sync.Mutex
	var executedAt time.Time
	if err := d.OnControl(op, func(ctx context.Context, c scl.Command) error {
		if c.OperTime.IsZero() {
			return mms.ObjectValueInvalid
		}
		mu.Lock()
		executedAt = time.Now()
		mu.Unlock()
		state, err := scl.Closed.MMSValue()
		if err != nil {
			return err
		}
		return d.Update(ctx, mms.Assignment{Name: pos, Value: state})
	}); err != nil {
		t.Fatal(err)
	}
	server, err := d.NewServer(mms.Config{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		<-finished
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := ied.Dial(ctx, listener.Addr().String(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("timed")}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(600 * time.Millisecond).Truncate(time.Second).Add(time.Second)
	result, err := client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", mms.Boolean(true), ied.ControlOptions{OperTime: deadline})
	if err != nil {
		t.Fatal(err)
	}
	// Operate waits for the enhanced-security termination, which follows
	// execution at operTm: neither may happen before the requested time.
	if !result.Acknowledged || !result.Terminated {
		t.Fatalf("time-activated operate did not complete: %+v", result)
	}
	mu.Lock()
	at := executedAt
	mu.Unlock()
	if at.Before(deadline.Add(-100 * time.Millisecond)) {
		t.Fatalf("actuator ran %v before operTm", deadline.Sub(at))
	}
}

// Immediate control still works on the same seven-field layout; a zero operTm
// means execute now.
func TestImmediateControlWithOperTmDeclared(t *testing.T) {
	d := device(t, timeActivatedFixture(t))
	op := lookup(t, d, "XCBR1", "CO", "Pos.Oper")
	pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	var seen time.Time
	if err := d.OnControl(op, func(ctx context.Context, c scl.Command) error {
		seen = c.OperTime
		state, err := scl.Closed.MMSValue()
		if err != nil {
			return err
		}
		return d.Update(ctx, mms.Assignment{Name: pos, Value: state})
	}); err != nil {
		t.Fatal(err)
	}
	server, err := d.NewServer(mms.Config{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		<-finished
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := ied.Dial(ctx, listener.Addr().String(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("now")}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", mms.Boolean(true), ied.ControlOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Acknowledged || !result.Terminated || !seen.IsZero() {
		t.Fatalf("immediate control on a time-activated layout: result=%+v operTm=%v", result, seen)
	}
}
