package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var errRunJSONDecode = errors.New("decode persisted run JSON")

func prepareRun(run app.AgentRun, now time.Time) (app.AgentRun, error) {
	if strings.TrimSpace(run.ID) == "" {
		return app.AgentRun{}, errors.New("run ID is required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.StartedAt = normalizeRunTime(run.StartedAt)
	run.CompletedAt = normalizeRunTimePointer(run.CompletedAt)
	return cloneRun(run)
}

func prepareRunFeedback(feedback app.RunFeedback, existing *app.RunFeedback, now time.Time) (app.RunFeedback, error) {
	if strings.TrimSpace(feedback.RunID) == "" {
		return app.RunFeedback{}, errors.New("run feedback run ID is required")
	}
	if feedback.ID == "" {
		feedback.ID = app.NewID("fb")
	}
	if existing != nil {
		feedback.ID = existing.ID
		feedback.CreatedAt = existing.CreatedAt
	} else if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = now
	}
	feedback.CreatedAt = normalizeRunTime(feedback.CreatedAt)
	feedback.UpdatedAt = normalizeRunTime(now)
	feedback.Rating = strings.TrimSpace(feedback.Rating)
	feedback.Note = strings.TrimSpace(feedback.Note)
	feedback.Correction = strings.TrimSpace(feedback.Correction)
	return feedback, nil
}

func prepareModelCall(call app.ModelCall, now time.Time) (app.ModelCall, error) {
	if call.ID == "" {
		call.ID = app.NewID("mc")
	}
	if call.StartedAt.IsZero() {
		call.StartedAt = now
	}
	call.StartedAt = normalizeRunTime(call.StartedAt)
	call.CompletedAt = normalizeRunTimePointer(call.CompletedAt)
	return call, nil
}

func prepareToolCall(call app.ToolCall, now time.Time) (app.ToolCall, error) {
	if strings.TrimSpace(call.ID) == "" {
		return app.ToolCall{}, errors.New("tool call ID is required")
	}
	if call.StartedAt.IsZero() {
		call.StartedAt = now
	}
	call.StartedAt = normalizeRunTime(call.StartedAt)
	call.CompletedAt = normalizeRunTimePointer(call.CompletedAt)
	return cloneToolCall(call)
}

func prepareEpisodeSummary(summary app.EpisodeSummary, now time.Time) (app.EpisodeSummary, error) {
	if summary.ID == "" {
		summary.ID = app.NewID("ep")
	}
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = now
	}
	summary.CreatedAt = normalizeRunTime(summary.CreatedAt)
	return cloneEpisodeSummary(summary), nil
}

func normalizeRunTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

func normalizeRunTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := normalizeRunTime(*value)
	return &normalized
}

func cloneRun(run app.AgentRun) (app.AgentRun, error) {
	return cloneRunJSON(run)
}

func cloneToolCall(call app.ToolCall) (app.ToolCall, error) {
	return cloneRunJSON(call)
}

func cloneRunJSON[T any](value T) (T, error) {
	var cloned T
	raw, err := json.Marshal(value)
	if err != nil {
		return cloned, err
	}
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return cloned, err
	}
	return cloned, nil
}

func mustCloneRun[T any](value T) T {
	cloned, err := cloneRunJSON(value)
	if err != nil {
		panic(err)
	}
	return cloned
}

func cloneRunFeedback(values []app.RunFeedback) []app.RunFeedback {
	return append([]app.RunFeedback(nil), values...)
}

func cloneEpisodeSummary(summary app.EpisodeSummary) app.EpisodeSummary {
	summary.Tools = append([]string(nil), summary.Tools...)
	summary.Approvals = append([]string(nil), summary.Approvals...)
	summary.Failures = append([]string(nil), summary.Failures...)
	return summary
}

func runRecordsEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func ReconcileRunWrite(ctx context.Context, repository RunRepository, candidate app.AgentRun, writeErr error) (app.AgentRun, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.AgentRun{}, writeErr
	}
	current, found, err := repository.GetRun(ctx, candidate.ID)
	if err != nil {
		return app.AgentRun{}, errors.Join(writeErr, err)
	}
	if found && runRecordsEqual(current, candidate) {
		return current, nil
	}
	return app.AgentRun{}, writeErr
}

func ReconcileToolCallWrite(ctx context.Context, repository RunRepository, candidate app.ToolCall, writeErr error) (app.ToolCall, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.ToolCall{}, writeErr
	}
	current, found, err := repository.GetToolCall(ctx, candidate.ID)
	if err != nil {
		return app.ToolCall{}, errors.Join(writeErr, err)
	}
	if found && runRecordsEqual(current, candidate) {
		return current, nil
	}
	return app.ToolCall{}, writeErr
}
