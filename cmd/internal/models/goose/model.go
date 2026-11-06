// SPDX-License-Identifier: GPL-3.0-or-later
// Package goosemodel supplies the deterministic dataset shared with C fixtures.
package goosemodel

import (
	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/goose"
)

func Frame(source [6]byte) goose.Frame {
	bits, _ := data.BitString([]byte{0xa5, 0xc0}, 10)
	binaryTime, _ := data.BinaryTime([]byte{0, 0x12, 0xd6, 0x87, 0x30, 0x39})
	return goose.Frame{Source: source, Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 0xaa}, VLAN: &goose.VLAN{Priority: 4, ID: 123}, APPID: 0x1000, PDU: goose.PDU{GoCBRef: "sample/LLN0$GO$interop", DataSet: "sample/LLN0$interop", GoID: "interop", TimeAllowedToLive: 1000, Timestamp: [8]byte{0x65, 0x53, 0xf1, 0, 0x80, 0, 0, 0x0a}, StateNumber: 7, SequenceNumber: 11, ConfRev: 3, Values: []data.Value{data.Boolean(true), data.Integer(-123), data.Unsigned(123456), data.Float32(1.25), data.Float64(-123.5), bits, data.OctetString([]byte{0, 0x7f, 0x80, 0xff}), data.VisibleString("goose"), data.UTCFromBytes([8]byte{0x65, 0x53, 0xf1, 0, 0x80, 0, 0, 0x0a}), binaryTime, data.Array(data.Integer(1), data.Integer(2), data.Integer(3)), data.Structure(data.Boolean(false), data.Integer(7))}}}
}
