// SPDX-License-Identifier: GPL-3.0-or-later
// Protocol mapping derived from libiec61850's iso_cotp, iso_session,
// iso_presentation and iso_acse implementations (Michael Zillgith).

// Package iso implements the RFC 1006/COTP class 0, session, presentation and
// ACSE association profile used by IEC 61850 MMS. Conn has one receive stream;
// independent MMS requests are multiplexed by its caller, not by this package.
package iso

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Config specifies association addresses and resource limits. Nil selectors
// use the libiec61850 defaults; a non-nil empty selector omits that selector.
// AP titles are optional: nil omits the corresponding AP title and qualifier.
type Config struct {
	LocalTSelector, RemoteTSelector     []byte
	LocalSSelector, RemoteSSelector     []byte
	LocalPSelector, RemotePSelector     []byte
	LocalAPTitle, RemoteAPTitle         []uint32
	LocalAEQualifier, RemoteAEQualifier int
	MaxTPDUSize, MaxPDUSize             int
	HandshakeTimeout                    time.Duration
}

// DefaultConfig returns independent selector buffers. AP titles can be supplied
// by applications requiring them; omitting them is accepted by libiec61850.
func DefaultConfig() Config {
	return Config{
		LocalTSelector: []byte{0, 1}, RemoteTSelector: []byte{0, 1},
		LocalSSelector: []byte{0, 1}, RemoteSSelector: []byte{0, 1},
		LocalPSelector: []byte{0, 0, 0, 1}, RemotePSelector: []byte{0, 0, 0, 1},
		MaxTPDUSize: 8192, MaxPDUSize: 65000, HandshakeTimeout: 10 * time.Second,
	}
}

func normalize(cfg Config) (Config, error) {
	d := DefaultConfig()
	selectors := []*[]byte{&cfg.LocalTSelector, &cfg.RemoteTSelector, &cfg.LocalSSelector, &cfg.RemoteSSelector, &cfg.LocalPSelector, &cfg.RemotePSelector}
	defaults := [][]byte{d.LocalTSelector, d.RemoteTSelector, d.LocalSSelector, d.RemoteSSelector, d.LocalPSelector, d.RemotePSelector}
	for i, p := range selectors {
		if *p == nil {
			*p = defaults[i]
		}
		if len(*p) > 16 {
			return cfg, errors.New("iso: selector exceeds 16 octets")
		}
		*p = append([]byte{}, (*p)...)
	}
	if cfg.MaxTPDUSize == 0 {
		cfg.MaxTPDUSize = d.MaxTPDUSize
	}
	if cfg.MaxTPDUSize < 128 || cfg.MaxTPDUSize > 32768 {
		return cfg, errors.New("iso: TPDU limit must be 128..32768")
	}
	// COTP negotiates a power of two; round down, never above our buffer limit.
	cfg.MaxTPDUSize = 1 << tpduExponent(cfg.MaxTPDUSize)
	if cfg.MaxPDUSize == 0 {
		cfg.MaxPDUSize = d.MaxPDUSize
	}
	if cfg.MaxPDUSize < 64 || cfg.MaxPDUSize > 16<<20 {
		return cfg, errors.New("iso: PDU limit must be 64..16777216")
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = d.HandshakeTimeout
	}
	if cfg.HandshakeTimeout < 0 {
		return cfg, errors.New("iso: negative handshake timeout")
	}
	for _, title := range [][]uint32{cfg.LocalAPTitle, cfg.RemoteAPTitle} {
		if len(title) != 0 && (len(title) < 2 || title[0] > 2 || (title[0] < 2 && title[1] > 39)) {
			return cfg, errors.New("iso: invalid AP-title OID")
		}
	}
	if cfg.LocalAEQualifier < 0 || cfg.RemoteAEQualifier < 0 || uint64(cfg.LocalAEQualifier) > uint64(^uint32(0)) || uint64(cfg.RemoteAEQualifier) > uint64(^uint32(0)) {
		return cfg, errors.New("iso: AE qualifier must be a nonnegative 32-bit integer")
	}
	cfg.LocalAPTitle = append([]uint32(nil), cfg.LocalAPTitle...)
	cfg.RemoteAPTitle = append([]uint32(nil), cfg.RemoteAPTitle...)
	return cfg, nil
}

// Conn owns a TCP association. ReadPDU returns one complete MMS PDU in its own
// buffer. WritePDU serializes complete TSDUs, including their COTP segments.
type Conn struct {
	raw                     net.Conn
	cfg                     Config
	readMu, writeMu         sync.Mutex
	tpdu                    int
	localRef, remoteRef     uint16
	acseContext, mmsContext byte
	closing                 atomic.Bool
	releasePending          atomic.Bool
}

func (c *Conn) Close() error {
	c.closing.Store(true)
	c.releasePending.Store(false)
	return c.raw.Close()
}
func (c *Conn) SetDeadline(t time.Time) error      { return c.raw.SetDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.raw.SetWriteDeadline(t) }
func (c *Conn) LocalAddr() net.Addr                { return c.raw.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr               { return c.raw.RemoteAddr() }

func handshakeDeadline(ctx context.Context, raw net.Conn, timeout time.Duration) func() {
	d := time.Now().Add(timeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(d) {
		d = deadline
	}
	_ = raw.SetDeadline(d)
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(finished); _ = raw.SetDeadline(time.Now()) })
	return func() {
		if !stop() {
			<-finished
		}
		_ = raw.SetDeadline(time.Time{})
	}
}

// Dial performs COTP and ACSE association and carries the MMS Initiate PDU.
func Dial(ctx context.Context, address string, cfg Config, initiate []byte) (_ *Conn, response []byte, err error) {
	cfg, err = normalize(cfg)
	if err != nil {
		return nil, nil, err
	}
	if len(initiate) == 0 || len(initiate) > cfg.MaxPDUSize || len(initiate) > 65000 {
		return nil, nil, errors.New("iso: invalid initiate size")
	}
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = raw.Close()
			if ctx.Err() != nil {
				err = ctx.Err()
			}
		}
	}()
	clearDeadline := handshakeDeadline(ctx, raw, cfg.HandshakeTimeout)
	defer clearDeadline()
	c := &Conn{raw: raw, cfg: cfg, tpdu: cfg.MaxTPDUSize, localRef: 1, acseContext: 1, mmsContext: 3}
	if err = c.writeTPKT(c.connectTPDU(0xe0, cfg.LocalTSelector, cfg.RemoteTSelector)); err != nil {
		return nil, nil, fmt.Errorf("iso: COTP request: %w", err)
	}
	confirm, err := c.readTPKT()
	if err != nil {
		return nil, nil, fmt.Errorf("iso: COTP confirm: %w", err)
	}
	if _, _, err = c.parseConnect(confirm, 0xd0); err != nil {
		return nil, nil, err
	}
	request := encodeSession(13, cfg.LocalSSelector, cfg.RemoteSSelector, presentationConnect(cfg, associateRequest(cfg, initiate)))
	if err = c.writeTSDU(request); err != nil {
		return nil, nil, fmt.Errorf("iso: association request: %w", err)
	}
	accept, err := c.readTSDU()
	if err != nil {
		return nil, nil, fmt.Errorf("iso: association response: %w", err)
	}
	_, _, payload, err := parseSession(accept, 14)
	if err != nil {
		return nil, nil, err
	}
	acse, err := c.parsePresentationConnect(payload, true)
	if err != nil {
		return nil, nil, err
	}
	response, _, err = parseAssociate(acse, true)
	if err != nil {
		return nil, nil, err
	}
	if len(response) > cfg.MaxPDUSize {
		return nil, nil, errors.New("iso: initiate response exceeds PDU limit")
	}
	success = true
	return c, response, nil
}

// Accept accepts one association on an already accepted socket. On failure it
// closes raw; initiateHandler negotiates MMS and returns the Initiate response.
func Accept(ctx context.Context, raw net.Conn, cfg Config, initiateHandler func([]byte) ([]byte, error)) (_ *Conn, err error) {
	if raw == nil || initiateHandler == nil {
		return nil, errors.New("iso: socket and initiate handler are required")
	}
	success := false
	defer func() {
		if !success {
			_ = raw.Close()
			if ctx.Err() != nil {
				err = ctx.Err()
			}
		}
	}()
	cfg, err = normalize(cfg)
	if err != nil {
		return nil, err
	}
	clearDeadline := handshakeDeadline(ctx, raw, cfg.HandshakeTimeout)
	defer clearDeadline()
	c := &Conn{raw: raw, cfg: cfg, tpdu: cfg.MaxTPDUSize, localRef: 1, acseContext: 1, mmsContext: 3}
	request, err := c.readTPKT()
	if err != nil {
		return nil, fmt.Errorf("iso: COTP request: %w", err)
	}
	calling, called, err := c.parseConnect(request, 0xe0)
	if err != nil {
		return nil, err
	}
	if len(called) != 0 && !bytes.Equal(called, cfg.LocalTSelector) {
		return nil, errors.New("iso: called transport selector mismatch")
	}
	// The confirm repeats the requested source/destination selectors.
	if err = c.writeTPKT(c.connectTPDU(0xd0, calling, called)); err != nil {
		return nil, err
	}
	association, err := c.readTSDU()
	if err != nil {
		return nil, fmt.Errorf("iso: association request: %w", err)
	}
	_, calledS, payload, err := parseSession(association, 13)
	if err != nil {
		return nil, err
	}
	if len(calledS) != 0 && !bytes.Equal(calledS, cfg.LocalSSelector) {
		return nil, errors.New("iso: called session selector mismatch")
	}
	acse, err := c.parsePresentationConnect(payload, false)
	if err != nil {
		return nil, err
	}
	initiate, indirect, err := parseAssociate(acse, false)
	if err != nil {
		return nil, err
	}
	if len(initiate) > cfg.MaxPDUSize {
		return nil, errors.New("iso: initiate exceeds PDU limit")
	}
	response, err := initiateHandler(initiate)
	if err != nil {
		return nil, err
	}
	if len(response) == 0 || len(response) > cfg.MaxPDUSize || len(response) > 65000 {
		return nil, errors.New("iso: invalid initiate response size")
	}
	accept := encodeSession(14, nil, cfg.LocalSSelector, presentationAccept(cfg, c.acseContext, associateResponse(response, indirect)))
	if err = c.writeTSDU(accept); err != nil {
		return nil, err
	}
	success = true
	return c, nil
}

func (c *Conn) ReadPDU() ([]byte, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	tsd, err := c.readTSDU()
	if err != nil {
		return nil, err
	}
	if len(tsd) >= 4 && bytes.Equal(tsd[:4], []byte{1, 0, 1, 0}) {
		id, pdu, err := parsePresentationData(tsd[4:])
		if err != nil {
			return nil, err
		}
		if id != c.mmsContext {
			return nil, errors.New("iso: unexpected presentation context")
		}
		if len(pdu) > c.cfg.MaxPDUSize {
			return nil, errors.New("iso: MMS PDU exceeds limit")
		}
		return pdu, nil
	}
	if len(tsd) == 0 {
		return nil, errors.New("iso: empty session TSDU")
	}
	switch tsd[0] {
	case 9:
		_, _, payload, err := parseSession(tsd, 9)
		if err != nil {
			return nil, err
		}
		id, acse, err := parsePresentationData(payload)
		if err != nil {
			return nil, err
		}
		_, acseErr := single(acse, 0x62)
		if id != c.acseContext || acseErr != nil {
			return nil, errors.New("iso: invalid ACSE release request")
		}
		c.closing.Store(true)
		c.releasePending.Store(false)
		c.writeMu.Lock()
		err = c.writeTSDU(encodeSession(10, nil, nil, presentationData(c.acseContext, tlv(0x63))))
		c.writeMu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, io.EOF
	case 10:
		_, _, payload, err := parseSession(tsd, 10)
		if err != nil {
			return nil, err
		}
		id, acse, err := parsePresentationData(payload)
		if err != nil {
			return nil, err
		}
		_, acseErr := single(acse, 0x63)
		if id != c.acseContext || acseErr != nil {
			return nil, errors.New("iso: invalid ACSE release response")
		}
		if !c.releasePending.Swap(false) {
			return nil, errors.New("iso: unsolicited ACSE release response")
		}
		return nil, io.EOF
	case 25:
		c.closing.Store(true)
		c.releasePending.Store(false)
		return nil, errors.New("iso: remote association aborted")
	default:
		return nil, fmt.Errorf("iso: unexpected session SPDU 0x%02x", tsd[0])
	}
}

func (c *Conn) WritePDU(pdu []byte) error {
	if len(pdu) == 0 || len(pdu) > c.cfg.MaxPDUSize {
		return errors.New("iso: invalid MMS PDU size")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closing.Load() {
		return net.ErrClosed
	}
	return c.writeTSDU(append([]byte{1, 0, 1, 0}, presentationData(c.mmsContext, pdu)...))
}

// Release starts an orderly ACSE release. ReadPDU consumes its confirmation;
// Close remains available to interrupt a stalled peer. Repeated calls are
// idempotent only while the release confirmation is pending; after completion
// or Close they return net.ErrClosed.
func (c *Conn) Release() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closing.Swap(true) {
		if c.releasePending.Load() {
			return nil
		}
		return net.ErrClosed
	}
	// Set this before writing: a fast peer may deliver its confirmation to
	// ReadPDU before Write returns. A failed write rolls the pending state back.
	c.releasePending.Store(true)
	err := c.writeTSDU(encodeSession(9, nil, nil, presentationData(c.acseContext, tlv(0x62, tlv(0x80, []byte{0})))))
	if err != nil {
		c.releasePending.Store(false)
	}
	return err
}

func (c *Conn) Abort() error {
	c.writeMu.Lock()
	c.closing.Store(true)
	c.releasePending.Store(false)
	presentation := tlv(0xa0, presentationData(c.acseContext, tlv(0x64, tlv(0x80, []byte{0}))))
	err := c.writeTSDU(encodeSession(25, nil, nil, presentation))
	c.writeMu.Unlock()
	closeErr := c.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func tpduExponent(size int) byte {
	var n byte
	for size > 1 {
		size >>= 1
		n++
	}
	return n
}

func (c *Conn) connectTPDU(kind byte, source, dest []byte) []byte {
	b := []byte{0, kind, byte(c.remoteRef >> 8), byte(c.remoteRef), byte(c.localRef >> 8), byte(c.localRef), 0, 0xc0, 1, tpduExponent(c.tpdu)}
	if len(dest) != 0 {
		b = append(b, 0xc2, byte(len(dest)))
		b = append(b, dest...)
	}
	if len(source) != 0 {
		b = append(b, 0xc1, byte(len(source)))
		b = append(b, source...)
	}
	b[0] = byte(len(b) - 1)
	return b
}

func (c *Conn) parseConnect(b []byte, kind byte) (source, dest []byte, err error) {
	if len(b) < 7 || int(b[0])+1 != len(b) || b[1] != kind || b[6] != 0 {
		return nil, nil, errors.New("iso: malformed COTP connect TPDU")
	}
	if kind == 0xd0 && binary.BigEndian.Uint16(b[2:4]) != c.localRef {
		return nil, nil, errors.New("iso: COTP destination reference mismatch")
	}
	if kind == 0xe0 && binary.BigEndian.Uint16(b[2:4]) != 0 {
		return nil, nil, errors.New("iso: COTP CR has nonzero destination reference")
	}
	c.remoteRef = binary.BigEndian.Uint16(b[4:6])
	peerSize := 128 // RFC 905 default when absent
	seen := map[byte]bool{}
	for p := 7; p < len(b); {
		if len(b)-p < 2 {
			return nil, nil, errors.New("iso: truncated COTP option")
		}
		tag, n := b[p], int(b[p+1])
		p += 2
		if n > len(b)-p || seen[tag] {
			return nil, nil, errors.New("iso: invalid COTP option")
		}
		seen[tag] = true
		value := b[p : p+n]
		p += n
		switch tag {
		case 0xc0:
			if n != 1 || value[0] < 7 || value[0] > 16 {
				return nil, nil, errors.New("iso: invalid TPDU exponent")
			}
			peerSize = 1 << value[0]
		case 0xc1:
			if n > 16 {
				return nil, nil, errors.New("iso: transport selector too long")
			}
			source = append([]byte(nil), value...)
		case 0xc2:
			if n > 16 {
				return nil, nil, errors.New("iso: transport selector too long")
			}
			dest = append([]byte(nil), value...)
		default:
			return nil, nil, fmt.Errorf("iso: unsupported COTP option 0x%02x", tag)
		}
	}
	if peerSize < c.tpdu {
		c.tpdu = peerSize
	}
	return source, dest, nil
}

func (c *Conn) readTPKT() ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(c.raw, h[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(h[2:]))
	if h[0] != 3 || h[1] != 0 || n < 7 {
		return nil, errors.New("iso: invalid TPKT header")
	}
	if n-4 > c.tpdu && n-4 > 256 {
		return nil, errors.New("iso: TPKT exceeds negotiated TPDU limit")
	}
	b := make([]byte, n-4)
	if _, err := io.ReadFull(c.raw, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (c *Conn) writeTPKT(tpdu []byte) error {
	if len(tpdu)+4 > 65535 {
		return errors.New("iso: TPKT too large")
	}
	b := make([]byte, len(tpdu)+4)
	b[0] = 3
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	copy(b[4:], tpdu)
	for len(b) > 0 {
		n, err := c.raw.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func (c *Conn) readTSDU() ([]byte, error) {
	var out []byte
	for {
		b, err := c.readTPKT()
		if err != nil {
			return nil, err
		}
		if len(b) < 3 || b[0] != 2 || b[1] != 0xf0 || b[2]&0x7f != 0 {
			return nil, errors.New("iso: malformed COTP DT TPDU")
		}
		if len(b) > c.tpdu {
			return nil, errors.New("iso: data TPDU exceeds negotiated limit")
		}
		if len(out)+len(b)-3 > c.cfg.MaxPDUSize+1024 {
			return nil, errors.New("iso: reassembled TSDU exceeds limit")
		}
		if len(b) == 3 && b[2]&0x80 == 0 {
			return nil, errors.New("iso: empty non-final COTP segment")
		}
		out = append(out, b[3:]...)
		if b[2]&0x80 != 0 {
			return out, nil
		}
	}
}

func (c *Conn) writeTSDU(b []byte) error {
	for len(b) > 0 {
		n := min(len(b), c.tpdu-3)
		header := []byte{2, 0xf0, 0}
		if n == len(b) {
			header[2] = 0x80
		}
		if err := c.writeTPKT(append(header, b[:n]...)); err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}
