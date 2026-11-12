// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// DiscoveryOptions bounds online discovery. An empty Domain inspects all domains; MaxObjects
// defaults to 4096 and must be 1..65536.
type DiscoveryOptions struct {
	Domain     string // empty discovers all logical-device domains
	MaxObjects int    // zero selects 4096; applies across all domains
}

// DiscoveredObject is an online object name, functional constraint and declared type. It
// contains no current process value.
type DiscoveredObject struct {
	Name mms.ObjectName
	FC   string
	Type data.TypeSpec
}

// DiscoveryDiagnostic identifies a remote object that could not be inspected while other
// objects remained discoverable.
type DiscoveryDiagnostic struct{ Reference, Message string }

// RemoteReportBlock contains the identifying RP/BR attributes read online. Error records an
// unavailable block without hiding its name.
type RemoteReportBlock struct {
	Name              mms.ObjectName
	Buffered, Enabled bool
	ID, DataSet       string
	ConfRev           uint32
	Error             string
}

// Directory is one online discovery result. Check Diagnostics and Truncated before treating
// its object list as complete.
type Directory struct {
	Domains       []string
	Objects       []DiscoveredObject
	DataSets      []mms.NamedList
	Reports       []RemoteReportBlock
	SettingGroups []mms.ObjectName
	Diagnostics   []DiscoveryDiagnostic
	Truncated     bool
}

// Discover uses confirmed MMS services only. It works without an SCL file.
// Online MMS cannot reconstruct the original SubNetwork, primary equipment,
// ExtRef bindings or NSD namespace; those remain engineering-file information.
// Unsupported members become diagnostics while other objects stay usable.
func (c *Client) Discover(ctx context.Context, options DiscoveryOptions) (Directory, error) {
	out := Directory{Objects: []DiscoveredObject{}, Reports: []RemoteReportBlock{}, DataSets: []mms.NamedList{}, SettingGroups: []mms.ObjectName{}, Diagnostics: []DiscoveryDiagnostic{}}
	limit := options.MaxObjects
	if limit == 0 {
		limit = 4096
	}
	if limit < 1 || limit > 65536 {
		return out, fmt.Errorf("ied: discovery limit must be 1..65536")
	}
	domains, err := c.raw.Names(ctx, mms.DomainObject, mms.VMDScope, "")
	if err != nil {
		return out, err
	}
	if options.Domain != "" {
		found := false
		for _, domain := range domains {
			found = found || domain == options.Domain
		}
		if !found {
			return out, mms.ObjectNonExistent
		}
		domains = []string{options.Domain}
	}
	out.Domains = domains
	seen := map[mms.ObjectName]bool{}
	var visit func(mms.ObjectName, data.TypeSpec)
	visit = func(name mms.ObjectName, spec data.TypeSpec) {
		if seen[name] {
			return
		}
		if len(out.Objects) >= limit {
			out.Truncated = true
			return
		}
		seen[name] = true
		parts := strings.Split(name.Item, "$")
		fc := ""
		if len(parts) > 1 {
			fc = parts[1]
		}
		out.Objects = append(out.Objects, DiscoveredObject{Name: name, FC: fc, Type: data.CloneType(spec)})
		if len(parts) == 3 && (fc == "RP" || fc == "BR") && spec.Kind == data.KindStructure {
			out.Reports = append(out.Reports, RemoteReportBlock{Name: name, Buffered: fc == "BR"})
		}
		if len(parts) == 3 && fc == "SP" && parts[2] == "SGCB" {
			out.SettingGroups = append(out.SettingGroups, name)
		}
		for _, field := range spec.Components {
			visit(mms.DomainName(name.Domain, name.Item+"$"+field.Name), field.Type)
		}
	}
	for _, domain := range domains {
		err = c.discoverNames(ctx, mms.NamedVariable, domain, limit, func(item string) error {
			name := mms.DomainName(domain, item)
			if seen[name] {
				return nil
			}
			if len(out.Objects) >= limit {
				out.Truncated = true
				return errDiscoveryLimit
			}
			spec, e := c.GetVariableAccessAttributes(ctx, name)
			if e != nil {
				out.Diagnostics = append(out.Diagnostics, DiscoveryDiagnostic{name.String(), e.Error()})
				return nil
			}
			visit(name, spec)
			return nil
		})
		if err != nil && err != errDiscoveryLimit {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Diagnostics = append(out.Diagnostics, DiscoveryDiagnostic{domain, err.Error()})
		}
		if err == errDiscoveryLimit {
			out.Truncated = true
		}
		err = c.discoverNames(ctx, mms.NamedVariableList, domain, limit, func(item string) error {
			if len(out.DataSets) >= limit {
				out.Truncated = true
				return errDiscoveryLimit
			}
			name := mms.DomainName(domain, item)
			set, e := c.GetNamedListAttributes(ctx, name)
			if e != nil {
				out.Diagnostics = append(out.Diagnostics, DiscoveryDiagnostic{name.String(), e.Error()})
			} else {
				out.DataSets = append(out.DataSets, set)
			}
			return nil
		})
		if err != nil && err != errDiscoveryLimit {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Diagnostics = append(out.Diagnostics, DiscoveryDiagnostic{domain + "/datasets", err.Error()})
		}
	}
	for i, block := range out.Reports {
		info, e := c.ReadReportBlock(ctx, block.Name)
		if e != nil {
			out.Reports[i].Error = e.Error()
		} else {
			out.Reports[i] = info
		}
	}
	sort.Slice(out.Objects, func(i, j int) bool { return out.Objects[i].Name.String() < out.Objects[j].Name.String() })
	sort.Slice(out.Reports, func(i, j int) bool { return out.Reports[i].Name.String() < out.Reports[j].Name.String() })
	return out, ctx.Err()
}

var errDiscoveryLimit = fmt.Errorf("ied: discovery limit reached")

func (c *Client) discoverNames(ctx context.Context, class mms.ObjectClass, domain string, limit int, visit func(string) error) error {
	after := ""
	for count := 0; count < limit; {
		page, err := c.raw.GetNameList(ctx, class, mms.DomainScope, domain, after)
		if err != nil {
			return err
		}
		for _, name := range page.Names {
			if name <= after {
				return mms.ErrProtocol
			}
			if err = visit(name); err != nil {
				return err
			}
			after = name
			count++
			if count >= limit && page.MoreFollows {
				return errDiscoveryLimit
			}
		}
		if !page.MoreFollows {
			return nil
		}
		if len(page.Names) == 0 {
			return mms.ErrProtocol
		}
	}
	return errDiscoveryLimit
}

// ReadReportBlock reads the identifying RCB attributes without changing them.
func (c *Client) ReadReportBlock(ctx context.Context, name mms.ObjectName) (RemoteReportBlock, error) {
	parts := strings.Split(name.Item, "$")
	out := RemoteReportBlock{Name: name}
	if name.Scope != mms.DomainScope || len(parts) != 3 || (parts[1] != "RP" && parts[1] != "BR") {
		return out, fmt.Errorf("ied: expected RP/BR control block")
	}
	out.Buffered = parts[1] == "BR"
	var accesses []mms.VariableAccess
	for _, field := range []string{"RptID", "DatSet", "ConfRev", "RptEna"} {
		accesses = append(accesses, mms.Access(mms.DomainName(name.Domain, name.Item+"$"+field)))
	}
	results, err := c.ReadMultiple(ctx, accesses...)
	if err != nil {
		return out, err
	}
	if len(results) != 4 {
		return out, mms.ErrProtocol
	}
	for _, r := range results {
		if r.Error != nil {
			return out, r.Error
		}
	}
	if out.ID, err = results[0].Value.StringValue(); err != nil {
		return out, err
	}
	if out.DataSet, err = results[1].Value.StringValue(); err != nil {
		return out, err
	}
	revision, err := results[2].Value.Uint()
	if err != nil || revision > 1<<32-1 {
		return out, fmt.Errorf("ied: invalid RCB ConfRev")
	}
	out.ConfRev = uint32(revision)
	out.Enabled, err = results[3].Value.Bool()
	return out, err
}
