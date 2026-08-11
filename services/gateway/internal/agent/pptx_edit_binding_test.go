package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestPPTXSlideOperationContextExposesExactShapeEvidence(t *testing.T) {
	context := documentOperationContextEvidence(map[string]any{
		"evidence_blocks": []any{
			map[string]any{"type": "slide_text", "text": "Third title", "location": map[string]any{"path": "presentation.slide[3].shape[1]"}},
		},
		"blocks": []any{
			map[string]any{"type": "shape_text", "text": "Third title", "location": map[string]any{"slide_index": 3, "shape_index": 1, "block_type": "shape_text"}},
			map[string]any{"type": "shape_text", "text": "Third body", "location": map[string]any{"slide_index": 3, "shape_index": 7, "block_type": "shape_text"}},
		},
		"enrichment": map[string]any{"layout": map[string]any{"shapes": []any{
			map[string]any{"slide_index": 3, "shape_index": 7, "companion_group_id": "slide:3:band:6", "companion_role": "body", "text_style": map[string]any{
				"font_size_pt": 16.5, "single_line_capacity_visual_units": 22.4, "single_line_fit_ratio": 0.75,
			}},
		}}},
	})
	for _, expected := range []string{
		"PPTXSlideOperationContext:",
		"slide_index=3:",
		`shape_index=1 old_text="Third title"`,
		`shape_index=7 old_text="Third body"`,
		"exact JSON keys shape_index and text",
		"replacement_text is not a schema key",
		"layout_policy=coordinated",
		"only include shapes whose replacement text differs from current_text",
		"Runtime rejects changes limited to punctuation, symbols, spacing, or letter case",
		"font_size_pt=16.5",
		"companion_group=slide:3:band:6 companion_role=body",
	} {
		if !strings.Contains(context, expected) {
			t.Fatalf("PPTX operation context omitted %q:\n%s", expected, context)
		}
	}
}

func TestExplicitSlideIndex(t *testing.T) {
	for _, test := range []struct {
		query string
		want  int
	}{
		{query: "把第三页完善一下", want: 3},
		{query: "完善第12张幻灯片", want: 12},
		{query: "Improve slide 8", want: 8},
		{query: "修改第二十一页", want: 21},
	} {
		got := explicitSlideIndexes(test.query)
		if len(got) == 0 || got[0] != test.want {
			t.Fatalf("explicitSlideIndexes(%q) = %#v, want first index %d", test.query, got, test.want)
		}
	}
}

func TestBindPPTXSlideUpdateArgumentsUsesOwnerOrdinalAndReadEvidence(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSession("pptx binding")
	run := app.AgentRun{
		ID:        app.NewID("run"),
		SessionID: session.ID,
		StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			Route: app.RouteDecision{Slots: app.RouteSlots{Query: "把第三页完善一下"}},
		},
	}
	st.SaveRun(run)
	st.SaveToolCall(app.ToolCall{
		ID: app.NewID("tc"), SessionID: session.ID, RunID: run.ID, Tool: "files.read", Status: "completed",
		Arguments: map[string]any{"path": "uploads/deck.pptx"},
		Result: map[string]any{
			"rel_path": "uploads/deck.pptx",
			"document": map[string]any{"format": "pptx", "blocks": []any{
				map[string]any{"type": "shape_text", "text": "Exact third title", "location": map[string]any{"slide_index": 3, "shape_index": 1, "block_type": "shape_text"}},
				map[string]any{"type": "shape_text", "text": "Exact third body", "location": map[string]any{"slide_index": 3, "shape_index": 7, "block_type": "shape_text"}},
			}},
		},
	})
	runtime := Runtime{store: st}
	args := runtime.bindPPTXEditArguments(run, "update_slide", map[string]any{
		"path": "uploads/deck.pptx", "slide_index": 2,
		"updates": []any{
			map[string]any{"shape_index": 1, "new_text": "Improved title"},
			map[string]any{"shape_index": 7, "old_text": "Model supplied evidence", "text": "Improved body"},
		},
	})
	if intLikeValue(args["slide_index"]) != 3 {
		t.Fatalf("owner ordinal did not override guessed slide index: %#v", args)
	}
	updates := anySlice(args["updates"])
	first, _ := anyMap(updates[0])
	second, _ := anyMap(updates[1])
	if first["old_text"] != "Exact third title" {
		t.Fatalf("missing old_text was not grounded from structured read: %#v", first)
	}
	if first["text"] != "Improved title" || first["new_text"] != nil {
		t.Fatalf("new_text alias was not normalized before schema validation: %#v", first)
	}
	if second["old_text"] != "Model supplied evidence" {
		t.Fatalf("model-supplied old_text must remain subject to stale-evidence validation: %#v", second)
	}
}
