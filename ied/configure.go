// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// CreateDataSet validates the member declarations, then defines one
// domain-scoped named variable list on the peer. Members are pre-validated
// with confirmed attribute reads so the first invalid member fails locally
// with its name instead of a remote definition error. Association-private
// lists are deliberately not offered: they disappear with the connection and
// cannot back a domain report control block. The list is deletable over MMS
// exactly as the peer's profile permits.
func (c *Client) CreateDataSet(ctx context.Context, name mms.ObjectName, members []mms.VariableAccess) error {
	if name.Scope != mms.DomainScope {
		return fmt.Errorf("ied: dataset name must be domain-scoped")
	}
	if len(members) == 0 || len(members) > 4096 {
		return fmt.Errorf("ied: dataset needs 1..4096 members")
	}
	for i, member := range members {
		spec, err := c.GetVariableAccessAttributes(ctx, member.Name)
		if err != nil {
			return fmt.Errorf("member %d (%s): %w", i, member.Name, err)
		}
		if _, err = mms.SelectedType(spec, member.Selection); err != nil {
			return fmt.Errorf("member %d (%s): %w", i, member.Name, err)
		}
	}
	return c.raw.DefineNamedList(ctx, name, members)
}

// DeleteDataSet removes a remotely deletable list. A list the peer marks
// non-deletable (an engineered static dataset) is reported through the
// DeleteResult without error, mirroring the MMS service semantics.
func (c *Client) DeleteDataSet(ctx context.Context, name mms.ObjectName) (mms.DeleteResult, error) {
	return c.raw.DeleteNamedList(ctx, name)
}

// RCBConfiguration is the requested report-control-block change. DataSet is
// the target dataset; an empty RptID keeps the current report identifier.
// Only these online-reconfigurable fields are offered: trigger options and
// optional fields belong to the subscription call, whose encoding follows the
// peer's declaration.
type RCBConfiguration struct {
	DataSet mms.ObjectName
	RptID   string
}

// ConfiguredRCB is the post-configuration state: the re-read declaration
// carrying the peer's new ConfRev, plus the ordered members and selected
// types of the bound dataset ready for a decoder or binding.
type ConfiguredRCB struct {
	Block       mms.ObjectName
	Declaration RemoteReportBlock
	Members     []mms.VariableAccess
	Types       []data.TypeSpec
}

// ConfigureRCB rebinds a disabled report control block to a dataset in the
// order the modeled peers implement: read the declaration, take ownership
// through Resv when the peer requires it, write DatSet (the peer bumps
// ConfRev itself), optionally write RptID, then re-read the declaration and
// resolve the bound members. The caller subscribes afterwards; this call
// never enables the block. A peer that denies an online field reports the
// remote error unchanged, so capability restrictions stay visible instead of
// being silently skipped.
func (c *Client) ConfigureRCB(ctx context.Context, block mms.ObjectName, config RCBConfiguration) (ConfiguredRCB, error) {
	if block.Scope != mms.DomainScope {
		return ConfiguredRCB{}, fmt.Errorf("ied: expected domain-scoped report control block")
	}
	if config.DataSet == (mms.ObjectName{}) || config.DataSet.Scope != mms.DomainScope {
		return ConfiguredRCB{}, fmt.Errorf("ied: expected a domain-scoped target dataset")
	}
	out := ConfiguredRCB{Block: block}
	attr := func(field string) mms.ObjectName { return mms.DomainName(block.Domain, block.Item+"$"+field) }
	info, err := c.ReadReportBlock(ctx, block)
	if err != nil {
		return out, err
	}
	if info.Enabled {
		return out, fmt.Errorf("ied: report control block is enabled; stop its subscription before reconfiguring")
	}
	if err = c.Write(ctx, attr("Resv"), data.Boolean(true)); err != nil {
		// Peers without a reservation attribute for this block reject the
		// write; ownership is then enforced per write as implemented.
		if !isMissingAttribute(err) {
			return out, fmt.Errorf("reserve: %w", err)
		}
	}
	target := config.DataSet.String()
	if info.DataSet != target {
		if err = c.Write(ctx, attr("DatSet"), mms.VisibleString(target)); err != nil {
			return out, fmt.Errorf("write DatSet: %w", err)
		}
	}
	if config.RptID != "" && config.RptID != info.ID {
		if err = c.Write(ctx, attr("RptID"), mms.VisibleString(config.RptID)); err != nil {
			return out, fmt.Errorf("write RptID: %w", err)
		}
	}
	declaration, err := c.ReadReportBlock(ctx, block)
	if err != nil {
		return out, err
	}
	if declaration.DataSet != target {
		return out, fmt.Errorf("ied: peer did not accept the dataset binding: %s", declaration.DataSet)
	}
	out.Declaration = declaration
	if out.Members, err = c.readListMembers(ctx, config.DataSet); err != nil {
		return out, err
	}
	for _, member := range out.Members {
		spec, e := c.GetVariableAccessAttributes(ctx, member.Name)
		if e != nil {
			return out, e
		}
		spec, e = mms.SelectedType(spec, member.Selection)
		if e != nil {
			return out, e
		}
		out.Types = append(out.Types, spec)
	}
	return out, nil
}

// isMissingAttribute reports remote errors meaning the field does not exist
// on this peer, so callers can distinguish an absent reservation attribute
// from a refused one.
func isMissingAttribute(err error) bool {
	return strings.Contains(err.Error(), "non-existent") || strings.Contains(err.Error(), "undefined")
}
