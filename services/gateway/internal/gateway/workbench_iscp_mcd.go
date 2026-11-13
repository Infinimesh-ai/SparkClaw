package gateway

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

func WithMCDSelection(manager *mcdintegration.Manager) Option {
	return func(s *Server) { s.mcdSelection = manager }
}

// This dedicated channel bypasses generic journals and object spools. Only
// metadata receipts leave the Vault module; a raw envelope is never echoed.
func (a *iscpDomainAdapter) mcd(ctx context.Context, r iscpworkbench.Request) iscpDomainResult {
	session, ok := iscpworkbench.SessionFromContext(ctx)
	if !ok || session.Binding == nil || !slices.Contains(session.NegotiatedOperations, r.Operation) ||
		!slices.Contains(session.QualifiedOperations, r.Operation) || !slices.Contains(a.config.QualifiedCapabilities, r.Operation) {
		return domainError(403, "mcd_selection_not_qualified")
	}
	if a.server.mcdSelection == nil {
		return domainError(503, "mcd_selection_unavailable")
	}
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	p := mcdintegration.Principal{DeploymentID: a.server.cfg.Gateway.DeploymentID, OwnerID: principal.OwnerID, ClientID: principal.ClientID, InstallationID: r.InstallationID}
	var result any
	var err error
	switch r.Operation {
	case iscpworkbench.OperationMCDSessionProvision:
		result, err = a.server.mcdSelection.Provision(ctx, p, r.Body)
	case iscpworkbench.OperationMCDSessionRevoke:
		var input struct {
			Identity     mcdintegration.Identity `json:"identity"`
			ConnectionID string                  `json:"connection_id"`
			RequestID    string                  `json:"request_id"`
		}
		if domainDecode(r.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		result, err = a.server.mcdSelection.Revoke(ctx, p, input.Identity, input.ConnectionID, input.RequestID)
	case iscpworkbench.OperationMCDSessionStatus:
		if !domainEmpty(r.Body) {
			return domainError(400, "invalid_input")
		}
		var statuses []mcdintegration.Status
		statuses, err = a.server.mcdSelection.Status(ctx, p)
		result = map[string]any{"connections": statuses, "checkout_enabled": false}
	default:
		return domainError(400, "invalid_operation")
	}
	if err != nil {
		status := 503
		switch {
		case errors.Is(err, mcdintegration.ErrInvalid):
			status = 400
		case errors.Is(err, mcdintegration.ErrScope):
			status = 403
		case errors.Is(err, mcdintegration.ErrClosed), errors.Is(err, mcdintegration.ErrConflict), errors.Is(err, mcdintegration.ErrExpired):
			status = 409
		}
		return domainError(status, "mcd_session_unavailable")
	}
	return domainJSON(200, result)
}
