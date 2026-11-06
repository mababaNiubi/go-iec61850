// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import "context"

// Assignment sets one locally acquired value in a Model.
type Assignment struct {
	Name  ObjectName
	Value Value
}

// SetValues applies one local acquisition atomically, including to variables
// that are read-only over MMS. Remote writes still use Write and its access
// checks. A failed assignment leaves every variable at its previous value.
func (m *Model) SetValues(ctx context.Context, changes ...Assignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	staged := make(map[ObjectName]Variable)
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		v, path, err := m.findLocked(change.Name)
		if err != nil {
			return err
		}
		if previous, ok := staged[v.Name]; ok {
			v = previous
		}
		_, spec, _, err := selected(v, path, Selection{})
		if err != nil {
			return err
		}
		if err = ValidateValue(spec, change.Value); err != nil {
			return TypeInconsistent
		}
		v.Value = replaceValue(v.Value, path, change.Value)
		staged[v.Name] = v
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for name, v := range staged {
		m.variables[name] = v
	}
	return nil
}
