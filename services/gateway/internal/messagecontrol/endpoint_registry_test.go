package messagecontrol

import (
	"errors"
	"testing"
	"time"

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

func TestEndpointRegistryListsOnlyActorScopedExactSendEndpoints(t *testing.T) {
	st := store.NewMemoryStore()
	saveEndpointFixture(st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(st, "bind-b", "chat-b", "owner-a", "actor-b", "telegram", "Alex", "user-2", "chat-2", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(st, "bind-reminder", "chat-reminder", "owner-a", "actor-a", "weixin", "Only reminder", "user-3", "chat-3", []string{app.BindingScopeReminderSendSelf})

	registry := NewEndpointRegistry(st)
	endpoints, err := registry.List(t.Context(), "owner-a", "actor-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].ID != "chat-a" || endpoints[0].ExternalUserRef != "user-1" || endpoints[0].Address != "chat-1" {
		t.Fatalf("unexpected exact endpoint list: %#v", endpoints)
	}
	if endpoints[0].BindingRef == "" || endpoints[0].RecipientDisplayName != "Alex" {
		t.Fatalf("endpoint lost internal binding or public identity: %#v", endpoints[0])
	}
}

func TestEndpointRegistryDeterministicTargetResolution(t *testing.T) {
	st := store.NewMemoryStore()
	web := st.CreateSessionWithScope("Web", "owner-a", t.TempDir(), "webchat", false)
	saveEndpointFixture(st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(st, "bind-b", "chat-b", "owner-a", "actor-a", "telegram", "Alex", "user-2", "chat-2", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(st, "bind-c", "chat-c", "owner-a", "actor-a", "weixin", "Chen", "user-3", "chat-3", []string{app.BindingScopeMessageSendSelf})
	registry := NewEndpointRegistry(st)

	selection, err := registry.ResolveTarget(t.Context(), TargetRequest{OwnerID: "owner-a", ActorID: "actor-a", WebSessionID: web.ID})
	if err != nil || selection.Status != app.TargetDefaultWeb || selection.ResolvedEndpointID != WebEndpointID(web.ID) {
		t.Fatalf("Web default was not deterministic: %#v err=%v", selection, err)
	}
	selection, _ = registry.ResolveTarget(t.Context(), TargetRequest{OwnerID: "owner-a", ActorID: "actor-a", ExternalIntent: true})
	if selection.Status != app.TargetNeedsChannel {
		t.Fatalf("missing software should clarify: %#v", selection)
	}
	selection, _ = registry.ResolveTarget(t.Context(), TargetRequest{OwnerID: "owner-a", ActorID: "actor-a", ExternalIntent: true, ProviderKey: "telegram"})
	if selection.Status != app.TargetNeedsRecipient || len(selection.CandidateEndpointIDs) != 2 {
		t.Fatalf("multiple recipients should clarify: %#v", selection)
	}
	selection, _ = registry.ResolveTarget(t.Context(), TargetRequest{OwnerID: "owner-a", ActorID: "actor-a", ExternalIntent: true, ProviderKey: "telegram", RecipientText: "Alex"})
	if selection.Status != app.TargetAmbiguous {
		t.Fatalf("same-name recipients should be ambiguous: %#v", selection)
	}
	selection, _ = registry.ResolveTarget(t.Context(), TargetRequest{OwnerID: "owner-a", ActorID: "actor-a", ExternalIntent: true, ProviderKey: "weixin"})
	if selection.Status != app.TargetResolved || selection.ResolvedEndpointID != "chat-c" {
		t.Fatalf("sole software endpoint should resolve: %#v", selection)
	}
	selection, _ = registry.ResolveTarget(t.Context(), TargetRequest{OwnerID: "owner-a", ActorID: "actor-a", ExternalIntent: true, ProviderKey: "signal"})
	if selection.Status != app.TargetUnavailable {
		t.Fatalf("zero endpoints should block: %#v", selection)
	}
}

func TestEndpointRegistryRejectsWrongActorScopeAndExpiredBinding(t *testing.T) {
	st := store.NewMemoryStore()
	expired := time.Now().UTC().Add(-time.Minute)
	saveEndpointFixture(st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{app.BindingScopeReminderSendSelf})
	saveEndpointFixture(st, "bind-expired", "chat-expired", "owner-a", "actor-a", "weixin", "Chen", "user-2", "chat-2", []string{app.BindingScopeMessageSendSelf})
	binding, _ := st.GetNotificationBinding("bind-expired")
	binding.ExpiresAt = &expired
	st.SaveNotificationBinding(binding)
	registry := NewEndpointRegistry(st)

	_, err := registry.GetForDirectSend(t.Context(), "chat-a", "owner-a", "actor-a")
	var targetErr *TargetError
	if !errors.As(err, &targetErr) || targetErr.Code != CodeScopeDenied {
		t.Fatalf("expected scope denial, got %v", err)
	}
	_, err = registry.GetForDirectSend(t.Context(), "chat-a", "owner-a", "actor-b")
	if !errors.As(err, &targetErr) || targetErr.Code != CodeCrossUserDenied {
		t.Fatalf("expected cross-actor denial, got %v", err)
	}
	endpoints, err := registry.List(t.Context(), "owner-a", "actor-a")
	if err != nil || len(endpoints) != 0 {
		t.Fatalf("ineligible endpoints leaked into catalog: %#v err=%v", endpoints, err)
	}
}

func saveEndpointFixture(st *store.MemoryStore, bindingID, chatID, ownerID, actorID, channel, displayName, externalUserID, externalChatID string, scopes []string) {
	st.SaveNotificationBinding(app.NotificationBinding{
		ID: bindingID, OwnerID: ownerID, ActorID: actorID, Channel: channel, Provider: channel + "-provider",
		Status: "active", DisplayName: channel + " account", Scopes: scopes,
	})
	st.SaveExternalChatSession(app.ExternalChatSession{
		ID: chatID, OwnerID: "source-" + externalUserID, AuthorizedOwnerID: ownerID, AuthorizedActorID: actorID,
		BindingID: bindingID, Channel: channel, ExternalUserID: externalUserID, ExternalChatID: externalChatID,
		DisplayName: displayName, Status: "active",
	})
}
