// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/sv"
)

type SampledInput struct {
	Profile sv.Profile
	Field   sv.Field
	Quality *sv.Field
}

// SVInput resolves one scalar SMV ExtRef.intAddr, locating its source AP,
// control block, FCDA and field offset. Ambiguous sources are rejected.
// The receiving application supplies the acquisition/calculation policy.
func (s *System) SVInput(receiver, ap, intAddr string) (SampledInput, error) {
	refs, err := s.SVInputs(receiver, ap)
	if err != nil {
		return SampledInput{}, err
	}
	var matches []InputReference
	for _, ref := range refs {
		if intAddr != "" && ref.IntAddr == intAddr {
			matches = append(matches, ref)
		}
	}
	if len(matches) != 1 {
		return SampledInput{}, fmt.Errorf("scl: expected one SMV ExtRef %s/%s/%s", receiver, ap, intAddr)
	}
	x := matches[0]
	var results []SampledInput
	for _, point := range s.AccessPoints() {
		if point.IED != x.SourceIED || !point.HasServer {
			continue
		}
		profiles, err := s.SVStreams(point.IED, point.AP)
		if err != nil {
			return SampledInput{}, err
		}
		domain := x.DataLD
		if domain == x.ControlLD {
			domain = "local"
		}
		for _, p := range profiles {
			if p.Reference != point.IED+"/"+point.AP+"/"+x.ControlLD+"/"+x.ControlBlock || x.ControlLN != "LLN0" {
				continue
			}
			want := x.DataLN + "$MX$" + strings.ReplaceAll(x.DataPath, ".", "$")
			for _, member := range p.Layout.Members {
				if member.Domain != domain {
					continue
				}
				path, ok := strings.CutPrefix(want, member.Name+"$")
				if want == member.Name {
					path = ""
					ok = true
				}
				if !ok {
					continue
				}
				field, err := p.Layout.Field(member.Name, strings.ReplaceAll(path, "$", "."))
				if err != nil {
					continue
				}
				q, err := p.Layout.QualityFor(field)
				if err != nil {
					return SampledInput{}, err
				}
				results = append(results, SampledInput{Profile: p, Field: field, Quality: q})
			}
		}
	}
	if len(results) != 1 {
		return SampledInput{}, fmt.Errorf("scl: SMV ExtRef %s resolves to %d scalar fields", intAddr, len(results))
	}
	return results[0], nil
}
