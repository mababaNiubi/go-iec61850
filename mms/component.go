// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import "context"

// AddComponent extends a locally defined structure, preserving existing fields
// and their values. It is useful while constructing a model's service groups.
// Define the complete model before accepting associations so clients can cache
// stable declarations. The operation itself is atomic with respect to readers.
func (m *Model) AddComponent(ctx context.Context, parent ObjectName, component Component, value Value) error {
	if !validIdentifier(component.Name) {
		return InvalidAddress
	}
	encoded, err := EncodeType(component.Type)
	if err != nil {
		return err
	}
	component.Type, err = DecodeType(encoded)
	if err != nil {
		return err
	}
	if err = ValidateValue(component.Type, value); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	variable, path, err := m.findLocked(parent)
	if err != nil {
		return err
	}
	current, spec, _, err := selected(variable, path, Selection{})
	if err != nil {
		return err
	}
	if spec.Kind != KindStructure {
		return ObjectAccessUnsupported
	}
	for _, field := range spec.Components {
		if field.Name == component.Name {
			return ObjectValueInvalid
		}
	}
	spec.Components = append(append([]Component(nil), spec.Components...), component)
	items, _ := current.Elements()
	items = append(items, value)
	variable.Type = replaceType(variable.Type, path, spec)
	variable.Value = replaceValue(variable.Value, path, Structure(items...))
	m.variables[variable.Name] = variable
	return nil
}
func replaceType(base TypeSpec, path []int, replacement TypeSpec) TypeSpec {
	if len(path) == 0 {
		return replacement
	}
	base.Components = append([]Component(nil), base.Components...)
	base.Components[path[0]].Type = replaceType(base.Components[path[0]].Type, path[1:], replacement)
	return base
}
