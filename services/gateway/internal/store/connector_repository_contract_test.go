package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type connectorContractBackend struct {
	name  string
	store testBackend
}

func newConnectorContractBackends(t *testing.T) []connectorContractBackend {
	t.Helper()
	fileStore, err := NewFileStore(filepath.Join(t.TempDir(), "connector-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	return []connectorContractBackend{
		{name: "memory", store: NewMemoryStore()},
		{name: "file", store: fileStore},
	}
}

func TestConnectorRepositorySettingContract(t *testing.T) {
	for _, backend := range newConnectorContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := backend.store.GetConnectorSetting(ctx, "owner", "alpha"); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled read error=%v code=%q", err, StoreErrorCodeOf(err))
			}
			if settings, err := backend.store.ListConnectorSettings(t.Context(), "owner"); err != nil || settings == nil || len(settings) != 0 {
				t.Fatalf("empty owner list=%#v err=%v", settings, err)
			}
			if settings, err := backend.store.ListAllConnectorSettings(t.Context()); err != nil || settings == nil || len(settings) != 0 {
				t.Fatalf("empty global list=%#v err=%v", settings, err)
			}

			alpha, err := backend.store.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
				OwnerID: " owner-b ", Channel: " Alpha ", Enabled: true, UpdatedBy: " actor ",
			}, 0)
			if err != nil || alpha.OwnerID != "owner-b" || alpha.Channel != "alpha" || alpha.UpdatedBy != "actor" || alpha.Version != 1 || alpha.UpdatedAt.IsZero() {
				t.Fatalf("create setting=%#v err=%v", alpha, err)
			}
			if _, err := backend.store.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner-b", Channel: "alpha"}, 0); !errors.Is(err, ErrConnectorSettingConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("duplicate setting error=%v code=%q", err, StoreErrorCodeOf(err))
			}
			updated, err := backend.store.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
				OwnerID: "owner-b", Channel: "alpha", Enabled: false, UpdatedBy: "actor",
			}, alpha.Version)
			if err != nil || updated.Version != 2 || !updated.UpdatedAt.After(alpha.UpdatedAt) {
				t.Fatalf("update setting=%#v err=%v", updated, err)
			}
			for _, setting := range []app.ConnectorSetting{
				{OwnerID: "owner-a", Channel: "zeta", Enabled: true},
				{OwnerID: "owner-a", Channel: "beta", Enabled: true},
			} {
				if _, err := backend.store.UpdateConnectorSetting(t.Context(), setting, 0); err != nil {
					t.Fatal(err)
				}
			}
			ownerSettings, err := backend.store.ListConnectorSettings(t.Context(), "owner-a")
			if err != nil || len(ownerSettings) != 2 || ownerSettings[0].Channel != "beta" || ownerSettings[1].Channel != "zeta" {
				t.Fatalf("owner ordering=%#v err=%v", ownerSettings, err)
			}
			all, err := backend.store.ListAllConnectorSettings(t.Context())
			if err != nil || len(all) != 3 || all[0].OwnerID != "owner-a" || all[0].Channel != "beta" || all[2].OwnerID != "owner-b" {
				t.Fatalf("global ordering=%#v err=%v", all, err)
			}
		})
	}
}

func TestConnectorRepositoryBindingLifecycleContract(t *testing.T) {
	for _, backend := range newConnectorContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			first := mustCreateNotificationBindingFixture(t, backend.store, app.NotificationBinding{
				ID: "binding-first", OwnerID: "owner", ActorID: "actor", Channel: "telegram",
				Provider: "telegram-bot-api", Status: app.NotificationBindingStarting,
				Scopes: []string{app.BindingScopeMessageSendSelf}, CredentialKind: "bot-token",
			})
			if first.Version != 1 || first.CreatedAt.IsZero() || !first.CreatedAt.Equal(first.UpdatedAt) {
				t.Fatalf("starting binding=%#v", first)
			}
			if _, err := backend.store.CreateNotificationBinding(t.Context(), app.NotificationBinding{
				ID: first.ID, OwnerID: first.OwnerID, ActorID: first.ActorID, Channel: first.Channel,
				Provider: first.Provider, Status: app.NotificationBindingStarting,
			}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("duplicate binding error=%v code=%q", err, StoreErrorCodeOf(err))
			}

			activeCandidate := first
			activeCandidate.Status = app.NotificationBindingActive
			activeCandidate.CredentialRef = "cred_shared-proof"
			activeCandidate.DefaultForChannel = true
			firstActive, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(first, activeCandidate))
			if err != nil || firstActive.Status != app.NotificationBindingActive || firstActive.Version != 2 || !firstActive.DefaultForChannel {
				t.Fatalf("activate first=%#v err=%v", firstActive, err)
			}

			conflicting := mustCreateNotificationBindingFixture(t, backend.store, app.NotificationBinding{
				ID: "binding-conflict", OwnerID: "owner", ActorID: "actor", Channel: "telegram",
				Provider: "telegram-bot-api", Status: app.NotificationBindingStarting, CredentialKind: "bot-token",
			})
			conflictingActive := conflicting
			conflictingActive.Status = app.NotificationBindingActive
			conflictingActive.CredentialRef = firstActive.CredentialRef
			if candidate, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(conflicting, conflictingActive)); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("shared Vault ref candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}

			second := mustCreateNotificationBindingFixture(t, backend.store, app.NotificationBinding{
				ID: "binding-second", OwnerID: "owner", ActorID: "actor", Channel: "telegram",
				Provider: "telegram-bot-api", Status: app.NotificationBindingStarting, CredentialKind: "bot-token",
			})
			secondActiveCandidate := second
			secondActiveCandidate.Status = app.NotificationBindingActive
			secondActiveCandidate.CredentialRef = "cred_second-proof"
			secondActiveCandidate.DefaultForChannel = true
			secondActive, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(second, secondActiveCandidate))
			if err != nil || !secondActive.DefaultForChannel {
				t.Fatalf("activate second=%#v err=%v", secondActive, err)
			}
			firstAfterDemotion, found, err := backend.store.GetNotificationBinding(t.Context(), first.ID)
			if err != nil || !found || firstAfterDemotion.DefaultForChannel || firstAfterDemotion.Version != firstActive.Version+1 || !firstAfterDemotion.UpdatedAt.Equal(secondActive.UpdatedAt) {
				t.Fatalf("default demotion=%#v found=%v err=%v", firstAfterDemotion, found, err)
			}

			revoking := secondActive
			revoking.Status = app.NotificationBindingRevoking
			revoking.DefaultForChannel = false
			secondRevoking, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(secondActive, revoking))
			if err != nil || secondRevoking.CredentialRef != secondActive.CredentialRef || secondRevoking.Status != app.NotificationBindingRevoking {
				t.Fatalf("revoking=%#v err=%v", secondRevoking, err)
			}
			revoked := secondRevoking
			revoked.Status = app.NotificationBindingRevoked
			secondRevoked, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(secondRevoking, revoked))
			if err != nil || secondRevoked.RevokedAt == nil || secondRevoked.CredentialRef != secondActive.CredentialRef {
				t.Fatalf("revoked=%#v err=%v", secondRevoked, err)
			}
			if candidate, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(secondRevoked, secondActive)); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("terminal transition candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}
			if candidate, err := backend.store.CreateNotificationBinding(t.Context(), app.NotificationBinding{
				ID: secondRevoked.ID, OwnerID: secondRevoked.OwnerID, ActorID: secondRevoked.ActorID,
				Channel: secondRevoked.Channel, Provider: secondRevoked.Provider, Status: app.NotificationBindingStarting,
			}); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("terminal ID reuse candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}
			third := mustCreateNotificationBindingFixture(t, backend.store, app.NotificationBinding{
				ID: "binding-terminal-ref", OwnerID: "owner", ActorID: "actor", Channel: "telegram",
				Provider: "telegram-bot-api", Status: app.NotificationBindingStarting, CredentialKind: "bot-token",
			})
			thirdActive := third
			thirdActive.Status = app.NotificationBindingActive
			thirdActive.CredentialRef = secondRevoked.CredentialRef
			if candidate, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(third, thirdActive)); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("terminal ref reuse candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}

			listed, err := backend.store.ListNotificationBindings(t.Context(), "telegram", "")
			if err != nil || listed == nil || len(listed) != 4 {
				t.Fatalf("binding list=%#v err=%v", listed, err)
			}
			for index := 1; index < len(listed); index++ {
				if listed[index].UpdatedAt.After(listed[index-1].UpdatedAt) {
					t.Fatalf("binding order is not newest first: %#v", listed)
				}
			}
			listed[0].Scopes = append(listed[0].Scopes, "mutated")
			again, _, err := backend.store.GetNotificationBinding(t.Context(), listed[0].ID)
			if err != nil || len(again.Scopes) == len(listed[0].Scopes) {
				t.Fatalf("binding scopes alias escaped: %#v err=%v", again, err)
			}
		})
	}
}

func TestConnectorRepositoryBindingInputAndFieldMasks(t *testing.T) {
	for _, backend := range newConnectorContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			for _, testCase := range []struct {
				name   string
				mutate func(*app.NotificationBinding)
			}{
				{name: "version", mutate: func(binding *app.NotificationBinding) { binding.Version = 1 }},
				{name: "created at", mutate: func(binding *app.NotificationBinding) { binding.CreatedAt = time.Now().UTC() }},
				{name: "updated at", mutate: func(binding *app.NotificationBinding) { binding.UpdatedAt = time.Now().UTC() }},
			} {
				t.Run("create rejects "+testCase.name, func(t *testing.T) {
					request := app.NotificationBinding{
						ID: "binding-create-" + strings.ReplaceAll(testCase.name, " ", "-"), OwnerID: "owner", ActorID: "actor",
						Channel: "weixin", Provider: "openclaw-weixin-qr", Status: app.NotificationBindingStarting,
					}
					testCase.mutate(&request)
					if candidate, err := backend.store.CreateNotificationBinding(t.Context(), request); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorInvalid {
						t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
					}
				})
			}

			starting, err := backend.store.CreateNotificationBinding(t.Context(), app.NotificationBinding{
				ID: "binding-field-mask", OwnerID: "owner", ActorID: "actor", Channel: "weixin",
				Provider: "openclaw-weixin-qr", Status: app.NotificationBindingStarting,
				Scopes: []string{app.BindingScopeMessageSendSelf}, CredentialKind: "openclaw-weixin-bot-token",
			})
			if err != nil {
				t.Fatal(err)
			}
			waitingCandidate := starting
			waitingCandidate.Status = app.NotificationBindingWaitingScan
			waitingCandidate.DisplayName = "Waiting account"
			waitingCandidate.BaseURL = "https://provider.example"
			waitingCandidate.ProviderSessionID = "provider-session"
			waitingCandidate.ProviderState = "provider-state"
			waitingCandidate.QRCodeURL = "https://provider.example/qr"
			waitingCandidate.QRCodeImage = "data:image/png;base64,AA"
			expires := time.Now().UTC().Add(time.Hour)
			waitingCandidate.ExpiresAt = &expires
			waiting, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(starting, waitingCandidate))
			if err != nil {
				t.Fatal(err)
			}
			for _, testCase := range []struct {
				name   string
				mutate func(*app.NotificationBinding)
			}{
				{name: "scopes", mutate: func(binding *app.NotificationBinding) { binding.Scopes = []string{app.BindingScopeReminderSendSelf} }},
				{name: "context", mutate: func(binding *app.NotificationBinding) { binding.ContextToken = "forbidden" }},
				{name: "delivery identity", mutate: func(binding *app.NotificationBinding) { binding.ExternalUserID = "forbidden" }},
			} {
				t.Run("waiting rejects "+testCase.name, func(t *testing.T) {
					replacement := waiting
					testCase.mutate(&replacement)
					if candidate, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(waiting, replacement)); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorInvalid {
						t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
					}
				})
			}
			waitingConfirm := waiting
			waitingConfirm.Status = app.NotificationBindingWaitingConfirm
			waitingConfirm.DisplayName = "Confirmed account"
			waitingConfirm.ProviderState = "confirmed"
			waitingConfirm.LastError = "connector_provider_pending"
			waiting, err = backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(waiting, waitingConfirm))
			if err != nil {
				t.Fatal(err)
			}

			activeCandidate := waiting
			activeCandidate.Status = app.NotificationBindingActive
			activeCandidate.ExternalUserID = "user"
			activeCandidate.AccountID = "account"
			activeCandidate.CredentialRef = "cred_field-mask"
			activeCandidate.QRCodeURL = ""
			activeCandidate.QRCodeImage = ""
			activeCandidate.ExpiresAt = nil
			activeCandidate.LastError = ""
			active, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(waiting, activeCandidate))
			if err != nil {
				t.Fatal(err)
			}
			allowedActive := active
			allowedActive.ContextToken = "context"
			allowedActive.ProviderCursor = "cursor"
			allowedActive.Scopes = []string{app.BindingScopeReminderSendSelf}
			allowedActive.LastError = "connector_sync_failed"
			active, err = backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(active, allowedActive))
			if err != nil || active.ContextToken != "context" || active.ProviderCursor != "cursor" {
				t.Fatalf("allowed active update=%#v err=%v", active, err)
			}
			forbiddenActive := active
			forbiddenActive.ProviderState = "replaced-after-activation"
			if candidate, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(active, forbiddenActive)); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("forbidden active candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}
		})
	}
}

func TestConnectorRepositoryTimestampHighWaterSurvivesClockRollback(t *testing.T) {
	for _, backend := range newConnectorContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			future := time.Date(2030, 1, 2, 3, 4, 5, 6000, time.UTC)
			past := future.Add(-24 * time.Hour)
			setConnectorContractClock(t, backend.store, func() time.Time { return future })
			created, err := backend.store.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
			if err != nil {
				t.Fatal(err)
			}
			setConnectorContractClock(t, backend.store, func() time.Time { return past })
			updated, err := backend.store.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha", Enabled: true}, created.Version)
			if err != nil || !updated.UpdatedAt.After(created.UpdatedAt) {
				t.Fatalf("setting high-water created=%s updated=%s err=%v", created.UpdatedAt, updated.UpdatedAt, err)
			}

			starting, err := backend.store.CreateNotificationBinding(t.Context(), app.NotificationBinding{
				ID: "binding-clock", OwnerID: "owner", ActorID: "actor", Channel: "telegram",
				Provider: "telegram-bot-api", Status: app.NotificationBindingStarting,
			})
			if err != nil {
				t.Fatal(err)
			}
			waiting := starting
			waiting.Status = app.NotificationBindingWaitingConfirm
			updatedBinding, err := backend.store.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(starting, waiting))
			if err != nil || !updatedBinding.UpdatedAt.After(starting.UpdatedAt) {
				t.Fatalf("binding high-water starting=%s updated=%s err=%v", starting.UpdatedAt, updatedBinding.UpdatedAt, err)
			}
		})
	}
}

func setConnectorContractClock(t *testing.T, st testBackend, now func() time.Time) {
	t.Helper()
	switch concrete := st.(type) {
	case *MemoryStore:
		concrete.connectorNow = now
	case *FileStore:
		concrete.inner.connectorNow = now
	default:
		t.Fatalf("unsupported connector contract backend %T", st)
	}
}
