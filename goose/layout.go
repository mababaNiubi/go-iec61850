// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/mababaNiubi/go-iec61850/data"
)

// Member describes an ordered FCDA. Domain identifies its logical-device
// relationship (local/relative/absolute for SCL-created layouts), not a MAC/IP.
type Member struct {
	Name, Domain string
	Type         data.TypeSpec
}
type Layout struct{ Members []Member }

// Field is an allData slot followed by an ordered structure/array path.
// Treat compiled fields as immutable; Type retains the actual declaration.
type Field struct {
	Slot int
	Path []int
	Type data.TypeSpec
}

func (l Layout) Field(memberRef, path string) (Field, error) {
	slot, err := strconv.Atoi(memberRef)
	if err != nil {
		slot = -1
		for i, member := range l.Members {
			if member.Name == memberRef {
				if slot >= 0 {
					return Field{}, fmt.Errorf("goose: ambiguous FCDA %q; select its index", memberRef)
				}
				slot = i
			}
		}
	}
	if slot < 0 || slot >= len(l.Members) {
		return Field{}, fmt.Errorf("goose: unknown FCDA %q", memberRef)
	}
	typ, indices, err := data.ResolveComponent(l.Members[slot].Type, path)
	return Field{Slot: slot, Path: indices, Type: typ}, err
}
func (f Field) Read(values []data.Value) (data.Value, error) {
	if f.Slot < 0 || f.Slot >= len(values) {
		return data.Value{}, fmt.Errorf("goose: short allData")
	}
	value, err := data.ValueAt(values[f.Slot], f.Path)
	if err == nil {
		err = data.ValidateValue(f.Type, value)
	}
	return value, err
}

// Set changes the caller-owned allData slice, preserving the immutable root
// value and all unselected siblings. Publish the complete batch with Update.
func (f Field) Set(values []data.Value, value data.Value) error {
	if f.Slot < 0 || f.Slot >= len(values) {
		return fmt.Errorf("goose: short allData")
	}
	if err := data.ValidateValue(f.Type, value); err != nil {
		return err
	}
	updated, err := data.WithComponent(values[f.Slot], f.Path, value)
	if err != nil {
		return err
	}
	values[f.Slot] = updated
	return nil
}
func (l Layout) CheckCompatible(other Layout) error {
	if len(l.Members) != len(other.Members) {
		return fmt.Errorf("goose: dataset member count differs")
	}
	for i, member := range l.Members {
		target := other.Members[i]
		a, err := data.EncodeType(member.Type)
		if err != nil {
			return err
		}
		b, err := data.EncodeType(target.Type)
		if err != nil {
			return err
		}
		if member.Name != target.Name || member.Domain != target.Domain || !bytes.Equal(a, b) {
			return fmt.Errorf("goose: dataset member %d differs", i)
		}
	}
	return nil
}

// LeafCount is useful for applications requiring complete initialization of a
// publication. Invalid or excessively large declarations return an error.
func (l Layout) LeafCount() (int, error) {
	const limit = 1 << 20
	var count func(data.TypeSpec) (int, error)
	count = func(spec data.TypeSpec) (int, error) {
		switch spec.Kind {
		case data.KindArray:
			n, err := count(*spec.Element)
			if err != nil {
				return 0, err
			}
			if n > 0 && spec.Length > limit/n {
				return 0, fmt.Errorf("goose: excessive layout size")
			}
			return n * spec.Length, nil
		case data.KindStructure:
			total := 0
			for _, field := range spec.Components {
				n, err := count(field.Type)
				if err != nil {
					return 0, err
				}
				total += n
				if total > limit {
					return 0, fmt.Errorf("goose: excessive layout size")
				}
			}
			return total, nil
		default:
			return 1, nil
		}
	}
	total := 0
	for _, member := range l.Members {
		if err := data.ValidateType(member.Type); err != nil {
			return 0, err
		}
		n, err := count(member.Type)
		if err != nil {
			return 0, err
		}
		total += n
		if total > limit {
			return 0, fmt.Errorf("goose: excessive layout size")
		}
	}
	return total, nil
}
