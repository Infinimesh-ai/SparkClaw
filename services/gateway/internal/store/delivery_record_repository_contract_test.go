package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type deliveryRecordContractBackend struct {
	name       string
	repository DeliveryRecordRepository
	store      testBackend
}

func newDeliveryRecordContractBackends(t *testing.T) []deliveryRecordContractBackend {
	t.Helper()
	memoryStore := NewMemoryStore()
	fileStore, err := NewFileStore(filepath.Join(t.TempDir(), "delivery-record-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	return []deliveryRecordContractBackend{
		{name: "memory", repository: memoryStore, store: memoryStore},
		{name: "file", repository: fileStore, store: fileStore},
	}
}

func TestDeliveryRecordRepositoryRejectsCrossBoundIdentity(t *testing.T) {
	for _, backend := range newDeliveryRecordContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			for _, record := range []app.MessageReceiveRecord{
				{ID: "receive-a", OwnerID: "owner", ActorID: "actor", ProviderKey: "telegram", SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "received"},
				{ID: "receive-b", OwnerID: "owner", ActorID: "actor", ProviderKey: "telegram", SourceEndpointID: "endpoint-b", NativeMessageID: "native-b", Status: "received"},
			} {
				if _, err := backend.repository.SaveMessageReceive(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := backend.repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
				ID: "receive-b", OwnerID: "owner", ActorID: "actor", ProviderKey: "telegram",
				SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "duplicate",
			}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("cross-bound receive code=%q err=%v", StoreErrorCodeOf(err), err)
			}

			for _, record := range []app.MessageDeliveryRecord{
				{ID: "delivery-a", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:a", Request: app.DeliveryRequest{IdempotencyKey: "key-a", Target: "endpoint-a", ContentDigest: "sha256:a"}},
				{ID: "delivery-b", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:b", Request: app.DeliveryRequest{IdempotencyKey: "key-b", Target: "endpoint-b", ContentDigest: "sha256:b"}},
			} {
				if _, err := backend.repository.SaveMessageDelivery(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := backend.repository.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
				ID: "delivery-b", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:a",
				Request: app.DeliveryRequest{IdempotencyKey: "key-a", Target: "endpoint-a", ContentDigest: "sha256:a"},
			}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("cross-bound delivery code=%q err=%v", StoreErrorCodeOf(err), err)
			}

			for _, update := range []app.ChannelInboxUpdate{
				{ID: "inbox-a", BindingID: "binding-a", Channel: "telegram", ExternalID: "external-a", Payload: json.RawMessage(`{"id":"a"}`)},
				{ID: "inbox-b", BindingID: "binding-b", Channel: "telegram", ExternalID: "external-b", Payload: json.RawMessage(`{"id":"b"}`)},
			} {
				if _, err := backend.repository.SaveChannelInboxUpdate(t.Context(), update); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := backend.repository.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
				ID: "inbox-b", BindingID: "binding-a", Channel: "telegram", ExternalID: "external-a", Payload: json.RawMessage(`{"id":"a"}`),
			}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("cross-bound inbox code=%q err=%v", StoreErrorCodeOf(err), err)
			}
		})
	}
}

func TestDeliveryRecordRepositoryWritesLifecycleAudit(t *testing.T) {
	for _, backend := range newDeliveryRecordContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			if _, err := backend.repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
				ID: "receive-audit", SourceEndpointID: "endpoint-audit", NativeMessageID: "native-audit", Status: "received",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := backend.repository.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
				ID: "delivery-audit", OwnerID: "owner-audit", ActorID: "actor-audit", Status: app.DeliveryPending, ContentDigest: "sha256:audit",
				Request: app.DeliveryRequest{IdempotencyKey: "key-audit", Target: "endpoint-audit", ContentDigest: "sha256:audit"},
			}); err != nil {
				t.Fatal(err)
			}
			audits, err := backend.store.ListAudit(t.Context(), "")
			if err != nil || !hasAuditType(audits, "message.receive.received") || !hasAuditType(audits, "message.send.pending") {
				t.Fatalf("delivery audits=%#v err=%v", audits, err)
			}
		})
	}
}

func TestDeliveryRecordRepositoryReceiveContract(t *testing.T) {
	for _, backend := range newDeliveryRecordContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.repository
			created, err := repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
				ID: " receive-a ", OwnerID: " owner-a ", ActorID: " actor-a ", ProviderKey: " telegram ",
				SourceEndpointID: " endpoint-a ", NativeMessageID: " native-a ", Status: " received ",
			})
			if err != nil || created.ID != "receive-a" || created.OwnerID != "owner-a" || created.ActorID != "actor-a" ||
				created.ProviderKey != "telegram" || created.SourceEndpointID != "endpoint-a" || created.NativeMessageID != "native-a" ||
				created.Direction != app.MessageDirectionReceive || len(created.Transitions) != 1 {
				t.Fatalf("created receive=%#v err=%v", created, err)
			}

			updated := created
			updated.OwnerID, updated.ActorID, updated.ProviderKey = "", "", ""
			updated.Status = "processed"
			updated, err = repository.SaveMessageReceive(t.Context(), updated)
			if err != nil || updated.OwnerID != "owner-a" || updated.ActorID != "actor-a" || updated.ProviderKey != "telegram" || len(updated.Transitions) != 2 {
				t.Fatalf("updated receive=%#v err=%v", updated, err)
			}

			duplicate, err := repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
				OwnerID: "owner-a", ActorID: "actor-a", ProviderKey: "telegram",
				SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "duplicate",
			})
			if err != nil || duplicate.ID != created.ID || len(duplicate.Transitions) != 3 || duplicate.Status != "duplicate" {
				t.Fatalf("duplicate receive=%#v err=%v", duplicate, err)
			}

			conflicting := duplicate
			conflicting.NativeMessageID = "native-b"
			if candidate, err := repository.SaveMessageReceive(t.Context(), conflicting); candidate.ID != "" ||
				StoreErrorCodeOf(err) != StoreErrorConflict || !errors.Is(err, ErrMessageReceiveConflict) {
				t.Fatalf("receive conflict candidate=%#v code=%q err=%v", candidate, StoreErrorCodeOf(err), err)
			}

			found, ok, err := repository.FindMessageReceive(t.Context(), " endpoint-a ", " native-a ")
			if err != nil || !ok || found.ID != created.ID {
				t.Fatalf("find receive=%#v ok=%v err=%v", found, ok, err)
			}
			listed, err := repository.ListMessageReceives(t.Context(), "owner-a", "actor-a", 10)
			if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
				t.Fatalf("list receive=%#v err=%v", listed, err)
			}
		})
	}
}

func TestDeliveryRecordRepositoryDeliveryContractAndIsolation(t *testing.T) {
	for _, backend := range newDeliveryRecordContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			now := time.Date(2026, 8, 21, 12, 0, 0, 123456789, time.UTC)
			deliveredAt := now.Add(time.Second)
			input := app.MessageDeliveryRecord{
				ID: " delivery-a ", OwnerID: " owner-a ", ActorID: " actor-a ", Origin: app.DeliveryOriginWebDirect,
				Request: app.DeliveryRequest{
					ID: "delivery-a", IdempotencyKey: " key-a ", OwnerID: "owner-a", ActorID: "actor-a", Target: " endpoint-a ",
					Authorization: app.MessageAuthorization{PrincipalID: "actor-a", Scope: []string{"delivery.send"}},
					Content: app.MessageContent{Parts: []app.MessagePart{{ID: "part-a", Kind: app.MessagePartFile, Resource: &app.ResourceRef{
						Kind: "artifact", Ref: "artifact-a", Attributes: map[string]string{"name": "original"},
					}}}},
					ResultError: &app.WorkflowResultError{Code: "none", Message: "original"},
					MCP:         &app.MCPInvocationRef{InvocationID: "invocation-a"}, ContentDigest: "sha256:a", CreatedAt: now,
				},
				TargetSelection: app.DeliveryTargetSelection{CandidateEndpointIDs: []app.EndpointID{"endpoint-a", "endpoint-b"}},
				Status:          app.DeliverySucceeded, ContentDigest: "sha256:a", Attempts: 1,
				Receipt: &app.DeliveryReceipt{DeliveryID: "delivery-a", EndpointID: "endpoint-a", Status: app.DeliverySucceeded,
					PartReceipts: []app.PartDeliveryReceipt{{PartID: "part-a", Status: "sent"}}, AttemptedAt: now, DeliveredAt: &deliveredAt},
			}
			stored, err := backend.repository.SaveMessageDelivery(t.Context(), input)
			if err != nil || stored.ID != "delivery-a" || stored.OwnerID != "owner-a" || stored.ActorID != "actor-a" ||
				stored.Request.IdempotencyKey != "key-a" || stored.Request.Target != "endpoint-a" || stored.Direction != app.MessageDirectionSend {
				t.Fatalf("stored delivery=%#v err=%v", stored, err)
			}

			input.Request.Authorization.Scope[0] = "mutated-input"
			input.Request.Content.Parts[0].Resource.Attributes["name"] = "mutated-input"
			input.TargetSelection.CandidateEndpointIDs[0] = "mutated-input"
			input.Receipt.PartReceipts[0].Status = "mutated-input"
			stored.Request.Authorization.Scope[0] = "mutated-output"
			stored.Request.Content.Parts[0].Resource.Attributes["name"] = "mutated-output"
			stored.TargetSelection.CandidateEndpointIDs[0] = "mutated-output"
			stored.Receipt.PartReceipts[0].Status = "mutated-output"
			stored.Request.ResultError.Message = "mutated-output"

			again, ok, err := backend.repository.GetMessageDelivery(t.Context(), "delivery-a")
			if err != nil || !ok || again.Request.Authorization.Scope[0] != "delivery.send" ||
				again.Request.Content.Parts[0].Resource.Attributes["name"] != "original" ||
				again.TargetSelection.CandidateEndpointIDs[0] != "endpoint-a" || again.Receipt.PartReceipts[0].Status != "sent" ||
				again.Request.ResultError.Message != "original" {
				t.Fatalf("delivery aliases crossed repository boundary: %#v ok=%v err=%v", again, ok, err)
			}

			replay := again
			replay.ID = ""
			replayed, err := backend.repository.SaveMessageDelivery(t.Context(), replay)
			if err != nil || replayed.ID != again.ID {
				t.Fatalf("delivery replay=%#v err=%v", replayed, err)
			}
			for name, mutate := range map[string]func(*app.MessageDeliveryRecord){
				"target": func(record *app.MessageDeliveryRecord) { record.Request.Target = "endpoint-other" },
				"digest": func(record *app.MessageDeliveryRecord) {
					record.ContentDigest, record.Request.ContentDigest = "sha256:other", "sha256:other"
				},
			} {
				t.Run(name+" conflict", func(t *testing.T) {
					conflicting := again
					conflicting.ID = ""
					mutate(&conflicting)
					if _, err := backend.repository.SaveMessageDelivery(t.Context(), conflicting); StoreErrorCodeOf(err) != StoreErrorConflict || !errors.Is(err, ErrMessageDeliveryConflict) {
						t.Fatalf("delivery conflict code=%q err=%v", StoreErrorCodeOf(err), err)
					}
				})
			}
		})
	}
}

func TestDeliveryRecordRepositoryInboxContract(t *testing.T) {
	for _, backend := range newDeliveryRecordContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			created, err := backend.repository.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
				ID: " inbox-a ", BindingID: " binding-a ", Channel: " Telegram ", ExternalID: " external-a ",
				Payload: json.RawMessage(`{"update_id":1}`), Status: "pending",
			})
			if err != nil || created.ID != "inbox-a" || created.BindingID != "binding-a" || created.Channel != "telegram" || created.ExternalID != "external-a" {
				t.Fatalf("created inbox=%#v err=%v", created, err)
			}
			duplicate, err := backend.repository.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
				BindingID: "binding-a", Channel: "telegram", ExternalID: "external-a", Payload: json.RawMessage(`{"update_id":2}`), Status: "pending",
			})
			if err != nil || duplicate.ID != created.ID || string(duplicate.Payload) != `{"update_id":1}` {
				t.Fatalf("duplicate inbox=%#v err=%v", duplicate, err)
			}
			if _, err := backend.repository.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
				BindingID: "binding-a", Channel: "telegram", ExternalID: "invalid", Payload: json.RawMessage(`{"broken"`), Status: "pending",
			}); StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("invalid payload code=%q err=%v", StoreErrorCodeOf(err), err)
			}
			conflicting := created
			conflicting.Channel = "weixin"
			if _, err := backend.repository.SaveChannelInboxUpdate(t.Context(), conflicting); StoreErrorCodeOf(err) != StoreErrorConflict || !errors.Is(err, ErrChannelInboxUpdateConflict) {
				t.Fatalf("inbox conflict code=%q err=%v", StoreErrorCodeOf(err), err)
			}
			created.Payload[0] = '['
			again, ok, err := backend.repository.GetChannelInboxUpdate(t.Context(), created.ID)
			if err != nil || !ok || string(again.Payload) != `{"update_id":1}` {
				t.Fatalf("inbox payload alias=%#v ok=%v err=%v", again, ok, err)
			}
			again.Status, again.Payload = "completed", nil
			if _, err := backend.repository.SaveChannelInboxUpdate(t.Context(), again); err != nil {
				t.Fatal(err)
			}
			again, ok, err = backend.repository.GetChannelInboxUpdate(t.Context(), again.ID)
			if err != nil || !ok || len(again.Payload) != 0 || again.Status != "completed" {
				t.Fatalf("empty inbox payload=%#v ok=%v err=%v", again, ok, err)
			}
		})
	}
}

func TestDeliveryRecordRepositoryContextCancellation(t *testing.T) {
	for _, backend := range newDeliveryRecordContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			checks := []func() error{
				func() error {
					_, err := backend.repository.SaveMessageReceive(ctx, app.MessageReceiveRecord{})
					return err
				},
				func() error { _, _, err := backend.repository.GetMessageReceive(ctx, "id"); return err },
				func() error {
					_, _, err := backend.repository.FindMessageReceive(ctx, "endpoint", "native")
					return err
				},
				func() error { _, err := backend.repository.ListMessageReceives(ctx, "owner", "actor", 1); return err },
				func() error {
					_, err := backend.repository.SaveMessageDelivery(ctx, app.MessageDeliveryRecord{})
					return err
				},
				func() error { _, _, err := backend.repository.GetMessageDelivery(ctx, "id"); return err },
				func() error {
					_, _, err := backend.repository.FindMessageDeliveryByIdempotency(ctx, "owner", "actor", "key")
					return err
				},
				func() error { _, err := backend.repository.ListMessageDeliveries(ctx, "owner", "actor", 1); return err },
				func() error {
					_, err := backend.repository.SaveChannelInboxUpdate(ctx, app.ChannelInboxUpdate{})
					return err
				},
				func() error { _, _, err := backend.repository.GetChannelInboxUpdate(ctx, "id"); return err },
				func() error {
					_, _, err := backend.repository.FindChannelInboxUpdate(ctx, "binding", "external")
					return err
				},
				func() error {
					_, err := backend.repository.ListChannelInboxUpdates(ctx, "telegram", "pending", time.Time{}, 1)
					return err
				},
			}
			for index, check := range checks {
				if err := check(); StoreErrorCodeOf(err) != StoreErrorCanceled {
					t.Fatalf("operation %d code=%q err=%v", index, StoreErrorCodeOf(err), err)
				}
			}
		})
	}
}

func TestDeliveryRecordRepositoryConcurrentIdempotency(t *testing.T) {
	for _, backend := range newDeliveryRecordContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			const workers = 24
			receiveIDs := make(chan string, workers)
			deliveryIDs := make(chan app.DeliveryID, workers)
			errorsSeen := make(chan error, workers*2)
			var group sync.WaitGroup
			for range workers {
				group.Add(2)
				go func() {
					defer group.Done()
					record, err := backend.repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
						OwnerID: "owner-race", ActorID: "actor-race", ProviderKey: "telegram",
						SourceEndpointID: "endpoint-race", NativeMessageID: "native-race", Status: "received",
					})
					if err != nil {
						errorsSeen <- err
						return
					}
					receiveIDs <- record.ID
				}()
				go func() {
					defer group.Done()
					record, err := backend.repository.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
						OwnerID: "owner-race", ActorID: "actor-race", Status: app.DeliveryPending, ContentDigest: "sha256:race",
						Request: app.DeliveryRequest{IdempotencyKey: "key-race", Target: "endpoint-race", ContentDigest: "sha256:race"},
					})
					if err != nil {
						errorsSeen <- err
						return
					}
					deliveryIDs <- record.ID
				}()
			}
			group.Wait()
			close(receiveIDs)
			close(deliveryIDs)
			close(errorsSeen)
			for err := range errorsSeen {
				t.Error(err)
			}
			var receiveID string
			for id := range receiveIDs {
				if receiveID == "" {
					receiveID = id
				}
				if id != receiveID {
					t.Errorf("receive IDs differ: %q/%q", receiveID, id)
				}
			}
			var deliveryID app.DeliveryID
			for id := range deliveryIDs {
				if deliveryID == "" {
					deliveryID = id
				}
				if id != deliveryID {
					t.Errorf("delivery IDs differ: %q/%q", deliveryID, id)
				}
			}
			receives, err := backend.repository.ListMessageReceives(t.Context(), "owner-race", "actor-race", workers)
			if err != nil || len(receives) != 1 {
				t.Fatalf("receive records=%#v err=%v", receives, err)
			}
			deliveries, err := backend.repository.ListMessageDeliveries(t.Context(), "owner-race", "actor-race", workers)
			if err != nil || len(deliveries) != 1 {
				t.Fatalf("delivery records=%#v err=%v", deliveries, err)
			}
		})
	}
}
