package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type emailIntakeOperationContextKey struct{}

func (a *iscpDomainAdapter) intakeOperation(ctx context.Context, r iscpworkbench.Request, enabled bool, version int64) store.EmailIntakeOperation {
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	scope := a.receiptScope(principal, r.InstallationID)
	return store.EmailIntakeOperation{Key: execution.Digest([]byte(scope + "\x00" + r.OperationID)), Digest: domainRequestDigest(r), Provider: r.Params["provider"], Enabled: enabled, ExpectedVersion: version}
}

// Reconciliation has no separate intent journal. It only resolves the original
// scoped operation, and repeating it after cancellation/lost response is safe.
func (a *iscpDomainAdapter) reconcileMailIntake(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if _, _, err := a.executionIdentity(ctx, request); err != nil {
		return domainError(403, "installation_required")
	}
	if a.server.emailManagement == nil {
		return domainError(503, "mail_unavailable")
	}
	var input struct {
		OperationID string `json:"operation_id"`
		Original    struct {
			Operation        string            `json:"operation"`
			Params           map[string]string `json:"params"`
			ExpectedRevision string            `json:"expected_revision,omitempty"`
			RequestID        string            `json:"request_id,omitempty"`
			InputDigest      string            `json:"input_digest,omitempty"`
			Body             json.RawMessage   `json:"body"`
		} `json:"original"`
	}
	if domainDecode(request.Body, &input) != nil || !execution.UUID(input.OperationID) || input.Original.Operation != iscpworkbench.OperationMailIntakeUpdate || len(input.Original.Params) != 1 || input.Original.Params["provider"] != request.Params["provider"] || !app.KnownEmailProvider(request.Params["provider"]) {
		return domainError(400, "invalid_input")
	}
	var body struct {
		Enabled *bool  `json:"intake_enabled"`
		Version *int64 `json:"expected_mailbox_version"`
	}
	if domainDecode(input.Original.Body, &body) != nil || body.Enabled == nil || body.Version == nil || *body.Version < 0 {
		return domainError(400, "invalid_input")
	}
	original := iscpworkbench.Request{Operation: input.Original.Operation, OperationID: input.OperationID, Params: input.Original.Params, ExpectedRevision: input.Original.ExpectedRevision, RequestID: input.Original.RequestID, InputDigest: input.Original.InputDigest, Body: input.Original.Body, InstallationID: request.InstallationID}
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	scope := a.receiptScope(principal, request.InstallationID)
	a.receipts.mu.Lock()
	receipt, found, err := a.receipts.load(scope, input.OperationID)
	a.receipts.mu.Unlock()
	if err != nil {
		return domainError(503, "operation_receipt_unavailable")
	}
	if !found {
		return domainError(404, "operation_not_found")
	}
	if receipt.Operation != iscpworkbench.OperationMailIntakeUpdate || receipt.Digest != domainRequestDigest(original) {
		return domainError(409, "operation_input_conflict")
	}
	if receipt.Complete {
		return intakeReceiptResult(input.OperationID, receipt)
	}
	resolved, err := a.server.emailManagement.ResolveIntake(ctx, principal.OwnerID, a.intakeOperation(ctx, original, *body.Enabled, *body.Version))
	if err != nil || ctx.Err() != nil {
		return domainError(409, "operation_outcome_unknown")
	}
	var result iscpDomainResult
	switch resolved.State {
	case store.EmailIntakeCommitted:
		result = domainJSON(200, map[string]any{"mailbox": emailmanagement.ProjectMailbox(resolved.Mailbox)})
	case store.EmailIntakeNotApplied:
		result = domainJSON(409, map[string]any{"code": "email_intake_not_applied", "error": "The previous receiving change was not applied. Review the current setting and try again.", "retryable": false, "provider": resolved.Operation.Provider, "provider_version": resolved.Version})
	default:
		return domainError(409, "operation_outcome_unknown")
	}
	a.receipts.mu.Lock()
	defer a.receipts.mu.Unlock()
	current, found, err := a.receipts.load(scope, input.OperationID)
	if err != nil || !found || current.Digest != receipt.Digest {
		return domainError(409, "operation_outcome_unknown")
	}
	if current.Complete {
		return intakeReceiptResult(input.OperationID, current)
	}
	receipt.Complete, receipt.Status, receipt.Body = true, result.status, result.body
	// The durable business proof outlives ordinary response retention. Return
	// it without recreating a body already removed by the receipt sweeper.
	if _, retired, loadErr := a.receipts.loadRetired(filepath.Base(a.receipts.path(scope, input.OperationID))); loadErr != nil {
		return domainError(409, "operation_outcome_unknown")
	} else if retired {
		return intakeReceiptResult(input.OperationID, receipt)
	}
	if err = a.receipts.save(scope, input.OperationID, receipt); err != nil {
		return domainError(409, "operation_outcome_unknown")
	}
	return intakeReceiptResult(input.OperationID, receipt)
}
func intakeReceiptResult(id string, receipt iscpDomainReceipt) iscpDomainResult {
	return domainJSON(200, map[string]any{"state": "completed", "operation_id": id, "input_digest": receipt.Digest, "response": map[string]any{"status": receipt.Status, "body": receipt.Body}})
}
