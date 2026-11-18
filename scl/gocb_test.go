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

// T-GOCB: the modeled GoCB exposes the engineered stream and GoEna gates
// publication; reading it back reflects the write.
func TestGoCBOnlineManagement(t *testing.T) {
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
	client, err := ied.Dial(ctx, listener.Addr().String(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("gocb")}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	block := mms.DomainName("BayIEDCTRL", "LLN0$GO$gcbPos")
	gcb, err := client.ReadGoCB(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	if !gcb.GoEna {
		t.Fatalf("engineered GoCB must start enabled: %+v", gcb)
	}
	if gcb.DataSet != "BayIEDCTRL/LLN0$dsPos" || gcb.ConfRev != 1 {
		t.Fatalf("unexpected GoCB declaration: %+v", gcb)
	}
	if gcb.DstAddress.APPID == 0 || gcb.DstAddress.DestinationMAC == "" {
		t.Fatalf("destination address missing: %+v", gcb.DstAddress)
	}
	if err = client.SetGoCBEnabled(ctx, block, false); err != nil {
		t.Fatal(err)
	}
	after, err := client.ReadGoCB(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoEna {
		t.Fatalf("GoEna write did not take effect: %+v", after)
	}
	// Re-enabling restores publication.
	if err = client.SetGoCBEnabled(ctx, block, true); err != nil {
		t.Fatal(err)
	}
	if again, err := client.ReadGoCB(ctx, block); err != nil || !again.GoEna {
		t.Fatalf("re-enable failed: %+v err=%v", again, err)
	}
	// Fields that would change the engineered stream layout are refused
	// explicitly instead of silently ignored.
	if err = client.Write(ctx, mms.DomainName("BayIEDCTRL", "LLN0$GO$gcbPos$DatSet"), mms.VisibleString("BayIEDCTRL/LLN0$other")); err == nil {
		t.Fatal("a GoCB DatSet write must be refused")
	}
}
