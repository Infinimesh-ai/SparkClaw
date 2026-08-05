package document

import "testing"

func TestNormalizeXLSXPreservesTypedCellsAndStableSourceHashes(t *testing.T) {
	raw := map[string]any{"sheets": []any{map[string]any{
		"name": "Data", "index": 1, "state": "visible", "rows": []any{map[string]any{
			"index": 2, "hidden": false, "height": 18, "cells": []any{
				map[string]any{
					"address": "B2", "column": 2, "value": "42.00", "display_text": "42.00",
					"raw_value": 42, "value_kind": "number", "number_format": "0.00",
					"style": map[string]any{"font": map[string]any{"bold": true}},
				},
				map[string]any{
					"address": "C2", "column": 3, "value": "84", "display_text": "84",
					"raw_value": 84, "value_kind": "formula", "formula": "B2*2",
					"merge_anchor": "C2",
				},
			},
		}},
	}}}
	metadata := Metadata{Path: "/workspace/book.xlsx", Relative: "book.xlsx", Format: "xlsx"}
	first, err := Normalize(metadata, "small_file_v1", "42.00\t84", raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Normalize(metadata, "small_file_v1", "42.00\t84", raw)
	if err != nil {
		t.Fatal(err)
	}

	row := mapSlice(first.Sheets[0]["rows"])[0]
	cells := mapSlice(row["cells"])
	if cells[0]["value_kind"] != "number" || cells[0]["raw_value"] != 42 || cells[0]["display_text"] != "42.00" || cells[0]["number_format"] != "0.00" {
		t.Fatalf("typed numeric cell was not preserved: %#v", cells[0])
	}
	if cells[1]["value_kind"] != "formula" || cells[1]["formula"] != "B2*2" || cells[1]["raw_value"] != 84 {
		t.Fatalf("formula cell was not preserved: %#v", cells[1])
	}
	for name, value := range map[string]any{
		"cell source hash":  cells[0]["source_hash"],
		"cell style hash":   cells[0]["style_hash"],
		"row source hash":   row["source_hash"],
		"sheet source hash": first.Sheets[0]["source_hash"],
	} {
		if hash, ok := value.(string); !ok || len(hash) != len("sha256:")+64 {
			t.Fatalf("%s is missing: %#v", name, value)
		}
	}
	secondRow := mapSlice(second.Sheets[0]["rows"])[0]
	if row["source_hash"] != secondRow["source_hash"] || first.Sheets[0]["source_hash"] != second.Sheets[0]["source_hash"] {
		t.Fatalf("XLSX hashes are not stable: first=%#v second=%#v", row, secondRow)
	}
}
