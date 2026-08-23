package agent

import (
	"os"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestExternalMCPWorkspaceToolQueuesApprovalBeforeFilesystemAccess(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	if err := os.RemoveAll(session.WorkspaceRoot); err != nil {
		t.Fatal(err)
	}
	run := externalMCPPolicyTestRun(session, "run_external_workspace")
	testSaveRun(st, run)

	call, approval, _, _ := runtime.runToolPlan(t.Context(), session.ID, run.ID, toolPlan{
		Name: "files.search", Args: map[string]any{"query": "quarterly report"},
	})
	if approval == nil || call.Status != "approval_pending" || call.PolicyContext == nil || approval.PolicyContext == nil ||
		call.PolicyContext.ResourceClass != app.PolicyResourceSparkClawWorkspaceData || call.Result != nil {
		t.Fatalf("external MCP workspace search touched the missing root instead of pausing: call=%#v approval=%#v", call, approval)
	}
}

func TestLocalWorkflowWorkspaceToolKeepsRegisteredPolicy(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	if err := os.RemoveAll(session.WorkspaceRoot); err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{
		ID: "run_local_workspace", SessionID: session.ID, State: "executing", StartedAt: time.Now().UTC(),
		MessageContext: &app.MessageRunContext{OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID}},
	}
	testSaveRun(st, run)

	call, approval, _, _ := runtime.runToolPlan(t.Context(), session.ID, run.ID, toolPlan{
		Name: "files.search", Args: map[string]any{"query": "quarterly report"},
	})
	if approval != nil || call.Status != "completed" || call.PolicyContext != nil {
		t.Fatalf("local model execution was treated as an external AI principal: call=%#v approval=%#v", call, approval)
	}
}

func TestExternalMCPWeatherToolDoesNotGainWorkspaceApproval(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	run := externalMCPPolicyTestRun(session, "run_external_weather")
	testSaveRun(st, run)
	definition, ok := runtime.tools.Definition("weather.lookup")
	if !ok {
		t.Fatal("weather.lookup is unavailable in policy test runtime")
	}
	execution, err := runtime.toolPolicyExecutionContext(t.Context(), run.ID, definition, map[string]any{"location": "Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	decision := runtime.policy.DecideWithContext(definition, map[string]any{"location": "Shanghai"}, execution)
	if execution.PrincipalClass != app.PolicyPrincipalExternalMCPAI || execution.ResourceClass != "" || decision.RequiresApproval {
		t.Fatalf("external MCP weather lookup gained workspace approval: context=%#v decision=%#v", execution, decision)
	}
}

func TestExternalMCPWorkspaceDerivativeReadUsesSameEscalation(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	run := externalMCPPolicyTestRun(session, "run_external_derivative")
	testSaveRun(st, run)
	testSaveToolCall(st, app.ToolCall{
		ID: "tc_workspace_source", SessionID: session.ID, RunID: run.ID, Tool: "files.read", Status: "completed",
		ObservationRef: "artifact://sparkclaw/observations/source.json", StartedAt: time.Now().UTC(),
	})

	call, approval, _, _ := runtime.runToolPlan(t.Context(), session.ID, run.ID, toolPlan{
		Name: "observation.read", Args: map[string]any{"artifact_uri": "artifact://sparkclaw/observations/source.json", "max_bytes": 100},
	})
	if approval == nil || call.Status != "approval_pending" || call.PolicyContext == nil ||
		call.PolicyContext.ResourceClass != app.PolicyResourceSparkClawWorkspaceData {
		t.Fatalf("cached workspace derivative bypassed contextual approval: call=%#v approval=%#v", call, approval)
	}
}

func TestExternalMCPWorkspaceDerivativePolicyUsesCapabilityNotToolName(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	run := externalMCPPolicyTestRun(session, "run_external_derivative_capability")
	testSaveRun(st, run)
	testSaveToolCall(st, app.ToolCall{
		ID: "tc_workspace_capability_source", SessionID: session.ID, RunID: run.ID, Tool: "files.read", Status: "completed",
		ObservationRef: "artifact://sparkclaw/observations/capability-source.json", StartedAt: time.Now().UTC(),
	})

	definition, ok := runtime.tools.Definition("observation.read")
	if !ok {
		t.Fatal("observation.read is unavailable in policy test runtime")
	}
	definition.Name = "support.evidence.read"
	execution, err := runtime.toolPolicyExecutionContext(t.Context(), run.ID, definition, map[string]any{
		"artifact_uri": "artifact://sparkclaw/observations/capability-source.json", "max_bytes": 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if execution.ResourceClass != app.PolicyResourceSparkClawWorkspaceData ||
		execution.AccessClass != app.PolicyAccessWorkspaceSourceRead || execution.ContractDigest == "" {
		t.Fatalf("renamed observation capability bypassed workspace policy: %#v", execution)
	}
}

func TestExternalMCPContextSnapshotDoesNotReadPriorSessionDerivatives(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	storetest.MustAddMessage(t, st, app.Message{
		SessionID: session.ID,
		RunID:     "run_prior_document",
		Role:      "assistant",
		Content:   "private workspace-derived summary",
	})
	testSaveToolCall(st, app.ToolCall{
		ID:                 "tc_prior_document",
		SessionID:          session.ID,
		RunID:              "run_prior_document",
		Tool:               "files.read",
		Status:             "completed",
		ObservationSummary: "private workspace-derived tool evidence",
		StartedAt:          time.Now().UTC(),
	})

	run := externalMCPPolicyTestRun(session, "run_external_context")
	testSaveRun(st, run)

	snapshot, err := runtime.buildAgentContextSnapshot(t.Context(), session.ID, run.ID, "what did the file say?")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := snapshot.ForIntentRouting(intentRoutingContextTokenBudget); got != "" {
		t.Fatalf("external MCP routing inherited prior session data: %q", got)
	}
	if got, _ := snapshot.ForWorkflowStep(intentRoutingContextTokenBudget); got != "" {
		t.Fatalf("external MCP workflow inherited prior session data: %q", got)
	}
}

func TestContextBoundApprovalRejectsChangedMCPIdentity(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	run := externalMCPPolicyTestRun(session, "run_changed_mcp_identity")
	testSaveRun(st, run)
	call, pending, _, _ := runtime.runToolPlan(t.Context(), session.ID, run.ID, toolPlan{
		Name: "files.search", Args: map[string]any{"query": "quarterly report"},
	})
	if pending == nil {
		t.Fatal("external MCP workspace search did not queue approval")
	}
	approved, err := st.ResolveApproval(t.Context(), pending.ID, "approved", "owner approved")
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := testGetRun(st, run.ID)
	changed.MessageContext.MCP.RequesterDeviceID = "different-device"
	testSaveRun(st, changed)
	executed, err := runtime.ExecuteApprovedToolCall(t.Context(), approved)
	if err != nil || executed.ID != call.ID || executed.Status != "failed_after_approval" || executed.ErrorCode != string(app.ToolErrorPolicyBlocked) {
		t.Fatalf("changed MCP identity reused contextual approval: call=%#v err=%v", executed, err)
	}
}

func TestContextBoundApprovalRejectsChangedAuthorizationPrincipal(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	run := externalMCPPolicyTestRun(session, "run_changed_authorization")
	testSaveRun(st, run)
	call, pending, _, _ := runtime.runToolPlan(t.Context(), session.ID, run.ID, toolPlan{
		Name: "files.search", Args: map[string]any{"query": "quarterly report"},
	})
	if pending == nil {
		t.Fatal("external MCP workspace search did not queue approval")
	}
	approved, err := st.ResolveApproval(t.Context(), pending.ID, "approved", "owner approved")
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := testGetRun(st, run.ID)
	changed.MessageContext.Authorization.PrincipalID = "different-principal"
	testSaveRun(st, changed)
	executed, err := runtime.ExecuteApprovedToolCall(t.Context(), approved)
	if err != nil || executed.ID != call.ID || executed.Status != "failed_after_approval" || executed.ErrorCode != string(app.ToolErrorPolicyBlocked) {
		t.Fatalf("changed authorization principal reused contextual approval: call=%#v err=%v", executed, err)
	}
}

func TestApprovedToolExecutionUsesPersistedResolvedApproval(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	run := externalMCPPolicyTestRun(session, "run_persisted_approval_authority")
	testSaveRun(st, run)
	call, pending, _, _ := runtime.runToolPlan(t.Context(), session.ID, run.ID, toolPlan{
		Name: "files.search", Args: map[string]any{"query": "quarterly report"},
	})
	if pending == nil || call.Status != "approval_pending" {
		t.Fatalf("workspace search did not queue approval: call=%#v approval=%#v", call, pending)
	}
	if _, err := runtime.ExecuteApprovedToolCall(t.Context(), *pending); err == nil {
		t.Fatal("pending approval was accepted as executable authority")
	}
	forged := *pending
	forged.ID = "approval-forged"
	forged.Status = "approved"
	if _, err := runtime.ExecuteApprovedToolCall(t.Context(), forged); err == nil {
		t.Fatal("non-persisted approval identity was accepted as executable authority")
	}
	if current, ok := testGetToolCall(st, call.ID); !ok || current.Status != "approval_pending" {
		t.Fatalf("invalid approval attempt changed the tool call: %#v ok=%v", current, ok)
	}
}

func TestLegacyExternalSendApprovalCannotResumeDelivery(t *testing.T) {
	runtime, st, session, closeRuntime := newToolPolicyTestRuntime(t)
	defer closeRuntime()
	run := app.AgentRun{
		ID: "run_legacy_external_send", SessionID: session.ID, State: "approval_pending", StartedAt: time.Now().UTC(),
		MessageContext: &app.MessageRunContext{
			OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
			ReturnRoute: app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "legacy-target"},
		},
	}
	testSaveRun(st, run)
	call := app.ToolCall{
		ID: "tc_legacy_external_send", SessionID: session.ID, RunID: run.ID, Tool: "notify.ask_approval",
		Status: "approval_pending", Arguments: map[string]any{"message_control_action": legacyExternalSendApprovalAction}, StartedAt: time.Now().UTC(),
	}
	approval := app.Approval{
		ID: "ap_legacy_external_send", SessionID: session.ID, RunID: run.ID, ToolCallID: call.ID, Tool: call.Tool,
		Status: "pending", Arguments: call.Arguments, CreatedAt: time.Now().UTC(),
	}
	call.ApprovalID = approval.ID
	testSaveToolCall(st, call)
	storetest.MustSaveApproval(t, st, approval)
	approved, err := st.ResolveApproval(t.Context(), approval.ID, "approved", "old approval")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(t.Context(), approved)
	if err != nil || executed.Status != "failed_after_approval" {
		t.Fatalf("legacy external-send approval executed: call=%#v err=%v", executed, err)
	}
	result, resumed, err := runtime.ResumeRunAfterApproval(t.Context(), session.ID, run.ID)
	if err != nil || !resumed || result.Run.State != "blocked" || result.WorkflowResult != nil && result.WorkflowResult.Status == app.WorkflowResultSucceeded {
		t.Fatalf("legacy external-send approval resumed delivery: resumed=%v result=%#v err=%v", resumed, result, err)
	}
}

func newToolPolicyTestRuntime(t *testing.T) (Runtime, *store.MemoryStore, app.Session, func()) {
	t.Helper()
	cfg := agentTestConfig()
	root := t.TempDir()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseID = "lic_test"
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey = "ilk_v1.lic_test.test-key"
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "policy test", app.DefaultOwnerID, root, "web", true)
	hub := toolhub.New(cfg, st)
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	return runtime, st, session, func() { _ = hub.Close() }
}

func externalMCPPolicyTestRun(session app.Session, id string) app.AgentRun {
	endpoint := app.EndpointID("mcp:binding-policy")
	return app.AgentRun{
		ID: id, SessionID: session.ID, State: "executing", StartedAt: time.Now().UTC(),
		MessageContext: &app.MessageRunContext{
			OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
			MCP: &app.MCPInvocationRef{
				InvocationID: "inv-" + id, OperationID: "op-" + id, BindingRef: "binding-policy",
				BindingRevision: 1, RequesterDeviceID: "device-policy",
			},
			ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: endpoint},
		},
	}
}
