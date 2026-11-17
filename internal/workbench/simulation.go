// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	clientied "github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

func nowValue() mms.Value { return mms.UTC(time.Now()) }

type SimulationOptions struct {
	Listen      string `json:"listen"`
	GOOSE       string `json:"goose"` // off, memory, ethernet
	Interface   string `json:"interface"`
	Controls    bool   `json:"controls"`
	Promiscuous bool   `json:"promiscuous"`
}

// The public simulator owns transport and workers. The workbench keeps only
// UI selection, virtual actuator policy and its bounded event journal.
type simulation struct {
	runtime       *simulator.Runtime
	address, mode string
	done          <-chan struct{}
	store         *observedStore
}

func (s *simulation) failure() error { return s.runtime.Err() }
func (s *simulation) stop() error    { return s.runtime.Close() }
func (m *Manager) Start(id string, opt SimulationOptions) (DeviceSummary, error) {
	m.mu.RLock()
	d := m.devices[id]
	if m.closed || d == nil {
		m.mu.RUnlock()
		return DeviceSummary{}, ErrConflict
	}
	d.mu.Lock()
	m.mu.RUnlock()
	if d.station != "" {
		d.mu.Unlock()
		return DeviceSummary{}, fmt.Errorf("stop the station before starting an individual IED")
	}
	if d.run != nil {
		select {
		case <-d.run.done:
			if err := d.run.stop(); err != nil {
				d.mu.Unlock()
				return DeviceSummary{}, err
			}
			d.run = nil
		default:
			d.mu.Unlock()
			return DeviceSummary{}, fmt.Errorf("%w: simulation is already running", ErrConflict)
		}
	}
	d.controlEnabled.Store(opt.Controls)
	store := newObservedStore(d.ied, &m.log, id)
	runtime, err := simulator.Start(context.Background(), d.ied, simulator.Options{
		Listen: opt.Listen, GOOSE: simulator.GOOSEMode(opt.GOOSE), Interface: opt.Interface,
		Promiscuous: opt.Promiscuous,
		Store:       store,
		Observe: func(event simulator.Event) {
			if event.Err != nil {
				m.log.add("error", id, event.Err.Error())
				return
			}
			if e := event.GOOSE; e != nil {
				m.log.add(event.Kind, id, fmt.Sprintf("%s · st=%d sq=%d · %d values", event.Stream, e.Frame.PDU.StateNumber, e.Frame.PDU.SequenceNumber, len(e.Frame.PDU.Values)))
			}
		},
	})
	if err != nil {
		store.close()
		d.controlEnabled.Store(false)
		d.mu.Unlock()
		return DeviceSummary{}, err
	}
	d.run = &simulation{runtime: runtime, address: runtime.Address(), mode: string(runtime.GOOSEMode()), done: runtime.Done(), store: store}
	go store.follow(runtime.Done())
	d.mu.Unlock()
	m.log.add("simulation.start", id, fmt.Sprintf("MMS listening on %s · GOOSE %s", runtime.Address(), runtime.GOOSEMode()))
	return d.summary(), nil
}
func (m *Manager) Stop(id string) error {
	d, err := m.get(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	if d.station != "" {
		d.mu.Unlock()
		return fmt.Errorf("stop the station to stop this IED")
	}
	if d.run != nil {
		err = d.run.stop()
		if err == nil {
			d.run.store.close()
			d.run = nil
		}
	}
	d.controlEnabled.Store(false)
	d.mu.Unlock()
	if err == nil {
		m.log.add("simulation.stop", id, "Simulation stopped")
	}
	return err
}

// Wait is shared by the headless CLI and integrations. Transport failures
// terminate the command as well as updating the browser's simulator status.
func (m *Manager) Wait(ctx context.Context, id string) error {
	d, err := m.get(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	r := d.run
	d.mu.Unlock()
	if r == nil {
		return ErrConflict
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.failure()
	}
}
func (m *Manager) Interlock(id string, blocked bool) error {
	d, err := m.get(id)
	if err != nil {
		return err
	}
	d.interlocked.Store(blocked)
	m.log.addControl(ControlRecord{Device: id, Reference: "interlock", Action: "interlock", Value: fmt.Sprintf("blocked=%t", blocked), Via: "sim"})
	return nil
}

// 控制命令和状态反馈分开处理。此回调模拟执行器，网络仍发送完整的
// MMS Oper。替换这里即可接 PLC/驱动，采集结果由 updateFeedback 提交。
func (d *device) bindControls(log *journal) {
	for _, p := range d.points {
		if p.FC != "CO" || !strings.HasSuffix(p.Name.Item, "$Oper") {
			continue
		}
		c := Control{Reference: p.Reference}
		model := mms.DomainName(p.Name.Domain, strings.TrimSuffix(strings.Replace(p.Name.Item, "$CO$", "$CF$", 1), "$Oper")+"$ctlModel")
		if value, err := d.ied.Read(context.Background(), mms.Access(model)); err == nil {
			mode, _ := value.Int()
			c.Model = int(mode)
		}
		status := strings.Replace(p.Name.Item, "$CO$", "$ST$", 1)
		status = strings.TrimSuffix(status, "$Oper") + "$stVal"
		n := mms.DomainName(p.Name.Domain, status)
		t, err := d.ied.Type(context.Background(), n)
		if err != nil {
			c.Reason = "No modeled status feedback"
			d.controls = append(d.controls, c)
			continue
		}
		if !(t.Kind == mms.KindBoolean || t.Kind == mms.KindBitString && t.Length == 2) {
			c.Reason = "Expected SPC or DPC feedback"
			d.controls = append(d.controls, c)
			continue
		}
		parts := strings.Split(n.Item, "$")
		c.Feedback = n.Domain + "/" + parts[0] + "." + strings.Join(parts[2:], ".")
		target, kind := n, t.Kind
		err = d.ied.OnControl(p.Name, func(ctx context.Context, cmd scl.Command) error {
			if !d.controlEnabled.Load() || d.interlocked.Load() {
				return mms.ObjectAccessDenied
			}
			if cmd.Test || cmd.SynchroCheck {
				return mms.ObjectAccessUnsupported
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			value := mms.Boolean(cmd.Value)
			if kind == mms.KindBitString {
				position := scl.Open
				if cmd.Value {
					position = scl.Closed
				}
				var e error
				value, e = position.MMSValue()
				if e != nil {
					return e
				}
			}
			if err := updateFeedback(ctx, d.ied, target, value); err != nil {
				return err
			}
			log.addControl(ControlRecord{
				Device: d.id, Reference: c.Reference, Action: "feedback", Value: value.String(),
				Origin: string(cmd.OriginID), CtlNum: int(cmd.Number), Via: "sim",
			})
			return nil
		})
		if err != nil {
			c.Reason = err.Error()
		} else {
			c.Supported = true
		}
		d.controls = append(d.controls, c)
	}
}

// Operate deliberately crosses TCP, so a browser command exercises the same
// MMS encoding, association and access checks as an external station client.
func (m *Manager) Operate(ctx context.Context, id, ref string, on bool) error {
	d, err := m.get(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	r := d.run
	d.mu.Unlock()
	if r == nil {
		return fmt.Errorf("%w: start the simulator first", ErrConflict)
	}
	n, err := d.ied.Resolve(ref, "CO")
	if err != nil {
		return err
	}
	client, err := clientied.Dial(ctx, dialAddress(r.address), clientied.Config{MMS: d.ied.ClientConfig(), Origin: clientied.Origin{Category: 2, ID: []byte("IEDLab")}})
	if err != nil {
		return err
	}
	defer client.Close()
	parts := strings.Split(n.Item, "$")
	reference := n.Domain + "/" + parts[0] + "." + strings.Join(parts[2:len(parts)-1], ".")
	if _, err = client.Operate(ctx, reference, mms.Boolean(on), clientied.ControlOptions{InterlockCheck: true}); err != nil {
		m.log.addControl(ControlRecord{Device: id, Reference: ref, Action: "denied", Value: fmt.Sprintf("%t", on), Origin: "IEDLab", Result: err.Error(), Via: "sim"})
		return err
	}
	m.log.addControl(ControlRecord{Device: id, Reference: ref, Action: "operate", Value: fmt.Sprintf("%t", on), Origin: "IEDLab", Via: "sim"})
	return client.Conclude(ctx)
}
func dialAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	return net.JoinHostPort(host, port)
}

// observedStore traces MMS traffic into the journal. Reads are aggregated per
// second: a polling client would otherwise flood the bounded journal and evict
// control records. Writes stay individual because they are rare and matter.
type observedStore struct {
	mms.Store
	log *journal
	id  string

	reads   sync.Mutex
	count   int
	objects map[string]bool
	order   []string
	stop    chan struct{}
	closing sync.Once
}

func newObservedStore(store mms.Store, log *journal, id string) *observedStore {
	s := &observedStore{Store: store, log: log, id: id, objects: map[string]bool{}, stop: make(chan struct{})}
	go s.flushLoop()
	return s
}

// follow stops the aggregation shortly after the runtime it observes.
func (s *observedStore) follow(done <-chan struct{}) {
	go func() {
		<-done
		s.close()
	}()
}

func (s *observedStore) close() {
	s.closing.Do(func() {
		close(s.stop)
		s.flush()
	})
}

func (s *observedStore) flushLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
			s.flush()
		}
	}
}

func (s *observedStore) flush() {
	s.reads.Lock()
	count, objects, order := s.count, s.objects, s.order
	s.count, s.objects, s.order = 0, map[string]bool{}, nil
	s.reads.Unlock()
	s.emit(count, objects, order)
}

func (s *observedStore) emit(count int, objects map[string]bool, order []string) {
	if count == 0 {
		return
	}
	names := order
	if len(names) > 5 {
		names = names[:5]
	}
	message := fmt.Sprintf("mms.read ×%d over %d objects · %s", count, len(objects), strings.Join(names, ", "))
	if len(order) > 5 {
		message += fmt.Sprintf(" (+%d more)", len(order)-5)
	}
	s.log.add("mms.read", s.id, message)
}

func (s *observedStore) note(name string) {
	s.reads.Lock()
	defer s.reads.Unlock()
	s.count++
	if !s.objects[name] {
		s.objects[name] = true
		s.order = append(s.order, name)
	}
}

func (s *observedStore) ReadBatch(ctx context.Context, accesses []mms.VariableAccess) []mms.ReadResult {
	results := mms.ReadBatch(ctx, s.Store, accesses)
	for i := range results {
		if i < len(accesses) {
			s.note(accesses[i].Name.String())
		}
	}
	return results
}

func (s *observedStore) Read(ctx context.Context, a mms.VariableAccess) (mms.Value, error) {
	v, err := s.Store.Read(ctx, a)
	s.note(a.Name.String())
	return v, err
}
func (s *observedStore) Write(ctx context.Context, a mms.VariableAccess, v mms.Value) error {
	err := s.Store.Write(ctx, a, v)
	message := fmt.Sprintf("%s ← %s", a.Name, v)
	if err != nil {
		message += fmt.Sprintf(" · %v", err)
	}
	s.log.add("mms.write", s.id, message)
	return err
}
