// SPDX-License-Identifier: GPL-3.0-or-later
// Package workbench composes the existing protocol packages into a stateful
// debugging application. HTTP and CLI both call these same operations.
package workbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("operation conflicts with the current state")
)

type Document struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Size         int           `json:"size"`
	AccessPoints []AccessPoint `json:"accessPoints"`
}
type AccessPoint struct {
	IED    string `json:"ied"`
	AP     string `json:"ap"`
	Server bool   `json:"server"`
}
type imported struct {
	Document
	system *scl.System
	raw    []byte
}
type Manager struct {
	mu          sync.RWMutex
	docs        map[string]*imported
	devices     map[string]*device
	connections map[string]*connection
	closed      bool
	log         journal
}
type device struct {
	id, docID, name, ap string
	ied                 *scl.IED
	points              []scl.Point
	controls            []Control
	mu                  sync.Mutex // lifecycle only, never acquired by a protocol callback
	run                 *simulation
	controlEnabled      atomic.Bool
	interlocked         atomic.Bool
}
type Control struct {
	Reference string `json:"reference"`
	Feedback  string `json:"feedback"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}
type DeviceSummary struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	IED         string `json:"ied"`
	AP          string `json:"ap"`
	Points      int    `json:"points"`
	Status      string `json:"status"`
	Address     string `json:"address"`
	GOOSE       string `json:"goose"`
	Error       string `json:"error,omitempty"`
	Interlocked bool   `json:"interlocked"`
}
type Point struct {
	Reference string       `json:"reference"`
	FC        string       `json:"fc"`
	Domain    string       `json:"domain"`
	Item      string       `json:"item"`
	Kind      string       `json:"kind"`
	Type      mms.TypeSpec `json:"type"`
	Value     any          `json:"value"`
	Display   string       `json:"display"`
	Editable  bool         `json:"editable"`
}
type Detail struct {
	Device   DeviceSummary        `json:"device"`
	Points   []Point              `json:"points"`
	DataSets []mms.NamedList      `json:"datasets"`
	Streams  []scl.StreamInfo     `json:"streams"`
	Inputs   []scl.InputReference `json:"inputs"`
	Controls []Control            `json:"controls"`
}

func New() *Manager {
	return &Manager{docs: map[string]*imported{}, devices: map[string]*device{}, connections: map[string]*connection{}}
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:12]) }

// Import parses before changing workspace state. A bad upload leaves existing
// models and running simulators intact. Equal content reuses the same document.
func (m *Manager) Import(name string, raw []byte) (Document, error) {
	if len(raw) > 16<<20 {
		return Document{}, fmt.Errorf("SCL file exceeds 16 MiB")
	}
	sys, err := scl.Parse(bytes.NewReader(raw))
	if err != nil {
		return Document{}, err
	}
	id := digest(raw)
	aps := []AccessPoint{}
	for _, p := range sys.AccessPoints() {
		aps = append(aps, AccessPoint{p.IED, p.AP, p.HasServer})
	}
	doc := Document{ID: id, Name: name, Size: len(raw), AccessPoints: aps}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Document{}, ErrConflict
	}
	if old := m.docs[id]; old != nil {
		m.mu.Unlock()
		return old.Document, nil
	}
	if len(m.docs) >= 16 {
		m.mu.Unlock()
		return Document{}, fmt.Errorf("workspace limit is 16 SCL files")
	}
	m.docs[id] = &imported{doc, sys, bytes.Clone(raw)}
	m.mu.Unlock()
	m.log.add("model.import", "", fmt.Sprintf("Imported %s · %d access points", name, len(aps)))
	return doc, nil
}
func (m *Manager) Documents() []Document {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]Document, 0, len(m.docs))
	for _, d := range m.docs {
		list = append(list, d.Document)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}
func (m *Manager) Source(id string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d := m.docs[id]
	if d == nil {
		return nil, ErrNotFound
	}
	return bytes.Clone(d.raw), nil
}

// Select creates one independent SCL-backed store for an IED/AP pair. Repeated
// selections reuse it, so switching tabs never resets process values.
func (m *Manager) Select(docID, name, ap string) (DeviceSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return DeviceSummary{}, ErrConflict
	}
	doc := m.docs[docID]
	if doc == nil {
		return DeviceSummary{}, ErrNotFound
	}
	id := digest([]byte(docID + "/" + name + "/" + ap))
	if d := m.devices[id]; d != nil {
		return d.summary(), nil
	}
	if len(m.devices) >= 32 {
		return DeviceSummary{}, fmt.Errorf("workspace limit is 32 instantiated IEDs")
	}
	ied, err := doc.system.Device(name, ap)
	if err != nil {
		return DeviceSummary{}, err
	}
	d := &device{id: id, docID: docID, name: name, ap: ap, ied: ied, points: ied.Points()}
	d.bindControls(&m.log)
	m.devices[id] = d
	m.log.add("model.select", id, fmt.Sprintf("Instantiated %s/%s", name, ap))
	return d.summary(), nil
}
func (m *Manager) get(id string) (*device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrConflict
	}
	d := m.devices[id]
	if d == nil {
		return nil, ErrNotFound
	}
	return d, nil
}
func (d *device) summary() DeviceSummary {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := DeviceSummary{ID: d.id, Model: d.docID, IED: d.name, AP: d.ap, Points: len(d.points), Status: "stopped", GOOSE: "off", Interlocked: d.interlocked.Load()}
	if d.run != nil {
		s.Address = d.run.address
		s.GOOSE = d.run.mode
		s.Status = "running"
		select {
		case <-d.run.done:
			s.Status = "stopped"
			if err := d.run.failure(); err != nil {
				s.Status = "error"
				s.Error = err.Error()
			}
		default:
		}
	}
	return s
}
func (m *Manager) Devices() []DeviceSummary {
	m.mu.RLock()
	list := make([]*device, 0, len(m.devices))
	for _, d := range m.devices {
		list = append(list, d)
	}
	m.mu.RUnlock()
	out := make([]DeviceSummary, 0, len(list))
	for _, d := range list {
		out = append(out, d.summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IED < out[j].IED })
	return out
}
func (m *Manager) Detail(ctx context.Context, id string) (Detail, error) {
	d, err := m.get(id)
	if err != nil {
		return Detail{}, err
	}
	out := Detail{Device: d.summary(), Points: []Point{}, DataSets: d.ied.DataSets(), Streams: []scl.StreamInfo{}, Controls: append([]Control{}, d.controls...)}
	for _, g := range d.ied.Streams() {
		out.Streams = append(out.Streams, g.Info())
	}
	m.mu.RLock()
	doc := m.docs[d.docID]
	m.mu.RUnlock()
	for _, ap := range doc.AccessPoints {
		inputs, e := doc.system.GOOSEInputs(ap.IED, ap.AP)
		if e == nil {
			out.Inputs = append(out.Inputs, inputs...)
		}
	}
	for _, p := range d.points {
		t, e := d.ied.Type(ctx, p.Name)
		if e != nil {
			return Detail{}, e
		}
		v, e := d.ied.Read(ctx, mms.Access(p.Name))
		if e != nil {
			return Detail{}, e
		}
		out.Points = append(out.Points, Point{p.Reference, p.FC, p.Name.Domain, p.Name.Item, t.Kind.String(), t, ValueJSON(v), v.String(), p.FC == "ST" || p.FC == "MX"})
	}
	return out, nil
}

// Edit is local acquisition, so ST/MX remain read-only for real MMS clients.
func (m *Manager) Edit(ctx context.Context, id, ref, fc string, raw []byte) error {
	d, err := m.get(id)
	if err != nil {
		return err
	}
	n, err := d.ied.Resolve(ref, fc)
	if err != nil {
		return err
	}
	t, err := d.ied.Type(ctx, n)
	if err != nil {
		return err
	}
	v, err := ParseValue(t, raw)
	if err != nil {
		return err
	}
	if err = updateFeedback(ctx, d.ied, n, v); err != nil {
		return err
	}
	m.log.add("model.value", id, fmt.Sprintf("%s [%s] ← %s", ref, fc, v))
	return nil
}

// A signal's acquisition timestamp is updated with its value. Explicit edits
// to q/t preserve the user's entered bits/time. All assignments commit together.
func updateFeedback(ctx context.Context, ied *scl.IED, n mms.ObjectName, v mms.Value) error {
	batch := []mms.Assignment{{Name: n, Value: v}}
	parts := strings.Split(n.Item, "$")
	if len(parts) > 3 && parts[len(parts)-1] != "q" && parts[len(parts)-1] != "t" {
		for cut := len(parts) - 1; cut >= 3; cut-- {
			base := strings.Join(parts[:cut], "$")
			q := mms.DomainName(n.Domain, base+"$q")
			tm := mms.DomainName(n.Domain, base+"$t")
			qt, qe := ied.Type(ctx, q)
			tt, te := ied.Type(ctx, tm)
			if qe == nil && te == nil && qt.Kind == mms.KindBitString && tt.Kind == mms.KindUTC {
				quality, e := ied.Read(ctx, mms.Access(q))
				if e != nil {
					return e
				}
				batch = append(batch, mms.Assignment{Name: q, Value: quality}, mms.Assignment{Name: tm, Value: nowValue()})
				break
			}
		}
	}
	return ied.Update(ctx, batch...)
}

// Close first prevents new sessions, then cancels protocol work outside the
// manager lock. A callback can finish its journal write while shutdown waits.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	devices := make([]*device, 0, len(m.devices))
	for _, d := range m.devices {
		devices = append(devices, d)
	}
	conns := make([]*connection, 0, len(m.connections))
	for _, c := range m.connections {
		conns = append(conns, c)
	}
	m.mu.Unlock()
	var failures []error
	for _, d := range devices {
		d.mu.Lock()
		if d.run != nil {
			failures = append(failures, d.run.stop())
		}
		d.mu.Unlock()
	}
	for _, c := range conns {
		c.client.Close()
	}
	return errors.Join(failures...)
}
