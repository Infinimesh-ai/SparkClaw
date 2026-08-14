package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connector"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/provisioning"
)

type gatewayPairingAuthority struct {
	result iscppairing.AuthorityResult
}

func (a gatewayPairingAuthority) Ready(context.Context) error { return nil }
func (a gatewayPairingAuthority) IssuePairingTicket(context.Context, iscppairing.AuthorityRequest) (iscppairing.AuthorityResult, error) {
	return a.result, nil
}

func TestISCPPairingOwnerAPIReturnsTicketOnceAndPersistsReceipt(t *testing.T) {
	now := time.Now().UTC()
	cfg := testConfig(t.TempDir())
	cfg.Gateway.APIToken = "owner-token"
	cfg.MCPAccess.LocalDomainID = "domain-a"
	cfg.ISCPPairing.Enabled = true
	cfg.ISCPPairing.DomainID = "domain-a"
	cfg.ISCPPairing.ExpectedTicketType = provisioning.TypePairingTicket
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	pairing := iscppairing.New(st, iscppairing.Options{
		Enabled: true, DomainID: "domain-a", AuthorityHost: "authority.example", ExpectedTicketType: provisioning.TypePairingTicket,
		Authority: gatewayPairingAuthority{result: iscppairing.AuthorityResult{AuthorityRef: "authority-ref", Ticket: provisioning.PairingTicket{
			Type: provisioning.TypePairingTicket, TicketID: "pairing-ticket", DomainID: "domain-a", RelayID: "relay-a", TrustRootID: "root-a", MaxUses: 1,
			IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute), Signature: identity.Signature{Alg: "Ed25519", KID: "root-key", Value: "copy-once-signature"},
		}}},
	})
	registry := connector.NewRegistry(cfg, st)
	if err := registry.Register(connector.Registration{
		Channel: "mcp", SetupKind: app.ConnectorSetupExternal, Binding: mcpaccess.ConnectorAdapter{},
		Provider: mcpaccess.NewProvider(st), ExternalManaged: true,
	}); err != nil {
		t.Fatal(err)
	}
	server := New(cfg, st, tools, runtime, WithISCPPairing(pairing), WithConnectorController(registry))

	status := ownerRequest(t, server.Handler(), http.MethodGet, "/api/iscp-pairing/status", "")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"ready":true`) || strings.Contains(status.Body.String(), "token") {
		t.Fatalf("invalid pairing readiness: status=%d body=%s", status.Code, status.Body.String())
	}
	disabled := ownerRequest(t, server.Handler(), http.MethodPost, "/api/iscp-pairing/start", `{"display_name":"LocalMind gateway"}`)
	if disabled.Code != http.StatusConflict || !strings.Contains(disabled.Body.String(), "disabled") {
		t.Fatalf("disabled MCP connector started pairing: status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if _, err := registry.SetEnabled(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, "mcp", true, 0); err != nil {
		t.Fatal(err)
	}
	updated := ownerRequest(t, server.Handler(), http.MethodPatch, "/api/mcp-access/transports", `{"iscp_enabled":true,"lan_access_enabled":false,"expected_version":1}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"iscp_enabled":true`) || !strings.Contains(updated.Body.String(), `"version":2`) {
		t.Fatalf("ISCP transport update failed: status=%d body=%s", updated.Code, updated.Body.String())
	}
	stale := ownerRequest(t, server.Handler(), http.MethodPatch, "/api/mcp-access/transports", `{"iscp_enabled":false,"lan_access_enabled":true,"expected_version":1}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale MCP transport update returned %d: %s", stale.Code, stale.Body.String())
	}
	created := ownerRequest(t, server.Handler(), http.MethodPost, "/api/iscp-pairing/start", `{"display_name":"LocalMind gateway"}`)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), "copy-once-signature") || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pairing ticket was not returned copy-once: status=%d headers=%v body=%s", created.Code, created.Header(), created.Body.String())
	}
	listed := ownerRequest(t, server.Handler(), http.MethodGet, "/api/iscp-pairing/onboardings", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "pairing-ticket") || strings.Contains(listed.Body.String(), "copy-once-signature") || strings.Contains(listed.Body.String(), `"signature"`) {
		t.Fatalf("onboarding list leaked or omitted receipt data: status=%d body=%s", listed.Code, listed.Body.String())
	}
	audits, _ := json.Marshal(st.ListAudit(""))
	if strings.Contains(string(audits), "copy-once-signature") {
		t.Fatalf("audit leaked Pairing Ticket: %s", audits)
	}
}

func TestMCPAccessAPIRequiresConnectorOptInAndReturnsSecretOnce(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Gateway.APIToken = "owner-token"
	cfg.MCPAccess.LocalDomainID = "domain-a"
	st := store.NewMemoryStore()
	registry := connector.NewRegistry(cfg, st)
	if err := registry.Register(connector.Registration{
		Channel: "mcp", SetupKind: app.ConnectorSetupExternal, Binding: mcpaccess.ConnectorAdapter{},
		Provider: mcpaccess.NewProvider(st), ExternalManaged: true,
	}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime, WithConnectorController(registry))

	ticketBody := `{"domain_id":"domain-a"}`
	disabled := ownerRequest(t, server.Handler(), http.MethodPost, "/api/mcp-access/tickets", ticketBody)
	if disabled.Code != http.StatusBadRequest || !strings.Contains(disabled.Body.String(), "disabled") || len(st.ListMCPAccessTickets("")) != 0 {
		t.Fatalf("disabled MCP connector issued a ticket: status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if _, err := registry.SetEnabled(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, "mcp", true, 0); err != nil {
		t.Fatal(err)
	}
	created := ownerRequest(t, server.Handler(), http.MethodPost, "/api/mcp-access/tickets", ticketBody)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "secret_hash") || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("ticket response leaked its hash: status=%d body=%s", created.Code, created.Body.String())
	}
	var issued mcpaccess.IssuedTicket
	if err := json.Unmarshal(created.Body.Bytes(), &issued); err != nil || issued.Secret == "" || issued.Ticket.ID == "" {
		t.Fatalf("invalid issued ticket: %#v err=%v", issued, err)
	}
	stored, ok := st.GetMCPAccessTicket(issued.Ticket.ID)
	if !ok || stored.SecretHash == "" || stored.SecretHash == issued.Secret {
		t.Fatalf("ticket was not stored hash-only: %#v ok=%v", stored, ok)
	}

	listed := ownerRequest(t, server.Handler(), http.MethodGet, "/api/mcp-access/tickets", "")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), issued.Secret) || strings.Contains(listed.Body.String(), "secret_hash") || strings.Contains(listed.Body.String(), `"secret"`) {
		t.Fatalf("ticket list exposed secret material: status=%d body=%s", listed.Code, listed.Body.String())
	}

	rpc, _ := json.Marshal(mcpaccess.JSONRPCRequest{JSONRPC: mcpaccess.JSONRPCVersion, ID: json.RawMessage(`1`), Method: "ping"})
	bridgeBody, _ := json.Marshal(mcpaccess.PeerRequest{
		Peer: app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"},
		Request: mcpaccess.TransportRequest{
			ProtocolVersion: mcpaccess.TransportProtocolVersion, Type: mcpaccess.TransportTypeRequest, SessionID: "mcp-a", JSONRPC: rpc,
		},
	})
	bridge := httptest.NewRequest(http.MethodPost, "/api/bridge/v1/mcp/dispatch", bytes.NewReader(bridgeBody))
	bridge.RemoteAddr = "127.0.0.1:44000"
	bridge.Header.Set("Authorization", "Bearer owner-token")
	bridgeResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(bridgeResponse, bridge)
	if bridgeResponse.Code != http.StatusForbidden || !strings.Contains(bridgeResponse.Body.String(), "ISCP is disabled") {
		t.Fatalf("disabled ISCP transport accepted bridge request: status=%d body=%s", bridgeResponse.Code, bridgeResponse.Body.String())
	}
	if _, err := registry.SetMCPTransports(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, true, false, 1); err != nil {
		t.Fatal(err)
	}
	bridge = httptest.NewRequest(http.MethodPost, "/api/bridge/v1/mcp/dispatch", bytes.NewReader(bridgeBody))
	bridge.RemoteAddr = "127.0.0.1:44000"
	bridge.Header.Set("Authorization", "Bearer owner-token")
	bridgeResponse = httptest.NewRecorder()
	server.Handler().ServeHTTP(bridgeResponse, bridge)
	if bridgeResponse.Code != http.StatusOK {
		t.Fatalf("authenticated local MCP Bridge returned %d: %s", bridgeResponse.Code, bridgeResponse.Body.String())
	}
	var transport mcpaccess.TransportResponse
	if err := json.Unmarshal(bridgeResponse.Body.Bytes(), &transport); err != nil || transport.ProtocolVersion != mcpaccess.TransportProtocolVersion {
		t.Fatalf("invalid MCP Bridge response: %#v err=%v", transport, err)
	}
}

func TestGatewayPortMCPConsumesTicketAndContinuesWithSession(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Gateway.APIToken = "owner-token"
	cfg.MCPAccess.LocalDomainID = "sparkclaw-local"
	st := store.NewMemoryStore()
	registry := connector.NewRegistry(cfg, st)
	if err := registry.Register(connector.Registration{
		Channel: "mcp", SetupKind: app.ConnectorSetupExternal, Binding: mcpaccess.ConnectorAdapter{},
		Provider: mcpaccess.NewProvider(st), ExternalManaged: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetEnabled(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, "mcp", true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetMCPTransports(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, false, true, 1); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime, WithConnectorController(registry))

	catalog := ownerRequest(t, server.Handler(), http.MethodGet, "/api/mcp-access/catalog", "")
	if catalog.Code != http.StatusOK || !strings.Contains(catalog.Body.String(), `"lan_access_enabled":true`) ||
		!strings.Contains(catalog.Body.String(), `"domain_id":"sparkclaw-local"`) || !strings.Contains(catalog.Body.String(), `"endpoint_path":"/mcp"`) {
		t.Fatalf("MCP catalog omitted transport details: status=%d body=%s", catalog.Code, catalog.Body.String())
	}
	created := ownerRequest(t, server.Handler(), http.MethodPost, "/api/mcp-access/tickets", `{"domain_id":"sparkclaw-local"}`)
	var issued mcpaccess.IssuedTicket
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &issued) != nil || issued.Secret == "" {
		t.Fatalf("LAN MCP ticket was not issued: status=%d body=%s", created.Code, created.Body.String())
	}

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"localmind-test","version":"1"}}}`
	initResponse := directMCPRequest(server.Handler(), initialize, issued.Secret, "")
	sessionID := initResponse.Header().Get(mcpSessionHeader)
	if initResponse.Code != http.StatusOK || sessionID == "" || sessionID == issued.Secret || !strings.Contains(initResponse.Body.String(), `"name":"sparkclaw-conversation-mcp"`) {
		t.Fatalf("LAN MCP initialize failed: status=%d headers=%v body=%s", initResponse.Code, initResponse.Header(), initResponse.Body.String())
	}
	stored, ok := st.GetMCPAccessTicket(issued.Ticket.ID)
	if !ok || stored.Status != app.MCPAccessConsumed || stored.UseCount != 1 {
		t.Fatalf("LAN MCP ticket was not consumed exactly once: %#v", stored)
	}
	bindings := st.ListMCPBindings(app.DefaultOwnerID)
	if len(bindings) != 1 || bindings[0].DomainID != "sparkclaw-local" || bindings[0].RequesterDeviceID == "" {
		t.Fatalf("LAN MCP binding was not activated: %#v", bindings)
	}

	initialized := directMCPRequest(server.Handler(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`, issued.Secret, sessionID)
	if initialized.Code != http.StatusAccepted {
		t.Fatalf("LAN MCP initialized notification failed: status=%d body=%s", initialized.Code, initialized.Body.String())
	}
	listed := directMCPRequest(server.Handler(), `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, issued.Secret, sessionID)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "sparkclaw.conversation.send") || !strings.Contains(listed.Body.String(), "sparkclaw.operation.get") {
		t.Fatalf("LAN MCP tools/list failed: status=%d body=%s", listed.Code, listed.Body.String())
	}

	replayed := directMCPRequest(server.Handler(), initialize, issued.Secret, "")
	if replayed.Code != http.StatusUnauthorized || replayed.Header().Get(mcpSessionHeader) != "" {
		t.Fatalf("LAN MCP ticket replay was accepted: status=%d headers=%v body=%s", replayed.Code, replayed.Header(), replayed.Body.String())
	}
	unknown := directMCPRequest(server.Handler(), `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`, "", "unknown-session")
	if unknown.Code != http.StatusUnauthorized {
		t.Fatalf("unknown LAN MCP session was accepted: status=%d body=%s", unknown.Code, unknown.Body.String())
	}
	listedTickets, _ := json.Marshal(st.ListMCPAccessTickets(""))
	listedBindings, _ := json.Marshal(st.ListMCPBindings(""))
	if strings.Contains(string(listedTickets), issued.Secret) || strings.Contains(string(listedBindings), sessionID) {
		t.Fatal("LAN MCP persisted a plaintext ticket or session credential")
	}
}

func TestGatewayPortMCPIsAbsentWhenLANAccessDisabled(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Gateway.APIToken = "owner-token"
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	response := directMCPRequest(server.Handler(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled LAN MCP endpoint returned %d: %s", response.Code, response.Body.String())
	}
}

func directMCPRequest(handler http.Handler, body, bearer, sessionID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.RemoteAddr = "192.168.20.10:44000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(mcpProtocolHeader, mcpaccess.MCPProtocolVersion)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if sessionID != "" {
		request.Header.Set(mcpSessionHeader, sessionID)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestValidateMCPApprovalRequiresLiveOperation(t *testing.T) {
	st := store.NewMemoryStore()
	server := &Server{store: st}
	ref := &app.MCPInvocationRef{OperationID: "operation-approval"}
	st.SaveRun(app.AgentRun{ID: "run-approval", MessageContext: &app.MessageRunContext{MCP: ref}})
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{
		ID: ref.OperationID, BindingID: "binding-a", IdempotencyKey: "approval", Fingerprint: "approval",
		Invocation: app.MCPInvocationContext{RunID: "run-approval"}, State: app.MCPOperationApprovalRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval := app.Approval{RunID: "run-approval"}
	if err := server.validateMCPApproval(approval); err != nil {
		t.Fatalf("live local approval was rejected: %v", err)
	}
	operation.State = app.MCPOperationCancelled
	if _, err := st.UpdateMCPOperation(operation, operation.Version); err != nil {
		t.Fatal(err)
	}
	if err := server.validateMCPApproval(approval); err == nil {
		t.Fatal("approval succeeded after the MCP operation became terminal")
	}
}

func ownerRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:44000"
	request.Header.Set("Authorization", "Bearer owner-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
