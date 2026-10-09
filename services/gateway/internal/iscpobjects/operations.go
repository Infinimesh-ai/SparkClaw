package iscpobjects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

type OpenRequest struct {
	TransferID string `json:"transfer_id"`
	Purpose    string `json:"purpose"`
	Name       string `json:"name"`
	MediaType  string `json:"media_type"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}
type ChunkRequest struct {
	TransferID string `json:"transfer_id"`
	Index      int    `json:"index"`
	Offset     int64  `json:"offset"`
	SHA256     string `json:"sha256"`
	DataBase64 string `json:"data_base64"`
}
type objectRequest struct {
	ObjectID string `json:"object_id"`
	Version  uint64 `json:"version"`
	Offset   int64  `json:"offset,omitempty"`
	Length   int    `json:"length,omitempty"`
}
type transferRequest struct {
	TransferID string `json:"transfer_id"`
	Cursor     int    `json:"cursor,omitempty"`
}
type ReadResult struct {
	Offset     int64  `json:"offset"`
	DataBase64 string `json:"data_base64"`
	SHA256     string `json:"sha256"`
	EOF        bool   `json:"eof"`
}

func decode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return failure(wb.ErrorInvalidRequest, 400, "invalid object request")
	}
	return nil
}
func (s *Store) Open(ctx context.Context, b Binding, q OpenRequest) (Checkpoint, error) {
	if err := s.Reconcile(ctx); err != nil {
		return Checkpoint{}, err
	}
	if ctx.Err() != nil {
		return Checkpoint{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := wb.ObjectReference{ObjectID: q.TransferID, Version: 1, Size: q.Size, SHA256: q.SHA256, Purpose: q.Purpose, Name: q.Name, MediaType: q.MediaType}
	if !b.valid() || !idPattern.MatchString(q.TransferID) || ref.Validate() != nil || q.Size > min(purposeLimit(q.Purpose), s.limits.MaxObjectBytes) || purposeLimit(q.Purpose) == 0 || len(q.Name) > 255 || strings.ContainsAny(q.Name, "/\\\x00\r\n") || len(q.MediaType) > 128 || strings.ContainsAny(q.MediaType, "\r\n\x00") {
		return Checkpoint{}, failure(wb.ErrorInvalidRequest, 400, "invalid object manifest or purpose limit")
	}
	if r, err := s.read(q.TransferID); err == nil {
		if r.Binding != b {
			return Checkpoint{}, failure(wb.ErrorPermissionDenied, 403, "transfer principal mismatch")
		}
		expected := r.Object
		expected.ExpiresAt = ""
		if expected != ref {
			return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "transfer ID has different manifest")
		}
		if r.State != "uploading" && r.State != "committed" || !s.now().Before(r.ExpiresAt) {
			return Checkpoint{}, failure(wb.ErrorObjectExpired, 410, "transfer is terminal")
		}
		return r.checkpoint(0), nil
	} else {
		var oe *objectError
		if !errors.As(err, &oe) || oe.code != wb.ErrorNotFound {
			return Checkpoint{}, err
		}
	}
	if err := s.quota(b, q.Size); err != nil {
		return Checkpoint{}, err
	}
	if err := os.Mkdir(s.dir(q.TransferID), 0700); err != nil {
		return Checkpoint{}, err
	}
	now := s.now()
	ref.ExpiresAt = now.Add(s.retention(q.Purpose)).Format(time.RFC3339Nano)
	r := record{SchemaVersion: 1, TransferID: q.TransferID, Binding: b, State: "uploading", Object: ref, Received: map[int]string{}, ExpiresAt: now.Add(s.limits.UploadTTL)}
	if err := s.save(r); err != nil {
		return Checkpoint{}, err
	}
	return r.checkpoint(0), nil
}
func (s *Store) Status(ctx context.Context, b Binding, id string, cursor int) (Checkpoint, error) {
	if ctx.Err() != nil {
		return Checkpoint{}, ctx.Err()
	}
	if cursor < 0 || cursor > 8192 {
		return Checkpoint{}, failure(wb.ErrorInvalidRequest, 400, "invalid checkpoint cursor")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.bound(id, b)
	if err != nil {
		return Checkpoint{}, err
	}
	return r.checkpoint(cursor), nil
}
func (s *Store) Chunk(ctx context.Context, b Binding, q ChunkRequest) (Checkpoint, error) {
	s.chunkMu.Lock()
	slots := s.chunkSlots[q.TransferID]
	if slots == nil {
		if !idPattern.MatchString(q.TransferID) || len(s.chunkSlots) >= s.limits.MaxRecords {
			s.chunkMu.Unlock()
			return Checkpoint{}, failure(wb.ErrorInvalidRequest, 400, "invalid chunk transfer")
		}
		slots = make(chan struct{}, Window)
		s.chunkSlots[q.TransferID] = slots
	}
	s.chunkMu.Unlock()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		return Checkpoint{}, failure(wb.ErrorThrottled, 429, "transfer credit window exhausted")
	}

	if ctx.Err() != nil {
		return Checkpoint{}, ctx.Err()
	}
	if len(q.DataBase64) > base64.StdEncoding.EncodedLen(ChunkBytes) {
		return Checkpoint{}, failure(wb.ErrorResourceLimit, 413, "chunk exceeds negotiated size")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(q.DataBase64)
	if err != nil || digest(raw) != q.SHA256 {
		return Checkpoint{}, failure(wb.ErrorInvalidRequest, 400, "chunk digest mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.bound(q.TransferID, b)
	if err != nil {
		return Checkpoint{}, err
	}
	if r.State != "uploading" {
		return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "object already committed")
	}
	if q.Index < 0 || q.Offset != int64(q.Index)*ChunkBytes || q.Offset >= r.Object.Size || int64(len(raw)) != min(ChunkBytes, r.Object.Size-q.Offset) {
		return Checkpoint{}, failure(wb.ErrorInvalidRequest, 400, "chunk range mismatch")
	}
	if previous, ok := r.Received[q.Index]; ok {
		if previous != q.SHA256 {
			return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "chunk conflicts with durable bytes")
		}
		return r.checkpoint(0), nil
	}
	if err = atomicWrite(filepath.Join(s.dir(q.TransferID), fmt.Sprintf("chunk-%08d", q.Index)), raw); err != nil {
		return Checkpoint{}, err
	}
	r.Received[q.Index] = q.SHA256
	if err = s.save(r); err != nil {
		return Checkpoint{}, err
	}
	return r.checkpoint(0), nil
}
func (s *Store) Commit(ctx context.Context, b Binding, id string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.bound(id, b)
	if err != nil {
		return Checkpoint{}, err
	}
	if r.State == "committed" {
		return r.checkpoint(0), nil
	}
	count := int((r.Object.Size + ChunkBytes - 1) / ChunkBytes)
	if len(r.Received) != count {
		return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "object has missing chunks")
	}
	f, err := os.CreateTemp(s.dir(id), ".commit-")
	if err != nil {
		return Checkpoint{}, err
	}
	defer os.Remove(f.Name())
	hash := sha256.New()
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			f.Close()
			return Checkpoint{}, ctx.Err()
		}
		raw, err := readBounded(filepath.Join(s.dir(id), fmt.Sprintf("chunk-%08d", i)), ChunkBytes)
		if err != nil || digest(raw) != r.Received[i] || int64(len(raw)) != min(ChunkBytes, r.Object.Size-int64(i*ChunkBytes)) {
			f.Close()
			return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "stored chunk verification failed")
		}
		if _, err = io.MultiWriter(f, hash).Write(raw); err != nil {
			f.Close()
			return Checkpoint{}, err
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != r.Object.SHA256 {
		f.Close()
		return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "whole object digest mismatch")
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return Checkpoint{}, err
	}
	if err = f.Close(); err != nil {
		return Checkpoint{}, err
	}
	if err = os.Rename(f.Name(), filepath.Join(s.dir(id), "object.bin")); err != nil {
		return Checkpoint{}, err
	}
	r.State = "committed"
	r.ExpiresAt = s.now().Add(s.retention(r.Object.Purpose))
	r.Object.ExpiresAt = r.ExpiresAt.Format(time.RFC3339Nano)
	if err = s.save(r); err != nil {
		return Checkpoint{}, err
	}
	for i := 0; i < count; i++ {
		_ = os.Remove(filepath.Join(s.dir(id), fmt.Sprintf("chunk-%08d", i)))
	}
	return r.checkpoint(0), nil
}
func (s *Store) terminal(ctx context.Context, b Binding, id, state string) (Checkpoint, error) {
	if ctx.Err() != nil {
		return Checkpoint{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.read(id)
	if err != nil {
		return Checkpoint{}, err
	}
	if r.Binding != b {
		return Checkpoint{}, failure(wb.ErrorPermissionDenied, 403, "object principal mismatch")
	}
	if r.State == state {
		return r.checkpoint(0), nil
	}
	if state == "aborted" && r.State == "committed" {
		return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "committed object requires release")
	}
	r.State = state
	r.Received = map[int]string{}
	if err = s.save(r); err != nil {
		return Checkpoint{}, err
	}
	entries, err := os.ReadDir(s.dir(id))
	if err != nil {
		return Checkpoint{}, err
	}
	for _, e := range entries {
		if e.Name() != "state.json" {
			if err = os.Remove(filepath.Join(s.dir(id), e.Name())); err != nil {
				return Checkpoint{}, err
			}
		}
	}
	return r.checkpoint(0), nil
}
func (s *Store) Describe(ctx context.Context, b Binding, id string, version uint64) (wb.ObjectReference, error) {
	if ctx.Err() != nil {
		return wb.ObjectReference{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.bound(id, b)
	if err != nil {
		return wb.ObjectReference{}, err
	}
	if r.State != "committed" || r.Object.Version != version {
		return wb.ObjectReference{}, failure(wb.ErrorRevisionConflict, 409, "object version is not committed")
	}
	return r.Object, nil
}
func (s *Store) Read(ctx context.Context, b Binding, id string, version uint64, offset int64, length int) (ReadResult, error) {
	if ctx.Err() != nil {
		return ReadResult{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.bound(id, b)
	if err != nil {
		return ReadResult{}, err
	}
	if r.State != "committed" || version != r.Object.Version {
		return ReadResult{}, failure(wb.ErrorRevisionConflict, 409, "object version is not committed")
	}
	if offset < 0 || offset > r.Object.Size || length < 1 || length > ChunkBytes {
		return ReadResult{}, failure(wb.ErrorInvalidRequest, 400, "invalid object read range")
	}
	path := filepath.Join(s.dir(id), "object.bin")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != r.Object.Size {
		return ReadResult{}, failure(wb.ErrorInternal, 500, "stored object is unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return ReadResult{}, err
	}
	defer f.Close()
	raw := make([]byte, min(int64(length), r.Object.Size-offset))
	if _, err = f.ReadAt(raw, offset); err != nil && err != io.EOF {
		return ReadResult{}, err
	}
	return ReadResult{offset, base64.StdEncoding.EncodeToString(raw), digest(raw), offset+int64(len(raw)) == r.Object.Size}, nil
}

// Handle dispatches only the fixed transport object registry. All other domain
// operations remain with their existing adapter.
func (s *Store) Handle(ctx context.Context, b Binding, q wb.Request) (wb.Response, bool) {
	var value any
	var err error
	switch q.Operation {
	case wb.OperationTransferOpen:
		var in OpenRequest
		err = decode(q.Body, &in)
		if err == nil {
			value, err = s.Open(ctx, b, in)
		}
	case wb.OperationTransferChunk:
		var in ChunkRequest
		err = decode(q.Body, &in)
		if err == nil {
			value, err = s.Chunk(ctx, b, in)
		}
	case wb.OperationTransferStatus, wb.OperationTransferCommit, wb.OperationTransferAbort:
		var in transferRequest
		err = decode(q.Body, &in)
		if err == nil {
			switch q.Operation {
			case wb.OperationTransferStatus:
				value, err = s.Status(ctx, b, in.TransferID, in.Cursor)
			case wb.OperationTransferCommit:
				value, err = s.Commit(ctx, b, in.TransferID)
			default:
				value, err = s.terminal(ctx, b, in.TransferID, "aborted")
			}
		}
	case wb.OperationObjectDescribe, wb.OperationObjectRead, wb.OperationObjectRelease:
		var in objectRequest
		err = decode(q.Body, &in)
		if err == nil {
			switch q.Operation {
			case wb.OperationObjectDescribe:
				value, err = s.Describe(ctx, b, in.ObjectID, in.Version)
			case wb.OperationObjectRead:
				value, err = s.Read(ctx, b, in.ObjectID, in.Version, in.Offset, in.Length)
			default:
				value, err = s.Release(ctx, b, in.ObjectID, in.Version)
			}
		}
	default:
		return wb.Response{}, false
	}
	response := wb.Response{Type: wb.ResponseType, Profile: q.Profile, ID: q.ID, Status: 200}
	if err != nil {
		response.Status = 500
		response.Code = wb.ErrorInternal
		response.Error = "object persistence failed"
		var oe *objectError
		if errors.As(err, &oe) {
			response.Status = oe.status
			response.Code = oe.code
			response.Error = oe.message
			if oe.status == 429 {
				response.Retryable = true
				response.RetryAfterMS = 250
			}
		}
		return response, true
	}
	response.Body, _ = json.Marshal(value)
	return response, true
}

// Release replays its terminal receipt without requiring content to still be
// downloadable. It cannot substitute a different object version or principal.
func (s *Store) Release(ctx context.Context, b Binding, id string, version uint64) (Checkpoint, error) {
	s.mu.Lock()
	r, err := s.read(id)
	s.mu.Unlock()
	if err != nil {
		return Checkpoint{}, err
	}
	if r.Binding != b {
		return Checkpoint{}, failure(wb.ErrorPermissionDenied, 403, "object principal mismatch")
	}
	if r.Object.Version != version {
		return Checkpoint{}, failure(wb.ErrorRevisionConflict, 409, "object version mismatch")
	}
	if r.State != "committed" && r.State != "released" {
		return Checkpoint{}, failure(wb.ErrorObjectExpired, 410, "object is no longer available")
	}
	return s.terminal(ctx, b, id, "released")
}
