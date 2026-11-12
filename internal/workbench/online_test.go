// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/internal/sample"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func apiCall(t *testing.T, handler http.Handler, method, path string, body any, out any) {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	if out != nil {
		if err = json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkbenchDiscoversRemoteWithoutEngineeringModel(t *testing.T) {
	m := New()
	defer m.Close()
	doc, err := m.Import("station.scd", sample.StationSCL())
	if err != nil {
		t.Fatal(err)
	}
	device, err := m.Select(doc.ID, "BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := m.Start(device.ID, SimulationOptions{Listen: "127.0.0.1:0", GOOSE: "off", Controls: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := m.Connect(ctx, runtime.Address, "")
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/connections/" + connection.ID
	handler := m.Handler()
	var directory ied.Directory
	apiCall(t, handler, "GET", base+"/discover", nil, &directory)
	if len(directory.Reports) != 2 || len(directory.DataSets) != 2 {
		t.Fatal(directory)
	}
	var project scl.Project
	apiCall(t, handler, "GET", "/api/models/"+doc.ID+"/project", nil, &project)
	if len(project.Networks) != 2 {
		t.Fatal(project)
	}
	var detail Detail
	apiCall(t, handler, "GET", "/api/devices/"+device.ID, nil, &detail)
	if len(detail.Inputs) != 6 {
		t.Fatalf("selected device included another IED's inputs: %+v", detail.Inputs)
	}
	for _, input := range detail.Inputs {
		if input.Service != "SMV" {
			t.Fatal(input)
		}
	}
	var result ControlReply
	apiCall(t, handler, "POST", base+"/controls", map[string]any{"Reference": "BayIEDCTRL/XCBR1.Pos", "Action": "operate", "Value": true}, &result)
	if result.Error != "" || !result.Result.Acknowledged {
		t.Fatal(result)
	}
	apiCall(t, handler, "POST", base+"/reports", map[string]any{"Domain": "BayIEDCTRL", "Item": "LLN0$BR$history01", "Options": ied.ReportOptions{GI: true}}, nil)
	apiCall(t, handler, "POST", base+"/snapshot", map[string]string{"Domain": "BayIEDCTRL", "Item": "LLN0$BR$history01"}, nil)
	for {
		var page ReportPage
		apiCall(t, handler, "GET", base+"/report-history", nil, &page)
		confirmed, event := false, false
		for _, record := range page.Records {
			confirmed = confirmed || record.Source == "confirmed-read"
			event = event || record.Source == "buffered-report"
			if record.Source == "confirmed-read" && record.EntryID != "" {
				t.Fatal("snapshot advanced history cursor", record)
			}
			if len(record.Values) < 3 {
				t.Fatal("structured FCD fields missing", record)
			}
		}
		if confirmed && event {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("report page incomplete", page)
		case <-time.After(5 * time.Millisecond):
		}
	}
	apiCall(t, handler, "DELETE", base+"/reports?domain=BayIEDCTRL&item=LLN0%24BR%24history01", nil, nil)
}
