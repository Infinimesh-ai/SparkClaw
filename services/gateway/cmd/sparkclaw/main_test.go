package main

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/telegram"
)

type recordingSpeechTranscriber struct {
	status speech.Status
	result speech.Result
	input  speech.Request
	calls  int
}

func (t *recordingSpeechTranscriber) Status(context.Context) speech.Status {
	return t.status
}

func (t *recordingSpeechTranscriber) Transcribe(_ context.Context, request speech.Request) (speech.Result, error) {
	t.calls++
	t.input = request
	return t.result, nil
}

func (t *recordingSpeechTranscriber) Close() error {
	return nil
}

func TestTelegramSpeechTranscriberMapsProductionSpeechRequest(t *testing.T) {
	backend := &recordingSpeechTranscriber{
		status: speech.Status{Enabled: true, Ready: true, State: speech.StateReady},
		result: speech.Result{Text: "mapped transcript"},
	}
	adapter := telegramSpeechTranscriber{transcriber: backend}
	if err := adapter.Available(context.Background()); err != nil {
		t.Fatal(err)
	}

	audio := []byte{1, 2, 3, 4}
	text, err := adapter.Transcribe(context.Background(), telegram.VoiceTranscriptionRequest{
		RequestID:  "telegram-voice-1",
		SessionID:  "session-1",
		Language:   "zh-CN",
		PCM16WAV:   audio,
		DurationMS: 1250,
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "mapped transcript" || backend.calls != 1 {
		t.Fatalf("unexpected adapter result: text=%q calls=%d", text, backend.calls)
	}
	if backend.input.RequestID != "telegram-voice-1" || backend.input.SessionID != "session-1" || backend.input.Language != "zh-CN" || backend.input.DurationMS != 1250 {
		t.Fatalf("request metadata was not preserved: %#v", backend.input)
	}
	if len(backend.input.PCM16WAV) != len(audio) {
		t.Fatalf("audio payload was not forwarded: %#v", backend.input.PCM16WAV)
	}
}

func TestTelegramSpeechTranscriberReportsDisabledBeforeDownload(t *testing.T) {
	backend := &recordingSpeechTranscriber{status: speech.Status{Enabled: false, Ready: false, State: speech.StateDisabled}}
	adapter := telegramSpeechTranscriber{transcriber: backend}
	err := adapter.Available(context.Background())
	code, retryable := speech.ErrorDetails(err)
	if code != speech.CodeDisabled || retryable {
		t.Fatalf("unexpected disabled status: code=%q retryable=%v err=%v", code, retryable, err)
	}
	if backend.calls != 0 {
		t.Fatalf("disabled adapter invoked transcription: %d", backend.calls)
	}
}
