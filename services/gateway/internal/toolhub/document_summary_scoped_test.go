package toolhub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestScopedSummaryIncludesLatePartAndDisclosesOmittedParts(t *testing.T) {
	root := t.TempDir()
	source := "# Early\n\nInitial rule.\n\n# Long section\n\n" + strings.Repeat("Ordinary supporting source text.\n", 500) + "LATE_EXCEPTION_ONLY\n"
	if err := os.WriteFile(filepath.Join(root, "late.md"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var observedLate atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		requests.Add(1)
		answer := "Covered source part."
		if strings.Contains(string(raw), "LATE_EXCEPTION_ONLY") {
			observedLate.Store(true)
			answer = "Preserve LATE_EXCEPTION_ONLY."
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": answer}}}})
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	cfg.Runtime.StageEvidenceMaxBytes = 1024
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	st := store.NewMemoryStore()
	hub := New(cfg, st)
	defer hub.Close()
	result, err := hub.Execute(t.Context(), "document.summarize", map[string]any{"path": "late.md", "contract_version": 2}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	coverage := out["coverage"].(map[string]any)
	if !observedLate.Load() || !strings.Contains(out["content"].(string), "LATE_EXCEPTION_ONLY") {
		t.Fatal("whole-source summary never exposed the late exception to the model")
	}
	if out["read_complete"] != false || coverage["status"] != "partial" || len(out["missing_parts"].([]string)) == 0 {
		t.Fatalf("sampled summary claimed full coverage: %#v", coverage)
	}
	before := requests.Load()
	if _, err := hub.Execute(t.Context(), "document.summarize", map[string]any{"path": "late.md", "contract_version": 2}, "session", "run-cache"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != before {
		t.Fatal("cached summary dispatched the model again")
	}
	events, err := st.ListAudit(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "document.summary.cache_hit" && event.RunID == "run-cache" {
			found = true
			if event.Fields["cache_key"] == "" || event.Fields["source_sha256"] == "" || event.Fields["structure_generation"] == "" || event.Fields["summary"] == "" || event.Fields["model_dispatched"] != false {
				t.Fatalf("cache reuse provenance missing: %#v", event.Fields)
			}
		}
	}
	if !found {
		t.Fatal("cache reuse was not recorded")
	}
}

func TestScopedSummaryRejectsSourceVersionReplacement(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "version.md"), []byte("original source"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	hub := New(cfg, store.NewMemoryStore())
	defer hub.Close()
	outline, err := hub.Execute(t.Context(), "document.outline", map[string]any{"path": "version.md"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	first := outline.Output.(map[string]any)
	if err := os.WriteFile(filepath.Join(root, "version.md"), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = hub.Execute(t.Context(), "document.summarize", map[string]any{"path": "version.md", "contract_version": 2, "source_sha256": first["source_sha256"], "structure_generation": first["structure_generation"]}, "session", "run")
	if err == nil {
		t.Fatal("summary accepted a different source version")
	}
}

func TestScopedSummaryRetainsEmptyFormulaCellsAndDependencies(t *testing.T) {
	index := &docindex.Index{Nodes: []document.StructureNode{
		{ID: "header", Text: "Amount in EUR", Kind: "header"},
		{ID: "cached", Text: "42", Values: []map[string]any{{"formula": "SUM(A1:A3)", "cached_result": 42, "formula_result_recalculated": false}}, Dependencies: []string{"header"}},
		{ID: "empty", Text: "", Values: []map[string]any{{"formula": "A1+A2", "cached_result_available": false, "formula_result_recalculated": false}}, Dependencies: []string{"header"}},
	}, Chunks: []docindex.Chunk{{NodeID: "header", Text: "Amount in EUR"}, {NodeID: "cached", Text: "42"}}}
	unit := docindex.SummaryUnit{ID: "sheet", ChunkStart: 0, ChunkEnd: 2}
	parts, missing := scopedSummaryParts(index, unit, 2000)
	body := strings.Join(parts, "\n")
	for _, marker := range []string{"A1+A2", `"cached_result_available":false`, `"formula_result_recalculated":false`, "Amount in EUR"} {
		if len(missing) != 0 || !strings.Contains(body, marker) {
			t.Fatalf("typed source metadata lost %q: %s missing=%v", marker, body, missing)
		}
	}
	parts, missing = scopedSummaryParts(index, unit, 280)
	if len(missing) != 2 {
		t.Fatalf("oversized formula bundles were not reported: %v", missing)
	}
	for _, part := range parts {
		if strings.Contains(part, "SUM(A1:A3)") || strings.Contains(part, "A1+A2") {
			t.Fatal("typed bundle split under budget")
		}
	}
}
