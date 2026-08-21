package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
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
	if strings.Contains(provisioned.Text, "report.txt") || !strings.Contains(provisioned.Text, "first paragraph") {
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
		"url": "https://example.com/private", "digest": "runtime-only-digest",
		"controls": []any{
			map[string]any{"ref": "snapshot_1:e1", "role": "button", "accessible_name": "Open"},
			map[string]any{"ref": "snapshot_1:e2", "role": "button", "accessible_name": strings.Repeat("large", 300)},
		},
	}}
	text := slicePersistedToolEvidenceForRequest("browser.snapshot", output, workflowEvidenceStructured, 480, "")
	if !json.Valid([]byte(text)) || !strings.Contains(text, "snapshot_1:e1") ||
		strings.Contains(text, "snapshot_1:e2") || strings.Contains(text, "example.com") || strings.Contains(text, "runtime-only-digest") {
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

func TestDynamicToolStateAndArchiveOutputsStaySeparatedAcrossExecutionPaths(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	register := func(name string, risk app.RiskLevel, fail bool) {
		t.Helper()
		if err := runtime.tools.ReplaceDynamicTools("test."+name, []toolhub.DynamicToolRegistration{{
			Definition: app.ToolDefinition{
				Name: name, Description: "dual output", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true},
				Risk: risk, RequiresApproval: risk != app.RiskRead, TimeoutMS: 5000, Sandbox: "remote", Audit: "always",
				Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityExternalMCPWorkspace, Qualifiers: map[string]string{
					app.CapabilityQualifierProvider: app.CapabilityProviderLocalMind,
				}}},
			},
			RemoteName: strings.TrimPrefix(name, "localmind."),
			Execute: func(context.Context, map[string]any, string, string) (toolhub.Result, error) {
				result := toolhub.Result{
					Output:        map[string]any{"projection": "state-only"},
					ArchiveOutput: map[string]any{"projection": "archive-only"},
				}
				if fail {
					return result, errors.New("remote fixture failed")
				}
				return result, nil
			},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	assertSeparated := func(call app.ToolCall) {
		t.Helper()
		if output, _ := call.Result.(map[string]any); output["projection"] != "state-only" {
			t.Fatalf("state projection mismatch: %#v", call.Result)
		}
		if call.ObservationRef == "" {
			t.Fatal("archive projection was not persisted")
		}
		var object app.ArtifactObject
		for _, candidate := range st.ListArtifactObjects(0) {
			if candidate.URI == call.ObservationRef {
				object = candidate
				break
			}
		}
		raw, err := runtime.artifacts.Get(context.Background(), object.Key)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"projection":"archive-only"`) || strings.Contains(string(raw), `"projection":"state-only"`) {
			t.Fatalf("artifact contains the wrong projection: %s", raw)
		}
	}

	register("localmind.normal_read", app.RiskRead, false)
	now := time.Now().UTC()
	run := app.AgentRun{ID: "run_dual_normal", SessionID: session.ID, StartedAt: now}
	st.SaveRun(run)
	call, approval, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{Name: "localmind.normal_read", Args: map[string]any{}})
	if approval != nil || call.Status != "completed" {
		t.Fatalf("normal dynamic call did not complete: %#v %#v", call, approval)
	}
	assertSeparated(call)

	register("localmind.manual_read", app.RiskRead, false)
	manual, err := runtime.InvokeToolManually(context.Background(), "localmind.manual_read", map[string]any{}, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSeparated(manual.Call)

	register("localmind.approved_write", app.RiskReversible, false)
	pending, approval, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{Name: "localmind.approved_write", Args: map[string]any{"title": "safe"}})
	if approval == nil || pending.Status != "approval_pending" {
		t.Fatalf("mutation did not enter approval: %#v %#v", pending, approval)
	}
	resolved, err := st.ResolveApproval(approval.ID, "approved", "approved dynamic write")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "completed_after_approval" {
		t.Fatalf("approved dynamic call did not complete: %#v %v", executed, err)
	}
	assertSeparated(executed)

	register("localmind.normal_error", app.RiskRead, true)
	failed, approval, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{Name: "localmind.normal_error", Args: map[string]any{}})
	if approval != nil || failed.Status != "failed" {
		t.Fatalf("normal error output was not retained: %#v %#v", failed, approval)
	}
	assertSeparated(failed)

	register("localmind.manual_error", app.RiskRead, true)
	manualFailed, err := runtime.InvokeToolManually(context.Background(), "localmind.manual_error", map[string]any{}, session.ID)
	if err == nil || manualFailed.Call.Status != "failed" {
		t.Fatalf("manual error output was not retained: %#v %v", manualFailed, err)
	}
	assertSeparated(manualFailed.Call)

	register("localmind.approved_error", app.RiskReversible, true)
	pending, approval, _ = runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{Name: "localmind.approved_error", Args: map[string]any{"title": "safe"}})
	if approval == nil {
		t.Fatal("error fixture mutation did not enter approval")
	}
	resolved, err = st.ResolveApproval(approval.ID, "approved", "approved failing dynamic write")
	if err != nil {
		t.Fatal(err)
	}
	approvedFailed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || approvedFailed.Status != "failed_after_approval" {
		t.Fatalf("approved error output was not retained: %#v %v", approvedFailed, err)
	}
	assertSeparated(approvedFailed)
}

func TestLocalMindApprovalRejectsUnsafeArgumentsBeforePersistence(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	name := "localmind.upload_secret"
	if err := runtime.tools.ReplaceDynamicTools("test.unsafe", []toolhub.DynamicToolRegistration{{
		Definition: app.ToolDefinition{
			Name: name, Description: "unsafe approval fixture",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"base64": map[string]any{"type": "string"}}, "required": []string{"base64"}, "additionalProperties": false},
			Risk:        app.RiskReversible, RequiresApproval: true, TimeoutMS: 5000, Sandbox: "remote", Audit: "always",
			Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityExternalMCPWorkspace, Qualifiers: map[string]string{
				app.CapabilityQualifierProvider: app.CapabilityProviderLocalMind,
			}}},
		},
		RemoteName: "upload_secret",
		Execute: func(context.Context, map[string]any, string, string) (toolhub.Result, error) {
			return toolhub.Result{}, nil
		},
	}}); err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: "run_unsafe_approval", SessionID: session.ID, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	rawBase64 := strings.Repeat("QUJD", 2048)
	call, approval, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{Name: name, Args: map[string]any{"base64": rawBase64}})
	if approval != nil || call.Status != "blocked" || call.ErrorCode != string(app.ToolErrorMCPPersistenceUnsafe) {
		t.Fatalf("unsafe approval arguments did not fail closed: %#v %#v", call, approval)
	}
	persisted, ok := st.GetToolCall(call.ID)
	if !ok {
		t.Fatal("blocked call was not persisted")
	}
	raw, _ := json.Marshal(persisted)
	if strings.Contains(string(raw), rawBase64) || persisted.Arguments["persistence_rejected"] != true {
		t.Fatalf("unsafe arguments entered persistence: %s", raw)
	}
}

func TestGenericMCPManualApprovalRejectsUnsafeArgumentsBeforePersistence(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	name := "mcp.happy.create_task"
	if err := runtime.tools.ReplaceDynamicTools("test.generic-unsafe", []toolhub.DynamicToolRegistration{{
		Definition: app.ToolDefinition{
			Name: name, Description: "unsafe generic MCP approval fixture",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}, "required": []string{"url"}, "additionalProperties": false},
			Risk:        app.RiskReversible, RequiresApproval: true, TimeoutMS: 5000, Sandbox: "forbidden", Audit: "always",
			Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityMCPExternal}},
		},
		RemoteName: "create_task",
		Execute: func(context.Context, map[string]any, string, string) (toolhub.Result, error) {
			return toolhub.Result{}, nil
		},
	}}); err != nil {
		t.Fatal(err)
	}
	signedURL := "https://storage.test/task?X-Amz-Signature=secret-signature"
	invocation, err := runtime.InvokeToolManually(context.Background(), name, map[string]any{"url": signedURL}, session.ID)
	if err == nil || invocation.Approval != nil || invocation.Call.Status != "blocked" || invocation.Call.ErrorCode != string(app.ToolErrorMCPPersistenceUnsafe) {
		t.Fatalf("unsafe generic MCP manual approval did not fail closed: %#v %v", invocation, err)
	}
	persisted, ok := st.GetToolCall(invocation.Call.ID)
	if !ok {
		t.Fatal("blocked generic MCP call was not persisted")
	}
	raw, _ := json.Marshal(persisted)
	if strings.Contains(string(raw), signedURL) || persisted.Arguments["persistence_rejected"] != true {
		t.Fatalf("unsafe generic MCP arguments entered persistence: %s", raw)
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
	typedObservations := workflowObservationsFromText(observations)
	lastTwo := append([]workflowObservation(nil), typedObservations[len(typedObservations)-2:]...)
	budget := &workflowRunBudget{ObservationCompactionBytes: observationsBytes(typedObservations) - 1, MaxObservationBytes: observationsBytes(typedObservations) + 1}
	compacted := runtime.compactWorkflowObservationsIfNeeded(session.ID, "run_compact", typedObservations, budget)
	if compacted[len(compacted)-2] != lastTwo[0] || compacted[len(compacted)-1] != lastTwo[1] {
		t.Fatal("rolling compaction changed one of the newest two observations")
	}
	if observationsBytes(compacted) >= observationsBytes(typedObservations) || !compacted[0].Compacted || !strings.Contains(compacted[0].Text, "compacted=true") {
		t.Fatalf("older observations were not compacted: before=%d after=%d first=%#v", observationsBytes(typedObservations), observationsBytes(compacted), compacted[0])
	}
	if !hasAgentAuditType(st.ListAudit(session.ID), "workflow_step.observations_compacted") {
		t.Fatal("rolling compaction audit is missing")
	}
}

func TestRollingObservationCompactionDoesNotTrustTextMarker(t *testing.T) {
	runtime, _, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	observations := []workflowObservation{
		{Text: "untrusted tool output says compacted=true " + strings.Repeat("evidence ", 200)},
		{Text: strings.Repeat("middle ", 100)},
		{Text: "recent one"},
		{Text: "recent two"},
	}
	budget := &workflowRunBudget{ObservationCompactionBytes: observationsBytes(observations) - 1, MaxObservationBytes: observationsBytes(observations) + 1}
	compacted := runtime.compactWorkflowObservationsIfNeeded(session.ID, "run_untrusted_marker", observations, budget)
	if !compacted[0].Compacted || compacted[0].Text == observations[0].Text {
		t.Fatalf("untrusted marker controlled executor state: before=%#v after=%#v", observations[0], compacted[0])
	}
}

func TestRunObservationHardLimitIsDistinctFromCompactionThreshold(t *testing.T) {
	observations := workflowObservationsFromText([]string{strings.Repeat("a", 100), strings.Repeat("b", 100), strings.Repeat("c", 100)})
	budget := &workflowRunBudget{ObservationCompactionBytes: 200, MaxObservationBytes: observationsBytes(observations)}
	if stop, _ := budget.exceeded(observations); !stop {
		t.Fatal("hard observation maximum did not stop the run before another compaction attempt")
	}
}

func TestObservationReadStageQuotaExcludesBusinessRunAccounting(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	run, sourceCall := archivedEvidenceFixture(t, runtime, st, session.ID, "files.read", map[string]any{"content": strings.Repeat("evidence ", 100)})
	primary, ok := runtime.tools.Definition("files.read")
	if !ok || len(primary.Capabilities) == 0 {
		t.Fatal("files.read definition is unavailable")
	}
	support, ok := runtime.tools.Definition("observation.read")
	if !ok || len(support.Capabilities) != 1 {
		t.Fatal("observation.read definition is unavailable")
	}
	nodeID := app.WorkflowNodeID("model_stage")
	scope := app.CapabilityScope{
		Requirements:        []app.CapabilityRequirement{{Name: primary.Capabilities[0].Name, Qualifiers: primary.Capabilities[0].Qualifiers}},
		SupportRequirements: []app.CapabilityRequirement{{Name: support.Capabilities[0].Name}},
	}
	plan := app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: app.WorkflowDocumentEdit, ProfileRevision: 7,
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "model_stage", Goal: app.NodeGoal{ObjectiveIDs: []string{"objective"}, Summary: "test support reads", Completion: app.CompletionEvidence},
			InitialScope: scope, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}
	run.Workflow = &app.WorkflowState{
		SchemaVersion: 1, Plan: plan, PlanDigest: workflowPlanDigest(plan), Status: app.WorkflowStatusRunning,
		ActiveNodeIDs: []app.WorkflowNodeID{nodeID},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{nodeID: {
			Status: app.WorkflowNodeActive, Stage: "model_stage", CurrentScope: scope, ScopeRevision: 1,
			SelectedEntries: []app.ToolDirectoryEntryID{
				directoryEntryID(primary, primary.Capabilities[0]), directoryEntryID(support, support.Capabilities[0]),
			},
		}},
	}
	st.SaveRun(run)
	content := fmt.Sprintf("read more evidence\nMOCK_STEP_RESPONSE:{\"type\":\"action\",\"tool\":\"observation.read\",\"arguments\":{\"artifact_uri\":%q,\"max_bytes\":32}}", sourceCall.ObservationRef)
	runBudget := runtime.newWorkflowRunBudget(nil)
	result := runtime.runWorkflowStepLoop(context.Background(), session.ID, run, content, workflowStageContext{
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: nodeID, ScopeRevision: 1, Capability: primary.Capabilities[0].Name,
		RequiresToolEvidence: true, ModelLaneHint: workflowExecutionModelLane,
	}, []app.ToolDefinition{primary, support}, nil, nil, runBudget)
	if len(result.ToolCalls) != 2 || result.FailureCode != workflowFailureObservationReadLimit {
		t.Fatalf("stage quota did not allow exactly two reads before blocking a repeated violation: calls=%d failure=%q", len(result.ToolCalls), result.FailureCode)
	}
	for _, call := range result.ToolCalls {
		if call.Capability != app.ToolCapabilityObservationRead || call.Status != "completed" {
			t.Fatalf("unexpected support call outcome: %#v", call)
		}
	}
	if runBudget.ToolCalls != 0 || runBudget.RepeatedRun.Count != 0 {
		t.Fatalf("support reads consumed business run accounting: %#v", runBudget)
	}
	if !hasAgentAuditType(st.ListAudit(session.ID), "workflow_step.observation_read_limited") || !hasAgentAuditType(st.ListAudit(session.ID), "workflow_step.support_assessed") {
		t.Fatalf("support read quota/assessment audit is missing: %#v", st.ListAudit(session.ID))
	}
	if definitions := workflowDefinitionsWithoutSupport(run, nodeID, []app.ToolDefinition{primary, support}); !exactVisibleToolNames(definitions, primary.Name) {
		t.Fatalf("support tool remained visible after quota exhaustion: %#v", visibleToolNames(definitions))
	}
}

func TestFailedObservationReadCountsAfterExecutionAndStageBudgetResets(t *testing.T) {
	call := app.ToolCall{Capability: app.ToolCapabilityObservationRead, Status: "failed"}
	if !workflowSupportCallExecuted(call, true) || workflowSupportCallExecuted(call, false) {
		t.Fatal("failed support execution accounting does not distinguish pre-execution rejection")
	}
	runtime, _, _, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	first := runtime.newWorkflowStageBudget()
	first.ObservationReads = first.MaxObservationReads
	second := runtime.newWorkflowStageBudget()
	if second.ObservationReads != 0 || second.MaxObservationReads != first.MaxObservationReads {
		t.Fatalf("observation read quota did not reset for a new stage: first=%#v second=%#v", first, second)
	}
}

func TestContextBuilderDegradesLowerPrioritySectionFirst(t *testing.T) {
	builder := contextBuilder{Sections: []contextSection{
		degradingContextSection("low", 10, strings.Repeat("low ", 1000), "low compact", true),
		fixedContextSection("required", 100, contextChannelUser, "required contract"),
	}}
	rendered := builder.Render(30)
	if !strings.Contains(rendered, "required contract") || strings.Contains(rendered, strings.Repeat("low ", 20)) {
		t.Fatalf("context builder degraded the wrong section: %s", rendered)
	}
}

func TestContextBuilderHardTruncatesUTF8AndPreservesFixedTail(t *testing.T) {
	tail := "fixed output contract"
	builder := contextBuilder{Sections: []contextSection{
		truncatableContextSection("owner_goal", 10, contextChannelUser, strings.Repeat("目标内容", 400), contextTruncateHeadTail),
		fixedContextSection("output_contract", 1000, contextChannelUser, tail),
	}}
	admission, err := builder.Admit(120)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(admission.User) || estimatePromptTokens(admission.System, admission.User) > 120 {
		t.Fatalf("hard-truncated prompt is invalid or oversized: tokens=%d", estimatePromptTokens(admission.System, admission.User))
	}
	if !strings.Contains(admission.User, "[prompt_truncated=true kind=owner_goal omitted_bytes=") || !strings.HasSuffix(admission.User, tail) {
		t.Fatalf("hard truncation marker or fixed tail is missing: %s", admission.User)
	}
}

func TestContextBuilderRejectsOversizedFixedSections(t *testing.T) {
	builder := contextBuilder{Sections: []contextSection{
		fixedContextSection("base", 1000, contextChannelSystem, strings.Repeat("fixed ", 200)),
		fixedContextSection("tail", 1000, contextChannelUser, "output contract"),
	}}
	if _, err := builder.Admit(20); !errors.Is(err, errPromptFixedSectionsOversized) {
		t.Fatalf("oversized fixed prompt was admitted: %v", err)
	}
}

func TestContextSnapshotCompactVariantsAreMeaningfullySmaller(t *testing.T) {
	snapshot := agentContextSnapshot{}
	for index := 0; index < 8; index++ {
		snapshot.Messages = append(snapshot.Messages, app.Message{Role: "user", Content: strings.Repeat("conversation ", 40)})
	}
	for index := 0; index < 4; index++ {
		snapshot.Episodes = append(snapshot.Episodes, app.EpisodeSummary{Goal: strings.Repeat("goal ", 40), Outcome: "completed", Summary: strings.Repeat("summary ", 50), Failures: []string{"none"}})
		snapshot.Memories = append(snapshot.Memories, app.Memory{Kind: "preference", Content: strings.Repeat("memory ", 50)})
	}
	for index := 0; index < 6; index++ {
		snapshot.ToolResults = append(snapshot.ToolResults, app.ToolCall{ID: app.NewID("tc"), Tool: "files.read", Status: "completed", ObservationSummary: strings.Repeat("tool evidence ", 100)})
	}
	for index := 0; index < 3; index++ {
		snapshot.RecentImages = append(snapshot.RecentImages, app.MessageAttachment{RelPath: "media/image.png", Name: "image.png", ContentType: "image/png", Caption: strings.Repeat("caption ", 30), Summary: strings.Repeat("summary ", 40)})
	}
	for _, section := range snapshot.contextBuilder(contextRenderIntent).Sections {
		for variantIndex := 1; variantIndex < len(section.Variants); variantIndex++ {
			variant := section.Variants[variantIndex]
			if variant.Name != "compact" {
				continue
			}
			full := section.Variants[variantIndex-1]
			if variant.Text == full.Text || len([]byte(variant.Text)) >= len([]byte(full.Text)) {
				t.Fatalf("%s compact variant is not materially smaller: full=%d compact=%d", section.Kind, len([]byte(full.Text)), len([]byte(variant.Text)))
			}
		}
	}
}

func TestWorkflowFinalEvidencePacksMultipleObservations(t *testing.T) {
	evidence := workflowFinalEvidence(nil, []string{"first observation", "second observation"})
	if len(evidence) != 2 || !strings.Contains(evidence[0], "claim_coverage=bounded") ||
		!strings.HasSuffix(evidence[0], "first observation") || evidence[1] != "second observation" {
		t.Fatalf("finalizer did not pack multiple observations: %#v", evidence)
	}
}

func TestWorkflowFinalizerRecordsActualEvidenceProjection(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	fixture := pdfCoverageToolCall("complete", "complete PDF evidence", true)
	run, call := archivedEvidenceFixture(t, runtime, st, session.ID, fixture.Tool, fixture.Result)
	call.Capability = app.ToolCapabilityDocumentRead
	st.SaveToolCall(call)
	run.Workflow = &app.WorkflowState{Plan: app.WorkflowPlan{ProfileID: app.WorkflowDocumentRead}}
	st.SaveRun(run)

	projection, err := runtime.workflowFinalEvidence(context.Background(), run, []app.ToolCall{call}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runtime.synthesizeWorkflowFinalAnswer(
		context.Background(), run, "Summarize the PDF", []app.ToolCall{call}, nil, documentWorkflowModelLane, nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	var event *app.AuditEvent
	for _, candidate := range st.ListAudit(session.ID) {
		if candidate.RunID == run.ID && candidate.Type == "workflow.evidence_projection.created" &&
			candidate.Fields["semantic_variable"] == "final_answer_content" {
			current := candidate
			event = &current
			break
		}
	}
	if event == nil {
		t.Fatalf("finalizer evidence projection audit is missing: %#v", st.ListAudit(session.ID))
	}
	if event.Fields["claim_coverage"] != workflowCoverageComplete || event.Fields["complete_for_consumer"] != true ||
		intLikeValue(event.Fields["model_payload_bytes"]) != len([]byte(projection.modelPayload())) ||
		intLikeValue(event.Fields["archived_bytes"]) <= 0 ||
		!hasAgentAuditStringSliceField([]app.AuditEvent{*event}, event.Type, "source_event_ids", call.ObservationRef) {
		t.Fatalf("finalizer projection audit is incomplete: %#v", event.Fields)
	}
}

func newObservationManagementRuntime(t *testing.T) (Runtime, *store.MemoryStore, app.Session, func()) {
	t.Helper()
	cfg := agentTestConfig()
	cfg.Storage.ArtifactDir = filepath.Join(t.TempDir(), "artifacts")
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "observation management")
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
