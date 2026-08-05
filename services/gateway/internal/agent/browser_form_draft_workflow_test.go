package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestBrowserFormDraftPlanExposesOnlyTypeAndSelectAtDraftStage(t *testing.T) {
	profile := browserFormDraftProfile{}
	intent, plan, err := profile.Resolve(app.RouteDecision{Slots: app.RouteSlots{
		Operation: app.RouteOperationDraft, Query: "Fill Name with Alice and Topic with Support",
		TargetKind: string(app.TargetKindBrowserCurrentTab), TargetRef: "selected",
	}}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowPlan(intent, profile, plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0].MaxAttempts != 48 {
		t.Fatalf("unexpected form-draft plan: %#v", plan)
	}
	var draftCapabilities []string
	for _, rule := range plan.Nodes[0].StageCapabilities {
		if rule.Stage == browserStageChooseAndDraft {
			draftCapabilities = rule.Capabilities
		}
	}
	if len(draftCapabilities) != 2 || draftCapabilities[0] != app.ToolCapabilityBrowserFormType ||
		draftCapabilities[1] != app.ToolCapabilityBrowserFormSelect {
		t.Fatalf("draft stage escaped its type/select-only boundary: %#v", draftCapabilities)
	}
	for _, requirement := range plan.Nodes[0].InitialScope.Requirements {
		if requirement.Name == app.ToolCapabilityBrowserClick {
			t.Fatal("browser.form_draft scope exposed browser.click")
		}
	}
	for _, transition := range plan.Nodes[0].Transitions {
		if transition.ID == "draft_initial_action" && transition.NextStage != browserStagePresentVisible {
			t.Fatalf("form draft actions must start only after visible presentation: %#v", transition)
		}
	}
}

func TestBrowserFormDraftStopsBeforeSixthAction(t *testing.T) {
	actions := make([]app.BrowserDraftAction, app.BrowserFormDraftMaxActions)
	state := &app.WorkflowState{
		Browser:       &app.BrowserWorkflowState{DraftActions: actions},
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_form_draft"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_form_draft": {Stage: browserStageAssessGoalAfterAction},
		},
	}
	assessment := (browserFormDraftProfile{}).Assess(state, app.ToolOutcome{
		NodeID: "browser_form_draft", Signals: []app.OutcomeSignal{app.OutcomeSignalInteractionProgress},
	})
	if assessment.Status != app.AssessmentBlocked || assessment.ReasonCode != "draft_action_limit" {
		t.Fatalf("fifth completed action did not close the draft loop: %#v", assessment)
	}
}

func TestBrowserFormDraftSettleAllowsRedactedValueDigestToRemainStable(t *testing.T) {
	state := &app.WorkflowState{
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_form_draft"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_form_draft": {
				Stage: browserStageSettleAfterAction,
				OutcomeRefs: []app.ResourceRef{{
					Kind: "browser_snapshot", Ref: "snapshot_1",
					Attributes: map[string]string{"content_digest": "redacted-content", "session_generation": "7"},
				}},
			},
		},
		Browser: &app.BrowserWorkflowState{Target: app.BrowserTargetDescriptor{CanonicalURL: "https://example.com/contact"}},
	}
	args := (browserFormDraftProfile{}).DirectStageArguments(state)
	if args["allow_no_change"] != true || args["before_digest"] != "redacted-content" {
		t.Fatalf("form draft settle did not preserve lineage while allowing a redacted digest: %#v", args)
	}
}

func TestAdaptBrowserFormOutcomePreservesFrozenDraftLineage(t *testing.T) {
	outcome := adaptBrowserFormOutcome(app.ToolCall{
		ID: "tc_type", Tool: "browser.type", Status: "completed_after_approval",
		Arguments: map[string]any{
			"uid": "snapshot_1:e1:name", "page_id": "page_1", "snapshot_id": "snapshot_1",
			"session_generation": "1785922510882919", "page_generation": "9",
		},
		Result: map[string]any{
			"draft_action_id": "browser_draft_1", "page_id": "page_1", "snapshot_id": "snapshot_1",
			"session_generation": float64(1785922510882919), "page_generation": 9, "snapshot_digest": "digest_1",
			"role": "textbox", "accessible_name": "Name", "form_context": "Contact form",
			"value_source": "owner_request", "value_digest": "value_digest_1",
			"output": map[string]any{
				"page_id": "page_1", "snapshot_id": "snapshot_1",
				"session_generation": float64(1785922510882919), "page_generation": 10, "role": "textbox",
			},
		},
	}, "browser_form_draft")
	if len(outcome.Signals) != 1 || outcome.Signals[0] != app.OutcomeSignalDraftActionCompleted || len(outcome.Refs) != 1 {
		t.Fatalf("draft action outcome is incomplete: %#v", outcome)
	}
	ref := outcome.Refs[0]
	if ref.Kind != "browser_draft" || ref.Ref != "snapshot_1:e1:name" || ref.Provenance != "tc_type" {
		t.Fatalf("draft action identity was not preserved: %#v", ref)
	}
	want := map[string]string{
		"action_id": "browser_draft_1", "operation": "type", "page_id": "page_1", "snapshot_id": "snapshot_1",
		"session_generation": "1785922510882919", "page_generation": "9", "snapshot_digest": "digest_1", "role": "textbox",
		"name": "Name", "container": "Contact form", "value_source": "owner_request", "value_digest": "value_digest_1",
	}
	for key, expected := range want {
		if actual := ref.Attributes[key]; actual != expected {
			t.Fatalf("draft action attribute %s = %q, want %q; ref=%#v", key, actual, expected, ref)
		}
	}
}

func TestBrowserFormDraftTransitionPreservesActionLineage(t *testing.T) {
	before := app.ResourceRef{
		Kind: "browser_snapshot", Ref: "snapshot_1",
		Attributes: map[string]string{
			"page_id": "page_1", "session_generation": "7", "page_generation": "9", "digest": "digest_1",
		},
	}
	draft := app.ResourceRef{
		Kind: "browser_draft", Ref: "snapshot_1:e1:name",
		Attributes: map[string]string{
			"page_id": "page_1", "snapshot_id": "snapshot_1", "session_generation": "7",
		},
	}
	after := app.ResourceRef{
		Kind: "browser_snapshot", Ref: "snapshot_2",
		Attributes: map[string]string{
			"page_id": "page_1", "session_generation": "7", "page_generation": "10",
			"digest": "digest_2", "previous_snapshot_id": "snapshot_1",
		},
	}
	refs := browserTransitionRefs([]app.ResourceRef{before, draft}, []app.ResourceRef{after})
	if !browserDraftTransitionValid(refs) {
		t.Fatalf("valid draft transition lost its action or snapshot lineage: %#v", refs)
	}
}

func TestBrowserFormDraftRequiresEveryNamedControlBeforeVisibleCompletion(t *testing.T) {
	nameRef := "snapshot_2:e3:name-fingerprint"
	topicRef := "snapshot_2:e4:topic-fingerprint"
	state := &app.WorkflowState{
		Route:         app.RouteDecision{Slots: app.RouteSlots{Query: "Fill Name with Alice Example and Topic with Technical Support"}},
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_form_draft"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_form_draft": {
				Stage: browserStageAssessGoalAfterAction,
				OutcomeRefs: []app.ResourceRef{
					{Kind: "browser_page", Ref: "page_1", Attributes: map[string]string{"presentation": "visible"}},
					{Kind: "browser_snapshot", Ref: "snapshot_2", Provenance: "tc_snapshot", Attributes: map[string]string{
						"page_id": "page_1", "digest": "digest_2", "session_generation": "8", "page_generation": "4", "presentation": "visible",
					}},
					{Kind: "browser_element", Ref: nameRef, Attributes: map[string]string{"snapshot_id": "snapshot_2", "role": "textbox", "name": "Name"}},
					{Kind: "browser_element", Ref: topicRef, Attributes: map[string]string{"snapshot_id": "snapshot_2", "role": "combobox", "name": "Topic"}},
				},
			},
		},
		Browser: &app.BrowserWorkflowState{
			Result: &app.BrowserResultEvidence{},
			DraftActions: []app.BrowserDraftAction{{
				ElementRef: "snapshot_1:e3:name-fingerprint", Role: "textbox", AccessibleName: "Name", Completed: true,
			}},
		},
	}
	outcome := app.ToolOutcome{
		ToolCallID: "tc_goal", NodeID: "browser_form_draft",
		Signals: []app.OutcomeSignal{app.OutcomeSignalInteractionGoalSatisfied},
		Refs:    []app.ResourceRef{{Kind: "browser_goal_assessment", Ref: "tc_goal"}},
	}
	assessment := (browserFormDraftProfile{}).Assess(state, outcome)
	if assessment.Status != app.AssessmentNeedsMoreEvidence || assessment.ReasonCode != browserStageChooseAndDraft ||
		len(assessment.Signals) != 1 || assessment.Signals[0] != app.OutcomeSignalInteractionProgress {
		t.Fatalf("premature satisfied verdict skipped the requested Topic control: %#v", assessment)
	}
	state.Browser.DraftActions = append(state.Browser.DraftActions, app.BrowserDraftAction{
		ElementRef: "snapshot_1:e4:topic-fingerprint", Role: "combobox", AccessibleName: "Topic", Completed: true,
	})
	assessment = (browserFormDraftProfile{}).Assess(state, outcome)
	if assessment.Status != app.AssessmentComplete || assessment.ReasonCode != "browser_visible_draft_verified" ||
		state.Browser.Result.VisibleSnapshotID != "snapshot_2" || state.Browser.Result.GoalAssessmentCallID != "tc_goal" {
		t.Fatalf("complete named draft controls did not produce visible completion evidence: assessment=%#v result=%#v", assessment, state.Browser.Result)
	}
}

func TestBrowserFormDraftQueuesIndependentRedactedApprovals(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.browser = true
		cfg.config.Tools.BrowserAutomation.Enabled = true
	})
	defer closeRuntime()

	profile := browserFormDraftProfile{}
	route := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched,
		CatalogRevision: runtime.capabilities.Revision(),
		CapabilityPath:  []app.CapabilityID{"browser", app.CapabilityBrowserFormDraft},
		Slots: app.RouteSlots{
			Operation: app.RouteOperationDraft, Query: "Fill Name with Alice Example and Topic with Technical Support",
			TargetKind: string(app.TargetKindBrowserCurrentTab), TargetRef: "selected",
		},
	}
	intent, plan, err := profile.Resolve(route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	state := newWorkflowState(route, app.ReturnRoute{Mode: app.ReturnToSource}, intent, plan)
	if _, err := profile.Prepare(state); err != nil {
		t.Fatal(err)
	}
	nodeID := state.ActiveNodeIDs[0]
	node := state.Nodes[nodeID]
	node.Stage = browserStageChooseAndDraft
	node.OutcomeRefs = browserFormDraftApprovalRefs()
	node.SelectedEntries = []app.ToolDirectoryEntryID{browserFormDraftTestEntry(t, runtime, "browser.type", app.ToolCapabilityBrowserFormType)}
	state.Nodes[nodeID] = node
	run := app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "executing",
		StartedAt: time.Now().UTC(), Workflow: state,
	}
	st.SaveRun(run)

	typeCall, typeApproval, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
		Name: "browser.type", Args: map[string]any{"uid": "snapshot_1:e1:name", "text": "Alice Example"},
		WorkflowID: app.WorkflowBrowserFormDraft, WorkflowNodeID: nodeID, ScopeRevision: node.ScopeRevision,
		Capability: app.ToolCapabilityBrowserFormType,
	})
	stored, _ := st.GetRun(run.ID)
	storedNode := stored.Workflow.Nodes[nodeID]
	storedNode.SelectedEntries = []app.ToolDirectoryEntryID{browserFormDraftTestEntry(t, runtime, "browser.select", app.ToolCapabilityBrowserFormSelect)}
	stored.Workflow.Nodes[nodeID] = storedNode
	st.SaveRun(stored)
	selectCall, selectApproval, _ := runtime.runToolPlan(context.Background(), session.ID, run.ID, toolPlan{
		Name: "browser.select", Args: map[string]any{"uid": "snapshot_1:e2:topic", "value": "Technical Support"},
		WorkflowID: app.WorkflowBrowserFormDraft, WorkflowNodeID: nodeID, ScopeRevision: node.ScopeRevision,
		Capability: app.ToolCapabilityBrowserFormSelect,
	})
	if typeApproval == nil || selectApproval == nil || typeApproval.ID == selectApproval.ID ||
		typeApproval.ToolCallID == selectApproval.ToolCallID || typeCall.Status != "approval_pending" || selectCall.Status != "approval_pending" {
		t.Fatalf("draft actions did not receive independent approvals: type_call=%#v type=%#v select_call=%#v select=%#v", typeCall, typeApproval, selectCall, selectApproval)
	}
	for _, approval := range []*app.Approval{typeApproval, selectApproval} {
		if strings.Contains(approval.Summary, "Alice Example") || strings.Contains(approval.Summary, "Technical Support") ||
			!strings.Contains(approval.Summary, "value hidden") {
			t.Fatalf("approval summary exposed a draft value: %#v", approval)
		}
	}
	if typeApproval.Arguments["text"] != "Alice Example" || selectApproval.Arguments["value"] != "Technical Support" {
		t.Fatalf("approval did not bind exact executable owner values: type=%#v select=%#v", typeApproval.Arguments, selectApproval.Arguments)
	}
}

func TestBrowserFormDraftMaterializesCurrentAllowedUIDEnums(t *testing.T) {
	nameRef := "snapshot_2:e1:name"
	topicRef := "snapshot_2:e2:topic"
	submitRef := "snapshot_2:e3:submit"
	run := app.AgentRun{Workflow: &app.WorkflowState{
		Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserFormDraft},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_form_draft": {
				Stage: browserStageChooseAndDraft,
				OutcomeRefs: []app.ResourceRef{
					{Kind: "browser_snapshot", Ref: "snapshot_2", Attributes: map[string]string{"page_id": "page_1", "session_generation": "7", "page_generation": "10"}},
					{Kind: "browser_element", Ref: nameRef, Attributes: map[string]string{"snapshot_id": "snapshot_2", "role": "textbox", "name": "Name"}},
					{Kind: "browser_element", Ref: topicRef, Attributes: map[string]string{"snapshot_id": "snapshot_2", "role": "combobox", "name": "Topic"}},
					{Kind: "browser_element", Ref: submitRef, Attributes: map[string]string{"snapshot_id": "snapshot_2", "role": "button", "name": "Submit request"}},
				},
			},
		},
		Browser: &app.BrowserWorkflowState{DraftActions: []app.BrowserDraftAction{{
			ElementRef: "snapshot_1:e1:name", Role: "textbox", AccessibleName: "Name", Completed: true,
		}}},
	}}
	properties := map[string]any{
		"uid": map[string]any{"type": "string"}, "page_id": map[string]any{"type": "string"},
		"snapshot_id": map[string]any{"type": "string"}, "session_generation": map[string]any{"type": []any{"string", "number"}},
		"page_generation": map[string]any{"type": []any{"string", "number"}},
	}
	definitions := []app.ToolDefinition{
		{Name: "browser.type", Description: "Type", InputSchema: map[string]any{"type": "object", "properties": cloneAnyMap(properties)}},
		{Name: "browser.select", Description: "Select", InputSchema: map[string]any{"type": "object", "properties": cloneAnyMap(properties)}},
	}
	materialized := materializeBrowserFormDraftSchemas(definitions, run, "browser_form_draft")
	propertyEnum := func(definition app.ToolDefinition, key string) []any {
		properties, _ := anyMap(definition.InputSchema["properties"])
		property, _ := anyMap(properties[key])
		values, _ := property["enum"].([]any)
		return values
	}
	if got := propertyEnum(materialized[0], "uid"); len(got) != 1 || got[0] != topicRef {
		t.Fatalf("browser.type uid enum = %#v, want remaining current text controls", got)
	}
	if got := propertyEnum(materialized[1], "uid"); len(got) != 1 || got[0] != topicRef {
		t.Fatalf("browser.select uid enum = %#v, want current select controls", got)
	}
	for key, expected := range map[string]any{"page_id": "page_1", "snapshot_id": "snapshot_2", "session_generation": "7", "page_generation": "10"} {
		if got := propertyEnum(materialized[0], key); len(got) != 1 || got[0] != expected {
			t.Fatalf("browser.type %s enum = %#v, want exact current binding %v", key, got, expected)
		}
	}
	originalProperties, _ := anyMap(definitions[0].InputSchema["properties"])
	uid, _ := anyMap(originalProperties["uid"])
	if uid["enum"] != nil {
		t.Fatalf("materialization mutated the registered schema: %#v", definitions[0].InputSchema)
	}
}

func TestBrowserFormDraftMaterializesCurrentSnapshotBindings(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
	})
	defer closeRuntime()

	plan := browserFormDraftPlan()
	nodeID := plan.Nodes[0].ID
	state := newWorkflowState(app.RouteDecision{}, app.ReturnRoute{Mode: app.ReturnToSource}, app.IntentEnvelope{}, plan)
	node := state.Nodes[nodeID]
	node.Stage = browserStageChooseAndDraft
	node.ScopeRevision = 15
	node.OutcomeRefs = []app.ResourceRef{
		{Kind: "browser_snapshot", Ref: "snapshot_old", Attributes: map[string]string{
			"page_id": "page_old", "session_generation": "6", "page_generation": "2",
		}},
		{Kind: "browser_snapshot", Ref: "snapshot_current", Attributes: map[string]string{
			"page_id": "page_1", "session_generation": "1785923761219871", "page_generation": "3",
		}},
		{Kind: "browser_element", Ref: "snapshot_current:e4:topic", Attributes: map[string]string{
			"snapshot_id": "snapshot_current", "page_id": "page_1", "role": "combobox", "name": "Topic",
		}},
	}
	state.Nodes[nodeID] = node
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, Workflow: state, StartedAt: time.Now().UTC()}
	st.SaveRun(run)

	materialized := runtime.materializeWorkflowBoundArguments(run.ID, toolPlan{
		Name: "browser.select", Args: map[string]any{
			"uid": "snapshot_current:e4:topic", "value": "Technical Support",
			"page_id": "page_wrong", "snapshot_id": "snapshot_wrong",
			"session_generation": 1, "page_generation": 1,
		},
		WorkflowID: app.WorkflowBrowserFormDraft, WorkflowNodeID: nodeID, ScopeRevision: 15,
		Capability: app.ToolCapabilityBrowserFormSelect,
	})
	want := map[string]any{
		"page_id": "page_1", "snapshot_id": "snapshot_current",
		"session_generation": "1785923761219871", "page_generation": "3",
	}
	for key, expected := range want {
		if actual := materialized.Args[key]; actual != expected {
			t.Fatalf("materialized %s = %#v, want %#v; args=%#v", key, actual, expected, materialized.Args)
		}
	}
}

func browserFormDraftTestEntry(t *testing.T, runtime Runtime, tool, capability string) app.ToolDirectoryEntryID {
	t.Helper()
	definition, ok := runtime.tools.Definition(tool)
	if !ok {
		t.Fatalf("%s is unavailable", tool)
	}
	for _, descriptor := range definition.Capabilities {
		if descriptor.Name == capability {
			return directoryEntryID(definition, descriptor)
		}
	}
	t.Fatalf("%s does not register capability %s", tool, capability)
	return ""
}

func browserFormDraftApprovalRefs() []app.ResourceRef {
	return []app.ResourceRef{
		{Kind: "browser_page", Ref: "page_1", Attributes: map[string]string{"url": "https://example.com/contact"}},
		{Kind: "browser_snapshot", Ref: "snapshot_1", Attributes: map[string]string{
			"page_id": "page_1", "digest": "digest_1", "session_generation": "7", "page_generation": "9",
		}},
		{Kind: "browser_element", Ref: "snapshot_1:e1:name", Attributes: map[string]string{
			"snapshot_id": "snapshot_1", "page_id": "page_1", "role": "textbox", "name": "Name", "container": "Contact form",
		}},
		{Kind: "browser_element", Ref: "snapshot_1:e2:topic", Attributes: map[string]string{
			"snapshot_id": "snapshot_1", "page_id": "page_1", "role": "combobox", "name": "Topic", "container": "Contact form",
		}},
	}
}
