// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import "encoding/binary"

const (
	EtherType          uint16 = 0x88ba
	MaxFrameSize              = 1518 // single-VLAN Ethernet, excluding FCS
	ethernetPayloadMax        = 1500
	ethernetMinimum           = 60
)

// VLAN is one IEEE 802.1Q tag. VID4095 is reserved; VID0 is a priority tag.
type VLAN struct {
	Priority uint8
	DEI      bool
	ID       uint16
}

// Frame contains an SV Ethernet header and APDU, without Ethernet FCS.
// Reserved words are preserved; their security semantics are not implemented.
type Frame struct {
	Destination, Source         [6]byte
	VLAN                        *VLAN
	APPID, Reserved1, Reserved2 uint16
	PDU                         PDU
}

func (f Frame) Clone() Frame {
	if f.VLAN != nil {
		v := *f.VLAN
		f.VLAN = &v
	}
	f.PDU = f.PDU.Clone()
	return f
}

func EncodeFrame(f Frame) ([]byte, error) {
	if f.VLAN != nil && (f.VLAN.Priority > 7 || f.VLAN.ID >= 4095) {
		return nil, malformed("invalid VLAN priority or ID")
	}
	content, err := EncodePDU(f.PDU)
	if err != nil {
		return nil, err
	}
	headerSize := 14
	if f.VLAN != nil {
		headerSize += 4
	}
	length := 8 + len(content)
	if length > ethernetPayloadMax {
		return nil, malformed("SV payload exceeds Ethernet MTU")
	}
	b := make([]byte, max(ethernetMinimum, headerSize+length))
	copy(b[:6], f.Destination[:])
	copy(b[6:12], f.Source[:])
	pos := 12
	if f.VLAN != nil {
		binary.BigEndian.PutUint16(b[pos:], 0x8100)
		tci := uint16(f.VLAN.Priority)<<13 | f.VLAN.ID
		if f.VLAN.DEI {
			tci |= 1 << 12
		}
		binary.BigEndian.PutUint16(b[pos+2:], tci)
		pos += 4
	}
	binary.BigEndian.PutUint16(b[pos:], EtherType)
	pos += 2
	binary.BigEndian.PutUint16(b[pos:], f.APPID)
	binary.BigEndian.PutUint16(b[pos+2:], uint16(length))
	binary.BigEndian.PutUint16(b[pos+4:], f.Reserved1)
	binary.BigEndian.PutUint16(b[pos+6:], f.Reserved2)
	copy(b[pos+8:], content)
	return b, nil
}

// DecodeFrame uses the SV length to exclude padding. Capture input is expected
// without FCS and may omit minimum Ethernet padding. Only one VLAN is supported.
func DecodeFrame(b []byte) (Frame, error) {
	var f Frame
	if len(b) < 14 {
		return f, malformed("truncated Ethernet header")
	}
	copy(f.Destination[:], b[:6])
	copy(f.Source[:], b[6:12])
	pos, headerSize := 12, 14
	kind := binary.BigEndian.Uint16(b[pos:])
	if kind == 0x8100 {
		if len(b) < 18 {
			return Frame{}, malformed("truncated VLAN header")
		}
		tci := binary.BigEndian.Uint16(b[pos+2:])
		v := VLAN{Priority: uint8(tci >> 13), DEI: tci&(1<<12) != 0, ID: tci & 0x0fff}
		if v.ID == 4095 {
			return Frame{}, malformed("reserved VLAN ID")
		}
		f.VLAN = &v
		pos += 4
		headerSize += 4
		kind = binary.BigEndian.Uint16(b[pos:])
	}
	if kind != EtherType {
		return Frame{}, ErrNotSV
	}
	pos += 2
	if len(b) > headerSize+ethernetPayloadMax {
		return Frame{}, malformed("Ethernet frame exceeds MTU")
	}
	if len(b)-pos < 8 {
		return Frame{}, malformed("truncated SV header")
	}
	f.APPID = binary.BigEndian.Uint16(b[pos:])
	length := int(binary.BigEndian.Uint16(b[pos+2:]))
	f.Reserved1 = binary.BigEndian.Uint16(b[pos+4:])
	f.Reserved2 = binary.BigEndian.Uint16(b[pos+6:])
	if length < 8 || length > ethernetPayloadMax || length > len(b)-pos {
		return Frame{}, malformed("invalid or truncated SV length")
	}
	pdu, err := DecodePDU(b[pos+8 : pos+length])
	if err != nil {
		return Frame{}, err
	}
	f.PDU = pdu
	return f, nil
}
