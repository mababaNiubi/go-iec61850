// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

type ReportValue struct {
	Reference string        `json:"reference"`
	Value     any           `json:"value"`
	Display   string        `json:"display"`
	Reason    string        `json:"reason"`
	Selection mms.Selection `json:"selection"`
}
type ReportRecord struct {
	Index       uint64        `json:"index"`
	Block       string        `json:"block"`
	Source      string        `json:"source"` // buffered-report, report, or confirmed-read
	DataSet     string        `json:"dataset"`
	EntryID     string        `json:"entry_id"`
	ReceivedAt  time.Time     `json:"received_at"`
	TimeOfEntry string        `json:"time_of_entry"`
	Sequence    *uint16       `json:"sequence,omitempty"`
	Segments    int           `json:"segments"`
	Gap         bool          `json:"gap"`
	Values      []ReportValue `json:"values"`
}
type ReportPage struct {
	Records      []ReportRecord `json:"records"`
	Oldest, Last uint64
	Gap          bool `json:"gap"`
}

func (c *connection) appendRecord(block mms.ObjectName, source string, report ied.Report) error {
	leaves, err := report.Leaves()
	if err != nil {
		return err
	}
	r := ReportRecord{Block: block.String(), Source: source, DataSet: report.DataSet, EntryID: hex.EncodeToString(report.EntryID), ReceivedAt: report.ReceivedAt, Sequence: report.SequenceNumber, Segments: report.Segments, Gap: report.Reset, Values: []ReportValue{}}
	if report.TimeOfEntry.Kind() != 0 {
		if t, e := report.TimeOfEntry.Time(); e == nil {
			r.TimeOfEntry = t.Format(time.RFC3339Nano)
		}
	}
	for _, v := range leaves {
		r.Values = append(r.Values, ReportValue{Reference: v.Variable.Name.String(), Value: ValueJSON(v.Value), Display: v.Value.String(), Reason: v.Reason.String(), Selection: v.Variable.Selection.Clone()})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextRecord++
	r.Index = c.nextRecord
	c.records = append(c.records, r)
	if len(c.records) > 500 {
		c.records = append([]ReportRecord(nil), c.records[len(c.records)-500:]...)
	}
	return nil
}

func (m *Manager) ReportHistory(id string, after uint64) (ReportPage, error) {
	c, err := m.peer(id)
	if err != nil {
		return ReportPage{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	page := ReportPage{Records: []ReportRecord{}, Last: c.nextRecord}
	if len(c.records) > 0 {
		page.Oldest = c.records[0].Index
		page.Gap = after > 0 && after < page.Oldest-1
	}
	for _, r := range c.records {
		if r.Index > after {
			page.Records = append(page.Records, r)
		}
	}
	return page, nil
}

func (m *Manager) SnapshotRemote(ctx context.Context, id, domain, item string) (ied.DataSetSnapshot, error) {
	c, err := m.peer(id)
	if err != nil {
		return ied.DataSetSnapshot{}, err
	}
	block := mms.DomainName(domain, item)
	info, err := c.services.ReadReportBlock(ctx, block)
	if err != nil {
		return ied.DataSetSnapshot{}, err
	}
	d, member, ok := strings.Cut(info.DataSet, "/")
	if !ok {
		return ied.DataSetSnapshot{}, fmt.Errorf("invalid RCB dataset")
	}
	snapshot, err := c.services.ReadDataSet(ctx, mms.DomainName(d, member))
	if err != nil {
		return snapshot, err
	}
	snapshot.ConfRev = info.ConfRev
	err = c.appendRecord(block, "confirmed-read", ied.Report{DataSet: snapshot.DataSet, ConfRev: snapshot.ConfRev, ReceivedAt: snapshot.ReceivedAt, Entries: snapshot.Entries})
	return snapshot, err
}

func (m *Manager) Project(model string) (scl.Project, error) {
	m.mu.RLock()
	doc := m.docs[model]
	m.mu.RUnlock()
	if doc == nil {
		return scl.Project{}, ErrNotFound
	}
	return doc.system.Project(), nil
}

func (m *Manager) Compare(before, after, iedName, ap string) ([]scl.Difference, error) {
	a, err := m.Catalog(before, iedName, ap)
	if err != nil {
		return nil, err
	}
	b, err := m.Catalog(after, iedName, ap)
	if err != nil {
		return nil, err
	}
	return scl.CompareCatalog(a, b), nil
}

type ControlReply struct {
	Result ied.ControlResult `json:"result"`
	Phase  string            `json:"phase"`
	Error  string            `json:"error,omitempty"`
}

func (m *Manager) ControlRemote(ctx context.Context, id, reference, action string, raw json.RawMessage, options ied.ControlOptions) (ControlReply, error) {
	c, err := m.peer(id)
	if err != nil {
		return ControlReply{}, err
	}
	c.operations.Lock()
	defer c.operations.Unlock()
	if c.selections == nil {
		c.selections = map[string]*ied.Selection{}
	}
	reply := ControlReply{Phase: action}
	selection := c.selections[reference]
	if action == "cancel" {
		if selection == nil {
			return reply, fmt.Errorf("no selection for %s", reference)
		}
		err = selection.Cancel(ctx)
		if err == nil {
			delete(c.selections, reference)
		}
	} else if action == "operate" && selection != nil {
		reply.Result, err = selection.Operate(ctx)
		delete(c.selections, reference)
	} else {
		if action != "select" && action != "operate" {
			return reply, fmt.Errorf("expected select, operate or cancel")
		}
		if selection != nil {
			return reply, fmt.Errorf("cancel existing selection first")
		}
		control, e := c.services.Control(ctx, reference)
		if e != nil {
			return reply, e
		}
		spec, e := control.ValueType()
		if e != nil {
			return reply, e
		}
		value, e := ParseValue(spec, raw)
		if e != nil {
			return reply, e
		}
		if action == "select" {
			selection, err = control.Select(ctx, value, options)
			if err == nil {
				c.selections[reference] = selection
				reply.Result = ied.ControlResult{Model: control.Model(), Selected: true}
			}
		} else {
			reply.Result, err = control.Operate(ctx, value, options)
		}
	}
	if err != nil {
		reply.Error = err.Error()
	}
	m.log.add("control."+action, id, fmt.Sprintf("%s · %+v · %s", reference, reply.Result, reply.Error))
	// Preserve accepted-but-unconfirmed evidence even when termination fails.
	return reply, nil
}

type SettingsReply struct {
	Result ied.SettingResult `json:"result"`
	Error  string            `json:"error,omitempty"`
}

func (m *Manager) SettingsRemote(ctx context.Context, id, domain string, group uint32, values map[string]json.RawMessage, activateOnly, activate bool) (SettingsReply, error) {
	c, err := m.peer(id)
	if err != nil {
		return SettingsReply{}, err
	}
	reply := SettingsReply{}
	if activateOnly {
		err = c.services.ActivateSettings(ctx, domain, group)
		reply.Result.Activated = err == nil
	} else {
		converted := map[mms.ObjectName]mms.Value{}
		for ref, raw := range values {
			name, e := ied.ObjectName(domain, "SE", ref)
			if e != nil {
				return reply, e
			}
			spec, e := c.services.GetVariableAccessAttributes(ctx, name)
			if e != nil {
				return reply, e
			}
			value, e := ParseValue(spec, raw)
			if e != nil {
				return reply, e
			}
			converted[name] = value
		}
		reply.Result, err = c.services.EditSettings(ctx, domain, group, converted, activate)
	}
	if err != nil {
		reply.Error = err.Error()
	}
	m.log.add("settings.update", id, fmt.Sprintf("%s group %d · %+v · %s", domain, group, reply.Result, reply.Error))
	return reply, nil
}
