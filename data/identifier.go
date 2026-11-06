// SPDX-License-Identifier: GPL-3.0-or-later
package data

// ValidIdentifier checks the MMS Identifier representation used by object and
// structure-member names: 1..64 printable ASCII octets. It does not validate
// IEC 61850 object references, whose syntax and limits are different.
func ValidIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
