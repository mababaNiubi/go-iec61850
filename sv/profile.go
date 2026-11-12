// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"fmt"
	"time"
)

// Profile is an engineered multicast SVCB. Layout preserves FCDA order and
// original SCL basic types; Frame settings never come from point rows.
type Profile struct {
	Reference, SVID, DataSet                      string
	Destination                                   [6]byte
	APPID                                         uint16
	VLAN                                          *VLAN
	ConfRev                                       uint32
	SampleRate, SampleMode                        uint16
	ModePresent                                   bool
	ASDUCount                                     int
	WithDataSet, WithRate, WithTime, WithIdentity bool
	Layout                                        Layout
}

// SyncPolicy selects accepted sample synchronization: Synchronized accepts local/global
// sync, GlobalSynchronized requires global sync, and AnySynchronization also accepts
// unsynchronized samples.
type SyncPolicy uint8

const (
	Synchronized SyncPolicy = iota
	GlobalSynchronized
	AnySynchronization
)

// SubscriptionOptions supplies local filters and timing policy. NominalFrequency zero
// selects 50Hz; Timeout zero selects one second. The zero SyncPolicy rejects unsynchronized
// samples.
type SubscriptionOptions struct {
	Source           *[6]byte
	NominalFrequency uint16 // zero uses 50; only 50 and 60 are supported
	CounterModulus   uint32 // zero derives it from SCL and nominal frequency
	Timeout          time.Duration
	SyncPolicy       SyncPolicy
}

// Clone returns an independent copy of the engineered VLAN and layout metadata.
func (p Profile) Clone() Profile {
	if p.VLAN != nil {
		vlan := *p.VLAN
		p.VLAN = &vlan
	}
	p.Layout = p.Layout.Clone()
	return p
}

// SubscriptionConfig validates the layout and derives subscriber filters and counter
// modulus from the profile. Explicit CounterModulus overrides the derived value;
// synchronization policy remains local.
func (p Profile) SubscriptionConfig(options SubscriptionOptions) (SubscriptionConfig, error) {
	if err := p.Layout.Validate(); err != nil {
		return SubscriptionConfig{}, err
	}
	hz := options.NominalFrequency
	if hz == 0 {
		hz = 50
	}
	if hz != 50 && hz != 60 || options.SyncPolicy > AnySynchronization || options.Timeout < 0 {
		return SubscriptionConfig{}, fmt.Errorf("sv: invalid subscription options")
	}
	modulus := options.CounterModulus
	if modulus == 0 {
		switch p.SampleMode {
		case 0:
			modulus = uint32(p.SampleRate) * uint32(hz)
		case 1:
			modulus = uint32(p.SampleRate)
		case 2:
			modulus = 65536
		default:
			return SubscriptionConfig{}, fmt.Errorf("sv: invalid sample mode")
		}
	}
	if modulus < 2 || modulus > 65536 {
		return SubscriptionConfig{}, fmt.Errorf("sv: sample counter modulus outside 2..65536; configure explicitly")
	}
	config := SubscriptionConfig{Destination: p.Destination, Source: options.Source, APPID: p.APPID, SVID: p.SVID, ConfRev: p.ConfRev, DataLength: p.Layout.Size, CounterModulus: modulus, Timeout: options.Timeout, AllowUnsynchronized: options.SyncPolicy == AnySynchronization, RequireGlobalSync: options.SyncPolicy == GlobalSynchronized, RequireUntagged: p.VLAN == nil, AllowRestartAfterTimeout: true}
	if p.WithDataSet {
		config.DataSet = p.DataSet
	}
	if p.VLAN != nil {
		id := p.VLAN.ID
		config.VLANID = &id
	}
	return config, nil
}

// NewFrame constructs independent ASDU buffers. The caller supplies actual
// samples, counters and synchronization; they are not inferred from wall time.
func (p Profile) NewFrame(source [6]byte) (Frame, error) {
	if err := p.Layout.Validate(); err != nil {
		return Frame{}, err
	}
	if p.ASDUCount < 1 || p.ASDUCount > MaxASDUs || p.ConfRev == 0 || p.SampleRate == 0 || p.SampleMode > 2 {
		return Frame{}, fmt.Errorf("sv: invalid engineered stream")
	}
	f := Frame{Destination: p.Destination, Source: source, APPID: p.APPID}
	if p.VLAN != nil {
		vlan := *p.VLAN
		f.VLAN = &vlan
	}
	for i := 0; i < p.ASDUCount; i++ {
		a := ASDU{SVID: p.SVID, ConfRev: p.ConfRev, Data: make([]byte, p.Layout.Size)}
		if p.WithDataSet {
			a.DataSet = p.DataSet
		}
		if p.WithRate {
			rate := p.SampleRate
			a.SampleRate = &rate
		}
		if p.ModePresent {
			mode := p.SampleMode
			a.SampleMode = &mode
		}
		if p.WithTime {
			a.RefreshTime = &[8]byte{}
		}
		if p.WithIdentity {
			a.GMIdentity = &[8]byte{}
		}
		f.PDU.ASDUs = append(f.PDU.ASDUs, a)
	}
	if _, err := EncodeFrame(f); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// ValidateFrame runs before Subscriber.Process: a bad later ASDU must not
// advance a counter, partly replace samples, or refresh the receive deadline.
func (p Profile) ValidateFrame(frame Frame) error {
	if len(frame.PDU.ASDUs) != p.ASDUCount {
		return fmt.Errorf("%w: noASDU differs from SCL", ErrConfiguration)
	}
	for _, a := range frame.PDU.ASDUs {
		if a.SVID != p.SVID || a.ConfRev != p.ConfRev || len(a.Data) != p.Layout.Size ||
			(p.WithDataSet && a.DataSet != p.DataSet) || (!p.WithDataSet && a.DataSet != "") ||
			(p.WithTime != (a.RefreshTime != nil)) || (p.WithRate != (a.SampleRate != nil)) || (p.WithIdentity != (a.GMIdentity != nil)) ||
			(a.SampleRate != nil && *a.SampleRate != p.SampleRate) || (a.SampleMode != nil && *a.SampleMode != p.SampleMode) {
			return fmt.Errorf("%w: ASDU differs from engineered profile", ErrConfiguration)
		}
	}
	return nil
}
