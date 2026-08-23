package storetest

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// The run-repository fixtures below were copied verbatim into seven consumer
// packages before storetest existed; they now live here once, and each
// package aliases them under its historical test names. They keep the legacy
// panic-on-error, background-context contract because their call sites
// predate testing.TB threading; new tests should prefer Must* helpers that
// take a testing.TB. The store package keeps an in-package twin
// (run_repository_legacy_test.go) because its own tests cannot import
// storetest without an import cycle.

func SaveRun(repository store.RunRepository, run app.AgentRun) {
	if _, err := repository.SaveRun(context.Background(), run); err != nil {
		panic(err)
	}
}

func GetRun(repository store.RunRepository, id string) (app.AgentRun, bool) {
	value, found, err := repository.GetRun(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return value, found
}

func ListRuns(repository store.RunRepository, sessionID string) []app.AgentRun {
	values, err := repository.ListRuns(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}

func SaveModelCall(repository store.RunRepository, call app.ModelCall) {
	if _, err := repository.SaveModelCall(context.Background(), call); err != nil {
		panic(err)
	}
}

func ListModelCalls(repository store.RunRepository, sessionID, runID string) []app.ModelCall {
	values, err := repository.ListModelCalls(context.Background(), sessionID, runID)
	if err != nil {
		panic(err)
	}
	return values
}

func SaveToolCall(repository store.RunRepository, call app.ToolCall) {
	if _, err := repository.SaveToolCall(context.Background(), call); err != nil {
		panic(err)
	}
}

func GetToolCall(repository store.RunRepository, id string) (app.ToolCall, bool) {
	value, found, err := repository.GetToolCall(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return value, found
}

func ListToolCalls(repository store.RunRepository, sessionID string) []app.ToolCall {
	values, err := repository.ListToolCalls(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}

func SaveEpisodeSummary(repository store.RunRepository, summary app.EpisodeSummary) {
	if _, err := repository.SaveEpisodeSummary(context.Background(), summary); err != nil {
		panic(err)
	}
}

func ListEpisodeSummaries(repository store.RunRepository, sessionID string) []app.EpisodeSummary {
	values, err := repository.ListEpisodeSummaries(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}
