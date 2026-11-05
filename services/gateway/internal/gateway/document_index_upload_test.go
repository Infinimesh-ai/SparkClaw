package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func uploadTestDocument(t *testing.T, serverURL, name, content string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(serverURL+"/api/documents/upload", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload %s returned %d", name, resp.StatusCode)
	}
}

func TestDocumentUploadPrebuildsStructureForEveryDocumentSize(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	ts := httptest.NewServer(New(cfg, st, tools, runtime).Handler())
	defer ts.Close()

	var large strings.Builder
	for section := 0; large.Len() <= 400_000; section++ {
		fmt.Fprintf(&large, "## 第%d节\n\n%s\n\n", section, strings.Repeat("大文档导入阶段只预建目录和关键词索引。", 20))
	}
	uploadTestDocument(t, ts.URL, "small.md", "# Small\nfits one scoped read")
	uploadTestDocument(t, ts.URL, "large.md", large.String())

	indexRoot := filepath.Join(cfg.Workspaces.DefaultRoot, docindex.RelativeRoot)
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, _ := os.ReadDir(indexRoot)
		ready := map[string]bool{}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue // The workspace cursor key is not an index directory.
			}
			raw, err := os.ReadFile(filepath.Join(indexRoot, entry.Name(), "manifest.json"))
			var manifest docindex.Manifest
			if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.State != docindex.StateReady {
				continue
			}
			if manifest.StructureGeneration == "" || manifest.Chunks == 0 {
				t.Fatalf("prebuilt upload has no usable versioned structure: %+v", manifest)
			}
			for _, name := range []string{"uploads/small.md", "uploads/large.md"} {
				if slices.Contains(manifest.SourcePaths, name) {
					ready[name] = true
				}
			}
		}
		if len(ready) == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("uploads were not prebuilt for both sizes: %v", ready)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
