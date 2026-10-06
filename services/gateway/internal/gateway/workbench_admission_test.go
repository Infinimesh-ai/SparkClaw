package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func testWorkbenchRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	value[6] = (value[6] & 15) | 64
	value[8] = (value[8] & 63) | 128
	raw := hex.EncodeToString(value[:])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

func persistAdmittedStreamFixture(t *testing.T, ctx context.Context, st interface {
	store.RunRepository
	store.ConversationRepository
}, sessionID string, result agent.Result) agent.Result {
	t.Helper()
	lease := ctx.Value(admittedWorkbenchKey{}).(*r3execution.WorkbenchLease)
	var err error
	result.Run, err = st.SaveRun(ctx, app.AgentRun{ID: lease.RunID, SessionID: sessionID, State: "completed", StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	result.Message = storetest.MustAddMessage(t, st, app.Message{ID: app.NewID("m"), SessionID: sessionID, RunID: lease.RunID, Role: "assistant", Content: "fixture completed", CreatedAt: time.Now().UTC()})
	return result
}

const admissionToken = "workbench-admission-test-client-token"

func admissionFileServer(t *testing.T, root string) (*Server, *store.FileStore) {
	t.Helper()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired = true
	st, err := store.NewFileStore(cfg.State.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.GetClient(t.Context(), "host-client"); err != nil {
		t.Fatal(err)
	} else if !found {
		if _, err := st.RegisterClient(t.Context(), app.Client{ID: "host-client", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Name: "host", TokenHash: hashSecret(admissionToken)}); err != nil {
			t.Fatal(err)
		}
	}
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	s := New(cfg, st, tools, runtime)
	s.BindLifecycleContext(t.Context())
	t.Cleanup(func() {
		s.r3Mu.Lock()
		service := s.r3Executions
		s.r3Mu.Unlock()
		if service != nil {
			service.Close()
		}
	})
	return s, st
}

func admissionHTTP(s *Server, method, route, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, route, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func readAdmissionStatus(t *testing.T, w *httptest.ResponseRecorder) workbenchRequestStatus {
	t.Helper()
	var status workbenchRequestStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil {
		t.Fatalf("status response %d %s", w.Code, w.Body.String())
	}
	return status
}

func TestWorkbenchRequestPersistsOnceAcrossRestartAndGETDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	s, st := admissionFileServer(t, root)
	session, err := st.CreateSessionWithScope(t.Context(), "stable admission", app.DefaultOwnerID, root, "webchat", false)
	if err != nil {
		t.Fatal(err)
	}
	id := testWorkbenchRequestID()
	route := "/api/sessions/" + session.ID + "/messages"
	body := `{"request_id":"` + id + `","content":"hello","client_timezone":"Asia/Shanghai"}`
	first := admissionHTTP(s, "POST", route, admissionToken, body)
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	var result agent.Result
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	messages, err := st.ListMessages(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages=%d", len(messages))
	}
	second := readAdmissionStatus(t, admissionHTTP(s, "POST", route, admissionToken, body))
	if second.State != "completed" || second.RunID != result.Run.ID || second.Result == nil || second.MessageID != result.Message.ID {
		t.Fatalf("duplicate changed result: %+v", second)
	}
	changed := strings.Replace(body, "Asia/Shanghai", "UTC", 1)
	if w := admissionHTTP(s, "POST", route, admissionToken, changed); w.Code != 409 {
		t.Fatalf("accepted changed timezone %d %s", w.Code, w.Body.String())
	}
	changed = strings.TrimSuffix(body, "}") + `,"attachments":[{"artifact_id":"changed"}]}`
	if w := admissionHTTP(s, "POST", route, admissionToken, changed); w.Code != 409 {
		t.Fatalf("accepted changed attachment %d %s", w.Code, w.Body.String())
	}
	s.r3Executions.Close()
	s2, st2 := admissionFileServer(t, root)
	ledger := filepath.Join(root, "workbench-state.json.r3", "control.json")
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	statusRoute := "/api/sessions/" + session.ID + "/requests/" + id
	recovered := readAdmissionStatus(t, admissionHTTP(s2, "GET", statusRoute, admissionToken, ""))
	if recovered.RunID != result.Run.ID || recovered.Result == nil || recovered.State != "completed" {
		t.Fatalf("recovered=%+v", recovered)
	}
	if s2.r3Executions != nil {
		t.Fatal("GET started a writable execution service")
	}
	list := admissionHTTP(s2, "GET", "/api/sessions/"+session.ID+"/requests", admissionToken, "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), id) {
		t.Fatalf("list lost original ID: %d %s", list.Code, list.Body.String())
	}
	after, err := os.ReadFile(ledger)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("GET mutated ledger")
	}
	retry := readAdmissionStatus(t, admissionHTTP(s2, "POST", route, admissionToken, body))
	if retry.RunID != result.Run.ID {
		t.Fatal("restart replayed request")
	}
	messages, err = st2.ListMessages(t.Context(), session.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("replay persisted messages: %d %v", len(messages), err)
	}
	if w := admissionHTTP(s2, "POST", route, admissionToken, `{"content":"missing ID"}`); w.Code != 400 {
		t.Fatalf("missing request ID %d", w.Code)
	}
}

func TestWorkbenchDraftClearIsCASAfterAdmissionAndDuplicatesPreserveNewDraft(t *testing.T) {
	for _, newer := range []bool{false, true} {
		t.Run(fmt.Sprint("newer=", newer), func(t *testing.T) {
			s, st := admissionFileServer(t, t.TempDir())
			session := storetest.MustCreateSession(t, st, "draft admission")
			draft, err := st.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, session.ID, app.WorkbenchDraft{Content: "sent draft"})
			if err != nil {
				t.Fatal(err)
			}
			if newer {
				if _, err := st.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, session.ID, app.WorkbenchDraft{Content: "newer draft", Revision: draft.Revision}); err != nil {
					t.Fatal(err)
				}
			}
			id := testWorkbenchRequestID()
			route := "/api/sessions/" + session.ID + "/messages/stream"
			body := fmt.Sprintf(`{"request_id":%q,"content":"hello","draft_revision":%d}`, id, draft.Revision)
			response := admissionHTTP(s, "POST", route, admissionToken, body)
			if response.Code != 201 || !strings.Contains(response.Body.String(), "event: message.stream.started") {
				t.Fatalf("stream %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), `"draft_revision":2`) == newer {
				t.Fatalf("stream reported incorrect draft clear: %s", response.Body.String())
			}
			retained, err := st.GetWorkbenchDraft(t.Context(), app.DefaultOwnerID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if newer {
				want = "newer draft"
			}
			if retained.Content != want || retained.Revision != 2 {
				t.Fatalf("draft=%+v", retained)
			}
			if _, err := st.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, session.ID, app.WorkbenchDraft{Content: "typed after acceptance", Revision: retained.Revision}); err != nil {
				t.Fatal(err)
			}
			replay := readAdmissionStatus(t, admissionHTTP(s, "POST", route, admissionToken, body))
			if (replay.DraftRevision == nil) != newer {
				t.Fatalf("replay draft revision=%v", replay.DraftRevision)
			}
			retained, err = st.GetWorkbenchDraft(t.Context(), app.DefaultOwnerID, session.ID)
			if err != nil || retained.Content != "typed after acceptance" {
				t.Fatalf("duplicate cleared newer draft: %+v %v", retained, err)
			}
		})
	}
}

func TestWorkbenchCancelAndClientRevocationCancelAdmittedRunWithoutReplay(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint("revoke=", revoke), func(t *testing.T) {
			s, st := credentialServer(t)
			registerCredential(t, st, "client-a", "client-a-test-only-token")
			registerCredential(t, st, "client-b", "client-b-test-only-token")
			session, err := st.CreateSessionWithScope(t.Context(), "cancel", "owner", s.cfg.Workspaces.DefaultRoot, "webchat", false)
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan context.Context, 1)
			var calls atomic.Int32
			s.streamMessage = func(ctx context.Context, _ string, _ string, _ []agent.MessageAttachment, _ app.MessageIngressContext, _ agent.StreamHandler) (agent.Result, error) {
				calls.Add(1)
				started <- ctx
				<-ctx.Done()
				return agent.Result{}, ctx.Err()
			}
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()
			id := testWorkbenchRequestID()
			route := "/api/sessions/" + session.ID + "/messages/stream"
			body := `{"request_id":"` + id + `","content":"hello"}`
			response := credentialRequest(t, "POST", ts.URL+route, "client-a-test-only-token", "", body)
			defer response.Body.Close()
			var execution context.Context
			select {
			case execution = <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("worker not started")
			}
			if revoke {
				w := admissionHTTP(s, "POST", "/api/clients/client-a/revoke", "client-b-test-only-token", `{}`)
				if w.Code != 200 {
					t.Fatalf("revoke %d %s", w.Code, w.Body.String())
				}
			} else {
				status := readAdmissionStatus(t, admissionHTTP(s, "POST", "/api/sessions/"+session.ID+"/requests/"+id+"/cancel", "client-b-test-only-token", `{}`))
				if status.State != "unknown" {
					t.Fatalf("cancel state=%s", status.State)
				}
			}
			select {
			case <-execution.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("admitted run not canceled")
			}
			_, _ = io.ReadAll(response.Body)
			s.streamWG.Wait()
			replay := admissionHTTP(s, "POST", route, "client-a-test-only-token", body)
			if revoke {
				if replay.Code != 401 {
					t.Fatalf("revoked replay=%d", replay.Code)
				}
			} else {
				if readAdmissionStatus(t, replay).State != "unknown" {
					t.Fatal("canceled request became executable")
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("worker ran %d times", calls.Load())
			}
			// Shared owner workbench views can inspect and cancel across devices;
			// submission replay remains bound to the original caller.
			if status := readAdmissionStatus(t, admissionHTTP(s, "GET", "/api/sessions/"+session.ID+"/requests/"+id, "client-b-test-only-token", "")); status.State != "unknown" {
				t.Fatalf("same-owner status=%+v", status)
			}
			if w := admissionHTTP(s, "GET", "/api/sessions/"+session.ID+"/requests", "client-b-test-only-token", ""); w.Code != 200 || !strings.Contains(w.Body.String(), id) {
				t.Fatalf("same-owner list %d %s", w.Code, w.Body.String())
			}
			if w := admissionHTTP(s, "POST", route, "client-b-test-only-token", body); w.Code != 409 {
				t.Fatalf("other client replay %d %s", w.Code, w.Body.String())
			}
			if _, err := st.RegisterClient(t.Context(), app.Client{ID: "outsider", OwnerID: "another-owner", ActorID: "another-owner", Name: "outsider", TokenHash: hashSecret("outsider-test-token")}); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"/requests", "/requests/" + id} {
				if w := admissionHTTP(s, "GET", "/api/sessions/"+session.ID+suffix, "outsider-test-token", ""); w.Code != 404 {
					t.Fatalf("cross-owner access %d %s", w.Code, w.Body.String())
				}
			}
			s.r3Executions.Close()
		})
	}
}

func TestWorkbenchDoesNotDeclareUnpersistedResultComplete(t *testing.T) {
	s, st := admissionFileServer(t, t.TempDir())
	session := storetest.MustCreateSession(t, st, "missing durable result")
	s.streamMessage = func(ctx context.Context, sessionID, _ string, _ []agent.MessageAttachment, _ app.MessageIngressContext, _ agent.StreamHandler) (agent.Result, error) {
		lease := ctx.Value(admittedWorkbenchKey{}).(*r3execution.WorkbenchLease)
		return agent.Result{Run: app.AgentRun{ID: lease.RunID, SessionID: sessionID, State: "completed"}, Message: app.Message{ID: "fake-success", SessionID: sessionID}}, nil
	}
	id := testWorkbenchRequestID()
	route := "/api/sessions/" + session.ID + "/messages/stream"
	body := `{"request_id":"` + id + `","content":"hello"}`
	first := admissionHTTP(s, "POST", route, admissionToken, body)
	if !strings.Contains(first.Body.String(), "event: error") {
		t.Fatalf("reported unpersisted completion %s", first.Body.String())
	}
	status := readAdmissionStatus(t, admissionHTTP(s, "POST", route, admissionToken, body))
	if status.State != "unknown" || status.Result != nil {
		t.Fatalf("unpersisted result status=%+v", status)
	}
}

type failedStartedWriter struct{ *httptest.ResponseRecorder }

func (failedStartedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWorkbenchLostStartedResponseRetainsOwnerInputBeforeClearingDraft(t *testing.T) {
	s, st := admissionFileServer(t, t.TempDir())
	session := storetest.MustCreateSession(t, st, "lost started response")
	draft, err := st.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, session.ID, app.WorkbenchDraft{Content: "preserve exact submitted content"})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.streamMessage = func(context.Context, string, string, []agent.MessageAttachment, app.MessageIngressContext, agent.StreamHandler) (agent.Result, error) {
		calls.Add(1)
		return agent.Result{}, nil
	}
	id := testWorkbenchRequestID()
	route := "/api/sessions/" + session.ID + "/messages/stream"
	body := fmt.Sprintf(`{"request_id":%q,"content":%q,"draft_revision":%d}`, id, draft.Content, draft.Revision)
	r := httptest.NewRequest("POST", route, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+admissionToken)
	r.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(failedStartedWriter{httptest.NewRecorder()}, r)
	if calls.Load() != 0 {
		t.Fatal("worker started after lost acceptance")
	}
	messages, err := st.ListMessages(t.Context(), session.ID)
	if err != nil || len(messages) != 1 || messages[0].Content != draft.Content {
		t.Fatalf("lost submitted owner content: %+v %v", messages, err)
	}
	current, err := st.GetWorkbenchDraft(t.Context(), app.DefaultOwnerID, session.ID)
	if err != nil || current.Content != "" {
		t.Fatalf("draft=%+v %v", current, err)
	}
	status := readAdmissionStatus(t, admissionHTTP(s, "POST", route, admissionToken, body))
	if status.State != "unknown" || status.InputMessageID != messages[0].ID || status.DraftRevision == nil || status.SubmittedDraftRevision == nil {
		t.Fatalf("lost original recovery status %+v", status)
	}
	if calls.Load() != 0 {
		t.Fatal("replayed lost acceptance")
	}
}

func TestWorkbenchCanceledOrRevokedPendingApprovalCannotExecute(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint("revoke=", revoke), func(t *testing.T) {
			s, st := admissionFileServer(t, t.TempDir())
			session := storetest.MustCreateSessionWithScope(t, st, "fenced approval", app.DefaultOwnerID, s.cfg.Workspaces.DefaultRoot, "webchat", false)
			if _, err := st.RegisterClient(t.Context(), app.Client{ID: "second", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Name: "second", TokenHash: hashSecret("second-client-test-token")}); err != nil {
				t.Fatal(err)
			}
			service, err := s.r3ExecutionService()
			if err != nil {
				t.Fatal(err)
			}
			binding := r3execution.WorkbenchBinding{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, ClientID: "host-client", WorkspaceID: r3execution.Digest([]byte(session.WorkspaceRoot)), SessionID: session.ID, RequestID: testWorkbenchRequestID(), InputDigest: r3execution.Digest([]byte("input")), ContextDigest: r3execution.Digest([]byte("context")), ContextBefore: time.Now().UTC()}
			lease, _, err := service.BeginWorkbench(t.Context(), binding)
			if err != nil {
				t.Fatal(err)
			}
			run := app.AgentRun{ID: lease.RunID, SessionID: session.ID, State: "approval_pending", StartedAt: binding.ContextBefore}
			if _, err := st.SaveRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			message := storetest.MustAddMessage(t, st, app.Message{ID: "pending-message", SessionID: session.ID, RunID: run.ID, Role: "assistant", Content: "needs approval"})
			call := app.ToolCall{ID: "pending-call", SessionID: session.ID, RunID: run.ID, Tool: "notify.ask_approval", Status: app.ToolCallStatusApprovalPending, ApprovalID: "pending-approval", Arguments: map[string]any{"summary": "continue"}, StartedAt: time.Now().UTC()}
			if _, err := st.SaveToolCall(t.Context(), call); err != nil {
				t.Fatal(err)
			}
			storetest.MustSaveApproval(t, st, app.Approval{ID: call.ApprovalID, SessionID: session.ID, RunID: run.ID, ToolCallID: call.ID, Tool: call.Tool, Status: app.ApprovalStatusPending, Arguments: call.Arguments, CreatedAt: time.Now().UTC()})
			if err := lease.Finish("approval_pending", message.ID); err != nil {
				t.Fatal(err)
			}
			if revoke {
				if w := admissionHTTP(s, "POST", "/api/clients/host-client/revoke", "second-client-test-token", `{}`); w.Code != 200 {
					t.Fatalf("revoke %d %s", w.Code, w.Body.String())
				}
			} else {
				if status := readAdmissionStatus(t, admissionHTTP(s, "POST", "/api/sessions/"+session.ID+"/requests/"+binding.RequestID+"/cancel", "second-client-test-token", `{}`)); status.State != "unknown" {
					t.Fatalf("cancel %+v", status)
				}
			}
			w := admissionHTTP(s, "POST", "/api/approvals/"+call.ApprovalID+"/approve", "second-client-test-token", `{}`)
			if w.Code != 409 {
				t.Fatalf("fenced approval %d %s", w.Code, w.Body.String())
			}
			stored, found, err := st.GetToolCall(t.Context(), call.ID)
			if err != nil || !found || stored.Status != app.ToolCallStatusApprovalPending {
				t.Fatalf("tool executed after fence %+v %v", stored, err)
			}
			approval, found := storetest.MustGetApproval(t, st, call.ApprovalID)
			if !found || approval.Status != app.ApprovalStatusPending {
				t.Fatal("fenced approval consumed owner decision")
			}
		})
	}
}
