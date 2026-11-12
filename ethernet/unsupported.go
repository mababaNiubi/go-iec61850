// SPDX-License-Identifier: GPL-3.0-or-later
//go:build !linux && !(windows && amd64)

package ethernet

// Interfaces lists available packet adapters, or returns a platform/driver error.
func Interfaces() ([]Interface, error)               { return nil, ErrUnsupported }
func openBackend(Config) (backend, Interface, error) { return nil, Interface{}, ErrUnsupported }
