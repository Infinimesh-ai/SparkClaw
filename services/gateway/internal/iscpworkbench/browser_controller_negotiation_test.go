package iscpworkbench

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func preControllerOperations(ops []string) []string {
	return slices.DeleteFunc(slices.Clone(ops), func(name string) bool { return strings.HasPrefix(name, "browser.controller.") })
}

func TestBrowserControllerNegotiationAcceptsExactPrecedingTable(t *testing.T) {
	for _, mode := range []string{"local-test", "node-managed"} {
		ops := preControllerOperations(OperationsV2ForMode(mode))
		if len(OperationsV2ForMode(mode))-len(ops) != 5 {
			t.Fatal("Controller extension must contain exactly five operations")
		}
		caps := TransportCapabilities{SchemaVersion: 2, StreamSchemaVersion: 1, Profile: ProfileV2,
			SessionID: "session", Revision: "2", AuthorizationRevision: 1, ExpiresAt: time.Now().Add(time.Minute),
			Operations: ops, QualifiedOperations: slices.Clone(ops),
			Limits: TransportLimits{MaxRequestBytes, MaxResponseBytes, MaxBodyBytes, 0, 8192, 2}}
		if err := validateNegotiation(caps, "session", mode); err != nil {
			t.Fatalf("preceding %s peer must retain unrelated features: %v", mode, err)
		}
		invalid := caps
		invalid.Operations = slices.DeleteFunc(slices.Clone(ops), func(name string) bool { return name == OperationSettingsOwnerGet })
		if validateNegotiation(invalid, "session", mode) == nil {
			t.Fatal("arbitrary missing mandatory operation accepted")
		}
		invalid = caps
		invalid.QualifiedOperations = append(slices.Clone(ops), OperationBrowserControllerExecute)
		if validateNegotiation(invalid, "session", mode) == nil {
			t.Fatal("unadvertised Controller operation was qualified")
		}
		invalid = caps
		invalid.Operations = append(slices.Clone(ops), OperationBrowserControllerStatus)
		if validateNegotiation(invalid, "session", mode) == nil {
			t.Fatal("partial Controller extension accepted")
		}
	}
}

func TestBrowserControllerInitiatorRetriesStrictPrecedingPeerWithoutOtherDowngrades(t *testing.T) {
	calls := 0
	response, err := requestRPCNegotiation(t.Context(), func(_ context.Context, request Request) (Response, error) {
		calls++
		var old struct {
			Negotiation struct {
				Profiles  []string `json:"profiles"`
				Unbounded bool     `json:"unbounded_rpc,omitempty"`
				Progress  bool     `json:"execution_progress,omitempty"`
				Stream    int      `json:"stream_schema_version,omitempty"`
				Settings  bool     `json:"connection_settings,omitempty"`
			} `json:"transport_negotiation"`
		}
		if strictDecode(request.Body, &old) != nil {
			return Response{Status: 400}, nil
		}
		if !old.Negotiation.Unbounded || !old.Negotiation.Progress || old.Negotiation.Stream != 1 || !old.Negotiation.Settings {
			t.Fatal("Controller downgrade removed unrelated negotiated features")
		}
		return Response{Status: 200}, nil
	}, []string{ProfileV2, Profile})
	if err != nil || response.Status != 200 || calls != 2 {
		t.Fatalf("expected opt-in followed by exact legacy retry: calls=%d status=%d err=%v", calls, response.Status, err)
	}
}

func TestBrowserControllerResponderDoesNotAdvertiseToPrecedingClient(t *testing.T) {
	operations := make(chan []string, 1)
	initiator, responder, _ := profileEndpoints(t, true, true, func(ctx context.Context, _ Request) Response {
		info, _ := SessionFromContext(ctx)
		operations <- slices.Clone(info.NegotiatedOperations)
		info.NegotiatedOperations[0] = "caller.mutated"
		unchanged, _ := SessionFromContext(ctx)
		if unchanged.NegotiatedOperations[0] == "caller.mutated" {
			t.Error("handler mutated the session operation table")
		}
		return Response{Status: 200}
	})
	var offer negotiationOffer
	offer.Negotiation.Profiles = []string{ProfileV2, Profile}
	offer.Negotiation.UnboundedRPC = true
	offer.Negotiation.ExecutionProgress = true
	offer.Negotiation.StreamSchemaVersion = 1
	offer.Negotiation.ConnectionSettings = true
	body, _ := json.Marshal(offer)
	reply, err := initiator.Call(t.Context(), Request{Type: RequestType, Profile: Profile, ID: newUUID(), Operation: OperationIdentity, Body: body})
	var decoded negotiationReply
	if err != nil || reply.Status != 200 || json.Unmarshal(reply.Body, &decoded) != nil || decoded.Negotiation == nil {
		t.Fatalf("preceding client negotiation failed: %d %v", reply.Status, err)
	}
	if !slices.Equal(decoded.Negotiation.Operations, preControllerOperations(OperationsV2())) ||
		!slices.Equal(decoded.Negotiation.QualifiedOperations, preControllerOperations(OperationsV2())) {
		t.Fatal("new responder advertised Controller operations to an old client")
	}
	initiator.installCapabilities(*decoded.Negotiation)
	if reply, err := initiator.Call(t.Context(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationSettingsOwnerGet}); err != nil || reply.Status != 200 {
		t.Fatalf("unrelated settings became unavailable: %d %v", reply.Status, err)
	}
	if !slices.Equal(<-operations, decoded.Negotiation.Operations) {
		t.Fatal("domain handler received operations not negotiated by this peer")
	}
	request := Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationBrowserControllerStatus,
		InstallationID: newUUID(), Body: json.RawMessage(`{"schema_version":1}`)}
	if _, err := responder.Call(t.Context(), request); err == nil || !strings.Contains(err.Error(), "not negotiated") {
		t.Fatalf("sender admitted an unnegotiated Controller operation: %v", err)
	}
	raw, _ := json.Marshal(request)
	if err := initiator.acceptRequest(t.Context(), raw, decoded.Negotiation.SessionID); err == nil || !strings.Contains(err.Error(), "not negotiated") {
		t.Fatalf("receiver admitted an unnegotiated Controller operation: %v", err)
	}
}
