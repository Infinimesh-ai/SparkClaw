package sparkclawmobile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeenroll/enrolltest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

type memoryStore struct {
	mu     sync.Mutex
	values map[string]string
	sets   int
}

func (m *memoryStore) Get(service, account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[service+"\x00"+account]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (m *memoryStore) Set(service, account, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sets++
	m.values[service+"\x00"+account] = value
	return nil
}
func (m *memoryStore) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, service+"\x00"+account)
	return nil
}

func installStore(t *testing.T) *memoryStore {
	t.Helper()
	store := &memoryStore{values: map[string]string{}}
	SetSecretStore(store)
	t.Cleanup(func() { SetSecretStore(nil) })
	return store
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sparkclaw")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCompactCredentialRoundTripAndFitsOneQRCode(t *testing.T) {
	f := enrolltest.New(t, time.Minute)
	compact, err := CompactCredential(f.Credential)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(compact, CompactCredentialPrefix) || len(compact) >= len(f.Credential) {
		t.Fatalf("compact form is not smaller: %d >= %d", len(compact), len(f.Credential))
	}
	// QR version 40 with low error correction carries 2953 bytes. The fixture
	// uses a 1200-byte RSA test certificate; production TEST1 pins a 635-byte
	// certificate, so real compact credentials are several hundred bytes smaller.
	if len(compact) > 2953 {
		t.Fatalf("compact credential %d bytes does not fit one QR code", len(compact))
	}
	for _, input := range []string{compact, "  " + compact + "\n", f.Credential} {
		got, err := NormalizeCredential(input)
		if err != nil || got != f.Credential {
			t.Fatalf("normalize %q: %v", input[:30], err)
		}
	}
	if _, err := NormalizeCredential(CompactCredentialPrefix + "AAAA"); err == nil {
		t.Fatal("corrupt compact credential accepted")
	}
	tampered := []byte(f.Credential)
	tampered[len(tampered)-3] ^= 1
	if _, err := NormalizeCredential(string(tampered)); err == nil {
		t.Fatal("tampered credential accepted")
	}
	if ms, err := CredentialExpiresAtUnixMillis(compact); err != nil || ms <= time.Now().UnixMilli() {
		t.Fatal("credential expiry unavailable", err)
	}
}

func TestEnrollRequiresSecretStore(t *testing.T) {
	SetSecretStore(nil)
	f := enrolltest.New(t, time.Minute)
	if _, err := Enroll(f.Credential, privateDir(t), NewInstallationID(), 5000); err == nil {
		t.Fatal("enrollment ran without a native secret store")
	}
}

func TestEnrollKeepsKeyInHostStoreAndResumesSameOperation(t *testing.T) {
	store := installStore(t)
	f := enrolltest.New(t, time.Minute)
	compact, err := CompactCredential(f.Credential)
	if err != nil {
		t.Fatal(err)
	}
	dir, installation := privateDir(t), NewInstallationID()
	out, err := Enroll(compact, dir, installation, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		ProfilePath  string `json:"profile_path"`
		HelperConfig string `json:"helper_config"`
		Descriptor   struct {
			Mode              string `json:"mode"`
			InitiatorDeviceID string `json:"initiator_device_id"`
			ClientID          string `json:"client_id"`
		} `json:"descriptor"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Descriptor.Mode != "node-managed" || !strings.HasPrefix(result.Descriptor.InitiatorDeviceID, "phone-") || result.Descriptor.ClientID == "" {
		t.Fatalf("unexpected descriptor %+v", result.Descriptor)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity", iscpbridge.IdentityKeyFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("device private key was written to a file")
	}
	if store.sets != 1 {
		t.Fatalf("device key stored %d times", store.sets)
	}
	cfg, err := iscpworkbench.LoadConfig(result.HelperConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdentityKeyBackend != iscpbridge.IdentityKeyBackendHost || cfg.Role != iscpworkbench.RoleInitiator {
		t.Fatalf("profile backend %q role %q", cfg.IdentityKeyBackend, cfg.Role)
	}
	again, err := Enroll(f.Credential, dir, installation, 10000)
	if err != nil {
		t.Fatal("retry with the same credential failed", err)
	}
	if again != out {
		t.Fatal("retry produced a different enrollment")
	}
	if commits, _ := f.Registration.Counts(); commits != 1 || store.sets != 1 {
		t.Fatalf("retry created another device: commits=%d key sets=%d", commits, store.sets)
	}
	if _, err := Enroll(f.Credential, dir, NewInstallationID(), 10000); err == nil {
		t.Fatal("pending enrollment accepted a different installation")
	}
}

type recordingListener struct {
	mu     sync.Mutex
	states []string
}

func (l *recordingListener) OnState(s string) {
	l.mu.Lock()
	l.states = append(l.states, s)
	l.mu.Unlock()
}
func (l *recordingListener) OnCapabilities(string) {}

func TestOpenCallAndCloseWithoutRelaySession(t *testing.T) {
	installStore(t)
	f := enrolltest.New(t, time.Minute)
	out, err := Enroll(f.Credential, privateDir(t), NewInstallationID(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		HelperConfig string `json:"helper_config"`
	}
	_ = json.Unmarshal([]byte(out), &result)
	listener := &recordingListener{}
	client, err := Open(result.HelperConfig, listener)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.IdentityJSON(), "phone-") {
		t.Fatal("public identity missing")
	}
	if _, err := client.Call("no.such.operation", "", "", "", 500); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if _, err := client.Call(iscpworkbench.OperationExecutionProgressStream, "", "", "", 500); err == nil {
		t.Fatal("stream operation accepted by Call")
	}
	// The fixture Relay refuses duplex, so the call must return a typed,
	// retryable failure instead of hanging or panicking.
	raw, err := client.Call(iscpworkbench.OperationIdentity, "", "", "", 800)
	if err != nil {
		t.Fatal(err)
	}
	var response iscpworkbench.Response
	if err := json.Unmarshal([]byte(raw), &response); err != nil || response.Status < 400 {
		t.Fatalf("unexpected response %s", raw)
	}
	client.Close()
	client.Close()
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if len(listener.states) == 0 {
		t.Fatal("no connection state reported")
	}
}

func TestRequestsCarryTheBoundInstallation(t *testing.T) {
	installation := NewInstallationID()
	request, err := buildRequest(iscpworkbench.OperationPhoneSnapshotGet, "", "{}", "", installation)
	if err != nil || request.InstallationID != installation || request.Profile != iscpworkbench.ProfileV2 {
		t.Fatalf("request %+v %v", request, err)
	}
}

func TestEnvelopeCarriesExecutionFields(t *testing.T) {
	installation, requestID := NewInstallationID(), NewInstallationID()
	body := `{"schema_version":2}`
	request, err := requestWithEnvelope(iscpworkbench.OperationSubmit, `{"request_id":"`+requestID+`","input_digest":"`+strings.Repeat("a", 64)+`"}`, body, installation)
	if err != nil || request.RequestID != requestID || request.InstallationID != installation || string(request.Body) != body {
		t.Fatalf("submit envelope %+v %v", request, err)
	}
	if _, err := requestWithEnvelope(iscpworkbench.OperationSubmit, `{}`, body, installation); err == nil {
		t.Fatal("execution request without request_id accepted")
	}
	if _, err := requestWithEnvelope(iscpworkbench.OperationSubmit, `{"unknown":1}`, body, installation); err == nil {
		t.Fatal("unknown envelope field accepted")
	}
	approval, err := requestWithEnvelope(iscpworkbench.OperationExecutionApproval, `{"params":{"request_id":"`+requestID+`","approval_id":"a1"},"operation_id":"`+NewInstallationID()+`","expected_revision":"3"}`, `{"digest":"d","decision":"approve"}`, installation)
	if err != nil || approval.ExpectedRevision != "3" || approval.Params["approval_id"] != "a1" {
		t.Fatalf("approval envelope %+v %v", approval, err)
	}
}
