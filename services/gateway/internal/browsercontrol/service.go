package browsercontrol

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
)

const (
	credentialBinding = "browser-control:playwright-extension:default"
	credentialKind    = "playwright-extension-token-v1"

	StateNotConfigured          = "not_configured"
	StateChecking               = "checking"
	StateReady                  = "ready"
	StateNeedsAttention         = "needs_attention"
	StateTemporarilyUnavailable = "temporarily_unavailable"
	StateVaultUnavailable       = "vault_unavailable"

	minTokenBytes = 16
	maxTokenBytes = 4096
)

type BindingVault interface {
	Ready() error
	OpenBinding(context.Context, string, string) ([]byte, bool, error)
	ReplaceBinding(context.Context, string, string, []byte) error
	DeleteBinding(context.Context, string, string) error
}

type Status struct {
	Configured           bool      `json:"configured"`
	State                string    `json:"state"`
	ProfileID            string    `json:"profile_id"`
	CredentialGeneration int64     `json:"credential_generation"`
	ControllerGeneration int64     `json:"controller_generation,omitempty"`
	SessionGeneration    int64     `json:"session_generation,omitempty"`
	PageGeneration       int64     `json:"page_generation,omitempty"`
	LastValidatedAt      time.Time `json:"last_validated_at,omitempty"`
	ErrorCode            string    `json:"error_code,omitempty"`
	Versions             Versions  `json:"versions"`
}

type Service struct {
	vault     BindingVault
	client    ControllerClient
	profileID string
	now       func() time.Time

	opMu  sync.Mutex
	mu    sync.RWMutex
	state Status
}

func New(vault BindingVault, client ControllerClient, profileID string) *Service {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = "default"
	}
	service := &Service{
		vault: vault, client: client, profileID: profileID, now: func() time.Time { return time.Now().UTC() },
		state: Status{State: StateNotConfigured, ProfileID: profileID},
	}
	if vault == nil || vault.Ready() != nil {
		service.state.State = StateVaultUnavailable
		service.state.ErrorCode = CodeVaultUnavailable
	}
	return service
}

func (s *Service) Initialize(ctx context.Context) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.vault == nil || s.vault.Ready() != nil {
		s.publishFailure(StateVaultUnavailable, CodeVaultUnavailable)
		return
	}
	token, found, err := s.vault.OpenBinding(ctx, credentialBinding, credentialKind)
	defer zero(token)
	if err != nil {
		s.publishFailure(StateVaultUnavailable, CodeVaultUnavailable)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Configured = found
	s.state.State = StateNotConfigured
	s.state.ErrorCode = ""
	if found {
		s.state.CredentialGeneration = 1
		s.state.State = StateNeedsAttention
	}
}

func (s *Service) Status(context.Context) Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Service) SaveToken(ctx context.Context, candidate []byte) (Status, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := validateToken(candidate); err != nil {
		return s.Status(ctx), err
	}
	if s.vault == nil || s.vault.Ready() != nil {
		err := newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
		s.publishFailure(StateVaultUnavailable, err.Code)
		return s.Status(ctx), err
	}
	if s.client == nil {
		err := newError(CodeControllerUnavailable, true, errors.New("browser controller client is unavailable"))
		s.publishFailure(StateTemporarilyUnavailable, err.Code)
		return s.Status(ctx), err
	}
	s.publishChecking()
	result, err := s.client.ValidateToken(ctx, s.profileID, candidate)
	if err != nil {
		s.publishValidationFailure(err)
		return s.Status(ctx), err
	}
	if err := s.vault.ReplaceBinding(ctx, credentialBinding, credentialKind, candidate); err != nil {
		mapped := mapVaultError(err)
		s.publishFailure(StateVaultUnavailable, mapped.Code)
		return s.Status(ctx), mapped
	}
	s.publishReady(result, true)
	return s.Status(ctx), nil
}

func (s *Service) Check(ctx context.Context) (Status, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.vault == nil || s.vault.Ready() != nil {
		err := newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
		s.publishFailure(StateVaultUnavailable, err.Code)
		return s.Status(ctx), err
	}
	token, found, err := s.vault.OpenBinding(ctx, credentialBinding, credentialKind)
	defer zero(token)
	if err != nil {
		mapped := mapVaultError(err)
		s.publishFailure(StateVaultUnavailable, mapped.Code)
		return s.Status(ctx), mapped
	}
	if !found {
		err := newError(CodeNotConfigured, false, errors.New("browser extension credential is missing"))
		s.publishFailure(StateNotConfigured, err.Code)
		return s.Status(ctx), err
	}
	if s.client == nil {
		err := newError(CodeControllerUnavailable, true, errors.New("browser controller client is unavailable"))
		s.publishFailure(StateTemporarilyUnavailable, err.Code)
		return s.Status(ctx), err
	}
	s.publishChecking()
	result, err := s.client.ValidateToken(ctx, s.profileID, token)
	if err != nil {
		s.publishValidationFailure(err)
		return s.Status(ctx), err
	}
	s.publishReady(result, false)
	return s.Status(ctx), nil
}

func (s *Service) Remove(ctx context.Context) (Status, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.vault == nil || s.vault.Ready() != nil {
		err := newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
		s.publishFailure(StateVaultUnavailable, err.Code)
		return s.Status(ctx), err
	}
	if err := s.vault.DeleteBinding(ctx, credentialBinding, credentialKind); err != nil {
		mapped := mapVaultError(err)
		s.publishFailure(StateVaultUnavailable, mapped.Code)
		return s.Status(ctx), mapped
	}
	s.mu.Lock()
	s.state.Configured = false
	s.state.State = StateNotConfigured
	s.state.CredentialGeneration++
	s.state.ControllerGeneration = 0
	s.state.SessionGeneration = 0
	s.state.PageGeneration = 0
	s.state.LastValidatedAt = time.Time{}
	s.state.ErrorCode = ""
	s.state.Versions = Versions{}
	s.mu.Unlock()
	return s.Status(ctx), nil
}

func (s *Service) publishChecking() {
	s.mu.Lock()
	s.state.State = StateChecking
	s.state.ErrorCode = ""
	s.mu.Unlock()
}

func (s *Service) publishReady(result ValidationResult, replaced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Configured = true
	s.state.State = StateReady
	if replaced || s.state.CredentialGeneration == 0 {
		s.state.CredentialGeneration++
	}
	s.state.ControllerGeneration = result.ControllerGeneration
	s.state.SessionGeneration = result.SessionGeneration
	s.state.PageGeneration = result.PageGeneration
	s.state.LastValidatedAt = s.now()
	s.state.ErrorCode = ""
	s.state.Versions = result.Versions
}

func (s *Service) publishValidationFailure(err error) {
	state := StateTemporarilyUnavailable
	if ErrorCode(err) == CodeExtensionRejected {
		state = StateNeedsAttention
	}
	s.publishFailure(state, ErrorCode(err))
}

func (s *Service) publishFailure(state, code string) {
	s.mu.Lock()
	s.state.State = state
	s.state.ErrorCode = code
	s.mu.Unlock()
}

func validateToken(token []byte) error {
	if len(token) < minTokenBytes || len(token) > maxTokenBytes || !utf8.Valid(token) {
		return newError(CodeInvalidRequest, false, errors.New("token size or encoding is invalid"))
	}
	if len(bytes.TrimSpace(token)) != len(token) {
		return newError(CodeInvalidRequest, false, errors.New("token has surrounding whitespace"))
	}
	for _, value := range token {
		if value < 0x20 || value == 0x7f {
			return newError(CodeInvalidRequest, false, errors.New("token has control characters"))
		}
	}
	return nil
}

func mapVaultError(err error) *Error {
	switch credential.ErrorCode(err) {
	case credential.CodeInvalid:
		return newError(CodeInvalidRequest, false, err)
	case credential.CodeCanceled, credential.CodeUnavailable:
		return newError(CodeVaultUnavailable, true, err)
	default:
		return newError(CodeVaultUnavailable, false, err)
	}
}
