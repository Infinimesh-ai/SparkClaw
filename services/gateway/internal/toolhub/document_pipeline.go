package toolhub

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func attachSmallDocumentPipeline(document map[string]any, relPath, format, content string, maxBytes int) {
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
		"has_images":        documentImageCount(document) > 0,
		"is_scanned":        boolLikeDocumentStat(document, "scanned_unsupported"),
		"structure_quality": structureQualityForDocument(document, format),
		"complexity":        complexityForTokenEstimate(tokenEstimate),
	}
	strategy := map[string]any{
		"strategy":     string(app.DocumentStrategySmallDirect),
		"context_mode": string(app.DocumentContextFullText),
		"reason":       "document fits current complete small-file path",
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
		"mode":             string(app.DocumentContextFullText),
		"items":            smallDocumentContextItems(relPath, format, content, document),
		"citations":        smallDocumentCitations(document),
		"context_segments": smallDocumentContextSegments(relPath, content, document),
		"token_estimate":   tokenEstimate,
		"warnings":         []string{},
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
		"status":         string(app.DocumentProcessingSucceeded),
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

func boolLikeDocumentStat(document map[string]any, key string) bool {
	stats, _ := documentAnyMap(document["stats"])
	value, _ := stats[key].(bool)
	return value
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

func smallDocumentContextItems(relPath, format, content string, document map[string]any) []map[string]any {
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
	for _, segment := range smallDocumentContextSegments(relPath, "", document) {
		if segment["category"] == "content" {
			continue
		}
		items = append(items, map[string]any{
			"type": segment["category"], "anchor": segment["anchor"], "text": segment["text"],
			"untrusted": segment["untrusted"], "provenance": segment["provenance"],
		})
	}
	return items
}

func smallDocumentContextSegments(relPath, content string, document map[string]any) []map[string]any {
	segments := []map[string]any{}
	if strings.TrimSpace(content) != "" {
		segments = append(segments, map[string]any{
			"category": "content", "anchor": relPath, "text": content, "priority": 100, "untrusted": true,
		})
	}
	enrichment, ok := documentAnyMap(document["enrichment"])
	if !ok {
		return segments
	}
	assets, _ := documentAnyMap(enrichment["assets"])
	imageBudget := 4000
	ocrBudget := 8000
	seenSemanticHashes := map[string]struct{}{}
	seenOCRHashes := map[string]struct{}{}
	for _, value := range documentAnySlice(assets["images"]) {
		image, ok := documentAnyMap(value)
		if !ok {
			continue
		}
		hash := stringArg(image, "sha256", "")
		location, _ := documentAnyMap(image["location"])
		semantic, hasSemantic := documentAnyMap(image["semantic"])
		_, semanticSeen := seenSemanticHashes[hash]
		if imageBudget > 0 && hasSemantic && stringArg(semantic, "status", "") == "succeeded" && (hash == "" || !semanticSeen) {
			seenSemanticHashes[hash] = struct{}{}
			parts := []string{stringArg(semantic, "description", "")}
			if relationship := stringArg(semantic, "relationship_to_text", ""); relationship != "" {
				parts = append(parts, relationship)
			}
			if visibleText := outputStringArray(semantic["ocr_text"]); len(visibleText) > 0 {
				parts = append(parts, "Visible text: "+strings.Join(visibleText, " | "))
			}
			text := trimDocumentText(strings.Join(parts, " "), min(800, imageBudget))
			if strings.TrimSpace(text) != "" {
				segments = append(segments, map[string]any{
					"category": "image_semantic", "anchor": stringArg(location, "path", ""), "text": text, "priority": 80,
					"provenance": stringArg(semantic, "model_call_id", ""), "untrusted": true,
				})
				imageBudget -= utf8.RuneCountInString(text)
			}
		}
		ocr, hasOCR := documentAnyMap(image["ocr"])
		_, ocrSeen := seenOCRHashes[hash]
		promotedPDFPage := stringArg(document, "format", "") == "pdf" && stringArg(image, "kind", "") == "page_image"
		if !promotedPDFPage && ocrBudget > 0 && hasOCR && stringArg(ocr, "status", "") == "succeeded" && (hash == "" || !ocrSeen) {
			seenOCRHashes[hash] = struct{}{}
			text := trimDocumentText(stringArg(ocr, "markdown", ""), min(2000, ocrBudget))
			if strings.TrimSpace(text) != "" {
				segments = append(segments, map[string]any{
					"category": "ocr", "anchor": stringArg(location, "path", ""), "text": text, "priority": 90,
					"provenance": stringArg(ocr, "model_call_id", ""), "untrusted": true,
				})
				ocrBudget -= utf8.RuneCountInString(text)
			}
		}
	}
	annotations, _ := documentAnyMap(enrichment["annotations"])
	annotationCount := 0
	for _, category := range []string{"comments", "notes", "hyperlinks"} {
		for _, value := range documentAnySlice(annotations[category]) {
			annotation, ok := documentAnyMap(value)
			if !ok || annotationCount >= 6 {
				continue
			}
			text := trimDocumentText(stringArg(annotation, "text", ""), 300)
			if text == "" {
				continue
			}
			location, _ := documentAnyMap(annotation["location"])
			segments = append(segments, map[string]any{
				"category": "annotation", "anchor": stringArg(location, "path", ""), "text": text, "priority": 60, "untrusted": true,
			})
			annotationCount++
		}
	}
	return segments
}

func documentImageCount(document map[string]any) int {
	enrichment, ok := documentAnyMap(document["enrichment"])
	if !ok {
		return 0
	}
	assets, ok := documentAnyMap(enrichment["assets"])
	if !ok {
		return 0
	}
	return len(documentAnySlice(assets["images"]))
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
