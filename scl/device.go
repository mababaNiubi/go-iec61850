// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// IED is a model-backed MMS Store. Its engineering configuration is fixed at
// construction. Local acquisition uses Update and remote control uses OnOperate.
type IED struct {
	system            *System
	name, ap, address string
	iso               mms.ISOConfig
	model             *mms.Model
	domains           map[string]string
	types             map[mms.ObjectName]mms.TypeSpec
	binary            map[mms.ObjectName]bool
	sets              map[mms.ObjectName]mms.NamedList
	streams           map[mms.ObjectName]*Stream
	// Protects change notifications and coherent multi-point snapshots.
	mu            sync.RWMutex
	feeds         map[*feed]struct{}
	handlers      map[mms.ObjectName]*control
	reports       map[mms.ObjectName]*reportBlock
	triggers      map[mms.ObjectName]byte
	setpoints     map[mms.ObjectName]*setpoint
	settings      map[string]*settingGroup
	groupInitial  []groupInitial
	readOnlyPaths []string
}

func newIED(s *System, ied, ap string) *IED {
	return &IED{system: s, name: ied, ap: ap, model: mms.NewModel(), domains: map[string]string{}, types: map[mms.ObjectName]mms.TypeSpec{},
		binary: map[mms.ObjectName]bool{}, sets: map[mms.ObjectName]mms.NamedList{}, streams: map[mms.ObjectName]*Stream{},
		feeds: make(map[*feed]struct{}), handlers: map[mms.ObjectName]*control{}, reports: map[mms.ObjectName]*reportBlock{},
		triggers: map[mms.ObjectName]byte{}, setpoints: map[mms.ObjectName]*setpoint{}, settings: map[string]*settingGroup{}}
}

// MMSAddress returns host:port from ConnectedAP, or an empty string for an
// offline model with no configured IP. The default TCP port is 102.
func (d *IED) MMSAddress() string { return d.address }

// Point identifies an engineered object or attribute in both notations.
type Point struct {
	Reference, FC string
	Name          mms.ObjectName
}

func point(n mms.ObjectName) Point {
	parts := strings.Split(n.Item, "$")
	return Point{Reference: n.Domain + "/" + parts[0] + "." + strings.Join(parts[2:], "."), FC: parts[1], Name: n}
}

// Points returns the modeled DOs, DAs and BDAs in MMS name order.
func (d *IED) Points() []Point {
	var result []Point
	for n := range d.types {
		if strings.Count(n.Item, "$") >= 2 {
			result = append(result, point(n))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name.String() < result[j].Name.String() })
	return result
}
func (d *IED) indexType(n mms.ObjectName, spec mms.TypeSpec) error {
	if !data.ValidIdentifier(n.Item) {
		return fmt.Errorf("MMS item exceeds the supported identifier limit: %s", n)
	}
	d.types[n] = spec
	for _, c := range spec.Components {
		child := mms.DomainName(n.Domain, n.Item+"$"+c.Name)
		if err := d.indexType(child, c.Type); err != nil {
			return err
		}
	}
	return nil
}

// Resolve converts an object reference such as BayIEDCTRL/XCBR1.Pos.stVal
// and functional constraint ST to its verified MMS object name.
func (d *IED) Resolve(reference, fc string) (mms.ObjectName, error) {
	domain, path, ok := strings.Cut(reference, "/")
	ln, attr, hasAttr := strings.Cut(path, ".")
	if !ok || !hasAttr || !identifier(domain) || !identifier(ln) || !supportedFC(fc) {
		return mms.ObjectName{}, fmt.Errorf("scl: invalid reference %q [%s]", reference, fc)
	}
	for _, part := range strings.Split(attr, ".") {
		if !identifier(part) {
			return mms.ObjectName{}, fmt.Errorf("scl: invalid attribute path %q", attr)
		}
	}
	name := mms.DomainName(domain, ln+"$"+fc+"$"+strings.ReplaceAll(attr, ".", "$"))
	if _, ok := d.types[name]; !ok {
		return mms.ObjectName{}, fmt.Errorf("scl: %s [%s]: %w", reference, fc, mms.ObjectNonExistent)
	}
	return name, nil
}

// Lookup accepts an LDevice instance and applies its engineered ldName or
// the default IED name + LD instance before resolving the point.
func (d *IED) Lookup(ld, ln, fc, path string) (mms.ObjectName, error) {
	domain, ok := d.domains[ld]
	if !ok {
		return mms.ObjectName{}, fmt.Errorf("scl: unknown LDevice %q", ld)
	}
	return d.Resolve(domain+"/"+ln+"."+path, fc)
}

// DataSets returns independent copies of the static MMS named variable lists.
// Their FCDA order is also the order used by GOOSE allData.
func (d *IED) DataSets() []mms.NamedList {
	result := make([]mms.NamedList, 0, len(d.sets))
	for _, set := range d.sets {
		set.Variables = append([]mms.VariableAccess(nil), set.Variables...)
		for i := range set.Variables {
			set.Variables[i].Selection = set.Variables[i].Selection.Clone()
		}
		result = append(result, set)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name.String() < result[j].Name.String() })
	return result
}

// DataSet resolves a static dataset ready for Client.ReadNamedList or
// GetNamedListAttributes. Variables retain their configured FCDA order.
func (d *IED) DataSet(ld, ln, name string) (mms.NamedList, error) {
	n := mms.DomainName(d.domains[ld], ln+"$"+name)
	set, ok := d.sets[n]
	if !ok {
		return mms.NamedList{}, fmt.Errorf("scl: unknown dataset %s", n)
	}
	set.Variables = append([]mms.VariableAccess(nil), set.Variables...)
	for i := range set.Variables {
		set.Variables[i].Selection = set.Variables[i].Selection.Clone()
	}
	return set, nil
}

// NewServer exposes this store and its static datasets through MMS.
func (d *IED) NewServer(cfg mms.Config) (*mms.Server, error) {
	s, err := mms.NewServer(d, d.serverConfig(cfg))
	if err != nil {
		return nil, err
	}
	for _, set := range d.sets {
		if err = s.AddNamedList(set); err != nil {
			return nil, err
		}
	}
	s.OnRun = d.runReports
	s.OnAssociation = d.serveAssociation
	return s, nil
}

// Update commits one local ST/MX acquisition and wakes all publication loops.
// Put the value, quality and timestamp of a sample in the same call.
func (d *IED) Update(ctx context.Context, changes ...mms.Assignment) error {
	for _, c := range changes {
		_, fc, ok := strings.Cut(c.Name.Item, "$")
		if !ok || !(strings.HasPrefix(fc, "ST$") || strings.HasPrefix(fc, "MX$")) {
			return mms.ObjectAccessDenied
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.commitLocked(ctx, changes...)
}

// commitLocked is the single publication boundary for acquisition, SP writes
// and activated settings. Call with d.mu held so reports/GOOSE observe the same
// immutable before/after snapshot, including changes initiated over MMS.
func (d *IED) commitLocked(ctx context.Context, changes ...mms.Assignment) error {
	previous := d.reportValuesLocked()
	if err := d.model.SetValues(ctx, changes...); err != nil {
		return err
	}
	if len(changes) > 0 {
		d.captureReportsLocked(previous, changes)
		// The acquisition is committed. Publishers take ordered immutable snapshots
		// before the next writer can change the model.
		for f := range d.feeds {
			f.capture(context.WithoutCancel(ctx))
		}
	}
	return nil
}

func (d *IED) Domains(ctx context.Context) ([]string, error) { return d.model.Domains(ctx) }
func (d *IED) Names(ctx context.Context, s mms.Scope, domain string) ([]string, error) {
	return d.model.Names(ctx, s, domain)
}
func (d *IED) Type(ctx context.Context, n mms.ObjectName) (mms.TypeSpec, error) {
	return d.model.Type(ctx, n)
}
func (d *IED) Read(ctx context.Context, a mms.VariableAccess) (mms.Value, error) {
	if value, handled, err := d.readService(ctx, a); handled {
		return value, err
	}
	return d.model.Read(ctx, a)
}

// ReadSnapshot reads named values under one model lock. It is useful when a
// sampled-value source needs a coherent multi-member acquisition image.
func (d *IED) ReadSnapshot(ctx context.Context, names ...mms.ObjectName) ([]mms.Value, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	values := make([]mms.Value, 0, len(names))
	for _, name := range names {
		access := mms.Access(name)
		value, handled, err := d.readServiceLocked(ctx, access)
		if !handled {
			value, err = d.model.Read(ctx, access)
		}
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

var _ mms.Store = (*IED)(nil)
