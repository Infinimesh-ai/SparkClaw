package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/aichatexport"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

type aiLoginEndpointFake struct {
	browserControlEndpointController
	opened []string
	checks int
}

func (f *aiLoginEndpointFake) AcquireSession(context.Context, string, time.Duration, time.Duration) (browsercontrol.Session, error) {
	return &aiLoginEndpointSession{f}, nil
}
func (f *aiLoginEndpointFake) OpenProviderLogin(_ context.Context, r browsercontrol.OpenProviderLoginRequest) (browsercontrol.OpenProviderLoginResult, error) {
	f.opened = append(f.opened, r.Provider)
	return browsercontrol.OpenProviderLoginResult{}, nil
}

type aiLoginEndpointSession struct{ f *aiLoginEndpointFake }

func (s *aiLoginEndpointSession) Lease() browsercontrol.SessionLease {
	return browsercontrol.SessionLease{}
}
func (s *aiLoginEndpointSession) Release(context.Context) error { return nil }
func (s *aiLoginEndpointSession) Execute(_ context.Context, op string, args map[string]any) (map[string]any, error) {
	s.f.checks++
	return map[string]any{"provider": args["provider"], "state": "signed_in"}, nil
}
func TestAIPlatformLoginEndpointsAreAuthenticatedAndBounded(t *testing.T) {
	f := &aiLoginEndpointFake{browserControlEndpointController: browserControlEndpointController{status: browsercontrol.Status{State: "ready", ProfileID: "default", CredentialGeneration: 1}}}
	s := newBrowserControlEndpointTestServer(t, f, true)
	s.aiPlatformLogin = aichatexport.NewLoginManager(f)
	prefix := "/api/browser/extension/ai-platforms"
	for _, p := range []string{prefix, prefix + "/chatgpt/check", prefix + "/chatgpt/login"} {
		method := http.MethodPost
		if p == prefix {
			method = http.MethodGet
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(method, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", p, rec.Code)
		}
	}
	for _, test := range []struct{ path, body string }{{prefix + "/evil/login", `{}`}, {prefix + "/chatgpt/check?url=https://evil.test", `{}`}, {prefix + "/chatgpt/login", `{"url":"https://evil.test"}`}} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, authenticatedBrowserControlRequest(http.MethodPost, test.path, test.body))
		if rec.Code != 400 {
			t.Fatalf("accepted invalid request: %d %s", rec.Code, rec.Body.String())
		}
	}
	if len(f.opened) != 0 || f.checks != 0 {
		t.Fatal("invalid input reached browser")
	}
	for _, action := range []string{"login", "check"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, authenticatedBrowserControlRequest(http.MethodPost, prefix+"/chatgpt/"+action, `{}`))
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "script") || strings.Contains(rec.Body.String(), "token") {
			t.Fatal("unrequested script state or token exposed")
		}
	}
	if len(f.opened) != 1 || f.checks != 1 {
		t.Fatal("action not dispatched")
	}
}
