package iscpworkbench

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// The SparkX desktop installed before the phone projections (release d2230aef)
// compares the exact operation table. A current responder must leave both
// phone operations out unless the peer asks for them.
func TestPhoneProjectionsAreANegotiatedExtension(t *testing.T) {
	for _, mode := range []string{"local-test", "node-managed"} {
		full := OperationsV2ForMode(mode)
		preceding := withoutPhoneProjections(full)
		if len(full)-len(preceding) != 2 {
			t.Fatal("phone extension must contain exactly two operations")
		}
		caps := TransportCapabilities{SchemaVersion: 2, StreamSchemaVersion: 1, Profile: ProfileV2,
			SessionID: "session", Revision: "2", AuthorizationRevision: 1, ExpiresAt: time.Now().Add(time.Minute),
			Operations: preceding, QualifiedOperations: slices.Clone(preceding),
			Limits: TransportLimits{MaxRequestBytes, MaxResponseBytes, MaxBodyBytes, 0, 8192, 2}}
		if err := validateNegotiation(caps, "session", mode); err != nil {
			t.Fatalf("%s: table without phone projections rejected: %v", mode, err)
		}
		caps.Operations, caps.QualifiedOperations = full, slices.Clone(full)
		if err := validateNegotiation(caps, "session", mode); err != nil {
			t.Fatalf("%s: table with phone projections rejected: %v", mode, err)
		}
		partial := caps
		partial.Operations = append(slices.Clone(preceding), OperationPhoneSnapshotGet)
		partial.QualifiedOperations = slices.Clone(partial.Operations)
		if validateNegotiation(partial, "session", mode) == nil {
			t.Fatal("partial phone extension accepted")
		}
		qualified := caps
		qualified.Operations = preceding
		qualified.QualifiedOperations = append(slices.Clone(preceding), OperationPhoneActivityList)
		if validateNegotiation(qualified, "session", mode) == nil {
			t.Fatal("unadvertised phone operation qualified")
		}
	}
}

func TestInitiatorOffersPhoneProjectionsAndDropsThemForStrictPrecedingPeer(t *testing.T) {
	calls := 0
	_, err := requestRPCNegotiation(t.Context(), func(_ context.Context, request Request) (Response, error) {
		calls++
		var offer negotiationOffer
		if strictDecode(request.Body, &offer) != nil {
			t.Fatal("offer is not a current negotiation")
		}
		if calls == 1 {
			if !offer.Negotiation.PhoneProjections || !offer.Negotiation.MCDCheckout {
				t.Fatal("first offer must ask for phone projections and keep other extensions")
			}
			return Response{Status: 400}, nil
		}
		if offer.Negotiation.PhoneProjections || offer.Negotiation.MCDCheckout || !offer.Negotiation.MCDSelection || !offer.Negotiation.BrowserController {
			t.Fatal("first downgrade must drop phone projections with MCD checkout only")
		}
		body, _ := json.Marshal(negotiationReply{})
		return Response{Status: 200, Body: body}, nil
	}, []string{ProfileV2, Profile})
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
