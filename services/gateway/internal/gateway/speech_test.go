package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type fakeSpeechTranscriber struct {
	status        speech.Status
	result        speech.Result
	err           error
	input         speech.Request
	statusCalls   int
	transcribe    func(context.Context, speech.Request) (speech.Result, error)
	startRealtime func(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error)
}

func (f *fakeSpeechTranscriber) Status(context.Context) speech.Status {
	f.statusCalls++
	return f.status
}

func (f *fakeSpeechTranscriber) Transcribe(ctx context.Context, input speech.Request) (speech.Result, error) {
	f.input = input
	if f.transcribe != nil {
		return f.transcribe(ctx, input)
	}
	return f.result, f.err
}

func (f *fakeSpeechTranscriber) StartRealtime(ctx context.Context, input speech.RealtimeRequest) (speech.RealtimeSession, error) {
	if f.startRealtime != nil {
		return f.startRealtime(ctx, input)
	}
	return nil, speech.NewError(speech.CodeUnavailable, "realtime speech is unavailable", true, nil)
}

func (f *fakeSpeechTranscriber) Close() error {
	return nil
}

func TestSpeechStatusIsExplicitlyDisabledWithoutInjectedTranscriber(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/speech/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status speech.Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.Ready || status.State != speech.StateDisabled {
		t.Fatalf("unexpected disabled status: %#v", status)
	}
}

func TestPublicConfigDoesNotExposeSpeechDestination(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Speech map[string]any `json:"speech"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Speech["model"] != "sparkclaw-asr" || body.Speech["backend"] != "openai-http" {
		t.Fatalf("public speech identity missing: %#v", body.Speech)
	}
	if _, ok := body.Speech["base_url"]; ok {
		t.Fatalf("public config exposed speech destination: %#v", body.Speech)
	}
	if _, ok := body.Speech["allowed_hosts"]; ok {
		t.Fatalf("public config exposed speech allowlist: %#v", body.Speech)
	}
}

func TestSpeechTranscriptionReturnsDraftTextWithoutCreatingMessageOrArtifact(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Voice input")
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	fake := &fakeSpeechTranscriber{
		status: speech.Status{
			Enabled: true, Ready: true, State: speech.StateReady, Backend: "openai-http", Model: "fake-asr",
			AcceptedContentTypes: []string{"audio/wav"}, MaxAudioSeconds: 60, MaxUploadBytes: 3 << 20,
		},
		result: speech.Result{Text: "private transcript text", Language: "en", Model: "fake-asr", InferenceMS: 12},
	}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	req := newSpeechRequest(t, ts.URL, session.ID, "voice-request-1", "en", gatewayTestWAV(8000))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transcription returned %d", resp.StatusCode)
	}
	var result struct {
		Text          string `json:"text"`
		AudioRetained bool   `json:"audio_retained"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Text != "private transcript text" || result.AudioRetained {
		t.Fatalf("unexpected result: %#v", result)
	}
	if fake.input.SessionID != session.ID || fake.input.RequestID != "voice-request-1" || fake.input.DurationMS != 500 {
		t.Fatalf("unexpected transcriber input: %#v", fake.input)
	}
	if messages := st.ListMessages(session.ID); len(messages) != 0 {
		t.Fatalf("speech transcription must not create messages: %#v", messages)
	}
	if artifacts := st.ListArtifactObjects(0); len(artifacts) != 0 {
		t.Fatalf("speech transcription must not create artifacts: %#v", artifacts)
	}
	if runs := st.ListRuns(session.ID); len(runs) != 0 {
		t.Fatalf("speech transcription must not create agent runs: %#v", runs)
	}
	if calls := st.ListToolCalls(session.ID); len(calls) != 0 {
		t.Fatalf("speech transcription must not create tool calls: %#v", calls)
	}
	audits := st.ListAudit(session.ID)
	speechAudits := make([]any, 0, 2)
	startedAudit := false
	completedAudit := false
	for _, event := range audits {
		if event.Actor == "speech" {
			speechAudits = append(speechAudits, event)
			startedAudit = startedAudit || event.Type == "speech.transcription.started"
			completedAudit = completedAudit || event.Type == "speech.transcription.completed"
		}
	}
	if len(speechAudits) != 2 || !startedAudit || !completedAudit {
		t.Fatalf("unexpected speech audits: %#v", audits)
	}
	rawAudit, _ := json.Marshal(speechAudits)
	if bytes.Contains(rawAudit, []byte("private transcript text")) {
		t.Fatalf("transcript leaked into audit: %s", rawAudit)
	}
}

func TestSpeechTranscriptionRejectsNonCanonicalWAV(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Voice input")
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	fake := &fakeSpeechTranscriber{status: speech.Status{
		Enabled: true, Ready: true, State: speech.StateReady, Backend: "openai-http",
		AcceptedContentTypes: []string{"audio/wav"}, MaxAudioSeconds: 60, MaxUploadBytes: 3 << 20,
	}}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	req := newSpeechRequest(t, ts.URL, session.ID, "voice-request-2", "auto", []byte("not a wave file"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("invalid WAV returned %d", resp.StatusCode)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != speech.CodeUnsupported {
		t.Fatalf("unexpected error code: %#v", body)
	}
}

func TestSpeechTranscriptionRejectsUnexpectedFileField(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Voice input")
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	fake := &fakeSpeechTranscriber{status: speech.Status{
		Enabled: true, Ready: true, State: speech.StateReady, Backend: "openai-http",
		AcceptedContentTypes: []string{"audio/wav"}, MaxAudioSeconds: 60, MaxUploadBytes: 3 << 20,
	}}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	req := newSpeechRequestWithExtraFile(t, ts.URL, session.ID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected file field returned %d", resp.StatusCode)
	}
}

func TestSpeechTranscriptionRecordsCancellationWithoutTranscript(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Voice cancellation")
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	fake := &fakeSpeechTranscriber{
		status: speech.Status{
			Enabled: true, Ready: true, State: speech.StateReady, Backend: "openai-http",
			AcceptedContentTypes: []string{"audio/wav"}, MaxAudioSeconds: 60, MaxUploadBytes: 3 << 20,
		},
		err: speech.NewError(speech.CodeCancelled, "cancelled", true, context.Canceled),
	}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.DefaultClient.Do(newSpeechRequest(t, ts.URL, session.ID, "voice-request-cancel", "auto", gatewayTestWAV(8000)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("cancelled transcription returned %d", resp.StatusCode)
	}
	found := false
	for _, event := range st.ListAudit(session.ID) {
		if event.Type == "speech.transcription.cancelled" {
			found = true
			if _, ok := event.Fields["text"]; ok {
				t.Fatalf("cancel audit contains transcript field: %#v", event)
			}
		}
	}
	if !found {
		t.Fatalf("missing cancellation audit: %#v", st.ListAudit(session.ID))
	}
}

func TestSpeechTranscriptionUsesInferenceAsReadinessAuthority(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Voice readiness")
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	fake := &fakeSpeechTranscriber{
		status: speech.Status{Enabled: true, Ready: false, State: speech.StateUnavailable, Reason: "stale health failure"},
		result: speech.Result{Text: "actual inference succeeded", Model: "test-asr"},
	}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.DefaultClient.Do(newSpeechRequest(t, ts.URL, session.ID, "voice-readiness", "auto", gatewayTestWAV(8000)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transcription with successful inference returned %d", resp.StatusCode)
	}
	if fake.statusCalls != 0 {
		t.Fatalf("transcription performed %d prerequisite health checks", fake.statusCalls)
	}
}

func TestSpeechTranscriptionRejectsSessionOwnedByAnotherPrincipal(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	cfg.Gateway.APIToken = "default-owner-token"
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "Other owner voice", "owner-other", cfg.Workspaces.DefaultRoot, "webchat", false)
	pairing, err := st.SavePairingCode(t.Context(), app.PairingCode{
		ID: "client-requester-pair", CodeHash: "client-requester-pair-hash", Status: "pending", ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = st.ClaimPairingCode(t.Context(), pairing.ID, app.Client{
		ID:        "client-requester",
		OwnerID:   "owner-requester",
		ActorID:   "owner-requester",
		Name:      "Requester",
		TokenHash: hashSecret("requester-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	fake := &fakeSpeechTranscriber{result: speech.Result{Text: "must not run"}}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	req := newSpeechRequest(t, ts.URL, session.ID, "voice-cross-owner", "auto", gatewayTestWAV(8000))
	req.Header.Set("Authorization", "Bearer requester-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-owner speech request returned %d", resp.StatusCode)
	}
	if fake.input.RequestID != "" {
		t.Fatalf("cross-owner request reached the transcriber: %#v", fake.input)
	}
}

func TestSpeechTranscriptionAppliesEndToEndDeadline(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	cfg.Speech.TimeoutSeconds = 1
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Voice deadline")
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	fake := &fakeSpeechTranscriber{transcribe: func(ctx context.Context, _ speech.Request) (speech.Result, error) {
		<-ctx.Done()
		return speech.Result{}, speech.NewError(speech.CodeTimeout, "speech transcription timed out", true, ctx.Err())
	}}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(fake))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	started := time.Now()
	resp, err := http.DefaultClient.Do(newSpeechRequest(t, ts.URL, session.ID, "voice-deadline", "auto", gatewayTestWAV(8000)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("deadline request returned %d", resp.StatusCode)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("speech request exceeded its end-to-end deadline: %v", elapsed)
	}
}

func newSpeechRequest(t *testing.T, baseURL, sessionID, requestID, language string, audio []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileHeader := make(textproto.MIMEHeader)
	fileHeader.Set("Content-Disposition", `form-data; name="file"; filename="recording.wav"`)
	fileHeader.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(fileHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(audio); err != nil {
		t.Fatal(err)
	}
	_ = writer.WriteField("session_id", sessionID)
	_ = writer.WriteField("request_id", requestID)
	_ = writer.WriteField("language", language)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/speech/transcriptions", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func newSpeechRequestWithExtraFile(t *testing.T, baseURL, sessionID string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range []string{"file", "other_file"} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="recording.wav"`)
		header.Set("Content-Type", "audio/wav")
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(gatewayTestWAV(8000)); err != nil {
			t.Fatal(err)
		}
	}
	_ = writer.WriteField("session_id", sessionID)
	_ = writer.WriteField("request_id", "voice-extra-file")
	_ = writer.WriteField("language", "auto")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/speech/transcriptions", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func gatewayTestWAV(sampleFrames int) []byte {
	dataBytes := sampleFrames * 2
	raw := make([]byte, 44+dataBytes)
	copy(raw[0:4], "RIFF")
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)-8))
	copy(raw[8:12], "WAVE")
	copy(raw[12:16], "fmt ")
	binary.LittleEndian.PutUint32(raw[16:20], 16)
	binary.LittleEndian.PutUint16(raw[20:22], 1)
	binary.LittleEndian.PutUint16(raw[22:24], 1)
	binary.LittleEndian.PutUint32(raw[24:28], 16000)
	binary.LittleEndian.PutUint32(raw[28:32], 32000)
	binary.LittleEndian.PutUint16(raw[32:34], 2)
	binary.LittleEndian.PutUint16(raw[34:36], 16)
	copy(raw[36:40], "data")
	binary.LittleEndian.PutUint32(raw[40:44], uint32(dataBytes))
	return raw
}
