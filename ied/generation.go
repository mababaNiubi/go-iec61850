// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"fmt"
	"reflect"

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
	recheck := datasetGeneration{}
	if recheck.members, err = c.readListMembers(ctx, name); err != nil {
		return DataSetSnapshot{}, err
	}
	if policy.VerifyTypes {
		for _, access := range recheck.members {
			spec, e := c.GetVariableAccessAttributes(ctx, access.Name)
			if e != nil {
				return DataSetSnapshot{}, e
			}
			spec, e = mms.SelectedType(spec, access.Selection)
			if e != nil {
				return DataSetSnapshot{}, e
			}
			recheck.types = append(recheck.types, spec)
		}
	}
	if err := compareGenerations(before, recheck, policy.VerifyTypes); err != nil {
		return DataSetSnapshot{}, err
	}
	if policy.VerifyRCB != (mms.ObjectName{}) {
		block, e := c.ReadReportBlock(ctx, policy.VerifyRCB)
		if e != nil {
			return DataSetSnapshot{}, e
		}
		if block.ConfRev != before.confRev {
			return DataSetSnapshot{}, &GenerationChangeError{What: "RCB ConfRev", From: fmt.Sprint(before.confRev), To: fmt.Sprint(block.ConfRev)}
		}
	}
	snapshot.Verified = true
	return snapshot, nil
}
