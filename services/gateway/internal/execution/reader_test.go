package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readerDiskSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[relative] = info.ModTime().String() + info.Mode().String()
		if !entry.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[relative] += Digest(raw)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExecutionSnapshotDoesNotInitializeMissingStorage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing", "execution")
	reader, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Installation("owner", "client", newUUID()); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := reader.Lookup("owner", "client", newUUID()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created storage", err)
	}
}

func TestExecutionSnapshotReadsCompletedFilesAndProjectsInterruptedWithoutWrites(t *testing.T) {
	s, e, digest := setup(t, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		return Output{Content: "ready", Files: map[string][]byte{"report.txt": []byte("durable bytes")}}, nil
	})
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	status := completed(t, s, e)
	var payload Payload
	if err := json.Unmarshal([]byte(status.Result.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "content", "orphan.sealed"), []byte("must not prune on GET"), 0600); err != nil {
		t.Fatal(err)
	}
	before := readerDiskSnapshot(t, s.root)
	// A snapshot does not acquire the writer's process lock.
	reader, err := Read(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Installation(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err := reader.Installation(e.OwnerID, "other-client", e.InstallationID); !errors.Is(err, ErrConflict) {
		t.Fatal("client binding lost", err)
	}
	got, err := reader.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || got.Result == nil || got.Result.Digest != status.Result.Digest {
		t.Fatalf("result %+v %v", got, err)
	}
	raw, err := reader.File(e.OwnerID, e.ClientID, e.RequestID, payload.Files[0].ID)
	if err != nil || string(raw) != "durable bytes" {
		t.Fatalf("file %q %v", raw, err)
	}
	if !reflect.DeepEqual(before, readerDiskSnapshot(t, s.root)) {
		t.Fatal("snapshot changed disk")
	}
	s.mu.Lock()
	f := s.control.Fences[keyFor(e.OwnerID, e.ClientID, e.RequestID)]
	f.State = "running"
	s.control.Fences[keyFor(e.OwnerID, e.ClientID, e.RequestID)] = f
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	before = readerDiskSnapshot(t, s.root)
	reader, err = Read(s.root)
	if err != nil {
		t.Fatal(err)
	}
	got, err = reader.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || got.State != "unknown" || got.Result != nil {
		t.Fatalf("interrupted %+v %v", got, err)
	}
	if !reflect.DeepEqual(before, readerDiskSnapshot(t, s.root)) {
		t.Fatal("interruption GET persisted projection or pruned spool")
	}
}

func TestExecutionSnapshotRejectsManifestMismatchBeforeServingFile(t *testing.T) {
	s, e, digest := setup(t, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		return Output{Content: "ready", Files: map[string][]byte{"report.txt": []byte("correct")}}, nil
	})
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	status := completed(t, s, e)
	var payload Payload
	if err := json.Unmarshal([]byte(status.Result.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	key := keyFor(e.OwnerID, e.ClientID, e.RequestID)
	content, err := s.unseal(key)
	if err != nil {
		t.Fatal(err)
	}
	content.Files[payload.Files[0].ID] = []byte("wrong!!")
	if err := s.seal(key, content); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reader, err := Read(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.File(e.OwnerID, e.ClientID, e.RequestID, payload.Files[0].ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("manifest mismatch exposed file", err)
	}
	if _, err := reader.Lookup(e.OwnerID, e.ClientID, e.RequestID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("manifest mismatch exposed result", err)
	}
}
