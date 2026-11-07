// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// Device resolves a single access point into an independent MMS store and
// its configured GOOSE streams. Other IEDs may use different profiles.
func (s *System) Device(iedName, apName string) (*IED, error) {
	ap, err := s.find(iedName, apName)
	if err != nil {
		return nil, err
	}
	if ap.Server == nil {
		return nil, fmt.Errorf("scl: access point %s/%s does not host a local Server", iedName, apName)
	}
	d := newIED(s, iedName, apName)
	c := compiler{system: s, device: d, budget: 65536}
	if len(ap.Server.Devices) == 0 {
		return nil, fmt.Errorf("scl: %s/%s has no LDevice", iedName, apName)
	}
	seenDomains := map[string]bool{}
	// Index every domain before resolving cross-LDevice dataset references.
	for _, ld := range ap.Server.Devices {
		domain := ld.Name
		if domain == "" {
			domain = iedName + ld.Inst
		}
		if !identifier(ld.Inst) || !identifier(domain) || !data.ValidIdentifier(domain) || d.domains[ld.Inst] != "" || seenDomains[domain] {
			return nil, fmt.Errorf("scl: invalid or duplicate LDevice %q (domain %q)", ld.Inst, domain)
		}
		d.domains[ld.Inst] = domain
		seenDomains[domain] = true
		if len(ld.Zero) != 1 || ld.Zero[0].Class != "LLN0" || ld.Zero[0].name() != "LLN0" {
			return nil, fmt.Errorf("scl: %s needs one LLN0", domain)
		}
		for _, ln := range append([]logicalNode{ld.Zero[0]}, ld.Nodes...) {
			if err = c.node(domain, ln); err != nil {
				return nil, fmt.Errorf("scl: %s/%s: %w", domain, ln.name(), err)
			}
		}
	}
	if err = d.configureCommunication(ap); err != nil {
		return nil, fmt.Errorf("scl: %s/%s: %w", iedName, apName, err)
	}
	return d, nil
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

type compiler struct {
	system    *System
	device    *IED
	owner     mms.ObjectName
	budget    int
	initial   map[string]string
	enumCache map[string]enumValues
	metadata  map[string]attribute
}

func (c *compiler) charge(depth int) error {
	c.budget--
	if depth > 32 || c.budget < 0 {
		return fmt.Errorf("cyclic, deeply nested or oversized type expansion")
	}
	return nil
}

// branch groups DO/DA declarations under their functional constraint while
// preserving the declaration order of siblings in each MMS structure.
type branch struct {
	name     string
	children []*branch
	index    map[string]*branch
	leaf     *typedValue
}
type typedValue struct {
	spec  mms.TypeSpec
	value mms.Value
}

func (b *branch) insert(parts []string, leaf typedValue) error {
	for _, part := range parts {
		if !identifier(part) || b.leaf != nil {
			return fmt.Errorf("invalid/conflicting component %q", part)
		}
		if b.index == nil {
			b.index = make(map[string]*branch)
		}
		child := b.index[part]
		if child == nil {
			child = &branch{name: part}
			b.index[part] = child
			b.children = append(b.children, child)
		}
		b = child
	}
	if b.leaf != nil || len(b.children) > 0 {
		return fmt.Errorf("duplicate component %s", strings.Join(parts, "."))
	}
	b.leaf = &leaf
	return nil
}
func (b *branch) build() typedValue {
	if b.leaf != nil {
		return *b.leaf
	}
	spec := mms.TypeSpec{Kind: mms.KindStructure}
	values := make([]mms.Value, 0, len(b.children))
	for _, child := range b.children {
		f := child.build()
		spec.Components = append(spec.Components, mms.Component{Name: child.name, Type: f.spec})
		values = append(values, f.value)
	}
	return typedValue{spec, mms.Structure(values...)}
}

func (c *compiler) node(domain string, ln logicalNode) error {
	c.owner = mms.DomainName(domain, ln.name())
	t, ok := c.system.lnTypes[ln.Type]
	if !ok || ln.Class != t.Class || !identifier(ln.name()) {
		return fmt.Errorf("unresolved or mismatched LNodeType %q", ln.Type)
	}
	c.initial = make(map[string]string)
	for _, obj := range ln.Objects {
		if err := c.instance("", obj); err != nil {
			return err
		}
	}
	var root branch
	seen := make(map[string]bool)
	for _, obj := range t.Objects {
		if seen[obj.Name] {
			return fmt.Errorf("duplicate DO %s", obj.Name)
		}
		seen[obj.Name] = true
		if err := c.object(&root, obj, obj.Name, 1); err != nil {
			return fmt.Errorf("%s: %w", obj.Name, err)
		}
	}
	for path := range c.initial {
		return fmt.Errorf("instance value for unknown attribute %s", path)
	}
	if len(root.children) == 0 {
		return fmt.Errorf("empty LNodeType %q", ln.Type)
	}
	v := root.build()
	name := mms.DomainName(domain, ln.name())
	if err := c.device.model.Add(mms.Variable{Name: name, Type: v.spec, Value: v.value}); err != nil {
		return err
	}
	return c.device.indexType(name, v.spec)
}

func (c *compiler) instance(parent string, n instance) error {
	if !identifier(n.Name) {
		return fmt.Errorf("invalid or indexed instance %q", n.Name)
	}
	path := n.Name
	if parent != "" {
		path = parent + "." + path
	}
	if n.Index != "" {
		index, err := strconv.ParseUint(n.Index, 10, 16)
		if err != nil {
			return fmt.Errorf("invalid instance index %q", n.Index)
		}
		path += "." + strconv.FormatUint(index, 10)
	}
	if n.ValueKind == "RO" || n.ValueKind == "Conf" {
		c.device.readOnlyPaths = append(c.device.readOnlyPaths, c.owner.Domain+"/"+c.owner.Item+"."+path)
	}
	if len(n.Values) > 0 {
		values := n.Values
		groups := map[string]bool{}
		for _, v := range values {
			if v.Group != "" {
				if groups[v.Group] {
					return fmt.Errorf("duplicate setting group value at %s", path)
				}
				groups[v.Group] = true
				c.device.groupInitial = append(c.device.groupInitial, groupInitial{domain: c.owner.Domain, ln: c.owner.Item, path: path, group: v.Group, text: v.Text})
			}
		}
		values = ungrouped(values)
		if len(values) > 1 {
			return fmt.Errorf("%s: duplicate initial value", path)
		}
		if _, ok := c.initial[path]; ok {
			return fmt.Errorf("duplicate instance value %s", path)
		}
		if len(values) == 1 {
			c.initial[path] = values[0].Text
		}
	}
	for _, child := range n.SDI {
		if err := c.instance(path, child); err != nil {
			return err
		}
	}
	for _, child := range n.DAI {
		if err := c.instance(path, child); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) object(root *branch, ref declaration, path string, depth int) error {
	if err := c.charge(depth); err != nil {
		return err
	}
	t, ok := c.system.doTypes[ref.Type]
	if !ok {
		return fmt.Errorf("unknown DOType %q", ref.Type)
	}
	if !identifier(ref.Name) {
		return fmt.Errorf("invalid DO/SDO %q", ref.Name)
	}
	if ref.Count != "" && ref.Count != "0" {
		return c.arrayObject(root, ref, path, depth)
	}
	if len(t.Sub)+len(t.Attrs) == 0 {
		return fmt.Errorf("empty DOType %q", t.ID)
	}
	seen := map[string]bool{}
	for _, sub := range t.Sub {
		if seen[sub.Name] {
			return fmt.Errorf("duplicate %s.%s", path, sub.Name)
		}
		seen[sub.Name] = true
		if err := c.object(root, sub, path+"."+sub.Name, depth+1); err != nil {
			return err
		}
	}
	for _, a := range t.Attrs {
		if seen[a.Name] {
			return fmt.Errorf("duplicate %s.%s", path, a.Name)
		}
		seen[a.Name] = true
		childPath := path + "." + a.Name
		if !supportedFC(a.FC) {
			return fmt.Errorf("%s: unsupported FC %q", childPath, a.FC)
		}
		if a.FC == "CO" && a.Name == "Oper" && (t.CDC == "SPC" || t.CDC == "DPC") {
			c.device.binary[mms.DomainName(c.owner.Domain, c.owner.Item+"$CO$"+strings.ReplaceAll(childPath, ".", "$"))] = true
		}
		v, err := c.attribute(a, childPath, depth+1)
		if err != nil {
			return fmt.Errorf("%s: %w", childPath, err)
		}
		if err = root.insert(append([]string{a.FC}, strings.Split(childPath, ".")...), v); err != nil {
			return err
		}
		if a.FC == "SG" {
			if err = root.insert(append([]string{"SE"}, strings.Split(childPath, ".")...), v); err != nil {
				return err
			}
		}
		name := mms.DomainName(c.owner.Domain, c.owner.Item+"$"+a.FC+"$"+strings.ReplaceAll(childPath, ".", "$"))
		var reason byte
		if a.Dchg {
			reason |= 0x40
		}
		if a.Qchg {
			reason |= 0x20
		}
		if a.Dupd {
			reason |= 0x10
		}
		c.device.triggers[name] = reason
	}
	return nil
}
func supportedFC(fc string) bool {
	switch fc {
	case "ST", "MX", "SP", "SV", "CF", "DC", "CO", "EX", "BL", "OR", "SG", "SE", "SR":
		return true
	}
	return false
}
func (c *compiler) attribute(a attribute, path string, depth int) (typedValue, error) {
	if a.ValueKind == "RO" || a.ValueKind == "Conf" {
		c.device.readOnlyPaths = append(c.device.readOnlyPaths, c.owner.Domain+"/"+c.owner.Item+"."+path)
	}
	if c.metadata != nil {
		c.metadata[path] = a
	}
	if err := c.charge(depth); err != nil {
		return typedValue{}, err
	}
	if !identifier(a.Name) {
		return typedValue{}, fmt.Errorf("invalid DA/BDA %q", a.Name)
	}
	if a.Count != "" && a.Count != "0" {
		count, err := arrayCount(a.Count)
		if err != nil {
			return typedValue{}, err
		}
		a.Count = ""
		var values []mms.Value
		var element mms.TypeSpec
		for i := 0; i < count; i++ {
			v, err := c.attribute(a, path+"."+strconv.Itoa(i), depth+1)
			if err != nil {
				return typedValue{}, err
			}
			element = v.spec
			values = append(values, v.value)
		}
		return typedValue{mms.TypeSpec{Kind: mms.KindArray, Length: count, Element: &element}, mms.Array(values...)}, nil
	}
	for i := range c.device.groupInitial {
		v := &c.device.groupInitial[i]
		if v.domain == c.owner.Domain && v.ln == c.owner.Item && v.path == path {
			v.basic = a.Basic
			v.typ = a.Type
		}
	}
	groups := map[string]bool{}
	for _, v := range a.Values {
		if v.Group != "" {
			if groups[v.Group] {
				return typedValue{}, fmt.Errorf("duplicate setting group declaration at %s", path)
			}
			groups[v.Group] = true
			// Instance values take precedence over the template default.
			overridden := false
			for _, initial := range c.device.groupInitial {
				if initial.domain == c.owner.Domain && initial.ln == c.owner.Item && initial.path == path && initial.group == v.Group {
					overridden = true
					break
				}
			}
			if overridden {
				continue
			}
			c.device.groupInitial = append(c.device.groupInitial, groupInitial{domain: c.owner.Domain, ln: c.owner.Item, path: path, group: v.Group, text: v.Text, basic: a.Basic, typ: a.Type})
		}
	}
	a.Values = ungrouped(a.Values)
	if len(a.Values) > 1 {
		return typedValue{}, fmt.Errorf("duplicate attribute initial value")
	}
	raw := ""
	if len(a.Values) == 1 {
		raw = a.Values[0].Text
	}
	override, ok := c.initial[path]
	if ok {
		raw = override
		delete(c.initial, path)
	}
	if a.Basic == "PhyComAddr" {
		if ok || len(a.Values) > 0 {
			return typedValue{}, fmt.Errorf("PhyComAddr needs values on its components")
		}
		spec := mms.TypeSpec{Kind: mms.KindStructure}
		var values []mms.Value
		for _, field := range []attribute{{declaration: declaration{Name: "Addr"}, Basic: "Octet6"}, {declaration: declaration{Name: "PRIORITY"}, Basic: "INT8U"}, {declaration: declaration{Name: "VID"}, Basic: "INT16U"}, {declaration: declaration{Name: "APPID"}, Basic: "INT16U"}} {
			value, err := c.attribute(field, path+"."+field.Name, depth+1)
			if err != nil {
				return typedValue{}, err
			}
			if field.Name == "Addr" {
				bytes, _ := value.value.Bytes()
				if len(bytes) == 0 {
					bytes = make([]byte, 6)
				}
				value.spec.Length = 6
				value.value = mms.OctetString(bytes)
				if err := mms.ValidateValue(value.spec, value.value); err != nil {
					return typedValue{}, err
				}
			}
			spec.Components = append(spec.Components, mms.Component{Name: field.Name, Type: value.spec})
			values = append(values, value.value)
		}
		return typedValue{spec, mms.Structure(values...)}, nil
	}
	if a.Basic != "Struct" {
		return c.scalar(a, raw)
	}
	if ok || len(a.Values) > 0 {
		return typedValue{}, fmt.Errorf("a structure needs values on its BDA leaves")
	}
	t, ok := c.system.daTypes[a.Type]
	if !ok || len(t.Attrs) == 0 {
		return typedValue{}, fmt.Errorf("missing or empty DAType %q", a.Type)
	}
	spec := mms.TypeSpec{Kind: mms.KindStructure}
	values := make([]mms.Value, 0, len(t.Attrs))
	seen := map[string]bool{}
	for _, b := range t.Attrs {
		if seen[b.Name] {
			return typedValue{}, fmt.Errorf("duplicate BDA %s.%s", path, b.Name)
		}
		seen[b.Name] = true
		v, err := c.attribute(b, path+"."+b.Name, depth+1)
		if err != nil {
			return typedValue{}, fmt.Errorf("%s: %w", b.Name, err)
		}
		spec.Components = append(spec.Components, mms.Component{Name: b.Name, Type: v.spec})
		values = append(values, v.value)
	}
	return typedValue{spec, mms.Structure(values...)}, nil
}
