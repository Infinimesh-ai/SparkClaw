package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/telegram"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
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

func TestInfinimeshFailuresDoNotDisableLocalChatOrTelegram(t *testing.T) {
	for _, failure := range []string{"token-exhausted", "cloud-5xx"} {
		t.Run(failure, func(t *testing.T) {
			server := newFailingInfoServer(t, failure)
			defer server.Close()
			adapter, err := websearch.NewInfinimeshInfoAdapter(config.InfinimeshInfoConfig{
				BaseURL:               server.URL,
				TokenBatchSize:        1,
				MaxAttempts:           1,
				RetryBaseDelayMS:      1,
				RequestTimeoutSeconds: 2,
				ResponseBodyMaxBytes:  1 << 20,
				Language:              "en",
				MaxSources:            3,
				EntitlementProof:      "test-entitlement",
				DeviceAttestation:     "test-device",
				LicenseProof:          "test-license",
			}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Search(context.Background(), websearch.Request{Query: "failure isolation canary", MaxResults: 3}); err == nil {
				t.Fatalf("expected Infinimesh %s failure", failure)
			}

			cfg := integrationTestConfig(t)
			st := store.NewMemoryStore()
			tools := toolhub.New(cfg, st)
			defer tools.Close()
			runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))

			localSession := st.CreateSession("Local failure isolation")
			localResult, err := runtime.HandleMessage(context.Background(), localSession.ID, "confirm local chat still works")
			if err != nil || localResult.Message.Content == "" {
				t.Fatalf("local chat failed after Infinimesh error: result=%#v err=%v", localResult, err)
			}

			bot := &recordingTelegramBot{}
			binding := app.NotificationBinding{
				ID:             "telegram-binding",
				OwnerID:        app.DefaultOwnerID,
				Channel:        "telegram",
				Provider:       "telegram-bot-api",
				Status:         "active",
				ExternalUserID: "7",
				ExternalChatID: "9",
			}
			dispatcher := telegram.NewDispatcher(st, runtime, cfg).WithClient(bot)
			err = dispatcher.HandleUpdate(context.Background(), binding, telegram.Update{
				UpdateID: 1,
				Message: &telegram.Message{
					MessageID: 1,
					From:      &telegram.User{ID: 7, FirstName: "Owner"},
					Chat:      telegram.Chat{ID: 9, Type: "private"},
					Date:      time.Now().Unix(),
					Text:      "confirm Telegram still works",
				},
			})
			if err != nil || len(bot.messages) == 0 {
				t.Fatalf("Telegram failed after Infinimesh error: messages=%#v err=%v", bot.messages, err)
			}
		})
	}
}

func TestAllOptionalFeaturesComposeWithFileBackend(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Speech.Enabled = true
	cfg.Tools.Web.Search.Enabled = true
	cfg.Plugins.Entries.InfinimeshInfo.Config.EntitlementProof = "integration-entitlement-canary"
	cfg.Plugins.Entries.InfinimeshInfo.Config.DeviceAttestation = "integration-device-canary"
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseProof = "integration-license-canary"
	telegramConfig := cfg.Tools.Notifications.Channels["telegram"]
	telegramConfig.Enabled = true
	cfg.Tools.Notifications.Channels["telegram"] = telegramConfig
	cfg.Workspaces.DefaultRoot = root + "/workspaces"
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	cfg.Storage.TraceDir = root + "/traces"
	cfg.Storage.ArtifactDir = root + "/artifacts"
	cfg.State.Backend = "file"
	cfg.State.Path = root + "/state.json"
	cfg.State.CredentialKey = "01234567890123456789012345678901"
	cfg.State.CredentialKeyFile = ""

	st, err := newStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := st.(interface{ Close() }); ok {
		defer closer.Close()
	}
	artifactStore := artifact.NewStore(cfg.Storage)
	tools := toolhub.New(cfg, st).WithArtifactStore(artifactStore)
	defer tools.Close()
	traces := trace.NewWriter(cfg.Storage.TraceDir)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces).WithArtifactStore(artifactStore)
	backend := &recordingSpeechTranscriber{status: speech.Status{
		Enabled: true, Ready: true, State: speech.StateReady, Backend: "openai-http", Model: "sparkclaw-asr",
	}}
	connectors, err := newConnectorAssembly(cfg, st, runtime, backend)
	if err != nil {
		t.Fatal(err)
	}
	server := gateway.NewWithTrace(
		cfg,
		st,
		tools,
		runtime,
		traces,
		gateway.WithSpeechTranscriber(backend),
		gateway.WithCredentialVault(connectors.credentials),
		gateway.WithBindingRouter(connectors.registry.BindingRouter()),
		gateway.WithNotificationBindingCancellation(connectors.registry.CancelBinding),
	)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	readyRaw := readTestEndpoint(t, ts.URL+"/readyz")
	var ready struct {
		StateBackend string        `json:"state_backend"`
		Speech       speech.Status `json:"speech"`
	}
	if err := json.Unmarshal(readyRaw, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.StateBackend != "file" || !ready.Speech.Enabled || !ready.Speech.Ready {
		t.Fatalf("unexpected all-enabled readiness: %#v", ready)
	}

	configRaw := readTestEndpoint(t, ts.URL+"/api/config")
	var publicConfig struct {
		Speech struct {
			Enabled bool `json:"enabled"`
		} `json:"speech"`
		Tools struct {
			Web struct {
				Search struct {
					Enabled    bool `json:"enabled"`
					Configured bool `json:"configured"`
				} `json:"search"`
			} `json:"web"`
			Notifications struct {
				Channels map[string]struct {
					Enabled         bool `json:"enabled"`
					OperatorEnabled bool `json:"operator_enabled"`
				} `json:"channels"`
			} `json:"notifications"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(configRaw, &publicConfig); err != nil {
		t.Fatal(err)
	}
	telegramStatus := publicConfig.Tools.Notifications.Channels["telegram"]
	if !publicConfig.Speech.Enabled || !publicConfig.Tools.Web.Search.Enabled || !publicConfig.Tools.Web.Search.Configured || !telegramStatus.Enabled || !telegramStatus.OperatorEnabled {
		t.Fatalf("all-enabled public config mismatch: %#v", publicConfig)
	}
	for _, secret := range []string{
		cfg.Plugins.Entries.InfinimeshInfo.Config.EntitlementProof,
		cfg.Plugins.Entries.InfinimeshInfo.Config.DeviceAttestation,
		cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseProof,
		cfg.State.CredentialKey,
	} {
		if strings.Contains(string(configRaw), secret) || strings.Contains(string(readyRaw), secret) {
			t.Fatalf("public status leaked integration secret")
		}
	}
}

func readTestEndpoint(t *testing.T, endpoint string) []byte {
	t.Helper()
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s returned %d", endpoint, response.StatusCode)
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newFailingInfoServer(t *testing.T, failure string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/info/tokens/issue":
			issued := []map[string]any{}
			if failure == "cloud-5xx" {
				issued = append(issued, map[string]any{
					"type":       "basic",
					"token_mode": "internal_opaque",
					"token":      "one-shot-test-token",
					"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch":           time.Now().UTC().Format("2006-01-02"),
				"issued_tokens":   issued,
				"quota_remaining": map[string]int{"basic": 0},
			})
		case "/v1/info/query":
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": "UPSTREAM_ERROR", "message": "test failure", "retryable": true},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func integrationTestConfig(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Speech.Enabled = false
	cfg.Tools.Web.Search.Enabled = false
	telegramConfig := cfg.Tools.Notifications.Channels["telegram"]
	telegramConfig.Enabled = true
	cfg.Tools.Notifications.Channels["telegram"] = telegramConfig
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = root + "/traces"
	cfg.Storage.ArtifactDir = root + "/artifacts"
	cfg.State.Backend = "memory"
	return cfg
}

type recordingTelegramBot struct {
	messages []string
}

func (b *recordingTelegramBot) GetMe(context.Context) (telegram.User, error) {
	return telegram.User{}, nil
}

func (b *recordingTelegramBot) GetUpdates(context.Context, int64, int) ([]telegram.Update, error) {
	return nil, nil
}

func (b *recordingTelegramBot) GetFile(context.Context, string) (telegram.File, error) {
	return telegram.File{}, nil
}

func (b *recordingTelegramBot) DownloadFile(context.Context, string, string, int64) (int64, error) {
	return 0, nil
}

func (b *recordingTelegramBot) SendMessage(_ context.Context, _, _ int64, message string, _ *telegram.InlineKeyboardMarkup) (telegram.Message, error) {
	b.messages = append(b.messages, message)
	return telegram.Message{}, nil
}

func (b *recordingTelegramBot) SendChatAction(context.Context, int64, int64, string) error {
	return nil
}

func (b *recordingTelegramBot) SendPhoto(context.Context, int64, int64, string, string) (telegram.Message, error) {
	return telegram.Message{}, nil
}

func (b *recordingTelegramBot) SendDocument(context.Context, int64, int64, string, string, string) (telegram.Message, error) {
	return telegram.Message{}, nil
}

func (b *recordingTelegramBot) SendVoice(context.Context, int64, int64, string, string) (telegram.Message, error) {
	return telegram.Message{}, nil
}

func (b *recordingTelegramBot) AnswerCallbackQuery(context.Context, string, string) error {
	return nil
}

func (b *recordingTelegramBot) SetMyCommands(context.Context, []telegram.BotCommand) error {
	return nil
}
