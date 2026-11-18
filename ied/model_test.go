// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func TestBuildModelTreeGroupsHierarchy(t *testing.T) {
	reader, _, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir, err := reader.Discover(ctx, DiscoveryOptions{Domain: "LD"})
	if err != nil {
		t.Fatal(err)
	}
	if dir.Truncated {
		t.Fatalf("discovery truncated: %s", dir.TruncationReason)
	}
	root := BuildModelTree(dir)
	if root.Kind != "vmd" || len(root.Children()) == 0 {
		t.Fatalf("unexpected root: %+v", root)
	}
	points := 0
	devices, lns, fcs := 0, 0, 0
	root.Walk(func(path []string, node *ModelNode) {
		switch node.Kind {
		case "device":
			devices++
		case "logical-node":
			lns++
		case "fc":
			fcs++
		case "point":
			points++
		}
	})
	if devices != 1 || lns == 0 || fcs == 0 {
		t.Fatalf("hierarchy incomplete: devices=%d ln=%d fc=%d", devices, lns, fcs)
	}
	if points != len(dir.Objects) {
		t.Fatalf("points %d != discovered objects %d", points, len(dir.Objects))
	}
}

// T-DISC-02: a saved selection survives a JSON roundtrip and revalidates
// against the same peer; removed or retyped points are reported explicitly.
func TestSelectionDocumentRoundTripAndVerification(t *testing.T) {
	reader, _, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stVal := mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal")
	spec, err := reader.GetVariableAccessAttributes(ctx, stVal)
	if err != nil {
		t.Fatal(err)
	}
	doc := NewSelectionDocument([]SelectedPoint{
		{Domain: "LD", Item: "GGIO1$ST$SPCSO1$stVal", FC: "ST", Type: spec, Label: "breaker command status", Origin: "online"},
		{Domain: "LD", Item: "GGIO1$ST$Missing$stVal", FC: "ST", Type: spec, Origin: "online"},
		{Domain: "LD", Item: "GGIO1$ST$Ind1$stVal", FC: "ST", Type: mms.TypeSpec{Kind: mms.KindFloat, Width: 64}, Origin: "online"},
	})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var restored SelectionDocument
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Version != SelectionVersion || len(restored.Points) != 3 {
		t.Fatalf("roundtrip lost content: %+v", restored)
	}
	report, err := reader.VerifySelection(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, entry := range report.Entries {
		statuses[entry.Reference] = entry.Status
	}
	if statuses["LD/GGIO1$ST$SPCSO1$stVal"] != SelectionOK {
		t.Fatalf("unchanged point must verify: %+v", report.Entries)
	}
	if statuses["LD/GGIO1$ST$Missing$stVal"] != SelectionMissing {
		t.Fatalf("removed point must be reported missing: %+v", report.Entries)
	}
	if statuses["LD/GGIO1$ST$Ind1$stVal"] != SelectionTypeChanged {
		t.Fatalf("retyped point must be reported changed: %+v", report.Entries)
	}
}

func TestVerifySelectionRejectsUnknownVersion(t *testing.T) {
	reader, _, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	doc := SelectionDocument{Version: "0", Points: []SelectedPoint{{Domain: "LD", Item: "x"}}}
	if _, err := reader.VerifySelection(ctx, doc); err == nil {
		t.Fatal("unknown document versions must be rejected")
	}
}
