package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func mustRecognizeWorkflow(t *testing.T, sourceTurnID, content string) recognizedWorkflow {
	t.Helper()
	matched, ok, err := defaultWorkflowProfileRegistry().Recognize(sourceTurnID, content)
	if err != nil {
		t.Fatalf("recognize workflow: %v", err)
	}
	if !ok {
		t.Fatalf("no workflow profile recognized %q", content)
	}
	return matched
}

func assertNoLegacyRoutingAudit(t *testing.T, events []app.AuditEvent) {
	t.Helper()
	for _, event := range events {
		if event.Type == "task_hint.generated" || event.Type == "task_hint.fallback" || event.Type == "react.visible_tools_expanded" {
			t.Fatalf("migrated workflow used a legacy routing path: %#v", event)
		}
	}
}

func TestWorkflowRegistryRecognizesOneProfilePerMigratedIntent(t *testing.T) {
	tests := []struct {
		content string
		want    app.WorkflowID
	}{
		{content: "查一下 SparkClaw 最新消息", want: app.WorkflowWebPublicResearch},
		{content: "查一下最新的 profile API", want: app.WorkflowWebPublicResearch},
		{content: "总结 https://example.com/article", want: app.WorkflowWebExplicitURL},
		{content: "Search for approval-first in the workspace", want: app.WorkflowWorkspaceSearch},
		{content: "Find approval-first", want: app.WorkflowWorkspaceSearch},
		{content: "Find webhook config in the workspace", want: app.WorkflowWorkspaceSearch},
		{content: "Summarize docs/architecture.md", want: app.WorkflowWorkspaceRead},
		{content: "Summarize release/edition.txt", want: app.WorkflowWorkspaceRead},
	}
	registry := defaultWorkflowProfileRegistry()
	for _, test := range tests {
		matched, ok, err := registry.Recognize("turn", test.content)
		if err != nil || !ok || matched.Profile.ID() != test.want {
			t.Fatalf("recognize %q: profile=%v ok=%v err=%v", test.content, profileID(matched.Profile), ok, err)
		}
		profile, plan, err := registry.Resolve(matched)
		if err != nil || profile.ID() != test.want || plan.ProfileID != test.want {
			t.Fatalf("resolve %q: profile=%v plan=%v err=%v", test.content, profileID(profile), plan.ProfileID, err)
		}
	}
	for _, content := range []string{
		"List my reminders",
		"Edit docs/architecture.md",
		"Inspect screenshot.png",
		"Remember that risky actions need approval",
	} {
		if matched, ok, err := registry.Recognize("turn", content); err != nil || ok {
			t.Fatalf("unmigrated or specialized request %q was captured by %v: ok=%v err=%v", content, profileID(matched.Profile), ok, err)
		}
	}
}

func TestWorkflowRegistryRejectsInvalidProfilePlan(t *testing.T) {
	intent := singleObjectiveIntent("turn", app.IntentDomainWeb, app.IntentOperationSearch, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopePublic)
	profile := testWorkflowProfile{
		id:     "test.invalid",
		intent: intent,
		plan:   app.WorkflowPlan{SchemaVersion: 1, ProfileID: "test.other", ProfileRevision: 1},
	}
	registry := newWorkflowProfileRegistry(profile)
	matched, ok, err := registry.Recognize("turn", "anything")
	if err != nil || !ok {
		t.Fatalf("test profile was not recognized: ok=%v err=%v", ok, err)
	}
	if _, _, err := registry.Resolve(matched); err == nil {
		t.Fatal("profile plan identity mismatch must fail before persistence")
	}
}

func TestWorkflowActivatesDependentNodesBeforeCompleting(t *testing.T) {
	intent := singleObjectiveIntent("turn", app.IntentDomainWorkspace, app.IntentOperationSearch, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeWorkspace)
	plan := app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: "test.sequence", ProfileRevision: 1, InitialNodeIDs: []app.WorkflowNodeID{"discover"}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{
			{ID: "discover", Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "discover", Completion: app.CompletionEvidence}, InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "workspace.file.search"}}}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1},
			{ID: "read", DependsOn: []app.WorkflowNodeID{"discover"}, Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "read", Completion: app.CompletionEvidence}, InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "workspace.file.read"}}}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1},
		},
	}
	state := newWorkflowState(intent, plan)
	if state.Nodes["read"].Status != app.WorkflowNodePending {
		t.Fatalf("dependent node must start pending: %#v", state.Nodes["read"])
	}
	run := app.AgentRun{ID: "run_sequence", Workflow: state}
	changed, err := applyWorkflowOutcome(&run, app.ToolOutcome{ID: "outcome_discover", ToolCallID: "tc_discover", NodeID: "discover"}, app.NodeAssessment{OutcomeID: "outcome_discover", NodeID: "discover", Status: app.AssessmentComplete})
	if err != nil || !changed || run.Workflow.Status != app.WorkflowStatusRunning || run.Workflow.Nodes["read"].Status != app.WorkflowNodeActive || len(run.Workflow.ActiveNodeIDs) != 1 || run.Workflow.ActiveNodeIDs[0] != "read" {
		t.Fatalf("dependent node was not activated: changed=%v err=%v state=%#v", changed, err, run.Workflow)
	}
	changed, err = applyWorkflowOutcome(&run, app.ToolOutcome{ID: "outcome_read", ToolCallID: "tc_read", NodeID: "read"}, app.NodeAssessment{OutcomeID: "outcome_read", NodeID: "read", Status: app.AssessmentComplete})
	if err != nil || changed || run.Workflow.Status != app.WorkflowStatusSucceeded || len(run.Workflow.ActiveNodeIDs) != 0 {
		t.Fatalf("workflow completed before or after the wrong boundary: changed=%v err=%v state=%#v", changed, err, run.Workflow)
	}
}

func TestWorkflowRegistryRejectsAmbiguousRecognition(t *testing.T) {
	intent := singleObjectiveIntent("turn", app.IntentDomainWeb, app.IntentOperationSearch, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopePublic)
	registry := newWorkflowProfileRegistry(
		testWorkflowProfile{id: "test.one", intent: intent},
		testWorkflowProfile{id: "test.two", intent: intent},
	)
	if _, ok, err := registry.Recognize("turn", "anything"); !ok || err == nil {
		t.Fatalf("ambiguous recognition must fail closed: ok=%v err=%v", ok, err)
	}
}

func TestFastSemanticNormalizationCannotChangeDeterministicTargetOrProfile(t *testing.T) {
	fallback := singleObjectiveIntent("turn", app.IntentDomainWeb, app.IntentOperationRead, app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: "https://example.com/allowed"}, app.DataScopePublic)
	candidate := fallback
	candidate.SourceTurnID = "invented"
	candidate.Objectives = append([]app.Objective(nil), fallback.Objectives...)
	candidate.Objectives[0].Domain = app.IntentDomainWorkspace
	candidate.Objectives[0].Operation = app.IntentOperationSearch
	candidate.Objectives[0].Target = app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: "secrets.txt"}
	candidate.Objectives[0].Explicit = false
	normalized := normalizeStableIntent(candidate, fallback)
	if normalized.SourceTurnID != fallback.SourceTurnID || normalized.Objectives[0].Target != fallback.Objectives[0].Target || !normalized.Objectives[0].Explicit {
		t.Fatalf("Fast output changed deterministic facts: %#v", normalized)
	}
	if (webExplicitURLProfile{}).Match(normalized) {
		t.Fatalf("a semantic profile change should be rejected before routing: %#v", normalized)
	}
	normalized = fallback
	profile, ok, err := defaultWorkflowProfileRegistry().Route(normalized)
	if err != nil || !ok || profile.ID() != app.WorkflowWebExplicitURL {
		t.Fatalf("stable fallback did not route through the registry: profile=%v ok=%v err=%v", profileID(profile), ok, err)
	}
}

func TestWorkspaceReadArgumentsStayInsideIntentTarget(t *testing.T) {
	matched := mustRecognizeWorkflow(t, "turn", "Summarize docs/architecture.md")
	plan, err := matched.Profile.Resolve(matched.Intent)
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{
		ID:        "run_workspace_resource_boundary",
		SessionID: "session",
		Workflow:  newWorkflowState(matched.Intent, plan),
	}
	root := t.TempDir()
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, ".sparkclaw", "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	definition, ok := tools.Definition("files.read")
	if !ok {
		t.Fatal("files.read definition missing")
	}
	node := run.Workflow.Nodes["read"]
	node.SelectedEntries = []app.ToolDirectoryEntryID{directoryEntryID(definition, app.CapabilityDescriptor{Name: "workspace.file.read"})}
	run.Workflow.Nodes["read"] = node
	st.SaveRun(run)
	runtime := NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	call := toolPlan{
		Name: "files.read", Args: map[string]any{"path": "docs/architecture.md"},
		WorkflowID: app.WorkflowWorkspaceRead, WorkflowNodeID: "read", ScopeRevision: 1, Capability: "workspace.file.read",
	}
	if err := runtime.validateWorkflowToolPlan(run.ID, call, definition); err != nil {
		t.Fatalf("frozen workspace path was rejected: %v", err)
	}
	call.Args["path"] = "secrets.txt"
	if err := runtime.validateWorkflowToolPlan(run.ID, call, definition); err == nil {
		t.Fatal("unrelated workspace path escaped the intent resource boundary")
	}
}

func TestDirectorySelectionUsesOnlyBoundedEntries(t *testing.T) {
	cfg := agentTestConfig()
	st := store.NewMemoryStore()
	session := st.CreateSession("directory selection")
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	intent := singleObjectiveIntent("turn", app.IntentDomainWorkspace, app.IntentOperationSearch, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeWorkspace)
	plan, err := (workspaceFileSearchProfile{}).Resolve(intent)
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: "run", SessionID: session.ID, Workflow: newWorkflowState(intent, plan)}
	view := app.DirectoryView{
		NodeID: "search",
		Entries: []app.ToolDirectoryEntry{
			{ID: "entry_first", Capability: app.CapabilityDescriptor{Name: "workspace.file.search"}, Summary: "first"},
			{ID: "entry_second", Capability: app.CapabilityDescriptor{Name: "workspace.file.search"}, Summary: "second"},
		},
	}
	selected, automatic, err := runtime.selectDirectoryEntry(context.Background(), run, view)
	if err != nil || automatic || selected.ID != "entry_first" {
		t.Fatalf("bounded semantic selection failed: selected=%#v automatic=%v err=%v", selected, automatic, err)
	}
	if calls := st.ListModelCalls(run.SessionID, run.ID); len(calls) != 1 || calls[0].Operation != "tool_directory_selection" {
		t.Fatalf("directory selection was not recorded as a separate model operation: %#v", calls)
	}
}

func profileID(profile workflowProfile) app.WorkflowID {
	if profile == nil {
		return ""
	}
	return profile.ID()
}

type testWorkflowProfile struct {
	id     app.WorkflowID
	intent app.IntentEnvelope
	plan   app.WorkflowPlan
}

func (p testWorkflowProfile) ID() app.WorkflowID { return p.id }
func (testWorkflowProfile) Revision() int        { return 1 }
func (p testWorkflowProfile) Recognize(string, string) (app.IntentEnvelope, bool) {
	return p.intent, true
}
func (p testWorkflowProfile) Match(intent app.IntentEnvelope) bool {
	return len(intent.Objectives) == len(p.intent.Objectives) && len(intent.Objectives) > 0 &&
		intent.Objectives[0].Domain == p.intent.Objectives[0].Domain && intent.Objectives[0].Operation == p.intent.Objectives[0].Operation
}
func (p testWorkflowProfile) Resolve(app.IntentEnvelope) (app.WorkflowPlan, error) {
	if p.plan.ProfileID != "" {
		return p.plan, nil
	}
	return app.WorkflowPlan{ProfileID: p.id}, nil
}
func (testWorkflowProfile) Assess(*app.WorkflowState, app.ToolOutcome) app.NodeAssessment {
	return app.NodeAssessment{}
}
func (testWorkflowProfile) Hint(*app.WorkflowState) workflowExecutionHint {
	return workflowExecutionHint{}
}
func (testWorkflowProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
