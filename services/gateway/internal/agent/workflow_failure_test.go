package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type workflowSessionFailureStore struct {
	store.Store
	err error
}

func (s *workflowSessionFailureStore) GetSession(context.Context, string) (app.Session, bool, error) {
	return app.Session{}, false, s.err
}

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
			session := storetest.MustCreateSession(t, st, "failure projection")
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
			workflowResult := mustWorkflowResultForRun(t, runtime, run, route, app.ReturnRoute{Mode: app.ReturnToSource}, run.Summary, execution.FailureCode)
			assistant, err := runtime.persistWorkflowAssistantMessage(t.Context(), run, workflowResult, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}

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
	session := storetest.MustCreateSession(t, st, "setup failure projection")
	route := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched}
	run := app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			Route: route, ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource}, Status: app.WorkflowStatusRunning,
			Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserInternetSearch, ProfileRevision: 2},
		},
	}
	runtime := Runtime{store: st}
	result, err := runtime.blockPersistedWorkflowResume(t.Context(), run, "search", errors.New(sentinel))
	if err != nil {
		t.Fatal(err)
	}

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

func TestWorkflowSetupFailureDoesNotSuppressSessionStoreFailure(t *testing.T) {
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "setup failure store error")
	rawCause := errors.New("session backend unavailable")
	failing := &workflowSessionFailureStore{Store: base, err: &store.StoreError{
		Code: store.StoreErrorUnavailable, Operation: store.OperationSessionGet, Err: rawCause,
	}}
	route := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched}
	run := app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			Route: route, ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource}, Status: app.WorkflowStatusRunning,
			Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserInternetSearch, ProfileRevision: 2},
		},
	}
	runtime := Runtime{store: failing}
	result, err := runtime.blockPersistedWorkflowResume(t.Context(), run, "search", errors.New("setup failed"))
	if !errors.Is(err, rawCause) || result.Run.ID != "" || result.WorkflowResult != nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
