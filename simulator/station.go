// SPDX-License-Identifier: GPL-3.0-or-later
package simulator

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/sv"
)

type InputReading struct {
	Receiver, AP, Address, Service, Source string
	Value                                  data.Value
	ReceivedAt                             time.Time
	Counter                                uint16
	Error                                  string
}
type StationDevice struct {
	IED, AP string
	Model   *scl.IED
	Runtime *Runtime
}
type StationOptions struct {
	// Keys are IED/AP. Missing models are constructed from the SCL. Missing
	// listen addresses use loopback and an automatically allocated TCP port.
	Models map[string]*scl.IED
	Listen map[string]string
	// Source overrides use the complete SVCB reference as their key. The
	// default source encodes each MU's current modeled acquisition values.
	Sources map[string]SampledValues
	// Observe runs on receiver goroutines when a value/error changes. Return
	// promptly; close the station from the owning goroutine, not this callback.
	Observe func(InputReading)
}

// Station connects all SCL server APs and their GOOSE/SMV ExtRefs through one
// owned software Ethernet bus. intAddr remains an application input name: no
// undocumented receiver writes or measurement algorithms are inferred from it.
type Station struct {
	bus      *MemoryBus
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
	devices  []StationDevice
	mu       sync.Mutex
	readings map[string]InputReading
	observe  func(InputReading)
	ctx      context.Context
	err      error
}

func StartStation(ctx context.Context, system *scl.System, options StationOptions) (station *Station, err error) {
	if ctx == nil || system == nil {
		return nil, fmt.Errorf("simulator: station requires context and SCL")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s := &Station{bus: &MemoryBus{}, cancel: cancel, ctx: runCtx, readings: map[string]InputReading{}, observe: options.Observe}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	for _, ap := range system.AccessPoints() {
		if ap.HasServer {
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
	}
	if len(s.devices) == 0 || len(s.devices) > 32 {
		return nil, fmt.Errorf("simulator: station needs 1..32 server access points")
	}
	// Register receivers before publishers, retaining initial GOOSE state.
	for _, ap := range system.AccessPoints() {
		refs, e := system.GOOSEInputs(ap.IED, ap.AP)
		if e != nil {
			return nil, e
		}
		for _, ref := range refs {
			if ref.IntAddr == "" {
				return nil, fmt.Errorf("simulator: GOOSE ExtRef requires intAddr")
			}
			var source *scl.IED
			for _, device := range s.devices {
				if device.IED == ref.SourceIED {
					if _, e := device.Model.Input(ap.IED, ap.AP, ref.IntAddr); e == nil {
						if source != nil {
							return nil, fmt.Errorf("simulator: ambiguous input source")
						}
						source = device.Model
					}
				}
			}
			if source == nil {
				return nil, fmt.Errorf("simulator: missing GOOSE source for %s", ref.IntAddr)
			}
			input, e := source.Input(ap.IED, ap.AP, ref.IntAddr)
			if e != nil {
				return nil, e
			}
			sub, e := input.Stream.NewSubscriber()
			if e != nil {
				return nil, e
			}
			receiver := &goose.Receiver{}
			_, e = receiver.Add(sub, func(event goose.Event, receiveErr error) {
				reading := InputReading{Receiver: ap.IED, AP: ap.AP, Address: ref.IntAddr, Service: "GOOSE", Source: input.Stream.Info().Reference, ReceivedAt: event.ReceivedAt}
				if receiveErr != nil {
					reading.Error = receiveErr.Error()
				} else {
					reading.Value, receiveErr = input.Value(event)
					if receiveErr != nil {
						reading.Error = receiveErr.Error()
					}
				}
				s.record(reading)
			})
			if e != nil {
				return nil, e
			}
			if e = s.receive(runCtx, func(ctx context.Context, link goose.FrameIO) error { return receiver.Run(ctx, link) }); e != nil {
				return nil, e
			}
		}
		refs, e = system.SVInputs(ap.IED, ap.AP)
		if e != nil {
			return nil, e
		}
		for _, ref := range refs {
			input, e := system.SVInput(ap.IED, ap.AP, ref.IntAddr)
			if e != nil {
				return nil, e
			}
			cfg, e := input.Profile.SubscriptionConfig(sv.SubscriptionOptions{SyncPolicy: sv.AnySynchronization})
			if e != nil {
				return nil, e
			}
			sub, e := sv.NewSubscriber(cfg)
			if e != nil {
				return nil, e
			}
			receiver := &sv.Receiver{}
			_, e = receiver.Add(sub, input.Profile.ValidateFrame, func(events []sv.Event, receiveErr error) {
				for _, event := range events {
					reading := InputReading{Receiver: ap.IED, AP: ap.AP, Address: ref.IntAddr, Service: "SV", Source: input.Profile.Reference, ReceivedAt: event.ReceivedAt, Counter: event.ASDU.SampleCount}
					if receiveErr != nil {
						reading.Error = receiveErr.Error()
					} else if event.Kind != sv.SampleReceived {
						reading.Error = event.Kind.String()
					} else {
						reading.Value, receiveErr = input.Field.Decode(event.ASDU.Data)
						if receiveErr != nil {
							reading.Error = receiveErr.Error()
						}
					}
					s.record(reading)
				}
			})
			if e != nil {
				return nil, e
			}
			if e = s.receive(runCtx, func(ctx context.Context, link goose.FrameIO) error { return receiver.Run(ctx, link) }); e != nil {
				return nil, e
			}
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
			sources = append(sources, source)
		}
		listen := options.Listen[device.IED+"/"+device.AP]
		if listen == "" {
			listen = "127.0.0.1:0"
		}
		device.Runtime, err = Start(runCtx, device.Model, Options{Listen: listen, GOOSE: GOOSEMemory, Bus: s.bus, SV: sources, Observe: func(event Event) {
			if event.Err != nil {
				s.fail(event.Err)
			}
		}})
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Station) receive(ctx context.Context, run func(context.Context, goose.FrameIO) error) error {
	port, err := s.bus.Open(4096)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer port.Close()
		if err := run(ctx, port); err != nil && ctx.Err() == nil {
			s.fail(err)
		}
	}()
	return nil
}
func (s *Station) record(reading InputReading) {
	key := reading.Receiver + "/" + reading.AP + "/" + reading.Service + "/" + reading.Address
	s.mu.Lock()
	previous, exists := s.readings[key]
	s.readings[key] = reading
	s.mu.Unlock()
	if s.observe != nil && (!exists || !previous.Value.Equal(reading.Value) || previous.Error != reading.Error) {
		s.observe(reading)
	}
}
func (s *Station) Devices() []StationDevice { return append([]StationDevice(nil), s.devices...) }
func (s *Station) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.cancel()
}
func (s *Station) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Station) Done() <-chan struct{} { return s.ctx.Done() }
func (s *Station) Inputs() []InputReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InputReading, 0, len(s.readings))
	for _, reading := range s.readings {
		out = append(out, reading)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Receiver+out[i].Address < out[j].Receiver+out[j].Address })
	return out
}
func (s *Station) Close() error {
	s.once.Do(func() {
		s.cancel()
		for _, device := range s.devices {
			if device.Runtime != nil {
				device.Runtime.Close()
			}
		}
		s.wg.Wait()
		s.bus.Close()
	})
	return nil
}
