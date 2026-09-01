package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
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

func (t *recordingSpeechTranscriber) StartRealtime(context.Context, speech.RealtimeRequest) (speech.RealtimeSession, error) {
	return nil, speech.NewError(speech.CodeUnavailable, "realtime speech is unavailable", true, nil)
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
				LicenseID:             "lic_test",
				LicenseKey:            "ilk_v1.lic_test.test-key",
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

			localSession := storetest.MustCreateSession(t, st, "Local failure isolation")
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
			binding = storetest.MustCreateNotificationBinding(t, st, binding)
			dispatcher := telegram.NewDispatcher(st, runtime, cfg).WithClient(bot).WithResultDeliverer(recordingMainResultDeliverer{bot: bot})
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

type recordingMainResultDeliverer struct{ bot *recordingTelegramBot }

func (d recordingMainResultDeliverer) DeliverWorkflowResult(ctx context.Context, result app.WorkflowResult) (app.DeliveryReceipt, error) {
	for _, part := range result.Content.Parts {
		if part.Kind == app.MessagePartText && strings.TrimSpace(part.Text) != "" {
			_, err := d.bot.SendMessage(ctx, 9, 0, part.Text, nil)
			now := time.Now().UTC()
			return app.DeliveryReceipt{DeliveryID: "test_delivery", EndpointID: result.ReturnRoute.SourceEndpointID, Status: app.DeliverySucceeded, AttemptedAt: now, DeliveredAt: &now}, err
		}
	}
	return app.DeliveryReceipt{}, errors.New("workflow result has no text part")
}

func TestAllOptionalFeaturesComposeWithFileBackend(t *testing.T) {
	root := t.TempDir()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Speech.Enabled = true
	cfg.Tools.Web.Search.Enabled = true
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseID = "lic_integration"
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey = "ilk_v1.lic_integration.integration-key-canary"
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

	storeRuntime, err := newStore(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer storeRuntime.Close(context.Background())
	st := backendFromRuntime(storeRuntime)
	artifactStore := artifact.NewStore(cfg.Storage)
	tools := toolhub.New(cfg, st).WithArtifactStore(artifactStore)
	defer tools.Close()
	traces := trace.NewWriter(cfg.Storage.TraceDir)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces).WithArtifactStore(artifactStore)
	backend := &recordingSpeechTranscriber{status: speech.Status{
		Enabled: true, Ready: true, State: speech.StateReady, Backend: "openai-http", Model: "sparkclaw-asr",
	}}
	services, err := newGatewayServices(cfg, st, tools, runtime, traces, backend, storeRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if services.server == nil || services.connectors == nil || services.connectors.registry == nil || services.connectors.endpoints == nil || services.connectors.delivery == nil || services.reminderScheduler == nil {
		t.Fatalf("optional service assembly is incomplete: %#v", services)
	}
	ts := httptest.NewServer(services.server.Handler())
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

	endpointRaw := readTestEndpoint(t, ts.URL+"/api/delivery-endpoints")
	var endpointPayload struct {
		Endpoints []json.RawMessage `json:"endpoints"`
	}
	if err := json.Unmarshal(endpointRaw, &endpointPayload); err != nil || endpointPayload.Endpoints == nil {
		t.Fatalf("production assembly did not enable delivery endpoints: payload=%s err=%v", endpointRaw, err)
	}
	deliveryResponse, err := http.Post(ts.URL+"/api/deliveries", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	deliveryResponse.Body.Close()
	if deliveryResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("production assembly did not enable direct delivery: status=%d", deliveryResponse.StatusCode)
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
		cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseID,
		cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey,
		cfg.State.CredentialKey,
	} {
		if strings.Contains(string(configRaw), secret) || strings.Contains(string(readyRaw), secret) {
			t.Fatalf("public status leaked integration secret")
		}
	}
}

func TestNewStoreHonorsCanceledStartupContext(t *testing.T) {
	cfg := config.Default()
	cfg.State.Backend = "postgres"
	cfg.State.DSN = "postgres://sparkclaw:sparkclaw@127.0.0.1:1/sparkclaw?sslmode=disable"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newStore(ctx, cfg); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled store startup error = %v", err)
	}
}

func TestNewStorePropagatesOperationTimeouts(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			cfg := config.Default()
			cfg.State.Backend = backend
			cfg.State.Path = filepath.Join(t.TempDir(), "state.json")
			cfg.State.ReadTimeoutSeconds = 7
			cfg.State.WriteTimeoutSeconds = 19
			cfg.State.TransactionTimeoutSeconds = 23
			storeRuntime, err := newStore(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer storeRuntime.Close(context.Background())
			value := reflect.ValueOf(storeRuntime.SessionRepository()).Elem()
			fieldName := "operationTimeouts"
			if backend == "file" {
				fieldName = "timeouts"
			}
			timeouts := value.FieldByName(fieldName)
			read := time.Duration(timeouts.FieldByName("Read").Int())
			write := time.Duration(timeouts.FieldByName("Write").Int())
			transaction := time.Duration(timeouts.FieldByName("Transaction").Int())
			if read != 7*time.Second || write != 19*time.Second || transaction != 23*time.Second {
				t.Fatalf("assembled %s timeouts = read %s write %s transaction %s", backend, read, write, transaction)
			}
		})
	}
}

func TestProductionAssemblyPersistsScheduledWebMessage(t *testing.T) {
	root := t.TempDir()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = filepath.Join(root, "workspaces")
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	cfg.Storage.TraceDir = filepath.Join(root, "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	cfg.State.Backend = "file"
	cfg.State.Path = filepath.Join(root, "gateway-state.json")

	storeRuntime, err := newStore(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer storeRuntime.Close(context.Background())
	st := backendFromRuntime(storeRuntime)
	session := storetest.MustCreateSession(t, st, "Scheduled Web message")
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	traces := trace.NewWriter(cfg.Storage.TraceDir)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), traces)
	services, err := newGatewayServices(cfg, st, tools, runtime, traces, speech.NewDisabled(cfg.Speech), storeRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if services.reminderScheduler == nil {
		t.Fatal("production assembly did not start the reminder scheduler")
	}

	now := time.Now().UTC()
	schedule := app.MessageSchedule{
		ID: app.ScheduleID("schedule-web-production"), SessionID: session.ID,
		Spec: app.ScheduleSpec{
			SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
			Payload: app.SchedulePayload{Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: "schedule:text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "该喝水了吗？",
			}}}},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: messagecontrol.WebEndpointID(session.ID)},
			Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
		},
		DueTime: now.Add(-time.Second), Timezone: "UTC", DedupeKey: "schedule-web-production", Status: "pending",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}
	if _, err := messagecontrol.NewScheduleRegistry(st).Save(t.Context(), schedule); err != nil {
		t.Fatal(err)
	}

	deliveries, err := services.reminderScheduler.Tick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != "sent" || deliveries[0].Provider != "message-runtime" {
		t.Fatalf("scheduled Web delivery did not complete: %#v", deliveries)
	}
	messages := storetest.MustListMessages(t, st, session.ID)
	if len(messages) != 2 || messages[0].Role != "user" || messages[0].Content != "该喝水了吗？" || messages[1].Role != "assistant" {
		t.Fatalf("scheduled Web delivery did not enter the conversation: %#v", messages)
	}
	stored, ok, err := st.GetReminder(t.Context(), string(schedule.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || stored.Status != "sent" || stored.LastDeliveryID != deliveries[0].ID {
		t.Fatalf("scheduled Web delivery state was not completed: %#v", stored)
	}
	reloaded, err := store.NewFileStore(cfg.State.Path)
	if err != nil {
		t.Fatal(err)
	}
	if messages := storetest.MustListMessages(t, reloaded, session.ID); len(messages) != 2 || messages[0].Content != "该喝水了吗？" || messages[1].Role != "assistant" {
		t.Fatalf("scheduled Web message did not survive file-state reload: %#v", messages)
	}
}

func TestDefaultFileBackendProductionEntryReadsStructuredDocument(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("production entry evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.State.Backend = ""
	cfg.State.Path = filepath.Join(root, "gateway-state.json")
	cfg.Workspaces.DefaultRoot = workspace
	cfg.Workspaces.Allowlist = []string{workspace}

	storeRuntime, err := newStore(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer storeRuntime.Close(context.Background())
	st := backendFromRuntime(storeRuntime)
	session := storetest.MustCreateSessionWithScope(t, st, "document production entry", app.DefaultOwnerID, workspace, "web", false)
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	result, err := tools.Execute(context.Background(), "files.read", map[string]any{"path": "note.txt"}, session.ID, "run_document")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	documentOutput := output["document"].(map[string]any)
	if output["truncated"] != false || documentOutput["representation_version"] != "structured_document_v1" || documentOutput["id"] == "" {
		t.Fatalf("default file backend did not execute the structured document path: %#v", output)
	}
	if _, err := os.Stat(cfg.State.Path); err != nil {
		t.Fatalf("default file backend did not persist production state: %v", err)
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
	cfg := configtest.MustLoadDefault()
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
