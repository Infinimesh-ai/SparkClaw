package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentReadR8KeepsNativeLocationAndVisualEvidenceInFinalBudget(t *testing.T) {
	output := map[string]any{"source_sha256": "source", "source_locations": []any{map[string]any{"title": "Final", "block": "p[11]", "parent_block": "p[9]"}}, "visual_coverage": map[string]any{"status": "partial", "missing_pages": []int{3}}, "visual_evidence": []any{map[string]any{"page": 2, "description": "37 units", "png_sha256": "bound"}}, "dependencies": []any{map[string]any{"text": "units"}}, "nodes": []any{map[string]any{"text": "source value", "values": []any{map[string]any{"value": 37}}}}}
	full, truncated := scopedDocumentFinalEvidence(output, "summary text", 10000)
	for _, text := range []string{"p[11]", "p[9]", "37 units", "missing_pages", "source value"} {
		if !strings.Contains(full, text) || truncated {
			t.Fatal("lost source-bound context", text, full)
		}
	}
	short, truncated := scopedDocumentFinalEvidence(output, "summary text", 100)
	if !truncated || strings.Contains(short, "37 units") || len(short) > 100 {
		t.Fatal("partially projected visual evidence became a fact", short)
	}
}

func TestDocumentReadR8BindsVisualsAndSummaryVersionWithoutChangingR7(t *testing.T) {
	for _, revision := range []int{6, 7, 8} {
		state := scopedReadState()
		state.Plan.ProfileRevision = revision
		args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentSummarize}, map[string]any{"include_visuals": true, "include_source_locations": true, "contract_version": 99})
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if revision == 7 {
			want = 3
		}
		if revision == 8 {
			want = 4
		}
		if args["contract_version"] != want || (args["include_visuals"] == true) != (revision == 8) || (args["include_source_locations"] == true) != (revision == 8) {
			t.Fatal("runtime/frozen contract violated", revision, args)
		}
	}
}

func TestDocumentReadR8SearchDoesNotFreezeContentMode(t *testing.T) {
	for _, revision := range []int{7, 8} {
		state := scopedReadState()
		state.Plan.ProfileRevision = revision
		search := scopedReadOutcome(true, "")
		search.Refs[0].Kind = documentSearchResourceKind
		search.Refs[0].Attributes["capability"] = app.ToolCapabilityDocumentSearch
		search.Refs[0].Attributes["task_mode"] = "targeted"
		delete(search.Refs[0].Attributes, "scope_selector")
		assessment := (documentReadProfile{}).Assess(state, search)
		if assessment.Status != app.AssessmentNeedsMoreEvidence {
			t.Fatal("search stopped content selection", assessment)
		}
		_, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentSummarize}, map[string]any{})
		if (err == nil) != (revision == 8) {
			t.Fatal("incorrect frozen mode boundary", revision, err)
		}
		content := scopedReadOutcome(true, "")
		content.Refs[0].Attributes["scope_extent"] = "whole_document"
		content.Refs[0].Attributes["task_mode"] = "whole_summary"
		if revision == 8 {
			(documentReadProfile{}).Assess(state, content)
			if state.Route.Facts[documentReadFactPrefix+"task_mode"] != "whole_summary" {
				t.Fatal("actual read did not freeze mode")
			}
			if _, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentReadRange}, map[string]any{"task_mode": "targeted"}); err == nil {
				t.Fatal("frozen content scope changed")
			}
		}
	}
}
