// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

var (
	// ErrRejected means the peer or local declaration checks rejected the command.
	ErrRejected = errors.New("ied: command rejected")
	// ErrNotSubmitted means Oper was not submitted; inspect the wrapped cause.
	ErrNotSubmitted = errors.New("ied: operation was not submitted")
	// ErrUnconfirmed means acceptance or enhanced termination is uncertain.
	// Inspect ControlResult and independent ST feedback before any retry decision.
	ErrUnconfirmed = errors.New("ied: operation or termination unconfirmed")
)

// ControlModel is the online ctlModel value: direct or SBO, with normal or enhanced
// termination semantics.
type ControlModel int

const (
	StatusOnly ControlModel = iota
	DirectNormal
	SBONormal
	DirectEnhanced
	SBOEnhanced
)

// ControlOptions supplies the command timestamp and explicit test/interlock/synchrocheck
// flags. A zero Time uses the local clock; operations are immediate.
type ControlOptions struct {
	Test, SynchroCheck, InterlockCheck bool
	Time                               time.Time // zero uses the local clock; operTm remains immediate
}

// ControlResult records protocol evidence, not a physical actuator position.
// Acknowledged means a successful MMS Write response; Terminated additionally
// means a matching enhanced-security CommandTermination was received.
type ControlResult struct {
	Model                              ControlModel
	Selected, Acknowledged, Terminated bool
}

// Control is a declaration obtained online. Obtain it again after an IED
// configuration change. The session serializes selection/operate pairs so
// callers do not need an external mutex.
type Control struct {
	client *Client
	name   mms.ObjectName
	spec   data.TypeSpec
	model  ControlModel
}

// Control retains one remote object and its declared Oper layout. Obtain a fresh Control after
// device reconfiguration.
func (c *Client) Control(ctx context.Context, reference string) (*Control, error) {
	domain, object, ok := strings.Cut(reference, "/")
	if !ok {
		return nil, fmt.Errorf("%w: expected domain/LN.object", ErrNotSubmitted)
	}
	name, err := ObjectName(domain, "CO", object+".Oper")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotSubmitted, err)
	}
	modelName, _ := ObjectName(domain, "CF", object+".ctlModel")
	value, err := c.Read(ctx, modelName)
	if err != nil {
		return nil, before(err)
	}
	model, err := value.Int()
	if err != nil || model < 1 || model > 4 {
		return nil, fmt.Errorf("%w: unsupported ctlModel %s", ErrRejected, value)
	}
	spec, err := c.GetVariableAccessAttributes(ctx, name)
	if err != nil {
		return nil, before(err)
	}
	if spec.Kind != data.KindStructure {
		return nil, fmt.Errorf("%w: Oper is not a structure", ErrRejected)
	}
	control := &Control{client: c, name: name, spec: spec, model: ControlModel(model)}
	if _, err := control.ValueType(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	return control, nil
}

// ValueType returns the ctlVal declaration needed to construct a correctly typed command
// value.
func (o *Control) ValueType() (data.TypeSpec, error) {
	t, _, err := data.ResolveComponent(o.spec, "ctlVal")
	return t, err
}

// Model returns the ctlModel discovered when this Control was created.
func (o *Control) Model() ControlModel { return o.model }

// Operate performs the modeled control procedure. Inspect ControlResult even when err is
// non-nil: acceptance, termination and equipment feedback are separate evidence.
func (c *Client) Operate(ctx context.Context, reference string, value data.Value, options ControlOptions) (ControlResult, error) {
	o, err := c.Control(ctx, reference)
	if err != nil {
		return ControlResult{}, err
	}
	return o.Operate(ctx, value, options)
}

// BuildCommand follows the IED's declared component order, including optional
// ctlNum/operTm. It refuses unknown components instead of guessing their layout.
func BuildCommand(spec data.TypeSpec, value data.Value, origin Origin, sequence uint8, options ControlOptions) (data.Value, error) {
	if err := validateOrigin(origin); err != nil {
		return data.Value{}, err
	}
	if spec.Kind != data.KindStructure {
		return data.Value{}, fmt.Errorf("ied: Oper must be a structure")
	}
	if options.Time.IsZero() {
		options.Time = time.Now()
	}
	seen := make(map[string]bool)
	values := make([]data.Value, 0, len(spec.Components))
	for _, field := range spec.Components {
		if seen[field.Name] {
			return data.Value{}, fmt.Errorf("ied: duplicate control component %q", field.Name)
		}
		seen[field.Name] = true
		var part data.Value
		switch field.Name {
		case "ctlVal":
			part = value
		case "origin":
			if field.Type.Kind != data.KindStructure || len(field.Type.Components) != 2 {
				return data.Value{}, fmt.Errorf("ied: invalid origin declaration")
			}
			parts := make([]data.Value, 0, 2)
			for _, f := range field.Type.Components {
				switch f.Name {
				case "orCat":
					parts = append(parts, data.Integer(int64(origin.Category)))
				case "orIdent":
					parts = append(parts, data.OctetString(origin.ID))
				default:
					return data.Value{}, fmt.Errorf("ied: unknown origin component %q", f.Name)
				}
			}
			if field.Type.Components[0].Name == field.Type.Components[1].Name {
				return data.Value{}, fmt.Errorf("ied: duplicate origin component")
			}
			part = data.Structure(parts...)
		case "ctlNum":
			part = data.Unsigned(uint64(sequence))
		case "operTm":
			part = data.UTCFromBytes([8]byte{})
		case "T":
			part = data.UTC(options.Time)
		case "Test":
			part = data.Boolean(options.Test)
		case "Check":
			var flags byte
			if options.SynchroCheck {
				flags |= 0x80
			}
			if options.InterlockCheck {
				flags |= 0x40
			}
			part, _ = data.BitString([]byte{flags}, 2)
		default:
			return data.Value{}, fmt.Errorf("ied: unsupported control component %q", field.Name)
		}
		values = append(values, part)
	}
	for _, field := range []string{"ctlVal", "origin", "T", "Test", "Check"} {
		if !seen[field] {
			return data.Value{}, fmt.Errorf("ied: incomplete Oper: missing %s", field)
		}
	}
	command := data.Structure(values...)
	return command, data.ValidateValue(spec, command)
}

func before(err error) error {
	var denial mms.DataAccessError
	if errors.As(err, &denial) {
		return fmt.Errorf("%w: %w", ErrRejected, err)
	}
	return fmt.Errorf("%w: %w", ErrNotSubmitted, err)
}
func after(err error) error {
	var denial mms.DataAccessError
	if errors.As(err, &denial) {
		return fmt.Errorf("%w: %w", ErrRejected, err)
	}
	return fmt.Errorf("%w: %w", ErrUnconfirmed, err)
}

// Operate performs the modeled control procedure. Inspect ControlResult even when err is
// non-nil: acceptance, termination and equipment feedback are separate evidence.
func (o *Control) Operate(ctx context.Context, value data.Value, options ControlOptions) (ControlResult, error) {
	c := o.client
	result := ControlResult{Model: o.model}
	if err := c.acquire(ctx, c.controlToken); err != nil {
		return result, before(err)
	}
	defer func() { <-c.controlToken }()
	c.next++
	if options.Time.IsZero() {
		options.Time = time.Now()
	}
	command, err := BuildCommand(o.spec, value, c.origin, c.next, options)
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	attribute := func(suffix string) mms.ObjectName {
		return mms.DomainName(o.name.Domain, strings.TrimSuffix(o.name.Item, "$Oper")+"$"+suffix)
	}
	switch o.model {
	case SBONormal:
		sbo := attribute("SBO")
		selection, err := c.Read(ctx, sbo)
		if err != nil {
			return result, before(err)
		}
		token, err := selection.StringValue()
		target := sbo.Domain + "/" + strings.TrimSuffix(sbo.Item, "$SBO")
		if err != nil || (token != target && token != target+"$SBO") {
			return result, fmt.Errorf("%w: selection declined", ErrRejected)
		}
		result.Selected = true
	case SBOEnhanced:
		spec, err := c.GetVariableAccessAttributes(ctx, attribute("SBOw"))
		if err != nil {
			return result, before(err)
		}
		selection, err := BuildCommand(spec, value, c.origin, c.next, options)
		if err != nil {
			return result, fmt.Errorf("%w: %w", ErrRejected, err)
		}
		if err := c.Write(ctx, attribute("SBOw"), selection); err != nil {
			c.Close()
			return result, before(err)
		}
		result.Selected = true
	}
	return o.execute(ctx, command, c.next, result)
}

// execute is called with controlToken held by Operate or a selected operation.
func (o *Control) execute(ctx context.Context, command data.Value, number uint8, result ControlResult) (ControlResult, error) {
	c := o.client
	var pending *termination
	if o.model >= DirectEnhanced {
		_, indices, _ := data.ResolveComponent(o.spec, "origin")
		origin, _ := data.ValueAt(command, indices)
		pending = &termination{name: o.name, value: command, number: number, origin: origin, result: make(chan error, 1)}
		c.mu.Lock()
		c.pending = pending
		c.mu.Unlock()
		defer func() { c.mu.Lock(); c.pending = nil; c.mu.Unlock() }()
	}
	if err := c.Write(ctx, o.name, command); err != nil {
		// Releasing a selection also prevents a caller from accidentally reusing
		// it after an ambiguous transport failure. Never replay the Oper.
		if result.Selected {
			c.Close()
		}
		return result, after(err)
	}
	result.Acknowledged = true
	if pending != nil {
		select {
		case err := <-pending.result:
			result.Terminated = err == nil
			return result, err
		case <-ctx.Done():
			return result, fmt.Errorf("%w: command termination: %w", ErrUnconfirmed, ctx.Err())
		case <-c.Done():
			return result, fmt.Errorf("%w: %w", ErrUnconfirmed, c.Err())
		}
	}
	return result, nil
}

// Selection is a single-use SBO reservation. Complete it with Operate or
// Cancel. The IED's configured selection timeout also releases an abandoned
// reservation. Other controls on the association remain independently usable.
type Selection struct {
	control *Control
	command data.Value
	number  uint8
	mu      sync.Mutex
	used    bool
}

// Select reserves an SBO object and returns a single-use Selection. Complete it with Operate
// or Cancel before the IED selection timeout.
func (o *Control) Select(ctx context.Context, value data.Value, options ControlOptions) (*Selection, error) {
	if o.model != SBONormal && o.model != SBOEnhanced {
		return nil, fmt.Errorf("%w: control has no selection model", ErrRejected)
	}
	c := o.client
	if err := c.acquire(ctx, c.controlToken); err != nil {
		return nil, err
	}
	defer func() { <-c.controlToken }()
	c.next++
	command, err := BuildCommand(o.spec, value, c.origin, c.next, options)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(o.name.Item, "$Oper")
	if o.model == SBONormal {
		v, e := c.Read(ctx, mms.DomainName(o.name.Domain, base+"$SBO"))
		if e != nil {
			return nil, e
		}
		token, e := v.StringValue()
		if e != nil || (token != o.name.Domain+"/"+base && token != o.name.Domain+"/"+base+"$SBO") {
			return nil, fmt.Errorf("%w: selection declined", ErrRejected)
		}
	} else {
		name := mms.DomainName(o.name.Domain, base+"$SBOw")
		spec, e := c.GetVariableAccessAttributes(ctx, name)
		if e != nil {
			return nil, e
		}
		selection, e := BuildCommand(spec, value, c.origin, c.next, options)
		if e != nil {
			return nil, e
		}
		if e = c.Write(ctx, name, selection); e != nil {
			return nil, e
		}
	}
	return &Selection{control: o, command: command, number: c.next}, nil
}

// Operate performs the modeled control procedure. Inspect ControlResult even when err is
// non-nil: acceptance, termination and equipment feedback are separate evidence.
func (s *Selection) Operate(ctx context.Context) (ControlResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ControlResult{Model: s.control.model, Selected: true}
	if s.used {
		return result, fmt.Errorf("%w: selection already consumed", ErrRejected)
	}
	c := s.control.client
	if err := c.acquire(ctx, c.controlToken); err != nil {
		return result, err
	}
	defer func() { <-c.controlToken }()
	s.used = true
	return s.control.execute(ctx, s.command, s.number, result)
}

// Cancel releases this selection on the remote IED. A successful cancellation consumes the
// local Selection.
func (s *Selection) Cancel(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used {
		return fmt.Errorf("ied: selection already consumed")
	}
	c := s.control.client
	name := mms.DomainName(s.control.name.Domain, strings.TrimSuffix(s.control.name.Item, "$Oper")+"$Cancel")
	spec, err := c.GetVariableAccessAttributes(ctx, name)
	if err != nil {
		return err
	}
	var values []data.Value
	for _, field := range spec.Components {
		_, path, e := data.ResolveComponent(s.control.spec, field.Name)
		if e != nil {
			return e
		}
		v, e := data.ValueAt(s.command, path)
		if e != nil {
			return e
		}
		values = append(values, v)
	}
	command := data.Structure(values...)
	if err = data.ValidateValue(spec, command); err != nil {
		return err
	}
	err = c.Write(ctx, name, command)
	if err == nil {
		s.used = true
	}
	return err
}

type termination struct {
	name   mms.ObjectName
	value  data.Value
	number uint8
	origin data.Value
	result chan error
}

func (t *termination) match(report mms.InformationReport) (bool, error) {
	positive := false
	for i, variable := range report.Variables {
		if i >= len(report.Results) || report.Results[i].Error != nil {
			continue
		}
		value := report.Results[i].Value
		if variable.Name == t.name && value.Equal(t.value) {
			positive = true
		}
		if variable.Name != mms.VMDName("LastApplError") {
			continue
		}
		parts, err := value.Elements()
		if err != nil || len(parts) != 5 {
			continue
		}
		target, _ := parts[0].StringValue()
		sequence, err := parts[3].Uint()
		if err != nil || target != t.name.String() || sequence != uint64(t.number) || !parts[2].Equal(t.origin) {
			continue
		}
		code, e1 := parts[1].Int()
		cause, e2 := parts[4].Int()
		if e1 == nil && e2 == nil && (code != 0 || cause != 0) {
			return true, fmt.Errorf("%w: %s LastApplError error=%d addCause=%d", ErrRejected, target, code, cause)
		}
	}
	return positive, nil
}
