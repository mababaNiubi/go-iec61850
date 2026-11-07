// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"fmt"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Split only between complete dataset members. Every segment retains the full
// inclusion-bitmap width and one report sequence/EntryID/TimeOfEntry. A single
// member larger than the negotiated PDU cannot be split by ACSI reporting.
func (d *IED) sendReport(a *mms.Association, b *reportBlock, r reportRecord) error {
	raw := d.encodeReport(b, r, nil, false)
	size, err := a.ReportSize(raw)
	if err != nil {
		return err
	}
	if size <= a.Parameters().MaxPDUSize {
		return a.SendReport(a.Context(), raw)
	}
	b.options |= 0x4040
	var included []int
	for i, reason := range r.reasons {
		if reason != 0 {
			included = append(included, i)
		}
	}
	var sequence uint16
	for start := 0; start < len(included); {
		low, high := start+1, len(included)
		end := start
		var selected mms.InformationReport
		for low <= high {
			middle := low + (high-low)/2
			segment := r
			segment.reasons = make([]byte, len(r.reasons))
			for _, i := range included[start:middle] {
				segment.reasons[i] = r.reasons[i]
			}
			candidate := d.encodeReport(b, segment, &sequence, middle < len(included))
			size, err = a.ReportSize(candidate)
			if err != nil {
				return err
			}
			if size <= a.Parameters().MaxPDUSize {
				end = middle
				selected = candidate
				low = middle + 1
			} else {
				high = middle - 1
			}
		}
		if end == start {
			return fmt.Errorf("scl: dataset member exceeds negotiated report PDU")
		}
		if err := a.SendReport(a.Context(), selected); err != nil {
			return err
		}
		start = end
		sequence++
	}
	return nil
}
