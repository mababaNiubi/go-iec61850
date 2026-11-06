// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import "encoding/binary"

const (
	EtherType uint16 = 0x88b8
	// MaxFrameSize is the maximum single-VLAN Ethernet frame, excluding FCS.
	MaxFrameSize       = 1518
	ethernetPayloadMax = 1500
	ethernetMinimum    = 60
)

// VLAN is one IEEE 802.1Q tag. VID 0 is a priority tag; VID 4095 is reserved.
type VLAN struct {
	Priority uint8
	DEI      bool
	ID       uint16
}

// Frame contains a GOOSE Ethernet header and APDU, excluding Ethernet FCS.
// Reserved fields are preserved without implementing their security semantics.
type Frame struct {
	Destination, Source         [6]byte
	VLAN                        *VLAN
	APPID, Reserved1, Reserved2 uint16
	PDU                         PDU
}

// Clone returns independent mutable metadata and dataset slices.
func (f Frame) Clone() Frame {
	if f.VLAN != nil {
		v := *f.VLAN
		f.VLAN = &v
	}
	f.PDU = f.PDU.Clone()
	return f
}

// EncodeFrame emits one Ethernet frame with zero padding to the minimum frame
// length. The GOOSE length counts its eight-octet header and APDU only.
func EncodeFrame(f Frame) ([]byte, error) {
	if f.VLAN != nil && (f.VLAN.Priority > 7 || f.VLAN.ID >= 4095) {
		return nil, malformed("invalid VLAN priority or ID")
	}
	apdu, err := EncodePDU(f.PDU)
	if err != nil {
		return nil, err
	}
	headerSize := 14
	if f.VLAN != nil {
		headerSize += 4
	}
	length := 8 + len(apdu)
	if length > ethernetPayloadMax {
		return nil, malformed("GOOSE payload exceeds Ethernet MTU")
	}
	frame := make([]byte, max(ethernetMinimum, headerSize+length))
	copy(frame[:6], f.Destination[:])
	copy(frame[6:12], f.Source[:])
	pos := 12
	if f.VLAN != nil {
		binary.BigEndian.PutUint16(frame[pos:], 0x8100)
		tci := uint16(f.VLAN.Priority)<<13 | f.VLAN.ID
		if f.VLAN.DEI {
			tci |= 1 << 12
		}
		binary.BigEndian.PutUint16(frame[pos+2:], tci)
		pos += 4
	}
	binary.BigEndian.PutUint16(frame[pos:], EtherType)
	pos += 2
	binary.BigEndian.PutUint16(frame[pos:], f.APPID)
	binary.BigEndian.PutUint16(frame[pos+2:], uint16(length))
	binary.BigEndian.PutUint16(frame[pos+4:], f.Reserved1)
	binary.BigEndian.PutUint16(frame[pos+6:], f.Reserved2)
	copy(frame[pos+8:], apdu)
	return frame, nil
}

// DecodeFrame accepts an untagged or single-802.1Q-tagged Ethernet frame. The
// caller supplies bytes without FCS. Capture buffers need not contain Ethernet
// minimum padding; trailing padding is ignored using the GOOSE length field.
func DecodeFrame(encoded []byte) (Frame, error) {
	var f Frame
	if len(encoded) < 14 {
		return f, malformed("truncated Ethernet header")
	}
	copy(f.Destination[:], encoded[:6])
	copy(f.Source[:], encoded[6:12])
	pos := 12
	protocol := binary.BigEndian.Uint16(encoded[pos:])
	headerSize := 14
	if protocol == 0x8100 {
		if len(encoded) < 18 {
			return Frame{}, malformed("truncated VLAN header")
		}
		tci := binary.BigEndian.Uint16(encoded[pos+2:])
		v := VLAN{Priority: uint8(tci >> 13), DEI: tci&(1<<12) != 0, ID: tci & 0x0fff}
		if v.ID == 4095 {
			return Frame{}, malformed("reserved VLAN ID")
		}
		f.VLAN = &v
		pos += 4
		headerSize += 4
		protocol = binary.BigEndian.Uint16(encoded[pos:])
	}
	if protocol != EtherType {
		return Frame{}, ErrNotGOOSE
	}
	pos += 2
	if len(encoded) > headerSize+ethernetPayloadMax {
		return Frame{}, malformed("Ethernet frame exceeds supported MTU")
	}
	if len(encoded)-pos < 8 {
		return Frame{}, malformed("truncated GOOSE header")
	}
	f.APPID = binary.BigEndian.Uint16(encoded[pos:])
	length := int(binary.BigEndian.Uint16(encoded[pos+2:]))
	f.Reserved1 = binary.BigEndian.Uint16(encoded[pos+4:])
	f.Reserved2 = binary.BigEndian.Uint16(encoded[pos+6:])
	if length < 8 || length > ethernetPayloadMax || length > len(encoded)-pos {
		return Frame{}, malformed("invalid or truncated GOOSE length")
	}
	p, err := DecodePDU(encoded[pos+8 : pos+length])
	if err != nil {
		return Frame{}, err
	}
	f.PDU = p
	return f, nil
}
