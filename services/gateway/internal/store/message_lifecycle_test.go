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
	if got, ok := reloaded.FindMessageReceive("endpoint-a", "native-a"); !ok || got.Status != "duplicate" || len(got.Transitions) != 3 {
		t.Fatalf("receive lifecycle did not reload: %#v ok=%v", got, ok)
	}
	if got, ok := reloaded.FindMessageDeliveryByIdempotency("owner-a", "actor-a", "web-key-a"); !ok || got.Status != app.DeliverySucceeded || got.Receipt == nil {
		t.Fatalf("send lifecycle did not reload: %#v ok=%v", got, ok)
	}
}

func testMessageLifecycleParity(t *testing.T, st Store) {
	t.Helper()
	receive := st.SaveMessageReceive(app.MessageReceiveRecord{
		ID: "receive-a", OwnerID: "owner-a", ActorID: "actor-a", ProviderKey: "telegram",
		SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "received",
	})
	receive.Status = "processed"
	receive = st.SaveMessageReceive(receive)
	if receive.Direction != app.MessageDirectionReceive || len(receive.Transitions) != 2 {
		t.Fatalf("receive lifecycle was not advanced: %#v", receive)
	}
	if duplicate := st.SaveMessageReceive(app.MessageReceiveRecord{
		OwnerID: "owner-a", ActorID: "actor-a", ProviderKey: "telegram", SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "duplicate",
	}); duplicate.ID != receive.ID {
		t.Fatalf("receive idempotency boundary created a second record: %#v", duplicate)
	}

	receipt := app.DeliveryReceipt{DeliveryID: "delivery-a", EndpointID: "endpoint-a", Status: app.DeliverySucceeded, Attempt: 1}
	delivery := st.SaveMessageDelivery(app.MessageDeliveryRecord{
		ID: "delivery-a", OwnerID: "owner-a", ActorID: "actor-a", Origin: app.DeliveryOriginWebDirect,
		Request: app.DeliveryRequest{ID: "delivery-a", IdempotencyKey: "web-key-a", Target: "endpoint-a"},
		Status:  app.DeliverySucceeded, ContentDigest: "sha256:a", Receipt: &receipt, Attempts: 1,
	})
	if delivery.Direction != app.MessageDirectionSend {
		t.Fatalf("delivery direction was not normalized: %#v", delivery)
	}
	if got, ok := st.FindMessageDeliveryByIdempotency("owner-a", "actor-a", "web-key-a"); !ok || got.ID != delivery.ID {
		t.Fatalf("delivery idempotency lookup failed: %#v ok=%v", got, ok)
	}
	if got := st.ListMessageDeliveries("owner-a", "actor-b", 10); len(got) != 0 {
		t.Fatalf("cross-actor delivery history leaked: %#v", got)
	}
}
