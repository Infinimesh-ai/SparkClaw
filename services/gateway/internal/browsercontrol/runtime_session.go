package browsercontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	defaultRuntimeSessionTTL = 2 * time.Minute
	maxRuntimeSessionTTL     = 10 * time.Minute
	maxRuntimeWaitTimeout    = 30 * time.Second
	deferredReleaseTimeout   = 5 * time.Second
)

type Session interface {
	Lease() SessionLease
	Execute(context.Context, string, map[string]any) (map[string]any, error)
	Release(context.Context) error
}

// RuntimeSession serializes controller operations through ops, a one-slot
// channel, instead of holding a mutex across the controller round trip. That
// lets Release honor its context while an Execute is still in flight: the
// caller gets a fast, typed failure and the controller release runs as soon
// as the in-flight operation returns.
type RuntimeSession struct {
	service *Service
	client  ControllerClient

	ops chan struct{}

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
	release, err := s.acquireOperations(ctx, true)
	if err != nil {
		return nil, err
	}
	defer release()

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
	runtime := &RuntimeSession{
		service: s, client: s.client, lease: lease,
		ops: make(chan struct{}, 1), done: make(chan struct{}),
	}
	s.mu.Lock()
	s.active = runtime
	s.state.ControllerGeneration = lease.ControllerGeneration
	s.state.SessionGeneration = lease.SessionGeneration
	s.state.PageGeneration = lease.PageGeneration
	s.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			releaseCtx, cancel := context.WithTimeout(context.Background(), deferredReleaseTimeout)
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
	if s.isReleased() {
		return nil, releasedError()
	}
	if err := s.acquireOp(ctx); err != nil {
		return nil, err
	}
	defer s.releaseOp()
	if s.isReleased() {
		return nil, releasedError()
	}
	s.mu.Lock()
	lease := s.lease
	s.mu.Unlock()
	operation = strings.TrimSpace(operation)
	if operation == "" || arguments == nil {
		return nil, newError(CodeInvalidRequest, false, errors.New("browser operation input is invalid"))
	}
	result, err := s.client.Execute(ctx, ExecuteRequest{Lease: lease, Operation: operation, Arguments: arguments})
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
	s.mu.Lock()
	s.lease.PageGeneration = result.PageGeneration
	s.mu.Unlock()
	if s.service != nil {
		s.service.mu.Lock()
		if s.service.active == s {
			s.service.state.PageGeneration = result.PageGeneration
		}
		s.service.mu.Unlock()
	}
	return output, nil
}

// Release marks the session released at once, so no further Execute is
// admitted, and then releases the controller session. If ctx expires while an
// operation is still in flight, Release returns a retryable busy error and the
// controller release runs in the background once that operation returns.
func (s *RuntimeSession) Release(ctx context.Context) error {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return nil
	}
	s.released = true
	s.mu.Unlock()
	if err := s.acquireOp(ctx); err != nil {
		go func() {
			s.ops <- struct{}{}
			defer s.releaseOp()
			releaseCtx, cancel := context.WithTimeout(context.Background(), deferredReleaseTimeout)
			defer cancel()
			s.releaseControllerSession(releaseCtx)
		}()
		return newError(CodeBusy, true, fmt.Errorf("browser session release deferred behind an in-flight operation: %w", err))
	}
	defer s.releaseOp()
	return s.releaseControllerSession(ctx)
}

func (s *RuntimeSession) releaseControllerSession(ctx context.Context) error {
	s.mu.Lock()
	lease := s.lease
	s.mu.Unlock()
	_, err := s.client.Release(ctx, ReleaseRequest{
		ProfileID:            lease.ProfileID,
		SessionID:            lease.SessionID,
		ControllerGeneration: lease.ControllerGeneration,
		SessionGeneration:    lease.SessionGeneration,
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

func (s *RuntimeSession) isReleased() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.released
}

func releasedError() error {
	return newError(CodeSessionStale, false, errors.New("browser session is released"))
}

func (s *RuntimeSession) acquireOp(ctx context.Context) error {
	select {
	case s.ops <- struct{}{}:
		return nil
	case <-ctx.Done():
		return newError(CodeBusy, true, fmt.Errorf("browser session operation slot is busy: %w", ctx.Err()))
	}
}

func (s *RuntimeSession) releaseOp() {
	<-s.ops
}

// Close releases the active runtime session within closeTimeout, including
// any wait for in-flight scripts, credential updates or runtime acquisition.
func (s *Service) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.closeTimeout)
	defer cancel()
	release, err := s.acquireOperations(ctx, true)
	if err != nil {
		return err
	}
	defer release()
	return s.releaseActiveLocked(ctx)
}
