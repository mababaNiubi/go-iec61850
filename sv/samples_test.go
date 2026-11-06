// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"bytes"
	"encoding/hex"
	"math"
	"reflect"
	"testing"
)

func TestSVSampleDataFixedLayout(t *testing.T) {
	d, err := NewSampleData(53)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := [8]byte{0x65, 0x53, 0xf1, 0, 0x80, 0, 0, 0x0a}
	f32 := math.Float32frombits(0x80000000)
	f64 := math.Float64frombits(0x7ff8000000000042)
	writes := []error{d.SetInt8(0, -128), d.SetInt16(1, -32768), d.SetInt32(3, math.MinInt32), d.SetInt64(7, math.MinInt64), d.SetUint8(15, 255), d.SetUint16(16, 65535), d.SetUint32(18, math.MaxUint32), d.SetUint64(22, math.MaxUint64), d.SetFloat32(30, f32), d.SetFloat64(34, f64), d.SetQuality(42, 0xaabbccdd)}
	for _, err := range writes {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Timestamp uses a separate buffer to test an offset without overlapping fields.
	ts, _ := NewSampleData(9)
	if err = ts.SetUTCAt(1, timestamp); err != nil {
		t.Fatal(err)
	}
	gotTS, err := ts.UTCAt(1)
	if err != nil || gotTS != timestamp {
		t.Fatal("timestamp semantic bytes changed")
	}
	if v, e := d.Int8(0); e != nil || v != -128 {
		t.Fatal(v, e)
	}
	if v, e := d.Int16(1); e != nil || v != -32768 {
		t.Fatal(v, e)
	}
	if v, e := d.Int32(3); e != nil || v != math.MinInt32 {
		t.Fatal(v, e)
	}
	if v, e := d.Int64(7); e != nil || v != math.MinInt64 {
		t.Fatal(v, e)
	}
	if v, e := d.Uint8(15); e != nil || v != 255 {
		t.Fatal(v, e)
	}
	if v, e := d.Uint16(16); e != nil || v != 65535 {
		t.Fatal(v, e)
	}
	if v, e := d.Uint32(18); e != nil || v != math.MaxUint32 {
		t.Fatal(v, e)
	}
	if v, e := d.Uint64(22); e != nil || v != math.MaxUint64 {
		t.Fatal(v, e)
	}
	if v, e := d.Float32(30); e != nil || math.Float32bits(v) != 0x80000000 {
		t.Fatal(v, e)
	}
	if v, e := d.Float64(34); e != nil || math.Float64bits(v) != 0x7ff8000000000042 {
		t.Fatal(v, e)
	}
	if v, e := d.Quality(42); e != nil || v != 0xaabbccdd {
		t.Fatal(v, e)
	}
	if hex.EncodeToString(d[0:7]) != "80800080000000" {
		t.Fatalf("network order %x", d[:7])
	}
	copy := d.Bytes()
	copy[0] = 0
	if d[0] != 0x80 {
		t.Fatal("Bytes did not copy")
	}
}
func TestSVSampleDataBounds(t *testing.T) {
	for _, size := range []int{-1, MaxAPDUSize + 1, math.MaxInt} {
		if _, err := NewSampleData(size); err == nil {
			t.Fatalf("accepted sample size %d", size)
		}
	}
	d, _ := NewSampleData(8)
	for _, offset := range []int{-1, 1, 8, math.MaxInt} {
		before := d.Bytes()
		if err := d.SetInt64(offset, 99); err == nil {
			t.Fatalf("accepted overflow offset%d", offset)
		}
		if _, err := d.UTCAt(offset); err == nil {
			t.Fatalf("accepted timestamp overflow offset%d", offset)
		}
		if !bytes.Equal(before, d) {
			t.Fatal("failed write changed buffer")
		}
	}
	for _, offset := range []int{-1, 8, math.MaxInt} {
		if _, err := d.Int8(offset); err == nil {
			t.Fatalf("accepted byte read overflow%d", offset)
		}
	}
	if err := d.SetUint32(5, 1); err == nil {
		t.Fatal("accepted uint32 overflow")
	}
	if _, err := d.Float64(1); err == nil {
		t.Fatal("accepted float64 overflow")
	}
}
func TestSVInt32QualityPairs(t *testing.T) {
	values := []Int32Quality{{-123, 0x80000000}, {math.MaxInt32, 0xaabbccdd}}
	b, err := EncodeInt32Quality(values)
	if err != nil || hex.EncodeToString(b) != "ffffff85800000007fffffffaabbccdd" {
		t.Fatalf("fixed measurements %x %v", b, err)
	}
	got, err := DecodeInt32Quality(b)
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatalf("fixed measurement roundtrip %+v %v", got, err)
	}
	for _, size := range []int{1, 7, 9, MaxAPDUSize + 8} {
		if _, err := DecodeInt32Quality(make([]byte, size)); err == nil {
			t.Fatalf("accepted malformed pair length%d", size)
		}
	}
	if _, err := EncodeInt32Quality(make([]Int32Quality, MaxAPDUSize/8+1)); err == nil {
		t.Fatal("encoded oversized array")
	}
}
func FuzzSVSampleAccess(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 0)
	f.Add([]byte{}, -1)
	f.Fuzz(func(t *testing.T, b []byte, offset int) {
		if len(b) > MaxAPDUSize {
			return
		}
		d := SampleData(bytes.Clone(b))
		d.Int8(offset)
		d.Int16(offset)
		d.Int32(offset)
		d.Int64(offset)
		d.Float32(offset)
		d.Float64(offset)
		d.UTCAt(offset)
		before := d.Bytes()
		err := d.SetUint64(offset, 0x0102030405060708)
		if err != nil && !bytes.Equal(before, d) {
			t.Fatal("invalid offset mutated data")
		}
	})
}
