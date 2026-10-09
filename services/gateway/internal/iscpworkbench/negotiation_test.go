package iscpworkbench

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func v2Endpoints(t *testing.T, responderV2 bool) (*Endpoint, *Endpoint, *testRelayBus) {
	t.Helper()
	dmat, bmat := testMaterials(t)
	bus := &testRelayBus{peers: map[string]*testRelay{}}
	d := &testRelay{bus: bus, inbox: make(chan json.RawMessage, 128), disrupt: make(chan struct{}, 1)}
	b := &testRelay{bus: bus, inbox: make(chan json.RawMessage, 128), disrupt: make(chan struct{}, 1)}
	bus.peers[dmat.device.Identity.DeviceID] = d
	bus.peers[bmat.device.Identity.DeviceID] = b
	cfg := Config{Role: RoleInitiator, ApplicationProfiles: []string{ProfileV2, Profile}}
	initiator, err := newEndpoint(cfg, dmat, d, func(ctx context.Context, r Request) Response {
		return Response{Status: 200, Body: json.RawMessage(`{"host":true}`)}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Role = RoleResponder
	cfg.Binding = &Binding{"deployment", "owner", "client"}
	if !responderV2 {
		cfg.ApplicationProfiles = nil
	}
	responder, err := newEndpoint(cfg, bmat, b, func(ctx context.Context, r Request) Response {
		info, ok := SessionFromContext(ctx)
		if !ok || info.Binding.OwnerID != "owner" {
			t.Error("handler context missing principal")
		}
		return Response{Status: 200, Body: r.Body}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 2)
	go func() { _ = responder.Run(ctx); done <- true }()
	go func() { _ = initiator.Run(ctx); done <- true }()
	t.Cleanup(func() { cancel(); _ = initiator.Close(); _ = responder.Close(); <-done; <-done })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := initiator.Negotiated(); ok && (c.Profile == ProfileV2 || !responderV2) {
			return initiator, responder, bus
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("negotiation did not complete")
	return nil, nil, nil
}
func TestV2NegotiationKeepsExactV1ManifestAndDirections(t *testing.T) {
	i, r, bus := v2Endpoints(t, true)
	caps, ok := i.Negotiated()
	if !ok || caps.Binding.OwnerID != "owner" || caps.Profile != ProfileV2 {
		t.Fatalf("caps %+v", caps)
	}
	body := json.RawMessage(`{"value":true}`)
	resp, err := i.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationSettingsOwnerGet, Body: body})
	if err != nil || resp.Profile != ProfileV2 || string(resp.Body) != string(body) {
		t.Fatalf("response %+v %v", resp, err)
	}
	resp, err = r.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationBrowserCommand})
	if err != nil || string(resp.Body) != `{"host":true}` {
		t.Fatalf("reverse %+v %v", resp, err)
	}
	if _, err = i.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationBrowserCommand}); err == nil {
		t.Fatal("forward peer called reverse operation")
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, e := range bus.envelopes {
		if e.PayloadType == manifestType && len(e.Ciphertext) == 0 {
			t.Fatal("manifest not encrypted")
		}
	}
}
func TestV2FallsBackToExplicitV1Peer(t *testing.T) {
	i, _, _ := v2Endpoints(t, false)
	c, ok := i.Negotiated()
	if !ok || c.Profile != Profile {
		t.Fatalf("legacy selection %+v", c)
	}
	_, err := i.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationSettingsOwnerGet})
	if err == nil {
		t.Fatal("v2 request escaped negotiation")
	}
}
func TestV1RejectsV2FieldsAndV2RejectsUnregisteredParams(t *testing.T) {
	r := Request{Type: RequestType, Profile: Profile, ID: newUUID(), Operation: OperationIdentity, OperationID: newUUID()}
	if r.Validate() == nil {
		t.Fatal("v1 accepted extension")
	}
	r.Profile = ProfileV2
	r.Operation = OperationSettingsConnectorsPatch
	r.Params = map[string]string{"channel": "test"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Params["url"] = "https://business.invalid"
	if r.Validate() == nil {
		t.Fatal("unregistered tunnel parameter accepted")
	}
}

func TestV2ControlRemainsAvailableWithBulkCreditsExhausted(t *testing.T) {
	i, _, _ := v2Endpoints(t, true)
	bulk := i.classSlots["bulk"]
	for j := 0; j < cap(bulk); j++ {
		bulk <- struct{}{}
	}
	defer func() {
		for len(bulk) > 0 {
			<-bulk
		}
	}()
	_, err := i.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationObjectRead})
	if err == nil {
		t.Fatal("exhausted bulk window admitted a read")
	}
	resp, err := i.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationTransferAbort, Body: json.RawMessage(`{"transfer_id":"x"}`)})
	if err != nil || resp.Status != 200 {
		t.Fatalf("bulk starvation blocked control: %+v %v", resp, err)
	}
}

func TestV2ChunkCiphertextFitsStandardEnvelopeAndUsesBulkPriority(t *testing.T) {
	i, _, bus := v2Endpoints(t, true)
	raw := make([]byte, 8192)
	body, _ := json.Marshal(map[string]any{"transfer_id": newUUID(), "index": 0, "offset": 0, "sha256": strings.Repeat("a", 64), "data_base64": base64.StdEncoding.EncodeToString(raw)})
	r, err := i.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationTransferChunk, Body: body})
	if err != nil || r.Status != 200 {
		t.Fatalf("chunk carriage %+v %v", r, err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	found := false
	for _, env := range bus.envelopes {
		if env.Route.Priority == 1 {
			encoded, _ := json.Marshal(env)
			if len(encoded) > 32<<10 {
				t.Fatalf("8KiB chunk envelope exceeds 32KiB: %d", len(encoded))
			}
			found = true
		}
	}
	if !found {
		t.Fatal("bulk class did not use standard low-priority route")
	}
}

func TestV2RefreshRunsBeforeRealCapabilityExpiry(t *testing.T) {
	i, r, _ := v2Endpoints(t, true)
	initial, _ := i.Negotiated()
	deadline := time.Now().Add(1800 * time.Millisecond)
	for _, e := range []*Endpoint{i, r} {
		e.mu.Lock()
		e.session.capabilities.ExpiresAt = deadline
		e.mu.Unlock()
	}
	// Cross the original expiry in elapsed time: a timer condition alone is not
	// evidence that a refresh worker was actually dispatched and received.
	timer := time.NewTimer(time.Until(deadline) + 200*time.Millisecond)
	defer timer.Stop()
	<-timer.C
	updated, ok := i.Negotiated()
	if !ok || updated.SessionID != initial.SessionID || !updated.ExpiresAt.After(deadline.Add(time.Minute)) {
		t.Fatalf("capability was not refreshed before its original expiry: %+v", updated)
	}
	response, err := i.Call(context.Background(), Request{Type: RequestType, Profile: ProfileV2, ID: newUUID(), Operation: OperationSettingsOwnerGet})
	if err != nil || response.Status != 200 {
		t.Fatalf("business failed beyond previous manifest expiry: %+v %v", response, err)
	}
}
func TestV2NegotiationLocalFailureClearsFlagAndSchedulesRetry(t *testing.T) {
	i, _, _ := v2Endpoints(t, true)
	for n := 0; n < MaxConcurrent; n++ {
		i.slots <- struct{}{}
	}
	i.mu.Lock()
	id := i.session.id
	i.session.negotiating = true
	i.mu.Unlock()
	i.workers.Add(1)
	i.negotiate(context.Background(), id)
	i.mu.Lock()
	stuck := i.session.negotiating
	retry := i.session.nextNegotiationAt
	i.mu.Unlock()
	for n := 0; n < MaxConcurrent; n++ {
		<-i.slots
	}
	if stuck || !retry.After(time.Now()) {
		t.Fatalf("local negotiation failure stuck or unpaced: negotiating=%v retry=%v", stuck, retry)
	}
	i.mu.Lock()
	i.session.capabilities.ExpiresAt = time.Now().Add(1800 * time.Millisecond)
	i.session.nextNegotiationAt = time.Now()
	previous := i.session.capabilities.ExpiresAt
	i.mu.Unlock()
	timer := time.NewTimer(2100 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	cap, ok := i.Negotiated()
	if !ok || !cap.ExpiresAt.After(previous.Add(time.Minute)) {
		t.Fatal("released capacity did not allow the scheduled retry")
	}
}
