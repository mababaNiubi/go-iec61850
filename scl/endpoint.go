// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// configureEndpoint reads the ISO addressing fields of the selected server.
// A client sends these as called selectors. The server uses them as local selectors.
func (d *IED) configureEndpoint(p map[string]string) error {
	port := uint64(102)
	if val, ok := p["IP-TCP-PORT"]; ok {
		n, err := strconv.ParseUint(val, 10, 16)
		if err != nil || n == 0 {
			return fmt.Errorf("invalid MMS TCP port %q", val)
		}
		port = n
	}
	host := ""
	for _, key := range []string{"IP", "IPv6"} {
		if val, ok := p[key]; ok {
			ip := net.ParseIP(val)
			if ip == nil || (key == "IP") != (ip.To4() != nil) {
				return fmt.Errorf("invalid %s address %q", key, val)
			}
			if host == "" {
				host = val
			}
		}
	}
	if host == "" {
		host = p["DNSName"]
	}
	if host != "" {
		d.address = net.JoinHostPort(host, strconv.FormatUint(port, 10))
	}
	for _, field := range []struct {
		key  string
		dest *[]byte
		max  int
	}{
		{"OSI-TSEL", &d.iso.RemoteTSelector, 4}, {"OSI-SSEL", &d.iso.RemoteSSelector, 8}, {"OSI-PSEL", &d.iso.RemotePSelector, 8},
	} {
		if val, ok := p[field.key]; ok {
			out, err := hex.DecodeString(val)
			if err != nil || len(out) == 0 || len(out) > field.max {
				return fmt.Errorf("invalid %s %q", field.key, val)
			}
			*field.dest = out
		}
	}
	if title, ok := p["OSI-AP-Title"]; ok {
		arcs := strings.Split(title, ",")
		for _, arc := range arcs {
			n, err := strconv.ParseUint(arc, 10, 32)
			if err != nil {
				return fmt.Errorf("invalid OSI-AP-Title %q", title)
			}
			d.iso.RemoteAPTitle = append(d.iso.RemoteAPTitle, uint32(n))
		}
		oid := d.iso.RemoteAPTitle
		if len(oid) < 2 || oid[0] > 2 || oid[0] < 2 && oid[1] > 39 {
			return fmt.Errorf("invalid OSI-AP-Title %q", title)
		}
	}
	if val, ok := p["OSI-AE-Qualifier"]; ok {
		v, err := strconv.ParseUint(val, 10, 31)
		if err != nil {
			return fmt.Errorf("invalid OSI-AE-Qualifier %q", val)
		}
		d.iso.RemoteAEQualifier = int(v)
	}
	return nil
}

// ClientConfig returns a fresh config for dialing this IED. Applications can
// set timeouts and their own calling selectors before passing it to mms.Dial.
func (d *IED) ClientConfig() mms.Config {
	c := d.iso
	c.RemoteTSelector = bytes.Clone(c.RemoteTSelector)
	c.RemoteSSelector = bytes.Clone(c.RemoteSSelector)
	c.RemotePSelector = bytes.Clone(c.RemotePSelector)
	c.RemoteAPTitle = slices.Clone(c.RemoteAPTitle)
	return mms.Config{ISO: c}
}
func (d *IED) serverConfig(cfg mms.Config) mms.Config {
	c := &cfg.ISO
	if c.LocalTSelector == nil {
		c.LocalTSelector = bytes.Clone(d.iso.RemoteTSelector)
	}
	if c.LocalSSelector == nil {
		c.LocalSSelector = bytes.Clone(d.iso.RemoteSSelector)
	}
	if c.LocalPSelector == nil {
		c.LocalPSelector = bytes.Clone(d.iso.RemotePSelector)
	}
	if c.LocalAPTitle == nil {
		c.LocalAPTitle = slices.Clone(d.iso.RemoteAPTitle)
	}
	if c.LocalAEQualifier == 0 {
		c.LocalAEQualifier = d.iso.RemoteAEQualifier
	}
	return cfg
}
