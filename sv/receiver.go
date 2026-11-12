// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type receiveRoute struct {
	sub      *Subscriber
	validate func(Frame) error
	notify   func([]Event, error)
}

// Receiver drains one NIC for many SV streams. Engineering validation runs
// before a subscriber can commit any ASDU. The callback receives one atomic
// packet result and must return promptly; business polling remains independent
// of the continuous receive loop. The caller owns the FrameIO lifetime.
type Receiver struct {
	// Configure before Run. Observe sees every successfully decoded packet.
	Observe         func(Frame, []byte)
	IgnoreReadError func(error) bool
	mu              sync.RWMutex
	routes          map[*Subscriber]receiveRoute
	processing      sync.Mutex
	running         atomic.Bool
}

// Add reserves sub for this receiver and registers validation and synchronous
// notification callbacks. Do not also use Subscriber.Run or Process on sub.
// The returned remove waits for current dispatch; call it outside callbacks.
func (r *Receiver) Add(sub *Subscriber, validate func(Frame) error, notify func([]Event, error)) (remove func(), err error) {
	if sub == nil || !sub.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("sv: subscriber already has a receiver")
	}
	r.mu.Lock()
	if len(r.routes) >= 256 {
		r.mu.Unlock()
		sub.running.Store(false)
		return nil, fmt.Errorf("sv: receiver stream limit reached")
	}
	if r.routes == nil {
		r.routes = make(map[*Subscriber]receiveRoute)
	}
	r.routes[sub] = receiveRoute{sub: sub, validate: validate, notify: notify}
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.processing.Lock()
			defer r.processing.Unlock()
			r.mu.Lock()
			delete(r.routes, sub)
			r.mu.Unlock()
			sub.running.Store(false)
		})
	}, nil
}

// Process dispatches one decoded frame synchronously to matching routes. Validation
// precedes state changes; callbacks may receive an error with no events. Poll subscribers
// separately for expiry.
func (r *Receiver) Process(frame Frame) {
	r.processing.Lock()
	defer r.processing.Unlock()
	r.mu.RLock()
	routes := make([]receiveRoute, 0, len(r.routes))
	for _, route := range r.routes {
		routes = append(routes, route)
	}
	r.mu.RUnlock()
	for _, route := range routes {
		if !route.sub.frameMatches(frame) {
			continue
		}
		matches := false
		for _, asdu := range frame.PDU.ASDUs {
			if route.sub.asduMatches(asdu) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if route.validate != nil {
			if err := route.validate(frame); err != nil {
				if route.notify != nil {
					route.notify(nil, err)
				}
				continue
			}
		}
		events, err := route.sub.Process(frame)
		if errors.Is(err, ErrMismatch) || errors.Is(err, ErrStale) {
			continue
		}
		if route.notify != nil {
			route.notify(events, err)
		}
	}
}

// Run reads and decodes frames until cancellation or a read error. It owns the read loop,
// not the link. Callbacks run synchronously; poll CheckTimeout separately when traffic
// stops.
func (r *Receiver) Run(ctx context.Context, link FrameIO) error {
	if link == nil || !r.running.CompareAndSwap(false, true) {
		return fmt.Errorf("sv: receiver already running or missing link")
	}
	defer r.running.Store(false)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := link.ReadFrame(ctx)
		if err != nil {
			if r.IgnoreReadError != nil && r.IgnoreReadError(err) {
				continue
			}
			return err
		}
		frame, err := DecodeFrame(raw)
		if err != nil {
			continue
		}
		if r.Observe != nil {
			r.Observe(frame, raw)
		}
		r.Process(frame)
	}
}
