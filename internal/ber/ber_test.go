// SPDX-License-Identifier: GPL-3.0-or-later
package ber

import (
	"bytes"
	"math"
	"testing"
)

func TestIntegerBoundaries(t *testing.T) {
	for _, value := range []int64{math.MinInt64, -32769, -129, -128, -1, 0, 127, 128, 32768, math.MaxInt64} {
		e, rest, err := Parse(Int(0x02, value))
		if err != nil || len(rest) != 0 {
			t.Fatalf("parse %d: %v", value, err)
		}
		got, err := DecodeInt(e.Bytes)
		if err != nil || got != value {
			t.Fatalf("integer %d: got %d, %v", value, got, err)
		}
	}
	for _, value := range []uint64{0, 127, 128, math.MaxInt32, math.MaxUint32, math.MaxInt64, math.MaxUint64} {
		e, _, err := Parse(Uint(0x02, value))
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeUint(e.Bytes)
		if err != nil || got != value {
			t.Fatalf("unsigned %d: got %d, %v", value, got, err)
		}
	}
	if got := Int(0x02, -129); !bytes.Equal(got, []byte{2, 2, 0xff, 0x7f}) {
		t.Fatalf("signed golden: %x", got)
	}
	if got := Uint(0x02, 128); !bytes.Equal(got, []byte{2, 2, 0, 0x80}) {
		t.Fatalf("unsigned golden: %x", got)
	}
	for _, bad := range [][]byte{nil, {0x80}, {1, 0, 0, 0, 0, 0, 0, 0, 0}} {
		if _, err := DecodeUint(bad); err == nil {
			t.Fatalf("accepted invalid uint: %x", bad)
		}
	}
	if _, err := DecodeInt([]byte{0, 0x80, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("accepted signed overflow")
	}
}

func TestLengthsAndIndefinite(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 255, 256, 65535, 65536} {
		payload := bytes.Repeat([]byte{0xa5}, n)
		encoded := append(Encode(0x04, payload), 0xff)
		e, rest, err := Parse(encoded)
		if err != nil || e.Tag != 4 || !bytes.Equal(e.Bytes, payload) || !bytes.Equal(rest, []byte{0xff}) {
			t.Fatalf("length %d: %v", n, err)
		}
	}
	encoded := []byte{0x30, 0x80, 0x30, 0x80, 2, 1, 1, 0, 0, 0, 0, 2, 1, 3}
	e, rest, err := Parse(encoded)
	if err != nil || e.Tag != 0x30 || !bytes.Equal(rest, []byte{2, 1, 3}) {
		t.Fatalf("indefinite: %v, %x", err, rest)
	}
	children, err := ParseAll(e.Bytes)
	if err != nil || len(children) != 1 || !bytes.Equal(children[0].Bytes, []byte{2, 1, 1}) {
		t.Fatalf("indefinite children: %v %+v", err, children)
	}
	for _, bad := range [][]byte{nil, {0x30}, {4, 0x80, 0, 0}, {0x30, 0x80, 2, 1, 1}, {0x04, 0x82, 0}, {4, 0x84, 0xff, 0xff, 0xff, 0xff}, {0x1f, 0}, {0, 0}, {4, 2, 1}} {
		if _, _, err := Parse(bad); err == nil {
			t.Fatalf("accepted malformed BER %x", bad)
		}
	}
	deep := []byte{2, 1, 0}
	for i := 0; i < MaxDepth+1; i++ {
		deep = append(append([]byte{0x30, 0x80}, deep...), 0, 0)
	}
	if _, _, err := Parse(deep); err == nil {
		t.Fatal("accepted excessive indefinite nesting")
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range [][]byte{{2, 1, 0}, {0x30, 0x80, 2, 1, 1, 0, 0}, {4, 0x81, 0x80}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		e, rest, err := Parse(input)
		if err != nil {
			return
		}
		if len(rest) >= len(input) {
			t.Fatal("parser made no progress")
		}
		canonical := Encode(e.Tag, e.Bytes)
		again, tail, err := Parse(canonical)
		if err != nil || len(tail) != 0 || again.Tag != e.Tag || !bytes.Equal(again.Bytes, e.Bytes) {
			t.Fatal("canonical wrapping changed content")
		}
	})
}
