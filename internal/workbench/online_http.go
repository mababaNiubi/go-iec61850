// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
)

func (m *Manager) onlineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/models/{id}/project", func(w http.ResponseWriter, r *http.Request) {
		p, err := m.Project(r.PathValue("id"))
		respond(w, p, err)
	})
	mux.HandleFunc("POST /api/models/compare", func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Before, After, IED, AP string }
		if !decode(w, r, &q) {
			return
		}
		diff, err := m.Compare(q.Before, q.After, q.IED, q.AP)
		respond(w, diff, err)
	})
	mux.HandleFunc("GET /api/connections/{id}/discover", func(w http.ResponseWriter, r *http.Request) {
		c, err := m.peer(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		directory, err := c.services.Discover(ctx, ied.DiscoveryOptions{Domain: r.URL.Query().Get("domain"), MaxObjects: 4096})
		respond(w, directory, err)
	})
	mux.HandleFunc("GET /api/connections/{id}/report-history", func(w http.ResponseWriter, r *http.Request) {
		after := uint64(0)
		if text := r.URL.Query().Get("after"); text != "" {
			value, err := strconv.ParseUint(text, 10, 64)
			if err != nil {
				http.Error(w, "invalid report cursor", http.StatusBadRequest)
				return
			}
			after = value
		}
		page, err := m.ReportHistory(r.PathValue("id"), after)
		respond(w, page, err)
	})
	mux.HandleFunc("POST /api/connections/{id}/snapshot", func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Domain, Item string }
		if !decode(w, r, &q) {
			return
		}
		snapshot, err := m.SnapshotRemote(r.Context(), r.PathValue("id"), q.Domain, q.Item)
		respond(w, map[string]any{"dataset": snapshot.DataSet, "received_at": snapshot.ReceivedAt}, err)
	})
	mux.HandleFunc("POST /api/connections/{id}/controls", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Reference, Action string
			Value             json.RawMessage
			Options           ied.ControlOptions
		}
		if !decode(w, r, &q) {
			return
		}
		reply, err := m.ControlRemote(r.Context(), r.PathValue("id"), q.Reference, q.Action, q.Value, q.Options)
		respond(w, reply, err)
	})
	mux.HandleFunc("GET /api/connections/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		c, err := m.peer(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		state, err := c.services.SettingGroups(r.Context(), r.URL.Query().Get("domain"))
		respond(w, state, err)
	})
	mux.HandleFunc("POST /api/connections/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Domain                 string
			Group                  uint32
			Values                 map[string]json.RawMessage
			ActivateOnly, Activate bool
		}
		if !decode(w, r, &q) {
			return
		}
		reply, err := m.SettingsRemote(r.Context(), r.PathValue("id"), q.Domain, q.Group, q.Values, q.ActivateOnly, q.Activate)
		respond(w, reply, err)
	})
}
