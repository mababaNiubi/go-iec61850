// SPDX-License-Identifier: GPL-3.0-or-later
// Package ethernet provides optional operating-system Ethernet adapters.
// Frames include Ethernet headers and padding, but never an FCS. The protocol
// core does not depend on this package or an installed packet driver.
package ethernet

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

var (
	ErrUnavailable = errors.New("ethernet: packet adapter unavailable")
	ErrUnsupported = errors.New("ethernet: unsupported platform or link type")
	ErrFrameSize   = errors.New("ethernet: frame exceeds capture or configured size")
)

const (
	FlagLoopback   uint32 = 1
	FlagUp         uint32 = 2
	FlagRunning    uint32 = 4
	FlagWireless   uint32 = 8
	maxCaptureSize        = 65535
)

// Config requires an explicit interface; opening the default physical adapter
// implicitly is deliberately avoided. EtherType zero accepts all Ethernet types.
// ReadTimeout is the nonblocking polling interval (default 2 ms, maximum 100 ms).
type Config struct {
	Interface    string
	EtherType    uint16
	Promiscuous  bool
	ReadTimeout  time.Duration
	MaxFrameSize int
}

// Interface describes a packet adapter and its source MAC and capability flags.
type Interface struct {
	Name, Description string
	MAC               [6]byte
	Flags             uint32
}

type capturedPacket struct {
	data      []byte
	truncated bool
}
type backend interface {
	read() (capturedPacket, error)
	write([]byte) error
	close() error
}

// Conn serializes native handle operations. Native reads are nonblocking, so
// context cancellation and Close never race a blocking capture invocation.
// WriteFrame checks context before injection; native injection itself cannot be
// interrupted safely once entered and does not promise a hard real-time bound.
type Conn struct {
	mu        sync.Mutex
	readToken chan struct{}
	done      chan struct{}
	closed    bool
	driver    backend
	cfg       Config
	iface     Interface
}

func normalize(c Config) (Config, error) {
	if c.Interface == "" {
		return c, fmt.Errorf("ethernet: explicit interface required")
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 2 * time.Millisecond
	}
	if c.MaxFrameSize == 0 {
		c.MaxFrameSize = 1518
	}
	if c.ReadTimeout < time.Millisecond || c.ReadTimeout > 100*time.Millisecond || c.MaxFrameSize < 60 || c.MaxFrameSize > maxCaptureSize {
		return c, fmt.Errorf("ethernet: invalid capture limits")
	}
	return c, nil
}

// Open opens the explicitly named packet adapter. The caller owns Close. Physical access
// requires Linux raw-socket permissions or a Windows Npcap installation.
func Open(cfg Config) (*Conn, error) {
	cfg, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	driver, iface, err := openBackend(cfg)
	if err != nil {
		return nil, err
	}
	return &Conn{driver: driver, iface: iface, cfg: cfg, done: make(chan struct{}), readToken: make(chan struct{}, 1)}, nil
}

// Interface returns metadata for the selected adapter.
func (c *Conn) Interface() Interface { return c.iface }

// MAC returns the selected adapter source MAC by value.
func (c *Conn) MAC() [6]byte { return c.iface.MAC }

// Close closes the native adapter and unblocks polling reads. It is idempotent.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	close(c.done)
	return c.driver.close()
}

// ReadFrame waits for the next matching Ethernet frame without FCS. It returns owned bytes
// and honors cancellation while polling the adapter.
func (c *Conn) ReadFrame(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case c.readToken <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, net.ErrClosed
	}
	defer func() { <-c.readToken }()
	timer := time.NewTimer(c.cfg.ReadTimeout)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, net.ErrClosed
		}
		packet, err := c.driver.read()
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if packet.data != nil && matchesEtherType(packet.data, c.cfg.EtherType) {
			if packet.truncated || len(packet.data) > c.cfg.MaxFrameSize {
				return nil, ErrFrameSize
			}
			return packet.data, nil
		}
		if packet.data != nil {
			continue
		}
		timer.Reset(c.cfg.ReadTimeout)
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, net.ErrClosed
		}
	}
}

// WriteFrame validates and injects a complete Ethernet frame without FCS. Cancellation is
// checked before native injection; an entered injection cannot be interrupted.
func (c *Conn) WriteFrame(ctx context.Context, frame []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(frame) < 14 || len(frame) > c.cfg.MaxFrameSize {
		return ErrFrameSize
	}
	if !matchesEtherType(frame, c.cfg.EtherType) {
		return fmt.Errorf("ethernet: frame EtherType does not match configured filter")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.driver.write(frame)
}

// matchesEtherType understands up to two 802.1Q/802.1ad tags. Protocol codecs
// can impose a stricter VLAN profile independently.
func matchesEtherType(frame []byte, want uint16) bool {
	if len(frame) < 14 {
		return false
	}
	offset := 12
	for tags := 0; tags < 3; tags++ {
		if offset+2 > len(frame) {
			return false
		}
		kind := binary.BigEndian.Uint16(frame[offset : offset+2])
		if kind != 0x8100 && kind != 0x88a8 {
			return want == 0 || kind == want
		}
		if tags == 2 {
			return false
		}
		offset += 4
	}
	return false
}
