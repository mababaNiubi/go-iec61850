// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
)

// Kind identifies a shared MMS Data choice.
type Kind = data.Kind

// Value is the shared immutable IEC 61850/MMS Data value.
type Value = data.Value

const (
	KindInvalid       = data.KindInvalid
	KindArray         = data.KindArray
	KindStructure     = data.KindStructure
	KindBoolean       = data.KindBoolean
	KindBitString     = data.KindBitString
	KindInteger       = data.KindInteger
	KindUnsigned      = data.KindUnsigned
	KindFloat         = data.KindFloat
	KindOctetString   = data.KindOctetString
	KindVisibleString = data.KindVisibleString
	KindBinaryTime    = data.KindBinaryTime
	KindMMSString     = data.KindMMSString
	KindUTC           = data.KindUTC
)

// Boolean constructs an MMS boolean.
func Boolean(v bool) Value { return data.Boolean(v) }

// Integer constructs a signed MMS integer. Validate against the remote declared width before writing.
func Integer(v int64) Value { return data.Integer(v) }

// Unsigned constructs an unsigned MMS integer without narrowing it to a signed Go value.
func Unsigned(v uint64) Value { return data.Unsigned(v) }

// Float32 constructs a 32-bit MMS floating-point value, preserving its wire width.
func Float32(v float32) Value { return data.Float32(v) }

// Float64 constructs a 64-bit MMS floating-point value, preserving its wire width.
func Float64(v float64) Value { return data.Float64(v) }

// VisibleString constructs an MMS visible string. Encoding validates its supported character set.
func VisibleString(v string) Value { return data.VisibleString(v) }

// MMSString constructs a UTF-8 MMS string. Encoding rejects invalid UTF-8.
func MMSString(v string) Value { return data.MMSString(v) }

// OctetString copies arbitrary octets into an immutable MMS value.
func OctetString(v []byte) Value { return data.OctetString(v) }

// BitString copies ASN.1 MSB-first bits. The byte count must match bits and unused final
// bits must be zero.
func BitString(v []byte, bits int) (Value, error) { return data.BitString(v, bits) }

// BinaryTime copies a four- or six-octet MMS TimeOfDay and checks its millisecond range.
func BinaryTime(v []byte) (Value, error) { return data.BinaryTime(v) }

// UTC constructs an eight-octet MMS UTC time with unspecified accuracy. Out-of-range dates
// yield an invalid Value.
func UTC(v time.Time) Value { return data.UTC(v) }

// UTCFromBytes preserves the eight raw timestamp octets, including fraction and quality.
func UTCFromBytes(v [8]byte) Value { return data.UTCFromBytes(v) }

// Array copies an ordered list of immutable values into an MMS array.
func Array(v ...Value) Value { return data.Array(v...) }

// Structure copies ordered member values. Names and declarations are retained separately in TypeSpec.
func Structure(v ...Value) Value { return data.Structure(v...) }

// EncodeValue validates and BER-encodes a value. The returned bytes belong to the caller.
func EncodeValue(v Value) ([]byte, error) { return data.EncodeValue(v) }

// DecodeValue parses a complete BER value and rejects malformed input or excessive nesting.
func DecodeValue(v []byte) (Value, error) { return data.DecodeValue(v) }
