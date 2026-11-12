// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import "github.com/mababaNiubi/go-iec61850/data"

// TypeSpec describes a variable independently of its current Value.
type TypeSpec = data.TypeSpec

// Component is one ordered structure member.
type Component = data.Component

// TypeOf derives a declaration from a value. Use an explicit TypeSpec when the device requires
// fixed widths or lengths.
func TypeOf(v Value) TypeSpec { return data.TypeOf(v) }

// TypeDepth returns the nesting depth used to check negotiated MMS limits.
func TypeDepth(v TypeSpec) int { return data.TypeDepth(v) }

// ValidateType checks declaration shape, supported scalar widths and bounded nesting.
func ValidateType(v TypeSpec) error { return data.ValidateType(v) }

// ValidateValue checks a value against its declaration before transmission or model mutation.
func ValidateValue(t TypeSpec, v Value) error { return data.ValidateValue(t, v) }

// EncodeType validates and BER-encodes an MMS type declaration. The returned bytes belong to
// the caller.
func EncodeType(v TypeSpec) ([]byte, error) { return data.EncodeType(v) }

// DecodeType parses and validates a complete BER type declaration, rejecting malformed or
// unsupported input.
func DecodeType(v []byte) (TypeSpec, error) { return data.DecodeType(v) }
