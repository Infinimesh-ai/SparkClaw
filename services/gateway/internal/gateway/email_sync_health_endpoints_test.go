package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Only age the returned fixture record; preserve the real default File store
// and all production query, owner isolation and transport code underneath it.
type syncHealthEndpointRepository struct{ *store.FileStore }

func (s syncHealthEndpointRepository) ListEmailMailboxes(ctx context.Context, owner string) ([]app.EmailMailbox, error) {
	boxes, err := s.FileStore.ListEmailMailboxes(ctx, owner)
	for i := range boxes {
		boxes[i].IntakeEnabledAt = time.Now().Add(-2 * time.Hour)
		boxes[i].PollThrough = time.Now().Add(-time.Hour)
	}
	return boxes, err
}

func TestEmailCheckHealthAcrossHTTPAndEncryptedISCP(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	controller := &fakeEmailController{result: emailautomation.ProviderStatus{Provider: app.EmailProviderGmail, Enabled: true, State: app.EmailStateReady, Version: 3}}
	server.email = controller
	browser := &noEmailBrowser{}
	service, err := emailmanagement.New(syncHealthEndpointRepository{repo}, browser, emailautomation.DefaultRegistry(), nil, nil, emailmanagement.Options{WorkspaceRoot: t.TempDir(), QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
	if err != nil {
		t.Fatal(err)
	}
	server.emailManagement = service
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "iscp-owner", CommandKey: "health-bind"}, Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f := startV2EncryptedFixture(t, server, cfg, []string{"mail.read"}, mailProviderOperations)
	f.bind(t)
	check := func(transport string) (int, []byte) {
		if transport == "iscp" {
			request := v2Request(wb.OperationMailProvidersCheck, []byte(`{}`))
			request.Params = map[string]string{"provider": app.EmailProviderGmail}
			result := f.call(t, request)
			return result.Status, result.Body
		}
		request := httptest.NewRequest(http.MethodPost, "/api/email/providers/gmail/check", bytes.NewBufferString(`{}`)).WithContext(domainTestContext(t))
		request.SetPathValue("provider", app.EmailProviderGmail)
		response := httptest.NewRecorder()
		server.checkEmailProvider(response, request)
		return response.Code, response.Body.Bytes()
	}
	for _, transport := range []string{"http", "iscp"} {
		t.Run(transport, func(t *testing.T) {
			code, raw := check(transport)
			var response struct {
				emailautomation.ProviderStatus
				Health emailmanagement.SyncHealth `json:"sync_health"`
			}
			if code != 200 || json.Unmarshal(raw, &response) != nil || response.State != app.EmailStateReady || response.Health.State != emailmanagement.SyncHealthStalled || response.Health.Code != "email_sync_stalled" {
				t.Fatalf("code=%d body=%s", code, raw)
			}
			for _, failure := range []app.ToolErrorCode{app.ToolErrorEmailBrowserCleanupPending, app.ToolErrorEmailLoginRequired, app.ToolErrorEmailBrowserBusy, app.ToolErrorEmailProviderUnavailable} {
				controller.err = &emailautomation.Error{Code: failure, Message: "original provider diagnostic"}
				code, raw = check(transport)
				if code == 200 || !bytes.Contains(raw, []byte(failure)) || bytes.Contains(raw, []byte("sync_health")) {
					t.Fatalf("failure=%s code=%d body=%s", failure, code, raw)
				}
			}
			controller.err = nil
		})
	}
	if browser.calls != 0 {
		t.Fatal("health projection invoked browser", browser.calls)
	}
	unchanged, _, err := repo.GetEmailMailbox(t.Context(), "iscp-owner", box.ID)
	if err != nil || unchanged.Version != box.Version || !unchanged.PollThrough.Equal(box.PollThrough) {
		t.Fatal("check changed business checkpoint", err)
	}
}
