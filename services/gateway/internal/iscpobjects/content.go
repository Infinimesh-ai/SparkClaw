package iscpobjects

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// Put is the same verified commit entry used by incoming chunks. Domain code
// cannot publish a manifest before the object and journal are durable.
func (s *Store) Put(ctx context.Context, b Binding, purpose, name, mediaType string, raw []byte) (wb.ObjectReference, error) {
	id, err := newID()
	if err != nil {
		return wb.ObjectReference{}, err
	}
	c, err := s.Open(ctx, b, OpenRequest{id, purpose, name, mediaType, int64(len(raw)), digest(raw)})
	if err != nil {
		return wb.ObjectReference{}, err
	}
	for offset := 0; offset < len(raw); offset += ChunkBytes {
		chunk := raw[offset:min(offset+ChunkBytes, len(raw))]
		_, err = s.Chunk(ctx, b, ChunkRequest{id, offset / ChunkBytes, int64(offset), digest(chunk), base64.StdEncoding.EncodeToString(chunk)})
		if err != nil {
			_, _ = s.terminal(context.Background(), b, id, "aborted")
			return wb.ObjectReference{}, err
		}
	}
	c, err = s.Commit(ctx, b, id)
	return c.Object, err
}
func (s *Store) ReadAll(ctx context.Context, b Binding, ref wb.ObjectReference, maxBytes int64) ([]byte, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Size > maxBytes {
		return nil, failure(wb.ErrorResourceLimit, 413, "object exceeds domain limit")
	}
	actual, err := s.Describe(ctx, b, ref.ObjectID, ref.Version)
	if err != nil {
		return nil, err
	}
	if actual.SHA256 != ref.SHA256 || actual.Size != ref.Size || actual.Purpose != ref.Purpose {
		return nil, failure(wb.ErrorRevisionConflict, 409, "object reference does not match committed version")
	}
	raw := make([]byte, 0, ref.Size)
	for offset := int64(0); offset < ref.Size; {
		r, err := s.Read(ctx, b, ref.ObjectID, ref.Version, offset, ChunkBytes)
		if err != nil {
			return nil, err
		}
		chunk, err := base64.StdEncoding.Strict().DecodeString(r.DataBase64)
		if err != nil || digest(chunk) != r.SHA256 {
			return nil, failure(wb.ErrorInternal, 500, "stored chunk corrupted")
		}
		raw = append(raw, chunk...)
		offset += int64(len(chunk))
	}
	if digest(raw) != ref.SHA256 {
		return nil, failure(wb.ErrorRevisionConflict, 409, "object digest verification failed")
	}
	return raw, nil
}
func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 15) | 64
	raw[8] = (raw[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]), nil
}
