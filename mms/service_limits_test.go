// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

func TestNegotiatedNestingLimits(t *testing.T) {
	model := testModel(t)
	_, address := startServer(t, model, Config{}, nil)
	client := dialTest(t, address, Config{NestingLevel: 1})
	ctx := context.Background()
	if _, err := client.Read(ctx, DomainName("sample", "array")); !errors.Is(err, TypeUnsupported) {
		t.Fatalf("nested read: %v", err)
	}
	if _, err := client.GetVariableAccessAttributes(ctx, DomainName("sample", "array")); err == nil {
		t.Fatal("type exceeding nesting accepted")
	}
	if err := client.Write(ctx, DomainName("sample", "array"), Array(Integer(4), Integer(5), Integer(6))); err == nil {
		t.Fatal("nested outbound write accepted")
	}
	v, err := client.Read(ctx, DomainName("sample", "int"))
	if err != nil {
		t.Fatal(err)
	}
	requireInteger(t, v, 42)
}

func TestClientRejectsPeerNestingViolation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer raw.Close()
		cfg, _ := (Config{NestingLevel: 1}).normalized()
		conn, err := iso.Accept(context.Background(), raw, cfg.ISO, func([]byte) ([]byte, error) { return encodeInitiate(0xa9, localParameters(cfg)), nil })
		if err != nil {
			finished <- err
			return
		}
		defer conn.Close()
		data, err := conn.ReadPDU()
		if err != nil {
			finished <- err
			return
		}
		request, err := decodeEnvelope(data)
		if err != nil {
			finished <- err
			return
		}
		value, err := EncodeValue(Array(Integer(1)))
		if err != nil {
			finished <- err
			return
		}
		finished <- conn.WritePDU(confirmed(0xa1, request.id, ber.Encode(0xa4, ber.Encode(0xa1, value))))
	}()
	client := dialTest(t, listener.Addr().String(), Config{NestingLevel: 1})
	_, err = client.Read(context.Background(), VMDName("test"))
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("peer nesting violation: %v", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestWaitingCallCancellationPreservesAssociation(t *testing.T) {
	store := &delayedStore{Store: testModel(t), entered: make(chan struct{}), release: make(chan struct{})}
	_, address := startServer(t, store, Config{}, nil)
	client := dialTest(t, address, Config{MaxOutstandingCalling: 1})
	first := make(chan error, 1)
	go func() { _, err := client.Read(context.Background(), DomainName("sample", "item000")); first <- err }()
	<-store.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Read(ctx, DomainName("sample", "int")); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting canceled call: %v", err)
	}
	if client.Err() != nil {
		t.Fatalf("unsent call closed association: %v", client.Err())
	}
	close(store.release)
	select {
	case err := <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first call did not finish")
	}
	if _, err := client.Read(context.Background(), DomainName("sample", "int")); err != nil {
		t.Fatal(err)
	}
}
