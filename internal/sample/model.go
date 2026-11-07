// SPDX-License-Identifier: GPL-3.0-or-later
// Package sample owns the small, schema-validated SCL fixture shared by the
// workbench, command-line examples and SCL regression tests.
package sample

import (
	"bytes"
	_ "embed"
)

//go:embed bay.scd
var model []byte

//go:embed station.scd
var station []byte

func SCL() []byte        { return bytes.Clone(model) }
func StationSCL() []byte { return bytes.Clone(station) }
