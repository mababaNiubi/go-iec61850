// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrMismatch      = errors.New("sv: frame/stream does not match subscription")
	ErrStale         = errors.New("sv: duplicate or old sample count")
	ErrConfiguration = errors.New("sv: configuration or synchronization policy mismatch")
	ErrEventOverflow = errors.New("sv: event queue overflow")
)

type EventKind uint8

const (
	SampleReceived EventKind = iota + 1
	SampleGap
	Expired
	Recovered
)

func (k EventKind) String() string {
	switch k {
	case SampleReceived:
		return "sample"
	case SampleGap:
		return "sample-gap"
	case Expired:
		return "expired"
	case Recovered:
		return "recovered"
	}
	return "unknown"
}

type Event struct {
	Kind                   EventKind
	ASDU                   ASDU
	Frame                  Frame
	ReceivedAt, ValidUntil time.Time
	MissingSamples         uint32
}

type SubscriptionConfig struct {
	Destination     [6]byte
	Source          *[6]byte
	APPID           uint16
	SVID, DataSet   string
	ConfRev         uint32 // zero pins the first accepted configuration revision
	VLANID          *uint16
	RequireUntagged bool
	DataLength      int // zero pins the first accepted raw data length
	CounterModulus  uint32
	// Timeout is a local receive timeout, default one second; SV has no TAL.
	Timeout                  time.Duration
	AllowUnsynchronized      bool
	RequireGlobalSync        bool // require smpSynch=2, in addition to the ordinary checks
	AllowRestartAfterTimeout bool // requires expired state and sample count zero
	Clock                    Clock
	EventBuffer              int
}
type SubscriberStats struct{ Accepted, Frames, Rejected, Filtered, Stale, GapEvents, MissingSamples, Expired, Recovered uint64 }
type Subscriber struct {
	cfg             SubscriptionConfig
	mu              sync.Mutex
	last            ASDU
	lastFrame       Frame
	hasState, valid bool
	deadline        time.Time
	stats           SubscriberStats
	lastError       error
	events          chan Event
	running         atomic.Bool
}

func referenceOK(s string, optional bool) bool {
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

// NewSubscriber 创建采样流过滤与计数检查对象。Timeout 是本地策略，不是线上 TAL；
// 默认拒绝 SampleSync=0，需要由应用显式设置 AllowUnsynchronized 接受软件采样。
func NewSubscriber(cfg SubscriptionConfig) (*Subscriber, error) {
	if !multicastDestination(cfg.Destination) || !referenceOK(cfg.SVID, false) || !referenceOK(cfg.DataSet, true) {
		return nil, fmt.Errorf("sv: invalid subscription filter")
	}
	if cfg.Source != nil {
		v := *cfg.Source
		if !unicastSource(v) {
			return nil, fmt.Errorf("sv: source filter must be a unicast MAC")
		}
		cfg.Source = &v
	}
	if cfg.VLANID != nil {
		v := *cfg.VLANID
		if v > 4094 || cfg.RequireUntagged {
			return nil, fmt.Errorf("sv: invalid VLAN filter")
		}
		cfg.VLANID = &v
	}
	if cfg.DataLength < 0 || cfg.DataLength > 1492 {
		return nil, fmt.Errorf("sv: invalid data length")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = time.Second
	}
	if cfg.Timeout < 0 || cfg.Timeout > 24*time.Hour {
		return nil, fmt.Errorf("sv: invalid local receive timeout")
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.EventBuffer == 0 {
		cfg.EventBuffer = 256
	}
	if cfg.EventBuffer < 1 || cfg.EventBuffer > 65536 {
		return nil, fmt.Errorf("sv: invalid event buffer")
	}
	var err error
	cfg.CounterModulus, err = modulus(cfg.CounterModulus)
	if err != nil {
		return nil, err
	}
	return &Subscriber{cfg: cfg, events: make(chan Event, cfg.EventBuffer)}, nil
}

func (s *Subscriber) frameMatches(f Frame) bool {
	return f.Destination == s.cfg.Destination && f.APPID == s.cfg.APPID && (s.cfg.Source == nil || f.Source == *s.cfg.Source) && (!s.cfg.RequireUntagged || f.VLAN == nil) && (s.cfg.VLANID == nil || (f.VLAN != nil && f.VLAN.ID == *s.cfg.VLANID))
}
func (s *Subscriber) asduMatches(a ASDU) bool {
	return a.SVID == s.cfg.SVID && (s.cfg.DataSet == "" || a.DataSet == s.cfg.DataSet)
}
func (s *Subscriber) rejectLocked(err error) ([]Event, error) {
	s.stats.Rejected++
	s.lastError = err
	if errors.Is(err, ErrStale) {
		s.stats.Stale++
	}
	return nil, err
}
func sameOptional16(a, b *uint16) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func sameStreamConfiguration(a, b ASDU) bool {
	return a.SVID == b.SVID && a.DataSet == b.DataSet && a.ConfRev == b.ConfRev && len(a.Data) == len(b.Data) && sameOptional16(a.SampleRate, b.SampleRate) && sameOptional16(a.SampleMode, b.SampleMode)
}

// Process validates every matching ASDU before committing any of them. A bad
// later sample cannot partly advance the counter or refresh the receive timer.
// RefreshTime, GMIdentity and SampleSync are dynamic metadata; rate/mode,
// revision, dataset name and raw data length remain pinned to the stream.
// Returned events are not also queued; Run owns Events().
func (s *Subscriber) Process(f Frame) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.frameMatches(f) {
		s.stats.Filtered++
		return nil, ErrMismatch
	}
	if !unicastSource(f.Source) {
		return s.rejectLocked(ErrMalformed)
	}
	if _, err := EncodeFrame(f); err != nil {
		return s.rejectLocked(err)
	}
	if f.Reserved1 != 0 || f.Reserved2 != 0 || f.PDU.Security != nil {
		return s.rejectLocked(ErrConfiguration)
	}
	var matching []ASDU
	for _, a := range f.PDU.ASDUs {
		if s.asduMatches(a) {
			matching = append(matching, a)
		}
	}
	if len(matching) == 0 {
		s.stats.Filtered++
		return nil, ErrMismatch
	}
	now := s.cfg.Clock.Now()
	expired := s.hasState && (!s.valid || !now.Before(s.deadline))
	previous := s.last
	has := s.hasState
	results := make([]Event, 0, len(matching))
	var gaps, missing uint64
	for i, a := range matching {
		if uint32(a.SampleCount) >= s.cfg.CounterModulus || (!s.cfg.AllowUnsynchronized && a.SampleSync == 0) || (s.cfg.RequireGlobalSync && a.SampleSync != 2) || (s.cfg.ConfRev != 0 && a.ConfRev != s.cfg.ConfRev) || (s.cfg.DataLength != 0 && len(a.Data) != s.cfg.DataLength) {
			return s.rejectLocked(ErrConfiguration)
		}
		if has && !sameStreamConfiguration(a, previous) {
			return s.rejectLocked(ErrConfiguration)
		}
		kind := SampleReceived
		lost := uint32(0)
		if has {
			restart := i == 0 && expired && s.cfg.AllowRestartAfterTimeout && a.SampleCount == 0 && previous.SampleCount != 0
			if !restart {
				delta := (uint32(a.SampleCount) + s.cfg.CounterModulus - uint32(previous.SampleCount)) % s.cfg.CounterModulus
				if delta == 0 || delta > s.cfg.CounterModulus/2 {
					return s.rejectLocked(ErrStale)
				}
				lost = delta - 1
				if lost > 0 {
					kind = SampleGap
					gaps++
					missing += uint64(lost)
				}
			}
		}
		if i == 0 && expired {
			kind = Recovered
		}
		results = append(results, Event{Kind: kind, ASDU: a.Clone(), Frame: f.Clone(), ReceivedAt: now, ValidUntil: now.Add(s.cfg.Timeout), MissingSamples: lost})
		previous = a
		has = true
	}
	if s.valid && expired {
		s.stats.Expired++
	}
	if expired {
		s.stats.Recovered++
	}
	s.last = previous.Clone()
	s.lastFrame = f.Clone()
	s.hasState = true
	s.valid = true
	s.deadline = now.Add(s.cfg.Timeout)
	s.stats.Accepted += uint64(len(results))
	s.stats.Frames++
	s.stats.GapEvents += gaps
	s.stats.MissingSamples += missing
	return results, nil
}
func (s *Subscriber) expireLocked(now time.Time) (Event, bool) {
	if s.valid && !now.Before(s.deadline) {
		s.valid = false
		s.stats.Expired++
		return Event{Kind: Expired, ASDU: s.last.Clone(), Frame: s.lastFrame.Clone(), ReceivedAt: now, ValidUntil: s.deadline}, true
	}
	return Event{}, false
}
func (s *Subscriber) CheckTimeout() (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expireLocked(s.cfg.Clock.Now())
}
func (s *Subscriber) Snapshot() (ASDU, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last.Clone(), s.valid && s.cfg.Clock.Now().Before(s.deadline)
}
func (s *Subscriber) Stats() SubscriberStats { s.mu.Lock(); defer s.mu.Unlock(); return s.stats }
func (s *Subscriber) LastError() error       { s.mu.Lock(); defer s.mu.Unlock(); return s.lastError }
func (s *Subscriber) Events() <-chan Event   { return s.events }
func (s *Subscriber) emit(ctx context.Context, e Event) error {
	select {
	case s.events <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrEventOverflow
	}
}

// Run keeps reading past malformed or unrelated packets, reports timeout events
// independently of received samples, and cancels/joins the reader before return.
// Events stays open to allow a stopped Subscriber to be run again.
func (s *Subscriber) Run(ctx context.Context, link FrameIO) error {
	if link == nil {
		return fmt.Errorf("sv: nil frame link")
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
	done := make(chan struct{})
	go func() {
		defer close(done)
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
				err = fmt.Errorf("sv: frame reader returned an empty frame")
			}
			select {
			case packets <- readResult{err: err}:
			case <-readCtx.Done():
			}
			return
		}
	}()
	defer func() { cancel(); <-done }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e, expired := s.CheckTimeout(); expired {
			if err := s.emit(ctx, e); err != nil {
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
		case <-timerC:
		case item := <-packets:
			if timer != nil {
				timer.Stop()
			}
			if item.err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return item.err
			}
			f, err := DecodeFrame(item.frame)
			if err != nil {
				s.mu.Lock()
				if errors.Is(err, ErrNotSV) {
					s.stats.Filtered++
				} else {
					s.stats.Rejected++
					s.lastError = err
				}
				s.mu.Unlock()
				continue
			}
			if e, expired := s.CheckTimeout(); expired {
				if err = s.emit(ctx, e); err != nil {
					return err
				}
			}
			events, err := s.Process(f)
			if err == nil {
				for _, e := range events {
					if err = s.emit(ctx, e); err != nil {
						return err
					}
				}
			}
		}
	}
}
