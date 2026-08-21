package store

import (
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryStoreMessageLifecycleParity(t *testing.T) {
	testMessageLifecycleParity(t, NewMemoryStore())
}

func TestFileStoreMessageLifecycleRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	testMessageLifecycleParity(t, st)
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := reloaded.FindMessageReceive(t.Context(), "endpoint-a", "native-a"); err != nil || !ok || got.Status != "duplicate" || len(got.Transitions) != 3 {
		t.Fatalf("receive lifecycle did not reload: %#v ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := reloaded.FindMessageDeliveryByIdempotency(t.Context(), "owner-a", "actor-a", "web-key-a"); err != nil || !ok || got.Status != app.DeliverySucceeded || got.Receipt == nil {
		t.Fatalf("send lifecycle did not reload: %#v ok=%v err=%v", got, ok, err)
	}
}

func testMessageLifecycleParity(t *testing.T, st testBackend) {
	t.Helper()
	receive, err := st.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
		ID: "receive-a", OwnerID: "owner-a", ActorID: "actor-a", ProviderKey: "telegram",
		SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "received",
	})
	if err != nil {
		t.Fatal(err)
	}
	receive.Status = "processed"
	receive, err = st.SaveMessageReceive(t.Context(), receive)
	if err != nil {
		t.Fatal(err)
	}
	if receive.Direction != app.MessageDirectionReceive || len(receive.Transitions) != 2 {
		t.Fatalf("receive lifecycle was not advanced: %#v", receive)
	}
	if duplicate, err := st.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
		OwnerID: "owner-a", ActorID: "actor-a", ProviderKey: "telegram", SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "duplicate",
	}); err != nil || duplicate.ID != receive.ID {
		t.Fatalf("receive idempotency boundary created a second record: %#v err=%v", duplicate, err)
	}

	receipt := app.DeliveryReceipt{DeliveryID: "delivery-a", EndpointID: "endpoint-a", Status: app.DeliverySucceeded, Attempt: 1}
	delivery, err := st.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
		ID: "delivery-a", OwnerID: "owner-a", ActorID: "actor-a", Origin: app.DeliveryOriginWebDirect,
		Request: app.DeliveryRequest{ID: "delivery-a", IdempotencyKey: "web-key-a", Target: "endpoint-a"},
		Status:  app.DeliverySucceeded, ContentDigest: "sha256:a", Receipt: &receipt, Attempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Direction != app.MessageDirectionSend {
		t.Fatalf("delivery direction was not normalized: %#v", delivery)
	}
	if got, ok, err := st.FindMessageDeliveryByIdempotency(t.Context(), "owner-a", "actor-a", "web-key-a"); err != nil || !ok || got.ID != delivery.ID {
		t.Fatalf("delivery idempotency lookup failed: %#v ok=%v err=%v", got, ok, err)
	}
	if got, err := st.ListMessageDeliveries(t.Context(), "owner-a", "actor-b", 10); err != nil || len(got) != 0 {
		t.Fatalf("cross-actor delivery history leaked: %#v err=%v", got, err)
	}
}
