package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type runStoreTestRepository struct {
	store.RunRepository
	saveRun      func(context.Context, app.AgentRun) (app.AgentRun, error)
	getRun       func(context.Context, string) (app.AgentRun, bool, error)
	saveToolCall func(context.Context, app.ToolCall) (app.ToolCall, error)
	getToolCall  func(context.Context, string) (app.ToolCall, bool, error)
}

func (r runStoreTestRepository) SaveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	return r.saveRun(ctx, run)
}

func (r runStoreTestRepository) GetRun(ctx context.Context, id string) (app.AgentRun, bool, error) {
	return r.getRun(ctx, id)
}

func (r runStoreTestRepository) SaveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	return r.saveToolCall(ctx, call)
}

func (r runStoreTestRepository) GetToolCall(ctx context.Context, id string) (app.ToolCall, bool, error) {
	return r.getToolCall(ctx, id)
}

func TestRunStoreHelpersReturnDefiniteWriteFailures(t *testing.T) {
	failure := &store.StoreError{Code: store.StoreErrorDurability, Operation: store.OperationRunSave, Err: errors.New("write failed")}
	repository := runStoreTestRepository{
		saveRun: func(context.Context, app.AgentRun) (app.AgentRun, error) {
			return app.AgentRun{}, failure
		},
		getRun: func(context.Context, string) (app.AgentRun, bool, error) {
			t.Fatal("definite Run failure must not be reconciled")
			return app.AgentRun{}, false, nil
		},
	}
	if saved, err := saveRunRecord(t.Context(), repository, app.AgentRun{ID: "run-failed"}); saved.ID != "" || !errors.Is(err, failure) {
		t.Fatalf("saveRunRecord = %#v err=%v", saved, err)
	}

	failure = &store.StoreError{Code: store.StoreErrorDurability, Operation: store.OperationToolCallSave, Err: errors.New("write failed")}
	repository = runStoreTestRepository{
		saveToolCall: func(context.Context, app.ToolCall) (app.ToolCall, error) {
			return app.ToolCall{}, failure
		},
		getToolCall: func(context.Context, string) (app.ToolCall, bool, error) {
			t.Fatal("definite ToolCall failure must not be reconciled")
			return app.ToolCall{}, false, nil
		},
	}
	if saved, err := saveToolCallRecord(t.Context(), repository, app.ToolCall{ID: "tool-failed"}); saved.ID != "" || !errors.Is(err, failure) {
		t.Fatalf("saveToolCallRecord = %#v err=%v", saved, err)
	}
}

func TestRunStoreHelpersReconcileCommittedNormalizedCandidates(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "caller")
	startedAt := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	unknownRun := &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationRunSave, Err: errors.New("commit uncertain")}
	committedRun := app.AgentRun{ID: "run-committed", SessionID: "session", State: "running", StartedAt: startedAt}
	repository := runStoreTestRepository{
		saveRun: func(got context.Context, run app.AgentRun) (app.AgentRun, error) {
			if got.Value(contextKey{}) != "caller" || !run.StartedAt.IsZero() {
				t.Fatalf("SaveRun context/input = %v %#v", got.Value(contextKey{}), run)
			}
			return committedRun, unknownRun
		},
		getRun: func(got context.Context, id string) (app.AgentRun, bool, error) {
			if got.Value(contextKey{}) != "caller" || id != committedRun.ID {
				t.Fatalf("GetRun context/id = %v %q", got.Value(contextKey{}), id)
			}
			return committedRun, true, nil
		},
	}
	if saved, err := saveRunRecord(ctx, repository, app.AgentRun{ID: committedRun.ID, SessionID: "session", State: "running"}); err != nil || !saved.StartedAt.Equal(startedAt) {
		t.Fatalf("saveRunRecord = %#v err=%v", saved, err)
	}

	unknownTool := &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationToolCallSave, Err: errors.New("commit uncertain")}
	committedTool := app.ToolCall{ID: "tool-committed", SessionID: "session", RunID: committedRun.ID, Tool: "files.read", StartedAt: startedAt}
	repository = runStoreTestRepository{
		saveToolCall: func(got context.Context, call app.ToolCall) (app.ToolCall, error) {
			if got.Value(contextKey{}) != "caller" || !call.StartedAt.IsZero() {
				t.Fatalf("SaveToolCall context/input = %v %#v", got.Value(contextKey{}), call)
			}
			return committedTool, unknownTool
		},
		getToolCall: func(got context.Context, id string) (app.ToolCall, bool, error) {
			if got.Value(contextKey{}) != "caller" || id != committedTool.ID {
				t.Fatalf("GetToolCall context/id = %v %q", got.Value(contextKey{}), id)
			}
			return committedTool, true, nil
		},
	}
	if saved, err := saveToolCallRecord(ctx, repository, app.ToolCall{ID: committedTool.ID, SessionID: "session", RunID: committedRun.ID, Tool: "files.read"}); err != nil || !saved.StartedAt.Equal(startedAt) {
		t.Fatalf("saveToolCallRecord = %#v err=%v", saved, err)
	}
}
