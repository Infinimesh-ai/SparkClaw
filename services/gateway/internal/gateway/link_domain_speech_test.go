package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
)

// uploadLinkDomainObject uploads bytes as one of the device's domain objects.
func uploadLinkDomainObject(t *testing.T, ctx context.Context, c *linkclient.Client, purpose, name, mediaType string, content []byte) domainObjectRef {
	t.Helper()
	sum := sha256.Sum256(content)
	var state contract.ObjectUploadState
	if err := c.Request(ctx, contract.OpObjectUploadBegin, contract.ObjectUploadBeginParams{UploadKey: newLinkUUID(), Name: name, Size: int64(len(content)), ContentType: mediaType, SHA256: hex.EncodeToString(sum[:]), Purpose: purpose}, &state); err != nil {
		t.Fatal(err)
	}
	for state.ObjectID == "" && state.Received < int64(len(content)) {
		end := min(int(state.Received)+contract.MaxChunkBytes, len(content))
		if err := c.Request(ctx, contract.OpObjectUploadChunk, contract.ObjectUploadChunkParams{UploadID: state.UploadID, Offset: state.Received, Data: content[state.Received:end]}, &state); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Request(ctx, contract.OpObjectUploadCommit, contract.ObjectUploadCommitParams{UploadID: state.UploadID}, &state); err != nil {
		t.Fatal(err)
	}
	return domainObjectRef{ObjectID: state.ObjectID, Version: 1, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), Purpose: purpose, Name: name, MediaType: mediaType}
}

func enableLinkSpeech(b *linkTestBackend, engine *fakeSpeechTranscriber) {
	b.server.cfg.Speech.Enabled = true
	b.server.cfg.Speech.Backend = "openai-http"
	b.server.cfg.Speech.MaxUploadBytes = 3 << 20
	b.server.cfg.Speech.TimeoutSeconds = 30
	b.server.speech = engine
}

func TestLinkRecordedSpeechReadsTheDeviceObjectAndReplaysOnce(t *testing.T) {
	b := newLinkTestBackend(t)
	conditions := contract.Conditions{Screen: contract.ScreenLarge, Microphone: true, Attachments: true}
	desk, _, drec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, conditions)
	other, _, orec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, conditions)
	writeLinkDirectory(t, b.stateDir, drec, orec)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, c := range []*linkclient.Client{desk, other} {
		c.Start(ctx)
		defer c.Close()
		if c.WaitReady(ctx) != nil {
			t.Fatal("not ready")
		}
	}
	calls := 0
	enableLinkSpeech(b, &fakeSpeechTranscriber{transcribe: func(_ context.Context, input speech.Request) (speech.Result, error) {
		calls++
		if len(input.PCM16WAV) != 44+32000 {
			t.Error("wrong audio")
		}
		return speech.Result{Text: "isolated transcript", Language: "en", Model: "controlled"}, nil
	}})
	ref := uploadLinkDomainObject(t, ctx, desk, "speech_recording", "recording.wav", "audio/wav", gatewayTestWAV(16000))
	args := map[string]any{"session_id": "11111111-1111-4111-8111-111111111111", "request_id": "voice-22222222-2222-4222-8222-222222222222", "language": "en", "audio_object": ref}
	key := newLinkUUID()
	var first, again map[string]any
	if err := linkDomainCall(ctx, desk, "speech.transcribe", args, key, &first); err != nil || first["text"] != "isolated transcript" {
		t.Fatalf("transcribe %v %v", first, err)
	}
	if err := linkDomainCall(ctx, desk, "speech.transcribe", args, key, &again); err != nil || again["id"] != first["id"] || calls != 1 {
		t.Fatalf("replay ran the provider again: %v %v (calls %d)", again, err, calls)
	}
	// Another device cannot use this device's recording.
	if err := linkDomainCall(ctx, other, "speech.transcribe", args, newLinkUUID(), nil); contract.AsError(err).Reason != "speech_audio_unavailable" || calls != 1 {
		t.Fatalf("cross-device object: %v", err)
	}
}

func TestLinkRealtimeSpeechFramesEventsAndTerminalRecovery(t *testing.T) {
	b, desk, ctx := linkDomainTestDevice(t)
	realtime := newFakeGatewayRealtimeSession()
	enableLinkSpeech(b, &fakeSpeechTranscriber{status: speech.Status{Enabled: true, Ready: true, SupportsStreaming: true},
		startRealtime: func(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error) { return realtime, nil }})
	var opened struct {
		SessionID string `json:"session_id"`
	}
	if err := linkDomainCall(ctx, desk, "speech.session.open", map[string]string{"session_id": "11111111-1111-4111-8111-111111111111", "request_id": "live-test", "language": "auto"}, newLinkUUID(), &opened); err != nil || opened.SessionID == "" {
		t.Fatalf("open %v %v", opened, err)
	}
	frame := map[string]any{"session_id": opened.SessionID, "sequence": 1, "pcm16": base64.StdEncoding.EncodeToString(make([]byte, 3200))}
	for i := 0; i < 2; i++ {
		if err := linkDomainCall(ctx, desk, "speech.session.frame", frame, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(realtime.audio) != 3200 {
		t.Fatalf("a repeated frame reached the provider: %d bytes", len(realtime.audio))
	}
	type page struct {
		Events []speechEvent `json:"events"`
		Closed bool          `json:"closed"`
	}
	poll := func(want int) page {
		t.Helper()
		var p page
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if err := linkDomainCall(ctx, desk, "speech.session.events", map[string]any{"session_id": opened.SessionID, "after": 0}, "", &p); err != nil {
				t.Fatal(err)
			}
			if len(p.Events) >= want {
				return p
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("events %+v", p)
		return p
	}
	if p := poll(2); p.Events[1].Event.Event != "partial" {
		t.Fatalf("no partial %+v", p)
	}
	if err := linkDomainCall(ctx, desk, "speech.session.finish", map[string]any{"session_id": opened.SessionID, "last_sequence": 1, "total_samples": 1600, "reason": "user_stop"}, newLinkUUID(), nil); err != nil {
		t.Fatal(err)
	}
	final := poll(3)
	if final.Events[2].Event.Event != "final" || final.Events[2].Event.DurationMS != 100 {
		t.Fatalf("final %+v", final)
	}
	// The session leaves memory (as after a restart); its stored terminal
	// still answers.
	l := b.server.link.speech
	l.mu.Lock()
	delete(l.sessions, opened.SessionID)
	l.mu.Unlock()
	if p := poll(3); len(p.Events) != 3 || !p.Closed {
		t.Fatalf("recovered %+v", p)
	}
	// A session whose terminal was never stored is unknown, not absent.
	if err := l.prepare(desk.Hello().Principal.DeviceID, "speech_lost"); err != nil {
		t.Fatal(err)
	}
	if err := linkDomainCall(ctx, desk, "speech.session.events", map[string]any{"session_id": "speech_lost"}, "", nil); contract.AsError(err).Code != contract.CodeOutcomeUnknown {
		t.Fatal(err)
	}
	if err := linkDomainCall(ctx, desk, "speech.session.events", map[string]any{"session_id": "speech_none"}, "", nil); contract.AsError(err).Reason != "speech_session_not_found" {
		t.Fatal(err)
	}
}

type linkUncertainAudioProvider struct {
	*fakeGatewayRealtimeSession
	calls int
}

func (p *linkUncertainAudioProvider) WriteAudio(context.Context, uint32, []byte) error {
	p.calls++
	return errors.New("provider disconnected after accepting bytes")
}

func TestLinkUncertainAudioFrameStopsInsteadOfReplayingProviderBytes(t *testing.T) {
	b, desk, ctx := linkDomainTestDevice(t)
	provider := &linkUncertainAudioProvider{fakeGatewayRealtimeSession: newFakeGatewayRealtimeSession()}
	enableLinkSpeech(b, &fakeSpeechTranscriber{status: speech.Status{Enabled: true, Ready: true, SupportsStreaming: true},
		startRealtime: func(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error) { return provider, nil }})
	var opened struct {
		SessionID string `json:"session_id"`
	}
	if err := linkDomainCall(ctx, desk, "speech.session.open", map[string]string{"session_id": "11111111-1111-4111-8111-111111111111", "request_id": "live-test"}, newLinkUUID(), &opened); err != nil {
		t.Fatal(err)
	}
	frame := map[string]any{"session_id": opened.SessionID, "sequence": 1, "pcm16": base64.StdEncoding.EncodeToString(make([]byte, 3200))}
	if err := linkDomainCall(ctx, desk, "speech.session.frame", frame, "", nil); err == nil {
		t.Fatal("unknown delivery accepted")
	}
	if err := linkDomainCall(ctx, desk, "speech.session.frame", frame, "", nil); contract.AsError(err).Reason != "speech_session_closed" {
		t.Fatalf("closed stream resumed: %v", err)
	}
	if provider.calls != 1 {
		t.Fatal("uncertain audio was sent twice")
	}
}

func TestLinkSpeechNeedsTheMicrophoneCondition(t *testing.T) {
	b := newLinkTestBackend(t)
	phone, _, rec, _ := newLinkTestDevice(t, b, contract.ClassPhone, contract.Conditions{Screen: contract.ScreenSmall})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	phone.Start(ctx)
	defer phone.Close()
	if phone.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	enableLinkSpeech(b, &fakeSpeechTranscriber{})
	if err := linkDomainCall(ctx, phone, "speech.session.open", map[string]string{"session_id": "11111111-1111-4111-8111-111111111111", "request_id": "x"}, newLinkUUID(), nil); contract.AsError(err).Code != contract.CodeUnsupportedCapability {
		t.Fatal(err)
	}
}
