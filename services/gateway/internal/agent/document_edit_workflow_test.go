package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentEditWorkflowReadsApprovesResumesAndReturnsTextCopy(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "notes.md")
	if err := os.WriteFile(inputPath, []byte("# Notes\nOriginal reflection"), 0o640); err != nil {
		t.Fatal(err)
	}
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	defer closeRuntime()
	session = st.CreateSession("document edit without an explicit session workspace")

	goal := "Replace Original reflection in notes.md"
	started := time.Now().UTC()
	user := st.AddMessage(app.Message{SessionID: session.ID, Role: "user", Content: goal, CreatedAt: started})
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

	readCall, approval, _ := runtime.runToolPlan(context.Background(), session.ID, dispatch.Run.ID, toolPlan{
		Name: "files.read", Args: map[string]any{"path": "notes.md"}, WorkflowID: app.WorkflowDocumentEdit,
		WorkflowNodeID: "document_edit", ScopeRevision: 1, Capability: app.ToolCapabilityDocumentRead,
	})
	if approval != nil || !toolCallCompleted(readCall) {
		t.Fatalf("document read did not complete before mutation: call=%#v approval=%#v", readCall, approval)
	}
	definition, _ := runtime.tools.Definition(readCall.Tool)
	outcome, err := adaptWorkflowOutcome(definition, readCall)
	if err != nil {
		t.Fatal(err)
	}
	storedRun, _ := st.GetRun(dispatch.Run.ID)
	assessment := dispatch.Profile.Assess(storedRun.Workflow, outcome)
	changed, err := applyWorkflowOutcome(&storedRun, outcome, assessment)
	if err != nil || !changed {
		t.Fatalf("structured read did not activate editor: changed=%t assessment=%#v err=%v", changed, assessment, err)
	}
	st.SaveRun(storedRun)
	hint := dispatch.Profile.Hint(storedRun.Workflow)
	editTools, err := runtime.materializeActiveWorkflowTools(context.Background(), storedRun, runtime.workflowActorRef(session.ID), &hint)
	if err != nil {
		t.Fatal(err)
	}
	if len(editTools) != 1 || editTools[0].Name != "text.replace_text" {
		t.Fatalf("text edit stage exposed the wrong editor: %#v", visibleToolNames(editTools))
	}

	editCall, editApproval, _ := runtime.runToolPlan(context.Background(), session.ID, storedRun.ID, toolPlan{
		Name: "text.replace_text",
		Args: map[string]any{
			"path": "model-invented-input.md", "output_path": "model-invented-output.md", "expected_replacements": 1,
			"replacements": []any{map[string]any{"find": "Original reflection", "replace": "Improved reflection"}},
		},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 2, Capability: app.ToolCapabilityDocumentEdit,
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
	storedRun, _ = st.GetRun(storedRun.ID)
	storedRun.State = "approval_pending"
	st.SaveRun(storedRun)
	st.SaveModelCall(app.ModelCall{ID: app.NewID("mcall"), SessionID: session.ID, RunID: storedRun.ID, Operation: "react_step_2", Status: "completed", StartedAt: time.Now().UTC()})

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

	original, err := os.ReadFile(inputPath)
	if err != nil || string(original) != "# Notes\nOriginal reflection" {
		t.Fatalf("original text changed: %q err=%v", original, err)
	}
	updated, err := os.ReadFile(filepath.Join(root, "notes-sparkclaw-edit.md"))
	if err != nil || string(updated) != "# Notes\nImproved reflection" {
		t.Fatalf("output text mismatch: %q err=%v", updated, err)
	}
}
