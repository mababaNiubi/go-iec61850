// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func ptr(v float64) *float64 { return &v }

// waitValue polls the data model until cond passes, so tests tolerate tick
// timing without sleeping fixed amounts.
func waitValue(t *testing.T, m *Manager, id, ref string, cond func(float64) bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var last any
	for time.Now().Before(deadline) {
		detail, err := m.Detail(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range detail.Points {
			if p.Reference == ref {
				last = p.Value
				if v, ok := p.Value.(float64); ok && cond(v) {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never met the condition, last value %v", ref, last)
}

func readPoint(t *testing.T, m *Manager, id, ref string) any {
	t.Helper()
	detail, err := m.Detail(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range detail.Points {
		if p.Reference == ref {
			return p.Value
		}
	}
	t.Fatalf("point %s missing", ref)
	return nil
}

const measure = "BayIEDCTRL/MMXU1.TotW.mag.f"

func TestGeneratorRandomWithinRange(t *testing.T) {
	m, id := testDevice(t)
	if err := m.Edit(context.Background(), id, measure, "MX", []byte("0")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetGenerator(context.Background(), id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "random", IntervalMS: 100, Min: ptr(100), Max: ptr(200),
	}); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return v >= 100 && v <= 200 })
}

func TestGeneratorRampCountsUpAndWraps(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if err := m.Edit(ctx, id, measure, "MX", []byte("0")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "ramp", IntervalMS: 100, Step: ptr(25), Min: ptr(0), Max: ptr(100), Wrap: true,
	}); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return v >= 25 })
	// Only the wrap can bring the ramp back to its lower bound.
	waitValue(t, m, id, measure, func(v float64) bool { return v == 0 })
}

func TestGeneratorRampCountsDownAndClamps(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if err := m.Edit(ctx, id, measure, "MX", []byte("100")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "ramp", IntervalMS: 100, Step: ptr(-30), Min: ptr(0),
	}); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return v < 100 })
	waitValue(t, m, id, measure, func(v float64) bool { return v == 0 })
}

func TestGeneratorSineWithinBand(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if err := m.Edit(ctx, id, measure, "MX", []byte("999")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "sine", IntervalMS: 100, Base: ptr(10), Amplitude: ptr(5), FrequencyHz: ptr(1),
	}); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return v <= 15 })
}

func TestGeneratorSequenceLoopsOverAllValues(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if err := m.Edit(ctx, id, measure, "MX", []byte("0")); err != nil {
		t.Fatal(err)
	}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "sequence", IntervalMS: 100, Loop: true,
		Values: []json.RawMessage{raw("1"), raw("2"), raw("3")},
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[float64]bool{}
	deadline := time.Now().Add(4 * time.Second)
	for len(seen) < 3 && time.Now().Before(deadline) {
		if v, ok := readPoint(t, m, id, measure).(float64); ok {
			seen[v] = true
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(seen) < 3 {
		t.Fatalf("loop did not replay every value, saw %v", seen)
	}
}

func TestGeneratorSequenceStopsAtEndAndHoldsLastValue(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "sequence", IntervalMS: 100,
		Values: []json.RawMessage{raw("7"), raw("9")},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		list, err := m.Generators(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("expected the finished generator to stay listed, got %d", len(list))
		}
		if !list[0].Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v := readPoint(t, m, id, measure); v != float64(9) {
		t.Fatalf("expected the last sequence value to be held, got %v", v)
	}
}

// The DPC status feedback proves sequences are not restricted to numeric
// points: Pos.stVal is a two-bit Dbpos string.
func TestGeneratorSequenceOnStatusPoint(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: "BayIEDCTRL/XCBR1.Pos.stVal", FC: "ST", Mode: "sequence", IntervalMS: 100, Loop: true,
		Values: []json.RawMessage{raw(`"01"`), raw(`"10"`)},
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	deadline := time.Now().Add(4 * time.Second)
	for len(seen) < 2 && time.Now().Before(deadline) {
		if v, ok := readPoint(t, m, id, "BayIEDCTRL/XCBR1.Pos.stVal").(string); ok {
			seen[v] = true
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(seen) < 2 {
		t.Fatal("status sequence never alternated between open and closed")
	}
}

func TestGeneratorPauseFreezesAndResumeContinues(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	cfg := GeneratorConfig{Reference: measure, FC: "MX", Mode: "random", IntervalMS: 100, Min: ptr(0), Max: ptr(1)}
	if _, err := m.SetGenerator(ctx, id, cfg); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return true })
	cfg.Paused = true
	if _, err := m.SetGenerator(ctx, id, cfg); err != nil {
		t.Fatal(err)
	}
	frozen := readPoint(t, m, id, measure)
	time.Sleep(300 * time.Millisecond)
	if v := readPoint(t, m, id, measure); v != frozen {
		t.Fatalf("paused generator kept moving: %v then %v", frozen, v)
	}
	cfg.Paused = false
	if _, err := m.SetGenerator(ctx, id, cfg); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if readPoint(t, m, id, measure) != frozen {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("resumed generator did not move the value")
}

func TestGeneratorReplaceAndRemove(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "random", IntervalMS: 100, Min: ptr(0), Max: ptr(1),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "sine", IntervalMS: 100, Base: ptr(0), Amplitude: ptr(1), FrequencyHz: ptr(1),
	}); err != nil {
		t.Fatal(err)
	}
	list, err := m.Generators(id)
	if err != nil || len(list) != 1 || list[0].Mode != "sine" {
		t.Fatal(list, err)
	}
	if err = m.RemoveGenerator(id, measure, "MX"); err != nil {
		t.Fatal(err)
	}
	if err = m.RemoveGenerator(id, measure, "MX"); err == nil {
		t.Fatal("removing twice should not succeed")
	}
	if _, err = m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "random", IntervalMS: 100, Min: ptr(0), Max: ptr(1),
	}); err != nil {
		t.Fatal(err)
	}
	if err = m.RemoveAllGenerators(id); err != nil {
		t.Fatal(err)
	}
	if list, _ = m.Generators(id); len(list) != 0 {
		t.Fatalf("expected no generators left, got %d", len(list))
	}
}

// Generators run without any MMS listener: they only animate the local store.
func TestGeneratorRunsWhileSimulatorStopped(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if err := m.Edit(ctx, id, measure, "MX", []byte("0")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "ramp", IntervalMS: 100, Step: ptr(1),
	}); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return v > 0 })
	if _, err := m.Start(id, SimulationOptions{Listen: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return v > 5 })
	if err := m.Stop(id); err != nil {
		t.Fatal(err)
	}
	waitValue(t, m, id, measure, func(v float64) bool { return v > 10 })
}

func TestNumericValueAdaptsToDeclaredType(t *testing.T) {
	if v, _ := numericValue(mms.TypeSpec{Kind: mms.KindInteger}, 1.6).Int(); v != 2 {
		t.Fatal("expected rounding to 2")
	}
	if v, _ := numericValue(mms.TypeSpec{Kind: mms.KindInteger}, -1.6).Int(); v != -2 {
		t.Fatal("expected rounding to -2")
	}
	if v, _ := numericValue(mms.TypeSpec{Kind: mms.KindUnsigned}, -5).Uint(); v != 0 {
		t.Fatal("expected negatives clamped to 0")
	}
	if v, _ := numericValue(mms.TypeSpec{Kind: mms.KindUnsigned}, 3.2).Uint(); v != 3 {
		t.Fatal("expected 3")
	}
	if v, _ := numericValue(mms.TypeSpec{Kind: mms.KindFloat, Width: 64}, 0.5).Float(); v != 0.5 {
		t.Fatal("expected 0.5")
	}
	overflow, err := numericValue(mms.TypeSpec{Kind: mms.KindFloat, Width: 32}, 1e40).Float()
	if err != nil || !math.IsInf(overflow, 1) {
		t.Fatal("expected 32-bit overflow to produce +Inf", overflow, err)
	}
}

func TestGeneratorValidation(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	cases := []struct {
		name string
		cfg  GeneratorConfig
		want string
	}{
		{"bad fc", GeneratorConfig{Reference: measure, FC: "SP", Mode: "random", IntervalMS: 100}, "FC"},
		{"bad mode", GeneratorConfig{Reference: measure, FC: "MX", Mode: "pulse", IntervalMS: 100}, "Mode"},
		{"fast interval", GeneratorConfig{Reference: measure, FC: "MX", Mode: "random", IntervalMS: 10, Min: ptr(0), Max: ptr(1)}, "IntervalMS"},
		{"random range", GeneratorConfig{Reference: measure, FC: "MX", Mode: "random", IntervalMS: 100, Min: ptr(5), Max: ptr(1)}, "Min"},
		{"ramp step", GeneratorConfig{Reference: measure, FC: "MX", Mode: "ramp", IntervalMS: 100, Step: ptr(0)}, "Step"},
		{"ramp wrap bounds", GeneratorConfig{Reference: measure, FC: "MX", Mode: "ramp", IntervalMS: 100, Step: ptr(1), Wrap: true, Max: ptr(9)}, "Wrap"},
		{"sine fields", GeneratorConfig{Reference: measure, FC: "MX", Mode: "sine", IntervalMS: 100, Base: ptr(0)}, "sine"},
		{"sequence empty", GeneratorConfig{Reference: measure, FC: "MX", Mode: "sequence", IntervalMS: 100}, "sequence"},
		{"unknown point", GeneratorConfig{Reference: "BayIEDCTRL/MMXU9.TotW.mag.f", FC: "MX", Mode: "random", IntervalMS: 100, Min: ptr(0), Max: ptr(1)}, ""},
	}
	for _, c := range cases {
		_, err := m.SetGenerator(ctx, id, c.cfg)
		if err == nil {
			t.Fatalf("%s: expected an error", c.name)
		}
		if c.want != "" && !bytes.Contains([]byte(err.Error()), []byte(c.want)) {
			t.Fatalf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
	// The status feedback is boolean: a numeric generator must refuse it.
	if _, err := m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: "BayIEDCTRL/XCBR1.Pos.stVal", FC: "ST", Mode: "random", IntervalMS: 100, Min: ptr(0), Max: ptr(1),
	}); err == nil {
		t.Fatal("random on a boolean point must fail")
	}
}

func TestGeneratorRoutes(t *testing.T) {
	m, id := testDevice(t)
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	body := fmt.Sprintf(`{"Reference":%q,"FC":"MX","Mode":"sine","IntervalMS":100,"Base":10,"Amplitude":5,"FrequencyHz":1}`, measure)
	res, err := server.Client().Post(server.URL+"/api/devices/"+id+"/generators", "application/json", bytes.NewBufferString(body))
	if err != nil || res.StatusCode != 200 {
		t.Fatal(err, res)
	}
	res.Body.Close()
	// Strict decoding rejects typos before anything starts.
	res, err = server.Client().Post(server.URL+"/api/devices/"+id+"/generators", "application/json", bytes.NewBufferString(`{"Bogus":true}`))
	if err != nil || res.StatusCode != 400 {
		t.Fatal(err, res)
	}
	res.Body.Close()
	req, _ := http.NewRequest("DELETE", server.URL+"/api/devices/"+id+"/generators?reference="+measure+"&fc=MX", nil)
	res, err = server.Client().Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatal(err, res)
	}
	res.Body.Close()
	if list, _ := m.Generators(id); len(list) != 0 {
		t.Fatalf("expected delete to stop the generator, got %d", len(list))
	}
}
