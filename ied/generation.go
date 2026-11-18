// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// ErrConfigurationChanged marks outcomes where a remote declaration changed
// between the reads that established a generation and the read being
// verified. Double reads detect observed changes; they do not create an
// atomic remote configuration transaction and cannot exclude an unobserved
// change-and-revert. Callers rebind instead of retrying blindly.
var ErrConfigurationChanged = errors.New("ied: remote configuration changed")

// GenerationChangeError details one detected declaration change. What names
// the changed aspect ("dataset member count", "RCB ConfRev", …); From and To
// describe the observed values.
type GenerationChangeError struct {
	What, From, To string
}

func (e *GenerationChangeError) Error() string {
	return fmt.Sprintf("ied: %s changed from %s to %s between generation reads", e.What, e.From, e.To)
}
func (e *GenerationChangeError) Unwrap() error { return ErrConfigurationChanged }

// GenerationPolicy selects which declarations are rechecked after values are
// read. The zero value rechecks dataset membership (the baseline CFG-01
// double read); VerifyTypes also re-reads member types and VerifyRCB requires
// an unchanged ConfRev on the named report control block. Every check uses
// already-confirmed MMS services; no service behavior is assumed beyond what
// the peer actually returns.
type GenerationPolicy struct {
	VerifyMembers bool
	VerifyTypes   bool
	VerifyRCB     mms.ObjectName // empty skips the RCB revision check
}

// datasetGeneration is one observed dataset declaration: ordered members,
// their selected types and, when known, the owning RCB's ConfRev. A local
// fingerprint is not a remote ConfRev: ConfRevKnown distinguishes them.
type datasetGeneration struct {
	members      []mms.VariableAccess
	types        []data.TypeSpec
	confRev      uint32
	confRevKnown bool
}

func (c *Client) readListMembers(ctx context.Context, name mms.ObjectName) ([]mms.VariableAccess, error) {
	list, err := c.GetNamedListAttributes(ctx, name)
	if err != nil {
		return nil, err
	}
	return list.Variables, nil
}

// readDatasetGeneration reads the ordered members and their selected types.
func (c *Client) readDatasetGeneration(ctx context.Context, name mms.ObjectName) (datasetGeneration, error) {
	members, err := c.readListMembers(ctx, name)
	if err != nil {
		return datasetGeneration{}, err
	}
	g := datasetGeneration{members: members}
	for _, access := range members {
		spec, e := c.GetVariableAccessAttributes(ctx, access.Name)
		if e != nil {
			return datasetGeneration{}, e
		}
		spec, e = mms.SelectedType(spec, access.Selection)
		if e != nil {
			return datasetGeneration{}, e
		}
		g.types = append(g.types, spec)
	}
	return g, nil
}

func sameAccess(a, b mms.VariableAccess) bool {
	return a.Name == b.Name && reflect.DeepEqual(a.Selection, b.Selection)
}

// compareGenerations returns a typed change error when the rechecked
// declaration no longer matches the generation used to read values.
func compareGenerations(before, after datasetGeneration, checkTypes bool) error {
	if len(before.members) != len(after.members) {
		return &GenerationChangeError{What: "dataset member count", From: fmt.Sprint(len(before.members)), To: fmt.Sprint(len(after.members))}
	}
	for i := range before.members {
		if !sameAccess(before.members[i], after.members[i]) {
			return &GenerationChangeError{What: fmt.Sprintf("dataset member %d", i), From: before.members[i].Name.String(), To: after.members[i].Name.String()}
		}
	}
	if checkTypes {
		for i := range before.types {
			if !reflect.DeepEqual(before.types[i], after.types[i]) {
				return &GenerationChangeError{What: fmt.Sprintf("dataset member %d type", i), From: fmt.Sprint(before.types[i].Kind), To: fmt.Sprint(after.types[i].Kind)}
			}
		}
	}
	return nil
}

// ReadDataSetVerified reads current dataset values under a CFG-01 declaration
// recheck: the member list (plus types and the RCB ConfRev per policy) is
// read again after the values. On any observed change it returns an error
// wrapping ErrConfigurationChanged instead of publishing values under a stale
// generation. On success the snapshot is marked Verified; ConfRev is set only
// when policy.VerifyRCB observed and re-verified it, keeping "revision
// unknown" distinct from a verified revision.
func (c *Client) ReadDataSetVerified(ctx context.Context, name mms.ObjectName, policy GenerationPolicy) (DataSetSnapshot, error) {
	before, err := c.readDatasetGeneration(ctx, name)
	if err != nil {
		return DataSetSnapshot{}, err
	}
	if policy.VerifyRCB != (mms.ObjectName{}) {
		block, e := c.ReadReportBlock(ctx, policy.VerifyRCB)
		if e != nil {
			return DataSetSnapshot{}, e
		}
		before.confRev, before.confRevKnown = block.ConfRev, true
	}
	cfg := ReportConfig{DataSet: name.String(), Members: before.members, Types: before.types}
	if before.confRevKnown {
		cfg.ConfRev = before.confRev
	}
	snapshot, err := c.readSnapshot(ctx, cfg)
	if err != nil {
		return DataSetSnapshot{}, err
	}
	if err := c.verifyGeneration(ctx, name, before, policy); err != nil {
		return DataSetSnapshot{}, err
	}
	snapshot.Verified = true
	return snapshot, nil
}

// verifyGeneration rechecks a declaration after values were read with it.
func (c *Client) verifyGeneration(ctx context.Context, name mms.ObjectName, before datasetGeneration, policy GenerationPolicy) error {
	recheck := datasetGeneration{}
	members, err := c.readListMembers(ctx, name)
	if err != nil {
		return err
	}
	recheck.members = members
	if policy.VerifyTypes {
		for _, access := range members {
			spec, e := c.GetVariableAccessAttributes(ctx, access.Name)
			if e != nil {
				return e
			}
			spec, e = mms.SelectedType(spec, access.Selection)
			if e != nil {
				return e
			}
			recheck.types = append(recheck.types, spec)
		}
	}
	if err := compareGenerations(before, recheck, policy.VerifyTypes); err != nil {
		return err
	}
	if policy.VerifyRCB != (mms.ObjectName{}) {
		block, e := c.ReadReportBlock(ctx, policy.VerifyRCB)
		if e != nil {
			return e
		}
		if block.ConfRev != before.confRev {
			return &GenerationChangeError{What: "RCB ConfRev", From: fmt.Sprint(before.confRev), To: fmt.Sprint(block.ConfRev)}
		}
	}
	return nil
}

// BindOptions configures a reusable dataset binding. The default reuses the
// declaration read at bind time until an explicit Refresh: a metadata cache
// alone cannot prove the remote layout is unchanged, so callers choose
// per-read CFG-01 verification when the peer may reconfigure concurrently.
type BindOptions struct {
	// VerifyEachRead rechecks the declaration after every value read.
	VerifyEachRead bool
	// VerifyTypes also re-reads member types on every recheck (bind and
	// refresh always read types once to build the generation).
	VerifyTypes bool
	// VerifyRCB optionally names an RCB whose ConfRev must hold for the
	// binding to stay valid.
	VerifyRCB mms.ObjectName
}

// BindingInfo describes one bound generation. GenerationID increases by one
// on every observed change; it is a local counter, not a remote ConfRev.
type BindingInfo struct {
	DataSet       string
	Members       int
	ConfRev       uint32
	ConfRevKnown  bool
	GenerationID  uint64
	EachReadCheck bool
}

// DataSetBinding is a reusable acquisition handle over one remote dataset.
// Read reuses the bound metadata instead of re-requesting one type per
// member on every poll. Detected changes mark the binding stale; Read then
// fails with ErrConfigurationChanged until Refresh publishes the next
// generation. Close releases local state only: it never deletes the remote
// list or closes the shared client.
type DataSetBinding struct {
	client *Client
	name   mms.ObjectName
	policy BindOptions
	mu     sync.Mutex
	gen    datasetGeneration
	stale  bool
	closed bool
	next   uint64
}

// BindDataSet reads the dataset declaration and selected types once and
// returns a reusable binding.
func (c *Client) BindDataSet(ctx context.Context, name mms.ObjectName, options BindOptions) (*DataSetBinding, error) {
	gen, err := c.readDatasetGeneration(ctx, name)
	if err != nil {
		return nil, err
	}
	if options.VerifyRCB != (mms.ObjectName{}) {
		block, e := c.ReadReportBlock(ctx, options.VerifyRCB)
		if e != nil {
			return nil, e
		}
		gen.confRev, gen.confRevKnown = block.ConfRev, true
	}
	return &DataSetBinding{client: c, name: name, policy: options, gen: gen, next: 1}, nil
}

// Info reports the currently bound generation.
func (b *DataSetBinding) Info() BindingInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BindingInfo{DataSet: b.name.String(), Members: len(b.gen.members), ConfRev: b.gen.confRev, ConfRevKnown: b.gen.confRevKnown, GenerationID: b.next - 1, EachReadCheck: b.policy.VerifyEachRead}
}

// Read acquires current values through the bound declaration. Without
// VerifyEachRead the snapshot is unverified (the declaration is not
// rechecked); with it, a changed remote declaration returns an error wrapping
// ErrConfigurationChanged and marks the binding stale.
func (b *DataSetBinding) Read(ctx context.Context) (DataSetSnapshot, error) {
	b.mu.Lock()
	stale, closed, gen, policy := b.stale, b.closed, b.gen, b.policy
	b.mu.Unlock()
	if closed {
		return DataSetSnapshot{}, mms.ErrClosed
	}
	if stale {
		return DataSetSnapshot{}, fmt.Errorf("ied: binding is stale after a configuration change: %w", ErrConfigurationChanged)
	}
	cfg := ReportConfig{DataSet: b.name.String(), Members: gen.members, Types: gen.types}
	if gen.confRevKnown {
		cfg.ConfRev = gen.confRev
	}
	snapshot, err := b.client.readSnapshot(ctx, cfg)
	if err != nil {
		return DataSetSnapshot{}, err
	}
	if !policy.VerifyEachRead {
		return snapshot, nil
	}
	if err := b.client.verifyGeneration(ctx, b.name, gen, GenerationPolicy{VerifyMembers: true, VerifyTypes: policy.VerifyTypes, VerifyRCB: policy.VerifyRCB}); err != nil {
		if errors.Is(err, ErrConfigurationChanged) {
			b.mu.Lock()
			b.stale = true
			b.mu.Unlock()
		}
		return DataSetSnapshot{}, err
	}
	snapshot.Verified = true
	return snapshot, nil
}

// Refresh re-reads the declaration and publishes the next generation when it
// changed, clearing a stale marker. The returned info lets callers compare
// GenerationIDs and rebuild dependent decoders or mappings.
func (b *DataSetBinding) Refresh(ctx context.Context) (BindingInfo, error) {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return BindingInfo{}, mms.ErrClosed
	}
	gen, err := b.client.readDatasetGeneration(ctx, b.name)
	if err != nil {
		return BindingInfo{}, err
	}
	if b.policy.VerifyRCB != (mms.ObjectName{}) {
		block, e := b.client.ReadReportBlock(ctx, b.policy.VerifyRCB)
		if e != nil {
			return BindingInfo{}, e
		}
		gen.confRev, gen.confRevKnown = block.ConfRev, true
	}
	b.mu.Lock()
	changed := compareGenerations(b.gen, gen, true) != nil || b.gen.confRevKnown && gen.confRev != b.gen.confRev
	if changed {
		b.next++
	}
	b.gen = gen
	b.stale = false
	info := BindingInfo{DataSet: b.name.String(), Members: len(gen.members), ConfRev: gen.confRev, ConfRevKnown: gen.confRevKnown, GenerationID: b.next - 1, EachReadCheck: b.policy.VerifyEachRead}
	b.mu.Unlock()
	return info, nil
}

// Close releases the local binding. The remote list and the client stay
// untouched.
func (b *DataSetBinding) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}
