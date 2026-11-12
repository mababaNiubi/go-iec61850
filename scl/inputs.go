// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import "fmt"

// InputReference describes an ExtRef on a receiving logical node. Service
// retains the declared service type (for example GOOSE or SMV). Control and
// data LDs can differ. IntAddr is optional; Key identifies the scoped binding.
type InputReference struct {
	IntAddr                                       string
	SourceIED, ControlLD, ControlLN, ControlBlock string
	DataLD, DataLN, DataPath                      string
	ReceiverLD, ReceiverLN, Service               string
	Index                                         int // declaration position within the receiving LN's Inputs
}

// Key identifies an ExtRef even when intAddr is absent or reused on another LN.
func (r InputReference) Key() string {
	return fmt.Sprintf("%s/%s/%s/%d", r.ReceiverLD, r.ReceiverLN, r.Service, r.Index)
}

// Inputs lists all service types, preserving the receiving LD/LN and order.
func (s *System) Inputs(ied, ap string) ([]InputReference, error) { return s.inputs(ied, ap, "") }

// GOOSEInputs lists engineered GOOSE inputs in declaration order, including
// client-only LNs directly under an AccessPoint. Use IED.Input to resolve one
// of these references against the publishing server's dataset.
func (s *System) GOOSEInputs(ied, ap string) ([]InputReference, error) {
	return s.inputs(ied, ap, "GOOSE")
}

// SVInputs enumerates engineered sampled-value subscriber bindings.
func (s *System) SVInputs(ied, ap string) ([]InputReference, error) {
	return s.inputs(ied, ap, "SMV")
}
func (s *System) inputs(ied, ap, service string) ([]InputReference, error) {
	p, err := s.find(ied, ap)
	if err != nil {
		return nil, err
	}
	var refs []InputReference
	collect := func(ld string, nodes []logicalNode) {
		for _, ln := range nodes {
			for index, x := range ln.Inputs {
				if service != "" && x.Service != service {
					continue
				}
				path := x.DO
				if x.DA != "" {
					path += "." + x.DA
				}
				refs = append(refs, InputReference{IntAddr: x.Address, SourceIED: x.IED, ReceiverLD: ld, ReceiverLN: ln.name(), Service: x.Service, Index: index,
					ControlLD: x.SourceLD, ControlLN: x.SourcePrefix + x.SourceClass + x.SourceInst, ControlBlock: x.Block,
					DataLD: x.LD, DataLN: x.Prefix + x.Class + x.Inst, DataPath: path})
			}
		}
	}
	collect("", p.ClientNodes)
	if p.Server != nil {
		for _, ld := range p.Server.Devices {
			collect(ld.Inst, ld.Zero)
			collect(ld.Inst, ld.Nodes)
		}
	}
	return refs, nil
}
