package storetest

import (
	"context"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func MustCreateNotificationBinding(t testing.TB, repository store.ConnectorRepository, desired app.NotificationBinding) app.NotificationBinding {
	t.Helper()
	binding, err := CreateNotificationBinding(t.Context(), repository, desired)
	if err != nil {
		t.Fatalf("create notification binding fixture: %v", err)
	}
	return binding
}

func CreateNotificationBinding(ctx context.Context, repository store.ConnectorRepository, desired app.NotificationBinding) (app.NotificationBinding, error) {
	desired.ID = strings.TrimSpace(desired.ID)
	if desired.ID == "" {
		desired.ID = app.NewID("bind_test")
	}
	desired.OwnerID = strings.TrimSpace(desired.OwnerID)
	if desired.OwnerID == "" {
		desired.OwnerID = app.DefaultOwnerID
	}
	desired.ActorID = strings.TrimSpace(desired.ActorID)
	if desired.ActorID == "" {
		desired.ActorID = desired.OwnerID
	}
	desired.Channel = strings.ToLower(strings.TrimSpace(desired.Channel))
	if desired.Channel == "" {
		desired.Channel = "test"
	}
	desired.Provider = strings.TrimSpace(desired.Provider)
	if desired.Provider == "" {
		desired.Provider = desired.Channel + "-test"
	}
	if strings.HasPrefix(strings.TrimSpace(desired.CredentialRef), "cred_") && strings.TrimSpace(desired.CredentialKind) == "" {
		desired.CredentialKind = "test-secret"
	}
	targetStatus := strings.TrimSpace(desired.Status)
	if targetStatus == "" {
		targetStatus = app.NotificationBindingActive
	}
	starting := app.NotificationBinding{
		ID: desired.ID, OwnerID: desired.OwnerID, ActorID: desired.ActorID,
		Channel: desired.Channel, Provider: desired.Provider, Status: app.NotificationBindingStarting,
		Scopes: append([]string(nil), desired.Scopes...), CredentialKind: strings.TrimSpace(desired.CredentialKind),
	}
	current, err := repository.CreateNotificationBinding(ctx, starting)
	if err != nil || targetStatus == app.NotificationBindingStarting {
		return current, err
	}

	transition := func(status string, final bool) error {
		replacement := current
		if final {
			replacement = desired
			replacement.ID = current.ID
			replacement.OwnerID = current.OwnerID
			replacement.ActorID = current.ActorID
			replacement.Channel = current.Channel
			replacement.Provider = current.Provider
			replacement.CreatedAt = current.CreatedAt
			replacement.Version = current.Version
			replacement.UpdatedAt = current.UpdatedAt
			replacement.RevokedAt = nil
		}
		replacement.Status = status
		updated, updateErr := repository.UpdateNotificationBinding(ctx, store.NewNotificationBindingUpdate(current, replacement))
		if updateErr == nil {
			current = updated
		}
		return updateErr
	}

	switch targetStatus {
	case app.NotificationBindingWaitingScan, app.NotificationBindingWaitingConfirm,
		app.NotificationBindingActive, app.NotificationBindingFailed:
		err = transition(targetStatus, true)
	case app.NotificationBindingCredentialPending:
		if err = transition(app.NotificationBindingWaitingScan, false); err == nil {
			err = transition(targetStatus, true)
		}
	case app.NotificationBindingExpired:
		if err = transition(app.NotificationBindingWaitingScan, false); err == nil {
			err = transition(targetStatus, true)
		}
	case app.NotificationBindingRevoking, app.NotificationBindingRevoked:
		if strings.TrimSpace(desired.CredentialRef) != "" {
			active := desired
			active.Status = app.NotificationBindingActive
			active.DefaultForChannel = false
			active.RevokedAt = nil
			priorDesired := desired
			desired = active
			err = transition(app.NotificationBindingActive, true)
			desired = priorDesired
		}
		if err == nil {
			err = transition(app.NotificationBindingRevoking, targetStatus == app.NotificationBindingRevoking)
		}
		if err == nil && targetStatus == app.NotificationBindingRevoked {
			err = transition(app.NotificationBindingRevoked, true)
		}
	default:
		err = store.ErrConnectorSettingConflict
	}
	return current, err
}

func MustUpdateNotificationBinding(t testing.TB, repository store.ConnectorRepository, previous, replacement app.NotificationBinding) app.NotificationBinding {
	t.Helper()
	if previous.Status == app.NotificationBindingActive && replacement.Status == app.NotificationBindingRevoked {
		revoking := previous
		revoking.Status = app.NotificationBindingRevoking
		previous = MustUpdateNotificationBinding(t, repository, previous, revoking)
	}
	updated, err := repository.UpdateNotificationBinding(t.Context(), store.NewNotificationBindingUpdate(previous, replacement))
	if err != nil {
		t.Fatalf("update notification binding fixture: %v", err)
	}
	return updated
}

func MustGetNotificationBinding(t testing.TB, repository store.ConnectorRepository, id string) (app.NotificationBinding, bool) {
	t.Helper()
	binding, found, err := repository.GetNotificationBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("get notification binding fixture: %v", err)
	}
	return binding, found
}

func MustListNotificationBindings(t testing.TB, repository store.ConnectorRepository, channel, status string) []app.NotificationBinding {
	t.Helper()
	bindings, err := repository.ListNotificationBindings(t.Context(), channel, status)
	if err != nil {
		t.Fatalf("list notification binding fixtures: %v", err)
	}
	return bindings
}
