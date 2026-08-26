package jingsiruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type recordKind string

const (
	recordBound  recordKind = "bound"
	recordFenced recordKind = "negative_fence"
)

type record struct {
	Version           int              `json:"version"`
	Kind              recordKind       `json:"kind"`
	CallerID          string           `json:"caller_id"`
	RequestKey        string           `json:"request_key"`
	Authorization     Authorization    `json:"authorization"`
	AuthorizationHash string           `json:"authorization_sha256"`
	SemanticHash      string           `json:"semantic_sha256,omitempty"`
	Submit            *SubmitPayload   `json:"submit,omitempty"`
	ExecutionID       string           `json:"execution_id,omitempty"`
	State             string           `json:"state,omitempty"`
	AcceptedAt        *time.Time       `json:"accepted_at,omitempty"`
	UpdatedAt         time.Time        `json:"updated_at"`
	CompletedAt       *time.Time       `json:"completed_at,omitempty"`
	Events            []ExecutionEvent `json:"events,omitempty"`
	Result            *ExecutionResult `json:"result,omitempty"`
	FenceID           string           `json:"fence_id,omitempty"`
	FenceCommittedAt  *time.Time       `json:"fence_committed_at,omitempty"`
	CancelRequested   bool             `json:"cancel_requested,omitempty"`
}

type fileStore struct {
	dir         string
	mu          sync.Mutex
	byKey       map[string]*record
	byExecution map[string]*record
}

func newFileStore(dir string) (*fileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("runtime state directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create runtime state directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect runtime state directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("runtime state path must be a real directory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure runtime state directory: %w", err)
	}
	store := &fileStore{dir: dir, byKey: map[string]*record{}, byExecution: map[string]*record{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list runtime state directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		value, err := readRecord(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		key := recordMapKey(value.CallerID, value.Authorization.SpaceID, value.RequestKey)
		if _, exists := store.byKey[key]; exists {
			return nil, errors.New("duplicate durable runtime request key")
		}
		store.byKey[key] = value
		if value.ExecutionID != "" {
			if _, exists := store.byExecution[value.ExecutionID]; exists {
				return nil, errors.New("duplicate durable runtime execution id")
			}
			store.byExecution[value.ExecutionID] = value
		}
	}
	return store, nil
}

func readRecord(path string) (*record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open runtime record: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("runtime record is not a bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var value record
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode runtime record: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := validateRecord(&value); err != nil {
		return nil, err
	}
	return &value, nil
}

func validateRecord(value *record) error {
	if value == nil || value.Version != 1 || value.CallerID == "" || value.RequestKey == "" ||
		value.AuthorizationHash == "" || value.UpdatedAt.IsZero() {
		return errors.New("runtime record is incomplete")
	}
	switch value.Kind {
	case recordBound:
		if value.Submit == nil || value.SemanticHash == "" || value.ExecutionID == "" || value.AcceptedAt == nil || value.State == "" {
			return errors.New("bound runtime record is incomplete")
		}
	case recordFenced:
		if value.FenceID == "" || value.FenceCommittedAt == nil || value.ExecutionID != "" || value.Submit != nil {
			return errors.New("negative-fence runtime record is malformed")
		}
	default:
		return errors.New("runtime record kind is unsupported")
	}
	return nil
}

func (s *fileStore) key(callerID, spaceID, requestKey string) string {
	return recordMapKey(callerID, spaceID, requestKey)
}

func recordMapKey(callerID, spaceID, requestKey string) string {
	_ = spaceID
	return callerID + "\x00" + requestKey
}

func (s *fileStore) path(value *record) string {
	digest := sha256.Sum256([]byte(recordMapKey(value.CallerID, value.Authorization.SpaceID, value.RequestKey)))
	return filepath.Join(s.dir, hex.EncodeToString(digest[:])+".json")
}

func (s *fileStore) persistLocked(value *record) error {
	if err := validateRecord(value); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode runtime record: %w", err)
	}
	if len(raw) > 1<<20 {
		return errors.New("runtime record exceeds 1048576 bytes")
	}
	temporary, err := os.CreateTemp(s.dir, ".runtime-*.tmp")
	if err != nil {
		return fmt.Errorf("create runtime record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure runtime record: %w", err)
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return fmt.Errorf("write runtime record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync runtime record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close runtime record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(value)); err != nil {
		return fmt.Errorf("commit runtime record: %w", err)
	}
	directory, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open runtime state directory for sync: %w", err)
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return fmt.Errorf("sync runtime state directory: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close runtime state directory: %w", closeErr)
	}
	return nil
}

func canonicalAuthorizationHash(value Authorization) (string, error) {
	value.ToolScope = sortedStrings(value.ToolScope)
	value.DataScope = sortedStrings(value.DataScope)
	value.NetworkScope = sortedStrings(value.NetworkScope)
	value.DeadlineAt = value.DeadlineAt.UTC()
	return hashJSON(value)
}

func canonicalSubmitHash(callerID string, authorization Authorization, payload SubmitPayload) (string, error) {
	authorization.ToolScope = sortedStrings(authorization.ToolScope)
	authorization.DataScope = sortedStrings(authorization.DataScope)
	authorization.NetworkScope = sortedStrings(authorization.NetworkScope)
	authorization.DeadlineAt = authorization.DeadlineAt.UTC()
	if payload.MemoryContext != nil {
		copyMemory := *payload.MemoryContext
		copyMemory.MemoryRefs = sortedRefs(copyMemory.MemoryRefs)
		copyMemory.SourceRefs = sortedRefs(copyMemory.SourceRefs)
		payload.MemoryContext = &copyMemory
	}
	return hashJSON(struct {
		CallerID      string        `json:"caller_id"`
		Authorization Authorization `json:"authorization"`
		Payload       SubmitPayload `json:"payload"`
	}{callerID, authorization, payload})
}

func hashJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func sortedStrings(values []string) []string {
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	return copyValues
}

func sortedRefs(values []OpaqueRef) []OpaqueRef {
	copyValues := append([]OpaqueRef(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool {
		if copyValues[i].ID == copyValues[j].ID {
			return copyValues[i].Version < copyValues[j].Version
		}
		return copyValues[i].ID < copyValues[j].ID
	})
	return copyValues
}
