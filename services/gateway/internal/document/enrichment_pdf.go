package document

import "strings"

const (
	pdfPageTextNative        = "native"
	pdfPageTextOCRPending    = "ocr_pending"
	pdfPageTextOCRSucceeded  = "ocr_succeeded"
	pdfPageTextOCRDisabled   = "ocr_disabled"
	pdfPageTextOCRFailed     = "ocr_failed"
	pdfPageTextRenderFailed  = "render_failed"
	pdfPageTextBudgetOmitted = "budget_omitted"
)

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
		pageIndex := intValue(mapValue(image["location"])["page_index"])
		if pageIndex > 0 {
			ocrByPage[pageIndex] = ocr
		}
	}

	content := make([]string, 0, len(read.Document.Pages))
	missingPages := []int{}
	ocrPages := 0
	statusCounts := map[string]int{}
	for _, page := range read.Document.Pages {
		pageIndex := intValue(page["index"])
		nativeText := strings.TrimSpace(stringValue(page["text"]))
		text := nativeText
		status := strings.TrimSpace(stringValue(page["text_status"]))
		if status == "" {
			status = pdfPageTextOCRPending
			if text != "" {
				status = pdfPageTextNative
			}
		}
		if status == pdfPageTextOCRPending {
			ocr := ocrByPage[pageIndex]
			page["ocr_model_call_id"] = ocr["model_call_id"]
			page["ocr_cache_result"] = ocr["cache_result"]
			page["ocr_cache_record_id"] = ocr["cache_record_id"]
			page["ocr_prepared_sha256"] = ocr["prepared_sha256"]
			status, page["text_status_reason"] = finalPDFOCRPageStatus(ocr)
			if status == pdfPageTextOCRSucceeded {
				ocrText := strings.TrimSpace(stringValue(ocr["markdown"]))
				page["ocr_text"] = ocrText
				if nativeText == "" {
					text = ocrText
					page["text_source"] = "ocr"
				} else {
					page["native_text"] = nativeText
					text = mergePDFPageEvidence(nativeText, ocrText)
					page["text_source"] = "native+ocr"
				}
				page["text"] = text
				updatePDFPageBlocks(&read.Document, pageIndex, nativeText, ocrText, stringValue(page["text_source"]), ocr)
				ocrPages++
			}
		}
		page["text_status"] = status
		statusCounts[status]++
		if status != pdfPageTextNative && status != pdfPageTextOCRSucceeded {
			missingPages = append(missingPages, pageIndex)
		}
		if text != "" {
			content = append(content, text)
		}
	}
	read.Content = strings.Join(content, "\n\n")
	if ocrPages > 0 {
		if len(read.Document.Sections) == 0 {
			read.Document.Sections = deriveSections(read.Document.ID, read.Document.Blocks, read.Document.Paragraphs)
		}
	}
	complete := len(missingPages) == 0
	coverage := "partial"
	if complete {
		coverage = "complete"
	} else if strings.TrimSpace(read.Content) == "" {
		coverage = "unavailable"
	}
	read.Document.Stats["scanned_unsupported"] = !complete
	read.Document.Stats["ocr_pages"] = ocrPages
	read.Document.Stats["read_complete"] = complete
	read.Document.Stats["coverage_status"] = coverage
	read.Document.Stats["missing_page_indexes"] = missingPages
	read.Document.Stats["page_status_counts"] = statusCounts
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
		read.Document.Strategy.Reason = "pdf_pages_missing_text_evidence"
		SetCoverage(read.Document.Enrichment, "content", coverage)
	}
}

func finalPDFOCRPageStatus(ocr map[string]any) (string, any) {
	switch strings.ToLower(strings.TrimSpace(stringValue(ocr["status"]))) {
	case "succeeded":
		if strings.TrimSpace(stringValue(ocr["markdown"])) != "" {
			return pdfPageTextOCRSucceeded, "ocr_usable_text"
		}
		return pdfPageTextOCRFailed, "no_usable_text"
	case "failed":
		return pdfPageTextOCRFailed, firstString(ocr["reason_code"], "ocr_failed")
	case "disabled":
		return pdfPageTextOCRDisabled, "ocr_adapter_disabled"
	case "unsupported":
		return pdfPageTextRenderFailed, "ocr_page_resource_unavailable"
	case "skipped":
		if strings.Contains(strings.ToLower(stringValue(ocr["reason"])), "budget") {
			return pdfPageTextBudgetOmitted, "ocr_page_budget_exhausted"
		}
		return pdfPageTextOCRDisabled, "ocr_not_executed"
	default:
		return pdfPageTextOCRDisabled, "ocr_adapter_disabled"
	}
}

func mergePDFPageEvidence(nativeText, ocrText string) string {
	if pdfPageEvidenceEqual(nativeText, ocrText) {
		return nativeText
	}
	return strings.TrimSpace(nativeText + "\n\n" + ocrText)
}

func updatePDFPageBlocks(document *Representation, pageIndex int, nativeText, ocrText, source string, ocr map[string]any) {
	for index := range document.Blocks {
		block := &document.Blocks[index]
		if intValue(block.Location["page_index"]) != pageIndex {
			continue
		}
		if block.Format == nil {
			block.Format = map[string]any{}
		}
		if source == "native+ocr" {
			block.Text = nativeText
			block.Format["source"] = "native"
			if pdfPageEvidenceEqual(nativeText, ocrText) {
				block.Format["ocr_duplicate_model_call_id"] = ocr["model_call_id"]
				document.Stats["blocks"] = len(document.Blocks)
				return
			}
			location := cloneMap(block.Location)
			location["evidence_source"] = "ocr"
			document.Blocks = append(document.Blocks, Block{
				ID:       stableID("block", document.ID+"\x00pdf-ocr-page-"+stringValue(pageIndex)),
				Kind:     "page_ocr",
				Text:     ocrText,
				Location: location,
				Format: map[string]any{
					"source": "ocr", "provider": "ovisocr2", "model_call_id": ocr["model_call_id"], "untrusted": true,
				},
			})
		} else {
			block.Text = ocrText
			block.Format["source"] = "ocr"
			block.Format["provider"] = "ovisocr2"
			block.Format["model_call_id"] = ocr["model_call_id"]
			block.Format["untrusted"] = true
		}
		document.Stats["blocks"] = len(document.Blocks)
		return
	}
}

func pdfPageEvidenceEqual(left, right string) bool {
	return strings.Join(strings.Fields(left), " ") == strings.Join(strings.Fields(right), " ")
}
