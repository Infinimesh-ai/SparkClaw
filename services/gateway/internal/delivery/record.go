package delivery

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type externalDeliveryStore interface {
	GetExternalChatSession(string) (app.ExternalChatSession, bool)
	SaveExternalChatMessage(app.ExternalChatMessage) app.ExternalChatMessage
}

// RecordExternalDelivery keeps provider delivery state separate from the
// WorkflowResult business state. Binding-only schedule endpoints intentionally
// have no chat timeline to update.
func RecordExternalDelivery(st externalDeliveryStore, endpoint app.MessageEndpoint, request app.DeliveryRequest, receipt app.DeliveryReceipt) {
	if st == nil {
		return
	}
	chat, ok := st.GetExternalChatSession(string(endpoint.ID))
	if !ok {
		return
	}
	status := "sent"
	if receipt.Status != app.DeliverySucceeded {
		status = "failed"
		if receipt.Status == app.DeliveryPartiallySent {
			status = "partially_sent"
		}
	}
	createdAt := receipt.AttemptedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	st.SaveExternalChatMessage(app.ExternalChatMessage{
		ID:                "external_delivery_" + string(request.ID),
		ChatSessionID:     chat.ID,
		BindingID:         endpoint.BindingRef,
		Channel:           endpoint.ProviderKey,
		Direction:         "outbound",
		Role:              "assistant",
		ExternalMessageID: string(request.ID),
		Content:           deliveryContentSummary(request.Content),
		ContextToken:      endpoint.ContextRef,
		LinkedRunID:       request.RunID,
		Status:            status,
		Error:             receipt.Error,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	})
}

func deliveryContentSummary(content app.MessageContent) string {
	parts := make([]string, 0, len(content.Parts))
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText {
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
			}
			continue
		}
		name := strings.TrimSpace(part.Name)
		if name == "" {
			name = part.ID
		}
		parts = append(parts, "["+string(part.Kind)+": "+name+"]")
	}
	return strings.Join(parts, "\n")
}
