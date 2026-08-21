package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDOCXDecisionEvidencePrioritizesLateExplicitTargets(t *testing.T) {
	output := syntheticDOCXDecisionOutput(180)
	entries := syntheticDOCXDecisionEntries()

	english := projectDOCXDecisionEvidence(output, app.RouteDecision{
		Slots: app.RouteSlots{Query: `Replace paragraph 179 in report.docx`, TargetRef: "report.docx"},
		Facts: map[string]string{"document_format": app.DocumentFormatDOCX},
	}, entries, 2400)
	chinese := projectDOCXDecisionEvidence(output, app.RouteDecision{
		Slots: app.RouteSlots{Query: `替换 report.docx 第179段`, TargetRef: "report.docx"},
		Facts: map[string]string{"document_format": app.DocumentFormatDOCX},
	}, entries, 2400)
	for language, evidence := range map[string]string{"english": english, "chinese": chinese} {
		if !strings.Contains(evidence, `"candidate_id":"document.p[179]"`) || !strings.Contains(evidence, `"prioritizing_anchors":["paragraph:179"]`) {
			t.Fatalf("%s evidence omitted the late target: %s", language, evidence)
		}
		if len([]byte(evidence)) > 2400 || !utf8.ValidString(evidence) {
			t.Fatalf("%s evidence violated its byte/UTF-8 contract: bytes=%d", language, len([]byte(evidence)))
		}
	}
}

func TestDOCXDecisionEvidenceQuotedAnchorsAndReorderingStayStable(t *testing.T) {
	output := syntheticDOCXDecisionOutput(160)
	document, _ := anyMap(output["document"])
	blocks := documentAnySliceFromAny(document["evidence_blocks"])
	target, _ := anyMap(blocks[149])
	target["text"] = "心得与体会 target paragraph"

	project := func(request string) string {
		return projectDOCXDecisionEvidence(output, app.RouteDecision{
			Slots: app.RouteSlots{Query: request, TargetRef: "report.docx"},
			Facts: map[string]string{"document_format": app.DocumentFormatDOCX},
		}, syntheticDOCXDecisionEntries(), 2200)
	}
	english := project(`Polish the paragraph containing "心得与体会" in report.docx`)
	chinese := project(`润色 report.docx 中“心得与体会”所在段落`)
	for language, evidence := range map[string]string{"english": english, "chinese": chinese} {
		if !strings.Contains(evidence, `"candidate_id":"document.p[150]"`) {
			t.Fatalf("%s quote did not resolve the stable target: %s", language, evidence)
		}
	}

	blocks[0], blocks[75] = blocks[75], blocks[0]
	reordered := project(`Polish the paragraph containing "心得与体会" in report.docx`)
	if !strings.Contains(reordered, `"candidate_id":"document.p[150]"`) {
		t.Fatalf("unrelated early reorder evicted the explicit target: %s", reordered)
	}
}

func TestDOCXDecisionEvidenceUsesHeadTailAndStoryFallback(t *testing.T) {
	output := syntheticDOCXDecisionOutput(80)
	document, _ := anyMap(output["document"])
	enrichment, _ := anyMap(document["enrichment"])
	extensions, _ := anyMap(enrichment["extensions"])
	extensions["story_parts"] = []any{
		map[string]any{
			"kind": "header", "part_name": "/word/header1.xml",
			"blocks": []any{map[string]any{
				"text": "Confidential header", "location": map[string]any{
					"part_kind": "header", "path": "document.header[/word/header1.xml].p[1]",
				},
			}},
		},
	}
	evidence := projectDOCXDecisionEvidence(output, app.RouteDecision{
		Slots: app.RouteSlots{Query: "Improve report.docx", TargetRef: "report.docx"},
		Facts: map[string]string{"document_format": app.DocumentFormatDOCX},
	}, syntheticDOCXDecisionEntries(), 4200)
	for _, expected := range []string{
		`"candidate_id":"document.p[1]"`, `"candidate_id":"document.p[2]"`, `"candidate_id":"document.p[79]"`, `"candidate_id":"document.p[80]"`,
		`"scope":"header"`, `"record_type":"eligible_operation"`, `"coverage"`, `"omitted_ranges"`,
	} {
		if !strings.Contains(evidence, expected) {
			t.Fatalf("fallback evidence omitted %q: %s", expected, evidence)
		}
	}
	for _, runtimeField := range []string{`"location"`, `"sourceHash"`, `"governed_target"`, `"rel_path"`, `"source_bytes"`} {
		if strings.Contains(evidence, runtimeField) {
			t.Fatalf("DOCX decision projection exposes Runtime-owned field %s: %s", runtimeField, evidence)
		}
	}
}

func TestDOCXDecisionEvidenceBudgetsKeepWholeUTF8Records(t *testing.T) {
	output := syntheticDOCXDecisionOutput(120)
	route := app.RouteDecision{
		Slots: app.RouteSlots{Query: `把第118段“中文目标内容 118”替换为更准确的说明`, TargetRef: "report.docx"},
		Facts: map[string]string{"document_format": app.DocumentFormatDOCX},
	}
	for _, limit := range []int{8000, 4000, 2000} {
		evidence := projectDOCXDecisionEvidence(output, route, syntheticDOCXDecisionEntries(), limit)
		if evidence == "" || len([]byte(evidence)) > limit || !utf8.ValidString(evidence) {
			t.Fatalf("invalid evidence at limit %d: bytes=%d valid=%t", limit, len([]byte(evidence)), utf8.ValidString(evidence))
		}
		for lineNumber, line := range strings.Split(evidence, "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatalf("limit %d cut record %d: %q", limit, lineNumber+1, line)
			}
		}
		var summary map[string]any
		if err := json.Unmarshal([]byte(strings.SplitN(evidence, "\n", 2)[0]), &summary); err != nil || intLikeValue(summary["bytes_used"]) != len([]byte(evidence)) {
			t.Fatalf("limit %d reported the wrong byte usage: summary=%#v bytes=%d err=%v", limit, summary, len([]byte(evidence)), err)
		}
		if !strings.Contains(evidence, `"candidate_id":"document.p[118]"`) {
			t.Fatalf("limit %d evicted the explicit target: %s", limit, evidence)
		}
	}
}

func TestWorkflowDOCXDecisionEvidenceKeepsArchivedFallbackWithinBudget(t *testing.T) {
	runtime, _, _, dispatch := newDocumentDecisionFixture(t, "Improve paragraph 1 in report.docx")
	node, ok := workflowPlanNode(dispatch.Run.Workflow.Plan, "select_edit_operation")
	if !ok {
		t.Fatal("decision node is missing")
	}
	view, err := runtime.exposure.Search(context.Background(), app.ExposureRequest{
		RunID: dispatch.Run.ID, WorkflowID: dispatch.Run.Workflow.Plan.ProfileID, NodeID: node.ID,
		ScopeRevision: dispatch.Run.Workflow.Nodes[node.ID].ScopeRevision,
		ActorRef:      runtime.workflowActorRef(dispatch.Run), Limit: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := runtime.workflowDecisionEvidence(context.Background(), dispatch.Run, node, view.Entries)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidence, "structured evidence") || len([]byte(evidence)) > runtime.workflowStageEvidenceLimit() {
		t.Fatalf("archived decision evidence violated its fallback contract: %s", evidence)
	}
}

func syntheticDOCXDecisionOutput(paragraphCount int) map[string]any {
	blocks := make([]any, 0, paragraphCount)
	for index := 1; index <= paragraphCount; index++ {
		text := fmt.Sprintf("Paragraph content %03d with stable evidence", index)
		if index == paragraphCount-2 {
			text = fmt.Sprintf("中文目标内容 %d with stable evidence", index)
		}
		blocks = append(blocks, map[string]any{
			"blockId": fmt.Sprintf("document.p[%d]", index), "type": "paragraph", "text": text,
			"sourceHash": fmt.Sprintf("sha1:%03d", index),
			"location": map[string]any{
				"part": "document", "partKind": "body", "blockType": "paragraph",
				"paragraphIndex": index, "path": fmt.Sprintf("document.p[%d]", index),
			},
		})
	}
	return map[string]any{
		"rel_path": "report.docx", "truncated": false, "source_bytes": 200000,
		"document": map[string]any{
			"format": app.DocumentFormatDOCX, "source": "python_docx", "evidence_blocks": blocks,
			"stats": map[string]any{"paragraphs": paragraphCount, "complete": true},
			"enrichment": map[string]any{
				"coverage":   map[string]any{"content": "complete", "content_scopes": map[string]any{"body": "complete"}},
				"extensions": map[string]any{"status": "complete", "unparsed_parts": []any{}},
			},
		},
	}
}

func syntheticDOCXDecisionEntries() []app.ToolDirectoryEntry {
	entries := []app.ToolDirectoryEntry{}
	for index, operation := range []string{"replace_text", "replace_paragraph", "insert_paragraph", "delete_paragraph", "set_text_style"} {
		entries = append(entries, app.ToolDirectoryEntry{
			ID: app.ToolDirectoryEntryID(fmt.Sprintf("entry_%d", index+1)),
			Capability: app.CapabilityDescriptor{Qualifiers: map[string]string{
				app.CapabilityQualifierFormat: app.DocumentFormatDOCX, app.CapabilityQualifierOperation: operation,
			}},
			Summary: "Bounded DOCX " + operation, WhenToUse: "Use only for " + operation,
		})
	}
	return entries
}
