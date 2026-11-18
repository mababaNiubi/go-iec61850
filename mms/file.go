// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

// Read-only MMS file retrieval (directory/open/read/close). Wire tags follow
// libiec61850 v1.6.2.1 (NOTICE): request FileOpen [72], FileRead [73],
// FileClose [74], FileDirectory [77]; responses FileOpen [72], FileRead [9],
// FileClose [10], FileDirectory [77]. Delete, rename and obtainFile are NOT
// implemented: read-only retrieval is the reviewed scope.
const (
	fileServiceClass = 11 // MMS service-error class "file"
	fileErrOther     = 0
	fileErrBusy      = 2
	fileErrPosition  = 5
	fileErrDenied    = 6
	fileErrMissing   = 7

	fileOpenLimit   = 16  // concurrent open files per association
	maxFileNameSize = 256 // remote file name bound
	defaultFileMax  = 64 << 20
	fileChunkMax    = 16 << 10
)

// FileEntry describes one file in the virtual store.
type FileEntry struct {
	Name         string
	Size         uint32
	LastModified time.Time
}

// FileProvider is the server-side virtual file store. Names are logical MMS
// file names, never host paths: a provider decides where (or whether) real
// data lives. List returns entries ordered by name, starting after
// continueAfter, and reports whether more entries follow.
type FileProvider interface {
	List(ctx context.Context, spec, continueAfter string) ([]FileEntry, bool, error)
	// Open returns the entry plus a reader positioned by the server.
	Open(ctx context.Context, name string) (FileEntry, io.ReadSeekCloser, error)
}

// File provider errors map onto the MMS file service-error codes.
var (
	ErrFileNotFound     = errors.New("mms: file not found")
	ErrFileAccessDenied = errors.New("mms: file access denied")
	ErrFileBusy         = errors.New("mms: file busy")
)

func fileServiceError(err error) *ServiceError {
	switch {
	case errors.Is(err, ErrFileNotFound):
		return &ServiceError{fileServiceClass, fileErrMissing}
	case errors.Is(err, ErrFileAccessDenied):
		return &ServiceError{fileServiceClass, fileErrDenied}
	case errors.Is(err, ErrFileBusy):
		return &ServiceError{fileServiceClass, fileErrBusy}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return &ServiceError{fileServiceClass, fileErrBusy}
	default:
		return &ServiceError{fileServiceClass, fileErrOther}
	}
}

// validRemoteFileName rejects absolute paths, traversal and host-path syntax
// before a provider ever sees the name.
func validRemoteFileName(name string) bool {
	if name == "" || len(name) > maxFileNameSize {
		return false
	}
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) ||
		strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	return true
}

func encodeInt32(tag byte, v int32) []byte {
	return ber.Encode(tag, int32Bytes(v))
}

func int32Bytes(v int32) []byte {
	b := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	i := 0
	// Trim redundant leading octets but keep the sign.
	for i < 3 && ((b[i] == 0 && b[i+1]&0x80 == 0) || (b[i] == 0xff && b[i+1]&0x80 != 0)) {
		i++
	}
	return b[i:]
}

func decodeInt32(raw []byte) (int32, error) {
	if len(raw) == 0 || len(raw) > 4 {
		return 0, ErrProtocol
	}
	var v int32
	if raw[0]&0x80 != 0 {
		v = -1
	}
	for _, c := range raw {
		v = v<<8 | int32(c)
	}
	return v, nil
}

func generalizedTime(t time.Time) string {
	return t.UTC().Format("20060102150405.000Z")
}

func parseGeneralizedTime(text string) time.Time {
	text = strings.TrimSuffix(strings.TrimSpace(text), "Z")
	layouts := []string{"20060102150405.000", "20060102150405", "20060102150405.0"}
	for _, layout := range layouts {
		if v, err := time.Parse(layout, text); err == nil {
			return v.UTC()
		}
	}
	return time.Time{}
}

func fileAttributes(entry FileEntry) []byte {
	return ber.Encode(0xa1, ber.Uint(0x80, uint64(entry.Size)), ber.Encode(0x81, []byte(generalizedTime(entry.LastModified))))
}

func fileSpecification(tag byte, name string) []byte {
	return ber.Encode(tag, ber.Encode(0x19, []byte(name)))
}

// fileHandle is one server-side open file for one association.
type fileHandle struct {
	entry FileEntry
	file  io.ReadSeekCloser
	pos   uint32
}

// SetFileProvider binds the virtual file store and enables the read-only file
// services in the negotiated advertisement. Configure it before Serve.
func (s *Server) SetFileProvider(provider FileProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = provider
}

// localParameters advertises file services only when a provider is bound, so
// the wire profile always matches the implementation.
func (s *Server) localParameters() Parameters {
	p := localParameters(s.cfg)
	s.mu.Lock()
	enabled := s.files != nil
	s.mu.Unlock()
	if enabled {
		// Byte 9: fileOpen 0x80, fileRead 0x40, fileClose 0x20,
		// fileDirectory 0x04. Values per the pinned reference constants.
		p.Services[9] |= 0x80 | 0x40 | 0x20 | 0x04
	}
	return p
}

// --- server handlers -------------------------------------------------------

func (a *Association) fileOpen(id uint32, e ber.Element) []byte {
	a.server.mu.Lock()
	provider := a.server.files
	a.server.mu.Unlock()
	if provider == nil {
		return a.failure(id, &ServiceError{fileServiceClass, fileErrOther})
	}
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil || len(fields) != 2 {
		return a.failure(id, ErrProtocol)
	}
	if fields[0].Tag != 0xa0 || fields[1].Tag != 0x81 {
		return a.failure(id, ErrProtocol)
	}
	name, err := fileNameFromSpec(fields[0])
	if err != nil {
		return a.failure(id, err)
	}
	position, err := berDecodeUint(fields[1].Bytes)
	if err != nil {
		return a.failure(id, &ServiceError{fileServiceClass, fileErrPosition})
	}
	a.fileMu.Lock()
	full := len(a.files) >= fileOpenLimit
	a.fileMu.Unlock()
	if full {
		return a.failure(id, &ServiceError{fileServiceClass, fileErrBusy})
	}
	entry, reader, err := provider.Open(context.Background(), name)
	if err != nil {
		return a.failure(id, fileServiceError(err))
	}
	if position > uint64(entry.Size) || (position == uint64(entry.Size) && entry.Size != 0) {
		_ = reader.Close()
		return a.failure(id, &ServiceError{fileServiceClass, fileErrPosition})
	}
	if position != 0 {
		if _, err = reader.Seek(int64(position), io.SeekStart); err != nil {
			_ = reader.Close()
			return a.failure(id, &ServiceError{fileServiceClass, fileErrPosition})
		}
	}
	a.fileMu.Lock()
	a.nextFrsm++
	if a.nextFrsm <= 0 {
		a.nextFrsm = 1
	}
	handle := a.nextFrsm
	if a.files == nil {
		a.files = make(map[int32]*fileHandle)
	}
	a.files[handle] = &fileHandle{entry: entry, file: reader, pos: uint32(position)}
	a.fileMu.Unlock()
	return a.response(id, ber.EncodeHigh(0xbf, 0x48, encodeInt32(0x80, handle), fileAttributes(entry)))
}

func (a *Association) fileRead(id uint32, e ber.Element) []byte {
	handle, err := decodeInt32(e.Bytes)
	if err != nil {
		return a.failure(id, ErrProtocol)
	}
	a.fileMu.Lock()
	open := a.files[handle]
	var chunkSize uint32
	if open != nil {
		remaining := open.entry.Size - open.pos
		chunkSize = remaining
		if chunkSize > a.maxFileChunk() {
			chunkSize = a.maxFileChunk()
		}
	}
	a.fileMu.Unlock()
	if open == nil {
		return a.failure(id, &ServiceError{fileServiceClass, fileErrOther})
	}
	buf := make([]byte, chunkSize)
	n, err := io.ReadFull(open.file, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return a.failure(id, &ServiceError{fileServiceClass, fileErrOther})
	}
	buf = buf[:n]
	a.fileMu.Lock()
	open.pos += uint32(n)
	more := open.pos < open.entry.Size
	a.fileMu.Unlock()
	parts := [][]byte{ber.Encode(0x80, buf)}
	if !more {
		parts = append(parts, ber.Encode(0x81, []byte{0}))
	}
	return a.response(id, ber.EncodeHigh(0xbf, 0x49, parts...))
}

func (a *Association) fileClose(id uint32, e ber.Element) []byte {
	handle, err := decodeInt32(e.Bytes)
	if err != nil {
		return a.failure(id, ErrProtocol)
	}
	a.fileMu.Lock()
	open := a.files[handle]
	delete(a.files, handle)
	a.fileMu.Unlock()
	if open == nil {
		return a.failure(id, &ServiceError{fileServiceClass, fileErrOther})
	}
	_ = open.file.Close()
	return a.response(id, ber.EncodeHigh(0x9f, 0x4a))
}

func (a *Association) fileDirectory(id uint32, e ber.Element) []byte {
	a.server.mu.Lock()
	provider := a.server.files
	a.server.mu.Unlock()
	if provider == nil {
		return a.failure(id, &ServiceError{fileServiceClass, fileErrOther})
	}
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil || len(fields) == 0 || len(fields) > 2 {
		return a.failure(id, ErrProtocol)
	}
	if fields[0].Tag != 0xa0 {
		return a.failure(id, ErrProtocol)
	}
	spec, err := fileNameFromSpec(fields[0])
	if err != nil {
		return a.failure(id, err)
	}
	continueAfter := ""
	if len(fields) == 2 {
		if fields[1].Tag != 0xa1 {
			return a.failure(id, ErrProtocol)
		}
		if continueAfter, err = fileNameFromSpec(fields[1]); err != nil {
			return a.failure(id, err)
		}
	}
	entries, more, err := provider.List(context.Background(), spec, continueAfter)
	if err != nil {
		return a.failure(id, fileServiceError(err))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	var encoded []byte
	for _, entry := range entries {
		if !validRemoteFileName(entry.Name) {
			return a.failure(id, &ServiceError{fileServiceClass, fileErrOther})
		}
		encoded = append(encoded, ber.Encode(0x30, fileSpecification(0xa0, entry.Name), fileAttributes(entry))...)
	}
	parts := [][]byte{ber.Encode(0xa0, ber.Encode(0x30, encoded))}
	if more {
		parts = append(parts, ber.Encode(0x81, []byte{0xff}))
	}
	return a.response(id, ber.EncodeHigh(0xbf, 0x4d, parts...))
}

func (a *Association) maxFileChunk() uint32 {
	// Leave room for the confirmed-response wrapper and the PDU headers.
	size := a.params.MaxPDUSize - 64
	if size > fileChunkMax {
		size = fileChunkMax
	}
	if size < 64 {
		size = 64
	}
	return uint32(size)
}

// closeFiles releases every handle held by this association.
func (a *Association) closeFiles() {
	a.fileMu.Lock()
	files := a.files
	a.files = nil
	a.fileMu.Unlock()
	for _, handle := range files {
		_ = handle.file.Close()
	}
}

func fileNameFromSpec(e ber.Element) (string, error) {
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil || len(fields) == 0 || fields[0].Tag != 0x19 {
		return "", ErrProtocol
	}
	name := string(fields[0].Bytes)
	if !validRemoteFileName(name) {
		return "", &ServiceError{fileServiceClass, fileErrMissing}
	}
	return name, nil
}

func berDecodeUint(raw []byte) (uint64, error) {
	if len(raw) == 0 || len(raw) > 8 {
		return 0, ErrProtocol
	}
	var v uint64
	for _, c := range raw {
		v = v<<8 | uint64(c)
	}
	return v, nil
}

func (a *Association) response(id uint32, service []byte) []byte {
	out := confirmed(0xa1, id, service)
	if len(out) > a.params.MaxPDUSize {
		return serviceFailure(id, fileServiceClass, fileErrBusy)
	}
	return out
}

// --- client ---------------------------------------------------------------

// callHigh performs one confirmed call whose response uses a high tag number.
func (c *Client) callHigh(ctx context.Context, request []byte, first, second byte) (ber.Element, error) {
	return c.callTag(ctx, request, first, second)
}

// FileEntry listing limits. Zero selects the defaults.
type FileLimits struct {
	MaxEntries int   // 0 selects 4096
	MaxBytes   int64 // 0 selects 64 MiB; bounds one file retrieval
	ChunkSize  int   // 0 selects a PDU-derived chunk, at most 16 KiB
}

// FileDirectory lists the peer's virtual file store. It follows the
// continuation cursor until the peer reports no more entries, the limit is
// reached or ctx ends. A reached limit returns the completed entries plus
// ErrFileLimit so the caller can continue deliberately.
var ErrFileLimit = errors.New("mms: file limit reached")

func (c *Client) FileDirectory(ctx context.Context, spec string, limit FileLimits) ([]FileEntry, error) {
	if limit.MaxEntries == 0 {
		limit.MaxEntries = 4096
	}
	if limit.MaxEntries < 0 || limit.MaxEntries > 65536 {
		return nil, fmt.Errorf("mms: file entry limit must be 1..65536")
	}
	if !validRemoteFileName(spec) {
		return nil, fmt.Errorf("mms: invalid file specification")
	}
	var out []FileEntry
	continueAfter := ""
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var parts [][]byte
		parts = append(parts, fileSpecification(0xa0, spec))
		if continueAfter != "" {
			parts = append(parts, fileSpecification(0xa1, continueAfter))
		}
		response, err := c.callHigh(ctx, ber.EncodeHigh(0xbf, 0x4d, parts...), 0xbf, 0x4d)
		if err != nil {
			return out, err
		}
		entries, more, err := parseDirectoryResponse(response)
		if err != nil {
			return out, err
		}
		out = append(out, entries...)
		if len(out) > limit.MaxEntries {
			return out[:limit.MaxEntries], ErrFileLimit
		}
		if !more {
			return out, nil
		}
		if len(entries) == 0 {
			return out, ErrProtocol
		}
		next := entries[len(entries)-1].Name
		if next <= continueAfter {
			return out, ErrProtocol
		}
		continueAfter = next
	}
}

func parseDirectoryResponse(response ber.Element) ([]FileEntry, bool, error) {
	fields, err := ber.ParseAll(response.Bytes)
	if err != nil {
		return nil, false, err
	}
	var entries []FileEntry
	more := false
	for _, field := range fields {
		switch field.Tag {
		case 0xa0:
			sequence, err := ber.ParseAll(field.Bytes)
			if err != nil || len(sequence) != 1 || sequence[0].Tag != 0x30 {
				return nil, false, ErrProtocol
			}
			items, err := ber.ParseAll(sequence[0].Bytes)
			if err != nil {
				return nil, false, err
			}
			for _, item := range items {
				entry, err := parseDirectoryEntry(item)
				if err != nil {
					return nil, false, err
				}
				entries = append(entries, entry)
			}
		case 0x81:
			more = len(field.Bytes) > 0 && field.Bytes[0] != 0
		default:
			return nil, false, ErrProtocol
		}
	}
	return entries, more, nil
}

func parseDirectoryEntry(e ber.Element) (FileEntry, error) {
	if e.Tag != 0x30 {
		return FileEntry{}, ErrProtocol
	}
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil || len(fields) != 2 || fields[0].Tag != 0xa0 || fields[1].Tag != 0xa1 {
		return FileEntry{}, ErrProtocol
	}
	name, err := fileNameFromSpec(fields[0])
	if err != nil {
		return FileEntry{}, err
	}
	return parseAttributes(name, fields[1])
}

func parseAttributes(name string, e ber.Element) (FileEntry, error) {
	entry := FileEntry{Name: name}
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil {
		return entry, err
	}
	for _, field := range fields {
		switch field.Tag {
		case 0x80:
			size, err := berDecodeUint(field.Bytes)
			if err != nil || size > 1<<32-1 {
				return entry, ErrProtocol
			}
			entry.Size = uint32(size)
		case 0x81:
			entry.LastModified = parseGeneralizedTime(string(field.Bytes))
		default:
			return entry, ErrProtocol
		}
	}
	return entry, nil
}

// FileHandle is one open remote file. Read returns the next chunk and whether
// more data follows. Close must be called; the client also closes every handle
// when the association ends.
type FileHandle struct {
	client *Client
	id     int32
	entry  FileEntry
	pos    uint32
	mu     sync.Mutex
	closed bool
}

// Entry describes the open file as declared by the peer.
func (h *FileHandle) Entry() FileEntry { return h.entry }

// OpenFile opens a remote file for reading at the given position.
func (c *Client) OpenFile(ctx context.Context, name string, position uint32) (*FileHandle, error) {
	if !validRemoteFileName(name) {
		return nil, fmt.Errorf("mms: invalid file name")
	}
	request := ber.EncodeHigh(0xbf, 0x48, fileSpecification(0xa0, name), ber.Uint(0x81, uint64(position)))
	response, err := c.callHigh(ctx, request, 0xbf, 0x48)
	if err != nil {
		return nil, err
	}
	fields, err := ber.ParseAll(response.Bytes)
	if err != nil || len(fields) < 2 || fields[0].Tag != 0x80 || fields[1].Tag != 0xa1 {
		return nil, ErrProtocol
	}
	id, err := decodeInt32(fields[0].Bytes)
	if err != nil || id == 0 {
		return nil, ErrProtocol
	}
	entry, err := parseAttributes(name, fields[1])
	if err != nil {
		return nil, err
	}
	h := &FileHandle{client: c, id: id, entry: entry, pos: position}
	c.mu.Lock()
	if c.fileHandles == nil {
		c.fileHandles = make(map[int32]*FileHandle)
	}
	c.fileHandles[id] = h
	c.mu.Unlock()
	return h, nil
}

// Read returns the next chunk. more is false on the last chunk.
func (h *FileHandle) Read(ctx context.Context) (data []byte, more bool, err error) {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return nil, false, ErrClosed
	}
	response, err := h.client.callHigh(ctx, ber.EncodeHigh(0x9f, 0x49, int32Bytes(h.id)), 0xbf, 0x49)
	if err != nil {
		return nil, false, err
	}
	fields, err := ber.ParseAll(response.Bytes)
	if err != nil || len(fields) == 0 || fields[0].Tag != 0x80 {
		return nil, false, ErrProtocol
	}
	data = append([]byte(nil), fields[0].Bytes...)
	more = true
	for _, field := range fields[1:] {
		if field.Tag == 0x81 {
			more = len(field.Bytes) > 0 && field.Bytes[0] != 0
		}
	}
	h.mu.Lock()
	h.pos += uint32(len(data))
	h.mu.Unlock()
	return data, more, nil
}

// Close releases the remote handle. Closing twice is not an error.
func (h *FileHandle) Close(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()
	h.client.mu.Lock()
	delete(h.client.fileHandles, h.id)
	h.client.mu.Unlock()
	_, err := h.client.callHigh(ctx, ber.EncodeHigh(0x9f, 0x4a, int32Bytes(h.id)), 0x9f, 0x4a)
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

// FetchFile retrieves one file into w and returns its declared entry. The
// byte budget is enforced while reading, not only from the declared size, and
// the handle is closed on every exit path.
func (c *Client) FetchFile(ctx context.Context, name string, w io.Writer, limit FileLimits) (FileEntry, error) {
	if limit.MaxBytes <= 0 {
		limit.MaxBytes = defaultFileMax
	}
	handle, err := c.OpenFile(ctx, name, 0)
	if err != nil {
		return FileEntry{}, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = handle.Close(closeCtx)
	}()
	entry := handle.Entry()
	if int64(entry.Size) > limit.MaxBytes {
		return entry, fmt.Errorf("%w: declared size %d exceeds limit", ErrFileLimit, entry.Size)
	}
	var total int64
	for {
		data, more, err := handle.Read(ctx)
		if err != nil {
			return entry, err
		}
		total += int64(len(data))
		if total > limit.MaxBytes {
			return entry, ErrFileLimit
		}
		if _, err = w.Write(data); err != nil {
			return entry, err
		}
		if !more {
			return entry, nil
		}
	}
}

// CloseFiles closes every handle opened by this client. It runs during
// shutdown; callers normally close handles explicitly.
func (c *Client) CloseFiles(ctx context.Context) {
	handles := c.takeFileHandles()
	for _, handle := range handles {
		_ = handle.Close(ctx)
	}
}

// takeFileHandles detaches the handle table, so shutdown can release handles
// without holding the client lock during remote calls.
func (c *Client) takeFileHandles() map[int32]*FileHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	handles := c.fileHandles
	c.fileHandles = nil
	return handles
}

// closeFileHandles releases local handle state without issuing remote calls:
// the association is already ending, so there is nothing to close remotely.
func (c *Client) closeFileHandles() {
	for _, handle := range c.takeFileHandles() {
		handle.mu.Lock()
		handle.closed = true
		handle.mu.Unlock()
	}
}

// fileCount is a small test-visible accessor for the server handle table.
func (a *Association) fileCount() int {
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	return len(a.files)
}
