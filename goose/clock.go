// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import "time"

// Clock separates monotonic lifetime/retransmission scheduling from the event
// UTC timestamp carried on the wire. Applications normally use the default.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}
type systemClock struct{}

func (systemClock) Now() time.Time                 { return time.Now() }
func (systemClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }
