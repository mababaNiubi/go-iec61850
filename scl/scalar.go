// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Convert an SCL basic type into a declared MMS type and its initial value.
// In particular Dbpos is two bits, whereas a DPC Oper.ctlVal is BOOLEAN.
func (c *compiler) scalar(a attribute, raw string) (typedValue, error) {
	s := strings.TrimSpace(raw)
	var t mms.TypeSpec
	var v mms.Value
	var err error
	switch a.Basic {
	case "BOOLEAN":
		t.Kind = mms.KindBoolean
		switch s {
		case "", "false", "0":
			v = mms.Boolean(false)
		case "true", "1":
			v = mms.Boolean(true)
		default:
			err = fmt.Errorf("invalid boolean %q", raw)
		}
	case "INT8", "INT16", "INT24", "INT32", "INT64", "INT8U", "INT16U", "INT24U", "INT32U":
		t.Width, _ = strconv.Atoi(strings.TrimSuffix(a.Basic[3:], "U"))
		if s == "" {
			s = "0"
		}
		if strings.HasSuffix(a.Basic, "U") {
			t.Kind = mms.KindUnsigned
			var n uint64
			n, err = strconv.ParseUint(s, 10, t.Width)
			v = mms.Unsigned(n)
		} else {
			t.Kind = mms.KindInteger
			var n int64
			n, err = strconv.ParseInt(s, 10, t.Width)
			v = mms.Integer(n)
		}
	case "FLOAT32", "FLOAT64":
		t.Kind = mms.KindFloat
		t.Width, _ = strconv.Atoi(a.Basic[5:])
		if s == "" {
			s = "0"
		}
		var f float64
		f, err = strconv.ParseFloat(s, t.Width)
		if t.Width == 32 {
			t.ExponentWidth = 8
			v = mms.Float32(float32(f))
		} else {
			t.ExponentWidth = 11
			v = mms.Float64(f)
		}
	case "Enum":
		t = mms.TypeSpec{Kind: mms.KindInteger, Width: 8}
		var ord int
		ord, err = c.enum(a.Type, s)
		v = mms.Integer(int64(ord))
	case "Dbpos", "Quality", "Check", "Tcmd":
		t.Kind = mms.KindBitString
		bits := 2
		t.Length = 2
		if a.Basic == "Quality" {
			bits = 13
			t.Length = -13
		}
		if a.Basic == "Check" {
			t.Length = -2
		}
		if a.Basic == "Dbpos" {
			if b, ok := map[string]string{"intermediate-state": "00", "off": "01", "on": "10", "bad-state": "11"}[s]; ok {
				s = b
			}
		}
		v, err = bitValue(bits, s)
	case "Timestamp":
		t.Kind = mms.KindUTC
		v = mms.UTCFromBytes([8]byte{})
		if s != "" {
			var tm time.Time
			tm, err = time.Parse(time.RFC3339Nano, s)
			v = mms.UTC(tm)
		}
	case "ObjRef":
		t = mms.TypeSpec{Kind: mms.KindVisibleString, Length: -129}
		v = mms.VisibleString(raw)
	case "OptFlds", "TrgOps":
		width := 10
		if a.Basic == "TrgOps" {
			width = 6
		}
		t = mms.TypeSpec{Kind: mms.KindBitString, Length: -width}
		v, err = bitValue(width, s)
	case "EntryTime":
		t = mms.TypeSpec{Kind: mms.KindBinaryTime, Length: 6}
		if s == "" {
			v, err = mms.BinaryTime(make([]byte, 6))
		} else if tm, e := time.Parse(time.RFC3339Nano, s); e == nil {
			v = binaryTime(tm)
		} else {
			var b []byte
			b, err = hex.DecodeString(strings.Join(strings.Fields(s), ""))
			if err == nil {
				v, err = mms.BinaryTime(b)
			}
		}
	case "VisString32", "VisString64", "VisString65", "VisString129", "VisString255", "Unicode255":
		if a.Basic == "Unicode255" {
			t = mms.TypeSpec{Kind: mms.KindMMSString, Length: -255}
			v = mms.MMSString(raw)
		} else {
			n, _ := strconv.Atoi(a.Basic[9:])
			t = mms.TypeSpec{Kind: mms.KindVisibleString, Length: -n}
			v = mms.VisibleString(raw)
		}
	case "Octet6", "Octet8", "Octet64", "EntryID":
		n := 8
		if a.Basic != "EntryID" {
			n, _ = strconv.Atoi(a.Basic[5:])
		}
		t = mms.TypeSpec{Kind: mms.KindOctetString, Length: -n}
		var b []byte
		b, err = hex.DecodeString(strings.Join(strings.Fields(s), ""))
		if n == 8 {
			t.Length = 8
			if s == "" {
				b = make([]byte, 8)
			}
		}
		v = mms.OctetString(b)
	default:
		return typedValue{}, fmt.Errorf("unsupported bType %q", a.Basic)
	}
	if err == nil {
		err = mms.ValidateValue(t, v)
	}
	if err != nil {
		return typedValue{}, fmt.Errorf("%s initial value %q: %w", a.Basic, raw, err)
	}
	return typedValue{t, v}, nil
}

type enumValues struct {
	labels   map[string]int
	ordinals map[int]bool
}

func compileEnum(t enumType) (enumValues, error) {
	v := enumValues{labels: make(map[string]int), ordinals: make(map[int]bool)}
	for _, entry := range t.Entries {
		n, err := strconv.ParseInt(entry.Ord, 10, 8)
		if err != nil {
			return v, fmt.Errorf("EnumType %s: invalid signed 8-bit ordinal %q", t.ID, entry.Ord)
		}
		label := strings.TrimSpace(entry.Name)
		if _, duplicate := v.labels[label]; (label != "" && duplicate) || v.ordinals[int(n)] {
			return v, fmt.Errorf("EnumType %s: duplicate entry %q (%d)", t.ID, label, n)
		}
		// IEC's tEnumStringValue permits an empty label. The ordinal still
		// carries the enumeration; unnamed labels are not lookup aliases.
		if label != "" {
			v.labels[label] = int(n)
		}
		v.ordinals[int(n)] = true
	}
	return v, nil
}
func (c *compiler) enum(id, text string) (int, error) {
	v, ok := c.enumCache[id]
	if !ok {
		t, ok := c.system.enums[id]
		if !ok || len(t.Entries) == 0 {
			return 0, fmt.Errorf("missing or empty EnumType %q", id)
		}
		var err error
		v, err = compileEnum(t)
		if err != nil {
			return 0, err
		}
		if c.enumCache == nil {
			c.enumCache = make(map[string]enumValues)
		}
		c.enumCache[id] = v
	}
	// An absent initial value leaves zero, including status-only ctlModel.
	// Declaration order must never opt an application into active controls.
	if text == "" {
		return 0, nil
	}
	if n, ok := v.labels[text]; ok {
		return n, nil
	}
	if n, err := strconv.ParseInt(text, 10, 8); err == nil && v.ordinals[int(n)] {
		return int(n), nil
	}
	return 0, fmt.Errorf("unknown %s enum value %q", id, text)
}

func bitValue(n int, text string) (mms.Value, error) {
	b := make([]byte, (n+7)/8)
	if text != "" {
		if len(text) != n {
			return mms.Value{}, fmt.Errorf("expected %d binary digits", n)
		}
		for i, d := range text {
			switch d {
			case '0':
			case '1':
				b[i/8] |= 0x80 >> uint(i%8)
			default:
				return mms.Value{}, fmt.Errorf("invalid binary digits %q", text)
			}
		}
	}
	return mms.BitString(b, n)
}
