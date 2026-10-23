package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func waitPeerSession(t *testing.T, e *Endpoint) PeerSession {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if session, ok := e.PeerSession(); ok {
			return session
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("responder session was not negotiated")
	return PeerSession{}
}

func TestPeerSessionCommandsTravelOnlyOnTheirOwnSession(t *testing.T) {
	_, r, _ := v2Endpoints(t, true)
	session := waitPeerSession(t, r)
	if session.AuthorizationRevision == 0 || session.ID == "" {
		t.Fatalf("session %+v", session)
	}
	response, err := session.Command(context.Background(), OperationBrowserCommand, json.RawMessage(`{"probe":true}`))
	if err != nil || response.Status != 200 || string(response.Body) != `{"host":true}` {
		t.Fatalf("reverse command %+v %v", response, err)
	}
	r.mu.Lock()
	r.resetLocked()
	r.mu.Unlock()
	select {
	case <-session.Done:
	case <-time.After(time.Second):
		t.Fatal("session end was not signalled")
	}
	started := time.Now()
	if _, err := session.Command(context.Background(), OperationBrowserCommand, json.RawMessage(`{}`)); !errors.Is(err, ErrPeerUnavailable) || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("ended session accepted a command: %v", err)
	}
	// A replacement session never carries a command addressed to the old one.
	if _, err := r.Call(context.WithValue(context.Background(), expectedSessionKey{}, "replaced-session"), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationBrowserCommand}); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("mismatched session call %v", err)
	}
}

func TestPeerSessionInFlightCommandEndsWithItsSession(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	_, r, _ := profileEndpoints(t, true, true, func(_ context.Context, r Request) Response { return Response{Status: 200, Body: r.Body} }, func(ctx context.Context, _ Request) Response {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return Response{Status: 200, Body: json.RawMessage(`{}`)}
	})
	session := waitPeerSession(t, r)
	result := make(chan error, 1)
	go func() {
		_, err := session.Command(context.Background(), OperationBrowserCommand, json.RawMessage(`{}`))
		result <- err
	}()
	time.Sleep(50 * time.Millisecond)
	// Revocation removes the peer and closes its endpoint.
	_ = r.Close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("revoked session completed a command")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight command outlived its session")
	}
}

func TestNodeResponderHasNoPeerSessionForUnknownInstallation(t *testing.T) {
	n := &NodeResponder{peers: map[string]*nodePeer{}}
	started := time.Now()
	if _, err := n.PeerSession("owner", "client", "11111111-1111-4111-8111-111111111111"); !errors.Is(err, ErrPeerUnavailable) || time.Since(started) > 10*time.Millisecond {
		t.Fatalf("missing peer %v", err)
	}
}
