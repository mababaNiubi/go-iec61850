// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

// A completed response frees the negotiated window even when the peer sends
// the next request, or concludes, before the server worker returns.
func TestWindowOneConsecutiveCallsAndConclude(t *testing.T) {
	cfg := Config{MaxOutstandingCalling: 1, MaxOutstandingCalled: 1}
	_, address := startServer(t, testModel(t), cfg, nil)
	for association := 0; association < 4; association++ {
		c := dialTest(t, address, cfg)
		if p := c.Parameters(); p.MaxOutstandingCalling != 1 || p.MaxOutstandingCalled != 1 {
			t.Fatalf("expected one outstanding call in both directions: %+v", p)
		}
		for i := 0; i < 64; i++ {
			value, err := c.Read(context.Background(), DomainName("sample", "int"))
			if err != nil {
				t.Fatalf("association %d call %d: %v", association, i, err)
			}
			requireInteger(t, value, 42)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := c.Conclude(ctx)
		cancel()
		if err != nil {
			t.Fatalf("association %d immediate conclude: %v", association, err)
		}
		select {
		case <-c.Done():
		default:
			t.Fatal("successful Conclude did not close the association")
		}
	}
}

// Without negotiated MMS Cancel support an abandoned confirmed request must
// terminate the association; reusing its window can exceed the peer's limit.
func TestAbandonedResponseClosesAssociation(t *testing.T) {
	for _, timedOut := range []bool{false, true} {
		name := "canceled"
		if timedOut {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			store := &delayedStore{Store: testModel(t), entered: make(chan struct{}), release: make(chan struct{})}
			_, address := startServer(t, store, Config{RequestTimeout: 3 * time.Second, MaxOutstandingCalling: 1, MaxOutstandingCalled: 1}, nil)
			c := dialTest(t, address, Config{MaxOutstandingCalling: 1, MaxOutstandingCalled: 1})
			ctx, cancel := context.WithCancel(context.Background())
			if timedOut {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := c.Read(ctx, DomainName("sample", "item000"))
				result <- err
			}()
			select {
			case <-store.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("request never reached the peer's blocked handler")
			}
			if !timedOut {
				cancel()
			}
			want := context.Canceled
			if timedOut {
				want = context.DeadlineExceeded
			}
			select {
			case err := <-result:
				if !errors.Is(err, want) {
					t.Fatalf("abandoned read returned %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("abandoned read did not return")
			}
			select {
			case <-c.Done():
			case <-time.After(time.Second):
				t.Fatal("abandoned response left the association open")
			}
			if _, err := c.Read(context.Background(), DomainName("sample", "int")); err == nil {
				t.Fatal("a new request reused the abandoned request's association")
			}
			close(store.release)
		})
	}
}

func TestConfirmedWriteAfterMMSConcludeIsRejected(t *testing.T) {
	model := testModel(t)
	_, address := startServer(t, model, Config{}, nil)
	cfg, err := (Config{}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	peer, response, err := iso.Dial(ctx, address, cfg.ISO, encodeInitiate(0xa8, localParameters(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err = decodeInitiate(response, 0xa9); err != nil {
		t.Fatal(err)
	}
	if err = peer.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = peer.WritePDU([]byte{0x8b, 0}); err != nil {
		t.Fatal(err)
	}
	data, err := peer.ReadPDU()
	if err != nil {
		t.Fatal(err)
	}
	conclude, err := decodeEnvelope(data)
	if err != nil || conclude.tag != 0x8c {
		t.Fatalf("expected successful conclude response, got %x (%v)", data, err)
	}
	spec, err := encodeAccesses([]VariableAccess{Access(DomainName("sample", "int"))})
	if err != nil {
		t.Fatal(err)
	}
	encodedValue, err := EncodeValue(Integer(1001))
	if err != nil {
		t.Fatal(err)
	}
	request := confirmed(0xa0, 23, ber.Encode(0xa5, spec, ber.Encode(0xa0, encodedValue)))
	if err = peer.WritePDU(request); err == nil {
		data, err = peer.ReadPDU()
		if err == nil {
			m, decodeErr := decodeEnvelope(data)
			if decodeErr != nil || m.id != 23 || (m.tag != 0xa4 && m.tag != 0xa2) {
				t.Fatalf("confirmed write executed after conclude: %x (%v)", data, decodeErr)
			}
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatalf("server neither rejected nor closed after post-conclude service: %v", err)
		}
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("post-conclude write stalled: %v", err)
	}
	value, err := model.Read(context.Background(), Access(DomainName("sample", "int")))
	if err != nil {
		t.Fatal(err)
	}
	requireInteger(t, value, 42)
}

// Sending Abort after receiving FINISH makes the failure occur after Release
// has returned, rather than racing the earlier MMS Conclude response.
func TestConcludePropagatesAbortDuringISORelease(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerResult := make(chan error, 1)
	go func() {
		peerResult <- func() error {
			raw, err := listener.Accept()
			if err != nil {
				return err
			}
			defer raw.Close()
			cfg, err := (Config{}).normalized()
			if err != nil {
				return err
			}
			peer, err := iso.Accept(context.Background(), raw, cfg.ISO, func(request []byte) ([]byte, error) {
				p, err := decodeInitiate(request, 0xa8)
				if err != nil {
					return nil, err
				}
				return encodeInitiate(0xa9, negotiate(localParameters(cfg), p)), nil
			})
			if err != nil {
				return err
			}
			defer peer.Close()
			if err = peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				return err
			}
			request, err := peer.ReadPDU()
			if err != nil {
				return err
			}
			m, err := decodeEnvelope(request)
			if err != nil || m.tag != 0x8b {
				return fmt.Errorf("expected MMS conclude request: %x (%v)", request, err)
			}
			if err = peer.WritePDU([]byte{0x8c, 0}); err != nil {
				return err
			}
			// Read the small, single-TPDU FINISH without ReadPDU automatically
			// replying with DISCONNECT. This peer deliberately aborts instead.
			var header [4]byte
			if _, err = io.ReadFull(raw, header[:]); err != nil {
				return err
			}
			size := int(binary.BigEndian.Uint16(header[2:]))
			if header[0] != 3 || header[1] != 0 || size < 8 || size > 256 {
				return fmt.Errorf("invalid release TPKT: %x", header)
			}
			finish := make([]byte, size-4)
			if _, err = io.ReadFull(raw, finish); err != nil {
				return err
			}
			if finish[0] != 2 || finish[1] != 0xf0 || finish[2] != 0x80 || finish[3] != 9 {
				return fmt.Errorf("expected session FINISH: %x", finish)
			}
			return peer.Abort()
		}()
	}()
	c := dialTest(t, listener.Addr().String(), Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = c.Conclude(ctx)
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Conclude hid or stalled on a peer abort: %v", err)
	}
	select {
	case err = <-peerResult:
		if err != nil {
			t.Fatalf("synthetic abort peer failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("synthetic abort peer did not stop")
	}
}
