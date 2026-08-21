package mcpaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const defaultTicketTTL = 24 * time.Hour
const maxTicketTTL = 24 * time.Hour
const immediateResultWait = 20 * time.Second
const maxOperationDuration = 15 * time.Minute
const maxOperationUpdateAttempts = 8

type Runtime interface {
	HandleMCPConversation(context.Context, string, string, string, app.MCPConversationRequest, app.MessageIngressContext) (agent.Result, error)
}

type ResultDeliverer func(context.Context, agent.Result) error

type operationExecution struct {
	cancel context.CancelFunc
}

type Service struct {
	store            store.Store
	runtime          Runtime
	deliver          ResultDeliverer
	mu               sync.Mutex
	cancels          map[string]*operationExecution
	channelEnabled   func(string) bool
	executionContext func() context.Context
}

func (s *Service) WithChannelEnabled(enabled func(ownerID string) bool) *Service {
	s.channelEnabled = enabled
	return s
}

func (s *Service) enabled(ownerID string) bool {
	return s.channelEnabled != nil && s.channelEnabled(ownerID)
}

func (s *Service) WithExecutionContext(executionContext func() context.Context) *Service {
	s.executionContext = executionContext
	return s
}

func New(st store.Store, runtime Runtime, deliver ResultDeliverer) *Service {
	return &Service{store: st, runtime: runtime, deliver: deliver, cancels: map[string]*operationExecution{}}
}

func (s *Service) IssueTicket(ownerID, actorID string, input IssueTicketRequest, now time.Time) (IssuedTicket, error) {
	if s == nil || s.store == nil || strings.TrimSpace(input.DomainID) == "" {
		return IssuedTicket{}, errors.New("MCP ticket domain is required")
	}
	if !s.enabled(ownerID) {
		return IssuedTicket{}, errors.New("MCP connector is disabled")
	}
	ttl := time.Duration(input.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = defaultTicketTTL
	}
	if ttl < time.Minute || ttl > maxTicketTTL {
		return IssuedTicket{}, errors.New("MCP access ticket TTL must be between 60 and 86400 seconds")
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return IssuedTicket{}, err
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	hash := sha256.Sum256([]byte(secret))
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	if actorID = strings.TrimSpace(actorID); actorID == "" {
		actorID = ownerID
	}
	ticket, err := s.store.SaveMCPAccessTicket(app.MCPAccessTicket{
		SchemaVersion: app.MCPAccessTicketSchemaVersion, ID: app.NewID("mcp_ticket"), SecretHash: hex.EncodeToString(hash[:]),
		OwnerID: ownerID, ActorID: actorID, DomainID: strings.TrimSpace(input.DomainID), AuthorizationRevision: 1,
		Scope: app.MCPAccessConversation, Status: app.MCPAccessPending, MaxUses: 1,
		IssuedAt: now, ExpiresAt: now.Add(ttl),
	})
	if err != nil {
		return IssuedTicket{}, errors.New("MCP access ticket could not be persisted")
	}
	s.audit("mcp.access_ticket.issued", "", "", ownerID, "Issued a single-use MCP access ticket", map[string]any{
		"ticket_id": ticket.ID, "domain_id": ticket.DomainID, "scope": ticket.Scope, "expires_at": ticket.ExpiresAt,
	})
	public := ticket
	public.SecretHash = ""
	return IssuedTicket{Ticket: public, Secret: secret}, nil
}

func (s *Service) Dispatch(ctx context.Context, peerRequest PeerRequest) (TransportResponse, error) {
	request := peerRequest.Request
	if request.ProtocolVersion != TransportProtocolVersion || request.Type != TransportTypeRequest || len(request.JSONRPC) == 0 || len(request.JSONRPC) > MaxRequestBytes {
		return TransportResponse{}, errors.New("invalid MCP-over-ISCP request")
	}
	if peerRequest.Peer.DomainID == "" || peerRequest.Peer.DeviceID == "" || peerRequest.Peer.KeyThumbprint == "" || peerRequest.Peer.ISCPSessionID == "" {
		return TransportResponse{}, errors.New("authenticated ISCP peer identity is required")
	}
	if !request.Deadline.IsZero() && !time.Now().UTC().Before(request.Deadline) {
		return s.errorResponse(request, nil, -32001, "request deadline expired", nil)
	}
	var rpc JSONRPCRequest
	if err := strictJSON(request.JSONRPC, &rpc); err != nil || rpc.JSONRPC != JSONRPCVersion || strings.TrimSpace(rpc.Method) == "" {
		return s.errorResponse(request, rpc.ID, -32600, "invalid JSON-RPC request", nil)
	}
	if len(rpc.ID) == 0 && rpc.Method != "notifications/initialized" {
		return TransportResponse{
			ProtocolVersion: TransportProtocolVersion, Type: TransportTypeResponse, SessionID: request.SessionID,
		}, nil
	}
	result, rpcErr := s.dispatchMethod(ctx, peerRequest.Peer, request, rpc)
	if len(rpc.ID) == 0 {
		return TransportResponse{
			ProtocolVersion: TransportProtocolVersion, Type: TransportTypeResponse, SessionID: request.SessionID,
		}, nil
	}
	if rpcErr != nil {
		return s.errorResponse(request, rpc.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
	}
	return responseEnvelope(request, JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: rpc.ID, Result: result})
}

func (s *Service) dispatchMethod(ctx context.Context, peer app.MCPPeerIdentity, transport TransportRequest, rpc JSONRPCRequest) (any, *JSONRPCError) {
	switch rpc.Method {
	case "initialize":
		var params InitializeParams
		if strictParams(rpc.Params, &params) != nil || params.ProtocolVersion != MCPProtocolVersion ||
			strings.TrimSpace(params.ClientInfo.Name) == "" || strings.TrimSpace(params.ClientInfo.Version) == "" {
			return nil, invalidParams("MCP protocol version and clientInfo must match the supported contract")
		}
		return map[string]any{
			"protocolVersion": MCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "sparkclaw-conversation-mcp", "version": "2"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "notifications/initialized":
		return nil, nil
	case "sparkclaw/access/redeem":
		var params RedeemParams
		if strictParams(rpc.Params, &params) != nil || strings.TrimSpace(params.Ticket) == "" {
			return nil, invalidParams("ticket is required")
		}
		binding, err := s.RedeemAccessTicket(params.Ticket, peer)
		if err != nil {
			return nil, &JSONRPCError{Code: -32002, Message: "MCP access ticket is invalid or unavailable"}
		}
		return map[string]any{"binding": binding}, nil
	}
	binding, ok := s.store.FindMCPBindingForPeer(peer.DomainID, peer.DeviceID, peer.KeyThumbprint)
	if !ok || binding.SchemaVersion != app.MCPBindingSchemaVersion || binding.Scope != app.MCPAccessConversation || binding.Status != app.MCPBindingActive {
		s.auditPeerDenied(peer, "mcp.binding.denied", "Rejected an MCP request without an active binding", map[string]any{"reason": "active_binding_required"})
		return nil, &JSONRPCError{Code: -32003, Message: "active MCP binding is required"}
	}
	if !s.enabled(binding.OwnerID) {
		s.auditToolDenied(peer, binding, rpc.Method, "connector_disabled")
		return nil, &JSONRPCError{Code: -32003, Message: "MCP connector is disabled"}
	}
	previousISCPSessionID := binding.LatestISCPSessionID
	if err := s.store.TouchMCPBinding(binding.ID, peer.ISCPSessionID, time.Now().UTC()); err != nil {
		return nil, &JSONRPCError{Code: -32003, Message: "MCP binding is unavailable"}
	}
	if previousISCPSessionID != "" && previousISCPSessionID != peer.ISCPSessionID {
		s.audit("mcp.binding.reconnected", binding.LinkedSessionID, "", binding.ActorID, "Observed an MCP binding on a new authenticated ISCP session", map[string]any{
			"binding_id": binding.ID, "requester_device_id": peer.DeviceID, "iscp_session_id": peer.ISCPSessionID,
			"binding_revision": binding.AuthorizationRevision,
		})
	}
	switch rpc.Method {
	case "tools/list":
		tools := s.toolsForBinding(binding)
		s.audit("mcp.tools.listed", binding.LinkedSessionID, "", binding.ActorID, "Listed the MCP tools currently authorized for a binding", map[string]any{
			"binding_id": binding.ID, "requester_device_id": peer.DeviceID, "binding_revision": binding.AuthorizationRevision,
			"scope": binding.Scope, "tool_count": len(tools),
		})
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var params CallToolParams
		if strictParams(rpc.Params, &params) != nil {
			return nil, invalidParams("invalid tools/call params")
		}
		return s.callTool(ctx, peer, binding, transport, rpc, params)
	default:
		return nil, &JSONRPCError{Code: -32601, Message: "method not found"}
	}
}

// RedeemAccessTicket atomically turns one copy-once secret into a binding for
// an identity authenticated by the active transport. ISCP and the explicitly
// enabled direct LAN transport provide that identity through different mechanisms.
func (s *Service) RedeemAccessTicket(secret string, peer app.MCPPeerIdentity) (app.MCPBinding, error) {
	if s == nil || s.store == nil || strings.TrimSpace(secret) == "" || peer.DomainID == "" || peer.DeviceID == "" ||
		peer.KeyThumbprint == "" || peer.ISCPSessionID == "" {
		return app.MCPBinding{}, store.ErrMCPAccessTicketInvalid
	}
	hash := sha256.Sum256([]byte(secret))
	secretHash := hex.EncodeToString(hash[:])
	ticket, found := s.store.FindMCPAccessTicketBySecretHash(secretHash)
	if !found || !s.enabled(ticket.OwnerID) {
		s.auditPeerDenied(peer, "mcp.access_ticket.redeem_denied", "MCP access ticket redemption was denied", map[string]any{"reason": "invalid_or_unavailable"})
		return app.MCPBinding{}, store.ErrMCPAccessTicketInvalid
	}
	binding, err := s.store.RedeemMCPAccessTicket(secretHash, peer, time.Now().UTC())
	if err != nil {
		s.auditPeerDenied(peer, "mcp.access_ticket.redeem_denied", "MCP access ticket redemption was denied", map[string]any{
			"ticket_id": ticket.ID, "reason": "invalid_or_consumed",
		})
		return app.MCPBinding{}, store.ErrMCPAccessTicketInvalid
	}
	s.audit("mcp.binding.activated", binding.LinkedSessionID, "", binding.ActorID, "Activated an MCP binding for an authenticated external device", map[string]any{
		"ticket_id": ticket.ID, "binding_id": binding.ID, "domain_id": peer.DomainID,
		"requester_device_id": peer.DeviceID, "requester_key_thumbprint": peer.KeyThumbprint,
		"iscp_session_id": peer.ISCPSessionID, "binding_revision": binding.AuthorizationRevision,
	})
	return binding, nil
}

func (s *Service) ActiveBindingForPeer(peer app.MCPPeerIdentity) (app.MCPBinding, bool) {
	if s == nil || s.store == nil {
		return app.MCPBinding{}, false
	}
	binding, ok := s.store.FindMCPBindingForPeer(peer.DomainID, peer.DeviceID, peer.KeyThumbprint)
	return binding, ok && binding.SchemaVersion == app.MCPBindingSchemaVersion && binding.Scope == app.MCPAccessConversation &&
		binding.Status == app.MCPBindingActive && s.enabled(binding.OwnerID)
}

func (s *Service) callTool(ctx context.Context, peer app.MCPPeerIdentity, binding app.MCPBinding, transport TransportRequest, rpc JSONRPCRequest, params CallToolParams) (any, *JSONRPCError) {
	if params.Name == "sparkclaw.operation.get" || params.Name == "sparkclaw.operation.result" || params.Name == "sparkclaw.operation.cancel" {
		return s.operationTool(ctx, peer, binding, params)
	}
	if params.Name != conversationToolName || binding.Scope != app.MCPAccessConversation {
		s.auditToolDenied(peer, binding, params.Name, "tool_not_available")
		return nil, &JSONRPCError{Code: -32602, Message: "tool is not available to this MCP binding"}
	}
	request, err := conversationArguments(params.Arguments)
	if err != nil {
		s.auditToolDenied(peer, binding, params.Name, "invalid_arguments")
		return nil, invalidParams(err.Error())
	}
	if transport.IdempotencyKey == "" {
		s.auditToolDenied(peer, binding, params.Name, "idempotency_key_required")
		return nil, invalidParams("idempotency_key is required for tools/call")
	}
	argumentRaw, _ := json.Marshal(params.Arguments)
	argumentHash := sha256.Sum256(argumentRaw)
	requestID := string(rpc.ID)
	fingerprintHash := sha256.Sum256([]byte(params.Name + "\x00" + string(argumentRaw)))
	operationID := stableID("mcp_operation", binding.ID, transport.IdempotencyKey)
	invocationID := stableID("mcp_invocation", binding.ID, transport.IdempotencyKey)
	messageID := stableID("m_mcp", binding.ID, transport.IdempotencyKey)
	runID := stableID("run_mcp", binding.ID, transport.IdempotencyKey)
	deadline := transport.Deadline
	now := time.Now().UTC()
	if deadline.IsZero() {
		deadline = now.Add(maxOperationDuration)
	} else if deadline.After(now.Add(maxOperationDuration)) {
		return nil, invalidParams("deadline must be within 15 minutes")
	}
	invocation := app.MCPInvocationContext{
		SchemaVersion: app.MCPInvocationSchemaVersion, ID: invocationID, MCPRequestID: requestID, MCPSessionID: transport.SessionID,
		ISCPSessionID: peer.ISCPSessionID, OperationID: operationID, RequesterDeviceID: peer.DeviceID, RequesterKeyThumbprint: peer.KeyThumbprint,
		BindingRef: binding.ID, BindingRevision: binding.AuthorizationRevision, OwnerID: binding.OwnerID, ActorID: binding.ActorID,
		ToolName:  params.Name,
		Arguments: cloneArguments(params.Arguments), ArgumentDigest: hex.EncodeToString(argumentHash[:]),
		IdempotencyKey: transport.IdempotencyKey, Deadline: deadline, MessageID: messageID, RunID: runID, CreatedAt: now,
	}
	s.mu.Lock()
	currentBinding, bindingActive := s.store.GetMCPBinding(binding.ID)
	if !bindingActive || currentBinding.Status != app.MCPBindingActive || currentBinding.AuthorizationRevision != binding.AuthorizationRevision ||
		currentBinding.RequesterDeviceID != peer.DeviceID || currentBinding.RequesterKeyThumbprint != peer.KeyThumbprint {
		s.mu.Unlock()
		return nil, &JSONRPCError{Code: -32003, Message: "active MCP binding is required"}
	}
	stored, created, createErr := s.store.CreateMCPOperation(app.MCPOperation{
		SchemaVersion: app.MCPOperationSchemaVersion, ID: operationID, BindingID: binding.ID, IdempotencyKey: transport.IdempotencyKey,
		Fingerprint: hex.EncodeToString(fingerprintHash[:]), Invocation: invocation, State: app.MCPOperationRunning,
	})
	s.mu.Unlock()
	if errors.Is(createErr, store.ErrMCPOperationConflict) {
		s.auditToolDenied(peer, binding, params.Name, "idempotency_conflict")
		return nil, &JSONRPCError{Code: -32004, Message: createErr.Error()}
	}
	if createErr != nil {
		return nil, &JSONRPCError{Code: -32603, Message: "MCP operation could not be persisted"}
	}
	if !created {
		s.auditOperation("mcp.operation.replayed", stored, peer, "Returned the durable result for an idempotent MCP invocation", map[string]any{"outcome": stored.State})
		return operationCallResult(stored, true), nil
	}
	s.auditOperation("mcp.operation.created", stored, peer, "Created a durable MCP invocation", map[string]any{
		"scope": binding.Scope,
	})
	ref := app.MCPInvocationRef{InvocationID: invocationID, OperationID: operationID, BindingRef: binding.ID, BindingRevision: binding.AuthorizationRevision, RequesterDeviceID: peer.DeviceID}
	request.Invocation = ref
	ingress := app.MessageIngressContext{
		Source:  app.MessageSourceContext{Kind: app.MessageSourceThirdPartyDevice, Adapter: "mcp", EndpointID: app.EndpointID("mcp:" + binding.ID), NativeMessageID: requestID, NativeThreadRef: transport.SessionID},
		OwnerID: binding.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: binding.ActorID},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: app.EndpointID("mcp:" + binding.ID), SourceAdmitted: true},
	}
	done := make(chan struct{})
	go s.executeOperation(ctx, deadline, binding.LinkedSessionID, messageID, runID, operationID, request, ingress, done)
	wait := immediateResultWait
	if remaining := time.Until(deadline); remaining < wait {
		wait = remaining
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	operationRecord, ok := s.store.GetMCPOperation(operationID)
	if !ok {
		return nil, &JSONRPCError{Code: -32603, Message: "MCP operation could not be read after execution"}
	}
	return operationCallResult(operationRecord, true), nil
}

func (s *Service) executeOperation(ctx context.Context, deadline time.Time, sessionID, messageID, runID, operationID string, request app.MCPConversationRequest, ingress app.MessageIngressContext, done chan<- struct{}) {
	defer close(done)
	s.mu.Lock()
	operation, ok := s.store.GetMCPOperation(operationID)
	if !ok || operationTerminal(operation.State) {
		s.mu.Unlock()
		return
	}
	executionCtx, finishExecution := s.registerOperationExecutionLocked(ctx, operationID, deadline)
	s.mu.Unlock()
	defer finishExecution()
	result, runErr := s.runtime.HandleMCPConversation(executionCtx, sessionID, messageID, runID, request, ingress)
	persistenceCtx := context.WithoutCancel(executionCtx)
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			_ = s.finishOperationCancelled(persistenceCtx, operationID)
			return
		}
		_ = s.finishOperationError(persistenceCtx, operationID, "workflow_failed", "SparkClaw workflow execution failed")
		return
	}
	if result.WorkflowResult == nil {
		// Terminal fallback: syncOperationFromResult refuses nil results, so
		// without this the operation would stay "running" forever.
		_ = s.finishOperationError(persistenceCtx, operationID, "workflow_result_missing", "SparkClaw workflow produced no result")
		return
	}
	if s.deliver != nil {
		if err := s.deliver(executionCtx, result); err != nil {
			_ = s.finishOperationError(persistenceCtx, operationID, "delivery_failed", "MCP result delivery failed")
		}
		return
	}
	_ = s.syncOperationFromResult(persistenceCtx, operationID, result)
}

func (s *Service) operationTool(ctx context.Context, peer app.MCPPeerIdentity, binding app.MCPBinding, params CallToolParams) (any, *JSONRPCError) {
	var input OperationParams
	raw, _ := json.Marshal(params.Arguments)
	if strictParams(raw, &input) != nil || input.OperationID == "" {
		return nil, invalidParams("operation_id is required")
	}
	operation, ok := s.store.GetMCPOperation(input.OperationID)
	if !ok || operation.BindingID != binding.ID {
		return nil, &JSONRPCError{Code: -32005, Message: "operation not found"}
	}
	operation, err := s.reconcileOperation(ctx, operation)
	if err != nil {
		return nil, &JSONRPCError{Code: -32603, Message: "approval state is temporarily unavailable"}
	}
	if params.Name == "sparkclaw.operation.cancel" {
		s.mu.Lock()
		updated, _, err := updateOperationRecord(ctx, s.store, operation.ID, func(current *app.MCPOperation) bool {
			if operationTerminal(current.State) {
				return false
			}
			current.State = app.MCPOperationCancelled
			now := time.Now().UTC()
			current.CompletedAt = &now
			return true
		})
		if execution := s.cancels[operation.ID]; execution != nil {
			execution.cancel()
		}
		s.mu.Unlock()
		if err != nil {
			return nil, &JSONRPCError{Code: -32603, Message: "MCP operation cancellation could not be persisted"}
		}
		operation = updated
		if operation.State == app.MCPOperationCancelled {
			if err := rejectPendingApprovals(ctx, s.store, operation); err != nil {
				return nil, &JSONRPCError{Code: -32603, Message: "MCP operation cancellation could not be finalized"}
			}
		}
	}
	s.auditOperation(operationAuditType(params.Name), operation, peer, "Processed a binding-scoped MCP operation request", map[string]any{"outcome": operation.State})
	if params.Name == "sparkclaw.operation.result" && !operationTerminal(operation.State) {
		return callToolResult(map[string]any{"operation": operation, "ready": false}), nil
	}
	return operationCallResult(operation, params.Name == "sparkclaw.operation.result"), nil
}

func (s *Service) reconcileOperation(ctx context.Context, operation app.MCPOperation) (app.MCPOperation, error) {
	if operation.State != app.MCPOperationApprovalRequired {
		return operation, nil
	}
	approvals, err := s.store.ListApprovals(ctx, "")
	if err != nil {
		return operation, err
	}
	for _, approval := range approvals {
		if approval.RunID != operation.Invocation.RunID || approval.Status != "rejected" {
			continue
		}
		operation.State, operation.ErrorCode, operation.ErrorMessage = app.MCPOperationFailed, "approval_rejected", "The local owner rejected the pending action"
		now := time.Now().UTC()
		operation.CompletedAt = &now
		updated, _, err := updateOperationRecord(ctx, s.store, operation.ID, func(current *app.MCPOperation) bool {
			if current.State != app.MCPOperationApprovalRequired {
				return false
			}
			current.State, current.ErrorCode, current.ErrorMessage = app.MCPOperationFailed, "approval_rejected", "The local owner rejected the pending action"
			current.CompletedAt = &now
			return true
		})
		if err == nil {
			return updated, nil
		}
		if current, found := s.store.GetMCPOperation(operation.ID); found {
			return current, nil
		}
		return operation, nil
	}
	return operation, nil
}

func (s *Service) RevokeBinding(ctx context.Context, id string, now time.Time) (app.MCPBinding, error) {
	s.mu.Lock()
	binding, err := s.store.RevokeMCPBinding(id, now)
	if err != nil {
		s.mu.Unlock()
		return app.MCPBinding{}, err
	}
	operations := s.store.ListMCPOperations(id)
	s.cancelRevokedOperationsLocked(operations)
	s.mu.Unlock()
	if err := finalizeRevokedOperations(ctx, s.store, operations); err != nil {
		return binding, err
	}
	return binding, nil
}

func (s *Service) DeleteBinding(ctx context.Context, ownerID, id string, now time.Time) (app.MCPBinding, error) {
	if s == nil || s.store == nil {
		return app.MCPBinding{}, store.ErrMCPBindingUnavailable
	}
	s.mu.Lock()
	binding, ok := s.store.GetMCPBinding(id)
	if !ok || binding.OwnerID != ownerID {
		s.mu.Unlock()
		return app.MCPBinding{}, store.ErrMCPBindingUnavailable
	}
	if binding.Status != app.MCPBindingRevoked {
		var err error
		binding, err = s.store.RevokeMCPBinding(id, now)
		if err != nil {
			s.mu.Unlock()
			return app.MCPBinding{}, err
		}
	}
	operations := s.store.ListMCPOperations(id)
	s.cancelRevokedOperationsLocked(operations)
	if err := finalizeRevokedOperations(ctx, s.store, operations); err != nil {
		s.mu.Unlock()
		return binding, err
	}
	deleted, err := s.store.DeleteMCPBinding(ownerID, id)
	s.mu.Unlock()
	if err != nil {
		return app.MCPBinding{}, err
	}
	return deleted, nil
}

func (s *Service) DeleteAccessRecords(ctx context.Context, ownerID string, now time.Time) (store.MCPAccessRecordDeletion, error) {
	if s == nil || s.store == nil {
		return store.MCPAccessRecordDeletion{}, store.ErrMCPBindingUnavailable
	}
	s.mu.Lock()
	for _, binding := range s.store.ListMCPBindings(ownerID) {
		if binding.Status != app.MCPBindingRevoked {
			if _, err := s.store.RevokeMCPBinding(binding.ID, now); err != nil {
				s.mu.Unlock()
				return store.MCPAccessRecordDeletion{}, err
			}
		}
		operations := s.store.ListMCPOperations(binding.ID)
		s.cancelRevokedOperationsLocked(operations)
		if err := finalizeRevokedOperations(ctx, s.store, operations); err != nil {
			s.mu.Unlock()
			return store.MCPAccessRecordDeletion{}, err
		}
	}
	deleted, err := s.store.DeleteMCPAccessRecords(ownerID)
	s.mu.Unlock()
	return deleted, err
}

func (s *Service) cancelRevokedOperationsLocked(operations []app.MCPOperation) {
	for _, operation := range operations {
		if operation.State != app.MCPOperationRevoked {
			continue
		}
		if execution := s.cancels[operation.ID]; execution != nil {
			execution.cancel()
		}
	}
}

func (s *Service) registerOperationExecutionLocked(fallback context.Context, operationID string, deadline time.Time) (context.Context, func()) {
	base := fallback
	if s.executionContext != nil {
		if current := s.executionContext(); current != nil {
			base = current
		}
	}
	var (
		executionCtx context.Context
		cancel       context.CancelFunc
	)
	if deadline.IsZero() {
		executionCtx, cancel = context.WithCancel(base)
	} else {
		executionCtx, cancel = context.WithDeadline(base, deadline)
	}
	if previous := s.cancels[operationID]; previous != nil {
		previous.cancel()
	}
	registered := &operationExecution{cancel: cancel}
	s.cancels[operationID] = registered
	var once sync.Once
	return executionCtx, func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			if s.cancels[operationID] == registered {
				delete(s.cancels, operationID)
			}
			s.mu.Unlock()
		})
	}
}

func finalizeRevokedOperations(ctx context.Context, st store.Store, operations []app.MCPOperation) error {
	for _, operation := range operations {
		if operation.State != app.MCPOperationRevoked {
			continue
		}
		if err := rejectPendingApprovals(ctx, st, operation); err != nil {
			return err
		}
		auditOperationStore(st, "mcp.operation.revoked", operation, "Revoked an MCP operation with its binding", map[string]any{
			"outcome": operation.State, "error_code": operation.ErrorCode,
		})
	}
	return nil
}

func (s *Service) toolsForBinding(binding app.MCPBinding) []Tool {
	tools := []Tool{conversationTool()}
	if binding.SchemaVersion != app.MCPBindingSchemaVersion || binding.Scope != app.MCPAccessConversation {
		return []Tool{}
	}
	tools = append(tools, operationTools()...)
	return tools
}

func (s *Service) syncOperationFromResult(ctx context.Context, id string, result agent.Result) error {
	return s.syncOperationFromResultWithContent(ctx, id, result, true)
}

func (s *Service) syncOperationFromResultWithContent(ctx context.Context, id string, result agent.Result, includeContent bool) error {
	approvals := []app.Approval(nil)
	if result.WorkflowResult != nil && result.WorkflowResult.Status == app.WorkflowResultWaiting {
		var err error
		approvals, err = s.store.ListApprovals(ctx, "")
		if err != nil {
			return err
		}
	}
	updated, changed, err := updateOperationRecord(ctx, s.store, id, func(operation *app.MCPOperation) bool {
		if operationTerminal(operation.State) || result.WorkflowResult == nil {
			return false
		}
		if result.WorkflowResult.Status == app.WorkflowResultWaiting && runHasApprovedApproval(approvals, result.Run.ID) && !runHasPendingApproval(approvals, result.Run.ID) {
			return false
		}
		payload := map[string]any{"run_id": result.Run.ID}
		if includeContent {
			payload["content"] = result.WorkflowResult.Content
		} else {
			payload["delivery_recorded"] = result.WorkflowResult.Status == app.WorkflowResultSucceeded
		}
		raw, _ := json.Marshal(payload)
		operation.Result = raw
		applyWorkflowResultToOperation(operation, result.WorkflowResult.Status, result.WorkflowResult.Error)
		return true
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	auditOperationStore(s.store, "mcp.operation.result_recorded", updated, "Recorded a Workflow result for an MCP operation", map[string]any{
		"outcome": updated.State, "error_code": updated.ErrorCode,
	})
	return nil
}

func runHasApprovedApproval(approvals []app.Approval, runID string) bool {
	for _, approval := range approvals {
		if approval.Status != "approved" {
			continue
		}
		if approval.RunID == runID {
			return true
		}
	}
	return false
}

func runHasPendingApproval(approvals []app.Approval, runID string) bool {
	for _, approval := range approvals {
		if approval.Status != "pending" {
			continue
		}
		if approval.RunID == runID {
			return true
		}
	}
	return false
}

// BeginApprovalExecution moves an operation out of its owner-waiting state
// after the approval decision is durable. The actual tool and Workflow resume
// may then continue independently of the approval HTTP request.
func (s *Service) BeginApprovalExecution(ctx context.Context, runID string) (app.MCPOperation, error) {
	return s.beginApprovalExecution(ctx, runID)
}

// StartApprovalExecution also registers the resumed work under the operation's
// cancellable lifetime. Binding revocation, operation cancellation, Gateway
// shutdown, and the original invocation deadline therefore stop the resumed
// tool/Workflow path, not only the durable operation status.
func (s *Service) StartApprovalExecution(ctx context.Context, runID string) (app.MCPOperation, context.Context, func(), error) {
	if s == nil || s.store == nil {
		return app.MCPOperation{}, nil, nil, errors.New("MCP access service is unavailable")
	}
	s.mu.Lock()
	operation, err := s.beginApprovalExecution(ctx, runID)
	if err != nil {
		s.mu.Unlock()
		return operation, nil, nil, err
	}
	executionCtx, finishExecution := s.registerOperationExecutionLocked(ctx, operation.ID, operation.Invocation.Deadline)
	s.mu.Unlock()
	if err := executionCtx.Err(); err != nil {
		finishExecution()
		_ = s.finishOperationError(ctx, operation.ID, "approval_execution_expired", "The MCP operation deadline expired before approved execution could start")
		if current, ok := s.store.GetMCPOperation(operation.ID); ok {
			operation = current
		}
		return operation, nil, nil, errors.New("MCP operation deadline expired before approved execution could start")
	}
	return operation, executionCtx, finishExecution, nil
}

func (s *Service) beginApprovalExecution(ctx context.Context, runID string) (app.MCPOperation, error) {
	operation, err := s.operationForRun(ctx, runID)
	if err != nil {
		return app.MCPOperation{}, err
	}
	updated, changed, err := updateOperationRecord(ctx, s.store, operation.ID, func(current *app.MCPOperation) bool {
		if current.State == app.MCPOperationRunning {
			return false
		}
		if current.State != app.MCPOperationApprovalRequired {
			return false
		}
		current.State = app.MCPOperationRunning
		current.Result = nil
		current.ErrorCode = ""
		current.ErrorMessage = ""
		current.CompletedAt = nil
		return true
	})
	if err != nil {
		return app.MCPOperation{}, err
	}
	if !changed && updated.State != app.MCPOperationRunning {
		return updated, errors.New("MCP operation is no longer available for approved execution")
	}
	if changed {
		auditOperationStore(s.store, "mcp.operation.approval_resumed", updated, "Resumed an MCP operation after owner approval", map[string]any{"outcome": updated.State})
	}
	return updated, nil
}

func (s *Service) RestoreApprovalRequired(ctx context.Context, runID string) error {
	operation, err := s.operationForRun(ctx, runID)
	if err != nil {
		return err
	}
	updated, changed, err := updateOperationRecord(ctx, s.store, operation.ID, func(current *app.MCPOperation) bool {
		if current.State != app.MCPOperationRunning {
			return false
		}
		current.State = app.MCPOperationApprovalRequired
		current.Result = nil
		current.CompletedAt = nil
		return true
	})
	if err != nil {
		return err
	}
	if changed {
		auditOperationStore(s.store, "mcp.operation.approval_required", updated, "Parked an MCP operation for another owner approval", map[string]any{"outcome": updated.State})
	}
	return nil
}

func (s *Service) FailApprovalExecution(ctx context.Context, runID, code, message string) error {
	operation, err := s.operationForRun(ctx, runID)
	if err != nil {
		return err
	}
	return s.finishOperationError(ctx, operation.ID, code, message)
}

func (s *Service) operationForRun(ctx context.Context, runID string) (app.MCPOperation, error) {
	if s == nil || s.store == nil {
		return app.MCPOperation{}, errors.New("MCP access service is unavailable")
	}
	run, ok, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return app.MCPOperation{}, err
	}
	if !ok || run.MessageContext == nil || run.MessageContext.MCP == nil {
		return app.MCPOperation{}, errors.New("MCP run is unavailable")
	}
	ref := run.MessageContext.MCP
	operation, ok := s.store.GetMCPOperation(ref.OperationID)
	if !ok || operation.Invocation.RunID != run.ID || operation.Invocation.ID != ref.InvocationID ||
		operation.BindingID != ref.BindingRef || operation.Invocation.BindingRevision != ref.BindingRevision ||
		operation.Invocation.RequesterDeviceID != ref.RequesterDeviceID {
		return app.MCPOperation{}, errors.New("MCP operation does not match the persisted invocation")
	}
	return operation, nil
}

// RecordWorkflowResult synchronizes an MCP operation even when the frozen
// Workflow return route targets another delivery provider or intentionally
// suppresses a waiting/failed external send.
func (s *Service) RecordWorkflowResult(ctx context.Context, result agent.Result) error {
	if s == nil || result.WorkflowResult == nil || result.WorkflowResult.MCP == nil {
		return nil
	}
	ref := result.WorkflowResult.MCP
	operation, ok := s.store.GetMCPOperation(ref.OperationID)
	if !ok || operation.Invocation.ID != ref.InvocationID || operation.BindingID != ref.BindingRef ||
		operation.Invocation.BindingRevision != ref.BindingRevision || operation.Invocation.RequesterDeviceID != ref.RequesterDeviceID ||
		operation.Invocation.RunID != result.Run.ID || operation.Invocation.RunID != result.WorkflowResult.RunID {
		return nil
	}
	includeContent := result.WorkflowResult.ReturnRoute.Mode == app.ReturnToSource
	return s.syncOperationFromResultWithContent(ctx, operation.ID, result, includeContent)
}

func (s *Service) finishOperationError(ctx context.Context, id, code, message string) error {
	updated, changed, err := updateOperationRecord(ctx, s.store, id, func(operation *app.MCPOperation) bool {
		if operationTerminal(operation.State) {
			return false
		}
		operation.State, operation.ErrorCode, operation.ErrorMessage = app.MCPOperationFailed, code, message
		now := time.Now().UTC()
		operation.CompletedAt = &now
		return true
	})
	if err != nil {
		return err
	}
	if changed {
		if err := rejectPendingApprovals(ctx, s.store, updated); err != nil {
			return err
		}
		auditOperationStore(s.store, "mcp.operation.failed", updated, "Marked an MCP operation as failed", map[string]any{"outcome": updated.State, "error_code": code})
	}
	return nil
}

func (s *Service) finishOperationCancelled(ctx context.Context, id string) error {
	updated, changed, err := updateOperationRecord(ctx, s.store, id, func(operation *app.MCPOperation) bool {
		if operationTerminal(operation.State) {
			return false
		}
		operation.State = app.MCPOperationCancelled
		now := time.Now().UTC()
		operation.CompletedAt = &now
		return true
	})
	if err != nil {
		return err
	}
	if changed {
		auditOperationStore(s.store, "mcp.operation.cancelled", updated, "Marked an MCP operation as cancelled", map[string]any{"outcome": updated.State})
	}
	return nil
}

func operationTerminal(state app.MCPOperationState) bool {
	return state == app.MCPOperationSucceeded || state == app.MCPOperationFailed || state == app.MCPOperationCancelled || state == app.MCPOperationRevoked
}

func (s *Service) errorResponse(request TransportRequest, id json.RawMessage, code int, message string, data any) (TransportResponse, error) {
	return responseEnvelope(request, JSONRPCResponse{JSONRPC: JSONRPCVersion, ID: id, Error: &JSONRPCError{Code: code, Message: message, Data: data}})
}

func invalidParams(message string) *JSONRPCError {
	return &JSONRPCError{Code: -32602, Message: message}
}

func strictJSON(raw []byte, dst any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
func strictParams(raw []byte, dst any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	return strictJSON(raw, dst)
}
func cloneArguments(in map[string]any) map[string]any {
	raw, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}
func stableID(prefix string, values ...string) string {
	h := sha256.New()
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return prefix + "_" + hex.EncodeToString(h.Sum(nil)[:16])
}
func operationCallResult(operation app.MCPOperation, resultOnly bool) map[string]any {
	if resultOnly && len(operation.Result) > 0 && operation.State == app.MCPOperationSucceeded {
		var result map[string]any
		if json.Unmarshal(operation.Result, &result) == nil && result["structuredContent"] != nil {
			return result
		}
	}
	out := map[string]any{"operation": operation}
	if resultOnly && len(operation.Result) > 0 {
		var result any
		_ = json.Unmarshal(operation.Result, &result)
		out["result"] = result
	}
	return callToolResult(out)
}

func callToolResult(structured map[string]any) map[string]any {
	raw, _ := json.Marshal(structured)
	return map[string]any{
		"content":           []CallToolContent{{Type: "text", Text: string(raw)}},
		"structuredContent": structured,
	}
}
