// SPDX-License-Identifier: GPL-3.0-or-later
package iso

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

type fragmentConn struct {
	net.Conn
	size int
}

func (c fragmentConn) Write(p []byte) (int, error) { return c.Conn.Write(p[:min(len(p), c.size)]) }

func TestAssociationSegmentedDataAndRelease(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initial := []byte{0xa8, 1, 0}
	reply := []byte{0xa9, 1, 0}
	message := bytes.Repeat([]byte{0xa1, 0, 0xff, 0}, 10000)
	serverResult := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			serverResult <- err
			return
		}
		cfg := Config{MaxTPDUSize: 128}
		server, err := Accept(ctx, fragmentConn{raw, 3}, cfg, func(pdu []byte) ([]byte, error) {
			if !bytes.Equal(pdu, initial) {
				return nil, errors.New("initiate mismatch")
			}
			return reply, nil
		})
		if err != nil {
			serverResult <- err
			return
		}
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(4 * time.Second))
		pdu, err := server.ReadPDU()
		if err != nil {
			serverResult <- err
			return
		}
		if !bytes.Equal(pdu, message) {
			serverResult <- fmt.Errorf("data mismatch: %d bytes", len(pdu))
			return
		}
		if err = server.WritePDU(pdu); err != nil {
			serverResult <- err
			return
		}
		_, err = server.ReadPDU()
		if !errors.Is(err, io.EOF) {
			serverResult <- fmt.Errorf("release returned %v", err)
			return
		}
		serverResult <- nil
	}()
	client, got, err := Dial(ctx, listener.Addr().String(), Config{MaxTPDUSize: 512}, initial)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(4 * time.Second))
	if !bytes.Equal(got, reply) {
		t.Fatalf("initiate response %x", got)
	}
	if client.tpdu != 128 {
		t.Fatalf("negotiated TPDU=%d", client.tpdu)
	}
	if err = client.WritePDU(message); err != nil {
		t.Fatal(err)
	}
	pdu, err := client.ReadPDU()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pdu, message) {
		t.Fatal("response differs")
	}
	if err = client.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ReadPDU(); !errors.Is(err, io.EOF) {
		t.Fatalf("release: %v", err)
	}
	if err = <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestSelectorMismatchRejectsAssociation(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	serverResult := make(chan error, 1)
	go func() {
		raw, err := l.Accept()
		if err != nil {
			serverResult <- err
			return
		}
		_, err = Accept(ctx, raw, Config{}, func([]byte) ([]byte, error) { return nil, errors.New("handler must not run") })
		serverResult <- err
	}()
	c, _, err := Dial(ctx, l.Addr().String(), Config{RemoteTSelector: []byte{0, 2}}, []byte{0xa8, 0})
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("mismatched called T selector accepted")
	}
	if err = <-serverResult; err == nil {
		t.Fatal("server accepted selector mismatch")
	}
}

func TestDialCancellationDuringHandshake(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		raw, err := l.Accept()
		if err == nil {
			accepted <- raw
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	start := time.Now()
	_, _, err = Dial(ctx, l.Addr().String(), Config{HandshakeTimeout: 5 * time.Second}, []byte{0xa8, 0})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not interrupt handshake")
	}
	select {
	case raw := <-accepted:
		raw.Close()
	default:
	}
}

func TestMalformedTransportFrames(t *testing.T) {
	cases := map[string][]byte{
		"version":       {4, 0, 0, 7, 2, 0xf0, 0x80},
		"reserved":      {3, 1, 0, 7, 2, 0xf0, 0x80},
		"short":         {3, 0, 0, 6, 2, 0xf0},
		"truncated":     {3, 0, 0, 9, 2, 0xf0, 0x80},
		"DT length":     {3, 0, 0, 8, 3, 0xf0, 0x80, 0},
		"DT sequence":   {3, 0, 0, 8, 2, 0xf0, 0x81, 0},
		"empty segment": {3, 0, 0, 7, 2, 0xf0, 0},
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			go func() { _, _ = b.Write(frame); b.Close() }()
			c := &Conn{raw: a, cfg: DefaultConfig(), tpdu: 128}
			if _, err := c.readTSDU(); err == nil {
				t.Fatal("malformed frame accepted")
			}
		})
	}
}

func TestReassemblyBound(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := &Conn{raw: a, cfg: Config{MaxPDUSize: 64}, tpdu: 128}
	go func() { writer := &Conn{raw: b, tpdu: 128}; _ = writer.writeTSDU(make([]byte, 2000)); b.Close() }()
	if _, err := c.readTSDU(); err == nil {
		t.Fatal("unbounded reassembly")
	}
}

func TestPresentationContextNegotiation(t *testing.T) {
	c := &Conn{cfg: DefaultConfig()}
	contexts := tlv(0xa4, tlv(0x30, intTLV(2, 7), tlv(6, acseSyntax), tlv(0x30, tlv(6, berSyntax))), tlv(0x30, intTLV(2, 9), tlv(6, mmsSyntax), tlv(0x30, tlv(6, berSyntax))))
	acse := associateRequest(Config{}, []byte{0xa8, 0})
	cp := tlv(0x31, tlv(0xa0, tlv(0x80, []byte{1})), tlv(0xa2, contexts, presentationData(7, acse)))
	got, err := c.parsePresentationConnect(cp, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, acse) || c.acseContext != 7 || c.mmsContext != 9 {
		t.Fatal("peer context identifiers were not retained")
	}
}

func TestConstructedIndefiniteBER(t *testing.T) {
	b := []byte{0x61, 0x80, 0x30, 0x80, 2, 1, 3, 0xa0, 2, 0xa8, 0, 0, 0, 0, 0}
	id, payload, err := parsePresentationData(b)
	if err != nil {
		t.Fatal(err)
	}
	if id != 3 || !bytes.Equal(payload, []byte{0xa8, 0}) {
		t.Fatal("indefinite content mismatch")
	}
	if _, _, err = parsePresentationData(append(b, 0)); err == nil {
		t.Fatal("trailing content accepted")
	}
}

func TestAssociationRejectAndAuthentication(t *testing.T) {
	rejected := tlv(0x61, tlv(0xa1, tlv(6, mmsApplication)), tlv(0xa2, intTLV(2, 1)), tlv(0xbe, tlv(0x28, intTLV(2, 3), tlv(0xa0, []byte{0xa9, 0}))))
	if _, _, err := parseAssociate(rejected, true); err == nil {
		t.Fatal("ACSE rejection ignored")
	}
	auth := tlv(0x60, tlv(0xa1, tlv(6, mmsApplication)), tlv(0x8b, []byte{0x52, 3, 1}), tlv(0xbe, tlv(0x28, intTLV(2, 3), tlv(0xa0, []byte{0xa8, 0}))))
	if _, _, err := parseAssociate(auth, false); err == nil {
		t.Fatal("unsupported authentication silently accepted")
	}
}

func FuzzAssociationDecoders(f *testing.F) {
	f.Add(presentationConnect(DefaultConfig(), associateRequest(Config{}, []byte{0xa8, 0})))
	f.Add([]byte{0x61, 0x80, 0, 0})
	f.Add([]byte{13, 2, 0xc1, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 70000 {
			return
		}
		c := &Conn{cfg: DefaultConfig()}
		_, _ = c.parsePresentationConnect(b, false)
		_, _, _ = parseAssociate(b, false)
		_, _, _, _ = parseSession(b, 13)
		_, _, _ = parsePresentationData(b)
	})
}
