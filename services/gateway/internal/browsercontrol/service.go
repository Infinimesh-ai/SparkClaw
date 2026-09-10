package browsercontrol

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"golang.org/x/sync/semaphore"
)

const (
	credentialBinding = "browser-control:playwright-extension:default"
	credentialKind    = "playwright-extension-token-v1"

	minTokenBytes = 16
	maxTokenBytes = 4096

	defaultCloseTimeout = 5 * time.Second
)

type BindingVault interface {
	Ready() error
	OpenBindingVersion(context.Context, string, string) ([]byte, int64, bool, error)
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
	vault        BindingVault
	client       ControllerClient
	profileID    string
	now          func() time.Time
	closeTimeout time.Duration

	operations *semaphore.Weighted
	mu         sync.RWMutex
	state      Status
	active     *RuntimeSession
}

func New(vault BindingVault, client ControllerClient, profileID string) *Service {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = "default"
	}
	service := &Service{
		vault: vault, client: client, profileID: profileID, now: func() time.Time { return time.Now().UTC() },
		closeTimeout: defaultCloseTimeout,
		operations:   semaphore.NewWeighted(exclusiveOperationWeight),
		state:        Status{State: app.IntegrationStateNotConfigured, ProfileID: profileID},
	}
	if vault == nil || vault.Ready() != nil {
		service.state.State = app.IntegrationStateVaultUnavailable
		service.state.ErrorCode = CodeVaultUnavailable
	}
	return service
}

func (s *Service) Initialize(ctx context.Context) {
	release, err := s.acquireOperations(ctx, true)
	if err != nil {
		return
	}
	defer release()
	if s.vault == nil || s.vault.Ready() != nil {
		s.publishFailure(app.IntegrationStateVaultUnavailable, CodeVaultUnavailable)
		return
	}
	token, generation, found, err := s.vault.OpenBindingVersion(ctx, credentialBinding, credentialKind)
	defer zero(token)
	if err != nil {
		s.publishFailure(app.IntegrationStateVaultUnavailable, CodeVaultUnavailable)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Configured = found
	s.state.State = app.IntegrationStateNotConfigured
	s.state.ErrorCode = ""
	if found {
		if generation <= 0 {
			s.state.State = app.IntegrationStateVaultUnavailable
			s.state.ErrorCode = CodeVaultUnavailable
			return
		}
		s.state.CredentialGeneration = generation
		s.state.State = app.IntegrationStateNeedsAttention
	}
}

func (s *Service) Status(context.Context) Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Service) SaveToken(ctx context.Context, candidate []byte) (Status, error) {
	release, err := s.acquireOperations(ctx, true)
	if err != nil {
		return s.Status(ctx), err
	}
	defer release()
	if err := validateToken(candidate); err != nil {
		return s.Status(ctx), err
	}
	if s.vault == nil || s.vault.Ready() != nil {
		err := newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
		s.publishFailure(app.IntegrationStateVaultUnavailable, err.Code)
		return s.Status(ctx), err
	}
	if s.client == nil {
		err := newError(CodeControllerUnavailable, true, errors.New("browser controller client is unavailable"))
		s.publishFailure(app.IntegrationStateTemporarilyUnavailable, err.Code)
		return s.Status(ctx), err
	}
	if err := s.releaseActiveLocked(ctx); err != nil {
		s.publishValidationFailure(err)
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
		s.publishFailure(app.IntegrationStateVaultUnavailable, mapped.Code)
		return s.Status(ctx), mapped
	}
	stored, generation, found, err := s.vault.OpenBindingVersion(ctx, credentialBinding, credentialKind)
	defer zero(stored)
	if err != nil || !found || generation <= 0 || !bytes.Equal(stored, candidate) {
		var mapped *Error
		if err != nil {
			mapped = mapVaultError(err)
		} else {
			mapped = newError(CodeVaultUnavailable, false, errors.New("persisted browser credential is unavailable"))
		}
		s.publishFailure(app.IntegrationStateVaultUnavailable, mapped.Code)
		return s.Status(ctx), mapped
	}
	s.publishReady(result, generation)
	return s.Status(ctx), nil
}

func (s *Service) Check(ctx context.Context) (Status, error) {
	release, err := s.acquireOperations(ctx, true)
	if err != nil {
		return s.Status(ctx), err
	}
	defer release()
	if s.vault == nil || s.vault.Ready() != nil {
		err := newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
		s.publishFailure(app.IntegrationStateVaultUnavailable, err.Code)
		return s.Status(ctx), err
	}
	token, generation, found, err := s.vault.OpenBindingVersion(ctx, credentialBinding, credentialKind)
	defer zero(token)
	if err != nil {
		mapped := mapVaultError(err)
		s.publishFailure(app.IntegrationStateVaultUnavailable, mapped.Code)
		return s.Status(ctx), mapped
	}
	if !found {
		err := newError(CodeNotConfigured, false, errors.New("browser extension credential is missing"))
		s.publishFailure(app.IntegrationStateNotConfigured, err.Code)
		return s.Status(ctx), err
	}
	if generation <= 0 {
		err := newError(CodeVaultUnavailable, false, errors.New("browser credential generation is unavailable"))
		s.publishFailure(app.IntegrationStateVaultUnavailable, err.Code)
		return s.Status(ctx), err
	}
	if s.client == nil {
		err := newError(CodeControllerUnavailable, true, errors.New("browser controller client is unavailable"))
		s.publishFailure(app.IntegrationStateTemporarilyUnavailable, err.Code)
		return s.Status(ctx), err
	}
	s.publishChecking()
	result, err := s.client.ValidateToken(ctx, s.profileID, token)
	if err != nil {
		s.publishValidationFailure(err)
		return s.Status(ctx), err
	}
	s.publishReady(result, generation)
	return s.Status(ctx), nil
}

func (s *Service) Remove(ctx context.Context) (Status, error) {
	release, err := s.acquireOperations(ctx, true)
	if err != nil {
		return s.Status(ctx), err
	}
	defer release()
	if s.vault == nil || s.vault.Ready() != nil {
		err := newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
		s.publishFailure(app.IntegrationStateVaultUnavailable, err.Code)
		return s.Status(ctx), err
	}
	if err := s.releaseActiveLocked(ctx); err != nil {
		s.publishValidationFailure(err)
		return s.Status(ctx), err
	}
	if err := s.vault.DeleteBinding(ctx, credentialBinding, credentialKind); err != nil {
		mapped := mapVaultError(err)
		s.publishFailure(app.IntegrationStateVaultUnavailable, mapped.Code)
		return s.Status(ctx), mapped
	}
	s.mu.Lock()
	s.state.Configured = false
	s.state.State = app.IntegrationStateNotConfigured
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

func (s *Service) releaseActiveLocked(ctx context.Context) error {
	s.mu.RLock()
	active := s.active
	s.mu.RUnlock()
	if active == nil {
		return nil
	}
	return active.Release(ctx)
}

func (s *Service) publishChecking() {
	s.mu.Lock()
	s.state.State = app.IntegrationStateChecking
	s.state.ErrorCode = ""
	s.mu.Unlock()
}

func (s *Service) publishReady(result ValidationResult, credentialGeneration int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Configured = true
	s.state.State = app.IntegrationStateReady
	s.state.CredentialGeneration = credentialGeneration
	s.state.ControllerGeneration = result.ControllerGeneration
	s.state.SessionGeneration = result.SessionGeneration
	s.state.PageGeneration = result.PageGeneration
	s.state.LastValidatedAt = s.now()
	s.state.ErrorCode = ""
	s.state.Versions = result.Versions
}

func (s *Service) publishValidationFailure(err error) {
	state := app.IntegrationStateTemporarilyUnavailable
	if ErrorCode(err) == CodeExtensionRejected {
		state = app.IntegrationStateNeedsAttention
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
