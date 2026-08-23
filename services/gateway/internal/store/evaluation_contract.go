package store

import (
	"errors"
	"slices"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var errEvalRunJSONDecode = errors.New("decode evaluation run JSON")

func prepareEvalRun(run app.EvalRun, now time.Time) app.EvalRun {
	if run.ID == "" {
		run.ID = app.NewID("eval")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.StartedAt = postgresTime(run.StartedAt)
	if run.CompletedAt != nil {
		completedAt := postgresTime(*run.CompletedAt)
		run.CompletedAt = &completedAt
	}
	return cloneEvalRun(run)
}

func cloneEvalRun(run app.EvalRun) app.EvalRun {
	run.Cases = slices.Clone(run.Cases)
	run.FailureArchives = slices.Clone(run.FailureArchives)
	run.CompletedAt = cloneTimePointer(run.CompletedAt)
	return run
}
