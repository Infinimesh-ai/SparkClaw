package remindertarget

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestResolverUsesProviderNeutralBindingFields(t *testing.T) {
	st := store.NewMemoryStore()
	st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind_alpha", Channel: "alpha", Status: "active", DisplayName: "Alpha user",
		ExternalUserID: "alpha-user", ContextToken: "alpha-context", CredentialRef: "alpha-credential", BaseURL: "https://alpha.example",
	})
	target, err := NewResolver(st).Resolve("alpha", "web-session", "Alpha user")
	if err != nil {
		t.Fatal(err)
	}
	if target.Recipient != "alpha-user" || target.RecipientBinding != "alpha-context" || target.BindingID != "bind_alpha" || target.CredentialRef != "alpha-credential" {
		t.Fatalf("unexpected provider-neutral target: %#v", target)
	}
}

func TestResolverUsesCurrentExternalSession(t *testing.T) {
	st := store.NewMemoryStore()
	linked := st.CreateSession("linked")
	st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind_alpha", Channel: "alpha", Status: "active", ExternalUserID: "binding-user", CredentialRef: "credential",
	})
	st.SaveExternalChatSession(app.ExternalChatSession{
		BindingID: "bind_alpha", Channel: "alpha", ExternalUserID: "session-user", ExternalChatID: "session-chat",
		ExternalThreadID: "session-thread", LinkedSessionID: linked.ID, Status: "active",
	})
	target, err := NewResolver(st).Resolve("alpha", linked.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.Recipient != "session-chat" || target.RecipientBinding != "session-thread" || target.BindingID != "bind_alpha" {
		t.Fatalf("unexpected current-session target: %#v", target)
	}
}

func TestResolverRequiresSelectionForMultipleBindings(t *testing.T) {
	st := store.NewMemoryStore()
	for _, id := range []string{"bind_alpha_a", "bind_alpha_b"} {
		st.SaveNotificationBinding(app.NotificationBinding{ID: id, Channel: "alpha", Status: "active", ExternalUserID: id})
	}
	_, err := NewResolver(st).Resolve("alpha", "web-session", "")
	if err == nil || !strings.Contains(err.Error(), "multiple alpha bindings") {
		t.Fatalf("expected explicit binding selection error, got %v", err)
	}
}
