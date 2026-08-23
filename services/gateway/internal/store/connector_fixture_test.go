package store

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustCreateNotificationBindingFixture(t testing.TB, repository ConnectorRepository, desired app.NotificationBinding) app.NotificationBinding {
	t.Helper()
	if strings.TrimSpace(desired.ID) == "" {
		desired.ID = app.NewID("bind_test")
	}
	if strings.TrimSpace(desired.OwnerID) == "" {
		desired.OwnerID = app.DefaultOwnerID
	}
	if strings.TrimSpace(desired.ActorID) == "" {
		desired.ActorID = desired.OwnerID
	}
	desired.Channel = normalizeConnectorChannel(desired.Channel)
	if desired.Channel == "" {
		desired.Channel = "test"
	}
	if strings.TrimSpace(desired.Provider) == "" {
		desired.Provider = desired.Channel + "-test"
	}
	if strings.HasPrefix(desired.CredentialRef, "cred_") && strings.TrimSpace(desired.CredentialKind) == "" {
		desired.CredentialKind = "test-secret"
	}
	targetStatus := desired.Status
	if targetStatus == "" {
		targetStatus = app.NotificationBindingActive
	}
	created, err := repository.CreateNotificationBinding(t.Context(), app.NotificationBinding{
		ID: desired.ID, OwnerID: desired.OwnerID, ActorID: desired.ActorID,
		Channel: desired.Channel, Provider: desired.Provider, Status: app.NotificationBindingStarting,
		Scopes: append([]string(nil), desired.Scopes...), CredentialKind: desired.CredentialKind,
	})
	if err != nil {
		t.Fatalf("create notification binding fixture: %v", err)
	}
	if targetStatus == app.NotificationBindingStarting {
		return created
	}
	desired.ID, desired.OwnerID, desired.ActorID = created.ID, created.OwnerID, created.ActorID
	desired.Channel, desired.Provider = created.Channel, created.Provider
	desired.Status = targetStatus
	return mustUpdateNotificationBindingFixture(t, repository, created, desired)
}

func mustUpdateNotificationBindingFixture(t testing.TB, repository ConnectorRepository, previous, replacement app.NotificationBinding) app.NotificationBinding {
	t.Helper()
	if previous.Status == app.NotificationBindingActive && replacement.Status == app.NotificationBindingRevoked {
		revoking := previous
		revoking.Status = app.NotificationBindingRevoking
		previous = mustUpdateNotificationBindingFixture(t, repository, previous, revoking)
	}
	updated, err := repository.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(previous, replacement))
	if err != nil {
		t.Fatalf("update notification binding fixture: %v", err)
	}
	return updated
}

func mustGetNotificationBindingFixture(t testing.TB, repository ConnectorRepository, id string) (app.NotificationBinding, bool) {
	t.Helper()
	binding, found, err := repository.GetNotificationBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("get notification binding fixture: %v", err)
	}
	return binding, found
}

func mustListNotificationBindingsFixture(t testing.TB, repository ConnectorRepository, channel, status string) []app.NotificationBinding {
	t.Helper()
	bindings, err := repository.ListNotificationBindings(t.Context(), channel, status)
	if err != nil {
		t.Fatalf("list notification binding fixtures: %v", err)
	}
	return bindings
}
