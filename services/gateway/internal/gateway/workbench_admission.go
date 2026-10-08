package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type admittedWorkbenchKey struct{}
type workbenchAdmission struct {
	lease         *execution.WorkbenchLease
	binding       execution.WorkbenchBinding
	closeOnce     sync.Once
	release       func()
	snapshot      agent.WorkbenchContext
	continuations []*workbenchAdmission
}

func (a *workbenchAdmission) context() context.Context {
	return agent.WithWorkbenchContext(context.WithValue(a.lease.Context, admittedWorkbenchKey{}, a.lease), a.snapshot)
}
func (a *workbenchAdmission) close() {
	a.closeOnce.Do(func() {
		for _, child := range a.continuations {
			child.close()
		}
		_ = a.lease.Finish("unknown", "")
		a.release()
	})
}

type workbenchRequestStatus struct {
	execution.WorkbenchStatus
	Result *agent.Result `json:"result,omitempty"`
}

func workbenchBinding(r *http.Request, session app.Session, requestID string) execution.WorkbenchBinding {
	principal := principalForRequest(r)
	return execution.WorkbenchBinding{OwnerID: principal.OwnerID, ActorID: principal.ActorID, ClientID: principal.ClientID, WorkspaceID: execution.Digest([]byte(filepath.Clean(session.WorkspaceRoot))), SessionID: session.ID, RequestID: requestID}
}

func (s *Server) admittedWorkbenchResult(ctx context.Context, binding execution.WorkbenchBinding, status execution.WorkbenchStatus) (workbenchRequestStatus, error) {
	out := workbenchRequestStatus{WorkbenchStatus: status}
	if status.State == "running" {
		return out, nil
	}
	result, found, err := s.runtime.LookupRunResult(ctx, binding.SessionID, status.RunID)
	if err != nil {
		return out, err
	}
	if !found || !persistedWorkbenchResultValid(result) {
		if status.State == "unknown" || status.State == "failed" {
			return out, nil
		}
		return out, execution.ErrUnavailable
	}
	out.MessageID = result.Message.ID
	out.Result = &result
	// Approval/login continuations update the workbench's authoritative run.
	// A read projects that state; it never rewrites the admission ledger.
	if status.State == "approval_pending" || status.State == "browser_login_blocked" {
		out.State = result.Run.State
	}
	return out, nil
}

func (s *Server) admitWorkbenchMessage(r *http.Request, session app.Session, input webMessageInput, ingress app.MessageIngressContext) (*workbenchAdmission, *workbenchRequestStatus, error) {
	if input.DraftRevision != nil && *input.DraftRevision < 0 {
		return nil, nil, errors.New("draft_revision cannot be negative")
	}
	if !execution.UUID(input.RequestID) {
		return nil, nil, errors.New("request_id must be a UUID")
	}
	binding := workbenchBinding(r, session, input.RequestID)
	raw, err := json.Marshal(struct {
		Input   webMessageInput           `json:"input"`
		Ingress app.MessageIngressContext `json:"ingress"`
	}{input, ingress})
	if err != nil {
		return nil, nil, err
	}
	binding.InputDigest = execution.Digest(raw)
	binding.SubmittedDraftRevision = input.DraftRevision
	service, err := s.executionService()
	if err != nil {
		return nil, nil, err
	}
	if status, lookupErr := service.LookupWorkbenchClaim(binding); lookupErr == nil {
		if status.InputDigest != binding.InputDigest {
			return nil, nil, execution.ErrConflict
		}
		previous, err := s.admittedWorkbenchResult(r.Context(), binding, status)
		return nil, &previous, err
	} else if !errors.Is(lookupErr, execution.ErrNotFound) {
		return nil, nil, lookupErr
	}
	releaseSession := s.tryAdmitSessionMessage(session.ID)
	if releaseSession == nil {
		return nil, nil, execution.ErrConflict
	}
	executionCtx, finish := s.detachedExecutionContext()
	executionCtx, releaseClient, err := s.clientConnectionContext(executionCtx, binding.ClientID)
	if err != nil {
		finish()
		releaseSession()
		return nil, nil, err
	}
	release := func() { releaseClient(); finish(); releaseSession() }
	snapshot, err := s.runtime.PrepareWorkbenchContext(executionCtx, session.ID)
	if err != nil {
		release()
		return nil, nil, err
	}
	binding.ContextBefore, binding.ContextDigest = snapshot.Before, snapshot.Digest
	lease, status, err := service.BeginWorkbench(executionCtx, binding)
	if err != nil {
		release()
		return nil, nil, err
	}
	if lease == nil {
		release()
		previous, err := s.admittedWorkbenchResult(r.Context(), binding, status)
		return nil, &previous, err
	}
	admission := &workbenchAdmission{lease: lease, binding: binding, release: release, snapshot: snapshot}
	// Preserve the submitted owner input before consuming its draft or sending
	// acceptance. Runtime AddMessage is idempotent for this exact stable ID.
	attachments := sanitizeMessageAttachments(input.Attachments)
	message, err := s.store.AddMessage(lease.Context, app.Message{ID: lease.InputMessageID, SessionID: session.ID, Role: "user", Content: input.Content, Attachments: attachments, CreatedAt: time.Now().UTC()})
	if err != nil || message.ID != lease.InputMessageID || message.SessionID != session.ID || message.Role != "user" || message.Content != input.Content || !slices.Equal(message.Attachments, attachments) {
		admission.close()
		if err == nil {
			err = execution.ErrUnavailable
		}
		return nil, nil, err
	}
	if input.DraftRevision != nil {
		cleared, err := s.store.SaveWorkbenchDraft(lease.Context, binding.OwnerID, binding.SessionID, app.WorkbenchDraft{Revision: *input.DraftRevision})
		if err == nil {
			err = lease.RecordDraftClear(cleared.Revision)
		}
		if err != nil && store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
			admission.close()
			return nil, nil, err
		}
	}
	admission.lease.Context = agent.WithWorkbenchContinuation(admission.lease.Context, func(ctx context.Context, run app.AgentRun) (context.Context, error) {
		return s.workbenchBrowserContinuation(ctx, binding, run, admission)
	})
	return admission, nil, nil
}

func (s *Server) finishWorkbenchMessage(admission *workbenchAdmission, result agent.Result, executionErr, deliveryErr error) error {
	for _, child := range admission.continuations {
		if err := s.finishWorkbenchMessage(child, result, executionErr, deliveryErr); err != nil {
			executionErr = err
		}
	}
	if executionErr != nil {
		_ = admission.lease.Finish("unknown", "")
		return executionErr
	}
	if err := admission.lease.Context.Err(); err != nil {
		_ = admission.lease.Finish("unknown", "")
		return err
	}
	persisted, found, err := s.runtime.LookupRunResult(context.WithoutCancel(admission.lease.Context), admission.binding.SessionID, result.Run.ID)
	if err != nil || !found || !persistedWorkbenchResultValid(persisted) || persisted.Message.ID != result.Message.ID || persisted.Run.State != result.Run.State {
		_ = admission.lease.Finish("unknown", "")
		return execution.ErrUnavailable
	}
	state := persisted.Run.State
	if deliveryErr != nil {
		state = "delivery_failed"
	}
	return admission.lease.FinishRun(state, persisted.Run.ID, persisted.Message.ID)
}

func (s *Server) getWorkbenchRequest(w http.ResponseWriter, r *http.Request) {
	session, found, err := s.sessionForRequest(r.Context(), r, r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	service, err := s.workbenchReader()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	binding := workbenchBinding(r, session, r.PathValue("request"))
	status, err := service.LookupWorkbench(binding)
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	result, err := s.admittedWorkbenchResult(r.Context(), binding, status)
	if err != nil {
		writeConversationError(w, http.StatusServiceUnavailable, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

// Reading request status must not create storage, run cleanup or advance state.
func (s *Server) workbenchReader() (execution.WorkbenchReader, error) {
	s.executionMu.Lock()
	service := s.executions
	root := s.executionRoot
	s.executionMu.Unlock()
	if service != nil {
		return service, nil
	}
	if root == "" {
		root = s.cfg.State.Path + ".execution"
	}
	if root == ".execution" {
		return nil, execution.ErrUnavailable
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return execution.ReadWorkbench(absolute)
}

func (s *Server) listWorkbenchRequests(w http.ResponseWriter, r *http.Request) {
	session, found, err := s.sessionForRequest(r.Context(), r, r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	reader, err := s.workbenchReader()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	binding := workbenchBinding(r, session, "")
	rows, next, err := reader.ListWorkbench(binding, r.URL.Query().Get("cursor"), r.URL.Query().Get("attention") == "1")
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	projected := make([]workbenchRequestStatus, 0, len(rows))
	for _, row := range rows {
		// Only a suspended run can have advanced after its admission completed.
		// Keep list responses content-free; full results belong to single lookup.
		if row.State == "approval_pending" || row.State == "browser_login_blocked" {
			result, err := s.admittedWorkbenchResult(r.Context(), binding, row)
			if err != nil {
				writeConversationError(w, http.StatusServiceUnavailable, err)
				return
			}
			row = result.WorkbenchStatus
		}
		if r.URL.Query().Get("attention") == "1" && row.State == "completed" {
			continue
		}
		projected = append(projected, workbenchRequestStatus{WorkbenchStatus: row})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"requests": projected, "next_cursor": next})
}

func (s *Server) cancelWorkbenchRequest(w http.ResponseWriter, r *http.Request) {
	session, found, err := s.sessionForRequest(r.Context(), r, r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	service, err := s.executionService()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	binding := workbenchBinding(r, session, r.PathValue("request"))
	status, err := service.CancelWorkbench(binding)
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	result, err := s.admittedWorkbenchResult(r.Context(), binding, status)
	if err != nil {
		writeConversationError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func persistedWorkbenchResultValid(result agent.Result) bool {
	return result.Message.ID != "" || result.Message.SessionID == "" && result.WorkflowResult != nil && result.WorkflowResult.ReturnRoute.Mode == app.ReturnToEndpoint
}

func writeWorkbenchAdmissionError(w http.ResponseWriter, err error) {
	if status, public, ok := conversationErrorProjection(err); ok {
		writeError(w, status, public)
		return
	}
	writeExecutionError(w, err)
}

// Approval and browser-login continuations retain the original request budget
// and credential binding even when another owner device approves the action.
func (s *Server) beginWorkbenchContinuation(ctx context.Context, scope execution.WorkbenchBinding, runID string, lockSession bool) (*workbenchAdmission, error) {
	service, err := s.executionService()
	if err != nil {
		return nil, err
	}
	binding, found, err := service.WorkbenchForRun(scope, runID)
	if err != nil || !found {
		return nil, err
	}
	releaseSession := func() {}
	if lockSession {
		releaseSession = s.tryAdmitSessionMessage(scope.SessionID)
		if releaseSession == nil {
			return nil, execution.ErrConflict
		}
	}
	connected, releaseClient, err := s.clientConnectionContext(ctx, binding.ClientID)
	if err != nil {
		releaseSession()
		if errors.Is(err, errClientConnectionRevoked) {
			return nil, errors.Join(execution.ErrConflict, execution.ErrContinuationClosed)
		}
		return nil, execution.ErrUnavailable
	}
	// A second owner's device may approve, but revoking either the original
	// execution credential or this active approving credential cancels the work.
	if scope.ClientID != binding.ClientID {
		approverCtx, releaseApprover, attachErr := s.clientConnectionContext(connected, scope.ClientID)
		if attachErr != nil {
			releaseClient()
			releaseSession()
			return nil, execution.ErrConflict
		}
		releaseOriginal := releaseClient
		releaseClient = func() { releaseApprover(); releaseOriginal() }
		connected = approverCtx
	}
	lease, err := service.ContinueWorkbench(connected, binding)
	if err != nil {
		releaseClient()
		releaseSession()
		return nil, err
	}
	return &workbenchAdmission{lease: lease, binding: binding, release: func() { releaseClient(); releaseSession() }}, nil
}

func (s *Server) workbenchBrowserContinuation(ctx context.Context, scope execution.WorkbenchBinding, run app.AgentRun, parent *workbenchAdmission) (context.Context, error) {
	admission, err := s.beginWorkbenchContinuation(ctx, scope, run.ID, false)
	if err != nil {
		if errors.Is(err, execution.ErrContinuationClosed) || errors.Is(err, execution.ErrExpired) {
			return nil, agent.ErrWorkbenchContinuationClosed
		}
		return nil, err
	}
	if admission == nil {
		return ctx, nil
	}
	parent.continuations = append(parent.continuations, admission)
	return admission.lease.Context, nil
}
