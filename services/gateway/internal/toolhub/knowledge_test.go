package toolhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestKnowledgeIndexAndSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# SparkClaw\n\nApproval-first workflows keep risky actions bounded.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	hub := New(cfg, store.NewMemoryStore())

	indexed, err := hub.Execute(context.Background(), "knowledge.index_workspace", map[string]any{"chunk_size": 400}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	indexOut := indexed.Output.(map[string]any)
	if indexOut["status"] != "knowledge_index_written" {
		t.Fatalf("unexpected index output: %#v", indexOut)
	}

	searched, err := hub.Execute(context.Background(), "knowledge.search", map[string]any{"query": "approval workflows"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	searchOut := searched.Output.(map[string]any)
	if searchOut["count"] == 0 {
		t.Fatalf("expected knowledge hit: %#v", searchOut)
	}
	if searchOut["original_query"] != "approval workflows" || searchOut["rewritten_query"] != "approval workflows" {
		t.Fatalf("expected query metadata in search output: %#v", searchOut)
	}
	results := searchOut["results"].([]knowledgeHit)
	if results[0].RelPath != "notes.md" || results[0].StartLine <= 0 {
		t.Fatalf("unexpected search result: %#v", results[0])
	}
	if !strings.HasPrefix(results[0].Citation, "notes.md:L") {
		t.Fatalf("expected line citation on search result: %#v", results[0])
	}
	citations, ok := searchOut["citations"].([]string)
	if !ok || len(citations) == 0 || citations[0] != results[0].Citation {
		t.Fatalf("expected top-level citations matching result: %#v", searchOut["citations"])
	}
	evidenceContext, ok := searchOut["evidence_context"].(string)
	if !ok || !strings.Contains(evidenceContext, results[0].Citation) || !strings.Contains(evidenceContext, "Approval-first") {
		t.Fatalf("expected compressed evidence context with citation: %#v", searchOut["evidence_context"])
	}
	compression, ok := searchOut["context_compression"].(map[string]any)
	if !ok || compression["requires_citations"] != true {
		t.Fatalf("expected context compression metadata: %#v", searchOut["context_compression"])
	}
}

func TestKnowledgeSearchReranksJSONIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "alpha.md"), []byte("Approval workflows are briefly mentioned here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bravo.md"), []byte("Approval workflows need owner review, trace capture, policy checks, and bounded repair.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reranker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embeddings" {
			var body struct {
				Input []string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			data := make([]map[string]any, 0, len(body.Input))
			for i := range body.Input {
				data = append(data, map[string]any{"index": i, "embedding": []float64{1, 0, 0}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		var body struct {
			Documents []string `json:"documents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Documents) < 2 {
			t.Fatalf("expected rerank candidates, got %#v", body.Documents)
		}
		preferred := 0
		for i, doc := range body.Documents {
			if filepath.Base(strings.Split(doc, "\n")[0]) == "bravo.md" {
				preferred = i
				break
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": preferred, "relevance_score": 0.99},
			},
		})
	}))
	defer reranker.Close()

	cfg := config.Default()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = reranker.URL
	cfg.Model.Deep.BaseURL = reranker.URL
	cfg.Model.Embedding.BaseURL = reranker.URL
	cfg.Model.Reranker.BaseURL = reranker.URL
	cfg.Model.Reranker.Model = "test-reranker"
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	hub := New(cfg, store.NewMemoryStore())

	if _, err := hub.Execute(context.Background(), "knowledge.index_workspace", map[string]any{"chunk_size": 400}, "s", "run"); err != nil {
		t.Fatal(err)
	}
	searched, err := hub.Execute(context.Background(), "knowledge.search", map[string]any{"query": "approval workflows", "max_results": 1}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	searchOut := searched.Output.(map[string]any)
	if searchOut["reranker_model"] != "test-reranker" {
		t.Fatalf("reranker metadata missing: %#v", searchOut)
	}
	results := searchOut["results"].([]knowledgeHit)
	if len(results) != 1 || results[0].RelPath != "bravo.md" || results[0].RerankScore == 0 {
		t.Fatalf("expected reranked top hit from bravo.md: %#v", results)
	}
	if got, ok := searchOut["rerank_candidate_count"].(int); !ok || got < 2 {
		t.Fatalf("expected rerank candidate count before top-n trimming: %#v", searchOut["rerank_candidate_count"])
	}
}

func TestKnowledgeSearchRewritesInstructionalQuery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workflow.md"), []byte("Approval workflows keep owner review and evidence citations together.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	hub := New(cfg, store.NewMemoryStore())

	if _, err := hub.Execute(context.Background(), "knowledge.index_workspace", map[string]any{"chunk_size": 400}, "s", "run"); err != nil {
		t.Fatal(err)
	}
	searched, err := hub.Execute(context.Background(), "knowledge.search", map[string]any{
		"query":             "Search knowledge for approval workflows",
		"context_max_bytes": 96,
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	searchOut := searched.Output.(map[string]any)
	if searchOut["original_query"] != "Search knowledge for approval workflows" || searchOut["rewritten_query"] != "approval workflows" || searchOut["query"] != "approval workflows" {
		t.Fatalf("query rewrite metadata mismatch: %#v", searchOut)
	}
	if searchOut["candidate_count"] == 0 || searchOut["count"] == 0 {
		t.Fatalf("rewritten query did not retrieve evidence: %#v", searchOut)
	}
	evidenceContext := searchOut["evidence_context"].(string)
	if !strings.Contains(evidenceContext, "workflow.md:L") || len(evidenceContext) > 96 {
		t.Fatalf("evidence context should be cited and byte-bounded: %q", evidenceContext)
	}
}

func TestKnowledgeSearchRequiresIndex(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	if _, err := hub.Execute(context.Background(), "knowledge.search", map[string]any{"query": "sparkclaw"}, "s", "run"); err == nil {
		t.Fatal("expected missing index error")
	}
}

func TestKnowledgeUsesDocumentStoreWhenAvailable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# SparkClaw\n\nHybrid RAG uses pgvector evidence.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	st := newFakeDocumentStore()
	hub := New(cfg, st)

	indexed, err := hub.Execute(context.Background(), "knowledge.index_workspace", map[string]any{"chunk_size": 400}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if st.replaceCount != 1 || len(st.chunks) == 0 || len(st.chunks[0].Embedding) == 0 {
		t.Fatalf("document chunks were not persisted with embeddings: %#v", st)
	}
	indexOut := indexed.Output.(map[string]any)
	if _, ok := indexOut["document_store"]; !ok {
		t.Fatalf("index output did not include document_store summary: %#v", indexOut)
	}
	if len(st.documents) != 1 || st.documents[0].ObjectKey == "" {
		t.Fatalf("document store did not receive archived object key: %#v", st.documents)
	}
	objects := st.ListArtifactObjects(10)
	if !hasToolhubArtifactKind(objects, "knowledge_document") || !hasToolhubArtifactKind(objects, "knowledge_index") {
		t.Fatalf("knowledge artifacts were not cataloged: %#v", objects)
	}
	if _, err := os.Stat(filepath.Join(cfg.Storage.ArtifactDir, cfg.Storage.ArtifactBucket, st.documents[0].ObjectKey)); err != nil {
		t.Fatalf("archived knowledge document missing: %v", err)
	}

	searched, err := hub.Execute(context.Background(), "knowledge.search", map[string]any{"query": "pgvector evidence"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	searchOut := searched.Output.(map[string]any)
	if searchOut["backend"] != "document_store" || searchOut["index_kind"] != "hybrid_chunks" {
		t.Fatalf("search did not use document store: %#v", searchOut)
	}
	if searchOut["candidate_count"] != 1 || searchOut["rerank_candidate_count"] != 1 {
		t.Fatalf("document-store search missing candidate counts: %#v", searchOut)
	}
	results := searchOut["results"].([]app.DocumentChunkHit)
	if len(results) == 0 || !strings.HasPrefix(results[0].Citation, "notes.md:L") {
		t.Fatalf("expected document-store hit citation: %#v", results)
	}
	citations, ok := searchOut["citations"].([]string)
	if !ok || len(citations) == 0 || citations[0] != results[0].Citation {
		t.Fatalf("expected document-store top-level citations: %#v", searchOut["citations"])
	}
	if evidenceContext, ok := searchOut["evidence_context"].(string); !ok || !strings.Contains(evidenceContext, results[0].Citation) {
		t.Fatalf("expected document-store evidence context: %#v", searchOut["evidence_context"])
	}
}

type fakeDocumentStore struct {
	*store.MemoryStore
	replaceCount int
	documents    []app.Document
	chunks       []app.DocumentChunk
}

func newFakeDocumentStore() *fakeDocumentStore {
	return &fakeDocumentStore{MemoryStore: store.NewMemoryStore()}
}

func (s *fakeDocumentStore) ReplaceDocumentChunks(root string, documents []app.Document, chunks []app.DocumentChunk) (app.DocumentIndexSummary, error) {
	s.replaceCount++
	s.documents = append([]app.Document(nil), documents...)
	s.chunks = append([]app.DocumentChunk(nil), chunks...)
	return app.DocumentIndexSummary{
		Backend:        "fake_document_store",
		Root:           root,
		Documents:      len(documents),
		Chunks:         len(chunks),
		VectorEnabled:  len(chunks) > 0 && len(chunks[0].Embedding) > 0,
		EmbeddingModel: chunks[0].EmbeddingModel,
		IndexedAt:      documents[0].IndexedAt,
	}, nil
}

func (s *fakeDocumentStore) SearchDocumentChunks(query string, embedding []float32, maxResults int) ([]app.DocumentChunkHit, error) {
	if len(s.chunks) == 0 {
		return nil, nil
	}
	chunk := s.chunks[0]
	return []app.DocumentChunkHit{{
		Path:           chunk.Path,
		RelPath:        chunk.RelPath,
		StartLine:      chunk.StartLine,
		EndLine:        chunk.EndLine,
		Score:          10,
		KeywordScore:   4,
		VectorScore:    1,
		Snippet:        chunk.Text,
		Terms:          []string{"pgvector"},
		EmbeddingModel: chunk.EmbeddingModel,
		Backend:        "fake_document_store",
	}}, nil
}

func hasToolhubArtifactKind(objects []app.ArtifactObject, kind string) bool {
	for _, object := range objects {
		if object.Kind == kind {
			return true
		}
	}
	return false
}
