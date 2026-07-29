package document

import "testing"

func TestValidatePreservationAllowsOnlyReportedCoordinatedPPTXShapes(t *testing.T) {
	before := pptxPreservationRepresentation("old", 100, 0)
	after := pptxPreservationRepresentation("new", 200, 0)
	edit, matches, details := pptxCoordinatedPreservationInputs()

	if _, err := ValidatePreservation(before, after, edit, matches, details); err != nil {
		t.Fatalf("reported coordinated layout change was rejected: %v", err)
	}
}

func TestValidatePreservationRejectsUnreportedPPTXLayoutChange(t *testing.T) {
	before := pptxPreservationRepresentation("old", 100, 0)
	after := pptxPreservationRepresentation("new", 200, 1)
	edit, matches, details := pptxCoordinatedPreservationInputs()

	if _, err := ValidatePreservation(before, after, edit, matches, details); !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("unreported companion layout mutation did not fail closed: %v", err)
	}
}

func TestValidatePreservationAllowsReportedCompanionGeometryButNotText(t *testing.T) {
	before := pptxPreservationRepresentation("old", 100, 0)
	after := pptxPreservationRepresentation("new", 200, 1)
	companionPath := "presentation.slide[1].shape[2]"
	before.Blocks = append(before.Blocks, Block{
		Kind: "shape_text", Text: "label", Location: map[string]any{
			"slide_index": 1, "shape_index": 2, "path": companionPath,
		}, Format: map[string]any{"x": 0},
	})
	after.Blocks = append(after.Blocks, Block{
		Kind: "shape_text", Text: "label", Location: map[string]any{
			"slide_index": 1, "shape_index": 2, "path": companionPath,
		}, Format: map[string]any{"x": 1},
	})
	edit, matches, details := pptxCoordinatedPreservationInputs()
	details["layout_adjusted_shape_indexes"] = []any{1, 2}
	details["layout_changes"] = append(mapSlice(details["layout_changes"]), map[string]any{
		"shape_index": 2,
		"before":      map[string]any{"x": 0, "y": 0, "width": 100, "height": 20, "font_size_pt": 16.5, "word_wrap": false},
		"after":       map[string]any{"x": 1, "y": 0, "width": 100, "height": 20, "font_size_pt": 16.5, "word_wrap": false},
	})

	if _, err := ValidatePreservation(before, after, edit, matches, details); err != nil {
		t.Fatalf("reported companion geometry was rejected: %v", err)
	}
	after.Blocks[1].Text = "changed label"
	if _, err := ValidatePreservation(before, after, edit, matches, details); !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("companion text mutation was incorrectly allowed: %v", err)
	}
}

func pptxPreservationRepresentation(text string, targetWidth, unrelatedX int) Representation {
	path := "presentation.slide[1].shape[1]"
	shape := func(index, x, width int) map[string]any {
		return map[string]any{
			"slide_index": 1, "shape_index": index, "x": x, "y": 0, "width": width, "height": 20,
			"text": text, "text_style": map[string]any{
				"font_size_pt": 16.5, "word_wrap": false, "visual_units": 3.0,
				"single_line_capacity_visual_units": 10.0, "single_line_fit_ratio": 0.3,
			},
		}
	}
	return Representation{
		Format: "pptx",
		Blocks: []Block{{Kind: "shape_text", Text: text, Location: map[string]any{
			"slide_index": 1, "shape_index": 1, "path": path,
		}}},
		Slides: []map[string]any{{"index": 1, "items": []any{map[string]any{
			"shape_index": 1, "type": "text", "text": text, "path": path,
		}}}},
		Enrichment: map[string]any{
			"assets":      map[string]any{"images": []any{}, "charts": []any{}, "embedded_objects": []any{}},
			"annotations": map[string]any{"comments": []any{}, "notes": []any{}, "hyperlinks": []any{}},
			"layout": map[string]any{
				"sections": []any{}, "page_settings": []any{}, "slide_layouts": []any{}, "merged_ranges": []any{},
				"shapes": []any{shape(1, 0, targetWidth), shape(2, unrelatedX, 100)}, "companion_groups": []any{}, "page_markers": []any{},
			},
			"coverage": map[string]any{"assets": "complete", "annotations": "complete", "layout": "complete"},
		},
	}
}

func pptxCoordinatedPreservationInputs() (EditRequest, []Match, map[string]any) {
	path := "presentation.slide[1].shape[1]"
	edit := EditRequest{Operation: "update_slide", Arguments: map[string]any{
		"slide_index": 1, "updates": []any{map[string]any{"shape_index": 1, "old_text": "old", "text": "new"}},
	}}
	matches := []Match{{Kind: "shape", Text: "old", Location: map[string]any{"slide_index": 1, "shape_index": 1, "path": path}}}
	state := func(width int) map[string]any {
		return map[string]any{"x": 0, "y": 0, "width": width, "height": 20, "font_size_pt": 16.5, "word_wrap": false}
	}
	details := map[string]any{
		"layout_policy": "coordinated", "layout_adjusted_shape_indexes": []any{1},
		"layout_changes": []any{map[string]any{"shape_index": 1, "before": state(100), "after": state(200)}},
	}
	return edit, matches, details
}
