package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connector"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

func TestISCPDomainConnectionBindingsFilterOwnerAndActor(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []app.NotificationBinding{
		{ID: "owned-weixin", OwnerID: "iscp-owner", ActorID: "iscp-actor", Channel: "weixin"},
		{ID: "owned-telegram", OwnerID: "iscp-owner", ActorID: "iscp-actor", Channel: "telegram"},
		{ID: "foreign-owner", OwnerID: "other-owner", ActorID: "iscp-actor", Channel: "weixin"},
		{ID: "foreign-actor", OwnerID: "iscp-owner", ActorID: "other-actor", Channel: "weixin"},
	} {
		record.Status = app.NotificationBindingActive
		storetest.MustCreateNotificationBinding(t, repo, record)
	}
	read := domainTestRequest("settings.bindings.list", nil)
	read.Params = map[string]string{"channel": "weixin", "status": "active"}
	result := handler(domainTestContext(t), read)
	if result.Status != http.StatusOK || !bytes.Contains(result.Body, []byte("owned-weixin")) ||
		bytes.Contains(result.Body, []byte("owned-telegram")) || bytes.Contains(result.Body, []byte("foreign-")) {
		t.Fatalf("owner/actor-scoped filtered list: status=%d body=%s", result.Status, result.Body)
	}
	for _, operation := range []string{"settings.bindings.get", "settings.bindings.login"} {
		for _, id := range []string{"foreign-owner", "foreign-actor", "missing"} {
			request := domainTestRequest(operation, nil)
			request.Params = map[string]string{"binding_id": id}
			if result := handler(domainTestContext(t), request); result.Status != http.StatusNotFound {
				t.Fatalf("%s disclosed %s: status=%d", operation, id, result.Status)
			}
		}
	}
}

func TestISCPDomainConnectionBindingsRejectUntypedInputs(t *testing.T) {
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		operation string
		params    map[string]string
		body      string
	}{
		{"settings.bindings.start", map[string]string{"channel": "telegram"}, `{"credential_secret":"private","url":"https://attacker.example"}`},
		{"settings.bindings.start", map[string]string{"channel": "telegram", "owner_id": "other-owner"}, `{}`},
		{"settings.bindings.start", map[string]string{"channel": "mail"}, `{}`},
		{"settings.bindings.start", map[string]string{"channel": "weixin"}, `{"bot_token":"private"}`},
		{"settings.bindings.start", map[string]string{"channel": "telegram"}, `null`},
		{"settings.bindings.list", map[string]string{"status": "unrecognized"}, `{}`},
		{"settings.bindings.list", map[string]string{"url": "https://attacker.example"}, `{}`},
		{"settings.bindings.get", map[string]string{"binding_id": "../foreign"}, `{}`},
		{"settings.bindings.poll", map[string]string{"binding_id": "owned"}, `{"owner_id":"other-owner"}`},
		{"settings.bindings.revoke", map[string]string{"binding_id": "owned"}, `null`},
		{"settings.bindings.login", map[string]string{"binding_id": "owned"}, `{"url":"https://attacker.example"}`},
		{"settings.browser.save", nil, `{"token":"private","profile_id":"foreign"}`},
		{"settings.browser.save", map[string]string{"url": "https://attacker.example"}, `{"token":"private"}`},
		{"settings.browser.check", nil, `{"token":"private"}`},
		{"settings.browser.delete", nil, `null`},
	} {
		t.Run(test.operation+"/"+test.body, func(t *testing.T) {
			request := domainTestRequest(test.operation, []byte(test.body))
			request.Params = test.params
			result := handler(domainTestContext(t), request)
			if result.Status != http.StatusBadRequest || bytes.Contains(result.Body, []byte("private")) {
				t.Fatalf("untyped input reached a handler: status=%d body=%s", result.Status, result.Body)
			}
		})
	}
}

type desktopQRBindingAdapter struct {
	polls atomic.Int32
}

func (*desktopQRBindingAdapter) Availability() error { return nil }
func (*desktopQRBindingAdapter) Policy() binding.AdapterPolicy {
	return binding.AdapterPolicy{ExclusiveBinding: true}
}
func (*desktopQRBindingAdapter) Cancel(context.Context, app.NotificationBinding) error { return nil }
func (*desktopQRBindingAdapter) Start(_ context.Context, record app.NotificationBinding, _ binding.StartOptions) (app.NotificationBinding, error) {
	expiry := time.Now().UTC().Add(5 * time.Minute)
	record.Status, record.ExpiresAt = app.NotificationBindingWaitingScan, &expiry
	record.QRCodeURL = "https://liteapp.weixin.qq.com/q/desktop-test"
	record.ProviderSessionID = "private-provider-session"
	return record, nil
}
func (a *desktopQRBindingAdapter) Poll(context.Context, app.NotificationBinding) (binding.PollResult, error) {
	if a.polls.Add(1) == 1 {
		return binding.PollResult{Status: app.NotificationBindingWaitingConfirm}, nil
	}
	return binding.PollResult{Status: app.NotificationBindingActive, AccountID: "weixin-account"}, nil
}

func TestISCPDomainConnectionBindingPollReadsCurrentProviderState(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	server.cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{Enabled: true, Provider: weixinproto.QRProvider}
	adapter := &desktopQRBindingAdapter{}
	registry := connector.NewRegistry(server.cfg, repo)
	if err := registry.Register(connector.Registration{Channel: "weixin", Binding: adapter, BindingProvider: weixinproto.QRProvider}); err != nil {
		t.Fatal(err)
	}
	server.connectors = registry
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	start := domainTestRequest("settings.bindings.start", []byte(`{}`))
	start.Params = map[string]string{"channel": "weixin"}
	result := handler(ctx, start)
	var started struct {
		ID string `json:"id"`
	}
	if result.Status != http.StatusCreated || json.Unmarshal(result.Body, &started) != nil || started.ID == "" ||
		bytes.Contains(result.Body, []byte("private-provider-session")) {
		t.Fatalf("start: status=%d body=%s", result.Status, result.Body)
	}
	if replay := handler(ctx, start); replay.Status != http.StatusCreated || !bytes.Equal(replay.Body, result.Body) {
		t.Fatalf("start replay: status=%d body=%s", replay.Status, replay.Body)
	}
	records := storetest.MustListNotificationBindings(t, repo, "weixin", "")
	if len(records) != 1 || records[0].OwnerID != "iscp-owner" || records[0].ActorID != "iscp-actor" {
		t.Fatalf("start duplicated or lost principal: count=%d", len(records))
	}
	for _, principal := range []requestPrincipal{
		{Authenticated: true, OwnerID: "other-owner", ActorID: "iscp-actor", ClientID: "iscp-desktop-client"},
		{Authenticated: true, OwnerID: "iscp-owner", ActorID: "other-actor", ClientID: "iscp-desktop-client"},
	} {
		foreign := context.WithValue(t.Context(), requestPrincipalContextKey{}, principal)
		for _, operation := range []string{"settings.bindings.poll", "settings.bindings.revoke"} {
			request := domainTestRequest(operation, nil)
			request.Params = map[string]string{"binding_id": started.ID}
			if result := handler(foreign, request); result.Status != http.StatusNotFound || adapter.polls.Load() != 0 {
				t.Fatalf("foreign mutation %s: status=%d calls=%d", operation, result.Status, adapter.polls.Load())
			}
		}
	}
	poll := domainTestRequest("settings.bindings.poll", []byte(`{}`))
	poll.Params = map[string]string{"binding_id": started.ID}
	first := handler(ctx, poll)
	if first.Status != http.StatusOK || !bytes.Contains(first.Body, []byte(`"status":"waiting_confirm"`)) || adapter.polls.Load() != 1 {
		t.Fatalf("first poll: status=%d count=%d body=%s", first.Status, adapter.polls.Load(), first.Body)
	}
	// Polling is a status read without an operation receipt: the same request
	// queries the provider again and observes the completed binding.
	if completed := handler(ctx, poll); completed.Status != http.StatusOK ||
		!bytes.Contains(completed.Body, []byte(`"status":"active"`)) || adapter.polls.Load() != 2 {
		t.Fatalf("fresh poll did not progress: status=%d count=%d body=%s", completed.Status, adapter.polls.Load(), completed.Body)
	}
	revoke := domainTestRequest("settings.bindings.revoke", nil)
	revoke.Params = poll.Params
	if revoked := handler(ctx, revoke); revoked.Status != http.StatusOK || !bytes.Contains(revoked.Body, []byte(`"status":"revoked"`)) {
		t.Fatalf("revoke: status=%d body=%s", revoked.Status, revoked.Body)
	}
}

func TestISCPDomainTelegramBindingSealsTokenAndDoesNotRepeatVerification(t *testing.T) {
	const token = "123456789:AA-desktop-token-canary"
	var checks atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/getMe" {
			http.NotFound(w, r)
			return
		}
		checks.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": 123456789, "is_bot": true, "username": "desktop_binding_bot"}})
	}))
	defer provider.Close()
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	channel := config.NotificationChannelConfig{Enabled: true, Provider: "telegram-bot-api", BaseURL: provider.URL}
	server.cfg.Tools.Notifications.Channels["telegram"] = channel
	vault := credential.New(repo, credential.Options{Key: strings.Repeat("b", 32)})
	registry := connector.NewRegistry(server.cfg, repo).WithCredentialLifecycle(vault)
	if err := registry.Register(connector.Registration{
		Channel: "telegram", SetupKind: app.ConnectorSetupSecret, BindingProvider: "telegram-bot-api",
		CredentialKind: "telegram-bot-token", Binding: binding.NewTelegramAdapter("telegram", channel, vault),
	}); err != nil {
		t.Fatal(err)
	}
	server.connectors = registry
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest("settings.bindings.start", []byte(`{"credential_secret":"`+token+`"}`))
	request.Params = map[string]string{"channel": "telegram"}
	result := handler(domainTestContext(t), request)
	if result.Status != http.StatusCreated || bytes.Contains(result.Body, []byte(token)) || checks.Load() != 1 {
		t.Fatalf("Telegram binding: status=%d checks=%d", result.Status, checks.Load())
	}
	if replay := handler(domainTestContext(t), request); replay.Status != http.StatusCreated || checks.Load() != 1 {
		t.Fatalf("Telegram verification repeated: status=%d checks=%d", replay.Status, checks.Load())
	}
	records := storetest.MustListNotificationBindings(t, repo, "telegram", "")
	if len(records) != 1 || records[0].Status != app.NotificationBindingActive || records[0].ExternalUserID != "" || records[0].ExternalChatID != "" {
		t.Fatalf("Telegram should await first user message: count=%d", len(records))
	}
	sealed, found, err := repo.GetCredentialSecret(t.Context(), records[0].CredentialRef)
	if err != nil || !found || strings.Contains(sealed.Value, token) || !strings.Contains(sealed.Value, "AES-256-GCM") {
		t.Fatal("Telegram credential was not sealed")
	}
	assertDesktopConnectionSecretAbsent(t, filepath.Dir(server.executionRoot), token)
}

type desktopBindingBrowserClient struct {
	browsercontrol.ControllerClient
	validations atomic.Int32
}

func (c *desktopBindingBrowserClient) ValidateToken(context.Context, string, []byte) (browsercontrol.ValidationResult, error) {
	c.validations.Add(1)
	return browsercontrol.ValidationResult{SchemaVersion: 1, State: app.IntegrationStateReady, ProfileID: "default", ControllerGeneration: 1, SessionGeneration: 1, PageGeneration: 1}, nil
}

func TestISCPDomainBrowserTokenPersistsSealedAndRestores(t *testing.T) {
	const token = "desktop-extension-secret-canary"
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	vault := credential.New(repo, credential.Options{Key: strings.Repeat("b", 32)})
	client := &desktopBindingBrowserClient{}
	service := browsercontrol.New(vault, client, "default")
	service.Initialize(t.Context())
	server.browserControl = service
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest("settings.browser.save", []byte(`{"token":"`+token+`"}`))
	result := handler(domainTestContext(t), request)
	if result.Status != http.StatusOK || !bytes.Contains(result.Body, []byte(`"configured":true`)) ||
		bytes.Contains(result.Body, []byte(token)) || client.validations.Load() != 1 {
		t.Fatalf("browser save: status=%d calls=%d", result.Status, client.validations.Load())
	}
	if replay := handler(domainTestContext(t), request); replay.Status != http.StatusOK || client.validations.Load() != 1 {
		t.Fatalf("browser save replay: status=%d calls=%d", replay.Status, client.validations.Load())
	}
	secrets, err := repo.ListCredentialSecrets(t.Context())
	if err != nil || len(secrets) != 1 || !strings.Contains(secrets[0].Value, "AES-256-GCM") || strings.Contains(secrets[0].Value, token) {
		t.Fatal("browser extension token was not sealed")
	}
	assertDesktopConnectionSecretAbsent(t, filepath.Dir(server.executionRoot), token)
	restored := browsercontrol.New(vault, client, "default")
	restored.Initialize(t.Context())
	if !restored.Status(t.Context()).Configured {
		t.Fatal("browser token did not survive service restart")
	}
	server.browserControl = restored
	for _, operation := range []string{"settings.browser.get", "settings.browser.check", "settings.browser.delete"} {
		result := handler(domainTestContext(t), domainTestRequest(operation, nil))
		if result.Status != http.StatusOK || bytes.Contains(result.Body, []byte(token)) {
			t.Fatalf("%s: status=%d", operation, result.Status)
		}
	}
	if restored.Status(t.Context()).Configured {
		t.Fatal("browser token survived deletion")
	}
}

func assertDesktopConnectionSecretAbsent(t *testing.T, root, secret string) {
	t.Helper()
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("plaintext credential persisted in %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationBindingLoginURLValidatesPersistedStateForLANAndISCP(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(5 * time.Minute)
	expired := time.Now().UTC().Add(-time.Minute)
	for _, test := range []struct {
		id       string
		change   func(*app.NotificationBinding)
		expected int
	}{
		{"pending", nil, http.StatusOK},
		{"confirming", func(r *app.NotificationBinding) { r.Status = app.NotificationBindingWaitingConfirm }, http.StatusOK},
		{"foreign", func(r *app.NotificationBinding) { r.ActorID = "other-actor" }, http.StatusNotFound},
		{"wrong-host", func(r *app.NotificationBinding) { r.QRCodeURL = "https://attacker.example/q" }, http.StatusBadRequest},
		{"active", func(r *app.NotificationBinding) { r.Status = app.NotificationBindingActive }, http.StatusConflict},
		{"expired", func(r *app.NotificationBinding) { r.ExpiresAt = &expired }, http.StatusConflict},
		{"unbounded", func(r *app.NotificationBinding) { r.ExpiresAt = nil }, http.StatusConflict},
		{"wrong-provider", func(r *app.NotificationBinding) { r.Provider = "manual-weixin" }, http.StatusConflict},
	} {
		t.Run(test.id, func(t *testing.T) {
			record := app.NotificationBinding{
				ID: test.id, OwnerID: "iscp-owner", ActorID: "iscp-actor", Channel: "weixin", Provider: weixinproto.QRProvider,
				Status: app.NotificationBindingWaitingScan, QRCodeURL: "https://liteapp.weixin.qq.com/q/" + test.id,
				ProviderSessionID: "private-QR-session", ExpiresAt: &expires,
			}
			if test.change != nil {
				test.change(&record)
			}
			storetest.MustCreateNotificationBinding(t, repo, record)
			request := domainTestRequest("settings.bindings.login", []byte(`{}`))
			request.Params = map[string]string{"binding_id": record.ID}
			result := handler(domainTestContext(t), request)
			if result.Status != test.expected || bytes.Contains(result.Body, []byte("private-QR-session")) {
				t.Fatalf("ISCP login URL: status=%d body=%s", result.Status, result.Body)
			}
			lan := httptest.NewRequest(http.MethodGet, "/api/notification-bindings/"+record.ID+"/login-url", nil)
			lan.Header.Set("Authorization", "Bearer synthetic-issued-client-credential-never-used-by-iscp")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, lan)
			if response.Code != test.expected || response.Header().Get("Cache-Control") != "no-store" ||
				bytes.Contains(response.Body.Bytes(), []byte("private-QR-session")) {
				t.Fatalf("LAN login URL: status=%d body=%s", response.Code, response.Body.String())
			}
			if test.expected == http.StatusOK {
				var output struct {
					URL       string    `json:"url"`
					ExpiresAt time.Time `json:"expires_at"`
				}
				if json.Unmarshal(result.Body, &output) != nil || output.URL != record.QRCodeURL || !output.ExpiresAt.Equal(expires) {
					t.Fatalf("login URL did not use persisted state: %s", result.Body)
				}
			}
		})
	}
	for _, suffix := range []string{"?url=https://attacker.example", "?owner_id=other-owner"} {
		request := httptest.NewRequest(http.MethodGet, "/api/notification-bindings/pending/login-url"+suffix, nil)
		request.Header.Set("Authorization", "Bearer synthetic-issued-client-credential-never-used-by-iscp")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("login URL accepted caller-selected query: status=%d", response.Code)
		}
	}
}
