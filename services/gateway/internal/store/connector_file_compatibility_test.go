package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func writeConnectorSnapshotFixture(t *testing.T, path string, snapshot Snapshot) {
	t.Helper()
	raw, err := (osFileCommitOps{}).Encode(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileConnectorStartupAppliesOnlyAcceptedLegacyNormalization(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "connector.json")
	setting := app.ConnectorSetting{
		Channel: "weixin", Enabled: true, UpdatedBy: app.DefaultOwnerID, UpdatedAt: now,
	}
	binding := app.NotificationBinding{
		ID: "binding-legacy", OwnerID: app.DefaultOwnerID, Channel: "weixin", Provider: "legacy-weixin",
		Status: app.NotificationBindingActive, CredentialRef: "config:legacy.weixin.token",
		CreatedAt: now, UpdatedAt: now,
	}
	writeConnectorSnapshotFixture(t, path, Snapshot{
		ConnectorSettings: map[string]app.ConnectorSetting{
			connectorSettingKey(app.DefaultOwnerID, setting.Channel): setting,
		},
		NotificationBindings: map[string]app.NotificationBinding{binding.ID: binding},
	})
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	gotSetting, found, err := st.GetConnectorSetting(t.Context(), app.DefaultOwnerID, setting.Channel)
	if err != nil || !found || gotSetting.OwnerID != app.DefaultOwnerID || gotSetting.Version != 1 {
		t.Fatalf("legacy setting=%#v found=%v err=%v", gotSetting, found, err)
	}
	gotBinding, found, err := st.GetNotificationBinding(t.Context(), binding.ID)
	if err != nil || !found || gotBinding.ActorID != app.DefaultOwnerID || gotBinding.Version != 1 || gotBinding.CredentialKind != "" {
		t.Fatalf("legacy binding=%#v found=%v err=%v", gotBinding, found, err)
	}
}

func TestFileConnectorStartupRejectsGlobalBindingCorruption(t *testing.T) {
	now := time.Date(2026, 8, 21, 13, 0, 0, 0, time.UTC)
	base := app.NotificationBinding{
		OwnerID: "owner", ActorID: "actor", Channel: "telegram", Provider: "telegram-bot-api",
		Status: app.NotificationBindingActive, CredentialKind: "bot-token", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	for _, testCase := range []struct {
		name     string
		bindings map[string]app.NotificationBinding
		contains string
	}{
		{
			name: "duplicate Vault ref",
			bindings: func() map[string]app.NotificationBinding {
				first := base
				first.ID, first.CredentialRef = "binding-first", "cred_duplicate"
				second := base
				second.ID, second.CredentialRef = "binding-second", "cred_duplicate"
				return map[string]app.NotificationBinding{first.ID: first, second.ID: second}
			}(),
			contains: "share Vault credential ref",
		},
		{
			name: "multiple active defaults",
			bindings: func() map[string]app.NotificationBinding {
				first := base
				first.ID, first.CredentialRef, first.DefaultForChannel = "binding-first", "config:first", true
				second := base
				second.ID, second.CredentialRef, second.DefaultForChannel = "binding-second", "config:second", true
				return map[string]app.NotificationBinding{first.ID: first, second.ID: second}
			}(),
			contains: "both active defaults",
		},
		{
			name: "key identity mismatch",
			bindings: func() map[string]app.NotificationBinding {
				binding := base
				binding.ID, binding.CredentialRef = "embedded-id", "config:one"
				return map[string]app.NotificationBinding{"different-key": binding}
			}(),
			contains: "does not match embedded ID",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "connector.json")
			writeConnectorSnapshotFixture(t, path, Snapshot{NotificationBindings: testCase.bindings})
			_, err := NewFileStore(path)
			if err == nil || !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("startup error=%v, want %q", err, testCase.contains)
			}
		})
	}
}
