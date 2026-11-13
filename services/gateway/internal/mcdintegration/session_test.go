package mcdintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type fakeRunner struct {
	validate func(context.Context, Envelope) error
	read     func(context.Context, Envelope, Execution, string, json.RawMessage) (json.RawMessage, error)
}

func (f *fakeRunner) Validate(ctx context.Context, e Envelope) error {
	if f.validate != nil {
		return f.validate(ctx, e)
	}
	return nil
}
func (f *fakeRunner) Read(ctx context.Context, e Envelope, x Execution, op string, a json.RawMessage) (json.RawMessage, error) {
	if f.read != nil {
		return f.read(ctx, e, x, op, a)
	}
	return json.RawMessage(`{"store_code":"3330613"}`), nil
}
func fixture(t *testing.T) Envelope {
	t.Helper()
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/session-envelope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.IssuedAtMS = time.Now().UnixMilli() - 1000
	e.SessionNotAfterMS = e.IssuedAtMS + MaxLeaseMS
	return e
}
func encoded(t *testing.T, e Envelope) []byte {
	t.Helper()
	e.MaterialDigest = digest(e.Material)
	raw, _ := json.Marshal(e)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "envelope_digest")
	e.EnvelopeDigest = digest(fields)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func harness(t *testing.T) (*Connection, *credential.Vault, *fakeRunner, Envelope, *atomic.Int64) {
	t.Helper()
	e := fixture(t)
	now := new(atomic.Int64)
	now.Store(e.IssuedAtMS + 1000)
	v := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("01", 32)})
	if err := v.Ready(); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	c, err := NewConnection(v, r, e.Identity, e.ConnectionID, func() time.Time { return time.UnixMilli(now.Load()) })
	if err != nil {
		t.Fatal(err)
	}
	return c, v, r, e, now
}
func execution(e Envelope) Execution {
	return Execution{Identity: e.Identity, ConnectionID: e.ConnectionID, ExecutionID: "execution", CredentialRevision: e.CredentialRevision, GrantRevision: e.GrantRevision, DeadlineMS: time.Now().UnixMilli() + 30000, MaxRequests: 8}
}

func TestSharedCanonicalQuoteAndSessionVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/canonical-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string                     `json:"name"`
		Envelope map[string]json.RawMessage `json:"envelope"`
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			var want string
			_ = json.Unmarshal(f.Envelope["envelope_digest"], &want)
			delete(f.Envelope, "envelope_digest")
			if digest(f.Envelope) != want {
				t.Fatal("cross-language digest differs")
			}
		})
	}
	_ = fixture(t)
	for _, invalid := range []string{`{"a":1,"a":2}`, `{"x":1.5}`, `{"x":1e2}`, `{"x":9007199254740992}`, `{"x":"\ud800"}`, `{"中文":1}`} {
		if _, err = Canonical([]byte(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	got, err := Canonical([]byte("{\"2\":null,\"10\":\"<>&\\n\\t\\b\\f\\r\u2028中文\",\"x\":-0}"))
	if err != nil || string(got) != "{\"10\":\"<>&\\n\\t\\b\\f\\r\u2028中文\",\"2\":null,\"x\":0}" {
		t.Fatalf("canonical string/number: %q %v", got, err)
	}
}

func TestLeaseReplayRenewalRotationAndPermanentRevoke(t *testing.T) {
	c, v, runner, e, _ := harness(t)
	ctx := t.Context()
	original, err := c.Provision(ctx, encoded(t, e))
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := c.Provision(ctx, encoded(t, e)); err != nil || replay != original {
		t.Fatal("original receipt lost", err)
	}
	drift := e
	drift.SessionNotAfterMS--
	if _, err = c.Provision(ctx, encoded(t, drift)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	renew := e
	renew.ProvisionRequestID = "renew"
	renew.GrantRevision++
	renew.SessionNotAfterMS--
	if _, err = c.Provision(ctx, encoded(t, renew)); err != nil {
		t.Fatal(err)
	}
	x := execution(e)
	if _, err = c.Read(ctx, x, "store_get", json.RawMessage(`{}`)); err != nil {
		t.Fatal("unchanged credential lease invalidated execution", err)
	}
	x.GrantRevision = renew.GrantRevision
	if _, err = c.Read(ctx, x, "store_get", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	rotate := renew
	rotate.ProvisionRequestID = "rotation"
	rotate.GrantRevision++
	rotate.Material = bytes.ReplaceAll(e.Material, []byte("SYNTHETIC_SESSION_CANARY"), []byte("ROTATED_SESSION_CANARY"))
	if _, err = c.Provision(ctx, encoded(t, rotate)); !errors.Is(err, ErrConflict) {
		t.Fatal("material changed without revision", err)
	}
	rotate.CredentialRevision++
	if _, err = c.Provision(ctx, encoded(t, rotate)); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Revoke(ctx, "revoke"); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewConnection(v, runner, e.Identity, e.ConnectionID, nil)
	rotate.ProvisionRequestID = "late"
	rotate.GrantRevision++
	if _, err = restarted.Provision(ctx, encoded(t, rotate)); !errors.Is(err, ErrClosed) {
		t.Fatal("reopened revoked connection", err)
	}
	if replay, err := restarted.Provision(ctx, encoded(t, e)); err != nil || replay != original {
		t.Fatal("revocation erased dedupe", err)
	}
	r, _, err := restarted.load(ctx)
	if err != nil || len(r.Session.Material) != 0 || !r.Closed {
		t.Fatal("revocation retained material", err)
	}
	wrong := e.Identity
	wrong.MemberDigest = strings.Repeat("0", 64)
	rebound, _ := NewConnection(v, runner, wrong, e.ConnectionID, nil)
	if _, _, err = rebound.load(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("account rebound across tombstone", err)
	}
}

func TestExpiredCleanupRetainsHighWaterAndClockRollbackRejects(t *testing.T) {
	c, _, _, e, now := harness(t)
	if _, err := c.Provision(t.Context(), encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	now.Add(-1)
	if _, err := c.Read(t.Context(), execution(e), "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	now.Store(e.SessionNotAfterMS)
	if err := c.Expire(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, _, err := c.load(t.Context())
	if err != nil || len(r.Session.Material) != 0 || len(r.Receipts) != 1 || r.Session.GrantRevision != 1 {
		t.Fatal("expiry lost fence", err)
	}
	e.ProvisionRequestID = "rollback"
	if _, err = c.Provision(t.Context(), encoded(t, e)); err == nil {
		t.Fatal("restored expired lease")
	}
}

func TestMissingControlDoesNotBecomeAnEmptyConnection(t *testing.T) {
	c, v, _, e, _ := harness(t)
	if _, err := c.Provision(t.Context(), encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	if err := v.DeleteBinding(t.Context(), c.binding, c.kind); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Provision(t.Context(), encoded(t, e)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lost high-water mark reinitialized", err)
	}
}

func TestRestoringAnOlderValidRecordCannotUndoRevocation(t *testing.T) {
	c, v, _, e, _ := harness(t)
	if _, err := c.Provision(t.Context(), encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	previous, _, _, err := v.OpenBindingVersion(t.Context(), c.binding, c.kind)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(previous)
	if _, err = c.Revoke(t.Context(), "revoke"); err != nil {
		t.Fatal(err)
	}
	// A valid old credential/control record is still insufficient: the
	// independent anchor remembers the later revocation checkpoint.
	if err = v.ReplaceBinding(t.Context(), c.binding, c.kind, previous); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Read(t.Context(), execution(e), "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("old valid record restored access", err)
	}
}

type tornVault struct {
	Vault
	failBinding string
}

func (v *tornVault) CompareAndReplaceBinding(ctx context.Context, binding, kind string, raw []byte, gen int64) error {
	if binding == v.failBinding {
		return ErrUnavailable
	}
	return v.Vault.CompareAndReplaceBinding(ctx, binding, kind, raw, gen)
}
func TestTornCheckpointNeverFallsBackOrReplays(t *testing.T) {
	c, v, runner, e, _ := harness(t)
	if _, err := c.Provision(t.Context(), encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	c.vault = &tornVault{Vault: v, failBinding: c.binding}
	if _, err := c.Revoke(t.Context(), "revoke"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	restarted, _ := NewConnection(v, runner, e.Identity, e.ConnectionID, nil)
	if _, err := restarted.Read(t.Context(), execution(e), "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("torn checkpoint used old material", err)
	}
	if _, err := restarted.Provision(t.Context(), encoded(t, e)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("torn checkpoint replayed", err)
	}
}

func TestRevokeRacesHealthAndCancelsRead(t *testing.T) {
	c, _, runner, e, _ := harness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	runner.validate = func(context.Context, Envelope) error { close(entered); <-release; return nil }
	result := make(chan error, 1)
	go func() { _, err := c.Provision(t.Context(), encoded(t, e)); result <- err }()
	<-entered
	if _, err := c.Revoke(t.Context(), "before-provision"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrConflict) {
		t.Fatal("late health restored access", err)
	}
	c, _, runner, e, _ = harness(t)
	if _, err := c.Provision(t.Context(), encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	entered = make(chan struct{})
	runner.read = func(ctx context.Context, _ Envelope, _ Execution, _ string, _ json.RawMessage) (json.RawMessage, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() { _, err := c.Read(t.Context(), execution(e), "store_get", json.RawMessage(`{}`)); result <- err }()
	<-entered
	if _, err := c.Revoke(t.Context(), "during-read"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("delivered revoked read")
		}
	case <-time.After(time.Second):
		t.Fatal("read was not canceled")
	}
}

func TestFileVaultRestartNoSecretsInReceiptsAndCiphertextTamper(t *testing.T) {
	root := t.TempDir()
	state, key := filepath.Join(root, "state.json"), filepath.Join(root, "key")
	st, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	v := credential.New(st, credential.Options{KeyFile: key, AutoCreate: true})
	e := fixture(t)
	c, _ := NewConnection(v, &fakeRunner{}, e.Identity, e.ConnectionID, nil)
	receipt, err := c.Provision(t.Context(), encoded(t, e))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(receipt)
	for _, value := range [][]byte{raw, public} {
		if bytes.Contains(value, []byte("SYNTHETIC")) {
			t.Fatal("credential leaked")
		}
	}
	st, err = store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	v = credential.New(st, credential.Options{KeyFile: key})
	c, _ = NewConnection(v, &fakeRunner{}, e.Identity, e.ConnectionID, nil)
	if _, err = c.Read(t.Context(), execution(e), "store_get", json.RawMessage(`{}`)); err != nil {
		t.Fatal("restart requires Mac", err)
	}
	secrets, err := st.ListCredentialSecrets(t.Context())
	if err != nil || len(secrets) != 3 {
		t.Fatal(err)
	}
	var index int
	for i, secret := range secrets {
		if strings.HasSuffix(secret.Kind, ":material") {
			index = i
		}
	}
	bad := secrets[index]
	bad.Value = `{"version":1,"alg":"AES-256-GCM","nonce":"bad","ciphertext":"bad"}`
	if _, err = st.SaveCredentialSecret(t.Context(), store.NewCredentialReplace(secrets[index], bad)); err != nil {
		t.Fatal(err)
	}
	x := execution(e)
	x.ExecutionID = "ciphertext-recheck"
	if _, err = c.Read(t.Context(), x, "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("tampered ciphertext used", err)
	}
}

type observedVault struct {
	Vault
	materialOpens int
}

func (v *observedVault) OpenBindingVersion(ctx context.Context, binding, kind string) ([]byte, int64, bool, error) {
	if strings.HasSuffix(binding, ":material") {
		v.materialOpens++
	}
	return v.Vault.OpenBindingVersion(ctx, binding, kind)
}
func TestAdmissionPrecedesMaterialDecryptionAndCleanupKeepsControl(t *testing.T) {
	c, v, _, e, now := harness(t)
	if _, err := c.Provision(t.Context(), encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	control, _, _, err := v.OpenBindingVersion(t.Context(), c.binding, c.kind)
	if err != nil || bytes.Contains(control, []byte("SYNTHETIC")) {
		t.Fatal("control contains material", err)
	}
	clear(control)
	observer := &observedVault{Vault: v}
	c.vault = observer
	x := execution(e)
	x.Identity.OwnerID = "other"
	if _, err = c.Read(t.Context(), x, "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrScope) || observer.materialOpens != 0 {
		t.Fatal("foreign execution opened material", err)
	}
	now.Store(e.SessionNotAfterMS)
	x = execution(e)
	x.DeadlineMS = now.Load() + 30000
	if _, err = c.Read(t.Context(), x, "store_get", json.RawMessage(`{}`)); !errors.Is(err, ErrExpired) || observer.materialOpens != 0 {
		t.Fatal("expired execution opened material", err)
	}
	_, _, found, err := v.OpenBindingVersion(t.Context(), c.binding+":material", c.kind+":material")
	if err != nil || found {
		t.Fatal("expired ciphertext retained", err)
	}
	r, _, err := c.load(t.Context())
	if err != nil || r.MaterialPresent || len(r.Receipts) != 1 {
		t.Fatal("cleanup lost control", err)
	}
}
