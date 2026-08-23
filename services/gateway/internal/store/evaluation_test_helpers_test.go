package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustSaveEvalRun(t testing.TB, repository EvaluationRepository, run app.EvalRun) app.EvalRun {
	t.Helper()
	stored, err := repository.SaveEvalRun(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func mustGetEvalRun(t testing.TB, repository EvaluationRepository, id string) (app.EvalRun, bool) {
	t.Helper()
	run, found, err := repository.GetEvalRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return run, found
}

func mustListEvalRuns(t testing.TB, repository EvaluationRepository) []app.EvalRun {
	t.Helper()
	runs, err := repository.ListEvalRuns(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return runs
}
