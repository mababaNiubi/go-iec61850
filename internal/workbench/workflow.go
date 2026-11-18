// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"errors"
	"fmt"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// The acquisition workflow exposes the SDK's binding, dynamic-dataset and
// reconfiguration services over HTTP. Every operation is a thin wrapper over
// the public ied client; the workbench never reimplements protocol behavior.

func (m *Manager) BindRemote(ctx context.Context, id, domain, item string, verifyEachRead bool) (ied.BindingInfo, error) {
	c, err := m.peer(id)
	if err != nil {
		return ied.BindingInfo{}, err
	}
	name := mms.DomainName(domain, item)
	c.mu.Lock()
	_, exists := c.bindings[name]
	c.mu.Unlock()
	if exists {
		return ied.BindingInfo{}, fmt.Errorf("%w: dataset already bound", ErrConflict)
	}
	binding, err := c.services.BindDataSet(ctx, name, ied.BindOptions{VerifyEachRead: verifyEachRead})
	if err != nil {
		return ied.BindingInfo{}, err
	}
	c.mu.Lock()
	if c.bindings == nil {
		c.bindings = map[mms.ObjectName]*ied.DataSetBinding{}
	}
	c.bindings[name] = binding
	c.mu.Unlock()
	m.log.add("binding.create", id, fmt.Sprintf("%s bound · generation 0 · each-read check %t", name, verifyEachRead))
	return binding.Info(), nil
}

func (m *Manager) RemoteBindings(id string) ([]ied.BindingInfo, error) {
	c, err := m.peer(id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ied.BindingInfo, 0, len(c.bindings))
	for _, binding := range c.bindings {
		out = append(out, binding.Info())
	}
	return out, nil
}

// ReadRemoteBinding acquires values through a bound generation. A detected
// configuration change surfaces the typed error and marks the binding stale
// in the journal instead of publishing values under the old generation.
func (m *Manager) ReadRemoteBinding(ctx context.Context, id, domain, item string) (ied.DataSetSnapshot, error) {
	c, err := m.peer(id)
	if err != nil {
		return ied.DataSetSnapshot{}, err
	}
	name := mms.DomainName(domain, item)
	c.mu.Lock()
	binding := c.bindings[name]
	c.mu.Unlock()
	if binding == nil {
		return ied.DataSetSnapshot{}, ErrNotFound
	}
	snapshot, err := binding.Read(ctx)
	if err != nil {
		if errors.Is(err, ied.ErrConfigurationChanged) {
			m.log.add("binding.stale", id, fmt.Sprintf("%s: %v", name, err))
		}
		return ied.DataSetSnapshot{}, err
	}
	info := binding.Info()
	m.log.add("binding.read", id, fmt.Sprintf("%s · generation %d · verified=%t", name, info.GenerationID, snapshot.Verified))
	return snapshot, nil
}

func (m *Manager) UnbindRemote(id, domain, item string) error {
	c, err := m.peer(id)
	if err != nil {
		return err
	}
	name := mms.DomainName(domain, item)
	c.mu.Lock()
	binding := c.bindings[name]
	delete(c.bindings, name)
	c.mu.Unlock()
	if binding == nil {
		return ErrNotFound
	}
	m.log.add("binding.close", id, name.String())
	return binding.Close()
}

// MemberAccess is one dataset member in the HTTP request: a domain/item pair.
type MemberAccess struct {
	Domain, Item string
}

// CreateRemoteDataSet validates and defines a dynamic dataset on the peer.
func (m *Manager) CreateRemoteDataSet(ctx context.Context, id, domain, item string, members []MemberAccess) error {
	c, err := m.peer(id)
	if err != nil {
		return err
	}
	accesses := make([]mms.VariableAccess, len(members))
	for i, member := range members {
		accesses[i] = mms.Access(mms.DomainName(member.Domain, member.Item))
	}
	name := mms.DomainName(domain, item)
	if err = c.services.CreateDataSet(ctx, name, accesses); err != nil {
		return err
	}
	m.log.add("dataset.create", id, fmt.Sprintf("%s · %d members", name, len(members)))
	return nil
}

func (m *Manager) DeleteRemoteDataSet(ctx context.Context, id, domain, item string) (mms.DeleteResult, error) {
	c, err := m.peer(id)
	if err != nil {
		return mms.DeleteResult{}, err
	}
	result, err := c.services.DeleteDataSet(ctx, mms.DomainName(domain, item))
	if err == nil {
		m.log.add("dataset.delete", id, fmt.Sprintf("%s · matched=%d deleted=%d", mms.DomainName(domain, item), result.Matched, result.Deleted))
	}
	return result, err
}

// RemoteRCBSummary reports one reconfigured control block to the browser.
type RemoteRCBSummary struct {
	ied.ConfiguredRCB
}

// ConfigureRemoteRCB rebinds a report control block through the SDK workflow.
func (m *Manager) ConfigureRemoteRCB(ctx context.Context, id, domain, item, setDomain, setItem, rptID string) (ied.ConfiguredRCB, error) {
	c, err := m.peer(id)
	if err != nil {
		return ied.ConfiguredRCB{}, err
	}
	block := mms.DomainName(domain, item)
	config := ied.RCBConfiguration{DataSet: mms.DomainName(setDomain, setItem), RptID: rptID}
	configured, err := c.services.ConfigureRCB(ctx, block, config)
	if err != nil {
		return ied.ConfiguredRCB{}, err
	}
	m.log.add("rcb.configure", id, fmt.Sprintf("%s → %s · ConfRev=%d", block, configured.Declaration.DataSet, configured.Declaration.ConfRev))
	return configured, nil
}

// VerifyRemoteSelection revalidates a saved selection document against the
// connected peer, reporting per-point ok/missing/type-changed results.
func (m *Manager) VerifyRemoteSelection(ctx context.Context, id string, doc ied.SelectionDocument) (ied.SelectionReport, error) {
	c, err := m.peer(id)
	if err != nil {
		return ied.SelectionReport{}, err
	}
	report, err := c.services.VerifySelection(ctx, doc)
	if err != nil {
		return ied.SelectionReport{}, err
	}
	changed := 0
	for _, entry := range report.Entries {
		if entry.Status != ied.SelectionOK {
			changed++
		}
	}
	m.log.add("selection.verify", id, fmt.Sprintf("%d points · %d need attention", len(report.Entries), changed))
	return report, nil
}

// closeBindings releases a disconnecting connection's bindings.
func (c *connection) closeBindings() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, binding := range c.bindings {
		binding.Close()
	}
	c.bindings = nil
}
