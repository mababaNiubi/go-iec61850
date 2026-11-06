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

func Boolean(v bool) Value                        { return data.Boolean(v) }
func Integer(v int64) Value                       { return data.Integer(v) }
func Unsigned(v uint64) Value                     { return data.Unsigned(v) }
func Float32(v float32) Value                     { return data.Float32(v) }
func Float64(v float64) Value                     { return data.Float64(v) }
func VisibleString(v string) Value                { return data.VisibleString(v) }
func MMSString(v string) Value                    { return data.MMSString(v) }
func OctetString(v []byte) Value                  { return data.OctetString(v) }
func BitString(v []byte, bits int) (Value, error) { return data.BitString(v, bits) }
func BinaryTime(v []byte) (Value, error)          { return data.BinaryTime(v) }
func UTC(v time.Time) Value                       { return data.UTC(v) }
func UTCFromBytes(v [8]byte) Value                { return data.UTCFromBytes(v) }
func Array(v ...Value) Value                      { return data.Array(v...) }
func Structure(v ...Value) Value                  { return data.Structure(v...) }
func EncodeValue(v Value) ([]byte, error)         { return data.EncodeValue(v) }
func DecodeValue(v []byte) (Value, error)         { return data.DecodeValue(v) }
