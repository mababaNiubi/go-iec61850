// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mababaNiubi/go-iec61850/mms"
)

type substationXML struct {
	Name         string         `xml:"name,attr"`
	Transformers []equipmentXML `xml:"PowerTransformer"`
	Levels       []struct {
		Name         string         `xml:"name,attr"`
		Transformers []equipmentXML `xml:"PowerTransformer"`
		Bays         []struct {
			Name         string         `xml:"name,attr"`
			Equipment    []equipmentXML `xml:"ConductingEquipment"`
			Transformers []equipmentXML `xml:"PowerTransformer"`
			Connections  []equipmentXML `xml:"ConnectivityNode"`
		} `xml:"Bay"`
	} `xml:"VoltageLevel"`
}

type equipmentXML struct {
	Name      string          `xml:"name,attr"`
	Type      string          `xml:"type,attr"`
	Nodes     []NodeReference `xml:"LNode"`
	Terminals []Terminal      `xml:"Terminal"`
	Windings  []equipmentXML  `xml:"TransformerWinding"`
	Sub       []equipmentXML  `xml:"SubEquipment"`
}

// Terminal keeps the engineered connection to a ConnectivityNode.
type Terminal struct {
	Name             string `xml:"name,attr"`
	ConnectivityNode string `xml:"connectivityNode,attr"`
	Substation       string `xml:"substationName,attr"`
	VoltageLevel     string `xml:"voltageLevelName,attr"`
	Bay              string `xml:"bayName,attr"`
	Node             string `xml:"cNodeName,attr"`
}

// NodeReference connects primary equipment to an engineered logical node.
type NodeReference struct {
	IED      string `xml:"iedName,attr"`
	LD       string `xml:"ldInst,attr"`
	Prefix   string `xml:"prefix,attr"`
	Class    string `xml:"lnClass,attr"`
	Instance string `xml:"lnInst,attr"`
}

// Equipment connects a primary-equipment path to logical nodes and terminal connectivity
// declarations.
type Equipment struct {
	Path, Type string
	Nodes      []NodeReference
	Terminals  []Terminal
}

// Equipment returns the station/voltage-level/bay/equipment hierarchy without
// instantiating a server. This also works for an engineering-only SCL document.
func (s *System) Equipment() []Equipment {
	var out []Equipment
	var collect func(string, string, []equipmentXML)
	collect = func(parent, kind string, equipment []equipmentXML) {
		for _, item := range equipment {
			path := parent + "/" + item.Name
			typ := item.Type
			if typ == "" {
				typ = kind
			}
			out = append(out, Equipment{Path: path, Type: typ, Nodes: append([]NodeReference(nil), item.Nodes...), Terminals: append([]Terminal(nil), item.Terminals...)})
			collect(path, "TransformerWinding", item.Windings)
			collect(path, "SubEquipment", item.Sub)
		}
	}
	for _, station := range s.doc.Substations {
		collect(station.Name, "PTR", station.Transformers)
		for _, level := range station.Levels {
			base := station.Name + "/" + level.Name
			collect(base, "PTR", level.Transformers)
			for _, bay := range level.Bays {
				path := base + "/" + bay.Name
				collect(path, "ConductingEquipment", bay.Equipment)
				collect(path, "PTR", bay.Transformers)
				collect(path, "ConnectivityNode", bay.Connections)
			}
		}
	}
	return out
}

// ModelPoint retains IEC semantics alongside its checked MMS representation.
// Unit is the SCL declaration, when present. A gateway may apply an explicit
// conversion while retaining the source unit and quality separately.
type ModelPoint struct {
	Point
	Type               mms.TypeSpec
	CDC, Description   string
	BasicType          string
	EnumValues         map[int64]string
	Unit               string
	Quality, Timestamp mms.ObjectName
	Equipment          []string
}

// Diagnostic locates a declaration that could not be resolved or supported. Other catalog
// entries remain inspectable.
type Diagnostic struct{ Reference, Message string }

// Catalog is one selected IED/AP's typed point and dataset inventory with source semantics and
// partial-model diagnostics.
type Catalog struct {
	Version, Revision, Release string
	Points                     []ModelPoint
	Equipment                  []Equipment
	Diagnostics                []Diagnostic
	DataSets                   []mms.NamedList
	Reports                    []ReportDeclaration
	Inputs                     []InputReference
	ReportSettings             *ReportSettings
}

// Catalog inspects each DO independently. An unsupported DO is a diagnostic
// and does not hide unrelated points. Device still requires a runnable model.
// No schema or NSD conformance is implied by this structural inspection.
func (s *System) Catalog(ied, ap string) (Catalog, error) {
	p, err := s.find(ied, ap)
	if err != nil {
		return Catalog{}, err
	}
	out := Catalog{Version: s.doc.Version, Revision: s.doc.Revision, Release: s.doc.Release, Equipment: s.Equipment()}
	out.Inputs, _ = s.Inputs(ied, ap)
	out.ReportSettings, err = s.ReportSettings(ied, ap)
	if err != nil {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{ied + "/" + ap, err.Error()})
	}
	if p.Server == nil {
		return out, nil
	}
	catalogDevice := newIED(s, ied, ap)
	for _, ld := range p.Server.Devices {
		domain := ld.Name
		if domain == "" {
			domain = ied + ld.Inst
		}
		catalogDevice.domains[ld.Inst] = domain
	}
	for _, ld := range p.Server.Devices {
		domain := ld.Name
		if domain == "" {
			domain = ied + ld.Inst
		}
		for _, ln := range append(append([]logicalNode(nil), ld.Zero...), ld.Nodes...) {
			for _, block := range ln.Reports {
				out.Reports = append(out.Reports, ReportDeclaration{LD: ld.Inst, LN: ln.name(), Name: block.Name, DataSet: block.Set, ConfRev: block.Revision, Buffered: block.Buffered, Clients: append([]NodeReference(nil), block.Enabled.Clients...)})
			}
			lt, ok := s.lnTypes[ln.Type]
			if !ok || lt.Class != ln.Class {
				out.Diagnostics = append(out.Diagnostics, Diagnostic{domain + "/" + ln.name(), "unresolved or mismatched LNodeType " + ln.Type})
				continue
			}
			for _, decl := range lt.Objects {
				node := ln
				node.Type = "catalog"
				copySystem := *s
				copySystem.lnTypes = map[string]lnType{"catalog": {ID: "catalog", Class: ln.Class, Objects: []declaration{decl}}}
				node.Objects = nil
				for _, instance := range ln.Objects {
					if instance.Name == decl.Name {
						node.Objects = append(node.Objects, instance)
					}
				}
				model := newIED(&copySystem, ied, ap)
				c := compiler{system: &copySystem, device: model, budget: 65536, metadata: map[string]attribute{}}
				if err := c.node(domain, node); err != nil {
					out.Diagnostics = append(out.Diagnostics, Diagnostic{domain + "/" + ln.name() + "." + decl.Name, err.Error()})
					continue
				}
				for name, spec := range model.types {
					catalogDevice.types[name] = spec
				}
				for _, point := range model.Points() {
					spec := model.types[point.Name]
					if spec.Kind == mms.KindStructure {
						continue
					}
					meta := ModelPoint{Point: point, Type: spec, CDC: s.doTypes[decl.Type].CDC, Description: decl.Description}
					if meta.Description == "" {
						meta.Description = s.doTypes[decl.Type].Description
					}
					path := strings.TrimPrefix(point.Reference, domain+"/"+ln.name()+".")
					attribute := c.metadata[path]
					if attribute.Description != "" {
						meta.Description = attribute.Description
					}
					meta.BasicType = attribute.Basic
					if attribute.Basic == "Enum" {
						meta.EnumValues = map[int64]string{}
						for _, entry := range s.enums[attribute.Type].Entries {
							number, err := strconv.ParseInt(entry.Ord, 10, 64)
							if err == nil {
								meta.EnumValues[number] = strings.TrimSpace(entry.Name)
							}
						}
					}
					prefix := point.Name.Item[:strings.LastIndex(point.Name.Item, "$")]
					for strings.Count(prefix, "$") >= 2 {
						q := mms.DomainName(domain, prefix+"$q")
						stamp := mms.DomainName(domain, prefix+"$t")
						if model.types[q].Kind == mms.KindBitString {
							meta.Quality = q
						}
						if model.types[stamp].Kind == mms.KindUTC {
							meta.Timestamp = stamp
						}
						if meta.Quality.Item != "" || meta.Timestamp.Item != "" {
							break
						}
						prefix = prefix[:strings.LastIndex(prefix, "$")]
					}
					unit := mms.DomainName(domain, ln.name()+"$CF$"+decl.Name+"$units")
					if value, err := model.Read(context.Background(), mms.Access(unit)); err == nil {
						meta.Unit = value.String()
					}
					for _, item := range out.Equipment {
						for _, n := range item.Nodes {
							if n.IED == ied && n.LD == ld.Inst && n.Prefix+n.Class+n.Instance == ln.name() {
								meta.Equipment = append(meta.Equipment, item.Path)
							}
						}
					}
					out.Points = append(out.Points, meta)
				}
			}
		}
	}
	for _, ld := range p.Server.Devices {
		for _, ln := range append(append([]logicalNode(nil), ld.Zero...), ld.Nodes...) {
			for _, set := range ln.Sets {
				if err := catalogDevice.addSet(ld.Inst, ln.name(), set); err != nil {
					out.Diagnostics = append(out.Diagnostics, Diagnostic{ld.Inst + "/" + ln.name() + "." + set.Name, err.Error()})
				}
			}
		}
	}
	out.DataSets = catalogDevice.DataSets()
	sort.Slice(out.Points, func(i, j int) bool { return out.Points[i].Name.String() < out.Points[j].Name.String() })
	return out, nil
}

// CheckOnline verifies the selected declarations against an actual peer. It
// reads types only and reports every mismatch, so importing a model never
// silently changes a live device. Dataset order is checked separately below.
func (c Catalog) CheckOnline(ctx context.Context, client *mms.Client) ([]Diagnostic, error) {
	if client == nil {
		return nil, fmt.Errorf("scl: nil online client")
	}
	// A partial catalog must not produce a clean full-model verdict merely
	// because unsupported declarations were absent from the online queries.
	out := append([]Diagnostic(nil), c.Diagnostics...)
	for _, p := range c.Points {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		t, err := client.GetVariableAccessAttributes(ctx, p.Name)
		if err != nil {
			out = append(out, Diagnostic{p.Reference, err.Error()})
			continue
		}
		want, _ := mms.EncodeType(p.Type)
		got, err := mms.EncodeType(t)
		if err != nil || string(want) != string(got) {
			out = append(out, Diagnostic{p.Reference, "online type differs from SCL"})
		}
	}
	for _, set := range c.DataSets {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := CheckDataSet(ctx, client, set); err != nil {
			out = append(out, Diagnostic{set.Name.String(), err.Error()})
		}
	}
	return out, nil
}

// CheckDataSet compares every FCDA in order, including alternate access.
func CheckDataSet(ctx context.Context, client *mms.Client, expected mms.NamedList) error {
	if client == nil {
		return fmt.Errorf("scl: nil online client")
	}
	actual, err := client.GetNamedListAttributes(ctx, expected.Name)
	if err != nil {
		return err
	}
	if len(actual.Variables) != len(expected.Variables) {
		return fmt.Errorf("scl: dataset %s member count differs", expected.Name)
	}
	for i, want := range expected.Variables {
		got := actual.Variables[i]
		if got.Name != want.Name || !got.Selection.Equal(want.Selection) {
			return fmt.Errorf("scl: dataset %s member %d differs", expected.Name, i)
		}
	}
	return nil
}
