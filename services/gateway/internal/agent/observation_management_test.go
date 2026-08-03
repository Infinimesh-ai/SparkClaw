package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestWorkflowEvidenceProvisioningReadsPersistedNodeOutput(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	run, call := archivedEvidenceFixture(t, runtime, st, session.ID, "files.read", map[string]any{
		"path": "report.txt", "content": "first paragraph\nsecond paragraph", "truncated": false,
	})
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
	if !strings.Contains(provisioned.Text, "report.txt") || !strings.Contains(provisioned.Text, "first paragraph") {
		t.Fatalf("persisted evidence was not provisioned: %s", provisioned.Text)
	}
	if !hasAgentAuditType(st.ListAudit(session.ID), "workflow_step.evidence_provisioned") {
		t.Fatalf("provisioning audit is missing: %#v", st.ListAudit(session.ID))
	}

	call.ObservationRef = ""
	st.SaveToolCall(call)
	if _, err := runtime.provisionWorkflowEvidence(context.Background(), run, []workflowEvidenceRequirement{{
		SourceNodeID: "source", Mode: workflowEvidenceHead, MaxBytes: 1000,
	}}); err == nil || !strings.Contains(err.Error(), "persisted observation reference") {
		t.Fatalf("missing required evidence did not fail closed: %v", err)
	}
}

func TestWorkflowEvidenceDegradationNeverExpandsSmallSlice(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	run, call := archivedEvidenceFixture(t, runtime, st, session.ID, "files.read", map[string]any{
		"path": "report.txt", "content": strings.Repeat("evidence ", 200),
	})
	run.Workflow = &app.WorkflowState{
		Status: app.WorkflowStatusRunning,
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"source": {Status: app.WorkflowNodeSucceeded, ToolCallIDs: []string{call.ID}},
		},
	}
	provisioned, err := runtime.provisionWorkflowEvidence(context.Background(), run, []workflowEvidenceRequirement{{
		SourceNodeID: "source", Mode: workflowEvidenceHead, MaxBytes: 300,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(provisioned.CompactText) > len(provisioned.Text) || len(provisioned.MinimalText) > len(provisioned.CompactText) {
		t.Fatalf("evidence degradation expanded a slice: full=%d compact=%d minimal=%d", len(provisioned.Text), len(provisioned.CompactText), len(provisioned.MinimalText))
	}
}

func TestStructuredBrowserEvidenceKeepsWholeControlRefs(t *testing.T) {
	output := map[string]any{"output": map[string]any{
		"schema_version": "browser_interaction_snapshot_v1", "snapshot_id": "snapshot_1", "page_id": "page_1",
		"controls": []any{
			map[string]any{"ref": "snapshot_1:e1", "role": "button", "accessible_name": "Open"},
			map[string]any{"ref": "snapshot_1:e2", "role": "button", "accessible_name": strings.Repeat("large", 300)},
		},
	}}
	text := slicePersistedToolEvidence("browser.snapshot", output, workflowEvidenceStructured, 480)
	if !json.Valid([]byte(text)) || !strings.Contains(text, "snapshot_1:e1") || strings.Contains(text, "snapshot_1:e2") {
		t.Fatalf("structured browser slice cut or over-admitted controls: %s", text)
	}
}

func TestUniformObservationBudgetDoesNotDependOnToolName(t *testing.T) {
	runtime, _, _, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	maxBytes, evidenceBytes := runtime.toolResultObservationBudget()
	if maxBytes != 2400 || evidenceBytes != defaultToolResultEvidenceLimit {
		t.Fatalf("observation envelope still depends on run or tool budgets: %d %d", maxBytes, evidenceBytes)
	}
}

func TestRollingObservationCompactionPreservesRecentEntries(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	observations := []string{}
	for index := 0; index < 6; index++ {
		call := app.ToolCall{ID: app.NewID("tc"), Tool: "files.read", Status: "completed"}
		observations = append(observations, adaptToolResult(toolResultAdapterInput{
			Call: call, Output: map[string]any{"path": "file.txt", "content": strings.Repeat("evidence ", 300)}, MaxBytes: 1600,
		}))
	}
	lastTwo := append([]string(nil), observations[len(observations)-2:]...)
	budget := &workflowRunBudget{MaxObservationBytes: observationsBytes(observations) - 1}
	compacted := runtime.compactWorkflowObservationsIfNeeded(session.ID, "run_compact", observations, budget)
	if compacted[len(compacted)-2] != lastTwo[0] || compacted[len(compacted)-1] != lastTwo[1] {
		t.Fatal("rolling compaction changed one of the newest two observations")
	}
	if observationsBytes(compacted) >= observationsBytes(observations) || !strings.Contains(compacted[0], "compacted=true") {
		t.Fatalf("older observations were not compacted: before=%d after=%d first=%s", observationsBytes(observations), observationsBytes(compacted), compacted[0])
	}
	if !hasAgentAuditType(st.ListAudit(session.ID), "workflow_step.observations_compacted") {
		t.Fatal("rolling compaction audit is missing")
	}
}

func TestContextBuilderDegradesLowerPrioritySectionFirst(t *testing.T) {
	builder := contextBuilder{Sections: []contextSection{
		degradingContextSection("low", 10, strings.Repeat("low ", 1000), "low compact", true),
		staticContextSection("required", 100, "required contract"),
	}}
	rendered := builder.Render(30)
	if !strings.Contains(rendered, "required contract") || strings.Contains(rendered, strings.Repeat("low ", 20)) {
		t.Fatalf("context builder degraded the wrong section: %s", rendered)
	}
}

func TestWorkflowFinalEvidencePacksMultipleObservations(t *testing.T) {
	evidence := workflowFinalEvidence(nil, []string{"first observation", "second observation"})
	if len(evidence) != 2 || evidence[0] != "first observation" || evidence[1] != "second observation" {
		t.Fatalf("finalizer did not pack multiple observations: %#v", evidence)
	}
}

func newObservationManagementRuntime(t *testing.T) (Runtime, *store.MemoryStore, app.Session, func()) {
	t.Helper()
	cfg := agentTestConfig()
	cfg.Storage.ArtifactDir = filepath.Join(t.TempDir(), "artifacts")
	st := store.NewMemoryStore()
	session := st.CreateSession("observation management")
	hub := toolhub.New(cfg, st)
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	return runtime, st, session, func() { _ = hub.Close() }
}

func archivedEvidenceFixture(t *testing.T, runtime Runtime, st *store.MemoryStore, sessionID, tool string, output any) (app.AgentRun, app.ToolCall) {
	t.Helper()
	now := time.Now().UTC()
	run := app.AgentRun{ID: app.NewID("run"), SessionID: sessionID, StartedAt: now}
	call := app.ToolCall{
		ID: app.NewID("tc"), SessionID: sessionID, RunID: run.ID, Tool: tool,
		Status: "completed", Result: output, StartedAt: now, CompletedAt: &now,
	}
	call.ObservationRef = store.ArchiveToolObservation(context.Background(), st, runtime.artifacts, call, output)
	if call.ObservationRef == "" {
		t.Fatal("fixture evidence was not archived")
	}
	st.SaveToolCall(call)
	return run, call
}
