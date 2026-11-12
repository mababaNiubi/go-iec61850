// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"fmt"
	"sync"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Origin identifies the command issuer. Category is 0..8 and ID is at most 64 bytes; it is not
// an authentication credential.
type Origin struct {
	Category int
	ID       []byte
}

// Config configures the MMS association and control origin. Zero values use the library
// defaults.
type Config struct {
	MMS mms.Config
	// A zero Origin selects category 2 and ID "go-iec61850".
	Origin Origin
}

// Client adds controls, URCB/BRCB subscriptions and online discovery to an MMS association. Confirmed
// MMS services remain available through its methods and MMS(). The wrapper owns
// its lifetime and is the ONLY reader of the underlying raw Reports channel.
// Applications receive decoded reports from ReportSubscription.Events instead.
type Client struct {
	raw                       *mms.Client
	origin                    Origin
	mu                        sync.Mutex
	subscriptions             map[mms.ObjectName]*ReportSubscription
	pending                   *termination
	controlToken, reportToken chan struct{}
	settingToken              chan struct{}
	next                      uint8 // protected by controlToken, including the selection phase
	dispatched                chan struct{}
}

// Dial establishes an owned IEC 61850 client at host:port. ctx bounds connection setup; each
// later operation has its own context. Defer Close on success.
func Dial(ctx context.Context, address string, cfg Config) (*Client, error) {
	if err := validateOrigin(cfg.Origin); err != nil {
		return nil, err
	}
	raw, err := mms.Dial(ctx, address, cfg.MMS)
	if err != nil {
		return nil, err
	}
	c, err := Wrap(raw, cfg.Origin)
	if err != nil {
		raw.Close()
	}
	return c, err
}

// Wrap takes ownership on success. Do not concurrently consume raw.Reports or
// wrap the same association twice. An error leaves ownership with the caller.
func Wrap(raw *mms.Client, origin Origin) (*Client, error) {
	if raw == nil {
		return nil, fmt.Errorf("ied: nil MMS client")
	}
	if err := validateOrigin(origin); err != nil {
		return nil, err
	}
	if origin.ID == nil && origin.Category == 0 {
		origin = Origin{Category: 2, ID: []byte("go-iec61850")}
	}
	origin.ID = append([]byte(nil), origin.ID...)
	c := &Client{raw: raw, origin: origin, subscriptions: make(map[mms.ObjectName]*ReportSubscription), controlToken: make(chan struct{}, 1), reportToken: make(chan struct{}, 1), dispatched: make(chan struct{})}
	c.settingToken = make(chan struct{}, 1)
	go c.dispatch()
	return c, nil
}
func validateOrigin(origin Origin) error {
	if origin.Category < 0 || origin.Category > 8 || len(origin.ID) > 64 {
		return fmt.Errorf("ied: invalid origin category or ID (maximum 64 octets)")
	}
	return nil
}

// Close releases owned transports and workers. Defer it immediately after successful
// construction.
func (c *Client) Close() error { err := c.raw.Close(); <-c.dispatched; return err }

// MMS provides advanced confirmed services. Its Reports channel is owned by
// this wrapper; consuming that channel externally would steal active reports.
func (c *Client) MMS() *mms.Client { return c.raw }

// Done closes when the MMS association shuts down. Call Close to also wait for
// the report dispatcher; Err returns the association failure, if any.
func (c *Client) Done() <-chan struct{} { return c.raw.Done() }

// Err returns the terminal failure, or nil while no failure has been recorded. It is safe to
// call concurrently.
func (c *Client) Err() error { return c.raw.Err() }

// Read obtains one remote MMS value. Convert it with the typed Value accessors and handle a
// mismatched kind as an error.
func (c *Client) Read(ctx context.Context, name mms.ObjectName) (mms.Value, error) {
	return c.raw.Read(ctx, name)
}

// ReadMultiple reads ordered accesses in one confirmed request. Check both the request error
// and each ReadResult.Error.
func (c *Client) ReadMultiple(ctx context.Context, accesses ...mms.VariableAccess) ([]mms.ReadResult, error) {
	return c.raw.ReadMultiple(ctx, accesses...)
}

// Write sends one remote assignment. A transport timeout after submission does not prove that
// the IED rejected the write.
func (c *Client) Write(ctx context.Context, name mms.ObjectName, value mms.Value) error {
	return c.raw.Write(ctx, name, value)
}

// GetVariableAccessAttributes reads the remote declared type. ctx bounds this request; it does
// not read the current process value.
func (c *Client) GetVariableAccessAttributes(ctx context.Context, name mms.ObjectName) (mms.TypeSpec, error) {
	return c.raw.GetVariableAccessAttributes(ctx, name)
}

// GetNamedListAttributes reads ordered dataset members, preserving alternate access for arrays
// and structures.
func (c *Client) GetNamedListAttributes(ctx context.Context, name mms.ObjectName) (mms.NamedList, error) {
	return c.raw.GetNamedListAttributes(ctx, name)
}

// Conclude requests graceful association release. Close remains available for unconditional
// local cleanup.
func (c *Client) Conclude(ctx context.Context) error {
	err := c.raw.Conclude(ctx)
	// A busy-association error leaves MMS open; do not wait for a dispatcher
	// that is correctly still running. A successful/closed conclude can drain.
	if c.raw.Err() != nil {
		<-c.dispatched
	}
	return err
}

func (c *Client) acquire(ctx context.Context, token chan struct{}) error {
	select {
	case token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-token
			return err
		}
		if err := c.Err(); err != nil {
			<-token
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.Done():
		return c.Err()
	}
}

func (c *Client) dispatch() {
	defer close(c.dispatched)
	for raw := range c.raw.Reports() {
		c.mu.Lock()
		pending := c.pending
		subs := make([]*ReportSubscription, 0, len(c.subscriptions))
		for _, s := range c.subscriptions {
			subs = append(subs, s)
		}
		c.mu.Unlock()
		// Controls must never wait behind an application's report consumer.
		if pending != nil {
			if ok, err := pending.match(raw); ok {
				select {
				case pending.result <- err:
				default:
				}
			}
		}
		for _, s := range subs {
			s.accept(raw)
		}
	}
	c.mu.Lock()
	subs := c.subscriptions
	c.subscriptions = make(map[mms.ObjectName]*ReportSubscription)
	c.mu.Unlock()
	for _, s := range subs {
		s.finish(c.Err())
	}
}
