// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
)

// FrameIO exchanges complete Ethernet frames, excluding FCS. Implementations
// must honor context cancellation. The caller owns and closes the link.
type FrameIO interface {
	ReadFrame(context.Context) ([]byte, error)
	WriteFrame(context.Context, []byte) error
	Close() error
}

var ErrRunning = errors.New("goose: already running")

type PublisherConfig struct {
	Frame   Frame
	Types   []data.TypeSpec
	MinTime time.Duration
	MaxTime time.Duration
	// TALFactor multiplies the next retransmission interval (default 2).
	TALFactor uint32
	Clock     Clock
}

// Publisher owns stNum/sqNum and retransmission state. Update changes the
// dataset atomically; Run publishes immediately, then backs off to MaxTime.
type Publisher struct {
	link    FrameIO
	cfg     PublisherConfig
	mu      sync.Mutex
	frame   Frame
	wake    chan struct{}
	running atomic.Bool
	sent    uint64
}

func cloneTypes(types []data.TypeSpec) ([]data.TypeSpec, error) {
	if types == nil {
		return nil, nil
	}
	out := make([]data.TypeSpec, len(types))
	for i, t := range types {
		b, err := data.EncodeType(t)
		if err != nil {
			return nil, err
		}
		out[i], err = data.DecodeType(b)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func validateDataset(types []data.TypeSpec, values []data.Value) error {
	if types != nil && len(types) != len(values) {
		return fmt.Errorf("goose: dataset/type count mismatch")
	}
	if len(values) > MaxDataSetEntries {
		return fmt.Errorf("goose: dataset is too large")
	}
	for i, v := range values {
		if v.Depth() > 64 {
			return fmt.Errorf("goose: dataset nesting exceeds limit")
		}
		if _, err := data.EncodeValue(v); err != nil {
			return err
		}
		if types != nil {
			if err := data.ValidateValue(types[i], v); err != nil {
				return fmt.Errorf("goose: item %d: %w", i, err)
			}
		}
	}
	return nil
}
func eventTime(now time.Time) ([8]byte, error) {
	var out [8]byte
	v := data.UTC(now)
	b, err := v.Bytes()
	if err != nil || len(b) != 8 {
		return out, fmt.Errorf("goose: UTC timestamp out of range")
	}
	copy(out[:], b)
	return out, nil
}
func nextCounter(n uint32) uint32 {
	n++
	if n == 0 {
		return 1
	}
	return n
}
func sameValues(a, b []data.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}
func sameLayout(a, b data.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case data.KindArray, data.KindStructure:
		x, _ := a.Elements()
		y, _ := b.Elements()
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameLayout(x[i], y[i]) {
				return false
			}
		}
	case data.KindBitString:
		x, _ := a.BitLength()
		y, _ := b.BitLength()
		return x == y
	case data.KindFloat, data.KindBinaryTime:
		x, _ := a.Bytes()
		y, _ := b.Bytes()
		return len(x) == len(y)
	}
	return true
}

// NewPublisher 创建有状态发布者，复制初始 Frame 和类型约束。link 由调用方关闭；
// 真实网卡的 Source 应取 conn.MAC()，同一数据集两端的 APPID/引用/顺序应一致。
func NewPublisher(link FrameIO, cfg PublisherConfig) (*Publisher, error) {
	if link == nil {
		return nil, fmt.Errorf("goose: nil frame link")
	}
	if cfg.Frame.Source == ([6]byte{}) || cfg.Frame.Source[0]&1 != 0 || !multicastDestination(cfg.Frame.Destination) {
		return nil, fmt.Errorf("goose: publisher requires a unicast source and multicast destination MAC")
	}
	if cfg.Frame.Reserved1 != 0 || cfg.Frame.Reserved2 != 0 {
		return nil, fmt.Errorf("goose: security/reserved extensions are not supported")
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.MinTime == 0 {
		cfg.MinTime = 4 * time.Millisecond
	}
	if cfg.MaxTime == 0 {
		cfg.MaxTime = time.Second
	}
	if cfg.TALFactor == 0 {
		cfg.TALFactor = 2
	}
	if cfg.MinTime < time.Millisecond || cfg.MaxTime < cfg.MinTime || cfg.MaxTime > 24*time.Hour || cfg.TALFactor < 2 || uint64((cfg.MaxTime+time.Millisecond-1)/time.Millisecond)*uint64(cfg.TALFactor) > math.MaxUint32 {
		return nil, fmt.Errorf("goose: invalid retransmission timing")
	}
	types, err := cloneTypes(cfg.Types)
	if err != nil {
		return nil, err
	}
	cfg.Types = types
	if err = validateDataset(types, cfg.Frame.PDU.Values); err != nil {
		return nil, err
	}
	f := cfg.Frame.Clone()
	f.PDU.StateNumber = 1
	f.PDU.SequenceNumber = 0
	f.PDU.TimeAllowedToLive = uint32((cfg.MinTime+time.Millisecond-1)/time.Millisecond) * cfg.TALFactor
	f.PDU.Timestamp, err = eventTime(cfg.Clock.Now())
	if err != nil {
		return nil, err
	}
	if _, err = EncodeFrame(f); err != nil {
		return nil, err
	}
	cfg.Frame = f.Clone()
	return &Publisher{link: link, cfg: cfg, frame: f, wake: make(chan struct{}, 1)}, nil
}

// Update preserves the dataset layout and confRev. To change its configuration,
// create a publisher with the new revision. Equal values do not change stNum.
func (p *Publisher) Update(values []data.Value) (bool, error) {
	if err := validateDataset(p.cfg.Types, values); err != nil {
		return false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(values) != len(p.frame.PDU.Values) {
		return false, fmt.Errorf("goose: dataset layout changed; configure a new revision")
	}
	for i, v := range values {
		if !sameLayout(p.frame.PDU.Values[i], v) {
			return false, fmt.Errorf("goose: dataset item %d layout changed", i)
		}
	}
	if sameValues(p.frame.PDU.Values, values) {
		return false, nil
	}
	t, err := eventTime(p.cfg.Clock.Now())
	if err != nil {
		return false, err
	}
	candidate := p.frame.Clone()
	candidate.PDU.Values = append([]data.Value(nil), values...)
	candidate.PDU.StateNumber = nextCounter(candidate.PDU.StateNumber)
	candidate.PDU.SequenceNumber = 0
	candidate.PDU.Timestamp = t
	if _, err = EncodeFrame(candidate); err != nil {
		return false, err
	}
	p.frame = candidate
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return true, nil
}

func (p *Publisher) Snapshot() Frame { p.mu.Lock(); defer p.mu.Unlock(); return p.frame.Clone() }
func (p *Publisher) Sent() uint64    { p.mu.Lock(); defer p.mu.Unlock(); return p.sent }

func (p *Publisher) emit(ctx context.Context, interval time.Duration, lastState uint32) (uint32, time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frame.PDU.StateNumber != lastState {
		interval = p.cfg.MinTime
	}
	p.frame.PDU.TimeAllowedToLive = uint32((interval+time.Millisecond-1)/time.Millisecond) * p.cfg.TALFactor
	b, err := EncodeFrame(p.frame)
	if err != nil {
		return lastState, interval, err
	}
	if err = p.link.WriteFrame(ctx, b); err != nil {
		return lastState, interval, err
	}
	state := p.frame.PDU.StateNumber
	p.frame.PDU.SequenceNumber = nextCounter(p.frame.PDU.SequenceNumber)
	p.sent++
	select {
	case <-p.wake:
	default:
	}
	return state, interval, nil
}

// Run returns on cancellation or an encoding/link error. It does not close the
// supplied FrameIO. Only one Run or Publish may operate at a time.
func (p *Publisher) Run(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer p.running.Store(false)
	interval := p.cfg.MinTime
	var lastState uint32
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		lastState, interval, err = p.emit(ctx, interval, lastState)
		if err != nil {
			return err
		}
		timer := p.cfg.Clock.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-p.wake:
			timer.Stop()
			interval = p.cfg.MinTime
		case <-timer.C():
			interval = min(interval*2, p.cfg.MaxTime)
		}
	}
}

// Publish sends one frame using MaxTime to derive TAL; callers choosing manual
// scheduling must send again before that TAL. Run supplies automatic backoff.
func (p *Publisher) Publish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer p.running.Store(false)
	_, _, err := p.emit(ctx, p.cfg.MaxTime, p.Snapshot().PDU.StateNumber)
	return err
}
