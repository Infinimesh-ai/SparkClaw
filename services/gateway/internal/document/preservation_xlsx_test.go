package document

import "testing"

func TestXLSXUpdateRowPreservationRejectsTrailingCellChanges(t *testing.T) {
	before := xlsxPreservationRepresentation("alpha", 42, "=B2*2", "sha256:tail")
	after := xlsxPreservationRepresentation("beta", 42, "=B2*3", "sha256:changed-tail")
	edit := EditRequest{Operation: "update_row", Arguments: map[string]any{
		"sheet": "Data", "row": 2, "values": []any{"beta"},
	}}
	if _, err := ValidatePreservation(before, after, edit, nil); !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("trailing formula/hash change was not rejected: %v", err)
	}
}

func xlsxPreservationRepresentation(first string, second int, formula, tailHash string) Representation {
	cells := []any{
		map[string]any{"address": "A2", "column": 1, "value": first, "display_text": first, "raw_value": first, "value_kind": "string", "source_hash": "sha256:first"},
		map[string]any{"address": "B2", "column": 2, "value": second, "display_text": "42", "raw_value": second, "value_kind": "number", "formula": formula, "source_hash": tailHash},
	}
	return Representation{
		Format: "xlsx",
		Sheets: []map[string]any{{"name": "Data", "rows": []any{map[string]any{"index": 2, "cells": cells}}}},
		Blocks: []Block{
			{Text: first, Location: map[string]any{"path": "workbook.sheet[Data].cell[A2]", "sheet": "Data", "row_index": 2, "column_index": 1}},
			{Text: "42", Location: map[string]any{"path": "workbook.sheet[Data].cell[B2]", "sheet": "Data", "row_index": 2, "column_index": 2}},
		},
	}
}
