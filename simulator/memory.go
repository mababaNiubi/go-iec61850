// SPDX-License-Identifier: GPL-3.0-or-later
package simulator

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
)

// MemoryLink loops complete Ethernet frames back to one reader. It implements
// both goose.FrameIO and sv.FrameIO for deterministic integration tests without
// a NIC. It models frame transport, not a physical multicast network.
type MemoryLink struct {
	packets chan []byte
	done    chan struct{}
	once    sync.Once
}

// NewMemoryLink creates a caller-owned loopback link. Buffer zero selects 64 frames; the
// supported range is 1..4096.
func NewMemoryLink(buffer int) (*MemoryLink, error) {
	if buffer == 0 {
		buffer = 64
	}
	if buffer < 1 || buffer > 4096 {
		return nil, fmt.Errorf("simulator: invalid frame buffer size")
	}
	return &MemoryLink{packets: make(chan []byte, buffer), done: make(chan struct{})}, nil
}

// ReadFrame waits for an owned frame, cancellation or link closure. Use only one reader per link.
func (l *MemoryLink) ReadFrame(ctx context.Context) ([]byte, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, net.ErrClosed
	case raw := <-l.packets:
		return raw, nil
	}
}

// WriteFrame copies raw and waits for queue space, cancellation or link closure.
func (l *MemoryLink) WriteFrame(ctx context.Context, raw []byte) error {
	select {
	case <-l.done:
		return net.ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return net.ErrClosed
	case l.packets <- bytes.Clone(raw):
		return nil
	}
}

// Close closes the link and unblocks pending reads/writes. It is idempotent.
func (l *MemoryLink) Close() error { l.once.Do(func() { close(l.done) }); return nil }
