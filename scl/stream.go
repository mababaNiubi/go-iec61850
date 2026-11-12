// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// Stream is an engineered GSEControl bound to its ordered dataset and GSE address.
type Stream struct {
	ied              *IED
	name             mms.ObjectName
	set              mms.NamedList
	frame            goose.Frame
	types            []mms.TypeSpec
	minTime, maxTime time.Duration
}

// StreamInfo describes the wire settings without exposing the mutable runtime.
type StreamInfo struct {
	Reference, DataSet, GoID string
	ConfRev                  uint32
	Destination              [6]byte
	APPID                    uint16
	VLAN                     *goose.VLAN
	MinTime, MaxTime         time.Duration
	Members                  []mms.ObjectName
	Accesses                 []mms.VariableAccess // ordered FCDA members with alternate access
}

// Info returns an independent copy of stream addressing, timing and ordered dataset accesses,
// including array selection.
func (g *Stream) Info() StreamInfo {
	f := g.frame.Clone()
	i := StreamInfo{Reference: f.PDU.GoCBRef, DataSet: f.PDU.DataSet, GoID: f.PDU.GoID, ConfRev: f.PDU.ConfRev,
		Destination: f.Destination, APPID: f.APPID, VLAN: f.VLAN, MinTime: g.minTime, MaxTime: g.maxTime}
	for _, a := range g.set.Variables {
		i.Members = append(i.Members, a.Name)
		a.Selection = a.Selection.Clone()
		i.Accesses = append(i.Accesses, a)
	}
	return i
}

// Layout returns independent ordered member declarations suitable for generic
// field access and template compatibility checks across station instances.
func (g *Stream) Layout() goose.Layout {
	layout := goose.Layout{}
	for i, variable := range g.set.Variables {
		name := variable.Name
		domain := "absolute:" + name.Domain
		if name.Domain == g.name.Domain {
			domain = "local"
		} else if relative, ok := strings.CutPrefix(name.Domain, g.ied.name); ok {
			domain = "relative:" + relative
		}
		layout.Members = append(layout.Members, goose.Member{Name: name.Item, Domain: domain, Type: data.CloneType(g.types[i]), Selection: variable.Selection.Clone()})
	}
	return layout
}

// Values returns one coherent model snapshot in FCDA order.
func (g *Stream) Values(ctx context.Context) ([]mms.Value, error) {
	g.ied.mu.RLock()
	defer g.ied.mu.RUnlock()
	return g.values(ctx)
}

// Streams returns the modeled GOOSE streams sorted by reference. Treat their engineering
// configuration as immutable.
func (d *IED) Streams() []*Stream {
	out := make([]*Stream, 0, len(d.streams))
	for _, g := range d.streams {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name.String() < out[j].name.String() })
	return out
}

// Stream resolves a GSEControl by logical-device instance and control-block name, returning an
// error when absent.
func (d *IED) Stream(ld, cb string) (*Stream, error) {
	g := d.streams[mms.DomainName(d.domains[ld], "LLN0$GO$"+cb)]
	if g == nil {
		return nil, fmt.Errorf("scl: unknown GSEControl %s/%s", ld, cb)
	}
	return g, nil
}

// NewSubscriber creates a subscriber pinned to this stream's address, revision and selected
// member types.
func (g *Stream) NewSubscriber() (*goose.Subscriber, error) {
	f := g.frame
	c := goose.SubscriptionConfig{Destination: f.Destination, APPID: f.APPID, GoCBRef: f.PDU.GoCBRef,
		DataSet: f.PDU.DataSet, GoID: f.PDU.GoID, ConfRev: f.PDU.ConfRev, Types: g.types, RequireUntagged: f.VLAN == nil}
	if f.VLAN != nil {
		c.VLANID = &f.VLAN.ID
	}
	return goose.NewSubscriber(c)
}

// ErrPublicationOverflow means the transport fell behind 64 dataset transitions.
// The acquisition remains committed and Run reports the failed publication.
var ErrPublicationOverflow = errors.New("scl: GOOSE publication update queue overflow")

type feed struct {
	stream  *Stream
	last    []mms.Value
	updates chan []mms.Value
	stop    context.CancelCauseFunc
}

// The IED mutex serializes registration, snapshotting, and detaching a feed.
// The network sender never holds this lock, even when the link stalls.
func (g *Stream) attach(ctx context.Context, stop context.CancelCauseFunc) (*feed, []mms.Value, error) {
	g.ied.mu.Lock()
	defer g.ied.mu.Unlock()
	initial, err := g.values(ctx)
	if err != nil {
		return nil, nil, err
	}
	f := &feed{stream: g, last: initial, updates: make(chan []mms.Value, 64), stop: stop}
	g.ied.feeds[f] = struct{}{}
	return f, initial, nil
}
func (f *feed) detach() {
	d := f.stream.ied
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.feeds, f)
}
func (f *feed) capture(ctx context.Context) {
	values, err := f.stream.values(ctx)
	if err == nil {
		same := true
		for i, v := range values {
			if !v.Equal(f.last[i]) {
				same = false
				break
			}
		}
		if same {
			return
		}
		select {
		case f.updates <- values:
			f.last = values
			return
		default:
			err = ErrPublicationOverflow
		}
	}
	delete(f.stream.ied.feeds, f)
	f.stop(err)
}
func (g *Stream) values(ctx context.Context) ([]mms.Value, error) {
	values := make([]mms.Value, len(g.set.Variables))
	for i, a := range g.set.Variables {
		v, err := g.ied.model.Read(ctx, a)
		if err != nil {
			return nil, err
		}
		values[i] = v
	}
	return values, nil
}

// Run publishes the current snapshot and retransmits until cancellation or
// failure. Local acquisitions wake the loop. Use the actual interface's source
// MAC for a live link. The caller owns the link and closes it after Run exits.
func (g *Stream) Run(ctx context.Context, link goose.FrameIO, source [6]byte) error {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	f, values, err := g.attach(runCtx, cancel)
	if err != nil {
		return err
	}
	defer f.detach()
	frame := g.frame.Clone()
	frame.Source = source
	frame.PDU.Values = values
	pub, err := goose.NewPublisher(link, goose.PublisherConfig{Frame: frame, Types: g.types, MinTime: g.minTime, MaxTime: g.maxTime})
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- pub.Run(runCtx) }()
	for {
		select {
		case err := <-done:
			if cause := context.Cause(runCtx); cause != nil {
				return cause
			}
			return err
		case values := <-f.updates:
			if _, err := pub.Update(values); err != nil {
				cancel(err)
				<-done
				return err
			}
		}
	}
}

// Input identifies the dataset member selected by a receiver's ExtRef.intAddr.
// Index is its zero-based allData slot, even when it contains a whole FCD.
type Input struct {
	Stream   *Stream
	Index    int
	Name     mms.ObjectName
	Variable mms.VariableAccess
	path     []int
	quality  *goose.Field
}

// Input resolves one unambiguous subscriber-local address against this source
// IED's GSEControl and FCDA declarations. The named receiver may be a separate IED.
func (d *IED) Input(receiver, ap, intAddr string) (Input, error) {
	inputs, err := d.system.GOOSEInputs(receiver, ap)
	if err != nil {
		return Input{}, err
	}
	var refs []InputReference
	for _, x := range inputs {
		if intAddr != "" && x.IntAddr == intAddr {
			refs = append(refs, x)
		}
	}
	if len(refs) != 1 {
		return Input{}, fmt.Errorf("scl: expected one ExtRef %s/%s/%s, found %d", receiver, ap, intAddr, len(refs))
	}
	x := refs[0]
	return d.ResolveGOOSEInput(x)
}

// ResolveGOOSEInput resolves a scoped ExtRef returned by System.Inputs. It
// avoids the optional, application-defined intAddr as a global lookup key.
func (d *IED) ResolveGOOSEInput(x InputReference) (Input, error) {
	if x.SourceIED != d.name || x.ControlLN != "LLN0" || x.DataPath == "" {
		return Input{}, fmt.Errorf("scl: %s requires an explicit GOOSE source bound to %s/LLN0", x.Key(), d.name)
	}
	g, err := d.Stream(x.ControlLD, x.ControlBlock)
	if err != nil {
		return Input{}, err
	}
	var matches []Input
	for _, access := range g.set.Variables {
		fc := point(access.Name).FC
		wanted, e := d.memberAccess(memberXML{LD: x.DataLD, Class: x.DataLN, FC: fc}, x.DataPath)
		if e != nil {
			continue
		}
		for _, candidate := range g.inputsForAccess(wanted) {
			duplicate := false
			for _, previous := range matches {
				duplicate = duplicate || previous.Index == candidate.Index
			}
			if !duplicate {
				matches = append(matches, candidate)
			}
		}
	}
	if len(matches) != 1 {
		return Input{}, fmt.Errorf("scl: %s resolves to %d members in %s", x.Key(), len(matches), g.set.Name)
	}
	in := matches[0]
	// Find the nearest q carried by this same GOOSE dataset. Absence stays
	// explicit; another dataset or another array element cannot supply it.
	parent := x.DataPath
	for strings.Contains(parent, ".") {
		parent = parent[:strings.LastIndexByte(parent, '.')]
		q, e := d.memberAccess(memberXML{LD: x.DataLD, Class: x.DataLN, FC: point(in.Variable.Name).FC}, parent+".q")
		if e != nil {
			continue
		}
		fields := g.inputsForAccess(q)
		if len(fields) == 1 {
			field := fields[0]
			typ, e := mms.SelectedType(d.types[q.Name], q.Selection)
			if e == nil && typ.Kind == mms.KindBitString && typ.Length == 13 {
				in.quality = &goose.Field{Slot: field.Index, Path: field.path, Type: typ}
				break
			}
		}
	}
	return in, nil
}

// selectionPathText translates a validated alternate access into data's typed
// component path, retaining array indices and nested component order.
func selectionPathText(selection mms.Selection) (string, error) {
	steps, err := selection.Steps()
	if err != nil {
		return "", err
	}
	var path []string
	for _, step := range steps {
		if step.Index != nil {
			path = append(path, fmt.Sprint(*step.Index))
		} else {
			path = append(path, strings.ReplaceAll(step.Component, "$", "."))
		}
	}
	return strings.Join(path, "."), nil
}
func (g *Stream) inputsForAccess(want mms.VariableAccess) []Input {
	var matches []Input
	for i, member := range g.set.Variables {
		if want.Name.Domain != member.Name.Domain {
			continue
		}
		prefix := ""
		if want.Name != member.Name {
			suffix, ok := strings.CutPrefix(want.Name.Item, member.Name.Item+"$")
			if !ok {
				continue
			}
			prefix = strings.ReplaceAll(suffix, "$", ".")
		}
		selected, err := selectionPathText(want.Selection)
		if err != nil {
			continue
		}
		if prefix != "" && selected != "" {
			prefix += "."
		}
		_, wantPath, err := data.ResolveComponent(g.ied.types[member.Name], prefix+selected)
		if err != nil {
			continue
		}
		selected, err = selectionPathText(member.Selection)
		if err != nil {
			continue
		}
		_, memberPath, err := data.ResolveComponent(g.ied.types[member.Name], selected)
		if err != nil || len(wantPath) < len(memberPath) {
			continue
		}
		same := true
		for j, index := range memberPath {
			same = same && wantPath[j] == index
		}
		if same {
			matches = append(matches, Input{Stream: g, Index: i, Name: want.Name, Variable: want, path: append([]int(nil), wantPath[len(memberPath):]...)})
		}
	}
	return matches
}

// Quality returns q from this accepted frame when the dataset carries it.
func (in Input) Quality(event goose.Event) (mms.Value, bool, error) {
	if in.quality == nil {
		return mms.Value{}, false, nil
	}
	if _, err := in.Value(event); err != nil {
		return mms.Value{}, true, err
	}
	value, err := in.quality.Read(event.Frame.PDU.Values)
	return value, true, err
}

// Value extracts the subscribed attribute from an accepted subscriber event.
// Expired events have no current process value.
func (in Input) Value(event goose.Event) (mms.Value, error) {
	f := event.Frame
	if in.Stream == nil || event.Kind == goose.Expired || f.PDU.GoCBRef != in.Stream.name.String() ||
		f.PDU.DataSet != in.Stream.set.Name.String() || f.PDU.ConfRev != in.Stream.frame.PDU.ConfRev || in.Index < 0 || in.Index >= len(f.PDU.Values) {
		return mms.Value{}, fmt.Errorf("scl: no valid subscribed value for %s", in.Name)
	}
	v := f.PDU.Values[in.Index]
	for _, i := range in.path {
		items, err := v.Elements()
		if err != nil || i >= len(items) {
			return mms.Value{}, fmt.Errorf("scl: invalid member layout for %s", in.Name)
		}
		v = items[i]
	}
	return v, nil
}
