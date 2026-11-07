// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func (m *Manager) Catalog(model, device, ap string) (scl.Catalog, error) {
	m.mu.RLock()
	doc := m.docs[model]
	m.mu.RUnlock()
	if doc == nil {
		return scl.Catalog{}, ErrNotFound
	}
	return doc.system.Catalog(device, ap)
}
func (m *Manager) CheckRemote(ctx context.Context, id, model, device, ap string) ([]scl.Diagnostic, error) {
	catalog, err := m.Catalog(model, device, ap)
	if err != nil {
		return nil, err
	}
	peer, err := m.peer(id)
	if err != nil {
		return nil, err
	}
	return catalog.CheckOnline(ctx, peer.client)
}

// SubscribeRemote sends actual RCB requests over the selected MMS association.
// Report values and entry IDs go to the existing bounded SSE event journal.
func (m *Manager) SubscribeRemote(ctx context.Context, id, domain, item string, options ied.ReportOptions) error {
	c, err := m.peer(id)
	if err != nil {
		return err
	}
	name := mms.DomainName(domain, item)
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.subscriptions[name]; existing != nil {
		return fmt.Errorf("report already subscribed")
	}
	var subscription *ied.ReportSubscription
	if strings.Contains(item, "$BR$") {
		subscription, err = c.services.SubscribeBRCB(ctx, name, options)
	} else {
		subscription, err = c.services.SubscribeURCB(ctx, name, options)
	}
	if err != nil {
		return err
	}
	c.subscriptions[name] = subscription
	go func() {
		for event := range subscription.Events() {
			if event.Err != nil {
				m.log.add("report.error", id, event.Err.Error())
				continue
			}
			report := event.Report
			m.log.add("report.received", id, fmt.Sprintf("%s · %d members · entryID=%s · historyGap=%t", report.ID, len(report.Entries), hex.EncodeToString(report.EntryID), report.Reset))
			for _, entry := range report.Entries {
				m.log.add("report.value", id, fmt.Sprintf("%s = %s · reason=%s", entry.Variable.Name, entry.Value, entry.Reason))
			}
		}
		if err := subscription.Err(); err != nil {
			m.log.add("report.error", id, err.Error())
		}
	}()
	return nil
}
func (m *Manager) StopRemoteReport(ctx context.Context, id, domain, item string) error {
	c, err := m.peer(id)
	if err != nil {
		return err
	}
	name := mms.DomainName(domain, item)
	c.mu.Lock()
	defer c.mu.Unlock()
	sub := c.subscriptions[name]
	if sub == nil {
		return ErrNotFound
	}
	delete(c.subscriptions, name)
	return sub.Stop(ctx)
}
