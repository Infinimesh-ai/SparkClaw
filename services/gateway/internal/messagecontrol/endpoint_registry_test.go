package messagecontrol

import (
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestEndpointRegistryResolvesWebAndProviderNeutralBinding(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Web")
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
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
	saveEndpointFixture(t, st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(t, st, "bind-b", "chat-b", "owner-a", "actor-b", "telegram", "Alex", "user-2", "chat-2", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(t, st, "bind-reminder", "chat-reminder", "owner-a", "actor-a", "weixin", "Only reminder", "user-3", "chat-3", []string{app.BindingScopeReminderSendSelf})

	registry := NewEndpointRegistry(st)
	endpoints, err := registry.List(t.Context(), "owner-a", "actor-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 || endpoints[0].ID != "chat-a" || endpoints[0].ExternalUserRef != "user-1" || endpoints[0].Address != "chat-1" || endpoints[1].ID != "chat-reminder" {
		t.Fatalf("unexpected exact endpoint list: %#v", endpoints)
	}
	if endpoints[0].BindingRef == "" || endpoints[0].RecipientDisplayName != "Alex" {
		t.Fatalf("endpoint lost internal binding or public identity: %#v", endpoints[0])
	}
}

func TestEndpointRegistryDeterministicTargetResolution(t *testing.T) {
	st := store.NewMemoryStore()
	web := storetest.MustCreateSessionWithScope(t, st, "Web", "owner-a", t.TempDir(), "webchat", false)
	saveEndpointFixture(t, st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(t, st, "bind-b", "chat-b", "owner-a", "actor-a", "telegram", "Alex", "user-2", "chat-2", []string{app.BindingScopeMessageSendSelf})
	saveEndpointFixture(t, st, "bind-c", "chat-c", "owner-a", "actor-a", "weixin", "Chen", "user-3", "chat-3", []string{app.BindingScopeMessageSendSelf})
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
	saveEndpointFixture(t, st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{"unknown"})
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "bind-expired", OwnerID: "owner-a", ActorID: "actor-a", Channel: "weixin", Provider: "weixin-provider",
		Status: app.NotificationBindingActive, DisplayName: "weixin account",
		Scopes: []string{app.BindingScopeMessageSendSelf}, ExpiresAt: &expired,
	})
	storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{
		ID: "chat-expired", OwnerID: "source-user-2", AuthorizedOwnerID: "owner-a", AuthorizedActorID: "actor-a",
		BindingID: "bind-expired", Channel: "weixin", ExternalUserID: "user-2", ExternalChatID: "chat-2",
		DisplayName: "Chen", Status: "active",
	})
	registry := NewEndpointRegistry(st)

	_, err := registry.GetForMessageSend(t.Context(), "chat-a", "owner-a", "actor-a")
	var targetErr *TargetError
	if !errors.As(err, &targetErr) || targetErr.Code != CodeScopeDenied {
		t.Fatalf("expected scope denial, got %v", err)
	}
	_, err = registry.GetForMessageSend(t.Context(), "chat-a", "owner-a", "actor-b")
	if !errors.As(err, &targetErr) || targetErr.Code != CodeCrossUserDenied {
		t.Fatalf("expected cross-actor denial, got %v", err)
	}
	endpoints, err := registry.List(t.Context(), "owner-a", "actor-a")
	if err != nil || len(endpoints) != 0 {
		t.Fatalf("ineligible endpoints leaked into catalog: %#v err=%v", endpoints, err)
	}
}

func TestEndpointRegistryMessageSendRejectsBindingFallback(t *testing.T) {
	st := store.NewMemoryStore()
	saveEndpointFixture(t, st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{app.BindingScopeMessageSendSelf})
	registry := NewEndpointRegistry(st)

	_, err := registry.GetForMessageSend(t.Context(), "bind-a", "owner-a", "actor-a")
	var targetErr *TargetError
	if !errors.As(err, &targetErr) || targetErr.Code != CodeBindingUnavailable {
		t.Fatalf("binding ID was accepted as an exact message recipient: %v", err)
	}
	endpoint, err := registry.GetForMessageSend(t.Context(), "chat-a", "owner-a", "actor-a")
	if err != nil || endpoint.ID != "chat-a" || endpoint.BindingRef != "bind-a" || endpoint.ExternalUserRef != "user-1" || endpoint.Address != "chat-1" {
		t.Fatalf("exact chat endpoint was rejected: endpoint=%#v err=%v", endpoint, err)
	}
}

func TestEndpointRegistryHidesDisabledConnectorEndpoints(t *testing.T) {
	st := store.NewMemoryStore()
	saveEndpointFixture(t, st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1", []string{app.BindingScopeMessageSendSelf})
	enabled := false
	registry := NewEndpointRegistry(st).WithChannelEnabled(func(ownerID, channel string) bool {
		return enabled && ownerID == "owner-a" && channel == "telegram"
	})
	if endpoints, err := registry.List(t.Context(), "owner-a", "actor-a"); err != nil || len(endpoints) != 0 {
		t.Fatalf("disabled connector endpoints remained visible: %#v err=%v", endpoints, err)
	}
	_, err := registry.Get(t.Context(), "chat-a")
	if errorCode := targetErrorCode(err); errorCode != CodeConnectorDisabled {
		t.Fatalf("disabled connector get error = %q (%v)", errorCode, err)
	}
	enabled = true
	if endpoints, err := registry.List(t.Context(), "owner-a", "actor-a"); err != nil || len(endpoints) != 1 || endpoints[0].ID != "chat-a" {
		t.Fatalf("enabled connector endpoint missing: %#v err=%v", endpoints, err)
	}
}

func targetErrorCode(err error) string {
	var targetErr *TargetError
	if errors.As(err, &targetErr) {
		return targetErr.Code
	}
	return ""
}

func saveEndpointFixture(t testing.TB, st *store.MemoryStore, bindingID, chatID, ownerID, actorID, channel, displayName, externalUserID, externalChatID string, scopes []string) {
	t.Helper()
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: bindingID, OwnerID: ownerID, ActorID: actorID, Channel: channel, Provider: channel + "-provider",
		Status: "active", DisplayName: channel + " account", Scopes: scopes,
	})
	storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{
		ID: chatID, OwnerID: "source-" + externalUserID, AuthorizedOwnerID: ownerID, AuthorizedActorID: actorID,
		BindingID: bindingID, Channel: channel, ExternalUserID: externalUserID, ExternalChatID: externalChatID,
		DisplayName: displayName, Status: "active",
	})
}
