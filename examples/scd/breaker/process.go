// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

// Replace these two methods with your meter/PLC driver. Read returns actual
// feedback and its acquisition times. Operate returns the actuator's outcome.
type processIO interface {
	Read(context.Context) (sample, error)
	Operate(context.Context, scl.Command) error
}
type sample struct {
	watts                                  float32
	position                               scl.Position
	operations                             int32
	blocked                                bool
	statusTime, blockTime, measurementTime time.Time
	// Thirteen ASN.1 bits, most significant bit first. Zero is good quality.
	powerQuality, statusQuality [2]byte
}

type simulator struct {
	mu                    sync.Mutex
	position              scl.Position
	operations            int32
	blocked               bool
	statusTime, blockTime time.Time
}

func newSimulator() *simulator {
	now := time.Now()
	return &simulator{position: scl.Open, statusTime: now, blockTime: now}
}
func (p *simulator) Read(ctx context.Context) (sample, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return sample{}, err
	}
	s := sample{position: p.position, operations: p.operations, blocked: p.blocked, statusTime: p.statusTime, blockTime: p.blockTime, measurementTime: time.Now()}
	if p.position == scl.Closed {
		s.watts = 1250
	}
	return s, nil
}
func (p *simulator) Operate(ctx context.Context, c scl.Command) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// This simulated actuator cannot perform a synchrocheck or test operation.
	if c.Test || c.SynchroCheck {
		return mms.ObjectAccessUnsupported
	}
	if c.Value && p.blocked {
		return mms.ObjectAccessDenied
	}
	target := scl.Open
	if c.Value {
		target = scl.Closed
	}
	if target != p.position {
		p.position = target
		p.operations++
		p.statusTime = time.Now()
	}
	return nil
}
func (p *simulator) blockClose(blocked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked != blocked {
		p.blocked = blocked
		p.blockTime = time.Now()
	}
}

// These are application signal names. All addresses, member ordering, wire
// types, dataset references and Ethernet settings are resolved from the SCD.
type bayPoints struct {
	power, position, operations, blocked scl.Signal
	operate                              mms.ObjectName
}

func bindPoints(d *scl.IED, ld string) (bayPoints, error) {
	var p bayPoints
	for _, r := range []struct {
		dest                   *scl.Signal
		ln, fc, object, member string
	}{
		{&p.power, "MMXU1", "MX", "TotW", "mag.f"},
		{&p.position, "XCBR1", "ST", "Pos", "stVal"},
		{&p.operations, "XCBR1", "ST", "OpCnt", "stVal"},
		{&p.blocked, "XCBR1", "ST", "BlkCls", "stVal"},
	} {
		n, err := d.Signal(ld, r.ln, r.fc, r.object, r.member)
		if err != nil {
			return bayPoints{}, err
		}
		*r.dest = n
	}
	var err error
	p.operate, err = d.Lookup(ld, "XCBR1", "CO", "Pos.Oper")
	return p, err
}

type bay struct {
	device *scl.IED
	points bayPoints
	io     processIO
	mu     sync.Mutex // serialize polling and command feedback in acquisition order
}

func (b *bay) acquire(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readFeedback(ctx)
}
func (b *bay) perform(ctx context.Context, c scl.Command) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.io.Operate(ctx, c); err != nil {
		return err
	}
	return b.readFeedback(ctx)
}
func (b *bay) readFeedback(ctx context.Context) error {
	s, err := b.io.Read(ctx)
	if err != nil {
		return err
	}
	pos, err := s.position.MMSValue()
	if err != nil {
		return err
	}
	powerQ, err := mms.BitString(s.powerQuality[:], 13)
	if err != nil {
		return err
	}
	statusQ, err := mms.BitString(s.statusQuality[:], 13)
	if err != nil {
		return err
	}
	p := b.points
	batch := p.power.Sample(mms.Float32(s.watts), powerQ, mms.UTC(s.measurementTime))
	batch = append(batch, p.position.Sample(pos, statusQ, mms.UTC(s.statusTime))...)
	batch = append(batch, p.operations.Sample(mms.Integer(int64(s.operations)), statusQ, mms.UTC(s.statusTime))...)
	batch = append(batch, p.blocked.Sample(mms.Boolean(s.blocked), statusQ, mms.UTC(s.blockTime))...)
	return b.device.Update(ctx, batch...)
}
