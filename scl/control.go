// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Command is an immediate binary SPC/DPC Oper with normal security. Origin
// category 2 denotes station control. Check bit 0 requests synchrocheck and
// bit 1 requests interlocking. The application implements those checks.
type Command struct {
	Value                              bool
	OriginCategory                     int
	OriginID                           []byte
	Number                             uint8
	Time                               time.Time
	Test, SynchroCheck, InterlockCheck bool
}

// MMSValue encodes the complete Oper structure expected by a direct-control IED.
func (c Command) MMSValue() (mms.Value, error) {
	if c.OriginCategory < 0 || c.OriginCategory > 8 || len(c.OriginID) > 64 {
		return mms.Value{}, fmt.Errorf("scl: invalid control origin")
	}
	t := mms.UTC(c.Time)
	if t.Kind() != mms.KindUTC {
		return mms.Value{}, fmt.Errorf("scl: control time is outside MMS UTC range")
	}
	var flags byte
	if c.SynchroCheck {
		flags |= 0x80
	}
	if c.InterlockCheck {
		flags |= 0x40
	}
	check, err := mms.BitString([]byte{flags}, 2)
	if err != nil {
		return mms.Value{}, err
	}
	origin := mms.Structure(mms.Integer(int64(c.OriginCategory)), mms.OctetString(c.OriginID))
	return mms.Structure(mms.Boolean(c.Value), origin, mms.Unsigned(uint64(c.Number)), t, mms.Boolean(c.Test), check), nil
}

type control struct {
	perform   func(context.Context, Command) error
	busy      chan struct{}
	model     int64
	owner     *mms.Association
	deadline  time.Time
	selection mms.Value
}

// OnOperate binds one whole Oper object to a hardware or simulation handler.
// Only a direct-with-normal-security binary control may be bound. Concurrent
// commands to the same object receive TemporarilyUnavailable while it is busy.
func (d *IED) OnOperate(name mms.ObjectName, perform func(context.Context, Command) error) error {
	model := settingName(name, "CF")
	model.Item = strings.TrimSuffix(model.Item, "$Oper") + "$ctlModel"
	v, err := d.model.Read(context.Background(), mms.Access(model))
	mode, _ := v.Int()
	if err != nil || mode != 1 {
		return fmt.Errorf("scl: OnOperate requires ctlModel=1; use OnControl for modeled selection/termination")
	}
	return d.OnControl(name, perform)
}

// OnControl binds an immediate SPC/DPC actuator for ctlModel 1..4. SBO modes
// require their SCL-declared SBO/SBOw objects. The server owns selection,
// expiry and enhanced termination. The callback owns hardware execution.
// For enhanced models it runs only after the successful Oper Write response.
// A callback failure then produces negative CommandTermination, not a rejected
// acceptance. Its context lives until the association closes.
func (d *IED) OnControl(name mms.ObjectName, perform func(context.Context, Command) error) error {
	if perform == nil || !d.binary[name] {
		return fmt.Errorf("scl: %s is not a modeled binary Oper, or handler is nil", name)
	}
	spec := d.types[name]
	if !operLayout(spec) {
		return fmt.Errorf("scl: unsupported Oper layout at %s", name)
	}
	parts := strings.Split(name.Item, "$")
	parts[1] = "CF"
	parts[len(parts)-1] = "ctlModel"
	model, err := d.model.Read(context.Background(), mms.Access(mms.DomainName(name.Domain, strings.Join(parts, "$"))))
	if err != nil {
		return fmt.Errorf("scl: %s ctlModel: %w", name, err)
	}
	mode, err := model.Int()
	if err != nil || mode < 1 || mode > 4 {
		return fmt.Errorf("scl: %s requires ctlModel 1..4", name)
	}
	base := strings.TrimSuffix(name.Item, "$Oper")
	if mode == 2 && d.types[mms.DomainName(name.Domain, base+"$SBO")].Kind != mms.KindVisibleString {
		return fmt.Errorf("scl: missing SBO declaration")
	}
	if mode == 4 && !operLayout(d.types[mms.DomainName(name.Domain, base+"$SBOw")]) {
		return fmt.Errorf("scl: missing compatible SBOw declaration")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handlers[name] != nil {
		return fmt.Errorf("scl: duplicate control handler for %s", name)
	}
	d.handlers[name] = &control{perform: perform, busy: make(chan struct{}, 1), model: mode}
	return nil
}

func operLayout(t mms.TypeSpec) bool {
	if t.Kind != mms.KindStructure || len(t.Components) != 6 {
		return false
	}
	names := [...]string{"ctlVal", "origin", "ctlNum", "T", "Test", "Check"}
	kinds := [...]mms.Kind{mms.KindBoolean, mms.KindStructure, mms.KindUnsigned, mms.KindUTC, mms.KindBoolean, mms.KindBitString}
	for i, c := range t.Components {
		if c.Name != names[i] || c.Type.Kind != kinds[i] {
			return false
		}
	}
	o := t.Components[1].Type.Components
	return len(o) == 2 && o[0].Name == "orCat" && o[0].Type.Kind == mms.KindInteger && o[1].Name == "orIdent" && o[1].Type.Kind == mms.KindOctetString &&
		o[1].Type.Length == -64 && t.Components[2].Type.Width == 8 && (t.Components[5].Type.Length == -2 || t.Components[5].Type.Length == 2)
}

// Write is the MMS access policy. Only an exact, registered whole Oper is
// dispatched. Writes to its children, ST, MX, CF or their ancestors are denied.
func (d *IED) Write(ctx context.Context, a mms.VariableAccess, value mms.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if handled, err := d.writeService(ctx, a, value); handled {
		return err
	}
	if handled, err := d.writeControl(ctx, a, value); handled {
		return err
	}
	_, known := d.types[a.Name]
	if !known {
		return mms.ObjectNonExistent
	}
	return mms.ObjectAccessDenied
}

// The caller has checked the entire value against an accepted Oper layout.
func decodeCommand(v mms.Value) (Command, error) {
	e, _ := v.Elements()
	o, _ := e[1].Elements()
	cat, _ := o[0].Int()
	id, _ := o[1].Bytes()
	if cat < 0 || cat > 8 {
		return Command{}, mms.ObjectValueInvalid
	}
	on, _ := e[0].Bool()
	num, _ := e[2].Uint()
	t, err := e[3].Time()
	if err != nil {
		return Command{}, mms.ObjectValueInvalid
	}
	test, _ := e[4].Bool()
	n, _ := e[5].BitLength()
	flags, _ := e[5].Bytes()
	if n != 2 {
		return Command{}, mms.TypeInconsistent
	}
	return Command{Value: on, OriginCategory: int(cat), OriginID: id, Number: uint8(num), Time: t, Test: test,
		SynchroCheck: flags[0]&0x80 != 0, InterlockCheck: flags[0]&0x40 != 0}, nil
}

// Position is the IEC double-point status carried as two bits on the wire.
type Position uint8

const (
	Intermediate Position = iota
	Open
	Closed
	BadPosition
)

func (p Position) MMSValue() (mms.Value, error) {
	if p > BadPosition {
		return mms.Value{}, fmt.Errorf("scl: invalid Dbpos %d", p)
	}
	return mms.BitString([]byte{byte(p) << 6}, 2)
}
func ReadPosition(v mms.Value) (Position, error) {
	n, err := v.BitLength()
	if err != nil || n != 2 {
		return 0, fmt.Errorf("scl: expected two-bit Dbpos")
	}
	b, err := v.Bytes()
	if err != nil {
		return 0, err
	}
	return Position(b[0] >> 6), nil
}
func (p Position) String() string {
	switch p {
	case Intermediate:
		return "intermediate"
	case Open:
		return "open"
	case Closed:
		return "closed"
	case BadPosition:
		return "bad"
	}
	return "unknown"
}
