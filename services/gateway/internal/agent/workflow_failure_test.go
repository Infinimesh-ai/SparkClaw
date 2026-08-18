package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestWorkflowFailureProjectionKeepsDiagnosticsOutOfPublicResults(t *testing.T) {
	codes := []workflowFailureCode{
		workflowFailureEvidenceUnavailable,
		workflowFailureToolOutsideActiveScope,
		workflowFailureSemanticPreflight,
		workflowFailureSemanticOutputInvalid,
		workflowFailurePromptFixedOversized,
		workflowFailureObservationReadLimit,
	}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			const sentinel = "secret=issue17-token path=/srv/private/owner-data.txt"
			st := store.NewMemoryStore()
			session := st.CreateSession("failure projection")
			nodeID := app.WorkflowNodeID("active")
			route := app.RouteDecision{
				SchemaVersion: app.RouteDecisionSchemaVersion,
				Status:        app.RouteMatched,
				CapabilityPath: []app.CapabilityID{
					app.CapabilityBrowserInternetSearch,
				},
			}
			run := app.AgentRun{
				ID: app.NewID("run"), SessionID: session.ID, State: "blocked", StartedAt: time.Now().UTC(),
				Workflow: &app.WorkflowState{
					Route: route, Status: app.WorkflowStatusBlocked, ActiveNodeIDs: []app.WorkflowNodeID{nodeID},
					Plan:  app.WorkflowPlan{ProfileID: app.WorkflowBrowserInternetSearch, ProfileRevision: 2},
					Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{nodeID: {Status: app.WorkflowNodeBlocked}},
				},
			}
			execution := workflowExecutionResult{}
			execution.fail(code, errors.New(sentinel))
			runtime := Runtime{store: st}
			runtime.auditWorkflowExecutionFailure(session.ID, run.ID, "workflow.test_failure", execution.FailureCode, execution.FailureDiagnostic, nil)
			execution = execution.withPublicFailureProjection()
			run.Summary = publicWorkflowFailureMessage(execution.FailureCode)
			st.SaveRun(run)
			workflowResult := runtime.workflowResultForRun(run, route, app.ReturnRoute{Mode: app.ReturnToSource}, run.Summary, execution.FailureCode)
			assistant := runtime.persistWorkflowAssistantMessage(run, workflowResult, time.Now().UTC())

			publicValues := []any{execution.FinalAnswer, run.Summary, assistant, workflowResult}
			for _, value := range publicValues {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), sentinel) {
					t.Fatalf("failure diagnostic leaked through public projection for %q: %s", code, encoded)
				}
			}
			if workflowResult == nil || workflowResult.Error == nil || workflowResult.Error.Code != string(code) || workflowResult.Error.Message != run.Summary {
				t.Fatalf("workflow result did not preserve the stable public failure contract: %#v", workflowResult)
			}
			auditJSON, err := json.Marshal(st.ListAudit(session.ID))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(auditJSON), sentinel) {
				t.Fatalf("internal diagnostic was not retained in audit for %q: %s", code, auditJSON)
			}
		})
	}
}

func TestWorkflowSetupFailureUsesSafePublicProjection(t *testing.T) {
	const sentinel = "credential=setup-secret path=/home/owner/.ssh/id_ed25519"
	st := store.NewMemoryStore()
	session := st.CreateSession("setup failure projection")
	route := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched}
	run := app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			Route: route, ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource}, Status: app.WorkflowStatusRunning,
			Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserInternetSearch, ProfileRevision: 2},
		},
	}
	runtime := Runtime{store: st}
	result := runtime.blockPersistedWorkflowResume(t.Context(), run, "search", errors.New(sentinel))

	publicValues := []any{result.Run.Summary, result.Message, result.WorkflowResult}
	for _, value := range publicValues {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), sentinel) {
			t.Fatalf("setup diagnostic leaked through public projection: %s", encoded)
		}
	}
	if result.WorkflowResult == nil || result.WorkflowResult.Error == nil || result.WorkflowResult.Error.Code != string(workflowFailureSetup) {
		t.Fatalf("setup failure did not expose its stable reason code: %#v", result.WorkflowResult)
	}
	auditJSON, err := json.Marshal(st.ListAudit(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auditJSON), sentinel) {
		t.Fatalf("setup diagnostic was not retained in audit: %s", auditJSON)
	}
}
