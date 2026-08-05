package document

import "strings"

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
