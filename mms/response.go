// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"fmt"
	"sync"
)

type responseContextKey struct{}
type responseActions struct {
	mu        sync.Mutex
	closed    bool
	callbacks []func(context.Context, error)
}

// AfterResponse registers work for the current server Store request. It runs
// after the confirmed response has been sent, outside server locks. sendErr is
// non-nil if transmission failed; an actuator must not run in that case. The
// callback context belongs to the association, not the expired request timer.
// The server joins callbacks on shutdown, so they must respect cancellation.
// Local Store calls have no response lifecycle and return an error here.
func AfterResponse(ctx context.Context, callback func(context.Context, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a, _ := ctx.Value(responseContextKey{}).(*responseActions)
	if a == nil || callback == nil {
		return fmt.Errorf("mms: no confirmed response lifecycle")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	a.callbacks = append(a.callbacks, callback)
	return nil
}

func (a *responseActions) run(ctx context.Context, sendErr error) {
	a.mu.Lock()
	a.closed = true
	callbacks := a.callbacks
	a.callbacks = nil
	a.mu.Unlock()
	for _, callback := range callbacks {
		callback(ctx, sendErr)
	}
}
