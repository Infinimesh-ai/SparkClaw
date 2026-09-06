package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveRunFeedback(ctx context.Context, feedback app.RunFeedback) (app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackSave, ctx); err != nil {
		return app.RunFeedback{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationRunFeedbackSave, ctx); err != nil {
		return app.RunFeedback{}, err
	}
	items := s.runFeedback[feedback.RunID]
	var existing *app.RunFeedback
	existingIndex := -1
	for i, current := range items {
		if current.ID == feedback.ID || current.MessageID != "" && current.MessageID == feedback.MessageID {
			existingCopy := current
			existing = &existingCopy
			existingIndex = i
			break
		}
	}
	feedback, err := prepareRunFeedback(feedback, existing, time.Now().UTC())
	if err != nil {
		return app.RunFeedback{}, storeError(ctx, OperationRunFeedbackSave, StoreErrorInvalid, err)
	}
	if existingIndex >= 0 {
		items[existingIndex] = feedback
	} else {
		items = append(items, feedback)
	}
	s.runFeedback[feedback.RunID] = items
	s.appendAuditLocked("run_feedback.saved", feedback.SessionID, feedback.RunID, "owner", feedback.Rating, map[string]any{
		"feedback_id":    feedback.ID,
		"message_id":     feedback.MessageID,
		"has_note":       feedback.Note != "",
		"has_correction": feedback.Correction != "",
	})
	s.appendEventLocked("run_feedback.saved", feedback.SessionID, feedback.RunID, feedback)
	return feedback, nil
}

func (s *MemoryStore) ListRunFeedback(ctx context.Context, runID string) ([]app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationRunFeedbackList, ctx); err != nil {
		return nil, err
	}
	out := []app.RunFeedback{}
	if runID != "" {
		out = append(out, s.runFeedback[runID]...)
	} else {
		for _, items := range s.runFeedback {
			out = append(out, items...)
		}
	}
	slices.SortFunc(out, func(a, b app.RunFeedback) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return cloneRunFeedback(out), nil
}

func (s *MemoryStore) SaveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	ctx, cancel := operationContext(ctx, OperationRunSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunSave, ctx); err != nil {
		return app.AgentRun{}, err
	}
	run, err := prepareRun(run, time.Now().UTC())
	if err != nil {
		return app.AgentRun{}, storeError(ctx, OperationRunSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationRunSave, ctx); err != nil {
		return app.AgentRun{}, err
	}
	s.runs[run.ID] = run
	s.appendEventLocked("run."+run.State, run.SessionID, run.ID, run)
	return cloneRun(run)
}

func (s *MemoryStore) GetRun(ctx context.Context, id string) (app.AgentRun, bool, error) {
	ctx, cancel := operationContext(ctx, OperationRunGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunGet, ctx); err != nil {
		return app.AgentRun{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationRunGet, ctx); err != nil {
		return app.AgentRun{}, false, err
	}
	run, ok := s.runs[id]
	if !ok {
		return app.AgentRun{}, false, nil
	}
	cloned, err := cloneRun(run)
	if err != nil {
		return app.AgentRun{}, false, storeError(ctx, OperationRunGet, StoreErrorCorrupt, err)
	}
	return cloned, true, nil
}

func (s *MemoryStore) ListRuns(ctx context.Context, sessionID string) ([]app.AgentRun, error) {
	ctx, cancel := operationContext(ctx, OperationRunList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationRunList, ctx); err != nil {
		return nil, err
	}
	out := []app.AgentRun{}
	for _, run := range s.runs {
		if sessionID == "" || run.SessionID == sessionID {
			cloned, err := cloneRun(run)
			if err != nil {
				return nil, storeError(ctx, OperationRunList, StoreErrorCorrupt, err)
			}
			out = append(out, cloned)
		}
	}
	slices.SortFunc(out, func(a, b app.AgentRun) int {
		if order := b.StartedAt.Compare(a.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) SaveModelCall(ctx context.Context, call app.ModelCall) (app.ModelCall, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallSave, ctx); err != nil {
		return app.ModelCall{}, err
	}
	call, err := prepareModelCall(call, time.Now().UTC())
	if err != nil {
		return app.ModelCall{}, storeError(ctx, OperationModelCallSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationModelCallSave, ctx); err != nil {
		return app.ModelCall{}, err
	}
	s.modelCalls[call.ID] = call
	s.appendAuditLocked("model_call."+call.Status, call.SessionID, call.RunID, "model-router", call.Model, map[string]any{
		"lane":       call.Lane,
		"profile":    call.Profile,
		"operation":  call.Operation,
		"latency_ms": call.LatencyMS,
	})
	s.appendEventLocked("model_call."+call.Status, call.SessionID, call.RunID, call)
	return call, nil
}

func (s *MemoryStore) ListModelCalls(ctx context.Context, sessionID, runID string) ([]app.ModelCall, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationModelCallList, ctx); err != nil {
		return nil, err
	}
	out := []app.ModelCall{}
	for _, call := range s.modelCalls {
		if (sessionID == "" || call.SessionID == sessionID) && (runID == "" || call.RunID == runID) {
			out = append(out, call)
		}
	}
	slices.SortFunc(out, func(a, b app.ModelCall) int {
		if order := a.StartedAt.Compare(b.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) LatestModelCallsByLane(ctx context.Context) (map[string]app.ModelCall, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallLatestByLane, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallLatestByLane, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationModelCallLatestByLane, ctx); err != nil {
		return nil, err
	}
	latest := map[string]app.ModelCall{}
	for _, call := range s.modelCalls {
		if current, ok := latest[call.Lane]; !ok || modelCallIsLater(call, current) {
			latest[call.Lane] = call
		}
	}
	return latest, nil
}

func (s *MemoryStore) SaveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallSave, ctx); err != nil {
		return app.ToolCall{}, err
	}
	call, err := prepareToolCall(call, time.Now().UTC())
	if err != nil {
		return app.ToolCall{}, storeError(ctx, OperationToolCallSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationToolCallSave, ctx); err != nil {
		return app.ToolCall{}, err
	}
	s.indexToolCallLocked(call)
	s.toolCalls[call.ID] = call
	s.appendAuditLocked("tool_call."+string(call.Status), call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
		"risk": call.Risk,
		"id":   call.ID,
	})
	s.appendEventLocked("tool_call."+string(call.Status), call.SessionID, call.RunID, call)
	return cloneToolCall(call)
}

// indexToolCallLocked keeps the per-session tool-call order (StartedAt
// ascending, ID ascending) that ListRecentToolCalls walks newest-first. It
// must run before s.toolCalls[call.ID] is replaced: the comparator resolves
// indexed IDs through s.toolCalls, so a re-saved record is located by its
// previous key first. Runtime writes are near-monotonic, so the binary search
// usually lands at the tail and the insert is a no-op move.
func (s *MemoryStore) indexToolCallLocked(call app.ToolCall) {
	compare := func(id string, target app.ToolCall) int {
		return compareToolCallsAscending(s.toolCalls[id], target)
	}
	if previous, existed := s.toolCalls[call.ID]; existed {
		if previous.SessionID == call.SessionID && compareToolCallsAscending(previous, call) == 0 {
			return
		}
		previousIDs := s.toolCallIDsBySession[previous.SessionID]
		if index, found := slices.BinarySearchFunc(previousIDs, previous, compare); found {
			s.toolCallIDsBySession[previous.SessionID] = slices.Delete(previousIDs, index, index+1)
		}
	}
	ids := s.toolCallIDsBySession[call.SessionID]
	index, _ := slices.BinarySearchFunc(ids, call, compare)
	s.toolCallIDsBySession[call.SessionID] = slices.Insert(ids, index, call.ID)
}

func compareToolCallsAscending(left, right app.ToolCall) int {
	if order := left.StartedAt.Compare(right.StartedAt); order != 0 {
		return order
	}
	return strings.Compare(left.ID, right.ID)
}

func (s *MemoryStore) GetToolCall(ctx context.Context, id string) (app.ToolCall, bool, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallGet, ctx); err != nil {
		return app.ToolCall{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationToolCallGet, ctx); err != nil {
		return app.ToolCall{}, false, err
	}
	call, ok := s.toolCalls[id]
	if !ok {
		return app.ToolCall{}, false, nil
	}
	cloned, err := cloneToolCall(call)
	if err != nil {
		return app.ToolCall{}, false, storeError(ctx, OperationToolCallGet, StoreErrorCorrupt, err)
	}
	return cloned, true, nil
}

func (s *MemoryStore) ListToolCalls(ctx context.Context, sessionID string) ([]app.ToolCall, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationToolCallList, ctx); err != nil {
		return nil, err
	}
	out := []app.ToolCall{}
	for _, call := range s.toolCalls {
		if sessionID == "" || call.SessionID == sessionID {
			cloned, err := cloneToolCall(call)
			if err != nil {
				return nil, storeError(ctx, OperationToolCallList, StoreErrorCorrupt, err)
			}
			out = append(out, cloned)
		}
	}
	slices.SortFunc(out, compareToolCallsAscending)
	return out, nil
}

func (s *MemoryStore) ListRecentToolCalls(ctx context.Context, sessionID string, cutoff time.Time, excludeRunID string, scanLimit int) ([]app.ToolCall, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallListRecent, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallListRecent, ctx); err != nil {
		return nil, err
	}
	if err := validateRecentHistoryQuery(sessionID, cutoff, scanLimit); err != nil {
		return nil, storeError(ctx, OperationToolCallListRecent, StoreErrorInvalid, err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationToolCallListRecent, ctx); err != nil {
		return nil, err
	}
	ids := s.toolCallIDsBySession[sessionID]
	out := make([]app.ToolCall, 0, min(scanLimit, len(ids)))
	for index := len(ids) - 1; index >= 0 && len(out) < scanLimit; index-- {
		call := s.toolCalls[ids[index]]
		if call.RunID == excludeRunID || call.StartedAt.After(cutoff) || call.CompletedAt == nil || call.CompletedAt.After(cutoff) {
			continue
		}
		cloned, err := cloneToolCall(call)
		if err != nil {
			return nil, storeError(ctx, OperationToolCallListRecent, StoreErrorCorrupt, err)
		}
		out = append(out, cloned)
	}
	return out, nil
}

func (s *MemoryStore) SaveEpisodeSummary(ctx context.Context, summary app.EpisodeSummary) (app.EpisodeSummary, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummarySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummarySave, ctx); err != nil {
		return app.EpisodeSummary{}, err
	}
	summary, err := prepareEpisodeSummary(summary, time.Now().UTC())
	if err != nil {
		return app.EpisodeSummary{}, storeError(ctx, OperationEpisodeSummarySave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationEpisodeSummarySave, ctx); err != nil {
		return app.EpisodeSummary{}, err
	}
	s.indexEpisodeSummaryLocked(summary)
	s.episodeSummaries[summary.ID] = summary
	s.appendAuditLocked("episode_summary.saved", summary.SessionID, summary.RunID, "runtime", summary.Outcome, map[string]any{
		"tools":            summary.Tools,
		"repair_performed": summary.RepairPerformed,
	})
	s.appendEventLocked("episode_summary.saved", summary.SessionID, summary.RunID, summary)
	return cloneEpisodeSummary(summary), nil
}

// indexEpisodeSummaryLocked keeps the per-session episode order (CreatedAt
// descending, ID ascending) that ListRecentEpisodeSummaries walks from the
// front. Like indexToolCallLocked it must run before
// s.episodeSummaries[summary.ID] is replaced.
func (s *MemoryStore) indexEpisodeSummaryLocked(summary app.EpisodeSummary) {
	compare := func(id string, target app.EpisodeSummary) int {
		return compareEpisodeSummariesNewestFirst(s.episodeSummaries[id], target)
	}
	if previous, existed := s.episodeSummaries[summary.ID]; existed {
		if previous.SessionID == summary.SessionID && compareEpisodeSummariesNewestFirst(previous, summary) == 0 {
			return
		}
		previousIDs := s.episodeIDsBySession[previous.SessionID]
		if index, found := slices.BinarySearchFunc(previousIDs, previous, compare); found {
			s.episodeIDsBySession[previous.SessionID] = slices.Delete(previousIDs, index, index+1)
		}
	}
	ids := s.episodeIDsBySession[summary.SessionID]
	index, _ := slices.BinarySearchFunc(ids, summary, compare)
	s.episodeIDsBySession[summary.SessionID] = slices.Insert(ids, index, summary.ID)
}

func compareEpisodeSummariesNewestFirst(left, right app.EpisodeSummary) int {
	if order := right.CreatedAt.Compare(left.CreatedAt); order != 0 {
		return order
	}
	return strings.Compare(left.ID, right.ID)
}

func (s *MemoryStore) ListEpisodeSummaries(ctx context.Context, sessionID string) ([]app.EpisodeSummary, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummaryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummaryList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEpisodeSummaryList, ctx); err != nil {
		return nil, err
	}
	out := []app.EpisodeSummary{}
	for _, summary := range s.episodeSummaries {
		if sessionID == "" || summary.SessionID == sessionID {
			out = append(out, cloneEpisodeSummary(summary))
		}
	}
	slices.SortFunc(out, compareEpisodeSummariesNewestFirst)
	return out, nil
}

func (s *MemoryStore) ListRecentEpisodeSummaries(ctx context.Context, sessionID string, cutoff time.Time, scanLimit int) ([]app.EpisodeSummary, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummaryListRecent, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummaryListRecent, ctx); err != nil {
		return nil, err
	}
	if err := validateRecentHistoryQuery(sessionID, cutoff, scanLimit); err != nil {
		return nil, storeError(ctx, OperationEpisodeSummaryListRecent, StoreErrorInvalid, err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEpisodeSummaryListRecent, ctx); err != nil {
		return nil, err
	}
	ids := s.episodeIDsBySession[sessionID]
	out := make([]app.EpisodeSummary, 0, min(scanLimit, len(ids)))
	for _, id := range ids {
		if len(out) >= scanLimit {
			break
		}
		summary := s.episodeSummaries[id]
		if summary.CreatedAt.After(cutoff) {
			continue
		}
		out = append(out, cloneEpisodeSummary(summary))
	}
	return out, nil
}

func (s *MemoryStore) CountVisibleRuns(ctx context.Context) (int, error) {
	ctx, cancel := operationContext(ctx, OperationRunCountVisible, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunCountVisible, ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationRunCountVisible, ctx); err != nil {
		return 0, err
	}
	count := 0
	for _, run := range s.runs {
		if session, ok := s.sessions[run.SessionID]; ok && !session.Hidden {
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) ModelCallStats(ctx context.Context) (app.ModelCallStats, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallStats, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallStats, ctx); err != nil {
		return app.ModelCallStats{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationModelCallStats, ctx); err != nil {
		return app.ModelCallStats{}, err
	}
	stats := app.ModelCallStats{}
	for _, call := range s.modelCalls {
		stats.Count++
		if modelCallFailed(call) {
			stats.FailedCount++
		}
		stats.LatencyMSTotal += call.LatencyMS
		stats.TotalTokens += call.TotalTokens
	}
	return stats, nil
}

func (s *MemoryStore) CountToolCalls(ctx context.Context) (int, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallCount, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallCount, ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationToolCallCount, ctx); err != nil {
		return 0, err
	}
	return len(s.toolCalls), nil
}

func (s *MemoryStore) CountEpisodeSummaries(ctx context.Context) (int, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummaryCount, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummaryCount, ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEpisodeSummaryCount, ctx); err != nil {
		return 0, err
	}
	return len(s.episodeSummaries), nil
}
