// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// memoryProvider is the virtual file store used by the tests. It paginates
// deliberately so continuation handling is exercised.
type memoryProvider struct {
	files    map[string][]byte
	pageSize int
}

type memoryFile struct {
	*bytes.Reader
}

func (memoryFile) Close() error { return nil }

func (p *memoryProvider) List(ctx context.Context, spec, continueAfter string) ([]FileEntry, bool, error) {
	names := make([]string, 0, len(p.files))
	for name := range p.files {
		if strings.HasPrefix(name, spec) && name > continueAfter {
			names = append(names, name)
		}
	}
	sortStrings(names)
	limit := p.pageSize
	if limit <= 0 || limit > len(names) {
		limit = len(names)
	}
	entries := make([]FileEntry, 0, limit)
	mod := time.Unix(1700000000, 0).UTC()
	for _, name := range names[:limit] {
		entries = append(entries, FileEntry{Name: name, Size: uint32(len(p.files[name])), LastModified: mod})
	}
	return entries, len(names) > limit, nil
}

func (p *memoryProvider) Open(ctx context.Context, name string) (FileEntry, io.ReadSeekCloser, error) {
	data, ok := p.files[name]
	if !ok {
		return FileEntry{}, nil, ErrFileNotFound
	}
	return FileEntry{Name: name, Size: uint32(len(data)), LastModified: time.Unix(1700000000, 0).UTC()},
		memoryFile{bytes.NewReader(data)}, nil
}

func sortStrings(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

func startFileServer(t *testing.T, provider FileProvider) string {
	t.Helper()
	_, address := startServer(t, tlsTestStore(t), Config{}, func(s *Server) { s.SetFileProvider(provider) })
	return address
}

func dialFile(t *testing.T, address string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := Dial(ctx, address, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func testFiles() *memoryProvider {
	return &memoryProvider{
		pageSize: 2,
		files: map[string][]byte{
			"fault-001.cfg": []byte("station,dev,2013\r\n"),
			"fault-001.dat": []byte("1,0,0\r\n2,1000,1\r\n"),
			"fault-002.cfg": []byte("station,dev,2013\r\n"),
			"fault-002.dat": []byte("1,0,1\r\n"),
		},
	}
}

// T-FILE-01: directory pagination follows the continuation cursor.
func TestFileDirectoryPaginates(t *testing.T) {
	address := startFileServer(t, testFiles())
	client := dialFile(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entries, err := client.FileDirectory(ctx, "fault-", FileLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries across pages, got %d: %+v", len(entries), entries)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Name >= entries[i].Name {
			t.Fatalf("entries are not ordered: %+v", entries)
		}
	}
	if entries[0].Size == 0 || entries[0].LastModified.IsZero() {
		t.Fatalf("attributes missing: %+v", entries[0])
	}
	// An entry limit stops deterministically with the completed prefix.
	limited, err := client.FileDirectory(ctx, "fault-", FileLimits{MaxEntries: 3})
	if !errors.Is(err, ErrFileLimit) || len(limited) != 3 {
		t.Fatalf("expected a bounded prefix, got %d entries err=%v", len(limited), err)
	}
}

// A whole file crosses open/read/close intact, with the last chunk flagged.
func TestFetchFileRoundTrip(t *testing.T) {
	provider := testFiles()
	address := startFileServer(t, provider)
	client := dialFile(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	entry, err := client.FetchFile(ctx, "fault-001.dat", &out, FileLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), provider.files["fault-001.dat"]) {
		t.Fatalf("content mismatch: %q", out.Bytes())
	}
	if entry.Size != uint32(len(provider.files["fault-001.dat"])) || entry.Name != "fault-001.dat" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

// The byte budget is enforced while reading, not only from the declared size.
func TestFetchFileEnforcesLimit(t *testing.T) {
	address := startFileServer(t, testFiles())
	client := dialFile(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	if _, err := client.FetchFile(ctx, "fault-001.dat", &out, FileLimits{MaxBytes: 4}); !errors.Is(err, ErrFileLimit) {
		t.Fatalf("expected the limit to stop retrieval, got %v", err)
	}
	if out.Len() > 4 {
		t.Fatalf("wrote %d bytes beyond the budget", out.Len())
	}
}

// Reader state: position, incremental reads and close semantics.
func TestFileOpenReadClose(t *testing.T) {
	provider := testFiles()
	address := startFileServer(t, provider)
	client := dialFile(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handle, err := client.OpenFile(ctx, "fault-002.dat", 0)
	if err != nil {
		t.Fatal(err)
	}
	var collected []byte
	for {
		chunk, more, err := handle.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		collected = append(collected, chunk...)
		if !more {
			break
		}
	}
	if !bytes.Equal(collected, provider.files["fault-002.dat"]) {
		t.Fatalf("content mismatch: %q", collected)
	}
	if err = handle.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = handle.Read(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("reading a closed handle must fail, got %v", err)
	}
	// Opening in the middle returns the remaining bytes only.
	mid, err := client.OpenFile(ctx, "fault-002.dat", 3)
	if err != nil {
		t.Fatal(err)
	}
	chunk, more, err := mid.Read(ctx)
	if err != nil || more || !bytes.Equal(chunk, provider.files["fault-002.dat"][3:]) {
		t.Fatalf("mid-file read: %q more=%t err=%v", chunk, more, err)
	}
	_ = mid.Close(ctx)
}

func TestFileServiceRejectsUnknownAndUnsafeNames(t *testing.T) {
	address := startFileServer(t, testFiles())
	client := dialFile(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.OpenFile(ctx, "missing.dat", 0); err == nil {
		t.Fatal("a missing file must be refused")
	} else {
		var se *ServiceError
		if !errors.As(err, &se) || se.Class != fileServiceClass || se.Code != fileErrMissing {
			t.Fatalf("expected the file non-existent code, got %v", err)
		}
	}
	for _, name := range []string{"../etc/passwd", "/abs/path", `windows\path`, "a..b"} {
		if _, err := client.OpenFile(ctx, name, 0); err == nil {
			t.Fatalf("unsafe name %q must be rejected before the wire", name)
		}
	}
	// A position beyond the file end is refused.
	if _, err := client.OpenFile(ctx, "fault-002.dat", 9999); err == nil {
		t.Fatal("an out-of-range position must be refused")
	}
}

func TestFileServiceBoundsOpenHandles(t *testing.T) {
	address := startFileServer(t, testFiles())
	client := dialFile(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var opened []*FileHandle
	for i := 0; i < fileOpenLimit; i++ {
		handle, err := client.OpenFile(ctx, "fault-001.cfg", 0)
		if err != nil {
			t.Fatalf("handle %d: %v", i, err)
		}
		opened = append(opened, handle)
	}
	if _, err := client.OpenFile(ctx, "fault-001.cfg", 0); err == nil {
		t.Fatal("the open-handle limit must be enforced")
	}
	for _, handle := range opened {
		if err := handle.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Capacity is released again.
	again, err := client.OpenFile(ctx, "fault-001.cfg", 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close(ctx)
}

// The advertisement gains the read-only file bits only when a provider is
// bound, and the default profile stays byte-identical.
func TestFileServiceAdvertisement(t *testing.T) {
	defaultAddress := startServer2(t, nil)
	defaultClient := dialFile(t, defaultAddress)
	if bits := defaultClient.Parameters().Services[9]; bits&(0x80|0x40|0x20|0x04) != 0 {
		t.Fatalf("file services advertised without a provider: %#x", bits)
	}
	address := startFileServer(t, testFiles())
	client := dialFile(t, address)
	if bits := client.Parameters().Services[9]; bits&(0x80|0x40|0x20|0x04) != 0xE4|0x01 {
		if bits&(0x80|0x40|0x20|0x04) != (0x80 | 0x40 | 0x20 | 0x04) {
			t.Fatalf("file service bits missing: %#x", bits)
		}
	}
	// Rename and delete are not advertised: the scope is read-only.
	if bits := client.Parameters().Services[9]; bits&(0x10|0x08) != 0 {
		t.Fatalf("write-side file services must stay off: %#x", bits)
	}
	// A server without a provider refuses the service.
	if _, err := defaultClient.FileDirectory(context.Background(), "fault-", FileLimits{}); err == nil {
		t.Fatal("file services must fail without a provider")
	}
}

func startServer2(t *testing.T, provider FileProvider) string {
	t.Helper()
	_, address := startServer(t, tlsTestStore(t), Config{}, func(s *Server) {
		if provider != nil {
			s.SetFileProvider(provider)
		}
	})
	return address
}
