// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

func concludePeer(t *testing.T, serve func(*iso.Conn) error) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer raw.Close()
		cfg, _ := (Config{}).normalized()
		peer, err := iso.Accept(context.Background(), raw, cfg.ISO, func([]byte) ([]byte, error) {
			return encodeInitiate(0xa9, localParameters(cfg)), nil
		})
		if err != nil {
			finished <- err
			return
		}
		defer peer.Close()
		finished <- serve(peer)
	}()
	t.Cleanup(func() { listener.Close() })
	return listener.Addr().String(), finished
}

func TestClientRejectsUnsolicitedConcludeResponse(t *testing.T) {
	address, finished := concludePeer(t, func(peer *iso.Conn) error {
		if err := peer.WritePDU([]byte{0x8c, 0}); err != nil {
			return err
		}
		// Keep the peer open until the client rejects the response, so EOF
		// cannot mask acceptance of a stale response by the old implementation.
		_, _ = peer.ReadPDU()
		return nil
	})
	client := dialTest(t, address, Config{})
	select {
	case <-client.Done():
		if !errors.Is(client.Err(), ErrProtocol) {
			t.Fatalf("unsolicited Conclude response: %v", client.Err())
		}
	case <-time.After(2 * time.Second):
		client.Close()
		t.Fatal("unsolicited Conclude response was cached instead of rejected")
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsDuplicateConcludeResponse(t *testing.T) {
	address, finished := concludePeer(t, func(peer *iso.Conn) error {
		data, err := peer.ReadPDU()
		if err != nil {
			return err
		}
		request, err := decodeEnvelope(data)
		if err != nil || request.tag != 0x8b {
			return fmt.Errorf("expected Conclude request: %x (%v)", data, err)
		}
		for i := 0; i < 2; i++ {
			if err := peer.WritePDU([]byte{0x8c, 0}); err != nil {
				return err
			}
		}
		_, _ = peer.ReadPDU()
		return nil
	})
	client := dialTest(t, address, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Conclude(ctx); err == nil {
		t.Fatal("duplicate Conclude response completed a normal association release")
	}
	if !errors.Is(client.Err(), ErrProtocol) {
		t.Fatalf("duplicate Conclude response did not reject the peer: %v", client.Err())
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
