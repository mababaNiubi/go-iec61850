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

// Config contains local limits. Zero values select conservative defaults.
type Config struct {
	ISO                   ISOConfig
	MaxPDUSize            int           // bytes; zero selects 65000; range 128..16 MiB
	MaxOutstandingCalling int           // zero selects 5; range 1..1024
	MaxOutstandingCalled  int           // zero selects 5; range 1..1024
	NestingLevel          int           // zero selects 16; range 1..64
	RequestTimeout        time.Duration // per confirmed request; zero selects 5s; negative is invalid
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

// Identity is the vendor, model and revision returned by MMS Identify.
type Identity struct{ Vendor, Model, Revision string }

// Status holds the logical and physical status codes returned by MMS Status.
type Status struct{ Logical, Physical int }
