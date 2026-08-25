package speech

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type recordingModelCallRepository struct {
	mu    sync.Mutex
	calls []app.ModelCall
	ctxs  []context.Context
}

func (r *recordingModelCallRepository) SaveModelCall(ctx context.Context, call app.ModelCall) (app.ModelCall, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	r.ctxs = append(r.ctxs, ctx)
	return call, nil
}

func (r *recordingModelCallRepository) snapshot() ([]app.ModelCall, []context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]app.ModelCall(nil), r.calls...), append([]context.Context(nil), r.ctxs...)
}

type recordingTestTranscriber struct {
	result   Result
	err      error
	realtime RealtimeSession
	startErr error
}

func (t *recordingTestTranscriber) Status(context.Context) Status { return Status{} }
func (t *recordingTestTranscriber) Transcribe(context.Context, Request) (Result, error) {
	return t.result, t.err
}
func (t *recordingTestTranscriber) StartRealtime(context.Context, RealtimeRequest) (RealtimeSession, error) {
	return t.realtime, t.startErr
}
func (t *recordingTestTranscriber) Close() error { return nil }

func TestModelCallRecordingTranscriberRecordsBatchSuccessAndFailure(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		result     Result
		err        error
		wantStatus string
		wantModel  string
	}{
		{name: "success", result: Result{Text: "draft", Model: "reported-asr"}, wantStatus: "completed", wantModel: "reported-asr"},
		{name: "failure", err: NewError(CodeTimeout, "speech timed out", true, context.DeadlineExceeded), wantStatus: "failed", wantModel: "configured-asr"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &recordingModelCallRepository{}
			inner := &recordingTestTranscriber{result: testCase.result, err: testCase.err}
			transcriber := WithModelCallRecording(inner, repository, config.SpeechConfig{Backend: "openai-http", Model: "configured-asr"})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, _ = transcriber.Transcribe(ctx, Request{SessionID: "session-a"})

			calls, contexts := repository.snapshot()
			if len(calls) != 1 {
				t.Fatalf("recorded calls = %#v", calls)
			}
			call := calls[0]
			if call.Lane != "asr" || call.Profile != "openai-http" || call.Model != testCase.wantModel ||
				call.Operation != "speech_transcription" || call.Status != testCase.wantStatus || call.SessionID != "session-a" ||
				call.CompletedAt == nil || call.StartedAt.IsZero() || call.LatencyMS < 0 {
				t.Fatalf("unexpected model call: %#v", call)
			}
			if testCase.err == nil && call.Error != "" {
				t.Fatalf("successful call retained an error: %#v", call)
			}
			if testCase.err != nil && call.Error == "" {
				t.Fatalf("failed call omitted its error: %#v", call)
			}
			if contexts[0].Err() != nil {
				t.Fatalf("recording reused cancelled request context: %v", contexts[0].Err())
			}
		})
	}
}

type recordingTestRealtimeSession struct {
	event RealtimeEvent
	err   error
}

func (s *recordingTestRealtimeSession) ReadyEvent() RealtimeEvent {
	return RealtimeEvent{Event: "ready"}
}
func (s *recordingTestRealtimeSession) WriteAudio(context.Context, uint32, []byte) error {
	return nil
}
func (s *recordingTestRealtimeSession) Finish(context.Context, uint32, int64, string) error {
	return nil
}
func (s *recordingTestRealtimeSession) Cancel(context.Context, uint32) error { return nil }
func (s *recordingTestRealtimeSession) ReadEvent(context.Context) (RealtimeEvent, error) {
	return s.event, s.err
}
func (s *recordingTestRealtimeSession) Close() error { return nil }

func TestModelCallRecordingTranscriberRecordsOneRealtimeCallPerSession(t *testing.T) {
	repository := &recordingModelCallRepository{}
	innerSession := &recordingTestRealtimeSession{event: RealtimeEvent{Event: "final", Model: "live-asr"}}
	inner := &recordingTestTranscriber{realtime: innerSession}
	transcriber := WithModelCallRecording(inner, repository, config.SpeechConfig{Backend: "openai-http", Model: "configured-asr"})

	session, err := transcriber.StartRealtime(context.Background(), RealtimeRequest{SessionID: "session-live"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ReadEvent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	calls, _ := repository.snapshot()
	if len(calls) != 1 || calls[0].Lane != "asr" || calls[0].Operation != "speech_realtime" ||
		calls[0].Status != "completed" || calls[0].Model != "live-asr" || calls[0].SessionID != "session-live" {
		t.Fatalf("unexpected realtime model calls: %#v", calls)
	}
}

func TestModelCallRecordingTranscriberRecordsRealtimeStartFailure(t *testing.T) {
	repository := &recordingModelCallRepository{}
	inner := &recordingTestTranscriber{startErr: errors.New("dial failed")}
	transcriber := WithModelCallRecording(inner, repository, config.SpeechConfig{Backend: "openai-http", Model: "configured-asr"})

	if _, err := transcriber.StartRealtime(context.Background(), RealtimeRequest{SessionID: "session-live"}); err == nil {
		t.Fatal("realtime start unexpectedly succeeded")
	}
	calls, _ := repository.snapshot()
	if len(calls) != 1 || calls[0].Status != "failed" || calls[0].Error != "dial failed" {
		t.Fatalf("unexpected realtime failure call: %#v", calls)
	}
}
