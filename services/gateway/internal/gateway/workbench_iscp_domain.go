package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

type iscpDomainAdapter struct {
	server   *Server
	config   iscpworkbench.Config
	receipts *iscpDomainReceipts
}
type iscpDomainResult struct {
	status int
	body   json.RawMessage
}

// NewWorkbenchISCPDomainHandler is only mounted behind the Endpoint's pinned
// authorization and the Gateway's current per-operation scope admission. It
// never accepts a route, HTTP method or caller-selected principal.
func (s *Server) NewWorkbenchISCPDomainHandler(cfg iscpworkbench.Config) (iscpworkbench.Handler, error) {
	journal, err := newISCPDomainReceipts(s.executionRoot)
	if err != nil {
		return nil, err
	}
	adapter := &iscpDomainAdapter{server: s, config: cfg, receipts: journal}
	return adapter.handle, nil
}
func (a *iscpDomainAdapter) handle(ctx context.Context, request iscpworkbench.Request) iscpworkbench.Response {
	respond := func(result iscpDomainResult) iscpworkbench.Response {
		return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: request.Profile, ID: request.ID, Status: result.status, Body: result.body}
	}
	if ctx.Err() != nil {
		return respond(domainError(401, "authorization_closed"))
	}
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	if !principal.Authenticated || principal.ClientID == "" {
		return respond(domainError(401, "authorization_required"))
	}
	if !domainMutation(request.Operation) {
		return respond(a.dispatch(ctx, request))
	}
	if !execution.UUID(request.OperationID) {
		return respond(domainError(400, "operation_id_required"))
	}
	scope := a.receiptScope(principal, request.InstallationID)
	digestRaw, _ := json.Marshal(struct {
		Operation string
		Params    map[string]string
		Revision  string
		Body      json.RawMessage
	}{request.Operation, request.Params, request.ExpectedRevision, request.Body})
	digest := execution.Digest(digestRaw)
	// The owner lock serializes related resources across multiple Client peers;
	// repository-level preconditions still arbitrate non-ISCP concurrent writes.
	unlock := a.server.lockApproval("iscp-domain:" + principal.OwnerID)
	defer unlock()
	a.receipts.mu.Lock()
	defer a.receipts.mu.Unlock()
	receipt, exists, err := a.receipts.load(scope, request.OperationID)
	if err != nil {
		return respond(domainError(503, "operation_receipt_unavailable"))
	}
	if exists {
		if receipt.Digest != digest {
			return respond(domainError(409, "operation_input_conflict"))
		}
		if !receipt.Complete {
			return respond(domainError(409, "operation_outcome_unknown"))
		}
		return respond(iscpDomainResult{receipt.Status, receipt.Body})
	}
	entries, scanErr := os.ReadDir(a.receipts.root)
	if scanErr != nil || len(entries) >= 65536 {
		return respond(domainError(507, "operation_receipt_capacity"))
	}
	receipt = iscpDomainReceipt{Version: 1, Digest: digest, Operation: request.Operation}
	if err = a.receipts.save(scope, request.OperationID, receipt); err != nil {
		return respond(domainError(503, "operation_receipt_unavailable"))
	}
	result := a.dispatch(ctx, request)
	// Never erase an intent on cancellation or uncertain persistence. The client
	// reconciles business state and cannot turn a lost response into a second write.
	if ctx.Err() != nil {
		return respond(domainError(409, "operation_outcome_unknown"))
	}
	receipt.Complete, receipt.Status, receipt.Body = true, result.status, result.body
	if err = a.receipts.save(scope, request.OperationID, receipt); err != nil {
		return respond(domainError(409, "operation_outcome_unknown"))
	}
	return respond(result)
}
func domainMutation(operation string) bool {
	spec, found := iscpworkbench.LookupOperation(operation)
	return found && spec.Recovery == "operation_receipt"
}

func (a *iscpDomainAdapter) dispatch(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	switch request.Operation {
	case iscpworkbench.OperationCapabilitiesGet:
		return a.capabilities(ctx, request)
	case iscpworkbench.OperationOperationsReceipt:
		if !domainEmpty(request.Body) || !execution.UUID(request.Params["operation_id"]) {
			return domainError(400, "invalid_input")
		}
		principal := principalForRequest((&http.Request{}).WithContext(ctx))
		a.receipts.mu.Lock()
		defer a.receipts.mu.Unlock()
		receipt, found, err := a.receipts.load(a.receiptScope(principal, request.InstallationID), request.Params["operation_id"])
		if err != nil {
			return domainError(503, "operation_receipt_unavailable")
		}
		if !found {
			return domainError(404, "operation_not_found")
		}
		spec, known := iscpworkbench.LookupOperation(receipt.Operation)
		session, authenticated := iscpworkbench.SessionFromContext(ctx)
		if !known || !authenticated || !slices.Contains(session.Scopes, spec.Scope) {
			return domainError(403, "permission_denied")
		}
		if !receipt.Complete {
			return domainJSON(200, map[string]any{"state": "unknown", "operation_id": request.Params["operation_id"], "input_digest": receipt.Digest})
		}
		return domainJSON(200, map[string]any{"state": "completed", "operation_id": request.Params["operation_id"], "input_digest": receipt.Digest, "response": map[string]any{"status": receipt.Status, "body": receipt.Body}})
	case iscpworkbench.OperationSettingsOwnerGet, iscpworkbench.OperationSettingsOwnerPatch:
		return a.owner(ctx, request)
	case iscpworkbench.OperationSettingsConnectorsList, iscpworkbench.OperationSettingsConnectorsPatch:
		return a.connectors(ctx, request)
	case iscpworkbench.OperationSettingsIntegrationsList, iscpworkbench.OperationSettingsCredentialsAdd, iscpworkbench.OperationSettingsCredentialsActivate, iscpworkbench.OperationSettingsCredentialsCheck, iscpworkbench.OperationSettingsCredentialsDelete:
		return a.integrations(ctx, request)
	case iscpworkbench.OperationApprovalsList, iscpworkbench.OperationApprovalsGet, iscpworkbench.OperationApprovalsDecide, iscpworkbench.OperationExecutionApproval:
		return a.approvals(ctx, request)
	case iscpworkbench.OperationNotificationsList, iscpworkbench.OperationNotificationsRead, iscpworkbench.OperationNotificationsReadAll:
		return a.notifications(ctx, request)
	default:
		return domainError(501, "capability_unavailable")
	}
}
func domainJSON(status int, value any) iscpDomainResult {
	raw, err := json.Marshal(value)
	if err != nil {
		return domainError(500, "response_encoding_failed")
	}
	return iscpDomainResult{status, raw}
}
func domainError(status int, code string) iscpDomainResult {
	raw, _ := json.Marshal(map[string]any{"code": code, "retryable": false})
	return iscpDomainResult{status, raw}
}
func domainRevision(value any) string { raw, _ := json.Marshal(value); return execution.Digest(raw) }
func domainValue(value any) iscpDomainResult {
	return domainJSON(200, map[string]any{"revision": domainRevision(value), "value": value})
}
func domainDecode(raw []byte, value any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}
func domainEmpty(raw []byte) bool { var empty struct{}; return domainDecode(raw, &empty) == nil }

// domainHTTP adapts a specific preselected handler; its path and method are not
// input fields. The adapter must validate each typed body before calling it.
func domainHTTP(ctx context.Context, request iscpworkbench.Request, method string, handler http.HandlerFunc, params map[string]string) iscpDomainResult {
	r := (&http.Request{Method: method, URL: &url.URL{Path: "/iscp-domain"}, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(request.Body)), ContentLength: int64(len(request.Body))}).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-SparkClaw-Installation", request.InstallationID)
	r.Header.Set("X-SparkClaw-Digest", request.InputDigest)
	for key, value := range params {
		r.SetPathValue(key, value)
	}
	writer := &workbenchISCPWriter{header: make(http.Header)}
	handler(writer, r)
	if writer.oversized {
		return domainError(413, "response_too_large")
	}
	if writer.status == 0 {
		writer.status = 200
	}
	return iscpDomainResult{writer.status, append(json.RawMessage(nil), writer.body.Bytes()...)}
}
func domainNow() time.Time { return time.Now().UTC() }

func (a *iscpDomainAdapter) receiptScope(principal requestPrincipal, installation string) string {
	return a.config.Binding.DeploymentID + "\x00" + principal.OwnerID + "\x00" + principal.ClientID + "\x00" + installation
}
