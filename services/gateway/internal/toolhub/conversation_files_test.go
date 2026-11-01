package toolhub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestConversationFileReadMaterializesAndWritesAreRejected(t *testing.T) {
	root := t.TempDir()
	cfg := configtest.MustLoadDefault()
	parent := New(cfg, store.NewMemoryStore())
	defer parent.Close()
	storage := cfg.Storage
	storage.ArtifactBackend = "filesystem"
	storage.ArtifactDir = t.TempDir()
	calls := 0
	hub, err := parent.WithExecutionScope(store.NewMemoryStore(), ExecutionResources{OwnerID: "owner", WorkspaceRoot: root, Artifacts: artifact.NewStore(storage), MaterializeConversationFile: func(_ context.Context, abs string) error {
		calls++
		if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte("historical text"), 0600)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	name := filepath.Join("conversation-files", "file-id", "report.txt")
	got, err := hub.filesRead(t.Context(), map[string]any{"path": name}, "session", "run")
	if err != nil || !strings.Contains(strings.TrimSpace(got.Output.(map[string]any)["content"].(string)), "historical text") || calls != 1 {
		t.Fatalf("read=%#v err=%v calls=%d", got, err, calls)
	}
	if _, err = hub.resolveNewOutputPath(name); err == nil {
		t.Fatal("accepted write to handle")
	}
	if _, err = hub.fileDelete(t.Context(), map[string]any{"path": name}); err == nil {
		t.Fatal("accepted delete of handle")
	}
}
