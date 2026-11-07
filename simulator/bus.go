// SPDX-License-Identifier: GPL-3.0-or-later
package simulator

import (
	"bytes"
	"context"
	"fmt"
	"sync"
)

// MemoryBus fans complete Ethernet frames out to independent IED endpoints.
// It is a software station bus shared by GOOSE and SV. Each endpoint has one
// reader. Frames are copied and a full endpoint rejects the entire broadcast,
// making overload observable instead of silently losing a simulated event.
type MemoryBus struct {
	mu     sync.Mutex
	ports  map[*BusPort]struct{}
	closed bool
}
type BusPort struct {
	bus    *MemoryBus
	frames chan []byte
	done   chan struct{}
	closed bool // bus.mu
}

func (b *MemoryBus) Open(capacity int) (*BusPort, error) {
	if capacity < 1 || capacity > 65536 {
		return nil, fmt.Errorf("simulator: bus capacity must be 1..65536")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, fmt.Errorf("simulator: bus closed")
	}
	if len(b.ports) >= 256 {
		return nil, fmt.Errorf("simulator: bus endpoint limit")
	}
	if b.ports == nil {
		b.ports = map[*BusPort]struct{}{}
	}
	p := &BusPort{bus: b, frames: make(chan []byte, capacity), done: make(chan struct{})}
	b.ports[p] = struct{}{}
	return p, nil
}
func (p *BusPort) ReadFrame(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, fmt.Errorf("simulator: bus endpoint closed")
	case frame := <-p.frames:
		return bytes.Clone(frame), nil
	}
}
func (p *BusPort) WriteFrame(ctx context.Context, frame []byte) error {
	if len(frame) < 14 || len(frame) > 1522 {
		return fmt.Errorf("simulator: invalid Ethernet frame size")
	}
	b := p.bus
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.closed || b.closed {
		return fmt.Errorf("simulator: bus endpoint closed")
	}
	for port := range b.ports {
		if len(port.frames) == cap(port.frames) {
			return fmt.Errorf("simulator: station bus receive queue overflow")
		}
	}
	copy := bytes.Clone(frame)
	for port := range b.ports {
		port.frames <- copy
	}
	return nil
}
func (p *BusPort) Close() error {
	b := p.bus
	b.mu.Lock()
	defer b.mu.Unlock()
	if !p.closed {
		p.closed = true
		delete(b.ports, p)
		close(p.done)
	}
	return nil
}
func (b *MemoryBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		for p := range b.ports {
			p.closed = true
			close(p.done)
		}
		b.ports = nil
	}
	return nil
}
