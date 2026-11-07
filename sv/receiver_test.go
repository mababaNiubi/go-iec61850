// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"fmt"
	"testing"
)

func TestReceiverValidatesBeforeAtomicCommit(t *testing.T) {
	subscriber := runtimeSubscriber(t, runtimeConfig(newRuntimeClock()))
	receiver := &Receiver{}
	rejected := 0
	remove, err := receiver.Add(subscriber, func(f Frame) error {
		if len(f.PDU.ASDUs) != 2 {
			return fmt.Errorf("wrong engineered count")
		}
		return nil
	}, func(events []Event, err error) {
		if err != nil {
			rejected++
		} else if len(events) != 2 {
			t.Error(events)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	frame := runtimeFrame()
	receiver.Process(frame)
	if _, valid := subscriber.Snapshot(); valid || rejected != 1 {
		t.Fatal(valid, rejected)
	}
	second := frame.PDU.ASDUs[0].Clone()
	second.SampleCount++
	frame.PDU.ASDUs = append(frame.PDU.ASDUs, second)
	receiver.Process(frame)
	current, valid := subscriber.Snapshot()
	if !valid || current.SampleCount != second.SampleCount {
		t.Fatal(current, valid)
	}
	// A subsequent packet rejected by the profile never refreshes the state.
	frame.PDU.ASDUs = frame.PDU.ASDUs[:1]
	frame.PDU.ASDUs[0].SampleCount += 2
	receiver.Process(frame)
	current, valid = subscriber.Snapshot()
	if !valid || current.SampleCount != second.SampleCount || rejected != 2 {
		t.Fatal(current, valid, rejected)
	}
}
