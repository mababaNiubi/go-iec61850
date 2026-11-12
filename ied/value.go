// SPDX-License-Identifier: GPL-3.0-or-later
// Package ied provides IEC 61850 control, reporting, setting-group and online
// discovery clients above MMS. It requires no SCL file. Use ObjectName to map
// a domain, functional constraint and relative IEC reference to an MMS name.
//
// Dial creates an owned association; defer Client.Close on success. Each
// request uses its own context. SubscribeURCB and SubscribeBRCB use that context
// for setup only; stop their lifetime with Stop, Close or Client.Close. Client
// owns the raw MMS report channel; applications consume subscription Events.
//
// Discover inspects online declarations. ReadDataSet obtains current values;
// snapshots have no EntryID and do not advance a retained report cursor. Save a
// BRCB EntryID only after durably processing its complete Report. ControlResult
// records protocol acceptance and termination, not physical device feedback.
//
// Values remain data.Value. Engineering-unit conversion, quality policies,
// retry decisions and persistence belong to the application. See the runnable
// examples in this package and the repository docs/api guides.
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

// GoodValidity checks only the validity bits. Inspect old-data, test, substitution and other
// flags for application suitability.
func (q Quality) GoodValidity() bool { return q&ValidityMask == 0 }

// Has reports whether any of the supplied quality flags are set.
func (q Quality) Has(flags Quality) bool { return q&flags != 0 }

// Valid reports whether the quality contains only the defined 13 IEC bits.
func (q Quality) Valid() bool { return q & ^Quality(0x1fff) == 0 }

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
