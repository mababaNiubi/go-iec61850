// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func codecSamplePDU() PDU {
	return PDU{GoCBRef: "cb", DataSet: "ds", GoID: "id", TimeAllowedToLive: 1000, Timestamp: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, StateNumber: 1, ConfRev: 1, Values: []data.Value{data.Boolean(true), data.Integer(-5)}}
}

func equalPDUs(a, b PDU) bool {
	av, bv := a.Values, b.Values
	a.Values = nil
	b.Values = nil
	if !reflect.DeepEqual(a, b) || len(av) != len(bv) {
		return false
	}
	for i := range av {
		if !av[i].Equal(bv[i]) {
			return false
		}
	}
	return true
}

func TestGOOSEAPDUGolden(t *testing.T) {
	const golden = "613480026362810203e88202647383026964840801020304050607088501018601008701008801018901008a0102ab068301ff8501fb"
	encoded, err := EncodePDU(codecSamplePDU())
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(encoded) != golden {
		t.Fatalf("APDU got %x, want %s", encoded, golden)
	}
	decoded, err := DecodePDU(encoded)
	if err != nil || !equalPDUs(decoded, codecSamplePDU()) {
		t.Fatalf("APDU roundtrip %+v %v", decoded, err)
	}
}

func TestGOOSEFrameGoldenAndVLANHighBits(t *testing.T) {
	f := Frame{Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 1}, Source: [6]byte{2, 3, 4, 5, 6, 7}, VLAN: &VLAN{Priority: 5, DEI: true, ID: 0xabc}, APPID: 0x1001, Reserved1: 0x1212, Reserved2: 0x3434, PDU: codecSamplePDU()}
	encoded, err := EncodeFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "010ccd0100010203040506078100babc88b81001003e12123434"
	if hex.EncodeToString(encoded[:26]) != prefix {
		t.Fatalf("Ethernet/GOOSE header %x, want %s", encoded[:26], prefix)
	}
	if len(encoded) != 80 {
		t.Fatalf("frame length %d", len(encoded))
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil || !reflect.DeepEqual(decoded.VLAN, f.VLAN) || decoded.APPID != f.APPID || decoded.Reserved1 != f.Reserved1 || decoded.Reserved2 != f.Reserved2 || decoded.Source != f.Source || decoded.Destination != f.Destination || !equalPDUs(decoded.PDU, f.PDU) {
		t.Fatalf("frame roundtrip %+v %v", decoded, err)
	}
	for priority := uint8(0); priority < 8; priority++ {
		for _, id := range []uint16{0, 1, 2047, 2048, 4094} {
			for _, dei := range []bool{false, true} {
				f.VLAN = &VLAN{Priority: priority, DEI: dei, ID: id}
				b, e := EncodeFrame(f)
				if e != nil {
					t.Fatal(e)
				}
				got, e := DecodeFrame(b)
				if e != nil || *got.VLAN != *f.VLAN {
					t.Fatalf("VLAN %+v -> %+v (%v)", f.VLAN, got.VLAN, e)
				}
			}
		}
	}
}

func TestGOOSEFrameLengthAndPadding(t *testing.T) {
	p := codecSamplePDU()
	p.GoCBRef = "a"
	p.DataSet = "b"
	p.GoID = ""
	p.TimeAllowedToLive = 1
	p.Values = nil
	f := Frame{PDU: p}
	encoded, err := EncodeFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) < ethernetMinimum {
		t.Fatal("encoder omitted minimum Ethernet padding")
	}
	gooseLength := int(binary.BigEndian.Uint16(encoded[16:18]))
	meaningful := 14 + gooseLength
	if meaningful > len(encoded) {
		t.Fatal("GOOSE length includes unavailable bytes")
	}
	for _, wire := range [][]byte{encoded, encoded[:meaningful], append(append([]byte(nil), encoded...), 0xfa, 0xfb)} {
		got, err := DecodeFrame(wire)
		if err != nil || !equalPDUs(got.PDU, p) {
			t.Fatalf("padding changed PDU: %v", err)
		}
	}
	wrongType := append([]byte(nil), encoded...)
	wrongType[12], wrongType[13] = 8, 0
	if _, err := DecodeFrame(wrongType); !errors.Is(err, ErrNotGOOSE) {
		t.Fatalf("different protocol: %v", err)
	}
	for _, length := range []uint16{0, 7, 1501, uint16(len(encoded) + 1)} {
		wire := append([]byte(nil), encoded...)
		binary.BigEndian.PutUint16(wire[16:18], length)
		if _, err := DecodeFrame(wire); !errors.Is(err, ErrMalformed) {
			t.Fatalf("accepted length %d: %v", length, err)
		}
	}
	for n := 0; n < meaningful; n++ {
		if _, err := DecodeFrame(encoded[:n]); err == nil {
			t.Fatalf("accepted truncated frame length %d", n)
		}
	}
	oversized := append(append([]byte(nil), encoded...), make([]byte, 1515-len(encoded))...)
	if _, err := DecodeFrame(oversized); !errors.Is(err, ErrMalformed) {
		t.Fatalf("accepted oversized untagged frame: %v", err)
	}
}

func TestGOOSEOptionalDefaultsAndEmptyDataSet(t *testing.T) {
	p := codecSamplePDU()
	encoded, _ := EncodePDU(p)
	top, _, _ := ber.Parse(encoded)
	fields, _ := ber.ParseAll(top.Bytes)
	var content [][]byte
	for _, field := range fields {
		if field.Tag != 0x83 && field.Tag != 0x87 && field.Tag != 0x89 {
			content = append(content, ber.Encode(field.Tag, field.Bytes))
		}
	}
	decoded, err := DecodePDU(ber.Encode(0x61, content...))
	if err != nil {
		t.Fatal(err)
	}
	p.GoID = ""
	if !equalPDUs(decoded, p) {
		t.Fatalf("optional defaults %+v", decoded)
	}
	p.Values = nil
	encoded, err = EncodePDU(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodePDU(encoded)
	if err != nil || len(decoded.Values) != 0 {
		t.Fatalf("empty dataset %+v %v", decoded, err)
	}
}

func TestGOOSEDatasetValuesPreserveWireSemantics(t *testing.T) {
	bits, _ := data.BitString([]byte{0xa5, 0xc0}, 10)
	binaryTime, _ := data.BinaryTime([]byte{0, 0x12, 0xd6, 0x87, 0x30, 0x39})
	p := codecSamplePDU()
	p.StateNumber = math.MaxUint32
	p.SequenceNumber = math.MaxUint32
	p.ConfRev = math.MaxUint32
	p.TimeAllowedToLive = math.MaxUint32
	p.Simulation = true
	p.NeedsCommission = true
	p.Values = []data.Value{data.Integer(math.MinInt64), data.Unsigned(math.MaxUint64), data.Float32(float32(math.Copysign(0, -1))), data.Float64(math.Float64frombits(0x7ff8000000000042)), bits, data.OctetString([]byte{0, 0xff}), data.VisibleString("status"), data.MMSString("电力"), data.UTCFromBytes([8]byte{0x65, 0x53, 0xf1, 0, 0x80, 0, 0, 0x0a}), binaryTime, data.Structure(data.Boolean(true), data.Array(data.Integer(1), data.Integer(2)))}
	encoded, err := EncodePDU(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePDU(encoded)
	if err != nil || !equalPDUs(decoded, p) {
		t.Fatalf("values changed: %v", err)
	}
}

func TestGOOSERejectsMalformedAPDU(t *testing.T) {
	encoded, _ := EncodePDU(codecSamplePDU())
	top, _, _ := ber.Parse(encoded)
	fields, _ := ber.ParseAll(top.Bytes)
	build := func(replaceTag byte, replacement []byte, skip bool) []byte {
		var parts [][]byte
		for _, f := range fields {
			if f.Tag == replaceTag {
				if !skip {
					parts = append(parts, replacement)
				}
			} else {
				parts = append(parts, ber.Encode(f.Tag, f.Bytes))
			}
		}
		return ber.Encode(0x61, parts...)
	}
	bad := [][]byte{
		{}, {0x61, 0}, append(append([]byte(nil), encoded...), 0), ber.Encode(0x60, top.Bytes),
		build(0x81, ber.Uint(0x81, 0), false), build(0x85, ber.Uint(0x85, 0), false),
		build(0x86, ber.Uint(0x86, uint64(math.MaxUint32)+1), false), build(0x86, ber.Int(0x86, -1), false),
		build(0x80, ber.Encode(0x80, []byte(strings.Repeat("x", 130))), false), build(0x82, ber.Encode(0x82, []byte{0}), false),
		build(0x83, ber.Encode(0x83), false), build(0x84, ber.Encode(0x84, make([]byte, 7)), false),
		build(0x87, ber.Encode(0x87), false), build(0x89, ber.Encode(0x89, []byte{0, 0}), false),
		build(0x8a, ber.Uint(0x8a, 1), false), build(0x8a, ber.Uint(0x8a, 3), false), build(0x8a, ber.Uint(0x8a, 1025), false),
		build(0xab, ber.Encode(0xab, []byte{0x8f, 0}), false),
		ber.Encode(0x61, top.Bytes, ber.Encode(0x8c, []byte{0})),
		ber.Encode(0x61, ber.Encode(0x80, []byte("cb")), top.Bytes),
	}
	for _, field := range fields {
		if field.Tag != 0x83 && field.Tag != 0x87 && field.Tag != 0x89 {
			bad = append(bad, build(field.Tag, nil, true))
		}
	}
	for i, input := range bad {
		if _, err := DecodePDU(input); !errors.Is(err, ErrMalformed) {
			t.Fatalf("accepted malformed APDU %d (%x): %v", i, input, err)
		}
	}
	reordered := append([]ber.Element(nil), fields...)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	var content [][]byte
	for _, f := range reordered {
		content = append(content, ber.Encode(f.Tag, f.Bytes))
	}
	if _, err := DecodePDU(ber.Encode(0x61, content...)); err == nil {
		t.Fatal("accepted out-of-order SEQUENCE")
	}
}

func TestGOOSEEncodingLimitsAndClone(t *testing.T) {
	p := codecSamplePDU()
	p.GoCBRef = strings.Repeat("x", 129)
	if _, err := EncodePDU(p); err != nil {
		t.Fatal(err)
	}
	p.GoCBRef += "x"
	if _, err := EncodePDU(p); err == nil {
		t.Fatal("encoded oversized reference")
	}
	p = codecSamplePDU()
	p.Values = []data.Value{data.OctetString(make([]byte, 1500))}
	if _, err := EncodePDU(p); err == nil {
		t.Fatal("encoded oversized dataset")
	}
	p = codecSamplePDU()
	p.Values = make([]data.Value, 1025)
	if _, err := EncodePDU(p); err == nil {
		t.Fatal("encoded too many entries")
	}
	p = codecSamplePDU()
	p.Values = []data.Value{{}}
	if _, err := EncodePDU(p); err == nil {
		t.Fatal("encoded invalid Data")
	}
	for _, tag := range []*VLAN{{Priority: 8}, {ID: 4095}, {ID: 65535}} {
		if _, err := EncodeFrame(Frame{VLAN: tag, PDU: codecSamplePDU()}); err == nil {
			t.Fatalf("encoded invalid VLAN %+v", tag)
		}
	}
	f := Frame{VLAN: &VLAN{Priority: 7, ID: 2000}, PDU: codecSamplePDU()}
	copy := f.Clone()
	copy.VLAN.ID = 1
	copy.PDU.Values[0] = data.Boolean(false)
	if f.VLAN.ID != 2000 || !f.PDU.Values[0].Equal(data.Boolean(true)) {
		t.Fatal("mutable metadata escaped Frame.Clone")
	}
}

func FuzzGOOSEAPDU(f *testing.F) {
	encoded, _ := EncodePDU(codecSamplePDU())
	f.Add(encoded)
	f.Add([]byte{0x61, 0})
	f.Add([]byte{0x61, 0x80, 0, 0})
	f.Fuzz(func(t *testing.T, input []byte) {
		p, err := DecodePDU(input)
		if err != nil {
			return
		}
		encoded, err := EncodePDU(p)
		if err != nil {
			t.Fatalf("decoded unencodable APDU: %v", err)
		}
		again, err := DecodePDU(encoded)
		if err != nil || !equalPDUs(p, again) {
			t.Fatalf("APDU semantic roundtrip changed: %v", err)
		}
	})
}

func FuzzGOOSEFrame(f *testing.F) {
	encoded, _ := EncodeFrame(Frame{VLAN: &VLAN{Priority: 5, DEI: true, ID: 3000}, PDU: codecSamplePDU()})
	f.Add(encoded)
	f.Add([]byte{})
	f.Add(make([]byte, 18))
	f.Fuzz(func(t *testing.T, input []byte) {
		decoded, err := DecodeFrame(input)
		if err != nil {
			return
		}
		wire, err := EncodeFrame(decoded)
		if err != nil {
			t.Fatalf("decoded unencodable frame: %v", err)
		}
		again, err := DecodeFrame(wire)
		if err != nil || decoded.Source != again.Source || decoded.Destination != again.Destination || decoded.APPID != again.APPID || decoded.Reserved1 != again.Reserved1 || decoded.Reserved2 != again.Reserved2 || !reflect.DeepEqual(decoded.VLAN, again.VLAN) || !equalPDUs(decoded.PDU, again.PDU) {
			t.Fatalf("frame semantic roundtrip changed: %v", err)
		}
		if bytes.Equal(wire, nil) {
			t.Fatal("empty encoded frame")
		}
	})
}
