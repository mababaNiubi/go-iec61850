// SPDX-License-Identifier: GPL-3.0-or-later
package mms

// ServiceDescriptor names one MMS service the server implements. Rows map a
// service name to this codebase's own dispatch site; opcode tags and handler
// locations are verifiable from server.go, not claims about ISO 9506-2
// clause numbering. The negotiated service-support bytes in wire.go remain a
// separately reviewed constant: this table documents what those bytes
// advertise without re-deriving them, because mapping bits to services
// requires the licensed ISO 9506-2 text and stays TBD (BASE-01).
type ServiceDescriptor struct {
	Name string
	// Opcode is the confirmed-request tag dispatched in Server.dispatch.
	Opcode byte
	// Handled names the implementation site for services carried outside
	// the confirmed-request path.
	Handled string
}

// AdvertisedServices lists every MMS service the server advertises and
// dispatches. Adding a dispatch case without a row here (or advertising a
// service without a handler) is a profile change that must be reviewed
// deliberately.
func AdvertisedServices() []ServiceDescriptor {
	return []ServiceDescriptor{
		{Name: "status", Opcode: 0x80},
		{Name: "getNameList", Opcode: 0xa1},
		{Name: "identify", Opcode: 0x82},
		{Name: "read", Opcode: 0xa4},
		{Name: "write", Opcode: 0xa5},
		{Name: "getVariableAccessAttributes", Opcode: 0xa6},
		{Name: "defineNamedVariableList", Opcode: 0xab},
		{Name: "getNamedVariableListAttributes", Opcode: 0xac},
		{Name: "deleteNamedVariableList", Opcode: 0xad},
		{Name: "conclude", Opcode: 0, Handled: "association 0x8b/0x8c"},
		{Name: "informationReport", Opcode: 0, Handled: "association SendReport"},
	}
}
