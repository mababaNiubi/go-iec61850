// SPDX-License-Identifier: GPL-3.0-or-later

// Package mms implements native Go MMS clients and servers over RFC 1006.
// Dial opens an association; defer Client.Close on success and supply a context
// to each confirmed request. Config zero values select bounded defaults.
//
// ObjectName identifies VMD-, domain- or association-scoped objects. Values
// alias data.Value, which is immutable and provides checked typed accessors.
// ReadMultiple preserves access order and per-member errors separately from
// the overall request error.
//
// NewServer serves a concurrency-safe Store. Model provides an in-memory Store
// with atomic SetValues acquisitions. Optional BatchReader implementations can
// provide coherent multi-member reads. Use package ied for IEC control, RCB
// reports, discovery and settings; raw InformationReport is an MMS service.
package mms

import (
	"fmt"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

// ISOConfig configures the underlying ISO association, including selectors.
type ISOConfig = iso.Config

// ACSEAuthMechanism identifies the association authentication presented by a
// peer. Only the ISO password mechanism (2.2.3.1) is implemented.
type ACSEAuthMechanism = iso.ACSEAuthMechanism

const (
	ACSEAuthNone     = iso.ACSEAuthNone
	ACSEAuthPassword = iso.ACSEAuthPassword
)

// Config contains local limits. Zero values select conservative defaults.
type Config struct {
	ISO                   ISOConfig
	MaxPDUSize            int           // bytes; zero selects 65000; range 128..16 MiB
	MaxOutstandingCalling int           // zero selects 5; range 1..1024
	MaxOutstandingCalled  int           // zero selects 5; range 1..1024
	NestingLevel          int           // zero selects 16; range 1..64
	RequestTimeout        time.Duration // per confirmed request; zero selects 5s; negative is invalid
	// Password sends ACSE password authentication when dialing. Empty sends
	// none. This is association authentication, separate from TLS.
	Password []byte
	// KeepAlive enables TCP keepalive probes with this period on client and
	// server sockets. Zero disables it.
	KeepAlive time.Duration
	// MaxConnections bounds concurrent server associations; zero is
	// unlimited. Further associations are closed during accept.
	MaxConnections int
	// OnStateChanged reports client association state transitions
	// (connecting, connected, closed). It runs on the calling or receive
	// goroutine and must return promptly.
	OnStateChanged func(State)
	// OnConnectionLost reports an unexpected client association end. A normal
	// Close does not call it.
	OnConnectionLost func(error)
	// OnConnection reports server association acceptance and closure.
	OnConnection func(remote string, connected bool)
	// Authenticator verifies an incoming association (server role). When set,
	// every association is checked, including one that presents no
	// credentials. When nil, associations without credentials are accepted
	// and any association that presents credentials is refused, so
	// credentials are never silently ignored.
	Authenticator func(mechanism ACSEAuthMechanism, value []byte) error
}

func (c Config) normalized() (Config, error) {
	if len(c.Password) > 0 {
		c.ISO.Password = append([]byte(nil), c.Password...)
	}
	if c.Authenticator != nil {
		c.ISO.Authenticator = c.Authenticator
	}
	if c.MaxConnections < 0 {
		return c, fmt.Errorf("mms: MaxConnections must not be negative")
	}
	if c.KeepAlive < 0 {
		return c, fmt.Errorf("mms: KeepAlive must not be negative")
	}
	if c.MaxPDUSize == 0 {
		c.MaxPDUSize = 65000
	}
	if c.MaxOutstandingCalling == 0 {
		c.MaxOutstandingCalling = 5
	}
	if c.MaxOutstandingCalled == 0 {
		c.MaxOutstandingCalled = 5
	}
	if c.NestingLevel == 0 {
		c.NestingLevel = 16
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 5 * time.Second
	}
	if c.MaxPDUSize < 128 || c.MaxPDUSize > 16<<20 || c.MaxOutstandingCalling < 1 || c.MaxOutstandingCalling > 1024 || c.MaxOutstandingCalled < 1 || c.MaxOutstandingCalled > 1024 || c.NestingLevel < 1 || c.NestingLevel > 64 || c.RequestTimeout < 0 {
		return c, fmt.Errorf("mms: invalid limits")
	}
	c.ISO.MaxPDUSize = c.MaxPDUSize
	return c, nil
}

// Parameters is an immutable snapshot of association negotiation.
type Parameters struct {
	MaxPDUSize            int
	MaxOutstandingCalling int
	MaxOutstandingCalled  int
	NestingLevel          int
	ParameterCBB          [2]byte
	Services              [11]byte
}

// Supports reports the peer's service capability bit (ISO 9506 service number).
func (p Parameters) Supports(service int) bool {
	return service >= 0 && service < 85 && p.Services[service/8]&(0x80>>uint(service%8)) != 0
}

// Identity is the vendor, model and revision returned by MMS Identify.
type Identity struct{ Vendor, Model, Revision string }

// Status holds the logical and physical status codes returned by MMS Status.
type Status struct{ Logical, Physical int }

// State is one client association state.
type State string

const (
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateClosed     State = "closed"
)
