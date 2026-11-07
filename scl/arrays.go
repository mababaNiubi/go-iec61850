// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

func arrayCount(raw string) (int, error) {
	count, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || count < 1 || count > 4096 {
		return 0, fmt.Errorf("SCL array count must be 1..4096")
	}
	return int(count), nil
}

// FCDA paths use SCL's component(index) syntax. Name the enclosing MMS array,
// then retain the ordered index/component path as genuine AlternateAccess.
func (d *IED) memberAccess(m memberXML, path string) (mms.VariableAccess, error) {
	var names []string
	var steps []mms.Selector
	var firstIndex *uint32
	for _, part := range strings.Split(path, ".") {
		name := part
		var index *uint32
		if start := strings.IndexByte(part, '('); start >= 0 {
			if !strings.HasSuffix(part, ")") {
				return mms.VariableAccess{}, fmt.Errorf("invalid FCDA array path %q", path)
			}
			name = part[:start]
			number, err := strconv.ParseUint(part[start+1:len(part)-1], 10, 32)
			if err != nil {
				return mms.VariableAccess{}, err
			}
			n := uint32(number)
			index = &n
		}
		if !identifier(name) {
			return mms.VariableAccess{}, fmt.Errorf("invalid FCDA component %q", part)
		}
		if firstIndex == nil {
			names = append(names, name)
		} else {
			steps = append(steps, mms.Selector{Component: name})
		}
		if index != nil {
			if firstIndex == nil {
				firstIndex = index
			}
			steps = append(steps, mms.Selector{Index: index})
		}
	}
	n, err := d.Lookup(m.LD, m.Prefix+m.Class+m.Inst, m.FC, strings.Join(names, "."))
	if err != nil {
		return mms.VariableAccess{}, err
	}
	if m.Index != "" {
		number, err := strconv.ParseUint(m.Index, 10, 32)
		if err != nil {
			return mms.VariableAccess{}, err
		}
		index := uint32(number)
		if firstIndex != nil {
			if index != *firstIndex {
				return mms.VariableAccess{}, fmt.Errorf("conflicting FCDA ix and path index")
			}
		} else {
			steps = append(steps, mms.Selector{Index: &index})
		}
	}
	access := mms.Access(n)
	if len(steps) == 1 {
		access.Selection.Index = steps[0].Index
		access.Selection.Component = steps[0].Component
	} else if len(steps) > 1 {
		access.Selection.Path = steps
	}
	if _, err = mms.SelectedType(d.types[n], access.Selection); err != nil {
		return mms.VariableAccess{}, err
	}
	return access, nil
}

// DO/SDO arrays are split by FC, then each complete FC structure is an MMS
// array element. Numeric compiler paths identify initial values/triggers only;
// they never create fake named variables for array elements on the wire.
func (c *compiler) arrayObject(root *branch, ref declaration, path string, depth int) error {
	count, err := arrayCount(ref.Count)
	if err != nil {
		return err
	}
	ref.Count = ""
	type valuesByFC struct {
		fc     string
		spec   mms.TypeSpec
		values []mms.Value
	}
	var groups []*valuesByFC
	byFC := map[string]*valuesByFC{}
	for i := 0; i < count; i++ {
		var element branch
		indexed := path + "." + strconv.Itoa(i)
		if err = c.object(&element, ref, indexed, depth+1); err != nil {
			return err
		}
		for _, fc := range element.children {
			node := fc
			for _, part := range strings.Split(indexed, ".") {
				node = node.index[part]
				if node == nil {
					return fmt.Errorf("scl: incomplete array element")
				}
			}
			value := node.build()
			group := byFC[fc.name]
			if group == nil {
				group = &valuesByFC{fc: fc.name, spec: value.spec}
				byFC[fc.name] = group
				groups = append(groups, group)
			}
			group.values = append(group.values, value.value)
		}
	}
	for _, group := range groups {
		value := typedValue{mms.TypeSpec{Kind: mms.KindArray, Length: count, Element: &group.spec}, mms.Array(group.values...)}
		if err := root.insert(append([]string{group.fc}, strings.Split(path, ".")...), value); err != nil {
			return err
		}
	}
	return nil
}

func accessReference(a mms.VariableAccess) mms.ObjectName {
	n := a.Name
	steps, _ := a.Selection.Steps()
	for _, step := range steps {
		if step.Component != "" {
			n.Item += "$" + step.Component
		} else {
			n.Item += "$" + strconv.FormatUint(uint64(*step.Index), 10)
		}
	}
	return n
}

// A trigger may point inside an array, while only the enclosing array is an
// MMS named variable. Resolve that compiler path through its declared type.
func (d *IED) triggerValue(ctx context.Context, n mms.ObjectName) (mms.Value, error) {
	base, indices, _, err := d.nestedType(n)
	if err != nil {
		return mms.Value{}, err
	}
	value, err := d.model.Read(ctx, mms.Access(base))
	if err != nil {
		return mms.Value{}, err
	}
	return data.ValueAt(value, indices)
}
func (d *IED) nestedType(n mms.ObjectName) (mms.ObjectName, []int, mms.TypeSpec, error) {
	if spec, ok := d.types[n]; ok {
		return n, nil, spec, nil
	}
	parts := strings.Split(n.Item, "$")
	for cut := len(parts) - 1; cut >= 2; cut-- {
		base := mms.DomainName(n.Domain, strings.Join(parts[:cut], "$"))
		spec, ok := d.types[base]
		if !ok {
			continue
		}
		selected, path, err := data.ResolveComponent(spec, strings.Join(parts[cut:], "."))
		if err != nil {
			continue
		}
		return base, path, selected, nil
	}
	return mms.ObjectName{}, nil, mms.TypeSpec{}, mms.ObjectNonExistent
}
