package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestDocumentEditWorkflowReadsApprovesResumesAndReturnsTextCopy(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "notes.md")
	if err := os.WriteFile(inputPath, []byte("# Notes\nOriginal reflection"), 0o640); err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	session = storetest.MustCreateSession(t, st, "document edit without an explicit session workspace")

	goal := "Replace Original reflection in notes.md"
	started := time.Now().UTC()
	user := storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: goal, CreatedAt: started})
	route, err := runtime.routeIntentForTest(session.ID, user.ID, goal, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit || route.Facts["document_operation"] != "" {
		t.Fatalf("text edit did not resolve to document.edit: %#v", route)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "received", Risk: app.RiskReversible, StartedAt: started,
		MessageContext: &app.MessageRunContext{OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID}, ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource}, Route: route},
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != "files.read" {
		t.Fatalf("document edit did not expose its reader first: %#v", visibleToolNames(dispatch.Tools))
	}

	readCall, approval, _, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "files.read", Args: map[string]any{"path": "notes.md"}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: "document_locate_evidence", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		t.Fatalf("document read did not complete before mutation: call=%#v approval=%#v", readCall, approval)
	}
	definition, _ := runtime.tools.Definition(readCall.Tool)
	outcome, err := adaptWorkflowOutcome(definition, readCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := testGetRun(st, dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(storedRun.Workflow, outcome)
	changed, err := applyWorkflowOutcome(&storedRun, outcome, assessment)
	if err != nil || !changed {
		t.Fatalf("structured read did not activate operation decision: changed=%t assessment=%#v err=%v", changed, assessment, err)
	}
	testSaveRun(st, storedRun)
	if _, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &storedRun, dispatch.Profile); err != nil || !changed {
		t.Fatalf("single-candidate text operation decision did not resolve deterministically: changed=%t err=%v", changed, err)
	}
	if hasModelCallOperation(testListModelCalls(st, session.ID, storedRun.ID), "workflow_operation_selection", documentWorkflowModelLane) {
		t.Fatalf("single-candidate text edit made an unnecessary operation-selection model call")
	}
	stageContext := dispatch.Profile.StageContext(storedRun.Workflow)
	editTools, err := runtime.materializeActiveWorkflowTools(context.Background(), storedRun, runtime.workflowActorRef(storedRun), &stageContext)
	if err != nil {
		t.Fatal(err)
	}
	if !exactVisibleToolNames(editTools, "text.replace_text", "observation.read") {
		t.Fatalf("text edit stage exposed the wrong editor: %#v", visibleToolNames(editTools))
	}

	editCall, editApproval, _, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "text.replace_text",
		Args: map[string]any{
			"path": "model-invented-input.md", "output_path": "model-invented-output.md", "expected_replacements": 1,
			"replacements": []any{map[string]any{"find": "Original reflection", "replace": "Improved reflection"}},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentEdit,
	})
	if editApproval == nil || editCall.Status != "approval_pending" {
		t.Fatalf("text edit did not enter recoverable approval: call=%#v approval=%#v", editCall, editApproval)
	}
	if editCall.Arguments["path"] != "notes.md" || editCall.Arguments["output_path"] != "notes-sparkclaw-edit.md" ||
		editApproval.Arguments["path"] != "notes.md" || editApproval.Arguments["output_path"] != "notes-sparkclaw-edit.md" {
		t.Fatalf("workflow did not inject its frozen input/output paths: call=%#v approval=%#v", editCall.Arguments, editApproval.Arguments)
	}
	if _, err := os.Stat(filepath.Join(root, "notes-sparkclaw-edit.md")); !os.IsNotExist(err) {
		t.Fatalf("text output existed before approval: %v", err)
	}
	storedRun, _ = testGetRun(st, storedRun.ID)
	storedRun.State = "approval_pending"
	testSaveRun(st, storedRun)
	testSaveModelCall(st, app.ModelCall{ID: app.NewID("mcall"), SessionID: session.ID, RunID: storedRun.ID, Operation: "workflow_step_2", Status: "completed", StartedAt: time.Now().UTC()})

	resolved, err := st.ResolveApproval(editApproval.ID, "approved", "owner approved document copy")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved)
	if err != nil || executed.Status != "completed_after_approval" {
		t.Fatalf("approved text edit did not execute: call=%#v err=%v", executed, err)
	}
	result, resumed, err := runtime.ResumeRunAfterApproval(context.Background(), session.ID, storedRun.ID)
	if err != nil || !resumed {
		t.Fatalf("approved document workflow did not resume: resumed=%t result=%#v err=%v", resumed, result, err)
	}
	if result.Run.State != "completed" || result.WorkflowResult == nil || result.WorkflowResult.Status != app.WorkflowResultSucceeded {
		t.Fatalf("document workflow did not complete after approval: %#v", result)
	}
	if result.Message.Content != "" || len(result.Message.Attachments) != 1 || result.Message.Attachments[0].RelPath != "notes-sparkclaw-edit.md" {
		t.Fatalf("document workflow did not return the modified file as an attachment: %#v", result.Message)
	}
	if result.WorkflowResult.Data == nil || result.WorkflowResult.Data["change_summary"] == nil || len(result.WorkflowResult.Content.Parts) != 1 ||
		result.WorkflowResult.Content.Parts[0].Kind != app.MessagePartFile || result.WorkflowResult.Content.Parts[0].Disposition != app.MessageDispositionAttachment {
		t.Fatalf("unified document result omitted change summary or file: %#v", result.WorkflowResult)
	}
	documentRecords := st.ListDocumentRecords(session.OwnerID, session.ID, 10)
	if len(documentRecords) < 2 || documentRecords[0].GovernedPath != "notes-sparkclaw-edit.md" ||
		documentRecords[0].ParentDocumentID == "" || documentRecords[0].SourceToolCallID != executed.ID ||
		documentRecords[0].LastActivity != app.DocumentActivityEdited {
		t.Fatalf("approved edit output was not recorded with document lineage: %#v", documentRecords)
	}
	outputRecord := documentRecords[0]
	followUp := "继续修改刚才编辑好的文件，把 Improved reflection 改为 Final reflection"
	resolution := mustResolveDocumentContext(t, runtime, session.ID, "run_follow_up", followUp, nil)
	if len(resolution.References) != 1 || resolution.References[0].DocumentID != outputRecord.ID ||
		resolution.References[0].ParentDocumentID != outputRecord.ParentDocumentID ||
		resolution.References[0].Ref != outputRecord.GovernedPath ||
		resolution.References[0].Source != app.DocumentSourceToolOutput ||
		resolution.References[0].Activity != app.DocumentActivityEdited {
		t.Fatalf("recent document resolver did not preserve the edited output and its lineage: %#v", resolution)
	}
	unrelatedRoute := mustRouteIntentOutput(t, runtime, session.ID, "今天杭州天气", nil, app.MessageSourceWeb).Route
	if unrelatedRoute.Status != app.RouteMatched || len(unrelatedRoute.CapabilityPath) != 2 ||
		unrelatedRoute.CapabilityPath[1] != app.CapabilityBrowserWeather ||
		unrelatedRoute.Slots.TargetKind != string(app.TargetKindLocation) ||
		unrelatedRoute.Slots.TargetRef != "杭州" || unrelatedRoute.Facts["document_id"] != "" {
		t.Fatalf("unrelated request inherited the recent edited document: %#v", unrelatedRoute)
	}
	followUpRoute := mustRouteIntentOutput(t, runtime, session.ID, followUp, nil, app.MessageSourceWeb).Route
	if followUpRoute.Status != app.RouteMatched || len(followUpRoute.CapabilityPath) != 2 ||
		followUpRoute.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		followUpRoute.Slots.TargetRef != "notes-sparkclaw-edit.md" ||
		followUpRoute.Slots.OutputRef != "notes-sparkclaw-edit-2.md" ||
		followUpRoute.Facts["document_id"] != outputRecord.ID ||
		followUpRoute.Facts["document_parent_id"] != outputRecord.ParentDocumentID {
		t.Fatalf("follow-up edit did not bind the latest edited document: %#v", followUpRoute)
	}

	original, err := os.ReadFile(inputPath)
	if err != nil || string(original) != "# Notes\nOriginal reflection" {
		t.Fatalf("original text changed: %q err=%v", original, err)
	}
	updated, err := os.ReadFile(filepath.Join(root, "notes-sparkclaw-edit.md"))
	if err != nil || string(updated) != "# Notes\nImproved reflection" {
		t.Fatalf("output text mismatch: %q err=%v", updated, err)
	}
}

func TestDocumentEditLocateEvidenceInvokesBoundReaderBeforeModel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Notes\nOriginal reflection"), 0o640); err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	goal := "Replace Original reflection in notes.md"
	route, err := runtime.routeIntentForTest(session.ID, "turn", goal, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}

	result := runtime.runWorkflow(context.Background(), session.ID, dispatch.Run, goal+`
MOCK_STEP_RESPONSE:{"type":"action","tool":"text.replace_text","arguments":{"path":"model-path.md","output_path":"model-output.md","expected_replacements":1,"replacements":[{"find":"Original reflection","replace":"Improved reflection"}]}}`, dispatch.Profile, dispatch.Context, dispatch.Tools)

	storedRun, ok := testGetRun(st, dispatch.Run.ID)
	if !ok || storedRun.Workflow == nil {
		t.Fatal("document edit workflow state was not persisted")
	}
	locate := storedRun.Workflow.Nodes["document_locate_evidence"]
	if locate.Status != app.WorkflowNodeSucceeded || locate.Attempts != 1 ||
		locate.LastAssessment == nil || locate.LastAssessment.ReasonCode != "document_evidence_located" {
		t.Fatalf("direct document read did not complete the locate node: %#v", locate)
	}
	if len(result.ToolCalls) != 2 || result.ToolCalls[0].Tool != "files.read" || result.ToolCalls[0].Status != "completed" ||
		result.ToolCalls[0].Arguments["path"] != "notes.md" ||
		result.ToolCalls[1].Tool != "text.replace_text" || result.ToolCalls[1].Status != "approval_pending" {
		t.Fatalf("document edit did not run one bound read before the editor: %#v", result.ToolCalls)
	}
	modelCalls := testListModelCalls(st, session.ID, dispatch.Run.ID)
	if countModelCalls(modelCalls, "workflow_step_1", documentWorkflowModelLane) != 1 ||
		countModelCalls(modelCalls, "workflow_step_2", documentWorkflowModelLane) != 0 ||
		hasModelCallOperation(modelCalls, "workflow_operation_selection", documentWorkflowModelLane) {
		t.Fatalf("document localization made an unnecessary model call: %#v", modelCalls)
	}
	if result.ToolCalls[0].CompletedAt == nil || modelCalls[0].StartedAt.Before(*result.ToolCalls[0].CompletedAt) {
		t.Fatalf("editor model call started before the fixed localization read completed: call=%#v models=%#v", result.ToolCalls[0], modelCalls)
	}
	if !hasAgentAuditType(st.ListAudit(session.ID), "workflow.direct_tool_invoked") {
		t.Fatalf("direct localization read was not audited: %#v", st.ListAudit(session.ID))
	}
}

func TestDocumentEditDirectLocalizationRejectsMismatchedWorkflowHint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Notes\nOriginal reflection"), 0o640); err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn", "Replace Original reflection in notes.md", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Context.WorkflowNodeID = "document_edit"

	result := runtime.runWorkflowDirectToolOnce(context.Background(), session.ID, dispatch.Run, dispatch.Context, dispatch.Tools, nil, nil)

	if result.FailureCode != workflowFailureDirectToolInvocationInvalid || len(result.ToolCalls) != 0 {
		t.Fatalf("mismatched direct localization hint was not rejected before execution: %#v", result)
	}
	if calls := toolCallsForRun(testListToolCalls(st, session.ID), dispatch.Run.ID); len(calls) != 0 {
		t.Fatalf("mismatched direct localization hint executed a tool: %#v", calls)
	}
	if calls := testListModelCalls(st, session.ID, dispatch.Run.ID); len(calls) != 0 {
		t.Fatalf("mismatched direct localization hint invoked a model: %#v", calls)
	}
}

func TestDocumentEditEditorBlocksAfterRepeatedPrematureFinal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# 心得与体会\n原始内容"), 0o640); err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn", "重新完善 notes.md 的心得与体会", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}

	result := runtime.runWorkflow(context.Background(), session.ID, dispatch.Run, `重新完善 notes.md 的心得与体会
MOCK_STEP_RESPONSE:{"type":"final","answer":"已经完善。"}`, dispatch.Profile, dispatch.Context, dispatch.Tools)

	storedRun, ok := testGetRun(st, dispatch.Run.ID)
	if !ok || storedRun.Workflow == nil {
		t.Fatal("document edit workflow state was not persisted")
	}
	locate := storedRun.Workflow.Nodes["document_locate_evidence"]
	editor := storedRun.Workflow.Nodes["document_edit"]
	if locate.Status != app.WorkflowNodeSucceeded || editor.Status != app.WorkflowNodeBlocked ||
		editor.LastAssessment == nil || editor.LastAssessment.ReasonCode != string(workflowFailureRequiredToolNotCalled) {
		t.Fatalf("premature final did not block the editor after direct localization: %#v", storedRun.Workflow)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Tool != "files.read" {
		t.Fatalf("direct localization should be the only executed tool: %#v", result.ToolCalls)
	}
	modelCalls := testListModelCalls(st, session.ID, dispatch.Run.ID)
	if countModelCalls(modelCalls, "workflow_step_1", documentWorkflowModelLane) != 1 ||
		countModelCalls(modelCalls, "workflow_step_2", documentWorkflowModelLane) != 1 ||
		countModelCalls(modelCalls, "workflow_step_3", documentWorkflowModelLane) != 0 {
		t.Fatalf("editor protocol retry was not bounded to two calls: %#v", modelCalls)
	}
	if result.FinalAnswer != "The workflow is blocked: required tool not called." {
		t.Fatalf("workflow did not surface the explicit editor protocol failure: %q", result.FinalAnswer)
	}
}
