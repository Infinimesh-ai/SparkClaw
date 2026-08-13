package iscpbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/payload"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
)

func TestServiceSessionHandshakeAndEncryptedGatewayRequest(t *testing.T) {
	now := time.Now().UTC()
	provider := iscpcrypto.NewProvider()
	root, _ := identity.NewDevice(provider, "domain-test", "trust-root", now)
	bridgeDevice, _ := identity.NewDevice(provider, "domain-test", "bridge-device", now)
	peerDevice, _ := identity.NewDevice(provider, "domain-test", "app-device", now)
	bridgeThumbprint, _ := identity.Thumbprint(bridgeDevice.Identity)
	peerThumbprint, _ := identity.Thumbprint(peerDevice.Identity)
	inbound, _ := trust.SignGrant(provider, root, trust.Grant{
		GrantID: "grant-peer", SubjectDeviceID: peerDevice.Identity.DeviceID, Audience: bridgeDevice.Identity.DeviceID,
		ConfirmationThumbprint: peerThumbprint, Permissions: []string{"agent.bridge"}, RelayConstraints: []string{"relay-test"},
		NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	})
	outbound, _ := trust.SignGrant(provider, root, trust.Grant{
		GrantID: "grant-bridge", SubjectDeviceID: bridgeDevice.Identity.DeviceID, Audience: peerDevice.Identity.DeviceID,
		ConfirmationThumbprint: bridgeThumbprint, Permissions: []string{"agent.bridge"}, RelayConstraints: []string{"relay-test"},
		NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	})

	delivered := make(chan json.RawMessage, 8)
	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode submitted envelope: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		delivered <- append(json.RawMessage(nil), raw...)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer relayServer.Close()

	bundle := EnrollmentBundle{
		Type: EnrollmentBundleType, DomainID: "domain-test", DeviceID: bridgeDevice.Identity.DeviceID,
		RelayID: "relay-test", RelayBaseURL: relayServer.URL, RelayWebSocketURL: "ws" + strings.TrimPrefix(relayServer.URL, "http") + "/v2/relay/connect",
		TrustRootIdentity: root.Identity,
		Access:            RelayCredential{DomainID: "domain-test", DeviceID: bridgeDevice.Identity.DeviceID, Token: "access", ExpiresAt: now.Add(time.Hour)},
		Refresh:           RelayCredential{DomainID: "domain-test", DeviceID: bridgeDevice.Identity.DeviceID, Token: "refresh", ExpiresAt: now.Add(time.Hour)},
		Peers:             []PeerAuthorization{{Identity: peerDevice.Identity, InboundGrant: inbound, OutboundGrant: outbound}},
		IssuedAt:          now, ExpiresAt: now.Add(time.Hour),
	}
	relay, err := NewRelayClient(ProfileLocalLab, t.TempDir()+"/enrollment.json", bundle, bridgeDevice, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	mcpRequests := make(chan mcpaccess.PeerRequest, 1)
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/bridge/v1/mcp/dispatch" {
			var request mcpaccess.PeerRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode MCP request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mcpRequests <- request
			rpc, _ := json.Marshal(mcpaccess.JSONRPCResponse{
				JSONRPC: mcpaccess.JSONRPCVersion, ID: json.RawMessage(`7`), Result: map[string]any{},
			})
			_ = json.NewEncoder(w).Encode(mcpaccess.TransportResponse{
				ProtocolVersion: mcpaccess.TransportProtocolVersion, Type: mcpaccess.TransportTypeResponse,
				SessionID: request.Request.SessionID, JSONRPC: rpc,
			})
			return
		}
		var request Request
		_ = json.NewDecoder(r.Body).Decode(&request)
		result := any(map[string]any{"sessions": []any{}})
		if request.Type == TypeCapabilitiesDescribe {
			result = DefaultManifest()
		}
		_ = json.NewEncoder(w).Encode(newResponse(request, "ok", result, nil, nil, time.Now().UTC()))
	}))
	defer gatewayServer.Close()
	gateway, err := NewGatewayClient(GatewayClientOptions{BaseURL: gatewayServer.URL, Token: "gateway", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Permission: "agent.bridge", Relay: RelaySettings{EnvelopeTTLSeconds: 300, EventPollMilliseconds: 500}}
	service, err := NewService(config, gateway, relay, bridgeDevice)
	if err != nil {
		t.Fatal(err)
	}

	peerHello, err := session.CreateHello(provider, peerDevice, "iscp-session-1", bridgeDevice.Identity.DeviceID, inbound.GrantID, now)
	if err != nil {
		t.Fatal(err)
	}
	helloRaw, _ := json.Marshal(peerHello.Hello)
	helloFrame, _ := json.Marshal(relayFrame{
		Type: wireFrameType, DomainID: bundle.DomainID, MessageID: "hello-1",
		SenderDeviceID: peerDevice.Identity.DeviceID, RecipientDeviceID: bridgeDevice.Identity.DeviceID,
		PayloadType: session.TypeHello, Route: envelope.Route{RelayID: bundle.RelayID, TTLSeconds: 300, Priority: 5}, Payload: helloRaw,
	})
	if err := service.handleRelayMessage(t.Context(), helloFrame); err != nil {
		t.Fatalf("accept hello: %v", err)
	}
	localHelloFrame := decodeSubmittedFrame(t, <-delivered)
	localReadyFrame := decodeSubmittedFrame(t, <-delivered)
	var localHello session.Hello
	var localReady session.Ready
	if err := json.Unmarshal(localHelloFrame.Payload, &localHello); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(localReadyFrame.Payload, &localReady); err != nil {
		t.Fatal(err)
	}
	peerState, err := session.Establish(provider, peerHello, localHello, peerDevice.Identity, bridgeDevice.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := peerState.VerifyReady(provider, localReady, bridgeDevice.Identity); err != nil {
		t.Fatal(err)
	}
	prematureRequest := validRequest(TypeSessionList, "request-before-ready", peerDevice.Identity.DeviceID, "", "", map[string]any{})
	prematureRaw, _ := json.Marshal(prematureRequest)
	prematureEnvelope, err := envelope.Encrypt(provider, peerState, "premature-envelope", payload.TypeTaskInvoke,
		envelope.Route{RelayID: bundle.RelayID, TTLSeconds: 300, Priority: 5}, prematureRaw)
	if err != nil {
		t.Fatal(err)
	}
	prematureEnvelopeRaw, _ := json.Marshal(prematureEnvelope)
	if err := service.handleRelayMessage(t.Context(), prematureEnvelopeRaw); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("premature SecureEnvelope was not rejected: %v", err)
	}
	peerReady, _ := peerState.CreateReady(provider, peerDevice)
	peerReadyRaw, _ := json.Marshal(peerReady)
	readyFrame, _ := json.Marshal(relayFrame{
		Type: wireFrameType, DomainID: bundle.DomainID, MessageID: "ready-1",
		SenderDeviceID: peerDevice.Identity.DeviceID, RecipientDeviceID: bridgeDevice.Identity.DeviceID,
		PayloadType: session.TypeReady, Route: envelope.Route{RelayID: bundle.RelayID, TTLSeconds: 300, Priority: 5}, Payload: peerReadyRaw,
	})
	if err := service.handleRelayMessage(t.Context(), readyFrame); err != nil {
		t.Fatalf("accept ready: %v", err)
	}
	capabilitiesEnvelope := decodeSubmittedEnvelope(t, <-delivered)
	capabilitiesPlaintext, err := envelope.Decrypt(provider, peerState, capabilitiesEnvelope)
	if err != nil {
		t.Fatalf("decrypt capabilities: %v", err)
	}
	var capabilities Response
	if err := json.Unmarshal(capabilitiesPlaintext, &capabilities); err != nil || capabilities.Status != "ok" {
		t.Fatalf("invalid capabilities response: %s err=%v", capabilitiesPlaintext, err)
	}

	request := validRequest(TypeSessionList, "request-list", peerDevice.Identity.DeviceID, "", "", map[string]any{})
	requestRaw, _ := json.Marshal(request)
	requestEnvelope, err := envelope.Encrypt(provider, peerState, "request-envelope", payload.TypeTaskInvoke,
		envelope.Route{RelayID: bundle.RelayID, TTLSeconds: 300, Priority: 5}, requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	requestEnvelopeRaw, _ := json.Marshal(requestEnvelope)
	if err := service.handleRelayMessage(context.Background(), requestEnvelopeRaw); err != nil {
		t.Fatalf("handle encrypted request: %v", err)
	}
	responseEnvelope := decodeSubmittedEnvelope(t, <-delivered)
	responsePlaintext, err := envelope.Decrypt(provider, peerState, responseEnvelope)
	if err != nil {
		t.Fatalf("decrypt Gateway response: %v", err)
	}
	var response Response
	if err := json.Unmarshal(responsePlaintext, &response); err != nil || response.RequestID != request.RequestID || response.Status != "ok" {
		t.Fatalf("invalid Gateway response: %s err=%v", responsePlaintext, err)
	}

	mcpRPC, _ := json.Marshal(mcpaccess.JSONRPCRequest{
		JSONRPC: mcpaccess.JSONRPCVersion, ID: json.RawMessage(`7`), Method: "ping",
	})
	mcpRequest := mcpaccess.TransportRequest{
		ProtocolVersion: mcpaccess.TransportProtocolVersion, Type: mcpaccess.TransportTypeRequest,
		SessionID: "mcp-session-1", Deadline: time.Now().Add(time.Minute), JSONRPC: mcpRPC,
	}
	mcpRaw, _ := json.Marshal(mcpRequest)
	mcpEnvelope, err := envelope.Encrypt(provider, peerState, "mcp-envelope", payload.TypeTaskInvoke,
		envelope.Route{RelayID: bundle.RelayID, TTLSeconds: 300, Priority: 5}, mcpRaw)
	if err != nil {
		t.Fatal(err)
	}
	mcpEnvelopeRaw, _ := json.Marshal(mcpEnvelope)
	if err := service.handleRelayMessage(context.Background(), mcpEnvelopeRaw); err != nil {
		t.Fatalf("handle encrypted MCP request: %v", err)
	}
	forwarded := <-mcpRequests
	wantPeer := app.MCPPeerIdentity{
		DomainID: bundle.DomainID, DeviceID: peerDevice.Identity.DeviceID,
		KeyThumbprint: peerThumbprint, ISCPSessionID: "iscp-session-1",
	}
	if forwarded.Peer != wantPeer || forwarded.Request.SessionID != mcpRequest.SessionID {
		t.Fatalf("Bridge did not inject authenticated MCP peer identity: got=%#v want=%#v", forwarded, wantPeer)
	}
	mcpResponseEnvelope := decodeSubmittedEnvelope(t, <-delivered)
	mcpResponsePlaintext, err := envelope.Decrypt(provider, peerState, mcpResponseEnvelope)
	if err != nil {
		t.Fatalf("decrypt MCP response: %v", err)
	}
	var mcpResponse mcpaccess.TransportResponse
	if err := json.Unmarshal(mcpResponsePlaintext, &mcpResponse); err != nil ||
		mcpResponse.ProtocolVersion != mcpaccess.TransportProtocolVersion || mcpResponse.SessionID != mcpRequest.SessionID {
		t.Fatalf("invalid encrypted MCP response: %s err=%v", mcpResponsePlaintext, err)
	}
	var responseRPC mcpaccess.JSONRPCResponse
	if err := json.Unmarshal(mcpResponse.JSONRPC, &responseRPC); err != nil || string(responseRPC.ID) != "7" || responseRPC.Error != nil {
		t.Fatalf("invalid MCP JSON-RPC response: %s err=%v", mcpResponse.JSONRPC, err)
	}
}

func decodeSubmittedFrame(t *testing.T, raw json.RawMessage) relayFrame {
	t.Helper()
	var frame relayFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func decodeSubmittedEnvelope(t *testing.T, raw json.RawMessage) envelope.SecureEnvelope {
	t.Helper()
	var secureEnvelope envelope.SecureEnvelope
	if err := json.Unmarshal(raw, &secureEnvelope); err != nil {
		t.Fatal(err)
	}
	return secureEnvelope
}
