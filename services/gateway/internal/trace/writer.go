package trace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

type Writer struct {
	dir            string
	artifacts      artifact.Store
	redactPatterns []string
}

type RunTrace struct {
	Run        app.AgentRun           `json:"run"`
	Model      modelrouter.ChatResult `json:"model"`
	ModelCalls []app.ModelCall        `json:"model_calls,omitempty"`
	Messages   []app.Message          `json:"messages"`
	ToolCalls  []app.ToolCall         `json:"tool_calls"`
	Approvals  []app.Approval         `json:"approvals"`
	Feedback   []app.RunFeedback      `json:"feedback,omitempty"`
	Audit      []app.AuditEvent       `json:"audit"`
	Episode    *app.EpisodeSummary    `json:"episode,omitempty"`
	Artifact   *artifact.Object       `json:"artifact,omitempty"`
}

func NewWriter(dir string) *Writer {
	cfg := config.Default()
	return &Writer{dir: dir, redactPatterns: traceRedactPatterns(cfg)}
}

func NewWriterFromConfig(cfg config.Config) *Writer {
	return &Writer{
		dir:            cfg.Storage.TraceDir,
		artifacts:      artifact.NewStore(cfg.Storage),
		redactPatterns: traceRedactPatterns(cfg),
	}
}

func (w *Writer) WriteRun(ctx context.Context, trace RunTrace) error {
	_, err := w.WriteRunObject(ctx, trace)
	return err
}

func (w *Writer) WriteRunObject(ctx context.Context, trace RunTrace) (*artifact.Object, error) {
	if w == nil || w.dir == "" {
		return nil, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return nil, err
	}
	safeTrace := w.redactedTrace(trace)
	raw, err := json.MarshalIndent(safeTrace, "", "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(w.dir, safeTrace.Run.ID+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, err
	}
	if w.artifacts == nil {
		return nil, nil
	}
	object, err := w.artifacts.Put(ctx, filepath.Join("traces", safeTrace.Run.ID+".json"), "application/json", raw)
	if err != nil {
		return nil, nil
	}
	safeTrace.Artifact = &object
	raw, err = json.MarshalIndent(safeTrace, "", "  ")
	if err != nil {
		return &object, err
	}
	return &object, os.WriteFile(path, raw, 0o644)
}

func (w *Writer) redactedTrace(in RunTrace) RunTrace {
	if len(w.redactPatterns) == 0 {
		return in
	}
	out := in
	out.Run.Summary = redactString(out.Run.Summary, w.redactPatterns)
	out.Model.Content = redactString(out.Model.Content, w.redactPatterns)
	out.Model.ErrorNote = redactString(out.Model.ErrorNote, w.redactPatterns)
	out.ModelCalls = make([]app.ModelCall, len(in.ModelCalls))
	for i, call := range in.ModelCalls {
		out.ModelCalls[i] = call
		out.ModelCalls[i].Error = redactString(call.Error, w.redactPatterns)
	}
	out.Messages = make([]app.Message, len(in.Messages))
	for i, message := range in.Messages {
		out.Messages[i] = message
		out.Messages[i].Content = redactString(message.Content, w.redactPatterns)
	}
	out.ToolCalls = make([]app.ToolCall, len(in.ToolCalls))
	for i, call := range in.ToolCalls {
		out.ToolCalls[i] = call
		out.ToolCalls[i].Arguments = redactMap(call.Arguments, w.redactPatterns)
		out.ToolCalls[i].Result = redactAny(call.Result, w.redactPatterns)
		out.ToolCalls[i].Error = redactString(call.Error, w.redactPatterns)
	}
	out.Approvals = make([]app.Approval, len(in.Approvals))
	for i, approval := range in.Approvals {
		out.Approvals[i] = approval
		out.Approvals[i].Summary = redactString(approval.Summary, w.redactPatterns)
		out.Approvals[i].Reason = redactString(approval.Reason, w.redactPatterns)
		out.Approvals[i].Resources = redactStringSlice(approval.Resources, w.redactPatterns)
		out.Approvals[i].Arguments = redactMap(approval.Arguments, w.redactPatterns)
		out.Approvals[i].ResolutionNote = redactString(approval.ResolutionNote, w.redactPatterns)
	}
	out.Feedback = make([]app.RunFeedback, len(in.Feedback))
	for i, feedback := range in.Feedback {
		out.Feedback[i] = feedback
		out.Feedback[i].Note = redactString(feedback.Note, w.redactPatterns)
		out.Feedback[i].Correction = redactString(feedback.Correction, w.redactPatterns)
	}
	out.Audit = make([]app.AuditEvent, len(in.Audit))
	for i, event := range in.Audit {
		out.Audit[i] = event
		out.Audit[i].Summary = redactString(event.Summary, w.redactPatterns)
		out.Audit[i].Fields = redactMap(event.Fields, w.redactPatterns)
	}
	if in.Episode != nil {
		episode := *in.Episode
		episode.Summary = redactString(episode.Summary, w.redactPatterns)
		episode.Outcome = redactString(episode.Outcome, w.redactPatterns)
		episode.Failures = redactStringSlice(episode.Failures, w.redactPatterns)
		out.Episode = &episode
	}
	return out
}
