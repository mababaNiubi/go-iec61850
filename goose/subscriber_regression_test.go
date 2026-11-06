// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"errors"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
)

func TestRegressionExpiredDuplicateInitialFrameCannotRestart(t *testing.T) {
	clock := newRuntimeClock()
	cfg := runtimeConfig(clock)
	cfg.AllowRestartAfterTimeout = true
	s := runtimeSubscriber(t, cfg)
	initial := runtimeFrame()
	first, err := s.Process(initial)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Duration(initial.PDU.TimeAllowedToLive) * time.Millisecond)
	if _, err = s.Process(initial); !errors.Is(err, ErrStale) {
		t.Fatalf("identical sqNum0 frame recovered an expired subscription: %v", err)
	}
	if _, valid := s.Snapshot(); valid {
		t.Fatal("replayed initial frame refreshed TAL")
	}
	if stats := s.Stats(); stats.Accepted != 1 || stats.Stale != 1 || stats.Expired != 0 {
		t.Fatalf("rejected replay changed receive/expiry state: %+v", stats)
	}
	expired, ok := s.CheckTimeout()
	if !ok || expired.Kind != Expired || expired.ValidUntil != first.ValidUntil {
		t.Fatalf("rejected replay consumed expiry notification: %+v %v", expired, ok)
	}
	if _, ok = s.CheckTimeout(); ok {
		t.Fatal("expiry notification repeated")
	}
	// A newly timestamped initial event can still represent the same publisher
	// after a restart when the application explicitly enables that policy.
	restarted := initial.Clone()
	restarted.PDU.Timestamp[6]++
	if e, err := s.Process(restarted); err != nil || e.Kind != Recovered {
		t.Fatalf("opt-in restart with a new event timestamp: %+v %v", e, err)
	}
}

func TestRegressionExpiredInitialReplayAfterRetransmissionCannotRestart(t *testing.T) {
	clock := newRuntimeClock()
	cfg := runtimeConfig(clock)
	cfg.AllowRestartAfterTimeout = true
	s := runtimeSubscriber(t, cfg)
	initial := runtimeFrame()
	if _, err := s.Process(initial); err != nil {
		t.Fatal(err)
	}
	clock.Advance(20 * time.Millisecond)
	retransmission := initial.Clone()
	retransmission.PDU.SequenceNumber = 1
	last, err := s.Process(retransmission)
	if err != nil || last.Kind != Retransmission {
		t.Fatalf("first retransmission: %+v %v", last, err)
	}
	clock.Advance(time.Duration(initial.PDU.TimeAllowedToLive) * time.Millisecond)
	if _, err = s.Process(initial); !errors.Is(err, ErrStale) {
		t.Fatalf("initial frame replay after sqNum1 recovered subscription: %v", err)
	}
	if frame, valid := s.Snapshot(); valid || frame.PDU.SequenceNumber != 1 {
		t.Fatal("replayed initial frame replaced the last accepted event or TAL")
	}
	if stats := s.Stats(); stats.Accepted != 2 || stats.Stale != 1 || stats.Expired != 0 {
		t.Fatalf("replay changed receive/expiry state: %+v", stats)
	}
	if event, ok := s.CheckTimeout(); !ok || event.Kind != Expired || event.ValidUntil != last.ValidUntil {
		t.Fatalf("replay consumed retransmission timeout: %+v %v", event, ok)
	}
	if _, ok := s.CheckTimeout(); ok {
		t.Fatal("expiry notification repeated")
	}
	restarted := initial.Clone()
	restarted.PDU.Timestamp[6]++
	if event, err := s.Process(restarted); err != nil || event.Kind != Recovered {
		t.Fatalf("opt-in restart with a new event timestamp: %+v %v", event, err)
	}
}

func TestRegressionRejectedExpiredFramePreservesTimeout(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Frame)
		want   error
	}{
		{"stale", func(f *Frame) {}, ErrStale},
		{"state-conflict", func(f *Frame) {
			f.PDU.SequenceNumber++
			f.PDU.Values[0] = data.Boolean(false)
		}, ErrStateConflict},
		{"layout-conflict", func(f *Frame) {
			f.PDU.StateNumber++
			f.PDU.Values[0] = data.Integer(1)
		}, ErrConfiguration},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newRuntimeClock()
			cfg := runtimeConfig(clock)
			cfg.Types = nil
			s := runtimeSubscriber(t, cfg)
			initial := runtimeFrame()
			first, err := s.Process(initial)
			if err != nil {
				t.Fatal(err)
			}
			clock.Advance(time.Duration(initial.PDU.TimeAllowedToLive) * time.Millisecond)
			bad := initial.Clone()
			test.mutate(&bad)
			if _, err = s.Process(bad); !errors.Is(err, test.want) {
				t.Fatalf("rejection: %v, want %v", err, test.want)
			}
			if last, valid := s.Snapshot(); valid || !equalPDUs(last.PDU, initial.PDU) {
				t.Fatal("rejected frame changed expired state")
			}
			if stats := s.Stats(); stats.Expired != 0 || stats.Accepted != 1 {
				t.Fatalf("rejected frame consumed expiry: %+v", stats)
			}
			if e, ok := s.CheckTimeout(); !ok || e.Kind != Expired || e.ValidUntil != first.ValidUntil {
				t.Fatalf("expiry was lost: %+v %v", e, ok)
			}
			if _, ok := s.CheckTimeout(); ok {
				t.Fatal("expiry emitted more than once")
			}
		})
	}
}
