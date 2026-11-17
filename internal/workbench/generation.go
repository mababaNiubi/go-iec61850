// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

// Generators animate process values locally, through the same acquisition path
// as a manual edit. They are deliberately independent of the transport: values
// keep updating whether or not an MMS server is listening, so reports and
// GOOSE pick the changes up whenever a simulator runs.
const (
	generatorMinIntervalMS = 100
	generatorMaxIntervalMS = 3600000
	generatorLimit         = 64
	sequenceLimit          = 4096
)

type GeneratorConfig struct {
	Reference   string            `json:"Reference"`
	FC          string            `json:"FC"`   // ST or MX
	Mode        string            `json:"Mode"` // random, ramp, sine, sequence
	IntervalMS  int               `json:"IntervalMS"`
	Paused      bool              `json:"Paused"`
	Min         *float64          `json:"Min,omitempty"` // random range, ramp bound
	Max         *float64          `json:"Max,omitempty"`
	Step        *float64          `json:"Step,omitempty"` // ramp increment, negative counts down
	Wrap        bool              `json:"Wrap,omitempty"` // ramp wraps at the bound instead of clamping
	Base        *float64          `json:"Base,omitempty"` // sine offset
	Amplitude   *float64          `json:"Amplitude,omitempty"`
	FrequencyHz *float64          `json:"FrequencyHz,omitempty"`
	PhaseDeg    *float64          `json:"PhaseDeg,omitempty"`
	Values      []json.RawMessage `json:"Values,omitempty"`    // inline sequence
	Loop        bool              `json:"Loop,omitempty"`      // sequence restarts at the end
	Recording   string            `json:"Recording,omitempty"` // sequence from an imported recording
	// Channel and Decimation keep zero values in JSON: channel 0 is a valid
	// playback source.
	Channel    int `json:"Channel"`
	Decimation int `json:"Decimation"`
}
type GeneratorStatus struct {
	GeneratorConfig
	Running       bool   `json:"Running"`
	Last          string `json:"Last,omitempty"`
	SequenceIndex int    `json:"SequenceIndex"`
}

// generatorSet owns the per-device generators. Its mutex is independent of
// device.mu (lifecycle only) so a tick never blocks or is blocked by start,
// stop and control callbacks.
type generatorSet struct {
	mu   sync.Mutex
	id   string
	ied  *scl.IED
	log  *journal
	list map[mms.ObjectName]*generator
}

type generator struct {
	cfg  GeneratorConfig
	name mms.ObjectName
	typ  mms.TypeSpec
	seq  []mms.Value // sequence mode, parsed once at configuration time

	cancel context.CancelFunc
	done   chan struct{}
	paused atomic.Bool
	last   atomic.Value // string display of the last applied value
	seqIdx atomic.Int64

	// Loop-owned animation state. ticks counts applied intervals so sine stays
	// deterministic even after a pause.
	state float64
	ticks int64
	idx   int
}

func newGeneratorSet(id string, ied *scl.IED, log *journal) *generatorSet {
	return &generatorSet{id: id, ied: ied, log: log, list: map[mms.ObjectName]*generator{}}
}

func (s *generatorSet) snapshot() []GeneratorStatus {
	if s == nil {
		return []GeneratorStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]GeneratorStatus, 0, len(s.list))
	for _, g := range s.list {
		out = append(out, g.status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reference < out[j].Reference })
	return out
}

// modes reports which points are animated, for the data model table badges.
func (s *generatorSet) modes() map[mms.ObjectName]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[mms.ObjectName]string, len(s.list))
	for name, g := range s.list {
		out[name] = g.cfg.Mode
	}
	return out
}

func (s *generatorSet) active() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, g := range s.list {
		if g.alive() {
			n++
		}
	}
	return n
}

// set starts a generator, replacing any previous one on the same point. A POST
// that only toggles Paused keeps the running animation state.
func (s *generatorSet) set(cfg GeneratorConfig, name mms.ObjectName, typ mms.TypeSpec, seq []mms.Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.list[name]; old != nil {
		restart := old.cfg
		restart.Paused = cfg.Paused
		if reflect.DeepEqual(restart, cfg) && old.alive() {
			old.cfg.Paused = cfg.Paused
			old.paused.Store(cfg.Paused)
			return nil
		}
		s.stopLocked(name)
	}
	if len(s.list) >= generatorLimit {
		return fmt.Errorf("generator limit is %d per IED", generatorLimit)
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &generator{cfg: cfg, name: name, typ: typ, seq: seq, cancel: cancel, done: make(chan struct{})}
	if cfg.Mode == "ramp" {
		g.state = 0
		if v, err := s.ied.Read(ctx, mms.Access(name)); err == nil {
			if f, err := v.Float(); err == nil {
				g.state = f
			}
		}
	}
	g.paused.Store(cfg.Paused)
	s.list[name] = g
	go g.run(ctx, s)
	return nil
}

func (s *generatorSet) remove(name mms.ObjectName) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.list[name] == nil {
		return false
	}
	s.stopLocked(name)
	return true
}

func (s *generatorSet) stopAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name := range s.list {
		s.stopLocked(name)
	}
}

// removeRecording stops playback that still references a deleted capture.
func (s *generatorSet) removeRecording(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, g := range s.list {
		if g.cfg.Recording == id {
			s.stopLocked(name)
		}
	}
}

func (s *generatorSet) stopLocked(name mms.ObjectName) {
	if g := s.list[name]; g != nil {
		g.cancel()
		<-g.done
		delete(s.list, name)
	}
}

func (g *generator) alive() bool {
	select {
	case <-g.done:
		return false
	default:
		return true
	}
}

func (g *generator) status() GeneratorStatus {
	st := GeneratorStatus{GeneratorConfig: g.cfg, SequenceIndex: int(g.seqIdx.Load())}
	st.Last, _ = g.last.Load().(string)
	st.Running = g.alive() && !g.paused.Load()
	return st
}

func (g *generator) run(ctx context.Context, s *generatorSet) {
	defer close(g.done)
	interval := time.Duration(g.cfg.IntervalMS) * time.Millisecond
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if g.paused.Load() {
				continue
			}
			v, finished := g.advance(interval)
			if finished {
				s.log.add("simulation.generator", s.id, fmt.Sprintf("%s sequence complete · last value held", g.cfg.Reference))
				return
			}
			if err := updateFeedback(ctx, s.ied, g.name, v); err != nil {
				if ctx.Err() == nil {
					s.log.add("simulation.generator", s.id, fmt.Sprintf("%s stopped after error: %v", g.cfg.Reference, err))
				}
				return
			}
			g.last.Store(v.String())
		}
	}
}

// advance computes the next value. It reports finished when a non-looping
// sequence is exhausted; the final value is applied before that happens.
func (g *generator) advance(interval time.Duration) (mms.Value, bool) {
	switch g.cfg.Mode {
	case "random":
		return numericValue(g.typ, *g.cfg.Min+rand.Float64()*(*g.cfg.Max-*g.cfg.Min)), false
	case "ramp":
		g.state += *g.cfg.Step
		if g.cfg.Max != nil && g.state > *g.cfg.Max {
			if g.cfg.Wrap && g.cfg.Min != nil {
				g.state = *g.cfg.Min
			} else {
				g.state = *g.cfg.Max
			}
		}
		if g.cfg.Min != nil && g.state < *g.cfg.Min {
			if g.cfg.Wrap && g.cfg.Max != nil {
				g.state = *g.cfg.Max
			} else {
				g.state = *g.cfg.Min
			}
		}
		return numericValue(g.typ, g.state), false
	case "sine":
		g.ticks++
		phase := 0.0
		if g.cfg.PhaseDeg != nil {
			phase = *g.cfg.PhaseDeg * math.Pi / 180
		}
		t := float64(g.ticks) * interval.Seconds()
		return numericValue(g.typ, *g.cfg.Base+*g.cfg.Amplitude*math.Sin(2*math.Pi**g.cfg.FrequencyHz*t+phase)), false
	case "sequence":
		if g.idx >= len(g.seq) {
			return mms.Value{}, true
		}
		v := g.seq[g.idx]
		g.idx++
		g.seqIdx.Store(int64(g.idx))
		if g.idx >= len(g.seq) {
			if g.cfg.Loop {
				g.idx = 0
				g.seqIdx.Store(0)
			}
		}
		return v, false
	}
	return mms.Value{}, true
}

// numericValue adapts a computed sample to the point's declared type. Integer
// points round; unsigned points clamp negatives so a ramp never underflows.
func numericValue(t mms.TypeSpec, x float64) mms.Value {
	switch t.Kind {
	case mms.KindInteger:
		return mms.Integer(int64(math.Round(x)))
	case mms.KindUnsigned:
		if x < 0 {
			x = 0
		}
		if x > math.MaxInt64 {
			x = math.MaxInt64
		}
		return mms.Unsigned(uint64(math.Round(x)))
	case mms.KindFloat:
		if t.Width == 32 {
			return mms.Float32(float32(x))
		}
		return mms.Float64(x)
	}
	return mms.Value{}
}

func validateGenerator(ctx context.Context, ied *scl.IED, cfg GeneratorConfig) (mms.ObjectName, mms.TypeSpec, error) {
	bad := func(format string, args ...any) (mms.ObjectName, mms.TypeSpec, error) {
		return mms.ObjectName{}, mms.TypeSpec{}, fmt.Errorf(format, args...)
	}
	switch cfg.FC {
	case "ST", "MX":
	default:
		return bad("FC must be ST or MX, got %q", cfg.FC)
	}
	switch cfg.Mode {
	case "random", "ramp", "sine", "sequence":
	default:
		return bad("Mode must be random, ramp, sine or sequence, got %q", cfg.Mode)
	}
	if cfg.IntervalMS < generatorMinIntervalMS || cfg.IntervalMS > generatorMaxIntervalMS {
		return bad("IntervalMS must be between %d and %d", generatorMinIntervalMS, generatorMaxIntervalMS)
	}
	name, err := ied.Resolve(cfg.Reference, cfg.FC)
	if err != nil {
		return bad("%v", err)
	}
	typ, err := ied.Type(ctx, name)
	if err != nil {
		return bad("%v", err)
	}
	numeric := typ.Kind == mms.KindFloat || typ.Kind == mms.KindInteger || typ.Kind == mms.KindUnsigned
	switch cfg.Mode {
	case "random":
		if !numeric {
			return bad("random needs a FLOAT, INT32 or INT64 point")
		}
		if cfg.Min == nil || cfg.Max == nil {
			return bad("random needs Min and Max")
		}
		if *cfg.Min > *cfg.Max {
			return bad("random Min must not exceed Max")
		}
	case "ramp":
		if !numeric {
			return bad("ramp needs a FLOAT, INT32 or INT64 point")
		}
		if cfg.Step == nil || *cfg.Step == 0 {
			return bad("ramp needs a non-zero Step")
		}
		if cfg.Wrap && (cfg.Min == nil || cfg.Max == nil) {
			return bad("ramp Wrap needs both Min and Max")
		}
		if cfg.Min != nil && cfg.Max != nil && *cfg.Min > *cfg.Max {
			return bad("ramp Min must not exceed Max")
		}
	case "sine":
		if !numeric {
			return bad("sine needs a FLOAT, INT32 or INT64 point")
		}
		if cfg.Base == nil || cfg.Amplitude == nil || cfg.FrequencyHz == nil {
			return bad("sine needs Base, Amplitude and FrequencyHz")
		}
		if *cfg.Amplitude <= 0 || *cfg.FrequencyHz <= 0 {
			return bad("sine Amplitude and FrequencyHz must be positive")
		}
	}
	return name, typ, nil
}

// sequenceValues materialises playback values at configuration time so a tick
// never parses and a bad entry fails the POST with its index.
func sequenceValues(typ mms.TypeSpec, cfg GeneratorConfig, lookup func(string) (*recording, bool)) ([]mms.Value, error) {
	if cfg.Recording != "" {
		if len(cfg.Values) > 0 {
			return nil, fmt.Errorf("use either Values or Recording, not both")
		}
		rec, ok := lookup(cfg.Recording)
		if !ok {
			return nil, fmt.Errorf("unknown recording %q", cfg.Recording)
		}
		return rec.sequence(typ, cfg)
	}
	if len(cfg.Values) == 0 {
		return nil, fmt.Errorf("sequence needs Values or Recording")
	}
	if len(cfg.Values) > sequenceLimit {
		return nil, fmt.Errorf("sequence exceeds %d values", sequenceLimit)
	}
	out := make([]mms.Value, len(cfg.Values))
	for i, raw := range cfg.Values {
		v, err := ParseValue(typ, raw)
		if err != nil {
			return nil, fmt.Errorf("Values[%d]: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}

func (m *Manager) Generators(id string) ([]GeneratorStatus, error) {
	d, err := m.get(id)
	if err != nil {
		return nil, err
	}
	return d.gens.snapshot(), nil
}

func (m *Manager) SetGenerator(ctx context.Context, id string, cfg GeneratorConfig) ([]GeneratorStatus, error) {
	d, err := m.get(id)
	if err != nil {
		return nil, err
	}
	name, typ, err := validateGenerator(ctx, d.ied, cfg)
	if err != nil {
		return nil, err
	}
	var seq []mms.Value
	if cfg.Mode == "sequence" {
		if seq, err = sequenceValues(typ, cfg, m.lookupRecording); err != nil {
			return nil, err
		}
	}
	if err = d.gens.set(cfg, name, typ, seq); err != nil {
		return nil, err
	}
	state := "started"
	if cfg.Paused {
		state = "paused"
	}
	m.log.add("simulation.generator", id, fmt.Sprintf("%s [%s] %s every %dms · %s", cfg.Reference, cfg.FC, cfg.Mode, cfg.IntervalMS, state))
	return d.gens.snapshot(), nil
}

// RemoveGenerator stops one animation. The acquisition value stays wherever
// the generator left it.
func (m *Manager) RemoveGenerator(id, ref, fc string) error {
	d, err := m.get(id)
	if err != nil {
		return err
	}
	name, err := d.ied.Resolve(ref, fc)
	if err != nil {
		return err
	}
	if !d.gens.remove(name) {
		return ErrNotFound
	}
	m.log.add("simulation.generator", id, fmt.Sprintf("%s [%s] removed", ref, fc))
	return nil
}

func (m *Manager) RemoveAllGenerators(id string) error {
	d, err := m.get(id)
	if err != nil {
		return err
	}
	d.gens.stopAll()
	m.log.add("simulation.generator", id, "all generators removed")
	return nil
}
