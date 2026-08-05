package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func docxToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		docxToolDefinition("docx.replace_paragraph", "Replace one DOCX paragraph by location or 1-based paragraph_index and write a new DOCX file. Requires source_hash evidence from files.read preflight; old_text is an optional exact-text consistency guard.", []string{"path", "source_hash", "text", "output_path"}, map[string]any{
			"path":            stringSchema(),
			"paragraph_index": integerSchema(),
			"location":        objectValueSchema(),
			"old_text":        stringSchema(),
			"source_hash":     stringSchema(),
			"text":            stringSchema(),
			"output_path":     stringSchema(),
		}),
		docxToolDefinition("docx.insert_paragraph", "Insert a DOCX paragraph at start/end or before/after a location or 1-based paragraph_index and write a new DOCX file.", []string{"path", "position", "text", "output_path"}, map[string]any{
			"path":            stringSchema(),
			"position":        map[string]any{"enum": []any{"start", "end", "before", "after"}},
			"paragraph_index": integerSchema(),
			"location":        objectValueSchema(),
			"text":            stringSchema(),
			"output_path":     stringSchema(),
		}),
		docxToolDefinition("docx.delete_paragraph", "Delete one DOCX paragraph by location or 1-based paragraph_index and write a new DOCX file.", []string{"path", "output_path"}, map[string]any{
			"path":            stringSchema(),
			"paragraph_index": integerSchema(),
			"location":        objectValueSchema(),
			"output_path":     stringSchema(),
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
	definition := docxToolDefinition("docx.set_text_style", "Set one or more paragraph-level DOCX style properties by a files.read location or 1-based paragraph_index and write a new DOCX file.", []string{"path", "style", "output_path"}, map[string]any{
		"path":            stringSchema(),
		"paragraph_index": integerSchema(),
		"location":        objectValueSchema(),
		"style":           style,
		"output_path":     stringSchema(),
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
