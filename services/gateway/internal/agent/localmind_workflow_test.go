package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestLocalMindProfilesUseBoundDirectTaskLifecycles(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	for _, test := range []struct {
		id       app.WorkflowID
		nodes    int
		initial  string
		approval app.RiskLevel
	}{
		{id: app.WorkflowLocalMindRead, nodes: 2, initial: "delegate_to_localmind", approval: app.RiskRead},
		{id: app.WorkflowLocalMindWrite, nodes: 2, initial: "delegate_to_localmind", approval: app.RiskDangerous},
		{id: app.WorkflowLocalMindQuery, nodes: 1, initial: "query_localmind_task", approval: app.RiskRead},
		{id: app.WorkflowLocalMindCancel, nodes: 1, initial: "cancel_localmind_task", approval: app.RiskDangerous},
	} {
		profile, err := registry.Get(test.id)
		if err != nil {
			t.Fatal(err)
		}
		route := localMindMatchedRoute(t, test.id, "让 LocalMind 处理当前内容", "task-1")
		_, plan, err := profile.Resolve(route, "turn-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Nodes) != test.nodes || string(plan.InitialNodeIDs[0]) != test.initial || plan.Nodes[0].AllowedRisks[0] != test.approval || !workflowProfileAlwaysDirect(profile) {
			t.Fatalf("unexpected %s plan: %#v", test.id, plan)
		}
		if test.nodes == 2 {
			poll := plan.Nodes[1]
			if poll.ID != "query_current_task" || len(poll.DependsOn) != 1 || poll.DependsOn[0] != "delegate_to_localmind" || poll.MaxAttempts != localMindPollAttempts || len(poll.Transitions) != 1 {
				t.Fatalf("delegation profile omitted its bounded query node: %#v", poll)
			}
		}
	}
}

func TestLocalMindRouteRequiresExplicitNameAndRejectsMedia(t *testing.T) {
	runtime := Runtime{capabilities: capability.MustDefaultCatalog()}
	decision := localMindClearDecision(t, app.CapabilityLocalMindRead)

	implicit, err := runtime.routeFromFusionDecision(t.Context(), "请调研并总结这个问题", intentGroundingProjection{}, decision, "")
	if err != nil {
		t.Fatal(err)
	}
	if implicit.Status != app.RouteUnmatched || implicit.Reason != "explicit_localmind_assignment_required" {
		t.Fatalf("implicit delegation selected LocalMind: %#v", implicit)
	}

	media, err := runtime.routeFromFusionDecision(t.Context(), "请 LocalMind 总结这个附件", intentGroundingProjection{HasUnsupportedMedia: true}, decision, "")
	if err != nil {
		t.Fatal(err)
	}
	if media.Status != app.RouteBlocked || media.Reason != "localmind_text_only_input_required" {
		t.Fatalf("multimedia LocalMind request was not blocked: %#v", media)
	}

	explicit, err := runtime.routeFromFusionDecision(t.Context(), "请 LocalMind 总结当前这段文字", intentGroundingProjection{}, decision, "")
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Status != app.RouteMatched || explicit.Slots.Query != "请 LocalMind 总结当前这段文字" {
		t.Fatalf("explicit text delegation did not match: %#v", explicit)
	}
}

func TestLocalMindSemanticRoutingDistinguishesFourLeaves(t *testing.T) {
	for _, test := range []struct {
		request string
		want    app.CapabilityID
		taskID  string
	}{
		{request: "请 LocalMind 阅读并总结当前这段文字", want: app.CapabilityLocalMindRead},
		{request: "请 LocalMind 创建一份项目报告", want: app.CapabilityLocalMindWrite},
		{request: "查询 LocalMind taskId: task-123 的状态", want: app.CapabilityLocalMindQuery, taskID: "task-123"},
		{request: "取消 LocalMind taskId: task-123", want: app.CapabilityLocalMindCancel, taskID: "task-123"},
	} {
		route := mustRouteIntent(t, Runtime{}, test.request)
		if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 || route.CapabilityPath[1] != test.want || route.Slots.TargetRef != test.taskID {
			t.Fatalf("LocalMind semantic route for %q = %#v", test.request, route)
		}
	}
	for _, request := range []string{"请总结当前这段文字", "LocalMind 是什么"} {
		route := mustRouteIntent(t, Runtime{}, request)
		if len(route.CapabilityPath) == 2 && isLocalMindCapability(route.CapabilityPath[1]) {
			t.Fatalf("non-assignment request %q selected LocalMind: %#v", request, route)
		}
	}
}

func TestLocalMindExternalAICandidatesAreIneligible(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	graph, err := registry.SemanticGraph(capability.MustDefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	all := graph.EligibleCandidates(app.MessageSourceThirdPartyDevice)
	for _, sourceKind := range []app.MessageSourceKind{app.MessageSourceWeb, app.MessageSourceThirdPartyDevice, app.MessageSourceTimer} {
		found := false
		for _, candidate := range graph.EligibleCandidates(sourceKind) {
			found = found || isLocalMindCapability(candidate.Capability)
		}
		if !found {
			t.Fatalf("human/message-runtime source %q cannot select LocalMind", sourceKind)
		}
	}
	eligible := withoutLocalMindCandidates(all)
	for _, candidate := range eligible {
		if isLocalMindCapability(candidate.Capability) {
			t.Fatalf("external-AI eligibility retained LocalMind candidate %#v", candidate)
		}
	}
	if len(eligible) == len(all) {
		t.Fatal("external-AI eligibility filter did not remove any LocalMind candidates")
	}
}

func TestLocalMindTaskGroundingResolvesExactAndSameSessionRecent(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "localmind grounding")
	completed := time.Now().UTC().Add(-time.Minute)
	testSaveToolCall(st, app.ToolCall{
		ID: "tc_delegate", SessionID: session.ID, RunID: "run_delegate", Tool: "localmind.task.delegate_read",
		Status: app.ToolCallStatusCompleted, CompletedAt: &completed,
		Result: localMindTaskOutput("task-recent-42", "running", false, "v2", ""),
	})
	runtime := Runtime{store: st}

	exact, err := runtime.resolveLocalMindTaskTargets(t.Context(), session.ID, "查询 LocalMind taskId: task-explicit-7 的状态", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact) != 1 || exact[0].Ref != "task-explicit-7" || exact[0].Facts["task_provenance"] != "current_turn" {
		t.Fatalf("explicit task ID was not grounded from current text: %#v", exact)
	}

	recent, err := runtime.resolveLocalMindTaskTargets(t.Context(), session.ID, "查询 LocalMind 刚才那个任务的状态", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Ref != "task-recent-42" || recent[0].Facts["task_provenance"] != "tc_delegate" {
		t.Fatalf("same-session recent task was not grounded: %#v", recent)
	}
	route := mustRouteIntentWithResources(t, Runtime{store: st}, session.ID, "查询 LocalMind 刚才那个任务的状态", nil, app.MessageSourceWeb)
	if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 || route.CapabilityPath[1] != app.CapabilityLocalMindQuery || route.Slots.TargetRef != "task-recent-42" {
		t.Fatalf("recent-task context did not complete the query route: %#v", route)
	}
}

func TestInboundMCPPrincipalCannotRouteToLocalMind(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_localmind_external", "run_localmind_external", app.MCPConversationRequest{
		Text: "请 LocalMind 阅读并总结当前这段文字",
		Invocation: app.MCPInvocationRef{
			InvocationID: "inv_localmind_external", OperationID: "op_localmind_external",
			BindingRef: "binding_localmind_external", BindingRevision: 1, RequesterDeviceID: "external-ai",
		},
	}, app.MessageIngressContext{
		Source:  app.MessageSourceContext{Kind: app.MessageSourceThirdPartyDevice, Adapter: "mcp", EndpointID: "mcp:binding_localmind_external"},
		OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "mcp:binding_localmind_external"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision != nil && len(result.RouteDecision.CapabilityPath) == 2 && isLocalMindCapability(result.RouteDecision.CapabilityPath[1]) {
		t.Fatalf("inbound MCP principal selected LocalMind: %#v", result.RouteDecision)
	}
	for _, call := range testListToolCalls(st, session.ID) {
		if isLocalMindTaskToolCall(call) {
			t.Fatalf("inbound MCP principal invoked LocalMind: %#v", call)
		}
	}
}

func TestLocalMindReadDelegationPollsToCompletionWithoutApproval(t *testing.T) {
	runtime, st, hub, session := newLocalMindWorkflowRuntime(t)
	delegateExecutions := 0
	statusExecutions := 0
	var delegateArgs map[string]any
	registerLocalMindAgentTool(t, hub, "localmind.task.delegate_read", app.ToolCapabilityLocalMindDelegateRead, app.RiskRead, false, func(_ context.Context, args map[string]any, _, _ string) (toolhub.Result, error) {
		delegateExecutions++
		delegateArgs = clonePlanArgs(args)
		return toolhub.Result{Output: localMindTaskOutput("task-read-1", "queued", false, "v1", "")}, nil
	})
	registerLocalMindAgentTool(t, hub, "localmind.task.get", app.ToolCapabilityLocalMindTaskStatus, app.RiskRead, false, func(_ context.Context, args map[string]any, _, _ string) (toolhub.Result, error) {
		statusExecutions++
		if args["task_id"] != "task-read-1" {
			t.Fatalf("poll was not bound to delegated task: %#v", args)
		}
		wantVersion := "v1"
		if statusExecutions == 2 {
			wantVersion = "v2"
		}
		if args["known_state_version"] != wantVersion {
			t.Fatalf("poll %d did not advance known state version to %s: %#v", statusExecutions, wantVersion, args)
		}
		if statusExecutions == 1 {
			return toolhub.Result{Output: localMindTaskOutput("task-read-1", "running", false, "v2", "")}, nil
		}
		return toolhub.Result{Output: localMindTaskOutput("task-read-1", "completed", true, "v3", "LocalMind answer")}, nil
	})

	goal := "请 LocalMind 总结当前这段文字"
	execution, run := executeLocalMindRoute(t, runtime, session, app.WorkflowLocalMindRead, goal, "")
	if run.Workflow.Status != app.WorkflowStatusSucceeded || delegateExecutions != 1 || statusExecutions != 2 || len(execution.Approvals) != 0 {
		t.Fatalf("read delegation did not complete through polling: workflow=%#v executions=%d/%d approvals=%#v", run.Workflow, delegateExecutions, statusExecutions, execution.Approvals)
	}
	if delegateArgs["request"] != goal || len(delegateArgs) != 1 {
		t.Fatalf("delegation did not transfer only current text: %#v", delegateArgs)
	}
	calls := toolCallsForRun(testListToolCalls(st, session.ID), run.ID)
	if summary, ok := groundedLocalMindTaskSummary(calls); !ok || !strings.Contains(summary, "LocalMind answer") || !strings.Contains(summary, "task-read-1") {
		t.Fatalf("completed LocalMind result was not grounded: %q ok=%v calls=%#v", summary, ok, calls)
	}
}

func TestLocalMindPlanFreezesEndpointSnapshotQualifiers(t *testing.T) {
	route := localMindMatchedRoute(t, app.WorkflowLocalMindRead, "请 LocalMind 总结", "")
	route.Facts = map[string]string{localMindEndpointFact: "lm_endpoint", localMindSnapshotFact: "snapshot_v1"}
	_, plan, err := (localMindReadProfile{}).Resolve(route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range plan.Nodes {
		requirement := node.InitialScope.Requirements[0]
		if requirement.Qualifiers[app.CapabilityQualifierEndpointID] != "lm_endpoint" ||
			requirement.Qualifiers[app.CapabilityQualifierSnapshotRevision] != "snapshot_v1" {
			t.Fatalf("LocalMind node %s did not freeze endpoint identity: %#v", node.ID, requirement)
		}
	}
}

func TestLocalMindRouteGroundsRegisteredEndpointSnapshot(t *testing.T) {
	runtime, _, hub, _ := newLocalMindWorkflowRuntime(t)
	registerLocalMindAgentTool(t, hub, "localmind.task.delegate_read", app.ToolCapabilityLocalMindDelegateRead, app.RiskRead, false, func(context.Context, map[string]any, string, string) (toolhub.Result, error) {
		return toolhub.Result{}, nil
	})
	route := localMindMatchedRoute(t, app.WorkflowLocalMindRead, "请 LocalMind 总结", "")
	runtime.groundLocalMindRouteIdentity(app.CapabilityLocalMindRead, &route)
	if route.Facts[localMindEndpointFact] != "test_endpoint" || route.Facts[localMindSnapshotFact] != "test_snapshot" {
		t.Fatalf("LocalMind route did not freeze the registered endpoint snapshot: %#v", route.Facts)
	}
}

func TestLocalMindWriteAndCancelStopForApproval(t *testing.T) {
	for _, workflowID := range []app.WorkflowID{app.WorkflowLocalMindWrite, app.WorkflowLocalMindCancel} {
		t.Run(string(workflowID), func(t *testing.T) {
			runtime, _, hub, session := newLocalMindWorkflowRuntime(t)
			executions := 0
			toolName := "localmind.task.delegate"
			capability := app.ToolCapabilityLocalMindDelegateWrite
			if workflowID == app.WorkflowLocalMindCancel {
				toolName, capability = "localmind.task.cancel", app.ToolCapabilityLocalMindTaskCancel
			}
			registerLocalMindAgentTool(t, hub, toolName, capability, app.RiskDangerous, true, func(context.Context, map[string]any, string, string) (toolhub.Result, error) {
				executions++
				return toolhub.Result{Output: localMindTaskOutput("task-approval", "queued", false, "v1", "")}, nil
			})
			goal := "请 LocalMind 创建一份报告"
			taskID := ""
			if workflowID == app.WorkflowLocalMindCancel {
				goal, taskID = "取消 LocalMind taskId: task-approval", "task-approval"
			}
			execution, _ := executeLocalMindRoute(t, runtime, session, workflowID, goal, taskID)
			if executions != 0 || len(execution.Approvals) != 1 || execution.ToolCalls[0].Status != app.ToolCallStatusApprovalPending {
				t.Fatalf("dangerous LocalMind action did not stop for approval: executions=%d result=%#v", executions, execution)
			}
		})
	}
}

func TestLocalMindQueryReadsOnceAndReturnsActualTerminalState(t *testing.T) {
	runtime, _, hub, session := newLocalMindWorkflowRuntime(t)
	executions := 0
	registerLocalMindAgentTool(t, hub, "localmind.task.get", app.ToolCapabilityLocalMindTaskStatus, app.RiskRead, false, func(_ context.Context, args map[string]any, _, _ string) (toolhub.Result, error) {
		executions++
		if args["task_id"] != "task-query" || stringValue(args["wait_ms"]) != "0" {
			t.Fatalf("query was not an immediate bound read: %#v", args)
		}
		return toolhub.Result{Output: localMindTaskOutput("task-query", "failed", true, "v9", "")}, nil
	})
	execution, run := executeLocalMindRoute(t, runtime, session, app.WorkflowLocalMindQuery, "查询 LocalMind taskId: task-query", "task-query")
	if executions != 1 || run.Workflow.Status != app.WorkflowStatusSucceeded || len(execution.Approvals) != 0 {
		t.Fatalf("query did not return one actual state read: executions=%d workflow=%#v", executions, run.Workflow)
	}
}

func TestLocalMindDelegationPreservesFailedCancelledAndTimeout(t *testing.T) {
	state := localMindDelegationStateForTest(t, time.Now().UTC().Add(-localMindDelegationWaitLimit-time.Second))
	for _, test := range []struct {
		status string
		signal app.OutcomeSignal
		want   string
	}{
		{status: "failed", signal: app.OutcomeSignalLocalMindTaskFailed, want: "localmind_task_failed"},
		{status: "cancelled", signal: app.OutcomeSignalLocalMindTaskCancelled, want: "localmind_task_cancelled"},
	} {
		outcome := localMindOutcomeForTest(test.status, test.signal, "30000")
		assessment := assessLocalMindDelegation(state, outcome)
		if assessment.Status != app.AssessmentBlocked || assessment.ReasonCode != test.want {
			t.Fatalf("terminal %s was not preserved: %#v", test.status, assessment)
		}
	}

	pending := localMindOutcomeForTest("running", app.OutcomeSignalLocalMindTaskPending, "30000")
	if assessment := assessLocalMindDelegation(state, pending); assessment.Status != app.AssessmentNeedsMoreEvidence {
		t.Fatalf("deadline-crossing long poll skipped its final immediate read: %#v", assessment)
	}
	pending.Refs[0].Attributes["wait_ms"] = "0"
	if assessment := assessLocalMindDelegation(state, pending); assessment.Status != app.AssessmentBlocked || assessment.ReasonCode != "localmind_task_wait_timeout" {
		t.Fatalf("expired final immediate read did not time out: %#v", assessment)
	}
	if wait := localMindTaskWaitMS(state); wait != 0 {
		t.Fatalf("expired delegation produced nonzero wait_ms: %d", wait)
	}
}

func TestLocalMindStatusPollingDoesNotConsumeGenericRepetitionBudget(t *testing.T) {
	budget := &workflowRunBudget{MaxToolCalls: 32, MaxRepeatedToolCalls: 3}
	for index := 0; index < 25; index++ {
		budget.observeToolCall(app.ToolCall{
			Tool: "localmind.task.get", Capability: app.ToolCapabilityLocalMindTaskStatus,
			Status: app.ToolCallStatusCompleted, Arguments: map[string]any{"task_id": "task-1", "wait_ms": 30000},
		})
	}
	if budget.ToolCalls != 0 || budget.RepeatedRun.Count != 0 {
		t.Fatalf("lifecycle polling consumed generic tool budget: %#v", budget)
	}
}

func localMindMatchedRoute(t *testing.T, workflowID app.WorkflowID, query, taskID string) app.RouteDecision {
	t.Helper()
	catalog := capability.MustDefaultCatalog()
	capabilityID := app.CapabilityID(workflowID)
	path, err := catalog.PathTo(capabilityID)
	if err != nil {
		t.Fatal(err)
	}
	operation := app.RouteOperationRead
	switch workflowID {
	case app.WorkflowLocalMindWrite:
		operation = app.RouteOperationEdit
	case app.WorkflowLocalMindCancel:
		operation = app.RouteOperationDelete
	}
	route := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: path, Confidence: 1, Slots: app.RouteSlots{Operation: operation, Query: query},
	}
	if taskID != "" {
		route.Slots.TargetKind, route.Slots.TargetRef = string(app.TargetKindLocalMindTask), taskID
	}
	return route
}

func localMindClearDecision(t *testing.T, capabilityID app.CapabilityID) semanticrouting.Decision {
	t.Helper()
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range graph.Candidates() {
		if candidate.Capability == capabilityID {
			return semanticrouting.Decision{
				Verdict: semanticrouting.VerdictClear, Confidence: 1, ReasonCode: "test",
				Candidates: []semanticrouting.CandidateScore{{Candidate: candidate, FusionScore: 1}},
			}
		}
	}
	t.Fatalf("missing semantic candidate for %s", capabilityID)
	return semanticrouting.Decision{}
}

func newLocalMindWorkflowRuntime(t *testing.T) (Runtime, *store.MemoryStore, *toolhub.ToolHub, app.Session) {
	t.Helper()
	cfg := agentTestConfig()
	st := store.NewMemoryStore()
	hub := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = hub.Close() })
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	session := storetest.MustCreateSession(t, st, "LocalMind Workflow")
	return runtime, st, hub, session
}

func registerLocalMindAgentTool(t *testing.T, hub *toolhub.ToolHub, name, capability string, risk app.RiskLevel, approval bool, execute toolhub.DynamicToolExecutor) {
	t.Helper()
	definition := app.ToolDefinition{
		Name: name, Description: "LocalMind Workflow test tool.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"request": map[string]any{"type": "string"}, "task_id": map[string]any{"type": "string"},
			"known_state_version": map[string]any{"type": "string"}, "wait_ms": map[string]any{"type": "integer"},
		}, "additionalProperties": false},
		Risk: risk, RequiresApproval: approval, Idempotent: true, TimeoutMS: 1000, Sandbox: "remote", Audit: "always",
		Capabilities: []app.CapabilityDescriptor{{Name: capability, Qualifiers: map[string]string{
			app.CapabilityQualifierEndpointID: "test_endpoint", app.CapabilityQualifierSnapshotRevision: "test_snapshot",
		}}}, OutcomeAdapter: app.OutcomeAdapterLocalMindTask,
		Directory: app.ToolDirectoryMetadata{Summary: "LocalMind task fixture.", WhenToUse: "Use in LocalMind Workflow tests.", Effects: []app.ToolEffect{app.ToolEffectExternalRead}},
	}
	if err := hub.ReplaceDynamicTools("fixture."+name, []toolhub.DynamicToolRegistration{{Definition: definition, RemoteName: name, Execute: execute}}); err != nil {
		t.Fatal(err)
	}
}

func executeLocalMindRoute(t *testing.T, runtime Runtime, session app.Session, workflowID app.WorkflowID, goal, taskID string) (workflowExecutionResult, app.AgentRun) {
	t.Helper()
	route := localMindMatchedRoute(t, workflowID, goal, taskID)
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(t.Context(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, app.NewID("turn"))
	if err != nil {
		t.Fatal(err)
	}
	execution := runtime.runWorkflow(t.Context(), session.ID, dispatch.Run, goal, dispatch.Profile, dispatch.Context, dispatch.Tools)
	stored, ok, err := runtime.store.GetRun(t.Context(), dispatch.Run.ID)
	if err != nil || !ok {
		t.Fatalf("load LocalMind Workflow run: ok=%v err=%v", ok, err)
	}
	return execution, stored
}

func localMindTaskOutput(taskID, status string, terminal bool, stateVersion, answer string) map[string]any {
	result := map[string]any{"kind": "answer"}
	if answer != "" {
		result["answer"] = answer
	}
	return map[string]any{
		"protocolVersion": "localmind.task.v1", "taskId": taskID, "stateVersion": stateVersion,
		"status": status, "terminal": terminal, "result": result, "error": nil,
	}
}

func localMindDelegationStateForTest(t *testing.T, acceptedAt time.Time) *app.WorkflowState {
	t.Helper()
	profile := localMindReadProfile{}
	route := localMindMatchedRoute(t, profile.ID(), "请 LocalMind 总结", "")
	intent, plan, err := profile.Resolve(route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	state := newWorkflowState(route, app.ReturnRoute{Mode: app.ReturnToSource}, intent, plan)
	delegate := state.Nodes["delegate_to_localmind"]
	delegate.Status = app.WorkflowNodeSucceeded
	delegate.OutcomeRefs = []app.ResourceRef{{
		Kind: string(app.TargetKindLocalMindTask), Ref: "task-timeout", Provenance: "tc_delegate",
		Attributes: map[string]string{"accepted_at": acceptedAt.Format(time.RFC3339Nano), "state_version": "v1"},
	}}
	state.Nodes["delegate_to_localmind"] = delegate
	query := state.Nodes["query_current_task"]
	query.Status = app.WorkflowNodeActive
	state.Nodes["query_current_task"] = query
	state.ActiveNodeIDs = []app.WorkflowNodeID{"query_current_task"}
	return state
}

func localMindOutcomeForTest(status string, signal app.OutcomeSignal, waitMS string) app.ToolOutcome {
	return app.ToolOutcome{
		ID: "outcome", ToolCallID: "tc_query", Tool: "localmind.task.get", NodeID: "query_current_task", Status: app.ToolCallStatusCompleted,
		Signals: []app.OutcomeSignal{signal}, Refs: []app.ResourceRef{{
			Kind: string(app.TargetKindLocalMindTask), Ref: "task-timeout", Provenance: "tc_query",
			Attributes: map[string]string{"status": status, "wait_ms": waitMS},
		}},
	}
}
