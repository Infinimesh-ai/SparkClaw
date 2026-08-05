package toolhub

import (
	"context"
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
	metadata documentOCRCallMetadata
}

type documentOCRResult struct {
	hash       string
	invocation documentOCRInvocation
}

func (e *ovisDocumentOCREnricher) Name() string { return "ovisocr2_page_parsing" }

func (e *ovisDocumentOCREnricher) Supports(format string, category string) bool {
	if e == nil || e.hub == nil || category != "assets" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "pdf":
		return true
	case "docx", "xlsx", "pptx":
		return e.hub.ocr != nil && e.hub.ocr.Enabled()
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
	execution := documentOCRExecutionFromContext(ctx)
	if scannedPDF {
		mode = "all"
		if e.hub.ocr == nil || !e.hub.ocr.Enabled() {
			for _, value := range imageValues {
				record, ok := documentAnyMap(value)
				if ok && stringArg(record, "kind", "") == "page_image" {
					record["ocr"] = skippedDocumentOCR("disabled", "document OCR adapter is disabled")
					metadata := documentOCRMetadataForRecord(request.Document, record, execution)
					if resource, exists := resources[strings.TrimSpace(stringArg(record, "resource_key", ""))]; exists {
						metadata.SourceSHA256 = resource.SHA256
					}
					e.hub.recordDocumentOCRBypass(metadata, "disabled", "ocr_adapter_disabled")
				}
			}
			return document.EnrichmentResult{Enrichment: enrichment}, nil
		}
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
			if scannedPDF && stringArg(record, "kind", "") == "page_image" {
				e.hub.recordDocumentOCRBypass(documentOCRMetadataForRecord(request.Document, record, execution), "render_failed", "ocr_page_resource_unavailable")
			}
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
			metadata := documentOCRMetadataForRecord(request.Document, record, execution)
			metadata.SourceSHA256 = hash
			representative[hash] = documentOCRTask{hash: hash, resource: resource, metadata: metadata}
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
			invocation := e.hub.recordDocumentOCRBypass(representative[hash].metadata, "budget_omitted", "ocr_page_budget_exhausted")
			e.hub.recordAdditionalDocumentOCRPages(invocation, len(recordsByHash[hash])-1)
			warnings = append(warnings, "OvisOCR2 page budget was exhausted before all relevant images were parsed")
			continue
		}
		tasks = append(tasks, representative[hash])
	}

	for _, result := range e.parseImages(ctx, tasks) {
		invocation := result.invocation
		e.hub.recordAdditionalDocumentOCRPages(invocation, len(recordsByHash[result.hash])-1)
		if invocation.Err != nil {
			ocr := documentOCRProvenance(invocation, result.hash)
			ocr["status"] = "failed"
			ocr["provider"] = "ovisocr2"
			ocr["reason_code"] = invocation.ReasonCode
			ocr["warning"] = invocation.Err.Error()
			ocr["untrusted"] = true
			setOCRForRecords(recordsByHash[result.hash], ocr)
			warnings = append(warnings, "OvisOCR2 page parsing failed: "+invocation.Err.Error())
			continue
		}
		if scannedPDF && documentocr.IsTrivialMarkdown(invocation.Result.Markdown) {
			ocr := documentOCRProvenance(invocation, result.hash)
			ocr["status"] = "failed"
			ocr["provider"] = "ovisocr2"
			ocr["reason_code"] = "no_usable_text"
			ocr["untrusted"] = true
			setOCRForRecords(recordsByHash[result.hash], ocr)
			warnings = append(warnings, "OvisOCR2 page parsing returned no usable text")
			continue
		}
		ocr := documentOCRProvenance(invocation, result.hash)
		ocr["status"] = "succeeded"
		ocr["provider"] = "ovisocr2"
		ocr["model"] = invocation.Result.Model
		ocr["markdown"] = invocation.Result.Markdown
		ocr["inference_ms"] = invocation.Result.InferenceMS
		ocr["untrusted"] = true
		setOCRForRecords(recordsByHash[result.hash], ocr)
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
				invocation := e.hub.recordDocumentOCRBypass(task.metadata, "cancelled", "request_cancelled")
				invocation.Err = ctx.Err()
				results <- documentOCRResult{hash: task.hash, invocation: invocation}
				return
			}
			defer func() { <-semaphore }()
			prepared, err := prepareImageForModel(task.resource.Content, task.resource.ContentType)
			if err != nil {
				invocation := e.hub.recordDocumentOCRBypass(task.metadata, "render_failed", "ocr_image_preparation_failed")
				invocation.Err = err
				results <- documentOCRResult{hash: task.hash, invocation: invocation}
				return
			}
			invocation := e.hub.parseDocumentOCR(ctx, documentocr.Request{Content: prepared.Content, ContentType: prepared.ContentType}, task.metadata)
			results <- documentOCRResult{hash: task.hash, invocation: invocation}
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

func documentOCRMetadataForRecord(documentValue document.Representation, record map[string]any, execution documentOCRExecution) documentOCRCallMetadata {
	location, _ := documentAnyMap(record["location"])
	pageIndex := intArg(location, "page_index", 0)
	metadata := documentOCRCallMetadata{
		SessionID: execution.SessionID, RunID: execution.RunID, PageIndex: pageIndex,
		SourceSHA256: stringArg(record, "sha256", ""), PreprocessingVersion: documentOCRDefaultPreprocessingVersion,
	}
	for _, pageValue := range documentValue.Pages {
		if intArg(pageValue, "index", 0) != pageIndex {
			continue
		}
		metadata.PreprocessingVersion = stringArg(pageValue, "ocr_preprocessing_version", metadata.PreprocessingVersion)
		quality, _ := documentAnyMap(pageValue["native_text_quality"])
		metadata.ClassifierVersion = stringArg(quality, "version", "")
		break
	}
	return metadata
}

func documentOCRProvenance(invocation documentOCRInvocation, sourceSHA string) map[string]any {
	out := map[string]any{
		"model_call_id": invocation.ModelCallID, "source_sha256": sourceSHA, "prepared_sha256": invocation.PreparedSHA256,
		"cache_result": invocation.CacheResult, "cache_record_id": invocation.CacheRecordID,
	}
	return out
}
