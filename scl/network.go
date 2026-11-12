// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import "fmt"

// Network retains the SCL Ethernet broadcast domain. ServerAt connects another
// access point to an existing MMS server, without bridging its Ethernet ports.
type Network struct {
	Name, Type   string
	AccessPoints []AccessPoint
}

// Networks returns independent SubNetwork and ConnectedAP descriptions, including resolved
// ServerAt ownership. Calling it opens no transports.
func (s *System) Networks() []Network {
	out := make([]Network, 0, len(s.doc.Networks))
	for _, n := range s.doc.Networks {
		item := Network{Name: n.Name, Type: n.Type}
		for _, c := range n.Connections {
			owner, _ := s.ServerAccessPoint(c.IED, c.AP)
			item.AccessPoints = append(item.AccessPoints, AccessPoint{IED: c.IED, AP: c.AP, HasServer: owner == c.AP && owner != "", ServerAP: owner})
		}
		out = append(out, item)
	}
	return out
}

// ServerAccessPoint returns the owning Server AP, an empty name for a client
// AP, or an error for unresolved/cyclic ServerAt declarations.
func (s *System) ServerAccessPoint(ied, ap string) (string, error) {
	seen := map[string]bool{}
	for {
		if seen[ap] {
			return "", fmt.Errorf("scl: cyclic ServerAt at %s/%s", ied, ap)
		}
		seen[ap] = true
		p, err := s.findRaw(ied, ap)
		if err != nil {
			return "", err
		}
		if p.ServerAt == nil {
			if p.Server != nil {
				return ap, nil
			}
			return "", nil
		}
		if p.Server != nil || p.ServerAt.AP == "" {
			return "", fmt.Errorf("scl: invalid ServerAt at %s/%s", ied, ap)
		}
		ap = p.ServerAt.AP
	}
}

// AccessNetworks includes the physical APs that share this AP's server. The
// returned networks never imply a bridge between those physical ports.
func (s *System) AccessNetworks(ied, ap string) ([]string, error) {
	owner, err := s.ServerAccessPoint(ied, ap)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range s.Networks() {
		for _, p := range n.AccessPoints {
			if p.IED == ied && (p.AP == ap || owner != "" && p.ServerAP == owner) {
				out = append(out, n.Name)
				break
			}
		}
	}
	return out, nil
}

// SharedNetwork resolves a subscriber onto the publisher AP's broadcast
// domain. An entirely unaddressed engineering fixture uses an unnamed bus.
// A partially wired real project cannot silently acquire that fallback.
func (s *System) SharedNetwork(sourceIED, sourceAP, receiverIED, receiverAP string) (string, error) {
	if len(s.doc.Networks) == 0 {
		return "", nil
	}
	receiver, err := s.AccessNetworks(receiverIED, receiverAP)
	if err != nil {
		return "", err
	}
	for _, c := range s.doc.Connections {
		if c.IED == sourceIED && c.AP == sourceAP {
			for _, n := range receiver {
				if n == c.Network {
					return n, nil
				}
			}
		}
	}
	return "", fmt.Errorf("scl: no common SubNetwork for %s/%s -> %s/%s", sourceIED, sourceAP, receiverIED, receiverAP)
}

// PublisherNetwork selects the physical port carrying this AP's control
// blocks. Multiple ConnectedAP declarations for one physical AP are ambiguous.
func (s *System) PublisherNetwork(ied, ap string) (string, error) {
	var name string
	found := false
	for _, c := range s.doc.Connections {
		if c.IED == ied && c.AP == ap {
			if found {
				return "", fmt.Errorf("scl: ambiguous ConnectedAP %s/%s", ied, ap)
			}
			name, found = c.Network, true
		}
	}
	return name, nil
}
