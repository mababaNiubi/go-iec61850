// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Online GOOSE control block management. The modeled GoCB exposes the
// engineered stream configuration and lets a remote client enable or disable
// publication through GoEna, matching the reference implementation's writable
// subset. Dataset rebinding and destination-address changes are not offered:
// they alter the engineered stream layout and need their own review.

// gocbError marks an unsupported GoCB write so the generic service path can
// report it as access-denied instead of silently ignoring it.
var gocbError = fmt.Errorf("scl: GoCB write unsupported")

// addGoCB registers the LLN0$GO$<name> structure for one engineered stream and
// wires its GoEna field to the publisher.
func (d *IED) addGoCB(name mms.ObjectName, stream *Stream) error {
	mac := stream.frame.Destination
	priority, vlanID, appID := uint8(4), uint16(0), stream.frame.APPID
	if stream.frame.VLAN != nil {
		priority, vlanID = stream.frame.VLAN.Priority, stream.frame.VLAN.ID
	}
	address := mms.Structure(
		mms.Unsigned(uint64(priority)),
		mms.Unsigned(uint64(vlanID)),
		mms.Unsigned(uint64(appID)),
		mms.VisibleString(fmt.Sprintf("%02X-%02X-%02X-%02X-%02X-%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])),
	)
	addressSpec := mms.TypeSpec{Kind: mms.KindStructure, Components: []mms.Component{
		{Name: "VLAN-PRIORITY", Type: uintSpec(8)},
		{Name: "VLAN-ID", Type: uintSpec(16)},
		{Name: "APPID", Type: uintSpec(16)},
		{Name: "MAC-Address", Type: textSpec()},
	}}
	components := []mms.Component{
		{Name: "GoEna", Type: boolSpec()},
		{Name: "GoID", Type: textSpec()},
		{Name: "DatSet", Type: textSpec()},
		{Name: "ConfRev", Type: uintSpec(32)},
		{Name: "NdsCom", Type: boolSpec()},
		{Name: "DstAddress", Type: addressSpec},
		{Name: "MinTime", Type: uintSpec(32)},
		{Name: "MaxTime", Type: uintSpec(32)},
		{Name: "FixedOffs", Type: boolSpec()},
	}
	values := []mms.Value{
		mms.Boolean(true),
		mms.VisibleString(stream.frame.PDU.GoID),
		mms.VisibleString(stream.set.Name.String()),
		mms.Unsigned(uint64(stream.frame.PDU.ConfRev)),
		mms.Boolean(false),
		address,
		mms.Unsigned(uint64(stream.minTime.Milliseconds())),
		mms.Unsigned(uint64(stream.maxTime.Milliseconds())),
		mms.Boolean(false),
	}
	if err := d.addService(name, components, values); err != nil {
		return err
	}
	d.mu.Lock()
	if d.gocbs == nil {
		d.gocbs = make(map[mms.ObjectName]*Stream)
	}
	d.gocbs[name] = stream
	d.mu.Unlock()
	return nil
}

// writeGoCB applies the supported online writes. It reports handled=false for
// names that are not GoCB fields. Only GoEna is writable; any other child of a
// modeled GoCB is refused explicitly rather than accepted as a no-op.
func (d *IED) writeGoCB(ctx context.Context, name mms.ObjectName, value mms.Value) (bool, error) {
	parts := splitItem(name.Item)
	if len(parts) < 4 || parts[1] != "GO" {
		return false, nil
	}
	base := mms.DomainName(name.Domain, parts[0]+"$GO$"+parts[2])
	d.mu.RLock()
	stream := d.gocbs[base]
	d.mu.RUnlock()
	if stream == nil {
		return false, nil
	}
	if len(parts) != 4 || parts[3] != "GoEna" {
		return true, mms.ObjectAccessDenied
	}
	on, err := value.Bool()
	if err != nil {
		return true, mms.TypeInconsistent
	}
	if on != stream.enabled.Load() {
		stream.enabled.Store(on)
		// A re-enabled stream must publish a fresh state even though its
		// values did not change while publication was off.
		stream.force.Store(on)
	}
	return true, nil
}

// splitItem splits an MMS item into its $-separated components.
func splitItem(item string) []string {
	var out []string
	start := 0
	for i := 0; i < len(item); i++ {
		if item[i] == '$' {
			out = append(out, item[start:i])
			start = i + 1
		}
	}
	return append(out, item[start:])
}

// setGoCBEnabled is the local equivalent of the GoEna write, for tests and
// applications that own the engineering configuration.
func (s *Stream) setGoCBEnabled(enabled bool) {
	if s.enabled.Swap(enabled) != enabled {
		s.force.Store(enabled)
	}
}

var _ = atomic.Bool{}
