package iscpworkbench

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestMCDSessionOperationsRequireIndependentOptIn(t *testing.T) {
	i, r, _ := v2Endpoints(t, true)
	for _, enabled := range []bool{false, true} {
		var offer negotiationOffer
		offer.Negotiation.Profiles = []string{ProfileV2, Profile}
		offer.Negotiation.MCDSelection = enabled
		raw, _ := json.Marshal(offer)
		reply, err := i.Call(t.Context(), Request{Type: RequestType, Profile: Profile, ID: newUUID(), Operation: OperationIdentity, Body: raw})
		var value negotiationReply
		if err != nil || json.Unmarshal(reply.Body, &value) != nil || value.Negotiation == nil {
			t.Fatal("negotiation failed", err)
		}
		for _, op := range []string{OperationMCDSessionProvision, OperationMCDSessionRevoke, OperationMCDSessionStatus} {
			if slices.Contains(value.Negotiation.Operations, op) != enabled {
				t.Fatal("McDonald's opt-in ignored")
			}
		}
		if slices.Contains(value.Negotiation.Operations, OperationBrowserControllerExecute) {
			t.Fatal("McDonald's opted in to browser control")
		}
		i.installCapabilities(*value.Negotiation)
		request := Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationMCDSessionStatus, Body: json.RawMessage(`{}`)}
		if !enabled {
			if _, err = i.Call(t.Context(), request); err == nil {
				t.Fatal("unnegotiated client dispatch")
			}
			wire, _ := json.Marshal(request)
			if r.acceptRequest(t.Context(), wire, value.Negotiation.SessionID) == nil {
				t.Fatal("unnegotiated receiver dispatch")
			}
		}
	}
}
