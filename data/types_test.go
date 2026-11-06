// SPDX-License-Identifier: GPL-3.0-or-later
package data

import (
	"math"
	"reflect"
	"testing"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func TestTypeSpecifications(t *testing.T) {
	integer := TypeSpec{Kind: KindInteger, Width: 32}
	types := []TypeSpec{
		{Kind: KindBoolean}, {Kind: KindInteger, Width: 8}, {Kind: KindUnsigned, Width: 64},
		{Kind: KindFloat, Width: 32, ExponentWidth: 8}, {Kind: KindFloat, Width: 64, ExponentWidth: 11},
		{Kind: KindBitString, Length: 13}, {Kind: KindOctetString, Length: -256}, {Kind: KindVisibleString, Length: -129}, {Kind: KindMMSString, Length: -64},
		{Kind: KindBinaryTime, Length: 4}, {Kind: KindBinaryTime, Length: 6}, {Kind: KindUTC},
		{Kind: KindArray, Length: 10, Element: &integer},
		{Kind: KindStructure, Components: []Component{{Name: "status", Type: TypeSpec{Kind: KindBoolean}}, {Name: "count", Type: integer}}},
	}
	for _, original := range types {
		encoded, err := EncodeType(original)
		if err != nil {
			t.Fatalf("encode %s: %v", original.Kind, err)
		}
		decoded, err := DecodeType(encoded)
		if err != nil || !reflect.DeepEqual(decoded, original) {
			t.Fatalf("type %s (%x): got %+v, %v", original.Kind, encoded, decoded, err)
		}
	}
	golden := map[string]TypeSpec{
		"850120":                               integer,
		"a706020120020108":                     {Kind: KindFloat, Width: 32, ExponentWidth: 8},
		"a108810102a203850120":                 {Kind: KindArray, Length: 2, Element: &integer},
		"a210a10e300c8006737461747573a1028300": {Kind: KindStructure, Components: []Component{{Name: "status", Type: TypeSpec{Kind: KindBoolean}}}},
	}
	for hex, typ := range golden {
		encoded, err := EncodeType(typ)
		if err != nil || fmtHex(encoded) != hex {
			t.Fatalf("type golden %s: %x, %v", hex, encoded, err)
		}
	}
}

func TestTypeValidation(t *testing.T) {
	int8type := TypeSpec{Kind: KindInteger, Width: 8}
	for _, n := range []int64{-128, 0, 127} {
		if err := ValidateValue(int8type, Integer(n)); err != nil {
			t.Fatalf("%d: %v", n, err)
		}
	}
	for _, n := range []int64{-129, 128, math.MinInt64, math.MaxInt64} {
		if err := ValidateValue(int8type, Integer(n)); err == nil {
			t.Fatalf("accepted out of range integer %d", n)
		}
	}
	if err := ValidateValue(TypeSpec{Kind: KindUnsigned, Width: 8}, Unsigned(256)); err == nil {
		t.Fatal("accepted out of range unsigned")
	}
	if err := ValidateValue(TypeSpec{Kind: KindUnsigned, Width: 64}, Unsigned(math.MaxUint64)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateValue(TypeSpec{Kind: KindFloat, Width: 32}, Float64(1)); err == nil {
		t.Fatal("coerced float width")
	}
	if err := ValidateValue(TypeSpec{Kind: KindVisibleString, Length: -4}, VisibleString("abc")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateValue(TypeSpec{Kind: KindVisibleString, Length: -4}, VisibleString("abcde")); err == nil {
		t.Fatal("accepted too long string")
	}
	if err := ValidateValue(TypeSpec{Kind: KindVisibleString, Length: 4}, VisibleString("abc")); err == nil {
		t.Fatal("accepted wrong fixed length")
	}
	if err := ValidateValue(TypeSpec{Kind: KindMMSString, Length: 2}, MMSString("中文")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateValue(TypeSpec{Kind: KindBoolean}, Integer(1)); err == nil {
		t.Fatal("coerced wrong kind")
	}
	arrayType := TypeSpec{Kind: KindArray, Length: 2, Element: &int8type}
	if err := ValidateValue(arrayType, Array(Integer(1), Unsigned(2))); err == nil {
		t.Fatal("accepted heterogeneous array")
	}
	if err := ValidateValue(arrayType, Array(Integer(1))); err == nil {
		t.Fatal("accepted wrong element count")
	}
	cycle := TypeSpec{Kind: KindArray, Length: 1}
	cycle.Element = &cycle
	if _, err := EncodeType(cycle); err == nil {
		t.Fatal("accepted cyclic type")
	}
	if err := ValidateType(TypeSpec{Kind: KindArray}); err == nil {
		t.Fatal("accepted missing element type")
	}
}

func TestMalformedTypes(t *testing.T) {
	bad := [][]byte{
		{0x83, 1, 0}, {0x85, 1, 0}, {0x85, 1, 65}, {0x8c, 0},
		{0xa7, 3, 2, 1, 32}, {0xa7, 6, 2, 1, 32, 2, 1, 11},
		{0xa1, 3, 0x81, 1, 2}, {0xa2, 0},
		{0xa2, 5, 0x80, 1, 0xff, 0xa1, 0},
	}
	for _, input := range bad {
		if _, err := DecodeType(input); err == nil {
			t.Fatalf("accepted invalid TypeSpec %x", input)
		}
	}
	content := []byte{0x83, 0}
	for i := 0; i < ber.MaxDepth+1; i++ {
		content = ber.Encode(0xa1, ber.Uint(0x81, 1), ber.Encode(0xa2, content))
	}
	if _, err := DecodeType(content); err == nil {
		t.Fatal("accepted deeply nested descriptor")
	}
}

// The fuzz corpus found that an explicit zero exponent was accidentally
// accepted as a local shorthand and changed to eight bits on re-encoding.
func TestDecodeTypeRejectsExplicitZeroExponent(t *testing.T) {
	for _, width := range []uint64{32, 64} {
		encoded := ber.Encode(0xa7, ber.Uint(0x02, width), ber.Uint(0x02, 0))
		if _, err := DecodeType(encoded); err == nil {
			t.Fatalf("accepted on-wire float%d with zero exponent width", width)
		}
	}
}

func TestEncodeTypeResolvesLocalDefaultExponent(t *testing.T) {
	for _, tt := range []struct{ width, exponent int }{{32, 8}, {64, 11}} {
		encoded, err := EncodeType(TypeSpec{Kind: KindFloat, Width: tt.width})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeType(encoded)
		if err != nil || decoded.Width != tt.width || decoded.ExponentWidth != tt.exponent {
			t.Fatalf("local float%d default: %+v, %v", tt.width, decoded, err)
		}
	}
}

func FuzzDecodeType(f *testing.F) {
	f.Add([]byte{0x85, 1, 32})
	f.Add([]byte{0xa7, 6, 2, 1, 32, 2, 1, 8})
	f.Add([]byte{0xa2, 2, 0xa1, 0})
	f.Fuzz(func(t *testing.T, input []byte) {
		typ, err := DecodeType(input)
		if err != nil {
			return
		}
		encoded, err := EncodeType(typ)
		if err != nil {
			t.Fatalf("decoded unencodable type: %v", err)
		}
		again, err := DecodeType(encoded)
		if err != nil || !reflect.DeepEqual(typ, again) {
			t.Fatal("TypeSpec roundtrip changed content")
		}
	})
}
