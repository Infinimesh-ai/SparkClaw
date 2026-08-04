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
