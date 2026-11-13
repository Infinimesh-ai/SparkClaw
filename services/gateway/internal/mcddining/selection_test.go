package mcddining

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type selectionRunner struct {
	calls     int
	args      map[string]any
	execution mcdintegration.Execution
}

func (r *selectionRunner) Validate(context.Context, mcdintegration.Envelope) error { return nil }
func (r *selectionRunner) Read(_ context.Context, _ mcdintegration.Envelope, x mcdintegration.Execution, _ string, args json.RawMessage) (json.RawMessage, error) {
	r.calls++
	r.execution = x
	_ = json.Unmarshal(args, &r.args)
	return json.RawMessage(`{"schema_version":1,"result_encoding":"bounded-json-v1","result_json":"{\"store_code\":\"3330613\"}"}`), nil
}
func TestSelectionBindsAccountAndRejectsCredentialOrPaymentArguments(t *testing.T) {
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/session-envelope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["issued_at_ms"] = time.Now().UnixMilli() - 1000
	fields["session_not_after_ms"] = time.Now().UnixMilli() + 60000
	fields["operations"] = []string{"store_get", "stores_search"}
	delete(fields, "envelope_digest")
	raw, _ = json.Marshal(fields)
	canonical, err := mcdintegration.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	fields["envelope_digest"] = hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(fields)
	e, err := mcdintegration.DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	runner := &selectionRunner{}
	m, err := mcdintegration.NewManager(credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("11", 32)}), runner, e.Identity.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	i := e.Identity
	p := mcdintegration.Principal{DeploymentID: i.DeploymentID, OwnerID: i.OwnerID, ClientID: i.ClientID, InstallationID: i.InstallationID}
	if _, err = m.Provision(t.Context(), p, raw); err != nil {
		t.Fatal(err)
	}
	s, err := NewSelection(t.Context(), m, p, "execution")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"operation":"order_pay"}`, `{"operation":"store_get","account":"foreign"}`, `{"operation":"store_get","token":"private"}`} {
		if _, err = s.Read(t.Context(), []byte(bad)); err == nil {
			t.Fatal("forbidden input accepted")
		}
	}
	if runner.calls != 0 {
		t.Fatal("forbidden input reached helper")
	}
	if _, err = s.Read(t.Context(), []byte(`{"operation":"stores_search","city_code":"杭州","keyword":"佳宝"}`)); err != nil {
		t.Fatal(err)
	}
	if runner.args["account"] != i.AccountRef || runner.args["city"] != "杭州" || runner.execution.Identity != i {
		t.Fatal("server identity not used")
	}
	if _, err = m.Revoke(t.Context(), p, i, e.ConnectionID, "disconnect"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(t.Context(), []byte(`{"operation":"store_get","store_code":"3330613"}`)); err == nil {
		t.Fatal("closed selection remained usable")
	}
	if runner.calls != 1 {
		t.Fatal("closed session reached helper")
	}
}
