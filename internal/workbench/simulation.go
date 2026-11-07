// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func nowValue() mms.Value { return mms.UTC(time.Now()) }

type SimulationOptions struct {
	Listen      string `json:"listen"`
	GOOSE       string `json:"goose"` // off, memory, ethernet
	Interface   string `json:"interface"`
	Controls    bool   `json:"controls"`
	Promiscuous bool   `json:"promiscuous"`
}
type simulation struct {
	address, mode string
	server        *mms.Server
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan struct{}
	wg            sync.WaitGroup
	mu            sync.Mutex
	err           error
	links         []goose.FrameIO
}

func (s *simulation) failure() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *simulation) worker(log *journal, id string, fn func() error) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := fn(); err != nil && s.ctx.Err() == nil {
			s.mu.Lock()
			if s.err == nil {
				s.err = err
			}
			s.mu.Unlock()
			log.add("error", id, err.Error())
			s.cancel()
		}
	}()
}
func (s *simulation) stop() error {
	s.cancel()
	// Context cancellation releases frame readers before the caller closes links.
	s.server.Close()
	for _, l := range s.links {
		l.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.server.Shutdown(ctx)
}
func (m *Manager) Start(id string, opt SimulationOptions) (DeviceSummary, error) {
	// Lock order is Manager -> device. Close cannot miss a simulator being
	// started by a concurrent HTTP request after it takes its device snapshot.
	m.mu.RLock()
	d := m.devices[id]
	if m.closed || d == nil {
		m.mu.RUnlock()
		return DeviceSummary{}, ErrConflict
	}
	d.mu.Lock()
	m.mu.RUnlock()
	var err error
	if d.run != nil {
		select {
		case <-d.run.done:
			if err = d.run.stop(); err != nil {
				d.mu.Unlock()
				return DeviceSummary{}, err
			}
			d.run = nil
		default:
			d.mu.Unlock()
			return DeviceSummary{}, fmt.Errorf("%w: simulation is already running", ErrConflict)
		}
	}
	if opt.Listen == "" {
		opt.Listen = "127.0.0.1:8102"
	}
	if opt.GOOSE == "" {
		opt.GOOSE = "memory"
	}
	if opt.GOOSE != "memory" && opt.GOOSE != "ethernet" && opt.GOOSE != "off" {
		d.mu.Unlock()
		return DeviceSummary{}, fmt.Errorf("choose off, memory or ethernet GOOSE")
	}
	srv, err := d.ied.NewServer(mms.Config{})
	if err != nil {
		d.mu.Unlock()
		return DeviceSummary{}, err
	}
	// Log real protocol accesses, including reads from external MMS clients.
	srv.Store = &observedStore{Store: d.ied, log: &m.log, id: id}
	listener, err := net.Listen("tcp", opt.Listen)
	if err != nil {
		d.mu.Unlock()
		return DeviceSummary{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &simulation{address: listener.Addr().String(), mode: opt.GOOSE, server: srv, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	var nic *ethernet.Conn
	if opt.GOOSE == "ethernet" {
		if opt.Interface == "" {
			listener.Close()
			cancel()
			d.mu.Unlock()
			return DeviceSummary{}, fmt.Errorf("select an Ethernet interface")
		}
		nic, err = ethernet.Open(ethernet.Config{Interface: opt.Interface, EtherType: goose.EtherType, Promiscuous: opt.Promiscuous})
		if err != nil {
			listener.Close()
			cancel()
			d.mu.Unlock()
			return DeviceSummary{}, err
		}
		r.links = append(r.links, nic)
	}
	d.controlEnabled.Store(opt.Controls)
	d.run = r
	r.worker(&m.log, id, func() error {
		stop := context.AfterFunc(ctx, func() { srv.Close(); listener.Close() })
		defer stop()
		defer listener.Close()
		return srv.Serve(listener)
	})
	for _, stream := range d.ied.Streams() {
		g := stream
		if opt.GOOSE == "off" {
			break
		}
		var link goose.FrameIO = nic
		source := [6]byte{2, 0, 0, 0, 0, 1}
		if nic != nil {
			source = nic.MAC()
		} else {
			link = &memoryLink{packets: make(chan []byte, 64), done: make(chan struct{})}
			r.links = append(r.links, link)
			sub, e := g.NewSubscriber()
			if e != nil {
				r.mu.Lock()
				r.err = e
				r.mu.Unlock()
				cancel()
				continue
			}
			l := link
			r.worker(&m.log, id, func() error { return sub.Run(ctx, l) })
			r.worker(&m.log, id, func() error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case e := <-sub.Events():
						if e.Kind != goose.Retransmission {
							m.log.add("goose."+e.Kind.String(), id, fmt.Sprintf("%s · st=%d sq=%d · %d values", g.Info().Reference, e.Frame.PDU.StateNumber, e.Frame.PDU.SequenceNumber, len(e.Frame.PDU.Values)))
						}
					}
				}
			})
		}
		l := link
		r.worker(&m.log, id, func() error { return g.Run(ctx, l, source) })
	}
	go func() {
		r.wg.Wait()
		// Wait for MMS connection handlers too. Close releases blocked TCP reads.
		finish, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		srv.Shutdown(finish)
		close(r.done)
	}()
	d.mu.Unlock()
	m.log.add("simulation.start", id, fmt.Sprintf("MMS listening on %s · GOOSE %s", r.address, r.mode))
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
	client, err := mms.Dial(ctx, dialAddress(r.address), d.ied.ClientConfig())
	if err != nil {
		return err
	}
	defer client.Close()
	value, err := (scl.Command{Value: on, Number: uint8(d.controlNumber.Add(1)), OriginCategory: 2, OriginID: []byte("IEDLab"), Time: time.Now(), InterlockCheck: true}).MMSValue()
	if err != nil {
		return err
	}
	if err = client.Write(ctx, n, value); err != nil {
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

type memoryLink struct {
	packets chan []byte
	done    chan struct{}
	once    sync.Once
}

func (l *memoryLink) ReadFrame(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, net.ErrClosed
	case b := <-l.packets:
		return b, nil
	}
}
func (l *memoryLink) WriteFrame(ctx context.Context, b []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return net.ErrClosed
	case l.packets <- bytes.Clone(b):
		return nil
	}
}
func (l *memoryLink) Close() error { l.once.Do(func() { close(l.done) }); return nil }
