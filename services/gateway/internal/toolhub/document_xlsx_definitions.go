package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func xlsxToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		xlsxToolDefinition("xlsx.update_cell", "Update one XLSX cell by sheet name and A1 cell address, then write a new XLSX file.", []string{"path", "sheet", "cell", "value", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"sheet":       stringSchema(),
			"cell":        stringSchema(),
			"value":       scalarValueSchema(),
			"output_path": stringSchema(),
		}),
		xlsxToolDefinition("xlsx.insert_row", "Insert one XLSX row before or after a 1-based row index, then write a new XLSX file.", []string{"path", "sheet", "row", "position", "values", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"sheet":       stringSchema(),
			"row":         integerSchema(),
			"position":    map[string]any{"enum": []any{"before", "after"}},
			"values":      arraySchema(scalarValueSchema()),
			"output_path": stringSchema(),
		}),
		xlsxToolDefinition("xlsx.delete_row", "Delete one XLSX row by sheet name and 1-based row index, then write a new XLSX file.", []string{"path", "sheet", "row", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"sheet":       stringSchema(),
			"row":         integerSchema(),
			"output_path": stringSchema(),
		}),
		xlsxToolDefinition("xlsx.update_row", "Replace one XLSX row's leading cells with provided values, then write a new XLSX file.", []string{"path", "sheet", "row", "values", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"sheet":       stringSchema(),
			"row":         integerSchema(),
			"values":      arraySchema(scalarValueSchema()),
			"output_path": stringSchema(),
		}),
		xlsxToolDefinition("xlsx.append_row", "Append one XLSX row to the end of a sheet, then write a new XLSX file.", []string{"path", "sheet", "values", "output_path"}, map[string]any{
			"path":        stringSchema(),
			"sheet":       stringSchema(),
			"values":      arraySchema(scalarValueSchema()),
			"output_path": stringSchema(),
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
