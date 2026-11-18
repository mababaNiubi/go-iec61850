// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// T-RCB-01 (reservation): ResvTms and Owner follow the reference semantics,
// and a whole-block read observes runtime state rather than creation values.
func TestBRCBReservationTimeoutAndOwner(t *testing.T) {
	d := device(t, fixture(t))
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
	client, err := ied.Dial(ctx, listener.Addr().String(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("resv")}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	block := mms.DomainName("BayIEDCTRL", "LLN0$BR$history01")
	field := func(name string) mms.ObjectName {
		return mms.DomainName(block.Domain, block.Item+"$"+name)
	}
	// A whole-block read: names come from the declared type, values from the
	// model. The structure must reflect runtime state, not creation values.
	read := func() map[string]mms.Value {
		spec, err := client.GetVariableAccessAttributes(ctx, block)
		if err != nil {
			t.Fatal(err)
		}
		value, err := client.Read(ctx, block)
		if err != nil {
			t.Fatal(err)
		}
		parts, err := value.Elements()
		if err != nil || len(parts) != len(spec.Components) {
			t.Fatalf("structure mismatch: %d values, %d components", len(parts), len(spec.Components))
		}
		out := make(map[string]mms.Value, len(parts))
		for i, component := range spec.Components {
			out[component.Name] = parts[i]
		}
		return out
	}
	initial := read()
	if n, _ := initial["ResvTms"].Int(); n != 0 {
		t.Fatalf("a fresh BRCB must report ResvTms 0, got %v", n)
	}
	if owner, _ := initial["Owner"].Bytes(); len(owner) != 0 {
		t.Fatalf("a fresh BRCB must report an empty Owner, got %x", owner)
	}
	// Reserve indefinitely, as the reference implementation does for -1.
	if err := client.Write(ctx, field("ResvTms"), mms.Integer(-1)); err != nil {
		t.Fatal(err)
	}
	reserved := read()
	if n, _ := reserved["ResvTms"].Int(); n != -1 {
		t.Fatalf("ResvTms must report -1 while reserved, got %v", n)
	}
	owner, _ := reserved["Owner"].Bytes()
	if !net.IP(owner).Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("Owner must carry the reserving client's address, got %x", owner)
	}
	if on, _ := reserved["Resv"].Bool(); on {
		t.Fatal("BRCB has no Resv attribute")
	}
	// Release.
	if err := client.Write(ctx, field("ResvTms"), mms.Integer(0)); err != nil {
		t.Fatal(err)
	}
	released := read()
	if n, _ := released["ResvTms"].Int(); n != 0 {
		t.Fatalf("release must report 0, got %v", n)
	}
	if owner, _ := released["Owner"].Bytes(); len(owner) != 0 {
		t.Fatalf("release must clear Owner, got %x", owner)
	}
	// A timed reservation expires on its own.
	if err := client.Write(ctx, field("ResvTms"), mms.Integer(1)); err != nil {
		t.Fatal(err)
	}
	if n, _ := read()["ResvTms"].Int(); n != 1 {
		t.Fatalf("a timed reservation must be visible, got %v", n)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if n, _ := read()["ResvTms"].Int(); n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the timed reservation did not expire")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if owner, _ := read()["Owner"].Bytes(); len(owner) != 0 {
		t.Fatalf("expiry must clear Owner, got %x", owner)
	}
}
