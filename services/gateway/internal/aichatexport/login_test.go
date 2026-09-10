package aichatexport

import (
	"context"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

type loginFake struct {
	status     browsercontrol.Status
	opened     []string
	ops        []string
	released   int
	probeState string
}

func (f *loginFake) Status(context.Context) browsercontrol.Status { return f.status }
func (f *loginFake) OpenProviderLogin(_ context.Context, r browsercontrol.OpenProviderLoginRequest) (browsercontrol.OpenProviderLoginResult, error) {
	f.opened = append(f.opened, r.Provider)
	return browsercontrol.OpenProviderLoginResult{}, nil
}
func (f *loginFake) AcquireSession(context.Context, string, time.Duration, time.Duration) (browsercontrol.Session, error) {
	return &loginSession{f}, nil
}

type loginSession struct{ f *loginFake }

func (s *loginSession) Lease() browsercontrol.SessionLease { return browsercontrol.SessionLease{} }
func (s *loginSession) Release(context.Context) error      { s.f.released++; return nil }
func (s *loginSession) Execute(_ context.Context, op string, args map[string]any) (map[string]any, error) {
	s.f.ops = append(s.f.ops, op)
	return map[string]any{"provider": args["provider"], "state": s.f.probeState}, nil
}
func TestLoginReuseIndependentStatusAndInvalidation(t *testing.T) {
	f := &loginFake{status: browsercontrol.Status{State: "ready", ProfileID: "default", CredentialGeneration: 2, ControllerGeneration: 1}, probeState: "signed_in"}
	m := NewLoginManager(f)
	if len(m.Overview(t.Context()).Providers) != 4 {
		t.Fatal("missing providers")
	}
	out, err := m.Check(t.Context(), "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	if out.Providers[0].State != "signed_in" || out.Providers[1].State != "unchecked" || len(f.opened) != 0 || f.released != 1 {
		t.Fatal("check did not reuse independent login", out)
	}
	if len(f.ops) != 1 || f.ops[0] != "ai_platform.check" {
		t.Fatal("check invoked export or script inspection")
	}
	out, err = m.OpenLogin(t.Context(), "chatgpt")
	if err != nil || len(f.opened) != 1 || out.Providers[0].State != "unchecked" {
		t.Fatal("opening login must invalidate, not mark signed in")
	}
	_, _ = m.Check(t.Context(), "claude")
	f.status.CredentialGeneration++
	if m.Overview(t.Context()).Providers[1].State != "unchecked" {
		t.Fatal("credential change not invalidated")
	}
	_, _ = m.Check(t.Context(), "grok")
	old := time.Now().Add(-6 * time.Minute)
	v := m.checks["grok"]
	v.CheckedAt = &old
	m.checks["grok"] = v
	if m.Overview(t.Context()).Providers[3].State != "unchecked" {
		t.Fatal("expired check retained")
	}
	if _, err = m.Check(t.Context(), "evil"); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
