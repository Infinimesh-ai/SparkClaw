package toolhub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
)

func TestFilesSearchReturnsBoundedFilenameOnlyCandidates(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"b/annual-report.pdf", "a/annual-report.pdf", "content-only.txt"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		content := []byte(rel)
		if rel == "content-only.txt" {
			content = []byte("annual report")
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())
	defer hub.Close()
	result, err := hub.filesSearch(t.Context(), map[string]any{"query": "annual report", "max_results": 1})
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	matches := output["results"].([]workspacefiles.Match)
	if output["complete"] != true || output["truncated"] != true || output["count"] != 1 ||
		len(matches) != 1 || matches[0].RelPath != "a/annual-report.pdf" {
		t.Fatalf("files.search result = %#v", output)
	}
	for _, key := range []string{"root", "path", "preview"} {
		if _, exists := output[key]; exists {
			t.Fatalf("files.search leaked %q: %#v", key, output)
		}
	}
}
