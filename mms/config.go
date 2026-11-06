// SPDX-License-Identifier: GPL-3.0-or-later

// Package mms implements a native Go MMS client and server over RFC 1006.
package mms

import (
	"fmt"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

// ISOConfig configures the underlying ISO association, including selectors.
type ISOConfig = iso.Config

// Config contains local limits. Zero values select conservative defaults.
type Config struct {
	ISO                   ISOConfig
	MaxPDUSize            int
	MaxOutstandingCalling int
	MaxOutstandingCalled  int
	NestingLevel          int
	RequestTimeout        time.Duration
}

func (c Config) normalized() (Config, error) {
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

type Identity struct{ Vendor, Model, Revision string }
type Status struct{ Logical, Physical int }
