package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// HandleAdmittedWorkbenchMessage executes already-admitted work with stable
// record IDs. Admission/replay decisions belong to the execution service.
func (r Runtime) HandleAdmittedWorkbenchMessage(ctx context.Context, sessionID, messageID, runID, content string, attachments []MessageAttachment, ingress app.MessageIngressContext, action *ScheduleAction, emit StreamHandler) (Result, error) {
	return r.handleMessage(ctx, sessionID, content, attachments, emit, messageID, runID, &ingress, action)
}

// LookupRunResult is a read-only projection of the workbench's persisted run.
func (r Runtime) LookupRunResult(ctx context.Context, sessionID, runID string) (Result, bool, error) {
	run, found, err := r.store.GetRun(ctx, runID)
	if err != nil || !found || run.SessionID != sessionID {
		return Result{}, false, err
	}
	result, err := r.resultForExistingRun(ctx, run)
	return result, true, err
}

// WorkbenchContext freezes the same bounded history selected for an invocation
// before the durable admission. Only its digest and cutoff enter control state.
type WorkbenchContext struct {
	Before    time.Time
	Digest    string
	sessionID string
	history   invocationHistory
}
type workbenchContextKey struct{}

func (r Runtime) PrepareWorkbenchContext(ctx context.Context, sessionID string) (WorkbenchContext, error) {
	before := time.Now().UTC().Truncate(time.Microsecond)
	history, err := r.buildInvocationHistory(ctx, app.AgentRun{ID: "workbench-admission", SessionID: sessionID, StartedAt: before}, "")
	if err != nil {
		return WorkbenchContext{}, err
	}
	raw, err := json.Marshal(history)
	if err != nil {
		return WorkbenchContext{}, err
	}
	digest := sha256.Sum256(raw)
	return WorkbenchContext{Before: before, Digest: hex.EncodeToString(digest[:]), sessionID: sessionID, history: history}, nil
}

func WithWorkbenchContext(ctx context.Context, snapshot WorkbenchContext) context.Context {
	return context.WithValue(ctx, workbenchContextKey{}, snapshot)
}

// ErrWorkbenchContinuationClosed permits cleanup of a stale browser block,
// never continuation of the original tools. Temporary admission errors must not
// use this sentinel.
var ErrWorkbenchContinuationClosed = errors.New("original workbench request cannot resume")

type WorkbenchContinuation func(context.Context, app.AgentRun) (context.Context, error)
type workbenchContinuationKey struct{}

func WithWorkbenchContinuation(ctx context.Context, guard WorkbenchContinuation) context.Context {
	return context.WithValue(ctx, workbenchContinuationKey{}, guard)
}
