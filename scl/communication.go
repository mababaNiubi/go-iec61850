// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/mms"
)

func (d *IED) configureCommunication(ap accessPoint) error {
	var cap *connection
	for i := range d.system.doc.Connections {
		p := &d.system.doc.Connections[i]
		if p.IED != d.name || p.AP != d.ap {
			continue
		}
		if cap != nil {
			return fmt.Errorf("ambiguous ConnectedAP on multiple subnetworks")
		}
		cap = p
	}
	var routes = map[[2]string]gse{}
	if cap != nil {
		p, err := parameters(cap.Address)
		if err != nil {
			return err
		}
		if err = d.configureEndpoint(p); err != nil {
			return err
		}
		for _, g := range cap.GSE {
			k := [2]string{g.LD, g.Block}
			if _, ok := routes[k]; ok {
				return fmt.Errorf("duplicate GSE address %s/%s", g.LD, g.Block)
			}
			routes[k] = g
		}
	}
	for _, ld := range ap.Server.Devices {
		for _, ln := range append([]logicalNode{ld.Zero[0]}, ld.Nodes...) {
			for _, set := range ln.Sets {
				if err := d.addSet(ld.Inst, ln.name(), set); err != nil {
					return err
				}
			}
			for _, b := range ln.Blocks {
				if ln.name() != "LLN0" {
					return fmt.Errorf("GSEControl %s must belong to LLN0", b.Name)
				}
				k := [2]string{ld.Inst, b.Name}
				g, ok := routes[k]
				if !ok {
					return fmt.Errorf("GSEControl %s/%s has no matching Communication/GSE", ld.Inst, b.Name)
				}
				if err := d.addStream(ld.Inst, b, g); err != nil {
					return fmt.Errorf("GSEControl %s/%s: %w", ld.Inst, b.Name, err)
				}
				delete(routes, k)
			}
		}
	}
	for k := range routes {
		return fmt.Errorf("Communication/GSE %s/%s has no matching GSEControl", k[0], k[1])
	}
	return nil
}

func (d *IED) addSet(ld, ln string, s setXML) error {
	name := mms.DomainName(d.domains[ld], ln+"$"+s.Name)
	if !identifier(s.Name) || !data.ValidIdentifier(name.Item) || len(s.Members) == 0 || len(s.Members) > 4096 {
		return fmt.Errorf("invalid static dataset %s", name)
	}
	if _, exists := d.sets[name]; exists {
		return fmt.Errorf("duplicate dataset %s", name)
	}
	set := mms.NamedList{Name: name}
	for i, m := range s.Members {
		if m.Index != "" || m.LD == "" || m.Class == "" || m.DO == "" {
			return fmt.Errorf("dataset %s FCDA %d requires an explicit, unindexed target", name, i)
		}
		path := m.DO
		if m.DA != "" {
			path += "." + m.DA
		}
		n, err := d.Lookup(m.LD, m.Prefix+m.Class+m.Inst, m.FC, path)
		if err != nil {
			return fmt.Errorf("dataset %s FCDA %d: %w", name, i, err)
		}
		set.Variables = append(set.Variables, mms.Access(n))
	}
	d.sets[name] = set
	return nil
}

func parameters(params []param) (map[string]string, error) {
	result := make(map[string]string, len(params))
	for _, p := range params {
		if _, ok := result[p.Type]; ok {
			return nil, fmt.Errorf("duplicate Address/P %s", p.Type)
		}
		result[p.Type] = strings.TrimSpace(p.Text)
	}
	return result, nil
}
func milliseconds(t *timing, fallback time.Duration) (time.Duration, error) {
	if t == nil {
		return fallback, nil
	}
	if t.Unit != "" && t.Unit != "s" || t.Multiplier != "" && t.Multiplier != "m" {
		return 0, fmt.Errorf("GSE time must be in milliseconds")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(t.Text), 64)
	if err != nil || math.IsNaN(n) || n < 1 || n > float64(24*time.Hour/time.Millisecond) {
		return 0, fmt.Errorf("unsupported GSE time %q ms", t.Text)
	}
	return time.Duration(n * float64(time.Millisecond)), nil
}
func (d *IED) addStream(ld string, b blockXML, g gse) error {
	if !identifier(b.Name) || b.Type != "" && b.Type != "GOOSE" || b.Protocol != nil || b.Fixed || b.Security != "" && b.Security != "None" {
		return fmt.Errorf("requires ordinary Layer 2 GOOSE")
	}
	if b.Revision == 0 {
		return fmt.Errorf("a positive confRev is required")
	}
	domain := d.domains[ld]
	set, ok := d.sets[mms.DomainName(domain, "LLN0$"+b.Set)]
	if !ok {
		return fmt.Errorf("dataset %q not found in LLN0", b.Set)
	}
	if len(set.Variables) > goose.MaxDataSetEntries {
		return fmt.Errorf("dataset exceeds the GOOSE entry limit")
	}
	p, err := parameters(g.Address)
	if err != nil {
		return err
	}
	mac, err := net.ParseMAC(p["MAC-Address"])
	if err != nil || len(mac) != 6 || mac[0] != 1 || mac[1] != 0x0c || mac[2] != 0xcd || mac[3] != 1 || mac[4] > 1 {
		return fmt.Errorf("invalid GOOSE multicast MAC %q", p["MAC-Address"])
	}
	app, err := strconv.ParseUint(p["APPID"], 16, 16)
	if err != nil || app > 0x3fff {
		return fmt.Errorf("invalid hexadecimal GOOSE APPID %q", p["APPID"])
	}
	var vlan *goose.VLAN
	vid, hasVID := p["VLAN-ID"]
	pcp, hasPCP := p["VLAN-PRIORITY"]
	if hasVID || hasPCP {
		vlan = &goose.VLAN{Priority: 4}
		if hasVID {
			v, e := strconv.ParseUint(vid, 16, 12)
			if e != nil || v >= 4095 {
				return fmt.Errorf("invalid hexadecimal VLAN-ID %q", vid)
			}
			vlan.ID = uint16(v)
		}
		if hasPCP {
			v, e := strconv.ParseUint(pcp, 10, 3)
			if e != nil {
				return fmt.Errorf("invalid VLAN priority %q", pcp)
			}
			vlan.Priority = uint8(v)
		}
	}
	minTime, err := milliseconds(g.Min, 4*time.Millisecond)
	if err != nil {
		return err
	}
	maxTime, err := milliseconds(g.Max, time.Second)
	if err != nil {
		return err
	}
	if minTime > maxTime {
		return fmt.Errorf("MinTime exceeds MaxTime")
	}
	n := mms.DomainName(domain, "LLN0$GO$"+b.Name)
	stream := &Stream{ied: d, name: n, set: set, minTime: minTime, maxTime: maxTime}
	stream.frame = goose.Frame{Destination: [6]byte(mac), APPID: uint16(app), VLAN: vlan,
		PDU: goose.PDU{GoCBRef: n.String(), DataSet: set.Name.String(), GoID: b.GoID, ConfRev: b.Revision}}
	for _, v := range set.Variables {
		stream.types = append(stream.types, d.types[v.Name])
	}
	if _, err := stream.NewSubscriber(); err != nil {
		return err
	}
	d.streams[n] = stream
	return nil
}
