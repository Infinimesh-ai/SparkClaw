package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const webProviderRef = "web-session"

type webMessageStore interface {
	AddMessage(app.Message) app.Message
	ListMessages(string) []app.Message
}

type PersistentWebDelivery struct {
	store webMessageStore
}

func NewPersistentWebDelivery(st webMessageStore) *PersistentWebDelivery {
	return &PersistentWebDelivery{store: st}
}

func (d *PersistentWebDelivery) Deliver(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	if d == nil || d.store == nil {
		return webDeliveryFailure(endpoint, request, errors.New("web message store is unavailable"))
	}
	if err := ctx.Err(); err != nil {
		return webDeliveryFailure(endpoint, request, err)
	}
	if endpoint.Kind != app.EndpointKindWeb || strings.TrimSpace(endpoint.SessionID) == "" {
		return webDeliveryFailure(endpoint, request, errors.New("web delivery requires a session endpoint"))
	}
	if err := validateWebMessageContent(request.Content); err != nil {
		return webDeliveryFailure(endpoint, request, err)
	}

	content, attachments := ProjectWebMessageContent(request.Content, "")
	message := app.Message{
		ID:          webDeliveryMessageID(endpoint.ID, request.IdempotencyKey),
		SessionID:   endpoint.SessionID,
		RunID:       request.RunID,
		Role:        "assistant",
		Content:     content,
		Attachments: attachments,
		CreatedAt:   request.CreatedAt,
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}

	for _, existing := range d.store.ListMessages(endpoint.SessionID) {
		switch {
		case existing.ID == message.ID:
			if !sameWebMessage(existing, message) {
				return webDeliveryFailure(endpoint, request, NewError(CodeIdempotencyConflict, "web delivery idempotency key was already used for another message", "blocked"))
			}
			return webDeliverySuccess(endpoint, request), nil
		case request.RunID != "" && existing.RunID == request.RunID && sameWebMessagePayload(existing, message):
			// Agent Runtime already persists synchronous Web results. Treat that
			// projection as the same delivery instead of appending a duplicate.
			return webDeliverySuccess(endpoint, request), nil
		}
	}

	stored := d.store.AddMessage(message)
	if !sameWebMessage(stored, message) {
		return webDeliveryFailure(endpoint, request, NewError(CodeIdempotencyConflict, "web delivery message identity conflicts with persisted content", "blocked"))
	}
	return webDeliverySuccess(endpoint, request), nil
}

func ProjectWebMessageContent(content app.MessageContent, fallback string) (string, []app.MessageAttachment) {
	textParts := make([]string, 0, len(content.Parts))
	attachments := make([]app.MessageAttachment, 0, len(content.Parts))
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText {
			if text := strings.TrimSpace(part.Text); text != "" {
				textParts = append(textParts, text)
			}
			continue
		}
		if part.Kind != app.MessagePartImage && part.Kind != app.MessagePartAudio && part.Kind != app.MessagePartFile {
			continue
		}
		relPath := ""
		if part.Resource != nil && part.Resource.Kind == "workspace_file" {
			relPath = strings.TrimSpace(part.Resource.Ref)
		}
		if relPath == "" {
			continue
		}
		name := strings.TrimSpace(part.Name)
		if name == "" {
			name = path.Base(strings.TrimSuffix(relPath, "/"))
		}
		attachments = append(attachments, app.MessageAttachment{
			ArtifactID:  part.ArtifactID,
			Name:        name,
			RelPath:     relPath,
			URI:         "workspace://" + relPath,
			ContentType: part.ContentType,
			Bytes:       part.Bytes,
			Width:       part.Width,
			Height:      part.Height,
			SHA256:      part.SHA256,
			Source:      "workflow_result",
			Caption:     part.Caption,
		})
	}
	projected := strings.Join(textParts, "\n\n")
	if projected == "" && len(attachments) == 0 {
		projected = fallback
	}
	return projected, attachments
}

func validateWebMessageContent(content app.MessageContent) error {
	if err := ValidateCapabilities(app.DeliveryCapabilities{
		Kinds: []app.MessagePartKind{app.MessagePartText, app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile},
	}, content); err != nil {
		return err
	}
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText {
			continue
		}
		if part.Resource == nil || part.Resource.Kind != "workspace_file" || strings.TrimSpace(part.Resource.Ref) == "" {
			return NewError(CodeArtifactInvalid, "web binary delivery requires a governed workspace_file resource", "blocked")
		}
	}
	return nil
}

func webDeliveryMessageID(endpointID app.EndpointID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(string(endpointID) + "\x00" + idempotencyKey))
	return "m_delivery_" + hex.EncodeToString(digest[:16])
}

func sameWebMessage(existing, expected app.Message) bool {
	return existing.ID == expected.ID && existing.SessionID == expected.SessionID && sameWebMessagePayload(existing, expected)
}

func sameWebMessagePayload(existing, expected app.Message) bool {
	return existing.RunID == expected.RunID && existing.Role == expected.Role && existing.Content == expected.Content && slices.Equal(existing.Attachments, expected.Attachments)
}

func webDeliverySuccess(endpoint app.MessageEndpoint, request app.DeliveryRequest) app.DeliveryReceipt {
	now := time.Now().UTC()
	receipts := make([]app.PartDeliveryReceipt, 0, len(request.Content.Parts))
	for _, part := range request.Content.Parts {
		receipts = append(receipts, app.PartDeliveryReceipt{
			PartID: part.ID, Status: "sent", Representation: "web_message", ProviderRef: webProviderRef,
		})
	}
	return app.DeliveryReceipt{
		DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded,
		ProviderRef: webProviderRef, Attempt: 1, PartReceipts: receipts, AttemptedAt: now, DeliveredAt: &now,
	}
}

func webDeliveryFailure(endpoint app.MessageEndpoint, request app.DeliveryRequest, err error) (app.DeliveryReceipt, error) {
	receipt := failedReceipt(endpoint, request, err.Error())
	receipt.ErrorCode = ErrorCode(err)
	return receipt, err
}
