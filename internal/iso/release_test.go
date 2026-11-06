// SPDX-License-Identifier: GPL-3.0-or-later
package iso

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func pipeAssociation(t *testing.T) (*Conn, *Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := &Conn{raw: a, cfg: DefaultConfig(), tpdu: 128, acseContext: 1, mmsContext: 3}
	peer := &Conn{raw: b, cfg: DefaultConfig(), tpdu: 128, acseContext: 1, mmsContext: 3}
	deadline := time.Now().Add(3 * time.Second)
	_ = c.SetDeadline(deadline)
	_ = peer.SetDeadline(deadline)
	t.Cleanup(func() { c.Close(); peer.Close() })
	return c, peer
}

func TestUnsolicitedDisconnectIsNotEOF(t *testing.T) {
	c, peer := pipeAssociation(t)
	sent := make(chan error, 1)
	go func() {
		sent <- peer.writeTSDU(encodeSession(10, nil, nil, presentationData(1, tlv(0x63))))
	}()
	_, err := c.ReadPDU()
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("unsolicited DISCONNECT was accepted as normal release: %v", err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if c.releasePending.Load() {
		t.Fatal("unsolicited response created a pending release")
	}
}

func TestReleaseIdempotentOnlyWhilePending(t *testing.T) {
	c, peer := pipeAssociation(t)
	received := make(chan error, 1)
	confirm := make(chan struct{})
	finished := make(chan error, 1)
	t.Cleanup(func() {
		select {
		case <-confirm:
		default:
			close(confirm)
		}
	})
	go func() {
		pdu, err := peer.readTSDU()
		if err != nil {
			received <- err
			return
		}
		_, _, _, err = parseSession(pdu, 9)
		received <- err
		if err != nil {
			return
		}
		<-confirm
		finished <- peer.writeTSDU(encodeSession(10, nil, nil, presentationData(1, tlv(0x63))))
	}()
	if err := c.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
	if !c.releasePending.Load() {
		t.Fatal("initiated release was not marked pending")
	}
	// The peer is not reading here, so another actual FINISH write would
	// block and fail the pipe deadline instead of silently passing this test.
	if err := c.Release(); err != nil {
		t.Fatalf("pending release is not idempotent: %v", err)
	}
	close(confirm)
	if _, err := c.ReadPDU(); !errors.Is(err, io.EOF) {
		t.Fatalf("valid release confirmation: %v", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if c.releasePending.Load() {
		t.Fatal("completed release remained pending")
	}
	if err := c.Release(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Release after completion returned %v, want net.ErrClosed", err)
	}
}

func TestReleaseAfterCloseAndFailedWrite(t *testing.T) {
	t.Run("closed", func(t *testing.T) {
		c, _ := pipeAssociation(t)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if err := c.Release(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Release after Close returned %v, want net.ErrClosed", err)
		}
		if c.releasePending.Load() {
			t.Fatal("Release after Close created pending state")
		}
	})
	t.Run("write failed", func(t *testing.T) {
		c, peer := pipeAssociation(t)
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := c.Release(); err == nil {
			t.Fatal("Release succeeded on a disconnected transport")
		}
		if c.releasePending.Load() {
			t.Fatal("failed release write left pending state")
		}
		if err := c.Release(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("failed release was treated as pending: %v", err)
		}
	})
}
