// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"fmt"
	"sort"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

// Imported captures live in the workspace only: nothing is written to disk.
const (
	recordingLimit    = 8
	recordingCfgLimit = 4 << 20
	recordingDatLimit = 60 << 20
)

// Recording is the browser-facing projection of an imported capture.
type Recording struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Format   string             `json:"format"` // ASCII, BINARY, BINARY32, FLOAT32
	RateHz   float64            `json:"rateHz"`
	Samples  int                `json:"samples"`
	Start    time.Time          `json:"start"`
	End      time.Time          `json:"end"`
	Channels []RecordingChannel `json:"channels"`
}

// RecordingChannel describes one captured channel. Digital channels report
// their normal state through the phase field.
type RecordingChannel struct {
	Index   int     `json:"index"`
	ID      string  `json:"id"`
	Phase   string  `json:"phase"`
	Unit    string  `json:"unit,omitempty"`
	CCBM    string  `json:"ccbm,omitempty"`
	Digital bool    `json:"digital"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
}

// recording is one imported COMTRADE capture. Channel samples stay internal
// and are materialised into MMS values only when a playback generator is
// configured.
type recording struct {
	Recording
	analog     [][]float32
	digital    [][]bool
	lastMicros float64
}

// sequence materialises one channel as playback values for the target point.
// Analog channels adapt to any numeric point; digital channels map onto
// boolean, integer and Dbpos status points.
func (r *recording) sequence(typ mms.TypeSpec, cfg GeneratorConfig) ([]mms.Value, error) {
	if cfg.Channel < 0 || cfg.Channel >= len(r.Channels) {
		return nil, fmt.Errorf("Channel %d is outside the recording", cfg.Channel)
	}
	dec := cfg.Decimation
	if dec < 1 {
		dec = 1
	}
	ch := r.Channels[cfg.Channel]
	// The combined channel list keeps definition order; locate the sample
	// series behind the requested index.
	ai, di := -1, -1
	var analog []float32
	var digital []bool
	for i, c := range r.Channels {
		if c.Digital {
			di++
		} else {
			ai++
		}
		if i == cfg.Channel {
			if c.Digital {
				digital = r.digital[di]
			} else {
				analog = r.analog[ai]
			}
			break
		}
	}
	out := []mms.Value{}
	if ch.Digital {
		for n := 0; n < len(digital); n += dec {
			var v mms.Value
			var err error
			on := digital[n]
			switch {
			case typ.Kind == mms.KindBoolean:
				v = mms.Boolean(on)
			case typ.Kind == mms.KindInteger:
				v = mms.Integer(int64(boolToInt(on)))
			case typ.Kind == mms.KindUnsigned:
				v = mms.Unsigned(uint64(boolToInt(on)))
			case typ.Kind == mms.KindBitString && typ.Length == 2:
				position := scl.Open
				if on {
					position = scl.Closed
				}
				if v, err = position.MMSValue(); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("digital channel %q needs a BOOL, INT32/INT64 or Dbpos point", ch.ID)
			}
			out = append(out, v)
		}
		return out, nil
	}
	numeric := typ.Kind == mms.KindFloat || typ.Kind == mms.KindInteger || typ.Kind == mms.KindUnsigned
	if !numeric {
		return nil, fmt.Errorf("analog channel %q needs a FLOAT, INT32 or INT64 point", ch.ID)
	}
	for n := 0; n < len(analog); n += dec {
		out = append(out, numericValue(typ, float64(analog[n])))
	}
	return out, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (m *Manager) lookupRecording(id string) (*recording, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.recordings[id]
	return r, r != nil
}

// ImportRecording parses before storing, so a bad pair never replaces a
// working capture. Equal content reuses the same recording.
func (m *Manager) ImportRecording(name string, cfg, dat []byte) (Recording, error) {
	if len(cfg) > recordingCfgLimit {
		return Recording{}, fmt.Errorf("cfg exceeds 4 MiB")
	}
	if len(dat) > recordingDatLimit {
		return Recording{}, fmt.Errorf("dat exceeds 60 MiB")
	}
	if name == "" {
		name = "recording"
	}
	if len(name) > 256 {
		name = name[:256]
	}
	rec, err := parseComtrade(cfg, dat, name)
	if err != nil {
		return Recording{}, err
	}
	id := digest(append(append([]byte{}, cfg...), dat...))
	rec.ID = id
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Recording{}, ErrConflict
	}
	if old := m.recordings[id]; old != nil {
		m.mu.Unlock()
		return old.Recording, nil
	}
	if len(m.recordings) >= recordingLimit {
		m.mu.Unlock()
		return Recording{}, fmt.Errorf("workspace limit is %d recordings", recordingLimit)
	}
	m.recordings[id] = rec
	m.mu.Unlock()
	m.log.add("recording.import", "", fmt.Sprintf("Imported %s · %s · %d channels · %d samples", name, rec.Format, len(rec.Channels), rec.Samples))
	return rec.Recording, nil
}

func (m *Manager) Recordings() []Recording {
	m.mu.RLock()
	list := make([]Recording, 0, len(m.recordings))
	for _, r := range m.recordings {
		list = append(list, r.Recording)
	}
	m.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func (m *Manager) Recording(id string) (Recording, error) {
	r, ok := m.lookupRecording(id)
	if !ok {
		return Recording{}, ErrNotFound
	}
	return r.Recording, nil
}

// RemoveRecording stops any playback still reading the capture first, so a
// running generator never references freed samples.
func (m *Manager) RemoveRecording(id string) error {
	rec, ok := m.lookupRecording(id)
	if !ok {
		return ErrNotFound
	}
	m.mu.RLock()
	devices := make([]*device, 0, len(m.devices))
	for _, d := range m.devices {
		devices = append(devices, d)
	}
	m.mu.RUnlock()
	for _, d := range devices {
		d.gens.removeRecording(id)
	}
	m.mu.Lock()
	delete(m.recordings, id)
	m.mu.Unlock()
	m.log.add("recording.remove", "", fmt.Sprintf("Removed %s · %s", rec.Name, rec.ID[:12]))
	return nil
}
