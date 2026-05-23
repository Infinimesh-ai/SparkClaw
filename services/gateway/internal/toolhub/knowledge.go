package toolhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type knowledgeIndex struct {
	Version   int              `json:"version"`
	Kind      string           `json:"kind"`
	Root      string           `json:"root"`
	BuiltAt   string           `json:"built_at"`
	ChunkSize int              `json:"chunk_size"`
	Chunks    []knowledgeChunk `json:"chunks"`
}

type knowledgeChunk struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	RelPath   string   `json:"rel_path"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	Text      string   `json:"text"`
	Terms     []string `json:"terms"`
}

type knowledgeHit struct {
	Path          string   `json:"path"`
	RelPath       string   `json:"rel_path"`
	StartLine     int      `json:"start_line"`
	EndLine       int      `json:"end_line"`
	Citation      string   `json:"citation"`
	Score         int      `json:"score"`
	RerankScore   float64  `json:"rerank_score,omitempty"`
	RerankerModel string   `json:"reranker_model,omitempty"`
	Snippet       string   `json:"snippet"`
	Terms         []string `json:"terms"`
}

type knowledgeEvidenceItem struct {
	Citation string
	Snippet  string
}

func (h *ToolHub) knowledgeIndexWorkspace(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	root, err := h.resolveRoot(stringArg(args, "root", ""))
	if err != nil {
		return Result{}, err
	}
	maxFiles := intArg(args, "max_files", 500)
	if maxFiles <= 0 || maxFiles > 5000 {
		maxFiles = 500
	}
	maxBytes := intArg(args, "max_bytes", 500000)
	if maxBytes <= 0 || maxBytes > 5_000_000 {
		maxBytes = 500000
	}
	chunkSize := intArg(args, "chunk_size", 1200)
	if chunkSize < 300 || chunkSize > 5000 {
		chunkSize = 1200
	}
	index := knowledgeIndex{
		Version:   1,
		Kind:      "keyword_chunks",
		Root:      root,
		BuiltAt:   time.Now().UTC().Format(time.RFC3339),
		ChunkSize: chunkSize,
		Chunks:    []knowledgeChunk{},
	}
	filesSeen := 0
	documents := []app.Document{}
	docChunks := []app.DocumentChunk{}
	archivedDocuments := 0
	archiveErrors := []string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := d.Name()
		if d.IsDir() && skipDir(name) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !looksText(path) {
			return nil
		}
		if filesSeen >= maxFiles {
			return errEnough
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 || len(raw) > maxBytes {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		indexedAt := time.Now().UTC()
		hash := contentHash(raw)
		documentObject, archiveErr := h.archiveKnowledgeObject(ctx, app.ArtifactObject{
			Kind:      "knowledge_document",
			RunID:     runID,
			SessionID: sessionID,
		}, knowledgeDocumentObjectKey(hash, rel), contentTypeForPath(path), raw)
		objectKey := ""
		if archiveErr != nil {
			archiveErrors = appendArchiveError(archiveErrors, rel+": "+archiveErr.Error())
		} else if documentObject != nil {
			objectKey = documentObject.Key
			archivedDocuments++
		}
		doc := app.Document{
			ID:          app.NewID("doc"),
			Source:      "workspace",
			Root:        root,
			Path:        path,
			RelPath:     rel,
			ObjectKey:   objectKey,
			ContentHash: hash,
			Bytes:       len(raw),
			IndexedAt:   indexedAt,
		}
		documents = append(documents, doc)
		chunks := chunkText(path, rel, string(raw), chunkSize)
		index.Chunks = append(index.Chunks, chunks...)
		for _, chunk := range chunks {
			docChunks = append(docChunks, app.DocumentChunk{
				ID:          app.NewID("chk"),
				DocumentID:  doc.ID,
				Source:      doc.Source,
				Root:        root,
				Path:        chunk.Path,
				RelPath:     chunk.RelPath,
				StartLine:   chunk.StartLine,
				EndLine:     chunk.EndLine,
				Text:        chunk.Text,
				Terms:       chunk.Terms,
				ContentHash: contentHash([]byte(chunk.Text)),
				IndexedAt:   indexedAt,
			})
		}
		filesSeen++
		return nil
	})
	if errors.Is(err, errEnough) {
		err = nil
	}
	if err != nil {
		return Result{}, err
	}
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return Result{}, err
	}
	indexPath, err := h.resolveDraftPath(filepath.Join(".sparkclaw", "index", "knowledge.json"))
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(indexPath, append(raw, '\n'), 0o644); err != nil {
		return Result{}, err
	}
	indexObject, indexArchiveErr := h.archiveKnowledgeObject(ctx, app.ArtifactObject{
		Kind:      "knowledge_index",
		RunID:     runID,
		SessionID: sessionID,
	}, knowledgeIndexObjectKey(runID), "application/json", append(raw, '\n'))
	if indexArchiveErr != nil {
		archiveErrors = appendArchiveError(archiveErrors, "knowledge index: "+indexArchiveErr.Error())
	}
	output := map[string]any{
		"status":     "knowledge_index_written",
		"index_kind": index.Kind,
		"path":       indexPath,
		"root":       root,
		"files":      filesSeen,
		"chunks":     len(index.Chunks),
		"built_at":   index.BuiltAt,
	}
	if archivedDocuments > 0 || indexObject != nil {
		output["artifact_archive"] = map[string]any{
			"documents": archivedDocuments,
			"index":     indexObject != nil,
		}
	}
	if indexObject != nil {
		output["index_object_key"] = indexObject.Key
		output["index_object_uri"] = indexObject.URI
	}
	if len(archiveErrors) > 0 {
		output["artifact_archive_errors"] = archiveErrors
	}
	if summary, embeddingErr, err := h.persistDocumentChunks(ctx, root, documents, docChunks, sessionID, runID); err != nil {
		output["document_store_error"] = err.Error()
	} else if summary != nil {
		output["document_store"] = summary
		if embeddingErr != nil {
			output["embedding_error"] = embeddingErr.Error()
		}
	}
	return Result{Output: output}, nil
}

func (h *ToolHub) knowledgeSearch(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	originalQuery := strings.TrimSpace(stringArg(args, "query", ""))
	if originalQuery == "" {
		return Result{}, errors.New("query cannot be empty")
	}
	index, indexPath, err := h.loadKnowledgeIndex()
	if err != nil {
		return Result{}, err
	}
	maxResults := intArg(args, "max_results", 8)
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 8
	}
	contextMaxBytes := intArg(args, "context_max_bytes", 1600)
	if contextMaxBytes <= 0 || contextMaxBytes > 8000 {
		contextMaxBytes = 1600
	}
	rewrittenQuery := originalQuery
	if boolArg(args, "rewrite_query", true) {
		rewrittenQuery = rewriteKnowledgeQuery(originalQuery)
	}
	query := rewrittenQuery
	queryTerms := uniqueTerms(query)
	if documentStore, ok := h.store.(store.DocumentStore); ok {
		var embedding []float32
		embeddingModel := ""
		embeddingErr := ""
		if result, err := h.modelEmbed(ctx, []string{query}, sessionID, runID); err == nil && len(result.Vectors) > 0 {
			embedding = result.Vectors[0]
			embeddingModel = result.Model
		} else if err != nil {
			embeddingErr = err.Error()
		}
		if hits, err := documentStore.SearchDocumentChunks(query, embedding, 50); err == nil && len(hits) > 0 {
			candidateCount := len(hits)
			hits, rerankerModel, rerankerErr := h.rerankDocumentHits(ctx, query, hits, maxResults, sessionID, runID)
			evidenceContext, contextCompression := compressEvidenceContext(documentEvidenceItems(hits), contextMaxBytes)
			output := map[string]any{
				"query":                  query,
				"original_query":         originalQuery,
				"rewritten_query":        rewrittenQuery,
				"query_terms":            queryTerms,
				"index_kind":             "hybrid_chunks",
				"index_path":             indexPath,
				"built_at":               index.BuiltAt,
				"count":                  len(hits),
				"candidate_count":        candidateCount,
				"rerank_candidate_count": candidateCount,
				"results":                hits,
				"citations":              documentHitCitations(hits),
				"backend":                "document_store",
				"evidence_context":       evidenceContext,
				"context_compression":    contextCompression,
				"embedding_model":        embeddingModel,
			}
			if embeddingErr != "" {
				output["embedding_error"] = embeddingErr
			}
			if rerankerModel != "" {
				output["reranker_model"] = rerankerModel
			}
			if rerankerErr != "" {
				output["reranker_error"] = rerankerErr
			}
			return Result{Output: output}, nil
		}
	}
	hits := []knowledgeHit{}
	for _, chunk := range index.Chunks {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		default:
		}
		score := scoreChunk(queryTerms, chunk)
		if score == 0 {
			continue
		}
		hits = append(hits, knowledgeHit{
			Path:      chunk.Path,
			RelPath:   chunk.RelPath,
			StartLine: chunk.StartLine,
			EndLine:   chunk.EndLine,
			Citation:  citationForRange(chunk.RelPath, chunk.StartLine, chunk.EndLine),
			Score:     score,
			Snippet:   snippetForTerms(chunk.Text, queryTerms),
			Terms:     intersectTerms(queryTerms, chunk.Terms),
		})
	}
	slices.SortFunc(hits, func(a, b knowledgeHit) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.RelPath, b.RelPath)
	})
	candidateLimit := maxResults * 4
	if candidateLimit < maxResults {
		candidateLimit = maxResults
	}
	if len(hits) > candidateLimit {
		hits = hits[:candidateLimit]
	}
	candidateCount := len(hits)
	rerankerModel := ""
	rerankerErr := ""
	hits, rerankerModel, rerankerErr = h.rerankKnowledgeHits(ctx, query, hits, maxResults, sessionID, runID)
	evidenceContext, contextCompression := compressEvidenceContext(knowledgeEvidenceItems(hits), contextMaxBytes)
	output := map[string]any{
		"query":                  query,
		"original_query":         originalQuery,
		"rewritten_query":        rewrittenQuery,
		"query_terms":            queryTerms,
		"index_kind":             index.Kind,
		"index_path":             indexPath,
		"built_at":               index.BuiltAt,
		"count":                  len(hits),
		"candidate_count":        candidateCount,
		"rerank_candidate_count": candidateCount,
		"results":                hits,
		"citations":              knowledgeHitCitations(hits),
		"backend":                "json_keyword",
		"evidence_context":       evidenceContext,
		"context_compression":    contextCompression,
	}
	if rerankerModel != "" {
		output["reranker_model"] = rerankerModel
	}
	if rerankerErr != "" {
		output["reranker_error"] = rerankerErr
	}
	return Result{Output: output}, nil
}

func (h *ToolHub) rerankDocumentHits(ctx context.Context, query string, hits []app.DocumentChunkHit, maxResults int, sessionID, runID string) ([]app.DocumentChunkHit, string, string) {
	if len(hits) == 0 {
		return hits, "", ""
	}
	docs := make([]string, 0, len(hits))
	for _, hit := range hits {
		docs = append(docs, hit.RelPath+"\n"+hit.Snippet)
	}
	result, err := h.modelRerank(ctx, query, docs, maxResults, sessionID, runID)
	if err != nil {
		return trimDocumentHits(hits, maxResults), "", err.Error()
	}
	out := make([]app.DocumentChunkHit, 0, len(result.Results))
	used := map[int]bool{}
	for _, item := range result.Results {
		if item.Index < 0 || item.Index >= len(hits) || used[item.Index] {
			continue
		}
		hit := hits[item.Index]
		hit.RerankScore = item.Score
		hit.RerankerModel = result.Model
		hit.Citation = citationForRange(hit.RelPath, hit.StartLine, hit.EndLine)
		out = append(out, hit)
		used[item.Index] = true
	}
	for i, hit := range hits {
		if len(out) >= maxResults {
			break
		}
		if used[i] {
			continue
		}
		out = append(out, hit)
	}
	out = trimDocumentHits(out, maxResults)
	for i := range out {
		out[i].Citation = citationForRange(out[i].RelPath, out[i].StartLine, out[i].EndLine)
	}
	return out, result.Model, ""
}

func trimDocumentHits(hits []app.DocumentChunkHit, maxResults int) []app.DocumentChunkHit {
	if maxResults > 0 && len(hits) > maxResults {
		return hits[:maxResults]
	}
	return hits
}

func (h *ToolHub) rerankKnowledgeHits(ctx context.Context, query string, hits []knowledgeHit, maxResults int, sessionID, runID string) ([]knowledgeHit, string, string) {
	if len(hits) == 0 {
		return hits, "", ""
	}
	docs := make([]string, 0, len(hits))
	for _, hit := range hits {
		docs = append(docs, hit.RelPath+"\n"+hit.Snippet)
	}
	result, err := h.modelRerank(ctx, query, docs, maxResults, sessionID, runID)
	if err != nil {
		return trimKnowledgeHits(hits, maxResults), "", err.Error()
	}
	out := make([]knowledgeHit, 0, len(result.Results))
	used := map[int]bool{}
	for _, item := range result.Results {
		if item.Index < 0 || item.Index >= len(hits) || used[item.Index] {
			continue
		}
		hit := hits[item.Index]
		hit.RerankScore = item.Score
		hit.RerankerModel = result.Model
		hit.Citation = citationForRange(hit.RelPath, hit.StartLine, hit.EndLine)
		out = append(out, hit)
		used[item.Index] = true
	}
	for i, hit := range hits {
		if len(out) >= maxResults {
			break
		}
		if used[i] {
			continue
		}
		out = append(out, hit)
	}
	out = trimKnowledgeHits(out, maxResults)
	for i := range out {
		out[i].Citation = citationForRange(out[i].RelPath, out[i].StartLine, out[i].EndLine)
	}
	return out, result.Model, ""
}

func trimKnowledgeHits(hits []knowledgeHit, maxResults int) []knowledgeHit {
	if maxResults > 0 && len(hits) > maxResults {
		return hits[:maxResults]
	}
	return hits
}

func knowledgeHitCitations(hits []knowledgeHit) []string {
	out := make([]string, 0, len(hits))
	seen := map[string]bool{}
	for _, hit := range hits {
		citation := hit.Citation
		if citation == "" {
			citation = citationForRange(hit.RelPath, hit.StartLine, hit.EndLine)
		}
		if citation == "" || seen[citation] {
			continue
		}
		seen[citation] = true
		out = append(out, citation)
	}
	return out
}

func documentHitCitations(hits []app.DocumentChunkHit) []string {
	out := make([]string, 0, len(hits))
	seen := map[string]bool{}
	for i := range hits {
		if hits[i].Citation == "" {
			hits[i].Citation = citationForRange(hits[i].RelPath, hits[i].StartLine, hits[i].EndLine)
		}
		if hits[i].Citation == "" || seen[hits[i].Citation] {
			continue
		}
		seen[hits[i].Citation] = true
		out = append(out, hits[i].Citation)
	}
	return out
}

func knowledgeEvidenceItems(hits []knowledgeHit) []knowledgeEvidenceItem {
	items := make([]knowledgeEvidenceItem, 0, len(hits))
	for _, hit := range hits {
		citation := hit.Citation
		if citation == "" {
			citation = citationForRange(hit.RelPath, hit.StartLine, hit.EndLine)
		}
		items = append(items, knowledgeEvidenceItem{
			Citation: citation,
			Snippet:  hit.Snippet,
		})
	}
	return items
}

func documentEvidenceItems(hits []app.DocumentChunkHit) []knowledgeEvidenceItem {
	items := make([]knowledgeEvidenceItem, 0, len(hits))
	for _, hit := range hits {
		citation := hit.Citation
		if citation == "" {
			citation = citationForRange(hit.RelPath, hit.StartLine, hit.EndLine)
		}
		items = append(items, knowledgeEvidenceItem{
			Citation: citation,
			Snippet:  hit.Snippet,
		})
	}
	return items
}

func compressEvidenceContext(items []knowledgeEvidenceItem, maxBytes int) (string, map[string]any) {
	if maxBytes <= 0 {
		maxBytes = 1600
	}
	lines := []string{}
	usedBytes := 0
	truncated := false
	for i, item := range items {
		citation := strings.TrimSpace(item.Citation)
		snippet := compactWhitespace(item.Snippet)
		if citation == "" || snippet == "" {
			continue
		}
		prefix := "[" + strconv.Itoa(i+1) + "] " + citation + ": "
		available := maxBytes - usedBytes - len(prefix)
		if len(lines) > 0 {
			available--
		}
		if available <= 0 {
			truncated = true
			break
		}
		if len(snippet) > available {
			snippet = strings.TrimSpace(snippet[:available])
			truncated = true
		}
		line := prefix + snippet
		lines = append(lines, line)
		usedBytes += len(line)
		if len(lines) > 1 {
			usedBytes++
		}
		if truncated {
			break
		}
	}
	context := strings.Join(lines, "\n")
	return context, map[string]any{
		"strategy":            "top_evidence_citations",
		"max_bytes":           maxBytes,
		"input_items":         len(items),
		"included_items":      len(lines),
		"output_bytes":        len(context),
		"truncated":           truncated || len(lines) < len(items),
		"requires_citations":  true,
		"suggest_memory_tool": "memory.write_candidate",
	}
}

func citationForRange(relPath string, startLine, endLine int) string {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return ""
	}
	if startLine <= 0 {
		return relPath
	}
	if endLine <= 0 || endLine < startLine || endLine == startLine {
		return relPath + ":L" + strconv.Itoa(startLine)
	}
	return relPath + ":L" + strconv.Itoa(startLine) + "-L" + strconv.Itoa(endLine)
}

func (h *ToolHub) persistDocumentChunks(ctx context.Context, root string, documents []app.Document, chunks []app.DocumentChunk, sessionID, runID string) (*app.DocumentIndexSummary, error, error) {
	documentStore, ok := h.store.(store.DocumentStore)
	if !ok {
		return nil, nil, nil
	}
	embeddingErr := h.embedDocumentChunks(ctx, chunks, sessionID, runID)
	summary, err := documentStore.ReplaceDocumentChunks(root, documents, chunks)
	if err != nil {
		return nil, embeddingErr, err
	}
	return &summary, embeddingErr, nil
}

func (h *ToolHub) archiveKnowledgeObject(ctx context.Context, meta app.ArtifactObject, key, contentType string, raw []byte) (*artifact.Object, error) {
	if h.artifacts == nil {
		return nil, nil
	}
	object, err := h.artifacts.Put(ctx, key, contentType, raw)
	if err != nil {
		return nil, err
	}
	h.store.SaveArtifactObject(app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        meta.Kind,
		RunID:       meta.RunID,
		SessionID:   meta.SessionID,
		Backend:     object.Backend,
		Bucket:      object.Bucket,
		Key:         object.Key,
		URI:         object.URI,
		Path:        object.Path,
		ContentType: object.ContentType,
		Bytes:       object.Bytes,
		CreatedAt:   time.Now().UTC(),
	})
	return &object, nil
}

func knowledgeDocumentObjectKey(contentHash, relPath string) string {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	relPath = strings.TrimPrefix(relPath, "/")
	for strings.HasPrefix(relPath, "../") {
		relPath = strings.TrimPrefix(relPath, "../")
	}
	if relPath == "." || relPath == "" {
		relPath = "document"
	}
	return filepath.ToSlash(filepath.Join("knowledge", "documents", contentHash, relPath))
}

func knowledgeIndexObjectKey(runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		runID = app.NewID("run")
	}
	return filepath.ToSlash(filepath.Join("knowledge", "indexes", runID+".json"))
}

func contentTypeForPath(path string) string {
	if contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); contentType != "" {
		if strings.HasPrefix(contentType, "text/") && !strings.Contains(strings.ToLower(contentType), "charset=") {
			return contentType + "; charset=utf-8"
		}
		return contentType
	}
	return "text/plain; charset=utf-8"
}

func appendArchiveError(errors []string, message string) []string {
	if len(errors) >= 5 {
		return errors
	}
	return append(errors, message)
}

func (h *ToolHub) embedDocumentChunks(ctx context.Context, chunks []app.DocumentChunk, sessionID, runID string) error {
	if len(chunks) == 0 {
		return nil
	}
	const batchSize = 32
	model := ""
	for start := 0; start < len(chunks); start += batchSize {
		end := start + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		inputs := make([]string, 0, end-start)
		for _, chunk := range chunks[start:end] {
			inputs = append(inputs, chunk.RelPath+"\n"+chunk.Text)
		}
		result, err := h.modelEmbed(ctx, inputs, sessionID, runID)
		if err != nil {
			return err
		}
		if len(result.Vectors) != len(inputs) {
			return errors.New("embedding response length did not match chunk batch")
		}
		model = result.Model
		for i, vector := range result.Vectors {
			chunks[start+i].Embedding = vector
			chunks[start+i].EmbeddingModel = model
		}
	}
	return nil
}

func (h *ToolHub) modelEmbed(ctx context.Context, inputs []string, sessionID, runID string) (modelrouter.EmbeddingResult, error) {
	started := time.Now().UTC()
	result, err := h.models.Embed(ctx, inputs)
	completed := time.Now().UTC()
	h.store.SaveModelCall(modelCallFromEmbedding(sessionID, runID, result, err, started, completed))
	return result, err
}

func (h *ToolHub) modelRerank(ctx context.Context, query string, docs []string, maxResults int, sessionID, runID string) (modelrouter.RerankResult, error) {
	started := time.Now().UTC()
	result, err := h.models.Rerank(ctx, query, docs, maxResults)
	completed := time.Now().UTC()
	h.store.SaveModelCall(modelCallFromRerank(sessionID, runID, result, err, started, completed))
	return result, err
}

func modelCallFromEmbedding(sessionID, runID string, result modelrouter.EmbeddingResult, err error, started, completed time.Time) app.ModelCall {
	status := "completed"
	errorText := ""
	if err != nil {
		status = "failed"
		errorText = err.Error()
	}
	if result.Lane == "" {
		result.Lane = "embedding"
	}
	if result.Profile == "" {
		result.Profile = "unknown"
	}
	if result.Model == "" {
		result.Model = "unknown"
	}
	return app.ModelCall{
		ID:           app.NewID("mcall"),
		SessionID:    sessionID,
		RunID:        runID,
		Lane:         result.Lane,
		Profile:      result.Profile,
		Model:        result.Model,
		Operation:    "embedding",
		Mock:         result.Mock,
		Status:       status,
		PromptTokens: result.PromptTokens,
		TotalTokens:  result.TotalTokens,
		LatencyMS:    completed.Sub(started).Milliseconds(),
		Error:        errorText,
		StartedAt:    started,
		CompletedAt:  &completed,
	}
}

func modelCallFromRerank(sessionID, runID string, result modelrouter.RerankResult, err error, started, completed time.Time) app.ModelCall {
	status := "completed"
	errorText := ""
	if err != nil {
		status = "failed"
		errorText = err.Error()
	}
	if result.Lane == "" {
		result.Lane = "reranker"
	}
	if result.Profile == "" {
		result.Profile = "unknown"
	}
	if result.Model == "" {
		result.Model = "unknown"
	}
	return app.ModelCall{
		ID:           app.NewID("mcall"),
		SessionID:    sessionID,
		RunID:        runID,
		Lane:         result.Lane,
		Profile:      result.Profile,
		Model:        result.Model,
		Operation:    "rerank",
		Mock:         result.Mock,
		Status:       status,
		PromptTokens: result.PromptTokens,
		TotalTokens:  result.TotalTokens,
		LatencyMS:    completed.Sub(started).Milliseconds(),
		Error:        errorText,
		StartedAt:    started,
		CompletedAt:  &completed,
	}
}

func (h *ToolHub) loadKnowledgeIndex() (knowledgeIndex, string, error) {
	path, err := h.resolvePath(filepath.Join(".sparkclaw", "index", "knowledge.json"))
	if err != nil {
		return knowledgeIndex{}, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return knowledgeIndex{}, "", err
	}
	var index knowledgeIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		return knowledgeIndex{}, "", err
	}
	return index, path, nil
}

func chunkText(path, rel, text string, chunkSize int) []knowledgeChunk {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	chunks := []knowledgeChunk{}
	current := strings.Builder{}
	startLine := 1
	for i, line := range lines {
		if current.Len() > 0 && current.Len()+len(line)+1 > chunkSize {
			chunks = append(chunks, makeKnowledgeChunk(path, rel, len(chunks), startLine, i, current.String()))
			current.Reset()
			startLine = i + 1
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}
	if strings.TrimSpace(current.String()) != "" {
		chunks = append(chunks, makeKnowledgeChunk(path, rel, len(chunks), startLine, len(lines), current.String()))
	}
	return chunks
}

func makeKnowledgeChunk(path, rel string, idx, startLine, endLine int, text string) knowledgeChunk {
	return knowledgeChunk{
		ID:        rel + "#" + strconv.Itoa(idx),
		Path:      path,
		RelPath:   rel,
		StartLine: startLine,
		EndLine:   endLine,
		Text:      strings.TrimSpace(text),
		Terms:     uniqueTerms(rel + " " + text),
	}
}

func contentHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func uniqueTerms(text string) []string {
	seen := map[string]bool{}
	terms := []string{}
	for _, term := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(term) < 2 || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func rewriteKnowledgeQuery(query string) string {
	query = compactWhitespace(query)
	if query == "" {
		return query
	}
	lower := strings.ToLower(query)
	for _, prefix := range []string{
		"search knowledge for",
		"search the knowledge base for",
		"search knowledge",
		"knowledge search for",
		"find knowledge about",
		"query knowledge for",
		"检索知识库",
		"搜索知识库",
		"查询知识库",
		"查找知识库",
		"知识库检索",
		"知识库搜索",
	} {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			candidate := strings.TrimSpace(query[len(prefix):])
			if candidate != "" {
				return strings.Trim(candidate, " :：,，.?？!！")
			}
		}
	}
	terms := uniqueTerms(query)
	if len(terms) == 0 || len(terms) > 12 {
		return query
	}
	stopWords := map[string]bool{
		"search": true, "knowledge": true, "for": true, "find": true, "query": true,
		"about": true, "please": true, "the": true, "this": true, "workspace": true,
	}
	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		if !stopWords[term] {
			filtered = append(filtered, term)
		}
	}
	if len(filtered) == 0 {
		return query
	}
	return strings.Join(filtered, " ")
}

func scoreChunk(queryTerms []string, chunk knowledgeChunk) int {
	score := 0
	text := strings.ToLower(chunk.Text)
	rel := strings.ToLower(chunk.RelPath)
	for _, term := range queryTerms {
		if slices.Contains(chunk.Terms, term) {
			score += 3
		}
		if strings.Contains(rel, term) {
			score += 2
		}
		if strings.Contains(text, term) {
			score++
		}
	}
	return score
}

func intersectTerms(queryTerms, chunkTerms []string) []string {
	out := []string{}
	for _, term := range queryTerms {
		if slices.Contains(chunkTerms, term) {
			out = append(out, term)
		}
	}
	return out
}

func snippetForTerms(text string, terms []string) string {
	lower := strings.ToLower(text)
	idx := -1
	queryLen := 0
	for _, term := range terms {
		if found := strings.Index(lower, term); found >= 0 && (idx < 0 || found < idx) {
			idx = found
			queryLen = len(term)
		}
	}
	if idx < 0 {
		return previewText(text, 240)
	}
	return preview(text, idx, queryLen)
}
