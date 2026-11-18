// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func stationDevice(t *testing.T, iedName, ap string) *scl.IED {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "internal", "sample", "station.scd"))
	if err != nil {
		t.Fatal(err)
	}
	system, err := scl.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	d, err := system.Device(iedName, ap)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// T-SVCB: the modeled SVCB echoes the engineered stream and SvEna gates
// publication; Resv is association-scoped.
func TestSVCBOnlineManagement(t *testing.T) {
	d := stationDevice(t, "MUA", "AP1")
	block := mms.DomainName("MUALD0", "LLN0$MS$MSVCB01")
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
	client, err := ied.Dial(ctx, listener.Addr().String(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("svcb")}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	svcb, err := client.ReadSVCB(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	if !svcb.SvEna || svcb.MsvID != "MUA_Samples" || svcb.ConfRev != 1 || svcb.SmpRate != 80 || svcb.NoASDU != 2 {
		t.Fatalf("unexpected SVCB: %+v", svcb)
	}
	// sampleSynchronized (bit 1) and sampleRate (bit 2) are declared.
	if len(svcb.OptFlds) == 0 || svcb.OptFlds[0]&0x60 != 0x60 {
		t.Fatalf("OptFlds does not declare the engineered options: %x", svcb.OptFlds)
	}
	if !d.SVEnabled("MUA/AP1/LD0/MSVCB01") {
		t.Fatal("SV publication must start enabled")
	}
	if err = client.SetSVCBEnabled(ctx, block, false); err != nil {
		t.Fatal(err)
	}
	if d.SVEnabled("MUA/AP1/LD0/MSVCB01") {
		t.Fatal("SvEna write did not gate publication")
	}
	if again, err := client.ReadSVCB(ctx, block); err != nil || again.SvEna {
		t.Fatalf("SvEna read-back failed: %+v err=%v", again, err)
	}
	if err = client.SetSVCBEnabled(ctx, block, true); err != nil {
		t.Fatal(err)
	}
	if !d.SVEnabled("MUA/AP1/LD0/MSVCB01") {
		t.Fatal("re-enable did not restore publication")
	}
	// Resv is association-scoped: this client can take and release it.
	if err = client.Write(ctx, mms.DomainName(block.Domain, block.Item+"$Resv"), mms.Boolean(true)); err != nil {
		t.Fatal(err)
	}
	if r, err := client.ReadSVCB(ctx, block); err != nil || !r.Resv {
		t.Fatalf("Resv did not stick: %+v err=%v", r, err)
	}
	// Changing the engineered dataset is refused.
	if err = client.Write(ctx, mms.DomainName(block.Domain, block.Item+"$DatSet"), mms.VisibleString("other")); err == nil {
		t.Fatal("an SVCB DatSet write must be refused")
	}
}
