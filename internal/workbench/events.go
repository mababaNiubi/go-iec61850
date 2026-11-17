// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

type Event struct {
	ID      uint64    `json:"id"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Device  string    `json:"device,omitempty"`
	Message string    `json:"message"`
	// Control carries the structured view of kind "control.record" events for
	// the control history table. Other kinds leave it empty.
	Control *ControlRecord `json:"control,omitempty"`
}

// ControlRecord is the auditable view of one control action, local or remote.
type ControlRecord struct {
	Device    string `json:"device"` // simulator id, or connection id for remote IEDs
	Reference string `json:"reference"`
	Action    string `json:"action"` // select, operate, cancel, feedback, denied, interlock
	Value     string `json:"value,omitempty"`
	Origin    string `json:"origin,omitempty"`
	CtlNum    int    `json:"ctlNum,omitempty"`
	Result    string `json:"result,omitempty"` // empty means accepted
	Via       string `json:"via,omitempty"`    // sim loop or remote connection
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
	j.publish(Event{Time: time.Now().UTC(), Kind: kind, Device: device, Message: message})
}

// addControl records a control action with both a readable journal line and
// the structured payload the history table renders.
func (j *journal) addControl(rec ControlRecord) {
	parts := []string{rec.Action, rec.Reference}
	if rec.Value != "" {
		parts = append(parts, "← "+rec.Value)
	}
	if rec.Origin != "" {
		parts = append(parts, "origin="+rec.Origin)
	}
	if rec.CtlNum != 0 {
		parts = append(parts, "ctlNum="+strconv.Itoa(rec.CtlNum))
	}
	if rec.Result != "" {
		parts = append(parts, "· "+rec.Result)
	}
	j.publish(Event{Time: time.Now().UTC(), Kind: "control.record", Device: rec.Device, Message: strings.Join(parts, " "), Control: &rec})
}

func (j *journal) publish(e Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.next++
	e.ID = j.next
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

// controlHistory returns the structured control events after an ID, so the
// browser can resume without replaying everything.
func (j *journal) controlHistory(after uint64) []Event {
	out := []Event{}
	for _, e := range j.snapshot() {
		if e.Kind == "control.record" && e.ID > after {
			out = append(out, e)
		}
	}
	return out
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

// ControlHistory reports recorded control actions for the history table.
func (m *Manager) ControlHistory(after uint64) []Event { return m.log.controlHistory(after) }
