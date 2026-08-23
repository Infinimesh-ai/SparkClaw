package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// credentialSecretsEqual is a test-only comparison helper; the credential
// vault keeps its own equality check next to its replay logic.
func credentialSecretsEqual(left, right app.CredentialSecret) bool {
	return left.Ref == right.Ref && left.Kind == right.Kind && left.Value == right.Value &&
		left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt)
}

// persistSnapshot writes a plain (non-durable) snapshot directly to the
// FileStore path. Production writes go through the durable commit pipeline in
// file_durability.go; tests use this to seed on-disk state.
func (s *FileStore) persistSnapshot() error {
	if s.path == "" {
		return nil
	}
	return s.persistSnapshotLocked()
}

func (s *FileStore) persistSnapshotLocked() error {
	if s.path == "" {
		return nil
	}
	snapshot := s.inner.snapshot()
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if s.encryption != nil {
		raw, err = s.encryption.encrypt(raw)
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
