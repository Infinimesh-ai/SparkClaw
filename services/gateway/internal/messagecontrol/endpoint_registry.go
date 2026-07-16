package messagecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type endpointStore interface {
	GetSession(string) (app.Session, bool)
	GetNotificationBinding(string) (app.NotificationBinding, bool)
	GetReminder(string) (app.Reminder, bool)
	GetExternalChatSession(string) (app.ExternalChatSession, bool)
}

type EndpointRegistry struct {
	store endpointStore
}

func NewEndpointRegistry(st endpointStore) *EndpointRegistry {
	return &EndpointRegistry{store: st}
}

func WebEndpointID(sessionID string) app.EndpointID {
	return app.EndpointID("session:" + strings.TrimSpace(sessionID))
}

func BindingEndpointID(bindingID string) app.EndpointID {
	return app.EndpointID(strings.TrimSpace(bindingID))
}

func LegacyScheduleEndpointID(scheduleID string) app.EndpointID {
	return app.EndpointID("legacy-schedule:" + strings.TrimSpace(scheduleID))
}

func (r *EndpointRegistry) Get(_ context.Context, id app.EndpointID) (app.MessageEndpoint, error) {
	if r == nil || r.store == nil {
		return app.MessageEndpoint{}, errors.New("endpoint registry is unavailable")
	}
	value := strings.TrimSpace(string(id))
	if value == "" {
		return app.MessageEndpoint{}, errors.New("endpoint id is required")
	}
	if strings.HasPrefix(value, "session:") {
		sessionID := strings.TrimSpace(strings.TrimPrefix(value, "session:"))
		session, ok := r.store.GetSession(sessionID)
		if !ok {
			return app.MessageEndpoint{}, fmt.Errorf("web endpoint %q is unavailable", value)
		}
		ownerID := strings.TrimSpace(session.OwnerID)
		if ownerID == "" {
			ownerID = app.DefaultOwnerID
		}
		return app.MessageEndpoint{
			ID:        id,
			OwnerID:   ownerID,
			Kind:      app.EndpointKindWeb,
			SessionID: session.ID,
			Status:    app.EndpointActive,
			CreatedAt: session.CreatedAt,
			UpdatedAt: session.UpdatedAt,
		}, nil
	}
	if strings.HasPrefix(value, "legacy-schedule:") {
		reminderID := strings.TrimSpace(strings.TrimPrefix(value, "legacy-schedule:"))
		reminder, ok := r.store.GetReminder(reminderID)
		if !ok {
			return app.MessageEndpoint{}, fmt.Errorf("legacy schedule endpoint %q is unavailable", value)
		}
		ownerID := app.DefaultOwnerID
		if binding, ok := r.store.GetNotificationBinding(reminder.BindingID); ok && strings.TrimSpace(binding.OwnerID) != "" {
			ownerID = binding.OwnerID
		} else if session, ok := r.store.GetSession(reminder.SessionID); ok && strings.TrimSpace(session.OwnerID) != "" {
			ownerID = session.OwnerID
		}
		kind := app.EndpointKindThirdPartyDevice
		providerKey := strings.ToLower(strings.TrimSpace(reminder.Channel))
		if providerKey == "" || providerKey == "web" {
			kind, providerKey = app.EndpointKindWeb, ""
		}
		return app.MessageEndpoint{ID: id, OwnerID: ownerID, Kind: kind, ProviderKey: providerKey, BindingRef: reminder.BindingID, Status: app.EndpointActive, CreatedAt: reminder.CreatedAt, UpdatedAt: reminder.UpdatedAt}, nil
	}
	binding, ok := r.store.GetNotificationBinding(value)
	if !ok {
		if chat, chatOK := r.store.GetExternalChatSession(value); chatOK && chat.Status == "active" {
			binding, bindingOK := r.store.GetNotificationBinding(chat.BindingID)
			if !bindingOK || binding.Status != "active" {
				return app.MessageEndpoint{}, fmt.Errorf("third-party endpoint %q is unavailable", value)
			}
			ownerID := firstEndpointValue(chat.OwnerID, binding.OwnerID, app.DefaultOwnerID)
			return app.MessageEndpoint{
				ID: id, OwnerID: ownerID, Kind: app.EndpointKindThirdPartyDevice,
				ProviderKey: strings.ToLower(strings.TrimSpace(chat.Channel)), BindingRef: binding.ID,
				Address: firstEndpointValue(chat.ExternalChatID, chat.ExternalUserID), ThreadRef: chat.ExternalThreadID, ContextRef: chat.LastContextToken,
				Status: app.EndpointActive, CreatedAt: chat.CreatedAt, UpdatedAt: chat.UpdatedAt,
			}, nil
		}
	}
	if !ok || strings.TrimSpace(binding.Status) != string(app.EndpointActive) {
		return app.MessageEndpoint{}, fmt.Errorf("third-party endpoint %q is unavailable", value)
	}
	providerKey := strings.ToLower(strings.TrimSpace(binding.Channel))
	if providerKey == "" {
		return app.MessageEndpoint{}, fmt.Errorf("third-party endpoint %q has no provider registration", value)
	}
	ownerID := strings.TrimSpace(binding.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	return app.MessageEndpoint{
		ID:          id,
		OwnerID:     ownerID,
		Kind:        app.EndpointKindThirdPartyDevice,
		ProviderKey: providerKey,
		BindingRef:  binding.ID,
		Address:     firstEndpointValue(binding.ExternalChatID, binding.ExternalUserID),
		ThreadRef:   binding.ExternalThreadID,
		ContextRef:  binding.ContextToken,
		Status:      app.EndpointActive,
		CreatedAt:   binding.CreatedAt,
		UpdatedAt:   binding.UpdatedAt,
	}, nil
}

func firstEndpointValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
