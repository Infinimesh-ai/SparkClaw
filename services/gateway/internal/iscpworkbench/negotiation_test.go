package iscpworkbench

import (
	"context"
	"encoding/json"
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
