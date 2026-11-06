// SPDX-License-Identifier: GPL-3.0-or-later
package goose_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/goose"
)

type finalFrameLink struct{ raw []byte }

func (l *finalFrameLink) ReadFrame(context.Context) ([]byte, error) {
	b := l.raw
	l.raw = nil
	return b, io.EOF
}
func (*finalFrameLink) WriteFrame(context.Context, []byte) error { return io.ErrClosedPipe }
func (*finalFrameLink) Close() error                             { return nil }
func TestReaderFinalFramePrecedesEOF(t *testing.T) {
	f := goose.Frame{
		Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 1}, Source: [6]byte{2, 0, 0, 0, 0, 1}, APPID: 0x1000,
		PDU: goose.PDU{GoCBRef: "test/LLN0$GO$events", DataSet: "test/LLN0$events", TimeAllowedToLive: 1000,
			StateNumber: 7, ConfRev: 1, Values: []data.Value{data.Boolean(true)}},
	}
	raw, err := goose.EncodeFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	s, err := goose.NewSubscriber(goose.SubscriptionConfig{Destination: f.Destination, APPID: f.APPID, GoCBRef: f.PDU.GoCBRef, DataSet: f.PDU.DataSet})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Run(context.Background(), &finalFrameLink{raw: raw}); !errors.Is(err, io.EOF) {
		t.Fatalf("stream end %v", err)
	}
	select {
	case event := <-s.Events():
		if event.Frame.PDU.StateNumber != 7 {
			t.Fatal("last frame lost")
		}
	default:
		t.Fatal("EOF overtook final frame")
	}
}
