// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import "time"

// Clock separates monotonic lifetime/retransmission scheduling from the event
// UTC timestamp carried on the wire. Applications normally use the default.
type Clock interface {
	// Now returns the current time, retaining monotonic information for deadlines.
	Now() time.Time
	// NewTimer creates a timer owned and stopped by the requesting worker.
	NewTimer(time.Duration) Timer
}

// Timer is an owned scheduling timer. C delivers its wakeup; Stop cancels a
// pending wakeup and reports whether it was active.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}
type systemClock struct{}

func (systemClock) Now() time.Time                 { return time.Now() }
func (systemClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }
