// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"fmt"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Signal holds the three MMS addresses for one measured or status signal.
type Signal struct{ Value, Quality, Time mms.ObjectName }

// Signal resolves a value and its sibling q/t in the same data object. For
// example, Signal("CTRL","MMXU1","MX","TotW","mag.f") binds power, quality
// and acquisition time, honoring LD aliases and the instantiated templates.
func (d *IED) Signal(ld, ln, fc, object, member string) (Signal, error) {
	if fc != "ST" && fc != "MX" {
		return Signal{}, fmt.Errorf("scl: signal requires ST or MX")
	}
	var s Signal
	fields := []struct {
		dest *mms.ObjectName
		path string
	}{
		{&s.Value, member}, {&s.Quality, "q"}, {&s.Time, "t"},
	}
	for _, f := range fields {
		n, err := d.Lookup(ld, ln, fc, object+"."+f.path)
		if err != nil {
			return Signal{}, err
		}
		*f.dest = n
	}
	q := d.types[s.Quality]
	if s.Value == s.Quality || s.Value == s.Time || q.Kind != mms.KindBitString || (q.Length != -13 && q.Length != 13) || d.types[s.Time].Kind != mms.KindUTC {
		return Signal{}, fmt.Errorf("scl: %s.%s has no compatible value/quality/time tuple", ln, object)
	}
	return s, nil
}

// Sample prepares a coherent local update. Pass its result to IED.Update,
// or append several samples to the same acquisition batch. A raw MMS UTC
// timestamp can retain the device's time-quality bits.
func (s Signal) Sample(value, quality, timestamp mms.Value) []mms.Assignment {
	return []mms.Assignment{
		{Name: s.Value, Value: value},
		{Name: s.Quality, Value: quality},
		{Name: s.Time, Value: timestamp},
	}
}
