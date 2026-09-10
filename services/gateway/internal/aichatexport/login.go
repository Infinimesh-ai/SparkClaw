package aichatexport

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

var Providers = []string{"chatgpt", "claude", "gemini", "grok"}

type LoginController interface {
	Controller
	Status(context.Context) browsercontrol.Status
	OpenProviderLogin(context.Context, browsercontrol.OpenProviderLoginRequest) (browsercontrol.OpenProviderLoginResult, error)
}
type LoginStatus struct {
	Provider  string     `json:"provider"`
	State     string     `json:"state"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	ErrorCode string     `json:"error_code,omitempty"`
}
type LoginOverview struct {
	generation int64

	BrowserState string        `json:"browser_state"`
	ProfileID    string        `json:"profile_id"`
	Providers    []LoginStatus `json:"providers"`
}
type LoginManager struct {
	operations           sync.Mutex
	controller           LoginController
	mu                   sync.Mutex
	checks               map[string]LoginStatus
	profile              string
	generation           int64
	controllerGeneration int64
}

func NewLoginManager(c LoginController) *LoginManager {
	return &LoginManager{controller: c, checks: map[string]LoginStatus{}}
}
func (m *LoginManager) Overview(ctx context.Context) LoginOverview {
	b := m.controller.Status(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.profile != b.ProfileID || m.generation != b.CredentialGeneration || m.controllerGeneration != b.ControllerGeneration || b.State != "ready" {
		m.checks = map[string]LoginStatus{}
		m.profile = b.ProfileID
		m.generation = b.CredentialGeneration
		m.controllerGeneration = b.ControllerGeneration
	}
	out := LoginOverview{generation: b.CredentialGeneration, BrowserState: b.State, ProfileID: b.ProfileID, Providers: []LoginStatus{}}
	for _, p := range Providers {
		s, ok := m.checks[p]
		if !ok || s.CheckedAt == nil || time.Since(*s.CheckedAt) > 5*time.Minute {
			s = LoginStatus{Provider: p, State: "unchecked"}
		}
		out.Providers = append(out.Providers, s)
	}
	return out
}
func (m *LoginManager) OpenLogin(ctx context.Context, p string) (LoginOverview, error) {
	if !m.operations.TryLock() {
		return m.Overview(ctx), &browsercontrol.Error{Code: browsercontrol.CodeBusy, Retryable: true}
	}
	defer m.operations.Unlock()
	if urls[p] == nil {
		return LoginOverview{}, errors.New("ai_platform_invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := m.controller.OpenProviderLogin(ctx, browsercontrol.OpenProviderLoginRequest{Provider: p, TaskID: "ai-login-" + rand.Text(), WaitTimeoutMS: 0})
	if err != nil {
		return m.Overview(ctx), err
	}
	m.mu.Lock()
	delete(m.checks, p)
	m.mu.Unlock()
	return m.Overview(ctx), nil
}
func (m *LoginManager) Check(ctx context.Context, p string) (LoginOverview, error) {
	if !m.operations.TryLock() {
		return m.Overview(ctx), &browsercontrol.Error{Code: browsercontrol.CodeBusy, Retryable: true}
	}
	defer m.operations.Unlock()
	if urls[p] == nil {
		return LoginOverview{}, errors.New("ai_platform_invalid")
	}
	before := m.Overview(ctx)
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	session, err := m.controller.AcquireSession(ctx, "ai-check-"+rand.Text(), 0, time.Minute)
	state, code := "unconfirmed", ""
	if err == nil {
		var result map[string]any
		result, err = session.Execute(ctx, "ai_platform.check", map[string]any{"provider": p})
		release, done := context.WithTimeout(context.Background(), 5*time.Second)
		releaseErr := session.Release(release)
		done()
		if err == nil {
			err = releaseErr
		}
		if err == nil && result["provider"] == p {
			switch result["state"] {
			case "signed_in", "signed_out", "user_action_required", "unconfirmed":
				state = result["state"].(string)
			}
		}
	}
	if err != nil {
		code = browsercontrol.ErrorCode(err)
		if code == "" {
			code = "browser_controller_unavailable"
		}
	}
	now := time.Now().UTC()
	after := m.Overview(ctx)
	m.mu.Lock()
	// Never apply a probe from another browser profile. Current results are display-only.
	if before.ProfileID == after.ProfileID && before.generation == after.generation && after.BrowserState == "ready" {
		m.checks[p] = LoginStatus{Provider: p, State: state, CheckedAt: &now, ErrorCode: code}
	}
	m.mu.Unlock()
	return m.Overview(ctx), err
}
