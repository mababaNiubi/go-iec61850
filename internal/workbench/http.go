// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/internal/sample"
)

//go:embed web/*
var webFiles embed.FS

// Handler embeds the frontend in the Go binary. Requests are same-origin and
// have bounded bodies/deadlines. HTTP never accepts arbitrary host file paths.
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stations", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, m.Stations()) })
	mux.HandleFunc("POST /api/models/{id}/station", func(w http.ResponseWriter, r *http.Request) {
		v, err := m.StartStation(r.PathValue("id"))
		respond(w, v, err)
	})
	mux.HandleFunc("DELETE /api/models/{id}/station", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, m.StopStation(r.PathValue("id")))
	})
	mux.HandleFunc("GET /api/models/{id}/catalog", func(w http.ResponseWriter, r *http.Request) {
		catalog, err := m.Catalog(r.PathValue("id"), r.URL.Query().Get("ied"), r.URL.Query().Get("ap"))
		respond(w, catalog, err)
	})
	mux.HandleFunc("POST /api/connections/{id}/check", func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Model, IED, AP string }
		if !decode(w, r, &q) {
			return
		}
		result, err := m.CheckRemote(r.Context(), r.PathValue("id"), q.Model, q.IED, q.AP)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /api/connections/{id}/reports", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Domain, Item string
			Options      ied.ReportOptions
		}
		if !decode(w, r, &q) {
			return
		}
		respond(w, map[string]bool{"ok": true}, m.SubscribeRemote(r.Context(), r.PathValue("id"), q.Domain, q.Item, q.Options))
	})
	mux.HandleFunc("DELETE /api/connections/{id}/reports", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, m.StopRemoteReport(r.Context(), r.PathValue("id"), r.URL.Query().Get("domain"), r.URL.Query().Get("item")))
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"documents": m.Documents(), "devices": m.Devices(), "connections": m.Connections(), "events": m.log.snapshot(), "stations": m.Stations()})
	})
	mux.HandleFunc("POST /api/models", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil {
			writeError(w, fmt.Errorf("SCL upload exceeds 16 MiB or cannot be read"))
			return
		}
		name, err := url.QueryUnescape(r.Header.Get("X-File-Name"))
		if err != nil || name == "" {
			name = "Imported.scd"
		}
		if len(name) > 256 {
			name = name[:256]
		}
		doc, err := m.Import(name, raw)
		respond(w, doc, err)
	})
	mux.HandleFunc("POST /api/models/sample", func(w http.ResponseWriter, r *http.Request) {
		doc, err := m.Import("bay.scd", SampleSCL())
		respond(w, doc, err)
	})
	mux.HandleFunc("POST /api/models/station-sample", func(w http.ResponseWriter, r *http.Request) {
		doc, err := m.Import("station.scd", sample.StationSCL())
		respond(w, doc, err)
	})
	mux.HandleFunc("GET /api/models/{id}/inputs", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		doc := m.docs[r.PathValue("id")]
		m.mu.RUnlock()
		if doc == nil {
			writeError(w, ErrNotFound)
			return
		}
		inputs, err := doc.system.GOOSEInputs(r.URL.Query().Get("ied"), r.URL.Query().Get("ap"))
		respond(w, inputs, err)
	})
	mux.HandleFunc("GET /api/models/{id}/source", func(w http.ResponseWriter, r *http.Request) {
		raw, err := m.Source(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Content-Disposition", `attachment; filename="model.scd"`)
		w.Write(raw)
	})
	mux.HandleFunc("POST /api/devices", func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Model, IED, AP string }
		if !decode(w, r, &q) {
			return
		}
		v, e := m.Select(q.Model, q.IED, q.AP)
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := m.Detail(r.Context(), r.PathValue("id"))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/devices/{id}/values", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Reference, FC string
			Value         json.RawMessage
		}
		if !decode(w, r, &q) {
			return
		}
		respond(w, map[string]bool{"ok": true}, m.Edit(r.Context(), r.PathValue("id"), q.Reference, q.FC, q.Value))
	})
	mux.HandleFunc("POST /api/devices/{id}/simulation", func(w http.ResponseWriter, r *http.Request) {
		var q SimulationOptions
		if !decode(w, r, &q) {
			return
		}
		v, e := m.Start(r.PathValue("id"), q)
		respond(w, v, e)
	})
	mux.HandleFunc("DELETE /api/devices/{id}/simulation", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, m.Stop(r.PathValue("id")))
	})
	mux.HandleFunc("POST /api/devices/{id}/interlock", func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Blocked *bool }
		if !decode(w, r, &q) {
			return
		}
		if q.Blocked == nil {
			writeError(w, fmt.Errorf("Blocked is required"))
			return
		}
		respond(w, map[string]bool{"ok": true}, m.Interlock(r.PathValue("id"), *q.Blocked))
	})
	mux.HandleFunc("POST /api/devices/{id}/operate", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Reference string
			Value     *bool
		}
		if !decode(w, r, &q) {
			return
		}
		// A missing boolean is different from an explicit open command. Reject
		// incomplete requests before any MMS association or control is created.
		if q.Value == nil {
			writeError(w, fmt.Errorf("Value is required"))
			return
		}
		respond(w, map[string]bool{"ok": true}, m.Operate(r.Context(), r.PathValue("id"), q.Reference, *q.Value))
	})
	mux.HandleFunc("GET /api/interfaces", func(w http.ResponseWriter, r *http.Request) { v, e := ethernet.Interfaces(); respond(w, v, e) })
	mux.HandleFunc("POST /api/connections", func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Address, Device string }
		if !decode(w, r, &q) {
			return
		}
		v, e := m.Connect(r.Context(), q.Address, q.Device)
		respond(w, v, e)
	})
	mux.HandleFunc("DELETE /api/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"ok": true}, m.Disconnect(r.PathValue("id")))
	})
	mux.HandleFunc("GET /api/connections/{id}/domains", func(w http.ResponseWriter, r *http.Request) {
		v, e := m.Domains(r.Context(), r.PathValue("id"))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/connections/{id}/names", func(w http.ResponseWriter, r *http.Request) {
		v, e := m.Names(r.Context(), r.PathValue("id"), r.URL.Query().Get("domain"))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/connections/{id}/value", func(w http.ResponseWriter, r *http.Request) {
		v, e := m.ReadRemote(r.Context(), r.PathValue("id"), r.URL.Query().Get("domain"), r.URL.Query().Get("item"))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /api/connections/{id}/value", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Domain, Item string
			Value        json.RawMessage
		}
		if !decode(w, r, &q) {
			return
		}
		respond(w, map[string]bool{"ok": true}, m.WriteRemote(r.Context(), r.PathValue("id"), q.Domain, q.Item, q.Value))
	})
	mux.HandleFunc("GET /api/events", m.eventStream)
	files, _ := fs.Sub(webFiles, "web")
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, ErrNotFound)
	})
	mux.Handle("/", http.FileServer(http.FS(files)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != r.Host || !(u.Scheme == "http" || u.Scheme == "https") {
					writeError(w, fmt.Errorf("cross-origin requests are denied"))
					return
				}
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/events" {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
		mux.ServeHTTP(w, r)
	})
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeError(w, err)
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeError(w, fmt.Errorf("expected one JSON object"))
		return false
	}
	return true
}
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, ErrNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, ErrConflict) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func (m *Manager) eventStream(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, unsubscribe := m.log.subscribe()
	defer unsubscribe()
	fmt.Fprint(w, ": connected\n\n")
	f.Flush()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	rc := http.NewResponseController(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}
			raw, err := json.Marshal(event)
			if err != nil {
				return
			}
			rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, raw); err != nil {
				return
			}
			f.Flush()
		case <-tick.C:
			rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			f.Flush()
		}
	}
}
