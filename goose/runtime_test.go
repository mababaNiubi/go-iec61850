// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
)

// runtimeClock advances only when a test requests it. created synchronizes timer
// registration, avoiding dependence on scheduler timing or real sleeps.
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
		return append([]byte(nil), b...), nil
	}
}
func (l *runtimeLink) WriteFrame(ctx context.Context, b []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.closed:
		return io.ErrClosedPipe
	case l.writes <- append([]byte(nil), b...):
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
	p := codecSamplePDU()
	p.GoCBRef = "IED/LLN0$GO$cb"
	p.DataSet = "IED/LLN0$ds"
	p.GoID = "stream"
	p.ConfRev = 7
	p.TimeAllowedToLive = 100
	return Frame{Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 1}, Source: [6]byte{2, 0, 0, 0, 0, 1}, VLAN: &VLAN{Priority: 4, ID: 3000}, APPID: 0x1001, PDU: p}
}
func runtimeConfig(c Clock) SubscriptionConfig {
	f := runtimeFrame()
	source := f.Source
	vlan := f.VLAN.ID
	return SubscriptionConfig{Destination: f.Destination, Source: &source, APPID: f.APPID, GoCBRef: f.PDU.GoCBRef, DataSet: f.PDU.DataSet, GoID: f.PDU.GoID, ConfRev: f.PDU.ConfRev, VLANID: &vlan, Clock: c, Types: []data.TypeSpec{{Kind: data.KindBoolean}, {Kind: data.KindInteger, Width: 32}}}
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
	wire, err := EncodeFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	l.input <- wire
}

func TestRuntimePublisherBackoffAndDataChanges(t *testing.T) {
	clock := newRuntimeClock()
	link := newRuntimeLink()
	original := runtimeFrame()
	publisher, err := NewPublisher(link, PublisherConfig{Frame: original, MinTime: 4 * time.Millisecond, MaxTime: 16 * time.Millisecond, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- publisher.Run(ctx) }()
	first := runtimeReadWrite(t, link)
	timer := runtimeTimerCreated(t, clock)
	if first.PDU.StateNumber != 1 || first.PDU.SequenceNumber != 0 || first.PDU.TimeAllowedToLive != 8 || timer.delay != 4*time.Millisecond {
		t.Fatalf("initial frame %+v, timer %v", first.PDU, timer.delay)
	}
	expectedTime, _ := eventTime(clock.Now())
	if first.PDU.Timestamp != expectedTime {
		t.Fatal("initial timestamp did not use clock")
	}
	if err := publisher.Run(ctx); !errors.Is(err, ErrRunning) {
		t.Fatalf("second Run: %v", err)
	}
	if err := publisher.Publish(ctx); !errors.Is(err, ErrRunning) {
		t.Fatalf("Publish while Run: %v", err)
	}
	for i, delay := range []time.Duration{4 * time.Millisecond, 8 * time.Millisecond, 16 * time.Millisecond, 16 * time.Millisecond} {
		clock.Advance(delay)
		frame := runtimeReadWrite(t, link)
		timer = runtimeTimerCreated(t, clock)
		next := min(delay*2, 16*time.Millisecond)
		if frame.PDU.StateNumber != 1 || frame.PDU.SequenceNumber != uint32(i+1) || frame.PDU.Timestamp != first.PDU.Timestamp || frame.PDU.TimeAllowedToLive != uint32(next/time.Millisecond)*2 || timer.delay != next {
			t.Fatalf("retransmission %d: %+v delay=%v", i, frame.PDU, timer.delay)
		}
	}
	before := publisher.Snapshot()
	changed, err := publisher.Update(append([]data.Value(nil), before.PDU.Values...))
	if err != nil || changed {
		t.Fatalf("equal update changed state: %v %v", changed, err)
	}
	if after := publisher.Snapshot(); after.PDU.StateNumber != before.PDU.StateNumber || after.PDU.Timestamp != before.PDU.Timestamp {
		t.Fatal("equal update changed metadata")
	}
	clock.Advance(time.Millisecond)
	values := []data.Value{data.Boolean(false), data.Integer(10)}
	changed, err = publisher.Update(values)
	if err != nil || !changed {
		t.Fatalf("changed update: %v %v", changed, err)
	}
	values[0] = data.Boolean(true)
	event := runtimeReadWrite(t, link)
	timer = runtimeTimerCreated(t, clock)
	expectedTime, _ = eventTime(clock.Now())
	if event.PDU.StateNumber != 2 || event.PDU.SequenceNumber != 0 || event.PDU.Timestamp != expectedTime || event.PDU.Timestamp == first.PDU.Timestamp || event.PDU.TimeAllowedToLive != 8 || timer.delay != 4*time.Millisecond || !event.PDU.Values[0].Equal(data.Boolean(false)) {
		t.Fatalf("state change %+v timer=%v", event.PDU, timer.delay)
	}
	copy := publisher.Snapshot()
	copy.VLAN.ID = 1
	copy.PDU.Values[0] = data.Boolean(true)
	if publisher.Snapshot().VLAN.ID != 3000 || !publisher.Snapshot().PDU.Values[0].Equal(data.Boolean(false)) {
		t.Fatal("snapshot mutated publisher")
	}
	cancel()
	if err := runtimeResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run cancellation: %v", err)
	}
	select {
	case <-link.closed:
		t.Fatal("Publisher closed caller-owned link")
	default:
	}
}

func TestRuntimePublisherCountersAndValidation(t *testing.T) {
	clock := newRuntimeClock()
	link := newRuntimeLink()
	publisher, err := NewPublisher(link, PublisherConfig{Frame: runtimeFrame(), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	publisher.mu.Lock()
	publisher.frame.PDU.StateNumber = math.MaxUint32
	publisher.frame.PDU.SequenceNumber = math.MaxUint32
	publisher.mu.Unlock()
	if err := publisher.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	sent := runtimeReadWrite(t, link)
	if sent.PDU.SequenceNumber != math.MaxUint32 || publisher.Snapshot().PDU.SequenceNumber != 1 {
		t.Fatal("sequence did not wrap past reserved zero")
	}
	clock.Advance(time.Second)
	if changed, err := publisher.Update([]data.Value{data.Boolean(false), data.Integer(-6)}); err != nil || !changed {
		t.Fatal(err)
	}
	if snapshot := publisher.Snapshot(); snapshot.PDU.StateNumber != 1 || snapshot.PDU.SequenceNumber != 0 {
		t.Fatalf("state wrap %+v", snapshot.PDU)
	}
	for _, values := range [][]data.Value{{data.Boolean(true)}, {data.Integer(1), data.Integer(2)}, {data.Boolean(true), data.Array(data.Integer(2))}} {
		before := publisher.Snapshot()
		if _, err := publisher.Update(values); err == nil {
			t.Fatal("accepted changed dataset layout")
		}
		if after := publisher.Snapshot(); !equalPDUs(before.PDU, after.PDU) {
			t.Fatal("invalid update changed publisher state")
		}
	}
	typed, err := NewPublisher(newRuntimeLink(), PublisherConfig{Frame: runtimeFrame(), Clock: clock, Types: runtimeConfig(clock).Types})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := typed.Update([]data.Value{data.Boolean(true), data.Integer(1 << 40)}); err == nil {
		t.Fatal("accepted out-of-range declared type")
	}
}

func TestRuntimeSubscriberJointFiltersAndPolicies(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Frame)
		want   error
	}{
		{"destination", func(f *Frame) { f.Destination[5]++ }, ErrMismatch},
		{"source", func(f *Frame) { f.Source[5]++ }, ErrMismatch},
		{"appid", func(f *Frame) { f.APPID++ }, ErrMismatch},
		{"gocb", func(f *Frame) { f.PDU.GoCBRef += "x" }, ErrMismatch},
		{"dataset", func(f *Frame) { f.PDU.DataSet += "x" }, ErrMismatch},
		{"goid", func(f *Frame) { f.PDU.GoID += "x" }, ErrMismatch},
		{"vlan", func(f *Frame) { f.VLAN.ID++ }, ErrMismatch},
		{"untagged", func(f *Frame) { f.VLAN = nil }, ErrMismatch},
		{"revision", func(f *Frame) { f.PDU.ConfRev++ }, ErrConfiguration},
		{"simulation", func(f *Frame) { f.PDU.Simulation = true }, ErrConfiguration},
		{"commission", func(f *Frame) { f.PDU.NeedsCommission = true }, ErrConfiguration},
		{"reserved1", func(f *Frame) { f.Reserved1 = 1 }, ErrConfiguration},
		{"reserved2", func(f *Frame) { f.Reserved2 = 1 }, ErrConfiguration},
		{"declared-width", func(f *Frame) { f.PDU.Values[1] = data.Integer(1 << 40) }, nil},
		{"declared-kind", func(f *Frame) { f.PDU.Values[1] = data.Unsigned(3) }, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clock := newRuntimeClock()
			s := runtimeSubscriber(t, runtimeConfig(clock))
			baseline := runtimeFrame()
			if _, err := s.Process(baseline); err != nil {
				t.Fatal(err)
			}
			old := s.deadline
			clock.Advance(time.Millisecond)
			bad := baseline.Clone()
			bad.PDU.StateNumber++
			test.mutate(&bad)
			_, err := s.Process(bad)
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("policy result %v, want %v", err, test.want)
			}
			got, valid := s.Snapshot()
			if !valid || !equalPDUs(got.PDU, baseline.PDU) || s.deadline != old || s.Stats().Accepted != 1 {
				t.Fatal("rejected frame mutated accepted state or TAL")
			}
		})
	}
	clock := newRuntimeClock()
	cfg := runtimeConfig(clock)
	cfg.AllowSimulation = true
	cfg.AllowNeedsCommission = true
	cfg.ConfRev = 0
	s := runtimeSubscriber(t, cfg)
	f := runtimeFrame()
	f.PDU.Simulation = true
	f.PDU.NeedsCommission = true
	if _, err := s.Process(f); err != nil {
		t.Fatal(err)
	}
	f.PDU.StateNumber++
	f.PDU.ConfRev++
	if _, err := s.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("initial revision was not pinned: %v", err)
	}
}

func TestRuntimeSubscriberSequenceAndExpiry(t *testing.T) {
	clock := newRuntimeClock()
	s := runtimeSubscriber(t, runtimeConfig(clock))
	base := runtimeFrame()
	first, err := s.Process(base)
	if err != nil || first.Kind != StateChanged {
		t.Fatalf("first: %+v %v", first, err)
	}
	clock.Advance(40 * time.Millisecond)
	if _, err = s.Process(base); !errors.Is(err, ErrStale) {
		t.Fatalf("duplicate: %v", err)
	}
	old := base.Clone()
	old.PDU.StateNumber = math.MaxUint32
	if _, err = s.Process(old); !errors.Is(err, ErrStale) {
		t.Fatalf("old state: %v", err)
	}
	conflict := base.Clone()
	conflict.PDU.SequenceNumber = 1
	conflict.PDU.Values[0] = data.Boolean(false)
	if _, err = s.Process(conflict); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("same-state changed data: %v", err)
	}
	conflict = base.Clone()
	conflict.PDU.SequenceNumber = 1
	conflict.PDU.Timestamp[6]++
	if _, err = s.Process(conflict); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("same-state changed timestamp: %v", err)
	}
	clock.Advance(60 * time.Millisecond)
	if _, valid := s.Snapshot(); valid {
		t.Fatal("snapshot stayed valid at exact TAL boundary")
	}
	expired, ok := s.CheckTimeout()
	if !ok || expired.Kind != Expired || expired.ValidUntil != first.ValidUntil {
		t.Fatalf("expiry %+v %v", expired, ok)
	}
	if _, ok = s.CheckTimeout(); ok {
		t.Fatal("expiry emitted twice")
	}
	retrans := base.Clone()
	retrans.PDU.SequenceNumber = 1
	recovered, err := s.Process(retrans)
	if err != nil || recovered.Kind != Recovered {
		t.Fatalf("recovery %+v %v", recovered, err)
	}
	for _, restartAllowed := range []bool{false, true} {
		cfg := runtimeConfig(clock)
		cfg.AllowRestartAfterTimeout = restartAllowed
		sub := runtimeSubscriber(t, cfg)
		last := base.Clone()
		last.PDU.StateNumber = 20
		if _, err := sub.Process(last); err != nil {
			t.Fatal(err)
		}
		clock.Advance(100 * time.Millisecond)
		restart := base.Clone()
		restart.PDU.StateNumber = 1
		restart.PDU.SequenceNumber = 0
		e, err := sub.Process(restart)
		if restartAllowed {
			if err != nil || e.Kind != Recovered {
				t.Fatalf("restart recovery: %+v %v", e, err)
			}
		} else if !errors.Is(err, ErrStale) {
			t.Fatalf("restart accepted without opt-in: %v", err)
		}
	}
	wrap := runtimeSubscriber(t, runtimeConfig(clock))
	f := base.Clone()
	f.PDU.StateNumber = math.MaxUint32
	f.PDU.SequenceNumber = math.MaxUint32
	if _, err := wrap.Process(f); err != nil {
		t.Fatal(err)
	}
	f.PDU.SequenceNumber = 1
	if e, err := wrap.Process(f); err != nil || e.Kind != Retransmission {
		t.Fatalf("sequence wrap: %+v %v", e, err)
	}
	f.PDU.StateNumber = 1
	f.PDU.SequenceNumber = 0
	if e, err := wrap.Process(f); err != nil || e.Kind != StateChanged {
		t.Fatalf("state wrap: %+v %v", e, err)
	}
}

func TestRuntimeSubscriberSnapshotsAndLayout(t *testing.T) {
	clock := newRuntimeClock()
	cfg := runtimeConfig(clock)
	source := *cfg.Source
	vlan := *cfg.VLANID
	s := runtimeSubscriber(t, cfg)
	cfg.Source[5]++
	*cfg.VLANID = 1
	cfg.Types[1].Width = 1
	f := runtimeFrame()
	f.Source = source
	f.VLAN.ID = vlan
	e, err := s.Process(f)
	if err != nil {
		t.Fatal(err)
	}
	f.PDU.Values[0] = data.Boolean(false)
	f.VLAN.ID = 1
	e.Frame.PDU.Values[0] = data.Boolean(false)
	e.Frame.VLAN.ID = 1
	copy, valid := s.Snapshot()
	if !valid || copy.VLAN.ID != 3000 || !copy.PDU.Values[0].Equal(data.Boolean(true)) {
		t.Fatal("input or event mutated accepted snapshot")
	}
	copy.VLAN.ID = 0
	copy.PDU.Values[0] = data.Boolean(false)
	again, _ := s.Snapshot()
	if again.VLAN.ID != 3000 || !again.PDU.Values[0].Equal(data.Boolean(true)) {
		t.Fatal("snapshot aliased subscriber")
	}
	cfg = runtimeConfig(clock)
	cfg.Types = nil
	sub := runtimeSubscriber(t, cfg)
	f = runtimeFrame()
	if _, err := sub.Process(f); err != nil {
		t.Fatal(err)
	}
	f.PDU.StateNumber++
	f.PDU.Values[0] = data.Integer(1)
	if _, err := sub.Process(f); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("untyped layout not pinned: %v", err)
	}
	cfg = runtimeConfig(clock)
	cfg.VLANID = nil
	cfg.RequireUntagged = true
	sub = runtimeSubscriber(t, cfg)
	f = runtimeFrame()
	if _, err := sub.Process(f); !errors.Is(err, ErrMismatch) {
		t.Fatalf("tagged frame passed untagged filter: %v", err)
	}
	f.VLAN = nil
	if _, err := sub.Process(f); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSubscriberRunExpiryRecoveryAndCancellation(t *testing.T) {
	clock := newRuntimeClock()
	s := runtimeSubscriber(t, runtimeConfig(clock))
	link := newRuntimeLink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, link) }()
	base := runtimeFrame()
	runtimeSend(t, link, base)
	if e := runtimeEvent(t, s); e.Kind != StateChanged {
		t.Fatalf("first event %+v", e)
	}
	runtimeTimerCreated(t, clock)
	if err := s.Run(ctx, link); !errors.Is(err, ErrRunning) {
		t.Fatalf("concurrent subscriber Run: %v", err)
	}
	// At expiry, deliver the next retransmission concurrently with the timer.
	// Both expiry and recovery must be visible whichever select case wins.
	clock.Advance(100 * time.Millisecond)
	base.PDU.SequenceNumber = 1
	runtimeSend(t, link, base)
	if e := runtimeEvent(t, s); e.Kind != Expired {
		t.Fatalf("lost expiry before recovery: %+v", e)
	}
	if e := runtimeEvent(t, s); e.Kind != Recovered {
		t.Fatalf("recovery event %+v", e)
	}
	cancel()
	if err := runtimeResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
	select {
	case <-link.readStopped:
	case <-time.After(3 * time.Second):
		t.Fatal("reader goroutine was not canceled")
	}
	select {
	case <-link.closed:
		t.Fatal("Subscriber closed caller-owned link")
	default:
	}
}

func TestRuntimeSubscriberRunRejectsUnrelatedAndOverflowsExplicitly(t *testing.T) {
	clock := newRuntimeClock()
	cfg := runtimeConfig(clock)
	cfg.EventBuffer = 1
	s := runtimeSubscriber(t, cfg)
	link := newRuntimeLink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, link) }()
	base := runtimeFrame()
	wrong, _ := EncodeFrame(base)
	wrong[12], wrong[13] = 8, 0 // remove VLAN protocol so the packet is unrelated Ethernet
	link.input <- wrong
	link.input <- []byte{1, 2, 3}
	filtered := base.Clone()
	filtered.APPID++
	runtimeSend(t, link, filtered)
	runtimeSend(t, link, base)
	// Timer registration follows queuing the first accepted event, so the
	// queue is full before the second accepted frame is delivered.
	runtimeTimerCreated(t, clock)
	base.PDU.SequenceNumber++
	runtimeSend(t, link, base)
	if err := runtimeResult(t, done); !errors.Is(err, ErrEventOverflow) {
		t.Fatalf("overflow was not explicit: %v", err)
	}
	stats := s.Stats()
	if stats.Accepted != 2 || stats.Filtered != 2 || stats.Rejected != 1 {
		t.Fatalf("filter/rejection counters %+v", stats)
	}
	e := runtimeEvent(t, s)
	if e.Kind != StateChanged {
		t.Fatalf("queued first event %+v", e)
	}
}

type runtimeFailLink struct {
	*runtimeLink
	err error
}

func (l runtimeFailLink) WriteFrame(context.Context, []byte) error { return l.err }
func TestRuntimePublisherSendFailureDoesNotAdvanceCounters(t *testing.T) {
	errSent := errors.New("injected link failure")
	p, err := NewPublisher(runtimeFailLink{newRuntimeLink(), errSent}, PublisherConfig{Frame: runtimeFrame(), Clock: newRuntimeClock()})
	if err != nil {
		t.Fatal(err)
	}
	before := p.Snapshot()
	if err = p.Publish(context.Background()); !errors.Is(err, errSent) {
		t.Fatalf("link error %v", err)
	}
	after := p.Snapshot()
	if p.Sent() != 0 || after.PDU.StateNumber != before.PDU.StateNumber || after.PDU.SequenceNumber != before.PDU.SequenceNumber || !bytes.Equal(after.PDU.Timestamp[:], before.PDU.Timestamp[:]) {
		t.Fatal("failed send consumed publisher sequence")
	}
}
