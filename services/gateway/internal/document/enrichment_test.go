package document

import "testing"

func TestPromotePDFOCRContentLeavesTextOnlyPDFUnchanged(t *testing.T) {
	read := ReadResult{
		Metadata: Metadata{Format: "pdf"},
		Content:  "existing text",
		Document: Representation{
			Pages:        []map[string]any{{"index": 1, "text": "existing text"}},
			Stats:        map[string]any{"complete": false, "scanned_unsupported": false},
			ContentScope: map[string]any{"complete": false},
			Strategy:     StrategyMetadata{Reason: "existing_partial_reason", Complete: false},
		},
	}

	promotePDFOCRContent(&read)

	if read.Content != "existing text" || read.Document.Strategy.Reason != "existing_partial_reason" || read.Document.Strategy.Complete {
		t.Fatalf("text-only PDF state was rewritten by OCR promotion: %#v", read)
	}
}

func TestFinalPDFOCRPageStatusKeepsUnavailableReasonsDistinct(t *testing.T) {
	tests := []struct {
		name       string
		ocr        map[string]any
		wantStatus string
		wantReason string
	}{
		{name: "disabled", ocr: map[string]any{"status": "disabled"}, wantStatus: "ocr_disabled", wantReason: "ocr_adapter_disabled"},
		{name: "failed", ocr: map[string]any{"status": "failed", "reason_code": "provider_timeout"}, wantStatus: "ocr_failed", wantReason: "provider_timeout"},
		{name: "render", ocr: map[string]any{"status": "unsupported"}, wantStatus: "render_failed", wantReason: "ocr_page_resource_unavailable"},
		{name: "budget", ocr: map[string]any{"status": "skipped", "reason": "OCR page budget was exhausted"}, wantStatus: "budget_omitted", wantReason: "ocr_page_budget_exhausted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, reason := finalPDFOCRPageStatus(test.ocr)
			if status != test.wantStatus || reason != test.wantReason {
				t.Fatalf("unexpected OCR page outcome: status=%q reason=%v", status, reason)
			}
		})
	}
}
