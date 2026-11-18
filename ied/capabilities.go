// SPDX-License-Identifier: GPL-3.0-or-later
package ied

// CapabilityStatus classifies one service against the implementation profile.
// The matrix is a reviewed declaration (BASE-01), not a conformance report:
// Supported names implemented and tested behavior, Conditional names support
// bounded by an explicitly documented restriction, and Unsupported names
// services deliberately absent from the wire profile.
type CapabilityStatus string

const (
	Supported   CapabilityStatus = "supported"
	Conditional CapabilityStatus = "conditional"
	Unsupported CapabilityStatus = "unsupported"
)

// Capability is one row of the role/service matrix. Package names the public
// package owning the behavior; Restriction carries the Conditional clause.
type Capability struct {
	Area        string // client, server, goose, sv
	Service     string
	Status      CapabilityStatus
	Package     string
	Restriction string
}

// CapabilityMatrix is the versioned implementation profile. It separates
// client/server and publisher/subscriber roles so downstream tools can render
// exactly what the SDK does and does not offer on the wire.
type CapabilityMatrix struct {
	// Version follows the module release that reviewed the matrix.
	Version string
	Rows    []Capability
}

// Capabilities returns the implementation profile for the SDK's MMS client,
// the modeled server composed by the scl/simulator packages, and the GOOSE
// and SV publisher/subscriber roles. An unsupported service must not be
// advertised on the wire; mms.AdvertisedServices pins the negotiated MMS
// bits to the same profile.
func Capabilities() CapabilityMatrix {
	return CapabilityMatrix{
		Version: "1.4.1",
		Rows: []Capability{
			// MMS client role.
			{Area: "client", Service: "association identify/status/conclude", Status: Supported, Package: "mms"},
			{Area: "client", Service: "getNameList (domains, variables, lists)", Status: Supported, Package: "mms"},
			{Area: "client", Service: "read/write with alternate access", Status: Supported, Package: "mms"},
			{Area: "client", Service: "typed value codec (quality, times, bit strings)", Status: Supported, Package: "mms/data"},
			{Area: "client", Service: "define/get/delete named variable lists", Status: Conditional, Package: "mms", Restriction: "create/delete availability depends on the peer's profile; the modeled server accepts domain lists"},
			{Area: "client", Service: "online discovery without SCL", Status: Conditional, Package: "ied", Restriction: "object/type/budget bounded; no CDC semantics, equipment, ExtRefs or namespace reconstruction"},
			{Area: "client", Service: "URCB/BRCB subscribe, GI, resume, purge", Status: Supported, Package: "ied"},
			{Area: "client", Service: "current dataset snapshot (confirmed read)", Status: Supported, Package: "ied"},
			{Area: "client", Service: "immediate controls (direct/SBO, normal/enhanced security)", Status: Supported, Package: "ied"},
			{Area: "client", Service: "setting group read/edit/confirm/activate", Status: Supported, Package: "ied"},
			{Area: "client", Service: "MMS files", Status: Unsupported},
			{Area: "client", Service: "journals, MMS cancel, rename", Status: Unsupported},
			{Area: "client", Service: "IEC 62351 secure transport/authentication", Status: Unsupported},

			// Modeled server role (scl + simulator).
			{Area: "server", Service: "typed model read, local ST/MX acquisition", Status: Supported, Package: "scl"},
			{Area: "server", Service: "URCB/BRCB with buffering, segmentation and EntryID resume", Status: Supported, Package: "scl"},
			{Area: "server", Service: "RCB online field writes", Status: Conditional, Package: "scl", Restriction: "limited by SCL ReportSettings Fix/Conf/Dyn; binding dynamically created lists arrives with DS-02"},
			{Area: "server", Service: "SPC/DPC controls with virtual actuator callbacks", Status: Supported, Package: "scl"},
			{Area: "server", Service: "setting groups with edit reservations", Status: Supported, Package: "scl"},
			{Area: "server", Service: "additional CDC control models", Status: Unsupported},
			{Area: "server", Service: "online GoCB/SVCB management", Status: Unsupported},

			// Publisher/subscriber roles.
			{Area: "goose", Service: "publish/subscribe over software bus or Ethernet", Status: Supported, Package: "goose"},
			{Area: "goose", Service: "R-GOOSE (routable, session SPDU)", Status: Unsupported},
			{Area: "sv", Service: "publish/subscribe sampled values from model values", Status: Conditional, Package: "sv/simulator", Restriction: "unsynchronized snapshots of modeled MU values; no waveform synthesis"},
			{Area: "sv", Service: "R-SV (routable, session SPDU)", Status: Unsupported},
		},
	}
}
