package gateway

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

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
	case iscpworkbench.OperationBrowserHostGrant:
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		grant, err := broker.IssueGrant(identity)
		if err != nil {
			return domainBrowserError(err)
		}
		return domainJSON(200, grant)
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
	case iscpworkbench.OperationBrowserHostRegister:
		var input struct {
			HostID            string `json:"host_id"`
			GrantToken        string `json:"grant_token"`
			RuntimeGeneration string `json:"runtime_generation"`
		}
		if domainDecode(request.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		connected, release, err := a.server.clientConnectionContext(a.server.executionContext(), principal.ClientID)
		if err != nil {
			return domainError(401, "authorization_closed")
		}
		epoch, err := broker.OpenPolling(connected, identity, input.HostID, input.GrantToken, input.RuntimeGeneration, release)
		if err != nil {
			release()
			return domainBrowserError(err)
		}
		return domainJSON(200, map[string]any{"host_id": input.HostID, "connection_epoch": epoch})
	case iscpworkbench.OperationBrowserHostPoll:
		var input struct {
			HostID string `json:"host_id"`
			Epoch  string `json:"connection_epoch"`
			After  uint64 `json:"after"`
		}
		if domainDecode(request.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		messages, err := broker.Poll(identity, input.HostID, input.Epoch, input.After)
		if err != nil {
			return domainBrowserError(err)
		}
		return domainJSON(200, map[string]any{"messages": messages})
	case iscpworkbench.OperationBrowserHostReply, iscpworkbench.OperationBrowserHostHeartbeat:
		var input struct {
			HostID  string              `json:"host_id"`
			Epoch   string              `json:"connection_epoch"`
			Message browserhost.Message `json:"message"`
		}
		if domainDecode(request.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		if (request.Operation == iscpworkbench.OperationBrowserHostHeartbeat && input.Message.Type != "heartbeat") || (request.Operation == iscpworkbench.OperationBrowserHostReply && input.Message.Type != "result") {
			return domainError(400, "invalid_input")
		}
		if err := broker.ReceivePolling(identity, input.HostID, input.Epoch, input.Message); err != nil {
			return domainBrowserError(err)
		}
		return domainJSON(200, map[string]bool{"accepted": true})
	case iscpworkbench.OperationBrowserHostClose, iscpworkbench.OperationBrowserHostRevoke:
		var input struct {
			HostID string `json:"host_id"`
			Epoch  string `json:"connection_epoch,omitempty"`
		}
		if domainDecode(request.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		if request.Operation == iscpworkbench.OperationBrowserHostRevoke {
			err = broker.RevokeGrant(identity, input.HostID)
		} else {
			err = broker.ClosePolling(identity, input.HostID, input.Epoch)
		}
		if err != nil {
			return domainBrowserError(err)
		}
		return domainJSON(200, map[string]bool{"closed": true})
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
