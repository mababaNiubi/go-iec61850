// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"strings"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func (d *IED) readService(ctx context.Context, a mms.VariableAccess) (mms.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return mms.Value{}, true, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(a.Selection.Path) > 0 || a.Selection.Index != nil {
		value, handled, err := d.readServiceLocked(ctx, mms.Access(a.Name))
		if !handled || err != nil {
			return value, handled, err
		}
		value, err = mms.SelectedValue(d.types[a.Name], value, a.Selection)
		return value, true, err
	}
	if a.Selection.Component != "" && a.Selection.Index == nil {
		a.Name.Item += "$" + a.Selection.Component
		a.Selection.Component = ""
	}
	if a.Selection.Index != nil {
		return mms.Value{}, false, nil
	}
	return d.readServiceLocked(ctx, a)
}
func (d *IED) readServiceLocked(ctx context.Context, a mms.VariableAccess) (mms.Value, bool, error) {
	if v, ok, err := d.reportAccessLocked(ctx, a.Name, nil); ok {
		return v, true, err
	}
	if v, ok, err := d.settingAccessLocked(ctx, a.Name, nil); ok {
		return v, true, err
	}
	if v, ok, err := d.readControlLocked(ctx, a); ok {
		return v, true, err
	}
	dynamic := false
	for n := range d.reports {
		if n.Domain == a.Name.Domain && strings.HasPrefix(n.Item, a.Name.Item+"$") {
			dynamic = true
			break
		}
	}
	if g := d.settings[a.Name.Domain]; g != nil {
		dynamic = dynamic || strings.HasPrefix(g.name.Item, a.Name.Item+"$")
		for _, field := range d.types[a.Name].Components {
			if field.Name == "SG" || field.Name == "SE" {
				dynamic = true
			}
		}
	}
	if !dynamic {
		return mms.Value{}, false, nil
	}
	var values []mms.Value
	for _, field := range d.types[a.Name].Components {
		child := mms.Access(mms.DomainName(a.Name.Domain, a.Name.Item+"$"+field.Name))
		v, ok, err := d.readServiceLocked(ctx, child)
		if !ok {
			v, err = d.model.Read(ctx, child)
		}
		if err != nil {
			return mms.Value{}, true, err
		}
		values = append(values, v)
	}
	return mms.Structure(values...), true, nil
}

type setpoint struct {
	apply func(context.Context, mms.Value) error
	busy  chan struct{}
}

// OnSetpoint registers a validated SP write. A rejected callback preserves the
// prior value. A successful callback commits SP locally. Submit independent
// equipment feedback with Update. Concurrent writes to this point return busy.
func (d *IED) OnSetpoint(name mms.ObjectName, apply func(context.Context, mms.Value) error) error {
	if apply == nil {
		return mms.ObjectAccessDenied
	}
	if _, err := d.Type(context.Background(), name); err != nil {
		return err
	}
	if fcOf(name) != "SP" || d.isReadOnly(name) {
		return mms.ObjectAccessDenied
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.setpoints[name] != nil {
		return mms.ObjectAccessDenied
	}
	d.setpoints[name] = &setpoint{apply: apply, busy: make(chan struct{}, 1)}
	return nil
}

func (d *IED) isReadOnly(name mms.ObjectName) bool {
	parts := strings.Split(name.Item, "$")
	if len(parts) < 3 {
		return false
	}
	reference := name.Domain + "/" + parts[0] + "." + strings.Join(parts[2:], ".")
	for _, path := range d.readOnlyPaths {
		if reference == path || strings.HasPrefix(reference, path+".") || strings.HasPrefix(path, reference+".") {
			return true
		}
	}
	return false
}

func (d *IED) writeService(ctx context.Context, a mms.VariableAccess, v mms.Value) (bool, error) {
	d.mu.Lock()
	if a.Selection.Index != nil || a.Selection.Component != "" || len(a.Selection.Path) > 0 {
		d.mu.Unlock()
		return false, nil
	}
	if _, ok, err := d.reportAccessLocked(ctx, a.Name, &v); ok {
		d.mu.Unlock()
		return true, err
	}
	if _, ok, err := d.settingAccessLocked(ctx, a.Name, &v); ok {
		d.mu.Unlock()
		return true, err
	}
	apply := d.setpoints[a.Name]
	d.mu.Unlock()
	if apply == nil {
		return false, nil
	}
	if err := mms.ValidateValue(d.types[a.Name], v); err != nil {
		return true, mms.TypeInconsistent
	}
	select {
	case apply.busy <- struct{}{}:
		defer func() { <-apply.busy }()
	default:
		return true, mms.TemporarilyUnavailable
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if err := apply.apply(ctx, v); err != nil {
		return true, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return true, d.commitLocked(context.WithoutCancel(ctx), mms.Assignment{Name: a.Name, Value: v})
}
