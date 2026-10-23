package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// The embedded browser has no ISCP host lifecycle. The desktop only reads its
// write fences and reconciles them; commands arrive as reverse browser.command
// calls on the session that already authenticates the device.
func (a *iscpDomainAdapter) browser(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	principal, _, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	broker, err := a.server.browserHostBroker()
	if err != nil {
		return domainError(503, "browser_unavailable")
	}
	identity := browserhost.Identity{OwnerID: principal.OwnerID, ClientID: principal.ClientID, InstallationID: request.InstallationID}
	switch request.Operation {
	case iscpworkbench.OperationBrowserReceipt:
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainJSON(200, map[string]any{"fences": broker.Fences(identity)})
	case iscpworkbench.OperationBrowserReconcile:
		var input struct {
			CommandID string `json:"command_id"`
			Digest    string `json:"digest"`
			Outcome   string `json:"outcome"`
		}
		if domainDecode(request.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		if err := broker.Reconcile(identity, input.CommandID, input.Digest, input.Outcome); err != nil {
			return domainBrowserError(err)
		}
		return domainJSON(200, map[string]bool{"reconciled": true})
	}
	return domainError(501, "capability_unavailable")
}
func domainBrowserError(err error) iscpDomainResult {
	if errors.Is(err, browserhost.ErrUnknown) {
		return domainError(409, "browser_outcome_unknown")
	}
	if errors.Is(err, browserhost.ErrFence) {
		return domainError(409, "browser_fenced")
	}
	return domainError(503, "browser_unavailable")
}

// workbenchPeerRoute finds the negotiated session for one installation from
// a running responder transport, using local state only.
type workbenchPeerRoute func(ownerID, clientID, installationID string) (iscpworkbench.PeerSession, error)

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

func nodeBrowserRoute(responder *iscpworkbench.NodeResponder) workbenchPeerRoute {
	return responder.PeerSession
}

// endpointBrowserRoute serves the single-peer transport, whose binding names
// the only owner and Client it carries.
func endpointBrowserRoute(endpoint *iscpworkbench.Endpoint, expectedInstallationID string) workbenchPeerRoute {
	return func(ownerID, clientID, installationID string) (iscpworkbench.PeerSession, error) {
		binding, ok := endpoint.Binding()
		if !ok || binding.OwnerID != ownerID || binding.ClientID != clientID || expectedInstallationID != "" && expectedInstallationID != installationID {
			return iscpworkbench.PeerSession{}, iscpworkbench.ErrPeerUnavailable
		}
		session, ok := endpoint.PeerSession()
		if !ok {
			return iscpworkbench.PeerSession{}, iscpworkbench.ErrPeerUnavailable
		}
		return session, nil
	}
}

type browserPeerSession struct{ session iscpworkbench.PeerSession }

func (p browserPeerSession) SessionID() string             { return p.session.ID }
func (p browserPeerSession) AuthorizationRevision() uint64 { return p.session.AuthorizationRevision }
func (p browserPeerSession) Done() <-chan struct{}         { return p.session.Done }

// Command maps the reverse call onto the broker's outcome rules: only a local
// refusal before sending, or the desktop's own refusal before executing,
// proves the page was untouched. Every other failure is unknown.
func (p browserPeerSession) Command(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	return reverseCommandOutcome(p.session.Command(ctx, iscpworkbench.OperationBrowserCommand, body))
}
func reverseCommandOutcome(response iscpworkbench.Response, err error) (json.RawMessage, error) {
	if errors.Is(err, iscpworkbench.ErrPeerUnavailable) {
		return nil, browserhost.ErrNotDispatched
	}
	if err != nil {
		return nil, err
	}
	if response.Status == 200 {
		return response.Body, nil
	}
	if (response.Status == 403 && response.Code == iscpworkbench.ErrorPermissionDenied) || (response.Status == 503 && response.Code == iscpworkbench.ErrorCapabilityUnavailable) {
		return nil, browserhost.ErrNotDispatched
	}
	return nil, errors.New("browser command outcome is unknown")
}

var _ browserhost.ReverseRouter = (*workbenchBrowserRoutes)(nil)
