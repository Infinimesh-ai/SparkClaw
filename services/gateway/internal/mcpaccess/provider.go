package mcpaccess

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
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
		MaxParts:     9, MaxTotalBytes: MaxResultRawBinaryBytes,
		MaxBytesByKind: map[app.MessagePartKind]int64{
			app.MessagePartImage: MaxResultRawBinaryBytes,
			app.MessagePartAudio: MaxResultRawBinaryBytes,
			app.MessagePartFile:  MaxResultRawBinaryBytes,
		},
		SupportsCaption: true,
	}
}

func (p *Provider) Deliver(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
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
	resultStatus := request.ResultStatus
	if resultStatus == "" {
		run, hasRun, err := p.store.GetRun(ctx, operation.Invocation.RunID)
		if err != nil {
			return app.DeliveryReceipt{}, delivery.NewError(delivery.CodeProviderRetryable, "MCP workflow state is unavailable", "retryable")
		}
		if hasRun && (run.State == "approval_pending" || run.State == "browser_login_blocked") {
			resultStatus = app.WorkflowResultWaiting
		} else {
			resultStatus = app.WorkflowResultSucceeded
		}
	}
	var payload []byte
	if resultStatus == app.WorkflowResultSucceeded {
		result, err := p.callToolResult(ctx, endpoint, request, operation)
		if err != nil {
			return app.DeliveryReceipt{}, err
		}
		payload, err = json.Marshal(result)
		if err != nil {
			return app.DeliveryReceipt{}, err
		}
		if len(payload) > MaxResultEnvelopeBytes {
			return app.DeliveryReceipt{}, delivery.NewError(delivery.CodePayloadTooLarge, "encoded MCP result exceeds the qualified transport envelope", "blocked")
		}
	}
	operation.Result = payload
	applyWorkflowResultToOperation(&operation, resultStatus, request.ResultError)
	now := time.Now().UTC()
	updated, changed, err := updateOperationRecord(ctx, p.store, operation.ID, func(current *app.MCPOperation) bool {
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
	auditOperationStore(ctx, p.store, "mcp.operation.result_recorded", operation, "Recorded a Workflow result for an MCP operation", map[string]any{
		"outcome": operation.State, "workflow_result_status": resultStatus, "error_code": operation.ErrorCode,
	})

	receipt := app.DeliveryReceipt{
		DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded,
		ProviderRef: "mcp-operation:" + operation.ID, Attempt: 1, AttemptedAt: now, DeliveredAt: &now,
	}
	delivery.RecordExternalDelivery(p.store, endpoint, request, receipt)
	return receipt, nil
}

func (p *Provider) callToolResult(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest, operation app.MCPOperation) (CallToolResult, error) {
	if err := delivery.ValidateCapabilities(p.Capabilities(), request.Content); err != nil {
		return CallToolResult{}, err
	}
	prepared, err := delivery.PrepareParts(ctx, request.Content, delivery.NewEndpointResourceResolver(p.store, endpoint))
	if err != nil {
		return CallToolResult{}, delivery.NewError(delivery.CodeArtifactInvalid, "MCP result resource could not be resolved", "blocked")
	}
	content := make([]CallToolContent, 0, len(prepared))
	parts := make([]map[string]any, 0, len(prepared))
	for index, item := range prepared {
		part := item.Part
		if part.Kind != app.MessagePartText {
			belongs, err := p.resourceBelongsToLinkedSession(ctx, endpoint.SessionID, part)
			if err != nil {
				return CallToolResult{}, delivery.NewError(delivery.CodeProviderRetryable, "MCP result artifact metadata is unavailable", "retryable")
			}
			if !belongs {
				return CallToolResult{}, delivery.NewError(delivery.CodeCrossUserDenied, fmt.Sprintf("MCP result part %q is outside the linked conversation", part.ID), "blocked")
			}
		}
		if strings.TrimSpace(part.ContentType) == "" {
			part.ContentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(part.Name)))
			if part.ContentType == "" {
				part.ContentType = "application/octet-stream"
			}
		}
		projection := map[string]any{
			"index": index, "kind": part.Kind, "name": part.Name, "content_type": part.ContentType, "bytes": part.Bytes, "sha256": part.SHA256,
		}
		if part.Kind == app.MessagePartText {
			content = append(content, CallToolContent{Type: "text", Text: part.Text})
			parts = append(parts, projection)
			continue
		}
		raw, err := os.ReadFile(item.Path)
		if err != nil {
			return CallToolResult{}, delivery.NewError(delivery.CodeArtifactInvalid, fmt.Sprintf("MCP result part %q is unreadable", part.ID), "blocked")
		}
		if part.Bytes > 0 && len(raw) != part.Bytes {
			return CallToolResult{}, delivery.NewError(delivery.CodeArtifactInvalid, fmt.Sprintf("MCP result part %q changed after governance", part.ID), "blocked")
		}
		digest := sha256.Sum256(raw)
		digestHex := hex.EncodeToString(digest[:])
		if strings.TrimSpace(part.SHA256) != "" && !strings.EqualFold(strings.TrimSpace(part.SHA256), digestHex) {
			return CallToolResult{}, delivery.NewError(delivery.CodeArtifactInvalid, fmt.Sprintf("MCP result part %q changed after governance", part.ID), "blocked")
		}
		projection["bytes"] = len(raw)
		projection["sha256"] = digestHex
		encoded := base64.StdEncoding.EncodeToString(raw)
		switch part.Kind {
		case app.MessagePartImage:
			content = append(content, CallToolContent{Type: "image", Data: encoded, MimeType: part.ContentType})
		case app.MessagePartAudio:
			content = append(content, CallToolContent{Type: "audio", Data: encoded, MimeType: part.ContentType})
		case app.MessagePartFile:
			content = append(content, CallToolContent{Type: "resource", Resource: &CallToolResource{
				URI:  "sparkclaw://mcp-operation/" + operation.ID + "/part/" + fmt.Sprint(index),
				Name: part.Name, MimeType: part.ContentType, Blob: encoded,
			}})
		default:
			return CallToolResult{}, delivery.NewError(delivery.CodePartUnsupported, fmt.Sprintf("MCP result kind %q is unsupported", part.Kind), "blocked")
		}
		parts = append(parts, projection)
	}
	structured := map[string]any{
		"operation_id": operation.ID, "result_id": request.ResultID, "run_id": request.RunID,
		"state": app.MCPOperationSucceeded, "completed_at": time.Now().UTC(),
		"result_status": request.ResultStatus, "parts": parts,
	}
	return CallToolResult{Content: content, StructuredContent: structured}, nil
}

func (p *Provider) resourceBelongsToLinkedSession(ctx context.Context, sessionID string, part app.MessagePart) (bool, error) {
	if p == nil || p.store == nil || strings.TrimSpace(sessionID) == "" {
		return false, nil
	}
	if artifactID := strings.TrimSpace(part.ArtifactID); artifactID != "" {
		objects, err := p.store.ListArtifactObjects(ctx, 0)
		if err != nil {
			return false, err
		}
		for _, object := range objects {
			if object.ID == artifactID {
				return object.SessionID == sessionID, nil
			}
		}
		return false, nil
	}
	return part.Resource != nil && part.Resource.Kind == "workspace_file" && strings.TrimSpace(part.Resource.Ref) != "", nil
}
