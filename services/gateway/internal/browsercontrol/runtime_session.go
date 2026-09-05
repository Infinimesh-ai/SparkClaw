package browsercontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	defaultRuntimeSessionTTL = 2 * time.Minute
	maxRuntimeSessionTTL     = 10 * time.Minute
	maxRuntimeWaitTimeout    = 30 * time.Second
)

type Session interface {
	Lease() SessionLease
	Execute(context.Context, string, map[string]any) (map[string]any, error)
	Release(context.Context) error
}

type RuntimeSession struct {
	service *Service
	client  ControllerClient

	mu       sync.Mutex
	lease    SessionLease
	released bool
	done     chan struct{}
}

func (s *Service) AcquireSession(
	ctx context.Context,
	taskID string,
	waitTimeout time.Duration,
	sessionTTL time.Duration,
) (Session, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	taskID = strings.TrimSpace(taskID)
	if taskID == "" || waitTimeout < 0 || waitTimeout > maxRuntimeWaitTimeout || sessionTTL < 0 || sessionTTL > maxRuntimeSessionTTL {
		return nil, newError(CodeInvalidRequest, false, errors.New("runtime browser session input is invalid"))
	}
	if sessionTTL == 0 {
		sessionTTL = defaultRuntimeSessionTTL
	}
	if s.vault == nil || s.vault.Ready() != nil {
		return nil, newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
	}
	if s.client == nil {
		return nil, newError(CodeControllerUnavailable, true, errors.New("browser controller client is unavailable"))
	}
	s.mu.RLock()
	active := s.active
	s.mu.RUnlock()
	if active != nil {
		return nil, newError(CodeBusy, true, errors.New("browser profile already has an active runtime session"))
	}

	token, credentialGeneration, found, err := s.vault.OpenBindingVersion(ctx, credentialBinding, credentialKind)
	defer zero(token)
	if err != nil {
		return nil, mapVaultError(err)
	}
	if !found {
		return nil, newError(CodeNotConfigured, false, errors.New("browser extension credential is missing"))
	}
	s.mu.RLock()
	status := s.state
	s.mu.RUnlock()
	if credentialGeneration <= 0 {
		return nil, newError(CodeVaultUnavailable, false, errors.New("browser credential generation is unavailable"))
	}
	if !status.Configured || status.CredentialGeneration != credentialGeneration {
		return nil, newError(CodeCredentialStale, false, errors.New("browser credential generation changed"))
	}
	lease, err := s.client.Acquire(ctx, AcquireRequest{
		ProfileID: s.profileID, TaskID: taskID, CredentialGeneration: credentialGeneration,
		WaitTimeoutMS: waitTimeout.Milliseconds(), SessionTTLMS: sessionTTL.Milliseconds(),
	}, token)
	if err != nil {
		return nil, err
	}
	runtime := &RuntimeSession{service: s, client: s.client, lease: lease, done: make(chan struct{})}
	s.mu.Lock()
	s.active = runtime
	s.state.ControllerGeneration = lease.ControllerGeneration
	s.state.SessionGeneration = lease.SessionGeneration
	s.state.PageGeneration = lease.PageGeneration
	s.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = runtime.Release(releaseCtx)
		case <-runtime.done:
		}
	}()
	return runtime, nil
}

func (s *RuntimeSession) Lease() SessionLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lease
}

func (s *RuntimeSession) Execute(ctx context.Context, operation string, arguments map[string]any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, newError(CodeSessionStale, false, errors.New("browser session is released"))
	}
	operation = strings.TrimSpace(operation)
	if operation == "" || arguments == nil {
		return nil, newError(CodeInvalidRequest, false, errors.New("browser operation input is invalid"))
	}
	result, err := s.client.Execute(ctx, ExecuteRequest{Lease: s.lease, Operation: operation, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	var output map[string]any
	decoder := json.NewDecoder(bytes.NewReader(result.Result))
	decoder.UseNumber()
	if err := decoder.Decode(&output); err != nil || output == nil {
		return nil, invalidControllerResponse()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidControllerResponse()
	}
	s.lease.PageGeneration = result.PageGeneration
	if s.service != nil {
		s.service.mu.Lock()
		if s.service.active == s {
			s.service.state.PageGeneration = result.PageGeneration
		}
		s.service.mu.Unlock()
	}
	return output, nil
}

func (s *RuntimeSession) Release(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil
	}
	s.released = true
	_, err := s.client.Release(ctx, ReleaseRequest{
		ProfileID:            s.lease.ProfileID,
		SessionID:            s.lease.SessionID,
		ControllerGeneration: s.lease.ControllerGeneration,
		SessionGeneration:    s.lease.SessionGeneration,
	})
	if s.service != nil {
		s.service.mu.Lock()
		if s.service.active == s {
			s.service.active = nil
			s.service.state.SessionGeneration = 0
			s.service.state.PageGeneration = 0
		}
		s.service.mu.Unlock()
	}
	close(s.done)
	return err
}

func (s *Service) Close() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.releaseActiveLocked(ctx)
}
