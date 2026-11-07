// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// ValueJSON keeps integers as decimal strings. JavaScript numbers cannot retain
// every INT64/UINT64, so the HTTP boundary must never round them silently.
func ValueJSON(v mms.Value) any {
	switch v.Kind() {
	case mms.KindInteger:
		n, _ := v.Int()
		return strconv.FormatInt(n, 10)
	case mms.KindUnsigned:
		n, _ := v.Uint()
		return strconv.FormatUint(n, 10)
	case mms.KindBoolean:
		b, _ := v.Bool()
		return b
	case mms.KindFloat:
		n, _ := v.Float()
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return strconv.FormatFloat(n, 'g', -1, 64)
		}
		return n
	case mms.KindVisibleString, mms.KindMMSString:
		s, _ := v.StringValue()
		return s
	case mms.KindBitString:
		n, _ := v.BitLength()
		raw, _ := v.Bytes()
		var out strings.Builder
		for i := 0; i < n; i++ {
			if raw[i/8]&(0x80>>uint(i%8)) != 0 {
				out.WriteByte('1')
			} else {
				out.WriteByte('0')
			}
		}
		return out.String()
	case mms.KindUTC:
		t, _ := v.Time()
		return t.UTC().Format(time.RFC3339Nano)
	case mms.KindArray, mms.KindStructure:
		elements, _ := v.Elements()
		items := make([]any, len(elements))
		for i, e := range elements {
			items[i] = ValueJSON(e)
		}
		return items
	default:
		raw, _ := v.Bytes()
		return hex.EncodeToString(raw)
	}
}

// ParseValue uses the declared type, including structure component order. The
// browser and CLI share this function rather than implementing separate codecs.
func ParseValue(t mms.TypeSpec, raw json.RawMessage) (mms.Value, error) {
	if err := mms.ValidateType(t); err != nil {
		return mms.Value{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var input any
	if err := decoder.Decode(&input); err != nil {
		return mms.Value{}, fmt.Errorf("enter a JSON value: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return mms.Value{}, fmt.Errorf("expected exactly one JSON value")
	}
	v, err := parseValue(t, input)
	if err == nil {
		err = mms.ValidateValue(t, v)
	}
	if err != nil {
		return mms.Value{}, err
	}
	return v, nil
}
func scalarText(input any) (string, error) {
	switch v := input.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	default:
		return "", fmt.Errorf("expected a string or number")
	}
}
func parseValue(t mms.TypeSpec, input any) (mms.Value, error) {
	switch t.Kind {
	case mms.KindBoolean:
		b, ok := input.(bool)
		if !ok {
			return mms.Value{}, fmt.Errorf("expected true or false")
		}
		return mms.Boolean(b), nil
	case mms.KindInteger:
		s, e := scalarText(input)
		if e != nil {
			return mms.Value{}, e
		}
		n, e := strconv.ParseInt(s, 10, 64)
		return mms.Integer(n), e
	case mms.KindUnsigned:
		s, e := scalarText(input)
		if e != nil {
			return mms.Value{}, e
		}
		n, e := strconv.ParseUint(s, 10, 64)
		return mms.Unsigned(n), e
	case mms.KindFloat:
		s, e := scalarText(input)
		if e != nil {
			return mms.Value{}, e
		}
		n, e := strconv.ParseFloat(s, t.Width)
		if t.Width == 32 {
			return mms.Float32(float32(n)), e
		}
		return mms.Float64(n), e
	case mms.KindVisibleString, mms.KindMMSString:
		s, ok := input.(string)
		if !ok {
			return mms.Value{}, fmt.Errorf("expected a JSON string")
		}
		if t.Kind == mms.KindMMSString {
			return mms.MMSString(s), nil
		}
		return mms.VisibleString(s), nil
	case mms.KindBitString:
		s, ok := input.(string)
		if !ok {
			return mms.Value{}, fmt.Errorf("expected a binary string, e.g. \"01\"")
		}
		raw := make([]byte, (len(s)+7)/8)
		for i, b := range s {
			switch b {
			case '0':
			case '1':
				raw[i/8] |= 0x80 >> uint(i%8)
			default:
				return mms.Value{}, fmt.Errorf("bit strings use only 0 and 1")
			}
		}
		return mms.BitString(raw, len(s))
	case mms.KindUTC:
		s, ok := input.(string)
		if !ok {
			return mms.Value{}, fmt.Errorf("expected an RFC3339 timestamp")
		}
		tm, e := time.Parse(time.RFC3339Nano, s)
		return mms.UTC(tm), e
	case mms.KindOctetString, mms.KindBinaryTime:
		s, ok := input.(string)
		if !ok {
			return mms.Value{}, fmt.Errorf("expected a hexadecimal string")
		}
		b, e := hex.DecodeString(strings.Join(strings.Fields(s), ""))
		if e != nil {
			return mms.Value{}, e
		}
		if t.Kind == mms.KindBinaryTime {
			return mms.BinaryTime(b)
		}
		return mms.OctetString(b), nil
	case mms.KindArray, mms.KindStructure:
		items, ok := input.([]any)
		if !ok {
			return mms.Value{}, fmt.Errorf("expected an ordered JSON array")
		}
		want := len(t.Components)
		if t.Kind == mms.KindArray {
			want = t.Length
		}
		if len(items) != want {
			return mms.Value{}, fmt.Errorf("expected %d elements", want)
		}
		values := make([]mms.Value, len(items))
		for i, item := range items {
			var child mms.TypeSpec
			if t.Kind == mms.KindArray {
				if t.Element == nil {
					return mms.Value{}, fmt.Errorf("missing array element type")
				}
				child = *t.Element
			} else {
				child = t.Components[i].Type
			}
			v, e := parseValue(child, item)
			if e != nil {
				return mms.Value{}, fmt.Errorf("element %d: %w", i, e)
			}
			values[i] = v
		}
		if t.Kind == mms.KindArray {
			return mms.Array(values...), nil
		}
		return mms.Structure(values...), nil
	}
	return mms.Value{}, fmt.Errorf("unsupported value type %s", t.Kind)
}
