// SPDX-License-Identifier: GPL-3.0-or-later
package data

import (
	"bytes"
	"math"
	"testing"
	"time"
)

func TestValuesRoundTrip(t *testing.T) {
	bits, _ := BitString([]byte{0xa0}, 3)
	binaryTime, _ := BinaryTime([]byte{0, 0, 3, 0xe8, 0, 1})
	utc := UTCFromBytes([8]byte{0x65, 0x53, 0xf1, 0, 0x80, 0, 1, 0xa7})
	values := []Value{Boolean(false), Boolean(true), Integer(math.MinInt64), Integer(math.MaxInt64), Unsigned(math.MaxUint64), Float32(-0.5), Float64(math.Float64frombits(0x7ff8000000000042)), bits, OctetString([]byte{0, 0xff}), VisibleString("MMS 61850"), MMSString("测试🌍"), binaryTime, utc, Array(Integer(1), Integer(2)), Structure(Boolean(true), Array(Unsigned(4), Unsigned(5))), Structure()}
	for _, original := range values {
		encoded, err := EncodeValue(original)
		if err != nil {
			t.Fatalf("encode %s: %v", original.Kind(), err)
		}
		decoded, err := DecodeValue(encoded)
		if err != nil || !original.Equal(decoded) {
			t.Fatalf("roundtrip %s (%x): %v, %v", original.Kind(), encoded, decoded, err)
		}
	}
	golden := map[string]Value{"8301ff": Boolean(true), "8502ff7f": Integer(-129), "86020080": Unsigned(128), "8705083f800000": Float32(1), "a20783010085020100": Structure(Boolean(false), Integer(256))}
	for hex, value := range golden {
		encoded, err := EncodeValue(value)
		if err != nil || fmtHex(encoded) != hex {
			t.Fatalf("golden %s: %x, %v", hex, encoded, err)
		}
	}
}

func fmtHex(b []byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, len(b)*2)
	for i, c := range b {
		result[i*2], result[i*2+1] = digits[c>>4], digits[c&15]
	}
	return string(result)
}

func TestValueCopyAndTime(t *testing.T) {
	raw := []byte{1, 2, 3}
	value := OctetString(raw)
	raw[0] = 9
	got, _ := value.Bytes()
	got[1] = 9
	again, _ := value.Bytes()
	if !bytes.Equal(again, []byte{1, 2, 3}) {
		t.Fatal("value aliases caller data")
	}
	children := []Value{Integer(3)}
	composite := Array(children...)
	children[0] = Integer(8)
	read, _ := composite.Elements()
	read[0] = Integer(9)
	read, _ = composite.Elements()
	n, _ := read[0].Int()
	if n != 3 {
		t.Fatal("composite aliases caller slice")
	}
	now := time.Date(2026, 11, 6, 1, 2, 3, 500000000, time.UTC)
	timestamp := UTC(now)
	decoded, err := timestamp.Time()
	if err != nil || !decoded.Equal(now) {
		t.Fatalf("UTC roundtrip: %v %v", decoded, err)
	}
	encoded, _ := timestamp.Bytes()
	if encoded[7] != 0x1f {
		t.Fatal("constructor invented clock accuracy")
	}
	if UTC(time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)).Kind() != KindInvalid {
		t.Fatal("out-of-range UTC silently wrapped")
	}
	if _, err := Integer(1).Bool(); err == nil {
		t.Fatal("getter coerced a type")
	}
}

func TestMalformedValues(t *testing.T) {
	bad := [][]byte{{0x83, 0}, {0x83, 2, 0, 0}, {0x84, 1, 8}, {0x84, 2, 7, 1}, {0x87, 5, 11, 0, 0, 0, 0}, {0x91, 1, 0}, {0x8c, 4, 0xff, 0xff, 0xff, 0xff}, {0x90, 1, 0xff}, {0x8a, 1, 0x1f}, {0x85, 0}, {0x86, 1, 0xff}, {0x83, 1, 0, 0}}
	for _, input := range bad {
		if _, err := DecodeValue(input); err == nil {
			t.Fatalf("accepted invalid Data %x", input)
		}
	}
	if _, err := BitString([]byte{0xff}, 3); err == nil {
		t.Fatal("accepted dirty padding")
	}
	if _, err := BinaryTime([]byte{1}); err == nil {
		t.Fatal("accepted wrong BinaryTime size")
	}
	if _, err := EncodeValue(VisibleString("中文")); err == nil {
		t.Fatal("encoded non-ASCII VisibleString")
	}
	deep := Boolean(true)
	for i := 0; i < 65; i++ {
		deep = Structure(deep)
	}
	if _, err := EncodeValue(deep); err == nil {
		t.Fatal("encoded too deep Data")
	}
}

func FuzzDecodeValue(f *testing.F) {
	f.Add([]byte{0x85, 1, 42})
	f.Add([]byte{0xa2, 3, 0x83, 1, 0xff})
	f.Add([]byte{0x84, 2, 5, 0xa0})
	f.Fuzz(func(t *testing.T, input []byte) {
		value, err := DecodeValue(input)
		if err != nil {
			return
		}
		encoded, err := EncodeValue(value)
		if err != nil {
			t.Fatalf("decoded unencodable value: %v", err)
		}
		again, err := DecodeValue(encoded)
		if err != nil || !value.Equal(again) {
			t.Fatal("Data roundtrip changed content")
		}
	})
}
