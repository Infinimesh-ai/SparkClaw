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

var mailProviderOperations = []string{wb.OperationMailProvidersList, wb.OperationMailProvidersUpdate, wb.OperationMailProvidersCheck}

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
	// Trusted ISCP sessions are not narrowed by grant scopes or qualification.
	a.config.QualifiedCapabilities = mailProviderOperations[:2]
	if !inspect(nil).Enabled {
		t.Fatal("scope-free trusted session lost mail settings")
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
	if !slices.Equal(controller.actions, []string{"update", "check"}) {
		t.Fatal(controller.actions)
	}
	for _, body := range []string{`{"intake_enabled":true,"expected_mailbox_version":0}`, `{"enabled":true}`, `{"enabled":true,"expected_version":-1}`} {
		request := v2Request(wb.OperationMailProvidersUpdate, []byte(body))
		request.Params = map[string]string{"provider": "outlook"}
		requireV2Status(t, f.call(t, request), 400)
	}
	invalid := v2Request(wb.OperationMailProvidersCheck, []byte(`{"url":"https://evil.test"}`))
	invalid.Params = map[string]string{"provider": "outlook"}
	requireV2Status(t, f.call(t, invalid), 400)
	if len(controller.actions) != 2 {
		t.Fatal("invalid input reached provider")
	}
}

// Desktop mail settings only check sign-in state; opening the backend sign-in
// browser is not an ISCP operation.
func TestISCPMailProviderLoginBrowserIsNotAnOperation(t *testing.T) {
	if _, found := wb.LookupOperation("mail.providers.login"); found {
		t.Fatal("mail sign-in browser is registered as an ISCP operation")
	}
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	controller := &fakeEmailController{}
	server.email = controller
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest("mail.providers.login", []byte(`{}`))
	request.Params = map[string]string{"provider": "qq_mail"}
	if result := handler(domainTestContext(t), request); result.Status < 400 {
		t.Fatalf("login browser request was accepted: %+v", result)
	}
	if len(controller.actions) != 0 {
		t.Fatal("login browser request reached the provider", controller.actions)
	}
}
