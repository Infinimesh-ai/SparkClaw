package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const xlsxOperationSelectionCorpusPath = "../../../../eval/golden/xlsx-operation-selection.json"

var xlsxEvaluatedOperations = []string{
	"replace_text",
	"update_cell",
	"update_row",
	"insert_row",
	"append_row",
	"delete_row",
}

type xlsxOperationSelectionCorpus struct {
	SchemaVersion string `json:"schema_version"`
	ReleaseGates  struct {
		OverallExactOperationAccuracy float64 `json:"overall_exact_operation_accuracy"`
		DeleteRowAccuracy             float64 `json:"delete_row_accuracy"`
		HardNegativeAccuracy          float64 `json:"hard_negative_accuracy"`
		CrossFormatSelections         int     `json:"cross_format_selections"`
	} `json:"release_gates"`
	Cases []xlsxOperationSelectionCase `json:"cases"`
}

type xlsxOperationSelectionCase struct {
	ID                string   `json:"id"`
	Language          string   `json:"language"`
	Prompt            string   `json:"prompt"`
	ExpectedOperation string   `json:"expected_operation"`
	EvidenceProfile   string   `json:"evidence_profile"`
	Tags              []string `json:"tags"`
}

type xlsxOperationDirectory struct {
	entries         []app.ToolDirectoryEntry
	operationByID   map[app.ToolDirectoryEntryID]string
	formatByEntryID map[app.ToolDirectoryEntryID]string
}

func TestXLSXOperationSelectionCorpusCoverage(t *testing.T) {
	corpus := loadXLSXOperationSelectionCorpus(t)
	if corpus.SchemaVersion != "xlsx_operation_selection_eval_v1" {
		t.Fatalf("unexpected corpus schema %q", corpus.SchemaVersion)
	}
	if corpus.ReleaseGates.OverallExactOperationAccuracy != 0.95 || corpus.ReleaseGates.DeleteRowAccuracy != 1 ||
		corpus.ReleaseGates.HardNegativeAccuracy != 1 || corpus.ReleaseGates.CrossFormatSelections != 0 {
		t.Fatalf("unexpected XLSX release gates: %#v", corpus.ReleaseGates)
	}

	wantedOperations := stringSet(xlsxEvaluatedOperations)
	wantedProfiles := stringSet([]string{"small", "near_budget", "multi_sheet", "formulas", "formatted_numbers", "hidden_rows", "merged_cells"})
	wantedNegativeTags := stringSet([]string{"negation", "quotation", "troubleshooting", "unsupported_operation", "ambiguous_target", "whole_file_delete"})
	operationLanguages := map[string]map[string]int{}
	profileCounts := map[string]int{}
	negativeTagLanguages := map[string]map[string]int{}
	seenIDs := map[string]bool{}
	positiveCount := 0
	negativeCount := 0

	for _, testCase := range corpus.Cases {
		if strings.TrimSpace(testCase.ID) == "" || seenIDs[testCase.ID] {
			t.Fatalf("case ID is empty or duplicated: %q", testCase.ID)
		}
		seenIDs[testCase.ID] = true
		if strings.TrimSpace(testCase.Prompt) == "" {
			t.Fatalf("case %s has an empty prompt", testCase.ID)
		}
		if testCase.Language != "en" && testCase.Language != "zh-CN" {
			t.Fatalf("case %s has unsupported language %q", testCase.ID, testCase.Language)
		}
		if !wantedProfiles[testCase.EvidenceProfile] {
			t.Fatalf("case %s has unsupported evidence profile %q", testCase.ID, testCase.EvidenceProfile)
		}
		profileCounts[testCase.EvidenceProfile]++

		if testCase.ExpectedOperation != "" {
			if !wantedOperations[testCase.ExpectedOperation] {
				t.Fatalf("case %s has unsupported expected operation %q", testCase.ID, testCase.ExpectedOperation)
			}
			positiveCount++
			if operationLanguages[testCase.ExpectedOperation] == nil {
				operationLanguages[testCase.ExpectedOperation] = map[string]int{}
			}
			operationLanguages[testCase.ExpectedOperation][testCase.Language]++
			continue
		}

		negativeCount++
		if !containsString(testCase.Tags, "hard_negative") {
			t.Fatalf("negative case %s is missing hard_negative tag", testCase.ID)
		}
		category := ""
		for _, tag := range testCase.Tags {
			if wantedNegativeTags[tag] {
				category = tag
				break
			}
		}
		if category == "" {
			t.Fatalf("negative case %s has no required category: %v", testCase.ID, testCase.Tags)
		}
		if negativeTagLanguages[category] == nil {
			negativeTagLanguages[category] = map[string]int{}
		}
		negativeTagLanguages[category][testCase.Language]++
	}

	if positiveCount != 48 || negativeCount != 12 || len(corpus.Cases) != 60 {
		t.Fatalf("unexpected corpus size: positives=%d negatives=%d total=%d", positiveCount, negativeCount, len(corpus.Cases))
	}
	for operation := range wantedOperations {
		languages := operationLanguages[operation]
		if languages["en"] != 4 || languages["zh-CN"] != 4 {
			t.Fatalf("operation %s language coverage = %#v, want en=4 zh-CN=4", operation, languages)
		}
	}
	for category := range wantedNegativeTags {
		languages := negativeTagLanguages[category]
		if languages["en"] != 1 || languages["zh-CN"] != 1 {
			t.Fatalf("hard-negative category %s language coverage = %#v, want en=1 zh-CN=1", category, languages)
		}
	}
	for profile := range wantedProfiles {
		if profileCounts[profile] == 0 {
			t.Fatalf("evidence profile %s has no cases", profile)
		}
	}
}

func TestXLSXOperationSelectionPromptUsesProductionDirectoryAndEvidence(t *testing.T) {
	directory := loadXLSXOperationDirectory(t)
	if len(directory.entries) != len(xlsxEvaluatedOperations) {
		t.Fatalf("XLSX directory entries=%d want=%d: %#v", len(directory.entries), len(xlsxEvaluatedOperations), directory.entries)
	}
	candidateProjection, bindings, err := buildWorkflowDecisionCandidateProjection(directory.entries)
	if err != nil {
		t.Fatal(err)
	}
	run, node := xlsxOperationSelectionPromptFixture("Set Data!B12 to 42.5.", 0)
	evidence := xlsxOperationSelectionEvidence(t, "formatted_numbers")
	system, user := workflowDecisionSelectionPromptWithLimit(run, documentEditProfile{}, node, candidateProjection, evidence, 8000)

	for _, entry := range directory.entries {
		operation := entry.Capability.Qualifiers[app.CapabilityQualifierOperation]
		candidateID := workflowDecisionCandidateID(entry.ID)
		if entry.ID == "" || operation == "" || !strings.Contains(user, candidateID) ||
			!strings.Contains(user, entry.WhenToUse) || !strings.Contains(user, entry.WhenNotToUse) {
			t.Fatalf("production prompt omitted XLSX %s directory boundary: %s", operation, user)
		}
		if bindings[candidateID] != entry.ID || strings.Contains(user, string(entry.ID)) {
			t.Fatalf("production prompt leaked or failed to bind XLSX directory identity: %s", user)
		}
	}
	for _, want := range []string{"named sheet may narrow", "cannot supply a missing target or value", "multiple supplied fields", "complete row", "schema_version", "formatted_numbers", "placement", "preservation_behavior"} {
		if !strings.Contains(system+"\n"+user, want) {
			t.Fatalf("production selection prompt omitted %q", want)
		}
	}
	if got := len(xlsxOperationSelectionEvidence(t, "near_budget")); got < 7000 || got > 8000 {
		t.Fatalf("near-budget evidence bytes=%d, want 7000..8000", got)
	}
}

func TestXLSXOperationSelectionScoringRequiresCompletedSelection(t *testing.T) {
	tests := []struct {
		name      string
		completed bool
		selected  string
		expected  string
		want      bool
	}{
		{name: "completed positive", completed: true, selected: "update_cell", expected: "update_cell", want: true},
		{name: "completed negative", completed: true, selected: "", expected: "", want: true},
		{name: "model error negative", completed: false, selected: "", expected: "", want: false},
		{name: "invalid json negative", completed: false, selected: "", expected: "", want: false},
		{name: "wrong operation", completed: true, selected: "update_cell", expected: "replace_text", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := xlsxOperationSelectionCaseCorrect(test.completed, test.selected, test.expected); got != test.want {
				t.Fatalf("case correctness=%t want=%t", got, test.want)
			}
		})
	}
}

func TestRealFastXLSXOperationSelection(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_XLSX_OPERATION_EVAL") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_XLSX_OPERATION_EVAL=1 to call the configured Fast model")
	}
	corpus := loadXLSXOperationSelectionCorpus(t)
	directory := loadXLSXOperationDirectory(t)
	candidateProjection, bindings, err := buildWorkflowDecisionCandidateProjection(directory.entries)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
	models := modelrouter.New(cfg)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()

	correct := 0
	deleteCorrect := 0
	deleteTotal := 0
	negativeCorrect := 0
	negativeTotal := 0
	crossFormatSelections := 0
	for _, testCase := range corpus.Cases {
		evidence := xlsxOperationSelectionEvidence(t, testCase.EvidenceProfile)
		selectedID := app.ToolDirectoryEntryID("")
		selectedOperation := ""
		modelName := ""
		profileName := ""
		reason := ""
		retries := 0
		selectionCompleted := false
		crossFormatSelected := false

		for attempt := 0; attempt < 2; attempt++ {
			run, node := xlsxOperationSelectionPromptFixture(testCase.Prompt, attempt)
			system, user := workflowDecisionSelectionPromptWithLimit(run, documentEditProfile{}, node, candidateProjection, evidence, 8000)
			chat, callErr := models.ChatWithProfile(ctx, modelcapacity.OperationWorkflowDecision, "fast", system, user)
			if callErr != nil {
				reason = "model_error: " + callErr.Error()
				if attempt == 0 {
					retries++
					continue
				}
				break
			}
			if chat.Mock {
				t.Fatal("real XLSX operation eval resolved to the mock model")
			}
			modelName, profileName = chat.Model, chat.Profile
			selection, parseErr := parseWorkflowDecisionSelection(chat.Content)
			if parseErr != nil {
				reason = "invalid_json: " + parseErr.Error()
				if attempt == 0 {
					retries++
					continue
				}
				break
			}
			selectedID = bindings[selection.CandidateID]
			selectedOperation = directory.operationByID[selectedID]
			if format := directory.formatByEntryID[selectedID]; selectedID != "" && format != "" && format != app.DocumentFormatXLSX {
				crossFormatSelected = true
			}
			if selectedID != "" && selectedOperation == "" {
				reason = "entry_outside_active_xlsx_view"
				if attempt == 0 {
					retries++
					continue
				}
				break
			}
			if selectedID == "" && testCase.ExpectedOperation != "" {
				reason = "empty_entry_for_supported_operation"
				if attempt == 0 {
					retries++
					continue
				}
				break
			}
			reason = "selection_completed"
			selectionCompleted = true
			break
		}
		if crossFormatSelected {
			crossFormatSelections++
		}

		caseCorrect := xlsxOperationSelectionCaseCorrect(selectionCompleted, selectedOperation, testCase.ExpectedOperation)
		if caseCorrect {
			correct++
			reason = "exact_operation_match"
		} else {
			reason = fmt.Sprintf("%s; expected=%q actual=%q", reason, testCase.ExpectedOperation, selectedOperation)
		}
		if testCase.ExpectedOperation == "delete_row" {
			deleteTotal++
			if caseCorrect {
				deleteCorrect++
			}
		}
		if testCase.ExpectedOperation == "" {
			negativeTotal++
			if caseCorrect {
				negativeCorrect++
			}
		}
		t.Logf("case=%s model=%q profile=%q entry=%q operation=%q retries=%d reason=%q",
			testCase.ID, modelName, profileName, selectedID, selectedOperation, retries, reason)
	}

	overallAccuracy := float64(correct) / float64(len(corpus.Cases))
	deleteAccuracy := float64(deleteCorrect) / float64(deleteTotal)
	negativeAccuracy := float64(negativeCorrect) / float64(negativeTotal)
	t.Logf("xlsx_operation_selection overall=%d/%d %.4f delete=%d/%d %.4f hard_negative=%d/%d %.4f cross_format=%d",
		correct, len(corpus.Cases), overallAccuracy, deleteCorrect, deleteTotal, deleteAccuracy,
		negativeCorrect, negativeTotal, negativeAccuracy, crossFormatSelections)
	if overallAccuracy < corpus.ReleaseGates.OverallExactOperationAccuracy {
		t.Errorf("overall exact operation accuracy %.4f is below %.4f", overallAccuracy, corpus.ReleaseGates.OverallExactOperationAccuracy)
	}
	if deleteAccuracy < corpus.ReleaseGates.DeleteRowAccuracy {
		t.Errorf("delete_row accuracy %.4f is below %.4f", deleteAccuracy, corpus.ReleaseGates.DeleteRowAccuracy)
	}
	if negativeAccuracy < corpus.ReleaseGates.HardNegativeAccuracy {
		t.Errorf("hard-negative accuracy %.4f is below %.4f", negativeAccuracy, corpus.ReleaseGates.HardNegativeAccuracy)
	}
	if crossFormatSelections != corpus.ReleaseGates.CrossFormatSelections {
		t.Errorf("cross-format selections=%d want=%d", crossFormatSelections, corpus.ReleaseGates.CrossFormatSelections)
	}
}

func xlsxOperationSelectionCaseCorrect(selectionCompleted bool, selectedOperation, expectedOperation string) bool {
	return selectionCompleted && selectedOperation == expectedOperation
}

func loadXLSXOperationSelectionCorpus(t *testing.T) xlsxOperationSelectionCorpus {
	t.Helper()
	content, err := os.ReadFile(xlsxOperationSelectionCorpusPath)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	var corpus xlsxOperationSelectionCorpus
	if err := decoder.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	return corpus
}

func loadXLSXOperationDirectory(t *testing.T) xlsxOperationDirectory {
	t.Helper()
	hub := toolhub.New(configtest.MustLoadDefault(), store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	wantedOperations := stringSet(xlsxEvaluatedOperations)
	directory := xlsxOperationDirectory{
		operationByID:   map[app.ToolDirectoryEntryID]string{},
		formatByEntryID: map[app.ToolDirectoryEntryID]string{},
	}
	for _, definition := range hub.Definitions() {
		for _, capability := range definition.Capabilities {
			entryID := directoryEntryID(definition, capability)
			directory.formatByEntryID[entryID] = capability.Qualifiers[app.CapabilityQualifierFormat]
			operation := capability.Qualifiers[app.CapabilityQualifierOperation]
			if capability.Name != app.ToolCapabilityDocumentEdit || capability.Qualifiers[app.CapabilityQualifierFormat] != app.DocumentFormatXLSX || !wantedOperations[operation] {
				continue
			}
			directory.operationByID[entryID] = operation
			directory.entries = append(directory.entries, app.ToolDirectoryEntry{
				ID: entryID, Capability: capability, Summary: definition.Directory.Summary,
				WhenToUse: definition.Directory.WhenToUse, WhenNotToUse: definition.Directory.WhenNotToUse,
				Effects: append([]app.ToolEffect(nil), definition.Directory.Effects...), Risk: definition.Risk,
			})
		}
	}
	sort.Slice(directory.entries, func(i, j int) bool { return directory.entries[i].ID < directory.entries[j].ID })
	return directory
}

func xlsxOperationSelectionPromptFixture(query string, attempts int) (app.AgentRun, app.WorkflowNode) {
	node := app.WorkflowNode{
		ID: "select_edit_operation",
		Goal: app.NodeGoal{
			Summary:    "Select the exact XLSX edit operation supported by the located evidence.",
			Completion: app.CompletionDecision,
		},
	}
	run := app.AgentRun{Workflow: &app.WorkflowState{
		Route: app.RouteDecision{Slots: app.RouteSlots{Query: query}},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{node.ID: {Attempts: attempts}},
	}}
	return run, node
}

func xlsxOperationSelectionEvidence(t *testing.T, profile string) string {
	t.Helper()
	base := map[string]any{
		"schema_version":     "xlsx_sheet_evidence_v1",
		"source_complete":    true,
		"selection_complete": true,
		"evidence_profile":   profile,
		"sheets": []any{
			map[string]any{"name": "Data", "index": 1, "state": "visible", "rows": []any{
				map[string]any{"index": 1, "cells": []any{map[string]any{"address": "A1", "value_kind": "string", "raw_value": "Order ID"}, map[string]any{"address": "B1", "value_kind": "string", "raw_value": "Status"}, map[string]any{"address": "C1", "value_kind": "string", "raw_value": "Amount"}}},
				map[string]any{"index": 8, "hidden": profile == "hidden_rows", "cells": []any{map[string]any{"address": "A8", "value_kind": "string", "raw_value": "INV-900"}, map[string]any{"address": "B8", "value_kind": "string", "raw_value": "Alice"}}},
				map[string]any{"index": 12, "cells": []any{map[string]any{"address": "A12", "value_kind": "string", "raw_value": "A-104"}, map[string]any{"address": "B12", "value_kind": "number", "raw_value": 42.5, "display_text": "42.50", "number_format": "0.00"}, map[string]any{"address": "C12", "value_kind": "string", "raw_value": "Pending"}}},
			}},
		},
		"omitted": map[string]any{"sheets": 0, "rows": 0, "cells": 0, "reason": ""},
	}
	sheets := base["sheets"].([]any)
	switch profile {
	case "small":
	case "multi_sheet":
		sheets = append(sheets,
			xlsxSelectionEvidenceSheet("Status", 2, 2, "awaiting review", false),
			xlsxSelectionEvidenceSheet("Scores", 3, 2, "Bravo", false),
			xlsxSelectionEvidenceSheet("Archive", 4, 8, "obsolete", true),
		)
	case "formulas":
		sheets = append(sheets, map[string]any{
			"name": "Calc", "index": 2, "state": "visible",
			"rows": []any{map[string]any{
				"index": 2,
				"cells": []any{map[string]any{
					"address": "C2", "value_kind": "formula", "formula": "SUM(Data!B2:B12)", "display_text": "84",
				}},
			}},
		})
	case "formatted_numbers", "hidden_rows":
	case "merged_cells":
		base["merged_ranges"] = []string{"A20:C20"}
	case "near_budget":
		padding := make([]any, 0, 35)
		for row := 20; row < 55; row++ {
			padding = append(padding, map[string]any{"index": row, "cells": []any{
				map[string]any{"address": fmt.Sprintf("A%d", row), "value_kind": "string", "raw_value": fmt.Sprintf("unrelated-ledger-record-%03d", row)},
				map[string]any{"address": fmt.Sprintf("B%d", row), "value_kind": "string", "raw_value": "bounded evidence filler"},
			}})
		}
		sheets = append(sheets, map[string]any{"name": "History", "index": 2, "state": "visible", "rows": padding})
		base["omitted"] = map[string]any{"sheets": 0, "rows": 91, "cells": 273, "reason": "byte_budget"}
	default:
		t.Fatalf("unknown XLSX evidence profile %q", profile)
	}
	base["sheets"] = sheets
	content, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if profile == "near_budget" && len(content) > 8000 {
		t.Fatalf("near-budget evidence exceeds production slice: %d bytes", len(content))
	}
	return string(content)
}

func xlsxSelectionEvidenceSheet(name string, index, row int, value string, hidden bool) map[string]any {
	return map[string]any{
		"name": name, "index": index, "state": "visible",
		"rows": []any{map[string]any{
			"index": row, "hidden": hidden,
			"cells": []any{map[string]any{
				"address": fmt.Sprintf("A%d", row), "value_kind": "string", "raw_value": value,
			}},
		}},
	}
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
