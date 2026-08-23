package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func testSaveRunFeedback(repository RunRepository, feedback app.RunFeedback) app.RunFeedback {
	saved, err := repository.SaveRunFeedback(context.Background(), feedback)
	if err != nil {
		panic(err)
	}
	return saved
}

func testListRunFeedback(repository RunRepository, runID string) []app.RunFeedback {
	values, err := repository.ListRunFeedback(context.Background(), runID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveRun(repository RunRepository, run app.AgentRun) {
	if _, err := repository.SaveRun(context.Background(), run); err != nil {
		panic(err)
	}
}

func testGetRun(repository RunRepository, id string) (app.AgentRun, bool) {
	value, found, err := repository.GetRun(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return value, found
}

func testListRuns(repository RunRepository, sessionID string) []app.AgentRun {
	values, err := repository.ListRuns(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveModelCall(repository RunRepository, call app.ModelCall) {
	if _, err := repository.SaveModelCall(context.Background(), call); err != nil {
		panic(err)
	}
}

func testListModelCalls(repository RunRepository, sessionID, runID string) []app.ModelCall {
	values, err := repository.ListModelCalls(context.Background(), sessionID, runID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveToolCall(repository RunRepository, call app.ToolCall) {
	if _, err := repository.SaveToolCall(context.Background(), call); err != nil {
		panic(err)
	}
}

func testGetToolCall(repository RunRepository, id string) (app.ToolCall, bool) {
	value, found, err := repository.GetToolCall(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return value, found
}

func testListToolCalls(repository RunRepository, sessionID string) []app.ToolCall {
	values, err := repository.ListToolCalls(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveEpisodeSummary(repository RunRepository, summary app.EpisodeSummary) {
	if _, err := repository.SaveEpisodeSummary(context.Background(), summary); err != nil {
		panic(err)
	}
}

func testListEpisodeSummaries(repository RunRepository, sessionID string) []app.EpisodeSummary {
	values, err := repository.ListEpisodeSummaries(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}
