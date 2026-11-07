// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"sync"
	"time"
)

type Event struct {
	ID      uint64    `json:"id"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Device  string    `json:"device,omitempty"`
	Message string    `json:"message"`
}

// The journal is bounded. A slow browser is disconnected instead of blocking
// the protocol goroutine. Its EventSource reconnects and reloads /api/state.
type journal struct {
	mu      sync.Mutex
	next    uint64
	events  []Event
	readers map[chan Event]struct{}
}

func (j *journal) add(kind, device, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.next++
	e := Event{j.next, time.Now().UTC(), kind, device, message}
	j.events = append(j.events, e)
	if len(j.events) > 500 {
		j.events = append([]Event(nil), j.events[len(j.events)-500:]...)
	}
	for ch := range j.readers {
		select {
		case ch <- e:
		default:
			delete(j.readers, ch)
			close(ch)
		}
	}
}
func (j *journal) snapshot() []Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Event{}, j.events...)
}
func (j *journal) subscribe() (chan Event, func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.readers == nil {
		j.readers = make(map[chan Event]struct{})
	}
	ch := make(chan Event, 64)
	j.readers[ch] = struct{}{}
	return ch, func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if _, ok := j.readers[ch]; ok {
			delete(j.readers, ch)
			close(ch)
		}
	}
}
