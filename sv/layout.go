// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
)

// Field describes one scalar in seqData. Basic is the original SCL bType:
// MMS TypeSpec alone cannot distinguish an SV Enum/Dbpos/Quality wire width.
// Offsets are absolute within seqData, paths relative to the owning FCDA.
type Field struct {
	Path, Basic   string
	Offset, Width int
}

// Member retains one ordered FCDA and its scalar field layout.
type Member struct {
	Name, Domain string
	Fields       []Field
}

// Layout describes contiguous seqData fields in dataset order. Size and all field
// offsets/widths are byte counts.
type Layout struct {
	Members []Member
	Size    int
}

var basicWidths = map[string]int{"BOOLEAN": 1, "INT8": 1, "INT16": 2, "INT24": 3, "INT32": 4, "INT64": 8,
	"INT8U": 1, "INT16U": 2, "INT24U": 3, "INT32U": 4, "INT64U": 8, "FLOAT32": 4, "FLOAT64": 8,
	"Enum": 4, "Dbpos": 4, "Quality": 4, "Check": 4, "Timestamp": 8}

// BasicWidth returns the SV byte width for a supported SCL basic type. Unsupported types
// return an error; MMS type width is not a substitute.
func BasicWidth(basic string) (int, error) {
	width := basicWidths[basic]
	if width == 0 {
		return 0, fmt.Errorf("sv: unsupported SCL bType %q", basic)
	}
	return width, nil
}

// Validate checks nonempty members, unique field paths, contiguous offsets, supported
// widths and the total seqData byte size.
func (l Layout) Validate() error {
	if len(l.Members) == 0 || l.Size < 1 || l.Size > MaxAPDUSize {
		return fmt.Errorf("sv: invalid dataset layout size")
	}
	offset := 0
	for _, member := range l.Members {
		if member.Name == "" || len(member.Fields) == 0 {
			return fmt.Errorf("sv: empty dataset member")
		}
		seen := map[string]bool{}
		for _, field := range member.Fields {
			width, err := BasicWidth(field.Basic)
			if err != nil {
				return err
			}
			if seen[field.Path] || field.Offset != offset || field.Width != width {
				return fmt.Errorf("sv: duplicate field or noncontiguous layout at %s", field.Path)
			}
			seen[field.Path] = true
			offset += width
		}
	}
	if offset != l.Size {
		return fmt.Errorf("sv: dataset size does not match its fields")
	}
	return nil
}

// Field selects an FCDA by its MMS item or zero-based index and a scalar path.
// Duplicate item names require an index to avoid a cross-domain ambiguity.
func (l Layout) Field(memberRef, path string) (Field, error) {
	index, err := strconv.Atoi(memberRef)
	if err != nil {
		index = -1
		for i, member := range l.Members {
			if member.Name == memberRef {
				if index >= 0 {
					return Field{}, fmt.Errorf("sv: ambiguous FCDA %q; select an index", memberRef)
				}
				index = i
			}
		}
	}
	if index < 0 || index >= len(l.Members) {
		return Field{}, fmt.Errorf("sv: unknown FCDA %q", memberRef)
	}
	for _, field := range l.Members[index].Fields {
		if field.Path == path {
			return field, nil
		}
	}
	return Field{}, fmt.Errorf("sv: FCDA %q has no scalar path %q", memberRef, path)
}

func fieldName(member Member, field Field) string {
	if field.Path == "" {
		return member.Name
	}
	return member.Name + "$" + strings.ReplaceAll(field.Path, ".", "$")
}

// QualityFor searches the closest enclosing data object's q in the same
// logical-device relationship. It never borrows another ASDU's quality.
func (l Layout) QualityFor(field Field) (*Field, error) {
	if field.Basic == "Quality" {
		return nil, nil
	}
	var name, domain string
	for _, member := range l.Members {
		for _, f := range member.Fields {
			if f == field {
				name = fieldName(member, f)
				domain = member.Domain
			}
		}
	}
	for strings.Count(name, "$") > 2 {
		name = name[:strings.LastIndexByte(name, '$')]
		var quality *Field
		for _, member := range l.Members {
			for _, f := range member.Fields {
				if member.Domain == domain && f.Basic == "Quality" && fieldName(member, f) == name+"$q" {
					if quality != nil {
						return nil, fmt.Errorf("sv: ambiguous quality; select it explicitly")
					}
					f := f
					quality = &f
				}
			}
		}
		if quality != nil {
			return quality, nil
		}
	}
	return nil, nil
}

// Compatible reports exact ordered layout equality, including member/domain names, fields
// and byte size.
func (l Layout) Compatible(other Layout) bool {
	return l.Size == other.Size && reflect.DeepEqual(l.Members, other.Members)
}

// Clone returns an independent copy of the members and field slices.
func (l Layout) Clone() Layout {
	members := make([]Member, len(l.Members))
	for i, m := range l.Members {
		m.Fields = append([]Field(nil), m.Fields...)
		members[i] = m
	}
	return Layout{Members: members, Size: l.Size}
}

// Decode preserves signedness, integer precision, Float32 width, raw quality
// bits and UTC quality bytes. Engineering scaling belongs to the application.
func (f Field) Decode(raw []byte) (data.Value, error) {
	width, err := BasicWidth(f.Basic)
	if err != nil {
		return data.Value{}, err
	}
	if f.Width != width || f.Offset < 0 || f.Offset > len(raw)-width {
		return data.Value{}, fmt.Errorf("sv: field outside seqData")
	}
	b := raw[f.Offset : f.Offset+width]
	var u uint64
	for _, octet := range b {
		u = u<<8 | uint64(octet)
	}
	switch f.Basic {
	case "BOOLEAN":
		if u > 1 {
			return data.Value{}, fmt.Errorf("sv: invalid BOOLEAN")
		}
		return data.Boolean(u == 1), nil
	case "INT8", "INT16", "INT24", "INT32", "INT64", "Enum":
		shift := 64 - 8*width
		return data.Integer(int64(u<<shift) >> shift), nil
	case "INT8U", "INT16U", "INT24U", "INT32U", "INT64U", "Quality", "Check":
		return data.Unsigned(u), nil
	case "Dbpos":
		if u > 3 {
			return data.Value{}, fmt.Errorf("sv: invalid Dbpos")
		}
		return data.Unsigned(u), nil
	case "FLOAT32":
		v := math.Float32frombits(uint32(u))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return data.Value{}, fmt.Errorf("sv: non-finite float")
		}
		return data.Float32(v), nil
	case "FLOAT64":
		v := math.Float64frombits(u)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return data.Value{}, fmt.Errorf("sv: non-finite float")
		}
		return data.Float64(v), nil
	case "Timestamp":
		return data.UTCFromBytes([8]byte(b)), nil
	}
	return data.Value{}, fmt.Errorf("sv: unsupported field")
}

// Encode writes one field after validating it; adjacent fields are preserved.
func (f Field) Encode(raw []byte, value data.Value) error {
	width, err := BasicWidth(f.Basic)
	if err != nil {
		return err
	}
	if f.Width != width || f.Offset < 0 || f.Offset > len(raw)-width {
		return fmt.Errorf("sv: field outside seqData")
	}
	var u uint64
	switch f.Basic {
	case "BOOLEAN":
		v, e := value.Bool()
		if e != nil {
			return e
		}
		if v {
			u = 1
		}
	case "INT8", "INT16", "INT24", "INT32", "INT64", "Enum":
		v, e := value.Int()
		if e != nil {
			return e
		}
		bits := width * 8
		if bits < 64 && (v < -(int64(1)<<uint(bits-1)) || v > (int64(1)<<uint(bits-1))-1) {
			return fmt.Errorf("sv: signed value exceeds field width")
		}
		u = uint64(v)
	case "INT8U", "INT16U", "INT24U", "INT32U", "INT64U", "Dbpos", "Quality", "Check":
		u, err = value.Uint()
		if err != nil {
			return err
		}
		if width < 8 && u>>uint(width*8) != 0 || f.Basic == "Dbpos" && u > 3 {
			return fmt.Errorf("sv: unsigned value exceeds field width")
		}
	case "FLOAT32", "FLOAT64":
		v, e := value.Float()
		if e != nil {
			return e
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("sv: non-finite float")
		}
		if width == 4 {
			n := float32(v)
			if math.IsInf(float64(n), 0) {
				return fmt.Errorf("sv: Float32 overflow")
			}
			u = uint64(math.Float32bits(n))
		} else {
			u = math.Float64bits(v)
		}
	case "Timestamp":
		if value.Kind() != data.KindUTC {
			return fmt.Errorf("sv: expected UTC timestamp")
		}
		b, _ := value.Bytes()
		if len(b) != 8 {
			return fmt.Errorf("sv: invalid timestamp")
		}
		copy(raw[f.Offset:f.Offset+8], b)
		return nil
	default:
		return fmt.Errorf("sv: unsupported field")
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], u)
	copy(raw[f.Offset:f.Offset+width], encoded[8-width:])
	return nil
}
