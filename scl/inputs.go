// SPDX-License-Identifier: GPL-3.0-or-later
package scl

// InputReference describes a GOOSE ExtRef on a receiving logical node. Control
// and data LDs can be different. IntAddr is the receiver's application name.
type InputReference struct {
	IntAddr                                       string
	SourceIED, ControlLD, ControlLN, ControlBlock string
	DataLD, DataLN, DataPath                      string
}

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
	collect := func(nodes []logicalNode) {
		for _, ln := range nodes {
			for _, x := range ln.Inputs {
				if x.Service != service {
					continue
				}
				path := x.DO
				if x.DA != "" {
					path += "." + x.DA
				}
				refs = append(refs, InputReference{IntAddr: x.Address, SourceIED: x.IED,
					ControlLD: x.SourceLD, ControlLN: x.SourcePrefix + x.SourceClass + x.SourceInst, ControlBlock: x.Block,
					DataLD: x.LD, DataLN: x.Prefix + x.Class + x.Inst, DataPath: path})
			}
		}
	}
	collect(p.ClientNodes)
	if p.Server != nil {
		for _, ld := range p.Server.Devices {
			collect(ld.Zero)
			collect(ld.Nodes)
		}
	}
	return refs, nil
}
