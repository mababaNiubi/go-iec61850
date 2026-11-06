// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

type NamedList struct {
	Name      ObjectName
	Variables []VariableAccess
	Deletable bool
}
type DeleteResult struct{ Matched, Deleted uint32 }

func selectedType(t TypeSpec, selection Selection) (TypeSpec, error) {
	if selection.Component != "" {
		for _, part := range strings.Split(selection.Component, "$") {
			found := false
			for _, component := range t.Components {
				if component.Name == part {
					t = component.Type
					found = true
					break
				}
			}
			if !found {
				return TypeSpec{}, ObjectNonExistent
			}
		}
	}
	if selection.Index != nil {
		if t.Kind != KindArray || t.Element == nil {
			return TypeSpec{}, ObjectAccessUnsupported
		}
		if uint64(*selection.Index) >= uint64(t.Length) {
			return TypeSpec{}, InvalidAddress
		}
		t = *t.Element
	}
	return t, nil
}

func cloneAccesses(in []VariableAccess) []VariableAccess {
	out := append([]VariableAccess(nil), in...)
	for i := range out {
		if out[i].Selection.Index != nil {
			v := *out[i].Selection.Index
			out[i].Selection.Index = &v
		}
	}
	return out
}

// AddNamedList registers a VMD/domain list before or during serving.
func (s *Server) AddNamedList(list NamedList) error {
	if list.Name.Scope == AssociationScope {
		return fmt.Errorf("mms: use DefineNamedList on an association for AA lists")
	}
	if err := list.Name.validate(); err != nil {
		return err
	}
	if _, err := encodeAccesses(list.Variables); err != nil {
		return err
	}
	list.Variables = cloneAccesses(list.Variables)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lists) >= 256 {
		return fmt.Errorf("mms: named list limit reached")
	}
	if _, ok := s.lists[list.Name]; ok {
		return &ServiceError{2, 5}
	}
	s.lists[list.Name] = list
	return nil
}
func (a *Association) getList(n ObjectName) (NamedList, error) {
	var list NamedList
	var ok bool
	if n.Scope == AssociationScope {
		a.mu.Lock()
		list, ok = a.lists[n]
		a.mu.Unlock()
	} else {
		a.server.mu.Lock()
		list, ok = a.server.lists[n]
		a.server.mu.Unlock()
	}
	if !ok {
		return list, &ServiceError{7, 2}
	}
	list.Variables = cloneAccesses(list.Variables)
	return list, nil
}
func (a *Association) listNames(scope Scope, domain string) []string {
	var names []string
	if scope == AssociationScope {
		a.mu.Lock()
		defer a.mu.Unlock()
		for n := range a.lists {
			names = append(names, n.Item)
		}
	} else {
		a.server.mu.Lock()
		defer a.server.mu.Unlock()
		for n := range a.server.lists {
			if n.Scope == scope && n.Domain == domain {
				names = append(names, n.Item)
			}
		}
	}
	return names
}
func (a *Association) resolveAccess(spec ber.Element) ([]VariableAccess, error) {
	if spec.Tag == 0xa0 {
		return decodeAccesses(spec)
	}
	if spec.Tag != 0xa1 {
		return nil, ErrProtocol
	}
	e, rest, err := ber.Parse(spec.Bytes)
	if err != nil || len(rest) != 0 {
		return nil, ErrProtocol
	}
	name, err := decodeName(e)
	if err != nil {
		return nil, err
	}
	list, err := a.getList(name)
	return list.Variables, err
}
func (a *Association) listService(ctx context.Context, id uint32, e ber.Element) []byte {
	fail := func(err error) []byte { return a.failure(id, err) }
	success := func(data []byte) []byte {
		out := confirmed(0xa1, id, data)
		if len(out) > a.params.MaxPDUSize {
			return serviceFailure(id, 4, 3)
		}
		return out
	}
	switch e.Tag {
	case 0xab:
		f, err := ber.ParseAll(e.Bytes)
		if err != nil || len(f) != 2 {
			return fail(ErrProtocol)
		}
		name, err := decodeName(f[0])
		if err != nil {
			return fail(err)
		}
		vars, err := decodeAccesses(f[1])
		if err != nil {
			return fail(err)
		}
		if name.Scope == DomainScope {
			domains, err := a.server.Store.Domains(ctx)
			if err != nil {
				return fail(err)
			}
			found := false
			for _, n := range domains {
				found = found || n == name.Domain
			}
			if !found {
				return fail(&ServiceError{2, 1})
			}
		}
		for _, v := range vars {
			t, err := a.server.Store.Type(ctx, v.Name)
			if err == nil {
				_, err = selectedType(t, v.Selection)
			}
			if err != nil {
				return fail(&ServiceError{2, 1})
			}
		}
		list := NamedList{name, cloneAccesses(vars), true}
		if name.Scope == AssociationScope {
			a.mu.Lock()
			defer a.mu.Unlock()
			if _, ok := a.lists[name]; ok {
				return fail(&ServiceError{2, 5})
			}
			if len(a.lists) >= 128 {
				return fail(&ServiceError{3, 0})
			}
			a.lists[name] = list
		} else {
			if err = a.server.AddNamedList(list); err != nil {
				return fail(err)
			}
		}
		return success(ber.Encode(0x8b))
	case 0xac:
		n, rest, err := ber.Parse(e.Bytes)
		if err != nil || len(rest) != 0 {
			return fail(ErrProtocol)
		}
		name, err := decodeName(n)
		if err != nil {
			return fail(err)
		}
		list, err := a.getList(name)
		if err != nil {
			return fail(err)
		}
		vars, err := encodeAccesses(list.Variables)
		if err != nil {
			return fail(err)
		}
		seq, _, _ := ber.Parse(vars)
		flag := byte(0)
		if list.Deletable {
			flag = 0xff
		}
		return success(ber.Encode(0xac, ber.Encode(0x80, []byte{flag}), ber.Encode(0xa1, seq.Bytes)))
	case 0xad:
		f, err := ber.ParseAll(e.Bytes)
		if err != nil {
			return fail(err)
		}
		scope := uint32(0)
		var names []ObjectName
		domain := ""
		seen := byte(0)
		for _, x := range f {
			switch x.Tag {
			case 0x80:
				if seen&1 != 0 {
					return fail(ErrProtocol)
				}
				seen |= 1
				scope, err = unsigned(x)
				if err != nil || scope > 3 {
					return fail(ErrProtocol)
				}
			case 0xa1:
				if seen&2 != 0 {
					return fail(ErrProtocol)
				}
				seen |= 2
				items, err := ber.ParseAll(x.Bytes)
				if err != nil || len(items) > 4096 {
					return fail(ErrProtocol)
				}
				for _, item := range items {
					n, err := decodeName(item)
					if err != nil {
						return fail(err)
					}
					names = append(names, n)
				}
			case 0x82:
				if seen&4 != 0 {
					return fail(ErrProtocol)
				}
				seen |= 4
				domain = string(x.Bytes)
				if !validIdentifier(domain) {
					return fail(ErrProtocol)
				}
			default:
				return fail(ErrProtocol)
			}
		}
		if scope == 0 {
			if len(names) == 0 || seen&4 != 0 {
				return fail(ErrProtocol)
			}
		} else {
			if seen&2 != 0 || (scope == 2 && domain == "") || (scope != 2 && domain != "") {
				return fail(ErrProtocol)
			}
			switch scope {
			case 1:
				a.mu.Lock()
				for n := range a.lists {
					names = append(names, n)
				}
				a.mu.Unlock()
			case 2, 3:
				a.server.mu.Lock()
				for n := range a.server.lists {
					if (scope == 2 && n.Scope == DomainScope && n.Domain == domain) || (scope == 3 && n.Scope == VMDScope) {
						names = append(names, n)
					}
				}
				a.server.mu.Unlock()
			}
		}
		var result DeleteResult
		for _, n := range names {
			if n.Scope == AssociationScope {
				a.mu.Lock()
				list, ok := a.lists[n]
				if ok {
					result.Matched++
					if list.Deletable {
						delete(a.lists, n)
						result.Deleted++
					}
				}
				a.mu.Unlock()
			} else {
				a.server.mu.Lock()
				list, ok := a.server.lists[n]
				if ok {
					result.Matched++
					if list.Deletable {
						delete(a.server.lists, n)
						result.Deleted++
					}
				}
				a.server.mu.Unlock()
			}
		}
		return success(ber.Encode(0xad, ber.Uint(0x80, uint64(result.Matched)), ber.Uint(0x81, uint64(result.Deleted))))
	}
	return reject(id, 1, 1)
}

// DefineNamedList 定义按顺序访问多个变量的 MMS 命名表。AssociationName 创建
// 仅当前关联可见的 AA 表；这不配置 IEC 61850 SCL 数据集或报告控制块。
func (c *Client) DefineNamedList(ctx context.Context, n ObjectName, variables []VariableAccess) error {
	if err := n.validate(); err != nil {
		return err
	}
	vars, err := encodeAccesses(variables)
	if err != nil {
		return err
	}
	e, err := c.call(ctx, ber.Encode(0xab, encodeName(n), vars), 0x8b)
	if err != nil {
		return err
	}
	if len(e.Bytes) != 0 {
		return ErrProtocol
	}
	return nil
}

// GetNamedListAttributes 返回列表成员顺序和能否删除的声明。
func (c *Client) GetNamedListAttributes(ctx context.Context, n ObjectName) (NamedList, error) {
	if err := n.validate(); err != nil {
		return NamedList{}, err
	}
	e, err := c.call(ctx, ber.Encode(0xac, encodeName(n)), 0xac)
	if err != nil {
		return NamedList{}, err
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) != 2 || f[0].Tag != 0x80 || len(f[0].Bytes) != 1 || f[1].Tag != 0xa1 {
		return NamedList{}, ErrProtocol
	}
	f[1].Tag = 0xa0
	vars, err := decodeAccesses(f[1])
	return NamedList{n, vars, f[0].Bytes[0] != 0}, err
}

// DeleteNamedList 请求删除一个命名表，返回匹配数与实际删除数。
func (c *Client) DeleteNamedList(ctx context.Context, n ObjectName) (DeleteResult, error) {
	if err := n.validate(); err != nil {
		return DeleteResult{}, err
	}
	e, err := c.call(ctx, ber.Encode(0xad, ber.Encode(0xa1, encodeName(n))), 0xad)
	if err != nil {
		return DeleteResult{}, err
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) != 2 || f[0].Tag != 0x80 || f[1].Tag != 0x81 {
		return DeleteResult{}, ErrProtocol
	}
	matched, err := unsigned(f[0])
	if err != nil {
		return DeleteResult{}, err
	}
	deleted, err := unsigned(f[1])
	if deleted > matched {
		return DeleteResult{}, ErrProtocol
	}
	return DeleteResult{matched, deleted}, err
}

// ReadNamedList 按已定义的成员顺序读取；除外层 error，还要检查每项 Error。
func (c *Client) ReadNamedList(ctx context.Context, n ObjectName) ([]ReadResult, error) {
	if err := n.validate(); err != nil {
		return nil, err
	}
	return c.read(ctx, ber.Encode(0xa1, encodeName(n)), -1)
}

// WriteNamedList 的 values 按列表成员顺序提供，结果也须逐项检查；不是原子事务。
func (c *Client) WriteNamedList(ctx context.Context, n ObjectName, values []Value) ([]error, error) {
	if err := n.validate(); err != nil {
		return nil, err
	}
	return c.write(ctx, ber.Encode(0xa1, encodeName(n)), values)
}

func decodeReport(e ber.Element) (InformationReport, error) {
	var r InformationReport
	if e.Tag != 0xa0 {
		return r, ErrProtocol
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) != 2 || f[1].Tag != 0xa0 {
		return r, ErrProtocol
	}
	if f[0].Tag == 0xa1 {
		n, rest, err := ber.Parse(f[0].Bytes)
		if err != nil || len(rest) != 0 {
			return r, ErrProtocol
		}
		name, err := decodeName(n)
		if err != nil {
			return r, err
		}
		r.List = &name
	} else {
		r.Variables, err = decodeAccesses(f[0])
		if err != nil {
			return r, err
		}
	}
	r.Results, err = decodeResults(f[1].Bytes)
	if err == nil && r.List == nil && len(r.Variables) != len(r.Results) {
		return r, ErrProtocol
	}
	return r, err
}
func (a *Association) SendReport(ctx context.Context, r InformationReport) error {
	var spec []byte
	if r.List != nil {
		if len(r.Variables) != 0 {
			return fmt.Errorf("mms: ambiguous report specification")
		}
		if err := r.List.validate(); err != nil {
			return err
		}
		spec = ber.Encode(0xa1, encodeName(*r.List))
	} else {
		if len(r.Variables) != len(r.Results) {
			return fmt.Errorf("mms: report results count mismatch")
		}
		var err error
		spec, err = encodeAccesses(r.Variables)
		if err != nil {
			return err
		}
	}
	for _, x := range r.Results {
		if x.Error == nil && depth(x.Value) > a.params.NestingLevel {
			return fmt.Errorf("mms: report exceeds negotiated nesting")
		}
	}
	values, err := encodeResults(r.Results)
	if err != nil {
		return err
	}
	data := ber.Encode(0xa3, ber.Encode(0xa0, spec, ber.Encode(0xa0, values)))
	if len(data) > a.params.MaxPDUSize {
		return fmt.Errorf("mms: report exceeds negotiated PDU size")
	}
	ctx, cancel := context.WithTimeout(ctx, a.server.cfg.RequestTimeout)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.concluded {
		return ErrClosed
	}
	return sendPDU(ctx, a.conn, a.sendToken, data)
}
