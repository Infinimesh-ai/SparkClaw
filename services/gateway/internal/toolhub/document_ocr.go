package toolhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
)

type ovisDocumentOCREnricher struct {
	hub *ToolHub
}

type documentOCRTask struct {
	hash     string
	resource document.Resource
}

type documentOCRResult struct {
	hash   string
	result documentocr.Result
	err    error
}

func (e *ovisDocumentOCREnricher) Name() string { return "ovisocr2_page_parsing" }

func (e *ovisDocumentOCREnricher) Supports(format string, category string) bool {
	if e == nil || e.hub == nil || e.hub.ocr == nil || !e.hub.ocr.Enabled() || category != "assets" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "docx", "xlsx", "pptx", "pdf":
		return true
	default:
		return false
	}
}

func (e *ovisDocumentOCREnricher) Enrich(ctx context.Context, request document.EnrichmentRequest) (document.EnrichmentResult, error) {
	enrichment := request.Document.Enrichment
	assets, ok := documentAnyMap(enrichment["assets"])
	if !ok {
		return document.EnrichmentResult{Enrichment: enrichment}, nil
	}
	imageValues := documentAnySlice(assets["images"])
	if len(imageValues) == 0 {
		return document.EnrichmentResult{Enrichment: enrichment}, nil
	}
	resources := map[string]document.Resource{}
	for _, resource := range request.Resources {
		resources[resource.Key] = resource
	}
	mode := strings.ToLower(strings.TrimSpace(request.Options.ImageAnalysis))
	if mode == "" {
		mode = "targeted"
	}
	if mode != "none" && mode != "targeted" && mode != "all" {
		return document.EnrichmentResult{}, fmt.Errorf("unsupported image_analysis mode %q", mode)
	}
	scannedPDF := request.Metadata.Format == "pdf" && boolArg(request.Document.Stats, "scanned_unsupported", false)
	if scannedPDF {
		mode = "all"
	}

	representative := map[string]documentOCRTask{}
	recordsByHash := map[string][]map[string]any{}
	warnings := []string{}
	for _, value := range imageValues {
		record, ok := documentAnyMap(value)
		if !ok {
			continue
		}
		if scannedPDF && stringArg(record, "kind", "") != "page_image" {
			record["ocr"] = skippedDocumentOCR("skipped", "scanned PDF OCR uses the rendered full page")
			continue
		}
		resource, exists := resources[strings.TrimSpace(stringArg(record, "resource_key", ""))]
		if !exists || len(resource.Content) == 0 {
			record["ocr"] = skippedDocumentOCR("unsupported", "image bytes were not exposed by the document parser")
			continue
		}
		hash := resource.SHA256
		recordsByHash[hash] = append(recordsByHash[hash], record)
		if mode == "none" {
			record["ocr"] = skippedDocumentOCR("skipped", "OCR was not requested")
			continue
		}
		if mode == "targeted" && !imageMatchesTargets(record, request.Options.TargetPaths) {
			record["ocr"] = skippedDocumentOCR("skipped", "image was outside the requested OCR target")
			continue
		}
		width, height := imageDimensions(resource.Content)
		if !scannedPDF && width > 0 && height > 0 && width <= 64 && height <= 64 {
			record["ocr"] = skippedDocumentOCR("skipped", "tiny decorative image")
			continue
		}
		if !supportedImageContentType(resource.ContentType) {
			record["ocr"] = skippedDocumentOCR("unsupported", "image media type is not supported by OvisOCR2")
			continue
		}
		if _, exists := representative[hash]; !exists {
			representative[hash] = documentOCRTask{hash: hash, resource: resource}
		}
	}

	hashes := make([]string, 0, len(representative))
	for hash := range representative {
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)
	limit := documentTargetedImageLimit
	if mode == "all" {
		limit = documentFullImageLimit
	}
	tasks := make([]documentOCRTask, 0, min(len(hashes), limit))
	for _, hash := range hashes {
		if len(tasks) >= limit {
			setOCRForRecords(recordsByHash[hash], skippedDocumentOCR("skipped", "OCR page budget was exhausted"))
			warnings = append(warnings, "OvisOCR2 page budget was exhausted before all relevant images were parsed")
			continue
		}
		tasks = append(tasks, representative[hash])
	}

	for _, result := range e.parseImages(ctx, tasks) {
		if result.err != nil {
			setOCRForRecords(recordsByHash[result.hash], map[string]any{
				"status": "failed", "provider": "ovisocr2", "warning": result.err.Error(), "source_sha256": result.hash, "untrusted": true,
			})
			warnings = append(warnings, "OvisOCR2 page parsing failed: "+result.err.Error())
			continue
		}
		setOCRForRecords(recordsByHash[result.hash], map[string]any{
			"status": "succeeded", "provider": "ovisocr2", "model": result.result.Model,
			"model_call_id": documentOCRCallID(result.hash, result.result.Model), "source_sha256": result.hash,
			"markdown": result.result.Markdown, "inference_ms": result.result.InferenceMS, "untrusted": true,
		})
	}
	return document.EnrichmentResult{Enrichment: enrichment, Warnings: uniqueDocumentWarnings(warnings)}, nil
}

func (e *ovisDocumentOCREnricher) parseImages(ctx context.Context, tasks []documentOCRTask) []documentOCRResult {
	if len(tasks) == 0 {
		return nil
	}
	workerLimit := max(1, e.hub.cfg.Adapters.DocumentOCR.MaxConcurrency)
	results := make(chan documentOCRResult, len(tasks))
	semaphore := make(chan struct{}, workerLimit)
	var wait sync.WaitGroup
	for _, task := range tasks {
		task := task
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				results <- documentOCRResult{hash: task.hash, err: ctx.Err()}
				return
			}
			defer func() { <-semaphore }()
			prepared, err := prepareImageForModel(task.resource.Content, task.resource.ContentType)
			if err != nil {
				results <- documentOCRResult{hash: task.hash, err: err}
				return
			}
			parsed, err := e.hub.ocr.Parse(ctx, documentocr.Request{Content: prepared.Content, ContentType: prepared.ContentType})
			results <- documentOCRResult{hash: task.hash, result: parsed, err: err}
		}()
	}
	wait.Wait()
	close(results)
	out := make([]documentOCRResult, 0, len(tasks))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func setOCRForRecords(records []map[string]any, ocr map[string]any) {
	for _, record := range records {
		record["ocr"] = cloneDocumentMap(ocr)
	}
}

func skippedDocumentOCR(status, reason string) map[string]any {
	return map[string]any{"status": status, "provider": "ovisocr2", "reason": reason, "untrusted": true}
}

func documentOCRCallID(hash, model string) string {
	digest := sha256.Sum256([]byte(hash + "\x00" + model + "\x00ovisocr2_page_parse_v1"))
	return "mcall_" + hex.EncodeToString(digest[:8])
}
