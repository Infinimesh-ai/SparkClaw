package toolhub

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
)

func (h *ToolHub) confirmWorkspaceDataAccess(_ context.Context, args map[string]any) (Result, error) {
	return Result{Output: map[string]any{
		"status": "approval_contract_confirmed", "request_digest": strings.TrimSpace(fmt.Sprint(args["request_digest"])),
	}}, nil
}

func (h *ToolHub) filesSearch(ctx context.Context, args map[string]any) (Result, error) {
	query := strings.TrimSpace(stringArg(args, "query", ""))
	if query == "" {
		return Result{}, errors.New("query cannot be empty")
	}
	root, err := h.resolveRoot(stringArg(args, "root", ""))
	if err != nil {
		return Result{}, err
	}
	maxResults := intArg(args, "max_results", 20)
	if maxResults <= 0 || maxResults > 100 {
		maxResults = 20
	}
	search, err := workspacefiles.Search(ctx, root, workspacefiles.SearchRequest{
		Mode: workspacefiles.MatchFuzzy, Term: query, MaxResults: maxResults,
		MaxEntries: 20000, MaxDepth: 32, Timeout: 3 * time.Second,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"query": query, "results": search.Matches, "count": len(search.Matches), "complete": search.Complete, "truncated": search.Truncated,
	}}, nil
}

func (h *ToolHub) filesRead(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	maxBytes := intArg(args, "max_bytes", document.SmallExtractedMaxBytes)
	if maxBytes <= 0 || maxBytes > document.SmallExtractedMaxBytes {
		maxBytes = document.SmallExtractedMaxBytes
	}
	read, err := h.readDocumentWorkflow(withDocumentOCRExecution(ctx, sessionID, runID), path, maxBytes, document.EnrichmentOptions{
		ImageAnalysis: stringArg(args, "image_analysis", "targeted"),
		TargetPaths:   outputStringArray(args["image_target_paths"]),
		Question:      stringArg(args, "image_question", ""),
		Required:      boolArg(args, "image_required", false),
	})
	if err != nil {
		return Result{}, err
	}
	output, err := documentReadOutput(read, maxBytes)
	return Result{Output: output}, err
}

func textDocumentReadEnvelope(content string, truncated bool, maxBytes int) map[string]any {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if normalized == "" {
		lines = []string{}
	}
	blocks := []map[string]any{}
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lineNumber := index + 1
		blocks = append(blocks, map[string]any{
			"text": line,
			"location": map[string]any{
				"part":        "document",
				"block_type":  "line",
				"block_index": len(blocks) + 1,
				"line_start":  lineNumber,
				"line_end":    lineNumber,
				"path":        fmt.Sprintf("document.line[%d]", lineNumber),
			},
		})
	}
	evidenceBlocks := evidenceBlocksFromDocumentBlocks("", "text", blocks)
	mode := "full"
	reason := "default_full_read"
	if truncated {
		mode = "byte_limited"
		reason = "max_bytes_exceeded"
	}
	newlineStyle := "lf"
	if strings.Contains(content, "\r\n") {
		newlineStyle = "crlf"
	} else if strings.Contains(content, "\r") {
		newlineStyle = "cr"
	}
	return map[string]any{
		"schema_version": "document_read_v1",
		"format":         "text",
		"source":         "plain_text",
		"content_scope":  map[string]any{"kind": "full_document", "complete": !truncated},
		"strategy": map[string]any{
			"mode":       mode,
			"reason":     reason,
			"complete":   !truncated,
			"max_bytes":  maxBytes,
			"extensible": true,
		},
		"blocks":          blocks,
		"evidence_blocks": evidenceBlocks,
		"enrichment": map[string]any{
			"schema_version": document.EnrichmentSchemaVersion,
			"assets":         map[string]any{"images": []any{}, "charts": []any{}, "embedded_objects": []any{}},
			"annotations":    map[string]any{"comments": []any{}, "notes": []any{}, "hyperlinks": []any{}},
			"layout": map[string]any{
				"sections": []any{}, "page_settings": []any{map[string]any{
					"encoding": "utf-8", "bom": strings.HasPrefix(content, "\ufeff"), "newline_style": newlineStyle,
				}}, "slide_layouts": []any{}, "merged_ranges": []any{},
			},
			"extensions": map[string]any{"status": "deferred", "parts": []any{}},
			"coverage":   map[string]any{"content": "complete", "assets": "complete", "annotations": "complete", "layout": "complete", "extensions": "deferred"},
		},
		"stats": map[string]any{
			"blocks":   len(blocks),
			"complete": !truncated,
		},
	}
}

func attachEvidenceBlocks(document map[string]any, documentID, fileType string) {
	if document == nil {
		return
	}
	blocks := documentAnySlice(document["blocks"])
	if len(blocks) == 0 {
		return
	}
	document["evidence_blocks"] = evidenceBlocksFromAnyBlocks(documentID, fileType, blocks)
}

func evidenceBlocksFromDocumentBlocks(documentID, fileType string, blocks []map[string]any) []map[string]any {
	raw := make([]any, 0, len(blocks))
	for _, block := range blocks {
		raw = append(raw, block)
	}
	return evidenceBlocksFromAnyBlocks(documentID, fileType, raw)
}

func evidenceBlocksFromAnyBlocks(documentID, fileType string, blocks []any) []map[string]any {
	out := []map[string]any{}
	headingPath := []string{}
	for i, item := range blocks {
		block, ok := documentAnyMap(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(documentStringValue(block["text"]))
		if text == "" || text == "<nil>" {
			continue
		}
		location, _ := documentAnyMap(block["location"])
		blockType := evidenceBlockType(fileType, block, location, text)
		if blockType == "heading" {
			headingPath = appendHeadingPath(headingPath, text)
		}
		blockID := evidenceBlockID(location, i+1)
		normalizedLocation := evidenceBlockLocation(location, headingPath)
		evidence := map[string]any{
			"blockId":    blockID,
			"documentId": documentID,
			"fileType":   fileType,
			"type":       blockType,
			"text":       text,
			"location":   normalizedLocation,
			"sourceHash": sourceHash(text),
		}
		out = append(out, evidence)
	}
	return out
}

func evidenceBlockType(fileType string, block map[string]any, location map[string]any, text string) string {
	if blockType := strings.TrimSpace(documentStringValue(location["block_type"])); blockType == "table_cell" {
		return "table_cell"
	}
	style := strings.ToLower(strings.TrimSpace(documentStringValue(block["style"])))
	if strings.Contains(style, "heading") || looksDocumentHeading(text) {
		return "heading"
	}
	switch fileType {
	case "pdf":
		return "pdf_text"
	case "pptx":
		return "slide_text"
	default:
		return "paragraph"
	}
}

func looksDocumentHeading(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 40 {
		return false
	}
	prefixes := []string{"一、", "二、", "三、", "四、", "五、", "六、", "七、", "八、", "九、", "十、"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func appendHeadingPath(path []string, heading string) []string {
	next := append([]string(nil), path...)
	if len(next) == 0 {
		return []string{heading}
	}
	next[len(next)-1] = heading
	return next
}

func evidenceBlockID(location map[string]any, fallback int) string {
	if path := strings.TrimSpace(documentStringValue(location["path"])); path != "" && path != "<nil>" {
		return path
	}
	return fmt.Sprintf("blk_%d", fallback)
}

func evidenceBlockLocation(location map[string]any, headingPath []string) map[string]any {
	out := map[string]any{}
	if value := documentIntValue(location["page_number"]); value > 0 {
		out["pageNumber"] = value
	}
	if value := documentIntValue(location["paragraph_index"]); value > 0 {
		out["paragraphIndex"] = value
	}
	if value := documentIntValue(location["table_index"]); value > 0 {
		out["tableId"] = fmt.Sprintf("table_%d", value)
	}
	if value := documentIntValue(location["row_index"]); value > 0 {
		out["rowIndex"] = value
	}
	if value := documentIntValue(location["cell_index"]); value > 0 {
		out["columnIndex"] = value
	}
	if value := documentIntValue(location["slide_number"]); value > 0 {
		out["slideNumber"] = value
	}
	if len(headingPath) > 0 {
		out["headingPath"] = append([]string(nil), headingPath...)
		out["sectionPath"] = append([]string(nil), headingPath...)
	}
	if path := strings.TrimSpace(documentStringValue(location["path"])); path != "" && path != "<nil>" {
		out["path"] = path
	}
	return out
}

func sourceHash(text string) string {
	sum := sha1.Sum([]byte(text))
	return "sha1:" + hex.EncodeToString(sum[:])
}

func (h *ToolHub) filesWriteDraft(ctx context.Context, args map[string]any) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	pathArg := stringArg(args, "path", "")
	if pathArg == "" {
		pathArg = filepath.Join(".sparkclaw", "drafts", "draft-"+strconv.FormatInt(time.Now().Unix(), 10)+".md")
	}
	path, err := h.resolveDraftPath(pathArg)
	if err != nil {
		return Result{}, err
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{"path": path, "bytes": len(content), "status": "draft_written"}}, nil
}

func (h *ToolHub) resolveRoot(root string) (string, error) {
	if root == "" {
		root = h.cfg.Workspaces.DefaultRoot
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !h.allowed(abs) {
		return "", fmt.Errorf("path %s is outside workspace allowlist", abs)
	}
	return abs, nil
}

func (h *ToolHub) resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	path = h.normalizePossiblyMissingRootSlash(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.cfg.Workspaces.DefaultRoot, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !h.allowed(abs) {
		return "", fmt.Errorf("path %s is outside workspace allowlist", abs)
	}
	return abs, nil
}

func (h *ToolHub) normalizePossiblyMissingRootSlash(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if filepath.IsAbs(clean) {
		return path
	}
	candidate := string(os.PathSeparator) + clean
	abs, err := filepath.Abs(candidate)
	if err != nil || !h.allowed(abs) {
		return path
	}
	return candidate
}

func (h *ToolHub) resolveDraftPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return h.resolvePath(path)
	}
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, "..") {
		return "", errors.New("draft path cannot escape workspace")
	}
	return h.resolvePath(clean)
}

func (h *ToolHub) allowed(abs string) bool {
	for _, root := range h.cfg.Workspaces.Allowlist {
		cleanRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if abs == cleanRoot || strings.HasPrefix(abs, cleanRoot+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
