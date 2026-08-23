package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func (s *Server) getTrace(w http.ResponseWriter, r *http.Request) {
	runID := filepath.Base(r.PathValue("run_id"))
	path := filepath.Join(s.cfg.Storage.TraceDir, runID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("trace not found"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	runs, err := s.store.ListRuns(r.Context(), "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	storedApprovals, err := s.store.ListApprovals(r.Context(), "")
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	out := make([]app.TraceMetadata, 0, min(limit, len(runs)))
	for _, run := range runs {
		if len(out) >= limit {
			break
		}
		storedToolCalls, err := s.store.ListToolCalls(r.Context(), run.SessionID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		toolCalls := toolCallsForRun(storedToolCalls, run.ID)
		approvals := approvalsForRun(storedApprovals, run.ID)
		modelCalls, err := s.store.ListModelCalls(r.Context(), run.SessionID, run.ID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		messages, err := s.store.ListMessages(r.Context(), run.SessionID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		meta := app.TraceMetadata{
			RunID:          run.ID,
			SessionID:      run.SessionID,
			State:          run.State,
			Risk:           run.Risk,
			ModelLane:      run.ModelLane,
			Summary:        run.Summary,
			StartedAt:      run.StartedAt,
			CompletedAt:    run.CompletedAt,
			MessageCount:   len(messages),
			ToolCallCount:  len(toolCalls),
			ApprovalCount:  len(approvals),
			ModelCallCount: len(modelCalls),
		}
		if artifactURI, artifactPath := s.traceArtifactRef(run.ID); artifactURI != "" || artifactPath != "" {
			meta.ArtifactURI = artifactURI
			meta.ArtifactPath = artifactPath
		}
		out = append(out, meta)
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": out})
}

func (s *Server) traceArtifactRef(runID string) (string, string) {
	raw, err := os.ReadFile(filepath.Join(s.cfg.Storage.TraceDir, filepath.Base(runID)+".json"))
	if err != nil {
		return "", ""
	}
	var current trace.RunTrace
	if err := json.Unmarshal(raw, &current); err != nil || current.Artifact == nil {
		return "", ""
	}
	return current.Artifact.URI, current.Artifact.Path
}

func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	ownerID := queryOwnerID(r)
	objects := []app.ArtifactObject{}
	storedObjects, err := s.store.ListArtifactObjects(r.Context(), 0)
	if err != nil {
		writeArtifactMetadataStoreError(w, err)
		return
	}
	for _, object := range storedObjects {
		visible, err := s.artifactVisibleToOwner(r.Context(), object, ownerID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		if visible {
			objects = append(objects, object)
		}
		if limit > 0 && len(objects) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": objects})
}

func writeArtifactMetadataStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("artifact metadata request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("artifact metadata operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("artifact metadata service is unavailable"))
	}
}

func (s *Server) refreshTrace(ctx context.Context, runID string) {
	if s.traces == nil || runID == "" {
		return
	}
	run, ok, err := s.store.GetRun(ctx, runID)
	if err != nil {
		slog.Warn("trace run refresh unavailable", "run_id", runID, "code", store.StoreErrorCodeOf(err))
		return
	}
	if !ok {
		return
	}
	current := trace.RunTrace{}
	if raw, err := os.ReadFile(filepath.Join(s.cfg.Storage.TraceDir, filepath.Base(runID)+".json")); err == nil {
		_ = json.Unmarshal(raw, &current)
	}
	if current.Episode == nil {
		episodes, err := s.store.ListEpisodeSummaries(ctx, run.SessionID)
		if err != nil {
			slog.Warn("trace episode refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
			return
		}
		for _, episode := range episodes {
			if episode.RunID == run.ID {
				current.Episode = &episode
				break
			}
		}
	}
	current.Run = run
	current.ModelCalls, err = s.store.ListModelCalls(ctx, run.SessionID, run.ID)
	if err != nil {
		slog.Warn("trace model call refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	storedToolCalls, err := s.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		slog.Warn("trace tool call refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	current.ToolCalls = toolCallsForRun(storedToolCalls, run.ID)
	storedApprovals, err := s.store.ListApprovals(ctx, "")
	if err != nil {
		slog.Warn("trace approval refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	current.Approvals = approvalsForRun(storedApprovals, run.ID)
	current.Feedback, err = s.store.ListRunFeedback(ctx, run.ID)
	if err != nil {
		slog.Warn("trace feedback refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	messages, err := s.store.ListMessages(ctx, run.SessionID)
	if err != nil {
		slog.Warn("trace message refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	current.Messages = messages
	current.Audit, err = s.store.ListAudit(ctx, run.SessionID)
	if err != nil {
		slog.Warn("trace audit refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	object, _ := s.traces.WriteRunObject(ctx, current)
	if object != nil {
		if _, err := s.store.SaveArtifactObject(ctx, app.ArtifactObject{
			ID:          app.NewID("obj"),
			Kind:        "trace",
			RunID:       run.ID,
			SessionID:   run.SessionID,
			Backend:     object.Backend,
			Bucket:      object.Bucket,
			Key:         object.Key,
			URI:         object.URI,
			Path:        object.Path,
			ContentType: object.ContentType,
			Bytes:       object.Bytes,
			CreatedAt:   time.Now().UTC(),
		}); err != nil {
			slog.Warn("trace artifact metadata unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		}
	}
}

func toolCallsForRun(calls []app.ToolCall, runID string) []app.ToolCall {
	out := []app.ToolCall{}
	for _, call := range calls {
		if call.RunID == runID {
			out = append(out, call)
		}
	}
	return out
}

func approvalsForRun(approvals []app.Approval, runID string) []app.Approval {
	out := []app.Approval{}
	for _, approval := range approvals {
		if approval.RunID == runID {
			out = append(out, approval)
		}
	}
	return out
}

func modelCallFromChat(sessionID, runID, operation string, chat modelrouter.ChatResult, err error, started, completed time.Time) app.ModelCall {
	status := "completed"
	errorText := ""
	if err != nil {
		status = "failed"
		errorText = err.Error()
	}
	if chat.Lane == "" {
		chat.Lane = "unknown"
	}
	if chat.Profile == "" {
		chat.Profile = "unknown"
	}
	if chat.Model == "" {
		chat.Model = "unknown"
	}
	return app.ModelCall{
		ID:             app.NewID("mcall"),
		SessionID:      sessionID,
		RunID:          runID,
		Lane:           chat.Lane,
		Profile:        chat.Profile,
		Model:          chat.Model,
		Operation:      operation,
		Mock:           chat.Mock,
		Fallback:       chat.Fallback,
		Status:         status,
		PromptTokens:   chat.PromptTokens,
		ResponseTokens: chat.ResponseTokens,
		TotalTokens:    chat.TotalTokens,
		LatencyMS:      completed.Sub(started).Milliseconds(),
		Error:          errorText,
		StartedAt:      started,
		CompletedAt:    &completed,
	}
}
