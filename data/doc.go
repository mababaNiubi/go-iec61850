// SPDX-License-Identifier: GPL-3.0-or-later

// Package data provides the shared MMS Data values and TypeSpecification codec
// used by IEC 61850 protocols. It preserves wire-level details such as bit-string
// lengths, floating-point formats, and UTC time quality.
//
// Values are immutable snapshots. Type descriptions are separate from values
// and remain mutable Go descriptions, so owners must copy them when exposing
// shared models. This package contains no MMS association or Ethernet logic:
// MMS services and future GOOSE allData codecs can share these data types.
package data
