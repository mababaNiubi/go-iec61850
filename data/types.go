// SPDX-License-Identifier: GPL-3.0-or-later
// MMS TypeSpecification layout follows libiec61850 v1.6.2.1 ASN.1 definitions.
package data

import (
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

// Component is an ordered structure member. Name may be empty when the remote
// peer supplies an unnamed component.
type Component struct {
	Name string
	Type TypeSpec
}

// TypeSpec describes an MMS variable independently of its current Value.
// Width and ExponentWidth are bit counts. Length is an array element count,
// a BinaryTime length of 4/6 octets, or a string/bit-string length. For strings
// and bit strings, a positive Length is fixed and a negative Length is a maximum;
// zero permits only an empty value. MMSString length counts Unicode characters.
// Array Element and ordered structure Components carry recursive descriptions.
type TypeSpec struct {
	Kind          Kind
	Width         int
	ExponentWidth int
	Length        int
	Element       *TypeSpec
	Components    []Component
}

// TypeDepth counts scalar types as one level, and arrays/structures as one
// additional level above their deepest child. Invalid, cyclic or excessively
// complex descriptions return 65, exceeding every supported negotiated limit.
func TypeDepth(t TypeSpec) int {
	if err := ValidateType(t); err != nil {
		return ber.MaxDepth + 1
	}
	return typeDepth(t)
}

func typeDepth(t TypeSpec) int {
	d := 1
	if t.Kind == KindArray {
		return 1 + typeDepth(*t.Element)
	}
	if t.Kind == KindStructure {
		for _, c := range t.Components {
			d = max(d, 1+typeDepth(c.Type))
		}
	}
	return d
}

// TypeOf infers a type from a value. Integers use 64 bits. Structure members are
// unnamed, and array types use the first element's type. Applications should
// declare their own TypeSpec when widths, variable lengths or names matter.
func TypeOf(v Value) TypeSpec {
	budget := ber.MaxElements
	return typeOf(v, 0, &budget)
}

func typeOf(v Value, depth int, budget *int) TypeSpec {
	if depth >= ber.MaxDepth || *budget <= 0 {
		return TypeSpec{}
	}
	*budget--
	t := TypeSpec{Kind: v.kind}
	switch v.kind {
	case KindInteger, KindUnsigned:
		t.Width = 64
	case KindFloat:
		t.Width = (len(v.raw) - 1) * 8
		if len(v.raw) != 0 {
			t.ExponentWidth = int(v.raw[0])
		}
	case KindBitString:
		t.Length = v.bitLength
	case KindOctetString, KindVisibleString, KindBinaryTime:
		t.Length = len(v.raw)
	case KindMMSString:
		t.Length = utf8.RuneCount(v.raw)
	case KindArray:
		t.Length = len(v.elements)
		if len(v.elements) > 0 {
			child := typeOf(v.elements[0], depth+1, budget)
			t.Element = &child
		}
	case KindStructure:
		t.Components = make([]Component, len(v.elements))
		for i, child := range v.elements {
			t.Components[i].Type = typeOf(child, depth+1, budget)
		}
	}
	return t
}

// ValidateType rejects types the implementation cannot faithfully represent.
// In particular, it rejects pointer cycles and excessively nested descriptors.
func ValidateType(t TypeSpec) error {
	budget := ber.MaxElements
	return validateType(t, 0, &budget)
}

func validateType(t TypeSpec, depth int, budget *int) error {
	if depth >= ber.MaxDepth || *budget <= 0 {
		return errors.New("mms: type nesting limit exceeded")
	}
	*budget--
	switch t.Kind {
	case KindBoolean, KindUTC:
		return nil
	case KindInteger, KindUnsigned:
		if t.Width < 1 || t.Width > 64 {
			return errors.New("mms: integer width must be 1..64")
		}
	case KindFloat:
		if !((t.Width == 32 && (t.ExponentWidth == 0 || t.ExponentWidth == 8)) || (t.Width == 64 && (t.ExponentWidth == 0 || t.ExponentWidth == 11))) {
			return errors.New("mms: only IEEE float32/float64 types are supported")
		}
	case KindBinaryTime:
		if t.Length != 4 && t.Length != 6 {
			return errors.New("mms: BinaryTime type requires length 4 or 6")
		}
	case KindBitString, KindOctetString, KindVisibleString, KindMMSString:
		if int64(t.Length) < math.MinInt32 || int64(t.Length) > math.MaxInt32 {
			return errors.New("mms: type length exceeds Integer32")
		}
	case KindArray:
		if t.Length < 0 || uint64(t.Length) > math.MaxUint32 || t.Element == nil {
			return errors.New("mms: array type requires count and element type")
		}
		return validateType(*t.Element, depth+1, budget)
	case KindStructure:
		if len(t.Components) > ber.MaxElements {
			return errors.New("mms: too many structure components")
		}
		for _, c := range t.Components {
			if c.Name != "" && !ValidIdentifier(c.Name) {
				return errors.New("mms: component name must be 1..64 printable ASCII octets")
			}
			if err := validateType(c.Type, depth+1, budget); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("mms: unsupported type kind %d", t.Kind)
	}
	return nil
}

// ValidateValue checks kind, width, lengths and all composite children against
// the declared type. It does not mutate the value or coerce types.
func ValidateValue(t TypeSpec, v Value) error {
	if err := ValidateType(t); err != nil {
		return err
	}
	return validateValue(t, v, 0)
}

func validateValue(t TypeSpec, v Value, depth int) error {
	if depth >= ber.MaxDepth {
		return errors.New("mms: value nesting limit exceeded")
	}
	if t.Kind != v.kind {
		return fmt.Errorf("mms: expected %s, got %s", t.Kind, v.kind)
	}
	switch t.Kind {
	case KindInteger:
		if t.Width < 64 {
			limit := int64(1) << (t.Width - 1)
			if v.integer < -limit || v.integer >= limit {
				return errors.New("mms: signed integer outside declared width")
			}
		}
	case KindUnsigned:
		if t.Width < 64 && v.unsigned >= uint64(1)<<t.Width {
			return errors.New("mms: unsigned integer outside declared width")
		}
	case KindFloat:
		if _, err := v.Float(); err != nil {
			return err
		}
		if (len(v.raw)-1)*8 != t.Width {
			return errors.New("mms: float width mismatch")
		}
	case KindBitString:
		return validateLength(t.Length, v.bitLength)
	case KindOctetString:
		return validateLength(t.Length, len(v.raw))
	case KindVisibleString:
		if !visible(v.raw) {
			return errors.New("mms: invalid VisibleString")
		}
		return validateLength(t.Length, len(v.raw))
	case KindMMSString:
		if !utf8.Valid(v.raw) {
			return errors.New("mms: invalid MMSString")
		}
		return validateLength(t.Length, utf8.RuneCount(v.raw))
	case KindBinaryTime:
		if t.Length != len(v.raw) {
			return errors.New("mms: BinaryTime width mismatch")
		}
	case KindArray:
		if t.Length != len(v.elements) {
			return errors.New("mms: array element count mismatch")
		}
		for _, e := range v.elements {
			if err := validateValue(*t.Element, e, depth+1); err != nil {
				return err
			}
		}
	case KindStructure:
		if len(t.Components) != len(v.elements) {
			return errors.New("mms: structure component count mismatch")
		}
		for i, c := range t.Components {
			if err := validateValue(c.Type, v.elements[i], depth+1); err != nil {
				return fmt.Errorf("mms: component %q: %w", c.Name, err)
			}
		}
	}
	return nil
}

func validateLength(declared, actual int) error {
	if (declared >= 0 && declared != actual) || (declared < 0 && int64(actual) > -int64(declared)) {
		return fmt.Errorf("mms: value length %d does not satisfy declared length %d", actual, declared)
	}
	return nil
}

// EncodeType encodes the MMS TypeSpecification choice, without a service wrapper.
func EncodeType(t TypeSpec) ([]byte, error) {
	if err := ValidateType(t); err != nil {
		return nil, err
	}
	return encodeType(t, 0)
}

func encodeType(t TypeSpec, depth int) ([]byte, error) {
	if depth >= ber.MaxDepth {
		return nil, errors.New("mms: type nesting limit exceeded")
	}
	tag := byte(t.Kind) | 0x80
	switch t.Kind {
	case KindBoolean, KindUTC:
		return ber.Encode(tag), nil
	case KindInteger, KindUnsigned:
		return ber.Uint(tag, uint64(t.Width)), nil
	case KindBitString, KindOctetString, KindVisibleString, KindMMSString:
		return ber.Int(tag, int64(t.Length)), nil
	case KindBinaryTime:
		if t.Length == 6 {
			return ber.Encode(tag, []byte{0xff}), nil
		}
		return ber.Encode(tag, []byte{0}), nil
	case KindFloat:
		exponent := t.ExponentWidth
		if exponent == 0 {
			if t.Width == 32 {
				exponent = 8
			} else {
				exponent = 11
			}
		}
		return ber.Encode(0xa7, ber.Uint(0x02, uint64(t.Width)), ber.Uint(0x02, uint64(exponent))), nil
	case KindArray:
		child, err := encodeType(*t.Element, depth+1)
		if err != nil {
			return nil, err
		}
		return ber.Encode(0xa1, ber.Uint(0x81, uint64(t.Length)), ber.Encode(0xa2, child)), nil
	case KindStructure:
		var components []byte
		for _, c := range t.Components {
			child, err := encodeType(c.Type, depth+1)
			if err != nil {
				return nil, err
			}
			var name []byte
			if c.Name != "" {
				name = ber.Encode(0x80, []byte(c.Name))
			}
			components = append(components, ber.Encode(0x30, name, ber.Encode(0xa1, child))...)
			if len(components) > ber.MaxElementSize {
				return nil, errors.New("mms: type size limit exceeded")
			}
		}
		return ber.Encode(0xa2, ber.Encode(0xa1, components)), nil
	default:
		return nil, errors.New("mms: unsupported type")
	}
}

// DecodeType reads exactly one MMS TypeSpecification choice.
func DecodeType(b []byte) (TypeSpec, error) {
	e, rest, err := ber.Parse(b)
	if err != nil {
		return TypeSpec{}, err
	}
	if len(rest) != 0 {
		return TypeSpec{}, errors.New("mms: trailing TypeSpecification content")
	}
	budget := ber.MaxElements
	t, err := decodeType(e, 0, &budget)
	if err != nil {
		return TypeSpec{}, err
	}
	if err = ValidateType(t); err != nil {
		return TypeSpec{}, err
	}
	return t, nil
}

func decodeType(e ber.Element, depth int, budget *int) (TypeSpec, error) {
	if depth >= ber.MaxDepth || *budget <= 0 {
		return TypeSpec{}, errors.New("mms: type complexity limit exceeded")
	}
	*budget--
	t := TypeSpec{Kind: Kind(e.Tag & 0x1f)}
	switch e.Tag {
	case 0x83, 0x91:
		if len(e.Bytes) != 0 {
			return TypeSpec{}, errors.New("mms: nonempty NULL type")
		}
	case 0x85, 0x86:
		n, err := ber.DecodeUint(e.Bytes)
		if err != nil || n > 64 || n == 0 {
			return TypeSpec{}, errors.New("mms: unsupported integer width")
		}
		t.Width = int(n)
	case 0x84, 0x89, 0x8a, 0x90:
		n, err := ber.DecodeInt(e.Bytes)
		if err != nil || n < math.MinInt32 || n > math.MaxInt32 {
			return TypeSpec{}, errors.New("mms: invalid type length")
		}
		t.Length = int(n)
	case 0x8c:
		if len(e.Bytes) != 1 {
			return TypeSpec{}, errors.New("mms: invalid BinaryTime type")
		}
		t.Length = 4
		if e.Bytes[0] != 0 {
			t.Length = 6
		}
	case 0xa7:
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil {
			return TypeSpec{}, err
		}
		if len(fields) != 2 || fields[0].Tag != 0x02 || fields[1].Tag != 0x02 {
			return TypeSpec{}, errors.New("mms: invalid float type")
		}
		width, err := ber.DecodeUint(fields[0].Bytes)
		if err != nil || width > 255 {
			return TypeSpec{}, errors.New("mms: invalid float width")
		}
		exponent, err := ber.DecodeUint(fields[1].Bytes)
		if err != nil || exponent > 255 {
			return TypeSpec{}, errors.New("mms: invalid exponent width")
		}
		// Zero is a local TypeSpec constructor shorthand, not an ASN.1
		// default. An explicit zero exponent on the wire describes a format
		// this implementation cannot represent and must not be normalized.
		if !((width == 32 && exponent == 8) || (width == 64 && exponent == 11)) {
			return TypeSpec{}, errors.New("mms: unsupported floating-point type format")
		}
		t.Width, t.ExponentWidth = int(width), int(exponent)
	case 0xa1:
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil {
			return TypeSpec{}, err
		}
		fields, err = unpackType(fields)
		if err != nil {
			return TypeSpec{}, err
		}
		if len(fields) != 2 || fields[0].Tag != 0x81 || fields[1].Tag != 0xa2 {
			return TypeSpec{}, errors.New("mms: invalid array type")
		}
		count, err := ber.DecodeUint(fields[0].Bytes)
		if err != nil || count > math.MaxUint32 {
			return TypeSpec{}, errors.New("mms: invalid array element count")
		}
		child, rest, err := ber.Parse(fields[1].Bytes)
		if err != nil || len(rest) != 0 {
			return TypeSpec{}, errors.New("mms: invalid array element type wrapper")
		}
		elementType, err := decodeType(child, depth+1, budget)
		if err != nil {
			return TypeSpec{}, err
		}
		t.Length, t.Element = int(count), &elementType
	case 0xa2:
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil {
			return TypeSpec{}, err
		}
		fields, err = unpackType(fields)
		if err != nil {
			return TypeSpec{}, err
		}
		if len(fields) != 1 || fields[0].Tag != 0xa1 {
			return TypeSpec{}, errors.New("mms: invalid structure type")
		}
		members, err := ber.ParseAll(fields[0].Bytes)
		if err != nil {
			return TypeSpec{}, err
		}
		for _, member := range members {
			if member.Tag != 0x30 {
				return TypeSpec{}, errors.New("mms: invalid component sequence")
			}
			parts, err := ber.ParseAll(member.Bytes)
			if err != nil {
				return TypeSpec{}, err
			}
			var name string
			if len(parts) > 0 && parts[0].Tag == 0x80 {
				name = string(parts[0].Bytes)
				if !ValidIdentifier(name) {
					return TypeSpec{}, errors.New("mms: invalid component identifier")
				}
				parts = parts[1:]
			}
			if len(parts) != 1 || parts[0].Tag != 0xa1 {
				return TypeSpec{}, errors.New("mms: invalid component type wrapper")
			}
			child, rest, err := ber.Parse(parts[0].Bytes)
			if err != nil || len(rest) != 0 {
				return TypeSpec{}, errors.New("mms: invalid component type")
			}
			childType, err := decodeType(child, depth+1, budget)
			if err != nil {
				return TypeSpec{}, err
			}
			t.Components = append(t.Components, Component{Name: name, Type: childType})
		}
	default:
		return TypeSpec{}, fmt.Errorf("mms: unsupported TypeSpecification tag %#x", e.Tag)
	}
	return t, nil
}

func unpackType(fields []ber.Element) ([]ber.Element, error) {
	if len(fields) > 0 && fields[0].Tag == 0x80 {
		if len(fields[0].Bytes) != 1 || fields[0].Bytes[0] != 0 {
			return nil, errors.New("mms: packed composite types are unsupported")
		}
		return fields[1:], nil
	}
	return fields, nil
}
