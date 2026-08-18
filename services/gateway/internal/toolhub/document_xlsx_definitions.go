package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func xlsxToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		xlsxToolDefinition("xlsx.update_cell", "Update one evidence-bound XLSX cell by sheet name and A1 cell address, then write a new XLSX file.", []string{"path", app.DocumentSourceSHA256Argument, "sheet", "cell", "source_cell_hash", "value", "output_path"}, map[string]any{
			"path":                           stringSchema(),
			app.DocumentSourceSHA256Argument: stringSchema(),
			"sheet":                          stringSchema(),
			"cell":                           stringSchema(),
			"source_cell_hash":               stringSchema(),
			"value":                          scalarValueSchema(),
			"output_path":                    stringSchema(),
		}),
		xlsxToolDefinition("xlsx.insert_row", "Insert one XLSX row before or after an evidence-bound 1-based row index, then write a new XLSX file.", []string{"path", app.DocumentSourceSHA256Argument, "sheet", "row", "source_row_hash", "position", "values", "output_path"}, map[string]any{
			"path":                           stringSchema(),
			app.DocumentSourceSHA256Argument: stringSchema(),
			"sheet":                          stringSchema(),
			"row":                            integerSchema(),
			"source_row_hash":                stringSchema(),
			"position":                       map[string]any{"enum": []any{"before", "after"}},
			"values":                         arraySchema(scalarValueSchema()),
			"output_path":                    stringSchema(),
		}),
		xlsxToolDefinition("xlsx.delete_row", "Delete one evidence-bound XLSX row by sheet name and 1-based row index, then write a new XLSX file.", []string{"path", app.DocumentSourceSHA256Argument, "sheet", "row", "source_row_hash", "output_path"}, map[string]any{
			"path":                           stringSchema(),
			app.DocumentSourceSHA256Argument: stringSchema(),
			"sheet":                          stringSchema(),
			"row":                            integerSchema(),
			"source_row_hash":                stringSchema(),
			"output_path":                    stringSchema(),
		}),
		xlsxToolDefinition("xlsx.update_row", "Update only the supplied leading cells of one evidence-bound XLSX row, preserve its trailing cells, then write a new XLSX file.", []string{"path", app.DocumentSourceSHA256Argument, "sheet", "row", "source_row_hash", "values", "output_path"}, map[string]any{
			"path":                           stringSchema(),
			app.DocumentSourceSHA256Argument: stringSchema(),
			"sheet":                          stringSchema(),
			"row":                            integerSchema(),
			"source_row_hash":                stringSchema(),
			"values":                         xlsxNonEmptyValuesSchema(),
			"output_path":                    stringSchema(),
		}),
		xlsxToolDefinition("xlsx.append_row", "Append one XLSX row after the evidence-bound structured end of a sheet, then write a new XLSX file.", []string{"path", app.DocumentSourceSHA256Argument, "sheet", "source_sheet_hash", "values", "output_path"}, map[string]any{
			"path":                           stringSchema(),
			app.DocumentSourceSHA256Argument: stringSchema(),
			"sheet":                          stringSchema(),
			"source_sheet_hash":              stringSchema(),
			"values":                         arraySchema(scalarValueSchema()),
			"output_path":                    stringSchema(),
		}),
	}
}

func xlsxToolDefinition(name, description string, required []string, input map[string]any) app.ToolDefinition {
	return app.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: schema("object", required, input),
		OutputSchema: objectSchema([]string{"status", "operation", "path", "output_path", "bytes", "sheet", "change_summary", "untrusted"}, map[string]any{
			"status":         stringSchema(),
			"operation":      stringSchema(),
			"path":           stringSchema(),
			"output_path":    stringSchema(),
			"bytes":          integerSchema(),
			"sheet":          stringSchema(),
			"cell":           stringSchema(),
			"row":            integerSchema(),
			"inserted_row":   integerSchema(),
			"value":          scalarValueSchema(),
			"values":         arraySchema(scalarValueSchema()),
			"changed_cells":  arraySchema(objectValueSchema()),
			"change_summary": objectValueSchema(),
			"untrusted":      booleanSchema(),
		}),
		Risk:             app.RiskReversible,
		RequiresApproval: true,
		Idempotent:       false,
		TimeoutMS:        10000,
		Sandbox:          "optional",
		Audit:            "always",
	}
}

func xlsxNonEmptyValuesSchema() map[string]any {
	schema := arraySchema(scalarValueSchema())
	schema["minItems"] = 1
	return schema
}
