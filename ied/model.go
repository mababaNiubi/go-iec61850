// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// ModelNode is one node of the typed view over a discovery result:
// logical device → logical node → functional constraint → data object/leaf.
// The hierarchy is derived from the MMS names the peer actually returned;
// CDC semantics, units, enum labels, equipment and ExtRefs remain
// engineering-file information and are never invented here. Array members
// stay one lazy node: expanding per-index nodes is a caller decision.
type ModelNode struct {
	// Name is the display segment of this node, not its identity.
	Name string
	// Kind is one of device, logical-node, fc, point.
	Kind string
	// FC carries the functional constraint for fc and point nodes.
	FC string
	// Object is set on point nodes.
	Object   *DiscoveredObject
	children []*ModelNode
}

// Children returns the ordered child nodes. The slice must not be modified.
func (n *ModelNode) Children() []*ModelNode { return n.children }

// Walk visits every node in order with its path of display segments.
func (n *ModelNode) Walk(visit func(path []string, node *ModelNode)) {
	var recur func(prefix []string, node *ModelNode)
	recur = func(prefix []string, node *ModelNode) {
		path := append(append([]string(nil), prefix...), node.Name)
		visit(path, node)
		for _, child := range node.children {
			recur(path, child)
		}
	}
	recur(nil, n)
}

// BuildModelTree groups one discovery result into the typed hierarchy. Every
// discovered object appears exactly once even when diagnostics or truncation
// limited the underlying scan; check the Directory's own Truncated flag
// before treating the tree as complete.
func BuildModelTree(dir Directory) *ModelNode {
	root := &ModelNode{Name: "VMD", Kind: "vmd"}
	devices := map[string]*ModelNode{}
	nodes := map[string]*ModelNode{}
	ensure := func(parent *ModelNode, name, kind, fc string) *ModelNode {
		key := parent.Name + "/" + name
		if node := nodes[key]; node != nil {
			return node
		}
		node := &ModelNode{Name: name, Kind: kind, FC: fc}
		parent.children = append(parent.children, node)
		nodes[key] = node
		return node
	}
	objects := append([]DiscoveredObject(nil), dir.Objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Name.String() < objects[j].Name.String() })
	for i := range objects {
		object := objects[i]
		parts := strings.Split(object.Name.Item, "$")
		device := devices[object.Name.Domain]
		if device == nil {
			device = ensure(root, object.Name.Domain, "device", "")
			devices[object.Name.Domain] = device
		}
		if len(parts) < 3 {
			ensure(device, object.Name.Item, "point", object.FC).Object = &objects[i]
			continue
		}
		ln := ensure(device, parts[0], "logical-node", "")
		fc := ensure(ln, parts[1], "fc", parts[1])
		ensure(fc, strings.Join(parts[2:], "$"), "point", parts[1]).Object = &objects[i]
	}
	return root
}

// PointIdentity is the canonical identity of one selected point: the MMS
// domain/item pair, the functional constraint when known, the alternate
// access selection and the declared type observed when the point was
// selected. Display labels are kept separately in a SelectedPoint.
type PointIdentity struct {
	Domain, Item string
	FC           string
	Selection    mms.Selection
	Type         data.TypeSpec
}

// MMSName returns the domain-scoped object name of this identity.
func (p PointIdentity) MMSName() mms.ObjectName {
	return mms.DomainName(p.Domain, p.Item)
}

// SelectedPoint pairs a canonical identity with an optional display label and
// an origin marker: "online" for discovery-derived points and "engineered"
// for SCL-derived ones. The marker is informational; verification always uses
// the identity and type.
type SelectedPoint struct {
	Domain    string        `json:"domain"`
	Item      string        `json:"item"`
	FC        string        `json:"fc,omitempty"`
	Selection mms.Selection `json:"selection,omitempty"`
	Type      data.TypeSpec `json:"type"`
	Label     string        `json:"label,omitempty"`
	Origin    string        `json:"origin,omitempty"`
}

// SelectionDocument is a versioned, sanitized point-selection export. It
// carries no connection credentials or secrets; store those separately.
const SelectionVersion = "1"

type SelectionDocument struct {
	Version string          `json:"version"`
	Created time.Time       `json:"created"`
	Points  []SelectedPoint `json:"points"`
}

// NewSelectionDocument stamps the current version and creation time. The
// point order is preserved and is part of the document's identity.
func NewSelectionDocument(points []SelectedPoint) SelectionDocument {
	return SelectionDocument{Version: SelectionVersion, Created: time.Now().UTC(), Points: append([]SelectedPoint(nil), points...)}
}

// Selection statuses for one verified point.
const (
	SelectionOK          = "ok"
	SelectionMissing     = "missing"
	SelectionTypeChanged = "type-changed"
)

type SelectionCheck struct {
	Reference string
	Status    string
	Detail    string
}

// SelectionReport is the outcome of revalidating a saved selection against
// the current peer declarations.
type SelectionReport struct {
	Version string
	Entries []SelectionCheck
}

// typeSummary renders the observable type identity used for change checks.
func typeSummary(spec data.TypeSpec) string {
	var walk func(s data.TypeSpec, depth int) string
	walk = func(s data.TypeSpec, depth int) string {
		summary := fmt.Sprintf("%s/%d/%d", s.Kind, s.Width, s.Length)
		if depth >= 8 || len(s.Components) == 0 {
			return summary
		}
		parts := make([]string, len(s.Components))
		for i, c := range s.Components {
			parts[i] = c.Name + ":" + walk(c.Type, depth+1)
		}
		return summary + "(" + strings.Join(parts, ",") + ")"
	}
	return walk(spec, 0)
}

// VerifySelection revalidates every point of a saved selection against the
// peer's current declarations: it re-reads each object's declared type,
// applies the saved selection and compares the observable type identity. A
// missing object, a changed type or an inapplicable selection is reported per
// point; nothing is guessed and no remote state is modified. Unknown document
// versions are rejected so older exports fail explicitly instead of being
// silently reinterpreted.
func (c *Client) VerifySelection(ctx context.Context, doc SelectionDocument) (SelectionReport, error) {
	if doc.Version != SelectionVersion {
		return SelectionReport{}, fmt.Errorf("ied: unsupported selection document version %q", doc.Version)
	}
	report := SelectionReport{Version: doc.Version, Entries: make([]SelectionCheck, 0, len(doc.Points))}
	for _, point := range doc.Points {
		reference := point.Domain + "/" + point.Item
		spec, err := c.GetVariableAccessAttributes(ctx, mms.DomainName(point.Domain, point.Item))
		if err != nil {
			report.Entries = append(report.Entries, SelectionCheck{Reference: reference, Status: SelectionMissing, Detail: err.Error()})
			continue
		}
		selected, err := mms.SelectedType(spec, point.Selection)
		if err != nil {
			report.Entries = append(report.Entries, SelectionCheck{Reference: reference, Status: SelectionTypeChanged, Detail: "selection no longer applies: " + err.Error()})
			continue
		}
		if !reflect.DeepEqual(selected, point.Type) && typeSummary(selected) != typeSummary(point.Type) {
			report.Entries = append(report.Entries, SelectionCheck{Reference: reference, Status: SelectionTypeChanged, Detail: fmt.Sprintf("%s -> %s", typeSummary(point.Type), typeSummary(selected))})
			continue
		}
		report.Entries = append(report.Entries, SelectionCheck{Reference: reference, Status: SelectionOK})
	}
	return report, nil
}
