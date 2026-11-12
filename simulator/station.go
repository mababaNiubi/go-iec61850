// SPDX-License-Identifier: GPL-3.0-or-later
package simulator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/sv"
)

// InputReading separates the last observed value from its current usability.
// ReceivedAt and ValidUntil refer to accepted traffic, not browser polling.
// State is waiting, valid, unbound, invalid, quality or expired. A retained
// Value alone does not establish usability: inspect Valid and HasQuality.
// Address is the optional intAddr or scoped fallback; Reference is always the
// InputReference.Key. Counter/SampleSync apply to SV and state/sequence to GOOSE.
type InputReading struct {
	Receiver, AP, Address, Service, Source string
	Reference, Network, State              string // scoped input key, source SubNetwork and usability state
	Value                                  data.Value
	Quality                                ied.Quality
	HasQuality, Valid                      bool      // nonzero source quality is conservatively unusable
	ReceivedAt, ValidUntil, LastGoodAt     time.Time // local reception, expiry and last usable acquisition
	Counter                                uint16
	SampleSync                             uint8
	StateNumber, SequenceNumber            uint32
	Error                                  string
}

// StationDevice associates one owning server AP with its model and running transports.
type StationDevice struct {
	IED, AP string
	Model   *scl.IED
	Runtime *Runtime
}

// SampleEvent contains every bound field from ONE ASDU. Calculations can use
// simultaneous inputs without assembling independently changing callbacks.
type SampleEvent struct {
	Receiver, AP, Source string
	Event                sv.Event
	Inputs               []InputReading
}

// GOOSEEvent delivers an accepted state or timeout together with all bound receiver inputs
// from that frame.
type GOOSEEvent struct {
	Receiver, AP, Source string
	Event                goose.Event
	Inputs               []InputReading
}

// StationOptions supplies model/source overrides and synchronous application callbacks. Keys
// use IED/AP or full SVCB references. Callbacks must return promptly.
type StationOptions struct {
	Models           map[string]*scl.IED      // IED/AP; omitted models are constructed from SCL
	Listen           map[string]string        // IED/AP; default loopback with an allocated port
	Sources          map[string]SampledValues // complete SVCB reference
	NominalFrequency uint16                   // 0 selects 50 Hz; supported values are 50 and 60
	InputTimeout     time.Duration            // SV receive timeout, default one second
	StrictInputs     bool                     // fail startup on unresolved or disconnected inputs
	// Callbacks run on receiver workers. Return promptly and close the station
	// from its owner. OnSample/OnGOOSE errors stop the station visibly.
	Observe  func(InputReading) // changes only, suitable for user interfaces
	OnSample func(context.Context, SampleEvent) error
	OnGOOSE  func(context.Context, GOOSEEvent) error
}

// Station owns one software Ethernet bus per SubNetwork. ServerAt aliases
// expose another physical port of a server without bridging the two networks.
type Station struct {
	buses    map[string]*MemoryBus
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
	devices  []StationDevice
	mu       sync.Mutex
	readings map[string]InputReading
	options  StationOptions
	ctx      context.Context
	err      error
}
type gooseBinding struct {
	ref   scl.InputReference
	input scl.Input
}
type svBinding struct {
	ref   scl.InputReference
	input scl.SampledInput
}
type gooseRoute struct {
	receiver, ap, sourceAP, network string
	bindings                        []gooseBinding
}
type svRoute struct {
	receiver, ap, network string
	profile               sv.Profile
	bindings              []svBinding
}

// StartStation resolves all owning server APs and registers inputs before publishing. The
// returned station owns its listeners, network buses and workers; defer Close. StrictInputs
// rejects unresolved wiring.
func StartStation(ctx context.Context, system *scl.System, options StationOptions) (station *Station, err error) {
	if ctx == nil || system == nil {
		return nil, fmt.Errorf("simulator: station requires context and SCL")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.InputTimeout < 0 || options.InputTimeout > 24*time.Hour {
		return nil, fmt.Errorf("simulator: invalid input timeout")
	}
	if options.NominalFrequency != 0 && options.NominalFrequency != 50 && options.NominalFrequency != 60 {
		return nil, fmt.Errorf("simulator: nominal frequency must be 50 or 60 Hz")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s := &Station{buses: map[string]*MemoryBus{}, cancel: cancel, ctx: runCtx, readings: map[string]InputReading{}, options: options}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	for _, ap := range system.AccessPoints() {
		if !ap.HasServer {
			continue
		}
		key := ap.IED + "/" + ap.AP
		model := options.Models[key]
		if model == nil {
			model, err = system.Device(ap.IED, ap.AP)
			if err != nil {
				return nil, err
			}
		}
		s.devices = append(s.devices, StationDevice{IED: ap.IED, AP: ap.AP, Model: model})
	}
	if len(s.devices) == 0 || len(s.devices) > 32 {
		return nil, fmt.Errorf("simulator: station needs 1..32 server access points")
	}
	// Resolve and register inputs before any publisher starts.
	gr, sr, err := s.routes(system)
	if err != nil {
		return nil, err
	}
	for _, route := range gr {
		if err = s.startGOOSE(route); err != nil {
			return nil, err
		}
	}
	for _, route := range sr {
		if err = s.startSV(route); err != nil {
			return nil, err
		}
	}
	for i := range s.devices {
		device := &s.devices[i]
		profiles, e := system.SVStreams(device.IED, device.AP)
		if e != nil {
			return nil, e
		}
		var sources []SampledValues
		for _, profile := range profiles {
			source, exists := options.Sources[profile.Reference]
			if !exists {
				source, e = ModelSVSource(device.Model, profile)
				if e != nil {
					return nil, e
				}
			}
			source.Profile = profile.Clone()
			if source.NominalFrequency == 0 {
				source.NominalFrequency = options.NominalFrequency
			}
			sources = append(sources, source)
		}
		network, e := system.PublisherNetwork(device.IED, device.AP)
		if e != nil {
			return nil, e
		}
		if network == "" && len(system.Networks()) > 0 {
			network = "unconnected:" + device.IED + "/" + device.AP
		}
		listen := options.Listen[device.IED+"/"+device.AP]
		if listen == "" {
			listen = "127.0.0.1:0"
		}
		device.Runtime, err = Start(runCtx, device.Model, Options{
			Listen: listen, GOOSE: GOOSEMemory, Bus: s.bus(network), SV: sources,
			Observe: func(event Event) {
				if event.Err != nil {
					s.fail(event.Err)
				}
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Station) bus(network string) *MemoryBus {
	b := s.buses[network]
	if b == nil {
		b = &MemoryBus{}
		s.buses[network] = b
	}
	return b
}
func inputAddress(ref scl.InputReference) string {
	if ref.IntAddr != "" {
		return ref.IntAddr
	}
	return ref.Key()
}
func initialInput(receiver, ap, network, source string, ref scl.InputReference) InputReading {
	service := ref.Service
	if service == "SMV" {
		service = "SV"
	}
	return InputReading{Receiver: receiver, AP: ap, Address: inputAddress(ref), Reference: ref.Key(), Network: network, Service: service, Source: source, State: "waiting"}
}
func (s *Station) unresolved(receiver, ap string, ref scl.InputReference, err error) error {
	if s.options.StrictInputs {
		return err
	}
	reading := initialInput(receiver, ap, "", "", ref)
	reading.State, reading.Error = "unbound", err.Error()
	s.record(reading)
	return nil
}
func (s *Station) routes(system *scl.System) ([]*gooseRoute, []*svRoute, error) {
	gm := map[string]*gooseRoute{}
	sm := map[string]*svRoute{}
	var gr []*gooseRoute
	var sr []*svRoute
	for _, ap := range system.AccessPoints() {
		if ap.ServerAP != "" && ap.ServerAP != ap.AP {
			continue
		}
		refs, err := system.Inputs(ap.IED, ap.AP)
		if err != nil {
			return nil, nil, err
		}
		for _, ref := range refs {
			switch ref.Service {
			case "GOOSE":
				var input scl.Input
				var sourceAP string
				matches := 0
				for _, device := range s.devices {
					if device.IED != ref.SourceIED {
						continue
					}
					candidate, e := device.Model.ResolveGOOSEInput(ref)
					if e == nil {
						input, sourceAP = candidate, device.AP
						matches++
					}
				}
				if matches != 1 {
					err = s.unresolved(ap.IED, ap.AP, ref, fmt.Errorf("simulator: GOOSE input %s resolves to %d sources", ref.Key(), matches))
					if err != nil {
						return nil, nil, err
					}
					continue
				}
				network, e := system.SharedNetwork(ref.SourceIED, sourceAP, ap.IED, ap.AP)
				if e != nil {
					if err = s.unresolved(ap.IED, ap.AP, ref, e); err != nil {
						return nil, nil, err
					}
					continue
				}
				info := input.Stream.Info()
				key := ap.IED + "/" + ap.AP + "/" + network + "/" + info.Reference
				route := gm[key]
				if route == nil {
					route = &gooseRoute{receiver: ap.IED, ap: ap.AP, sourceAP: sourceAP, network: network}
					gm[key] = route
					gr = append(gr, route)
				}
				route.bindings = append(route.bindings, gooseBinding{ref, input})
				s.record(initialInput(ap.IED, ap.AP, network, info.Reference, ref))
			case "SMV":
				input, e := system.ResolveSVInput(ref)
				if e != nil {
					if err = s.unresolved(ap.IED, ap.AP, ref, e); err != nil {
						return nil, nil, err
					}
					continue
				}
				source := strings.Split(input.Profile.Reference, "/")
				network, e := system.SharedNetwork(source[0], source[1], ap.IED, ap.AP)
				if e != nil {
					if err = s.unresolved(ap.IED, ap.AP, ref, e); err != nil {
						return nil, nil, err
					}
					continue
				}
				key := ap.IED + "/" + ap.AP + "/" + network + "/" + input.Profile.Reference
				route := sm[key]
				if route == nil {
					route = &svRoute{receiver: ap.IED, ap: ap.AP, network: network, profile: input.Profile}
					sm[key] = route
					sr = append(sr, route)
				}
				route.bindings = append(route.bindings, svBinding{ref, input})
				s.record(initialInput(ap.IED, ap.AP, network, input.Profile.Reference, ref))
			}
		}
	}
	return gr, sr, nil
}
func (s *Station) startGOOSE(route *gooseRoute) error {
	stream := route.bindings[0].input.Stream
	sub, err := stream.NewSubscriber()
	if err != nil {
		return err
	}
	var dispatch sync.Mutex
	notify := func(event goose.Event, receiveErr error) {
		dispatch.Lock()
		defer dispatch.Unlock()
		var readings []InputReading
		for _, binding := range route.bindings {
			reading := initialInput(route.receiver, route.ap, route.network, stream.Info().Reference, binding.ref)
			reading.ReceivedAt, reading.ValidUntil = event.ReceivedAt, event.ValidUntil
			reading.StateNumber, reading.SequenceNumber = event.Frame.PDU.StateNumber, event.Frame.PDU.SequenceNumber
			reading.State = "valid"
			if receiveErr != nil {
				reading.State, reading.Error = "invalid", receiveErr.Error()
			} else if event.Kind == goose.Expired {
				reading.State, reading.Error = "expired", "GOOSE timeAllowedToLive expired"
			} else {
				value, e := binding.input.Value(event)
				if e != nil {
					reading.State, reading.Error = "invalid", e.Error()
				} else {
					reading.Value, reading.Valid = value, true
					quality, present, e := binding.input.Quality(event)
					if present {
						q, decodeErr := ied.DecodeQuality(quality)
						reading.Quality, reading.HasQuality = q, true
						if e != nil || decodeErr != nil || q != 0 {
							reading.Valid = false
							reading.State, reading.Error = "quality", fmt.Sprintf("source quality %d", q)
						}
					}
				}
			}
			if saved, ok := s.record(reading); ok {
				readings = append(readings, saved)
			}
		}
		if len(readings) > 0 && s.options.OnGOOSE != nil {
			if err := s.options.OnGOOSE(s.ctx, GOOSEEvent{route.receiver, route.ap, stream.Info().Reference, event, readings}); err != nil {
				s.fail(err)
			}
		}
	}
	receiver := &goose.Receiver{}
	remove, err := receiver.Add(sub, notify)
	if err != nil {
		return err
	}
	if err = s.receive(route.network, func(ctx context.Context, link goose.FrameIO) error { return receiver.Run(ctx, link) }); err != nil {
		remove()
		return err
	}
	s.watch(func() {
		if event, ok := sub.CheckTimeout(); ok {
			notify(event, nil)
		}
	}, remove)
	return nil
}
func (s *Station) startSV(route *svRoute) error {
	hz := s.options.NominalFrequency
	if source, ok := s.options.Sources[route.profile.Reference]; ok && source.NominalFrequency != 0 {
		hz = source.NominalFrequency
	}
	cfg, err := route.profile.SubscriptionConfig(sv.SubscriptionOptions{SyncPolicy: sv.AnySynchronization, NominalFrequency: hz, Timeout: s.options.InputTimeout})
	if err != nil {
		return err
	}
	sub, err := sv.NewSubscriber(cfg)
	if err != nil {
		return err
	}
	var dispatch sync.Mutex
	notify := func(events []sv.Event, receiveErr error) {
		dispatch.Lock()
		defer dispatch.Unlock()
		// Validation can reject the entire packet before any sample event exists.
		if receiveErr != nil {
			for _, binding := range route.bindings {
				reading := initialInput(route.receiver, route.ap, route.network, route.profile.Reference, binding.ref)
				reading.State, reading.Error = "invalid", receiveErr.Error()
				s.record(reading)
			}
			return
		}
		for _, event := range events {
			var readings []InputReading
			for _, binding := range route.bindings {
				reading := initialInput(route.receiver, route.ap, route.network, route.profile.Reference, binding.ref)
				reading.ReceivedAt, reading.ValidUntil = event.ReceivedAt, event.ValidUntil
				reading.Counter, reading.SampleSync = event.ASDU.SampleCount, event.ASDU.SampleSync
				reading.State = "valid"
				switch event.Kind {
				case sv.Expired:
					reading.State, reading.Error = "expired", "SV receive timeout"
				default:
					value, e := binding.input.Field.Decode(event.ASDU.Data)
					if e != nil {
						reading.State, reading.Error = "invalid", e.Error()
					} else {
						reading.Value, reading.Valid = value, true
					}
					if binding.input.Quality != nil && reading.Valid {
						q, e := binding.input.Quality.Decode(event.ASDU.Data)
						number, conversion := q.Uint()
						if e != nil || conversion != nil || number > 0x1fff {
							reading.Valid = false
							reading.State, reading.Error = "invalid", "invalid SV quality"
						} else {
							reading.Quality, reading.HasQuality = ied.Quality(number), true
							if number != 0 {
								reading.Valid = false
								reading.State, reading.Error = "quality", fmt.Sprintf("source quality %d", number)
							}
						}
					}
					if event.Kind == sv.SampleGap {
						reading.Valid = false
						reading.State, reading.Error = "gap", fmt.Sprintf("missing %d samples", event.MissingSamples)
					}
				}
				if saved, ok := s.record(reading); ok {
					readings = append(readings, saved)
				}
			}
			if len(readings) > 0 && s.options.OnSample != nil {
				if err := s.options.OnSample(s.ctx, SampleEvent{route.receiver, route.ap, route.profile.Reference, event, readings}); err != nil {
					s.fail(err)
				}
			}
		}
	}
	receiver := &sv.Receiver{}
	remove, err := receiver.Add(sub, route.profile.ValidateFrame, notify)
	if err != nil {
		return err
	}
	if err = s.receive(route.network, func(ctx context.Context, link goose.FrameIO) error { return receiver.Run(ctx, link) }); err != nil {
		remove()
		return err
	}
	s.watch(func() {
		if event, ok := sub.CheckTimeout(); ok {
			notify([]sv.Event{event}, nil)
		}
	}, remove)
	return nil
}
func (s *Station) watch(check, remove func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer remove()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
}
func (s *Station) receive(network string, run func(context.Context, goose.FrameIO) error) error {
	port, err := s.bus(network).Open(4096)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer port.Close()
		if err := run(s.ctx, port); err != nil && s.ctx.Err() == nil {
			s.fail(err)
		}
	}()
	return nil
}
func (s *Station) record(reading InputReading) (InputReading, bool) {
	key := reading.Receiver + "/" + reading.AP + "/" + reading.Reference
	s.mu.Lock()
	previous, exists := s.readings[key]
	// Do not let an old timeout overwrite a more recently accepted frame.
	if exists && reading.State == "expired" && previous.ValidUntil.After(reading.ValidUntil) {
		s.mu.Unlock()
		return previous, false
	}
	if reading.State == "expired" || reading.ReceivedAt.IsZero() {
		reading.Value, reading.ReceivedAt, reading.ValidUntil = previous.Value, previous.ReceivedAt, previous.ValidUntil
		reading.Counter, reading.SampleSync = previous.Counter, previous.SampleSync
		reading.Quality, reading.HasQuality = previous.Quality, previous.HasQuality
		reading.StateNumber, reading.SequenceNumber = previous.StateNumber, previous.SequenceNumber
	}
	reading.LastGoodAt = previous.LastGoodAt
	if reading.Valid {
		reading.LastGoodAt = reading.ReceivedAt
	}
	s.readings[key] = reading
	s.mu.Unlock()
	if s.options.Observe != nil && (!exists || !previous.Value.Equal(reading.Value) || previous.Error != reading.Error || previous.Valid != reading.Valid || previous.Quality != reading.Quality) {
		s.options.Observe(reading)
	}
	return reading, true
}

// Devices returns a copy of the device list. Models and runtimes remain owned by the station.
func (s *Station) Devices() []StationDevice { return append([]StationDevice(nil), s.devices...) }
func (s *Station) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.cancel()
}

// Err returns the terminal failure, or nil while no failure has been recorded. It is safe to
// call concurrently.
func (s *Station) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// Done closes when the station context is cancelled, including on a worker failure.
// It does not join workers; call Close from the owner to wait for cleanup.
func (s *Station) Done() <-chan struct{} { return s.ctx.Done() }

// Inputs returns a sorted snapshot of input observations. Inspect Valid and State before using
// Value; invalid or expired readings can retain the last observed value.
func (s *Station) Inputs() []InputReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InputReading, 0, len(s.readings))
	for _, reading := range s.readings {
		out = append(out, reading)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Receiver+out[i].AP+out[i].Reference < out[j].Receiver+out[j].AP+out[j].Reference
	})
	return out
}

// Close releases owned transports and workers. Defer it immediately after successful
// construction.
func (s *Station) Close() error {
	s.once.Do(func() {
		s.cancel()
		for _, device := range s.devices {
			if device.Runtime != nil {
				device.Runtime.Close()
			}
		}
		s.wg.Wait()
		for _, bus := range s.buses {
			bus.Close()
		}
	})
	return nil
}
