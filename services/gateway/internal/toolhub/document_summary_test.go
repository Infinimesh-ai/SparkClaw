package toolhub

import (
	"context"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDocumentSummarizeMapsEachChapterOnceAndReusesTheCache(t *testing.T) {
	root := t.TempDir()
	path := writeLargeMarkdownBook(t, root)
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	st := store.NewMemoryStore()
	hub := New(cfg, st)
	t.Cleanup(func() { _ = hub.Close() })

	progress := []string{}
	ctx := WithProgressReporter(context.Background(), func(text string) { progress = append(progress, text) })
	result, err := hub.Execute(ctx, "document.summarize", map[string]any{"path": path}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	sections := output["sections"].([]any)
	if len(sections) != 5 || output["read_complete"] != true {
		t.Fatalf("whole-document summary sections = %d complete=%v", len(sections), output["read_complete"])
	}
	content := output["content"].(string)
	for _, chapter := range []string{"第1章 物理层", "第5章 应用层"} {
		if !strings.Contains(content, "## "+chapter) {
			t.Fatalf("summary is missing %s: %.300s", chapter, content)
		}
	}
	if len(progress) == 0 || progress[len(progress)-1] != "正在总结文档章节（5/5）" {
		t.Fatalf("summary progress = %v", progress)
	}
	calls, err := st.ListModelCalls(context.Background(), "session", "")
	if err != nil {
		t.Fatal(err)
	}
	first := len(calls)
	if first < 5 {
		t.Fatalf("expected one model call per chapter, got %d", first)
	}
	if _, err := hub.Execute(context.Background(), "document.summarize", map[string]any{"path": path}, "session", "run"); err != nil {
		t.Fatal(err)
	}
	calls, _ = st.ListModelCalls(context.Background(), "session", "")
	if len(calls) != first {
		t.Fatalf("a repeated summary called the model again: %d -> %d", first, len(calls))
	}
}

func TestSplitSummaryTextKeepsPartsWithinTheBudget(t *testing.T) {
	text := strings.Repeat("一段较长的章节正文。\n", 2000)
	parts := splitSummaryText(text, 4000)
	joined := 0
	for _, part := range parts {
		if len(part) > 4000 {
			t.Fatalf("part of %d bytes exceeds the budget", len(part))
		}
		joined += len(part)
	}
	if len(parts) < 2 || joined < len(text)-len(parts) {
		t.Fatalf("split lost text: %d parts, %d of %d bytes", len(parts), joined, len(text))
	}
}
