package agent

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (r Runtime) saveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	return saveRunRecord(ctx, r.store, run)
}

func saveRunRecord(ctx context.Context, repository store.RunRepository, run app.AgentRun) (app.AgentRun, error) {
	saved, err := repository.SaveRun(ctx, run)
	if err == nil {
		return saved, nil
	}
	return store.ReconcileRunWrite(ctx, repository, saved, err)
}

func (r Runtime) saveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	return saveToolCallRecord(ctx, r.store, call)
}

func saveToolCallRecord(ctx context.Context, repository store.RunRepository, call app.ToolCall) (app.ToolCall, error) {
	saved, err := repository.SaveToolCall(ctx, call)
	if err == nil {
		return saved, nil
	}
	return store.ReconcileToolCallWrite(ctx, repository, saved, err)
}
