// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/mababaNiubi/go-iec61850/sv"
)

type svControlXML struct {
	Name      string         `xml:"name,attr"`
	ID        string         `xml:"smvID,attr"`
	Set       string         `xml:"datSet,attr"`
	Revision  string         `xml:"confRev,attr"`
	Rate      string         `xml:"smpRate,attr"`
	Mode      string         `xml:"smpMod,attr"`
	Count     string         `xml:"nofASDU,attr"`
	Multicast string         `xml:"multicast,attr"`
	Security  string         `xml:"securityEnable,attr"`
	Protocols []string       `xml:"Protocol"`
	Options   []svOptionsXML `xml:"SmvOpts"`
}
type svOptionsXML struct {
	DataSet   string `xml:"dataSet,attr"`
	Rate      string `xml:"sampleRate,attr"`
	Time      string `xml:"refreshTime,attr"`
	Identity  string `xml:"synchSourceId,attr"`
	Sync      string `xml:"sampleSynchronized,attr"`
	Security  string `xml:"security,attr"`
	Timestamp string `xml:"timestamp,attr"`
}
type smvXML struct {
	LD      string  `xml:"ldInst,attr"`
	Block   string  `xml:"cbName,attr"`
	Address []param `xml:"Address>P"`
}

func svNumber(raw, field string, min, max uint64) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("scl: invalid SV %s %q", field, raw)
	}
	return n, nil
}
func svBoolean(raw, field string, def bool) (bool, error) {
	if raw == "" {
		return def, nil
	}
	switch raw {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("scl: invalid SV %s %q", field, raw)
	}
}

// SVStreams resolves the ordinary multicast SampledValueControls of one AP.
// It shares the namespace/type parser with MMS/GOOSE and does not construct an
// MMS server, so SV-only models and SV-specific widths remain independent.
func (s *System) SVStreams(ied, ap string) ([]sv.Profile, error) {
	a, err := s.find(ied, ap)
	if err != nil {
		return nil, err
	}
	if a.Server == nil {
		return nil, nil
	}
	var out []sv.Profile
	for _, ld := range a.Server.Devices {
		for _, ln := range ld.Zero {
			for _, block := range ln.Sampled {
				profile, err := s.SVStream(ied + "/" + ap + "/" + ld.Inst + "/" + block.Name)
				if err != nil {
					return nil, err
				}
				out = append(out, profile)
			}
		}
		for _, ln := range ld.Nodes {
			if len(ln.Sampled) != 0 {
				return nil, fmt.Errorf("scl: SampledValueControl must belong to LLN0")
			}
		}
	}
	return out, nil
}

func (s *System) SVStream(reference string) (sv.Profile, error) {
	parts := strings.Split(reference, "/")
	if len(parts) != 4 {
		return sv.Profile{}, fmt.Errorf("scl: SV reference requires IED/AP/LDevice/SVCB")
	}
	for _, p := range parts {
		if !identifier(p) || len(p) > 64 {
			return sv.Profile{}, fmt.Errorf("scl: invalid SV reference %q", reference)
		}
	}
	ap, err := s.find(parts[0], parts[1])
	if err != nil {
		return sv.Profile{}, err
	}
	if ap.Server == nil {
		return sv.Profile{}, fmt.Errorf("scl: SV access point has no server")
	}
	devices, err := index(ap.Server.Devices, func(ld logicalDevice) string { return ld.Inst })
	if err != nil {
		return sv.Profile{}, err
	}
	ld, ok := devices[parts[2]]
	if !ok || len(ld.Zero) != 1 || ld.Zero[0].name() != "LLN0" {
		return sv.Profile{}, fmt.Errorf("scl: invalid SV logical device")
	}
	blocks, err := index(ld.Zero[0].Sampled, func(b svControlXML) string { return b.Name })
	if err != nil {
		return sv.Profile{}, err
	}
	b, ok := blocks[parts[3]]
	if !ok {
		return sv.Profile{}, fmt.Errorf("scl: unknown SampledValueControl %q", reference)
	}
	profile, err := s.compileSV(reference, parts, ap, ld, b)
	if err != nil {
		return sv.Profile{}, fmt.Errorf("scl: %s: %w", reference, err)
	}
	return profile, nil
}

func (s *System) compileSV(reference string, parts []string, ap accessPoint, ld logicalDevice, b svControlXML) (sv.Profile, error) {
	p := sv.Profile{Reference: reference, SVID: b.ID}
	if len(b.Protocols) != 0 || b.Security != "" && b.Security != "None" {
		return p, fmt.Errorf("secured or routable SV is unsupported")
	}
	multicast, err := svBoolean(b.Multicast, "multicast", true)
	if err != nil || !multicast {
		return p, fmt.Errorf("unicast SVCB reservation is unsupported")
	}
	n, err := svNumber(b.Revision, "confRev", 1, 1<<32-1)
	if err != nil {
		return p, err
	}
	p.ConfRev = uint32(n)
	n, err = svNumber(b.Rate, "smpRate", 1, 65535)
	if err != nil {
		return p, err
	}
	p.SampleRate = uint16(n)
	n, err = svNumber(b.Count, "nofASDU", 1, sv.MaxASDUs)
	if err != nil {
		return p, err
	}
	p.ASDUCount = int(n)
	p.ModePresent = b.Mode != ""
	switch b.Mode {
	case "", "SmpPerPeriod":
		p.SampleMode = 0
	case "SmpPerSec":
		p.SampleMode = 1
	case "SecPerSmp":
		p.SampleMode = 2
	default:
		return p, fmt.Errorf("invalid smpMod")
	}
	if len(b.Options) != 1 {
		return p, fmt.Errorf("SVCB needs one SmvOpts")
	}
	opts := b.Options[0]
	if p.WithDataSet, err = svBoolean(opts.DataSet, "dataSet", false); err != nil {
		return p, err
	}
	if p.WithRate, err = svBoolean(opts.Rate, "sampleRate", false); err != nil {
		return p, err
	}
	if p.WithTime, err = svBoolean(opts.Time, "refreshTime", false); err != nil {
		return p, err
	}
	if p.WithIdentity, err = svBoolean(opts.Identity, "synchSourceId", false); err != nil {
		return p, err
	}
	if v, e := svBoolean(opts.Sync, "sampleSynchronized", true); e != nil || !v {
		return p, fmt.Errorf("SV profile requires synchronization metadata")
	}
	if v, e := svBoolean(opts.Security, "security", false); e != nil || v {
		return p, fmt.Errorf("secured SV is unsupported")
	}
	if v, e := svBoolean(opts.Timestamp, "timestamp", false); e != nil || v {
		return p, fmt.Errorf("routable SV timestamp option is unsupported")
	}
	sets, err := index(ld.Zero[0].Sets, func(v setXML) string { return v.Name })
	if err != nil {
		return p, err
	}
	set, ok := sets[b.Set]
	if !ok {
		return p, fmt.Errorf("unknown SV dataset")
	}
	domain := ld.Name
	if domain == "" {
		domain = parts[0] + ld.Inst
	}
	p.DataSet = domain + "/LLN0$" + set.Name
	if err := s.svAddress(&p, parts); err != nil {
		return p, err
	}
	budget := 65536
	compiler := svCompiler{system: s, devices: ap.Server.Devices, budget: &budget}
	for _, fcda := range set.Members {
		member, err := compiler.member(fcda, ld.Inst)
		if err != nil {
			return p, err
		}
		for i := range member.Fields {
			member.Fields[i].Offset = p.Layout.Size
			p.Layout.Size += member.Fields[i].Width
		}
		if p.Layout.Size > sv.MaxAPDUSize {
			return p, fmt.Errorf("SV dataset exceeds Ethernet payload")
		}
		p.Layout.Members = append(p.Layout.Members, member)
	}
	// Include all configured optional fields and ASDUs in the MTU check.
	if _, err := p.NewFrame([6]byte{2, 0, 0, 0, 0, 1}); err != nil {
		return p, err
	}
	return p, nil
}

func (s *System) svAddress(p *sv.Profile, parts []string) error {
	var route *smvXML
	for _, connection := range s.doc.Connections {
		if connection.IED == parts[0] && connection.AP == parts[1] {
			for _, smv := range connection.SMV {
				if smv.LD == parts[2] && smv.Block == parts[3] {
					if route != nil {
						return fmt.Errorf("ambiguous SMV communication address")
					}
					smv := smv
					route = &smv
				}
			}
		}
	}
	if route == nil {
		return fmt.Errorf("missing SMV communication address")
	}
	params, err := parameters(route.Address)
	if err != nil {
		return err
	}
	mac, err := net.ParseMAC(strings.ReplaceAll(params["MAC-Address"], "-", ":"))
	if err != nil || len(mac) != 6 || mac[0]&1 == 0 || mac.String() == "ff:ff:ff:ff:ff:ff" {
		return fmt.Errorf("SV requires multicast destination MAC")
	}
	copy(p.Destination[:], mac)
	appid, err := strconv.ParseUint(params["APPID"], 16, 16)
	if err != nil || appid < 0x4000 || appid > 0x7fff {
		return fmt.Errorf("SV APPID must be hexadecimal 4000..7FFF")
	}
	p.APPID = uint16(appid)
	if raw, ok := params["VLAN-ID"]; ok {
		id, err := strconv.ParseUint(raw, 16, 12)
		if err != nil || id > 4094 {
			return fmt.Errorf("invalid SV VLAN-ID")
		}
		p.VLAN = &sv.VLAN{ID: uint16(id), Priority: 4}
		if raw, ok := params["VLAN-PRIORITY"]; ok {
			n, err := strconv.ParseUint(raw, 10, 3)
			if err != nil {
				return fmt.Errorf("invalid SV VLAN priority")
			}
			p.VLAN.Priority = uint8(n)
		}
	} else if _, ok := params["VLAN-PRIORITY"]; ok {
		return fmt.Errorf("VLAN priority requires VLAN-ID")
	}
	return nil
}

type svCompiler struct {
	system  *System
	devices []logicalDevice
	budget  *int
}

func (c svCompiler) member(f memberXML, defaultLD string) (sv.Member, error) {
	var m sv.Member
	if f.Index != "" {
		return m, fmt.Errorf("indexed FCDA ix is unsupported; select the array and a field path")
	}
	if f.LD == "" {
		f.LD = defaultLD
	}
	devices, err := index(c.devices, func(d logicalDevice) string { return d.Inst })
	if err != nil {
		return m, err
	}
	ld, ok := devices[f.LD]
	if !ok {
		return m, fmt.Errorf("unknown FCDA LDevice")
	}
	lnName := f.Prefix + f.Class + f.Inst
	nodes, err := index(append(append([]logicalNode(nil), ld.Zero...), ld.Nodes...), func(n logicalNode) string { return n.name() })
	if err != nil {
		return m, err
	}
	ln, ok := nodes[lnName]
	if !ok || f.FC == "" || f.DO == "" {
		return m, fmt.Errorf("unresolved FCDA logical node or FC")
	}
	typeLN, ok := c.system.lnTypes[ln.Type]
	if !ok {
		return m, fmt.Errorf("unknown LNodeType")
	}
	objects, err := index(typeLN.Objects, func(d declaration) string { return d.Name })
	if err != nil {
		return m, err
	}
	parts := strings.Split(f.DO, ".")
	object, ok := objects[parts[0]]
	if !ok || object.Count != "" {
		return m, fmt.Errorf("unresolved or array DO")
	}
	typ, ok := c.system.doTypes[object.Type]
	if !ok {
		return m, fmt.Errorf("unknown DOType")
	}
	for _, part := range parts[1:] {
		subs, err := index(typ.Sub, func(d declaration) string { return d.Name })
		if err != nil {
			return m, err
		}
		sub, ok := subs[part]
		if !ok || sub.Count != "" {
			return m, fmt.Errorf("unresolved or indexed array SDO")
		}
		typ, ok = c.system.doTypes[sub.Type]
		if !ok {
			return m, fmt.Errorf("unknown SDO type")
		}
	}
	m.Domain = f.LD
	if f.LD == defaultLD {
		m.Domain = "local"
	}
	m.Name = lnName + "$" + f.FC + "$" + strings.ReplaceAll(f.DO, ".", "$")
	fields, err := c.flatten(typ.order, nil, f.FC, "", 0)
	if err != nil {
		return m, err
	}
	if f.DA != "" {
		m.Name += "$" + strings.ReplaceAll(f.DA, ".", "$")
	}
	for _, field := range fields {
		if f.DA == "" {
			m.Fields = append(m.Fields, field)
		} else if field.Path == f.DA {
			field.Path = ""
			m.Fields = append(m.Fields, field)
		} else if suffix, ok := strings.CutPrefix(field.Path, f.DA+"."); ok {
			field.Path = suffix
			m.Fields = append(m.Fields, field)
		}
	}
	if len(m.Fields) == 0 {
		return m, fmt.Errorf("unresolved SV FCDA %s", m.Name)
	}
	return m, nil
}

func (c svCompiler) flatten(ordered []orderedAttribute, bda []attribute, fc, prefix string, depth int) ([]sv.Field, error) {
	if c.budget == nil {
		budget := 65536
		c.budget = &budget
	}
	if depth > 24 {
		return nil, fmt.Errorf("recursive or excessively nested SV type")
	}
	if bda != nil {
		for _, a := range bda {
			ordered = append(ordered, orderedAttribute{attribute: a})
		}
	}
	var out []sv.Field
	seen := map[string]bool{}
	for _, field := range ordered {
		a := field.attribute
		if !field.sub && bda == nil && a.FC != fc {
			continue
		}
		if a.Name == "" || seen[a.Name] {
			return nil, fmt.Errorf("empty or duplicate SV component %q", a.Name)
		}
		seen[a.Name] = true
		count := 1
		if a.Count != "" {
			n, err := svNumber(a.Count, "array count", 1, sv.MaxAPDUSize)
			if err != nil {
				return nil, err
			}
			count = int(n)
		}
		for i := 0; i < count; i++ {
			*c.budget--
			if *c.budget < 0 {
				return nil, fmt.Errorf("SV type expansion budget exceeded")
			}
			path := prefix + a.Name
			if a.Count != "" {
				path += "." + strconv.Itoa(i)
			}
			if field.sub || a.Basic == "Struct" {
				var children []sv.Field
				var err error
				if field.sub {
					typ, ok := c.system.doTypes[a.Type]
					if !ok {
						return nil, fmt.Errorf("missing SV DOType %q", a.Type)
					}
					children, err = c.flatten(typ.order, nil, fc, path+".", depth+1)
				} else {
					typ, ok := c.system.daTypes[a.Type]
					if !ok {
						return nil, fmt.Errorf("missing SV DAType %q", a.Type)
					}
					children, err = c.flatten(nil, typ.Attrs, fc, path+".", depth+1)
				}
				if err != nil {
					return nil, err
				}
				out = append(out, children...)
			} else {
				width, err := sv.BasicWidth(a.Basic)
				if err != nil {
					return nil, err
				}
				out = append(out, sv.Field{Path: path, Basic: a.Basic, Width: width})
			}
			if len(out) > sv.MaxAPDUSize {
				return nil, fmt.Errorf("SV type expansion exceeds payload limit")
			}
		}
	}
	return out, nil
}
