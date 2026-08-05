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
