// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

// comtradeFiles serves the ASCII fixture pair over MMS file services.
type comtradeFiles struct{ files map[string][]byte }

type bytesFile struct{ *bytes.Reader }

func (bytesFile) Close() error { return nil }

func (p comtradeFiles) List(ctx context.Context, spec, continueAfter string) ([]mms.FileEntry, bool, error) {
	out := []mms.FileEntry{}
	for name, data := range p.files {
		if len(spec) > 0 && name < spec {
			continue
		}
		if continueAfter != "" && name <= continueAfter {
			continue
		}
		out = append(out, mms.FileEntry{Name: name, Size: uint32(len(data)), LastModified: time.Unix(1700000000, 0).UTC()})
	}
	return out, false, nil
}

func (p comtradeFiles) Open(ctx context.Context, name string) (mms.FileEntry, io.ReadSeekCloser, error) {
	data, ok := p.files[name]
	if !ok {
		return mms.FileEntry{}, nil, mms.ErrFileNotFound
	}
	return mms.FileEntry{Name: name, Size: uint32(len(data)), LastModified: time.Unix(1700000000, 0).UTC()},
		bytesFile{bytes.NewReader(data)}, nil
}

// T-FILE-01 (workbench half): retrieve a disturbance-record pair from a peer
// and hand it to the existing COMTRADE playback path.
func TestFetchRemoteRecording(t *testing.T) {
	m, id := testDevice(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	model, err := m.get(id)
	if err != nil {
		t.Fatal(err)
	}
	files := comtradeFiles{files: map[string][]byte{
		"fault.cfg": []byte(asciiCFG),
		"fault.dat": []byte(asciiDAT),
	}}
	runtime, err := simulator.Start(ctx, model.ied, simulator.Options{
		Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEOff, Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	conn, err := m.Connect(ctx, runtime.Address(), "")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := m.RemoteFiles(ctx, conn.ID, "fault", 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected the cfg/dat pair, got %+v", entries)
	}
	rec, err := m.FetchRemoteRecording(ctx, conn.ID, "fault.cfg", "fault.dat")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Samples != 5 || len(rec.Channels) != 3 || rec.Format != "ASCII" {
		t.Fatalf("unexpected recording: %+v", rec)
	}
	if list := m.Recordings(); len(list) != 1 {
		t.Fatalf("the fetched capture must enter the recordings store, got %d", len(list))
	}
	// A missing file surfaces the peer's error instead of a partial import.
	if _, err = m.FetchRemoteRecording(ctx, conn.ID, "ghost.cfg", "ghost.dat"); err == nil {
		t.Fatal("a missing remote file must fail")
	}
	if err = m.Disconnect(conn.ID); err != nil {
		t.Fatal(err)
	}
}
