// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"errors"
	"fmt"
)

var (
	ErrClosed         = errors.New("mms: association closed")
	ErrProtocol       = errors.New("mms: invalid protocol message")
	ErrReportOverflow = errors.New("mms: information report queue overflow")
)

// DataAccessError is an error for one item in a read or write result.
type DataAccessError int

const (
	ObjectInvalidated DataAccessError = iota
	HardwareFault
	TemporarilyUnavailable
	ObjectAccessDenied
	ObjectUndefined
	InvalidAddress
	TypeUnsupported
	TypeInconsistent
	ObjectAttributeInconsistent
	ObjectAccessUnsupported
	ObjectNonExistent
	ObjectValueInvalid
)

// Error formats the per-member access error without discarding its numeric identity.
func (e DataAccessError) Error() string {
	names := [...]string{"object invalidated", "hardware fault", "temporarily unavailable", "object access denied", "object undefined", "invalid address", "type unsupported", "type inconsistent", "object attribute inconsistent", "object access unsupported", "object nonexistent", "object value invalid"}
	if int(e) >= 0 && int(e) < len(names) {
		return "mms: " + names[e]
	}
	return fmt.Sprintf("mms: data access error %d", e)
}

// ServiceError preserves the MMS error class and code of a whole request.
type ServiceError struct{ Class, Code int }

// Error formats the request-level MMS error class and code.
func (e *ServiceError) Error() string {
	return fmt.Sprintf("mms: service error class=%d code=%d", e.Class, e.Code)
}

// RejectError indicates a rejected PDU, rather than a data access failure.
type RejectError struct{ Class, Reason int }

// Error formats the rejected PDU class and reason.
func (e *RejectError) Error() string {
	return fmt.Sprintf("mms: rejected PDU class=%d reason=%d", e.Class, e.Reason)
}
