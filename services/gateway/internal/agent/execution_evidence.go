package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type evidencePersistenceKey struct{}

func (r Runtime) evidencePersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, active := ctx.Value(evidencePersistenceKey{}).(bool); active {
		return context.WithCancel(ctx)
	}
	timeout := executionevidence.DefaultOptions().OperationTimeout
	if r.tools != nil && r.tools.Config().Storage.ExecutionEvidence.OperationTimeoutSeconds > 0 {
		timeout = time.Duration(r.tools.Config().Storage.ExecutionEvidence.OperationTimeoutSeconds) * time.Second
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	return context.WithValue(persist, evidencePersistenceKey{}, true), cancel
}

// executionEvidenceContext freezes the received request once. Resumes bind the
// same scope and never replace its original request with later session history.
func (r Runtime) executionEvidenceContext(ctx context.Context, run app.AgentRun) context.Context {
	if r.evidence == nil {
		if _, bound := executionevidence.ScopeFromContext(ctx); bound {
			return executionevidence.WithRecorder(ctx, nil, executionevidence.Scope{})
		}
		return ctx
	}
	if run.ID == "" || run.SessionID == "" {
		return ctx
	}
	if scope, ok := executionevidence.ScopeFromContext(ctx); ok {
		if scope.RunID == run.ID && scope.SessionID == run.SessionID {
			return ctx
		}
		// A nested operation cannot switch or write into another run's capture.
		return executionevidence.WithRecorder(ctx, nil, executionevidence.Scope{})
	}
	persist, cancel := r.evidencePersistenceContext(ctx)
	defer cancel()
	session, found, err := r.store.GetSession(persist, run.SessionID)
	if err != nil || !found || session.OwnerID == "" {
		return ctx
	}
	scope := executionevidence.Scope{OwnerID: session.OwnerID, SessionID: run.SessionID, RunID: run.ID}
	bound := r.evidence.Bind(ctx, scope)
	if _, err = r.evidence.Read(persist, scope); err == nil {
		return bound
	}
	if !errors.Is(err, fs.ErrNotExist) {
		r.evidenceDiagnostic(run.ID, "open", err)
		return bound
	}
	request := map[string]any{"run_id": run.ID, "session_id": run.SessionID,
		"owner_id": session.OwnerID, "started_at": run.StartedAt,
		"request_context": run.MessageContext, "build": evidenceBuildIdentity()}
	if err = r.evidence.Begin(persist, scope, request); err != nil {
		r.evidenceDiagnostic(run.ID, "begin", err)
	} else if run.State != "received" {
		_ = executionevidence.MarkGap(bound, "capture_started_after_admission", "earlier decisions were not observed by this recorder")
	}
	return bound
}

func (r Runtime) recordEvidenceApproval(ctx context.Context, approval app.Approval) {
	if r.evidence == nil {
		return
	}
	ctx, cancel := r.evidencePersistenceContext(ctx)
	defer cancel()
	if run, found, err := r.store.GetRun(ctx, approval.RunID); err == nil && found {
		ctx = r.executionEvidenceContext(ctx, run)
	}
	r.captureEvidence(ctx, executionevidence.Event{Kind: "approval.state", CallID: approval.ToolCallID, Status: string(approval.Status)},
		"approval", approval.ID, approval)
}

func (r Runtime) beginEvidenceToolCall(ctx context.Context, call app.ToolCall) context.Context {
	if r.evidence == nil {
		return ctx
	}
	r.captureEvidence(ctx, executionevidence.Event{ID: call.ID, Kind: "tool.call.started", CallID: call.ID,
		NodeID: string(call.WorkflowNodeID), Status: "started"}, "tool_call", call.ID, call)
	return executionevidence.WithParent(ctx, call.ID)
}

func (r Runtime) recordEvidenceManualRequest(ctx context.Context, call app.ToolCall) {
	if r.evidence == nil {
		return
	}
	r.captureEvidence(ctx, executionevidence.Event{Kind: "manual.request", Status: "received", Data: map[string]any{"tool_call_id": call.ID}}, "manual_request", call.ID, call)
}

func (r Runtime) recordEvidenceDocumentOutputs(ctx context.Context, call app.ToolCall) {
	if r.evidence == nil {
		return
	}
	root, err := r.workspaceRootForSession(ctx, call.SessionID)
	if err != nil {
		_ = executionevidence.MarkGap(ctx, "output_scope_unavailable", "cannot resolve output workspace")
		return
	}
	for _, path := range documentOutputPaths(call, root) {
		toolhub.CaptureDocumentOutputEvidence(ctx, root, filepath.Join(root, filepath.FromSlash(path)))
	}
}

func evidenceBuildIdentity() map[string]string {
	identity := map[string]string{"revision": "unknown", "modified": "unknown"}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				identity["revision"] = setting.Value
			case "vcs.modified":
				identity["modified"] = setting.Value
			}
		}
	}
	return identity
}

func (r Runtime) recordEvidenceRun(ctx context.Context, run app.AgentRun) {
	if r.evidence == nil {
		return
	}
	ctx = r.executionEvidenceContext(ctx, run)
	r.captureEvidence(ctx, executionevidence.Event{Kind: "run.state", Status: run.State,
		Data: map[string]any{"risk": run.Risk}}, "run", run.ID, run)
}

func (r Runtime) recordEvidenceToolCall(ctx context.Context, call app.ToolCall) {
	if r.evidence == nil {
		return
	}
	ctx, cancel := r.evidencePersistenceContext(ctx)
	defer cancel()
	if _, bound := executionevidence.ScopeFromContext(ctx); !bound {
		if run, found, err := r.store.GetRun(ctx, call.RunID); err == nil && found {
			ctx = r.executionEvidenceContext(ctx, run)
		}
	}
	status := string(call.Status)
	if call.CompletedAt != nil {
		status = "completed"
		if call.Error != "" {
			status = "failed"
		}
	}
	r.captureEvidence(ctx, executionevidence.Event{Kind: "tool.state", CallID: call.ID,
		NodeID: string(call.WorkflowNodeID), Status: status, ErrorCode: call.ErrorCode,
		Data: map[string]any{"tool": call.Tool, "business_status": call.Status, "capability": call.Capability}},
		"tool_call", call.ID, call)
}

func (r Runtime) recordEvidenceAudit(ctx context.Context, event app.AuditEvent) {
	if r.evidence == nil || event.RunID == "" {
		return
	}
	ctx, cancel := r.evidencePersistenceContext(ctx)
	defer cancel()
	if _, bound := executionevidence.ScopeFromContext(ctx); !bound {
		if run, found, err := r.store.GetRun(ctx, event.RunID); err == nil && found {
			ctx = r.executionEvidenceContext(ctx, run)
		}
	}
	r.captureEvidence(ctx, executionevidence.Event{Kind: "audit", Status: "observed",
		Data: map[string]any{"audit_type": event.Type, "actor": event.Actor}}, "audit", event.Type, event)
}

func (r Runtime) captureEvidence(ctx context.Context, event executionevidence.Event, kind, name string, value any) {
	scope, bound := executionevidence.ScopeFromContext(ctx)
	if !bound {
		return
	}
	ctx, cancel := r.evidencePersistenceContext(ctx)
	defer cancel()
	payload, err := executionevidence.CaptureJSON(ctx, kind, name, value)
	if err != nil {
		if errors.Is(err, executionevidence.ErrSealed) {
			return
		}
		_ = executionevidence.MarkGap(ctx, "payload_capture_failed", kind)
		r.evidenceDiagnostic(scope.RunID, "payload", err)
		return
	}
	event.PayloadIDs = append(event.PayloadIDs, payload.ID)
	if err = executionevidence.Record(ctx, event); err != nil {
		_ = executionevidence.MarkGap(ctx, "event_capture_failed", event.Kind)
		r.evidenceDiagnostic(scope.RunID, "event", err)
	}
}

func (r Runtime) finishExecutionEvidence(ctx context.Context, run app.AgentRun, chat modelrouter.ChatResult) {
	if r.evidence == nil {
		return
	}
	ctx, cancel := r.evidencePersistenceContext(ctx)
	defer cancel()
	ctx = r.executionEvidenceContext(ctx, run)
	scope, bound := executionevidence.ScopeFromContext(ctx)
	if !bound {
		return
	}
	current, err := r.evidence.Read(ctx, scope)
	if err != nil || current.Capture.Sealed {
		return
	}
	messages, msgErr := r.store.ListMessages(ctx, run.SessionID)
	selected := []app.Message{}
	for _, message := range messages {
		if message.RunID == run.ID {
			selected = append(selected, message)
		}
	}
	modelCalls, callErr := r.store.ListModelCalls(ctx, run.SessionID, run.ID)
	toolCalls, toolErr := r.store.ListToolCalls(ctx, run.SessionID)
	selectedTools := []app.ToolCall{}
	approvals := []app.Approval{}
	for _, call := range toolCalls {
		if call.RunID != run.ID {
			continue
		}
		selectedTools = append(selectedTools, call)
		if call.ApprovalID == "" {
			continue
		}
		approval, found, err := r.store.GetApproval(ctx, call.ApprovalID)
		if err != nil || !found {
			_ = executionevidence.MarkGap(ctx, "approval_unavailable", "final approval record unavailable")
			continue
		}
		approvals = append(approvals, approval)
		r.recordEvidenceApproval(ctx, approval)
	}
	if msgErr != nil || callErr != nil || toolErr != nil {
		_ = executionevidence.MarkGap(ctx, "final_records_unavailable", "messages or model metadata unavailable")
	}
	r.captureEvidence(ctx, executionevidence.Event{Kind: "run.result", Status: run.State}, "result", run.ID,
		map[string]any{"run": run, "messages": selected, "model": chat, "model_calls": modelCalls, "tool_calls": selectedTools, "approvals": approvals})
	if run.CompletedAt != nil {
		if err = r.evidence.Seal(ctx, scope, run.State); err != nil {
			r.evidenceDiagnostic(run.ID, "seal", err)
		}
	}
}

// Entry-point failures may exit before writeTrace. Preserve the actual durable
// state; an uncompleted operation is a capture gap, never a synthesized success.
func (r Runtime) finishUnsealedEvidence(ctx context.Context, runID string) {
	if r.evidence == nil {
		return
	}
	persist, cancel := r.evidencePersistenceContext(ctx)
	defer cancel()
	run, found, err := r.store.GetRun(persist, runID)
	if err != nil || !found {
		return
	}
	if run.CompletedAt != nil {
		r.finishExecutionEvidence(persist, run, modelrouter.ChatResult{})
		return
	}
	if run.State != "approval_pending" && run.State != "browser_login_blocked" {
		persist = r.executionEvidenceContext(persist, run)
		_ = executionevidence.MarkGap(persist, "run_interrupted", "entry point exited without a terminal run record")
	}
}

func (r Runtime) evidenceDiagnostic(runID, stage string, err error) {
	// Details can contain original content or private paths. The recorder owns
	// the bounded diagnostic; ordinary application logs retain only identifiers.
	if err != nil {
		slog.Warn("execution evidence incomplete", "run_id", runID, "stage", stage)
	}
}

func (r Runtime) recordEvidenceInterruptedRun(ctx context.Context, run app.AgentRun) {
	if r.evidence == nil {
		return
	}
	ctx = r.executionEvidenceContext(ctx, run)
	_ = executionevidence.MarkGap(ctx, "process_interrupted", "the process ended before a terminal operation record")
	r.recordEvidenceRun(ctx, run)
	r.finishExecutionEvidence(ctx, run, modelrouter.ChatResult{})
}
