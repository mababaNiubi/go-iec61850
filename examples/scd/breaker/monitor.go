// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

// monitor polls MMS and consumes the events produced by the configured subscriber.
func monitor(ctx context.Context, c *mms.Client, p bayPoints, sub *goose.Subscriber, input scl.Input, command string) error {
	if _, err := readProcess(ctx, c, p); err != nil {
		return err
	}
	if command != "" {
		if err := operate(ctx, c, p.operate, command == "close", 1); err != nil {
			return err
		}
	}
	var events <-chan goose.Event
	if sub != nil {
		events = sub.Events()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := readProcess(ctx, c, p); err != nil {
				return err
			}
		case e := <-events:
			if e.Kind == goose.Expired {
				fmt.Println("GOOSE expired: position is stale")
				continue
			}
			if e.Kind == goose.Retransmission {
				continue
			}
			pos, err := eventPosition(input, e)
			if err != nil {
				return err
			}
			fmt.Printf("GOOSE %s stNum=%d position=%s\n", e.Kind, e.Frame.PDU.StateNumber, pos)
		}
	}
}

// A station uses the same engineered point definitions as the source IED.
func readProcess(ctx context.Context, c *mms.Client, p bayPoints) (scl.Position, error) {
	results, err := c.ReadMultiple(ctx, mms.Access(p.power.Value), mms.Access(p.position.Value))
	if err != nil {
		return 0, err
	}
	for _, r := range results {
		if r.Error != nil {
			return 0, r.Error
		}
	}
	watts, err := results[0].Value.Float()
	if err != nil {
		return 0, err
	}
	pos, err := scl.ReadPosition(results[1].Value)
	if err != nil {
		return 0, err
	}
	fmt.Printf("MMS active power=%.0f W position=%s\n", watts, pos)
	return pos, nil
}

// Issue an immediate, direct-with-normal-security control using a whole Oper.
func operate(ctx context.Context, c *mms.Client, name mms.ObjectName, closeBreaker bool, number uint8) error {
	cmd := scl.Command{Value: closeBreaker, OriginCategory: 2, OriginID: []byte("StationHMI"), Number: number, Time: time.Now(), InterlockCheck: true}
	v, err := cmd.MMSValue()
	if err != nil {
		return err
	}
	return c.Write(ctx, name, v)
}
func eventPosition(input scl.Input, e goose.Event) (scl.Position, error) {
	v, err := input.Value(e)
	if err != nil {
		return 0, err
	}
	return scl.ReadPosition(v)
}
func waitPosition(ctx context.Context, sub *goose.Subscriber, input scl.Input, want scl.Position) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-sub.Events():
			if e.Kind == goose.Expired {
				fmt.Println("GOOSE expired; waiting for valid feedback")
				continue
			}
			pos, err := eventPosition(input, e)
			if err != nil {
				return err
			}
			if pos == want {
				fmt.Printf("GOOSE %s stNum=%d position=%s\n", input.Name, e.Frame.PDU.StateNumber, pos)
				return nil
			}
		}
	}
}
