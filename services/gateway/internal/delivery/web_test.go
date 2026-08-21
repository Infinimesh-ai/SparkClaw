package delivery

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestPersistentWebDeliveryProjectsAndPersistsMessageOnce(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Web delivery")
	endpointID := messagecontrol.WebEndpointID(session.ID)
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion,
		ID:            "delivery-web-1", IdempotencyKey: "web-once", ResultID: "result-web-1", RunID: "run-web-1",
		OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
		Target: endpointID, Origin: app.DeliveryOriginSchedule, CreatedAt: time.Now().UTC(),
		Content: app.MessageContent{Parts: []app.MessagePart{
			{ID: "text-1", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "Completed."},
			{ID: "image-1", Kind: app.MessagePartImage, Disposition: app.MessageDispositionInline, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "media/card.png"}, Name: "card.png", ContentType: "image/png", Width: 900, Height: 1200},
			{ID: "audio-1", Kind: app.MessagePartAudio, Disposition: app.MessageDispositionAttachment, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "media/reply.mp3"}, ContentType: "audio/mpeg"},
			{ID: "file-1", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "outputs/report.pdf"}, ContentType: "application/pdf"},
		}},
	}
	gateway := NewGateway(messagecontrol.NewEndpointRegistry(st), nil, NewPersistentWebDelivery(st))

	for range 2 {
		receipt, err := gateway.Deliver(t.Context(), request)
		if err != nil || receipt.Status != app.DeliverySucceeded || receipt.ProviderRef != webProviderRef {
			t.Fatalf("web delivery failed: receipt=%#v err=%v", receipt, err)
		}
	}

	messages := st.ListMessages(session.ID)
	if len(messages) != 1 || messages[0].Role != "assistant" || messages[0].Content != "Completed." || messages[0].RunID != request.RunID {
		t.Fatalf("web delivery did not persist exactly one assistant message: %#v", messages)
	}
	attachments := messages[0].Attachments
	if len(attachments) != 3 || attachments[0].RelPath != "media/card.png" || attachments[0].URI != "workspace://media/card.png" || attachments[0].Width != 900 || attachments[0].Height != 1200 {
		t.Fatalf("web delivery did not preserve attachment metadata: %#v", attachments)
	}
	if attachments[1].Name != "reply.mp3" || attachments[2].Name != "report.pdf" {
		t.Fatalf("web delivery did not derive resource names: %#v", attachments)
	}
	events := st.EventsAfter(session.ID, "")
	messageEvents := 0
	for _, event := range events {
		if event.Type == "message.created" {
			messageEvents++
		}
	}
	if messageEvents != 1 {
		t.Fatalf("web delivery did not emit one message event: %#v", events)
	}
}

func TestPersistentWebDeliveryRejectsIdempotencyConflict(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Web conflict")
	endpointID := messagecontrol.WebEndpointID(session.ID)
	request := webTextRequest(endpointID, "delivery-web-1", "same-key", "first")
	gateway := NewGateway(messagecontrol.NewEndpointRegistry(st), nil, NewPersistentWebDelivery(st))
	if _, err := gateway.Deliver(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	request.ID = "delivery-web-2"
	request.Content.Parts[0].Text = "different"
	receipt, err := gateway.Deliver(t.Context(), request)
	if err == nil || ErrorCode(err) != CodeIdempotencyConflict || receipt.Status != app.DeliveryFailed || len(st.ListMessages(session.ID)) != 1 {
		t.Fatalf("idempotency conflict was not rejected: receipt=%#v messages=%#v err=%v", receipt, st.ListMessages(session.ID), err)
	}
}

func TestPersistentWebDeliveryReusesMatchingRuntimeMessage(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Runtime result")
	endpointID := messagecontrol.WebEndpointID(session.ID)
	request := webTextRequest(endpointID, "delivery-web-runtime", "runtime-key", "already persisted")
	request.RunID = "run-runtime"
	st.AddMessage(app.Message{SessionID: session.ID, RunID: request.RunID, Role: "assistant", Content: "already persisted"})

	gateway := NewGateway(messagecontrol.NewEndpointRegistry(st), nil, NewPersistentWebDelivery(st))
	if receipt, err := gateway.Deliver(t.Context(), request); err != nil || receipt.Status != app.DeliverySucceeded {
		t.Fatalf("matching runtime message was not reused: receipt=%#v err=%v", receipt, err)
	}
	if messages := st.ListMessages(session.ID); len(messages) != 1 {
		t.Fatalf("matching runtime message was duplicated: %#v", messages)
	}
}

func TestProjectWebMessageContentUsesFallbackOnlyWithoutDeliverableParts(t *testing.T) {
	content, attachments := ProjectWebMessageContent(app.MessageContent{}, "fallback")
	if content != "fallback" || len(attachments) != 0 {
		t.Fatalf("unexpected empty projection: content=%q attachments=%#v", content, attachments)
	}
	content, attachments = ProjectWebMessageContent(app.MessageContent{Parts: []app.MessagePart{{
		ID: "image-1", Kind: app.MessagePartImage, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "media/card.png"},
	}}}, "fallback")
	if content != "" || len(attachments) != 1 {
		t.Fatalf("attachment-only projection duplicated fallback: content=%q attachments=%#v", content, attachments)
	}
}

func webTextRequest(endpointID app.EndpointID, deliveryID, idempotencyKey, text string) app.DeliveryRequest {
	return app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion, ID: app.DeliveryID(deliveryID), IdempotencyKey: idempotencyKey,
		OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
		Target: endpointID, Origin: app.DeliveryOriginSchedule, CreatedAt: time.Now().UTC(),
		Content: app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: text}}},
	}
}
