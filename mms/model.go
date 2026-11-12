// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"fmt"
	"github.com/mababaNiubi/go-iec61850/data"
	"sort"
	"strings"
	"sync"
)

// ObjectClass selects named variables, named variable lists or logical-device domains for
// directory browsing.
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

// BatchReader is an optional Store capability. ReadBatch returns exactly one
// result per access, in request order, from one acquisition image. A failed
// member does not suppress its siblings. Store decorators should forward this
// capability when their underlying store supports it.
type BatchReader interface {
	ReadBatch(context.Context, []VariableAccess) []ReadResult
}

// ReadBatch uses a store's coherent batch capability when available. The
// fallback preserves existing Store implementations but cannot promise that
// separate Read calls observe the same acquisition.
func ReadBatch(ctx context.Context, store Store, accesses []VariableAccess) []ReadResult {
	// Use the concrete Model here. Adding an exported batch method to Model
	// would silently opt existing decorators embedding *Model into batching,
	// bypassing their overridden Read methods.
	if model, ok := store.(*Model); ok {
		return model.readBatch(ctx, accesses)
	}
	if reader, ok := store.(BatchReader); ok {
		return reader.ReadBatch(ctx, accesses)
	}
	results := make([]ReadResult, len(accesses))
	for i, access := range accesses {
		results[i].Value, results[i].Error = store.Read(ctx, access)
	}
	return results
}

// Variable declares a named typed initial value and its remote-write permission. Local
// acquisition uses Model.SetValues.
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

// NewModel creates an empty concurrency-safe in-memory Store. Add declarations before serving
// clients.
func NewModel() *Model {
	return &Model{variables: make(map[ObjectName]Variable), domains: make(map[string]struct{})}
}

// AddDomain registers an empty MMS domain after validating its identifier.
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

// Add validates and registers a variable. Duplicate names or incompatible initial values
// return an error.
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

// Domains returns registered domain names in sorted order and honors ctx cancellation.
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

// Names returns names in the requested MMS scope and domain. The server handles wire
// pagination.
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
	selected, indices, err := selectionPath(t, sel)
	if err != nil {
		return Value{}, TypeSpec{}, nil, err
	}
	value, err = data.ValueAt(value, indices)
	if err != nil {
		return Value{}, TypeSpec{}, nil, InvalidAddress
	}
	t = selected
	path = append(path, indices...)
	return value, t, path, nil
}

// Type resolves a named variable or structure member and returns an independent type
// declaration.
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

// Read obtains a typed immutable value, applying any requested component or array selection.
func (m *Model) Read(ctx context.Context, a VariableAccess) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.readLocked(a)
}

func (m *Model) readBatch(ctx context.Context, accesses []VariableAccess) []ReadResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	results := make([]ReadResult, len(accesses))
	for i, access := range accesses {
		if err := ctx.Err(); err != nil {
			results[i].Error = err
			continue
		}
		results[i].Value, results[i].Error = m.readLocked(access)
	}
	return results
}

func (m *Model) readLocked(a VariableAccess) (Value, error) {
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

// Write validates remote access permission and selected type before atomically changing the
// variable.
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
