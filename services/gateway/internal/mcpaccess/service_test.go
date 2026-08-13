package mcpaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type fakeRuntime struct {
	request            app.MCPBoundRouteRequest
	ingress            app.MessageIngressContext
	result             agent.Result
	block              chan struct{}
	invoked            chan struct{}
	ignoreCancellation bool
}

type terminalConflictStore struct {
	store.Store
	conflicted bool
}

func (s *terminalConflictStore) UpdateMCPOperation(operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error) {
	if !s.conflicted {
		s.conflicted = true
		current, ok := s.Store.GetMCPOperation(operation.ID)
		if !ok {
			return app.MCPOperation{}, errors.New("MCP operation not found")
		}
		current.State = app.MCPOperationSucceeded
		now := time.Now().UTC()
		current.CompletedAt = &now
		if _, err := s.Store.UpdateMCPOperation(current, current.Version); err != nil {
			return app.MCPOperation{}, err
		}
		return app.MCPOperation{}, store.ErrMCPOperationVersionConflict
	}
	return s.Store.UpdateMCPOperation(operation, expectedVersion)
}

func TestServiceCASConflictReturnsConcurrentTerminalOperation(t *testing.T) {
	base := store.NewMemoryStore()
	operation, _, err := base.CreateMCPOperation(app.MCPOperation{
		ID: "operation-conflict", BindingID: "binding-a", IdempotencyKey: "conflict", Fingerprint: "conflict",
	})
	if err != nil {
		t.Fatal(err)
	}
	service := New(&terminalConflictStore{Store: base}, &fakeRuntime{}, nil)
	updated, changed, err := updateOperationRecord(service.store, operation.ID, func(current *app.MCPOperation) bool {
		if operationTerminal(current.State) {
			return false
		}
		current.State = app.MCPOperationCancelled
		return true
	})
	if err != nil || changed || updated.State != app.MCPOperationSucceeded {
		t.Fatalf("CAS reconciliation = %#v changed=%v err=%v", updated, changed, err)
	}
}

func (r *fakeRuntime) MCPRouteAvailable(id app.CapabilityID, workflow app.WorkflowContractRef) bool {
	return id == app.CapabilityConversationAnswer && workflow.ID == app.WorkflowConversationAnswer && workflow.Revision == 2
}

func (r *fakeRuntime) HandleMCPBoundRoute(ctx context.Context, sessionID, _, _ string, request app.MCPBoundRouteRequest, ingress app.MessageIngressContext) (agent.Result, error) {
	r.request, r.ingress = request, ingress
	if r.invoked != nil {
		select {
		case r.invoked <- struct{}{}:
		default:
		}
	}
	if r.block != nil {
		if r.ignoreCancellation {
			<-r.block
			return r.result, nil
		}
		select {
		case <-r.block:
		case <-ctx.Done():
			return agent.Result{}, ctx.Err()
		}
	}
	return r.result, nil
}

func TestServiceDefaultsAccessTicketTTLToOneDay(t *testing.T) {
	st := store.NewMemoryStore()
	service := New(st, &fakeRuntime{}, nil).WithChannelEnabled(func(string) bool { return true })
	now := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := issued.Ticket.ExpiresAt.Sub(issued.Ticket.IssuedAt); got != 24*time.Hour {
		t.Fatalf("default MCP access ticket TTL = %s, want 24h", got)
	}
}

func TestServiceDoesNotStartAlreadyCancelledOperation(t *testing.T) {
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{
		ID: "operation-cancelled-before-start", BindingID: "binding-a", IdempotencyKey: "cancelled-before-start", Fingerprint: "cancelled-before-start",
		State: app.MCPOperationCancelled, CompletedAt: &now,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{invoked: make(chan struct{}, 1)}
	service := New(st, runtime, nil)
	done := make(chan struct{})
	go service.executeOperation(time.Now().Add(time.Minute), "session-a", "message-a", "run-a", operation.ID, app.MCPBoundRouteRequest{}, app.MessageIngressContext{}, done)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled operation did not leave the execution queue")
	}
	select {
	case <-runtime.invoked:
		t.Fatal("already cancelled operation entered Runtime")
	default:
	}
}

func TestServiceLateResultCannotOverwriteCancelledOperation(t *testing.T) {
	st := store.NewMemoryStore()
	runtime := &fakeRuntime{
		block: make(chan struct{}), ignoreCancellation: true,
		result: agent.Result{Run: app.AgentRun{ID: "late-run", State: "completed"}, WorkflowResult: &app.WorkflowResult{
			Status:  app.WorkflowResultSucceeded,
			Content: app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Text: "late"}}},
		}},
	}
	service := New(st, runtime, func(context.Context, agent.Result) error {
		return errors.New("terminal delivery rejected")
	}).WithChannelEnabled(func(string) bool { return true })
	issued, err := service.IssueTicket(app.DefaultOwnerID, "management-client", IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	_ = bindingFromRPCResult(t, dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}))

	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_ = dispatchRPC(t, service, peer, "mcp", "late-result", "tools/call", map[string]any{
			"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "wait"},
		})
	}()
	binding := bindingForPeer(t, st, peer)
	operation := waitForOperation(t, st, binding.ID, "late-result")
	cancelled := operationFromRPCResult(t, dispatchRPC(t, service, peer, "mcp", "", "tools/call", map[string]any{
		"name": "sparkclaw.operation.cancel", "arguments": map[string]any{"operation_id": operation.ID},
	}))
	if cancelled.State != app.MCPOperationCancelled {
		t.Fatalf("cancel result = %#v", cancelled)
	}
	close(runtime.block)
	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("late runtime did not finish")
	}
	stored, _ := st.GetMCPOperation(operation.ID)
	if stored.State != app.MCPOperationCancelled {
		t.Fatalf("late result overwrote cancellation: %#v", stored)
	}
}

func TestServiceNegotiatesMCPVersionAndIgnoresBusinessNotifications(t *testing.T) {
	st := store.NewMemoryStore()
	service := New(st, &fakeRuntime{}, nil).WithChannelEnabled(func(string) bool { return true })
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	initialized := dispatchRPC(t, service, peer, "mcp", "", "initialize", map[string]any{
		"protocolVersion": MCPProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "1"},
	})
	if initialized.Error != nil {
		t.Fatalf("initialize failed: %#v", initialized.Error)
	}
	raw, _ := json.Marshal(initialized.Result)
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.ProtocolVersion != MCPProtocolVersion || result.Capabilities.Tools.ListChanged {
		t.Fatalf("invalid initialize result: %s err=%v", raw, err)
	}
	wrong := dispatchRPC(t, service, peer, "mcp", "", "initialize", map[string]any{
		"protocolVersion": "2026-01-01", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test-client", "version": "1"},
	})
	if wrong.Error == nil || wrong.Error.Code != -32602 {
		t.Fatalf("unsupported MCP version was negotiated: %#v", wrong)
	}

	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"ticket": issued.Secret})
	rpcRaw, _ := json.Marshal(JSONRPCRequest{JSONRPC: JSONRPCVersion, Method: "sparkclaw/access/redeem", Params: params})
	response, err := service.Dispatch(t.Context(), PeerRequest{Peer: peer, Request: TransportRequest{
		ProtocolVersion: TransportProtocolVersion, Type: TransportTypeRequest, SessionID: "mcp", JSONRPC: rpcRaw,
	}})
	if err != nil || len(response.JSONRPC) != 0 {
		t.Fatalf("business notification returned JSON-RPC data: response=%#v err=%v", response, err)
	}
	ticket, _ := st.GetMCPAccessTicket(issued.Ticket.ID)
	if ticket.Status != app.MCPAccessPending || ticket.UseCount != 0 {
		t.Fatalf("business notification executed without a request ID: %#v", ticket)
	}
}

func TestServiceTicketBindingAndBoundLeafFlow(t *testing.T) {
	st := store.NewMemoryStore()
	runtime := &fakeRuntime{}
	service := New(st, runtime, nil).WithChannelEnabled(func(string) bool { return true })
	now := time.Now().UTC()
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer, Operations: []app.RouteOperation{app.RouteOperationAnswer}}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Secret == "" || issued.Ticket.SecretHash != "" {
		t.Fatalf("ticket secret contract leaked hash or omitted secret: %#v", issued)
	}
	if issued.Ticket.ActorID != app.DefaultOwnerID {
		t.Fatalf("management client became the MCP execution actor: %#v", issued.Ticket)
	}
	stored, _ := st.GetMCPAccessTicket(issued.Ticket.ID)
	if stored.SecretHash == "" || stored.SecretHash == issued.Secret {
		t.Fatal("store did not retain only a hash")
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "external-device", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-session-a"}
	redeem := dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret})
	binding := bindingFromRPCResult(t, redeem)
	if binding.RequesterDeviceID != peer.DeviceID || binding.ActorID != app.DefaultOwnerID || binding.ActorID == binding.RequesterDeviceID {
		t.Fatalf("identity separation failed: %#v", binding)
	}
	replayed := dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret})
	if replayed.Error == nil {
		t.Fatal("ticket replay succeeded")
	}

	listed := dispatchRPC(t, service, peer, "mcp-session", "", "tools/list", map[string]any{})
	tools := toolsFromRPCResult(t, listed)
	if !toolListed(tools, "sparkclaw.route.conversation.answer") || !toolListed(tools, "sparkclaw.operation.get") {
		t.Fatalf("filtered tool list = %#v", tools)
	}

	runtime.result = agent.Result{Run: app.AgentRun{ID: "run-result", State: "completed"}, WorkflowResult: &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, Status: app.WorkflowResultSucceeded,
		Content: app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "answer"}}},
	}}
	called := dispatchRPC(t, service, peer, "mcp-session", "idem-a", "tools/call", map[string]any{
		"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "exact request"},
	})
	operation := operationFromRPCResult(t, called)
	if operation.State != app.MCPOperationSucceeded || runtime.request.CapabilityID != app.CapabilityConversationAnswer || runtime.request.Slots.Operation != app.RouteOperationAnswer {
		t.Fatalf("bound invocation mismatch: operation=%#v request=%#v", operation, runtime.request)
	}
	if runtime.ingress.Authorization.PrincipalID != app.DefaultOwnerID || runtime.request.Invocation.RequesterDeviceID != peer.DeviceID {
		t.Fatalf("requester was promoted to executor: ingress=%#v request=%#v", runtime.ingress, runtime.request)
	}
	if replay := operationFromRPCResult(t, dispatchRPC(t, service, peer, "mcp-session", "idem-a", "tools/call", map[string]any{
		"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "exact request"},
	})); replay.ID != operation.ID {
		t.Fatalf("idempotent replay changed operation: %#v", replay)
	}
	conflict := dispatchRPC(t, service, peer, "mcp-session", "idem-a", "tools/call", map[string]any{
		"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "changed"},
	})
	if conflict.Error == nil || conflict.Error.Code != -32004 {
		t.Fatalf("changed replay was not rejected: %#v", conflict)
	}
}

func TestServiceRejectsOperationDeadlineBeyondMaximum(t *testing.T) {
	st := store.NewMemoryStore()
	service := New(st, &fakeRuntime{}, nil).WithChannelEnabled(func(string) bool { return true })
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	binding := bindingFromRPCResult(t, dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}))
	paramsRaw := mustJSON(map[string]any{"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "bounded"}})
	rpcRaw := mustJSON(JSONRPCRequest{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`), Method: "tools/call", Params: paramsRaw})
	response, err := service.Dispatch(t.Context(), PeerRequest{Peer: peer, Request: TransportRequest{
		ProtocolVersion: TransportProtocolVersion, Type: TransportTypeRequest, IdempotencyKey: "too-long",
		Deadline: time.Now().UTC().Add(maxOperationDuration + time.Minute), JSONRPC: rpcRaw,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var rpc JSONRPCResponse
	if err := json.Unmarshal(response.JSONRPC, &rpc); err != nil {
		t.Fatal(err)
	}
	if rpc.Error == nil || rpc.Error.Code != -32602 {
		t.Fatalf("long deadline response = %#v", rpc)
	}
	if _, ok := st.FindMCPOperationByIdempotency(binding.ID, "too-long"); ok {
		t.Fatal("operation with an excessive deadline was persisted")
	}
}

func TestProviderMapsBlockedResultToFailedOperation(t *testing.T) {
	st := store.NewMemoryStore()
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{
		ID: "operation-blocked", BindingID: "binding-a", IdempotencyKey: "blocked", Fingerprint: "blocked",
		Invocation: app.MCPInvocationContext{ID: "inv-blocked", OperationID: "operation-blocked", BindingRef: "binding-a", ActorID: app.DefaultOwnerID},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := NewProvider(st)
	_, err = provider.Deliver(t.Context(), app.MessageEndpoint{ProviderKey: "mcp", BindingRef: "binding-a"}, app.DeliveryRequest{
		ID: "delivery-blocked", MCP: &app.MCPInvocationRef{InvocationID: "inv-blocked", OperationID: operation.ID, BindingRef: "binding-a"},
		ResultStatus: app.WorkflowResultBlocked, ResultError: &app.WorkflowResultError{Code: "policy_blocked", Message: "blocked by policy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := st.GetMCPOperation(operation.ID)
	if stored.State != app.MCPOperationFailed || stored.ErrorCode != "policy_blocked" || stored.CompletedAt == nil {
		t.Fatalf("blocked Workflow result was not terminal failure: %#v", stored)
	}
}

func TestProviderFailsClosedWhenApprovalWasNotGranted(t *testing.T) {
	st := store.NewMemoryStore()
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{
		ID: "operation-no-approval", BindingID: "binding-a", IdempotencyKey: "no-approval", Fingerprint: "no-approval",
		Invocation: app.MCPInvocationContext{ID: "inv-no-approval", OperationID: "operation-no-approval", BindingRef: "binding-a", RunID: "run-no-approval"},
	})
	if err != nil {
		t.Fatal(err)
	}
	st.SaveRun(app.AgentRun{ID: "run-no-approval", State: "approval_pending"})
	st.SaveToolCall(app.ToolCall{ID: "call-no-approval", RunID: "run-no-approval", Status: "approval_pending"})
	st.SaveApproval(app.Approval{ID: "approval-no-approval", RunID: "run-no-approval", ToolCallID: "call-no-approval", Status: "pending"})
	_, err = NewProvider(st).Deliver(t.Context(), app.MessageEndpoint{ProviderKey: "mcp", BindingRef: "binding-a"}, app.DeliveryRequest{
		ID: "delivery-no-approval", MCP: &app.MCPInvocationRef{InvocationID: "inv-no-approval", OperationID: operation.ID, BindingRef: "binding-a"},
		ResultStatus: app.WorkflowResultWaiting,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := st.GetMCPOperation(operation.ID)
	approval, _ := st.GetApproval("approval-no-approval")
	call, _ := st.GetToolCall("call-no-approval")
	run, _ := st.GetRun("run-no-approval")
	if stored.State != app.MCPOperationFailed || stored.ErrorCode != "approval_not_granted" || approval.Status != "rejected" || call.Status != "rejected" || run.State != "blocked" {
		t.Fatalf("ungranted approval did not fail closed: operation=%#v approval=%#v call=%#v run=%#v", stored, approval, call, run)
	}
}

func TestServiceMCPAuditDoesNotContainSecretOrArguments(t *testing.T) {
	st := store.NewMemoryStore()
	runtime := &fakeRuntime{result: agent.Result{Run: app.AgentRun{ID: "audit-run", State: "completed"}, WorkflowResult: &app.WorkflowResult{
		Status: app.WorkflowResultSucceeded, Content: app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Text: "private result"}}},
	}}}
	service := New(st, runtime, nil).WithChannelEnabled(func(string) bool { return true })
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	_ = bindingFromRPCResult(t, dispatchRPC(t, service, peer, "mcp", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}))
	_ = dispatchRPC(t, service, peer, "mcp", "", "tools/list", map[string]any{})
	_ = dispatchRPC(t, service, peer, "mcp", "audit-idempotency-secret", "tools/call", map[string]any{
		"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "private argument"},
	})
	raw, err := json.Marshal(st.ListAudit(""))
	if err != nil {
		t.Fatal(err)
	}
	audit := string(raw)
	for _, sensitive := range []string{issued.Secret, "private argument", "private result", "audit-idempotency-secret"} {
		if strings.Contains(audit, sensitive) {
			t.Fatalf("MCP audit leaked sensitive value %q: %s", sensitive, audit)
		}
	}
	for _, required := range []string{"mcp.access_ticket.issued", "mcp.binding.activated", "mcp.tools.listed", "mcp.operation.created", "mcp.operation.result_recorded"} {
		if !strings.Contains(audit, required) {
			t.Fatalf("MCP audit omitted %q: %s", required, audit)
		}
	}
}

func TestServiceRejectsDeviceSubstitutionAndRevocation(t *testing.T) {
	st := store.NewMemoryStore()
	service := New(st, &fakeRuntime{}, nil).WithChannelEnabled(func(string) bool { return true })
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	binding := bindingFromRPCResult(t, dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}))
	substitute := peer
	substitute.DeviceID = "device-b"
	if response := dispatchRPC(t, service, substitute, "", "", "tools/list", map[string]any{}); response.Error == nil {
		t.Fatal("device substitution listed tools")
	}
	if _, err := st.RevokeMCPBinding(binding.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if response := dispatchRPC(t, service, peer, "", "", "tools/list", map[string]any{}); response.Error == nil {
		t.Fatal("revoked binding listed tools")
	}
}

func TestServiceBindingRevocationCancelsExecutionAndRejectsApproval(t *testing.T) {
	st := store.NewMemoryStore()
	runtime := &fakeRuntime{block: make(chan struct{})}
	service := New(st, runtime, nil).WithChannelEnabled(func(string) bool { return true })
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer, AllowApproval: true}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	binding := bindingFromRPCResult(t, dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}))
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_ = dispatchRPC(t, service, peer, "mcp", "revoke-running", "tools/call", map[string]any{
			"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "wait"},
		})
	}()
	running := waitForOperation(t, st, binding.ID, "revoke-running")
	approvalOperation, _, err := st.CreateMCPOperation(app.MCPOperation{
		BindingID: binding.ID, IdempotencyKey: "revoke-approval", Fingerprint: "revoke-approval",
		Invocation: app.MCPInvocationContext{RunID: "run-revoke-approval"}, State: app.MCPOperationApprovalRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	st.SaveRun(app.AgentRun{ID: "run-revoke-approval", State: "approval_pending"})
	st.SaveToolCall(app.ToolCall{ID: "call-revoke-approval", RunID: "run-revoke-approval", Status: "approval_pending"})
	st.SaveApproval(app.Approval{ID: "approval-revoke-approval", RunID: "run-revoke-approval", ToolCallID: "call-revoke-approval", Status: "pending"})
	if _, err := service.RevokeBinding(binding.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("binding revocation did not cancel running execution")
	}
	storedRunning, _ := st.GetMCPOperation(running.ID)
	storedApproval, _ := st.GetMCPOperation(approvalOperation.ID)
	approval, _ := st.GetApproval("approval-revoke-approval")
	call, _ := st.GetToolCall("call-revoke-approval")
	run, _ := st.GetRun("run-revoke-approval")
	if storedRunning.State != app.MCPOperationRevoked || storedApproval.State != app.MCPOperationRevoked ||
		approval.Status != "rejected" || call.Status != "rejected" || run.State != "blocked" {
		t.Fatalf("revocation lifecycle incomplete: running=%#v approval_operation=%#v approval=%#v call=%#v run=%#v", storedRunning, storedApproval, approval, call, run)
	}
}

func TestServiceDoesNotConsumeTicketWhileConnectorDisabled(t *testing.T) {
	st := store.NewMemoryStore()
	enabled := true
	service := New(st, &fakeRuntime{}, nil).WithChannelEnabled(func(string) bool { return enabled })
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	enabled = false
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	if response := dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}); response.Error == nil {
		t.Fatal("disabled connector redeemed ticket")
	}
	ticket, _ := st.GetMCPAccessTicket(issued.Ticket.ID)
	if ticket.Status != app.MCPAccessPending || ticket.UseCount != 0 {
		t.Fatalf("disabled redemption consumed ticket: %#v", ticket)
	}
}

func TestServiceHidesOnlyStaleGrantedLeaf(t *testing.T) {
	st := store.NewMemoryStore()
	runtime := &fakeRuntime{}
	service := New(st, runtime, nil).WithChannelEnabled(func(string) bool { return true })
	now := time.Now().UTC()
	secret := "stale-grant-secret"
	secretDigest := sha256.Sum256([]byte(secret))
	if _, err := st.SaveMCPAccessTicket(app.MCPAccessTicket{
		SecretHash: hex.EncodeToString(secretDigest[:]), OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		DomainID: "domain-a", Status: app.MCPAccessPending, MaxUses: 1, IssuedAt: now, ExpiresAt: now.Add(time.Minute),
		Grants: []app.MCPLeafGrant{
			{
				CapabilityID: app.CapabilityConversationAnswer, Operations: []app.RouteOperation{app.RouteOperationAnswer},
				Effects: []app.ToolEffect{app.ToolEffectLocalCompute}, Workflow: app.WorkflowContractRef{ID: app.WorkflowConversationAnswer, Revision: 2},
				ProjectionRevision: 1, Status: app.MCPLeafGrantActive,
			},
			{
				CapabilityID: app.CapabilityBrowserWeather, Operations: []app.RouteOperation{app.RouteOperationRead},
				Effects: []app.ToolEffect{app.ToolEffectExternalRead}, Workflow: app.WorkflowContractRef{ID: app.WorkflowBrowserWeather, Revision: 1},
				ProjectionRevision: 999, Status: app.MCPLeafGrantStale,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	_ = bindingFromRPCResult(t, dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": secret}))
	tools := toolsFromRPCResult(t, dispatchRPC(t, service, peer, "mcp", "", "tools/list", map[string]any{}))
	if !toolListed(tools, "sparkclaw.route.conversation.answer") || toolListed(tools, "sparkclaw.route.browser.weather") || !toolListed(tools, "sparkclaw.operation.get") {
		t.Fatalf("stale grant filtering changed unrelated tools: %#v", tools)
	}
}

func TestApprovalLifecycleUpdatesSameDurableOperation(t *testing.T) {
	st := store.NewMemoryStore()
	service := New(st, &fakeRuntime{}, nil).WithChannelEnabled(func(string) bool { return true })
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{
		DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer, AllowApproval: true}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	binding := bindingFromRPCResult(t, dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}))
	enabled := func(_, channel string) bool { return channel == "mcp" }
	endpoints := messagecontrol.NewEndpointRegistry(st).WithChannelEnabled(enabled)
	providers := delivery.NewProviderRegistry()
	if err := providers.Register(NewProvider(st)); err != nil {
		t.Fatal(err)
	}
	deliverer := delivery.NewWorkflowResultDeliverer(
		messagecontrol.NewReturnRouteResolver(endpoints), delivery.NewGateway(endpoints, providers, nil),
	)

	createOperation := func(id, runID string) (app.MCPOperation, app.MCPInvocationRef) {
		t.Helper()
		ref := app.MCPInvocationRef{
			InvocationID: "invocation-" + id, OperationID: id, BindingRef: binding.ID,
			BindingRevision: binding.AuthorizationRevision, RequesterDeviceID: binding.RequesterDeviceID,
		}
		operation, created, err := st.CreateMCPOperation(app.MCPOperation{
			ID: id, BindingID: binding.ID, IdempotencyKey: "idem-" + id, Fingerprint: "fingerprint-" + id,
			Invocation: app.MCPInvocationContext{
				ID: ref.InvocationID, OperationID: id, BindingRef: binding.ID, BindingRevision: binding.AuthorizationRevision,
				RequesterDeviceID: binding.RequesterDeviceID, OwnerID: binding.OwnerID, ActorID: binding.ActorID,
				RunID: runID, AllowApproval: true,
			},
			State: app.MCPOperationRunning,
		})
		if err != nil || !created {
			t.Fatalf("create operation %q: created=%v err=%v", id, created, err)
		}
		return operation, ref
	}
	result := func(id, runID string, ref app.MCPInvocationRef, status app.WorkflowResultStatus, text string) app.WorkflowResult {
		return app.WorkflowResult{
			SchemaVersion: app.WorkflowResultSchemaVersion, ID: id, RunID: runID,
			OwnerID: binding.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: binding.ActorID}, Status: status,
			Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: text}}},
			ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: app.EndpointID("mcp:" + binding.ID)}, MCP: &ref,
		}
	}

	operation, ref := createOperation("operation-approved", "run-approved")
	st.SaveRun(app.AgentRun{ID: "run-approved", SessionID: binding.LinkedSessionID, State: "approval_pending"})
	if _, err := deliverer.DeliverWorkflowResult(t.Context(), result("waiting-result", "run-approved", ref, app.WorkflowResultWaiting, "approval required")); err != nil {
		t.Fatal(err)
	}
	waiting, _ := st.GetMCPOperation(operation.ID)
	if waiting.State != app.MCPOperationApprovalRequired || waiting.CompletedAt != nil {
		t.Fatalf("waiting result did not park the operation: %#v", waiting)
	}
	st.SaveRun(app.AgentRun{ID: "run-approved", SessionID: binding.LinkedSessionID, State: "completed"})
	if _, err := deliverer.DeliverWorkflowResult(t.Context(), result("completed-result", "run-approved", ref, app.WorkflowResultSucceeded, "approved result")); err != nil {
		t.Fatal(err)
	}

	restarted := New(st, &fakeRuntime{}, nil).WithChannelEnabled(func(string) bool { return true })
	completed := operationFromRPCResult(t, dispatchRPC(t, restarted, peer, "mcp", "", "tools/call", map[string]any{
		"name": "sparkclaw.operation.result", "arguments": map[string]any{"operation_id": operation.ID},
	}))
	if completed.ID != operation.ID || completed.State != app.MCPOperationSucceeded || completed.CompletedAt == nil {
		t.Fatalf("approved operation did not survive service restart: %#v", completed)
	}

	rejectedOperation, rejectedRef := createOperation("operation-rejected", "run-rejected")
	st.SaveRun(app.AgentRun{ID: "run-rejected", SessionID: binding.LinkedSessionID, State: "approval_pending"})
	st.SaveApproval(app.Approval{ID: "approval-rejected", SessionID: binding.LinkedSessionID, RunID: "run-rejected", Status: "pending", Tool: "test.write"})
	if _, err := deliverer.DeliverWorkflowResult(t.Context(), result("rejected-waiting-result", "run-rejected", rejectedRef, app.WorkflowResultWaiting, "approval required")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveApproval("approval-rejected", "rejected", "owner rejected"); err != nil {
		t.Fatal(err)
	}
	rejected := operationFromRPCResult(t, dispatchRPC(t, restarted, peer, "mcp", "", "tools/call", map[string]any{
		"name": "sparkclaw.operation.result", "arguments": map[string]any{"operation_id": rejectedOperation.ID},
	}))
	if rejected.State != app.MCPOperationFailed || rejected.ErrorCode != "approval_rejected" || rejected.CompletedAt == nil {
		t.Fatalf("rejected approval did not fail the operation: %#v", rejected)
	}
}

func TestServiceOperationCancelStopsRunningInvocation(t *testing.T) {
	st := store.NewMemoryStore()
	runtime := &fakeRuntime{block: make(chan struct{})}
	service := New(st, runtime, nil).WithChannelEnabled(func(string) bool { return true })
	issued, err := service.IssueTicket(app.DefaultOwnerID, app.DefaultOwnerID, IssueTicketRequest{DomainID: "domain-a", Grants: []RequestedGrant{{CapabilityID: app.CapabilityConversationAnswer}}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	_ = bindingFromRPCResult(t, dispatchRPC(t, service, peer, "", "", "sparkclaw/access/redeem", map[string]any{"ticket": issued.Secret}))

	rpcRaw, _ := json.Marshal(JSONRPCRequest{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`2`), Method: "tools/call", Params: mustJSON(map[string]any{
		"name": "sparkclaw.route.conversation.answer", "arguments": map[string]any{"query": "wait"},
	})})
	callDone := make(chan JSONRPCResponse, 1)
	go func() {
		response, _ := service.Dispatch(context.Background(), PeerRequest{Peer: peer, Request: TransportRequest{
			ProtocolVersion: TransportProtocolVersion, Type: TransportTypeRequest, SessionID: "mcp", IdempotencyKey: "cancel-me",
			Deadline: time.Now().Add(time.Minute), JSONRPC: rpcRaw,
		}})
		var rpc JSONRPCResponse
		_ = json.Unmarshal(response.JSONRPC, &rpc)
		callDone <- rpc
	}()
	binding := bindingForPeer(t, st, peer)
	var operation app.MCPOperation
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if found, ok := st.FindMCPOperationByIdempotency(binding.ID, "cancel-me"); ok {
			operation = found
			break
		}
		time.Sleep(time.Millisecond)
	}
	if operation.ID == "" {
		t.Fatal("running operation was not persisted")
	}
	cancelled := dispatchRPC(t, service, peer, "mcp", "", "tools/call", map[string]any{
		"name": "sparkclaw.operation.cancel", "arguments": map[string]any{"operation_id": operation.ID},
	})
	if got := operationFromRPCResult(t, cancelled); got.State != app.MCPOperationCancelled {
		t.Fatalf("cancel result = %#v", got)
	}
	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop invocation")
	}
	stored, _ := st.GetMCPOperation(operation.ID)
	if stored.State != app.MCPOperationCancelled {
		t.Fatalf("cancel was overwritten: %#v", stored)
	}
}

func dispatchRPC(t *testing.T, service *Service, peer app.MCPPeerIdentity, sessionID, idempotencyKey, method string, params any) JSONRPCResponse {
	t.Helper()
	paramsRaw, _ := json.Marshal(params)
	rpcRaw, _ := json.Marshal(JSONRPCRequest{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`), Method: method, Params: paramsRaw})
	response, err := service.Dispatch(t.Context(), PeerRequest{Peer: peer, Request: TransportRequest{
		ProtocolVersion: TransportProtocolVersion, Type: TransportTypeRequest, SessionID: sessionID,
		IdempotencyKey: idempotencyKey, Deadline: time.Now().Add(time.Minute), JSONRPC: rpcRaw,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var rpc JSONRPCResponse
	if err := json.Unmarshal(response.JSONRPC, &rpc); err != nil {
		t.Fatal(err)
	}
	return rpc
}

func bindingFromRPCResult(t *testing.T, response JSONRPCResponse) app.MCPBinding {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("RPC failed: %#v", response.Error)
	}
	raw, _ := json.Marshal(response.Result)
	var result struct {
		Binding app.MCPBinding `json:"binding"`
	}
	_ = json.Unmarshal(raw, &result)
	return result.Binding
}
func operationFromRPCResult(t *testing.T, response JSONRPCResponse) app.MCPOperation {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("RPC failed: %#v", response.Error)
	}
	raw, _ := json.Marshal(response.Result)
	var result struct {
		Content           []CallToolContent `json:"content"`
		StructuredContent struct {
			Operation app.MCPOperation `json:"operation"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("tools/call did not return MCP CallToolResult content: %s", raw)
	}
	var textResult struct {
		Operation app.MCPOperation `json:"operation"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &textResult); err != nil || textResult.Operation.ID != result.StructuredContent.Operation.ID {
		t.Fatalf("tools/call content and structuredContent disagree: %s err=%v", raw, err)
	}
	return result.StructuredContent.Operation
}
func toolsFromRPCResult(t *testing.T, response JSONRPCResponse) []Tool {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("RPC failed: %#v", response.Error)
	}
	raw, _ := json.Marshal(response.Result)
	var result struct {
		Tools []Tool `json:"tools"`
	}
	_ = json.Unmarshal(raw, &result)
	return result.Tools
}
func toolListed(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func mustJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func bindingForPeer(t *testing.T, st store.Store, peer app.MCPPeerIdentity) app.MCPBinding {
	t.Helper()
	binding, ok := st.FindMCPBindingForPeer(peer.DomainID, peer.DeviceID, peer.KeyThumbprint)
	if !ok {
		t.Fatal("binding not found")
	}
	return binding
}

func waitForOperation(t *testing.T, st store.Store, bindingID, idempotencyKey string) app.MCPOperation {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if operation, ok := st.FindMCPOperationByIdempotency(bindingID, idempotencyKey); ok {
			return operation
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("operation %q was not persisted", idempotencyKey)
	return app.MCPOperation{}
}
