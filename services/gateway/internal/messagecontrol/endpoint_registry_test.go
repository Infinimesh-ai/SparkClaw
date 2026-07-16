package messagecontrol

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestEndpointRegistryResolvesWebAndProviderNeutralBinding(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSession("Web")
	binding := st.SaveNotificationBinding(app.NotificationBinding{
		ID:       "bind_future",
		OwnerID:  app.DefaultOwnerID,
		Channel:  "future-chat",
		Provider: "vendor-specific-protocol",
		Status:   "active",
	})
	registry := NewEndpointRegistry(st)

	web, err := registry.Get(t.Context(), WebEndpointID(session.ID))
	if err != nil || web.Kind != app.EndpointKindWeb || web.ProviderKey != "" {
		t.Fatalf("unexpected web endpoint: %#v err=%v", web, err)
	}
	device, err := registry.Get(t.Context(), BindingEndpointID(binding.ID))
	if err != nil {
		t.Fatal(err)
	}
	if device.Kind != app.EndpointKindThirdPartyDevice || device.ProviderKey != "future-chat" || device.BindingRef != binding.ID {
		t.Fatalf("unexpected device endpoint: %#v", device)
	}
}
