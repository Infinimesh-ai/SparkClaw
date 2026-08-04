package toolhub

import (
	"strings"
	"testing"
)

func TestSmallDocumentContextDeduplicatesSuccessfulOCRText(t *testing.T) {
	document := documentWithImageEvidence("succeeded", "# Receipt\n\nTotal: 42")
	segments := smallDocumentContextSegments("receipt.docx", "", document)

	semantic := contextSegmentByCategory(segments, "image_semantic")
	if semantic == nil || strings.Contains(stringArg(semantic, "text", ""), "Visible text:") {
		t.Fatalf("semantic context repeated successful OCR text: %#v", segments)
	}
	ocr := contextSegmentByCategory(segments, "ocr")
	if ocr == nil || !strings.Contains(stringArg(ocr, "text", ""), "Total: 42") {
		t.Fatalf("successful OCR context was not retained: %#v", segments)
	}
}

func TestSmallDocumentContextKeepsFastTextWhenOCRUnavailable(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   string
		markdown string
	}{
		{name: "disabled", status: "disabled"},
		{name: "failed", status: "failed"},
		{name: "empty", status: "succeeded", markdown: "  \n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			segments := smallDocumentContextSegments("receipt.docx", "", documentWithImageEvidence(test.status, test.markdown))
			semantic := contextSegmentByCategory(segments, "image_semantic")
			if semantic == nil || !strings.Contains(stringArg(semantic, "text", ""), "Visible text: Total 42") {
				t.Fatalf("Fast visible text fallback was lost: %#v", segments)
			}
			if contextSegmentByCategory(segments, "ocr") != nil {
				t.Fatalf("unavailable OCR produced a context segment: %#v", segments)
			}
		})
	}
}

func documentWithImageEvidence(ocrStatus, markdown string) map[string]any {
	return map[string]any{
		"format": "docx",
		"enrichment": map[string]any{
			"assets": map[string]any{
				"images": []any{map[string]any{
					"sha256":   "image-a",
					"location": map[string]any{"path": "document.image[1]"},
					"semantic": map[string]any{
						"status": "succeeded", "description": "A receipt layout.",
						"relationship_to_text": "The total is emphasized.",
						"ocr_text":             []string{"Total 42"},
						"model_call_id":        "mcall_fast",
					},
					"ocr": map[string]any{
						"status": ocrStatus, "markdown": markdown, "model_call_id": "mcall_ocr",
					},
				}},
			},
		},
	}
}

func contextSegmentByCategory(segments []map[string]any, category string) map[string]any {
	for _, segment := range segments {
		if stringArg(segment, "category", "") == category {
			return segment
		}
	}
	return nil
}
