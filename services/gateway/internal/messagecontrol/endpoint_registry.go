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
	binding, ok := r.store.GetNotificationBinding(value)
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
		Status:      app.EndpointActive,
		CreatedAt:   binding.CreatedAt,
		UpdatedAt:   binding.UpdatedAt,
	}, nil
}
