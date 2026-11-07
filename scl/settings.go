// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

type settingXML struct {
	Count  uint32 `xml:"numOfSGs,attr"`
	Active uint32 `xml:"actSG,attr"`
}
type groupInitial struct{ domain, ln, path, group, text, basic, typ string }

func ungrouped(values []initial) []initial {
	var out []initial
	for _, v := range values {
		if v.Group == "" {
			out = append(out, v)
		}
	}
	return out
}
func (d *IED) loadSettingInitial() error {
	c := compiler{system: d.system, device: d}
	for _, x := range d.groupInitial {
		g := d.settings[x.domain]
		index, err := strconv.ParseUint(x.group, 10, 8)
		if g == nil || err != nil || index < 1 || index > uint64(len(g.groups)) {
			return fmt.Errorf("scl: invalid setting group at %s/%s.%s", x.domain, x.ln, x.path)
		}
		name := mms.DomainName(x.domain, x.ln+"$SG$"+strings.ReplaceAll(x.path, ".", "$"))
		v, err := c.scalar(attribute{declaration: declaration{Type: x.typ}, Basic: x.basic}, x.text)
		if err != nil {
			return err
		}
		base, path, spec, err := d.nestedType(name)
		if err != nil {
			return err
		}
		if err = mms.ValidateValue(spec, v.value); err != nil {
			return fmt.Errorf("scl: invalid group value %s: %w", name, err)
		}
		if len(path) == 0 {
			g.groups[index-1][base] = v.value
		} else {
			updated, err := data.WithComponent(g.groups[index-1][base], path, v.value)
			if err != nil {
				return err
			}
			g.groups[index-1][base] = updated
		}
	}
	for _, g := range d.settings {
		// Initial engineering values establish the baseline, not historical
		// events. Only subsequent remote confirmation/activation publishes.
		if err := d.model.SetValues(context.Background(), settingAssignments(g.groups[g.active-1])...); err != nil {
			return err
		}
	}
	return nil
}

type settingGroup struct {
	name         mms.ObjectName
	groups       []map[mms.ObjectName]mms.Value
	active, edit uint32
	owner        *mms.Association
	deadline     time.Time
	staged       map[mms.ObjectName]mms.Value
	changed      time.Time
}

func fcOf(n mms.ObjectName) string {
	parts := strings.Split(n.Item, "$")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
func settingName(n mms.ObjectName, fc string) mms.ObjectName {
	p := strings.Split(n.Item, "$")
	p[1] = fc
	n.Item = strings.Join(p, "$")
	return n
}

func (d *IED) addSettings(ld string, x *settingXML) error {
	if x == nil {
		return nil
	}
	if x.Count < 1 || x.Count > 255 {
		return fmt.Errorf("scl: numOfSGs must be 1..255")
	}
	active := x.Active
	if active == 0 {
		active = 1
	}
	if active > x.Count {
		return fmt.Errorf("scl: actSG outside setting groups")
	}
	domain := d.domains[ld]
	g := &settingGroup{name: mms.DomainName(domain, "LLN0$SP$SGCB"), groups: make([]map[mms.ObjectName]mms.Value, x.Count), active: active, changed: time.Now()}
	for i := range g.groups {
		g.groups[i] = map[mms.ObjectName]mms.Value{}
	}
	for name, spec := range d.types {
		if name.Domain == domain && fcOf(name) == "SG" && spec.Kind != mms.KindStructure {
			v, err := d.model.Read(context.Background(), mms.Access(name))
			if err != nil {
				return err
			}
			for i := range g.groups {
				g.groups[i][name] = v
			}
		}
	}
	if err := d.addService(g.name, []mms.Component{{Name: "NumOfSG", Type: uintSpec(8)}, {Name: "ActSG", Type: uintSpec(8)}, {Name: "EditSG", Type: uintSpec(8)}, {Name: "CnfEdit", Type: boolSpec()}, {Name: "LActTm", Type: mms.TypeSpec{Kind: mms.KindUTC}}, {Name: "ResvTms", Type: uintSpec(16)}}, []mms.Value{mms.Unsigned(uint64(x.Count)), mms.Unsigned(uint64(active)), mms.Unsigned(0), mms.Boolean(false), mms.UTC(g.changed), mms.Unsigned(0)}); err != nil {
		return err
	}
	d.settings[domain] = g
	return nil
}
func (g *settingGroup) releaseExpired() {
	if g.owner != nil && (g.owner.Context().Err() != nil || !time.Now().Before(g.deadline)) {
		g.owner = nil
		g.edit = 0
		g.staged = nil
	}
}
func (d *IED) settingAccessLocked(ctx context.Context, n mms.ObjectName, value *mms.Value) (mms.Value, bool, error) {
	g := d.settings[n.Domain]
	if g == nil {
		return mms.Value{}, false, nil
	}
	g.releaseExpired()
	fc := fcOf(n)
	base := g.name.Item
	if fc == "SG" || fc == "SE" {
		if value == nil {
			if fc == "SG" {
				v, err := d.model.Read(ctx, mms.Access(n))
				return v, true, err
			}
			if g.edit == 0 || g.owner != mms.AssociationFromContext(ctx) {
				return mms.Value{}, true, mms.TemporarilyUnavailable
			}
			source := settingName(n, "SG")
			if v, ok := g.staged[source]; ok {
				return v, true, nil
			}
			// Composite reads are assembled from the edit copy, keeping the
			// active SG view intact until activation.
			v, err := d.settingValueLocked(source, g.staged)
			return v, true, err
		}
		if fc != "SE" || g.edit == 0 || g.owner != mms.AssociationFromContext(ctx) || d.isReadOnly(n) {
			return mms.Value{}, true, mms.ObjectAccessDenied
		}
		spec, ok := d.types[n]
		if !ok {
			return mms.Value{}, true, mms.ObjectNonExistent
		}
		if err := mms.ValidateValue(spec, *value); err != nil {
			return mms.Value{}, true, mms.TypeInconsistent
		}
		if spec.Kind == mms.KindStructure {
			return mms.Value{}, true, mms.ObjectAccessUnsupported
		}
		g.staged[settingName(n, "SG")] = *value
		g.deadline = time.Now().Add(60 * time.Second)
		return mms.Value{}, true, nil
	}
	if n.Item != base && !strings.HasPrefix(n.Item, base+"$") {
		return mms.Value{}, false, nil
	}
	field := strings.TrimPrefix(n.Item, base+"$")
	if value == nil {
		if n.Item == base {
			var vs []mms.Value
			for _, c := range d.types[g.name].Components {
				v, _, err := d.settingAccessLocked(ctx, mms.DomainName(n.Domain, base+"$"+c.Name), nil)
				if err != nil {
					return mms.Value{}, true, err
				}
				vs = append(vs, v)
			}
			return mms.Structure(vs...), true, nil
		}
		switch field {
		case "ActSG":
			return mms.Unsigned(uint64(g.active)), true, nil
		case "EditSG":
			return mms.Unsigned(uint64(g.edit)), true, nil
		case "ResvTms":
			remaining := uint64(0)
			if g.owner != nil {
				remaining = uint64(time.Until(g.deadline)/time.Second) + 1
			}
			return mms.Unsigned(remaining), true, nil
		case "LActTm":
			return mms.UTC(g.changed), true, nil
		}
		v, err := d.model.Read(ctx, mms.Access(n))
		return v, true, err
	}
	a := mms.AssociationFromContext(ctx)
	if a == nil {
		return mms.Value{}, true, mms.ObjectAccessDenied
	}
	if err := mms.ValidateValue(d.types[n], *value); err != nil {
		return mms.Value{}, true, mms.TypeInconsistent
	}
	switch field {
	case "EditSG":
		index, _ := value.Uint()
		if index > uint64(len(g.groups)) {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		if g.owner != nil && g.owner != a {
			return mms.Value{}, true, mms.TemporarilyUnavailable
		}
		if index == 0 {
			g.owner = nil
			g.edit = 0
			g.staged = nil
			break
		}
		g.owner = a
		g.edit = uint32(index)
		g.deadline = time.Now().Add(60 * time.Second)
		g.staged = map[mms.ObjectName]mms.Value{}
		for name, v := range g.groups[index-1] {
			g.staged[name] = v
		}
	case "CnfEdit":
		on, _ := value.Bool()
		if !on {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		if g.owner != a || g.edit == 0 {
			return mms.Value{}, true, mms.ObjectAccessDenied
		}
		if g.edit == g.active {
			if err := d.applySettingLocked(ctx, g.staged); err != nil {
				return mms.Value{}, true, err
			}
		}
		g.groups[g.edit-1] = g.staged
		g.staged = nil
		g.owner = nil
		g.edit = 0
	case "ActSG":
		index, _ := value.Uint()
		if index < 1 || index > uint64(len(g.groups)) {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		if err := d.applySettingLocked(ctx, g.groups[index-1]); err != nil {
			return mms.Value{}, true, err
		}
		g.active = uint32(index)
		g.changed = time.Now()
	default:
		return mms.Value{}, true, mms.ObjectAccessDenied
	}
	return mms.Value{}, true, nil
}
func (d *IED) applySettingLocked(ctx context.Context, values map[mms.ObjectName]mms.Value) error {
	return d.commitLocked(ctx, settingAssignments(values)...)
}

func settingAssignments(values map[mms.ObjectName]mms.Value) []mms.Assignment {
	changes := make([]mms.Assignment, 0, len(values))
	for name, v := range values {
		changes = append(changes, mms.Assignment{Name: name, Value: v})
	}
	return changes
}
func (d *IED) settingValueLocked(n mms.ObjectName, values map[mms.ObjectName]mms.Value) (mms.Value, error) {
	if v, ok := values[n]; ok {
		return v, nil
	}
	spec, ok := d.types[n]
	if !ok {
		return mms.Value{}, mms.ObjectNonExistent
	}
	var vs []mms.Value
	for _, c := range spec.Components {
		child := mms.DomainName(n.Domain, n.Item+"$"+c.Name)
		v, err := d.settingValueLocked(child, values)
		if err != nil {
			return mms.Value{}, err
		}
		vs = append(vs, v)
	}
	if spec.Kind != mms.KindStructure {
		return mms.Value{}, mms.ObjectNonExistent
	}
	return mms.Structure(vs...), nil
}
