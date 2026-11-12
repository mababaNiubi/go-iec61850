// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// ReportDeclaration is available even when an unsupported DO prevents Device
// construction. Clients identify actual indexed RCB instances online.
type ReportDeclaration struct {
	LD, LN, Name, DataSet string
	ConfRev               uint32
	Buffered              bool
	Clients               []NodeReference
}

// Project is an offline engineering inspection, including network topology, primary equipment
// and supported semantic diagnostics.
type Project struct {
	Version, Revision, Release string
	Networks                   []Network
	AccessPoints               []AccessPoint
	Equipment                  []Equipment
	Diagnostics                []Diagnostic
}

// Project inspects engineering references without starting any transport. It
// performs the supported semantic checks, not XSD or full NSD validation.
func (s *System) Project() Project {
	p := Project{Version: s.doc.Version, Revision: s.doc.Revision, Release: s.doc.Release, Networks: s.Networks(), AccessPoints: s.AccessPoints(), Equipment: s.Equipment(), Diagnostics: []Diagnostic{}}
	for _, ap := range p.AccessPoints {
		if _, err := s.ServerAccessPoint(ap.IED, ap.AP); err != nil {
			p.Diagnostics = append(p.Diagnostics, Diagnostic{ap.IED + "/" + ap.AP, err.Error()})
			continue
		}
		if ap.HasServer {
			c, err := s.Catalog(ap.IED, ap.AP)
			if err != nil {
				p.Diagnostics = append(p.Diagnostics, Diagnostic{ap.IED + "/" + ap.AP, err.Error()})
			} else {
				p.Diagnostics = append(p.Diagnostics, c.Diagnostics...)
			}
		}
	}
	for _, c := range s.doc.Connections {
		if _, err := s.find(c.IED, c.AP); err != nil {
			p.Diagnostics = append(p.Diagnostics, Diagnostic{c.Network + "/" + c.IED + "/" + c.AP, err.Error()})
		}
	}
	return p
}

// Difference identifies an added, removed or changed catalog entry. Reference locates the
// entry and Before/After describe the compared declarations.
type Difference struct{ Reference, Kind, Before, After string }

// CompareCatalog compares engineering meaning, including ordered FCDA
// selections and declared types. A missing object is an explicit difference.
func CompareCatalog(before, after Catalog) []Difference {
	flatten := func(c Catalog) map[string]string {
		out := map[string]string{}
		for _, p := range c.Points {
			raw, _ := mms.EncodeType(p.Type)
			out["point:"+p.Name.String()] = fmt.Sprintf("%x|%s|%s|%s|%s", raw, p.CDC, p.Unit, p.Quality, p.Timestamp)
		}
		for _, s := range c.DataSets {
			var members []string
			for _, a := range s.Variables {
				steps, _ := a.Selection.Steps()
				var path []string
				for _, step := range steps {
					if step.Index != nil {
						path = append(path, fmt.Sprint(*step.Index))
					} else {
						path = append(path, step.Component)
					}
				}
				members = append(members, a.Name.String()+"("+strings.Join(path, "/")+")")
			}
			out["dataset:"+s.Name.String()] = strings.Join(members, ",")
		}
		for _, r := range c.Reports {
			out["report:"+r.LD+"/"+r.LN+"/"+r.Name] = fmt.Sprintf("%s|%d|%t|%v", r.DataSet, r.ConfRev, r.Buffered, r.Clients)
		}
		for _, r := range c.Inputs {
			out["input:"+r.Key()] = fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s", r.SourceIED, r.ControlLD, r.ControlLN, r.ControlBlock, r.DataLD, r.DataLN, r.DataPath)
		}
		return out
	}
	a, b := flatten(before), flatten(after)
	var out []Difference
	for k, v := range a {
		next, ok := b[k]
		if !ok {
			out = append(out, Difference{k, "removed", v, ""})
		} else if next != v {
			out = append(out, Difference{k, "changed", v, next})
		}
	}
	for k, v := range b {
		if _, ok := a[k]; !ok {
			out = append(out, Difference{k, "added", "", v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reference < out[j].Reference })
	return out
}
