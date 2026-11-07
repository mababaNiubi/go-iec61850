// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReceiverRoutesAndReleasesSubscribers(t *testing.T) {
	clock := newRuntimeClock()
	first := runtimeSubscriber(t, runtimeConfig(clock))
	config := runtimeConfig(clock)
	config.GoCBRef = "OTHER/LLN0$GO$cb"
	second := runtimeSubscriber(t, config)
	counts := [2]int{}
	receiver := &Receiver{}
	remove1, err := receiver.Add(first, func(_ Event, err error) {
		if err != nil {
			t.Error(err)
		}
		counts[0]++
	})
	if err != nil {
		t.Fatal(err)
	}
	remove2, err := receiver.Add(second, func(_ Event, err error) {
		if err != nil {
			t.Error(err)
		}
		counts[1]++
	})
	if err != nil {
		t.Fatal(err)
	}
	defer remove2()
	frame := runtimeFrame()
	receiver.Process(frame)
	if counts != [2]int{1, 0} {
		t.Fatal(counts)
	}
	frame.PDU.GoCBRef = config.GoCBRef
	receiver.Process(frame)
	if counts != [2]int{1, 1} {
		t.Fatal(counts)
	}
	if _, err := receiver.Add(first, nil); err == nil {
		t.Fatal("subscriber acquired by two readers")
	}
	remove1()
	frame.PDU.GoCBRef = runtimeFrame().PDU.GoCBRef
	frame.PDU.SequenceNumber++
	receiver.Process(frame)
	if counts[0] != 1 {
		t.Fatal("removed stream still dispatched")
	}
	// Removing waits for dispatch before releasing Subscriber.Run ownership.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	link := newRuntimeLink()
	if err := first.Run(ctx, link); errors.Is(err, ErrRunning) {
		t.Fatal(err)
	}
	// Snapshot still enforces TAL even with no further network packets.
	clock.Advance(time.Second)
	if _, valid := second.Snapshot(); valid {
		t.Fatal("expired stream remained current")
	}
}
