// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Online GOOSE/SV control-block management. These reads and writes operate on
// the peer's declared control-block attributes; the peer decides which fields
// are writable and reports its refusal unchanged. Only the fields the
// reference implementation exposes for online change are written here.

// PhysicalCommunicationAddress is the DstAddress structure of a control block.
type PhysicalCommunicationAddress struct {
	VLANPriority   uint8
	VLANID         uint16
	APPID          uint16
	DestinationMAC string
}

// GooseControlBlock is one online GoCB declaration.
type GooseControlBlock struct {
	Reference  string
	GoEna      bool
	GoID       string
	DataSet    string
	ConfRev    uint32
	NdsCom     bool
	DstAddress PhysicalCommunicationAddress
	MinTime    uint32 // milliseconds
	MaxTime    uint32 // milliseconds
	FixedOffs  bool
}

// SampleValueControlBlock is one online SVCB declaration.
type SampleValueControlBlock struct {
	Reference string
	SvEna     bool
	Resv      bool
	MsvID     string
	DataSet   string
	ConfRev   uint32
	SmpRate   uint32
	OptFlds   []byte
	SmpMod    uint8
	NoASDU    uint8
}

func gocbAttribute(block mms.ObjectName, field string) mms.ObjectName {
	return mms.DomainName(block.Domain, block.Item+"$"+field)
}

func readOptional(ctx context.Context, c *Client, name mms.ObjectName) (mms.Value, bool, error) {
	value, err := c.Read(ctx, name)
	if err != nil {
		if strings.Contains(err.Error(), "non-existent") || strings.Contains(err.Error(), "undefined") {
			return mms.Value{}, false, nil
		}
		return mms.Value{}, false, err
	}
	return value, true, nil
}

// ReadGoCB reads the declared GoCB attributes. Optional fields keep their zero
// value when the peer does not declare them; a declared field with the wrong
// type is a protocol error, not a zero value.
func (c *Client) ReadGoCB(ctx context.Context, block mms.ObjectName) (GooseControlBlock, error) {
	if block.Scope != mms.DomainScope || !strings.Contains(block.Item, "$GO$") {
		return GooseControlBlock{}, fmt.Errorf("ied: expected a domain-scoped GO control block")
	}
	out := GooseControlBlock{Reference: block.String()}
	read := func(field string, apply func(mms.Value) error) error {
		value, ok, err := readOptional(ctx, c, gocbAttribute(block, field))
		if err != nil || !ok {
			return err
		}
		return apply(value)
	}
	if err := read("GoEna", func(v mms.Value) (e error) { out.GoEna, e = v.Bool(); return }); err != nil {
		return out, err
	}
	if err := read("GoID", func(v mms.Value) (e error) { out.GoID, e = v.StringValue(); return }); err != nil {
		return out, err
	}
	if err := read("DatSet", func(v mms.Value) (e error) { out.DataSet, e = v.StringValue(); return }); err != nil {
		return out, err
	}
	if err := read("ConfRev", func(v mms.Value) (e error) { e = uint32Into(v, &out.ConfRev); return }); err != nil {
		return out, err
	}
	if err := read("NdsCom", func(v mms.Value) (e error) { out.NdsCom, e = v.Bool(); return }); err != nil {
		return out, err
	}
	if err := read("MinTime", func(v mms.Value) (e error) { e = uint32Into(v, &out.MinTime); return }); err != nil {
		return out, err
	}
	if err := read("MaxTime", func(v mms.Value) (e error) { e = uint32Into(v, &out.MaxTime); return }); err != nil {
		return out, err
	}
	if err := read("FixedOffs", func(v mms.Value) (e error) { out.FixedOffs, e = v.Bool(); return }); err != nil {
		return out, err
	}
	if err := read("DstAddress", func(v mms.Value) error {
		parts, err := v.Elements()
		if err != nil || len(parts) != 4 {
			return fmt.Errorf("ied: invalid GoCB DstAddress")
		}
		priority, err := parts[0].Uint()
		if err != nil {
			return err
		}
		vlan, err := parts[1].Uint()
		if err != nil {
			return err
		}
		app, err := parts[2].Uint()
		if err != nil {
			return err
		}
		mac, err := parts[3].StringValue()
		if err != nil {
			return err
		}
		out.DstAddress = PhysicalCommunicationAddress{VLANPriority: uint8(priority), VLANID: uint16(vlan), APPID: uint16(app), DestinationMAC: mac}
		return nil
	}); err != nil {
		return out, err
	}
	return out, nil
}

// SetGoCBEnabled writes GoEna, the one online field with a settled behavior:
// the peer starts or stops publishing the engineered stream.
func (c *Client) SetGoCBEnabled(ctx context.Context, block mms.ObjectName, enabled bool) error {
	if !strings.Contains(block.Item, "$GO$") {
		return fmt.Errorf("ied: expected a GO control block")
	}
	return c.Write(ctx, gocbAttribute(block, "GoEna"), mms.Boolean(enabled))
}

// ReadSVCB reads the declared SVCB attributes.
func (c *Client) ReadSVCB(ctx context.Context, block mms.ObjectName) (SampleValueControlBlock, error) {
	if block.Scope != mms.DomainScope || !strings.Contains(block.Item, "$MS$") {
		return SampleValueControlBlock{}, fmt.Errorf("ied: expected a domain-scoped MS control block")
	}
	out := SampleValueControlBlock{Reference: block.String()}
	read := func(field string, apply func(mms.Value) error) error {
		value, ok, err := readOptional(ctx, c, gocbAttribute(block, field))
		if err != nil || !ok {
			return err
		}
		return apply(value)
	}
	if err := read("SvEna", func(v mms.Value) (e error) { out.SvEna, e = v.Bool(); return }); err != nil {
		return out, err
	}
	if err := read("Resv", func(v mms.Value) (e error) { out.Resv, e = v.Bool(); return }); err != nil {
		return out, err
	}
	if err := read("MsvID", func(v mms.Value) (e error) { out.MsvID, e = v.StringValue(); return }); err != nil {
		return out, err
	}
	if err := read("DatSet", func(v mms.Value) (e error) { out.DataSet, e = v.StringValue(); return }); err != nil {
		return out, err
	}
	if err := read("ConfRev", func(v mms.Value) (e error) { e = uint32Into(v, &out.ConfRev); return }); err != nil {
		return out, err
	}
	if err := read("SmpRate", func(v mms.Value) (e error) { e = uint32Into(v, &out.SmpRate); return }); err != nil {
		return out, err
	}
	if err := read("OptFlds", func(v mms.Value) (e error) { out.OptFlds, e = v.Bytes(); return }); err != nil {
		return out, err
	}
	if err := read("SmpMod", func(v mms.Value) (e error) { e = uint8Into(v, &out.SmpMod); return }); err != nil {
		return out, err
	}
	if err := read("NoASDU", func(v mms.Value) (e error) { e = uint8Into(v, &out.NoASDU); return }); err != nil {
		return out, err
	}
	return out, nil
}

// SetSVCBEnabled writes SvEna.
func (c *Client) SetSVCBEnabled(ctx context.Context, block mms.ObjectName, enabled bool) error {
	if !strings.Contains(block.Item, "$MS$") {
		return fmt.Errorf("ied: expected an MS control block")
	}
	return c.Write(ctx, gocbAttribute(block, "SvEna"), mms.Boolean(enabled))
}

func uint32Into(v mms.Value, out *uint32) error {
	n, err := v.Uint()
	if err != nil || n > 1<<32-1 {
		return fmt.Errorf("ied: value does not fit uint32")
	}
	*out = uint32(n)
	return nil
}

func uint8Into(v mms.Value, out *uint8) error {
	n, err := v.Uint()
	if err != nil || n > 255 {
		return fmt.Errorf("ied: value does not fit uint8")
	}
	*out = uint8(n)
	return nil
}
