package document

import "testing"

func TestDOCXStylePreservationRejectsReadbackMismatch(t *testing.T) {
	before := docxStyleRepresentation("Normal", map[string]any{
		"text": "Title", "start": 0, "end": 5, "bold": nil, "italic": true,
		"font_size_pt": nil, "effective_bold": false, "effective_font_size_pt": 11.0,
	})
	matches := []Match{{Text: "Title", Location: map[string]any{"path": "document.p[1]", "paragraph_index": 1}}}
	tests := []struct {
		name  string
		style map[string]any
		run   map[string]any
	}{
		{
			name:  "bold",
			style: map[string]any{"bold": true},
			run: map[string]any{
				"text": "Title", "start": 0, "end": 5, "bold": nil, "italic": true,
				"font_size_pt": nil, "effective_bold": false, "effective_font_size_pt": 11.0,
			},
		},
		{
			name:  "font_size",
			style: map[string]any{"font_size_pt": 18},
			run: map[string]any{
				"text": "Title", "start": 0, "end": 5, "bold": nil, "italic": true,
				"font_size_pt": 17.0, "effective_bold": false, "effective_font_size_pt": 17.0,
			},
		},
		{
			name:  "unrequested_italic",
			style: map[string]any{"bold": true},
			run: map[string]any{
				"text": "Title", "start": 0, "end": 5, "bold": true, "italic": false,
				"font_size_pt": nil, "effective_bold": true, "effective_font_size_pt": 11.0,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			after := docxStyleRepresentation("Normal", test.run)
			edit := EditRequest{Operation: "set_text_style", Arguments: map[string]any{"paragraph_index": 1, "style": test.style}}
			if _, err := ValidatePreservation(before, after, edit, matches); !IsErrorCode(err, CodePreservationMismatch) {
				t.Fatalf("expected preservation mismatch, got %v", err)
			}
		})
	}
}

func TestDOCXTextReplacementPreservationRejectsRunFlattening(t *testing.T) {
	location := map[string]any{"part": "document", "part_kind": "body", "block_type": "paragraph", "paragraph_index": 1, "path": "document.p[1]"}
	run := func(text string, bold, italic bool) map[string]any {
		return map[string]any{
			"text": text, "bold": bold, "italic": italic, "underline": nil, "font_name": "Arial",
			"font_size_pt": 12.0, "font_color": "112233", "effective_bold": bold,
			"effective_font_size_pt": 12.0, "relationship_id": "", "boundaries": []any{},
		}
	}
	representation := func(text string, runs []map[string]any, leftIndent int) Representation {
		return Representation{
			Format: "docx",
			Blocks: []Block{{ID: "block", Kind: "paragraph", Text: text, Location: location}},
			Paragraphs: []map[string]any{{
				"index": 1, "text": text, "raw_text": text, "style": "Normal", "part_kind": "body",
				"format": map[string]any{"left_indent": leftIndent}, "unsupported_boundaries": []any{},
				"location": location, "runs": runs,
			}},
		}
	}
	before := representation("Alpha target omega", []map[string]any{
		run("Alpha ", true, false), run("target", true, false), run(" omega", false, true),
	}, 0)
	edit := EditRequest{Operation: "replace_text", Arguments: map[string]any{
		"replacements": []any{map[string]any{"find": "target", "replace": "replacement"}},
	}}
	matches := []Match{{Text: "target", Location: location}}

	valid := representation("Alpha replacement omega", []map[string]any{
		run("Alpha ", true, false), run("replacement", true, false), run(" omega", false, true),
	}, 0)
	if _, err := ValidatePreservation(before, valid, edit, matches); err != nil {
		t.Fatalf("valid run-preserving replacement failed: %v", err)
	}

	flattened := representation("Alpha replacement omega", []map[string]any{
		run("Alpha replacement omega", true, false), run("", true, false), run("", false, true),
	}, 0)
	if _, err := ValidatePreservation(before, flattened, edit, matches); !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("flattened sibling runs passed preservation: %v", err)
	}

	formatChanged := representation("Alpha replacement omega", []map[string]any{
		run("Alpha ", true, false), run("replacement", false, false), run(" omega", false, true),
	}, 0)
	if _, err := ValidatePreservation(before, formatChanged, edit, matches); !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("changed target-run formatting passed preservation: %v", err)
	}

	paragraphChanged := representation("Alpha replacement omega", []map[string]any{
		run("Alpha ", true, false), run("replacement", true, false), run(" omega", false, true),
	}, 720)
	if _, err := ValidatePreservation(before, paragraphChanged, edit, matches); !IsErrorCode(err, CodePreservationMismatch) {
		t.Fatalf("changed paragraph properties passed preservation: %v", err)
	}
}

func docxStyleRepresentation(style string, run map[string]any) Representation {
	location := map[string]any{"part": "document", "part_kind": "body", "block_type": "paragraph", "paragraph_index": 1, "path": "document.p[1]"}
	return Representation{
		Format: "docx",
		Blocks: []Block{{ID: "block", Kind: "paragraph", Text: "Title", Location: location}},
		Paragraphs: []map[string]any{{
			"index": 1, "text": "Title", "style": style, "part_kind": "body",
			"location": location, "runs": []map[string]any{run},
		}},
	}
}
