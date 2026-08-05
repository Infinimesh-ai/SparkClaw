package document

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func normalizeSheets(documentID string, values []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for sheetIndex, value := range values {
		sheet := cloneMap(value)
		name := stringValue(sheet["name"])
		index := intValue(sheet["index"])
		if index <= 0 {
			index = sheetIndex + 1
			sheet["index"] = index
		}
		state := strings.ToLower(strings.TrimSpace(stringValue(sheet["state"])))
		if state == "" {
			state = "visible"
		}
		sheet["state"] = state
		sheet["hidden"] = state != "visible"
		sheetID := stableID("sheet", documentID+"\x00"+fmt.Sprintf("%d:%s", index, name))
		sheet["id"] = sheetID
		rows := mapSlice(sheet["rows"])
		for rowOffset, row := range rows {
			rowIndex := intValue(row["index"])
			if rowIndex <= 0 {
				rowIndex = rowOffset + 1
				row["index"] = rowIndex
			}
			row["hidden"] = xlsxBoolValue(row["hidden"])
			row["id"] = stableID("row", sheetID+"\x00"+strconv.Itoa(rowIndex))
			cells := mapSlice(row["cells"])
			for cellOffset, cell := range cells {
				address := strings.ToUpper(strings.TrimSpace(stringValue(cell["address"])))
				if address == "" {
					address = fmt.Sprintf("R%dC%d", rowIndex, cellOffset+1)
				}
				column := intValue(cell["column"])
				if column <= 0 {
					column = cellOffset + 1
				}
				cell["address"] = address
				cell["column"] = column
				normalizeXLSXCellValue(cell)
				style := mapValue(cell["style"])
				if len(style) == 0 {
					style = mapValue(cell["style_hint"])
				}
				cell["style_hash"] = xlsxSemanticHash(style)
				cell["id"] = stableID("cell", sheetID+"\x00"+address)
				cell["location"] = map[string]any{
					"part": "workbook", "block_type": "cell", "sheet": name, "sheet_index": index,
					"row_index": rowIndex, "column_index": column, "cell": address,
					"path": fmt.Sprintf("workbook.sheet[%s].cell[%s]", name, address),
				}
				cell["source_hash"] = xlsxSemanticHash(xlsxCellHashInput(cell))
			}
			row["cells"] = cells
			row["source_hash"] = xlsxSemanticHash(map[string]any{
				"index": rowIndex, "hidden": row["hidden"], "height": row["height"], "cells": xlsxRowHashCells(cells),
			})
		}
		sheet["rows"] = rows
		sheet["source_hash"] = xlsxSemanticHash(map[string]any{
			"name": name, "index": index, "state": state, "rows": xlsxSheetHashRows(rows),
		})
		out = append(out, sheet)
	}
	return out
}

func normalizeXLSXCellValue(cell map[string]any) {
	display := firstString(cell["display_text"], cell["value"])
	cell["display_text"] = display
	cell["value"] = display
	if _, ok := cell["raw_value"]; !ok {
		cell["raw_value"] = display
	}
	kind := strings.ToLower(strings.TrimSpace(stringValue(cell["value_kind"])))
	if kind == "" {
		switch cell["raw_value"].(type) {
		case nil:
			kind = "blank"
		case bool:
			kind = "boolean"
		case float32, float64, int, int32, int64:
			kind = "number"
		default:
			kind = "string"
		}
	}
	cell["value_kind"] = kind
	cell["formula"] = strings.TrimSpace(stringValue(cell["formula"]))
	cell["number_format"] = stringValue(cell["number_format"])
	cell["hidden"] = xlsxBoolValue(cell["hidden"])
	cell["merge_anchor"] = strings.ToUpper(strings.TrimSpace(stringValue(cell["merge_anchor"])))
}

func xlsxBoolValue(value any) bool {
	switch current := value.(type) {
	case bool:
		return current
	case string:
		return strings.EqualFold(strings.TrimSpace(current), "true")
	default:
		return false
	}
}

func xlsxCellHashInput(cell map[string]any) map[string]any {
	return map[string]any{
		"address": cell["address"], "column": cell["column"], "value_kind": cell["value_kind"],
		"raw_value": cell["raw_value"], "formula": cell["formula"], "number_format": cell["number_format"],
		"hidden": cell["hidden"], "style_hash": cell["style_hash"], "merge_anchor": cell["merge_anchor"],
	}
}

func xlsxRowHashCells(cells []map[string]any) []any {
	out := make([]any, 0, len(cells))
	for _, cell := range cells {
		out = append(out, xlsxCellHashInput(cell))
	}
	return out
}

func xlsxSheetHashRows(rows []map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{"index": row["index"], "source_hash": row["source_hash"]})
	}
	return out
}

func xlsxSemanticHash(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}
