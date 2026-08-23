package weixin

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func testSaveRunFeedback(repository store.RunRepository, feedback app.RunFeedback) app.RunFeedback {
	saved, err := repository.SaveRunFeedback(context.Background(), feedback)
	if err != nil {
		panic(err)
	}
	return saved
}

func testListRunFeedback(repository store.RunRepository, runID string) []app.RunFeedback {
	values, err := repository.ListRunFeedback(context.Background(), runID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveRun(repository store.RunRepository, run app.AgentRun) {
	if _, err := repository.SaveRun(context.Background(), run); err != nil {
		panic(err)
	}
}

func testGetRun(repository store.RunRepository, id string) (app.AgentRun, bool) {
	value, found, err := repository.GetRun(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return value, found
}

func testListRuns(repository store.RunRepository, sessionID string) []app.AgentRun {
	values, err := repository.ListRuns(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveModelCall(repository store.RunRepository, call app.ModelCall) {
	if _, err := repository.SaveModelCall(context.Background(), call); err != nil {
		panic(err)
	}
}

func testListModelCalls(repository store.RunRepository, sessionID, runID string) []app.ModelCall {
	values, err := repository.ListModelCalls(context.Background(), sessionID, runID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveToolCall(repository store.RunRepository, call app.ToolCall) {
	if _, err := repository.SaveToolCall(context.Background(), call); err != nil {
		panic(err)
	}
}

func testGetToolCall(repository store.RunRepository, id string) (app.ToolCall, bool) {
	value, found, err := repository.GetToolCall(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return value, found
}

func testListToolCalls(repository store.RunRepository, sessionID string) []app.ToolCall {
	values, err := repository.ListToolCalls(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}

func testSaveEpisodeSummary(repository store.RunRepository, summary app.EpisodeSummary) {
	if _, err := repository.SaveEpisodeSummary(context.Background(), summary); err != nil {
		panic(err)
	}
}

func testListEpisodeSummaries(repository store.RunRepository, sessionID string) []app.EpisodeSummary {
	values, err := repository.ListEpisodeSummaries(context.Background(), sessionID)
	if err != nil {
		panic(err)
	}
	return values
}
