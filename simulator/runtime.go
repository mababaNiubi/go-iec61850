// SPDX-License-Identifier: GPL-3.0-or-later
// Package simulator runs an SCL-backed device without depending on a CLI, HTTP
// server or gateway application. Bind hardware/virtual actuators with
// scl.IED.OnControl (or OnOperate for direct normal controls) and commit
// independent acquisitions with scl.IED.Update. Start runs one device;
// StartStation resolves a multi-IED SCL project onto isolated software buses.
//
// Runtime and Station own their transports and workers. Cancel their parent
// context and call Close from the owner to join workers. Receive callbacks
// execute synchronously; return promptly and never call Close inside them.
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

// GOOSEMode selects off, in-memory or physical Ethernet GOOSE transport.
type GOOSEMode string

const (
	GOOSEOff      GOOSEMode = "off"
	GOOSEMemory   GOOSEMode = "memory"
	GOOSEEthernet GOOSEMode = "ethernet"
)

// Event reports a runtime stream event or failure to the optional Observe callback.
type Event struct {
	Kind, Stream string
	GOOSE        *goose.Event
	Err          error
}

// Options configures owned MMS and Ethernet transports. Bind application controls on the model
// before Start.
type Options struct {
	Listen      string // empty uses 127.0.0.1:8102; port zero requests a free port
	MMS         mms.Config
	GOOSE       GOOSEMode // empty defaults to memory
	Interface   string
	Promiscuous bool
	Store       mms.Store       // optional decorator, e.g. protocol tracing around the IED
	Observe     func(Event)     // concurrent callback; must return promptly, never call Stop here
	Bus         *MemoryBus      // optional shared software station bus for multiple runtimes
	SV          []SampledValues // software sources published on Bus
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

// Start constructs transports and starts protocol workers for one model. ctx owns runtime
// lifetime; defer Close after success. Setup failure releases acquired resources.
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
			if options.Bus != nil {
				port, err := options.Bus.Open(4096)
				if err != nil {
					rollback()
					return nil, err
				}
				link = port
			} else {
				memory, _ := NewMemoryLink(64)
				link = memory
			}
			r.links = append(r.links, link)
			subscriber, err := stream.NewSubscriber()
			if err != nil {
				rollback()
				return nil, err
			}
			tasks = append(tasks, func() error { return subscriber.Run(runCtx, link) }, func() error {
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
	for _, source := range options.SV {
		if options.Bus == nil {
			rollback()
			return nil, fmt.Errorf("simulator: runtime SV requires a shared MemoryBus")
		}
		if _, _, _, err := prepareSV(source); err != nil {
			rollback()
			return nil, err
		}
		port, err := options.Bus.Open(4096)
		if err != nil {
			rollback()
			return nil, err
		}
		r.links = append(r.links, port)
		// A publisher has no reason to read its own copy of a bus broadcast.
		// Drain it to keep the endpoint bounded during a long simulation.
		tasks = append(tasks, func() error { return RunSampledValues(runCtx, port, source) }, func() error {
			for {
				if _, err := port.ReadFrame(runCtx); err != nil {
					return err
				}
			}
		})
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

// Address returns the actual MMS listen address, including the allocated port when Listen used
// port zero.
func (r *Runtime) Address() string { return r.address }

// GOOSEMode returns the configured transport mode.
func (r *Runtime) GOOSEMode() GOOSEMode { return r.mode }

// Model returns the application-owned SCL model used by this runtime.
func (r *Runtime) Model() *scl.IED { return r.model }

// Done closes when the owned connection or runtime has stopped. It carries no values; use Err
// to inspect failure.
func (r *Runtime) Done() <-chan struct{} { return r.done }

// Err returns the terminal failure, or nil while no failure has been recorded. It is safe to
// call concurrently.
func (r *Runtime) Err() error { r.mu.Lock(); defer r.mu.Unlock(); return r.err }

// Wait waits for runtime completion or ctx cancellation. Cancelling ctx only
// stops this wait; use Stop to also cancel the runtime's transport work.
func (r *Runtime) Wait(ctx context.Context) error {
	select {
	case <-r.done:
		return r.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop cancels transport work and waits within ctx. The model remains available to restart in
// another runtime.
func (r *Runtime) Stop(ctx context.Context) error { r.cancel(); return r.Wait(ctx) }

// Close cancels transport work and waits up to five seconds for workers. Defer
// it after successful Start; inspect its error if cleanup completion matters.
func (r *Runtime) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.Stop(ctx)
}
