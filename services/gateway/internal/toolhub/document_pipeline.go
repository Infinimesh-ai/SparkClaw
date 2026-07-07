package toolhub

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func attachSmallDocumentPipeline(document map[string]any, relPath, format, content string, truncated bool, maxBytes int) {
	if document == nil {
		return
	}
	tokenEstimate := estimateDocumentTokens(content)
	profile := map[string]any{
		"page_count":        intLikeDocumentStat(document, "pages", 0),
		"char_count":        utf8.RuneCountInString(content),
		"token_estimate":    tokenEstimate,
		"language":          detectDocumentLanguage(content),
		"has_tables":        len(documentAnySlice(document["tables"])) > 0,
		"has_images":        false,
		"is_scanned":        false,
		"structure_quality": structureQualityForDocument(document, format),
		"complexity":        complexityForTokenEstimate(tokenEstimate),
	}
	strategyReason := "document fits current full-read path"
	if truncated {
		strategyReason = "document content exceeded max_bytes; full answer requires a larger read budget or future range strategy"
	}
	strategy := map[string]any{
		"strategy":     string(app.DocumentStrategySmallDirect),
		"context_mode": string(app.DocumentContextFullText),
		"reason":       strategyReason,
		"limits": map[string]any{
			"max_input_tokens": tokenEstimate,
			"max_chunks":       0,
			"max_latency_ms":   5000,
		},
	}
	index := map[string]any{
		"document_id":  relPath,
		"index_status": string(app.DocumentIndexSkipped),
		"indexes": map[string]any{
			"vector_index_id":  "",
			"keyword_index_id": "",
			"summary_index_id": "",
		},
		"reason": "small_direct uses full_text context without retrieval index",
	}
	context := map[string]any{
		"mode":           string(app.DocumentContextFullText),
		"items":          smallDocumentContextItems(relPath, format, content),
		"citations":      smallDocumentCitations(document),
		"token_estimate": tokenEstimate,
		"warnings":       smallDocumentWarnings(truncated),
	}
	telemetry := map[string]any{
		"document_id":    relPath,
		"strategy":       string(app.DocumentStrategySmallDirect),
		"file_type":      format,
		"page_count":     profile["page_count"],
		"token_estimate": tokenEstimate,
		"fallback_used":  false,
	}
	document["pipeline"] = map[string]any{
		"document_id":    relPath,
		"status":         pipelineStatusForTruncation(truncated),
		"profile":        profile,
		"strategy":       strategy,
		"index":          index,
		"context_bundle": context,
		"telemetry":      telemetry,
	}
}

func estimateDocumentTokens(content string) int {
	content = strings.TrimSpace(content)
	if content == "" {
		return 0
	}
	runes := utf8.RuneCountInString(content)
	estimate := runes / 3
	if estimate <= 0 {
		estimate = 1
	}
	return estimate
}

func detectDocumentLanguage(content string) string {
	for _, r := range content {
		if r >= '\u4e00' && r <= '\u9fff' {
			return "zh"
		}
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return "unknown"
}

func intLikeDocumentStat(document map[string]any, key string, fallback int) int {
	stats, _ := documentAnyMap(document["stats"])
	if value := documentIntValue(stats[key]); value > 0 {
		return value
	}
	if values := documentAnySlice(document[key]); len(values) > 0 {
		return len(values)
	}
	return fallback
}

func structureQualityForDocument(document map[string]any, format string) string {
	if len(documentAnySlice(document["blocks"])) > 0 || len(documentAnySlice(document["paragraphs"])) > 0 || len(documentAnySlice(document["pages"])) > 0 {
		return "good"
	}
	if format == "text" {
		return "plain_text"
	}
	return "unknown"
}

func complexityForTokenEstimate(tokens int) string {
	switch {
	case tokens <= 4000:
		return "low"
	case tokens <= 16000:
		return "medium"
	default:
		return "high"
	}
}

func pipelineStatusForTruncation(truncated bool) string {
	if truncated {
		return string(app.DocumentProcessingPartial)
	}
	return string(app.DocumentProcessingSucceeded)
}

func smallDocumentContextItems(relPath, format, content string) []map[string]any {
	items := []map[string]any{{
		"type": "document_metadata",
		"text": "document=" + relPath + " format=" + format,
	}}
	if strings.TrimSpace(content) != "" {
		items = append(items, map[string]any{
			"type":  "document_chunk",
			"text":  content,
			"score": 1,
		})
	}
	return items
}

func smallDocumentCitations(document map[string]any) []map[string]any {
	blocks := documentAnySlice(document["blocks"])
	out := []map[string]any{}
	for _, item := range blocks {
		block, ok := documentAnyMap(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(documentStringValue(block["text"]))
		if text == "" || text == "<nil>" {
			continue
		}
		location, _ := documentAnyMap(block["location"])
		out = append(out, map[string]any{
			"block_id": blockIDFromLocation(location, len(out)+1),
			"quote":    trimDocumentText(text, 180),
		})
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func blockIDFromLocation(location map[string]any, fallback int) string {
	if path := strings.TrimSpace(documentStringValue(location["path"])); path != "" && path != "<nil>" {
		return path
	}
	if index := documentIntValue(location["block_index"]); index > 0 {
		return "block_" + documentStringValue(index)
	}
	return "block_" + documentStringValue(fallback)
}

func smallDocumentWarnings(truncated bool) []string {
	if !truncated {
		return []string{}
	}
	return []string{"content truncated by max_bytes; do not claim full-document coverage"}
}

func documentAnyMap(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		return v, true
	default:
		return nil, false
	}
}

func documentAnySlice(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func documentStringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func documentIntValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func trimDocumentText(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}
