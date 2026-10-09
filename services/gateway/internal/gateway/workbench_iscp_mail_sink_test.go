package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// This isolated provider adapter uses an httptest loopback sink. It exercises
// the actual mail service and durable store, not a real Gmail account.
type iscpLoopbackMailProvider struct {
	noEmailBrowser
	endpoint string
	client   *http.Client
}

func (p *iscpLoopbackMailProvider) call(ctx context.Context, path string, input app.EmailSendRequest) (app.EmailSendResult, error) {
	raw, _ := json.Marshal(input)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+path, bytes.NewReader(raw))
	if err != nil {
		return app.EmailSendResult{}, err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return app.EmailSendResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return app.EmailSendResult{}, errors.New("isolated provider response lost")
	}
	var receipt app.EmailSendResult
	err = json.NewDecoder(response.Body).Decode(&receipt)
	return receipt, err
}
func (p *iscpLoopbackMailProvider) SendForOwner(ctx context.Context, _ string, input app.EmailSendRequest) (app.EmailSendResult, error) {
	return p.call(ctx, "/send", input)
}
func (p *iscpLoopbackMailProvider) ReconcileSendForOwner(ctx context.Context, _ string, input app.EmailSendRequest) (app.EmailSendResult, error) {
	return p.call(ctx, "/reconcile", input)
}
func TestISCPDomainMailLoopbackLostReceiptReconcilesWithoutResend(t *testing.T) {
	server, repo, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	var mu sync.Mutex
	var sent app.EmailSendRequest
	sends, reconciles := 0, 0
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input app.EmailSendRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/send" {
			sends++
			sent = input
			http.Error(w, "simulated lost receipt after effect", 503)
			return
		}
		if r.URL.Path != "/reconcile" || input.InvocationID != sent.InvocationID || input.Subject != sent.Subject || input.Body != sent.Body || len(input.To) != 1 || input.To[0] != sent.To[0] {
			http.Error(w, "not the same immutable send", 409)
			return
		}
		reconciles++
		_ = json.NewEncoder(w).Encode(app.EmailSendResult{Provider: app.EmailProviderGmail, Status: "sent", ProviderMessageID: "loopback-message-1"})
	}))
	defer sink.Close()
	provider := &iscpLoopbackMailProvider{endpoint: sink.URL, client: sink.Client()}
	service, err := emailmanagement.New(repo, provider, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: t.TempDir(), QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
	if err != nil {
		t.Fatal(err)
	}
	server.emailManagement = service
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "iscp-owner", CommandKey: "loopback"}, Provider: app.EmailProviderGmail, Address: "sender@example.test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &iscpDomainAdapter{server: server, config: cfg}
	if adapter.mailSendReady(t.Context(), "iscp-owner") {
		t.Fatal("unconfigured provider was advertised ready")
	}
	checked := domainNow()
	_, err = repo.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "iscp-owner", Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0)
	if err != nil || !adapter.mailSendReady(t.Context(), "iscp-owner") {
		t.Fatal("configured loopback provider readiness", err)
	}
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	invalid := handler(ctx, domainTestRequest("mail.drafts.save", []byte(`{"id":"unsupported-attachment","attachments":[{"object_id":"untrusted"}]}`)))
	if invalid.Status != 403 {
		t.Fatal("mail scope without workspace read accepted an attachment manifest")
	}
	body := strings.Repeat("x", 200<<10)
	raw, _ := json.Marshal(map[string]any{"id": "loopback-draft", "expected_version": 0, "mailbox_id": box.ID, "to": []string{"sink@example.test"}, "subject": "isolated send", "body": body})
	save := domainTestRequest("mail.drafts.save", raw)
	saved := handler(ctx, save)
	if saved.Status != 200 {
		t.Fatalf("large draft %d %s", saved.Status, saved.Body)
	}
	if replay := handler(ctx, save); replay.Status != 200 || !bytes.Equal(saved.Body, replay.Body) {
		t.Fatal("large saved draft lacked durable receipt")
	}
	// An edit after review invalidates the old expected-version confirmation.
	edited, _ := json.Marshal(map[string]any{"id": "loopback-draft", "expected_version": 1, "mailbox_id": box.ID, "to": []string{"sink@example.test"}, "subject": "isolated send amended", "body": body})
	if result := handler(ctx, domainTestRequest("mail.drafts.save", edited)); result.Status != 200 {
		t.Fatal("draft edit failed", result.Status)
	}
	stale := domainTestRequest("mail.drafts.send", []byte(`{"expected_version":1,"idempotency_key":"stale-review"}`))
	stale.Params = map[string]string{"draft": "loopback-draft"}
	if result := handler(ctx, stale); result.Status != 409 {
		t.Fatal("stale send review was accepted", result.Status)
	}
	send := domainTestRequest("mail.drafts.send", []byte(`{"expected_version":2,"idempotency_key":"loopback-send-once"}`))
	send.Params = map[string]string{"draft": "loopback-draft"}
	result := handler(ctx, send)
	var unknown store.EmailDraft
	if result.Status != 200 || json.Unmarshal(result.Body, &unknown) != nil || unknown.State != "unknown" || unknown.Snapshot == nil {
		t.Fatalf("missing unknown fence %d %s", result.Status, result.Body)
	}
	if replay := handler(ctx, send); replay.Status != 200 || !bytes.Equal(replay.Body, result.Body) {
		t.Fatal("lost receipt triggered different response")
	}
	reconcile := domainTestRequest("mail.drafts.reconcile", []byte(`{}`))
	reconcile.Params = send.Params
	result = handler(ctx, reconcile)
	var confirmed store.EmailDraft
	if result.Status != 200 || json.Unmarshal(result.Body, &confirmed) != nil || confirmed.State != "sent" || confirmed.Receipt == nil || confirmed.Receipt.ProviderMessageID != "loopback-message-1" {
		t.Fatalf("reconciliation %d %s", result.Status, result.Body)
	}
	if replay := handler(ctx, reconcile); replay.Status != 200 || !bytes.Equal(replay.Body, result.Body) {
		t.Fatal("reconcile receipt did not replay")
	}
	mu.Lock()
	defer mu.Unlock()
	if sends != 1 || reconciles != 1 || sent.Body != body || sent.To[0] != "sink@example.test" || sent.InvocationID == "" {
		t.Fatalf("side effects sends=%d reconcile=%d", sends, reconciles)
	}
}
