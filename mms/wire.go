// SPDX-License-Identifier: GPL-3.0-or-later
// Protocol layouts reference libiec61850 (Michael Zillgith), v1.6.2.1.
package mms

import (
	"fmt"
	"math"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

type Scope uint8

const (
	VMDScope Scope = iota
	DomainScope
	AssociationScope
)

// ObjectName retains the MMS naming scope. Domain is used only in DomainScope.
type ObjectName struct {
	Scope        Scope
	Domain, Item string
}

func DomainName(domain, item string) ObjectName {
	return ObjectName{Scope: DomainScope, Domain: domain, Item: item}
}
func VMDName(item string) ObjectName         { return ObjectName{Scope: VMDScope, Item: item} }
func AssociationName(item string) ObjectName { return ObjectName{Scope: AssociationScope, Item: item} }
func (n ObjectName) String() string {
	if n.Scope == DomainScope {
		return n.Domain + "/" + n.Item
	}
	return n.Item
}
func validIdentifier(s string) bool {
	return data.ValidIdentifier(s)
}
func (n ObjectName) validate() error {
	if n.Scope > AssociationScope || !validIdentifier(n.Item) || (n.Scope == DomainScope && !validIdentifier(n.Domain)) || (n.Scope != DomainScope && n.Domain != "") {
		return fmt.Errorf("mms: invalid object name %q", n.String())
	}
	return nil
}
func encodeName(n ObjectName) []byte {
	if n.Scope == DomainScope {
		return ber.Encode(0xa1, ber.Encode(0x1a, []byte(n.Domain)), ber.Encode(0x1a, []byte(n.Item)))
	}
	return ber.Encode(0x80+byte(n.Scope), []byte(n.Item))
}
func decodeName(e ber.Element) (ObjectName, error) {
	var n ObjectName
	switch e.Tag {
	case 0x80:
		n = VMDName(string(e.Bytes))
	case 0x82:
		n = AssociationName(string(e.Bytes))
	case 0xa1:
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil || len(fields) != 2 || fields[0].Tag != 0x1a || fields[1].Tag != 0x1a {
			return n, ErrProtocol
		}
		n = DomainName(string(fields[0].Bytes), string(fields[1].Bytes))
	default:
		return n, ErrProtocol
	}
	return n, n.validate()
}
func single(data []byte, tag byte) (ber.Element, error) {
	e, rest, err := ber.Parse(data)
	if err != nil {
		return e, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if len(rest) != 0 || e.Tag != tag {
		return e, ErrProtocol
	}
	return e, nil
}
func unsigned(e ber.Element) (uint32, error) {
	v, err := ber.DecodeUint(e.Bytes)
	if err != nil || v > math.MaxUint32 {
		return 0, ErrProtocol
	}
	return uint32(v), nil
}

func localParameters(c Config) Parameters {
	p := Parameters{MaxPDUSize: c.MaxPDUSize, MaxOutstandingCalling: c.MaxOutstandingCalling, MaxOutstandingCalled: c.MaxOutstandingCalled, NestingLevel: c.NestingLevel}
	// status, directory, identify, read, write, attributes; named variable lists;
	// InformationReport and Conclude. No file/journal/cancel capabilities.
	p.Services = [11]byte{0xee, 0x1c, 0, 0, 0, 0, 0, 0, 0, 0x01, 0x10}
	p.ParameterCBB = [2]byte{0xf1, 0}
	return p
}
func encodeInitiate(tag byte, p Parameters) []byte {
	return ber.Encode(tag, ber.Uint(0x80, uint64(p.MaxPDUSize)), ber.Uint(0x81, uint64(p.MaxOutstandingCalling)), ber.Uint(0x82, uint64(p.MaxOutstandingCalled)), ber.Uint(0x83, uint64(p.NestingLevel)), ber.Encode(0xa4, ber.Uint(0x80, 1), ber.Encode(0x81, append([]byte{5}, p.ParameterCBB[:]...)), ber.Encode(0x82, append([]byte{3}, p.Services[:]...))))
}
func decodeInitiate(data []byte, tag byte) (Parameters, error) {
	p := Parameters{MaxPDUSize: 65000, NestingLevel: 16}
	top, err := single(data, tag)
	if err != nil {
		return p, err
	}
	fields, err := ber.ParseAll(top.Bytes)
	if err != nil {
		return p, err
	}
	var seen byte
	for _, f := range fields {
		if f.Tag >= 0x80 && f.Tag <= 0x83 {
			bit := byte(1 << (f.Tag - 0x80))
			if seen&bit != 0 {
				return p, ErrProtocol
			}
			seen |= bit
			v, err := unsigned(f)
			if err != nil || v == 0 {
				return p, ErrProtocol
			}
			switch f.Tag {
			case 0x80:
				p.MaxPDUSize = int(v)
			case 0x81:
				p.MaxOutstandingCalling = int(v)
			case 0x82:
				p.MaxOutstandingCalled = int(v)
			case 0x83:
				p.NestingLevel = int(v)
			}
		} else if f.Tag == 0xa4 {
			if seen&16 != 0 {
				return p, ErrProtocol
			}
			seen |= 16
			detail, err := ber.ParseAll(f.Bytes)
			if err != nil {
				return p, err
			}
			var ds byte
			for _, d := range detail {
				if d.Tag < 0x80 || d.Tag > 0x82 {
					return p, ErrProtocol
				}
				bit := byte(1 << (d.Tag - 0x80))
				if ds&bit != 0 {
					return p, ErrProtocol
				}
				ds |= bit
				switch d.Tag {
				case 0x80:
					v, e := unsigned(d)
					if e != nil || v != 1 {
						return p, ErrProtocol
					}
				case 0x81:
					if len(d.Bytes) != 3 || d.Bytes[0] != 5 || d.Bytes[2]&0x1f != 0 {
						return p, ErrProtocol
					}
					copy(p.ParameterCBB[:], d.Bytes[1:])
				case 0x82:
					if len(d.Bytes) != 12 || d.Bytes[0] != 3 || d.Bytes[11]&0x07 != 0 {
						return p, ErrProtocol
					}
					copy(p.Services[:], d.Bytes[1:])
				}
			}
			if ds != 7 {
				return p, ErrProtocol
			}
		} else {
			return p, ErrProtocol
		}
	}
	if seen&22 != 22 || p.MaxPDUSize < 128 || p.MaxOutstandingCalling > 1024 || p.MaxOutstandingCalled > 1024 || p.NestingLevel > 64 {
		return p, ErrProtocol
	}
	return p, nil
}
func negotiate(local, peer Parameters) Parameters {
	p := local
	p.MaxPDUSize = min(p.MaxPDUSize, peer.MaxPDUSize)
	p.MaxOutstandingCalling = min(p.MaxOutstandingCalling, peer.MaxOutstandingCalling)
	p.MaxOutstandingCalled = min(p.MaxOutstandingCalled, peer.MaxOutstandingCalled)
	p.NestingLevel = min(p.NestingLevel, peer.NestingLevel)
	for i := range p.ParameterCBB {
		p.ParameterCBB[i] &= peer.ParameterCBB[i]
	}
	return p
}
func confirmed(tag byte, id uint32, service []byte) []byte {
	return ber.Encode(tag, ber.Uint(0x02, uint64(id)), service)
}
func serviceFailure(id uint32, class, code int) []byte {
	return ber.Encode(0xa2, ber.Uint(0x80, uint64(id)), ber.Encode(0xa2, ber.Encode(0xa0, ber.Uint(0x80+byte(class), uint64(code)))))
}
func reject(id uint32, class, reason int) []byte {
	return ber.Encode(0xa4, ber.Uint(0x80, uint64(id)), ber.Uint(0x80+byte(class), uint64(reason)))
}

type envelope struct {
	tag     byte
	id      uint32
	service ber.Element
	err     error
}

func decodeEnvelope(data []byte) (envelope, error) {
	var m envelope
	e, rest, err := ber.Parse(data)
	if err != nil || len(rest) != 0 {
		return m, ErrProtocol
	}
	m.tag = e.Tag
	if e.Tag == 0x8b || e.Tag == 0x8c {
		if len(e.Bytes) != 0 {
			return m, ErrProtocol
		}
		return m, nil
	}
	if e.Tag == 0xa3 {
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil || len(fields) != 1 {
			return m, ErrProtocol
		}
		m.service = fields[0]
		return m, nil
	}
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil || len(fields) < 2 {
		return m, ErrProtocol
	}
	wantID := byte(0x02)
	if e.Tag == 0xa2 || e.Tag == 0xa4 {
		wantID = 0x80
	}
	if fields[0].Tag != wantID {
		return m, ErrProtocol
	}
	m.id, err = unsigned(fields[0])
	if err != nil {
		return m, err
	}
	switch e.Tag {
	case 0xa0, 0xa1:
		if len(fields) != 2 {
			return m, ErrProtocol
		}
		m.service = fields[1]
	case 0xa2:
		if fields[1].Tag != 0xa2 {
			return m, ErrProtocol
		}
		se, err := ber.ParseAll(fields[1].Bytes)
		if err != nil || len(se) == 0 || se[0].Tag != 0xa0 {
			return m, ErrProtocol
		}
		ec, rest, err := ber.Parse(se[0].Bytes)
		if err != nil || len(rest) != 0 || ec.Tag < 0x80 || ec.Tag > 0x8c {
			return m, ErrProtocol
		}
		code, err := unsigned(ec)
		if err != nil {
			return m, err
		}
		m.err = &ServiceError{int(ec.Tag - 0x80), int(code)}
	case 0xa4:
		if fields[1].Tag < 0x81 || fields[1].Tag > 0x8b {
			return m, ErrProtocol
		}
		reason, err := unsigned(fields[1])
		if err != nil {
			return m, err
		}
		m.err = &RejectError{int(fields[1].Tag - 0x80), int(reason)}
	default:
		return m, ErrProtocol
	}
	return m, nil
}

// VariableAccess selects a whole variable. AlternateAccess may select a member
// or array element; a zero Selection selects the complete value.
type VariableAccess struct {
	Name      ObjectName
	Selection Selection
}
type Selection struct {
	Component string
	Index     *uint32
}

func Access(n ObjectName) VariableAccess { return VariableAccess{Name: n} }
func Element(n ObjectName, index uint32) VariableAccess {
	return VariableAccess{Name: n, Selection: Selection{Index: &index}}
}
func Member(n ObjectName, component string) VariableAccess {
	return VariableAccess{Name: n, Selection: Selection{Component: component}}
}
func encodeAccesses(accesses []VariableAccess) ([]byte, error) {
	if len(accesses) == 0 || len(accesses) > 4096 {
		return nil, fmt.Errorf("mms: invalid variable count")
	}
	var items [][]byte
	for _, a := range accesses {
		if err := a.Name.validate(); err != nil {
			return nil, err
		}
		fields := [][]byte{ber.Encode(0xa0, encodeName(a.Name))}
		if a.Selection.Component != "" && a.Selection.Index != nil {
			return nil, fmt.Errorf("mms: only one alternate selector is supported")
		}
		if a.Selection.Component != "" {
			if !validIdentifier(a.Selection.Component) {
				return nil, ErrProtocol
			}
			fields = append(fields, ber.Encode(0xa5, ber.Encode(0x81, []byte(a.Selection.Component))))
		} else if a.Selection.Index != nil {
			fields = append(fields, ber.Encode(0xa5, ber.Uint(0x82, uint64(*a.Selection.Index))))
		}
		items = append(items, ber.Encode(0x30, fields...))
	}
	return ber.Encode(0xa0, items...), nil
}
func decodeAccesses(e ber.Element) ([]VariableAccess, error) {
	if e.Tag != 0xa0 {
		return nil, ObjectAccessUnsupported
	}
	items, err := ber.ParseAll(e.Bytes)
	if err != nil || len(items) == 0 || len(items) > 4096 {
		return nil, ErrProtocol
	}
	out := make([]VariableAccess, 0, len(items))
	for _, item := range items {
		if item.Tag != 0x30 {
			return nil, ErrProtocol
		}
		fields, err := ber.ParseAll(item.Bytes)
		if err != nil || len(fields) < 1 || len(fields) > 2 || fields[0].Tag != 0xa0 {
			return nil, ErrProtocol
		}
		n, rest, err := ber.Parse(fields[0].Bytes)
		if err != nil || len(rest) != 0 {
			return nil, ErrProtocol
		}
		name, err := decodeName(n)
		if err != nil {
			return nil, err
		}
		a := Access(name)
		if len(fields) == 2 {
			if fields[1].Tag != 0xa5 {
				return nil, ErrProtocol
			}
			sel, rest, err := ber.Parse(fields[1].Bytes)
			if err != nil || len(rest) != 0 {
				return nil, ObjectAccessUnsupported
			}
			switch sel.Tag {
			case 0x81:
				a.Selection.Component = string(sel.Bytes)
				if !validIdentifier(a.Selection.Component) {
					return nil, ErrProtocol
				}
			case 0x82:
				v, err := unsigned(sel)
				if err != nil {
					return nil, err
				}
				a.Selection.Index = &v
			default:
				return nil, ObjectAccessUnsupported
			}
		}
		out = append(out, a)
	}
	return out, nil
}
