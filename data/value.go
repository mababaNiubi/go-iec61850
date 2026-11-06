// SPDX-License-Identifier: GPL-3.0-or-later
// MMS tags and representations are based on libiec61850 v1.6.2.1.
package data

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

// Kind identifies the MMS Data choice. The zero value is invalid.
type Kind byte

const (
	KindInvalid       Kind = 0
	KindArray         Kind = 1
	KindStructure     Kind = 2
	KindBoolean       Kind = 3
	KindBitString     Kind = 4
	KindInteger       Kind = 5
	KindUnsigned      Kind = 6
	KindFloat         Kind = 7
	KindOctetString   Kind = 9
	KindVisibleString Kind = 10
	KindBinaryTime    Kind = 12
	KindMMSString     Kind = 16
	KindUTC           Kind = 17
)

func (k Kind) String() string {
	switch k {
	case KindArray:
		return "array"
	case KindStructure:
		return "structure"
	case KindBoolean:
		return "boolean"
	case KindBitString:
		return "bit-string"
	case KindInteger:
		return "integer"
	case KindUnsigned:
		return "unsigned"
	case KindFloat:
		return "float"
	case KindOctetString:
		return "octet-string"
	case KindVisibleString:
		return "visible-string"
	case KindBinaryTime:
		return "binary-time"
	case KindMMSString:
		return "mms-string"
	case KindUTC:
		return "utc-time"
	default:
		return "invalid"
	}
}

// Value is an immutable MMS Data value. Constructors and accessors copy slices,
// so values may be shared across goroutines. TypeSpec stores declared widths and
// structure member names separately from the values carried by MMS Data.
type Value struct {
	kind      Kind
	boolean   bool
	integer   int64
	unsigned  uint64
	raw       []byte
	elements  []Value
	bitLength int
}

func Boolean(v bool) Value         { return Value{kind: KindBoolean, boolean: v} }
func Integer(v int64) Value        { return Value{kind: KindInteger, integer: v} }
func Unsigned(v uint64) Value      { return Value{kind: KindUnsigned, unsigned: v} }
func VisibleString(v string) Value { return Value{kind: KindVisibleString, raw: []byte(v)} }
func MMSString(v string) Value     { return Value{kind: KindMMSString, raw: []byte(v)} }
func OctetString(v []byte) Value   { return Value{kind: KindOctetString, raw: bytes.Clone(v)} }
func Array(v ...Value) Value       { return Value{kind: KindArray, elements: append([]Value(nil), v...)} }
func Structure(v ...Value) Value {
	return Value{kind: KindStructure, elements: append([]Value(nil), v...)}
}

func Float32(v float32) Value {
	raw := make([]byte, 5)
	raw[0] = 8
	binary.BigEndian.PutUint32(raw[1:], math.Float32bits(v))
	return Value{kind: KindFloat, raw: raw}
}

func Float64(v float64) Value {
	raw := make([]byte, 9)
	raw[0] = 11
	binary.BigEndian.PutUint64(raw[1:], math.Float64bits(v))
	return Value{kind: KindFloat, raw: raw}
}

// BitString uses ASN.1 bit order: the most significant bit is the first bit.
// Padding bits in the final octet must be zero.
func BitString(v []byte, bitLength int) (Value, error) {
	if bitLength < 0 || bitLength > len(v)*8 || (bitLength+7)/8 != len(v) {
		return Value{}, errors.New("mms: bit-string length does not match content")
	}
	unused := len(v)*8 - bitLength
	if unused != 0 && v[len(v)-1]&byte((1<<unused)-1) != 0 {
		return Value{}, errors.New("mms: nonzero unused bit-string bits")
	}
	return Value{kind: KindBitString, raw: bytes.Clone(v), bitLength: bitLength}, nil
}

// BinaryTime preserves the four- or six-octet MMS TimeOfDay representation:
// milliseconds since midnight, optionally followed by days since 1984-01-01.
func BinaryTime(v []byte) (Value, error) {
	if len(v) != 4 && len(v) != 6 {
		return Value{}, errors.New("mms: binary-time must contain 4 or 6 octets")
	}
	if binary.BigEndian.Uint32(v[:4]) >= 86400000 {
		return Value{}, errors.New("mms: binary-time milliseconds outside one day")
	}
	return Value{kind: KindBinaryTime, raw: bytes.Clone(v)}, nil
}

// UTC converts a time into the eight-octet MMS UTC representation. Its quality
// octet is 0x1f (unspecified accuracy); callers with known device quality
// should use UTCFromBytes. Dates outside the uint32 Unix-seconds range produce
// an invalid Value, rejected by EncodeValue and ValidateValue.
func UTC(t time.Time) Value {
	seconds := t.Unix()
	if seconds < 0 || seconds > math.MaxUint32 {
		return Value{}
	}
	var raw [8]byte
	binary.BigEndian.PutUint32(raw[:4], uint32(seconds))
	fraction := uint32((uint64(t.Nanosecond()) << 24) / 1000000000)
	raw[4], raw[5], raw[6], raw[7] = byte(fraction>>16), byte(fraction>>8), byte(fraction), 0x1f
	return UTCFromBytes(raw)
}

// UTCFromBytes retains fraction and time-quality bits without normalization.
func UTCFromBytes(v [8]byte) Value { return Value{kind: KindUTC, raw: bytes.Clone(v[:])} }

func (v Value) Kind() Kind { return v.kind }

// Depth counts a scalar or an empty composite as one level. Values exceeding
// 64 levels or 65,536 visited nodes return 65, above every supported negotiated
// nesting limit. The bounded traversal does not copy the immutable children.
func (v Value) Depth() int {
	nodes := 0
	var walk func(Value, int) int
	walk = func(value Value, level int) int {
		nodes++
		if level > ber.MaxDepth || nodes > ber.MaxElements {
			return ber.MaxDepth + 1
		}
		depth := level
		for _, child := range value.elements {
			depth = max(depth, walk(child, level+1))
			if depth > ber.MaxDepth {
				return depth
			}
		}
		return depth
	}
	return walk(v, 1)
}

func (v Value) wrongKind(want string) error {
	return fmt.Errorf("mms: %s value cannot be read as %s", v.kind, want)
}

func (v Value) Bool() (bool, error) {
	if v.kind != KindBoolean {
		return false, v.wrongKind("boolean")
	}
	return v.boolean, nil
}
func (v Value) Int() (int64, error) {
	if v.kind != KindInteger {
		return 0, v.wrongKind("integer")
	}
	return v.integer, nil
}
func (v Value) Uint() (uint64, error) {
	if v.kind != KindUnsigned {
		return 0, v.wrongKind("unsigned")
	}
	return v.unsigned, nil
}
func (v Value) Float() (float64, error) {
	if v.kind != KindFloat {
		return 0, v.wrongKind("float")
	}
	if len(v.raw) == 5 && v.raw[0] == 8 {
		return float64(math.Float32frombits(binary.BigEndian.Uint32(v.raw[1:]))), nil
	}
	if len(v.raw) == 9 && v.raw[0] == 11 {
		return math.Float64frombits(binary.BigEndian.Uint64(v.raw[1:])), nil
	}
	return 0, errors.New("mms: unsupported floating-point format")
}
func (v Value) StringValue() (string, error) {
	if v.kind != KindVisibleString && v.kind != KindMMSString {
		return "", v.wrongKind("string")
	}
	return string(v.raw), nil
}

// Bytes returns a copy of the raw payload. Float payloads include the exponent
// width octet; bit-string payloads exclude the BER unused-bit count octet.
func (v Value) Bytes() ([]byte, error) {
	switch v.kind {
	case KindBitString, KindOctetString, KindVisibleString, KindMMSString, KindFloat, KindBinaryTime, KindUTC:
		return bytes.Clone(v.raw), nil
	default:
		return nil, v.wrongKind("bytes")
	}
}
func (v Value) Elements() ([]Value, error) {
	if v.kind != KindArray && v.kind != KindStructure {
		return nil, v.wrongKind("elements")
	}
	return append([]Value(nil), v.elements...), nil
}
func (v Value) BitLength() (int, error) {
	if v.kind != KindBitString {
		return 0, v.wrongKind("bit length")
	}
	return v.bitLength, nil
}

// Time converts UTC or dated BinaryTime. Bytes retains the original wire data,
// including UTC quality and fractions more precise than nanoseconds.
func (v Value) Time() (time.Time, error) {
	if v.kind == KindUTC && len(v.raw) == 8 {
		fraction := uint64(v.raw[4])<<16 | uint64(v.raw[5])<<8 | uint64(v.raw[6])
		return time.Unix(int64(binary.BigEndian.Uint32(v.raw[:4])), int64(fraction*1000000000>>24)).UTC(), nil
	}
	if v.kind == KindBinaryTime && len(v.raw) == 6 {
		epoch := time.Date(1984, 1, 1, 0, 0, 0, 0, time.UTC)
		return epoch.Add(time.Duration(binary.BigEndian.Uint16(v.raw[4:]))*24*time.Hour + time.Duration(binary.BigEndian.Uint32(v.raw[:4]))*time.Millisecond), nil
	}
	return time.Time{}, v.wrongKind("absolute time")
}

// Equal compares exact protocol content, including float bit patterns and UTC
// quality, rather than approximate numeric equality.
func (v Value) Equal(other Value) bool {
	if v.kind != other.kind || v.boolean != other.boolean || v.integer != other.integer || v.unsigned != other.unsigned || v.bitLength != other.bitLength || !bytes.Equal(v.raw, other.raw) || len(v.elements) != len(other.elements) {
		return false
	}
	for i := range v.elements {
		if !v.elements[i].Equal(other.elements[i]) {
			return false
		}
	}
	return true
}

func (v Value) String() string {
	switch v.kind {
	case KindBoolean:
		return strconv.FormatBool(v.boolean)
	case KindInteger:
		return strconv.FormatInt(v.integer, 10)
	case KindUnsigned:
		return strconv.FormatUint(v.unsigned, 10)
	case KindFloat:
		f, _ := v.Float()
		return strconv.FormatFloat(f, 'g', -1, 64)
	case KindVisibleString, KindMMSString:
		return string(v.raw)
	case KindArray, KindStructure:
		return fmt.Sprint(v.elements)
	case KindInvalid:
		return "<invalid>"
	default:
		return fmt.Sprintf("%s(%x)", v.kind, v.raw)
	}
}

// EncodeValue encodes one MMS Data choice.
func EncodeValue(v Value) ([]byte, error) {
	budget := ber.MaxElements
	return encodeValue(v, 0, &budget)
}

func encodeValue(v Value, depth int, budget *int) ([]byte, error) {
	if depth >= ber.MaxDepth || *budget <= 0 {
		return nil, errors.New("mms: value complexity limit exceeded")
	}
	*budget--
	if len(v.raw) > ber.MaxElementSize || len(v.elements) > ber.MaxElements {
		return nil, errors.New("mms: value size limit exceeded")
	}
	tag := byte(v.kind) | 0x80
	switch v.kind {
	case KindBoolean:
		if v.boolean {
			return ber.Encode(tag, []byte{0xff}), nil
		}
		return ber.Encode(tag, []byte{0}), nil
	case KindInteger:
		return ber.Int(tag, v.integer), nil
	case KindUnsigned:
		return ber.Uint(tag, v.unsigned), nil
	case KindBitString:
		// The unused-bit-count octet is part of the BER content length.
		if len(v.raw) >= ber.MaxElementSize {
			return nil, errors.New("mms: bit-string payload size limit exceeded")
		}
		return ber.Encode(tag, []byte{byte(len(v.raw)*8 - v.bitLength)}, v.raw), nil
	case KindFloat:
		if _, err := v.Float(); err != nil {
			return nil, err
		}
		return ber.Encode(tag, v.raw), nil
	case KindOctetString, KindMMSString, KindVisibleString, KindUTC, KindBinaryTime:
		if v.kind == KindMMSString && !utf8.Valid(v.raw) {
			return nil, errors.New("mms: invalid UTF-8 MMSString")
		}
		if v.kind == KindVisibleString && !visible(v.raw) {
			return nil, errors.New("mms: VisibleString outside printable ASCII")
		}
		return ber.Encode(tag, v.raw), nil
	case KindArray, KindStructure:
		var content []byte
		for _, element := range v.elements {
			encoded, err := encodeValue(element, depth+1, budget)
			if err != nil {
				return nil, err
			}
			content = append(content, encoded...)
			if len(content) > ber.MaxElementSize {
				return nil, errors.New("mms: value size limit exceeded")
			}
		}
		return ber.Encode(tag|0x20, content), nil
	default:
		return nil, fmt.Errorf("mms: unsupported Data kind %d", v.kind)
	}
}

func visible(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// DecodeValue requires exactly one complete Data value, with no trailing bytes.
func DecodeValue(b []byte) (Value, error) {
	e, rest, err := ber.Parse(b)
	if err != nil {
		return Value{}, err
	}
	if len(rest) != 0 {
		return Value{}, errors.New("mms: trailing Data content")
	}
	budget := ber.MaxElements
	return decodeValue(e, 0, &budget)
}

func decodeValue(e ber.Element, depth int, budget *int) (Value, error) {
	if depth >= ber.MaxDepth || *budget <= 0 {
		return Value{}, errors.New("mms: value complexity limit exceeded")
	}
	*budget--
	switch e.Tag {
	case 0xa1, 0xa2:
		var values []Value
		content := e.Bytes
		for len(content) != 0 {
			child, rest, err := ber.Parse(content)
			if err != nil {
				return Value{}, err
			}
			value, err := decodeValue(child, depth+1, budget)
			if err != nil {
				return Value{}, err
			}
			values = append(values, value)
			content = rest
		}
		if e.Tag == 0xa1 {
			return Array(values...), nil
		}
		return Structure(values...), nil
	case 0x83:
		if len(e.Bytes) != 1 {
			return Value{}, errors.New("mms: boolean requires one octet")
		}
		return Boolean(e.Bytes[0] != 0), nil
	case 0x84:
		if len(e.Bytes) == 0 || e.Bytes[0] > 7 || (len(e.Bytes) == 1 && e.Bytes[0] != 0) {
			return Value{}, errors.New("mms: invalid bit-string unused bits")
		}
		return BitString(e.Bytes[1:], (len(e.Bytes)-1)*8-int(e.Bytes[0]))
	case 0x85:
		n, err := ber.DecodeInt(e.Bytes)
		return Integer(n), err
	case 0x86:
		n, err := ber.DecodeUint(e.Bytes)
		return Unsigned(n), err
	case 0x87:
		v := Value{kind: KindFloat, raw: bytes.Clone(e.Bytes)}
		if _, err := v.Float(); err != nil {
			return Value{}, err
		}
		return v, nil
	case 0x89:
		return OctetString(e.Bytes), nil
	case 0x8a:
		if !visible(e.Bytes) {
			return Value{}, errors.New("mms: invalid VisibleString")
		}
		return VisibleString(string(e.Bytes)), nil
	case 0x8c:
		return BinaryTime(e.Bytes)
	case 0x90:
		if !utf8.Valid(e.Bytes) {
			return Value{}, errors.New("mms: invalid MMSString UTF-8")
		}
		return MMSString(string(e.Bytes)), nil
	case 0x91:
		if len(e.Bytes) != 8 {
			return Value{}, errors.New("mms: UTC time requires eight octets")
		}
		var raw [8]byte
		copy(raw[:], e.Bytes)
		return UTCFromBytes(raw), nil
	default:
		return Value{}, fmt.Errorf("mms: unsupported Data tag %#x", e.Tag)
	}
}
