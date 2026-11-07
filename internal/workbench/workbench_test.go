// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func testDevice(t *testing.T) (*Manager, string) {
	t.Helper()
	m := New()
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	doc, err := m.Import("test.scd", SampleSCL())
	if err != nil {
		t.Fatal(err)
	}
	d, err := m.Select(doc.ID, "BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	return m, d.ID
}

func TestSimulatorCrossesMMSAndProtectsFeedback(t *testing.T) {
	m, id := testDevice(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := m.Start(id, SimulationOptions{Listen: "127.0.0.1:0", GOOSE: "memory", Controls: true})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := m.Connect(ctx, s.Address, id)
	if err != nil {
		t.Fatal(err)
	}
	names, err := m.Names(ctx, peer.ID, "BayIEDCTRL")
	if err != nil || len(names) == 0 {
		t.Fatal(names, err)
	}
	if err = m.Edit(ctx, id, "BayIEDCTRL/MMXU1.TotW.mag.f", "MX", []byte("1250.5")); err != nil {
		t.Fatal(err)
	}
	p, err := m.ReadRemote(ctx, peer.ID, "BayIEDCTRL", "MMXU1$MX$TotW$mag$f")
	if err != nil || p.Value != 1250.5 {
		t.Fatal(p, err)
	}
	if err = m.Edit(ctx, id, "BayIEDCTRL/MMXU1.TotW.mag.f", "MX", []byte("true")); err == nil {
		t.Fatal("incorrect measurement type accepted")
	}
	if err = m.Operate(ctx, id, "BayIEDCTRL/XCBR1.Pos.Oper", true); err != nil {
		t.Fatal(err)
	}
	p, err = m.ReadRemote(ctx, peer.ID, "BayIEDCTRL", "XCBR1$ST$Pos$stVal")
	if err != nil || p.Value != "10" {
		t.Fatal(p, err)
	}
	if err = m.WriteRemote(ctx, peer.ID, "BayIEDCTRL", "XCBR1$ST$Pos$stVal", []byte(`"01"`)); !errors.Is(err, mms.ObjectAccessDenied) {
		t.Fatal("remote status write:", err)
	}
	if err = m.Interlock(id, true); err != nil {
		t.Fatal(err)
	}
	if err = m.Operate(ctx, id, "BayIEDCTRL/XCBR1.Pos.Oper", false); !errors.Is(err, mms.ObjectAccessDenied) {
		t.Fatal("interlock:", err)
	}
	if err = m.Interlock(id, false); err != nil {
		t.Fatal(err)
	}
	if err = m.Operate(ctx, id, "BayIEDCTRL/XCBR1.Pos.Oper", false); err != nil {
		t.Fatal(err)
	}
	if err = m.Stop(id); err != nil {
		t.Fatal(err)
	}
	// A stopped IED must release its port and client associations.
	l, err := net.Listen("tcp", s.Address)
	if err != nil {
		t.Fatal("listener leaked:", err)
	}
	l.Close()
	foundGOOSE := false
	for _, e := range m.log.snapshot() {
		if e.Kind == "goose.state-changed" {
			foundGOOSE = true
		}
	}
	if !foundGOOSE {
		t.Fatal("no validated GOOSE feedback was recorded")
	}
}

func TestCloseRacingStartCannotLeakAListener(t *testing.T) {
	m, id := testDevice(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); m.Start(id, SimulationOptions{Listen: "127.0.0.1:0", GOOSE: "memory"}) }()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for _, d := range m.Devices() {
		if d.Status == "running" {
			t.Fatal("simulator outlived its workspace")
		}
	}
}

func TestHTTPImportAndValueErrors(t *testing.T) {
	m := New()
	defer m.Close()
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	post := func(path string, body []byte, origin string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest("POST", server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, raw
	}
	status, raw := post("/api/models", SampleSCL(), "")
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	status, raw = post("/api/devices", []byte(`{"Model":"`+doc.ID+`","IED":"BayIED","AP":"S1"}`), "")
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var d DeviceSummary
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	path := "/api/devices/" + d.ID + "/values"
	status, _ = post(path, []byte(`{"Reference":"BayIEDCTRL/MMXU1.TotW.mag.f","FC":"MX","Value":false}`), "")
	if status != 400 {
		t.Fatal(status)
	}
	status, _ = post(path, []byte(`{"Reference":"BayIEDCTRL/MMXU1.TotW.mag.f","FC":"MX","Value":8}`), "https://different.example")
	if status != 400 {
		t.Fatal("cross-origin request accepted")
	}
	status, _ = post("/api/devices/"+d.ID+"/operate", []byte(`{"Reference":"BayIEDCTRL/XCBR1.Pos.Oper"}`), "")
	if status != 400 {
		t.Fatal("missing control value must be rejected before dispatch", status)
	}
	status, _ = post("/api/devices/"+d.ID+"/interlock", []byte(`{}`), "")
	if status != 400 {
		t.Fatal("missing interlock state accepted", status)
	}
	status, _ = post("/api/models", []byte("<not-scl/>"), "")
	if status != 400 {
		t.Fatal("invalid SCL accepted")
	}
}

func TestJSONValuePrecisionAndStrictness(t *testing.T) {
	spec := mms.TypeSpec{Kind: mms.KindInteger, Width: 64}
	v, err := ParseValue(spec, []byte(`"9223372036854775807"`))
	if err != nil || ValueJSON(v) != "9223372036854775807" {
		t.Fatal(ValueJSON(v), err)
	}
	for _, raw := range []string{`"9223372036854775808"`, "1 2", "true"} {
		if _, err := ParseValue(spec, []byte(raw)); err == nil {
			t.Fatal("invalid integer accepted:", raw)
		}
	}
	q := mms.TypeSpec{Kind: mms.KindBitString, Length: 2}
	if _, err := ParseValue(q, []byte(`"10"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseValue(q, []byte(`"1z"`)); err == nil {
		t.Fatal("invalid bits accepted")
	}
	if _, err := ParseValue(mms.TypeSpec{Kind: mms.KindVisibleString, Length: -2}, []byte(`"long"`)); err == nil {
		t.Fatal("string bound ignored")
	}
}
func TestJournalDropsSlowBrowserInsteadOfBlockingProtocol(t *testing.T) {
	var j journal
	ch, unsubscribe := j.subscribe()
	defer unsubscribe()
	for i := 0; i < 700; i++ {
		j.add("model.value", "test", strings.Repeat("x", 8))
	}
	if len(j.snapshot()) != 500 {
		t.Fatal("journal is unbounded")
	}
	for range ch {
	}
}
