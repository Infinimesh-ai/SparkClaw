package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/latencytrace"
)

// ErrPeerUnavailable means no authenticated, negotiated session currently
// reaches the installation. Nothing was sent.
var ErrPeerUnavailable = errors.New("workbench peer session is unavailable")

type expectedSessionKey struct{}

// PeerSession is one negotiated v2 session to a desktop installation. Its
// commands travel only over that session: a replacement session never
// receives a command addressed to the old one, and Done closes when the
// session ends for any reason (reconnect, Gateway restart, revocation).
type PeerSession struct {
	ID                    string
	InstallationID        string
	AuthorizationRevision uint64
	Done                  <-chan struct{}
	endpoint              *Endpoint
	// Caller carries the round trip when the session is not an ISCP
	// endpoint (a link-layer connection to the device).
	Caller func(ctx context.Context, operation string, body json.RawMessage) (Response, error)
}

// Command performs one reverse round trip on this exact session. A local
// refusal before sending is ErrPeerUnavailable; any later failure leaves the
// outcome unknown to the caller.
func (s PeerSession) Command(ctx context.Context, operation string, body json.RawMessage) (Response, error) {
	request := Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: operation, InstallationID: s.InstallationID, Body: body}
	trace := func() {
		traceCtx := latencytrace.WithFields(ctx, requestTraceFields(RoleResponder, request))
		latencytrace.RecordDetail(traceCtx, "rpc.reverse_dispatch", time.Time{}, ErrPeerUnavailable, latencytrace.Detail{Reason: "peer_session_unavailable"})
	}
	if s.Caller != nil {
		select {
		case <-s.Done:
			trace()
			return Response{}, ErrPeerUnavailable
		default:
		}
		return s.Caller(ctx, operation, body)
	}
	if s.endpoint == nil {
		trace()
		return Response{}, ErrPeerUnavailable
	}
	select {
	case <-s.Done:
		trace()
		return Response{}, ErrPeerUnavailable
	default:
	}
	return s.endpoint.Call(context.WithValue(ctx, expectedSessionKey{}, s.ID), request)
}

// PeerSession reports the current negotiated v2 session, if any.
func (e *Endpoint) PeerSession() (PeerSession, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.session
	if e.closed || s == nil || !s.manifest || s.ctx.Err() != nil || s.capabilities == nil || s.capabilities.Profile != ProfileV2 || !time.Now().Before(s.capabilities.ExpiresAt) {
		return PeerSession{}, false
	}
	return PeerSession{ID: s.id, InstallationID: e.config.ExpectedInstallationID, AuthorizationRevision: s.capabilities.AuthorizationRevision, Done: s.ctx.Done(), endpoint: e}, true
}

// Binding is the business identity this endpoint serves.
func (e *Endpoint) Binding() (Binding, bool) {
	if e.config.Binding == nil {
		return Binding{}, false
	}
	return *e.config.Binding, true
}

// PeerSession routes by installation to the one peer enrolled for it. Peers
// are added and removed by the registry; a removed peer's session is done.
func (n *NodeResponder) PeerSession(ownerID, clientID, installationID string) (PeerSession, error) {
	n.mu.Lock()
	var endpoint *Endpoint
	for _, peer := range n.peers {
		if peer.peer.InstallationID == installationID && peer.peer.Binding.OwnerID == ownerID && peer.peer.Binding.ClientID == clientID {
			endpoint = peer.endpoint
			break
		}
	}
	closed := n.closed
	n.mu.Unlock()
	if closed || endpoint == nil {
		return PeerSession{}, ErrPeerUnavailable
	}
	session, ok := endpoint.PeerSession()
	if !ok {
		return PeerSession{}, ErrPeerUnavailable
	}
	return session, nil
}
