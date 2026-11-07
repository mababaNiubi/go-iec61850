// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
)

type ConnectionSummary struct {
	ID       string       `json:"id"`
	Address  string       `json:"address"`
	Identity mms.Identity `json:"identity"`
	Status   string       `json:"status"`
}
type connection struct {
	ConnectionSummary
	client        *mms.Client
	services      *ied.Client
	mu            sync.Mutex
	subscriptions map[mms.ObjectName]*ied.ReportSubscription
}

// Connect uses the optional engineering model for ISO addressing. The context
// bounds this connection attempt; Manager.Close owns the resulting association.
func (m *Manager) Connect(ctx context.Context, address, deviceID string) (ConnectionSummary, error) {
	cfg := mms.Config{}
	if deviceID != "" {
		d, err := m.get(deviceID)
		if err != nil {
			return ConnectionSummary{}, err
		}
		cfg = d.ied.ClientConfig()
	}
	peer, err := mms.Dial(ctx, address, cfg)
	if err != nil {
		return ConnectionSummary{}, err
	}
	identity, err := peer.Identify(ctx)
	if err != nil {
		peer.Close()
		return ConnectionSummary{}, err
	}
	id := digest([]byte(address + time.Now().Format(time.RFC3339Nano)))
	services, err := ied.Wrap(peer, ied.Origin{})
	if err != nil {
		peer.Close()
		return ConnectionSummary{}, err
	}
	c := &connection{ConnectionSummary: ConnectionSummary{id, address, identity, "connected"}, client: peer, services: services, subscriptions: map[mms.ObjectName]*ied.ReportSubscription{}}
	m.mu.Lock()
	if m.closed || len(m.connections) >= 16 {
		m.mu.Unlock()
		peer.Close()
		return ConnectionSummary{}, fmt.Errorf("connection limit reached or workspace closed")
	}
	m.connections[id] = c
	m.mu.Unlock()
	m.log.add("client.connect", id, fmt.Sprintf("Connected to %s · %s %s", address, identity.Vendor, identity.Model))
	return c.ConnectionSummary, nil
}
func (m *Manager) Connections() []ConnectionSummary {
	m.mu.RLock()
	list := make([]*connection, 0, len(m.connections))
	for _, c := range m.connections {
		list = append(list, c)
	}
	m.mu.RUnlock()
	out := make([]ConnectionSummary, 0, len(list))
	for _, c := range list {
		v := c.ConnectionSummary
		select {
		case <-c.client.Done():
			v.Status = "closed"
		default:
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}
func (m *Manager) peer(id string) (*connection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := m.connections[id]
	if c == nil {
		return nil, ErrNotFound
	}
	return c, nil
}
func (m *Manager) Disconnect(id string) error {
	m.mu.Lock()
	c := m.connections[id]
	delete(m.connections, id)
	m.mu.Unlock()
	if c == nil {
		return ErrNotFound
	}
	c.services.Close()
	m.log.add("client.disconnect", id, "MMS association closed")
	return nil
}
func (m *Manager) Domains(ctx context.Context, id string) ([]string, error) {
	c, err := m.peer(id)
	if err != nil {
		return nil, err
	}
	return c.client.Names(ctx, mms.DomainObject, mms.VMDScope, "")
}
func (m *Manager) Names(ctx context.Context, id, domain string) ([]string, error) {
	c, err := m.peer(id)
	if err != nil {
		return nil, err
	}
	return c.client.Names(ctx, mms.NamedVariable, mms.DomainScope, domain)
}
func (m *Manager) ReadRemote(ctx context.Context, id, domain, item string) (Point, error) {
	c, err := m.peer(id)
	if err != nil {
		return Point{}, err
	}
	n := mms.DomainName(domain, item)
	spec, err := c.client.GetVariableAccessAttributes(ctx, n)
	if err != nil {
		return Point{}, err
	}
	v, err := c.client.Read(ctx, n)
	if err != nil {
		return Point{}, err
	}
	m.log.add("client.read", id, fmt.Sprintf("%s = %s", n, v))
	return Point{Domain: domain, Item: item, Reference: n.String(), Kind: spec.Kind.String(), Type: spec, Value: ValueJSON(v), Display: v.String(), Editable: true}, nil
}
func (m *Manager) WriteRemote(ctx context.Context, id, domain, item string, raw []byte) error {
	c, err := m.peer(id)
	if err != nil {
		return err
	}
	n := mms.DomainName(domain, item)
	spec, err := c.client.GetVariableAccessAttributes(ctx, n)
	if err != nil {
		return err
	}
	v, err := ParseValue(spec, raw)
	if err != nil {
		return err
	}
	if err = c.client.Write(ctx, n, v); err != nil {
		return err
	}
	m.log.add("client.write", id, fmt.Sprintf("%s ← %s", n, v))
	return nil
}
