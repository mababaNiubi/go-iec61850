// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import "github.com/mababaNiubi/go-iec61850/data"

// TypeSpec describes a variable independently of its current Value.
type TypeSpec = data.TypeSpec

// Component is one ordered structure member.
type Component = data.Component

func TypeOf(v Value) TypeSpec                 { return data.TypeOf(v) }
func TypeDepth(v TypeSpec) int                { return data.TypeDepth(v) }
func ValidateType(v TypeSpec) error           { return data.ValidateType(v) }
func ValidateValue(t TypeSpec, v Value) error { return data.ValidateValue(t, v) }
func EncodeType(v TypeSpec) ([]byte, error)   { return data.EncodeType(v) }
func DecodeType(v []byte) (TypeSpec, error)   { return data.DecodeType(v) }
