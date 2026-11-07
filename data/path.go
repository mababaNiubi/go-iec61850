// SPDX-License-Identifier: GPL-3.0-or-later
package data

import (
	"fmt"
	"strconv"
	"strings"
)

// ResolveComponent compiles a dotted structure/array path into ordered indices.
// Array indices start at zero. An empty path selects the complete value.
// The returned TypeSpec belongs to the caller: changing it does not mutate spec.
func ResolveComponent(spec TypeSpec, path string) (TypeSpec, []int, error) {
	if err := ValidateType(spec); err != nil {
		return TypeSpec{}, nil, err
	}
	var indices []int
	if path != "" {
		for _, name := range strings.Split(path, ".") {
			index := -1
			switch spec.Kind {
			case KindArray:
				n, err := strconv.Atoi(name)
				if err == nil && n >= 0 && n < spec.Length {
					index = n
					spec = *spec.Element
				}
			case KindStructure:
				for i, field := range spec.Components {
					if field.Name == name && name != "" {
						if index >= 0 {
							return TypeSpec{}, nil, fmt.Errorf("data: ambiguous component %q", name)
						}
						index = i
					}
				}
				if index >= 0 {
					spec = spec.Components[index].Type
				}
			}
			if index < 0 {
				return TypeSpec{}, nil, fmt.Errorf("data: unknown component or array index %q", name)
			}
			indices = append(indices, index)
		}
	}
	return CloneType(spec), indices, nil
}

// CloneType copies a valid type, including its nested slices and pointers.
// Invalid/cyclic descriptions yield the invalid zero type.
func CloneType(spec TypeSpec) TypeSpec {
	if ValidateType(spec) != nil {
		return TypeSpec{}
	}
	return cloneType(spec)
}
func cloneType(spec TypeSpec) TypeSpec {
	if spec.Element != nil {
		element := cloneType(*spec.Element)
		spec.Element = &element
	}
	if spec.Components != nil {
		fields := make([]Component, len(spec.Components))
		for i, field := range spec.Components {
			fields[i] = Component{Name: field.Name, Type: cloneType(field.Type)}
		}
		spec.Components = fields
	}
	return spec
}

// ValueAt reads a compiled component path without changing its parent value.
func ValueAt(value Value, indices []int) (Value, error) {
	for _, index := range indices {
		children, err := value.Elements()
		if err != nil || index < 0 || index >= len(children) {
			return Value{}, fmt.Errorf("data: component index %d outside value", index)
		}
		value = children[index]
	}
	return value, nil
}

// WithComponent returns a new value with one component replaced. Untouched
// siblings and the original value are preserved. ValidateValue can subsequently
// check the result against the engineered/online declaration.
func WithComponent(value Value, indices []int, replacement Value) (Value, error) {
	if len(indices) == 0 {
		return replacement, nil
	}
	if len(indices) > 64 {
		return Value{}, fmt.Errorf("data: component path exceeds depth limit")
	}
	children, err := value.Elements()
	index := indices[0]
	if err != nil || index < 0 || index >= len(children) {
		return Value{}, fmt.Errorf("data: component index %d outside value", index)
	}
	children[index], err = WithComponent(children[index], indices[1:], replacement)
	if err != nil {
		return Value{}, err
	}
	if value.Kind() == KindArray {
		return Array(children...), nil
	}
	return Structure(children...), nil
}
