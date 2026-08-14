package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryStoreConnectorSettingUsesCASAndOwnerScope(t *testing.T) {
	st := NewMemoryStore()
	created, err := st.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: " Telegram ", Enabled: true, ISCPEnabled: true, LANAccessEnabled: true, UpdatedBy: "owner",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Channel != "telegram" || !created.Enabled || !created.ISCPEnabled || !created.LANAccessEnabled || created.Version != 1 || created.UpdatedAt.IsZero() {
		t.Fatalf("unexpected connector setting: %#v", created)
	}
	if _, err := st.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "telegram", Enabled: false,
	}, 0); !errors.Is(err, ErrConnectorSettingConflict) {
		t.Fatalf("stale connector update error = %v", err)
	}
	updated, err := st.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "telegram", Enabled: false, ISCPEnabled: true, LANAccessEnabled: true, UpdatedBy: "client-a",
	}, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Version != 2 || updated.UpdatedBy != "client-a" {
		t.Fatalf("unexpected updated connector setting: %#v", updated)
	}
	if settings := st.ListConnectorSettings("another-owner"); len(settings) != 0 {
		t.Fatalf("connector settings crossed owner scope: %#v", settings)
	}
	if !hasAuditType(st.ListAudit(""), "connector.enabled") || !hasAuditType(st.ListAudit(""), "connector.disabled") {
		t.Fatalf("connector changes were not audited: %#v", st.ListAudit(""))
	}
}

func TestFileStorePersistsConnectorSettingVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connector-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "weixin", Enabled: true, ISCPEnabled: true, LANAccessEnabled: true, UpdatedBy: app.DefaultOwnerID,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.GetConnectorSetting(app.DefaultOwnerID, "weixin")
	if !ok || got.Version != created.Version || !got.Enabled || !got.ISCPEnabled || !got.LANAccessEnabled || got.UpdatedBy != app.DefaultOwnerID {
		t.Fatalf("connector setting did not round trip: %#v ok=%v", got, ok)
	}
	if _, err := reloaded.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "weixin", Enabled: false,
	}, 0); !errors.Is(err, ErrConnectorSettingConflict) {
		t.Fatalf("reloaded connector CAS error = %v", err)
	}
}
