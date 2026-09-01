package integrationconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/localmind"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type unavailableBindingVault struct{}

func (unavailableBindingVault) Ready() error { return errors.New("vault unavailable") }
func (unavailableBindingVault) OpenBinding(context.Context, string, string) ([]byte, bool, error) {
	return nil, false, errors.New("vault unavailable")
}
func (unavailableBindingVault) ReplaceBinding(context.Context, string, string, []byte) error {
	return errors.New("vault unavailable")
}
func (unavailableBindingVault) DeleteBinding(context.Context, string, string) error {
	return errors.New("vault unavailable")
}

type localMindRuntimeStub struct {
	operatorConfigured    bool
	clearCalls            int
	activateOperatorCalls int
}

func (*localMindRuntimeStub) CheckCredentials(context.Context, localmind.Credentials) (localmind.Snapshot, error) {
	return localmind.Snapshot{}, nil
}
func (*localMindRuntimeStub) ActivateCredentials(context.Context, localmind.Credentials) (localmind.Snapshot, error) {
	return localmind.Snapshot{}, nil
}
func (s *localMindRuntimeStub) ActivateOperator(context.Context) (localmind.Snapshot, error) {
	s.activateOperatorCalls++
	return localmind.Snapshot{}, nil
}
func (s *localMindRuntimeStub) ClearRuntime() error {
	s.clearCalls++
	return nil
}
func (s *localMindRuntimeStub) OperatorConfigured() bool { return s.operatorConfigured }
func (*localMindRuntimeStub) Run(context.Context)        {}

func TestInitializeFailsClosedWhenCredentialVaultIsUnavailable(t *testing.T) {
	cfg := config.Default()
	cfg.Plugins.Entries.InfinimeshInfo.Config.BaseURL = "https://info.example"
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseID = "operator"
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey = "ilk_v1.operator.secret"
	st := store.NewMemoryStore()
	hub := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = hub.Close() })
	if !hub.InfoConfigured() {
		t.Fatal("test precondition: operator Info runtime was not configured")
	}
	localRuntime := &localMindRuntimeStub{operatorConfigured: true}
	controller := newController(cfg, unavailableBindingVault{}, st, hub, localRuntime)

	controller.Initialize(t.Context())

	if hub.InfoConfigured() {
		t.Fatal("vault failure silently retained the operator Info runtime")
	}
	if localRuntime.clearCalls != 1 || localRuntime.activateOperatorCalls != 0 {
		t.Fatalf("LocalMind clear=%d activate_operator=%d", localRuntime.clearCalls, localRuntime.activateOperatorCalls)
	}
	for _, id := range []string{InfoID, LocalMindID} {
		status, err := controller.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != StateVaultUnavailable || status.Source != SourceNone || status.Configured || !status.OperatorAvailable {
			t.Fatalf("%s did not fail closed: %#v", id, status)
		}
	}
}

func TestClassifyLocalMindCheckUsesTypedErrors(t *testing.T) {
	for _, test := range []struct {
		err      error
		wantCode string
	}{
		{err: localmind.ErrInvalidCredentials, wantCode: "credential_invalid"},
		{err: localmind.ErrContractInvalid, wantCode: "credential_contract_invalid"},
	} {
		if got := ErrorCode(classifyLocalMindCheck(test.err)); got != test.wantCode {
			t.Fatalf("error %v classified as %q, want %q", test.err, got, test.wantCode)
		}
	}
}

func TestInfoCredentialsAreValidatedRetainedAndExplicitlyActivated(t *testing.T) {
	info := newInfoCheckServer(t)
	cfg := config.Default()
	cfg.Plugins.Entries.InfinimeshInfo.Config.BaseURL = info.server.URL
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: integrationTestKey(1)})
	hub := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = hub.Close() })
	controller := New(cfg, vault, st, hub, nil)
	controller.Initialize(t.Context())

	first, err := controller.AddInfoCredential(t.Context(), AddInfoCredentialInput{
		Label: "Family account", LicenseID: "lic_a", LicenseKey: "ilk_v1.lic_a.secret-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Configured || first.ActiveCredentialID != "" || len(first.Credentials) != 1 || first.Credentials[0].Active {
		t.Fatalf("save unexpectedly activated credential: %#v", first)
	}
	second, err := controller.AddInfoCredential(t.Context(), AddInfoCredentialInput{
		Label: "Backup", LicenseID: "lic_b", LicenseKey: "ilk_v1.lic_b.secret-b",
	})
	if err != nil || len(second.Credentials) != 2 {
		t.Fatalf("second credential status=%#v err=%v", second, err)
	}
	beforeFailed := len(second.Credentials)
	info.reject.Store(true)
	failed, err := controller.AddInfoCredential(t.Context(), AddInfoCredentialInput{
		Label: "Rejected", LicenseID: "lic_bad", LicenseKey: "ilk_v1.lic_bad.secret-bad",
	})
	if ErrorCode(err) != "credential_auth_failed" || len(failed.Credentials) != 0 {
		t.Fatalf("failed validation status=%#v err=%v code=%q", failed, err, ErrorCode(err))
	}
	info.reject.Store(false)
	status, _ := controller.Get(t.Context(), InfoID)
	if len(status.Credentials) != beforeFailed {
		t.Fatalf("failed validation persisted a credential: %#v", status.Credentials)
	}

	activeID := status.Credentials[0].ID
	status, err = controller.Activate(t.Context(), InfoID, activeID, false)
	if err != nil || status.ActiveCredentialID != activeID || status.Source != SourceHousehold || !status.Configured {
		t.Fatalf("activate status=%#v err=%v", status, err)
	}
	info.reject.Store(true)
	status, err = controller.Check(t.Context(), InfoID, activeID)
	if ErrorCode(err) != "credential_auth_failed" || status.ActiveCredentialID != activeID || status.Source != SourceHousehold || status.State != StateNeedsAttention {
		t.Fatalf("failed active check fell back: status=%#v err=%v", status, err)
	}
	if _, err := controller.Delete(t.Context(), InfoID, activeID); ErrorCode(err) != "active_credential_replacement_required" {
		t.Fatalf("active delete err=%v code=%q", err, ErrorCode(err))
	}

	audits, err := st.ListAudit(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if audit.Type != "credential_secret.saved" {
			continue
		}
		ref, _ := audit.Fields["ref"].(string)
		stored, found, err := st.GetCredentialSecret(t.Context(), ref)
		if err != nil || !found {
			t.Fatalf("stored bundle missing: found=%v err=%v", found, err)
		}
		for _, secret := range []string{"secret-a", "secret-b", "lic_a", "lic_b"} {
			if strings.Contains(stored.Value, secret) {
				t.Fatalf("encrypted bundle leaked %q: %s", secret, stored.Value)
			}
		}
	}
}

func TestInfoValidationUsesOneBoundedQuery(t *testing.T) {
	info := newInfoCheckServer(t)
	cfg := config.Default()
	cfg.Plugins.Entries.InfinimeshInfo.Config.BaseURL = info.server.URL
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: integrationTestKey(2)})
	hub := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = hub.Close() })
	controller := New(cfg, vault, st, hub, nil)
	controller.Initialize(t.Context())
	if _, err := controller.AddInfoCredential(t.Context(), AddInfoCredentialInput{
		Label: "Checked", LicenseID: "lic_check", LicenseKey: "ilk_v1.lic_check.secret",
	}); err != nil {
		t.Fatal(err)
	}
	if info.issueCalls.Load() != 1 || info.queryCalls.Load() != 1 || info.requestedTokens.Load() != 1 || info.maxSources.Load() != 1 {
		t.Fatalf("issue=%d query=%d tokens=%d max_sources=%d", info.issueCalls.Load(), info.queryCalls.Load(), info.requestedTokens.Load(), info.maxSources.Load())
	}
	if got, _ := info.queryText.Load().(string); got != "SparkClaw connection check" {
		t.Fatalf("check query=%q", got)
	}
}

type infoCheckServer struct {
	server          *httptest.Server
	reject          atomic.Bool
	issueCalls      atomic.Int32
	queryCalls      atomic.Int32
	requestedTokens atomic.Int32
	maxSources      atomic.Int32
	queryText       atomic.Value
}

func newInfoCheckServer(t *testing.T) *infoCheckServer {
	t.Helper()
	fixture := &infoCheckServer{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fixture.reject.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "INVALID_LICENSE", "retryable": false}})
			return
		}
		switch r.URL.Path {
		case "/v1/info/tokens/issue":
			fixture.issueCalls.Add(1)
			var request struct {
				Requested []struct {
					Count int `json:"count"`
				} `json:"requested_tokens"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			count := 1
			if len(request.Requested) > 0 {
				count = request.Requested[0].Count
			}
			fixture.requestedTokens.Store(int32(count))
			issued := make([]map[string]any, 0, count)
			for index := 0; index < count; index++ {
				issued = append(issued, map[string]any{
					"type": "info.basic", "token_mode": "internal_opaque", "token": "check-token",
					"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch": time.Now().UTC().Format("2006-01-02"), "issued_tokens": issued,
				"quota_remaining": map[string]int{"info.basic": 9},
			})
		case "/v1/info/query":
			fixture.queryCalls.Add(1)
			var request struct {
				RequestID    string `json:"request_id"`
				Query        string `json:"query"`
				Requirements struct {
					MaxSources int `json:"max_sources"`
				} `json:"requirements"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			fixture.maxSources.Store(int32(request.Requirements.MaxSources))
			fixture.queryText.Store(request.Query)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": request.RequestID, "status": "ok",
				"answer_context": map[string]any{
					"summary": "ok", "key_facts": []any{},
					"freshness": map[string]any{"status": "current", "staleness_risk": "low"},
				},
				"sources": []any{}, "usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func integrationTestKey(fill byte) string {
	return base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}
