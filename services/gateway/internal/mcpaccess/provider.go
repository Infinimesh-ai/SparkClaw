package mcpaccess

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Provider struct{ store store.Store }

func NewProvider(st store.Store) *Provider { return &Provider{store: st} }
func (*Provider) Key() string              { return "mcp" }
func (*Provider) Capabilities() app.DeliveryCapabilities {
	return app.DeliveryCapabilities{
		Kinds:        []app.MessagePartKind{app.MessagePartText, app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile},
		Dispositions: []app.MessagePartDisposition{app.MessageDispositionInline, app.MessageDispositionAttachment, app.MessageDispositionVoiceNote},
		MaxParts:     8, MaxTotalBytes: 4 << 20,
		MaxBytesByKind:  map[app.MessagePartKind]int64{app.MessagePartImage: 4 << 20, app.MessagePartAudio: 4 << 20, app.MessagePartFile: 4 << 20},
		SupportsCaption: true,
	}
}

func (p *Provider) Deliver(_ context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	if p == nil || p.store == nil || request.MCP == nil || request.MCP.OperationID == "" {
		return app.DeliveryReceipt{}, delivery.NewError(delivery.CodeBindingUnavailable, "MCP delivery context is unavailable", "blocked")
	}
	if endpoint.ProviderKey != "mcp" || endpoint.BindingRef != request.MCP.BindingRef || endpoint.RequesterDeviceID != request.MCP.RequesterDeviceID {
		return app.DeliveryReceipt{}, delivery.NewError(delivery.CodeCrossUserDenied, "MCP source endpoint does not match the invocation", "blocked")
	}
	operation, ok := p.store.GetMCPOperation(request.MCP.OperationID)
	if !ok || operation.BindingID != endpoint.BindingRef || operation.Invocation.ID != request.MCP.InvocationID {
		return app.DeliveryReceipt{}, delivery.NewError(delivery.CodeCrossUserDenied, "MCP operation does not match the invocation", "blocked")
	}
	if operationTerminal(operation.State) {
		return app.DeliveryReceipt{}, delivery.NewError(delivery.CodeOutcomeUnknown, "MCP operation is already terminal", "outcome_unknown")
	}
	payload, err := json.Marshal(map[string]any{"content": request.Content, "run_id": request.RunID, "result_id": request.ResultID})
	if err != nil {
		return app.DeliveryReceipt{}, err
	}
	operation.Result = payload
	resultStatus := request.ResultStatus
	if resultStatus == "" {
		run, hasRun := p.store.GetRun(operation.Invocation.RunID)
		if hasRun && (run.State == "approval_pending" || run.State == "browser_login_blocked") {
			resultStatus = app.WorkflowResultWaiting
		} else {
			resultStatus = app.WorkflowResultSucceeded
		}
	}
	applyWorkflowResultToOperation(&operation, resultStatus, request.ResultError)
	now := time.Now().UTC()
	updated, changed, err := updateOperationRecord(p.store, operation.ID, func(current *app.MCPOperation) bool {
		if operationTerminal(current.State) {
			return false
		}
		current.Result = append([]byte(nil), operation.Result...)
		applyWorkflowResultToOperation(current, resultStatus, request.ResultError)
		return true
	})
	if err != nil {
		if errors.Is(err, store.ErrMCPOperationVersionConflict) {
			return app.DeliveryReceipt{}, delivery.NewError(delivery.CodeOutcomeUnknown, "MCP operation changed during delivery", "outcome_unknown")
		}
		return app.DeliveryReceipt{}, err
	}
	if !changed {
		return app.DeliveryReceipt{}, delivery.NewError(delivery.CodeOutcomeUnknown, "MCP operation changed during delivery", "outcome_unknown")
	}
	operation = updated
	if operation.ErrorCode == "approval_not_granted" {
		rejectPendingApprovals(p.store, operation)
	}
	auditOperationStore(p.store, "mcp.operation.result_recorded", operation, "Recorded a Workflow result for an MCP operation", map[string]any{
		"outcome": operation.State, "workflow_result_status": resultStatus, "error_code": operation.ErrorCode,
	})
	receipt := app.DeliveryReceipt{
		DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded,
		ProviderRef: "mcp-operation:" + operation.ID, Attempt: 1, AttemptedAt: now, DeliveredAt: &now,
	}
	delivery.RecordExternalDelivery(p.store, endpoint, request, receipt)
	return receipt, nil
}
