package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
)

// The routes the browser broker and the McDonald's host use to reach a
// device's built-in browser: each open link connection of a desktop that
// serves the reverse operations (link_reverse.go linkPeerRoute).

// workbenchPeerRoute finds the negotiated session for one installation from
// a running responder transport, using local state only.
type workbenchPeerRoute func(ownerID, clientID, installationID string) (linkPeer, error)

// workbenchBrowserRoutes lets the browser broker address every responder
// transport this Gateway runs, including both sides of a keyed migration.
type workbenchBrowserRoutes struct {
	mu     sync.Mutex
	next   int
	routes map[int]workbenchPeerRoute
}

func (r *workbenchBrowserRoutes) add(route workbenchPeerRoute) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.routes == nil {
		r.routes = map[int]workbenchPeerRoute{}
	}
	r.next++
	id := r.next
	r.routes[id] = route
	return func() { r.mu.Lock(); delete(r.routes, id); r.mu.Unlock() }
}

// peerFor returns the first route's session for an installation.
func (r *workbenchBrowserRoutes) peerFor(ownerID, clientID, installationID string) (linkPeer, error) {
	r.mu.Lock()
	routes := make([]workbenchPeerRoute, 0, len(r.routes))
	for _, route := range r.routes {
		routes = append(routes, route)
	}
	r.mu.Unlock()
	for _, route := range routes {
		if session, err := route(ownerID, clientID, installationID); err == nil {
			return session, nil
		}
	}
	return linkPeer{}, errLinkPeerUnavailable
}

func (r *workbenchBrowserRoutes) ReverseSession(identity browserhost.Identity) (browserhost.ReverseSession, error) {
	r.mu.Lock()
	routes := make([]workbenchPeerRoute, 0, len(r.routes))
	for _, route := range r.routes {
		routes = append(routes, route)
	}
	r.mu.Unlock()
	for _, route := range routes {
		if session, err := route(identity.OwnerID, identity.ClientID, identity.InstallationID); err == nil {
			if session.InstallationID == "" {
				session.InstallationID = identity.InstallationID
			}
			return browserPeerSession{session}, nil
		}
	}
	return nil, browserhost.ErrUnavailable
}

type browserPeerSession struct{ session linkPeer }

func (p browserPeerSession) SessionID() string             { return p.session.ID }
func (p browserPeerSession) AuthorizationRevision() uint64 { return p.session.AuthorizationRevision }
func (p browserPeerSession) Done() <-chan struct{}         { return p.session.Done }

func (p browserPeerSession) ControllerCall(ctx context.Context, operation string, body json.RawMessage) (json.RawMessage, error) {
	response, err := p.session.Command(ctx, operation, body)
	return controllerReverseOutcome(response, err)
}

func controllerReverseOutcome(response linkPeerResponse, err error) (json.RawMessage, error) {
	var cerr *contract.Error
	if errors.Is(err, errLinkPeerUnavailable) || errors.As(err, &cerr) && cerr.NotSent {
		return nil, browsercontrol.ErrControllerNotSent
	}
	if err != nil {
		return nil, err
	}
	if response.Status == 200 {
		return response.Body, nil
	}
	// A remote status alone cannot prove that a submitted mutation did not
	// execute. Only an authenticated Controller envelope carries that proof.
	return nil, errors.New("browser Controller response outcome is unavailable")
}

var _ browserhost.ReverseRouter = (*workbenchBrowserRoutes)(nil)
