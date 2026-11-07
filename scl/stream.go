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
}

func (g *Stream) Info() StreamInfo {
	f := g.frame.Clone()
	i := StreamInfo{Reference: f.PDU.GoCBRef, DataSet: f.PDU.DataSet, GoID: f.PDU.GoID, ConfRev: f.PDU.ConfRev,
		Destination: f.Destination, APPID: f.APPID, VLAN: f.VLAN, MinTime: g.minTime, MaxTime: g.maxTime}
	for _, a := range g.set.Variables {
		i.Members = append(i.Members, a.Name)
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
		layout.Members = append(layout.Members, goose.Member{Name: name.Item, Domain: domain, Type: data.CloneType(g.types[i])})
	}
	return layout
}

// Values returns one coherent model snapshot in FCDA order.
func (g *Stream) Values(ctx context.Context) ([]mms.Value, error) {
	g.ied.mu.RLock()
	defer g.ied.mu.RUnlock()
	return g.values(ctx)
}
func (d *IED) Streams() []*Stream {
	out := make([]*Stream, 0, len(d.streams))
	for _, g := range d.streams {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name.String() < out[j].name.String() })
	return out
}
func (d *IED) Stream(ld, cb string) (*Stream, error) {
	g := d.streams[mms.DomainName(d.domains[ld], "LLN0$GO$"+cb)]
	if g == nil {
		return nil, fmt.Errorf("scl: unknown GSEControl %s/%s", ld, cb)
	}
	return g, nil
}
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
	Stream *Stream
	Index  int
	Name   mms.ObjectName
	path   []int
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
	if x.SourceIED != d.name || x.ControlLN != "LLN0" || x.DataPath == "" {
		return Input{}, fmt.Errorf("scl: %s requires an explicit GOOSE source bound to %s/LLN0", intAddr, d.name)
	}
	g, err := d.Stream(x.ControlLD, x.ControlBlock)
	if err != nil {
		return Input{}, err
	}
	var matches []Input
	for i, a := range g.set.Variables {
		member := point(a.Name)
		want, err := d.Lookup(x.DataLD, x.DataLN, member.FC, x.DataPath)
		if err != nil || want.Domain != a.Name.Domain {
			continue
		}
		in := Input{Stream: g, Index: i, Name: want}
		if want != a.Name {
			suffix, ok := strings.CutPrefix(want.Item, a.Name.Item+"$")
			if !ok {
				continue
			}
			t := g.types[i]
			for _, part := range strings.Split(suffix, "$") {
				found := false
				for j, c := range t.Components {
					if c.Name == part {
						in.path = append(in.path, j)
						t = c.Type
						found = true
						break
					}
				}
				if !found {
					return Input{}, fmt.Errorf("scl: unresolved FCD member %s", want)
				}
			}
		}
		matches = append(matches, in)
	}
	if len(matches) != 1 {
		return Input{}, fmt.Errorf("scl: %s resolves to %d members in %s", intAddr, len(matches), g.set.Name)
	}
	return matches[0], nil
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
