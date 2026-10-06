package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

const continuationToken = "second-client-test-token"

func pendingWorkbenchFixture(t *testing.T, state string) (*Server, *store.FileStore, app.Session, execution.WorkbenchBinding, app.AgentRun, app.ToolCall) {
	t.Helper()
	s, st := admissionFileServer(t, t.TempDir())
	session := storetest.MustCreateSessionWithScope(t, st, "continuation", app.DefaultOwnerID, s.cfg.Workspaces.DefaultRoot, "webchat", false)
	if _, err := st.RegisterClient(t.Context(), app.Client{ID: "second", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Name: "second", TokenHash: hashSecret(continuationToken)}); err != nil {
		t.Fatal(err)
	}
	service, err := s.executionService()
	if err != nil {
		t.Fatal(err)
	}
	binding := execution.WorkbenchBinding{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, ClientID: "host-client", WorkspaceID: execution.Digest([]byte(session.WorkspaceRoot)), SessionID: session.ID, RequestID: testWorkbenchRequestID(), InputDigest: execution.Digest([]byte("input")), ContextDigest: execution.Digest([]byte("context")), ContextBefore: time.Now().UTC()}
	lease, _, err := service.BeginWorkbench(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: lease.RunID, SessionID: session.ID, State: state, StartedAt: binding.ContextBefore}
	if _, err := st.SaveRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	storetest.MustAddMessage(t, st, app.Message{ID: lease.InputMessageID, SessionID: session.ID, RunID: run.ID, Role: "user", Content: "original request", CreatedAt: run.StartedAt})
	message := storetest.MustAddMessage(t, st, app.Message{ID: "pending-message", SessionID: session.ID, RunID: run.ID, Role: "assistant", Content: "needs owner action"})
	call := app.ToolCall{ID: "pending-call", SessionID: session.ID, RunID: run.ID, Tool: "notify.ask_approval", Status: app.ToolCallStatusApprovalPending, ApprovalID: "pending-approval", Arguments: map[string]any{"summary": "continue"}, StartedAt: run.StartedAt}
	if _, err := st.SaveToolCall(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	storetest.MustSaveApproval(t, st, app.Approval{ID: call.ApprovalID, SessionID: session.ID, RunID: run.ID, ToolCallID: call.ID, Tool: call.Tool, Status: app.ApprovalStatusPending, Arguments: call.Arguments, CreatedAt: run.StartedAt})
	if err := lease.Finish(state, message.ID); err != nil {
		t.Fatal(err)
	}
	return s, st, session, binding, run, call
}

func closeWorkbenchFixture(t *testing.T, s *Server, binding execution.WorkbenchBinding, how string) {
	t.Helper()
	switch how {
	case "cancel":
		readAdmissionStatus(t, admissionHTTP(s, "POST", "/api/sessions/"+binding.SessionID+"/requests/"+binding.RequestID+"/cancel", continuationToken, `{}`))
	case "revoke":
		if w := admissionHTTP(s, "POST", "/api/clients/host-client/revoke", continuationToken, `{}`); w.Code != 200 {
			t.Fatalf("revoke %d %s", w.Code, w.Body.String())
		}
	case "expired":
		// Advance a persisted deadline across a service restart without sleeping.
		s.executions.Close()
		s.executions = nil
		path := filepath.Join(s.cfg.State.Path+".execution", "control.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var control map[string]any
		if err := json.Unmarshal(raw, &control); err != nil {
			t.Fatal(err)
		}
		for _, value := range control["workbench_fences"].(map[string]any) {
			value.(map[string]any)["deadline"] = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
		}
		raw, err = json.Marshal(control)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("invalid close fixture")
	}
}

func TestWorkbenchClosedBrowserRequestDoesNotTrapFreshMessages(t *testing.T) {
	for _, how := range []string{"cancel", "expired", "revoke"} {
		for _, content := range []string{"hello", "取消"} {
			t.Run(how+"/"+content, func(t *testing.T) {
				s, st, session, binding, run, call := pendingWorkbenchFixture(t, "browser_login_blocked")
				block := storetest.MustSaveBrowserLoginBlock(t, st, app.BrowserLoginBlock{SessionID: session.ID, RunID: run.ID, Status: app.BrowserLoginBlockStatusWaiting, SiteOrigin: "https://example.com", OriginalGoal: "original browser goal", ResumeTool: "browser.open", ResumeArgs: map[string]any{"url": "https://example.com"}})
				closeWorkbenchFixture(t, s, binding, how)
				body, _ := json.Marshal(map[string]string{"request_id": testWorkbenchRequestID(), "content": content})
				w := admissionHTTP(s, "POST", "/api/sessions/"+session.ID+"/messages", continuationToken, string(body))
				if w.Code != 201 {
					t.Fatalf("fresh message trapped: %d %s", w.Code, w.Body.String())
				}
				var result agent.Result
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Run.ID == run.ID || result.Run.ID == "" {
					t.Fatalf("resumed old request: %+v", result.Run)
				}
				messages, err := st.ListMessages(t.Context(), session.ID)
				if err != nil {
					t.Fatal(err)
				}
				retained := false
				for _, message := range messages {
					if message.Role == "user" && message.Content == content {
						retained = true
					}
				}
				if !retained {
					t.Fatal("fresh owner input was discarded during cleanup")
				}
				closed, found := storetest.MustGetBrowserLoginBlock(t, st, block.ID)
				if !found || closed.Status != app.BrowserLoginBlockStatusCanceled || closed.ResolvedAt == nil {
					t.Fatalf("active stale block: %+v", closed)
				}
				if _, found := storetest.MustFindActiveBrowserLoginBlock(t, st, session.ID); found {
					t.Fatal("block still active")
				}
				oldRun, found, err := st.GetRun(t.Context(), run.ID)
				if err != nil || !found || oldRun.State != "blocked" {
					t.Fatalf("stale run %+v %v", oldRun, err)
				}
				calls, err := st.ListToolCalls(t.Context(), session.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, current := range calls {
					if current.RunID == run.ID && (current.ID != call.ID || current.Status != call.Status) {
						t.Fatalf("old tool executed %+v", current)
					}
				}
				status := readAdmissionStatus(t, admissionHTTP(s, "GET", "/api/sessions/"+session.ID+"/requests/"+binding.RequestID, continuationToken, ""))
				if status.State != "unknown" {
					t.Fatalf("original fence escaped: %+v", status)
				}
			})
		}
	}
}

func TestWorkbenchClosedPendingApprovalCanBeRejectedWithoutExecuting(t *testing.T) {
	for _, how := range []string{"cancel", "expired", "revoke"} {
		t.Run(how, func(t *testing.T) {
			s, st, session, binding, _, call := pendingWorkbenchFixture(t, "approval_pending")
			closeWorkbenchFixture(t, s, binding, how)
			if w := admissionHTTP(s, "POST", "/api/approvals/"+call.ApprovalID+"/approve", continuationToken, `{}`); w.Code != 409 && w.Code != 410 {
				t.Fatalf("closed approval executed: %d %s", w.Code, w.Body.String())
			}
			if w := admissionHTTP(s, "POST", "/api/approvals/"+call.ApprovalID+"/reject", continuationToken, `{}`); w.Code != 200 {
				t.Fatalf("rejection trapped: %d %s", w.Code, w.Body.String())
			}
			rejected, found, err := st.GetToolCall(t.Context(), call.ID)
			if err != nil || !found || rejected.Status != app.ToolCallStatusRejected || rejected.Result != nil {
				t.Fatalf("rejection executed tool: %+v %v", rejected, err)
			}
			approval, found := storetest.MustGetApproval(t, st, call.ApprovalID)
			if !found || approval.Status != app.ApprovalStatusRejected {
				t.Fatal("approval still pending")
			}
			status := readAdmissionStatus(t, admissionHTTP(s, "GET", "/api/sessions/"+session.ID+"/requests/"+binding.RequestID, continuationToken, ""))
			if status.State != "unknown" {
				t.Fatalf("rejection escaped original fence: %+v", status)
			}
		})
	}
}

func TestWorkbenchApprovalDeliveryFailureRetainsDefinitiveFailure(t *testing.T) {
	s, st, session, binding, run, call := pendingWorkbenchFixture(t, "approval_pending")
	// Use a real persisted workflow plan so continuation has a deliverable result.
	template, err := s.runtime.HandleMessage(t.Context(), session.ID, "hello")
	if err != nil || template.Run.State != "completed" || template.Run.Workflow == nil {
		t.Fatalf("workflow fixture: %+v %v", template.Run, err)
	}
	run.Workflow, run.MessageContext = template.Run.Workflow, template.Run.MessageContext
	run.Workflow.ReturnRoute = app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "unavailable-endpoint"}
	run.MessageContext.ReturnRoute = run.Workflow.ReturnRoute
	if _, err := st.SaveRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveModelCall(t.Context(), app.ModelCall{ID: "original-model", SessionID: session.ID, RunID: run.ID, Operation: "workflow_step_1", Status: app.ModelCallStatusCompleted, StartedAt: run.StartedAt}); err != nil {
		t.Fatal(err)
	}
	w := admissionHTTP(s, "POST", "/api/approvals/"+call.ApprovalID+"/approve", continuationToken, `{}`)
	if w.Code != 502 {
		t.Fatalf("delivery response: %d %s", w.Code, w.Body.String())
	}
	status := readAdmissionStatus(t, admissionHTTP(s, "GET", "/api/sessions/"+session.ID+"/requests/"+binding.RequestID, continuationToken, ""))
	if status.State != "delivery_failed" || status.MessageID == "" || status.Result == nil {
		t.Fatalf("definitive delivery lost: %+v", status)
	}
	saved, found, err := st.GetToolCall(t.Context(), call.ID)
	if err != nil || !found || saved.Status != app.ToolCallStatusCompletedAfterApproval {
		t.Fatalf("approved tool %+v %v", saved, err)
	}
	if w := admissionHTTP(s, "POST", "/api/approvals/"+call.ApprovalID+"/approve", continuationToken, `{}`); w.Code != 400 {
		t.Fatalf("approval replay %d %s", w.Code, w.Body.String())
	}
}

func TestWorkbenchRejectionSerializesWithoutRequiringContinuationAuthority(t *testing.T) {
	s, st, session, binding, _, call := pendingWorkbenchFixture(t, "approval_pending")
	closeWorkbenchFixture(t, s, binding, "cancel")
	release := s.tryAdmitSessionMessage(session.ID)
	if release == nil {
		t.Fatal("fixture admission busy")
	}
	w := admissionHTTP(s, "POST", "/api/approvals/"+call.ApprovalID+"/reject", continuationToken, `{}`)
	release()
	if w.Code != 409 {
		t.Fatalf("rejection raced session worker: %d %s", w.Code, w.Body.String())
	}
	approval, found := storetest.MustGetApproval(t, st, call.ApprovalID)
	if !found || approval.Status != app.ApprovalStatusPending {
		t.Fatal("busy rejection mutated approval")
	}
	if w := admissionHTTP(s, "POST", "/api/approvals/"+call.ApprovalID+"/reject", continuationToken, `{}`); w.Code != 200 {
		t.Fatalf("idle terminal rejection requires authority: %d %s", w.Code, w.Body.String())
	}
}
