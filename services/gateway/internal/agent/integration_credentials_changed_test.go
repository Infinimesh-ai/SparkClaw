package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestCredentialChangeTerminallyStopsApprovalPendingRun(t *testing.T) {
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot = t.TempDir()
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	session := storetest.MustCreateSession(t, st, "integration switch")
	run := app.AgentRun{
		ID: "run_integration_switch", SessionID: session.ID, State: "approval_pending", StartedAt: time.Now().UTC(),
		MessageContext: &app.MessageRunContext{Route: app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion}},
	}
	if _, err := st.SaveRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	call := app.ToolCall{
		ID: "call_integration_switch", SessionID: session.ID, RunID: run.ID, Tool: "notify.ask_approval",
		Status: app.ToolCallStatusApprovalPending, ApprovalID: "approval_integration_switch", StartedAt: time.Now().UTC(),
	}
	if _, err := st.SaveToolCall(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	storetest.MustSaveApproval(t, st, app.Approval{
		ID: call.ApprovalID, Source: app.ApprovalSourceTool, SessionID: session.ID, RunID: run.ID,
		ToolCallID: call.ID, Tool: call.Tool, Status: app.ApprovalStatusPending, CreatedAt: time.Now().UTC(),
	})
	cause := &app.CodedToolError{
		Code: app.ToolErrorInfoCredentialsChanged, Err: errors.New("Info credentials changed; the task was stopped"),
	}

	result, err := runtime.completeIntegrationCredentialChangedRun(t.Context(), run, cause)
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "cancelled" || result.Run.CompletedAt == nil || result.Message.Content != cause.Error() {
		t.Fatalf("credential-changed result=%#v", result)
	}
	if result.WorkflowResult == nil || result.WorkflowResult.Error == nil || result.WorkflowResult.Error.Code != string(app.ToolErrorInfoCredentialsChanged) {
		t.Fatalf("workflow error did not retain typed credential cause: %#v", result.WorkflowResult)
	}
	resolved, ok := storetest.MustGetApproval(t, st, call.ApprovalID)
	if !ok || resolved.Status != app.ApprovalStatusResolvedElsewhere {
		t.Fatalf("pending approval remained actionable: %#v ok=%v", resolved, ok)
	}
	failedCall, ok, err := st.GetToolCall(t.Context(), call.ID)
	if err != nil || !ok || failedCall.Status != app.ToolCallStatusFailed || failedCall.ErrorCode != string(app.ToolErrorInfoCredentialsChanged) {
		t.Fatalf("approval tool call was not stopped: %#v ok=%v err=%v", failedCall, ok, err)
	}
}
