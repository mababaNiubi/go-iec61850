// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"fmt"
	"net"
	"strings"
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
	runtime, err := simulator.Start(context.Background(), d.ied, simulator.Options{
		Listen: opt.Listen, GOOSE: simulator.GOOSEMode(opt.GOOSE), Interface: opt.Interface,
		Promiscuous: opt.Promiscuous,
		Store:       &observedStore{Store: d.ied, log: &m.log, id: id},
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
		d.controlEnabled.Store(false)
		d.mu.Unlock()
		return DeviceSummary{}, err
	}
	d.run = &simulation{runtime: runtime, address: runtime.Address(), mode: string(runtime.GOOSEMode()), done: runtime.Done()}
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
	if d.run != nil {
		err = d.run.stop()
		if err == nil {
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
	m.log.add("control.interlock", id, fmt.Sprintf("Interlock blocked=%t", blocked))
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
		err = d.ied.OnOperate(p.Name, func(ctx context.Context, cmd scl.Command) error {
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
			log.add("control.feedback", d.id, fmt.Sprintf("%s ← %s · origin=%s ctlNum=%d", target, value, string(cmd.OriginID), cmd.Number))
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
		m.log.add("control.denied", id, err.Error())
		return err
	}
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

type observedStore struct {
	mms.Store
	log *journal
	id  string
}

func (s *observedStore) Read(ctx context.Context, a mms.VariableAccess) (mms.Value, error) {
	v, err := s.Store.Read(ctx, a)
	message := fmt.Sprintf("%s = %s", a.Name, v)
	if err != nil {
		message = fmt.Sprintf("%s: %v", a.Name, err)
	}
	s.log.add("mms.read", s.id, message)
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
