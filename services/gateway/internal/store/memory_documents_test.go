package store

import (
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func sampleDocumentIndex() ([]app.Document, []app.DocumentChunk) {
	docs := []app.Document{{
		ID:      "doc_1",
		Source:  "workspace",
		Root:    "/ws",
		Path:    "/ws/notes/roadmap.md",
		RelPath: "notes/roadmap.md",
		Bytes:   128,
	}}
	chunks := []app.DocumentChunk{
		{
			ID:         "chunk_1",
			DocumentID: "doc_1",
			Source:     "workspace",
			Root:       "/ws",
			Path:       "/ws/notes/roadmap.md",
			RelPath:    "notes/roadmap.md",
			StartLine:  1,
			EndLine:    10,
			Text:       "The delivery roadmap prioritizes reminder scheduling for Q3.",
			Terms:      []string{"delivery", "roadmap", "reminder", "scheduling"},
		},
		{
			ID:         "chunk_2",
			DocumentID: "doc_1",
			Source:     "workspace",
			Root:       "/ws",
			Path:       "/ws/notes/roadmap.md",
			RelPath:    "notes/roadmap.md",
			StartLine:  11,
			EndLine:    20,
			Text:       "Unrelated appendix about office furniture.",
			Terms:      []string{"appendix", "office", "furniture"},
		},
	}
	return docs, chunks
}

func TestMemoryStoreImplementsDocumentStore(t *testing.T) {
	var _ DocumentStore = NewMemoryStore()
	var _ DocumentStore = &FileStore{}
}

func TestMemoryStoreDocumentIndexAndSearch(t *testing.T) {
	st := NewMemoryStore()
	docs, chunks := sampleDocumentIndex()
	summary, err := st.ReplaceDocumentChunks("/ws", docs, chunks)
	if err != nil {
		t.Fatalf("ReplaceDocumentChunks: %v", err)
	}
	if summary.Documents != 1 || summary.Chunks != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	hits, err := st.SearchDocumentChunks("reminder roadmap", nil, "", 5)
	if err != nil {
		t.Fatalf("SearchDocumentChunks: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit for indexed content")
	}
	if hits[0].RelPath != "notes/roadmap.md" || hits[0].StartLine != 1 {
		t.Fatalf("unexpected top hit: %+v", hits[0])
	}

	// Re-indexing the same root replaces prior chunks instead of accumulating.
	if _, err := st.ReplaceDocumentChunks("/ws", docs, chunks[:1]); err != nil {
		t.Fatalf("ReplaceDocumentChunks (reindex): %v", err)
	}
	hits, err = st.SearchDocumentChunks("furniture", nil, "", 5)
	if err != nil {
		t.Fatalf("SearchDocumentChunks after reindex: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected replaced chunk to be gone, got %+v", hits)
	}
}

func TestFileStoreDocumentIndexSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	docs, chunks := sampleDocumentIndex()
	if _, err := st.ReplaceDocumentChunks("/ws", docs, chunks); err != nil {
		t.Fatalf("ReplaceDocumentChunks: %v", err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore (reopen): %v", err)
	}
	hits, err := reopened.SearchDocumentChunks("reminder scheduling", nil, "", 5)
	if err != nil {
		t.Fatalf("SearchDocumentChunks: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected persisted document chunks to be searchable after restart")
	}
	if hits[0].Backend != "file" {
		t.Fatalf("expected file backend marker, got %q", hits[0].Backend)
	}
}
