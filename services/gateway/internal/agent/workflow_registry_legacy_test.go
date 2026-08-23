package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// TestLegacyWrapperPreservesAlwaysDirectSupportRequirements guards against
// the legacyWorkflowProfile wrapper dropping optional marker interfaces: a
// wrapped always-direct profile must yield the same frozen support
// requirements as the unwrapped profile, or legacy revisions silently gain
// observation.read requirements the original plans never had.
func TestLegacyWrapperPreservesAlwaysDirectSupportRequirements(t *testing.T) {
	base := browserWeatherProfile{}
	wrapped := legacyWorkflowProfile{workflowProfile: base, revision: 1}
	if !workflowProfileAlwaysDirect(base) {
		t.Fatal("browserWeatherProfile lost its always-direct marker; pick another always-direct profile for this test")
	}
	if workflowProfileAlwaysDirect(base) != workflowProfileAlwaysDirect(wrapped) {
		t.Fatal("legacy wrapper diverges from wrapped profile on the always-direct marker")
	}
	plan := app.WorkflowPlan{Nodes: []app.WorkflowNode{{
		ID:   "check",
		Goal: app.NodeGoal{Completion: app.CompletionEvidence},
	}}}
	wrappedPlan := plan
	wrappedPlan.Nodes = append([]app.WorkflowNode(nil), plan.Nodes...)
	freezeWorkflowSupportRequirements(wrapped, &wrappedPlan)
	if len(wrappedPlan.Nodes[0].InitialScope.SupportRequirements) != 0 {
		t.Fatal("wrapped always-direct profile received injected support requirements")
	}
}
