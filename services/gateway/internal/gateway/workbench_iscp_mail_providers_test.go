package gateway

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

var mailProviderOperations = []string{wb.OperationMailProvidersList, wb.OperationMailProvidersUpdate, wb.OperationMailProvidersCheck, wb.OperationMailProvidersLogin}

func TestISCPMailProviderExactPermissionAndQualification(t *testing.T) {
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	server.email = &fakeEmailController{}
	cfg.QualifiedCapabilities = slices.Clone(mailProviderOperations)
	a := &iscpDomainAdapter{server: server, config: cfg}
	inspect := func(scopes []string) iscpDomainCapability {
		result := a.capabilitiesWithSession(domainTestContext(t), domainTestRequest(wb.OperationCapabilitiesGet, nil), wb.SessionInfo{Profile: wb.ProfileV2, Scopes: scopes})
		var body struct {
			Capabilities []iscpDomainCapability `json:"capabilities"`
		}
		if json.Unmarshal(result.body, &body) != nil {
			t.Fatal(string(result.body))
		}
		for _, row := range body.Capabilities {
			if row.ID == "mail_settings" {
				return row
			}
		}
		t.Fatal("mail settings capability missing")
		return iscpDomainCapability{}
	}
	scopes := []string{"mail.read", "settings.write", "browser.login"}
	if !inspect(scopes).Enabled {
		t.Fatal("unconfigured providers must still be configurable")
	}
	for _, removed := range scopes {
		limited := slices.DeleteFunc(slices.Clone(scopes), func(v string) bool { return v == removed })
		if inspect(limited).Enabled {
			t.Fatalf("missing %s enabled mail settings", removed)
		}
	}
	a.config.QualifiedCapabilities = mailProviderOperations[:3]
	if inspect(scopes).Enabled {
		t.Fatal("unqualified login enabled")
	}
	a.config.QualifiedCapabilities = mailProviderOperations
	server.email = nil
	if inspect(scopes).Enabled {
		t.Fatal("missing provider service enabled")
	}
}

func TestISCPMailProviderUsesBackendAndReplaysSideEffectsOnce(t *testing.T) {
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	status := emailautomation.ProviderStatus{Provider: app.EmailProviderOutlook, Enabled: true, State: app.EmailStateReady, Version: 3}
	controller := &fakeEmailController{providers: []emailautomation.ProviderStatus{status}, result: status}
	server.email = controller
	f := startV2EncryptedFixture(t, server, cfg, []string{"mail.read", "browser.login"}, mailProviderOperations)
	f.bind(t)
	listed := f.call(t, v2Request(wb.OperationMailProvidersList, nil))
	requireV2Status(t, listed, 200)
	if !bytes.Contains(listed.Body, []byte("outlook")) {
		t.Fatal(string(listed.Body))
	}
	for _, operation := range mailProviderOperations[1:] {
		body := []byte(`{}`)
		if operation == wb.OperationMailProvidersUpdate {
			body = []byte(`{"enabled":true,"expected_version":3}`)
		}
		request := v2Request(operation, body)
		request.Params = map[string]string{"provider": "outlook"}
		result := f.call(t, request)
		requireV2Status(t, result, 200)
		request.ID = v2Request(operation, nil).ID
		replayed := f.call(t, request)
		requireV2Status(t, replayed, 200)
		if !bytes.Equal(result.Body, replayed.Body) {
			t.Fatal("receipt differs")
		}
	}
	if !slices.Equal(controller.actions, []string{"update", "check", "login"}) {
		t.Fatal(controller.actions)
	}
	for _, body := range []string{`{"intake_enabled":true,"expected_mailbox_version":0}`, `{"enabled":true}`, `{"enabled":true,"expected_version":-1}`} {
		request := v2Request(wb.OperationMailProvidersUpdate, []byte(body))
		request.Params = map[string]string{"provider": "outlook"}
		requireV2Status(t, f.call(t, request), 400)
	}
	invalid := v2Request(wb.OperationMailProvidersLogin, []byte(`{"url":"https://evil.test"}`))
	invalid.Params = map[string]string{"provider": "outlook"}
	requireV2Status(t, f.call(t, invalid), 400)
	if len(controller.actions) != 3 {
		t.Fatal("invalid input reached provider")
	}
}

func TestISCPMailProviderLoginNeedsSeparateScopeAndUnknownNeverReopens(t *testing.T) {
	t.Run("login scope", func(t *testing.T) {
		server, _, cfg, _ := workbenchISCPFixture(t, nil)
		controller := &fakeEmailController{}
		server.email = controller
		f := startV2EncryptedFixture(t, server, cfg, []string{"mail.read"}, mailProviderOperations)
		f.bind(t)
		request := v2Request(wb.OperationMailProvidersLogin, []byte(`{}`))
		request.Params = map[string]string{"provider": "qq_mail"}
		requireV2Status(t, f.call(t, request), 403)
		if len(controller.actions) != 0 {
			t.Fatal("login occurred without browser.login")
		}
	})
	t.Run("backend response lost", func(t *testing.T) {
		server, _, cfg, _ := workbenchISCPFixture(t, nil)
		controller := &fakeEmailController{err: &emailautomation.Error{Code: app.ToolErrorEmailProviderUnavailable, Message: "controller response lost"}}
		server.email = controller
		handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		request := domainTestRequest(wb.OperationMailProvidersLogin, []byte(`{}`))
		request.Params = map[string]string{"provider": "qq_mail"}
		result := handler(domainTestContext(t), request)
		if result.Status != 409 || !bytes.Contains(result.Body, []byte("operation_outcome_unknown")) {
			t.Fatalf("%+v", result)
		}
		restarted, err := server.NewWorkbenchISCPDomainHandler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		controller.err = nil
		if got := restarted(domainTestContext(t), request); got.Status != 409 {
			t.Fatalf("%+v", got)
		}
		if !slices.Equal(controller.actions, []string{"login"}) {
			t.Fatal("repeated unknown login", controller.actions)
		}
	})
}
