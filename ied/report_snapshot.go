// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"fmt"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// DataSetSnapshot is a confirmed Read, independent of retained report replay.
// It has no EntryID and must never advance a durable report cursor. A response
// is one MMS transaction; coherence between separate FCDA values depends on
// the remote Store. An FCD member keeps its value/q/t in one structured value.
type DataSetSnapshot struct {
	DataSet    string
	ConfRev    uint32
	ReceivedAt time.Time
	Entries    []ReportEntry
}

// ReadSnapshot obtains current dataset values without treating a historical
// GI as the response to this session's request. GI carries no request ID.
func (s *ReportSubscription) ReadSnapshot(ctx context.Context) (DataSetSnapshot, error) {
	s.mu.Lock()
	closed := s.closed
	cfg := s.decoder.config
	s.mu.Unlock()
	if closed {
		return DataSetSnapshot{}, mms.ErrClosed
	}
	return s.client.readSnapshot(ctx, cfg)
}

// ReadDataSet reads the online list and selected types, then obtains its values
// in one confirmed Read. No report subscription or engineering file is needed.
func (c *Client) ReadDataSet(ctx context.Context, name mms.ObjectName) (DataSetSnapshot, error) {
	list, err := c.GetNamedListAttributes(ctx, name)
	if err != nil {
		return DataSetSnapshot{}, err
	}
	cfg := ReportConfig{DataSet: name.String(), Members: list.Variables}
	for _, access := range list.Variables {
		spec, e := c.GetVariableAccessAttributes(ctx, access.Name)
		if e != nil {
			return DataSetSnapshot{}, e
		}
		spec, e = mms.SelectedType(spec, access.Selection)
		if e != nil {
			return DataSetSnapshot{}, e
		}
		cfg.Types = append(cfg.Types, spec)
	}
	return c.readSnapshot(ctx, cfg)
}

func (c *Client) readSnapshot(ctx context.Context, cfg ReportConfig) (DataSetSnapshot, error) {
	results, err := c.ReadMultiple(ctx, cfg.Members...)
	if err != nil {
		return DataSetSnapshot{}, err
	}
	if len(results) != len(cfg.Members) {
		return DataSetSnapshot{}, mms.ErrProtocol
	}
	out := DataSetSnapshot{DataSet: cfg.DataSet, ConfRev: cfg.ConfRev, ReceivedAt: time.Now()}
	for i, r := range results {
		if r.Error != nil {
			return DataSetSnapshot{}, fmt.Errorf("%s: %w", cfg.Members[i].Name, r.Error)
		}
		entry := ReportEntry{Variable: cfg.Members[i], Value: r.Value}
		entry.Variable.Selection = entry.Variable.Selection.Clone()
		if len(cfg.Types) > 0 {
			entry.Type = cfg.Types[i]
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

// Leaves expands structured FCD members into typed leaf entries while preserving
// alternate-access identity. Arrays and selected members retain their original
// representation. A snapshot has no report inclusion reason.
func (s DataSetSnapshot) Leaves() ([]ReportEntry, error) {
	return (Report{Entries: s.Entries}).Leaves()
}
