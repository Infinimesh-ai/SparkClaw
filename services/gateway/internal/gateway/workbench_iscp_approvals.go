package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func (a *iscpDomainAdapter) executionIdentity(ctx context.Context, request iscpworkbench.Request) (requestPrincipal, *execution.Service, error) {
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	service, err := a.server.executionService()
	if err != nil {
		return principal, nil, err
	}
	if err = service.Installation(principal.OwnerID, principal.ClientID, request.InstallationID); err != nil {
		return principal, nil, err
	}
	return principal, service, nil
}
func (a *iscpDomainAdapter) approvals(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	principal, service, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	requestID := request.Params["request_id"]
	if requestID == "" {
		requestID = request.RequestID
	}
	if !execution.UUID(requestID) {
		return domainError(400, "request_id_required")
	}
	status, err := service.Lookup(principal.OwnerID, principal.ClientID, requestID)
	if err != nil {
		return domainExecutionError(err)
	}
	revision := strconv.FormatUint(status.Revision, 10)
	if request.Operation == iscpworkbench.OperationApprovalsList {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainJSON(200, map[string]any{"revision": revision, "request_id": requestID, "input_digest": status.InputDigest, "state": status.State, "termination_reason": status.TerminationReason, "approvals": status.PendingApprovals, "approval_receipts": status.ApprovalReceipts})
	}
	id := request.Params["approval_id"]
	if id == "" {
		id = request.Params["id"]
	}
	if request.Operation == iscpworkbench.OperationApprovalsGet {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		for _, approval := range status.PendingApprovals {
			if approval.ApprovalID == id {
				return domainJSON(200, map[string]any{"revision": revision, "request_id": requestID, "input_digest": status.InputDigest, "approval": approval})
			}
		}
		for _, receipt := range status.ApprovalReceipts {
			if receipt.ApprovalID == id {
				return domainJSON(200, map[string]any{"revision": revision, "request_id": requestID, "state": status.State, "termination_reason": status.TerminationReason, "receipt": receipt})
			}
		}
		return domainError(404, "approval_not_found")
	}
	var input struct {
		Digest      string `json:"digest"`
		Decision    string `json:"decision"`
		InputDigest string `json:"input_digest,omitempty"`
	}
	if domainDecode(request.Body, &input) != nil || input.Digest == "" || (input.Decision != "approve" && input.Decision != "reject") {
		return domainError(400, "invalid_input")
	}
	boundDigest := input.InputDigest
	if boundDigest == "" {
		boundDigest = request.InputDigest
	}
	if boundDigest != status.InputDigest || request.ExpectedRevision != revision {
		return domainError(409, "revision_conflict")
	}
	if err = service.DecideApprovalAtRevision(principal.OwnerID, principal.ClientID, requestID, id, input.Digest, input.Decision, status.Revision); err != nil {
		return domainExecutionError(err)
	}
	updated, err := service.Lookup(principal.OwnerID, principal.ClientID, requestID)
	if err != nil {
		return domainExecutionError(err)
	}
	return domainJSON(200, map[string]any{"resolved": true, "request_id": requestID, "revision": strconv.FormatUint(updated.Revision, 10), "approval_receipts": updated.ApprovalReceipts, "state": updated.State})
}
func domainExecutionError(err error) iscpDomainResult {
	switch {
	case errors.Is(err, execution.ErrNotFound):
		return domainError(404, "execution_not_found")
	case errors.Is(err, execution.ErrConflict):
		return domainError(409, "revision_conflict")
	case errors.Is(err, execution.ErrExpired):
		return domainError(410, "execution_expired")
	case errors.Is(err, execution.ErrCapacity):
		return domainError(429, "execution_capacity")
	default:
		return domainError(503, "execution_unavailable")
	}
}
