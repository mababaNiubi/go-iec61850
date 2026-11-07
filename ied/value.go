// SPDX-License-Identifier: GPL-3.0-or-later
// Package ied provides IEC 61850 client semantics above the raw MMS services.
// It owns report dispatch on its MMS association. Values remain data.Value;
// application-specific scalar, engineering-unit and quality policies stay with
// the caller.
package ied

import (
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// ObjectName maps a relative IEC reference (XCBR1.Pos.stVal) and functional
// constraint (ST) to an MMS name. It validates syntax, not device existence.
func ObjectName(domain, fc, reference string) (mms.ObjectName, error) {
	parts := strings.Split(reference, ".")
	valid := func(s string) bool {
		if s == "" {
			return false
		}
		for _, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
		return true
	}
	if !valid(domain) || len(fc) != 2 || fc[0] < 'A' || fc[0] > 'Z' || fc[1] < 'A' || fc[1] > 'Z' || len(parts) < 2 {
		return mms.ObjectName{}, fmt.Errorf("ied: invalid domain, FC or reference")
	}
	for _, part := range parts {
		if !valid(part) {
			return mms.ObjectName{}, fmt.Errorf("ied: invalid reference %q", reference)
		}
	}
	item := parts[0] + "$" + fc + "$" + strings.Join(parts[1:], "$")
	if !data.ValidIdentifier(domain) || !data.ValidIdentifier(item) {
		return mms.ObjectName{}, fmt.Errorf("ied: reference exceeds MMS identifier limit")
	}
	return mms.DomainName(domain, item), nil
}

// Quality uses IEC flag numbers in its low 13 bits, as in an SV Quality word.
// Good validity alone does not mean that a value is suitable for every use:
// callers must also decide how to handle test, substituted and old data.
type Quality uint32

const (
	ValidityMask    Quality = 3
	Overflow        Quality = 1 << 2
	OutOfRange      Quality = 1 << 3
	BadReference    Quality = 1 << 4
	Oscillatory     Quality = 1 << 5
	Failure         Quality = 1 << 6
	OldData         Quality = 1 << 7
	Inconsistent    Quality = 1 << 8
	Inaccurate      Quality = 1 << 9
	Substituted     Quality = 1 << 10
	Test            Quality = 1 << 11
	OperatorBlocked Quality = 1 << 12
)

// DecodeQuality converts the MMS/GOOSE BIT STRING packing to IEC flag numbers.
func DecodeQuality(value data.Value) (Quality, error) {
	bits, err := value.BitLength()
	if err != nil || bits != 13 {
		return 0, fmt.Errorf("ied: expected 13-bit Quality")
	}
	raw, err := value.Bytes()
	if err != nil || len(raw) != 2 {
		return 0, fmt.Errorf("ied: invalid Quality bytes")
	}
	var q Quality
	for i := 0; i < 13; i++ {
		if raw[i/8]&(0x80>>uint(i%8)) != 0 {
			q |= 1 << uint(i)
		}
	}
	return q, nil
}

func (q Quality) GoodValidity() bool     { return q&ValidityMask == 0 }
func (q Quality) Has(flags Quality) bool { return q&flags != 0 }
func (q Quality) Valid() bool            { return q & ^Quality(0x1fff) == 0 }

// MMSValue is also suitable for GOOSE allData; SV uses uint32(q) directly.
func (q Quality) MMSValue() (data.Value, error) {
	if !q.Valid() {
		return data.Value{}, fmt.Errorf("ied: reserved Quality flags")
	}
	raw := []byte{0, 0}
	for i := 0; i < 13; i++ {
		if q&(1<<uint(i)) != 0 {
			raw[i/8] |= 0x80 >> uint(i%8)
		}
	}
	return data.BitString(raw, 13)
}
