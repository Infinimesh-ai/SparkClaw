package gateway

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// The browser and McDonald's forward methods over the link layer (device
// condition browser_surface). The desktop reads its uncertain browser write
// fences and reconciles them, and provisions, revokes and reads its
// McDonald's sessions. Their reverse operations are link reverse calls
// (link_reverse.go).

// WithMCDSelection installs the McDonald's selection manager.
func WithMCDSelection(manager *mcdintegration.Manager) Option {
	return func(s *Server) { s.mcdSelection = manager }
}

func linkBrowserIdentity(p link.Principal) browserhost.Identity {
	return browserhost.Identity{OwnerID: p.OwnerID, ClientID: p.ClientID, InstallationID: linkInstallationID(p.DeviceID)}
}

func linkBrowserError(err error) error {
	switch {
	case errors.Is(err, browserhost.ErrUnknown):
		return linkDomainError(contract.CodeConflict, "browser_outcome_unknown", "the browser write's outcome is unknown", false)
	case errors.Is(err, browserhost.ErrFence):
		return linkDomainError(contract.CodeConflict, "browser_fenced", "the browser write is fenced", false)
	}
	return linkDomainError(contract.CodeBackendUnavailable, "browser_unavailable", "the browser is unavailable", true)
}

func linkMCDError(err error) error {
	switch {
	case errors.Is(err, mcdintegration.ErrInvalid):
		return linkDomainError(contract.CodeInvalidRequest, "mcd_session_invalid", "mcd_session_unavailable", false)
	case errors.Is(err, mcdintegration.ErrScope):
		return linkDomainError(contract.CodeUnauthorized, "mcd_session_scope", "mcd_session_unavailable", false)
	case errors.Is(err, mcdintegration.ErrClosed), errors.Is(err, mcdintegration.ErrConflict), errors.Is(err, mcdintegration.ErrExpired):
		return linkDomainError(contract.CodeConflict, "mcd_session_conflict", "mcd_session_unavailable", false)
	}
	return linkDomainError(contract.CodeBackendUnavailable, "mcd_session_unavailable", "mcd_session_unavailable", true)
}

type browserReconcileArgs struct {
	CommandID string `json:"command_id"`
	Digest    string `json:"digest"`
	Outcome   string `json:"outcome"`
}

type mcdRevokeArgs struct {
	Identity     mcdintegration.Identity `json:"identity"`
	ConnectionID string                  `json:"connection_id"`
	RequestID    string                  `json:"request_id"`
}

func init() {
	surface := func(call func(context.Context, *Server, *link.Request, json.RawMessage) (any, error)) func(context.Context, *Server, *link.Request, json.RawMessage) (any, error) {
		return func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			if !req.Principal.Conditions.BrowserSurface {
				return nil, link.Errorf(contract.CodeUnsupportedCapability, "this device has no built-in browser")
			}
			return call(ctx, s, req, raw)
		}
	}
	mcd := func(call func(context.Context, *Server, mcdintegration.Principal, json.RawMessage) (any, error)) func(context.Context, *Server, *link.Request, json.RawMessage) (any, error) {
		return surface(func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			if s.mcdSelection == nil || !s.cfg.MCDSelection.Enabled {
				return nil, linkDomainError(contract.CodeBackendUnavailable, "mcd_selection_unavailable", "McDonald's ordering is not enabled", false)
			}
			p := mcdintegration.Principal{DeploymentID: s.cfg.Gateway.DeploymentID, OwnerID: req.Principal.OwnerID, ClientID: req.Principal.ClientID, InstallationID: linkInstallationID(req.Principal.DeviceID)}
			return call(ctx, s, p, raw)
		})
	}
	registerLinkDomain("browser", map[string]linkDomainMethod{
		"browser.receipt": {Call: surface(func(_ context.Context, s *Server, req *link.Request, _ json.RawMessage) (any, error) {
			fences, err := s.browserFences(linkBrowserIdentity(req.Principal))
			if err != nil {
				return nil, linkBrowserError(err)
			}
			return map[string]any{"fences": fences}, nil
		})},
		"browser.reconcile": {Mutation: true, Call: surface(func(_ context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			args, err := linkArgs[browserReconcileArgs](raw)
			if err != nil {
				return nil, err
			}
			broker, err := s.browserHostBroker()
			if err != nil {
				return nil, linkBrowserError(err)
			}
			if err := broker.Reconcile(linkBrowserIdentity(req.Principal), args.CommandID, args.Digest, args.Outcome); err != nil {
				return nil, linkBrowserError(err)
			}
			return map[string]bool{"reconciled": true}, nil
		})},
	})
	// McDonald's sessions recover from their original request: provisioning
	// and revocation carry their own request ids.
	registerLinkDomain("mcd", map[string]linkDomainMethod{
		"mcd.session.provision": {Call: mcd(func(ctx context.Context, s *Server, p mcdintegration.Principal, raw json.RawMessage) (any, error) {
			out, err := s.mcdSelection.Provision(ctx, p, raw)
			if err != nil {
				return nil, linkMCDError(err)
			}
			return out, nil
		})},
		"mcd.session.revoke": {Call: mcd(func(ctx context.Context, s *Server, p mcdintegration.Principal, raw json.RawMessage) (any, error) {
			args, err := linkArgs[mcdRevokeArgs](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.mcdSelection.Revoke(ctx, p, args.Identity, args.ConnectionID, args.RequestID)
			if err != nil {
				return nil, linkMCDError(err)
			}
			return out, nil
		})},
		"mcd.session.status": {Call: mcd(func(ctx context.Context, s *Server, p mcdintegration.Principal, _ json.RawMessage) (any, error) {
			statuses, err := s.mcdSelection.Status(ctx, p)
			if err != nil {
				return nil, linkMCDError(err)
			}
			return map[string]any{"connections": statuses, "checkout_enabled": false}, nil
		})},
	})
}
