// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
)

var (
	ErrMismatch      = errors.New("goose: frame does not match subscription")
	ErrStale         = errors.New("goose: duplicate or old sequence")
	ErrConfiguration = errors.New("goose: configuration revision or policy mismatch")
	ErrStateConflict = errors.New("goose: data or event timestamp changed without a new state")
	ErrEventOverflow = errors.New("goose: event queue overflow")
)

type EventKind uint8

const (
	StateChanged EventKind = iota + 1
	Retransmission
	Expired
	Recovered
)

func (k EventKind) String() string {
	switch k {
	case StateChanged:
		return "state-changed"
	case Retransmission:
		return "retransmission"
	case Expired:
		return "expired"
	case Recovered:
		return "recovered"
	}
	return "unknown"
}

type Event struct {
	Kind       EventKind
	Frame      Frame
	ReceivedAt time.Time
	ValidUntil time.Time
}

type SubscriptionConfig struct {
	Destination            [6]byte
	Source                 *[6]byte
	APPID                  uint16
	GoCBRef, DataSet, GoID string
	ConfRev                uint32  // zero accepts the initial revision, then pins it
	VLANID                 *uint16 // nil accepts tagged and untagged frames
	RequireUntagged        bool
	Types                  []data.TypeSpec
	AllowSimulation        bool
	AllowNeedsCommission   bool
	// Restart recovery is opt-in and requires an expired subscription, sqNum0,
	// and a changed stNum or event timestamp. Identical initial-frame replays
	// do not recover the subscription; this policy does not authenticate freshness.
	AllowRestartAfterTimeout bool
	Clock                    Clock
	EventBuffer              int
}
type SubscriberStats struct{ Accepted, Rejected, Filtered, Stale, Expired uint64 }

// Subscriber validates a complete frame before changing data or its lifetime.
// Duplicates/old frames do not refresh TAL. A revision change requires a new
// subscription; initial revision pinning applies when Config.ConfRev is zero.
type Subscriber struct {
	cfg             SubscriptionConfig
	mu              sync.Mutex
	last            Frame
	hasState, valid bool
	deadline        time.Time
	stats           SubscriberStats
	lastError       error
	events          chan Event
	running         atomic.Bool
}

func subscriptionReference(s string, optional bool) bool {
	if s == "" {
		return optional
	}
	if len(s) > MaxReferenceSize {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
func multicastDestination(mac [6]byte) bool {
	return mac[0]&1 != 0 && mac != ([6]byte{255, 255, 255, 255, 255, 255})
}

// NewSubscriber 创建过滤和状态检查对象。VLANID=nil 接受有/无标签，
// ConfRev=0 固定首次有效修订；这些零值不表示忽略所有后续配置变化。
func NewSubscriber(cfg SubscriptionConfig) (*Subscriber, error) {
	if !subscriptionReference(cfg.GoCBRef, false) || !subscriptionReference(cfg.DataSet, false) || !subscriptionReference(cfg.GoID, true) || !multicastDestination(cfg.Destination) {
		return nil, fmt.Errorf("goose: invalid subscription filter")
	}
	if cfg.VLANID != nil {
		v := *cfg.VLANID
		if v > 4094 || cfg.RequireUntagged {
			return nil, fmt.Errorf("goose: invalid VLAN filter")
		}
		cfg.VLANID = &v
	}
	if cfg.Source != nil {
		v := *cfg.Source
		if v == ([6]byte{}) || v[0]&1 != 0 {
			return nil, fmt.Errorf("goose: source filter must be a nonzero unicast MAC")
		}
		cfg.Source = &v
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.EventBuffer == 0 {
		cfg.EventBuffer = 64
	}
	if cfg.EventBuffer < 1 || cfg.EventBuffer > 65536 {
		return nil, fmt.Errorf("goose: invalid event queue size")
	}
	var err error
	cfg.Types, err = cloneTypes(cfg.Types)
	if err != nil {
		return nil, err
	}
	return &Subscriber{cfg: cfg, events: make(chan Event, cfg.EventBuffer)}, nil
}
func newer(a, b uint32) bool { return a != b && uint32(a-b) < 1<<31 }
func (s *Subscriber) matches(f Frame) bool {
	return f.Destination == s.cfg.Destination && (s.cfg.Source == nil || f.Source == *s.cfg.Source) && f.APPID == s.cfg.APPID && f.PDU.GoCBRef == s.cfg.GoCBRef && f.PDU.DataSet == s.cfg.DataSet && (s.cfg.GoID == "" || f.PDU.GoID == s.cfg.GoID) && (!s.cfg.RequireUntagged || f.VLAN == nil) && (s.cfg.VLANID == nil || (f.VLAN != nil && f.VLAN.ID == *s.cfg.VLANID))
}
func (s *Subscriber) rejectLocked(err error) (Event, error) {
	s.stats.Rejected++
	s.lastError = err
	if errors.Is(err, ErrStale) {
		s.stats.Stale++
	}
	return Event{}, err
}
func (s *Subscriber) expireLocked(now time.Time) (Event, bool) {
	if s.valid && !now.Before(s.deadline) {
		s.valid = false
		s.stats.Expired++
		return Event{Kind: Expired, Frame: s.last.Clone(), ReceivedAt: now, ValidUntil: s.deadline}, true
	}
	return Event{}, false
}

// Process is the deterministic state-machine entry point for decoded frames.
// Events returned here are not also queued on Events(); Run owns that queue.
// Rejected frames do not consume a pending CheckTimeout notification.
func (s *Subscriber) Process(f Frame) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matches(f) {
		s.stats.Filtered++
		return Event{}, ErrMismatch
	}
	if f.Source == ([6]byte{}) || f.Source[0]&1 != 0 {
		return s.rejectLocked(ErrMalformed)
	}
	if _, err := EncodeFrame(f); err != nil {
		return s.rejectLocked(err)
	}
	if f.Reserved1 != 0 || f.Reserved2 != 0 {
		return s.rejectLocked(ErrConfiguration)
	}
	if (!s.cfg.AllowSimulation && f.PDU.Simulation) || (!s.cfg.AllowNeedsCommission && f.PDU.NeedsCommission) {
		return s.rejectLocked(ErrConfiguration)
	}
	if (s.cfg.ConfRev != 0 && f.PDU.ConfRev != s.cfg.ConfRev) || (s.hasState && f.PDU.ConfRev != s.last.PDU.ConfRev) {
		return s.rejectLocked(ErrConfiguration)
	}
	if err := validateDataset(s.cfg.Types, f.PDU.Values); err != nil {
		return s.rejectLocked(err)
	}
	now := s.cfg.Clock.Now()
	expired := s.hasState && (!s.valid || !now.Before(s.deadline))
	newState := !s.hasState || newer(f.PDU.StateNumber, s.last.PDU.StateNumber)
	restartChanged := f.PDU.StateNumber != s.last.PDU.StateNumber || f.PDU.Timestamp != s.last.PDU.Timestamp
	if s.hasState && !newState && expired && s.cfg.AllowRestartAfterTimeout && f.PDU.SequenceNumber == 0 && restartChanged {
		newState = true
	}
	if s.hasState && !newState {
		if f.PDU.StateNumber != s.last.PDU.StateNumber || !newer(f.PDU.SequenceNumber, s.last.PDU.SequenceNumber) {
			return s.rejectLocked(ErrStale)
		}
		if !sameValues(f.PDU.Values, s.last.PDU.Values) || f.PDU.Timestamp != s.last.PDU.Timestamp {
			return s.rejectLocked(ErrStateConflict)
		}
	}
	if s.hasState && s.cfg.Types == nil {
		if len(f.PDU.Values) != len(s.last.PDU.Values) {
			return s.rejectLocked(ErrConfiguration)
		}
		for i, v := range f.PDU.Values {
			if !sameLayout(s.last.PDU.Values[i], v) {
				return s.rejectLocked(ErrConfiguration)
			}
		}
	}
	kind := Retransmission
	if newState {
		kind = StateChanged
	}
	if expired {
		kind = Recovered
	}
	if s.valid && expired {
		s.stats.Expired++
	}
	s.last = f.Clone()
	s.hasState = true
	s.valid = true
	s.deadline = now.Add(time.Duration(f.PDU.TimeAllowedToLive) * time.Millisecond)
	s.stats.Accepted++
	return Event{Kind: kind, Frame: f.Clone(), ReceivedAt: now, ValidUntil: s.deadline}, nil
}
func (s *Subscriber) CheckTimeout() (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expireLocked(s.cfg.Clock.Now())
}
func (s *Subscriber) Snapshot() (Frame, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last.Clone(), s.valid && s.cfg.Clock.Now().Before(s.deadline)
}
func (s *Subscriber) Stats() SubscriberStats { s.mu.Lock(); defer s.mu.Unlock(); return s.stats }
func (s *Subscriber) LastError() error       { s.mu.Lock(); defer s.mu.Unlock(); return s.lastError }
func (s *Subscriber) Events() <-chan Event   { return s.events }
func (s *Subscriber) emitEvent(ctx context.Context, e Event) error {
	select {
	case s.events <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrEventOverflow
	}
}

// Run reads frames, emits accepted updates and TAL expiry, and continues past
// unrelated/malformed packets. It cancels its reader before returning. Events
// stays open so the same Subscriber may be run again after cancellation.
func (s *Subscriber) Run(ctx context.Context, link FrameIO) error {
	if link == nil {
		return fmt.Errorf("goose: nil frame link")
	}
	if !s.running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer s.running.Store(false)
	readCtx, cancel := context.WithCancel(ctx)
	type readResult struct {
		frame []byte
		err   error
	}
	packets := make(chan readResult, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			b, err := link.ReadFrame(readCtx)
			if len(b) > 0 {
				select {
				case packets <- readResult{frame: b}:
				case <-readCtx.Done():
					return
				}
			}
			if err == nil && len(b) > 0 {
				continue
			}
			if err == nil {
				err = fmt.Errorf("goose: frame reader returned an empty frame")
			}
			select {
			case packets <- readResult{err: err}:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { cancel(); <-readerDone }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e, expired := s.CheckTimeout(); expired {
			if err := s.emitEvent(ctx, e); err != nil {
				return err
			}
		}
		s.mu.Lock()
		var timer Timer
		var timerC <-chan time.Time
		if s.valid {
			timer = s.cfg.Clock.NewTimer(max(0, s.deadline.Sub(s.cfg.Clock.Now())))
			timerC = timer.C()
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return ctx.Err()
		case <-timerC: // CheckTimeout emits at the top of the loop.
		case received := <-packets:
			if timer != nil {
				timer.Stop()
			}
			if received.err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return received.err
			}
			f, err := DecodeFrame(received.frame)
			if err != nil {
				s.mu.Lock()
				if errors.Is(err, ErrNotGOOSE) {
					s.stats.Filtered++
				} else {
					s.stats.Rejected++
					s.lastError = err
				}
				s.mu.Unlock()
				continue
			}
			if expired, ok := s.CheckTimeout(); ok {
				if err = s.emitEvent(ctx, expired); err != nil {
					return err
				}
			}
			e, err := s.Process(f)
			if err == nil {
				if err = s.emitEvent(ctx, e); err != nil {
					return err
				}
			}
		}
	}
}
