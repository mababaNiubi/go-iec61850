// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type receiveRoute struct {
	sub    *Subscriber
	notify func(Event, error)
}

// Receiver dispatches one continuously drained Ethernet reader to many GOOSE
// state machines. Add may run concurrently with Run. Callbacks run on the
// receive goroutine, must return promptly, and receive validated snapshots.
// It neither owns nor closes the link; cancel ctx, wait for Run, then close it.
type Receiver struct {
	// Configure before Run. Observe sees every successfully decoded packet.
	Observe         func(Frame, []byte)
	IgnoreReadError func(error) bool
	mu              sync.RWMutex
	routes          map[*Subscriber]receiveRoute
	processing      sync.Mutex
	running         atomic.Bool
}

// Add reserves the subscriber for this receiver until remove is called.
// Do not also call Subscriber.Run or Process on the registered subscriber.
// The returned remove waits for current dispatch; call it outside callbacks.
func (r *Receiver) Add(sub *Subscriber, notify func(Event, error)) (remove func(), err error) {
	if sub == nil || !sub.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("goose: subscriber already has a receiver")
	}
	r.mu.Lock()
	if len(r.routes) >= 256 {
		r.mu.Unlock()
		sub.running.Store(false)
		return nil, fmt.Errorf("goose: receiver stream limit reached")
	}
	if r.routes == nil {
		r.routes = make(map[*Subscriber]receiveRoute)
	}
	r.routes[sub] = receiveRoute{sub: sub, notify: notify}
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

// Process routes an already decoded frame. Bad/stale frames cannot prevent
// another stream from receiving its independent valid state.
func (r *Receiver) Process(frame Frame) {
	r.processing.Lock()
	defer r.processing.Unlock()
	r.mu.RLock()
	routes := make([]receiveRoute, 0, len(r.routes))
	for _, route := range r.routes {
		if route.sub.cfg.GoCBRef == frame.PDU.GoCBRef {
			routes = append(routes, route)
		}
	}
	r.mu.RUnlock()
	for _, route := range routes {
		event, err := route.sub.Process(frame)
		if errors.Is(err, ErrMismatch) || errors.Is(err, ErrStale) {
			continue
		}
		if err != nil {
			event.Frame = frame.Clone()
		}
		if route.notify != nil {
			route.notify(event, err)
		}
	}
}
func (r *Receiver) Run(ctx context.Context, link FrameIO) error {
	if link == nil || !r.running.CompareAndSwap(false, true) {
		return fmt.Errorf("goose: receiver already running or missing link")
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
