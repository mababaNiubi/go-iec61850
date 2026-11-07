// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"fmt"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// Leaves expands a typed FCD structure into named DA/BDA entries, preserving
// this report's inclusion reason. A status FCD therefore supplies value, q and
// source t from the same event. Arrays and alternate-access members retain
// their original access representation. Scalar entries pass through unchanged.
func (r Report) Leaves() ([]ReportEntry, error) {
	var out []ReportEntry
	var visit func(ReportEntry, int) error
	visit = func(entry ReportEntry, depth int) error {
		if depth > 64 || len(out) >= 65536 {
			return fmt.Errorf("ied: report expansion limit")
		}
		if entry.Type.Kind != data.KindStructure || entry.Variable.Selection.Index != nil || entry.Variable.Selection.Component != "" || len(entry.Variable.Selection.Path) > 0 {
			out = append(out, entry)
			return nil
		}
		if err := data.ValidateValue(entry.Type, entry.Value); err != nil {
			return err
		}
		values, _ := entry.Value.Elements()
		for i, field := range entry.Type.Components {
			if field.Name == "" {
				return fmt.Errorf("ied: unnamed report structure field")
			}
			child := entry
			child.Type = field.Type
			child.Value = values[i]
			child.Variable = mms.Access(entry.Variable.Name)
			child.Variable.Name.Item += "$" + field.Name
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, entry := range r.Entries {
		if err := visit(entry, 0); err != nil {
			return nil, err
		}
	}
	return out, nil
}
