// SPDX-License-Identifier: GPL-3.0-or-later
// Package simulator runs an SCL-backed device without depending on a CLI, HTTP
// server or gateway application. Bind hardware/virtual actuators with the
// existing scl.IED.OnOperate and commit acquisitions with scl.IED.Update.
package simulator

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

type GOOSEMode string

const (
	GOOSEOff      GOOSEMode = "off"
	GOOSEMemory   GOOSEMode = "memory"
	GOOSEEthernet GOOSEMode = "ethernet"
)

type Event struct {
	Kind, Stream string
	GOOSE        *goose.Event
	Err          error
}
type Options struct {
	Listen      string // empty uses 127.0.0.1:8102; port zero requests a free port
	MMS         mms.Config
	GOOSE       GOOSEMode // empty defaults to memory
	Interface   string
	Promiscuous bool
	Store       mms.Store   // optional decorator, e.g. protocol tracing around the IED
	Observe     func(Event) // concurrent callback; must return promptly, never call Stop here
}

// Runtime owns its listener, accepted associations, links and protocol workers.
// Stopping transport does not discard the model; it can be started again.
// Automatic control execution is intentionally supplied by the caller's model.
type Runtime struct {
	model   *scl.IED
	address string
	mode    GOOSEMode
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	err     error
	server  *mms.Server
	links   []goose.FrameIO
	observe func(Event)
}

func Start(ctx context.Context, model *scl.IED, options Options) (*Runtime, error) {
	if ctx == nil || model == nil {
		return nil, fmt.Errorf("simulator: model and context are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Listen == "" {
		options.Listen = "127.0.0.1:8102"
	}
	if options.GOOSE == "" {
		options.GOOSE = GOOSEMemory
	}
	if options.GOOSE != GOOSEOff && options.GOOSE != GOOSEMemory && options.GOOSE != GOOSEEthernet {
		return nil, fmt.Errorf("simulator: choose off, memory or ethernet GOOSE")
	}
	server, err := model.NewServer(options.MMS)
	if err != nil {
		return nil, err
	}
	if options.Store != nil {
		server.Store = options.Store
	}
	listener, err := net.Listen("tcp", options.Listen)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	r := &Runtime{model: model, address: listener.Addr().String(), mode: options.GOOSE, ctx: runCtx, cancel: cancel, done: make(chan struct{}), server: server, observe: options.Observe}
	rollback := func() {
		cancel()
		listener.Close()
		server.Close()
		for _, link := range r.links {
			link.Close()
		}
	}
	var nic *ethernet.Conn
	if options.GOOSE == GOOSEEthernet {
		if options.Interface == "" {
			rollback()
			return nil, fmt.Errorf("simulator: Ethernet interface is required")
		}
		nic, err = ethernet.Open(ethernet.Config{Interface: options.Interface, EtherType: goose.EtherType, Promiscuous: options.Promiscuous})
		if err != nil {
			rollback()
			return nil, err
		}
		r.links = append(r.links, nic)
	}
	var tasks []func() error
	for _, stream := range model.Streams() {
		if options.GOOSE == GOOSEOff {
			break
		}
		stream := stream
		var link goose.FrameIO = nic
		source := [6]byte{2, 0, 0, 0, 0, 1}
		if nic != nil {
			source = nic.MAC()
		} else {
			memory, _ := NewMemoryLink(64)
			link = memory
			r.links = append(r.links, memory)
			subscriber, err := stream.NewSubscriber()
			if err != nil {
				rollback()
				return nil, err
			}
			tasks = append(tasks, func() error { return subscriber.Run(runCtx, memory) }, func() error {
				for {
					select {
					case <-runCtx.Done():
						return runCtx.Err()
					case event := <-subscriber.Events():
						if event.Kind != goose.Retransmission {
							r.emit(Event{Kind: "goose." + event.Kind.String(), Stream: stream.Info().Reference, GOOSE: &event})
						}
					}
				}
			})
		}
		l := link
		tasks = append(tasks, func() error { return stream.Run(runCtx, l, source) })
	}
	// All fallible setup completes before launching workers. A setup error can
	// never leave an unreachable listener or half-started publication running.
	stopTransport := context.AfterFunc(runCtx, func() {
		listener.Close()
		server.Close()
		for _, link := range r.links {
			link.Close()
		}
	})
	r.worker(func() error { return server.Serve(listener) })
	for _, task := range tasks {
		r.worker(task)
	}
	go func() {
		r.wg.Wait()
		cancel()
		stopTransport()
		listener.Close()
		server.Close()
		for _, link := range r.links {
			link.Close()
		}
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := server.Shutdown(shutdown); err != nil {
			r.recordError(err)
		}
		close(r.done)
	}()
	return r, nil
}
func (r *Runtime) emit(event Event) {
	if r.observe != nil {
		r.observe(event)
	}
}
func (r *Runtime) recordError(err error) {
	r.mu.Lock()
	if r.err == nil {
		r.err = err
	}
	r.mu.Unlock()
}
func (r *Runtime) worker(task func() error) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := task(); err != nil && r.ctx.Err() == nil {
			r.recordError(err)
			r.emit(Event{Kind: "error", Err: err})
			r.cancel()
		}
	}()
}
func (r *Runtime) Address() string       { return r.address }
func (r *Runtime) GOOSEMode() GOOSEMode  { return r.mode }
func (r *Runtime) Model() *scl.IED       { return r.model }
func (r *Runtime) Done() <-chan struct{} { return r.done }
func (r *Runtime) Err() error            { r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *Runtime) Wait(ctx context.Context) error {
	select {
	case <-r.done:
		return r.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *Runtime) Stop(ctx context.Context) error { r.cancel(); return r.Wait(ctx) }
func (r *Runtime) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.Stop(ctx)
}
