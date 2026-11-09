package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	modelViewHeadingID = "d0266f5eb43687664a43b33e2"
	modelViewBodyID    = "d49aaa4fd76065909c70434f7"
)

func modelViewReadOutput() map[string]any {
	heading := map[string]any{"id": modelViewHeadingID, "kind": "heading_label", "title": "最后的自我检查", "text": "最后的自我检查", "order": 846, "parent_id": "dc18759c994c4efc4e7820157", "provenance": "native", "availability": "available", "locator": map[string]any{"block": "document.p[641]"}}
	body := map[string]any{"id": modelViewBodyID, "kind": "paragraph", "text": "我能解释 Playwright 的对象层级。", "order": 847, "parent_id": "dc18759c994c4efc4e7820157", "provenance": "native", "availability": "available", "dependencies": []any{modelViewHeadingID}, "locator": map[string]any{"block": "document.p[645]"}, "byte_start": 0, "byte_end": 39, "complete": true, "whole_node": true}
	return map[string]any{
		"content":      "[" + modelViewBodyID + " document.p[645]]\n我能解释 Playwright 的对象层级。",
		"nodes":        []any{body},
		"dependencies": []any{heading},
		"source_locations": []any{map[string]any{"block": "document.p[641]", "parent_block": "document.p[580]", "title": "最后的自我检查", "page": 0, "node_id": "dfbe4c8c8a1b28e0c0a7c3f9a", "structure_source": "native_style",
			"span": map[string]any{"first_block": "document.p[641]", "last_block": "document.p[649]", "ordinal_start": 690, "ordinal_end": 699}}},
		"source_location_relations": []any{`"最后的自我检查" (document.p[641]) comes AFTER "附录 C" (document.p[630]) in the original source.`},
		"source_sha256":             "c810280636163852e77bb01dfebe02b9a5d1d4ab02f4a81743edb5ac5f6c0bac",
		"structure_generation":      "bf42727dfab350b80e5bf61dcca337e94813c49aa9b679ec629d0a19b1b004f8",
	}
}

func modelViewState(revision int) *app.WorkflowState {
	state := scopedReadState()
	state.Plan.ProfileRevision = revision
	return state
}

func TestRevision10FinalEvidenceCarriesSourceNotRuntimeRecords(t *testing.T) {
	output := modelViewReadOutput()
	got, truncated := documentFinalEvidence(modelViewState(10), output, stringValue(output["content"]), 1<<20)
	if truncated {
		t.Fatal("small evidence was truncated")
	}
	for _, want := range []string{"# 最后的自我检查 [document.p[641]]", "[document.p[645]] 我能解释 Playwright 的对象层级。", "block=document.p[641] parent_block=document.p[580]", "comes AFTER"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	for _, leaked := range []string{modelViewHeadingID, modelViewBodyID, "dfbe4c8c8a1b28e0c0a7c3f9a", "c810280636163852", "bf42727dfab350b8", `"availability"`, `"whole_node"`, "ordinal_start"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("runtime record %q reached the model:\n%s", leaked, got)
		}
	}
	// The same output under revision 9 keeps its persisted JSON projection.
	frozen, _ := documentFinalEvidence(modelViewState(9), output, stringValue(output["content"]), 1<<20)
	legacy, _ := scopedDocumentFinalEvidence(output, stringValue(output["content"]), 1<<20)
	if frozen != legacy || !strings.Contains(frozen, modelViewBodyID) {
		t.Fatal("revision 9 final projection changed")
	}
	if len(got) >= len(frozen) {
		t.Fatalf("model view %d bytes is not smaller than the frozen JSON %d bytes", len(got), len(frozen))
	}
}

func TestRevision10FinalEvidenceTruncatesExplicitly(t *testing.T) {
	output := modelViewReadOutput()
	delete(output, "source_locations")
	delete(output, "source_location_relations")
	got, truncated := documentFinalEvidence(modelViewState(10), output, "", 20)
	if !truncated || strings.Contains(got, "Playwright") {
		t.Fatalf("budget overflow must be explicit, got %q truncated=%v", got, truncated)
	}
}

func TestRevision10TaskSelectionUsesAliasesAndLocators(t *testing.T) {
	output := modelViewReadOutput()
	heading, _ := anyMap(anySlice(output["dependencies"])[0])
	body, _ := anyMap(anySlice(output["nodes"])[0])
	lookup := map[string]map[string]any{modelViewHeadingID: heading, modelViewBodyID: body}
	payload, err := documentTaskBatchViewPayload([]documentTaskCandidate{{Node: body}}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, "n1: [document.p[645]] 我能解释 Playwright 的对象层级。") || !strings.Contains(payload, "# 最后的自我检查 [document.p[641]]") {
		t.Fatalf("selection payload lost source:\n%s", payload)
	}
	if strings.Contains(payload, modelViewBodyID) || strings.Contains(payload, modelViewHeadingID) {
		t.Fatalf("selection payload leaked node IDs:\n%s", payload)
	}
}

func TestDocumentReadRevision9RemainsResolvable(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	for _, revision := range []int{9, 10} {
		profile, err := registry.Get(app.WorkflowDocumentRead, revision)
		if err != nil || profile.Revision() != revision {
			t.Fatalf("document.read revision %d unavailable: %v", revision, err)
		}
	}
	plan := toolPlan{Capability: app.ToolCapabilityDocumentSummarize}
	for revision, want := range map[int]int{9: 4, 10: 5} {
		state := modelViewState(revision)
		args, err := bindScopedDocumentReadArguments(state, plan, map[string]any{"model_view": true})
		if err != nil || intLikeValue(args["contract_version"]) != want {
			t.Fatalf("revision %d summary contract = %v (%v)", revision, args["contract_version"], err)
		}
		// model_view is runtime-bound: a model-supplied value never survives
		// under revision 9, and revision 10 binds it for every content tool.
		for _, capability := range []string{app.ToolCapabilityDocumentSummarize, app.ToolCapabilityDocumentOutline, app.ToolCapabilityDocumentReadRange} {
			bound, _ := bindScopedDocumentReadArguments(modelViewState(revision), toolPlan{Capability: capability}, map[string]any{"model_view": true, "task_mode": "targeted", "scope_extent": "selected_range", "node_ids": []any{"x"}})
			if got, _ := bound["model_view"].(bool); got != (revision >= 10) {
				t.Fatalf("revision %d %s model_view=%v", revision, capability, bound["model_view"])
			}
		}
	}
}
