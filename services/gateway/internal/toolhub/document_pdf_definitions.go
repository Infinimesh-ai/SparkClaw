package toolhub

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func pdfToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		{
			Name:        "pdf.extract_text",
			Description: "Extract text and stable page blocks from a workspace PDF. When OvisOCR2 is enabled, scanned pages are rasterized and parsed under bounded page and byte budgets; unavailable or failed OCR remains explicit partial evidence.",
			InputSchema: schema("object", []string{"path"}, map[string]any{
				"path":      stringSchema(),
				"max_bytes": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(document.SmallExtractedMaxBytes)},
			}),
			OutputSchema: objectSchema([]string{"path", "content", "bytes", "truncated", "untrusted", "read_complete", "coverage_status", "missing_page_indexes", "page_status_counts", "scanned_unsupported", "document"}, map[string]any{
				"path":                 stringSchema(),
				"content":              stringSchema(),
				"bytes":                integerSchema(),
				"truncated":            booleanSchema(),
				"untrusted":            booleanSchema(),
				"read_complete":        booleanSchema(),
				"coverage_status":      map[string]any{"enum": []any{"complete", "partial", "unavailable"}},
				"missing_page_indexes": map[string]any{"type": "array", "items": integerSchema()},
				"page_status_counts":   objectValueSchema(),
				"scanned_unsupported":  booleanSchema(),
				"document":             objectValueSchema(),
			}),
			Risk:             app.RiskRead,
			RequiresApproval: false,
			Idempotent:       true,
			TimeoutMS:        125000,
			Sandbox:          "optional",
			Audit:            "always",
		},
		{
			Name:        "pdf.transform",
			Description: "Perform a bounded extract_pages, delete_pages, rotate_pages, or split operation and write a new PDF copy.",
			InputSchema: strictObjectSchema([]string{"operation", "path", "output_path"}, map[string]any{
				"path":        stringSchema(),
				"operation":   map[string]any{"enum": documentOperationEnum(app.DocumentFormatPDF)},
				"pages":       pdfPageIndexesSchema(),
				"rotation":    pdfRotationSchema(),
				"output_path": stringSchema(),
			}),
			OutputSchema: objectSchema([]string{"status", "operation", "output_path", "bytes"}, map[string]any{
				"status":         stringSchema(),
				"operation":      stringSchema(),
				"path":           stringSchema(),
				"output_path":    stringSchema(),
				"outputs":        stringArraySchema(),
				"bytes":          integerSchema(),
				"pages":          integerSchema(),
				"change_summary": objectValueSchema(),
			}),
			Risk:             app.RiskReversible,
			RequiresApproval: true,
			Idempotent:       false,
			TimeoutMS:        15000,
			Sandbox:          "optional",
			Audit:            "always",
		},
	}
}

func pdfPageIndexesSchema() map[string]any {
	return map[string]any{
		"type": "array", "minItems": float64(1), "uniqueItems": true,
		"items": map[string]any{"type": "integer", "minimum": float64(1)},
	}
}

func pdfRotationSchema() map[string]any {
	return map[string]any{"enum": []any{-270, -180, -90, 90, 180, 270}}
}
