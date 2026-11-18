// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// DiscoveryOptions bounds online discovery. An empty Domain inspects all domains; MaxObjects
// defaults to 4096 and must be 1..65536. MaxRequests bounds the total confirmed requests one
// Discover call may issue (name pages, attribute probes, list reads, RCB reads); it defaults
// to 4096 and must be 1..65536. MaxBytes bounds accumulated decoded object-name text; zero
// selects 4 MiB. Exhausting any limit stops before issuing further work and marks the result
// truncated with the reason, preserving completed results for deterministic continuation by
// re-running with a higher budget.
type DiscoveryOptions struct {
	Domain      string // empty discovers all logical-device domains
	MaxObjects  int    // zero selects 4096; applies across all domains
	MaxRequests int    // zero selects 4096; charged across every request type
	MaxBytes    int    // zero selects 4 MiB of decoded name text
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
// its object list as complete. TruncationReason names the limit that stopped the scan
// ("object limit", "request budget", "name-byte budget"); Requests reports the confirmed
// requests actually issued.
type Directory struct {
	Domains          []string
	Objects          []DiscoveredObject
	DataSets         []mms.NamedList
	Reports          []RemoteReportBlock
	SettingGroups    []mms.ObjectName
	Diagnostics      []DiscoveryDiagnostic
	Truncated        bool
	TruncationReason string
	Requests         int
}

// discoveryBudget is the single DISC-01 operation budget. Every confirmed
// request is charged exactly once before it is issued, and decoded name text
// plus discovered objects are charged as they appear. Exhaustion stops before
// further work with the completed results preserved.
type discoveryBudget struct {
	requests, maxRequests int
	bytes, maxBytes       int
	objects, maxObjects   int
}

var errDiscoveryLimit = fmt.Errorf("ied: discovery limit reached")

// request admits one confirmed request or stops with the reason set.
func (b *discoveryBudget) request(reason *string) error {
	if b.requests >= b.maxRequests {
		*reason = "request budget"
		return errDiscoveryLimit
	}
	b.requests++
	return nil
}

// text charges decoded name bytes; object admits one discovered object.
func (b *discoveryBudget) text(n int, reason *string) error {
	b.bytes += n
	if b.bytes > b.maxBytes {
		*reason = "name-byte budget"
		return errDiscoveryLimit
	}
	return nil
}

func (b *discoveryBudget) object(reason *string) bool {
	if b.objects >= b.maxObjects {
		*reason = "object limit"
		return false
	}
	b.objects++
	return true
}

// Discover uses confirmed MMS services only. It works without an SCL file.
// Online MMS cannot reconstruct the original SubNetwork, primary equipment,
// ExtRef bindings or NSD namespace; those remain engineering-file information.
// Unsupported members become diagnostics while other objects stay usable.
func (c *Client) Discover(ctx context.Context, options DiscoveryOptions) (Directory, error) {
	out := Directory{Objects: []DiscoveredObject{}, Reports: []RemoteReportBlock{}, DataSets: []mms.NamedList{}, SettingGroups: []mms.ObjectName{}, Diagnostics: []DiscoveryDiagnostic{}}
	budget := &discoveryBudget{maxObjects: options.MaxObjects, maxRequests: options.MaxRequests, maxBytes: options.MaxBytes}
	if budget.maxObjects == 0 {
		budget.maxObjects = 4096
	}
	if budget.maxRequests == 0 {
		budget.maxRequests = 4096
	}
	if budget.maxBytes == 0 {
		budget.maxBytes = 4 << 20
	}
	if budget.maxObjects < 1 || budget.maxObjects > 65536 || budget.maxRequests < 1 || budget.maxRequests > 65536 || budget.maxBytes < 1 {
		return out, fmt.Errorf("ied: discovery limits must be MaxObjects 1..65536, MaxRequests 1..65536 and MaxBytes >= 1")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := budget.request(&out.TruncationReason); err != nil {
		out.Truncated = true
		return out, nil
	}
	domains, err := c.raw.Names(ctx, mms.DomainObject, mms.VMDScope, "")
	if err != nil {
		return out, err
	}
	for _, domain := range domains {
		if err := budget.text(len(domain), &out.TruncationReason); err != nil {
			out.Truncated = true
			return out, nil
		}
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
		if !budget.object(&out.TruncationReason) {
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
	finish := func(err error) (Directory, error, bool) {
		if errors.Is(err, errDiscoveryLimit) {
			out.Truncated = true
			out.Requests = budget.requests
			return out, nil, true
		}
		return out, err, false
	}
	for _, domain := range domains {
		err = c.discoverNames(ctx, mms.NamedVariable, domain, budget, &out.TruncationReason, func(item string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := mms.DomainName(domain, item)
			if seen[name] {
				return nil
			}
			if err := budget.text(len(item), &out.TruncationReason); err != nil {
				return err
			}
			if !budget.object(&out.TruncationReason) {
				out.Truncated = true
				return errDiscoveryLimit
			}
			if err := budget.request(&out.TruncationReason); err != nil {
				return err
			}
			spec, e := c.GetVariableAccessAttributes(ctx, name)
			if e != nil {
				out.Diagnostics = append(out.Diagnostics, DiscoveryDiagnostic{name.String(), e.Error()})
				return nil
			}
			visit(name, spec)
			return nil
		})
		if out, err, stop := finish(err); stop {
			return out, err
		}
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Diagnostics = append(out.Diagnostics, DiscoveryDiagnostic{domain, err.Error()})
		}
		err = c.discoverNames(ctx, mms.NamedVariableList, domain, budget, &out.TruncationReason, func(item string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := budget.text(len(item), &out.TruncationReason); err != nil {
				return err
			}
			if budget.objects >= budget.maxObjects {
				out.TruncationReason = "object limit"
				return errDiscoveryLimit
			}
			if err := budget.request(&out.TruncationReason); err != nil {
				return err
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
		if out, err, stop := finish(err); stop {
			return out, err
		}
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Diagnostics = append(out.Diagnostics, DiscoveryDiagnostic{domain + "/datasets", err.Error()})
		}
	}
	for i, block := range out.Reports {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := budget.request(&out.TruncationReason); err != nil {
			out.Truncated = true
			out.Requests = budget.requests
			return out, nil
		}
		info, e := c.ReadReportBlock(ctx, block.Name)
		if e != nil {
			out.Reports[i].Error = e.Error()
		} else {
			out.Reports[i] = info
		}
	}
	out.Requests = budget.requests
	sort.Slice(out.Objects, func(i, j int) bool { return out.Objects[i].Name.String() < out.Objects[j].Name.String() })
	sort.Slice(out.Reports, func(i, j int) bool { return out.Reports[i].Name.String() < out.Reports[j].Name.String() })
	return out, ctx.Err()
}

func (c *Client) discoverNames(ctx context.Context, class mms.ObjectClass, domain string, budget *discoveryBudget, reason *string, visit func(string) error) error {
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := budget.request(reason); err != nil {
			return err
		}
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
		}
		if !page.MoreFollows {
			return nil
		}
		if len(page.Names) == 0 {
			return mms.ErrProtocol
		}
	}
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
