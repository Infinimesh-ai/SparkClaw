package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type ambiguousCredentialBindingStore struct {
	store.Store
}

func (s *ambiguousCredentialBindingStore) SaveNotificationBinding(record app.NotificationBinding) app.NotificationBinding {
	if strings.TrimSpace(record.CredentialRef) != "" {
		return record
	}
	return s.Store.SaveNotificationBinding(record)
}

type credentialPollAdapter struct {
	secret string
}

func (*credentialPollAdapter) Availability() error { return nil }

func (*credentialPollAdapter) Policy() binding.AdapterPolicy {
	return binding.AdapterPolicy{ExclusiveBinding: true}
}

func (*credentialPollAdapter) Start(_ context.Context, record app.NotificationBinding, _ binding.StartOptions) (app.NotificationBinding, error) {
	record.Provider = "credential-test"
	record.Status = "waiting_confirm"
	return record, nil
}

func (a *credentialPollAdapter) Poll(context.Context, app.NotificationBinding) (binding.PollResult, error) {
	return binding.PollResult{
		Status: "active", CredentialRef: "provider-pending",
		CredentialKind: "openclaw-weixin-bot-token", CredentialSecret: a.secret,
	}, nil
}

func (*credentialPollAdapter) Cancel(context.Context, app.NotificationBinding) error { return nil }

func TestCredentialFoundationRetainsSecretWhenBindingSaveIsAmbiguous(t *testing.T) {
	const secret = "foundation-ambiguous-save-token"
	cfg := testConfig(t.TempDir())
	cfg.Tools.Notifications.Channels["credential-test"] = config.NotificationChannelConfig{
		Enabled: true, Provider: "credential-test",
	}
	base := store.NewMemoryStore()
	st := &ambiguousCredentialBindingStore{Store: base}
	vault := credential.New(st, credential.Options{Key: strings.Repeat("g", 32)})
	router := binding.NewBaseRouter(cfg).WithAdapter("credential-test", &credentialPollAdapter{secret: secret})
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime, WithCredentialVault(vault), WithBindingRouter(router))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	startedResponse, err := http.Post(ts.URL+"/api/notification-bindings/credential-test/start", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer startedResponse.Body.Close()
	if startedResponse.StatusCode != http.StatusCreated {
		t.Fatalf("start returned %d", startedResponse.StatusCode)
	}
	var started app.NotificationBinding
	if err := json.NewDecoder(startedResponse.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if started.ID == "" || started.Status != "waiting_confirm" {
		t.Fatalf("unexpected pending binding: %#v", started)
	}

	pollResponse, err := http.Get(ts.URL + "/api/notification-bindings/" + started.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer pollResponse.Body.Close()
	var projected map[string]any
	if err := json.NewDecoder(pollResponse.Body).Decode(&projected); err != nil {
		t.Fatal(err)
	}
	if pollResponse.StatusCode != http.StatusServiceUnavailable || projected["code"] != credential.CodeUnavailable {
		t.Fatalf("ambiguous binding save status=%d body=%#v", pollResponse.StatusCode, projected)
	}
	if strings.Contains(fmt.Sprint(projected), secret) || strings.Contains(fmt.Sprint(projected), "provider-pending") {
		t.Fatalf("ambiguous binding response disclosed credential material: %#v", projected)
	}
	retained, found := base.GetNotificationBinding(started.ID)
	if !found || retained.Status != "waiting_confirm" || retained.CredentialRef != "" {
		t.Fatalf("durable binding crossed the unverified save: %#v found=%v", retained, found)
	}
	ref, err := vault.Seal(t.Context(), started.ID, "openclaw-weixin-bot-token", []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := base.GetCredentialSecret(t.Context(), ref)
	if err != nil || !found || stored.Value == "" || strings.Contains(stored.Value, secret) {
		t.Fatalf("sealed credential was not retained safely: %#v found=%v err=%v", stored, found, err)
	}
	for _, audit := range base.ListAudit("") {
		if audit.Type == "credential_secret.deleted" {
			t.Fatalf("ambiguous binding save deleted its credential: %#v", audit)
		}
	}
}
