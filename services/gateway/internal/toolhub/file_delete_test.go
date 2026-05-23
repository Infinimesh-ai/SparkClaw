package toolhub

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestFileDeleteMovesFileToTrashWithManifest(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes", "remove-me.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("delete through approval only"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "file.delete", map[string]any{
		"path":   "notes/remove-me.txt",
		"reason": "test cleanup",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("source file should be moved out of place, err=%v", err)
	}
	out := result.Output.(map[string]any)
	trashPath, _ := out["trash_path"].(string)
	if trashPath == "" {
		t.Fatalf("missing trash path: %#v", out)
	}
	raw, err := os.ReadFile(trashPath)
	if err != nil || string(raw) != "delete through approval only" {
		t.Fatalf("trash file mismatch raw=%q err=%v", raw, err)
	}
	manifestPath, _ := out["manifest_path"].(string)
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Path      string `json:"path"`
		TrashPath string `json:"trash_path"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Path != target || manifest.TrashPath != trashPath || manifest.Reason != "test cleanup" {
		t.Fatalf("unexpected delete manifest: %#v", manifest)
	}
}

func TestFileDeleteRejectsSparkClawControlFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".sparkclaw", "state.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "file.delete", map[string]any{"path": ".sparkclaw/state.json"}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "control files") {
		t.Fatalf("expected control file rejection, got %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("control file should remain in place: %v", err)
	}
}
