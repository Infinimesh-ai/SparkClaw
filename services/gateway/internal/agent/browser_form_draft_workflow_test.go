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
