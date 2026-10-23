package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

type cachedGrantLifecycle struct {
	grantLifecycle
	grant         trust.Grant
	checkError    error
	checks        atomic.Int32
	checkState    func() string
	observedState string
}

func (l *cachedGrantLifecycle) Current(context.Context) (trust.Grant, error) { return l.grant, nil }
func (l *cachedGrantLifecycle) HasPendingRenewal() bool                      { return false }
func (l *cachedGrantLifecycle) AcceptGrant(trust.Grant) error                { return nil }
func (l *cachedGrantLifecycle) UsesStandingAuthorization() bool              { return true }
func (l *cachedGrantLifecycle) CheckAuthorization(context.Context) error {
	l.checks.Add(1)
	l.observedState = l.checkState()
	return l.checkError
}

type rejectedReconnectRelay struct {
	attempts         atomic.Int32
	beforeDisconnect func()
}

func (*rejectedReconnectRelay) Submit(context.Context, any) error { return errors.New("offline") }
func (r *rejectedReconnectRelay) RunOnceReady(context.Context, func(context.Context, json.RawMessage) error, func()) error {
	r.attempts.Add(1)
	r.beforeDisconnect()
	return errors.New("Relay ready binding mismatch")
}

func TestNodeReconnectChecksSignedAuthorityBeforeReportingDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name       string
		checkError error
		revoked    bool
	}{
		{"verified revoked", fmt.Errorf("signed issuer response: %w", iscpbridge.ErrAuthorizationRevoked), true},
		{"active", nil, false},
		{"unsigned denied", errors.New("HTTP 403 authorization_revoked"), false},
		{"issuer unavailable", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := testMaterials(t)
			relay := &rejectedReconnectRelay{}
			states := make(chan string, 10)
			e, err := newEndpoint(Config{Role: RoleInitiator}, m, relay, nil, func(s string) { states <- s })
			if err != nil {
				t.Fatal(err)
			}
			// The cached grant is still valid, exactly as when a just-enrolled
			// desktop is revoked before its first short authorization expires.
			e.config.Mode = "node-managed"
			relay.beforeDisconnect = func() { e.setState("transport_ready") }
			lifecycle := &cachedGrantLifecycle{grant: m.grant, checkError: tc.checkError}
			lifecycle.checkState = func() string { e.mu.Lock(); defer e.mu.Unlock(); return e.stateName }
			e.lifecycle = lifecycle
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			done := make(chan error, 1)
			go func() { done <- e.Run(ctx) }()
			var terminal string
			for terminal == "" {
				select {
				case state := <-states:
					if state == "disconnected" || state == "authorization_revoked" {
						terminal = state
					}
				case <-ctx.Done():
					t.Fatal("connection failure did not settle")
				}
			}
			cancel()
			<-done
			if lifecycle.checks.Load() != 1 || relay.attempts.Load() != 1 {
				t.Fatalf("checks=%d reconnect attempts=%d, want one bounded check per failed connection", lifecycle.checks.Load(), relay.attempts.Load())
			}
			if lifecycle.observedState != "connecting" {
				t.Fatalf("desktop was not fenced before issuer check: %s", lifecycle.observedState)
			}
			if (terminal == "authorization_revoked") != tc.revoked {
				t.Fatalf("state=%s revoked=%v", terminal, tc.revoked)
			}
			if e.authorizationRevoked != tc.revoked {
				t.Fatalf("revoked=%v", e.authorizationRevoked)
			}
		})
	}
}
