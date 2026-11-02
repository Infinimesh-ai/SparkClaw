package agent

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"testing"
)

func TestCompletionApprovalPublishesOnlyValidatedInputs(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	args := map[string]any{"draft": "frozen input"}
	call := app.ToolCall{ID: app.NewID("call"), SessionID: session.ID, RunID: app.NewID("run"), Tool: "notify.ask_approval", Status: app.ToolCallStatusApprovalPending, Arguments: args, ApprovalID: app.NewID("approval")}
	if _, err := st.SaveToolCall(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	pending, err := st.SaveApproval(t.Context(), app.Approval{ID: call.ApprovalID, SessionID: call.SessionID, RunID: call.RunID, ToolCallID: call.ID, Tool: call.Tool, Arguments: args, ArgumentsImmutable: true, InputForm: &app.ApprovalInputForm{SchemaVersion: 1, Entries: []app.ApprovalInputEntry{{ID: "e1", Label: "Medicine", Fields: []app.ApprovalInputField{{Name: "expiry", Label: "Expiry", Type: app.ApprovalInputDateOrNone, Required: true}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := st.ResolveApprovalWithInputs(t.Context(), pending.ID, app.ApprovalStatusApproved, "", app.ApprovalInputSubmission{Digest: pending.InputFormDigest, SchemaVersion: 1, Inputs: app.ApprovalInputValues{"e1": {"expiry": "2027-05-31"}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.ExecuteApprovedToolCall(t.Context(), approved)
	if err != nil {
		t.Fatal(err)
	}
	values := result.Result.(map[string]any)["input_values"].(app.ApprovalInputValues)
	if result.Status != app.ToolCallStatusCompletedAfterApproval || values["e1"]["expiry"] != "2027-05-31" || result.Arguments["draft"] != "frozen input" {
		t.Fatal(result)
	}
	if _, err := runtime.ExecuteApprovedToolCall(t.Context(), approved); err == nil {
		t.Fatal("duplicate continuation executed")
	}
}
