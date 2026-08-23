package agent

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustSemanticRoutingContext(t testing.TB, runtime Runtime, sessionID, runID, content string, resources []app.MessagePart) string {
	t.Helper()
	value, err := runtime.semanticRoutingContext(t.Context(), sessionID, runID, content, resources)
	if err != nil {
		t.Fatalf("resolve semantic routing context: %v", err)
	}
	return value
}

func mustResolveDocumentContext(t testing.TB, runtime Runtime, sessionID, runID, content string, resources []app.MessagePart) documentContextResolution {
	t.Helper()
	value, err := runtime.resolveDocumentContext(t.Context(), sessionID, runID, content, resources)
	if err != nil {
		t.Fatalf("resolve document context: %v", err)
	}
	return value
}

func mustWorkflowResultForRun(t testing.TB, runtime Runtime, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string, failureCodes ...workflowFailureCode) *app.WorkflowResult {
	t.Helper()
	result, err := runtime.workflowResultForRun(t.Context(), run, route, returnRoute, summary, failureCodes...)
	if err != nil {
		t.Fatalf("build workflow result: %v", err)
	}
	return result
}

func mustWorkflowResultForDispatchFailure(t testing.TB, runtime Runtime, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	t.Helper()
	result, err := runtime.workflowResultForDispatchFailure(t.Context(), run, route, returnRoute, summary)
	if err != nil {
		t.Fatalf("build dispatch failure result: %v", err)
	}
	return result
}

func mustWorkflowResultForTerminalRoute(t testing.TB, runtime Runtime, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	t.Helper()
	result, err := runtime.workflowResultForTerminalRoute(t.Context(), run, route, returnRoute, summary)
	if err != nil {
		t.Fatalf("build terminal route result: %v", err)
	}
	return result
}

func mustWorkflowResultForUnmatched(t testing.TB, runtime Runtime, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	t.Helper()
	result, err := runtime.workflowResultForUnmatched(t.Context(), run, route, returnRoute, summary)
	if err != nil {
		t.Fatalf("build unmatched result: %v", err)
	}
	return result
}

func mustWorkflowOutputResourceRef(t testing.TB, runtime Runtime, sessionID string, ref app.ResourceRef) (app.ResourceRef, bool) {
	t.Helper()
	resolved, found, err := runtime.workflowOutputResourceRef(t.Context(), sessionID, ref)
	if err != nil {
		t.Fatalf("resolve workflow output: %v", err)
	}
	return resolved, found
}

func mustCompleteTerminalRoute(t testing.TB, runtime Runtime, ctx context.Context, run app.AgentRun, goal string, returnRoute app.ReturnRoute, route app.RouteDecision) Result {
	t.Helper()
	result, err := runtime.completeTerminalRoute(ctx, run, goal, returnRoute, route)
	if err != nil {
		t.Fatalf("complete terminal route: %v", err)
	}
	return result
}

func mustResultForExistingRun(t testing.TB, runtime Runtime, run app.AgentRun) Result {
	t.Helper()
	result, err := runtime.resultForExistingRun(t.Context(), run)
	if err != nil {
		t.Fatalf("build existing run result: %v", err)
	}
	return result
}
