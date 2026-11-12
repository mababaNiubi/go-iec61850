// SPDX-License-Identifier: GPL-3.0-or-later
// Wire layout references libiec61850 v1.6.2.1 sampled_values/sv_publisher.c.

// Package sv implements ordinary Ethernet sampled values. ASDU Data is a raw,
// externally configured dataset layout, not a sequence of MMS BER values.
package sv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

var (
	ErrMalformed = errors.New("sv: malformed message")
	ErrNotSV     = errors.New("sv: not a sampled-values Ethernet frame")
)

const (
	MaxAPDUSize      = 1492
	MaxASDUs         = 255
	MaxReferenceSize = 129
)

// ASDU is one sample dataset with raw seqData bytes, counter, revision and
// synchronization metadata. Optional pointer fields distinguish absence from zero.
type ASDU struct {
	SVID, DataSet string
	SampleCount   uint16
	ConfRev       uint32
	RefreshTime   *[8]byte
	SampleSync    uint8
	SampleRate    *uint16
	// SampleMode is explicitly encoded in two octets, matching the reference
	// publisher profile. It is not treated as a local constructor default.
	SampleMode *uint16
	GMIdentity *[8]byte
	Data       []byte
}

// PDU groups ASDUs carried in one SV Ethernet frame. Security is opaque wire
// data only and is not authenticated by this package.
type PDU struct {
	ASDUs []ASDU
	// Security is optional opaque wire data. It is not authenticated here.
	// nil omits the field; nonnil empty data preserves an explicit empty field.
	Security []byte
}

// Clone copies the raw sample payload and optional metadata pointers.
func (a ASDU) Clone() ASDU {
	if a.RefreshTime != nil {
		v := *a.RefreshTime
		a.RefreshTime = &v
	}
	if a.SampleRate != nil {
		v := *a.SampleRate
		a.SampleRate = &v
	}
	if a.SampleMode != nil {
		v := *a.SampleMode
		a.SampleMode = &v
	}
	if a.GMIdentity != nil {
		v := *a.GMIdentity
		a.GMIdentity = &v
	}
	a.Data = bytes.Clone(a.Data)
	return a
}

// Clone copies all ASDUs and optional opaque security bytes.
func (p PDU) Clone() PDU {
	if p.ASDUs != nil {
		out := make([]ASDU, len(p.ASDUs))
		for i, a := range p.ASDUs {
			out[i] = a.Clone()
		}
		p.ASDUs = out
	}
	p.Security = bytes.Clone(p.Security)
	return p
}
func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, args...))
}
func validReference(s string, optional bool) bool {
	if s == "" {
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
func validateASDU(a ASDU) error {
	if !validReference(a.SVID, false) || !validReference(a.DataSet, true) {
		return malformed("invalid svID or dataset reference")
	}
	if a.SampleSync > 2 {
		return malformed("unsupported sample synchronization value")
	}
	if a.SampleMode != nil && *a.SampleMode > 2 {
		return malformed("unsupported sample mode")
	}
	if len(a.Data) > MaxAPDUSize {
		return malformed("sample data exceeds APDU bound")
	}
	return nil
}
func fixed16(tag byte, v uint16) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return ber.Encode(tag, b[:])
}
func fixed32(tag byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return ber.Encode(tag, b[:])
}
func encodeASDU(a ASDU) ([]byte, error) {
	if err := validateASDU(a); err != nil {
		return nil, err
	}
	fields := [][]byte{ber.Encode(0x80, []byte(a.SVID))}
	if a.DataSet != "" {
		fields = append(fields, ber.Encode(0x81, []byte(a.DataSet)))
	}
	fields = append(fields, fixed16(0x82, a.SampleCount), fixed32(0x83, a.ConfRev))
	if a.RefreshTime != nil {
		fields = append(fields, ber.Encode(0x84, a.RefreshTime[:]))
	}
	fields = append(fields, ber.Encode(0x85, []byte{a.SampleSync}))
	if a.SampleRate != nil {
		fields = append(fields, fixed16(0x86, *a.SampleRate))
	}
	fields = append(fields, ber.Encode(0x87, a.Data))
	if a.SampleMode != nil {
		fields = append(fields, fixed16(0x88, *a.SampleMode))
	}
	if a.GMIdentity != nil {
		fields = append(fields, ber.Encode(0x89, a.GMIdentity[:]))
	}
	return ber.Encode(0x30, fields...), nil
}

// EncodePDU validates and BER-encodes one SV application PDU without an Ethernet header.
func EncodePDU(p PDU) ([]byte, error) {
	if len(p.ASDUs) < 1 || len(p.ASDUs) > MaxASDUs {
		return nil, malformed("ASDU count must be 1..%d", MaxASDUs)
	}
	if len(p.Security) > MaxAPDUSize {
		return nil, malformed("security field exceeds APDU bound")
	}
	var sequence []byte
	for _, a := range p.ASDUs {
		encoded, err := encodeASDU(a)
		if err != nil {
			return nil, err
		}
		if len(encoded) > MaxAPDUSize-len(sequence) {
			return nil, malformed("ASDUs exceed Ethernet payload bound")
		}
		sequence = append(sequence, encoded...)
	}
	fields := [][]byte{ber.Uint(0x80, uint64(len(p.ASDUs)))}
	if p.Security != nil {
		fields = append(fields, ber.Encode(0x81, p.Security))
	}
	fields = append(fields, ber.Encode(0xa2, sequence))
	encoded := ber.Encode(0x60, fields...)
	if len(encoded) > MaxAPDUSize {
		return nil, malformed("APDU exceeds Ethernet payload bound")
	}
	return encoded, nil
}
func decodeASDU(e ber.Element) (ASDU, error) {
	var a ASDU
	if e.Tag != 0x30 {
		return a, malformed("expected ASDU SEQUENCE")
	}
	previous := -1
	var seen uint16
	for content := e.Bytes; len(content) > 0; {
		f, rest, err := ber.Parse(content)
		if err != nil {
			return ASDU{}, malformed("invalid ASDU field: %v", err)
		}
		if f.Tag < 0x80 || f.Tag > 0x89 {
			return ASDU{}, malformed("unknown ASDU field %#x", f.Tag)
		}
		index := int(f.Tag - 0x80)
		if index <= previous || seen&(1<<uint(index)) != 0 {
			return ASDU{}, malformed("duplicate or out-of-order ASDU field")
		}
		previous = index
		seen |= 1 << uint(index)
		switch index {
		case 0:
			a.SVID = string(f.Bytes)
		case 1:
			a.DataSet = string(f.Bytes)
			if a.DataSet == "" {
				return ASDU{}, malformed("explicit dataset reference must not be empty")
			}
		case 2:
			if len(f.Bytes) != 2 {
				return ASDU{}, malformed("smpCnt requires two octets")
			}
			a.SampleCount = binary.BigEndian.Uint16(f.Bytes)
		case 3:
			if len(f.Bytes) != 4 {
				return ASDU{}, malformed("confRev requires four octets")
			}
			a.ConfRev = binary.BigEndian.Uint32(f.Bytes)
		case 4:
			if len(f.Bytes) != 8 {
				return ASDU{}, malformed("refrTm requires eight octets")
			}
			var v [8]byte
			copy(v[:], f.Bytes)
			a.RefreshTime = &v
		case 5:
			if len(f.Bytes) != 1 {
				return ASDU{}, malformed("smpSynch requires one octet")
			}
			a.SampleSync = f.Bytes[0]
		case 6:
			if len(f.Bytes) != 2 {
				return ASDU{}, malformed("smpRate requires two octets")
			}
			v := binary.BigEndian.Uint16(f.Bytes)
			a.SampleRate = &v
		case 7:
			a.Data = bytes.Clone(f.Bytes)
		case 8:
			if len(f.Bytes) != 2 {
				return ASDU{}, malformed("smpMod requires two octets in this profile")
			}
			v := binary.BigEndian.Uint16(f.Bytes)
			a.SampleMode = &v
		case 9:
			if len(f.Bytes) != 8 {
				return ASDU{}, malformed("gmIdentity requires eight octets")
			}
			var v [8]byte
			copy(v[:], f.Bytes)
			a.GMIdentity = &v
		}
		content = rest
	}
	const mandatory uint16 = 1<<0 | 1<<2 | 1<<3 | 1<<5 | 1<<7
	if seen&mandatory != mandatory {
		return ASDU{}, malformed("missing required ASDU field")
	}
	if err := validateASDU(a); err != nil {
		return ASDU{}, err
	}
	return a, nil
}

// DecodePDU parses a complete bounded SV application PDU, rejecting malformed or unsupported encodings.
func DecodePDU(encoded []byte) (PDU, error) {
	var p PDU
	if len(encoded) > MaxAPDUSize {
		return p, malformed("APDU exceeds Ethernet payload bound")
	}
	top, rest, err := ber.Parse(encoded)
	if err != nil || len(rest) != 0 || top.Tag != 0x60 {
		return p, malformed("expected one APPLICATION 0 APDU")
	}
	fields, err := ber.ParseAll(top.Bytes)
	if err != nil || len(fields) < 2 || len(fields) > 3 || fields[0].Tag != 0x80 {
		return p, malformed("invalid SV APDU fields")
	}
	count, err := ber.DecodeUint(fields[0].Bytes)
	if err != nil || count < 1 || count > MaxASDUs {
		return p, malformed("invalid ASDU count")
	}
	index := 1
	if len(fields) == 3 {
		if fields[1].Tag != 0x81 {
			return p, malformed("unknown or out-of-order SV security field")
		}
		p.Security = bytes.Clone(fields[1].Bytes)
		index++
	}
	if fields[index].Tag != 0xa2 {
		return PDU{}, malformed("missing or out-of-order seqASDU")
	}
	for content := fields[index].Bytes; len(content) > 0; {
		if len(p.ASDUs) >= int(count) {
			return PDU{}, malformed("more ASDUs than declared")
		}
		e, rest, err := ber.Parse(content)
		if err != nil {
			return PDU{}, malformed("malformed ASDU sequence")
		}
		a, err := decodeASDU(e)
		if err != nil {
			return PDU{}, err
		}
		p.ASDUs = append(p.ASDUs, a)
		content = rest
	}
	if len(p.ASDUs) != int(count) {
		return PDU{}, malformed("ASDU count does not match sequence")
	}
	return p, nil
}
