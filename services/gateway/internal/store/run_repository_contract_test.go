package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestRunRepositoryContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		runRunRepositoryContract(t, NewMemoryStore(), "session-run-memory")
	})
	t.Run("file", func(t *testing.T) {
		repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		runRunRepositoryContract(t, repository, "session-run-file")
	})
}

func TestPostgresRunRepositoryContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	session, err := repository.CreateSession(t.Context(), "Run repository contract")
	if err != nil {
		t.Fatal(err)
	}
	runRunRepositoryContract(t, repository, session.ID)
}

func runRunRepositoryContract(t *testing.T, repository RunRepository, sessionID string) {
	t.Helper()
	ctx := t.Context()
	base := time.Date(2026, 8, 21, 8, 0, 0, 123456789, time.FixedZone("contract", 8*60*60))
	wantBase := base.UTC().Truncate(time.Microsecond)
	completed := base.Add(time.Minute)
	runID := "run-contract-a"
	run := app.AgentRun{
		ID: runID, SessionID: sessionID, State: "completed", ModelLane: "fast", Risk: app.RiskRead,
		StartedAt: base, CompletedAt: &completed, Summary: "original run",
		MessageContext: &app.MessageRunContext{
			OwnerID:        app.DefaultOwnerID,
			RequestContent: app.MessageContent{Parts: []app.MessagePart{{ID: "part-1", Kind: app.MessagePartText, Text: "original request"}}},
		},
		Workflow: &app.WorkflowState{
			SchemaVersion: 1,
			Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
				"answer": {Status: app.WorkflowNodeSucceeded},
			},
		},
	}
	savedRun, err := repository.SaveRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if !savedRun.StartedAt.Equal(wantBase) || savedRun.StartedAt.Location() != time.UTC || savedRun.CompletedAt == nil || savedRun.CompletedAt.Nanosecond()%1000 != 0 || savedRun.ID != runID {
		t.Fatalf("SaveRun did not apply the run contract: %#v", savedRun)
	}
	run.MessageContext.RequestContent.Parts[0].Text = "mutated caller request"
	run.Workflow.Nodes["answer"] = app.WorkflowNodeState{Status: app.WorkflowNodeBlocked}
	storedRun, found, err := repository.GetRun(ctx, runID)
	if err != nil || !found {
		t.Fatalf("GetRun = %#v found=%t err=%v", storedRun, found, err)
	}
	if storedRun.MessageContext.RequestContent.Parts[0].Text != "original request" || storedRun.Workflow.Nodes["answer"].Status != app.WorkflowNodeSucceeded {
		t.Fatalf("SaveRun retained caller aliases: %#v", storedRun)
	}
	storedRun.MessageContext.RequestContent.Parts[0].Text = "mutated read"
	storedAgain, found, err := repository.GetRun(ctx, runID)
	if err != nil || !found || storedAgain.MessageContext.RequestContent.Parts[0].Text != "original request" {
		t.Fatalf("GetRun returned a mutable alias: %#v found=%t err=%v", storedAgain, found, err)
	}
	storedAgain.Summary = "updated run"
	if _, err := repository.SaveRun(ctx, storedAgain); err != nil {
		t.Fatal(err)
	}
	secondRun := app.AgentRun{ID: "run-contract-b", SessionID: sessionID, State: "completed", StartedAt: savedRun.StartedAt}
	if _, err := repository.SaveRun(ctx, secondRun); err != nil {
		t.Fatal(err)
	}
	runs, err := repository.ListRuns(ctx, sessionID)
	if err != nil || len(runs) != 2 || runs[0].ID != runID || runs[0].Summary != "updated run" || runs[1].ID != secondRun.ID {
		t.Fatalf("ListRuns = %#v err=%v", runs, err)
	}
	missingRun, found, err := repository.GetRun(ctx, "missing-run")
	if err != nil || found || missingRun.ID != "" {
		t.Fatalf("missing GetRun = %#v found=%t err=%v", missingRun, found, err)
	}

	modelCall, err := repository.SaveModelCall(ctx, app.ModelCall{SessionID: sessionID, RunID: runID, Lane: "fast", Model: "model", Operation: "contract", Status: "completed", StartedAt: base})
	if err != nil || modelCall.ID == "" || !modelCall.StartedAt.Equal(wantBase) || modelCall.StartedAt.Location() != time.UTC {
		t.Fatalf("SaveModelCall = %#v err=%v", modelCall, err)
	}
	modelCalls, err := repository.ListModelCalls(ctx, sessionID, runID)
	if err != nil || len(modelCalls) != 1 || modelCalls[0].ID != modelCall.ID {
		t.Fatalf("ListModelCalls = %#v err=%v", modelCalls, err)
	}

	toolCall := app.ToolCall{
		ID: "tool-contract-a", SessionID: sessionID, RunID: runID, Tool: "files.read", Status: app.ToolCallStatusCompleted,
		Arguments: map[string]any{"path": "original.txt", "nested": map[string]any{"value": "original"}},
		Result:    map[string]any{"text": "original result"}, StartedAt: base,
	}
	savedToolCall, err := repository.SaveToolCall(ctx, toolCall)
	if err != nil || !savedToolCall.StartedAt.Equal(wantBase) || savedToolCall.StartedAt.Location() != time.UTC {
		t.Fatalf("SaveToolCall = %#v err=%v", savedToolCall, err)
	}
	toolCall.Arguments["path"] = "mutated.txt"
	toolCall.Result.(map[string]any)["text"] = "mutated result"
	storedToolCall, found, err := repository.GetToolCall(ctx, savedToolCall.ID)
	if err != nil || !found || storedToolCall.Arguments["path"] != "original.txt" || storedToolCall.Result.(map[string]any)["text"] != "original result" {
		t.Fatalf("GetToolCall = %#v found=%t err=%v", storedToolCall, found, err)
	}
	storedToolCall.Arguments["path"] = "mutated read"
	storedToolCall, found, err = repository.GetToolCall(ctx, savedToolCall.ID)
	if err != nil || !found || storedToolCall.Arguments["path"] != "original.txt" {
		t.Fatalf("GetToolCall returned a mutable alias: %#v found=%t err=%v", storedToolCall, found, err)
	}
	secondToolCall, err := repository.SaveToolCall(ctx, app.ToolCall{
		ID: "tool-contract-b", SessionID: sessionID, RunID: runID, Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: savedToolCall.StartedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	toolCalls, err := repository.ListToolCalls(ctx, sessionID)
	if err != nil || len(toolCalls) != 2 || toolCalls[0].ID != savedToolCall.ID || toolCalls[1].ID != secondToolCall.ID {
		t.Fatalf("ListToolCalls = %#v err=%v", toolCalls, err)
	}

	feedback, err := repository.SaveRunFeedback(ctx, app.RunFeedback{
		SessionID: sessionID, RunID: runID, MessageID: "message-contract", Rating: " up ", Note: " first ",
	})
	if err != nil || feedback.ID == "" || feedback.Rating != "up" || feedback.Note != "first" || feedback.CreatedAt.IsZero() {
		t.Fatalf("SaveRunFeedback = %#v err=%v", feedback, err)
	}
	updatedFeedback, err := repository.SaveRunFeedback(ctx, app.RunFeedback{
		SessionID: sessionID, RunID: runID, MessageID: "message-contract", Rating: "down", Correction: " corrected ",
	})
	if err != nil || updatedFeedback.ID != feedback.ID || !updatedFeedback.CreatedAt.Equal(feedback.CreatedAt) || updatedFeedback.Correction != "corrected" {
		t.Fatalf("feedback idempotent update = %#v err=%v", updatedFeedback, err)
	}
	feedbackItems, err := repository.ListRunFeedback(ctx, runID)
	if err != nil || len(feedbackItems) != 1 || feedbackItems[0].ID != feedback.ID {
		t.Fatalf("ListRunFeedback = %#v err=%v", feedbackItems, err)
	}

	summary := app.EpisodeSummary{
		SessionID: sessionID, RunID: runID, Goal: "contract", Outcome: "completed", Risk: app.RiskRead,
		Tools: []string{"files.read"}, Approvals: []string{"approval"}, Failures: []string{"none"}, CreatedAt: base,
	}
	savedSummary, err := repository.SaveEpisodeSummary(ctx, summary)
	if err != nil || savedSummary.ID == "" || !savedSummary.CreatedAt.Equal(wantBase) || savedSummary.CreatedAt.Location() != time.UTC {
		t.Fatalf("SaveEpisodeSummary = %#v err=%v", savedSummary, err)
	}
	summary.Tools[0] = "mutated"
	summaries, err := repository.ListEpisodeSummaries(ctx, sessionID)
	if err != nil || len(summaries) != 1 || summaries[0].Tools[0] != "files.read" {
		t.Fatalf("ListEpisodeSummaries = %#v err=%v", summaries, err)
	}
	summaries[0].Tools[0] = "mutated read"
	summaries, err = repository.ListEpisodeSummaries(ctx, sessionID)
	if err != nil || summaries[0].Tools[0] != "files.read" {
		t.Fatalf("ListEpisodeSummaries returned a mutable alias: %#v err=%v", summaries, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := repository.GetRun(cancelled, runID); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("cancelled GetRun error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, err := repository.SaveToolCall(cancelled, app.ToolCall{ID: "cancelled-tool"}); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("cancelled SaveToolCall error = %v code=%q", err, StoreErrorCodeOf(err))
	}
}
