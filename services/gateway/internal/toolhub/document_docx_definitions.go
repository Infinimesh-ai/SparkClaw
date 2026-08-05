package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func docxToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		docxToolDefinition("docx.replace_paragraph", "Replace one DOCX paragraph by location or 1-based paragraph_index and write a new DOCX file. Requires current document and paragraph evidence from files.read preflight; old_text is an optional exact-text consistency guard.", []string{"path", "source_document_sha256", "source_hash", "text", "output_path"}, map[string]any{
			"path":                   stringSchema(),
			"paragraph_index":        integerSchema(),
			"location":               objectValueSchema(),
			"old_text":               stringSchema(),
			"source_document_sha256": stringSchema(),
			"source_hash":            stringSchema(),
			"source_evidence":        objectValueSchema(),
			"text":                   stringSchema(),
			"output_path":            stringSchema(),
		}),
		docxToolDefinition("docx.insert_paragraph", "Insert a DOCX paragraph at a bound document boundary or before/after a paragraph backed by current files.read evidence, and write a new DOCX file.", []string{"path", "source_document_sha256", "position", "text", "output_path"}, map[string]any{
			"path":                   stringSchema(),
			"position":               map[string]any{"enum": []any{"start", "end", "before", "after"}},
			"paragraph_index":        integerSchema(),
			"location":               objectValueSchema(),
			"old_text":               stringSchema(),
			"source_document_sha256": stringSchema(),
			"source_hash":            stringSchema(),
			"source_evidence":        objectValueSchema(),
			"document_boundary":      map[string]any{"enum": []any{"start", "end"}},
			"text":                   stringSchema(),
			"output_path":            stringSchema(),
		}),
		docxToolDefinition("docx.delete_paragraph", "Delete one DOCX paragraph by a current files.read location and before-text/hash evidence, then write a new DOCX file.", []string{"path", "source_document_sha256", "source_hash", "old_text", "output_path"}, map[string]any{
			"path":                   stringSchema(),
			"paragraph_index":        integerSchema(),
			"location":               objectValueSchema(),
			"old_text":               stringSchema(),
			"source_document_sha256": stringSchema(),
			"source_hash":            stringSchema(),
			"source_evidence":        objectValueSchema(),
			"output_path":            stringSchema(),
		}),
		docxSetTextStyleDefinition(),
	}
}

func docxSetTextStyleDefinition() app.ToolDefinition {
	style := strictObjectSchema([]string{}, map[string]any{
		"builtin_style": stringSchema(),
		"bold":          booleanSchema(),
		"font_size_pt":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
	})
	style["minProperties"] = 1
	definition := docxToolDefinition("docx.set_text_style", "Set one or more paragraph-level DOCX style properties using current files.read document, paragraph, and before-format evidence, then write a new DOCX file.", []string{"path", "source_document_sha256", "source_hash", "before_format_sha256", "style", "output_path"}, map[string]any{
		"path":                   stringSchema(),
		"paragraph_index":        integerSchema(),
		"location":               objectValueSchema(),
		"old_text":               stringSchema(),
		"source_document_sha256": stringSchema(),
		"source_hash":            stringSchema(),
		"source_evidence":        objectValueSchema(),
		"before_format_sha256":   stringSchema(),
		"style":                  style,
		"output_path":            stringSchema(),
	})
	definition.InputSchema["anyOf"] = []any{
		map[string]any{"required": []string{"paragraph_index"}},
		map[string]any{"required": []string{"location"}},
	}
	return definition
}

func docxToolDefinition(name, description string, required []string, input map[string]any) app.ToolDefinition {
	return app.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: schema("object", required, input),
		OutputSchema: objectSchema([]string{"status", "operation", "path", "output_path", "bytes", "change_summary", "untrusted"}, map[string]any{
			"status":          stringSchema(),
			"operation":       stringSchema(),
			"path":            stringSchema(),
			"output_path":     stringSchema(),
			"bytes":           integerSchema(),
			"paragraph_index": integerSchema(),
			"position":        stringSchema(),
			"text":            stringSchema(),
			"style":           objectValueSchema(),
			"location":        objectValueSchema(),
			"change_summary":  objectValueSchema(),
			"untrusted":       booleanSchema(),
		}),
		Risk:             app.RiskReversible,
		RequiresApproval: true,
		Idempotent:       false,
		TimeoutMS:        10000,
		Sandbox:          "optional",
		Audit:            "always",
	}
}
