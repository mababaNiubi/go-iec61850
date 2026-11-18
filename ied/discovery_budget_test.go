// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"testing"
	"time"
)

// T-DISC-01: exhausting the request budget stops before issuing further work,
// keeps completed results and reports deterministic counts.
func TestDiscoveryRequestBudget(t *testing.T) {
	reader, _, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir, err := reader.Discover(ctx, DiscoveryOptions{Domain: "LD", MaxRequests: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !dir.Truncated || dir.TruncationReason != "request budget" {
		t.Fatalf("expected request-budget truncation, got truncated=%t reason=%q", dir.Truncated, dir.TruncationReason)
	}
	if dir.Requests > 3 {
		t.Fatalf("issued %d requests beyond the budget", dir.Requests)
	}
	if len(dir.Objects) == 0 && len(dir.DataSets) == 0 {
		t.Fatal("completed results were not preserved")
	}
	// A higher budget yields a superset and completes.
	full, err := reader.Discover(ctx, DiscoveryOptions{Domain: "LD", MaxRequests: 64})
	if err != nil {
		t.Fatal(err)
	}
	if full.Truncated {
		t.Fatalf("continuation still truncated: %s", full.TruncationReason)
	}
	if len(full.Objects) < len(dir.Objects) {
		t.Fatalf("continuation lost objects: %d < %d", len(full.Objects), len(dir.Objects))
	}
}

func TestDiscoveryByteBudget(t *testing.T) {
	reader, _, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir, err := reader.Discover(ctx, DiscoveryOptions{Domain: "LD", MaxBytes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !dir.Truncated || dir.TruncationReason != "name-byte budget" {
		t.Fatalf("expected name-byte truncation, got truncated=%t reason=%q", dir.Truncated, dir.TruncationReason)
	}
}

func TestDiscoveryOptionsValidation(t *testing.T) {
	reader, _, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, options := range []DiscoveryOptions{
		{MaxRequests: -1},
		{MaxRequests: 65537},
		{MaxObjects: -1},
		{MaxBytes: -1},
	} {
		if _, err := reader.Discover(ctx, options); err == nil {
			t.Fatalf("expected validation error for %+v", options)
		}
	}
}

func TestDiscoveryCancellation(t *testing.T) {
	reader, _, _ := bindPeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Discover(ctx, DiscoveryOptions{}); err == nil {
		t.Fatal("canceled context must surface its error")
	}
}
