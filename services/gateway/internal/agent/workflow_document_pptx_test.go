package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestPPTXScopeGroundingAndDirectoryFiltering(t *testing.T) {
	for _, test := range []struct {
		request string
		scope   string
		indexes []int
	}{
		{request: "Improve slide 8", scope: pptxScopeSingleSlide, indexes: []int{8}},
		{request: "完善第二十一页", scope: pptxScopeSingleSlide, indexes: []int{21}},
		{request: "Polish the entire presentation", scope: pptxScopeWholeDeck},
		{request: "优化整份演示文稿", scope: pptxScopeWholeDeck},
		{request: "Add a slide after slide 4 using slide 2 as the template", scope: pptxScopeStructural, indexes: []int{4, 2}},
		{request: "Replace Alpha with Beta", scope: pptxScopeExactText},
		{request: "Polish this presentation", scope: pptxScopeUnspecified},
		{request: "Edit the SmartArt animation", scope: pptxScopeUnsupported},
		{request: "修改演示文稿的图表数据", scope: pptxScopeUnsupported},
	} {
		grounded := groundPPTXEditScope(test.request)
		if grounded.Scope != test.scope || !equalIntSlices(grounded.SlideIndexes, test.indexes) {
			t.Fatalf("unexpected PPTX scope for %q: %#v", test.request, grounded)
		}
	}

	entries := []app.ToolDirectoryEntry{}
	for _, operation := range []string{"replace_text", "add_slide", "update_slide", "update_deck", "duplicate_slide", "delete_slide"} {
		entries = append(entries, app.ToolDirectoryEntry{Capability: app.CapabilityDescriptor{
			Name: app.ToolCapabilityDocumentEdit,
			Qualifiers: map[string]string{
				app.CapabilityQualifierFormat: app.DocumentFormatPPTX, app.CapabilityQualifierOperation: operation,
			},
		}})
	}
	for _, test := range []struct {
		scope string
		want  []string
	}{
		{scope: pptxScopeSingleSlide, want: []string{"update_slide"}},
		{scope: pptxScopeWholeDeck, want: []string{"update_deck"}},
		{scope: pptxScopeExactText, want: []string{"replace_text"}},
		{scope: pptxScopeStructural, want: []string{"add_slide", "duplicate_slide", "delete_slide"}},
	} {
		filtered := scopePPTXDirectoryEntries(app.RouteDecision{Facts: map[string]string{
			"document_format": app.DocumentFormatPPTX, pptxScopeFact: test.scope,
		}}, entries)
		got := []string{}
		for _, entry := range filtered {
			got = append(got, entry.Capability.Qualifiers[app.CapabilityQualifierOperation])
		}
		if !equalStringSlices(got, test.want) {
			t.Fatalf("scope %s exposed operations %#v; want %#v", test.scope, got, test.want)
		}
	}
}

func TestPPTXTargetEvidencePrioritizesLateSlideAndRejectsOverflow(t *testing.T) {
	slides := []any{}
	blocks := []any{}
	for slideIndex := 1; slideIndex <= 10; slideIndex++ {
		slides = append(slides, map[string]any{
			"index": slideIndex, "template_ref": "slide:" + intText(slideIndex),
			"layout_ref": "layout:/ppt/slideLayouts/slideLayout6.xml", "layout_name": "Blank",
		})
		text := strings.Repeat("early ", 350)
		if slideIndex == 10 {
			text = "Late editable target"
		}
		blocks = append(blocks, pptxEvidenceBlock(slideIndex, 1, 0, text, true))
	}
	blocks = append(blocks, pptxEvidenceBlock(10, 2, 1, "Grouped read only", false))
	output := map[string]any{
		"rel_path": "deck.pptx", "kind": "pptx", "source_bytes": 12345,
		"document": map[string]any{
			"format": "pptx", "slides": slides, "blocks": blocks,
			"metadata": map[string]any{"sha256": "sha256:deck", "relative_path": "deck.pptx", "format": "pptx"},
			"enrichment": map[string]any{"layout": map[string]any{
				"layout_inventory": []any{map[string]any{
					"layout_ref": "layout:/ppt/slideLayouts/slideLayout6.xml", "name": strings.Repeat("optional-layout-detail", 800),
				}},
			}},
		},
	}
	evidence, err := pptxTargetStructuredEvidenceForOperation(output, pptxScopeSingleSlide, pptxDefaultOperationForScope(pptxScopeSingleSlide), []int{10}, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidence, `"slide_index":10`) || !strings.Contains(evidence, "Late editable target") ||
		strings.Contains(evidence, "early") || strings.Contains(evidence, "Grouped read only") {
		t.Fatalf("late-slide target projection is incomplete or leaked non-target/read-only evidence:\n%s", evidence)
	}
	for _, runtimeField := range []string{`"path"`, `"rel_path"`, `"sha256"`, `"relative_path"`, `"target_hash"`, "source_bytes"} {
		if strings.Contains(evidence, runtimeField) {
			t.Fatalf("PPTX model projection exposes Runtime-owned field %s:\n%s", runtimeField, evidence)
		}
	}
	for _, line := range strings.Split(evidence, "\n") {
		if strings.HasPrefix(line, "shape_record=") && (!strings.HasPrefix(strings.TrimPrefix(line, "shape_record="), "{") || !strings.HasSuffix(line, "}")) {
			t.Fatalf("PPTX evidence cut a shape record: %q", line)
		}
	}
	if _, err := pptxTargetStructuredEvidenceForOperation(output, pptxScopeSingleSlide, pptxDefaultOperationForScope(pptxScopeSingleSlide), []int{10}, 350); err == nil || err.Error() != "pptx_target_evidence_exceeds_budget" {
		t.Fatalf("undersized target evidence budget was not rejected: %v", err)
	}
	oversizedSlides := append(append([]any{}, slides...), map[string]any{"index": 11}, map[string]any{"index": 12}, map[string]any{"index": 13})
	output["document"].(map[string]any)["slides"] = oversizedSlides
	if _, err := pptxTargetStructuredEvidenceForOperation(output, pptxScopeWholeDeck, pptxDefaultOperationForScope(pptxScopeWholeDeck), nil, 8000); err == nil || err.Error() != "pptx_whole_deck_exceeds_batch_bound" {
		t.Fatalf("oversized whole-deck evidence was not rejected: %v", err)
	}
	output["document"].(map[string]any)["slides"] = []any{map[string]any{"index": 1, "template_ref": "slide:1"}}
	output["document"].(map[string]any)["blocks"] = []any{pptxEvidenceBlock(1, 1, 0, strings.Repeat("large-slide ", 560), true)}
	if _, err := pptxTargetStructuredEvidenceForOperation(output, pptxScopeWholeDeck, pptxDefaultOperationForScope(pptxScopeWholeDeck), nil, 8000); err == nil || err.Error() != "pptx_slide_evidence_exceeds_budget" {
		t.Fatalf("single-slide whole-deck evidence bound was not enforced: %v", err)
	}
	blankOutput := map[string]any{"document": map[string]any{
		"format": "pptx", "slides": []any{map[string]any{
			"index": 1, "template_ref": "slide:1", "layout_ref": "layout:/ppt/slideLayouts/slideLayout6.xml",
		}},
		"enrichment": map[string]any{"layout": map[string]any{"layout_inventory": []any{map[string]any{
			"layout_ref": "layout:/ppt/slideLayouts/slideLayout6.xml", "name": "Blank",
		}}}},
	}}
	blankEvidence, err := pptxTargetStructuredEvidenceForOperation(blankOutput, pptxScopeStructural, pptxDefaultOperationForScope(pptxScopeStructural), []int{1}, 8000)
	if err != nil || !strings.Contains(blankEvidence, `"slide_index":1`) {
		t.Fatalf("blank structural target did not retain slide/layout evidence: evidence=%q err=%v", blankEvidence, err)
	}
	allStructuralEvidence, err := pptxTargetStructuredEvidenceForOperation(blankOutput, pptxScopeStructural, pptxDefaultOperationForScope(pptxScopeStructural), nil, 8000)
	if err != nil || !strings.Contains(allStructuralEvidence, `"slide_index":1`) {
		t.Fatalf("unpositioned structural edit did not retain the bounded slide inventory: evidence=%q err=%v", allStructuralEvidence, err)
	}
}

func TestPPTXBusinessProjectionIsOperationScopedAndOmitsRichTextTrees(t *testing.T) {
	richTree := map[string]any{"paragraphs": []any{map[string]any{"runs": []any{map[string]any{"text": strings.Repeat("rich-run", 300)}}}}}
	output := map[string]any{
		"rel_path": "deck.pptx", "kind": "pptx", "source_bytes": 43675, "content": strings.Repeat("duplicated content ", 200),
		"document": map[string]any{
			"format":   "pptx",
			"metadata": map[string]any{"sha256": strings.Repeat("a", 64), "relative_path": "deck.pptx", "format": "pptx"},
			"slides": []any{
				map[string]any{"index": 1, "template_ref": "slide:1", "layout_ref": "layout:/ppt/slideLayouts/slideLayout1.xml", "items": []any{richTree}},
				map[string]any{"index": 2, "template_ref": "slide:2", "layout_ref": "layout:/ppt/slideLayouts/slideLayout2.xml", "items": []any{richTree}},
			},
			"blocks": []any{
				map[string]any{"kind": "shape_text", "text": "Editable title", "location": map[string]any{"slide_index": 2, "shape_index": 1, "block_type": "shape_text"}, "format_metadata": map[string]any{"editable": true, "text_structure": richTree}},
				map[string]any{"kind": "shape_text", "text": "Grouped title", "location": map[string]any{"slide_index": 2, "shape_index": 2, "group_child_index": 1, "block_type": "shape_text"}, "format_metadata": map[string]any{"editable": false, "text_structure": richTree}},
			},
			"enrichment": map[string]any{
				"layout": map[string]any{
					"shapes": []any{map[string]any{
						"slide_index": 2, "shape_index": 1, "companion_group_id": "group:2", "companion_role": "body", "text_structure": richTree,
						"text_style": map[string]any{"font_size_pt": 18, "single_line_capacity_visual_units": 30, "single_line_fit_ratio": .6},
					}},
					"layout_inventory": []any{map[string]any{"layout_ref": "layout:/ppt/slideLayouts/slideLayout2.xml", "name": "Title and Content", "placeholder_roles": []any{"title", "body"}, "unused_tree": richTree}},
				},
				"annotations": map[string]any{"notes": []any{map[string]any{"text": "speaker note", "location": map[string]any{"slide_index": 2}}}},
			},
		},
	}
	projected, ok := pptxBusinessProjectionResult(output, pptxScopeStructural, []int{2})
	if !ok {
		t.Fatal("PPTX business projection was not produced")
	}
	raw, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	full, _ := json.Marshal(output)
	if len(raw) >= len(full)/3 || strings.Contains(string(raw), "text_structure") || strings.Contains(string(raw), "rich-run") || strings.Contains(string(raw), "duplicated content") {
		t.Fatalf("PPTX business projection retained replaceable parse detail: projected=%d full=%d\n%s", len(raw), len(full), raw)
	}
	if projected["projection_schema"] != pptxBusinessProjectionSchema || !strings.Contains(string(raw), `"has_notes":true`) || !strings.Contains(string(raw), `"target_hash"`) {
		t.Fatalf("PPTX business projection omitted stable business evidence: %s", raw)
	}

	tests := []struct {
		operation string
		want      []string
		reject    []string
	}{
		{operation: "replace_text", want: []string{"Editable title"}, reject: []string{"font_size_pt", "layout_record="}},
		{operation: "update_slide", want: []string{"Editable title", "font_size_pt"}, reject: []string{"Grouped title", "layout_record="}},
		{operation: "update_deck", want: []string{"Editable title", "processing_unit=slide"}, reject: []string{"Grouped title", "layout_record="}},
		{operation: "add_slide", want: []string{"Editable title", "layout_record="}, reject: []string{"Grouped title"}},
		{operation: "duplicate_slide", want: []string{`"has_notes":true`}, reject: []string{"shape_record=", "layout_record="}},
		{operation: "delete_slide", want: []string{`"has_notes":true`}, reject: []string{"shape_record=", "layout_record="}},
	}
	for _, test := range tests {
		evidence, err := pptxTargetStructuredEvidenceForOperation(projected, pptxScopeStructural, test.operation, []int{2}, 8000)
		if err != nil {
			t.Fatalf("%s projection failed: %v", test.operation, err)
		}
		for _, want := range test.want {
			if !strings.Contains(evidence, want) {
				t.Errorf("%s projection omitted %q:\n%s", test.operation, want, evidence)
			}
		}
		for _, reject := range test.reject {
			if strings.Contains(evidence, reject) {
				t.Errorf("%s projection leaked %q:\n%s", test.operation, reject, evidence)
			}
		}
		for _, runtimeField := range []string{`"path"`, `"rel_path"`, `"sha256"`, `"relative_path"`, `"target_hash"`} {
			if strings.Contains(evidence, runtimeField) {
				t.Errorf("%s model projection exposes Runtime-owned field %s:\n%s", test.operation, runtimeField, evidence)
			}
		}
	}
}

func TestPPTXSemanticMutationValidationRemovesNoopsAndRejectsInvalidItems(t *testing.T) {
	args, validationErr := normalizePPTXSemanticMutation("update_slide", map[string]any{
		"updates": []any{
			map[string]any{"shape_index": 1, "old_text": "Existing title", "text": "Existing title"},
			map[string]any{"shape_index": 2, "old_text": "Subtitle", "text": "   "},
			map[string]any{"shape_index": 3, "old_text": "Body", "text": "Improved body"},
			map[string]any{"shape_index": 4, "old_text": "Wi‑Fi", "text": "Wi-Fi"},
			map[string]any{"shape_index": 5, "old_text": "Legacy body", "replacement_text": "Improved legacy body"},
		},
	})
	updates := anySlice(args["updates"])
	if validationErr == nil || !containsString(validationErr.Codes, "replacement_text_empty") ||
		!containsString(validationErr.Codes, "cosmetic_only_change") || len(validationErr.ItemIndexes) != 2 ||
		validationErr.ItemIndexes[0] != 1 || validationErr.ItemIndexes[1] != 3 || len(updates) != 2 ||
		intLikeValue(updates[0].(map[string]any)["shape_index"]) != 3 ||
		stringValue(updates[1].(map[string]any)["text"]) != "Improved legacy body" ||
		updates[1].(map[string]any)["replacement_text"] != nil {
		t.Fatalf("PPTX mutation normalization did not retain only effective valid changes: args=%#v err=%#v", args, validationErr)
	}

	_, validationErr = normalizePPTXSemanticMutation("update_slide", map[string]any{
		"updates": []any{map[string]any{"shape_index": 1, "old_text": "Same", "text": "Same"}},
	})
	if validationErr == nil || !containsString(validationErr.Codes, "no_effective_mutation") {
		t.Fatalf("PPTX no-op-only mutation was accepted: %#v", validationErr)
	}

	args, validationErr = normalizePPTXSemanticMutation("replace_text", map[string]any{
		"replacements": []any{map[string]any{"find": "Wi‑Fi", "replace": "Wi-Fi"}},
	})
	if validationErr != nil || len(anySlice(args["replacements"])) != 1 {
		t.Fatalf("explicit PPTX text replacement rejected a cosmetic copy edit: args=%#v err=%#v", args, validationErr)
	}

	_, validationErr = normalizePPTXSemanticMutation("update_slide", map[string]any{
		"updates": []any{map[string]any{
			"shape_index": 1, "old_text": "Original", "text": "First proposal", "replacement_text": "Conflicting proposal",
		}},
	})
	if validationErr == nil || !containsString(validationErr.Codes, "conflicting_replacement_fields") ||
		len(validationErr.ItemIndexes) != 1 || validationErr.ItemIndexes[0] != 0 {
		t.Fatalf("conflicting PPTX replacement aliases were accepted: %#v", validationErr)
	}

	args, validationErr = normalizePPTXSemanticMutation("update_slide", map[string]any{
		"updates": []any{
			map[string]any{"shape_index": 1, "old_text": "Original title", "text": "A clearer title"},
			map[string]any{"shape_index": 2, "old_text": "Wi‑Fi", "text": "Wi-Fi"},
		},
	})
	updates = anySlice(args["updates"])
	if validationErr != nil || len(updates) != 1 || intLikeValue(updates[0].(map[string]any)["shape_index"]) != 1 {
		t.Fatalf("cosmetic PPTX update was not filtered beside a substantive update: args=%#v err=%#v", args, validationErr)
	}
}

func TestPPTXSemanticRepairInstructionsRequireEffectiveCorrections(t *testing.T) {
	tests := []struct {
		name     string
		codes    []string
		expected []string
		rejected []string
	}{
		{
			name:  "empty replacement",
			codes: []string{"replacement_text_empty"},
			expected: []string{
				"Preserve every other valid effective mutation from invalid_output",
				"supply meaningful non-empty text that differs from current_text or omit the entire update object",
				"at least one substantive clarity, accuracy, concision, or hierarchy improvement",
				"a Unicode punctuation or glyph substitution alone is valid only when the owner explicitly requested it",
				"never fill the update list by copying unchanged current_text",
			},
		},
		{
			name:  "cosmetic-only replacement",
			codes: []string{"cosmetic_only_change", "replacement_text_empty"},
			expected: []string{
				"changes limited to punctuation, symbols, spacing, or letter case do not satisfy",
				"whose letters or numbers differ from current_text",
				"at least one substantive clarity, accuracy, concision, or hierarchy improvement",
				"Never copy unchanged current_text or preserve a cosmetic-only replacement",
			},
		},
		{
			name:  "no effective mutation",
			codes: []string{"no_effective_mutation"},
			expected: []string{
				"at least one meaningful replacement that differs from current_text",
				"include only shapes that actually change",
			},
		},
		{
			name:  "layout conflict",
			codes: []string{string(app.ToolErrorPPTXLayoutFitConflict)},
			expected: []string{
				"Preserve every other valid effective mutation",
				"omit the entire update object",
				"never return empty text or unchanged current_text",
			},
			rejected: []string{"remove only the proposed replacement text"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := workflowSemanticRepairObservation(workflowSemanticRepairRequest{
				SchemaVersion: workflowSemanticRepairSchema,
				ProjectionID:  "projection",
				ErrorCodes:    test.codes,
				InvalidOutput: map[string]any{"updates": []any{map[string]any{"shape_index": 2, "text": ""}}},
			})
			for _, expected := range test.expected {
				if !strings.Contains(observation, expected) {
					t.Errorf("repair observation omitted %q:\n%s", expected, observation)
				}
			}
			for _, rejected := range test.rejected {
				if strings.Contains(observation, rejected) {
					t.Errorf("repair observation retained ambiguous guidance %q:\n%s", rejected, observation)
				}
			}
		})
	}
}

func TestPPTXSemanticMutationGetsOneSameProjectionRepair(t *testing.T) {
	for _, test := range []struct {
		name           string
		initialText    string
		repairText     string
		wantFailure    workflowFailureCode
		wantToolCalls  int
		wantApprovals  int
		wantRejections int
		wantErrorCode  string
	}{
		{name: "valid repair reaches approval", repairText: "Revised title", wantToolCalls: 1, wantApprovals: 1, wantRejections: 1},
		{name: "cosmetic repair reaches approval", initialText: "Original third title!", repairText: "A clearer third-slide title", wantToolCalls: 1, wantApprovals: 1, wantRejections: 1, wantErrorCode: "cosmetic_only_change"},
		{name: "layout preflight repair reaches one approval", initialText: strings.Repeat("Expanded title ", 80), repairText: "Concise title", wantToolCalls: 1, wantApprovals: 1, wantRejections: 1, wantErrorCode: string(app.ToolErrorPPTXLayoutFitConflict)},
		{name: "second invalid output blocks", repairText: "", wantFailure: workflowFailureSemanticOutputInvalid, wantRejections: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, st, session, run, root, closeRuntime := prepareRealPPTXUpdateNode(t)
			defer closeRuntime()
			profile := documentEditProfile{}
			stageContext := profile.StageContext(run.Workflow)
			visibleTools, err := runtime.materializeActiveWorkflowTools(
				context.Background(), run, runtime.workflowActorRef(run), &stageContext,
			)
			if err != nil {
				t.Fatal(err)
			}
			initial := `{"type":"action","tool":"pptx.update_slide","arguments":{"updates":[{"shape_index":1,"text":"` + test.initialText + `"}]}}`
			repair := `{"type":"action","tool":"pptx.update_slide","arguments":{"updates":[{"shape_index":1,"text":"` + test.repairText + `"}]}}`
			content := "Improve the selected slide\nMOCK_STEP_RESPONSE:" + initial + "\nMOCK_STEP_REPAIR_RESPONSE:" + repair
			result := runtime.runWorkflowStepLoop(
				context.Background(), session.ID, run, content, stageContext, visibleTools, nil, nil, nil,
			)
			if result.FailureCode != test.wantFailure || len(result.ToolCalls) != test.wantToolCalls || len(result.Approvals) != test.wantApprovals {
				t.Fatalf("unexpected semantic repair result: failure=%q calls=%#v approvals=%#v observations=%#v", result.FailureCode, result.ToolCalls, result.Approvals, result.Observations)
			}
			if test.wantApprovals == 1 && result.ToolCalls[0].Status != "approval_pending" {
				t.Fatalf("valid repaired mutation did not stop at approval: %#v", result.ToolCalls[0])
			}
			projections := []app.AuditEvent{}
			rejections := 0
			foundErrorCode := test.wantErrorCode == ""
			readCalls := 0
			for _, call := range testListToolCalls(st, session.ID) {
				if call.RunID == run.ID && call.Tool == "files.read" {
					readCalls++
				}
			}
			for _, event := range st.ListAudit(session.ID) {
				if event.RunID != run.ID {
					continue
				}
				if event.Type == "workflow.semantic_output.rejected" {
					rejections++
					if hasAgentAuditStringSliceField([]app.AuditEvent{event}, event.Type, "error_codes", test.wantErrorCode) {
						foundErrorCode = true
					}
				}
				if event.Type == "workflow.evidence_projection.created" && event.Fields["semantic_variable"] == "document_mutation_arguments" {
					projections = append(projections, event)
				}
			}
			if rejections != test.wantRejections || !foundErrorCode || len(projections) != 2 ||
				projections[0].Fields["model_payload_digest"] != projections[1].Fields["model_payload_digest"] ||
				readCalls != 1 {
				t.Fatalf("repair did not reuse one source projection: rejections=%d projections=%#v calls=%#v", rejections, projections, testListToolCalls(st, session.ID))
			}
			if _, err := os.Stat(filepath.Join(root, "deck-sparkclaw-edit.pptx")); !os.IsNotExist(err) {
				t.Fatalf("PPTX semantic preflight left a user-visible output before approval: %v", err)
			}
			if leftovers, err := filepath.Glob(filepath.Join(root, ".sparkclaw-pptx-preflight-*")); err != nil || len(leftovers) != 0 {
				t.Fatalf("PPTX semantic preflight left temporary output: paths=%v err=%v", leftovers, err)
			}
		})
	}
}

func TestPPTXReplacementTextAliasIsCanonicalizedBeforeApproval(t *testing.T) {
	runtime, _, session, run, _, closeRuntime := prepareRealPPTXUpdateNode(t)
	defer closeRuntime()
	stageContext := (documentEditProfile{}).StageContext(run.Workflow)
	visibleTools, err := runtime.materializeActiveWorkflowTools(
		context.Background(), run, runtime.workflowActorRef(run), &stageContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	content := `Improve the selected slide
MOCK_STEP_RESPONSE:{"type":"action","tool":"pptx.update_slide","arguments":{"updates":[{"shape_index":1,"replacement_text":"A clearer third-slide title"}]}}`
	result := runtime.runWorkflowStepLoop(
		context.Background(), session.ID, run, content, stageContext, visibleTools, nil, nil, nil,
	)
	if result.FailureCode != "" || len(result.ToolCalls) != 1 || len(result.Approvals) != 1 ||
		result.ToolCalls[0].Status != "approval_pending" {
		t.Fatalf("PPTX replacement_text alias did not reach approval: failure=%q calls=%#v approvals=%#v", result.FailureCode, result.ToolCalls, result.Approvals)
	}
	updates := anySlice(result.ToolCalls[0].Arguments["updates"])
	if len(updates) != 1 {
		t.Fatalf("canonicalized PPTX action lost its update: %#v", result.ToolCalls[0].Arguments)
	}
	update, _ := anyMap(updates[0])
	if stringValue(update["text"]) != "A clearer third-slide title" || update["replacement_text"] != nil {
		t.Fatalf("PPTX replacement_text alias was not removed before approval: %#v", update)
	}
}

func TestPPTXRouteGroundingBlocksAmbiguousAndUnsupportedScopes(t *testing.T) {
	root := t.TempDir()
	writeAgentPPTXFixture(t, root)
	runtime, _, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	for _, test := range []struct {
		request string
		status  app.RouteStatus
		scope   string
		reason  string
	}{
		{request: "Improve slide 3 in deck.pptx", status: app.RouteMatched, scope: pptxScopeSingleSlide},
		{request: "Polish the entire presentation deck.pptx", status: app.RouteMatched, scope: pptxScopeWholeDeck},
		{request: "Polish deck.pptx", status: app.RouteClarify, reason: "pptx_edit_scope_unspecified"},
		{request: "Edit the SmartArt animation in deck.pptx", status: app.RouteBlocked, reason: "pptx_edit_target_unsupported"},
		{request: "Edit the chart data in deck.pptx", status: app.RouteBlocked, reason: "pptx_edit_target_unsupported"},
	} {
		route, err := runtime.routeIntentForTest(session.ID, "turn", test.request, agentContextSnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		if route.Status != test.status || test.scope != "" && route.Facts[pptxScopeFact] != test.scope ||
			test.reason != "" && route.Reason != test.reason {
			t.Fatalf("unexpected PPTX route for %q: %#v", test.request, route)
		}
	}
	for _, request := range []string{"Read deck.pptx", "Create a new presentation named deck.pptx", "Send deck.pptx"} {
		route, err := runtime.routeIntentForTest(session.ID, "turn", request, agentContextSnapshot{})
		if err != nil {
			t.Fatal(err)
		}
		if route.Status == app.RouteMatched && len(route.CapabilityPath) > 1 && route.CapabilityPath[1] == app.CapabilityDocumentEdit {
			t.Fatalf("read/create/send hard negative entered document.edit for %q: %#v", request, route)
		}
	}
}

func TestPPTXWorkflowBlocksStaleAndGroupedTargetsBeforeApproval(t *testing.T) {
	for _, test := range []struct {
		name   string
		update map[string]any
	}{
		{name: "stale text", update: map[string]any{"shape_index": 1, "old_text": "stale title", "text": "Improved title"}},
		{name: "group child", update: map[string]any{"shape_index": 2, "old_text": "Grouped text", "text": "Forbidden"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, st, session, run, root, closeRuntime := prepareRealPPTXUpdateNode(t)
			defer closeRuntime()
			call, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
				Name: "pptx.update_slide",
				Args: map[string]any{
					"path": "model-invented.pptx", "output_path": "model-output.pptx", "slide_index": 2,
					"layout_policy": "preserve", "updates": []any{test.update},
				},
				WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
			})
			if approval != nil || call.Status != "blocked" {
				t.Fatalf("invalid PPTX target reached approval: call=%#v approval=%#v", call, approval)
			}
			if approvals := storetest.MustListApprovals(t, st, ""); len(approvals) != 0 {
				t.Fatalf("invalid PPTX target created approval records: %#v", approvals)
			}
			if _, err := os.Stat(filepath.Join(root, "deck-sparkclaw-edit.pptx")); !os.IsNotExist(err) {
				t.Fatalf("invalid PPTX target wrote an output: %v", err)
			}
		})
	}
}

func TestPPTXWorkflowBlocksStaleInsertionRefsBeforeApproval(t *testing.T) {
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{name: "layout ref", args: map[string]any{"layout_ref": "layout:/ppt/slideLayouts/stale.xml"}},
		{name: "template slide ref", args: map[string]any{"template_slide_ref": "slide:99"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, st, session, run, root, closeRuntime := prepareRealPPTXEditorNode(
				t, "Add a slide after slide 2 in deck.pptx", "pptx.add_slide", "add_slide",
			)
			defer closeRuntime()
			test.args["path"] = "model-invented.pptx"
			test.args["output_path"] = "model-output.pptx"
			test.args["after_slide_index"] = 2
			test.args["title"] = "New title"
			test.args["body"] = "New body"
			call, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
				Name: "pptx.add_slide", Args: test.args, WorkflowID: app.WorkflowDocumentEdit,
				WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
			})
			if approval != nil || call.Status != "blocked" {
				t.Fatalf("stale insertion reference reached approval: call=%#v approval=%#v", call, approval)
			}
			if approvals := storetest.MustListApprovals(t, st, ""); len(approvals) != 0 {
				t.Fatalf("stale insertion reference created approval records: %#v", approvals)
			}
			if _, err := os.Stat(filepath.Join(root, "deck-sparkclaw-edit.pptx")); !os.IsNotExist(err) {
				t.Fatalf("stale insertion reference wrote an output: %v", err)
			}
		})
	}
}

func TestPPTXWorkflowBlocksStaleExactTextBeforeApproval(t *testing.T) {
	runtime, st, session, run, root, closeRuntime := prepareRealPPTXEditorNode(
		t, "Replace Original third title with Improved title in deck.pptx", "pptx.replace_text", "replace_text",
	)
	defer closeRuntime()
	call, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
		Name: "pptx.replace_text",
		Args: map[string]any{
			"path": "model-invented.pptx", "output_path": "model-output.pptx",
			"replacements": []any{map[string]any{"find": "Stale title", "replace": "Improved title"}},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if approval != nil || call.Status != "blocked" {
		t.Fatalf("stale exact-text target reached approval: call=%#v approval=%#v", call, approval)
	}
	if approvals := storetest.MustListApprovals(t, st, ""); len(approvals) != 0 {
		t.Fatalf("stale exact-text target created approval records: %#v", approvals)
	}
	if _, err := os.Stat(filepath.Join(root, "deck-sparkclaw-edit.pptx")); !os.IsNotExist(err) {
		t.Fatalf("stale exact-text target wrote an output: %v", err)
	}
}

func TestPPTXWorkflowBlocksOversizedUpdatesBeforeApproval(t *testing.T) {
	for _, test := range []struct {
		name    string
		updates []any
	}{
		{name: "shape count", updates: func() []any {
			updates := make([]any, document.PPTXMaxUpdatedShapes+1)
			for index := range updates {
				updates[index] = map[string]any{"shape_index": 1, "old_text": "Original third title", "text": "Update"}
			}
			return updates
		}()},
		{name: "replacement bytes", updates: []any{map[string]any{
			"shape_index": 1, "old_text": "Original third title", "text": strings.Repeat("x", document.PPTXMaxReplacementBytes+1),
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, st, session, run, root, closeRuntime := prepareRealPPTXUpdateNode(t)
			defer closeRuntime()
			call, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
				Name: "pptx.update_slide",
				Args: map[string]any{
					"path": "model-invented.pptx", "output_path": "model-output.pptx", "slide_index": 3,
					"layout_policy": "preserve", "updates": test.updates,
				},
				WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
			})
			if approval != nil || call.Status != "blocked" || len(storetest.MustListApprovals(t, st, "")) != 0 {
				t.Fatalf("oversized PPTX update reached approval: call=%#v approval=%#v", call, approval)
			}
			if _, err := os.Stat(filepath.Join(root, "deck-sparkclaw-edit.pptx")); !os.IsNotExist(err) {
				t.Fatalf("oversized PPTX update wrote an output: %v", err)
			}
		})
	}
}

func TestPPTXRouteApprovalExecuteAndRereadRealFile(t *testing.T) {
	root := t.TempDir()
	writeAgentPPTXFixture(t, root)
	inputPath := filepath.Join(root, "deck.pptx")
	before, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	request := "Improve slide 3 in deck.pptx"
	started := time.Now().UTC()
	user := storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: request, CreatedAt: started})
	route, err := runtime.routeIntentForTest(session.ID, user.ID, request, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		route.Facts[pptxScopeFact] != pptxScopeSingleSlide || route.Facts[pptxSlideIndexesFact] != "3" {
		t.Fatalf("real PPTX request did not freeze single-slide scope: %#v", route)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "received", StartedAt: started,
		MessageContext: &app.MessageRunContext{
			OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
			ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource}, Route: route,
		},
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	readCall, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "files.read", Args: map[string]any{"path": "deck.pptx"}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: documentLocateEvidenceNodeID, ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		t.Fatalf("real PPTX localization read failed: call=%#v approval=%#v", readCall, approval)
	}
	readDefinition, _ := runtime.tools.Definition("files.read")
	readOutcome, err := adaptWorkflowOutcome(readDefinition, readCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := testGetRun(st, dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(storedRun.Workflow, readOutcome)
	if changed, err := applyWorkflowOutcome(&storedRun, readOutcome, assessment); err != nil || !changed {
		t.Fatalf("real PPTX read did not activate operation selection: changed=%t err=%v", changed, err)
	}
	testSaveRun(st, storedRun)
	if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &storedRun, dispatch.Profile); err != nil || !changed {
		t.Fatalf("single-slide PPTX operation selection failed: changed=%t err=%v", changed, err)
	}
	if calls := countModelCalls(testListModelCalls(st, session.ID, storedRun.ID), "workflow_operation_selection", documentWorkflowModelLane); calls != 0 {
		t.Fatalf("deterministic single-slide operation selection called the model %d times", calls)
	}
	stageContext := dispatch.Profile.StageContext(storedRun.Workflow)
	tools, err := runtime.materializeActiveWorkflowTools(context.Background(), storedRun, runtime.workflowActorRef(storedRun), &stageContext)
	if err != nil || !exactVisibleToolNames(tools, "pptx.update_slide", "observation.read") {
		t.Fatalf("single-slide scope exposed the wrong operation: tools=%#v err=%v", visibleToolNames(tools), err)
	}
	if !containsString(stageContext.SemanticVariables, "pptx.update_slide.updates") {
		t.Fatalf("PPTX editor stage did not declare its semantic content variable: %#v", stageContext.SemanticVariables)
	}
	properties, _ := anyMap(tools[0].InputSchema["properties"])
	for _, name := range []string{"path", "output_path", "source_sha256", "slide_index"} {
		if _, exposed := properties[name]; exposed || slices.Contains(toolDefinitionRequiredArgs(tools[0].InputSchema), name) {
			t.Fatalf("runtime-owned PPTX argument %s leaked into the model schema: %#v", name, tools[0].InputSchema)
		}
	}
	updatesSchema, _ := anyMap(properties["updates"])
	updateItemSchema, _ := anyMap(updatesSchema["items"])
	updateProperties, _ := anyMap(updateItemSchema["properties"])
	if _, exposed := updateProperties["old_text"]; exposed || updateProperties["break_mode"] != nil ||
		slices.Contains(toolDefinitionRequiredArgs(updateItemSchema), "old_text") || intLikeValue(updatesSchema["maxItems"]) != pptxModelMaxTextUpdates {
		t.Fatalf("runtime-owned PPTX evidence or newline control leaked into the model schema: %#v", tools[0].InputSchema)
	}
	registeredUpdateSlide, ok := runtime.tools.Definition("pptx.update_slide")
	if !ok {
		t.Fatal("registered PPTX update-slide definition is missing")
	}
	registeredProperties, _ := anyMap(registeredUpdateSlide.InputSchema["properties"])
	registeredUpdates, _ := anyMap(registeredProperties["updates"])
	registeredUpdateItem, _ := anyMap(registeredUpdates["items"])
	for _, name := range []string{"path", "output_path", "source_sha256", "slide_index"} {
		if _, declared := registeredProperties[name]; !declared || !slices.Contains(toolDefinitionRequiredArgs(registeredUpdateSlide.InputSchema), name) {
			t.Fatalf("registered PPTX editor lost required Runtime argument %s: %#v", name, registeredUpdateSlide.InputSchema)
		}
	}
	if registeredUpdateProperties, _ := anyMap(registeredUpdateItem["properties"]); registeredUpdateProperties["old_text"] == nil || !slices.Contains(toolDefinitionRequiredArgs(registeredUpdateItem), "old_text") {
		t.Fatalf("registered PPTX editor lost required old_text validation: %#v", registeredUpdateSlide.InputSchema)
	}
	if refreshed, ok := testGetRun(st, storedRun.ID); ok {
		storedRun = refreshed
	}

	editCall, editApproval, _, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "pptx.update_slide",
		Args: map[string]any{
			"layout_policy": "preserve", "updates": []any{map[string]any{"shape_index": 1, "text": "Improved third title"}},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if editApproval == nil || editCall.Status != "approval_pending" || editCall.Arguments["path"] != "deck.pptx" ||
		editCall.Arguments["output_path"] != "deck-sparkclaw-edit.pptx" || intLikeValue(editCall.Arguments["slide_index"]) != 3 {
		t.Fatalf("real PPTX edit did not enter approval with frozen resources: call=%#v approval=%#v", editCall, editApproval)
	}
	readDocument, _ := anyMap(readCall.Result.(map[string]any)["document"])
	readMetadata, _ := anyMap(readDocument["metadata"])
	readRaw, _ := json.Marshal(readCall.Result)
	if readCall.Result.(map[string]any)["projection_schema"] != pptxBusinessProjectionSchema ||
		strings.Contains(string(readRaw), "text_structure") || strings.TrimSpace(stringValue(readMetadata["sha256"])) == "" ||
		editCall.Arguments["source_sha256"] != readMetadata["sha256"] {
		t.Fatalf("PPTX edit was not bound to the compact localization projection: read=%s args=%#v", readRaw, editCall.Arguments)
	}
	if !strings.Contains(editApproval.Summary, "第 3 页") || !strings.Contains(editApproval.Summary, "deck.pptx") {
		t.Fatalf("PPTX approval omitted its affected-slide summary: %#v", editApproval)
	}
	updates := anySlice(editCall.Arguments["updates"])
	update, _ := anyMap(updates[0])
	if update["old_text"] != "Original third title" {
		t.Fatalf("PPTX approval did not bind old_text from current read: %#v", editCall.Arguments)
	}
	if _, err := os.Stat(filepath.Join(root, "deck-sparkclaw-edit.pptx")); !os.IsNotExist(err) {
		t.Fatalf("PPTX output existed before approval: %v", err)
	}
	storedRun, _ = testGetRun(st, storedRun.ID)
	storedRun.State = "approval_pending"
	testSaveRun(st, storedRun)
	testSaveModelCall(st, app.ModelCall{
		ID: app.NewID("mcall"), SessionID: session.ID, RunID: storedRun.ID,
		Operation: "workflow_step_1", Status: "completed", StartedAt: time.Now().UTC(),
	})

	resolved, err := st.ResolveApproval(t.Context(), editApproval.ID, "approved", "owner approved PPTX copy")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "completed_after_approval" {
		t.Fatalf("approved PPTX edit did not execute: call=%#v err=%v", executed, err)
	}
	result, resumed, err := runtime.ResumeRunAfterApproval(context.Background(), session.ID, storedRun.ID)
	if err != nil || !resumed || result.WorkflowResult == nil || result.WorkflowResult.Status != app.WorkflowResultSucceeded {
		t.Fatalf("approved PPTX workflow did not complete: resumed=%t result=%#v err=%v", resumed, result, err)
	}
	if len(result.Message.Attachments) != 1 || result.Message.Attachments[0].RelPath != "deck-sparkclaw-edit.pptx" {
		t.Fatalf("PPTX workflow result omitted its output attachment: %#v", result.Message)
	}
	reread, err := runtime.tools.Execute(context.Background(), "files.read", map[string]any{"path": "deck-sparkclaw-edit.pptx"}, session.ID, storedRun.ID)
	if err != nil || !strings.Contains(reread.Output.(map[string]any)["content"].(string), "Improved third title") {
		t.Fatalf("approved PPTX output could not be reread: output=%#v err=%v", reread.Output, err)
	}
	after, err := os.ReadFile(inputPath)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatalf("approved PPTX edit modified its source: %v", err)
	}
	records := mustListAgentDocumentRecords(t, st, session.OwnerID, session.ID, 10)
	if len(records) < 2 || records[0].GovernedPath != "deck-sparkclaw-edit.pptx" || records[0].ParentDocumentID == "" {
		t.Fatalf("PPTX output lineage was not persisted: %#v", records)
	}
}

func TestApprovedPPTXMutationFailsWhenSourceChangesWhilePending(t *testing.T) {
	runtime, st, session, run, root, closeRuntime := prepareRealPPTXUpdateNode(t)
	defer closeRuntime()
	call, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
		Name:       "pptx.update_slide",
		Args:       map[string]any{"slide_index": 3, "updates": []any{map[string]any{"shape_index": 1, "text": "Approved replacement"}}},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if approval == nil || call.Status != "approval_pending" {
		t.Fatalf("PPTX edit did not wait for approval: call=%#v approval=%#v", call, approval)
	}
	writeAgentPPTXFixtureWithThirdTitle(t, root, "Changed while approval was pending")
	resolved, err := st.ResolveApproval(t.Context(), approval.ID, "approved", "approve stale PPTX regression")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "failed_after_approval" || !strings.Contains(strings.ToLower(executed.Error), "stale") {
		t.Fatalf("stale approved PPTX mutation did not fail closed: call=%#v err=%v", executed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "deck-sparkclaw-edit.pptx")); !os.IsNotExist(err) {
		t.Fatalf("stale approved PPTX mutation left an output: %v", err)
	}
}

func writeAgentPPTXFixture(t *testing.T, root string) {
	writeAgentPPTXFixtureWithThirdTitle(t, root, "Original third title")
}

func writeAgentPPTXFixtureWithThirdTitle(t *testing.T, root, thirdTitle string) {
	t.Helper()
	const script = `
from pathlib import Path
from pptx import Presentation
from pptx.util import Inches
root = Path(__import__("sys").argv[1])
third_title = __import__("sys").argv[2]
prs = Presentation()
for index in range(1, 4):
    slide = prs.slides.add_slide(prs.slide_layouts[6])
    title = slide.shapes.add_textbox(Inches(1), Inches(1), Inches(7), Inches(.7))
    title.text = third_title if index == 3 else "Slide %d" % index
    if index == 3:
        group = slide.shapes.add_group_shape()
        child = group.shapes.add_textbox(Inches(1), Inches(2), Inches(3), Inches(.5))
        child.text = "Grouped text"
prs.save(root / "deck.pptx")
`
	cmd := exec.Command("python3", "-c", script, root, thirdTitle)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create agent PPTX fixture: %v\n%s", err, out)
	}
}

func prepareRealPPTXUpdateNode(t *testing.T) (Runtime, *store.MemoryStore, app.Session, app.AgentRun, string, func()) {
	return prepareRealPPTXEditorNode(t, "Improve slide 3 in deck.pptx", "pptx.update_slide", "update_slide")
}

func prepareRealPPTXEditorNode(t *testing.T, request, selectedTool, selectedOperation string) (Runtime, *store.MemoryStore, app.Session, app.AgentRun, string, func()) {
	t.Helper()
	root := t.TempDir()
	writeAgentPPTXFixture(t, root)
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	route, err := runtime.routeIntentForTest(session.ID, "turn", request, agentContextSnapshot{})
	if err != nil {
		closeRuntime()
		t.Fatal(err)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		closeRuntime()
		t.Fatal(err)
	}
	readCall, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "files.read", Args: map[string]any{"path": "deck.pptx"}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: documentLocateEvidenceNodeID, ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		closeRuntime()
		t.Fatalf("prepare real PPTX read: call=%#v approval=%#v", readCall, approval)
	}
	definition, _ := runtime.tools.Definition("files.read")
	outcome, err := adaptWorkflowOutcome(definition, readCall)
	if err != nil {
		closeRuntime()
		t.Fatal(err)
	}
	run, _ := testGetRun(st, dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(run.Workflow, outcome)
	if changed, err := applyWorkflowOutcome(&run, outcome, assessment); err != nil || !changed {
		closeRuntime()
		t.Fatalf("prepare real PPTX outcome: changed=%t err=%v", changed, err)
	}
	testSaveRun(st, run)
	definition, ok := runtime.tools.Definition(selectedTool)
	if !ok {
		closeRuntime()
		t.Fatalf("prepare real PPTX editor %q is unavailable", selectedTool)
	}
	decisionState := run.Workflow.Nodes["select_edit_operation"]
	selectedEntry := app.ToolDirectoryEntryID("")
	for _, capability := range definition.Capabilities {
		if capability.Name == app.ToolCapabilityDocumentEdit &&
			capability.Qualifiers[app.CapabilityQualifierOperation] == selectedOperation &&
			matchesAnyRequirement(capability, decisionState.CurrentScope.Requirements) {
			selectedEntry = directoryEntryID(definition, capability)
			break
		}
	}
	if selectedEntry == "" {
		closeRuntime()
		t.Fatalf("prepare real PPTX editor %q operation %q is outside the decision scope", selectedTool, selectedOperation)
	}
	run.Workflow.Route.Slots.Query += mockWorkflowDecisionSelectedResponse(selectedEntry)
	testSaveRun(st, run)
	if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &run, dispatch.Profile); err != nil || !changed {
		closeRuntime()
		t.Fatalf("prepare real PPTX decision: changed=%t err=%v", changed, err)
	}
	stageContext := dispatch.Profile.StageContext(run.Workflow)
	tools, err := runtime.materializeActiveWorkflowTools(context.Background(), run, runtime.workflowActorRef(run), &stageContext)
	if err != nil || !exactVisibleToolNames(tools, selectedTool, "observation.read") {
		closeRuntime()
		t.Fatalf("prepare real PPTX tool: tools=%#v err=%v", visibleToolNames(tools), err)
	}
	run, _ = testGetRun(st, run.ID)
	if evidence, err := runtime.currentPPTXWorkflowEditEvidence(context.Background(), run, map[string]any{"path": "deck.pptx"}); err != nil || evidence.SourceSHA256 == "" {
		closeRuntime()
		t.Fatalf("prepare real PPTX editor lost its compact localization evidence: evidence=%#v err=%v", evidence, err)
	}
	return runtime, st, session, run, root, closeRuntime
}

func pptxEvidenceBlock(slideIndex, shapeIndex, groupChildIndex int, text string, editable bool) map[string]any {
	location := map[string]any{
		"slide_index": slideIndex, "shape_index": shapeIndex, "block_type": "shape_text",
		"path": "presentation.slide[" + intText(slideIndex) + "].shape[" + intText(shapeIndex) + "]",
	}
	if groupChildIndex > 0 {
		location["group_child_index"] = groupChildIndex
	}
	return map[string]any{"kind": "shape_text", "text": text, "location": location, "format_metadata": map[string]any{"editable": editable}}
}

func intText(value int) string {
	const digits = "0123456789"
	if value < 10 {
		return string(digits[value])
	}
	return intText(value/10) + string(digits[value%10])
}

func equalIntSlices(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
