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
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type gatewayDeliveryProvider struct {
	key       string
	calls     []app.DeliveryRequest
	partialOn int
}

func (p *gatewayDeliveryProvider) Key() string { return p.key }
func (p *gatewayDeliveryProvider) Capabilities() app.DeliveryCapabilities {
	return app.DeliveryCapabilities{
		Kinds:             []app.MessagePartKind{app.MessagePartText, app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile},
		Dispositions:      []app.MessagePartDisposition{app.MessageDispositionInline, app.MessageDispositionAttachment, app.MessageDispositionVoiceNote},
		FileFallbackKinds: []app.MessagePartKind{app.MessagePartAudio}, MaxParts: 8, MaxTotalBytes: 25 << 20,
		SupportsCaption: true, SupportsFileFallback: true,
	}
}
func (p *gatewayDeliveryProvider) Deliver(_ context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	p.calls = append(p.calls, request)
	now := time.Now().UTC()
	receipt := app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded, Attempt: 1, AttemptedAt: now, DeliveredAt: &now}
	for index, part := range request.Content.Parts {
		status := "sent"
		code := ""
		if p.partialOn > 0 && len(p.calls) == p.partialOn && index > 0 {
			status = "failed"
			code = delivery.CodeProviderRetryable
		}
		receipt.PartReceipts = append(receipt.PartReceipts, app.PartDeliveryReceipt{PartID: part.ID, Status: status, Representation: "native", ErrorCode: code})
	}
	if p.partialOn > 0 && len(p.calls) == p.partialOn {
		receipt.Status = app.DeliveryPartiallySent
		receipt.ErrorCode = delivery.CodeProviderRetryable
		receipt.RetryState = "retryable"
		return receipt, delivery.NewError(delivery.CodeProviderRetryable, "provider temporarily unavailable", "retryable")
	}
	return receipt, nil
}

func TestDeliveryAPIListsOpaqueExactEndpointsAndSendsAllCanonicalParts(t *testing.T) {
	ts, st, provider, endpointID, artifacts := newDeliveryTestServer(t, 0)

	resp, err := http.Get(ts.URL + "/api/delivery-endpoints")
	if err != nil {
		t.Fatal(err)
	}
	raw := readResponse(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(raw), "bind-direct") || strings.Contains(string(raw), "external-user-raw") || strings.Contains(string(raw), "ctx-secret") {
		t.Fatalf("endpoint catalog leaked internal address data: status=%d body=%s", resp.StatusCode, raw)
	}
	var endpoints struct {
		Endpoints []publicDeliveryEndpoint `json:"endpoints"`
	}
	if err := json.Unmarshal(raw, &endpoints); err != nil {
		t.Fatal(err)
	}
	if len(endpoints.Endpoints) != 1 || endpoints.Endpoints[0].ID != endpointID || !strings.HasPrefix(endpoints.Endpoints[0].Recipient.ID, "recipient:") {
		t.Fatalf("unexpected endpoint catalog: %#v", endpoints)
	}

	payload := map[string]any{
		"target": endpointID, "idempotency_key": "web-key-1", "confirmed": true,
		"content": map[string]any{"parts": []map[string]any{
			{"id": "text", "kind": "text", "disposition": "inline", "text": "hello"},
			{"id": "image", "kind": "image", "disposition": "attachment", "artifact_id": artifacts[0]},
			{"id": "audio", "kind": "audio", "disposition": "voice_note", "artifact_id": artifacts[1]},
			{"id": "file", "kind": "file", "disposition": "attachment", "artifact_id": artifacts[2]},
		}},
	}
	created := postJSON(t, ts.URL+"/api/deliveries", payload)
	createdRaw := readResponse(t, created)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("delivery returned %d: %s", created.StatusCode, createdRaw)
	}
	if len(provider.calls) != 1 || len(provider.calls[0].Content.Parts) != 4 {
		t.Fatalf("provider did not receive all ordered parts: %#v", provider.calls)
	}
	for _, part := range provider.calls[0].Content.Parts[1:] {
		if part.Resource == nil || part.Bytes == 0 || part.SHA256 == "" {
			t.Fatalf("binary part was not governed before delivery: %#v", part)
		}
	}
	if runs := testListRuns(st, ""); len(runs) != 0 {
		t.Fatalf("Web direct send created an Agent run: %#v", runs)
	}

	replay := postJSON(t, ts.URL+"/api/deliveries", payload)
	if replay.StatusCode != http.StatusOK || len(provider.calls) != 1 {
		t.Fatalf("idempotent replay sent twice: status=%d calls=%d body=%s", replay.StatusCode, len(provider.calls), readResponse(t, replay))
	}
	payload["content"].(map[string]any)["parts"].([]map[string]any)[0]["text"] = "changed"
	conflict := postJSON(t, ts.URL+"/api/deliveries", payload)
	if conflict.StatusCode != http.StatusConflict || !strings.Contains(string(readResponse(t, conflict)), delivery.CodeIdempotencyConflict) || len(provider.calls) != 1 {
		t.Fatal("idempotency conflict was not blocked")
	}
}

func TestDeliveryAPIRetriesOnlyFailedPartsAndRechecksRevocation(t *testing.T) {
	ts, st, provider, endpointID, artifacts := newDeliveryTestServer(t, 1)
	payload := map[string]any{
		"target": endpointID, "idempotency_key": "web-key-retry", "confirmed": true,
		"content": map[string]any{"parts": []map[string]any{
			{"id": "text", "kind": "text", "disposition": "inline", "text": "hello"},
			{"id": "file", "kind": "file", "disposition": "attachment", "artifact_id": artifacts[2]},
		}},
	}
	failed := postJSON(t, ts.URL+"/api/deliveries", payload)
	failedRaw := readResponse(t, failed)
	if failed.StatusCode != http.StatusBadGateway || len(provider.calls) != 1 {
		t.Fatalf("expected partial provider failure: status=%d body=%s", failed.StatusCode, failedRaw)
	}
	var failure struct {
		Delivery publicDelivery `json:"delivery"`
	}
	if err := json.Unmarshal(failedRaw, &failure); err != nil {
		t.Fatal(err)
	}
	retried := postJSON(t, ts.URL+"/api/deliveries/"+string(failure.Delivery.ID)+"/retry", map[string]any{"confirmed": true})
	retriedRaw := readResponse(t, retried)
	if retried.StatusCode != http.StatusOK || len(provider.calls) != 2 || len(provider.calls[1].Content.Parts) != 1 || provider.calls[1].Content.Parts[0].ID != "file" {
		t.Fatalf("retry did not isolate failed part: status=%d calls=%#v body=%s", retried.StatusCode, provider.calls, retriedRaw)
	}

	binding, _ := storetest.MustGetNotificationBinding(t, st, "bind-direct")
	revokedBinding := binding
	revokedBinding.Status = app.NotificationBindingRevoked
	storetest.MustUpdateNotificationBinding(t, st, binding, revokedBinding)
	payload["idempotency_key"] = "web-key-after-revoke"
	revokedResponse := postJSON(t, ts.URL+"/api/deliveries", payload)
	if revokedResponse.StatusCode != http.StatusConflict || !strings.Contains(string(readResponse(t, revokedResponse)), delivery.CodeBindingUnavailable) || len(provider.calls) != 2 {
		t.Fatal("stale endpoint sent after binding revocation")
	}
}

func TestDeliveryAPIRejectsCrossOwnerArtifactBeforeProvider(t *testing.T) {
	ts, st, provider, endpointID, _ := newDeliveryTestServer(t, 0)
	otherRoot := t.TempDir()
	other := storetest.MustCreateSessionWithScope(t, st, "Other", "other-owner", otherRoot, "webchat", false)
	path := filepath.Join(otherRoot, "private.txt")
	if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	storetest.MustSaveArtifactObject(t, st, app.ArtifactObject{ID: "obj-other", SessionID: other.ID, Path: path, Key: "private.txt", Bytes: 7})
	resp := postJSON(t, ts.URL+"/api/deliveries", map[string]any{
		"target": endpointID, "idempotency_key": "web-key-cross", "confirmed": true,
		"content": map[string]any{"parts": []map[string]any{{"id": "file", "kind": "file", "disposition": "attachment", "artifact_id": "obj-other"}}},
	})
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(readResponse(t, resp)), delivery.CodeCrossUserDenied) || len(provider.calls) != 0 {
		t.Fatal("cross-owner artifact reached provider")
	}
}

func TestDeliveryAPIRejectsBindingTargetBeforeArtifactOrProvider(t *testing.T) {
	ts, _, provider, endpointID, _ := newDeliveryTestServer(t, 0)
	rejected := postJSON(t, ts.URL+"/api/deliveries", map[string]any{
		"target": "bind-direct", "idempotency_key": "web-key-binding", "confirmed": true,
		"content": map[string]any{"parts": []map[string]any{{"id": "file", "kind": "file", "disposition": "attachment", "artifact_id": "missing-artifact"}}},
	})
	rejectedRaw := readResponse(t, rejected)
	if rejected.StatusCode != http.StatusConflict || !strings.Contains(string(rejectedRaw), delivery.CodeBindingUnavailable) || len(provider.calls) != 0 {
		t.Fatalf("binding target crossed direct-send preflight: status=%d calls=%d body=%s", rejected.StatusCode, len(provider.calls), rejectedRaw)
	}
	accepted := postJSON(t, ts.URL+"/api/deliveries", map[string]any{
		"target": endpointID, "idempotency_key": "web-key-exact", "confirmed": true,
		"content": map[string]any{"parts": []map[string]any{{"id": "text", "kind": "text", "disposition": "inline", "text": "exact"}}},
	})
	acceptedRaw := readResponse(t, accepted)
	if accepted.StatusCode != http.StatusCreated || len(provider.calls) != 1 || provider.calls[0].Target != endpointID {
		t.Fatalf("exact endpoint did not pass direct-send preflight: status=%d calls=%#v body=%s", accepted.StatusCode, provider.calls, acceptedRaw)
	}
}

func newDeliveryTestServer(t *testing.T, partialOn int) (*httptest.Server, *store.MemoryStore, *gatewayDeliveryProvider, app.EndpointID, []string) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "Web", app.DefaultOwnerID, root, "webchat", false)
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "bind-direct", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Channel: "testchat", Status: "active",
		DisplayName: "Personal account", Scopes: []string{app.BindingScopeMessageSendSelf},
	})
	chat := st.SaveExternalChatSession(app.ExternalChatSession{
		ID: "endpoint-direct", OwnerID: "source-actor", AuthorizedOwnerID: app.DefaultOwnerID, AuthorizedActorID: app.DefaultOwnerID,
		BindingID: binding.ID, Channel: "testchat", ExternalUserID: "external-user-raw", ExternalChatID: "external-chat-raw",
		DisplayName: "Alex", LastContextToken: "ctx-secret", Status: "active",
	})
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{ID: "bind-reminder", OwnerID: app.DefaultOwnerID, Channel: "testchat", Status: "active", Scopes: []string{app.BindingScopeReminderSendSelf}})

	artifactIDs := []string{"obj-image", "obj-audio", "obj-file"}
	files := []struct{ name, contentType string }{{"image.png", "image/png"}, {"voice.wav", "audio/wav"}, {"report.txt", "text/plain"}}
	for index, file := range files {
		path := filepath.Join(root, file.name)
		raw := []byte("payload-" + file.name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		storetest.MustSaveArtifactObject(t, st, app.ArtifactObject{ID: artifactIDs[index], SessionID: session.ID, Path: path, Key: file.name, ContentType: file.contentType, Bytes: len(raw)})
	}
	provider := &gatewayDeliveryProvider{key: "testchat", partialOn: partialOn}
	providers := delivery.NewProviderRegistry()
	if err := providers.Register(provider); err != nil {
		t.Fatal(err)
	}
	endpoints := messagecontrol.NewEndpointRegistry(st)
	deliveryGateway := delivery.NewGateway(endpoints, providers, nil)
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime, WithMessageDelivery(endpoints, providers, deliveryGateway))
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, st, provider, app.EndpointID(chat.ID), artifactIDs
}

func postJSON(t *testing.T, url string, payload any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readResponse(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	var decoded bytes.Buffer
	if _, err := decoded.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	return decoded.Bytes()
}

func TestMessageStreamDeliveryFailureEmitsDistinctEvent(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "Web", app.DefaultOwnerID, root, "webchat", false)
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "bind-delivery-failed", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		Channel: "testchat", Status: "active", Scopes: []string{app.BindingScopeMessageSendSelf},
	})
	chat := st.SaveExternalChatSession(app.ExternalChatSession{
		ID: "endpoint-delivery-failed", OwnerID: "source-actor", AuthorizedOwnerID: app.DefaultOwnerID,
		AuthorizedActorID: app.DefaultOwnerID, BindingID: binding.ID, Channel: "testchat",
		ExternalUserID: "user", ExternalChatID: "chat", DisplayName: "Selected recipient", Status: "active",
	})
	provider := &gatewayDeliveryProvider{key: "testchat", partialOn: 1}
	providers := delivery.NewProviderRegistry()
	if err := providers.Register(provider); err != nil {
		t.Fatal(err)
	}
	endpoints := messagecontrol.NewEndpointRegistry(st)
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime, WithMessageDelivery(endpoints, providers, delivery.NewGateway(endpoints, providers, nil)))

	server.streamMessage = func(_ context.Context, _ string, _ string, _ []agent.MessageAttachment, ingress app.MessageIngressContext, _ agent.StreamHandler) (agent.Result, error) {
		return agent.Result{WorkflowResult: &app.WorkflowResult{
			SchemaVersion: app.WorkflowResultSchemaVersion,
			ID:            "result-delivery-failed",
			OwnerID:       ingress.OwnerID,
			Authorization: ingress.Authorization,
			Status:        app.WorkflowResultSucceeded,
			Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "run finished",
			}}},
			ReturnRoute: ingress.ReturnRoute,
		}}, nil
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	response := postJSON(t, ts.URL+"/api/sessions/"+session.ID+"/messages/stream", map[string]any{
		"content": "deliver externally", "target_endpoint_id": chat.ID,
	})
	raw := string(readResponse(t, response))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("stream returned %d: %s", response.StatusCode, raw)
	}
	if len(provider.calls) != 1 {
		t.Fatalf("delivery was not attempted exactly once: %#v", provider.calls)
	}
	if !strings.Contains(raw, "event: message.stream.delivery_failed") || !strings.Contains(raw, "provider temporarily unavailable") {
		t.Fatalf("post-run delivery failure did not emit a distinct event: %s", raw)
	}
	if strings.Contains(raw, "event: error") || strings.Contains(raw, "event: message.stream.final") {
		t.Fatalf("post-run delivery failure leaked into the run error or final event: %s", raw)
	}
}
