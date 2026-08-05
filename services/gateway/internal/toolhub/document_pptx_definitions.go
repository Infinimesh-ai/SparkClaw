package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func pptxToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		pptxToolDefinition("pptx.add_slide", "Add a new PPTX slide using a layout index and write a new PPTX file.", []string{"path", "output_path"}, map[string]any{
			"path":         stringSchema(),
			"layout_index": integerSchema(),
			"title":        stringSchema(),
			"body":         stringSchema(),
			"output_path":  stringSchema(),
		}),
		pptxToolDefinition("pptx.update_slide", "Improve one existing PPTX slide by updating one or more selected text shapes from files.read evidence. Replacement text may contain line breaks. Use layout_policy=coordinated so wrapping, text-box height, verified companion backgrounds, and peer rows or cards adapt together; use preserve only when existing geometry already fits. Runtime owns layout changes. Never submit a whole slide as one replacement.", []string{"path", "slide_index", "updates", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"slide_index": integerSchema(),
			"layout_policy": map[string]any{
				"type": "string", "enum": []string{"preserve", "coordinated"},
			},
			"updates": arraySchema(strictObjectSchema([]string{"shape_index", "old_text", "text"}, map[string]any{
				"shape_index": integerSchema(),
				"old_text":    stringSchema(),
				"text":        stringSchema(),
			})),
			"output_path": stringSchema(),
		}),
		pptxToolDefinition("pptx.duplicate_slide", "Duplicate one PPTX slide by 1-based slide_index and write a new PPTX file.", []string{"path", "slide_index", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"slide_index": integerSchema(),
			"output_path": stringSchema(),
		}),
		pptxToolDefinition("pptx.delete_slide", "Delete one PPTX slide by 1-based slide_index and write a new PPTX file.", []string{"path", "slide_index", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"slide_index": integerSchema(),
			"output_path": stringSchema(),
		}),
	}
}

func pptxToolDefinition(name, description string, required []string, input map[string]any) app.ToolDefinition {
	return app.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: schema("object", required, input),
		OutputSchema: objectSchema([]string{"status", "operation", "path", "output_path", "bytes", "slides", "change_summary", "untrusted"}, map[string]any{
			"status":                        stringSchema(),
			"operation":                     stringSchema(),
			"path":                          stringSchema(),
			"output_path":                   stringSchema(),
			"bytes":                         integerSchema(),
			"slides":                        integerSchema(),
			"slide_index":                   integerSchema(),
			"inserted_slide_index":          integerSchema(),
			"layout_index":                  integerSchema(),
			"title":                         stringSchema(),
			"body":                          stringSchema(),
			"updated_shapes":                integerSchema(),
			"fitted_shapes":                 integerSchema(),
			"wrapped_shapes":                integerSchema(),
			"wrapped_shape_indexes":         arraySchema(integerSchema()),
			"layout_policy":                 stringSchema(),
			"layout_adjusted_shapes":        integerSchema(),
			"change_summary":                objectValueSchema(),
			"layout_adjusted_shape_indexes": arraySchema(integerSchema()),
			"layout_changes":                arraySchema(objectValueSchema()),
			"layout_checks":                 objectValueSchema(),
			"companion_groups_used":         integerSchema(),
			"warnings":                      stringArraySchema(),
			"untrusted":                     booleanSchema(),
		}),
		Risk:             app.RiskReversible,
		RequiresApproval: true,
		Idempotent:       false,
		TimeoutMS:        10000,
		Sandbox:          "optional",
		Audit:            "always",
	}
}
