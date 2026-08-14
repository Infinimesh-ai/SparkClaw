package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

type bindingBrowserController struct {
	openedOwner string
	openedID    string
	openedURL   string
	closedID    string
}

func (c *bindingBrowserController) OpenManagedBrowserWindow(_ context.Context, ownerID, windowID, targetURL string) error {
	c.openedOwner, c.openedID, c.openedURL = ownerID, windowID, targetURL
	return nil
}
func (c *bindingBrowserController) CloseManagedBrowserWindow(_ context.Context, _ string, windowID string) error {
	c.closedID = windowID
	return nil
}

type activeQRBindingAdapter struct{}

func (activeQRBindingAdapter) Availability() error                                   { return nil }
func (activeQRBindingAdapter) Policy() binding.AdapterPolicy                         { return binding.AdapterPolicy{} }
func (activeQRBindingAdapter) Cancel(context.Context, app.NotificationBinding) error { return nil }
func (activeQRBindingAdapter) Start(_ context.Context, record app.NotificationBinding, _ binding.StartOptions) (app.NotificationBinding, error) {
	return record, nil
}
func (activeQRBindingAdapter) Poll(context.Context, app.NotificationBinding) (binding.PollResult, error) {
	return binding.PollResult{Status: "active", AccountID: "wx-account"}, nil
}

func TestNotificationBindingBrowserUsesPersistedWeixinURLAndClosesAfterActivation(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{Enabled: true, Provider: weixinproto.QRProvider}
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	controller := &bindingBrowserController{}
	router := binding.NewBaseRouter(cfg).WithAdapter("weixin", activeQRBindingAdapter{})
	server := New(cfg, st, tools, runtime, WithBindingRouter(router), WithManagedBrowserWindows(controller))

	now := time.Now().UTC()
	record := st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind-managed", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		Channel: "weixin", Provider: weixinproto.QRProvider, Status: "waiting_scan",
		QRCodeURL: "https://liteapp.weixin.qq.com/q/provider-ticket", CreatedAt: now, UpdatedAt: now,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/notification-bindings/"+record.ID+"/browser", strings.NewReader(`{"url":"https://attacker.example"}`))
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("open managed browser returned %d: %s", resp.Code, resp.Body.String())
	}
	if controller.openedOwner != app.DefaultOwnerID || controller.openedID != record.ID || controller.openedURL != record.QRCodeURL {
		t.Fatalf("managed browser did not use persisted owner-scoped binding data: %#v", controller)
	}

	pollReq := httptest.NewRequest(http.MethodGet, "/api/notification-bindings/"+record.ID, nil)
	pollResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(pollResp, pollReq)
	if pollResp.Code != http.StatusOK || controller.closedID != record.ID {
		t.Fatalf("binding activation did not close managed Chromium: status=%d controller=%#v body=%s", pollResp.Code, controller, pollResp.Body.String())
	}
}

func TestNotificationBindingBrowserRejectsUntrustedOrInactiveRecords(t *testing.T) {
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime, WithManagedBrowserWindows(&bindingBrowserController{}))
	now := time.Now().UTC()

	for _, record := range []app.NotificationBinding{
		{ID: "bad-host", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Channel: "weixin", Provider: weixinproto.QRProvider, Status: "waiting_scan", QRCodeURL: "https://attacker.example/q", CreatedAt: now, UpdatedAt: now},
		{ID: "active", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Channel: "weixin", Provider: weixinproto.QRProvider, Status: "active", QRCodeURL: "https://liteapp.weixin.qq.com/q/old", CreatedAt: now, UpdatedAt: now},
	} {
		st.SaveNotificationBinding(record)
		req := httptest.NewRequest(http.MethodPost, "/api/notification-bindings/"+record.ID+"/browser", nil)
		resp := httptest.NewRecorder()
		server.Handler().ServeHTTP(resp, req)
		if resp.Code < 400 {
			t.Fatalf("unsafe binding %#v unexpectedly opened Chromium", record)
		}
	}
}
