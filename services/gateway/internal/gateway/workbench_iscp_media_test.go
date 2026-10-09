package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestISCPDomainMailSendUsesPersistedDraftAndNoDuplicateProviderEffect(t *testing.T) {
	server, repo, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	browser := &noEmailBrowser{}
	service, err := emailmanagement.New(repo, browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: t.TempDir(), QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
	if err != nil {
		t.Fatal(err)
	}
	server.emailManagement = service
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "iscp-owner", CommandKey: "isolated-mailbox"}, Provider: app.EmailProviderGmail, Address: "isolated@example.test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	body, _ := json.Marshal(map[string]any{"id": "iscp-draft", "mailbox_id": box.ID, "to": []string{"never-send@example.test"}, "subject": "controlled provider", "body": "private draft", "expected_version": 0})
	save := domainTestRequest("mail.drafts.save", body)
	saved := handler(ctx, save)
	if saved.Status != 200 {
		t.Fatalf("draft %+v", saved)
	}
	send := domainTestRequest("mail.drafts.send", []byte(`{"expected_version":1,"idempotency_key":"isolated-send-key"}`))
	send.Params = map[string]string{"draft": "iscp-draft"}
	sent := handler(ctx, send)
	if sent.Status != 200 {
		t.Fatalf("send %+v", sent)
	}
	count := browser.calls
	replay := handler(ctx, send)
	if replay.Status != 200 || !bytes.Equal(replay.Body, sent.Body) || browser.calls != count {
		t.Fatalf("duplicate side effect %+v calls=%d", replay, browser.calls)
	}
	rows, err := service.Drafts(ctx, "iscp-owner", "iscp-draft")
	if err != nil || len(rows) != 1 || rows[0].State != "sent" {
		t.Fatalf("missing durable send %+v %v", rows, err)
	}
}
func TestISCPDomainRecordedSpeechReadsBoundObjectAndReplaysFinalOnce(t *testing.T) {
	server, _, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	server.cfg.Speech.Enabled = true
	server.cfg.Speech.Backend = "openai-http"
	server.cfg.Speech.MaxUploadBytes = 3 << 20
	server.cfg.Speech.TimeoutSeconds = 30
	calls := 0
	fake := &fakeSpeechTranscriber{transcribe: func(_ context.Context, input speech.Request) (speech.Result, error) {
		calls++
		if len(input.PCM16WAV) != 44+32000 {
			t.Fatal("wrong audio")
		}
		return speech.Result{Text: "isolated transcript", Language: "en", Model: "controlled"}, nil
	}}
	server.speech = fake
	objects, err := iscpobjects.NewStore(filepath.Join(t.TempDir(), "objects"), iscpobjects.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	binding := iscpobjects.Binding{DeploymentID: cfg.Binding.DeploymentID, OwnerID: cfg.Binding.OwnerID, ClientID: cfg.Binding.ClientID, InstallationID: iscpTestInstallation, AuthorizationRevision: 1}
	ref, err := objects.Put(t.Context(), binding, "speech_recording", "recording.wav", "audio/wav", gatewayTestWAV(16000))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(domainTestContext(t), iscpObjectContextKey{}, iscpObjectAccess{objects, binding})
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"session_id": iscpTestInstallation, "request_id": "isolated-recording", "language": "en", "audio_object": ref})
	request := domainTestRequest("speech.transcribe", body)
	result := handler(ctx, request)
	if result.Status != 200 || !bytes.Contains(result.Body, []byte("isolated transcript")) {
		t.Fatalf("transcribe %+v", result)
	}
	replay := handler(ctx, request)
	if replay.Status != 200 || !bytes.Equal(result.Body, replay.Body) || calls != 1 {
		t.Fatal("transcription replay ran provider twice")
	}
	wrong := binding
	wrong.InstallationID = "another-installation"
	badctx := context.WithValue(domainTestContext(t), iscpObjectContextKey{}, iscpObjectAccess{objects, wrong})
	request.OperationID = domainTestRequest("next", nil).ID
	if result = handler(badctx, request); result.Status != 409 || calls != 1 {
		t.Fatalf("cross-installation object %+v", result)
	}
}
func TestISCPDomainRealtimeSpeechPartialFinalReplayAndFrameConflict(t *testing.T) {
	server, _, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	server.cfg.Speech.Enabled = true
	server.cfg.Speech.Backend = "openai-http"
	realtime := newFakeGatewayRealtimeSession()
	server.speech = &fakeSpeechTranscriber{status: speech.Status{Enabled: true, Ready: true, SupportsStreaming: true}, startRealtime: func(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error) { return realtime, nil }}
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	open := domainTestRequest("speech.session.open", []byte(`{"session_id":"`+iscpTestInstallation+`","request_id":"live-test","language":"auto"}`))
	opened := handler(ctx, open)
	var start struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(opened.Body, &start)
	if opened.Status != 200 || start.SessionID == "" {
		t.Fatalf("open %+v", opened)
	}
	frameBody, _ := json.Marshal(map[string]any{"session_id": start.SessionID, "sequence": 1, "pcm16": base64.StdEncoding.EncodeToString(make([]byte, 3200))})
	frame := domainTestRequest("speech.session.frame", frameBody)
	if result := handler(ctx, frame); result.Status != 200 {
		t.Fatalf("frame %+v", result)
	}
	if result := handler(ctx, frame); result.Status != 200 || len(realtime.audio) != 3200 {
		t.Fatalf("frame was repeated %+v bytes=%d", result, len(realtime.audio))
	}
	var events struct {
		Events []iscpSpeechEvent `json:"events"`
	}
	eventBody, _ := json.Marshal(map[string]any{"session_id": start.SessionID, "after": 0})
	poll := domainTestRequest("speech.session.events", eventBody)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result := handler(ctx, poll)
		json.Unmarshal(result.Body, &events)
		if len(events.Events) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(events.Events) < 2 || events.Events[1].Event.Event != "partial" {
		t.Fatal("no partial before finish")
	}
	finishBody, _ := json.Marshal(map[string]any{"session_id": start.SessionID, "last_sequence": 1, "total_samples": 1600, "reason": "user_stop"})
	finish := domainTestRequest("speech.session.finish", finishBody)
	if result := handler(ctx, finish); result.Status != 200 {
		t.Fatalf("finish %+v", result)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result := handler(ctx, poll)
		json.Unmarshal(result.Body, &events)
		if len(events.Events) >= 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(events.Events) != 3 || events.Events[2].Event.Event != "final" || events.Events[2].Event.DurationMS != 100 {
		t.Fatalf("bad final %+v", events)
	}
	again := handler(ctx, poll)
	var replay struct {
		Events []iscpSpeechEvent `json:"events"`
	}
	json.Unmarshal(again.Body, &replay)
	if len(replay.Events) != 3 {
		t.Fatal("unacknowledged final lost")
	}
}

func TestISCPDomainRecordedSpeechCancelBypassesLongMutation(t *testing.T) {
	server, _, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	server.cfg.Speech.Enabled = true
	server.cfg.Speech.Backend = "openai-http"
	server.cfg.Speech.MaxUploadBytes = 3 << 20
	server.cfg.Speech.TimeoutSeconds = 30
	started := make(chan struct{})
	stopped := make(chan struct{})
	server.speech = &fakeSpeechTranscriber{transcribe: func(ctx context.Context, _ speech.Request) (speech.Result, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return speech.Result{}, ctx.Err()
	}}
	objects, err := iscpobjects.NewStore(filepath.Join(t.TempDir(), "objects"), iscpobjects.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	binding := iscpobjects.Binding{DeploymentID: cfg.Binding.DeploymentID, OwnerID: cfg.Binding.OwnerID, ClientID: cfg.Binding.ClientID, InstallationID: iscpTestInstallation, AuthorizationRevision: 1}
	ref, err := objects.Put(t.Context(), binding, "speech_recording", "recording.wav", "audio/wav", gatewayTestWAV(16000))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(domainTestContext(t), iscpObjectContextKey{}, iscpObjectAccess{objects, binding})
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"session_id": iscpTestInstallation, "request_id": "cancel-recording", "language": "en", "audio_object": ref})
	finished := make(chan struct{})
	go func() { defer close(finished); handler(ctx, domainTestRequest("speech.transcribe", body)) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	cancelBody, _ := json.Marshal(map[string]string{"session_id": iscpTestInstallation, "request_id": "cancel-recording"})
	cancelled := make(chan int, 1)
	go func() { cancelled <- handler(ctx, domainTestRequest("speech.cancel", cancelBody)).Status }()
	select {
	case status := <-cancelled:
		if status != 200 {
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked behind recording receipt")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("provider was not cancelled")
	}
	<-finished
}

func TestISCPDomainQuietSpeechStreamClosesOnStandingAuthorizationRevocation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := newFakeGatewayRealtimeSession()
	session := &iscpSpeechSession{ctx: ctx, cancel: cancel, session: stream}
	authorization := iscpworkbench.SessionInfo{GrantRevision: 1, CheckAuthorization: func(context.Context) (iscpauth.Policy, error) {
		return iscpauth.Policy{Version: 2, Revision: 2, State: iscpauth.Revoked}, nil
	}}
	go watchISCPSpeechAuthorization(session, authorization)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("quiet provider survived revocation")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.closed || len(session.events) != 1 || session.events[0].Event.Code != "authorization_closed" {
		t.Fatal("missing terminal revocation receipt")
	}
}
