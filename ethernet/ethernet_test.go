// SPDX-License-Identifier: GPL-3.0-or-later
package ethernet

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu      sync.Mutex
	packets []capturedPacket
	writes  [][]byte
	closed  bool
}

func (b *fakeBackend) read() (capturedPacket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return capturedPacket{}, net.ErrClosed
	}
	if len(b.packets) == 0 {
		return capturedPacket{}, nil
	}
	p := b.packets[0]
	b.packets = b.packets[1:]
	return p, nil
}
func (b *fakeBackend) write(p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return net.ErrClosed
	}
	b.writes = append(b.writes, append([]byte(nil), p...))
	return nil
}
func (b *fakeBackend) close() error { b.mu.Lock(); defer b.mu.Unlock(); b.closed = true; return nil }
func fakeConn(b *fakeBackend) *Conn {
	return &Conn{driver: b, cfg: Config{EtherType: 0x88b8, MaxFrameSize: 1518, ReadTimeout: time.Millisecond}, done: make(chan struct{}), readToken: make(chan struct{}, 1)}
}
func testFrame(tags int, kind uint16) []byte {
	b := make([]byte, 60)
	offset := 12
	for i := 0; i < tags; i++ {
		binary.BigEndian.PutUint16(b[offset:offset+2], 0x8100)
		offset += 4
	}
	binary.BigEndian.PutUint16(b[offset:offset+2], kind)
	return b
}

func TestEtherTypeAndVLAN(t *testing.T) {
	for tags := 0; tags <= 2; tags++ {
		if !matchesEtherType(testFrame(tags, 0x88b8), 0x88b8) {
			t.Fatalf("GOOSE VLAN tags=%d not matched", tags)
		}
		if matchesEtherType(testFrame(tags, 0x0800), 0x88b8) {
			t.Fatalf("IPv4 VLAN tags=%d matched", tags)
		}
	}
	if matchesEtherType([]byte{1, 2}, 0) {
		t.Fatal("short frame matched")
	}
	if matchesEtherType(testFrame(3, 0x88b8), 0x88b8) {
		t.Fatal("unbounded VLAN nesting")
	}
}
func TestReadFiltersAndBounds(t *testing.T) {
	b := &fakeBackend{packets: []capturedPacket{{data: testFrame(0, 0x0800)}, {data: testFrame(1, 0x88b8)}}}
	c := fakeConn(b)
	defer c.Close()
	p, err := c.ReadFrame(context.Background())
	if err != nil || !matchesEtherType(p, 0x88b8) {
		t.Fatalf("read %x %v", p, err)
	}
	b.mu.Lock()
	b.packets = append(b.packets, capturedPacket{data: testFrame(0, 0x88b8), truncated: true})
	b.mu.Unlock()
	if _, err := c.ReadFrame(context.Background()); !errors.Is(err, ErrFrameSize) {
		t.Fatalf("truncated frame: %v", err)
	}
}
func TestCancellationAndConcurrentClose(t *testing.T) {
	b := &fakeBackend{}
	c := fakeConn(b)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.ReadFrame(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	received := make(chan error, 1)
	go func() { _, err := c.ReadFrame(context.Background()); received <- err }()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-received:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock reader")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteFrame(context.Background(), testFrame(0, 0x88b8)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}
func TestWriteValidation(t *testing.T) {
	b := &fakeBackend{}
	c := fakeConn(b)
	defer c.Close()
	if err := c.WriteFrame(context.Background(), testFrame(0, 0x0800)); err == nil {
		t.Fatal("wrong EtherType injected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.WriteFrame(ctx, testFrame(0, 0x88b8)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.WriteFrame(context.Background(), testFrame(0, 0x88b8)); err != nil {
		t.Fatal(err)
	}
	if len(b.writes) != 1 {
		t.Fatalf("injected %d frames", len(b.writes))
	}
}
func TestConfigBounds(t *testing.T) {
	for _, cfg := range []Config{{}, {Interface: "x", ReadTimeout: -time.Millisecond}, {Interface: "x", MaxFrameSize: 10}, {Interface: "x", MaxFrameSize: 65536}} {
		if _, err := normalize(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}
