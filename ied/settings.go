// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// SettingGroups describes the number of groups, active group and currently edited group.
// Editing is zero when no group is reserved.
type SettingGroups struct{ Count, Active, Editing uint32 }

// SettingResult preserves confirmation and activation evidence separately when a later step
// fails.
type SettingResult struct{ Committed, Activated bool }

// ActivateSettings switches domain/LLN0.SGCB to an existing group numbered
// 1..NumOfSG without editing values. A timeout can leave the remote activation
// outcome uncertain; read SettingGroups before deciding whether to retry.
func (c *Client) ActivateSettings(ctx context.Context, domain string, group uint32) error {
	if err := c.acquire(ctx, c.settingToken); err != nil {
		return err
	}
	defer func() { <-c.settingToken }()
	state, err := c.SettingGroups(ctx, domain)
	if err != nil {
		return err
	}
	if group < 1 || group > state.Count {
		return fmt.Errorf("ied: setting group outside 1..%d", state.Count)
	}
	name, err := ObjectName(domain, "SP", "LLN0.SGCB.ActSG")
	if err != nil {
		return err
	}
	return c.Write(ctx, name, data.Unsigned(uint64(group)))
}

// SettingGroups reads NumOfSG, ActSG and EditSG from domain/LLN0.SGCB.
// ctx bounds the requests; Editing is zero when no group is reserved.
func (c *Client) SettingGroups(ctx context.Context, domain string) (SettingGroups, error) {
	var out SettingGroups
	for _, field := range []struct {
		name   string
		target *uint32
	}{{"NumOfSG", &out.Count}, {"ActSG", &out.Active}, {"EditSG", &out.Editing}} {
		n, err := ObjectName(domain, "SP", "LLN0.SGCB."+field.name)
		if err != nil {
			return out, err
		}
		v, err := c.Read(ctx, n)
		if err != nil {
			return out, err
		}
		number, err := v.Uint()
		if err != nil || number > 255 {
			return out, fmt.Errorf("ied: invalid SGCB.%s", field.name)
		}
		*field.target = uint32(number)
	}
	if out.Count == 0 || out.Active == 0 || out.Active > out.Count || out.Editing > out.Count {
		return out, fmt.Errorf("ied: invalid setting group state")
	}
	return out, nil
}

// EditSettings edits and confirms one group. Values use SE names in domain.
// Activation is explicit. Committed and Activated distinguish the evidence if
// a later request fails. Before confirmation a failure releases the edit copy.
func (c *Client) EditSettings(ctx context.Context, domain string, group uint32, values map[mms.ObjectName]data.Value, activate bool) (result SettingResult, err error) {
	if len(values) == 0 {
		return result, fmt.Errorf("ied: no setting edits")
	}
	if err = c.acquire(ctx, c.settingToken); err != nil {
		return result, err
	}
	defer func() { <-c.settingToken }()
	state, err := c.SettingGroups(ctx, domain)
	if err != nil {
		return result, err
	}
	if group < 1 || group > state.Count {
		return result, fmt.Errorf("ied: setting group outside declared range")
	}
	var names []mms.ObjectName
	for n, v := range values {
		if n.Scope != mms.DomainScope || n.Domain != domain || !strings.Contains(n.Item, "$SE$") {
			return result, fmt.Errorf("ied: settings require SE names in the selected domain")
		}
		spec, e := c.GetVariableAccessAttributes(ctx, n)
		if e != nil {
			return result, e
		}
		if e = data.ValidateValue(spec, v); e != nil {
			return result, e
		}
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Item < names[j].Item })
	attribute := func(field string) mms.ObjectName { return mms.DomainName(domain, "LLN0$SP$SGCB$"+field) }
	if err = c.Write(ctx, attribute("EditSG"), data.Unsigned(uint64(group))); err != nil {
		return result, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if cleanup := c.Write(release, attribute("EditSG"), data.Unsigned(0)); cleanup != nil {
			c.Close()
			if err == nil {
				err = cleanup
			}
		}
	}()
	for _, n := range names {
		if err = c.Write(ctx, n, values[n]); err != nil {
			return result, err
		}
	}
	if err = c.Write(ctx, attribute("CnfEdit"), data.Boolean(true)); err != nil {
		return result, err
	}
	result.Committed = true
	if activate {
		err = c.Write(ctx, attribute("ActSG"), data.Unsigned(uint64(group)))
		result.Activated = err == nil
	}
	return result, err
}
