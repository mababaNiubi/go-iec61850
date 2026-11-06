// SPDX-License-Identifier: GPL-3.0-or-later
package iso

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	acseSyntax     = []byte{0x52, 1, 0, 1}
	mmsSyntax      = []byte{0x28, 0xca, 0x22, 2, 1}
	mmsApplication = []byte{0x28, 0xca, 0x22, 2, 3}
	berSyntax      = []byte{0x51, 1}
)

func tlv(tag byte, parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	b := []byte{tag}
	if n < 128 {
		b = append(b, byte(n))
	} else {
		var tmp [8]byte
		binary.BigEndian.PutUint64(tmp[:], uint64(n))
		i := 0
		for tmp[i] == 0 {
			i++
		}
		b = append(b, 0x80|byte(8-i))
		b = append(b, tmp[i:]...)
	}
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

type element struct {
	tag   byte
	value []byte
}

// Lower layers also accept constructed BER indefinite lengths, with bounded
// recursion and no interpretation of the enclosed MMS payload.
func readElement(b []byte, depth int) (element, []byte, error) {
	if depth > 32 || len(b) < 2 {
		return element{}, nil, errors.New("iso: truncated BER element or excessive nesting")
	}
	if b[0]&0x1f == 0x1f || b[0] == 0 {
		return element{}, nil, errors.New("iso: unsupported BER tag")
	}
	tag := b[0]
	p := 2
	n := int(b[1])
	if n == 128 {
		if tag&0x20 == 0 {
			return element{}, nil, errors.New("iso: primitive indefinite BER length")
		}
		remaining := b[p:]
		length := 0
		for {
			if len(remaining) < 2 {
				return element{}, nil, errors.New("iso: missing BER end-of-contents")
			}
			if remaining[0] == 0 && remaining[1] == 0 {
				return element{tag, b[p : p+length]}, remaining[2:], nil
			}
			_, rest, err := readElement(remaining, depth+1)
			if err != nil {
				return element{}, nil, err
			}
			length += len(remaining) - len(rest)
			remaining = rest
		}
	}
	if n > 128 {
		count := n & 127
		if count > 4 || len(b) < p+count {
			return element{}, nil, errors.New("iso: invalid BER length")
		}
		var length uint64
		for _, v := range b[p : p+count] {
			length = length<<8 | uint64(v)
		}
		p += count
		// Check against the available buffer before converting to int. A
		// four-octet BER length can exceed MaxInt on 32-bit platforms.
		if length > uint64(len(b)-p) {
			return element{}, nil, errors.New("iso: truncated BER content")
		}
		n = int(length)
	}
	if n > len(b)-p {
		return element{}, nil, errors.New("iso: truncated BER content")
	}
	return element{tag, b[p : p+n]}, b[p+n:], nil
}

func single(b []byte, tag byte) (element, error) {
	e, rest, err := readElement(b, 0)
	if err != nil {
		return e, err
	}
	if e.tag != tag || len(rest) != 0 {
		return e, fmt.Errorf("iso: expected BER tag 0x%02x", tag)
	}
	return e, nil
}

func elements(b []byte) ([]element, error) {
	var out []element
	for len(b) > 0 {
		if len(out) >= 128 {
			return nil, errors.New("iso: too many BER fields")
		}
		e, rest, err := readElement(b, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
		b = rest
	}
	return out, nil
}

func positiveInt(b []byte) (uint32, error) {
	if len(b) == 0 || len(b) > 5 || b[0]&0x80 != 0 || (len(b) == 5 && b[0] != 0) {
		return 0, errors.New("iso: invalid nonnegative integer")
	}
	var n uint32
	for _, v := range b {
		n = n<<8 | uint32(v)
	}
	return n, nil
}

func intTLV(tag byte, n uint32) []byte {
	var b [5]byte
	binary.BigEndian.PutUint32(b[1:], n)
	p := 1
	for p < 4 && b[p] == 0 {
		p++
	}
	if b[p]&0x80 != 0 {
		p--
	}
	return tlv(tag, b[p:])
}

func oid(arcs []uint32) []byte {
	var out []byte
	add := func(v uint64) {
		var tmp [10]byte
		i := len(tmp) - 1
		tmp[i] = byte(v & 127)
		for v >>= 7; v != 0; v >>= 7 {
			i--
			tmp[i] = byte(v&127) | 128
		}
		out = append(out, tmp[i:]...)
	}
	add(uint64(arcs[0])*40 + uint64(arcs[1]))
	for _, n := range arcs[2:] {
		add(uint64(n))
	}
	return out
}

func sessionLength(n int) []byte {
	if n < 255 {
		return []byte{byte(n)}
	}
	return []byte{255, byte(n >> 8), byte(n)}
}
func sessionParam(tag byte, value []byte) []byte {
	return append(append([]byte{tag}, sessionLength(len(value))...), value...)
}

func encodeSession(kind byte, calling, called, payload []byte) []byte {
	var b []byte
	if kind == 13 || kind == 14 {
		b = append(b, 5, 6, 0x13, 1, 0, 0x16, 1, 2, 0x14, 2, 0, 2)
		if kind == 13 && len(calling) > 0 {
			b = append(b, sessionParam(0x33, calling)...)
		}
		if len(called) > 0 {
			b = append(b, sessionParam(0x34, called)...)
		}
	}
	if kind == 25 {
		b = append(b, 17, 1, 11)
	}
	b = append(b, sessionParam(0xc1, payload)...)
	return append(append([]byte{kind}, sessionLength(len(b))...), b...)
}

func readSessionLength(b []byte) (int, []byte, error) {
	if len(b) == 0 {
		return 0, nil, errors.New("iso: missing session length")
	}
	if b[0] != 255 {
		return int(b[0]), b[1:], nil
	}
	if len(b) < 3 {
		return 0, nil, errors.New("iso: truncated extended session length")
	}
	return int(binary.BigEndian.Uint16(b[1:3])), b[3:], nil
}

func parseSession(b []byte, kind byte) (calling, called, payload []byte, err error) {
	if len(b) < 2 || b[0] != kind {
		return nil, nil, nil, fmt.Errorf("iso: expected session SPDU %d", kind)
	}
	n, content, err := readSessionLength(b[1:])
	if err != nil {
		return nil, nil, nil, err
	}
	if n != len(content) {
		return nil, nil, nil, errors.New("iso: session length mismatch")
	}
	hasOptions, hasVersion, hasRequirements := false, false, false
	seen := map[byte]bool{}
	for len(content) > 0 {
		tag := content[0]
		size, rest, e := readSessionLength(content[1:])
		if e != nil {
			return nil, nil, nil, e
		}
		if size > len(rest) || seen[tag] {
			return nil, nil, nil, errors.New("iso: invalid or repeated session parameter")
		}
		seen[tag] = true
		value := rest[:size]
		content = rest[size:]
		switch tag {
		case 5:
			for len(value) > 0 {
				pi := value[0]
				pn, pr, pe := readSessionLength(value[1:])
				if pe != nil || pn > len(pr) {
					return nil, nil, nil, errors.New("iso: malformed connect/accept item")
				}
				pv := pr[:pn]
				value = pr[pn:]
				switch pi {
				case 0x13:
					if len(pv) != 1 || pv[0] != 0 {
						return nil, nil, nil, errors.New("iso: unsupported session options")
					}
					hasOptions = true
				case 0x16:
					if len(pv) != 1 || pv[0] != 2 {
						return nil, nil, nil, errors.New("iso: session version must be 2")
					}
					hasVersion = true
				}
			}
		case 0x14:
			if len(value) != 2 || binary.BigEndian.Uint16(value)&2 == 0 {
				return nil, nil, nil, errors.New("iso: duplex session required")
			}
			hasRequirements = true
		case 0x33:
			if len(value) > 16 {
				return nil, nil, nil, errors.New("iso: calling S selector too long")
			}
			calling = value
		case 0x34:
			if len(value) > 16 {
				return nil, nil, nil, errors.New("iso: called S selector too long")
			}
			called = value
		case 0xc1:
			payload = value
		}
	}
	if len(payload) == 0 {
		return nil, nil, nil, errors.New("iso: session user data missing")
	}
	if (kind == 13 || kind == 14) && (!hasOptions || !hasVersion || !hasRequirements) {
		return nil, nil, nil, errors.New("iso: incomplete session negotiation")
	}
	return calling, called, payload, nil
}

func presentationData(context byte, payload []byte) []byte {
	return tlv(0x61, tlv(0x30, intTLV(2, uint32(context)), tlv(0xa0, payload)))
}

func parsePresentationData(b []byte) (byte, []byte, error) {
	e, err := single(b, 0x61)
	if err != nil {
		return 0, nil, err
	}
	e, err = single(e.value, 0x30)
	if err != nil {
		return 0, nil, err
	}
	fields, err := elements(e.value)
	if err != nil {
		return 0, nil, err
	}
	var id byte
	var payload []byte
	for _, f := range fields {
		switch f.tag {
		case 6:
			if !bytes.Equal(f.value, berSyntax) {
				return 0, nil, errors.New("iso: unexpected transfer syntax")
			}
		case 2:
			n, e := positiveInt(f.value)
			if e != nil || n == 0 || n > 127 || id != 0 {
				return 0, nil, errors.New("iso: invalid presentation context")
			}
			id = byte(n)
		case 0xa0:
			if payload != nil {
				return 0, nil, errors.New("iso: repeated presentation payload")
			}
			payload = f.value
		default:
			return 0, nil, errors.New("iso: unsupported presentation encoding")
		}
	}
	if id == 0 || len(payload) == 0 {
		return 0, nil, errors.New("iso: incomplete presentation user data")
	}
	return id, payload, nil
}

func presentationConnect(cfg Config, acse []byte) []byte {
	contexts := tlv(0xa4,
		tlv(0x30, intTLV(2, 1), tlv(6, acseSyntax), tlv(0x30, tlv(6, berSyntax))),
		tlv(0x30, intTLV(2, 3), tlv(6, mmsSyntax), tlv(0x30, tlv(6, berSyntax))))
	return tlv(0x31, tlv(0xa0, tlv(0x80, []byte{1})), tlv(0xa2, tlv(0x81, cfg.LocalPSelector), tlv(0x82, cfg.RemotePSelector), contexts, presentationData(1, acse)))
}

func presentationAccept(cfg Config, context byte, acse []byte) []byte {
	accepted := tlv(0x30, tlv(0x80, []byte{0}), tlv(0x81, berSyntax))
	return tlv(0x31, tlv(0xa0, tlv(0x80, []byte{1})), tlv(0xa2, tlv(0x83, cfg.LocalPSelector), tlv(0xa5, accepted, accepted), presentationData(context, acse)))
}

func (c *Conn) parsePresentationConnect(b []byte, response bool) ([]byte, error) {
	e, err := single(b, 0x31)
	if err != nil {
		return nil, err
	}
	outer, err := elements(e.value)
	if err != nil {
		return nil, err
	}
	var normal []byte
	hasMode := false
	for _, f := range outer {
		switch f.tag {
		case 0xa0:
			mode, e := single(f.value, 0x80)
			if e != nil || !bytes.Equal(mode.value, []byte{1}) {
				return nil, errors.New("iso: normal presentation mode required")
			}
			hasMode = true
		case 0xa2:
			if normal != nil {
				return nil, errors.New("iso: duplicate presentation parameters")
			}
			normal = f.value
		default:
			return nil, errors.New("iso: unsupported presentation parameters")
		}
	}
	if !hasMode || normal == nil {
		return nil, errors.New("iso: incomplete presentation negotiation")
	}
	fields, err := elements(normal)
	if err != nil {
		return nil, err
	}
	var data []byte
	hasContexts := false
	for _, f := range fields {
		switch f.tag {
		case 0x81, 0x82, 0x83:
			if len(f.value) > 16 {
				return nil, errors.New("iso: presentation selector too long")
			}
			if !response && f.tag == 0x82 && len(f.value) > 0 && !bytes.Equal(f.value, c.cfg.LocalPSelector) {
				return nil, errors.New("iso: called presentation selector mismatch")
			}
		case 0xa4:
			if response || hasContexts {
				return nil, errors.New("iso: unexpected presentation context definitions")
			}
			hasContexts = true
			contexts, e := elements(f.value)
			if e != nil || len(contexts) != 2 {
				return nil, errors.New("iso: expected ACSE and MMS presentation contexts")
			}
			var acseID, mmsID byte
			for _, ctx := range contexts {
				if ctx.tag != 0x30 {
					return nil, errors.New("iso: invalid presentation context sequence")
				}
				parts, e := elements(ctx.value)
				if e != nil || len(parts) != 3 || parts[0].tag != 2 || parts[1].tag != 6 || parts[2].tag != 0x30 {
					return nil, errors.New("iso: malformed presentation context")
				}
				id, e := positiveInt(parts[0].value)
				if e != nil || id == 0 || id > 127 || id%2 == 0 {
					return nil, errors.New("iso: invalid context identifier")
				}
				transfer, e := single(parts[2].value, 6)
				if e != nil || !bytes.Equal(transfer.value, berSyntax) {
					return nil, errors.New("iso: BER transfer syntax required")
				}
				switch {
				case bytes.Equal(parts[1].value, acseSyntax):
					if acseID != 0 {
						return nil, errors.New("iso: duplicate ACSE context")
					}
					acseID = byte(id)
				case bytes.Equal(parts[1].value, mmsSyntax):
					if mmsID != 0 {
						return nil, errors.New("iso: duplicate MMS context")
					}
					mmsID = byte(id)
				default:
					return nil, errors.New("iso: unsupported abstract syntax")
				}
			}
			if acseID == 0 || mmsID == 0 || acseID == mmsID {
				return nil, errors.New("iso: missing or conflicting presentation contexts")
			}
			c.acseContext, c.mmsContext = acseID, mmsID
		case 0xa5:
			if !response || hasContexts {
				return nil, errors.New("iso: unexpected presentation results")
			}
			hasContexts = true
			results, e := elements(f.value)
			if e != nil || len(results) != 2 {
				return nil, errors.New("iso: incomplete presentation results")
			}
			for _, r := range results {
				parts, e := elements(r.value)
				if r.tag != 0x30 || e != nil || len(parts) != 2 || parts[0].tag != 0x80 || !bytes.Equal(parts[0].value, []byte{0}) || parts[1].tag != 0x81 || !bytes.Equal(parts[1].value, berSyntax) {
					return nil, errors.New("iso: presentation context rejected")
				}
			}
		case 0x61:
			if data != nil {
				return nil, errors.New("iso: repeated connect user data")
			}
			data = tlv(0x61, f.value)
		}
	}
	if !hasContexts || data == nil {
		return nil, errors.New("iso: presentation context or data missing")
	}
	id, payload, err := parsePresentationData(data)
	if err != nil {
		return nil, err
	}
	if id != c.acseContext {
		return nil, errors.New("iso: ACSE context mismatch")
	}
	return payload, nil
}

func associateRequest(cfg Config, initiate []byte) []byte {
	parts := [][]byte{tlv(0xa1, tlv(6, mmsApplication))}
	if len(cfg.RemoteAPTitle) > 0 {
		parts = append(parts, tlv(0xa2, tlv(6, oid(cfg.RemoteAPTitle))), tlv(0xa3, intTLV(2, uint32(cfg.RemoteAEQualifier))))
	}
	if len(cfg.LocalAPTitle) > 0 {
		parts = append(parts, tlv(0xa6, tlv(6, oid(cfg.LocalAPTitle))), tlv(0xa7, intTLV(2, uint32(cfg.LocalAEQualifier))))
	}
	parts = append(parts, tlv(0xbe, tlv(0x28, intTLV(2, 3), tlv(0xa0, initiate))))
	return tlv(0x60, parts...)
}

func associateResponse(initiate []byte, indirect uint32) []byte {
	return tlv(0x61, tlv(0xa1, tlv(6, mmsApplication)), tlv(0xa2, intTLV(2, 0)), tlv(0xa3, tlv(0xa1, intTLV(2, 0))), tlv(0xbe, tlv(0x28, intTLV(2, indirect), tlv(0xa0, initiate))))
}

func parseAssociate(b []byte, response bool) ([]byte, uint32, error) {
	tag := byte(0x60)
	if response {
		tag = 0x61
	}
	e, err := single(b, tag)
	if err != nil {
		return nil, 0, err
	}
	fields, err := elements(e.value)
	if err != nil {
		return nil, 0, err
	}
	hasContext, hasResult := false, false
	var payload []byte
	var indirect uint32
	seen := map[byte]bool{}
	for _, f := range fields {
		if seen[f.tag] {
			return nil, 0, errors.New("iso: repeated ACSE field")
		}
		seen[f.tag] = true
		switch f.tag {
		case 0xa1:
			ctx, e := single(f.value, 6)
			if e != nil || !bytes.Equal(ctx.value, mmsApplication) {
				return nil, 0, errors.New("iso: MMS application context required")
			}
			hasContext = true
		case 0xa2:
			if response {
				result, e := single(f.value, 2)
				if e != nil {
					return nil, 0, e
				}
				n, e := positiveInt(result.value)
				if e != nil || n != 0 {
					return nil, 0, errors.New("iso: ACSE association rejected")
				}
				hasResult = true
			}
		case 0x8a, 0x8b, 0xac:
			// Authentication must not silently succeed when no authenticator exists.
			return nil, 0, errors.New("iso: ACSE authentication is not supported")
		case 0xbe:
			external, e := single(f.value, 0x28)
			if e != nil {
				return nil, 0, e
			}
			parts, e := elements(external.value)
			if e != nil {
				return nil, 0, e
			}
			hasIndirect := false
			for _, part := range parts {
				switch part.tag {
				case 2:
					if hasIndirect {
						return nil, 0, errors.New("iso: duplicate indirect reference")
					}
					indirect, e = positiveInt(part.value)
					if e != nil {
						return nil, 0, e
					}
					hasIndirect = true
				case 0xa0:
					if payload != nil {
						return nil, 0, errors.New("iso: duplicate ACSE payload")
					}
					payload = part.value
				case 6:
					if !bytes.Equal(part.value, mmsSyntax) {
						return nil, 0, errors.New("iso: unexpected ACSE direct reference")
					}
				default:
					return nil, 0, errors.New("iso: unsupported ACSE external encoding")
				}
			}
			if !hasIndirect {
				return nil, 0, errors.New("iso: ACSE indirect reference missing")
			}
		}
	}
	if !hasContext || len(payload) == 0 || (response && !hasResult) {
		return nil, 0, errors.New("iso: incomplete ACSE association")
	}
	return payload, indirect, nil
}
