// SPDX-License-Identifier: GPL-3.0-or-later

// Package data provides immutable IEC 61850/MMS values and their separate type
// declarations. Constructors and accessors copy slices so values can be shared
// across goroutines. The zero Value is invalid, not a numeric or boolean zero.
//
// Select the constructor matching the declared kind and wire width. Int and
// Uint are distinct; Float32 and Float64 preserve their encoded widths. Typed
// getters return errors on incompatible kinds rather than coercing values.
// TypeSpec preserves names and widths not present in the MMS Data value itself.
// ValidateValue checks a candidate against that declaration before a write.
// TypeSpec itself is a mutable Go description; use CloneType before exposing
// shared declarations to code that may modify them.
//
// ResolveComponent, ValueAt and WithComponent address ordered structures and
// zero-based arrays without mutating immutable values. Raw quality and UTC
// bytes are retained; engineering scaling and acceptance policy belong to the
// application. Package mms aliases these value and type APIs for convenience.
package data
