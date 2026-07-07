package store

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// MemoryStore implements DocumentStore so knowledge indexing and document
// search work on the memory and file backends, not only on postgres.

func (s *MemoryStore) ReplaceDocumentChunks(root string, documents []app.Document, chunks []app.DocumentChunk) (app.DocumentIndexSummary, error) {
	now := time.Now().UTC()
	source := "workspace"
	if len(documents) > 0 && strings.TrimSpace(documents[0].Source) != "" {
		source = documents[0].Source
	}

	s.mu.Lock()
	for id, doc := range s.documents {
		if doc.Source == source && doc.Root == root {
			delete(s.documents, id)
		}
	}
	for id, chunk := range s.documentChunks {
		if chunk.Source == source && chunk.Root == root {
			delete(s.documentChunks, id)
		}
	}
	for _, doc := range documents {
		if doc.IndexedAt.IsZero() {
			doc.IndexedAt = now
		}
		s.documents[doc.ID] = doc
	}
	embeddingModel := ""
	embeddingDim := 0
	vectorEnabled := false
	for _, chunk := range chunks {
		if chunk.IndexedAt.IsZero() {
			chunk.IndexedAt = now
		}
		chunk.EmbeddingDim = len(chunk.Embedding)
		if chunk.EmbeddingDim > 0 && !vectorEnabled {
			vectorEnabled = true
			embeddingModel = chunk.EmbeddingModel
			embeddingDim = chunk.EmbeddingDim
		}
		s.documentChunks[chunk.ID] = chunk
	}
	s.appendEventLocked("documents.indexed", "", "", map[string]any{
		"root":            root,
		"documents":       len(documents),
		"chunks":          len(chunks),
		"vector_enabled":  vectorEnabled,
		"embedding_model": embeddingModel,
		"embedding_dim":   embeddingDim,
	})
	s.mu.Unlock()

	s.AddAudit(app.AuditEvent{
		Actor:   "toolhub",
		Type:    "documents.indexed",
		Summary: "Workspace knowledge indexed",
		Fields: map[string]any{
			"root":            root,
			"documents":       len(documents),
			"chunks":          len(chunks),
			"vector_enabled":  vectorEnabled,
			"embedding_model": embeddingModel,
			"embedding_dim":   embeddingDim,
		},
	})

	return app.DocumentIndexSummary{
		Backend:        "memory",
		Root:           root,
		Documents:      len(documents),
		Chunks:         len(chunks),
		VectorEnabled:  vectorEnabled,
		EmbeddingModel: embeddingModel,
		EmbeddingDim:   embeddingDim,
		IndexedAt:      now,
	}, nil
}

func (s *MemoryStore) SearchDocumentChunks(query string, embedding []float32, embeddingModel string, maxResults int) ([]app.DocumentChunkHit, error) {
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 8
	}
	embeddingModel = strings.TrimSpace(embeddingModel)
	embeddingDim := len(embedding)

	s.mu.RLock()
	candidates := make([]dbDocumentChunk, 0, len(s.documentChunks))
	for _, chunk := range s.documentChunks {
		if embeddingModel != "" && chunk.EmbeddingModel != "" && chunk.EmbeddingModel != embeddingModel {
			continue
		}
		if embeddingDim != 0 && chunk.EmbeddingDim != 0 && chunk.EmbeddingDim != embeddingDim {
			continue
		}
		candidates = append(candidates, dbDocumentChunk{
			ID:             chunk.ID,
			Path:           chunk.Path,
			RelPath:        chunk.RelPath,
			StartLine:      chunk.StartLine,
			EndLine:        chunk.EndLine,
			Text:           chunk.Text,
			Terms:          chunk.Terms,
			Embedding:      chunk.Embedding,
			EmbeddingModel: chunk.EmbeddingModel,
			EmbeddingDim:   chunk.EmbeddingDim,
			Backend:        "memory",
		})
	}
	s.mu.RUnlock()

	return rankDocumentChunks(query, embedding, candidates, maxResults), nil
}

func (s *FileStore) ReplaceDocumentChunks(root string, documents []app.Document, chunks []app.DocumentChunk) (app.DocumentIndexSummary, error) {
	summary, err := s.inner.ReplaceDocumentChunks(root, documents, chunks)
	if err != nil {
		return summary, err
	}
	summary.Backend = "file"
	s.persist()
	return summary, nil
}

func (s *FileStore) SearchDocumentChunks(query string, embedding []float32, embeddingModel string, maxResults int) ([]app.DocumentChunkHit, error) {
	hits, err := s.inner.SearchDocumentChunks(query, embedding, embeddingModel, maxResults)
	for i := range hits {
		hits[i].Backend = "file"
	}
	return hits, err
}
