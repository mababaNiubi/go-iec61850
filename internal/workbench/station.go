// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"fmt"
	"time"

	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

type stationRun struct {
	runtime *simulator.Station
	devices []*device
}
type StationInput struct {
	Receiver       string    `json:"receiver"`
	AP             string    `json:"ap"`
	Address        string    `json:"input"`
	Service        string    `json:"service"`
	Source         string    `json:"source"`
	Value          string    `json:"value"`
	ReceivedAt     time.Time `json:"received_at"`
	Counter        uint16    `json:"counter"`
	Error          string    `json:"error,omitempty"`
	Reference      string    `json:"reference"`
	Network        string    `json:"network"`
	State          string    `json:"state"`
	Valid          bool      `json:"valid"`
	Quality        uint32    `json:"quality"`
	HasQuality     bool      `json:"has_quality"`
	ValidUntil     time.Time `json:"valid_until"`
	LastGoodAt     time.Time `json:"last_good_at"`
	SampleSync     uint8     `json:"sample_sync"`
	StateNumber    uint32    `json:"state_number"`
	SequenceNumber uint32    `json:"sequence_number"`
}
type StationSummary struct {
	Model   string          `json:"model"`
	Status  string          `json:"status"`
	Error   string          `json:"error,omitempty"`
	Devices []DeviceSummary `json:"devices"`
	Inputs  []StationInput  `json:"inputs"`
}

func (m *Manager) StartStation(docID string) (StationSummary, error) {
	return m.StartStationWithControls(docID, true)
}

// StartStationWithControls is also used by the headless CLI so -controls=false
// has the same effect for a complete station as for an individual simulator.
func (m *Manager) StartStationWithControls(docID string, controls bool) (StationSummary, error) {
	m.stationOps.Lock()
	defer m.stationOps.Unlock()
	m.mu.RLock()
	doc := m.docs[docID]
	closed := m.closed
	existing := m.stations[docID]
	m.mu.RUnlock()
	if closed || doc == nil {
		return StationSummary{}, ErrNotFound
	}
	if existing != nil {
		return StationSummary{}, fmt.Errorf("station already started; stop it before restarting")
	}
	models := map[string]*scl.IED{}
	var devices []*device
	for _, ap := range doc.AccessPoints {
		if ap.Server {
			summary, err := m.Select(docID, ap.IED, ap.AP)
			if err != nil {
				return StationSummary{}, err
			}
			d, err := m.get(summary.ID)
			if err != nil {
				return StationSummary{}, err
			}
			devices = append(devices, d)
			models[ap.IED+"/"+ap.AP] = d.ied
		}
	}
	// Hold all member lifecycle locks until transport ownership is installed.
	// Protocol callbacks only use atomic control flags and the model lock.
	for _, d := range devices {
		d.mu.Lock()
	}
	unlock := func() {
		for i := len(devices) - 1; i >= 0; i-- {
			devices[i].mu.Unlock()
		}
	}
	for _, d := range devices {
		if d.run != nil {
			unlock()
			return StationSummary{}, fmt.Errorf("stop individual simulators before starting a station")
		}
	}
	runtime, err := simulator.StartStation(context.Background(), doc.system, simulator.StationOptions{Models: models, Observe: func(input simulator.InputReading) {
		message := fmt.Sprintf("%s/%s/%s ← %s · %s", input.Receiver, input.AP, input.Address, input.Value, input.Source)
		if input.Error != "" {
			message += " · " + input.Error
		}
		m.log.add("input."+input.Service, docID, message)
	}})
	if err != nil {
		unlock()
		return StationSummary{}, err
	}
	for _, member := range runtime.Devices() {
		for _, d := range devices {
			if d.name == member.IED && d.ap == member.AP {
				d.run = &simulation{runtime: member.Runtime, address: member.Runtime.Address(), mode: string(member.Runtime.GOOSEMode()), done: member.Runtime.Done()}
				d.station = docID
				d.controlEnabled.Store(controls)
			}
		}
	}
	unlock()
	station := &stationRun{runtime: runtime, devices: devices}
	m.mu.Lock()
	m.stations[docID] = station
	m.mu.Unlock()
	m.log.add("station.start", docID, "Started all engineered IEDs on a shared software bus; SV uses current MU values, unsynchronized")
	return m.stationSummary(docID, station), nil
}

func (m *Manager) StopStation(docID string) error {
	m.stationOps.Lock()
	defer m.stationOps.Unlock()
	m.mu.RLock()
	station := m.stations[docID]
	m.mu.RUnlock()
	if station == nil {
		return ErrNotFound
	}
	station.runtime.Close()
	for _, d := range station.devices {
		d.mu.Lock()
		d.run = nil
		d.station = ""
		d.controlEnabled.Store(false)
		d.mu.Unlock()
	}
	m.mu.Lock()
	delete(m.stations, docID)
	m.mu.Unlock()
	m.log.add("station.stop", docID, "Stopped station and released its bus/receivers")
	return nil
}
func (m *Manager) Stations() []StationSummary {
	m.mu.RLock()
	stations := map[string]*stationRun{}
	for id, s := range m.stations {
		stations[id] = s
	}
	m.mu.RUnlock()
	out := []StationSummary{}
	for id, s := range stations {
		out = append(out, m.stationSummary(id, s))
	}
	return out
}

func (m *Manager) WaitStation(ctx context.Context, docID string) error {
	m.mu.RLock()
	station := m.stations[docID]
	m.mu.RUnlock()
	if station == nil {
		return ErrNotFound
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-station.runtime.Done():
		return station.runtime.Err()
	}
}
func (m *Manager) stationSummary(id string, s *stationRun) StationSummary {
	out := StationSummary{Model: id, Status: "running", Devices: []DeviceSummary{}, Inputs: []StationInput{}}
	select {
	case <-s.runtime.Done():
		out.Status = "stopped"
	default:
	}
	if err := s.runtime.Err(); err != nil {
		out.Status = "error"
		out.Error = err.Error()
	}
	for _, d := range s.devices {
		out.Devices = append(out.Devices, d.summary())
	}
	for _, input := range s.runtime.Inputs() {
		out.Inputs = append(out.Inputs, StationInput{Receiver: input.Receiver, AP: input.AP, Address: input.Address, Service: input.Service, Source: input.Source, Value: input.Value.String(), ReceivedAt: input.ReceivedAt, Counter: input.Counter, Error: input.Error, Reference: input.Reference, Network: input.Network, State: input.State, Valid: input.Valid, Quality: uint32(input.Quality), HasQuality: input.HasQuality, ValidUntil: input.ValidUntil, LastGoodAt: input.LastGoodAt, SampleSync: input.SampleSync, StateNumber: input.StateNumber, SequenceNumber: input.SequenceNumber})
	}
	return out
}
