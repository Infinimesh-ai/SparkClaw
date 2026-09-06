package speech

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

type ModelCallRecorder interface {
	SaveModelCall(context.Context, app.ModelCall) (app.ModelCall, error)
}

type modelCallRecordingTranscriber struct {
	transcriber Transcriber
	recorder    ModelCallRecorder
	backend     string
	model       string
}

// WithModelCallRecording records every batch invocation and realtime session
// without changing the shared Transcriber contract used by Gateway and connectors.
func WithModelCallRecording(transcriber Transcriber, recorder ModelCallRecorder, cfg config.SpeechConfig) Transcriber {
	if transcriber == nil || recorder == nil {
		return transcriber
	}
	if _, ok := transcriber.(*modelCallRecordingTranscriber); ok {
		return transcriber
	}
	return &modelCallRecordingTranscriber{
		transcriber: transcriber,
		recorder:    recorder,
		backend:     strings.TrimSpace(cfg.Backend),
		model:       strings.TrimSpace(cfg.Model),
	}
}

func (t *modelCallRecordingTranscriber) Status(ctx context.Context) Status {
	return t.transcriber.Status(ctx)
}

func (t *modelCallRecordingTranscriber) Transcribe(ctx context.Context, input Request) (Result, error) {
	started := time.Now().UTC()
	result, err := t.transcriber.Transcribe(ctx, input)
	t.record(ctx, input.SessionID, "speech_transcription", result.Model, started, err)
	return result, err
}

func (t *modelCallRecordingTranscriber) StartRealtime(ctx context.Context, input RealtimeRequest) (RealtimeSession, error) {
	started := time.Now().UTC()
	session, err := t.transcriber.StartRealtime(ctx, input)
	if err != nil {
		t.record(ctx, input.SessionID, "speech_realtime", "", started, err)
		return nil, err
	}
	if session == nil {
		err = errors.New("speech transcriber returned a nil realtime session")
		t.record(ctx, input.SessionID, "speech_realtime", "", started, err)
		return nil, err
	}
	return &modelCallRecordingRealtimeSession{
		RealtimeSession: session,
		owner:           t,
		sessionID:       input.SessionID,
		started:         started,
	}, nil
}

func (t *modelCallRecordingTranscriber) Close() error {
	return t.transcriber.Close()
}

func (t *modelCallRecordingTranscriber) record(ctx context.Context, sessionID, operation, model string, started time.Time, callErr error) {
	completed := time.Now().UTC()
	status := "completed"
	errorText := ""
	if callErr != nil {
		status = "failed"
		errorText = callErr.Error()
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = t.model
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	if _, err := t.recorder.SaveModelCall(ctx, app.ModelCall{
		ID:          app.NewID("mcall"),
		SessionID:   strings.TrimSpace(sessionID),
		Lane:        string(modelcapacity.LaneASR),
		Profile:     t.backend,
		Model:       model,
		Operation:   operation,
		Status:      status,
		LatencyMS:   completed.Sub(started).Milliseconds(),
		Error:       errorText,
		StartedAt:   started,
		CompletedAt: &completed,
	}); err != nil {
		slog.Warn("speech model call recording failed", "operation", operation, "error", err)
	}
}

type modelCallRecordingRealtimeSession struct {
	RealtimeSession
	owner     *modelCallRecordingTranscriber
	sessionID string
	started   time.Time
	recorded  sync.Once
}

func (s *modelCallRecordingRealtimeSession) WriteAudio(ctx context.Context, sequence uint32, pcm16 []byte) error {
	err := s.RealtimeSession.WriteAudio(ctx, sequence, pcm16)
	if err != nil {
		s.record(ctx, "", err)
	}
	return err
}

func (s *modelCallRecordingRealtimeSession) Finish(ctx context.Context, lastSequence uint32, capturedMS int64, reason string) error {
	err := s.RealtimeSession.Finish(ctx, lastSequence, capturedMS, reason)
	if err != nil {
		s.record(ctx, "", err)
	}
	return err
}

func (s *modelCallRecordingRealtimeSession) Cancel(ctx context.Context, lastSequence uint32) error {
	err := s.RealtimeSession.Cancel(ctx, lastSequence)
	if err != nil {
		s.record(ctx, "", err)
	} else {
		s.record(ctx, "", NewError(CodeCancelled, "realtime speech session was cancelled", true, nil))
	}
	return err
}

func (s *modelCallRecordingRealtimeSession) ReadEvent(ctx context.Context) (RealtimeEvent, error) {
	event, err := s.RealtimeSession.ReadEvent(ctx)
	if err != nil {
		s.record(ctx, "", err)
		return RealtimeEvent{}, err
	}
	switch event.Event {
	case "final":
		s.record(ctx, event.Model, nil)
	case "fallback", "error":
		code := strings.TrimSpace(event.Code)
		if code == "" {
			code = CodeInferenceFailed
		}
		s.record(ctx, event.Model, NewError(code, code, event.Retryable, nil))
	}
	return event, nil
}

func (s *modelCallRecordingRealtimeSession) Close() error {
	err := s.RealtimeSession.Close()
	if err != nil {
		s.record(context.Background(), "", err)
	} else {
		s.record(context.Background(), "", NewError(CodeCancelled, "realtime speech session closed before completion", true, nil))
	}
	return err
}

func (s *modelCallRecordingRealtimeSession) record(ctx context.Context, model string, err error) {
	s.recorded.Do(func() {
		s.owner.record(ctx, s.sessionID, "speech_realtime", model, s.started, err)
	})
}
