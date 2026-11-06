// SPDX-License-Identifier: GPL-3.0-or-later
// The wire layout references libiec61850 v1.6.2.1, src/goose/iec61850_goose.asn.

// Package goose implements ordinary Ethernet GOOSE encoding and state handling.
package goose

import (
	"errors"
	"fmt"
	"math"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

var (
	// ErrMalformed marks invalid or unsupported GOOSE wire content.
	ErrMalformed = errors.New("goose: malformed message")
	// ErrNotGOOSE distinguishes a different Ethernet protocol from bad GOOSE.
	ErrNotGOOSE = errors.New("goose: not a GOOSE Ethernet frame")
)

const (
	// MaxAPDUSize leaves eight octets for the GOOSE header within a 1500-octet
	// Ethernet payload. Ordinary GOOSE does not fragment an oversized APDU.
	MaxAPDUSize       = 1492
	MaxDataSetEntries = 1024
	// MaxReferenceSize matches the reference receiver's object-reference buffers.
	// It is not the shorter MMS Identifier bound used for individual components.
	MaxReferenceSize = 129
)

// PDU is the ordinary IECGoosePdu. Timestamp preserves all eight UTC octets,
// including fractional time and quality. TimeAllowedToLive is milliseconds.
// An empty GoID omits that optional field. Values retain dataset order.
type PDU struct {
	GoCBRef, DataSet, GoID               string
	TimeAllowedToLive                    uint32
	Timestamp                            [8]byte
	StateNumber, SequenceNumber, ConfRev uint32
	Simulation, NeedsCommission          bool
	Values                               []data.Value
}

// Clone copies the mutable dataset slice. Each data.Value is immutable.
func (p PDU) Clone() PDU {
	p.Values = append([]data.Value(nil), p.Values...)
	return p
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, args...))
}

func validReference(s string, optional bool) bool {
	if len(s) == 0 {
		return optional
	}
	if len(s) > MaxReferenceSize {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func validatePDU(p PDU) error {
	if !validReference(p.GoCBRef, false) || !validReference(p.DataSet, false) || !validReference(p.GoID, true) {
		return malformed("references must be 1..%d printable ASCII octets (GoID may be omitted)", MaxReferenceSize)
	}
	if p.TimeAllowedToLive == 0 || p.StateNumber == 0 {
		return malformed("TAL and state number must be nonzero")
	}
	if len(p.Values) > MaxDataSetEntries {
		return malformed("too many dataset entries")
	}
	for i, v := range p.Values {
		if v.Depth() > ber.MaxDepth {
			return malformed("dataset entry %d exceeds complexity limit", i)
		}
	}
	return nil
}

func boolField(tag byte, value bool) []byte {
	b := byte(0)
	if value {
		b = 0xff
	}
	return ber.Encode(tag, []byte{b})
}

// EncodePDU encodes one GOOSE APDU, without the eight-octet GOOSE header or
// Ethernet header. Both optional default booleans are emitted explicitly.
func EncodePDU(p PDU) ([]byte, error) {
	if err := validatePDU(p); err != nil {
		return nil, err
	}
	var allData []byte
	for i, v := range p.Values {
		encoded, err := data.EncodeValue(v)
		if err != nil {
			return nil, malformed("dataset entry %d: %v", i, err)
		}
		if len(encoded) > MaxAPDUSize-len(allData) {
			return nil, malformed("dataset exceeds Ethernet payload limit")
		}
		allData = append(allData, encoded...)
	}
	fields := [][]byte{
		ber.Encode(0x80, []byte(p.GoCBRef)),
		ber.Uint(0x81, uint64(p.TimeAllowedToLive)),
		ber.Encode(0x82, []byte(p.DataSet)),
	}
	if p.GoID != "" {
		fields = append(fields, ber.Encode(0x83, []byte(p.GoID)))
	}
	fields = append(fields,
		ber.Encode(0x84, p.Timestamp[:]),
		ber.Uint(0x85, uint64(p.StateNumber)),
		ber.Uint(0x86, uint64(p.SequenceNumber)),
		boolField(0x87, p.Simulation),
		ber.Uint(0x88, uint64(p.ConfRev)),
		boolField(0x89, p.NeedsCommission),
		ber.Uint(0x8a, uint64(len(p.Values))),
		ber.Encode(0xab, allData),
	)
	encoded := ber.Encode(0x61, fields...)
	if len(encoded) > MaxAPDUSize {
		return nil, malformed("APDU exceeds Ethernet payload limit")
	}
	return encoded, nil
}

func decodeUint32(e ber.Element) (uint32, error) {
	v, err := ber.DecodeUint(e.Bytes)
	if err != nil || v > math.MaxUint32 {
		return 0, malformed("invalid uint32 field %#x", e.Tag)
	}
	return uint32(v), nil
}

func decodeBoolean(e ber.Element) (bool, error) {
	if len(e.Bytes) != 1 {
		return false, malformed("boolean field %#x must contain one octet", e.Tag)
	}
	return e.Bytes[0] != 0, nil
}

// DecodePDU consumes exactly one APDU. It validates all mandatory fields,
// dataset counts and limits before returning a value. Unknown extensions are
// rejected rather than silently interpreted as ordinary unsecured GOOSE.
func DecodePDU(encoded []byte) (PDU, error) {
	var p PDU
	if len(encoded) > MaxAPDUSize {
		return p, malformed("APDU exceeds Ethernet payload limit")
	}
	top, rest, err := ber.Parse(encoded)
	if err != nil || len(rest) != 0 || top.Tag != 0x61 {
		return p, malformed("expected one APPLICATION 1 APDU")
	}
	var seen uint16
	previous := -1
	var count uint32
	var allData []byte
	for content := top.Bytes; len(content) != 0; {
		field, rest, err := ber.Parse(content)
		if err != nil {
			return PDU{}, malformed("invalid APDU field: %v", err)
		}
		index := -1
		if field.Tag >= 0x80 && field.Tag <= 0x8a {
			index = int(field.Tag - 0x80)
		}
		if field.Tag == 0xab {
			index = 11
		}
		if index < 0 {
			return PDU{}, malformed("unknown APDU field %#x", field.Tag)
		}
		bit := uint16(1) << uint(index)
		if seen&bit != 0 || index <= previous {
			return PDU{}, malformed("duplicate or out-of-order field %#x", field.Tag)
		}
		seen |= bit
		previous = index
		switch index {
		case 0:
			p.GoCBRef = string(field.Bytes)
		case 1:
			p.TimeAllowedToLive, err = decodeUint32(field)
		case 2:
			p.DataSet = string(field.Bytes)
		case 3:
			p.GoID = string(field.Bytes)
			if p.GoID == "" {
				return PDU{}, malformed("explicit GoID must not be empty")
			}
		case 4:
			if len(field.Bytes) != len(p.Timestamp) {
				return PDU{}, malformed("UTC timestamp must contain eight octets")
			}
			copy(p.Timestamp[:], field.Bytes)
		case 5:
			p.StateNumber, err = decodeUint32(field)
		case 6:
			p.SequenceNumber, err = decodeUint32(field)
		case 7:
			p.Simulation, err = decodeBoolean(field)
		case 8:
			p.ConfRev, err = decodeUint32(field)
		case 9:
			p.NeedsCommission, err = decodeBoolean(field)
		case 10:
			count, err = decodeUint32(field)
			if err == nil && count > MaxDataSetEntries {
				err = malformed("too many dataset entries")
			}
		case 11:
			allData = field.Bytes
		}
		if err != nil {
			return PDU{}, err
		}
		content = rest
	}
	const optional = uint16(1<<3 | 1<<7 | 1<<9)
	const mandatory = uint16(0x0fff) &^ optional
	if seen&mandatory != mandatory {
		return PDU{}, malformed("missing mandatory APDU field")
	}
	for len(allData) != 0 {
		if len(p.Values) >= int(count) {
			return PDU{}, malformed("allData contains more entries than declared")
		}
		entry, rest, err := ber.Parse(allData)
		if err != nil {
			return PDU{}, malformed("invalid allData entry: %v", err)
		}
		value, err := data.DecodeValue(ber.Encode(entry.Tag, entry.Bytes))
		if err != nil {
			return PDU{}, malformed("unsupported or invalid allData entry: %v", err)
		}
		p.Values = append(p.Values, value)
		allData = rest
	}
	if len(p.Values) != int(count) {
		return PDU{}, malformed("allData entry count does not match declaration")
	}
	if err := validatePDU(p); err != nil {
		return PDU{}, err
	}
	return p, nil
}
