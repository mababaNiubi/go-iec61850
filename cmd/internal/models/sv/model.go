// SPDX-License-Identifier: GPL-3.0-or-later
// Package svmodel supplies an explicit example layout of eight INT32/Quality pairs.
package svmodel

import (
	"encoding/binary"
	"github.com/mababaNiubi/go-iec61850/sv"
)

func Frame(source [6]byte) sv.Frame {
	values := []int32{1, -2, 3, -4, 5, -6, 7, -8}
	qualities := []uint32{0, 1, 2, 3, 0x0400, 0x0800, 0x1000, 0x1fff}
	raw := make([]byte, 64)
	for i, value := range values {
		binary.BigEndian.PutUint32(raw[i*8:], uint32(value))
		binary.BigEndian.PutUint32(raw[i*8+4:], qualities[i])
	}
	return sv.Frame{Destination: [6]byte{1, 0x0c, 0xcd, 4, 0, 0xaa}, Source: source, VLAN: &sv.VLAN{Priority: 4, ID: 123}, APPID: 0x4000, PDU: sv.PDU{ASDUs: []sv.ASDU{{SVID: "sampleSV1", DataSet: "sample/LLN0$sv", SampleCount: 7, ConfRev: 3, SampleSync: 2, Data: raw}}}}
}
