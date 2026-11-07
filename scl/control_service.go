// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func (d *IED) controlFor(n mms.ObjectName) (*control, mms.ObjectName, string) {
	base, field, ok := strings.Cut(n.Item, "$CO$")
	if !ok {
		return nil, n, ""
	}
	object, suffix, ok := strings.Cut(field, "$")
	if !ok {
		return nil, n, ""
	}
	// DO/SDO references may have more than one component.
	parts := strings.Split(field, "$")
	suffix = parts[len(parts)-1]
	object = strings.Join(parts[:len(parts)-1], "$")
	oper := mms.DomainName(n.Domain, base+"$CO$"+object+"$Oper")
	return d.handlers[oper], oper, suffix
}
func (h *control) expire() {
	if h.owner != nil && (h.owner.Context().Err() != nil || !time.Now().Before(h.deadline)) {
		h.owner = nil
		h.selection = mms.Value{}
	}
}
func (d *IED) selectionTimeout(oper mms.ObjectName) time.Duration {
	name := settingName(oper, "CF")
	name.Item = strings.TrimSuffix(name.Item, "$Oper") + "$sboTimeout"
	v, err := d.model.Read(context.Background(), mms.Access(name))
	if err == nil {
		ms, e := v.Uint()
		if e == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 10 * time.Second
}
func (d *IED) readControlLocked(ctx context.Context, a mms.VariableAccess) (mms.Value, bool, error) {
	h, oper, field := d.controlFor(a.Name)
	if h == nil || field != "SBO" {
		return mms.Value{}, false, nil
	}
	if a.Selection.Index != nil || a.Selection.Component != "" || len(a.Selection.Path) > 0 {
		return mms.Value{}, true, mms.ObjectAccessUnsupported
	}
	h.expire()
	peer := mms.AssociationFromContext(ctx)
	if h.model != 2 || peer == nil || h.owner != nil && h.owner != peer || len(h.busy) > 0 {
		return mms.VisibleString(""), true, nil
	}
	h.owner = peer
	h.deadline = time.Now().Add(d.selectionTimeout(oper))
	return mms.VisibleString(strings.TrimSuffix(oper.String(), "$Oper")), true, nil
}
func (d *IED) writeControl(ctx context.Context, a mms.VariableAccess, value mms.Value) (bool, error) {
	d.mu.Lock()
	h, oper, field := d.controlFor(a.Name)
	if h == nil {
		d.mu.Unlock()
		return false, nil
	}
	if a.Selection.Index != nil || a.Selection.Component != "" || len(a.Selection.Path) > 0 {
		d.mu.Unlock()
		return true, mms.ObjectAccessDenied
	}
	spec, exists := d.types[a.Name]
	if !exists {
		d.mu.Unlock()
		return true, mms.ObjectNonExistent
	}
	if err := mms.ValidateValue(spec, value); err != nil {
		d.mu.Unlock()
		return true, mms.TypeInconsistent
	}
	h.expire()
	peer := mms.AssociationFromContext(ctx)
	if peer == nil {
		d.mu.Unlock()
		return true, mms.ObjectAccessDenied
	}
	if h.owner != nil && h.owner != peer {
		d.mu.Unlock()
		return true, mms.TemporarilyUnavailable
	}
	switch field {
	case "SBOw":
		if h.model != 4 || len(h.busy) > 0 {
			d.mu.Unlock()
			return true, mms.ObjectAccessDenied
		}
		h.owner = peer
		h.selection = value
		h.deadline = time.Now().Add(d.selectionTimeout(oper))
		d.mu.Unlock()
		return true, nil
	case "Cancel":
		if h.owner != peer || len(h.busy) > 0 {
			d.mu.Unlock()
			return true, mms.ObjectAccessDenied
		}
		if h.model == 4 {
			selected, _ := h.selection.Elements()
			cancelled, _ := value.Elements()
			if len(selected) < 3 || len(cancelled) < 3 || !selected[0].Equal(cancelled[0]) || !selected[1].Equal(cancelled[1]) || !selected[2].Equal(cancelled[2]) {
				d.mu.Unlock()
				return true, mms.ObjectValueInvalid
			}
		}
		h.owner = nil
		h.selection = mms.Value{}
		d.mu.Unlock()
		return true, nil
	case "Oper":
		if h.model == 2 || h.model == 4 {
			if h.owner != peer {
				d.mu.Unlock()
				return true, mms.ObjectAccessDenied
			}
			if h.model == 4 && !sameSelection(h.selection, value) {
				d.mu.Unlock()
				return true, mms.ObjectValueInvalid
			}
		}
	default:
		d.mu.Unlock()
		return true, mms.ObjectAccessDenied
	}
	select {
	case h.busy <- struct{}{}:
	default:
		d.mu.Unlock()
		return true, mms.TemporarilyUnavailable
	}
	// Consume the selection before invoking user code. A timeout/retry cannot
	// accidentally execute the same selected command a second time.
	h.owner = nil
	h.selection = mms.Value{}
	d.mu.Unlock()
	cmd, err := decodeCommand(value)
	if err != nil {
		<-h.busy
		return true, err
	}
	if h.model >= 3 {
		// Acceptance and completion are separate protocol evidence. Reserve
		// the operation now, acknowledge it, then execute and terminate it.
		err = mms.AfterResponse(ctx, func(runCtx context.Context, sendErr error) {
			defer func() { <-h.busy }()
			if sendErr != nil || runCtx.Err() != nil {
				return
			}
			operationErr := h.perform(runCtx, cmd)
			report := mms.InformationReport{Variables: []mms.VariableAccess{mms.Access(oper)}, Results: []mms.ReadResult{{Value: value}}}
			if operationErr != nil {
				parts, _ := value.Elements()
				last := mms.Structure(mms.VisibleString(oper.String()), mms.Integer(1), parts[1], parts[2], mms.Integer(10))
				report.Variables = append(report.Variables, mms.Access(mms.VMDName("LastApplError")))
				report.Results = append(report.Results, mms.ReadResult{Value: last})
			}
			if err := peer.SendReport(runCtx, report); err != nil {
				peer.Close()
			}
		})
		if err != nil {
			<-h.busy
			return true, mms.ObjectAccessDenied
		}
		return true, nil
	}
	defer func() { <-h.busy }()
	return true, h.perform(ctx, cmd)
}
func sameSelection(selected, command mms.Value) bool {
	s, es := selected.Elements()
	v, ev := command.Elements()
	if es != nil || ev != nil || len(s) != 6 || len(v) != 6 {
		return false
	}
	// The timestamp may change between selection and execution.
	for _, i := range []int{0, 1, 2, 4, 5} {
		if !s[i].Equal(v[i]) {
			return false
		}
	}
	return true
}
