// SPDX-License-Identifier: GPL-3.0-or-later
package simulator_test

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/internal/sample"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

func TestPublicRuntimeOwnsTransportsAndKeepsModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	system, err := scl.Parse(bytes.NewReader(sample.SCL()))
	if err != nil {
		t.Fatal(err)
	}
	model, err := system.Device("BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	goose := make(chan struct{}, 4)
	runtime, err := simulator.Start(ctx, model, simulator.Options{Listen: "127.0.0.1:0", Observe: func(event simulator.Event) {
		if event.GOOSE != nil {
			select {
			case goose <- struct{}{}:
			default:
			}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	client, err := ied.Dial(ctx, runtime.Address(), ied.Config{MMS: model.ClientConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	name, _ := model.Resolve("BayIEDCTRL/MMXU1.TotW.mag.f", "MX")
	if err := model.Update(ctx, mms.Assignment{Name: name, Value: mms.Float32(1250)}); err != nil {
		t.Fatal(err)
	}
	value, err := client.Read(ctx, name)
	n, _ := value.Float()
	if err != nil || n != 1250 {
		t.Fatal(value, err)
	}
	select {
	case <-goose:
	case <-ctx.Done():
		t.Fatal("memory GOOSE did not pass the subscriber")
	}
	address := runtime.Address()
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("listener leaked", err)
	}
	listener.Close()
	value, err = model.Read(ctx, mms.Access(name))
	n, _ = value.Float()
	if err != nil || n != 1250 {
		t.Fatal("stopping transport discarded the model", value, err)
	}
	// Parent cancellation also closes a freshly started transport.
	parent, stop := context.WithCancel(ctx)
	second, err := simulator.Start(parent, model, simulator.Options{Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEOff})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if err := second.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestSetupFailureReleasesListener(t *testing.T) {
	system, err := scl.Parse(bytes.NewReader(sample.SCL()))
	if err != nil {
		t.Fatal(err)
	}
	model, err := system.Device("BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if _, err := simulator.Start(context.Background(), model, simulator.Options{Listen: address, GOOSE: simulator.GOOSEEthernet}); err == nil {
		t.Fatal("missing NIC accepted")
	}
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal("setup failure leaked listener", err)
	}
	listener.Close()
}
