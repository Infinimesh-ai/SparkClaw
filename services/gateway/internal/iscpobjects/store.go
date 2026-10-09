// Package iscpobjects persists bounded, principal-bound ISCP objects. The caller
// must reauthorize every operation; possession of an object ID grants no access.
package iscpobjects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

const ChunkBytes = 8192
const Window = 2
const checkpointPage = 256

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Binding struct {
	DeploymentID          string `json:"deployment_id"`
	OwnerID               string `json:"owner_id"`
	ClientID              string `json:"client_id"`
	InstallationID        string `json:"installation_id"`
	AuthorizationRevision uint64 `json:"authorization_revision"`
}

func (b Binding) valid() bool {
	for _, v := range []string{b.DeploymentID, b.OwnerID, b.ClientID, b.InstallationID} {
		if v == "" || len(v) > 200 {
			return false
		}
	}
	return b.AuthorizationRevision > 0
}

type Limits struct {
	MaxObjectBytes, MaxOwnerBytes, MaxTotalBytes int64
	MaxTransfers, MaxRecords                     int
	UploadTTL, Retention                         time.Duration
}

func DefaultLimits() Limits {
	return Limits{64 << 20, 256 << 20, 512 << 20, 32, 16384, time.Hour, 24 * time.Hour}
}
func normalizeLimits(l Limits) (Limits, error) {
	d := DefaultLimits()
	if l == (Limits{}) {
		return d, nil
	}
	if l.MaxObjectBytes < ChunkBytes || l.MaxObjectBytes > d.MaxObjectBytes || l.MaxOwnerBytes < l.MaxObjectBytes || l.MaxTotalBytes < l.MaxOwnerBytes || l.MaxTransfers < 1 || l.MaxTransfers > 128 || l.MaxRecords < l.MaxTransfers || l.MaxRecords > 65536 || l.UploadTTL < time.Minute || l.UploadTTL > 24*time.Hour || l.Retention < time.Minute || l.Retention > 7*24*time.Hour {
		return l, errors.New("invalid bounded object limits")
	}
	return l, nil
}

type Store struct {
	chunkMu    sync.Mutex
	chunkSlots map[string]chan struct{}
	mu         sync.Mutex
	directory  string
	limits     Limits
	now        func() time.Time
}
type record struct {
	SchemaVersion int                `json:"schema_version"`
	TransferID    string             `json:"transfer_id"`
	Binding       Binding            `json:"binding"`
	State         string             `json:"state"`
	Object        wb.ObjectReference `json:"object"`
	Received      map[int]string     `json:"received"`
	ExpiresAt     time.Time          `json:"expires_at"`
}
type Checkpoint struct {
	TransferID        string             `json:"transfer_id"`
	State             string             `json:"state"`
	Object            wb.ObjectReference `json:"object"`
	ChunkBytes        int                `json:"chunk_bytes"`
	Window            int                `json:"window"`
	AcknowledgedBytes int64              `json:"acknowledged_bytes"`
	Received          []int              `json:"received"`
	NextCursor        *int               `json:"next_cursor"`
	ExpiresAt         time.Time          `json:"expires_at"`
}
type objectError struct {
	code    wb.ErrorCode
	status  int
	message string
}

func (e *objectError) Error() string { return e.message }
func failure(code wb.ErrorCode, status int, message string) error {
	return &objectError{code, status, message}
}
func NewStore(directory string, limits Limits) (*Store, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("object directory must be absolute")
	}
	l, err := normalizeLimits(limits)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("object directory must be private")
	}
	s := &Store{directory: directory, limits: l, chunkSlots: map[string]chan struct{}{}, now: func() time.Time { return time.Now().UTC() }}
	if err = s.Reconcile(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) dir(id string) string { return filepath.Join(s.directory, id) }
func (s *Store) read(id string) (record, error) {
	var r record
	if !idPattern.MatchString(id) {
		return r, failure(wb.ErrorInvalidRequest, 400, "invalid object ID")
	}
	dir := s.dir(id)
	if info, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return r, failure(wb.ErrorNotFound, 404, "object not found")
		}
		return r, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return r, errors.New("unsafe object directory")
	}
	path := filepath.Join(dir, "state.json")
	info, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return r, errors.New("invalid object journal")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if r.SchemaVersion != 1 || r.TransferID != id || r.Object.ObjectID != id || !r.Binding.valid() || r.Object.Validate() != nil || len(r.Received) > 8192 {
		return r, errors.New("invalid object journal binding")
	}
	switch r.State {
	case "uploading", "committed", "aborted", "expired", "released":
	default:
		return r, errors.New("invalid object state")
	}
	return r, nil
}
func (s *Store) save(r record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir(r.TransferID), "state.json"), raw)
}
func atomicWrite(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".next-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func (s *Store) bound(id string, b Binding) (record, error) {
	r, err := s.read(id)
	if err != nil {
		return r, err
	}
	if !b.valid() || r.Binding != b {
		return r, failure(wb.ErrorPermissionDenied, 403, "object principal mismatch")
	}
	if r.State == "expired" || r.State == "aborted" || r.State == "released" || !s.now().Before(r.ExpiresAt) {
		return r, failure(wb.ErrorObjectExpired, 410, "object is no longer available")
	}
	return r, nil
}
func purposeLimit(p string) int64 {
	switch p {
	case "execution_request":
		return 2 << 20
	case "context":
		return 1 << 20
	case "execution_input", "execution_result", "event_snapshot", "browser_capture", "request_body":
		return 8 << 20
	case "speech_recording", "speech_audio":
		return 25 << 20
	case app.EmailSendAttachmentPurpose:
		return app.EmailSendMaxAttachmentBytes
	case "file", "mail_attachment":
		return 64 << 20
	}
	return 0
}
func (r record) checkpoint(cursor int) Checkpoint {
	c := Checkpoint{TransferID: r.TransferID, State: r.State, Object: r.Object, ChunkBytes: ChunkBytes, Window: Window, Received: []int{}, ExpiresAt: r.ExpiresAt}
	count := int((r.Object.Size + ChunkBytes - 1) / ChunkBytes)
	for i := 0; i < count; i++ {
		if _, ok := r.Received[i]; ok {
			c.AcknowledgedBytes += min(ChunkBytes, r.Object.Size-int64(i*ChunkBytes))
		}
	}
	for i := cursor; i < count; i++ {
		if _, ok := r.Received[i]; !ok {
			continue
		}
		if len(c.Received) == checkpointPage {
			next := i
			c.NextCursor = &next
			break
		}
		c.Received = append(c.Received, i)
	}
	return c
}

// Reconcile reclaims expired payloads and uncommitted chunk writes after crashes.
// Small tombstones are retained and bounded; an old transfer ID never starts a
// new upload after expiry or cancellation.
func (s *Store) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return err
	}
	if len(entries) > s.limits.MaxRecords {
		return errors.New("object record limit exceeded")
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !idPattern.MatchString(entry.Name()) {
			return errors.New("unexpected object directory entry")
		}
		r, err := s.read(entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			if err = os.RemoveAll(s.dir(entry.Name())); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if (r.State == "uploading" || r.State == "committed") && !s.now().Before(r.ExpiresAt) {
			r.State = "expired"
			if err = s.save(r); err != nil {
				return err
			}
		}
		files, err := os.ReadDir(s.dir(r.TransferID))
		if err != nil {
			return err
		}
		for _, f := range files {
			keep := f.Name() == "state.json" || (r.State == "committed" && f.Name() == "object.bin")
			if r.State == "uploading" {
				for index := range r.Received {
					if f.Name() == fmt.Sprintf("chunk-%08d", index) {
						keep = true
						break
					}
				}
			}
			if !keep {
				if err = os.Remove(filepath.Join(s.dir(r.TransferID), f.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (s *Store) quota(b Binding, size int64) error {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return err
	}
	if len(entries) >= s.limits.MaxRecords {
		return failure(wb.ErrorResourceLimit, 413, "object receipt quota reached")
	}
	var total, owner int64
	active := 0
	for _, e := range entries {
		r, err := s.read(e.Name())
		if err != nil {
			return err
		}
		if (r.State != "uploading" && r.State != "committed") || !s.now().Before(r.ExpiresAt) {
			continue
		}
		total += r.Object.Size
		if r.Binding.OwnerID == b.OwnerID && r.Binding.DeploymentID == b.DeploymentID {
			owner += r.Object.Size
			if r.State == "uploading" {
				active++
			}
		}
	}
	if total+size > s.limits.MaxTotalBytes || owner+size > s.limits.MaxOwnerBytes || active >= s.limits.MaxTransfers {
		return failure(wb.ErrorResourceLimit, 413, "object storage quota exceeded")
	}
	return nil
}
func readBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid stored object file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// Run owns expiry cleanup independently of new uploads. The Gateway runs it
// under its lifecycle context; cancellation stops the worker and bounds scans.
func (s *Store) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.Reconcile(ctx); err != nil {
				return err
			}
		}
	}
}

// Mail uploads are temporary transfer copies, never permanent workspace storage.
// Reading, reopening, replaying commit, or saving a draft cannot extend this TTL.
func (s *Store) retention(purpose string) time.Duration {
	if purpose == app.EmailSendAttachmentPurpose {
		return min(s.limits.Retention, 24*time.Hour)
	}
	return s.limits.Retention
}
