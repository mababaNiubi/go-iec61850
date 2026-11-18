// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Sampled-value control block modeling. The SVCB echoes the engineered SV
// stream settings and lets a client enable or disable publication through
// SvEna, mirroring the GoCB behavior. Resv is association-scoped like a URCB
// reservation.
//
// OptFlds bit assignment: the pinned reference implementation stores whatever
// integer the application supplies and contains no SCL-to-bit mapping, so
// there was nothing to copy. The assignment below follows the public SVCB
// field list, with bit i at mask 0x8000>>i (the same convention this package
// uses for RCB OptFlds). It is implementation-derived and NOT verified against
// normative text; revisit it when the licensed clause is available.
const (
	svcbBitRefreshTime = iota
	svcbBitSampleSynchronized
	svcbBitSampleRate
	svcbBitDataSet
	svcbBitSecurity
	svcbBitTimestamp
	svcbBitSynchSourceID
)

type svcb struct {
	reference string
	enabled   atomic.Bool
	owner     *mms.Association
	reserved  bool
}

func (d *IED) addSVCBs() error {
	profiles, err := d.system.SVStreams(d.name, d.ap)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		parts := strings.Split(profile.Reference, "/")
		if len(parts) != 4 {
			return fmt.Errorf("scl: invalid SV reference %q", profile.Reference)
		}
		domain, ok := d.domains[parts[2]]
		if !ok {
			continue
		}
		name := mms.DomainName(domain, "LLN0$MS$"+parts[3])
		var optFlds uint16
		// sampleSynchronized is required true by the SCL parser, and secured
		// SV is unsupported, so those bits are constant.
		for bit, on := range []bool{
			profile.WithTime, true, profile.WithRate,
			profile.WithDataSet, false, false, profile.WithIdentity,
		} {
			if on {
				optFlds |= 0x8000 >> uint(bit)
			}
		}
		components := []mms.Component{
			{Name: "SvEna", Type: boolSpec()},
			{Name: "Resv", Type: boolSpec()},
			{Name: "MsvID", Type: textSpec()},
			{Name: "DatSet", Type: textSpec()},
			{Name: "ConfRev", Type: uintSpec(32)},
			{Name: "SmpRate", Type: uintSpec(32)},
			{Name: "OptFlds", Type: bitSpec(10)},
			{Name: "SmpMod", Type: uintSpec(8)},
			{Name: "NoASDU", Type: uintSpec(8)},
		}
		mode := uint64(profile.SampleMode)
		if !profile.ModePresent {
			mode = 0
		}
		values := []mms.Value{
			mms.Boolean(true),
			mms.Boolean(false),
			mms.VisibleString(profile.SVID),
			mms.VisibleString(profile.DataSet),
			mms.Unsigned(uint64(profile.ConfRev)),
			mms.Unsigned(uint64(profile.SampleRate)),
			bitsValue(optFlds, 10),
			mms.Unsigned(mode),
			mms.Unsigned(uint64(profile.ASDUCount)),
		}
		if err := d.addService(name, components, values); err != nil {
			return err
		}
		state := &svcb{reference: profile.Reference}
		state.enabled.Store(true)
		d.mu.Lock()
		if d.svcbs == nil {
			d.svcbs = make(map[mms.ObjectName]*svcb)
			d.svcbByRef = make(map[string]*svcb)
		}
		d.svcbs[name] = state
		d.svcbByRef[profile.Reference] = state
		d.mu.Unlock()
	}
	return nil
}

// SVEnabled reports whether the engineered SV stream publishes. Unknown
// references stay enabled so a source without a modeled SVCB keeps working.
func (d *IED) SVEnabled(reference string) bool {
	d.mu.RLock()
	state := d.svcbByRef[reference]
	d.mu.RUnlock()
	if state == nil {
		return true
	}
	return state.enabled.Load()
}

// writeSVCB applies the online SVCB writes: SvEna and Resv. Other attributes
// change the engineered stream and are refused.
func (d *IED) writeSVCB(ctx context.Context, name mms.ObjectName, value mms.Value) (bool, error) {
	parts := splitItem(name.Item)
	if len(parts) < 4 || parts[1] != "MS" {
		return false, nil
	}
	base := mms.DomainName(name.Domain, parts[0]+"$MS$"+parts[2])
	d.mu.RLock()
	state := d.svcbs[base]
	d.mu.RUnlock()
	if state == nil {
		return false, nil
	}
	if len(parts) != 4 {
		return true, mms.ObjectAccessDenied
	}
	switch parts[3] {
	case "SvEna":
		on, err := value.Bool()
		if err != nil {
			return true, mms.TypeInconsistent
		}
		state.enabled.Store(on)
		return true, nil
	case "Resv":
		on, err := value.Bool()
		if err != nil {
			return true, mms.TypeInconsistent
		}
		peer := mms.AssociationFromContext(ctx)
		d.mu.Lock()
		defer d.mu.Unlock()
		if state.owner != nil && state.owner != peer {
			return true, mms.TemporarilyUnavailable
		}
		state.reserved = on
		if on {
			state.owner = peer
		} else {
			state.owner = nil
		}
		return true, nil
	}
	return true, mms.ObjectAccessDenied
}

// svcbState reads the runtime SvEna/Resv values for the read path.
func (d *IED) svcbField(name mms.ObjectName) (mms.Value, bool) {
	parts := splitItem(name.Item)
	if len(parts) != 4 || parts[1] != "MS" {
		return mms.Value{}, false
	}
	base := mms.DomainName(name.Domain, parts[0]+"$MS$"+parts[2])
	d.mu.RLock()
	state := d.svcbs[base]
	var reserved bool
	if state != nil {
		reserved = state.reserved
	}
	d.mu.RUnlock()
	if state == nil {
		return mms.Value{}, false
	}
	switch parts[3] {
	case "SvEna":
		return mms.Boolean(state.enabled.Load()), true
	case "Resv":
		return mms.Boolean(reserved), true
	}
	return mms.Value{}, false
}
