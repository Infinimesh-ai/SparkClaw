package agent

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"testing"
)

func TestApprovalObservesLivePolicyRevocation(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	// Callers (including workbench scopes and external entry points) hold Runtime
	// values. A copied value must see the same current policy after Settings edits.
	admitted := runtime
	cfg := runtime.tools.Config()
	cfg.Security.ApprovalRequiredTools = []string{"weather.lookup"}
	runtime.policy.Update(cfg)
	invocation, err := admitted.InvokeToolManually(t.Context(), "weather.lookup", map[string]any{"location": "杭州"}, session.ID)
	if err != nil || invocation.Approval == nil {
		t.Fatalf("runtime missed current approval requirement: %+v %v", invocation, err)
	}
	cfg.Security.ApprovalRequiredTools = nil
	cfg.Security.DeniedTools = []string{"weather.lookup"}
	runtime.policy.Update(cfg)
	approved, err := st.ResolveApproval(t.Context(), invocation.Approval.ID, app.ApprovalStatusApproved, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := admitted.ExecuteApprovedToolCall(t.Context(), approved)
	if err != nil || result.Status != app.ToolCallStatusFailedAfterApproval || result.ErrorCode != string(app.ToolErrorPolicyBlocked) {
		t.Fatalf("revoked tool executed after approval: %+v %v", result, err)
	}
}
