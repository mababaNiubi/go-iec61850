// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

// Selector is exactly one component name or array index in an ordered alternate-access path.
type Selector struct {
	Component string
	Index     *uint32
}

// Steps returns a defensive copy of a validated alternate-access path.
func (s Selection) Steps() ([]Selector, error) {
	if len(s.Path) > 0 && (s.Component != "" || s.Index != nil) || s.Component != "" && s.Index != nil {
		return nil, fmt.Errorf("mms: ambiguous alternate access")
	}
	steps := s.Path
	if len(steps) == 0 {
		if s.Component != "" || s.Index != nil {
			steps = []Selector{{s.Component, s.Index}}
		}
	}
	if len(steps) > 32 {
		return nil, ObjectAccessUnsupported
	}
	out := make([]Selector, len(steps))
	for i, step := range steps {
		if (step.Component == "") == (step.Index == nil) {
			return nil, ObjectAccessUnsupported
		}
		if step.Component != "" && !validIdentifier(step.Component) {
			return nil, ErrProtocol
		}
		out[i] = step
		if step.Index != nil {
			index := *step.Index
			out[i].Index = &index
		}
	}
	return out, nil
}

// Clone returns an independent selection, including array-index pointers and nested path
// storage.
func (s Selection) Clone() Selection {
	out := s
	if s.Index != nil {
		index := *s.Index
		out.Index = &index
	}
	out.Path = make([]Selector, len(s.Path))
	for i, step := range s.Path {
		out.Path[i] = step
		if step.Index != nil {
			index := *step.Index
			out.Path[i].Index = &index
		}
	}
	return out
}

// Equal compares validated selection meaning, including legacy and nested representations.
func (s Selection) Equal(other Selection) bool {
	a, err := s.Steps()
	if err != nil {
		return false
	}
	b, err := other.Steps()
	if err != nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Component != b[i].Component || (a[i].Index == nil) != (b[i].Index == nil) {
			return false
		}
		if a[i].Index != nil && *a[i].Index != *b[i].Index {
			return false
		}
	}
	return true
}

func encodeSelection(steps []Selector) []byte {
	first := steps[0]
	if len(steps) == 1 {
		if first.Index != nil {
			return ber.Uint(0x82, uint64(*first.Index))
		}
		return ber.Encode(0x81, []byte(first.Component))
	}
	var access []byte
	if first.Index != nil {
		access = ber.Uint(0x81, uint64(*first.Index))
	} else {
		access = ber.Encode(0x80, []byte(first.Component))
	}
	return ber.Encode(0xa0, access, ber.Encode(0x30, encodeSelection(steps[1:])))
}
func decodeSelection(e ber.Element, depth int) ([]Selector, error) {
	if depth >= 32 {
		return nil, ObjectAccessUnsupported
	}
	var first Selector
	switch e.Tag {
	case 0x81:
		first.Component = string(e.Bytes)
		if !validIdentifier(first.Component) {
			return nil, ErrProtocol
		}
		return []Selector{first}, nil
	case 0x82:
		index, err := unsigned(e)
		if err != nil {
			return nil, err
		}
		first.Index = &index
		return []Selector{first}, nil
	case 0xa0:
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil || len(fields) != 2 || fields[1].Tag != 0x30 {
			return nil, ErrProtocol
		}
		switch fields[0].Tag {
		case 0x80:
			first.Component = string(fields[0].Bytes)
			if !validIdentifier(first.Component) {
				return nil, ErrProtocol
			}
		case 0x81:
			index, err := unsigned(fields[0])
			if err != nil {
				return nil, err
			}
			first.Index = &index
		default:
			return nil, ObjectAccessUnsupported
		}
		nested, rest, err := ber.Parse(fields[1].Bytes)
		if err != nil || len(rest) != 0 {
			return nil, ObjectAccessUnsupported
		}
		tail, err := decodeSelection(nested, depth+1)
		if err != nil {
			return nil, err
		}
		return append([]Selector{first}, tail...), nil
	default:
		return nil, ObjectAccessUnsupported
	}
}

func selectionPath(spec TypeSpec, selection Selection) (TypeSpec, []int, error) {
	steps, err := selection.Steps()
	if err != nil {
		return TypeSpec{}, nil, err
	}
	var indices []int
	for _, step := range steps {
		path := step.Component
		if step.Index != nil {
			if spec.Kind != KindArray || spec.Element == nil {
				return TypeSpec{}, nil, ObjectAccessUnsupported
			}
			if uint64(*step.Index) >= uint64(spec.Length) {
				return TypeSpec{}, nil, InvalidAddress
			}
			indices = append(indices, int(*step.Index))
			spec = *spec.Element
		} else {
			for _, part := range strings.Split(path, "$") {
				index := -1
				for i, field := range spec.Components {
					if field.Name == part {
						index = i
						break
					}
				}
				if index < 0 {
					return TypeSpec{}, nil, ObjectNonExistent
				}
				indices = append(indices, index)
				spec = spec.Components[index].Type
			}
		}
	}
	return spec, indices, nil
}

// SelectedType applies alternate access to an online declaration.
func SelectedType(spec TypeSpec, selection Selection) (TypeSpec, error) {
	selected, _, err := selectionPath(spec, selection)
	return data.CloneType(selected), err
}

// SelectedValue applies the same checked path to an immutable value snapshot.
func SelectedValue(spec TypeSpec, value Value, selection Selection) (Value, error) {
	_, path, err := selectionPath(spec, selection)
	if err != nil {
		return Value{}, err
	}
	return data.ValueAt(value, path)
}
