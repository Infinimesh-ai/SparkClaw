package document

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const documentEnrichmentTimeout = 120 * time.Second

func normalizeEnrichment(documentID string, raw any) map[string]any {
	source, ok := raw.(map[string]any)
	if !ok || source == nil {
		return nil
	}
	enrichment := deepCopyMap(source)
	enrichment["schema_version"] = EnrichmentSchemaVersion

	assets := ensureMap(enrichment, "assets")
	images := mapSlice(assets["images"])
	for index, image := range images {
		key := firstString(image["resource_key"], mapValue(image["source"])["part_name"], mapValue(image["location"])["path"], index+1)
		if strings.TrimSpace(stringValue(image["id"])) == "" {
			image["id"] = stableID("asset", documentID+"\x00"+key)
		}
		if strings.TrimSpace(stringValue(image["kind"])) == "" {
			image["kind"] = "image"
		}
		if strings.TrimSpace(stringValue(image["parent_path"])) == "" {
			image["parent_path"] = parentLocationPath(stringValue(mapValue(image["location"])["path"]))
		}
	}
	assets["images"] = images
	ensureSlice(assets, "charts")
	ensureSlice(assets, "embedded_objects")

	annotations := ensureMap(enrichment, "annotations")
	ensureSlice(annotations, "comments")
	ensureSlice(annotations, "notes")
	ensureSlice(annotations, "hyperlinks")

	layout := ensureMap(enrichment, "layout")
	ensureSlice(layout, "sections")
	ensureSlice(layout, "page_settings")
	ensureSlice(layout, "slide_layouts")
	ensureSlice(layout, "merged_ranges")
	ensureSlice(layout, "shapes")
	ensureSlice(layout, "companion_groups")
	ensureSlice(layout, "page_markers")

	extensions := ensureMap(enrichment, "extensions")
	if strings.TrimSpace(stringValue(extensions["status"])) == "" {
		extensions["status"] = "deferred"
	}
	ensureSlice(extensions, "parts")

	coverage := ensureMap(enrichment, "coverage")
	for key, fallback := range map[string]string{
		"content": "complete", "assets": "unknown", "annotations": "unknown", "layout": "unknown", "extensions": "deferred",
	} {
		if strings.TrimSpace(stringValue(coverage[key])) == "" {
			coverage[key] = fallback
		}
	}
	policy := ensureMap(enrichment, "category_policy")
	for key, value := range map[string]string{
		"content": "editable", "assets": "evidence_only", "annotations": "evidence_only", "layout": "evidence_only", "extensions": "evidence_only",
	} {
		policy[key] = value
	}
	return enrichment
}

func (p *Pipeline) enrich(ctx context.Context, read ReadResult, options EnrichmentOptions) (ReadResult, error) {
	if len(p.enrichers) == 0 || read.Document.Enrichment == nil {
		if options.Required {
			return ReadResult{}, &PipelineError{Code: CodeEnrichmentFailed, Stage: StageEnrich, Format: read.Metadata.Format, Detail: "required image evidence is unavailable"}
		}
		return read, nil
	}
	if strings.TrimSpace(options.ImageAnalysis) == "" {
		options.ImageAnalysis = "targeted"
	}
	stageCtx, stageCancel := context.WithTimeout(ctx, documentEnrichmentTimeout)
	defer stageCancel()
	for _, enricher := range p.enrichers {
		if enricher == nil || !enricher.Supports(read.Metadata.Format, "assets") {
			continue
		}
		result, err := enricher.Enrich(stageCtx, EnrichmentRequest{
			Metadata: read.Metadata, Document: read.Document, Resources: read.Resources, Options: options,
		})
		if err != nil {
			SetCoverage(read.Document.Enrichment, "assets", "partial")
			appendEnrichmentWarning(read.Document.Enrichment, fmt.Sprintf("%s: %v", enricher.Name(), err))
			if options.Required {
				return ReadResult{}, &PipelineError{Code: CodeEnrichmentFailed, Stage: StageEnrich, Format: read.Metadata.Format, Detail: err.Error()}
			}
			continue
		}
		if result.Enrichment != nil {
			read.Document.Enrichment = result.Enrichment
		}
		for _, warning := range result.Warnings {
			appendEnrichmentWarning(read.Document.Enrichment, warning)
		}
	}
	promotePDFOCRContent(&read)
	if options.Required && !hasSucceededImageEvidence(read.Document.Enrichment) {
		return ReadResult{}, &PipelineError{Code: CodeEnrichmentFailed, Stage: StageEnrich, Format: read.Metadata.Format, Detail: "required image evidence was not produced"}
	}
	return read, nil
}

func hasSucceededImageEvidence(enrichment map[string]any) bool {
	assets := mapValue(enrichment["assets"])
	for _, image := range mapSlice(assets["images"]) {
		if strings.EqualFold(stringValue(mapValue(image["semantic"])["status"]), "succeeded") ||
			strings.EqualFold(stringValue(mapValue(image["ocr"])["status"]), "succeeded") {
			return true
		}
	}
	return false
}

func promotePDFOCRContent(read *ReadResult) {
	if read == nil || read.Metadata.Format != "pdf" || len(read.Document.Pages) == 0 {
		return
	}
	scannedUnsupported, _ := read.Document.Stats["scanned_unsupported"].(bool)
	if !scannedUnsupported {
		return
	}
	ocrByPage := map[int]map[string]any{}
	assets := mapValue(read.Document.Enrichment["assets"])
	for _, image := range mapSlice(assets["images"]) {
		if stringValue(image["kind"]) != "page_image" {
			continue
		}
		ocr := mapValue(image["ocr"])
		if !strings.EqualFold(stringValue(ocr["status"]), "succeeded") || strings.TrimSpace(stringValue(ocr["markdown"])) == "" {
			continue
		}
		pageIndex := intValue(mapValue(image["location"])["page_index"])
		if pageIndex > 0 {
			ocrByPage[pageIndex] = ocr
		}
	}

	content := make([]string, 0, len(read.Document.Pages))
	missingPages := 0
	ocrPages := 0
	for _, page := range read.Document.Pages {
		pageIndex := intValue(page["index"])
		text := strings.TrimSpace(stringValue(page["text"]))
		if text == "" {
			if ocr := ocrByPage[pageIndex]; ocr != nil {
				text = strings.TrimSpace(stringValue(ocr["markdown"]))
				page["text"] = text
				page["text_source"] = "ovisocr2"
				page["ocr_model_call_id"] = ocr["model_call_id"]
				ocrPages++
			} else {
				missingPages++
			}
		}
		if text != "" {
			content = append(content, text)
		}
	}
	if ocrPages > 0 {
		read.Content = strings.Join(content, "\n\n")
		for index := range read.Document.Blocks {
			block := &read.Document.Blocks[index]
			pageIndex := intValue(block.Location["page_index"])
			if ocr := ocrByPage[pageIndex]; strings.TrimSpace(block.Text) == "" && ocr != nil {
				block.Text = strings.TrimSpace(stringValue(ocr["markdown"]))
				if block.Format == nil {
					block.Format = map[string]any{}
				}
				block.Format["source"] = "ovisocr2"
				block.Format["model_call_id"] = ocr["model_call_id"]
			}
		}
		if len(read.Document.Sections) == 0 {
			read.Document.Sections = deriveSections(read.Document.ID, read.Document.Blocks, read.Document.Paragraphs)
		}
	}
	complete := missingPages == 0
	read.Document.Stats["scanned_unsupported"] = !complete
	read.Document.Stats["ocr_pages"] = ocrPages
	read.Document.Stats["extracted_bytes"] = len([]byte(read.Content))
	read.Document.Stats["complete"] = complete
	read.Document.Stats["blocks"] = len(read.Document.Blocks)
	read.Document.Stats["sections"] = len(read.Document.Sections)
	read.Document.ContentScope["complete"] = complete
	read.Document.Strategy.Complete = complete
	if complete && ocrPages > 0 {
		read.Document.Strategy.Reason = "small_file_complete_read_with_ovisocr2"
		SetCoverage(read.Document.Enrichment, "content", "complete")
	} else if !complete {
		read.Document.Strategy.Reason = "scanned_pages_require_ocr"
		SetCoverage(read.Document.Enrichment, "content", "partial")
	}
}

func SetCoverage(enrichment map[string]any, category, status string) {
	if enrichment == nil {
		return
	}
	ensureMap(enrichment, "coverage")[category] = status
}

func appendEnrichmentWarning(enrichment map[string]any, warning string) {
	warning = strings.TrimSpace(warning)
	if enrichment == nil || warning == "" {
		return
	}
	warnings := stringSlice(enrichment["warnings"])
	warnings = append(warnings, warning)
	enrichment["warnings"] = uniqueStrings(warnings)
}

func ensureMap(parent map[string]any, key string) map[string]any {
	if current, ok := parent[key].(map[string]any); ok && current != nil {
		return current
	}
	current := map[string]any{}
	parent[key] = current
	return current
}

func ensureSlice(parent map[string]any, key string) {
	if parent[key] == nil {
		parent[key] = []any{}
	}
}

func deepCopyMap(source map[string]any) map[string]any {
	raw, err := json.Marshal(source)
	if err != nil {
		return cloneMap(source)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return cloneMap(source)
	}
	return out
}

func parentLocationPath(path string) string {
	if index := strings.LastIndex(path, "."); index > 0 {
		return path[:index]
	}
	return path
}

func stringSlice(value any) []string {
	switch current := value.(type) {
	case []string:
		return append([]string(nil), current...)
	case []any:
		out := make([]string, 0, len(current))
		for _, item := range current {
			if text := strings.TrimSpace(stringValue(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
