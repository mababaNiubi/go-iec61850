// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func controlEvents(t *testing.T, m *Manager) []Event {
	t.Helper()
	var out []Event
	for _, e := range m.ControlHistory(0) {
		out = append(out, e)
	}
	return out
}

func TestControlHistoryRecordsLocalOperate(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if _, err := m.Start(id, SimulationOptions{Listen: "127.0.0.1:0", Controls: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.Operate(ctx, id, "BayIEDCTRL/XCBR1.Pos.Oper", true); err != nil {
		t.Fatal(err)
	}
	records := controlEvents(t, m)
	var operate, feedback *ControlRecord
	for _, e := range records {
		if e.Control == nil {
			continue
		}
		switch e.Control.Action {
		case "operate":
			operate = e.Control
		case "feedback":
			feedback = e.Control
		}
	}
	if operate == nil || operate.Reference != "BayIEDCTRL/XCBR1.Pos.Oper" || operate.Value != "true" || operate.Result != "" {
		t.Fatalf("missing operate record, got %+v", operate)
	}
	if feedback == nil || feedback.Origin != "IEDLab" || feedback.CtlNum == 0 {
		t.Fatalf("missing feedback record with origin evidence, got %+v", feedback)
	}
}

func TestControlHistoryRecordsDenial(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	if _, err := m.Start(id, SimulationOptions{Listen: "127.0.0.1:0", Controls: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.Interlock(id, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Operate(ctx, id, "BayIEDCTRL/XCBR1.Pos.Oper", true); err == nil {
		t.Fatal("interlocked control must fail")
	}
	for _, e := range controlEvents(t, m) {
		if e.Control != nil && e.Control.Action == "denied" && e.Control.Result != "" {
			return
		}
	}
	t.Fatal("no denied record with a result")
}

func TestControlHistoryRouteFiltersAfterID(t *testing.T) {
	m, id := testDevice(t)
	if err := m.Interlock(id, true); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	res, err := server.Client().Get(server.URL + "/api/controls")
	if err != nil || res.StatusCode != 200 {
		t.Fatal(err, res)
	}
	var events []Event
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[len(events)-1].Control == nil {
		t.Fatal("expected at least one structured control record")
	}
	last := events[len(events)-1].ID
	res, err = server.Client().Get(server.URL + "/api/controls?after=" + strconv.FormatUint(last, 10))
	if err != nil {
		t.Fatal(err)
	}
	var tail []Event
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	json.Unmarshal(body, &tail)
	if len(tail) != 0 {
		t.Fatalf("expected nothing after the newest record, got %d", len(tail))
	}
}

// A write-friendly store: the model itself denies direct ST/MX writes, which
// is exactly the access rule under test elsewhere.
type acceptStore struct{ mms.Store }

func (acceptStore) Read(ctx context.Context, a mms.VariableAccess) (mms.Value, error) {
	return mms.Float64(0), nil
}
func (acceptStore) Write(ctx context.Context, a mms.VariableAccess, v mms.Value) error { return nil }

// A polling client must not flood the bounded journal: reads collapse into one
// summary line per second while writes stay individual.
func TestObservedStoreAggregatesReads(t *testing.T) {
	var log journal
	store := newObservedStore(acceptStore{}, &log, "dev")
	defer store.close()
	ctx := context.Background()
	name := mms.DomainName("BayIEDCTRL", "MMXU1$MX$TotW$mag$f")
	for i := 0; i < 25; i++ {
		if _, err := store.Read(ctx, mms.Access(name)); err != nil {
			t.Fatal(err)
		}
	}
	store.flush()
	events := log.snapshot()
	reads := 0
	for _, e := range events {
		if e.Kind != "mms.read" {
			t.Fatalf("unexpected kind %q", e.Kind)
		}
		reads++
		if !strings.Contains(e.Message, "×25") {
			t.Fatalf("aggregated message missing the count: %q", e.Message)
		}
	}
	if reads != 1 {
		t.Fatalf("expected one aggregated read event, got %d", reads)
	}
	for i := 0; i < 2; i++ {
		if err := store.Write(ctx, mms.Access(name), mms.Float64(1)); err != nil {
			t.Fatal(err)
		}
	}
	kinds := map[string]int{}
	for _, e := range log.snapshot() {
		kinds[e.Kind]++
	}
	if kinds["mms.write"] != 2 {
		t.Fatalf("expected writes to stay individual, got %v", kinds)
	}
}

// The aggregator must terminate with the simulation instead of leaking.
func TestObservedStoreStopsWithRuntime(t *testing.T) {
	m, id := testDevice(t)
	if _, err := m.Start(id, SimulationOptions{Listen: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	d, _ := m.get(id)
	store := d.run.store
	if err := m.Stop(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.stop:
	case <-time.After(time.Second):
		t.Fatal("aggregation loop did not stop with the simulation")
	}
}
