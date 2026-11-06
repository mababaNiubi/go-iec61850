// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func codecASDU() ASDU {
	return ASDU{SVID: "sv1", SampleCount: 65534, ConfRev: 0x01020304, SampleSync: 2, Data: []byte{0, 0xff}}
}
func codecPDU() PDU { return PDU{ASDUs: []ASDU{codecASDU()}} }
func codecFrame() Frame {
	return Frame{Destination: [6]byte{1, 0x0c, 0xcd, 4, 0, 1}, Source: [6]byte{2, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}, VLAN: &VLAN{Priority: 4, DEI: true, ID: 2345}, APPID: 0x4000, Reserved1: 0xabcd, Reserved2: 0x1234, PDU: codecPDU()}
}
func mustEncodePDU(t *testing.T, p PDU) []byte {
	t.Helper()
	b, e := EncodePDU(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestSVGoldenAPDUAndFrame(t *testing.T) {
	pdu := mustEncodePDU(t, codecPDU())
	golden := "601d800101a218301680037376318202fffe830401020304850102870200ff"
	if hex.EncodeToString(pdu) != golden {
		t.Fatalf("APDU %x", pdu)
	}
	p, err := DecodePDU(pdu)
	if err != nil || !reflect.DeepEqual(p, codecPDU()) {
		t.Fatalf("decoded %+v %v", p, err)
	}
	b, err := EncodeFrame(codecFrame())
	if err != nil {
		t.Fatal(err)
	}
	header := "010ccd04000102aabbccddee8100992988ba40000027abcd1234"
	if len(b) != 60 || hex.EncodeToString(b) != header+golden+"000000" {
		t.Fatalf("frame %x (%d bytes)", b, len(b))
	}
	f, err := DecodeFrame(b)
	if err != nil || !reflect.DeepEqual(f, codecFrame()) {
		t.Fatalf("frame roundtrip %+v %v", f, err)
	}
	f, err = DecodeFrame(b[:57])
	if err != nil || !reflect.DeepEqual(f, codecFrame()) {
		t.Fatalf("capture omitted padding: %v", err)
	}
}

func TestSVOptionalFieldsMultiASDUAndClone(t *testing.T) {
	timestamp := [8]byte{0x65, 0x53, 0xf1, 0, 0x80, 0, 0, 0x0a}
	gm := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	rate, mode := uint16(4000), uint16(1)
	a := codecASDU()
	a.DataSet = "sample/LLN0$sv"
	a.RefreshTime = &timestamp
	a.SampleRate = &rate
	a.SampleMode = &mode
	a.GMIdentity = &gm
	a.Data = []byte{1, 2, 3, 4}
	p := PDU{ASDUs: []ASDU{a, codecASDU()}, Security: []byte{0, 0xff, 1}}
	b := mustEncodePDU(t, p)
	decoded, err := DecodePDU(b)
	if err != nil || !reflect.DeepEqual(decoded, p) {
		t.Fatalf("optionals %+v %v", decoded, err)
	}
	if !bytes.Contains(b, []byte{0x88, 2, 0, 1}) {
		t.Fatal("smpMod not encoded in explicit two-octet profile")
	}
	clone := p.Clone()
	clone.ASDUs[0].Data[0] = 99
	clone.Security[0] = 99
	*clone.ASDUs[0].SampleMode = 2
	*clone.ASDUs[0].SampleRate = 100
	clone.ASDUs[0].RefreshTime[0] = 99
	clone.ASDUs[0].GMIdentity[0] = 99
	if p.ASDUs[0].Data[0] != 1 || p.Security[0] != 0 || *p.ASDUs[0].SampleMode != 1 || *p.ASDUs[0].SampleRate != 4000 || p.ASDUs[0].RefreshTime[0] != 0x65 || p.ASDUs[0].GMIdentity[0] != 1 {
		t.Fatal("Clone leaked mutable buffers")
	}
	snapshot := decoded.Clone()
	for i := range b {
		b[i] ^= 0xff
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatal("Decode borrowed mutable input bytes")
	}
	decoded.ASDUs[0].Data[0] = 77
	if p.ASDUs[0].Data[0] != 1 {
		t.Fatal("decoded sample data aliases caller model")
	}
	for _, security := range [][]byte{nil, {}} {
		q := codecPDU()
		q.Security = security
		r, err := DecodePDU(mustEncodePDU(t, q))
		if err != nil || (r.Security == nil) != (security == nil) {
			t.Fatal("optional empty security presence changed")
		}
	}
	f := codecFrame()
	fc := f.Clone()
	fc.VLAN.ID = 42
	fc.PDU.ASDUs[0].Data[0] = 42
	if f.VLAN.ID != 2345 || f.PDU.ASDUs[0].Data[0] != 0 {
		t.Fatal("Frame.Clone borrowed mutable state")
	}
}

func TestSVVLANAndMetadata(t *testing.T) {
	for _, priority := range []uint8{0, 1, 4, 7} {
		for _, dei := range []bool{false, true} {
			for _, vid := range []uint16{0, 1, 2048, 2345, 4094} {
				f := codecFrame()
				f.VLAN = &VLAN{priority, dei, vid}
				b, err := EncodeFrame(f)
				if err != nil {
					t.Fatal(err)
				}
				r, err := DecodeFrame(b)
				if err != nil || !reflect.DeepEqual(r.VLAN, f.VLAN) {
					t.Fatalf("TCI %+v %+v %v", f.VLAN, r.VLAN, err)
				}
			}
		}
	}
	f := codecFrame()
	f.VLAN = nil
	b, err := EncodeFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(b[12:]) != EtherType {
		t.Fatal("untagged EtherType")
	}
	r, err := DecodeFrame(b)
	if err != nil || !reflect.DeepEqual(r, f) {
		t.Fatalf("untagged %v", err)
	}
	f.VLAN = &VLAN{ID: 4095}
	if _, err = EncodeFrame(f); err == nil {
		t.Fatal("encoded reserved VLAN")
	}
	f.VLAN = &VLAN{Priority: 8}
	if _, err = EncodeFrame(f); err == nil {
		t.Fatal("encoded invalid priority")
	}
}

func TestSVMalformedFrames(t *testing.T) {
	good, err := EncodeFrame(codecFrame())
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 1, 13, 14, 17, 18, 20, 25, 30, 56} {
		if _, err := DecodeFrame(good[:size]); err == nil {
			t.Fatalf("accepted truncated capture %d", size)
		}
	}
	for _, length := range []uint16{0, 7, 1501, 0xffff} {
		bad := bytes.Clone(good)
		binary.BigEndian.PutUint16(bad[20:], length)
		if _, err := DecodeFrame(bad); err == nil {
			t.Fatalf("accepted bad length %d", length)
		}
	}
	bad := bytes.Clone(good)
	binary.BigEndian.PutUint16(bad[16:], 0x0800)
	if _, err = DecodeFrame(bad); !errors.Is(err, ErrNotSV) {
		t.Fatalf("other EtherType %v", err)
	}
	bad = bytes.Clone(good)
	binary.BigEndian.PutUint16(bad[14:], 0x0fff)
	if _, err = DecodeFrame(bad); err == nil {
		t.Fatal("accepted reserved VLAN ID")
	}
	if _, err = DecodeFrame(append(good, make([]byte, MaxFrameSize)...)); err == nil {
		t.Fatal("accepted oversize capture")
	}
}

func TestSVMalformedAPDUAndASDU(t *testing.T) {
	validASDU, _ := encodeASDU(codecASDU())
	makePDU := func(count []byte, sequence []byte) []byte { return ber.Encode(0x60, count, ber.Encode(0xa2, sequence)) }
	bad := [][]byte{
		{}, {0x60, 0}, {0x61, 0}, append(mustEncodePDU(t, codecPDU()), 0),
		makePDU(ber.Uint(0x80, 0), nil), makePDU(ber.Uint(0x80, 2), validASDU), makePDU(ber.Uint(0x80, 1), append(validASDU, validASDU...)), makePDU(ber.Uint(0x80, 256), nil),
		ber.Encode(0x60, ber.Uint(0x80, 1), ber.Uint(0x80, 1), ber.Encode(0xa2, validASDU)),
		ber.Encode(0x60, ber.Encode(0xa2, validASDU), ber.Uint(0x80, 1)),
		ber.Encode(0x60, ber.Uint(0x80, 1), ber.Encode(0x82), ber.Encode(0xa2, validASDU)),
		makePDU(ber.Uint(0x80, 1), ber.Encode(0x31)),
	}
	for i, b := range bad {
		if _, err := DecodePDU(b); err == nil {
			t.Fatalf("accepted malformed APDU %d: %x", i, b)
		}
	}
	base := [][]byte{ber.Encode(0x80, []byte("sv1")), fixed16(0x82, 123), fixed32(0x83, 3), ber.Encode(0x85, []byte{2}), ber.Encode(0x87, []byte{1})}
	for missing := range base {
		var fields [][]byte
		for i, v := range base {
			if i != missing {
				fields = append(fields, v)
			}
		}
		if _, err := DecodePDU(makePDU(ber.Uint(0x80, 1), ber.Encode(0x30, fields...))); err == nil {
			t.Fatalf("accepted missing required field %d", missing)
		}
	}
	for _, index := range []int{1, 2, 3} {
		fields := append([][]byte(nil), base...)
		fields[index] = ber.Encode(base[index][0], []byte{1})
		if index == 3 {
			fields[index] = ber.Encode(0x85, []byte{0, 2})
		}
		if _, err := DecodePDU(makePDU(ber.Uint(0x80, 1), ber.Encode(0x30, fields...))); err == nil {
			t.Fatalf("accepted wrong fixed width %d", index)
		}
	}
	for _, extra := range [][]byte{ber.Encode(0x88, []byte{1}), ber.Encode(0x88, []byte{0, 3}), ber.Encode(0x89, []byte{1}), ber.Encode(0x8a, []byte{0})} {
		fields := append(append([][]byte(nil), base...), extra)
		if _, err := DecodePDU(makePDU(ber.Uint(0x80, 1), ber.Encode(0x30, fields...))); err == nil {
			t.Fatalf("accepted invalid optional field %x", extra)
		}
	}
	fields := append(append([][]byte(nil), base...), base[4])
	if _, err := DecodePDU(makePDU(ber.Uint(0x80, 1), ber.Encode(0x30, fields...))); err == nil {
		t.Fatal("accepted duplicate ASDU field")
	}
}

func TestSVEncodingBounds(t *testing.T) {
	p := codecPDU()
	p.ASDUs[0].SVID = strings.Repeat("x", 129)
	if _, err := EncodePDU(p); err != nil {
		t.Fatal(err)
	}
	p.ASDUs[0].SVID += "x"
	if _, err := EncodePDU(p); err == nil {
		t.Fatal("encoded long svID")
	}
	for _, s := range []string{"", "line\n", "中文"} {
		p = codecPDU()
		p.ASDUs[0].SVID = s
		if _, err := EncodePDU(p); err == nil {
			t.Fatalf("encoded invalid svID %q", s)
		}
	}
	p = codecPDU()
	p.ASDUs[0].Data = make([]byte, MaxAPDUSize)
	if _, err := EncodePDU(p); err == nil {
		t.Fatal("encoded oversize ASDU")
	}
	p = codecPDU()
	p.Security = make([]byte, MaxAPDUSize)
	if _, err := EncodePDU(p); err == nil {
		t.Fatal("encoded oversize security")
	}
	if _, err := EncodePDU(PDU{}); err == nil {
		t.Fatal("encoded zero ASDU")
	}
	if _, err := EncodePDU(PDU{ASDUs: make([]ASDU, MaxASDUs+1)}); err == nil {
		t.Fatal("encoded too many ASDUs")
	}
	p = codecPDU()
	p.ASDUs[0].SampleCount = math.MaxUint16
	p.ASDUs[0].ConfRev = math.MaxUint32
	p.ASDUs[0].Data = []byte{}
	r, err := DecodePDU(mustEncodePDU(t, p))
	if err != nil || r.ASDUs[0].SampleCount != math.MaxUint16 || r.ASDUs[0].ConfRev != math.MaxUint32 || len(r.ASDUs[0].Data) != 0 {
		t.Fatalf("unsigned boundaries %+v %v", r, err)
	}
}

func FuzzSVPDU(f *testing.F) {
	b, _ := EncodePDU(codecPDU())
	f.Add(b)
	f.Add([]byte{0x60, 0})
	f.Add([]byte{0x60, 0x80, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := DecodePDU(b)
		if err != nil {
			return
		}
		encoded, err := EncodePDU(p)
		if err != nil {
			t.Fatalf("decoded unencodable APDU: %v", err)
		}
		q, err := DecodePDU(encoded)
		if err != nil || !reflect.DeepEqual(p, q) {
			t.Fatalf("APDU semantic roundtrip %+v %+v %v", p, q, err)
		}
	})
}
func FuzzSVFrame(f *testing.F) {
	b, _ := EncodeFrame(codecFrame())
	f.Add(b)
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, b []byte) {
		frame, err := DecodeFrame(b)
		if err != nil {
			return
		}
		encoded, err := EncodeFrame(frame)
		if err != nil {
			t.Fatal(err)
		}
		again, err := DecodeFrame(encoded)
		if err != nil || !reflect.DeepEqual(frame, again) {
			t.Fatalf("frame semantic roundtrip: %v", err)
		}
	})
}
