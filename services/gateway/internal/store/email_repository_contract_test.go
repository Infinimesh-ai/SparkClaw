package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestEmailProviderSettingsContract(t *testing.T) {
	for _, backend := range []struct {
		name string
		new  func(*testing.T) ConnectorRepository
	}{
		{name: "memory", new: func(*testing.T) ConnectorRepository { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) ConnectorRepository {
			st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return st
		}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			checked := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
			gmail, err := repository.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{
				OwnerID: "owner-email", Provider: app.EmailProviderGmail, Enabled: true, Default: true,
				AccountHint: "用a***@gmail.com", State: app.EmailStateReady, LastCheckedAt: &checked,
			}, 0)
			if err != nil || gmail.Version != 1 || gmail.Account != app.EmailAccountDefault {
				t.Fatalf("create Gmail setting = %#v err=%v", gmail, err)
			}
			outlook, err := repository.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{
				OwnerID: "owner-email", Provider: app.EmailProviderOutlook, Enabled: true, Default: true,
				State: app.EmailStateLoginRequired,
			}, 0)
			if err != nil || !outlook.Default {
				t.Fatalf("create Outlook setting = %#v err=%v", outlook, err)
			}
			settings, err := repository.ListEmailProviderSettings(t.Context(), "owner-email")
			if err != nil || len(settings) != 2 || settings[0].Provider != app.EmailProviderGmail || settings[0].Default || !settings[1].Default {
				t.Fatalf("default demotion/list order = %#v err=%v", settings, err)
			}
			if settings[0].Version != 2 {
				t.Fatalf("demoted provider version = %d, want 2", settings[0].Version)
			}
			if _, err := repository.UpdateEmailProviderSetting(t.Context(), outlook, 0); !errors.Is(err, ErrEmailProviderSettingConflict) {
				t.Fatalf("stale update error = %v", err)
			}
		})
	}
}

func TestEmailProviderSettingsRejectUnsafeState(t *testing.T) {
	repository := NewMemoryStore()
	for _, setting := range []app.EmailProviderSetting{
		{Provider: "unknown", Enabled: true},
		{Provider: app.EmailProviderGmail, Enabled: false, Default: true},
		{Provider: app.EmailProviderGmail, Enabled: true, Account: "secondary"},
		{Provider: app.EmailProviderGmail, Enabled: true, AccountHint: "user@gmail.com"},
		{Provider: app.EmailProviderGmail, Enabled: true, AccountHint: "abc***@gmail.com"},
		{Provider: app.EmailProviderGmail, Enabled: true, State: app.EmailStateReady},
		{Provider: app.EmailProviderGmail, Enabled: true, ErrorCode: "Unsafe Error"},
	} {
		if _, err := repository.UpdateEmailProviderSetting(t.Context(), setting, 0); StoreErrorCodeOf(err) != StoreErrorInvalid {
			t.Fatalf("unsafe setting %#v error = %v", setting, err)
		}
	}
}

func TestEmailProviderSettingsCloneCheckTime(t *testing.T) {
	repository := NewMemoryStore()
	checked := time.Now().UTC()
	created, err := repository.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{
		Provider: app.EmailProviderQQMail, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	*created.LastCheckedAt = created.LastCheckedAt.Add(time.Hour)
	stored, ok, err := repository.GetEmailProviderSetting(t.Context(), app.DefaultOwnerID, app.EmailProviderQQMail)
	if err != nil || !ok || stored.LastCheckedAt.Equal(*created.LastCheckedAt) {
		t.Fatalf("stored check time aliases caller state: %#v ok=%v err=%v", stored, ok, err)
	}
}
