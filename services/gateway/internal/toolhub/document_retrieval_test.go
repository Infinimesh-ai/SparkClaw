package toolhub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func writeLargeMarkdownBook(t *testing.T, root string) string {
	t.Helper()
	var book strings.Builder
	topics := []string{"物理层", "数据链路层", "网络层", "运输层", "应用层"}
	for chapter, topic := range topics {
		fmt.Fprintf(&book, "# 第%d章 %s\n\n", chapter+1, topic)
		for section := 1; section <= 8; section++ {
			fmt.Fprintf(&book, "## %d.%d %s专题%d\n\n", chapter+1, section, topic, section)
			for paragraph := 0; paragraph < 12; paragraph++ {
				book.WriteString(strings.Repeat("本节通过实例分析协议的工作过程，并比较不同方案的取舍。", 6))
				if topic == "运输层" && section == 4 && paragraph == 2 {
					book.WriteString("慢开始算法让拥塞窗口按指数增长，直到达到慢开始门限 ssthresh。")
				}
				book.WriteString("\n\n")
			}
		}
	}
	if book.Len() <= document.SmallExtractedMaxBytes {
		t.Fatalf("fixture is %d bytes; it must exceed the complete-read limit", book.Len())
	}
	if err := os.WriteFile(filepath.Join(root, "book.md"), []byte(book.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return "book.md"
}

func TestLargeDocumentRetrievalToolsLocateAndReadPassages(t *testing.T) {
	root := t.TempDir()
	path := writeLargeMarkdownBook(t, root)
	hub := newRetrievalTestHub(t, root)

	progress := []string{}
	ctx := WithProgressReporter(context.Background(), func(text string) { progress = append(progress, text) })
	read, err := hub.Execute(ctx, "files.read", map[string]any{"path": path}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	pointer := read.Output.(map[string]any)
	if pointer["retrieval_required"] != true || !strings.Contains(pointer["content"].(string), "第4章 运输层") {
		t.Fatalf("large read did not return the outline pointer: %v", pointer["content"])
	}
	if len(progress) != 1 || progress[0] != documentIndexProgress {
		t.Fatalf("index build progress = %v, want one %q", progress, documentIndexProgress)
	}

	progress = progress[:0]
	search, err := hub.Execute(ctx, "document.search", map[string]any{"path": path, "keywords": []any{"慢开始", "拥塞窗口"}, "top_k": 3}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 0 {
		t.Fatalf("a ready index still reported progress: %v", progress)
	}
	hits := search.Output.(map[string]any)["hits"].([]any)
	top := hits[0].(map[string]any)
	anchor := top["anchor"].(map[string]any)
	if !strings.Contains(fmt.Sprint(anchor["heading_path"]), "4.4 运输层专题4") {
		t.Fatalf("top hit is not the slow-start section: %#v", top)
	}

	rangeRead, err := hub.Execute(ctx, "document.read_range", map[string]any{"path": path, "chunk_ids": []any{top["chunk_id"]}}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	content := rangeRead.Output.(map[string]any)["content"].(string)
	if !strings.Contains(content, "慢开始门限") || !strings.HasPrefix(content, "[") {
		t.Fatalf("range read missed the passage or its location header: %.200s", content)
	}

	outline, err := hub.Execute(ctx, "document.outline", map[string]any{"path": path}, "session", "run")
	if err != nil || len(outline.Output.(map[string]any)["outline"].([]any)) != 45 {
		t.Fatalf("outline tool did not list the 5 chapters and 40 sections: %v", err)
	}
}

func TestReadToolsRefuseTheDocumentIndexDirectory(t *testing.T) {
	root := t.TempDir()
	path := writeLargeMarkdownBook(t, root)
	hub := newDocumentWorkflowHub(t, root, store.NewMemoryStore())
	if _, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": path}, "session", "run"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, docindex.RelativeRoot))
	if err != nil || len(entries) != 1 {
		t.Fatalf("index directory was not created: %v", err)
	}
	manifest := filepath.Join(docindex.RelativeRoot, entries[0].Name(), "manifest.json")
	if _, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": manifest}, "session", "run"); err == nil || !strings.Contains(err.Error(), "control files") {
		t.Fatalf("index manifest was readable through files.read: %v", err)
	}
}

func TestLargeDocumentReadKeepsDeferredErrorsForUnindexedFormats(t *testing.T) {
	hub := newDocumentWorkflowHub(t, t.TempDir(), store.NewMemoryStore())
	deferred := &document.PipelineError{Code: document.CodeStrategyDeferred, Stage: document.StageInspect, Format: app.DocumentFormatImage}
	if _, err := hub.largeDocumentRead(context.Background(), "scan.png", deferred); !errors.Is(err, deferred) {
		t.Fatalf("an image deferral was not returned unchanged: %v", err)
	}
	other := errors.New("parse failed")
	if _, err := hub.largeDocumentRead(context.Background(), "book.pdf", other); !errors.Is(err, other) {
		t.Fatalf("a non-deferral error was replaced: %v", err)
	}
}

// newRetrievalTestHub carries the dual-light Fast capacity so retrieval
// results are bounded by a realistic stage budget.
func newRetrievalTestHub(t *testing.T, root string) *ToolHub {
	t.Helper()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Model.Fast.ContextTokens = 32768
	cfg.Model.Fast.OutputBudgets = map[modelcapacity.OutputBudgetClass]int{modelcapacity.OutputAnswer: 4096, modelcapacity.OutputWorkflowStructured: 4096}
	hub := New(cfg, store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	return hub
}
