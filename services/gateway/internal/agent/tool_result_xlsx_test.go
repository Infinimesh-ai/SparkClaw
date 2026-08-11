package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestXLSXStructuredEvidencePrioritizesExplicitTailTarget(t *testing.T) {
	output := xlsxProjectionFixture(80)
	text := sliceDocumentStructuredEvidenceForRequest(output, 8000, "Update Data!B80 to 900")
	var projection map[string]any
	if err := json.Unmarshal([]byte(text), &projection); err != nil {
		t.Fatalf("XLSX evidence is not one valid JSON projection: %v\n%s", err, text)
	}
	if projection["schema_version"] != "xlsx_sheet_evidence_v1" || projection["selection_complete"] != true {
		t.Fatalf("unexpected projection contract: %#v", projection)
	}
	if !strings.Contains(text, `"address":"B80"`) {
		t.Fatalf("explicit tail target was not retained: %s", text)
	}
	for _, runtimeField := range []string{`"source"`, `"path"`, `"rel_path"`, `"source_hash"`, `"style_hash"`} {
		if strings.Contains(text, runtimeField) {
			t.Fatalf("XLSX model projection exposes Runtime-owned field %s: %s", runtimeField, text)
		}
	}
	omitted, _ := anyMap(projection["omitted"])
	if intLikeValue(omitted["rows"]) == 0 || intLikeValue(omitted["cells"]) == 0 {
		t.Fatalf("projection did not disclose omitted evidence: %#v", omitted)
	}
	if len([]byte(text)) > 8000 {
		t.Fatalf("projection exceeded its byte budget: %d", len([]byte(text)))
	}
}

func TestXLSXStructuredEvidenceFailsClosedWhenExplicitTargetCannotFit(t *testing.T) {
	text := sliceDocumentStructuredEvidenceForRequest(xlsxProjectionFixture(2), 420, "Update Data!B2")
	var projection map[string]any
	if err := json.Unmarshal([]byte(text), &projection); err != nil {
		t.Fatalf("small XLSX projection is invalid JSON: %v\n%s", err, text)
	}
	if projection["selection_complete"] != false {
		t.Fatalf("unfitted explicit target did not fail closed: %#v", projection)
	}
}

func TestDocumentReadEvidenceUsesTypedXLSXProjection(t *testing.T) {
	output := xlsxProjectionFixture(3)
	output["content"] = "Name\tScore\nAlice\t88"
	evidence := toolResultEvidenceForRequest(app.ToolCall{Tool: "files.read"}, output, 4000, "")
	found := false
	for _, item := range evidence {
		if item.Kind == "document.xlsx_sheets" && strings.Contains(item.Text, `"value_kind":"number"`) {
			found = true
		}
		if strings.HasPrefix(item.Kind, "content_") {
			t.Fatalf("XLSX observation still relies on flattened tabular text: %#v", item)
		}
	}
	if !found {
		t.Fatalf("typed XLSX evidence projection is missing: %#v", evidence)
	}
}

func TestDocumentReadEvidencePrioritizesOwnerRequestedXLSXCell(t *testing.T) {
	evidence := toolResultEvidenceForRequest(
		app.ToolCall{Tool: "files.read"}, xlsxProjectionFixture(80), 4000, "Read Data!B80",
	)
	if len(evidence) != 1 || evidence[0].Kind != "document.xlsx_sheets" || !strings.Contains(evidence[0].Text, `"address":"B80"`) {
		t.Fatalf("document.read did not retain the owner-requested XLSX cell: %#v", evidence)
	}
}

func TestWorkflowXLSXEvidenceUsesOwnerRequestAndAuditsSelectionCounts(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	now := time.Now().UTC()
	st.AddMessage(app.Message{
		SessionID: session.ID, Role: "user", Content: "Update Data!B80 to 900", CreatedAt: now.Add(-time.Second),
	})
	run, call := archivedEvidenceFixture(t, runtime, st, session.ID, "files.read", xlsxProjectionFixture(80))
	run.Workflow = &app.WorkflowState{
		Status: app.WorkflowStatusRunning,
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"source": {Status: app.WorkflowNodeSucceeded, ToolCallIDs: []string{call.ID}},
		},
	}
	provisioned, err := runtime.provisionWorkflowEvidence(context.Background(), run, []workflowEvidenceRequirement{{
		SourceNodeID: "source", Mode: workflowEvidenceStructured, MaxBytes: 8000,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(provisioned.Text, `"address":"B80"`) {
		t.Fatalf("owner-request target was not provisioned: %s", provisioned.Text)
	}
	var fields map[string]any
	for _, event := range st.ListAudit(session.ID) {
		if event.Type == "workflow_step.evidence_provisioned" {
			fields = event.Fields
		}
	}
	if fields["evidence_schema_version"] != xlsxSheetEvidenceSchemaVersion ||
		intLikeValue(fields["selected_rows"]) == 0 || intLikeValue(fields["omitted_rows"]) == 0 ||
		fields["selection_complete"] != true {
		t.Fatalf("XLSX evidence audit counts are incomplete: %#v", fields)
	}
}

func xlsxProjectionFixture(rows int) map[string]any {
	values := make([]any, 0, rows)
	for row := 1; row <= rows; row++ {
		values = append(values, map[string]any{
			"index": row, "hidden": false, "source_hash": fmt.Sprintf("sha256:row-%d", row),
			"cells": []any{
				map[string]any{"address": fmt.Sprintf("A%d", row), "column": 1, "value_kind": "string", "raw_value": fmt.Sprintf("Person %d", row), "display_text": fmt.Sprintf("Person %d", row), "formula": "", "number_format": "", "hidden": false, "style_hash": "sha256:style", "merge_anchor": "", "source_hash": fmt.Sprintf("sha256:a-%d", row)},
				map[string]any{"address": fmt.Sprintf("B%d", row), "column": 2, "value_kind": "number", "raw_value": row, "display_text": fmt.Sprint(row), "formula": "", "number_format": "0", "hidden": false, "style_hash": "sha256:style", "merge_anchor": "", "source_hash": fmt.Sprintf("sha256:b-%d", row)},
			},
		})
	}
	return map[string]any{
		"path": "book.xlsx", "rel_path": "book.xlsx", "truncated": false,
		"document": map[string]any{
			"format": "xlsx", "strategy": map[string]any{"complete": true},
			"sheets": []any{map[string]any{"name": "Data", "index": 1, "state": "visible", "source_hash": "sha256:sheet", "rows": values}},
		},
	}
}
