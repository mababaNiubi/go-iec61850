// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

type triggerXML struct {
	Data    bool  `xml:"dchg,attr"`
	Quality bool  `xml:"qchg,attr"`
	Update  bool  `xml:"dupd,attr"`
	Period  bool  `xml:"period,attr"`
	GI      *bool `xml:"gi,attr"`
}
type reportXML struct {
	Name       string     `xml:"name,attr"`
	ID         string     `xml:"rptID,attr"`
	Set        string     `xml:"datSet,attr"`
	Revision   uint32     `xml:"confRev,attr"`
	Buffered   bool       `xml:"buffered,attr"`
	Indexed    *bool      `xml:"indexed,attr"`
	BufferTime uint32     `xml:"bufTime,attr"`
	Period     uint32     `xml:"intgPd,attr"`
	Trigger    triggerXML `xml:"TrgOps"`
	Options    struct {
		Sequence  bool `xml:"seqNum,attr"`
		Time      bool `xml:"timeStamp,attr"`
		Reason    bool `xml:"reasonCode,attr"`
		DataSet   bool `xml:"dataSet,attr"`
		Reference bool `xml:"dataRef,attr"`
		Overflow  bool `xml:"bufOvfl,attr"`
		Entry     bool `xml:"entryID,attr"`
		Revision  bool `xml:"configRef,attr"`
	} `xml:"OptFields"`
	Enabled struct {
		Max int `xml:"max,attr"`
	} `xml:"RptEnabled"`
}

type ReportInfo struct {
	Name     mms.ObjectName
	DataSet  mms.ObjectName
	Buffered bool
	ConfRev  uint32
}
type reportRecord struct {
	id        uint64
	timestamp time.Time
	values    []mms.Value
	reasons   []byte
}
type reportBlock struct {
	info               ReportInfo
	id                 string
	options            uint16
	triggers           byte
	bufferTime, period time.Duration
	owner              *mms.Association
	enabled            bool
	reserved           bool
	queue              []reportRecord
	capacity           int
	nextID, cursor     uint64
	sequence           uint16
	generation         uint64
	watch              [][]mms.ObjectName
	overflow           bool
	overflowEpoch      uint64
	pending            *reportRecord
	due, integrity     time.Time
}

// Reports enumerates the actual indexed RCB instances available over MMS.
func (d *IED) Reports() []ReportInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]ReportInfo, 0, len(d.reports))
	for _, b := range d.reports {
		out = append(out, b.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name.String() < out[j].Name.String() })
	return out
}

// SetReportBufferCapacity bounds retained events per BRCB. Configure before
// serving or acquisition. Shrinking a populated buffer is rejected.
func (d *IED) SetReportBufferCapacity(capacity int) error {
	if capacity < 1 || capacity > 4096 {
		return fmt.Errorf("scl: report capacity must be 1..4096")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, b := range d.reports {
		if b.owner != nil || len(b.queue) > 0 {
			return fmt.Errorf("scl: report block is in use")
		}
	}
	for _, b := range d.reports {
		b.capacity = capacity
	}
	return nil
}

func bitsValue(bits uint16, width int) mms.Value {
	raw := []byte{byte(bits >> 8), byte(bits)}
	v, _ := mms.BitString(raw[:(width+7)/8], width)
	return v
}
func bitSpec(width int) mms.TypeSpec  { return mms.TypeSpec{Kind: mms.KindBitString, Length: width} }
func uintSpec(width int) mms.TypeSpec { return mms.TypeSpec{Kind: mms.KindUnsigned, Width: width} }
func boolSpec() mms.TypeSpec          { return mms.TypeSpec{Kind: mms.KindBoolean} }
func textSpec() mms.TypeSpec          { return mms.TypeSpec{Kind: mms.KindVisibleString, Length: -129} }

func (d *IED) addService(name mms.ObjectName, components []mms.Component, values []mms.Value) error {
	spec := mms.TypeSpec{Kind: mms.KindStructure, Components: components}
	parts := strings.Split(name.Item, "$")
	if len(parts) != 3 {
		return fmt.Errorf("scl: invalid service name %s", name)
	}
	parent := mms.DomainName(name.Domain, parts[0]+"$"+parts[1])
	value := mms.Structure(values...)
	if _, err := d.model.Type(context.Background(), parent); err == nil {
		if err = d.model.AddComponent(context.Background(), parent, mms.Component{Name: parts[2], Type: spec}, value); err != nil {
			return err
		}
	} else {
		parent.Item = parts[0]
		group := mms.TypeSpec{Kind: mms.KindStructure, Components: []mms.Component{{Name: parts[2], Type: spec}}}
		if err := d.model.AddComponent(context.Background(), parent, mms.Component{Name: parts[1], Type: group}, mms.Structure(value)); err != nil {
			return err
		}
	}
	root := mms.DomainName(name.Domain, parts[0])
	rootType, err := d.model.Type(context.Background(), root)
	if err != nil {
		return err
	}
	return d.indexType(root, rootType)
}

func (d *IED) addReports(ld, ln string, x reportXML) error {
	set := mms.ObjectName{}
	if x.Set != "" {
		set = mms.DomainName(d.domains[ld], ln+"$"+x.Set)
	}
	if !identifier(x.Name) || x.Revision == 0 || x.Set != "" && len(d.sets[set].Variables) == 0 {
		return fmt.Errorf("scl: invalid ReportControl %s/%s", ld, x.Name)
	}
	count := x.Enabled.Max
	if count == 0 {
		count = 1
	}
	if count < 1 || count > 64 {
		return fmt.Errorf("scl: RptEnabled.max must be 1..64")
	}
	indexed := x.Indexed == nil || *x.Indexed
	if !indexed && count != 1 {
		return fmt.Errorf("scl: unindexed RCB requires max=1")
	}
	var options uint16
	for i, enabled := range []bool{false, x.Options.Sequence, x.Options.Time, x.Options.Reason, x.Options.DataSet, x.Options.Reference, x.Options.Overflow, x.Options.Entry, x.Options.Revision} {
		if enabled {
			options |= 0x8000 >> i
		}
	}
	var triggers byte
	for i, enabled := range []bool{false, x.Trigger.Data, x.Trigger.Quality, x.Trigger.Update, x.Trigger.Period, x.Trigger.GI == nil || *x.Trigger.GI} {
		if enabled {
			triggers |= 0x80 >> i
		}
	}
	fc := "RP"
	if x.Buffered {
		fc = "BR"
	}
	for i := 1; i <= count; i++ {
		instance := x.Name
		if indexed {
			instance += fmt.Sprintf("%02d", i)
		}
		name := mms.DomainName(d.domains[ld], ln+"$"+fc+"$"+instance)
		id := x.ID
		if id == "" {
			id = name.String()
		} else if indexed && count > 1 {
			id += fmt.Sprintf("%02d", i)
		}
		if len(id) > 129 {
			return fmt.Errorf("scl: RptID exceeds 129 characters")
		}
		if _, exists := d.reports[name]; exists {
			return fmt.Errorf("scl: duplicate RCB %s", name)
		}
		b := &reportBlock{info: ReportInfo{name, set, x.Buffered, x.Revision}, id: id, options: options, triggers: triggers, bufferTime: time.Duration(x.BufferTime) * time.Millisecond, period: time.Duration(x.Period) * time.Millisecond, capacity: 256, nextID: uint64(time.Now().UnixNano())}
		for _, member := range d.sets[set].Variables {
			var watched []mms.ObjectName
			for trigger := range d.triggers {
				if overlaps(accessReference(member), trigger) {
					watched = append(watched, trigger)
				}
			}
			b.watch = append(b.watch, watched)
		}
		var cs []mms.Component
		var vs []mms.Value
		add := func(n string, t mms.TypeSpec, v mms.Value) {
			cs = append(cs, mms.Component{Name: n, Type: t})
			vs = append(vs, v)
		}
		add("RptID", textSpec(), mms.VisibleString(id))
		add("RptEna", boolSpec(), mms.Boolean(false))
		if !x.Buffered {
			add("Resv", boolSpec(), mms.Boolean(false))
		}
		add("DatSet", textSpec(), mms.VisibleString(set.String()))
		add("ConfRev", uintSpec(32), mms.Unsigned(uint64(x.Revision)))
		add("OptFlds", bitSpec(10), bitsValue(options, 10))
		add("BufTm", uintSpec(32), mms.Unsigned(uint64(x.BufferTime)))
		width := 8
		if x.Buffered {
			width = 16
		}
		add("SqNum", uintSpec(width), mms.Unsigned(0))
		add("TrgOps", bitSpec(6), bitsValue(uint16(triggers)<<8, 6))
		add("IntgPd", uintSpec(32), mms.Unsigned(uint64(x.Period)))
		add("GI", boolSpec(), mms.Boolean(false))
		if x.Buffered {
			add("PurgeBuf", boolSpec(), mms.Boolean(false))
			add("EntryID", mms.TypeSpec{Kind: mms.KindOctetString, Length: 8}, mms.OctetString(make([]byte, 8)))
			add("TimeOfEntry", mms.TypeSpec{Kind: mms.KindBinaryTime, Length: 6}, binaryTime(time.Now()))
		}
		if err := d.addService(name, cs, vs); err != nil {
			return err
		}
		d.reports[name] = b
	}
	return nil
}

func binaryTime(t time.Time) mms.Value {
	t = t.UTC()
	days := t.Unix()/86400 - 5113
	var raw [6]byte
	binary.BigEndian.PutUint32(raw[:4], uint32((t.Hour()*3600+t.Minute()*60+t.Second())*1000+t.Nanosecond()/1000000))
	binary.BigEndian.PutUint16(raw[4:], uint16(days))
	v, _ := mms.BinaryTime(raw[:])
	return v
}
func entryID(id uint64) mms.Value {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], id)
	return mms.OctetString(raw[:])
}

func (d *IED) reportValuesLocked() map[mms.ObjectName]mms.Value {
	values := map[mms.ObjectName]mms.Value{}
	for _, b := range d.reports {
		for _, a := range d.sets[b.info.DataSet].Variables {
			key := accessReference(a)
			if _, ok := values[key]; !ok {
				values[key], _ = d.model.Read(context.Background(), a)
			}
		}
		for _, watch := range b.watch {
			for _, name := range watch {
				if _, ok := values[name]; !ok {
					values[name], _ = d.triggerValue(context.Background(), name)
				}
			}
		}
	}
	return values
}

func (d *IED) captureReportsLocked(previous map[mms.ObjectName]mms.Value, changes []mms.Assignment) {
	now := time.Now()
	for _, b := range d.reports {
		if !b.info.Buffered && !b.enabled {
			continue
		}
		set := d.sets[b.info.DataSet]
		record := reportRecord{timestamp: now, values: make([]mms.Value, len(set.Variables)), reasons: make([]byte, len(set.Variables))}
		triggered := false
		for i, a := range set.Variables {
			record.values[i], _ = d.model.Read(context.Background(), a)
			for _, trigger := range b.watch[i] {
				for _, change := range changes {
					if !overlaps(change.Name, trigger) || !overlaps(change.Name, a.Name) {
						continue
					}
					current, _ := d.triggerValue(context.Background(), trigger)
					changed := !previous[trigger].Equal(current)
					if strings.HasPrefix(accessReference(a).Item, trigger.Item+"$") {
						changed = !previous[accessReference(a)].Equal(record.values[i])
					}
					reason := d.triggers[trigger]
					if changed {
						record.reasons[i] |= reason & b.triggers & 0x60
					}
					record.reasons[i] |= reason & b.triggers & 0x10
				}
			}
			triggered = triggered || record.reasons[i] != 0
		}
		if !triggered {
			continue
		}
		// A second change of an already included member flushes its earlier
		// event before replacing the value, retaining both SOE transitions.
		if b.pending != nil {
			for i, r := range record.reasons {
				if r != 0 && b.pending.reasons[i] != 0 {
					b.flush()
					break
				}
			}
		}
		if b.pending == nil {
			b.pending = &record
			b.due = now.Add(b.bufferTime)
		} else {
			for i, r := range record.reasons {
				if r != 0 {
					b.pending.values[i] = record.values[i]
					b.pending.reasons[i] |= r
				}
			}
		}
		if b.bufferTime == 0 {
			b.flush()
		}
	}
}
func overlaps(a, b mms.ObjectName) bool {
	return a.Domain == b.Domain && (a.Item == b.Item || strings.HasPrefix(a.Item, b.Item+"$") || strings.HasPrefix(b.Item, a.Item+"$"))
}
func (b *reportBlock) flush() {
	if b.pending == nil {
		return
	}
	b.nextID++
	b.pending.id = b.nextID
	if len(b.queue) == b.capacity {
		if b.queue[0].id > b.cursor {
			b.overflow = true
			b.overflowEpoch++
		}
		b.queue = b.queue[1:]
	}
	b.queue = append(b.queue, *b.pending)
	b.pending = nil
}
func (d *IED) fullReportLocked(b *reportBlock, reason byte) {
	b.flush()
	set := d.sets[b.info.DataSet]
	r := reportRecord{timestamp: time.Now(), values: make([]mms.Value, len(set.Variables)), reasons: make([]byte, len(set.Variables))}
	for i, a := range set.Variables {
		r.values[i], _ = d.model.Read(context.Background(), a)
		r.reasons[i] = reason
	}
	b.pending = &r
	b.flush()
}

func (d *IED) runReports(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.mu.Lock()
			for _, b := range d.reports {
				b.releaseClosed()
				if b.pending != nil && !now.Before(b.due) {
					b.flush()
				}
				if b.triggers&0x08 != 0 && b.period > 0 && len(d.sets[b.info.DataSet].Variables) > 0 && (b.enabled || b.info.Buffered) {
					if b.integrity.IsZero() {
						b.integrity = now.Add(b.period)
					} else if !now.Before(b.integrity) {
						d.fullReportLocked(b, 0x08)
						b.integrity = now.Add(b.period)
					}
				}
			}
			d.mu.Unlock()
		}
	}
}
func (b *reportBlock) releaseClosed() {
	if b.owner != nil && b.owner.Context().Err() != nil {
		b.owner = nil
		b.enabled = false
		b.reserved = false
		if !b.info.Buffered {
			b.queue = nil
			b.pending = nil
		}
	}
}
func (d *IED) serveAssociation(a *mms.Association) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	defer func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, b := range d.reports {
			if b.owner == a {
				b.owner = nil
				b.enabled = false
				b.reserved = false
				if !b.info.Buffered {
					b.queue = nil
					b.pending = nil
				}
			}
		}
	}()
	for {
		select {
		case <-a.Context().Done():
			return
		case <-ticker.C:
			for _, info := range d.Reports() {
				d.mu.Lock()
				b := d.reports[info.Name]
				var record *reportRecord
				if b.owner == a && b.enabled {
					for i := range b.queue {
						if b.queue[i].id > b.cursor {
							copy := b.queue[i]
							record = &copy
							break
						}
					}
				}
				if record == nil {
					d.mu.Unlock()
					continue
				}
				state := *b
				sequence := b.sequence
				d.mu.Unlock()
				if err := d.sendReport(a, &state, *record); err != nil {
					if !errors.Is(err, mms.ErrClosed) {
						a.Close()
					}
					return
				}
				d.mu.Lock()
				if b.owner == a && b.enabled && b.sequence == sequence && b.generation == state.generation {
					b.cursor = record.id
					b.sequence++
					if !b.info.Buffered {
						b.sequence &= 255
					}
					// An acquisition can overflow while the socket is sending.
					// Retain that later loss for the next report.
					b.overflow = b.overflowEpoch != state.overflowEpoch
					if !b.info.Buffered {
						for len(b.queue) > 0 && b.queue[0].id <= b.cursor {
							b.queue[0] = reportRecord{}
							b.queue = b.queue[1:]
						}
					}
				}
				d.mu.Unlock()
			}
		}
	}
}
func (d *IED) encodeReport(b *reportBlock, r reportRecord, subSequence *uint16, more bool) mms.InformationReport {
	values := []mms.Value{mms.VisibleString(b.id), bitsValue(b.options, 10)}
	for _, bit := range []int{1, 2, 4, 6, 7, 8} {
		if b.options&(0x8000>>bit) == 0 {
			continue
		}
		switch bit {
		case 1:
			values = append(values, mms.Unsigned(uint64(b.sequence)))
		case 2:
			values = append(values, binaryTime(r.timestamp))
		case 4:
			values = append(values, mms.VisibleString(b.info.DataSet.String()))
		case 6:
			values = append(values, mms.Boolean(b.overflow))
		case 7:
			values = append(values, entryID(r.id))
		case 8:
			values = append(values, mms.Unsigned(uint64(b.info.ConfRev)))
		}
	}
	if subSequence != nil {
		values = append(values, mms.Unsigned(uint64(*subSequence)), mms.Boolean(more))
	}
	inclusion := make([]byte, (len(r.values)+7)/8)
	for i, reason := range r.reasons {
		if reason != 0 {
			inclusion[i/8] |= 0x80 >> uint(i%8)
		}
	}
	bitmap, _ := mms.BitString(inclusion, len(r.values))
	values = append(values, bitmap)
	if b.options&0x0400 != 0 {
		for i, reason := range r.reasons {
			if reason != 0 {
				values = append(values, mms.VisibleString(d.sets[b.info.DataSet].Variables[i].Name.String()))
			}
		}
	}
	for i, reason := range r.reasons {
		if reason != 0 {
			values = append(values, r.values[i])
		}
	}
	if b.options&0x1000 != 0 {
		for _, reason := range r.reasons {
			if reason != 0 {
				values = append(values, bitsValue(uint16(reason)<<8, 6))
			}
		}
	}
	name := mms.VMDName("RPT")
	out := mms.InformationReport{List: &name}
	for _, v := range values {
		out.Results = append(out.Results, mms.ReadResult{Value: v})
	}
	return out
}

func (d *IED) reportAccessLocked(ctx context.Context, n mms.ObjectName, value *mms.Value) (mms.Value, bool, error) {
	base, attr, ok := strings.Cut(n.Item, "$RP$")
	fc := "RP"
	if !ok {
		base, attr, ok = strings.Cut(n.Item, "$BR$")
		fc = "BR"
	}
	if !ok {
		return mms.Value{}, false, nil
	}
	instance, field, _ := strings.Cut(attr, "$")
	b := d.reports[mms.DomainName(n.Domain, base+"$"+fc+"$"+instance)]
	if b == nil {
		return mms.Value{}, false, nil
	}
	b.releaseClosed()
	if value == nil {
		if field == "" {
			spec := d.types[b.info.Name]
			var vs []mms.Value
			for _, c := range spec.Components {
				v, _, err := d.reportAccessLocked(ctx, mms.DomainName(n.Domain, n.Item+"$"+c.Name), nil)
				if err != nil {
					return mms.Value{}, true, err
				}
				vs = append(vs, v)
			}
			return mms.Structure(vs...), true, nil
		}
		switch field {
		case "RptEna":
			return mms.Boolean(b.enabled), true, nil
		case "Resv":
			return mms.Boolean(b.reserved), true, nil
		case "SqNum":
			return mms.Unsigned(uint64(b.sequence)), true, nil
		case "EntryID":
			id := b.cursor
			if !b.enabled && len(b.queue) > 0 {
				id = b.queue[len(b.queue)-1].id
			}
			return entryID(id), true, nil
		case "TimeOfEntry":
			if len(b.queue) > 0 {
				return binaryTime(b.queue[len(b.queue)-1].timestamp), true, nil
			}
		}
		v, err := d.model.Read(ctx, mms.Access(n))
		return v, true, err
	}
	a := mms.AssociationFromContext(ctx)
	if a == nil || b.owner != nil && b.owner != a {
		return mms.Value{}, true, mms.TemporarilyUnavailable
	}
	if err := mms.ValidateValue(d.types[n], *value); err != nil {
		return mms.Value{}, true, mms.TypeInconsistent
	}
	switch field {
	case "DatSet":
		if b.enabled || len(b.queue) > 0 || b.pending != nil {
			return mms.Value{}, true, mms.TemporarilyUnavailable
		}
		text, _ := value.StringValue()
		domain, item, ok := strings.Cut(text, "/")
		set := mms.DomainName(domain, item)
		if !ok || domain != n.Domain || len(d.sets[set].Variables) == 0 {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		if set == b.info.DataSet {
			break
		}
		if b.info.ConfRev == ^uint32(0) {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		revision := b.info.ConfRev + 1
		if err := d.model.SetValues(ctx, mms.Assignment{Name: n, Value: *value}, mms.Assignment{Name: mms.DomainName(n.Domain, base+"$"+fc+"$"+instance+"$ConfRev"), Value: mms.Unsigned(uint64(revision))}); err != nil {
			return mms.Value{}, true, err
		}
		b.info.DataSet = set
		b.info.ConfRev = revision
		b.watch = nil
		for _, member := range d.sets[set].Variables {
			var watch []mms.ObjectName
			for trigger := range d.triggers {
				if overlaps(accessReference(member), trigger) {
					watch = append(watch, trigger)
				}
			}
			b.watch = append(b.watch, watch)
		}
	case "RptID":
		if b.enabled {
			return mms.Value{}, true, mms.TemporarilyUnavailable
		}
		id, _ := value.StringValue()
		if id == "" {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		if err := d.model.SetValues(ctx, mms.Assignment{Name: n, Value: *value}); err != nil {
			return mms.Value{}, true, err
		}
		b.id = id
	case "Resv":
		on, _ := value.Bool()
		if b.enabled && !on {
			return mms.Value{}, true, mms.TemporarilyUnavailable
		}
		b.reserved = on
		if on {
			b.owner = a
		} else {
			b.owner = nil
		}
	case "RptEna":
		on, _ := value.Bool()
		if on && len(d.sets[b.info.DataSet].Variables) == 0 {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		b.generation++
		b.enabled = on
		b.sequence = 0
		b.owner = a
		if !on && !b.reserved {
			b.owner = nil
		}
		if !b.info.Buffered {
			b.queue = nil
			b.pending = nil
			b.cursor = 0
		}
	case "GI":
		on, _ := value.Bool()
		if !on {
			break
		}
		if !b.enabled || b.triggers&0x04 == 0 {
			return mms.Value{}, true, mms.ObjectAccessDenied
		}
		d.fullReportLocked(b, 0x04)
	case "PurgeBuf":
		if b.enabled {
			return mms.Value{}, true, mms.TemporarilyUnavailable
		}
		on, _ := value.Bool()
		if on {
			b.queue = nil
			b.pending = nil
			b.overflow = false
			b.cursor = b.nextID
		}
	case "EntryID":
		if b.enabled {
			return mms.Value{}, true, mms.TemporarilyUnavailable
		}
		raw, _ := value.Bytes()
		id := binary.BigEndian.Uint64(raw)
		if id == 0 {
			b.cursor = 0
			break
		}
		found := false
		for _, r := range b.queue {
			if r.id == id {
				found = true
				break
			}
		}
		if !found {
			return mms.Value{}, true, mms.ObjectValueInvalid
		}
		b.cursor = id
		b.overflow = false
	case "BufTm", "IntgPd", "OptFlds", "TrgOps":
		if b.enabled {
			return mms.Value{}, true, mms.TemporarilyUnavailable
		}
		if field == "BufTm" || field == "IntgPd" {
			v, _ := value.Uint()
			if field == "BufTm" {
				b.bufferTime = time.Duration(v) * time.Millisecond
			} else {
				b.period = time.Duration(v) * time.Millisecond
				b.integrity = time.Time{}
			}
		} else {
			raw, _ := value.Bytes()
			if field == "TrgOps" {
				if raw[0]&0x83 != 0 {
					return mms.Value{}, true, mms.ObjectValueInvalid
				}
				b.triggers = raw[0]
			} else {
				if raw[0]&0x80 != 0 || raw[1]&0x40 != 0 {
					return mms.Value{}, true, mms.ObjectValueInvalid
				}
				b.options = binary.BigEndian.Uint16(raw)
			}
		}
		if err := d.model.SetValues(ctx, mms.Assignment{Name: n, Value: *value}); err != nil {
			return mms.Value{}, true, err
		}
	default:
		return mms.Value{}, true, mms.ObjectAccessDenied
	}
	return mms.Value{}, true, nil
}
