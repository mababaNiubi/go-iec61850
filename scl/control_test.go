// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func TestDirectControlOverMMS(t *testing.T) {
	d := device(t, fixture(t))
	op := lookup(t, d, "XCBR1", "CO", "Pos.Oper")
	pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	var calls atomic.Int32
	err := d.OnOperate(op, func(ctx context.Context, c scl.Command) error {
		if c.Test {
			return mms.ObjectAccessDenied
		}
		if !c.Value || !c.InterlockCheck || c.SynchroCheck || c.OriginCategory != 2 || string(c.OriginID) != "HMI" || c.Number != 37 {
			return mms.ObjectValueInvalid
		}
		state, err := scl.Closed.MMSValue()
		if err != nil {
			return err
		}
		calls.Add(1)
		return d.Update(ctx, mms.Assignment{Name: pos, Value: state})
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := d.NewServer(mms.Config{})
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() {
		finish, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if err := srv.Shutdown(finish); err != nil {
			t.Error(err)
		}
		l.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	client, err := mms.Dial(ctx, l.Addr().String(), d.ClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cmd := scl.Command{Value: true, OriginCategory: 2, OriginID: []byte("HMI"), Number: 37, Time: time.Now(), InterlockCheck: true}
	v, err := cmd.MMSValue()
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Write(ctx, op, mms.Boolean(true)); !errors.Is(err, mms.TypeInconsistent) {
		t.Fatal(err)
	}
	if err = client.Write(ctx, lookup(t, d, "XCBR1", "CO", "Pos.Oper.ctlVal"), mms.Boolean(true)); !errors.Is(err, mms.ObjectAccessDenied) {
		t.Fatal(err)
	}
	if err = client.Write(ctx, op, v); err != nil {
		t.Fatal(err)
	}
	set, err := d.DataSet("CTRL", "LLN0", "dsPos")
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.ReadNamedList(ctx, set.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Error != nil {
		t.Fatal(results)
	}
	got, err := scl.ReadPosition(results[0].Value)
	if err != nil || got != scl.Closed {
		t.Fatal(got, err)
	}
	cmd.Test = true
	v, err = cmd.MMSValue()
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Write(ctx, op, v); !errors.Is(err, mms.ObjectAccessDenied) {
		t.Fatal(err)
	}
	if err = client.Conclude(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("actuator called %d times", calls.Load())
	}
}

func TestStatusOnlyAndSBOCannotBindDirectHandler(t *testing.T) {
	for _, mode := range []string{"status-only", "sbo-with-normal-security", "direct-with-enhanced-security"} {
		x := strings.Replace(fixture(t), "<Val>direct-with-normal-security</Val>", "<Val>"+mode+"</Val>", 1)
		d := device(t, x)
		err := d.OnOperate(lookup(t, d, "XCBR1", "CO", "Pos.Oper"), func(context.Context, scl.Command) error { return nil })
		if err == nil {
			t.Fatalf("direct handler accepted for %s", mode)
		}
	}
}

func TestUninitializedControlModelDoesNotEnableOperation(t *testing.T) {
	x := strings.Replace(fixture(t), `<DOI name="Pos"><DAI name="ctlModel"><Val>direct-with-normal-security</Val></DAI></DOI>`, "", 1)
	x = strings.Replace(x, `<DA name="ctlModel" bType="Enum" type="ControlModel" fc="CF"/>`, `<DA name="ctlModel" bType="Enum" type="PartialCtl" fc="CF"/>`, 1)
	x = strings.Replace(x, "</DataTypeTemplates>", `<EnumType id="PartialCtl"><EnumVal ord="1">direct-with-normal-security</EnumVal></EnumType></DataTypeTemplates>`, 1)
	d := device(t, x)
	err := d.OnOperate(lookup(t, d, "XCBR1", "CO", "Pos.Oper"), func(context.Context, scl.Command) error { return nil })
	if err == nil {
		t.Fatal("a missing ctlModel initializer enabled a command")
	}
}

type stalledLink struct{ started chan struct{} }

func (s stalledLink) WriteFrame(ctx context.Context, _ []byte) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}
func (s stalledLink) ReadFrame(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s stalledLink) Close() error { return nil }

func TestStalledPublicationReportsOverflowWithoutBlockingAcquisition(t *testing.T) {
	d := device(t, fixture(t))
	g, err := d.Stream("CTRL", "gcbPos")
	if err != nil {
		t.Fatal(err)
	}
	pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	link := stalledLink{make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx, link, [6]byte{2, 0, 0, 0, 0, 1}) }()
	select {
	case <-link.started:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 70; i++ {
		p := scl.Open
		if i%2 == 1 {
			p = scl.Closed
		}
		v, _ := p.MMSValue()
		if err := d.Update(ctx, mms.Assignment{Name: pos, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, scl.ErrPublicationOverflow) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stalled publisher did not exit")
	}
	v, err := d.Read(ctx, mms.Access(pos))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := scl.ReadPosition(v)
	if got != scl.Closed {
		t.Fatal("acquisition was lost")
	}
}
