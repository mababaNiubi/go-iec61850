// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type ObjectClass uint8

const (
	NamedVariable     ObjectClass = 0
	NamedVariableList ObjectClass = 2
	DomainObject      ObjectClass = 9
)

// Store is the business-data boundary. Implementations must be concurrency safe
// and honor cancellation. Returned values and type descriptions are snapshots.
type Store interface {
	Names(context.Context, Scope, string) ([]string, error)
	Domains(context.Context) ([]string, error)
	Type(context.Context, ObjectName) (TypeSpec, error)
	Read(context.Context, VariableAccess) (Value, error)
	Write(context.Context, VariableAccess, Value) error
}

type Variable struct {
	Name     ObjectName
	Type     TypeSpec
	Value    Value
	Writable bool
}

// Model is a concurrency-safe in-memory Store. Variables keep their declared
// type when written. Values use immutable storage.
type Model struct {
	mu        sync.RWMutex
	variables map[ObjectName]Variable
	domains   map[string]struct{}
}

func NewModel() *Model {
	return &Model{variables: make(map[ObjectName]Variable), domains: make(map[string]struct{})}
}
func (m *Model) AddDomain(name string) error {
	if !validIdentifier(name) {
		return fmt.Errorf("mms: invalid domain name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.domains == nil {
		m.domains = make(map[string]struct{})
	}
	m.domains[name] = struct{}{}
	return nil
}
func (m *Model) Add(v Variable) error {
	if err := v.Name.validate(); err != nil {
		return err
	}
	if v.Name.Scope == AssociationScope {
		return fmt.Errorf("mms: association-specific variables are not supported by Model")
	}
	encoded, err := EncodeType(v.Type)
	if err != nil {
		return err
	}
	v.Type, err = DecodeType(encoded)
	if err != nil {
		return err
	}
	if err = ValidateValue(v.Type, v.Value); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.variables == nil {
		m.variables = make(map[ObjectName]Variable)
	}
	if m.domains == nil {
		m.domains = make(map[string]struct{})
	}
	if _, ok := m.variables[v.Name]; ok {
		return fmt.Errorf("mms: duplicate variable %s", v.Name)
	}
	m.variables[v.Name] = v
	if v.Name.Scope == DomainScope {
		m.domains[v.Name.Domain] = struct{}{}
	}
	return nil
}
func (m *Model) Domains(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.domains))
	for name := range m.domains {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
func (m *Model) Names(ctx context.Context, scope Scope, domain string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if scope == DomainScope {
		if _, ok := m.domains[domain]; !ok {
			return nil, &ServiceError{7, 2}
		}
	}
	names := make(map[string]struct{})
	var addChildren func(string, TypeSpec)
	addChildren = func(prefix string, t TypeSpec) {
		for _, c := range t.Components {
			if c.Name == "" {
				continue
			}
			n := prefix + "$" + c.Name
			if len(n) <= 64 {
				names[n] = struct{}{}
				addChildren(n, c.Type)
			}
		}
	}
	for n, v := range m.variables {
		if n.Scope == scope && n.Domain == domain {
			names[n.Item] = struct{}{}
			addChildren(n.Item, v.Type)
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
func cloneType(t TypeSpec) TypeSpec {
	if t.Element != nil {
		e := cloneType(*t.Element)
		t.Element = &e
	}
	if t.Components != nil {
		cs := make([]Component, len(t.Components))
		for i, c := range t.Components {
			cs[i] = Component{Name: c.Name, Type: cloneType(c.Type)}
		}
		t.Components = cs
	}
	return t
}

// findLocked resolves a registered variable, or a "$"-separated structure member.
func (m *Model) findLocked(n ObjectName) (Variable, []int, error) {
	if v, ok := m.variables[n]; ok {
		return v, nil, nil
	}
	parts := strings.Split(n.Item, "$")
	for cut := len(parts) - 1; cut >= 1; cut-- {
		base := n
		base.Item = strings.Join(parts[:cut], "$")
		v, ok := m.variables[base]
		if !ok {
			continue
		}
		t := v.Type
		path := make([]int, 0, len(parts)-cut)
		for _, part := range parts[cut:] {
			idx := -1
			for i, c := range t.Components {
				if c.Name == part {
					idx = i
					t = c.Type
					break
				}
			}
			if idx < 0 {
				return Variable{}, nil, ObjectNonExistent
			}
			path = append(path, idx)
		}
		return v, path, nil
	}
	return Variable{}, nil, ObjectNonExistent
}
func selected(v Variable, path []int, sel Selection) (Value, TypeSpec, []int, error) {
	value, t := v.Value, v.Type
	path = append([]int(nil), path...)
	for _, i := range path {
		items, err := value.Elements()
		if err != nil || i >= len(items) {
			return Value{}, TypeSpec{}, nil, InvalidAddress
		}
		value = items[i]
		t = t.Components[i].Type
	}
	if sel.Component != "" {
		for _, part := range strings.Split(sel.Component, "$") {
			idx := -1
			for i, c := range t.Components {
				if c.Name == part {
					idx = i
					break
				}
			}
			if idx < 0 {
				return Value{}, TypeSpec{}, nil, ObjectNonExistent
			}
			items, _ := value.Elements()
			value = items[idx]
			t = t.Components[idx].Type
			path = append(path, idx)
		}
	}
	if sel.Index != nil {
		if t.Kind != KindArray || t.Element == nil {
			return Value{}, TypeSpec{}, nil, ObjectAccessUnsupported
		}
		items, _ := value.Elements()
		if uint64(*sel.Index) >= uint64(len(items)) {
			return Value{}, TypeSpec{}, nil, InvalidAddress
		}
		idx := int(*sel.Index)
		value = items[idx]
		t = *t.Element
		path = append(path, idx)
	}
	return value, t, path, nil
}
func (m *Model) Type(ctx context.Context, n ObjectName) (TypeSpec, error) {
	if err := ctx.Err(); err != nil {
		return TypeSpec{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, path, err := m.findLocked(n)
	if err != nil {
		return TypeSpec{}, err
	}
	_, t, _, err := selected(v, path, Selection{})
	return cloneType(t), err
}
func (m *Model) Read(ctx context.Context, a VariableAccess) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, path, err := m.findLocked(a.Name)
	if err != nil {
		return Value{}, err
	}
	value, _, _, err := selected(v, path, a.Selection)
	return value, err
}
func replaceValue(base Value, path []int, value Value) Value {
	if len(path) == 0 {
		return value
	}
	items, _ := base.Elements()
	items[path[0]] = replaceValue(items[path[0]], path[1:], value)
	if base.Kind() == KindArray {
		return Array(items...)
	}
	return Structure(items...)
}
func (m *Model) Write(ctx context.Context, a VariableAccess, value Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, path, err := m.findLocked(a.Name)
	if err != nil {
		return err
	}
	if !v.Writable {
		return ObjectAccessDenied
	}
	_, t, path, err := selected(v, path, a.Selection)
	if err != nil {
		return err
	}
	if err = ValidateValue(t, value); err != nil {
		return TypeInconsistent
	}
	v.Value = replaceValue(v.Value, path, value)
	m.variables[v.Name] = v
	return nil
}
