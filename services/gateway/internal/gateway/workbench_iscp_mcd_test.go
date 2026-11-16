package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

type mcdFixtureRunner struct{}

func (mcdFixtureRunner) Validate(context.Context, mcdintegration.Envelope) error { return nil }
func (mcdFixtureRunner) Read(context.Context, mcdintegration.Envelope, mcdintegration.Execution, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"schema_version":1,"result_encoding":"bounded-json-v1","result_json":"{}"}`), nil
}

func mcdFixtureEnvelope(t *testing.T, binding *wb.Binding) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/session-envelope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("fixture")
	}
	i := value["identity"].(map[string]any)
	i["deployment_id"] = binding.DeploymentID
	i["owner_id"] = binding.OwnerID
	i["client_id"] = binding.ClientID
	i["installation_id"] = iscpTestInstallation
	value["issued_at_ms"] = time.Now().UnixMilli() - 1000
	value["session_not_after_ms"] = time.Now().UnixMilli() + 60000
	delete(value, "envelope_digest")
	raw, _ = json.Marshal(value)
	canonical, err := mcdintegration.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(canonical)
	value["envelope_digest"] = hex.EncodeToString(hash[:])
	raw, _ = json.Marshal(value)
	return raw
}

func TestMCDSessionEncryptedChannelRecoveryAndNoGenericJournal(t *testing.T) {
	server, repo, local, _ := workbenchISCPFixture(t, nil)
	vault := credential.New(repo, credential.Options{Key: strings.Repeat("07", 32)})
	manager, err := mcdintegration.NewManager(vault, mcdFixtureRunner{}, local.Binding.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	server.mcdSelection = manager
	ops := []string{wb.OperationMCDSessionProvision, wb.OperationMCDSessionRevoke, wb.OperationMCDSessionStatus}
	f := startV2EncryptedFixture(t, server, local, []string{"workbench.mcd.selection"}, ops)
	f.bind(t)
	raw := mcdFixtureEnvelope(t, local.Binding)
	first := f.call(t, v2Request(wb.OperationMCDSessionProvision, raw))
	requireV2Status(t, first, 200)
	// A new module instance discovers the original receipt/material in Vault.
	server.mcdSelection, err = mcdintegration.NewManager(vault, mcdFixtureRunner{}, local.Binding.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	replay := f.call(t, v2Request(wb.OperationMCDSessionProvision, raw))
	requireV2Status(t, replay, 200)
	if !bytes.Equal(first.Body, replay.Body) {
		t.Fatal("original receipt changed after restart")
	}
	status := f.jsonCall(t, wb.OperationMCDSessionStatus, map[string]any{})
	requireV2Status(t, status, 200)
	if !bytes.Contains(status.Body, []byte(`"state":"ready"`)) || bytes.Contains(status.Body, []byte("material")) {
		t.Fatal("status projection")
	}
	var e mcdintegration.Envelope
	if json.Unmarshal(raw, &e) != nil {
		t.Fatal("fixture")
	}
	revoke := map[string]any{"identity": e.Identity, "connection_id": e.ConnectionID, "request_id": "disconnect"}
	requireV2Status(t, f.jsonCall(t, wb.OperationMCDSessionRevoke, revoke), 200)
	status = f.jsonCall(t, wb.OperationMCDSessionStatus, map[string]any{})
	requireV2Status(t, status, 200)
	if !bytes.Contains(status.Body, []byte(`"state":"revoked"`)) {
		t.Fatal("revocation not visible")
	}
	// Missing per-capability qualification must fail even on an authenticated
	// channel. The API is not admitted just because the transport knows its name.
	server2, repo2, local2, _ := workbenchISCPFixture(t, nil)
	server2.mcdSelection, _ = mcdintegration.NewManager(credential.New(repo2, credential.Options{Key: strings.Repeat("08", 32)}), mcdFixtureRunner{}, local2.Binding.DeploymentID)
	f2 := startV2EncryptedFixture(t, server2, local2, nil, nil)
	f2.bind(t)
	requireV2Status(t, f2.jsonCall(t, wb.OperationMCDSessionStatus, map[string]any{}), 403)
	// The canary must occur only inside encrypted Vault and wire frames, never
	// generic operation receipts, response bodies or unencrypted state files.
	canary := []byte("SYNTHETIC")
	if bytes.Contains(first.Body, canary) || bytes.Contains(status.Body, canary) {
		t.Fatal("credential response leak")
	}
	if err = filepath.WalkDir(filepath.Dir(server.cfg.State.Path), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, canary) {
			t.Error("credential leaked to local state")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.network.mu.Lock()
	defer f.network.mu.Unlock()
	for _, wire := range f.network.envelopes {
		if bytes.Contains(wire, canary) {
			t.Fatal("relay credential leak")
		}
	}
}
