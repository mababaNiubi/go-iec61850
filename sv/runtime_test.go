// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Timer registration is observable so runtime tests advance a virtual clock
// only after a waiter exists, without depending on sleeps or wall-clock speed.
type runtimeClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*runtimeTimer]time.Time
	created chan *runtimeTimer
}
type runtimeTimer struct {
	clock  *runtimeClock
	c      chan time.Time
	delay  time.Duration
	active bool
}

func newRuntimeClock() *runtimeClock {
	return &runtimeClock{now: time.Unix(1700000000, 0).UTC(), timers: make(map[*runtimeTimer]time.Time), created: make(chan *runtimeTimer, 256)}
}
func (c *runtimeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *runtimeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	timer := &runtimeTimer{clock: c, c: make(chan time.Time, 1), delay: d, active: d > 0}
	if d > 0 {
		c.timers[timer] = c.now.Add(d)
	} else {
		timer.c <- c.now
	}
	c.mu.Unlock()
	c.created <- timer
	return timer
}
func (t *runtimeTimer) C() <-chan time.Time { return t.c }
func (t *runtimeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	active := t.active
	t.active = false
	delete(t.clock.timers, t)
	return active
}
func (c *runtimeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var fired []*runtimeTimer
	for timer, deadline := range c.timers {
		if !now.Before(deadline) {
			timer.active = false
			delete(c.timers, timer)
			fired = append(fired, timer)
		}
	}
	c.mu.Unlock()
	for _, timer := range fired {
		timer.c <- now
	}
}
func runtimeTimerCreated(t *testing.T, c *runtimeClock) *runtimeTimer {
	t.Helper()
	select {
	case timer := <-c.created:
		return timer
	case <-time.After(3 * time.Second):
		t.Fatal("timer was not registered")
		return nil
	}
}

type runtimeLink struct {
	input       chan []byte
	writes      chan []byte
	readStopped chan struct{}
	readOnce    sync.Once
	closed      chan struct{}
	closeOnce   sync.Once
	writeHook   func()
}

func newRuntimeLink() *runtimeLink {
	return &runtimeLink{input: make(chan []byte, 64), writes: make(chan []byte, 64), readStopped: make(chan struct{}), closed: make(chan struct{})}
}
func (l *runtimeLink) ReadFrame(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		l.readOnce.Do(func() { close(l.readStopped) })
		return nil, ctx.Err()
	case <-l.closed:
		return nil, io.EOF
	case b := <-l.input:
		return bytes.Clone(b), nil
	}
}
func (l *runtimeLink) WriteFrame(ctx context.Context, b []byte) error {
	select {
	case <-l.closed:
		return io.ErrClosedPipe
	default:
	}
	if l.writeHook != nil {
		l.writeHook()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.closed:
		return io.ErrClosedPipe
	case l.writes <- bytes.Clone(b):
		return nil
	}
}
func (l *runtimeLink) Close() error { l.closeOnce.Do(func() { close(l.closed) }); return nil }
func runtimeReadWrite(t *testing.T, l *runtimeLink) Frame {
	t.Helper()
	select {
	case b := <-l.writes:
		f, err := DecodeFrame(b)
		if err != nil {
			t.Fatal(err)
		}
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("publisher did not write a frame")
		return Frame{}
	}
}
func runtimeResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not stop")
		return nil
	}
}
func runtimeEvent(t *testing.T, s *Subscriber) Event {
	t.Helper()
	select {
	case e := <-s.Events():
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not emit an event")
		return Event{}
	}
}
func runtimeFrame() Frame {
	return Frame{Destination: [6]byte{1, 0x0c, 0xcd, 4, 0, 1}, Source: [6]byte{2, 0, 0, 0, 0, 1}, VLAN: &VLAN{Priority: 4, ID: 3000}, APPID: 0x4001, PDU: PDU{ASDUs: []ASDU{{SVID: "sampleSV1", DataSet: "IED/LLN0$ds", SampleCount: 7, ConfRev: 3, SampleSync: 2, Data: []byte{0, 0, 0, 1, 0, 0, 0, 0}}}}}
}
func runtimeConfig(c Clock) SubscriptionConfig {
	f := runtimeFrame()
	source := f.Source
	vlan := f.VLAN.ID
	return SubscriptionConfig{Destination: f.Destination, Source: &source, APPID: f.APPID, SVID: f.PDU.ASDUs[0].SVID, DataSet: f.PDU.ASDUs[0].DataSet, ConfRev: 3, VLANID: &vlan, DataLength: 8, Timeout: 100 * time.Millisecond, Clock: c}
}
func runtimeSubscriber(t *testing.T, cfg SubscriptionConfig) *Subscriber {
	t.Helper()
	s, err := NewSubscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func runtimeSend(t *testing.T, l *runtimeLink, f Frame) {
	t.Helper()
	b, err := EncodeFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	l.input <- b
}
func runtimeProcess(t *testing.T, s *Subscriber, f Frame) []Event {
	t.Helper()
	events, err := s.Process(f)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRuntimePublisherScheduleSkipsLateSamples(t *testing.T) {
	clock, link := newRuntimeClock(), newRuntimeLink()
	original := runtimeFrame()
	p, err := NewPublisher(link, PublisherConfig{Frame: original, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	original.PDU.ASDUs[0].Data[0] = 99
	original.VLAN.ID = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	first := runtimeReadWrite(t, link)
	timer := runtimeTimerCreated(t, clock)
	if first.PDU.ASDUs[0].SampleCount != 7 || first.PDU.ASDUs[0].Data[0] != 0 || first.VLAN.ID != 3000 || timer.delay != time.Millisecond {
		t.Fatalf("initial frame=%+v timer=%v", first, timer.delay)
	}
	if err = p.Publish(ctx); !errors.Is(err, ErrRunning) {
		t.Fatalf("concurrent Publish: %v", err)
	}
	clock.Advance(time.Millisecond)
	if got := runtimeReadWrite(t, link).PDU.ASDUs[0].SampleCount; got != 8 {
		t.Fatalf("next count %d", got)
	}
	runtimeTimerCreated(t, clock)
	clock.Advance(5 * time.Millisecond)
	if got := runtimeReadWrite(t, link).PDU.ASDUs[0].SampleCount; got != 13 {
		t.Fatalf("late count %d", got)
	}
	timer = runtimeTimerCreated(t, clock)
	if p.Sent() != 3 || p.SkippedSlots() != 4 || timer.delay != time.Millisecond {
		t.Fatalf("sent=%d skipped=%d delay=%v", p.Sent(), p.SkippedSlots(), timer.delay)
	}
	select {
	case <-link.writes:
		t.Fatal("late wake emitted a catch-up burst")
	default:
	}
	cancel()
	if err = runtimeResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRuntimePublisherSlowSendCountersAndCopies(t *testing.T) {
	clock, link := newRuntimeClock(), newRuntimeLink()
	link.writeHook = func() { clock.Advance(5 * time.Millisecond) }
	f := runtimeFrame()
	f.PDU.ASDUs = append(f.PDU.ASDUs, f.PDU.ASDUs[0].Clone())
	f.PDU.ASDUs[0].SampleCount = 3998
	f.PDU.ASDUs[1].SampleCount = 3999
	p, err := NewPublisher(link, PublisherConfig{Frame: f, Clock: clock, CounterModulus: 4000})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	first := runtimeReadWrite(t, link)
	timer := runtimeTimerCreated(t, clock)
	if first.PDU.ASDUs[0].SampleCount != 3998 || first.PDU.ASDUs[1].SampleCount != 3999 || timer.delay != time.Millisecond {
		t.Fatalf("first samples %+v delay %v", first.PDU.ASDUs, timer.delay)
	}
	snap := p.Snapshot()
	if snap.PDU.ASDUs[0].SampleCount != 10 || snap.PDU.ASDUs[1].SampleCount != 11 || p.SkippedSlots() != 5 {
		t.Fatalf("after slow write: %+v, skipped=%d", snap.PDU.ASDUs, p.SkippedSlots())
	}
	snap.PDU.ASDUs[0].Data[0] = 88
	snap.VLAN.ID = 5
	raw := []byte{9, 8, 7, 6, 5, 4, 3, 2}
	if err = p.UpdateData(0, raw); err != nil {
		t.Fatal(err)
	}
	raw[0] = 0
	if got := p.Snapshot(); got.PDU.ASDUs[0].Data[0] != 9 || got.VLAN.ID != 3000 {
		t.Fatalf("mutable snapshot/update alias: %+v", got)
	}
	if err = p.UpdateData(0, []byte{1}); err == nil {
		t.Fatal("changed sample length accepted")
	}
	if err = p.UpdateData(5, raw); err == nil {
		t.Fatal("invalid ASDU index accepted")
	}
	cancel()
	if err = runtimeResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRuntimePublisherCounterProgressionAndFailedWrite(t *testing.T) {
	link := newRuntimeLink()
	f := runtimeFrame()
	f.PDU.ASDUs[0].SampleCount = 65535
	a := f.PDU.ASDUs[0].Clone()
	a.SVID = "other"
	a.SampleCount = 100
	f.PDU.ASDUs = append(f.PDU.ASDUs, a)
	p, err := NewPublisher(link, PublisherConfig{Frame: f})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtimeReadWrite(t, link)
	snap := p.Snapshot()
	if snap.PDU.ASDUs[0].SampleCount != 0 || snap.PDU.ASDUs[1].SampleCount != 101 {
		t.Fatalf("independent stream progression %+v", snap.PDU.ASDUs)
	}
	link.Close()
	if err = p.Publish(context.Background()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed write: %v", err)
	}
	if p.Sent() != 1 || !reflect.DeepEqual(p.Snapshot(), snap) {
		t.Fatal("failed send advanced counters")
	}
	for _, m := range []uint32{1, 65537} {
		if _, err = NewPublisher(link, PublisherConfig{Frame: f, CounterModulus: m}); err == nil {
			t.Fatalf("invalid modulus %d accepted", m)
		}
	}
	f.PDU.ASDUs = append(f.PDU.ASDUs, f.PDU.ASDUs[0].Clone())
	if _, err = NewPublisher(link, PublisherConfig{Frame: f}); err == nil {
		t.Fatal("nonconsecutive same-stream ASDUs accepted")
	}
}

func TestRuntimeSubscriberCountersAndAtomicConfiguration(t *testing.T) {
	clock := newRuntimeClock()
	s := runtimeSubscriber(t, runtimeConfig(clock))
	f := runtimeFrame()
	f.PDU.ASDUs[0].SampleCount = 65534
	for _, n := range []uint16{65534, 65535, 0} {
		f.PDU.ASDUs[0].SampleCount = n
		events := runtimeProcess(t, s, f)
		if events[0].Kind != SampleReceived {
			t.Fatalf("count %d: %+v", n, events)
		}
	}
	f.PDU.ASDUs[0].SampleCount = 3
	events := runtimeProcess(t, s, f)
	if events[0].Kind != SampleGap || events[0].MissingSamples != 2 {
		t.Fatalf("gap %+v", events)
	}
	deadline := events[0].ValidUntil
	clock.Advance(20 * time.Millisecond)
	if _, err := s.Process(f); !errors.Is(err, ErrStale) {
		t.Fatalf("duplicate: %v", err)
	}
	f.PDU.ASDUs[0].SampleCount = 2
	if _, err := s.Process(f); !errors.Is(err, ErrStale) {
		t.Fatalf("older count: %v", err)
	}
	f.PDU.ASDUs[0].SampleCount = 4
	bad := f.Clone()
	bad.PDU.ASDUs = append(bad.PDU.ASDUs, bad.PDU.ASDUs[0].Clone())
	bad.PDU.ASDUs[1].SampleCount = 5
	bad.PDU.ASDUs[1].ConfRev = 4
	if _, err := s.Process(bad); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("bad later configuration: %v", err)
	}
	if snap, valid := s.Snapshot(); !valid || snap.SampleCount != 3 {
		t.Fatalf("partial commit: %+v valid=%v", snap, valid)
	}
	clock.Advance(80 * time.Millisecond)
	if _, valid := s.Snapshot(); valid {
		t.Fatal("duplicate refreshed timeout")
	}
	e, expired := s.CheckTimeout()
	if !expired || e.Kind != Expired || !e.ValidUntil.Equal(deadline) {
		t.Fatalf("timeout %+v %v", e, expired)
	}
	if _, expired = s.CheckTimeout(); expired {
		t.Fatal("timeout emitted twice")
	}
	stats := s.Stats()
	if stats.Accepted != 4 || stats.Frames != 4 || stats.Stale != 2 || stats.Rejected != 3 || stats.GapEvents != 1 || stats.MissingSamples != 2 || stats.Expired != 1 {
		t.Fatalf("stats %+v", stats)
	}
}

func TestRuntimeSubscriberLocalTimeoutRecoveryAndOwnership(t *testing.T) {
	clock := newRuntimeClock()
	cfg := runtimeConfig(clock)
	cfg.AllowRestartAfterTimeout = true
	cfg.ConfRev = 0
	cfg.DataLength = 0
	s := runtimeSubscriber(t, cfg)
	f := runtimeFrame()
	f.PDU.ASDUs[0].SampleCount = 100
	rate, mode := uint16(4000), uint16(1)
	refresh, gm := [8]byte{1}, [8]byte{2}
	f.PDU.ASDUs[0].SampleRate = &rate
	f.PDU.ASDUs[0].SampleMode = &mode
	f.PDU.ASDUs[0].RefreshTime = &refresh
	f.PDU.ASDUs[0].GMIdentity = &gm
	events := runtimeProcess(t, s, f)
	events[0].ASDU.Data[0] = 66
	events[0].Frame.VLAN.ID = 1
	f.PDU.ASDUs[0].Data[0] = 77
	rate = 123
	refresh[0] = 88
	snap, _ := s.Snapshot()
	if snap.Data[0] != 0 || *snap.SampleRate != 4000 || snap.RefreshTime[0] != 1 {
		t.Fatalf("input/event alias: %+v", snap)
	}
	snap.Data[0] = 44
	clock.Advance(100 * time.Millisecond)
	if _, valid := s.Snapshot(); valid {
		t.Fatal("Snapshot ignored deadline")
	}
	if _, ok := s.CheckTimeout(); !ok {
		t.Fatal("Snapshot consumed timeout event")
	}
	f = runtimeFrame()
	f.PDU.ASDUs[0].SampleCount = 0
	rate = 4000
	f.PDU.ASDUs[0].SampleRate = &rate
	f.PDU.ASDUs[0].SampleMode = &mode
	events = runtimeProcess(t, s, f)
	if events[0].Kind != Recovered || events[0].MissingSamples != 0 {
		t.Fatalf("restart %+v", events)
	}
	f.PDU.ASDUs[0].SampleCount = 1
	f.PDU.ASDUs[0].ConfRev = 4
	if _, err := s.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("unpinned revision: %v", err)
	}
	f.PDU.ASDUs[0].ConfRev = 3
	*f.PDU.ASDUs[0].SampleRate = 3999
	if _, err := s.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("changed sample rate: %v", err)
	}
	if stats := s.Stats(); stats.Expired != 1 || stats.Recovered != 1 {
		t.Fatalf("recovery stats %+v", stats)
	}
}

func TestRuntimeSubscriberDuplicateZeroCannotRestart(t *testing.T) {
	clock := newRuntimeClock()
	cfg := runtimeConfig(clock)
	cfg.AllowRestartAfterTimeout = true
	s := runtimeSubscriber(t, cfg)
	f := runtimeFrame()
	f.PDU.ASDUs[0].SampleCount = 0
	runtimeProcess(t, s, f)
	clock.Advance(cfg.Timeout)
	if _, err := s.Process(f); !errors.Is(err, ErrStale) {
		t.Fatalf("expired duplicate zero: %v", err)
	}
	if _, valid := s.Snapshot(); valid {
		t.Fatal("expired duplicate zero refreshed sample validity")
	}
	if event, expired := s.CheckTimeout(); !expired || event.Kind != Expired {
		t.Fatalf("duplicate consumed timeout: %+v %v", event, expired)
	}
	if stats := s.Stats(); stats.Accepted != 1 || stats.Recovered != 0 || stats.Stale != 1 {
		t.Fatalf("duplicate restart changed state: %+v", stats)
	}
}

func TestRuntimeSubscriberFiltersAndPolicies(t *testing.T) {
	clock := newRuntimeClock()
	s := runtimeSubscriber(t, runtimeConfig(clock))
	f := runtimeFrame()
	for _, mutate := range []func(*Frame){func(f *Frame) { f.APPID++ }, func(f *Frame) { f.Source[5]++ }, func(f *Frame) { f.VLAN.ID++ }, func(f *Frame) { f.PDU.ASDUs[0].SVID = "another" }} {
		bad := f.Clone()
		mutate(&bad)
		if _, err := s.Process(bad); !errors.Is(err, ErrMismatch) {
			t.Fatalf("filter: %v", err)
		}
	}
	f.PDU.ASDUs[0].SampleSync = 0
	if _, err := s.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("unsynchronized sample: %v", err)
	}
	cfg := runtimeConfig(clock)
	cfg.AllowUnsynchronized = true
	cfg.CounterModulus = 4000
	s = runtimeSubscriber(t, cfg)
	f.PDU.ASDUs[0].SampleCount = 3999
	runtimeProcess(t, s, f)
	f.PDU.ASDUs[0].SampleCount = 0
	runtimeProcess(t, s, f)
	f.PDU.ASDUs[0].SampleCount = 4000
	if _, err := s.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("count outside modulus: %v", err)
	}
	f.PDU.ASDUs[0].SampleCount = 1
	f.Reserved1 = 1
	if _, err := s.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("security extension: %v", err)
	}
	f.Reserved1 = 0
	f.PDU.Security = []byte{}
	if _, err := s.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("explicit empty security extension: %v", err)
	}
	if _, err := NewPublisher(newRuntimeLink(), PublisherConfig{Frame: f}); err == nil {
		t.Fatal("publisher accepted an explicit empty security extension")
	}
	cfg = runtimeConfig(clock)
	cfg.Source = nil
	cfg.VLANID = nil
	cfg.RequireUntagged = true
	s = runtimeSubscriber(t, cfg)
	if _, err := s.Process(runtimeFrame()); !errors.Is(err, ErrMismatch) {
		t.Fatalf("tagged frame passed untagged filter: %v", err)
	}
	f = runtimeFrame()
	f.VLAN = nil
	f.Source[0] = 1
	if _, err := s.Process(f); !errors.Is(err, ErrMalformed) {
		t.Fatalf("multicast source accepted: %v", err)
	}
}

func TestRuntimeSubscriberRunTimeoutCancellation(t *testing.T) {
	clock, link := newRuntimeClock(), newRuntimeLink()
	s := runtimeSubscriber(t, runtimeConfig(clock))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, link) }()
	f := runtimeFrame()
	runtimeSend(t, link, f)
	if e := runtimeEvent(t, s); e.Kind != SampleReceived {
		t.Fatalf("initial event %+v", e)
	}
	runtimeTimerCreated(t, clock)
	clock.Advance(100 * time.Millisecond)
	if e := runtimeEvent(t, s); e.Kind != Expired {
		t.Fatalf("timeout event %+v", e)
	}
	f.PDU.ASDUs[0].SampleCount++
	runtimeSend(t, link, f)
	if e := runtimeEvent(t, s); e.Kind != Recovered {
		t.Fatalf("recovery event %+v", e)
	}
	runtimeTimerCreated(t, clock)
	cancel()
	if err := runtimeResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-link.readStopped:
	default:
		t.Fatal("Run returned before reader stopped")
	}
}

type runtimeEOFLink struct {
	mu          sync.Mutex
	frames      [][]byte
	lastWithEOF bool
}

func (l *runtimeEOFLink) ReadFrame(ctx context.Context) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(l.frames) == 0 {
		return nil, io.EOF
	}
	b := l.frames[0]
	l.frames = l.frames[1:]
	if len(l.frames) == 0 && l.lastWithEOF {
		return bytes.Clone(b), io.EOF
	}
	return bytes.Clone(b), nil
}
func (*runtimeEOFLink) WriteFrame(context.Context, []byte) error { return nil }
func (*runtimeEOFLink) Close() error                             { return nil }
func TestRuntimeSubscriberOrderedEOFAndBoundedEvents(t *testing.T) {
	for _, lastWithEOF := range []bool{false, true} {
		t.Run(map[bool]string{false: "separateEOF", true: "dataWithEOF"}[lastWithEOF], func(t *testing.T) {
			f := runtimeFrame()
			a := f.PDU.ASDUs[0].Clone()
			a.SampleCount++
			f.PDU.ASDUs = append(f.PDU.ASDUs, a)
			wire, err := EncodeFrame(f)
			if err != nil {
				t.Fatal(err)
			}
			unrelated := bytes.Clone(wire)
			unrelated[16] = 0x08
			unrelated[17] = 0x00
			link := &runtimeEOFLink{frames: [][]byte{{1, 2, 3}, unrelated, wire}, lastWithEOF: lastWithEOF}
			s := runtimeSubscriber(t, runtimeConfig(newRuntimeClock()))
			if err = s.Run(context.Background(), link); !errors.Is(err, io.EOF) {
				t.Fatalf("Run: %v", err)
			}
			for _, n := range []uint16{7, 8} {
				if e := runtimeEvent(t, s); e.ASDU.SampleCount != n {
					t.Fatalf("EOF overtook sample %d: %+v", n, e)
				}
			}
			if stats := s.Stats(); stats.Accepted != 2 || stats.Rejected != 1 || stats.Filtered != 1 {
				t.Fatalf("read stats %+v", stats)
			}
			cfg := runtimeConfig(newRuntimeClock())
			cfg.EventBuffer = 1
			s = runtimeSubscriber(t, cfg)
			link = &runtimeEOFLink{frames: [][]byte{wire}}
			if err = s.Run(context.Background(), link); !errors.Is(err, ErrEventOverflow) {
				t.Fatalf("overflow: %v", err)
			}
			if stats := s.Stats(); stats.Accepted != 2 {
				t.Fatalf("frame did not commit atomically before queue overflow: %+v", stats)
			}
			if e := runtimeEvent(t, s); e.ASDU.SampleCount != 7 {
				t.Fatalf("first bounded event %+v", e)
			}
		})
	}
}

func TestRuntimeSubscriberMultipleASDUsAreAtomic(t *testing.T) {
	s := runtimeSubscriber(t, runtimeConfig(newRuntimeClock()))
	f := runtimeFrame()
	runtimeProcess(t, s, f)
	f.PDU.ASDUs[0].SampleCount = 8
	a := f.PDU.ASDUs[0].Clone()
	a.SampleCount = 9
	f.PDU.ASDUs = append(f.PDU.ASDUs, a)
	events := runtimeProcess(t, s, f)
	if len(events) != 2 || events[0].ASDU.SampleCount != 8 || events[1].ASDU.SampleCount != 9 {
		t.Fatalf("multiple events %+v", events)
	}
	events[0].ASDU.Data[0] = 99
	events[0].Frame.PDU.ASDUs[1].Data[0] = 88
	if events[1].ASDU.Data[0] != 0 || events[1].Frame.PDU.ASDUs[1].Data[0] != 0 {
		t.Fatal("events share mutable sample data")
	}
	f.PDU.ASDUs[0].SampleCount = 10
	f.PDU.ASDUs[1].SampleCount = 10
	if _, err := s.Process(f); !errors.Is(err, ErrStale) {
		t.Fatalf("duplicate later ASDU: %v", err)
	}
	if snap, _ := s.Snapshot(); snap.SampleCount != 9 {
		t.Fatalf("bad second ASDU partly committed: %+v", snap)
	}
}
