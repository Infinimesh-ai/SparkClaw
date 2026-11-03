package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// documentIndexProgress is shown on the running call only while a request
// waits for a document index (docs/large-document-context-design.md 4.3).
const documentIndexProgress = "正在建立文档索引"

// outlinePointerMaxBytes bounds the outline text a large-document read
// returns in place of the complete content.
const outlinePointerMaxBytes = 8000

func documentIndexFormatSupported(format string) bool {
	switch format {
	case app.DocumentFormatPDF, app.DocumentFormatDOCX, app.DocumentFormatText, app.DocumentFormatPPTX, app.DocumentFormatXLSX:
		return true
	default:
		return false
	}
}

// ensureDocumentIndex resolves a governed document and returns its index,
// building it now or waiting for a prebuild, with progress on the call.
func (h *ToolHub) ensureDocumentIndex(ctx context.Context, path string) (*docindex.Index, document.Metadata, error) {
	metadata, err := document.InspectFile(ctx, h.cfg.Workspaces.DefaultRoot, path)
	if err != nil {
		return nil, document.Metadata{}, err
	}
	if !documentIndexFormatSupported(metadata.Format) {
		return nil, document.Metadata{}, &document.PipelineError{
			Code: document.CodeFormatUnsupported, Stage: document.StageRead, Format: metadata.Format,
			Detail: "this format has no document index strategy",
		}
	}
	index, err := h.documentIndex.Ensure(ctx, docindex.Target{
		WorkspaceRoot: h.cfg.Workspaces.DefaultRoot, RelPath: metadata.Relative, Format: metadata.Format,
	}, func() { reportToolProgress(ctx, documentIndexProgress) })
	if err != nil {
		return nil, document.Metadata{}, fmt.Errorf("document index is unavailable: %w", err)
	}
	return index, metadata, nil
}

// largeDocumentRead answers a read the complete-read strategy deferred with
// the document outline and a retrieval pointer. It handles only deferred
// reads of indexable formats; every other error is returned unchanged.
func (h *ToolHub) largeDocumentRead(ctx context.Context, path string, readErr error) (map[string]any, error) {
	var pipelineErr *document.PipelineError
	if !errors.As(readErr, &pipelineErr) || pipelineErr.Code != document.CodeStrategyDeferred || !documentIndexFormatSupported(pipelineErr.Format) {
		return nil, readErr
	}
	if pipelineErr.Stage != document.StageInspect && pipelineErr.Limit < int64(h.documentExtractionLimit()) {
		// The caller asked for less than a complete read allows; a smaller
		// max_bytes keeps its complete-or-deferred contract.
		return nil, readErr
	}
	index, metadata, err := h.ensureDocumentIndex(ctx, path)
	if err != nil {
		return nil, err
	}
	return documentRetrievalPointer(h, index, metadata), nil
}

// documentRetrievalPointer is the read result of an indexed large document:
// its outline instead of its text, marked retrieval_required.
func documentRetrievalPointer(h *ToolHub, index *docindex.Index, metadata document.Metadata) map[string]any {
	outline := renderDocumentOutline(index, outlinePointerMaxBytes)
	manifest := index.Manifest
	indexStatus := app.DocumentIndexReady
	if manifest.State == docindex.StatePartial {
		indexStatus = app.DocumentIndexPartial
	}
	return map[string]any{
		"path": metadata.Path, "rel_path": metadata.Relative, "kind": metadata.Format, "already_read": true,
		"content": outline, "bytes": len(outline), "source_bytes": manifest.SourceBytes, "max_bytes": h.documentExtractionLimit(),
		"truncated": false, "untrusted": true, "retrieval_required": true, "read_complete": false, "coverage_status": "partial",
		"missing_page_indexes": append([]int{}, manifest.UnindexedPages...), "page_status_counts": map[string]any{}, "scanned_unsupported": len(manifest.UnindexedPages) > 0,
		"next_step_hint": "This document is too large to read whole. Use document.search with keywords from the request, then document.read_range on the best chunk_ids or a matching section; scanned pages in a section are recognized when it is read.",
		"document": map[string]any{
			"format": metadata.Format, "outline": documentJSONValue(documentOutlineNodes(index, 0)),
			"stats": map[string]any{
				"pages": manifest.Pages, "chunks": manifest.Chunks, "text_bytes": manifest.TextBytes,
				"read_complete": false, "coverage_status": "partial",
			},
			"pipeline": map[string]any{
				"document_id": metadata.Relative, "strategy": string(app.DocumentStrategyLargeIndexed),
				"context_mode": string(app.DocumentContextRetrieved),
				"index":        map[string]any{"index_status": string(indexStatus), "source_sha256": manifest.SourceSHA256},
			},
		},
	}
}

func (h *ToolHub) documentOutline(ctx context.Context, args map[string]any, _, _ string) (Result, error) {
	path, err := h.resolveReadPath(ctx, stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	index, metadata, err := h.ensureDocumentIndex(ctx, path)
	if err != nil {
		return Result{}, err
	}
	maxBytes := h.documentRetrievalMaxBytes(args)
	return Result{Output: map[string]any{
		"path": metadata.Relative, "pages": index.Manifest.Pages, "chunks": index.Manifest.Chunks,
		"unindexed_pages": append([]int{}, index.Manifest.UnindexedPages...), "outline": documentJSONValue(documentOutlineNodes(index, maxBytes)),
		"content": renderDocumentOutline(index, maxBytes), "untrusted": true,
	}}, nil
}

func (h *ToolHub) documentSearch(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	ctx = withDocumentOCRExecution(ctx, sessionID, runID)
	path, err := h.resolveReadPath(ctx, stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	keywords := outputStringArray(args["keywords"])
	if len(keywords) == 0 {
		return Result{}, errors.New("document.search requires at least one keyword")
	}
	index, metadata, err := h.ensureDocumentIndex(ctx, path)
	if err != nil {
		return Result{}, err
	}
	unrecognized := index.Manifest.UnindexedPages
	if len(unrecognized) > 0 && len(index.Outline) == 0 && index.Manifest.SourceBytes <= documentIndexFullOCRMaxSourceBytes {
		// A scanned file without an outline cannot be located by title, so
		// a file within the upload limit is recognized whole first.
		if index, unrecognized, err = h.recognizeDocumentPages(ctx, metadata, index, unrecognized); err != nil {
			return Result{}, err
		}
	}
	result, err := index.Search(docindex.SearchRequest{
		Keywords: keywords, TopK: intArg(args, "top_k", 8), Section: stringArg(args, "section", ""), MaxBytes: h.documentRetrievalMaxBytes(args),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"path": metadata.Relative, "keywords": keywords, "terms": result.Terms, "hits": documentJSONValue(result.Hits),
		"sections": documentJSONValue(result.Sections), "count": len(result.Hits) + len(result.Sections),
		"unrecognized_pages": append([]int{}, unrecognized...), "truncated": result.Truncated, "untrusted": true,
	}}, nil
}

func (h *ToolHub) documentReadRange(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	ctx = withDocumentOCRExecution(ctx, sessionID, runID)
	path, err := h.resolveReadPath(ctx, stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	index, metadata, err := h.ensureDocumentIndex(ctx, path)
	if err != nil {
		return Result{}, err
	}
	request := docindex.RangeRequest{
		ChunkIDs: outputStringArray(args["chunk_ids"]), PageStart: intArg(args, "page_start", 0), PageEnd: intArg(args, "page_end", 0),
		Section: stringArg(args, "section", ""), Context: intArg(args, "context", 1), MaxBytes: h.documentRetrievalMaxBytes(args),
	}
	pending, err := index.UnindexedPagesIn(request)
	if err != nil {
		return Result{}, err
	}
	// A targeted read recognizes at most one batch of the range's scanned
	// pages; the rest are reported so the answer can name them.
	unrecognized := pending[min(len(pending), documentIndexOCRPagesPerCall):]
	if batch := pending[:min(len(pending), documentIndexOCRPagesPerCall)]; len(batch) > 0 {
		var failed []int
		if index, failed, err = h.recognizeDocumentPages(ctx, metadata, index, batch); err != nil {
			return Result{}, err
		}
		unrecognized = append(failed, unrecognized...)
	}
	result, err := index.ReadRange(request)
	if err != nil && len(unrecognized) > 0 {
		return Result{}, fmt.Errorf("pages %s of this range are scanned and could not be recognized: %w", pageList(unrecognized), err)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"path": metadata.Relative, "rel_path": metadata.Relative, "kind": metadata.Format,
		"content": result.Text, "bytes": len(result.Text), "spans": documentJSONValue(result.Spans), "truncated": result.Truncated,
		"next_chunk_id": result.NextChunkID, "read_complete": false, "unrecognized_pages": append([]int{}, unrecognized...),
		"missing_page_indexes": append([]int{}, unrecognized...), "untrusted": true,
	}}, nil
}

func pageList(pages []int) string {
	parts := make([]string, 0, len(pages))
	for _, page := range pages {
		parts = append(parts, fmt.Sprint(page))
	}
	return strings.Join(parts, ", ")
}

// documentRetrievalMaxBytes bounds one retrieval result by the Fast lane's
// stage evidence budget, the lane document workflows run on.
func (h *ToolHub) documentRetrievalMaxBytes(args map[string]any) int {
	limit := h.cfg.StageEvidenceBytes(h.cfg.Model.Fast)
	if requested := intArg(args, "max_bytes", 0); requested > 0 && requested < limit {
		return requested
	}
	return limit
}

// documentOutlineNodes lists outline nodes until their JSON-ish size
// reaches maxBytes (zero means the outline pointer's own bound).
func documentOutlineNodes(index *docindex.Index, maxBytes int) []map[string]any {
	if maxBytes <= 0 {
		maxBytes = outlinePointerMaxBytes
	}
	nodes := []map[string]any{}
	used := 0
	for _, node := range index.Outline {
		used += len(node.Title) + 64
		if used > maxBytes {
			break
		}
		nodes = append(nodes, map[string]any{"id": node.ID, "title": node.Title, "level": node.Level, "page": node.Page})
	}
	return nodes
}

// renderDocumentOutline is the outline as indented text lines with node IDs
// and pages, which is how the model reads it.
func renderDocumentOutline(index *docindex.Index, maxBytes int) string {
	manifest := index.Manifest
	var out strings.Builder
	fmt.Fprintf(&out, "Document outline (large document, retrieval required): pages=%d chunks=%d", manifest.Pages, manifest.Chunks)
	if len(manifest.UnindexedPages) > 0 {
		fmt.Fprintf(&out, " unindexed_pages=%d", len(manifest.UnindexedPages))
	}
	out.WriteString("\n")
	for _, node := range index.Outline {
		line := strings.Repeat("  ", max(0, node.Level-1)) + node.ID + " " + node.Title
		if node.Page > 0 {
			line += fmt.Sprintf(" (p.%d)", node.Page)
		}
		if out.Len()+len(line)+1 > maxBytes {
			out.WriteString("...")
			break
		}
		out.WriteString(line + "\n")
	}
	if len(index.Outline) == 0 {
		out.WriteString("No headings were found; search the text directly.\n")
	}
	return strings.TrimSpace(out.String())
}

// documentJSONValue converts typed index results to the plain JSON values
// every tool output carries.
func documentJSONValue(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
