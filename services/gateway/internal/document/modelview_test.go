package document

import (
	"strings"
	"testing"
)

func TestModelViewRendersSourceWithoutRuntimeRecords(t *testing.T) {
	h1 := StructureNode{ID: "d000000000000000000000001", Kind: "heading_label", Title: "Guide", Text: "# Guide\n", Order: 1, Locator: StructureLocator{Block: "document.p[1]"}, Provenance: "native", Availability: NodeAvailable}
	h2 := StructureNode{ID: "d000000000000000000000002", Kind: "heading_label", Title: "Checks", Order: 2, Dependencies: []string{h1.ID}, Locator: StructureLocator{Block: "document.p[2]"}, Availability: NodeAvailable}
	body := StructureNode{ID: "d000000000000000000000003", Kind: "paragraph", Text: "Verify authorization.\n", Order: 3, Dependencies: []string{h1.ID, h2.ID}, Locator: StructureLocator{Block: "document.p[3]"}, Provenance: "native", Availability: NodeAvailable}
	blank := StructureNode{ID: "d000000000000000000000004", Kind: "text", Text: "\n", Order: 4, Locator: StructureLocator{Block: "text.bytes[9:10]"}, Availability: NodeAvailable}
	shape := StructureNode{ID: "d000000000000000000000005", Kind: "object", Order: 5, Locator: StructureLocator{Block: "presentation.slide[2].shape[1]", Page: 2}, Availability: NodeUnsupported, Reason: "non_text_shape_requires_visual_analysis"}
	long := StructureNode{ID: "d000000000000000000000006", Kind: "paragraph", Text: "second half", Order: 6, Locator: StructureLocator{Block: "document.p[6]"}, Availability: NodeAvailable}

	view := NewModelView()
	view.Add(ModelViewFragment{Node: body})
	view.Add(ModelViewFragment{Node: h2, Context: true})
	view.Add(ModelViewFragment{Node: h1, Context: true})
	view.Add(ModelViewFragment{Node: h1, Context: true})
	view.Add(ModelViewFragment{Node: blank})
	view.Add(ModelViewFragment{Node: shape})
	view.Add(ModelViewFragment{Node: long, Partial: true, ByteStart: 20, ByteEnd: 31})
	got := view.String()
	want := strings.Join([]string{
		"# Guide [document.p[1]]",
		"## Checks [document.p[2]]",
		"[document.p[3]] Verify authorization.",
		"[presentation.slide[2].shape[1]] (unsupported: non_text_shape_requires_visual_analysis)",
		"[document.p[6], partial text bytes 20-31] second half",
	}, "\n")
	if got != want {
		t.Fatalf("model view mismatch:\n%s\nwant:\n%s", got, want)
	}
	if view.Len() != len(got) {
		t.Fatalf("Len=%d, rendered=%d", view.Len(), len(got))
	}
	for _, leaked := range []string{"d0000000", "availability", "provenance", "byte_start", "dependencies"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("runtime record %q leaked into model view: %s", leaked, got)
		}
	}
}

func TestModelViewKeepsMeaningfulTypedValues(t *testing.T) {
	cell := StructureNode{ID: "c", Kind: "cell", Text: "999", Order: 1, Locator: StructureLocator{Block: "workbook.sheet[Budget].cell[B7]"}, Availability: NodeAvailable, Values: []map[string]any{{
		"address": "B7", "cached_result": float64(999), "cached_result_available": true, "column_hidden": false, "display_text": "999",
		"display_text_source": "exceljs.cell.text", "formula": "SUM(B3:B5)", "formula_result_recalculated": false, "number_format": "",
		"row_hidden": false, "sheet_state": "visible", "type": float64(6), "value": map[string]any{"formula": "SUM(B3:B5)", "result": float64(999)},
	}}}
	line := RenderModelView([]ModelViewFragment{{Node: cell}})
	want := `[workbook.sheet[Budget].cell[B7]] 999 {"cached_result":999,"cached_result_available":true,"formula":"SUM(B3:B5)","formula_result_recalculated":false}`
	if line != want {
		t.Fatalf("typed values:\n%s\nwant:\n%s", line, want)
	}
	hidden := cell
	hidden.Values = []map[string]any{{"row_hidden": true, "value": float64(0), "display_text": "0"}}
	hidden.Text = "0"
	if got := RenderModelView([]ModelViewFragment{{Node: hidden}}); got != `[workbook.sheet[Budget].cell[B7]] 0 {"row_hidden":true}` {
		t.Fatalf("hidden row or zero value lost: %s", got)
	}
	row := StructureNode{ID: "r", Kind: "table_row", Text: "Week 1\tHTTP\tObserve", Order: 2, Locator: StructureLocator{Block: "document.table[1].row[2]"}, Availability: NodeAvailable, Values: []map[string]any{
		{"column": float64(1), "is_merge_alias": false, "merge_anchor_column": float64(1), "merge_anchor_row": float64(2), "text": "Week 1"},
		{"column": float64(2), "is_merge_alias": true, "merge_anchor_column": float64(2), "merge_anchor_row": float64(1), "text": "HTTP"},
	}}
	if got := RenderModelView([]ModelViewFragment{{Node: row}}); got != `[document.table[1].row[2]] Week 1 | HTTP | Observe {"column":2,"merged_from":"r1c2"}` {
		t.Fatalf("table row: %s", got)
	}
}

func TestModelViewLocationsAndVisualsDropRuntimeIdentity(t *testing.T) {
	locations := ModelViewSourceLocations([]map[string]any{{
		"block": "document.p[641]", "parent_block": "document.p[580]", "title": "最后的自我检查", "page": float64(0), "node_id": "dabc", "structure_source": "native_style",
		"span": map[string]any{"first_block": "document.p[641]", "last_block": "document.p[649]", "ordinal_start": float64(690), "ordinal_end": float64(699)},
	}})
	if locations != `- block=document.p[641] parent_block=document.p[580] range=document.p[641]..document.p[649] title="最后的自我检查"` {
		t.Fatalf("locations: %s", locations)
	}
	visual := ModelViewVisualEvidence([]map[string]any{{"block": "presentation.slide[1]", "page": float64(1), "status": "processed", "observation": "BETA to ALPHA", "png_sha256": "x", "pdf_sha256": "y", "source_sha256": "z", "model_call_id": "m", "provenance": "rendered_slide_model"}})
	if visual != `- {"block":"presentation.slide[1]","observation":"BETA to ALPHA","page":1,"status":"processed"}` {
		t.Fatalf("visual: %s", visual)
	}
}

func TestModelViewNodeBytesMatchesRenderedLine(t *testing.T) {
	h := StructureNode{ID: "h", Kind: "heading_label", Title: "Data", Locator: StructureLocator{Block: "sheet"}, Availability: NodeAvailable}
	body := StructureNode{ID: "b", Kind: "paragraph", Text: "Body text.\n", Dependencies: []string{"h"}, Order: 1, Locator: StructureLocator{Block: "document.p[2]"}, Availability: NodeAvailable}
	rendered := RenderModelView([]ModelViewFragment{{Node: h}, {Node: body}})
	if got := ModelViewNodeBytes(h, 1) + ModelViewNodeBytes(body, 1); got != len(rendered)+1 {
		t.Fatalf("estimate %d != delivered %d", got, len(rendered)+1)
	}
}
