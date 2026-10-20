package iscpworkbench

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestConnectionSettingsNegotiationAcceptsOnlyExactCurrentAndPrecedingTables(t *testing.T) {
	if len(OperationsV2())-len(withoutConnectionSettings(OperationsV2())) != 23 {
		t.Fatal("connection settings extension changed its fixed operation set")
	}
	for _, mode := range []string{"local-test", "node-managed"} {
		for _, settings := range []bool{false, true} {
			for _, progress := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					ops := OperationsV2ForMode(mode)
					if !settings {
						ops = withoutConnectionSettings(ops)
					}
					if !progress {
						ops = withoutProgress(ops)
					}
					if !stream {
						ops = withoutStream(ops)
					}
					caps := TransportCapabilities{
						SchemaVersion: 2, Profile: ProfileV2, SessionID: "session", Revision: "2",
						AuthorizationRevision: 1, ExpiresAt: time.Now().Add(time.Minute),
						Operations: ops, QualifiedOperations: slices.Clone(ops),
						Limits: TransportLimits{MaxRequestBytes, MaxResponseBytes, MaxBodyBytes, 0, 8192, 2},
					}
					if stream {
						caps.StreamSchemaVersion = 1
					}
					if err := validateNegotiation(caps, "session", mode); err != nil {
						t.Fatalf("%s settings=%v progress=%v stream=%v: %v", mode, settings, progress, stream, err)
					}
					invalid := caps
					invalid.Operations = append(slices.Clone(ops), "caller.selected")
					if validateNegotiation(invalid, "session", mode) == nil {
						t.Fatal("accepted an unregistered operation")
					}
					invalid = caps
					invalid.Operations = slices.DeleteFunc(slices.Clone(ops), func(op string) bool { return op == OperationSettingsOwnerGet })
					if validateNegotiation(invalid, "session", mode) == nil {
						t.Fatal("accepted an arbitrary missing mandatory operation")
					}
					invalid = caps
					if settings {
						invalid.Operations = slices.DeleteFunc(slices.Clone(ops), func(op string) bool { return op == OperationSettingsBindingsStart })
					} else {
						invalid.Operations = append(slices.Clone(ops), OperationSettingsBindingsStart)
					}
					if validateNegotiation(invalid, "session", mode) == nil {
						t.Fatal("accepted a partial connection settings extension")
					}
					if !settings {
						invalid = caps
						invalid.QualifiedOperations = append(slices.Clone(ops), OperationSettingsBindingsStart)
						if validateNegotiation(invalid, "session", mode) == nil {
							t.Fatal("qualified an unadvertised connection settings operation")
						}
						if mode == "local-test" && validateNegotiation(caps, "session", "node-managed") == nil {
							t.Fatal("legacy fallback removed required node device management")
						}
					}
				}
			}
		}
	}
}

func TestConnectionSettingsResponderPreservesAnOldClientTable(t *testing.T) {
	for _, settings := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-client", true: "new-client"}[settings], func(t *testing.T) {
			initiator, responder, _ := v2Endpoints(t, true)
			var offer negotiationOffer
			offer.Negotiation.Profiles = []string{ProfileV2, Profile}
			offer.Negotiation.UnboundedRPC = true
			offer.Negotiation.ExecutionProgress = true
			offer.Negotiation.StreamSchemaVersion = 1
			offer.Negotiation.ConnectionSettings = settings
			body, _ := json.Marshal(offer)
			reply, err := initiator.Call(t.Context(), Request{
				Type: RequestType, Profile: Profile, ID: newUUID(), Operation: OperationIdentity, Body: body,
			})
			var decoded negotiationReply
			if err != nil || reply.Status != 200 || json.Unmarshal(reply.Body, &decoded) != nil || decoded.Negotiation == nil {
				t.Fatalf("negotiation failed: status=%d err=%v", reply.Status, err)
			}
			expected := OperationsV2()
			if !settings {
				expected = withoutConnectionSettings(expected)
			}
			caps := decoded.Negotiation
			if !slices.Equal(caps.Operations, expected) || !slices.Equal(caps.QualifiedOperations, expected) {
				t.Fatal("responder changed the table understood by the offered client generation")
			}
			if caps.StreamSchemaVersion != 1 || caps.Limits.Concurrent != 0 {
				t.Fatal("connection extension changed progress or capacity negotiation")
			}
			initiator.installCapabilities(*caps)
			if response, err := initiator.Call(t.Context(), Request{
				Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationSettingsOwnerGet,
			}); err != nil || response.Status != 200 {
				t.Fatalf("existing settings operation failed after negotiation: status=%d err=%v", response.Status, err)
			}
			request := Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationSettingsBindingsList}
			response, err := initiator.Call(t.Context(), request)
			if settings && (err != nil || response.Status != 200) {
				t.Fatalf("new settings operation failed after opt-in: status=%d err=%v", response.Status, err)
			}
			if !settings {
				if err == nil {
					t.Fatal("initiator sent a connection operation after downgrade")
				}
				raw, _ := json.Marshal(request)
				if err := responder.acceptRequest(t.Context(), raw, caps.SessionID); err == nil {
					t.Fatal("responder accepted a connection operation after downgrade")
				}
			}
		})
	}
}

func TestConnectionSettingsInitiatorRetriesStrictOldResponderWithoutLosingProgress(t *testing.T) {
	calls := 0
	seen := map[string]bool{}
	reply, err := requestRPCNegotiation(t.Context(), func(_ context.Context, request Request) (Response, error) {
		calls++
		if request.Operation != OperationIdentity || seen[request.ID] {
			t.Fatal("fallback repeated an invocation identity or issued a business action")
		}
		seen[request.ID] = true
		// This is the exact offer schema used before connection settings. The
		// first request must fail its strict decoder and the fallback must pass.
		var oldOffer struct {
			Negotiation struct {
				Profiles            []string `json:"profiles"`
				UnboundedRPC        bool     `json:"unbounded_rpc,omitempty"`
				ExecutionProgress   bool     `json:"execution_progress,omitempty"`
				StreamSchemaVersion int      `json:"stream_schema_version,omitempty"`
			} `json:"transport_negotiation"`
		}
		if strictDecode(request.Body, &oldOffer) != nil {
			if calls != 1 {
				t.Fatal("fallback retained fields unknown to the preceding responder")
			}
			return Response{Status: 400}, nil
		}
		if calls != 2 || !oldOffer.Negotiation.ExecutionProgress || !oldOffer.Negotiation.UnboundedRPC || oldOffer.Negotiation.StreamSchemaVersion != 1 {
			t.Fatal("fallback dropped independently supported options")
		}
		caps := TransportCapabilities{
			StreamSchemaVersion: 1, SchemaVersion: 2, Profile: ProfileV2, SessionID: "session", Revision: "2",
			AuthorizationRevision: 1, ExpiresAt: time.Now().Add(time.Minute),
			Operations: withoutConnectionSettings(OperationsV2()),
			Limits:     TransportLimits{MaxRequestBytes, MaxResponseBytes, MaxBodyBytes, 0, 8192, 2},
		}
		body, _ := json.Marshal(negotiationReply{Negotiation: &caps})
		return Response{Status: 200, Body: body}, nil
	}, []string{ProfileV2, Profile})
	var result negotiationReply
	if err != nil || calls != 2 || json.Unmarshal(reply.Body, &result) != nil || result.Negotiation == nil {
		t.Fatalf("old responder fallback failed: calls=%d err=%v", calls, err)
	}
	if err := validateNegotiation(*result.Negotiation, "session", "node-managed"); err != nil {
		t.Fatalf("old responder's accepted result could not be installed: %v", err)
	}
}
