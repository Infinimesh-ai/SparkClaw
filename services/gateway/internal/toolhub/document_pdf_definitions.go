package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func pdfToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		{
			Name:        "pdf.extract_text",
			Description: "Extract text and stable page blocks from a workspace PDF. When OvisOCR2 is enabled, scanned pages are rasterized and parsed under bounded page and byte budgets; unavailable or failed OCR remains explicit partial evidence.",
			InputSchema: schema("object", []string{"path"}, map[string]any{
				"path":      stringSchema(),
				"max_bytes": map[string]any{"type": "number"},
			}),
			OutputSchema: objectSchema([]string{"path", "content", "bytes", "truncated", "untrusted", "scanned_unsupported", "document"}, map[string]any{
				"path":                stringSchema(),
				"content":             stringSchema(),
				"bytes":               integerSchema(),
				"truncated":           booleanSchema(),
				"untrusted":           booleanSchema(),
				"scanned_unsupported": booleanSchema(),
				"document":            objectValueSchema(),
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
			Description: "Perform a bounded PDF transform such as extract_pages, delete_pages, rotate_pages, merge, or split and write a new PDF.",
			InputSchema: schema("object", []string{"operation", "output_path"}, map[string]any{
				"path":        stringSchema(),
				"inputs":      stringArraySchema(),
				"operation":   map[string]any{"enum": []any{"extract_pages", "delete_pages", "rotate_pages", "merge", "split"}},
				"pages":       map[string]any{"type": "array", "items": map[string]any{"type": "number"}},
				"rotation":    map[string]any{"type": "number"},
				"output_path": stringSchema(),
			}),
			OutputSchema: objectSchema([]string{"status", "operation", "output_path", "bytes"}, map[string]any{
				"status":         stringSchema(),
				"operation":      stringSchema(),
				"path":           stringSchema(),
				"inputs":         stringArraySchema(),
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
