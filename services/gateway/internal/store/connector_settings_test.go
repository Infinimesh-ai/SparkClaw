package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryStoreConnectorSettingUsesCASAndOwnerScope(t *testing.T) {
	st := NewMemoryStore()
	created, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: " Telegram ", Enabled: true, ISCPEnabled: true, LANAccessEnabled: true, UpdatedBy: "owner",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Channel != "telegram" || !created.Enabled || !created.ISCPEnabled || !created.LANAccessEnabled || created.Version != 1 || created.UpdatedAt.IsZero() {
		t.Fatalf("unexpected connector setting: %#v", created)
	}
	if _, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "telegram", Enabled: false,
	}, 0); !errors.Is(err, ErrConnectorSettingConflict) {
		t.Fatalf("stale connector update error = %v", err)
	}
	updated, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "telegram", Enabled: false, ISCPEnabled: true, LANAccessEnabled: true, UpdatedBy: "client-a",
	}, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Version != 2 || updated.UpdatedBy != "client-a" {
		t.Fatalf("unexpected updated connector setting: %#v", updated)
	}
	settings, err := st.ListConnectorSettings(t.Context(), "another-owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 0 {
		t.Fatalf("connector settings crossed owner scope: %#v", settings)
	}
	if !hasAuditType(mustListAudit(t, st, ""), "connector.enabled") || !hasAuditType(mustListAudit(t, st, ""), "connector.disabled") {
		t.Fatalf("connector changes were not audited: %#v", mustListAudit(t, st, ""))
	}
}

func TestMemoryStoreListsAllConnectorSettingsInStableOwnerChannelOrder(t *testing.T) {
	st := NewMemoryStore()
	for _, setting := range []app.ConnectorSetting{
		{OwnerID: "owner-b", Channel: "weixin", Enabled: true},
		{OwnerID: "owner-a", Channel: "telegram", Enabled: true},
		{OwnerID: "owner-a", Channel: "mcp", Enabled: false},
	} {
		if _, err := st.UpdateConnectorSetting(t.Context(), setting, 0); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := st.ListAllConnectorSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 3 || settings[0].OwnerID != "owner-a" || settings[0].Channel != "mcp" ||
		settings[1].OwnerID != "owner-a" || settings[1].Channel != "telegram" ||
		settings[2].OwnerID != "owner-b" || settings[2].Channel != "weixin" {
		t.Fatalf("all-owner connector setting order = %#v", settings)
	}
}

func TestFileStorePersistsConnectorSettingVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connector-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "weixin", Enabled: true, ISCPEnabled: true, LANAccessEnabled: true, UpdatedBy: app.DefaultOwnerID,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := reloaded.GetConnectorSetting(t.Context(), app.DefaultOwnerID, "weixin")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Version != created.Version || !got.Enabled || !got.ISCPEnabled || !got.LANAccessEnabled || got.UpdatedBy != app.DefaultOwnerID {
		t.Fatalf("connector setting did not round trip: %#v ok=%v", got, ok)
	}
	if _, err := reloaded.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "weixin", Enabled: false,
	}, 0); !errors.Is(err, ErrConnectorSettingConflict) {
		t.Fatalf("reloaded connector CAS error = %v", err)
	}
	all, err := reloaded.ListAllConnectorSettings(t.Context())
	if err != nil || len(all) != 1 || all[0].OwnerID != app.DefaultOwnerID || all[0].Channel != "weixin" {
		t.Fatalf("file all-owner connector settings = %#v, %v", all, err)
	}
}
