// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

var ErrReportOverflow = errors.New("ied: report consumer buffer overflow")

// ErrSegmentPending means Decode has retained a validated incomplete report.
// Subscriptions consume this internally and only deliver complete reports.
var ErrSegmentPending = errors.New("ied: waiting for remaining report segments")

type ReportEntry struct {
	Variable mms.VariableAccess
	Value    data.Value
	Reason   data.Value    // invalid zero value when reason-for-inclusion is absent
	Type     data.TypeSpec // selected online declaration, when provided
}
type Report struct {
	ID, DataSet    string
	ConfRev        uint32
	Sequence       *uint8
	SequenceNumber *uint16 // URCB 8-bit or BRCB 16-bit wire sequence
	EntryID        []byte
	BufferOverflow bool
	Segments       int        // number of complete wire segments assembled into this report
	TimeOfEntry    data.Value // preserves the raw MMS BinaryTime representation
	ReceivedAt     time.Time  // local reception time, with monotonic age information
	Reset          bool       // sequence discontinuity: discard prior cached members first
	Entries        []ReportEntry
}
type ReportEvent struct {
	Report Report
	Err    error
}
type ReportConfig struct {
	ID, DataSet string
	ConfRev     uint32
	Members     []mms.VariableAccess
	Buffered    bool
	Types       []data.TypeSpec
}

// ReportDecoder validates the complete report before publishing any member.
// It reassembles URCB/BRCB segments before delivery. A changed declaration or
// invalid segment returns an error so the application can invalidate/rebind.
// A decoder belongs to one subscription; callers must serialize Decode.
type ReportDecoder struct {
	config      ReportConfig
	sequence    int
	segment     *Report
	segmentNext uint16
	segmentAt   time.Time
	segmentMask []bool
}

func NewReportDecoder(config ReportConfig) (*ReportDecoder, error) {
	if config.ID == "" || config.DataSet == "" || len(config.Members) == 0 || len(config.Members) > 4096 {
		return nil, fmt.Errorf("ied: invalid URCB report configuration")
	}
	config.Members = append([]mms.VariableAccess(nil), config.Members...)
	if len(config.Types) != 0 && len(config.Types) != len(config.Members) {
		return nil, fmt.Errorf("ied: report type/member count differs")
	}
	types := make([]data.TypeSpec, len(config.Types))
	for i, spec := range config.Types {
		if err := data.ValidateType(spec); err != nil {
			return nil, err
		}
		types[i] = data.CloneType(spec)
	}
	config.Types = types
	for i := range config.Members {
		config.Members[i].Selection = config.Members[i].Selection.Clone()
	}
	return &ReportDecoder{config: config, sequence: -1}, nil
}
func reportBit(raw []byte, index int) bool { return raw[index/8]&(0x80>>uint(index%8)) != 0 }
func (d *ReportDecoder) Decode(raw mms.InformationReport) (report Report, err error) {
	defer func() {
		if err != nil && !errors.Is(err, ErrSegmentPending) {
			d.sequence = -1
			d.segment = nil
			d.segmentMask = nil
			report = Report{}
		}
	}()
	if raw.List == nil || *raw.List != mms.VMDName("RPT") || len(raw.Results) < 2 {
		return report, fmt.Errorf("ied: expected MMS RPT list")
	}
	for _, result := range raw.Results {
		if result.Error != nil {
			return report, result.Error
		}
	}
	results := raw.Results
	id, e := results[0].Value.StringValue()
	if e != nil || id != d.config.ID {
		return report, fmt.Errorf("ied: report ID mismatch")
	}
	bits, e := results[1].Value.BitLength()
	if e != nil || bits != 10 {
		return report, fmt.Errorf("ied: invalid RPT OptFlds")
	}
	flags, _ := results[1].Value.Bytes()
	if reportBit(flags, 0) {
		return report, fmt.Errorf("ied: reserved RPT option")
	}
	report = Report{ID: id, DataSet: d.config.DataSet, ConfRev: d.config.ConfRev, ReceivedAt: time.Now(), Segments: 1}
	position := 2
	segmented := false
	var subSequence uint64
	var moreSegments bool
	take := func() (data.Value, error) {
		if position >= len(results) {
			return data.Value{}, fmt.Errorf("ied: truncated report")
		}
		v := results[position].Value
		position++
		return v, nil
	}
	// Wire field order differs from the OptFlds bit order.
	for _, bit := range []int{1, 2, 4, 6, 7, 8, 9} {
		if !reportBit(flags, bit) {
			continue
		}
		v, e := take()
		if e != nil {
			return report, e
		}
		switch bit {
		case 1:
			seq, e := v.Uint()
			maximum := uint64(255)
			if d.config.Buffered {
				maximum = 65535
			}
			if e != nil || seq > maximum {
				return report, fmt.Errorf("ied: invalid URCB sequence")
			}
			n := uint8(seq)
			if !d.config.Buffered {
				report.Sequence = &n
			}
			full := uint16(seq)
			report.SequenceNumber = &full
		case 2:
			if v.Kind() != data.KindBinaryTime {
				return report, fmt.Errorf("ied: invalid report time")
			}
			report.TimeOfEntry = v
		case 4:
			name, e := v.StringValue()
			if e != nil || name != d.config.DataSet {
				return report, fmt.Errorf("ied: report dataset changed")
			}
		case 6:
			overflow, e := v.Bool()
			if e != nil {
				return report, fmt.Errorf("ied: report buffer overflow")
			}
			report.BufferOverflow = overflow
		case 7:
			if v.Kind() != data.KindOctetString {
				return report, fmt.Errorf("ied: invalid RPT EntryID")
			}
			report.EntryID, _ = v.Bytes()
		case 8:
			revision, e := v.Uint()
			if e != nil || revision != uint64(d.config.ConfRev) {
				return report, fmt.Errorf("ied: report configuration revision changed")
			}
		case 9:
			segmented = true
			subSequence, e = v.Uint()
			if e != nil || subSequence > 65535 {
				return report, fmt.Errorf("ied: invalid report subsequence")
			}
			next, e := take()
			if e != nil {
				return report, e
			}
			moreSegments, e = next.Bool()
			if e != nil {
				return report, e
			}
		}
	}
	inclusion, e := take()
	if e != nil {
		return report, e
	}
	bits, e = inclusion.BitLength()
	if e != nil || bits != len(d.config.Members) {
		return report, fmt.Errorf("ied: invalid report inclusion bitmap")
	}
	included, _ := inclusion.Bytes()
	mask := make([]bool, len(d.config.Members))
	for i, member := range d.config.Members {
		if reportBit(included, i) {
			mask[i] = true
			member.Selection = member.Selection.Clone()
			entry := ReportEntry{Variable: member}
			if len(d.config.Types) != 0 {
				entry.Type = data.CloneType(d.config.Types[i])
			}
			report.Entries = append(report.Entries, entry)
		}
	}
	if reportBit(flags, 5) {
		for _, entry := range report.Entries {
			v, e := take()
			if e != nil {
				return report, e
			}
			name, e := v.StringValue()
			if e != nil || name != entry.Variable.Name.String() {
				return report, fmt.Errorf("ied: report data reference mismatch")
			}
		}
	}
	for i := range report.Entries {
		v, e := take()
		if e != nil {
			return report, e
		}
		report.Entries[i].Value = v
		if spec := report.Entries[i].Type; spec.Kind != 0 {
			if err := data.ValidateValue(spec, v); err != nil {
				return report, fmt.Errorf("ied: report member %s: %w", report.Entries[i].Variable.Name, err)
			}
		}
	}
	if reportBit(flags, 3) {
		for i := range report.Entries {
			v, e := take()
			if e != nil {
				return report, e
			}
			if _, e = v.BitLength(); e != nil {
				return report, fmt.Errorf("ied: invalid reason for inclusion")
			}
			report.Entries[i].Reason = v
		}
	}
	if position != len(results) {
		return report, fmt.Errorf("ied: trailing RPT fields")
	}
	if segmented {
		if report.SequenceNumber == nil {
			return report, fmt.Errorf("ied: segmented report requires sequence number")
		}
		if subSequence == 0 {
			if d.segment != nil {
				return report, fmt.Errorf("ied: interrupted segmented report")
			}
			// The named return value becomes zero when returning Pending. Keep
			// a separate snapshot so the next segment retains its metadata.
			first := report
			d.segment = &first
			d.segmentMask = mask
			d.segmentNext = 1
			d.segmentAt = time.Now()
		} else {
			if d.segment == nil || subSequence != uint64(d.segmentNext) || time.Since(d.segmentAt) > 5*time.Second || *d.segment.SequenceNumber != *report.SequenceNumber || string(d.segment.EntryID) != string(report.EntryID) || d.segment.BufferOverflow != report.BufferOverflow || !d.segment.TimeOfEntry.Equal(report.TimeOfEntry) {
				return report, fmt.Errorf("ied: inconsistent report segments")
			}
			for i, included := range mask {
				if included {
					if d.segmentMask[i] {
						return report, fmt.Errorf("ied: duplicate segmented member")
					}
					d.segmentMask[i] = true
				}
			}
			d.segment.Entries = append(d.segment.Entries, report.Entries...)
			d.segment.Segments++
			d.segmentNext++
		}
		if moreSegments {
			return Report{}, ErrSegmentPending
		}
		report = *d.segment
		d.segment = nil
	} else if d.segment != nil {
		return report, fmt.Errorf("ied: interrupted segmented report")
	}
	if report.SequenceNumber != nil {
		seq := int(*report.SequenceNumber)
		modulus := 256
		if d.config.Buffered {
			modulus = 65536
		}
		report.Reset = d.sequence >= 0 && seq != (d.sequence+1)%modulus
		d.sequence = seq
	}
	report.Reset = report.Reset || report.BufferOverflow
	return report, nil
}

type ReportOptions struct {
	Buffer        int // zero selects 64; maximum is 4096
	GI            bool
	ResumeEntryID []byte // BRCB only; nil uses the server's existing cursor
	PurgeBuffer   bool   // explicit destructive reset of that BRCB's retained history
}

// ReportSubscription owns a bounded event queue. A slow consumer is stopped
// with ErrReportOverflow, never silently given a partial report history. Stop
// releases this association's RCB; closing the Client releases all its RCBs.
type ReportSubscription struct {
	client   *Client
	block    mms.ObjectName
	decoder  *ReportDecoder
	mu       sync.Mutex
	events   chan ReportEvent
	closed   bool
	err      error
	reserved bool
}

func (s *ReportSubscription) Events() <-chan ReportEvent { return s.events }
func (s *ReportSubscription) Err() error                 { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *ReportSubscription) Block() mms.ObjectName      { return s.block }
func (s *ReportSubscription) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.err = err
		close(s.events)
	}
}
func (s *ReportSubscription) accept(raw mms.InformationReport) {
	if raw.List == nil || *raw.List != mms.VMDName("RPT") || len(raw.Results) < 2 {
		return
	}
	id, err := raw.Results[0].Value.StringValue()
	if err != nil || id != s.decoder.config.ID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	report, err := s.decoder.Decode(raw)
	if errors.Is(err, ErrSegmentPending) {
		return
	}
	select {
	case s.events <- ReportEvent{Report: report, Err: err}:
	default:
		s.err = ErrReportOverflow
		s.closed = true
		close(s.events)
	}
}

// SubscribeURCB uses an already engineered RP control block, e.g.
// ObjectName("LD", "RP", "LLN0.Events01"). ctx bounds setup only. The client
// does not rewrite the device's dataset, trigger options or OptFlds.
func (c *Client) SubscribeURCB(ctx context.Context, block mms.ObjectName, options ReportOptions) (*ReportSubscription, error) {
	if options.ResumeEntryID != nil || options.PurgeBuffer {
		return nil, fmt.Errorf("ied: buffered options require a BRCB")
	}
	if block.Scope != mms.DomainScope || !strings.Contains(block.Item, "$RP$") {
		return nil, fmt.Errorf("ied: expected domain-specific RP control block")
	}
	return c.subscribeReport(ctx, block, options, false)
}

// SubscribeBRCB resumes an engineered buffered control block. Save EntryID only
// after the application has durably handled a report, then pass it on reconnect.
// An unknown cursor is returned as an error and is never silently purged.
func (c *Client) SubscribeBRCB(ctx context.Context, block mms.ObjectName, options ReportOptions) (*ReportSubscription, error) {
	if block.Scope != mms.DomainScope || !strings.Contains(block.Item, "$BR$") {
		return nil, fmt.Errorf("ied: expected domain-specific BR control block")
	}
	if options.PurgeBuffer && options.ResumeEntryID != nil {
		return nil, fmt.Errorf("ied: choose resume or purge")
	}
	return c.subscribeReport(ctx, block, options, true)
}

func (c *Client) subscribeReport(ctx context.Context, block mms.ObjectName, options ReportOptions, buffered bool) (*ReportSubscription, error) {
	if options.Buffer == 0 {
		options.Buffer = 64
	}
	if options.Buffer < 1 || options.Buffer > 4096 {
		return nil, fmt.Errorf("ied: invalid report buffer size")
	}
	if err := c.acquire(ctx, c.reportToken); err != nil {
		return nil, err
	}
	defer func() { <-c.reportToken }()
	c.mu.Lock()
	existing := c.subscriptions[block]
	c.mu.Unlock()
	if existing != nil {
		return nil, fmt.Errorf("ied: control block already subscribed")
	}
	attr := func(name string) mms.ObjectName { return mms.DomainName(block.Domain, block.Item+"$"+name) }
	enabled, err := c.Read(ctx, attr("RptEna"))
	if err != nil {
		return nil, err
	}
	active, err := enabled.Bool()
	if err != nil || active {
		return nil, fmt.Errorf("ied: control block already enabled or invalid")
	}
	idValue, err := c.Read(ctx, attr("RptID"))
	if err != nil {
		return nil, err
	}
	id, err := idValue.StringValue()
	if err != nil || id == "" {
		return nil, fmt.Errorf("ied: invalid report ID")
	}
	dataset, err := c.Read(ctx, attr("DatSet"))
	if err != nil {
		return nil, err
	}
	set, err := dataset.StringValue()
	parts := strings.Split(set, "/")
	if err != nil || len(parts) != 2 {
		return nil, fmt.Errorf("ied: invalid report dataset")
	}
	revision, err := c.Read(ctx, attr("ConfRev"))
	if err != nil {
		return nil, err
	}
	rev, err := revision.Uint()
	if err != nil || rev > 1<<32-1 {
		return nil, fmt.Errorf("ied: invalid report ConfRev")
	}
	list, err := c.GetNamedListAttributes(ctx, mms.DomainName(parts[0], parts[1]))
	if err != nil {
		return nil, err
	}
	var types []data.TypeSpec
	for _, member := range list.Variables {
		spec, err := c.GetVariableAccessAttributes(ctx, member.Name)
		if err != nil {
			return nil, err
		}
		spec, err = mms.SelectedType(spec, member.Selection)
		if err != nil {
			return nil, err
		}
		types = append(types, spec)
	}
	decoder, err := NewReportDecoder(ReportConfig{ID: id, DataSet: set, ConfRev: uint32(rev), Members: list.Variables, Buffered: buffered, Types: types})
	if err != nil {
		return nil, err
	}
	s := &ReportSubscription{client: c, block: block, decoder: decoder, events: make(chan ReportEvent, options.Buffer)}
	c.mu.Lock()
	for _, other := range c.subscriptions {
		if other.decoder.config.ID == id {
			c.mu.Unlock()
			return nil, fmt.Errorf("ied: duplicate report ID on association")
		}
	}
	// Register before enabling: an early valid report must not be lost.
	c.subscriptions[block] = s
	c.mu.Unlock()
	if buffered {
		if options.PurgeBuffer {
			err = c.Write(ctx, attr("PurgeBuf"), data.Boolean(true))
		}
		if err == nil && options.ResumeEntryID != nil {
			err = c.Write(ctx, attr("EntryID"), data.OctetString(options.ResumeEntryID))
		}
	} else {
		err = c.Write(ctx, attr("Resv"), data.Boolean(true))
		if errors.Is(err, mms.ObjectNonExistent) || errors.Is(err, mms.ObjectUndefined) {
			err = nil
		} else if err == nil {
			s.reserved = true
		}
	}
	if err == nil {
		err = c.Write(ctx, attr("RptEna"), data.Boolean(true))
		if err != nil {
			c.Close()
		} // association release resolves an uncertain enable
	}
	if err == nil && options.GI {
		err = c.Write(ctx, attr("GI"), data.Boolean(true))
		if err != nil {
			c.Close()
		}
	}
	if err != nil {
		c.mu.Lock()
		delete(c.subscriptions, block)
		c.mu.Unlock()
		s.finish(err)
		return nil, err
	}
	return s, nil
}

// Interrogate requests a fresh complete dataset using the engineered GI trigger.
func (s *ReportSubscription) Interrogate(ctx context.Context) error {
	return s.client.Write(ctx, mms.DomainName(s.block.Domain, s.block.Item+"$GI"), data.Boolean(true))
}

func (s *ReportSubscription) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.Stop(ctx)
}
func (s *ReportSubscription) Stop(ctx context.Context) error {
	c := s.client
	if c.Err() != nil {
		s.finish(c.Err())
		return nil
	}
	if err := c.acquire(ctx, c.reportToken); err != nil {
		return err
	}
	defer func() { <-c.reportToken }()
	c.mu.Lock()
	owned := c.subscriptions[s.block] == s
	if owned {
		delete(c.subscriptions, s.block)
	}
	c.mu.Unlock()
	if !owned {
		return nil
	}
	s.finish(nil)
	attr := func(name string) mms.ObjectName { return mms.DomainName(s.block.Domain, s.block.Item+"$"+name) }
	err := c.Write(ctx, attr("RptEna"), data.Boolean(false))
	if err == nil && s.reserved {
		err = c.Write(ctx, attr("Resv"), data.Boolean(false))
	}
	if err != nil {
		c.Close()
	}
	return err
}
