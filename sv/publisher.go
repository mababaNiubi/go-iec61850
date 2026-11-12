// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// FrameIO transfers complete Ethernet frames excluding FCS. ReadFrame must
// return independently owned bytes and all operations must honor cancellation.
// The caller owns and closes the link.
type FrameIO interface {
	ReadFrame(context.Context) ([]byte, error)
	WriteFrame(context.Context, []byte) error
	Close() error
}

var ErrRunning = errors.New("sv: already running")

// PublisherConfig supplies the initial frame and software send interval. Interval zero
// selects 1ms; CounterModulus zero selects 65536. It does not configure a hardware sampling
// clock.
type PublisherConfig struct {
	Frame    Frame
	Interval time.Duration
	// CounterModulus is the exclusive wrap limit; zero selects 65536.
	CounterModulus uint32
	Clock          Clock
}

// Publisher sends new samples periodically. Unlike GOOSE, it never retransmits
// a sample. Counters advance after successful sends and skipped schedule slots.
type Publisher struct {
	link          FrameIO
	cfg           PublisherConfig
	mu            sync.Mutex
	frame         Frame
	steps         []uint32
	sent, skipped uint64
	running       atomic.Bool
}

func modulus(n uint32) (uint32, error) {
	if n == 0 {
		return 65536, nil
	}
	if n < 2 || n > 65536 {
		return 0, fmt.Errorf("sv: counter modulus must be 2..65536")
	}
	return n, nil
}
func multicastDestination(m [6]byte) bool {
	return m[0]&1 != 0 && m != ([6]byte{255, 255, 255, 255, 255, 255})
}
func unicastSource(m [6]byte) bool { return m != ([6]byte{}) && m[0]&1 == 0 }

// NewPublisher copies an explicitly laid-out ASDU.Data frame. Successful sends advance
// counters; CounterModulus zero selects 65536. The caller owns link.Close; sampling and
// synchronization remain external.
func NewPublisher(link FrameIO, cfg PublisherConfig) (*Publisher, error) {
	if link == nil {
		return nil, fmt.Errorf("sv: nil frame link")
	}
	if !multicastDestination(cfg.Frame.Destination) || !unicastSource(cfg.Frame.Source) {
		return nil, fmt.Errorf("sv: publisher requires a multicast destination and unicast source")
	}
	if cfg.Frame.Reserved1 != 0 || cfg.Frame.Reserved2 != 0 || cfg.Frame.PDU.Security != nil {
		return nil, fmt.Errorf("sv: security/reserved extensions are not supported")
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Interval == 0 {
		cfg.Interval = time.Millisecond
	}
	if cfg.Interval < 0 || cfg.Interval > 24*time.Hour {
		return nil, fmt.Errorf("sv: invalid publication interval")
	}
	var err error
	cfg.CounterModulus, err = modulus(cfg.CounterModulus)
	if err != nil {
		return nil, err
	}
	frame := cfg.Frame.Clone()
	if _, err = EncodeFrame(frame); err != nil {
		return nil, err
	}
	counts := make(map[string]uint32)
	last := make(map[string]uint32)
	streams := make(map[string]ASDU)
	for _, a := range frame.PDU.ASDUs {
		if uint32(a.SampleCount) >= cfg.CounterModulus {
			return nil, fmt.Errorf("sv: initial sample count exceeds modulus")
		}
		if counts[a.SVID] > 0 && uint32(a.SampleCount) != (last[a.SVID]+1)%cfg.CounterModulus {
			return nil, fmt.Errorf("sv: same-stream ASDUs must have consecutive sample counts")
		}
		if previous, ok := streams[a.SVID]; ok && !sameStreamConfiguration(a, previous) {
			return nil, fmt.Errorf("sv: same-stream ASDUs must share a configuration")
		}
		streams[a.SVID] = a
		counts[a.SVID]++
		last[a.SVID] = uint32(a.SampleCount)
	}
	steps := make([]uint32, len(frame.PDU.ASDUs))
	for i, a := range frame.PDU.ASDUs {
		steps[i] = counts[a.SVID]
	}
	cfg.Frame = frame.Clone()
	return &Publisher{link: link, cfg: cfg, frame: frame, steps: steps}, nil
}

// UpdateData copies one ASDU's raw sequence data. Configuration and byte length
// remain fixed; create a new publisher for a different dataset or revision.
func (p *Publisher) UpdateData(index int, raw []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.frame.PDU.ASDUs) {
		return fmt.Errorf("sv: ASDU index out of range")
	}
	if len(raw) != len(p.frame.PDU.ASDUs[index].Data) {
		return fmt.Errorf("sv: sample data length changed")
	}
	p.frame.PDU.ASDUs[index].Data = append([]byte(nil), raw...)
	return nil
}

// Snapshot returns an independent copy of the current frame and next sample counters.
func (p *Publisher) Snapshot() Frame { p.mu.Lock(); defer p.mu.Unlock(); return p.frame.Clone() }

// Sent returns the number of successfully transmitted Ethernet frames.
func (p *Publisher) Sent() uint64 { p.mu.Lock(); defer p.mu.Unlock(); return p.sent }

// SkippedSlots returns missed software schedule slots; skipped slots advance sample
// counters without sending catch-up frames.
func (p *Publisher) SkippedSlots() uint64 { p.mu.Lock(); defer p.mu.Unlock(); return p.skipped }

func (p *Publisher) advanceLocked(slots uint64) {
	for i := range p.frame.PDU.ASDUs {
		a := &p.frame.PDU.ASDUs[i]
		// Reduce before multiplication so even very large clock advances do not
		// overflow a counter operation.
		delta := (slots % uint64(p.cfg.CounterModulus)) * uint64(p.steps[i]) % uint64(p.cfg.CounterModulus)
		a.SampleCount = uint16((uint64(a.SampleCount) + delta) % uint64(p.cfg.CounterModulus))
	}
}
func (p *Publisher) skip(slots uint64) {
	if slots == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advanceLocked(slots)
	if math.MaxUint64-p.skipped < slots {
		p.skipped = math.MaxUint64
	} else {
		p.skipped += slots
	}
}
func (p *Publisher) publish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	encoded, err := EncodeFrame(p.frame)
	if err != nil {
		return err
	}
	if err = p.link.WriteFrame(ctx, encoded); err != nil {
		return err
	}
	p.advanceLocked(1)
	p.sent++
	return nil
}

// Publish sends one current frame and advances sample counters on success. It returns
// ErrRunning if Run or another Publish is active. The caller retains ownership of the link.
func (p *Publisher) Publish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer p.running.Store(false)
	return p.publish(ctx)
}

// Run uses absolute slots. Late timer wakes skip old slots and advance counters
// before the next send. Slow writes skip already elapsed future slots and wait
// for the next one: there is no burst of catch-up packets. SkippedSlots records
// these gaps. This is software scheduling, not a real-time timing guarantee.
func (p *Publisher) Run(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer p.running.Store(false)
	next := p.cfg.Clock.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := p.cfg.Clock.Now()
		if now.Before(next) {
			timer := p.cfg.Clock.NewTimer(next.Sub(now))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C():
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		now = p.cfg.Clock.Now()
		if late := now.Sub(next); late >= p.cfg.Interval {
			slots := uint64(late / p.cfg.Interval)
			p.skip(slots)
			next = next.Add(time.Duration(slots) * p.cfg.Interval)
		}
		if err := p.publish(ctx); err != nil {
			return err
		}
		next = next.Add(p.cfg.Interval)
		if now = p.cfg.Clock.Now(); !now.Before(next) {
			slots := uint64(now.Sub(next)/p.cfg.Interval) + 1
			p.skip(slots)
			next = next.Add(time.Duration(slots) * p.cfg.Interval)
		}
	}
}
