package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type fakeEmailController struct {
	providers    []emailautomation.ProviderStatus
	result       emailautomation.ProviderStatus
	err          error
	updateInputs []emailautomation.UpdateProviderInput
	providerIDs  []string
	actions      []string
}

func (f *fakeEmailController) List(context.Context, string) ([]emailautomation.ProviderStatus, error) {
	return append([]emailautomation.ProviderStatus(nil), f.providers...), f.err
}

func (f *fakeEmailController) Update(_ context.Context, _, _ string, providerID string, input emailautomation.UpdateProviderInput) (emailautomation.ProviderStatus, error) {
	f.actions = append(f.actions, "update")
	f.providerIDs = append(f.providerIDs, providerID)
	f.updateInputs = append(f.updateInputs, input)
	return f.result, f.err
}

func (f *fakeEmailController) OpenLoginBrowser(_ context.Context, _, _, providerID string) (emailautomation.ProviderStatus, error) {
	f.actions = append(f.actions, "login")
	f.providerIDs = append(f.providerIDs, providerID)
	return f.result, f.err
}

func (f *fakeEmailController) Check(_ context.Context, _, _, providerID string) (emailautomation.ProviderStatus, error) {
	f.actions = append(f.actions, "check")
	f.providerIDs = append(f.providerIDs, providerID)
	return f.result, f.err
}

func TestEmailProviderEndpointsExposeVersionedConfigurationActions(t *testing.T) {
	status := emailautomation.ProviderStatus{
		Provider: app.EmailProviderGmail, DisplayName: "Gmail", Enabled: true, Default: true,
		Account: app.EmailAccountDefault, AccountHint: "a***@gmail.com", State: app.EmailStateReady, Version: 4,
	}
	controller := &fakeEmailController{providers: []emailautomation.ProviderStatus{status}, result: status}
	server := testEmailServer(t, controller)

	response, err := http.Get(server.URL + "/api/email/providers")
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Providers []emailautomation.ProviderStatus `json:"providers"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&listed) != nil || len(listed.Providers) != 1 || listed.Providers[0].AccountHint != "a***@gmail.com" {
		t.Fatalf("list response status=%d providers=%#v", response.StatusCode, listed.Providers)
	}
	response.Body.Close()

	request, err := http.NewRequest(http.MethodPatch, server.URL+"/api/email/providers/GMAIL", bytes.NewBufferString(`{"enabled":false,"expected_version":4}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(controller.updateInputs) != 1 || controller.providerIDs[0] != app.EmailProviderGmail ||
		controller.updateInputs[0].ExpectedVersion != 4 || controller.updateInputs[0].Enabled == nil || *controller.updateInputs[0].Enabled {
		t.Fatalf("patch status=%d providers=%v inputs=%#v", response.StatusCode, controller.providerIDs, controller.updateInputs)
	}

	for _, action := range []string{"login-browser", "check"} {
		response, err = http.Post(server.URL+"/api/email/providers/gmail/"+action, "application/json", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", action, response.StatusCode)
		}
	}
	if len(controller.actions) != 3 || controller.actions[1] != "login" || controller.actions[2] != "check" {
		t.Fatalf("actions = %v", controller.actions)
	}
}

func TestEmailProviderEndpointsRejectMalformedAndMapTypedFailures(t *testing.T) {
	controller := &fakeEmailController{}
	server := testEmailServer(t, controller)
	request, err := http.NewRequest(http.MethodPatch, server.URL+"/api/email/providers/gmail", bytes.NewBufferString(`{"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || len(controller.actions) != 0 {
		t.Fatalf("malformed patch status=%d actions=%v", response.StatusCode, controller.actions)
	}

	controller.err = &emailautomation.Error{Code: emailautomation.CodeAdmissionStale, Message: "stale"}
	response, err = http.Post(server.URL+"/api/email/providers/gmail/check", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale check status=%d", response.StatusCode)
	}
	controller.err = &emailautomation.Error{Code: emailautomation.CodeProviderUnavailable, Message: "unavailable"}
	response, err = http.Get(server.URL + "/api/email/providers")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unavailable list status=%d", response.StatusCode)
	}
}

func testEmailServer(t *testing.T, controller EmailController) *httptest.Server {
	t.Helper()
	cfg := testConfig(t.TempDir())
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	server := httptest.NewServer(New(cfg, st, tools, runtime, WithEmailController(controller)).Handler())
	t.Cleanup(server.Close)
	return server
}
