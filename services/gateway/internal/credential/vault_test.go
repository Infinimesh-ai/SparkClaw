package credential

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestVaultDeterministicSealReplayAndIsolation(t *testing.T) {
	st := store.NewMemoryStore()
	token := []byte("123456789:AA-canary-telegram-token")
	vault := New(st, Options{Key: testKey(1)})
	ref, err := vault.Seal(t.Context(), "binding-one", "telegram-bot-token", token)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ref, "123456789") || strings.Contains(ref, "telegram") || !strings.HasPrefix(ref, "cred_") {
		t.Fatalf("credential ref leaked token identity: %q", ref)
	}
	replayed, err := vault.Seal(t.Context(), " binding-one ", "telegram-bot-token", token)
	if err != nil || replayed != ref {
		t.Fatalf("replay ref=%q err=%v, want %q", replayed, err, ref)
	}
	other, err := vault.Seal(t.Context(), "binding-two", "telegram-bot-token", token)
	if err != nil || other == ref {
		t.Fatalf("independent binding ref=%q err=%v", other, err)
	}
	if _, err := vault.Seal(t.Context(), "binding-one", "telegram-bot-token", []byte("different")); ErrorCode(err) != CodeInvalid {
		t.Fatalf("binding input conflict=%v code=%q", err, ErrorCode(err))
	}
	stored, found, err := st.GetCredentialSecret(t.Context(), ref)
	if err != nil || !found || strings.Contains(stored.Value, string(token)) {
		t.Fatalf("stored credential=%#v found=%v err=%v", stored, found, err)
	}
	opened, err := vault.Open(t.Context(), ref)
	if err != nil || !bytes.Equal(opened, token) {
		t.Fatalf("opened=%q err=%v", opened, err)
	}
	if audits := mustCredentialListAudit(t, st, ""); countAuditType(audits, "credential_secret.saved") != 2 {
		t.Fatalf("replay appended an audit: %#v", audits)
	}
}

func TestVaultFileBackendPersistsOnlyCiphertext(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "gateway-state.json")
	keyPath := filepath.Join(dir, "secrets", "credential.key")
	st, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	vault := New(st, Options{KeyFile: keyPath, AutoCreate: true})
	if err := vault.Ready(); err != nil {
		t.Fatal(err)
	}
	token := []byte("987654321:AA-file-canary-token")
	ref, err := vault.Seal(t.Context(), "binding-file", "telegram-bot-token", token)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, token) || !bytes.Contains(raw, []byte("AES-256-GCM")) {
		t.Fatalf("file snapshot leaked plaintext or missed envelope: %s", raw)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("credential key permissions are too broad: %o", info.Mode().Perm())
	}
	reloaded, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := New(reloaded, Options{KeyFile: keyPath}).Open(t.Context(), ref)
	if err != nil || !bytes.Equal(reopened, token) {
		t.Fatalf("reopened=%q err=%v", reopened, err)
	}
}

func TestVaultReportsStableSanitizedFailures(t *testing.T) {
	st := store.NewMemoryStore()
	token := []byte("111111111:AA-error-canary-token")
	ref, err := New(st, Options{Key: testKey(2)}).Seal(t.Context(), "binding-errors", "telegram-bot-token", token)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(st, Options{Key: testKey(3)}).Open(t.Context(), ref)
	if ErrorCode(err) != CodeUnsealFailed || strings.Contains(err.Error(), string(token)) || strings.Contains(err.Error(), ref) {
		t.Fatalf("wrong-key error=%v code=%q", err, ErrorCode(err))
	}
	created, err := st.SaveCredentialSecret(t.Context(), store.NewCredentialCreate(app.CredentialSecret{Ref: "cred_plaintext", Kind: "telegram-bot-token", Value: string(token)}))
	if err != nil || created.Ref == "" {
		t.Fatal(err)
	}
	_, err = New(st, Options{Key: testKey(2)}).Open(t.Context(), created.Ref)
	if ErrorCode(err) != CodeUnsealFailed || strings.Contains(err.Error(), string(token)) {
		t.Fatalf("non-Weixin plaintext error=%v code=%q", err, ErrorCode(err))
	}
	if _, err := New(st, Options{Key: testKey(2)}).Seal(t.Context(), "", "kind", token); ErrorCode(err) != CodeInvalid {
		t.Fatalf("invalid seal=%v code=%q", err, ErrorCode(err))
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := New(st, Options{Key: testKey(2)}).Open(canceled, ref); ErrorCode(err) != CodeCanceled {
		t.Fatalf("canceled open=%v code=%q", err, ErrorCode(err))
	}
	missing := New(st, Options{})
	if err := missing.Ready(); ErrorCode(err) != CodeKeyUnavailable {
		t.Fatalf("missing key readiness=%v code=%q", err, ErrorCode(err))
	}
}

func TestVaultRewrapsLegacyWeixinCredential(t *testing.T) {
	st := store.NewMemoryStore()
	legacy := "legacy-weixin-token"
	created, err := st.SaveCredentialSecret(t.Context(), store.NewCredentialCreate(app.CredentialSecret{
		Ref: "legacy-weixin-ref", Kind: legacyWeixinCredentialKind, Value: legacy,
	}))
	if err != nil {
		t.Fatal(err)
	}
	vault := New(st, Options{Key: testKey(4)})
	opened, err := vault.Open(t.Context(), created.Ref)
	if err != nil || string(opened) != legacy {
		t.Fatalf("legacy open=%q err=%v", opened, err)
	}
	stored, found, err := st.GetCredentialSecret(t.Context(), created.Ref)
	if err != nil || !found || stored.Value == legacy || !strings.Contains(stored.Value, "AES-256-GCM") || !stored.CreatedAt.Equal(created.CreatedAt) || !stored.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("rewrapped=%#v found=%v err=%v", stored, found, err)
	}
	reopened, err := vault.Open(t.Context(), created.Ref)
	if err != nil || string(reopened) != legacy {
		t.Fatalf("rewrapped open=%q err=%v", reopened, err)
	}
}

func TestVaultDeletesOnlyOwnedLegacyWeixinCredentials(t *testing.T) {
	st := store.NewMemoryStore()
	vault := New(st, Options{Key: testKey(11)})
	for _, testCase := range []struct {
		name   string
		ref    string
		rewrap bool
	}{
		{name: "raw", ref: legacyWeixinRefPrefix + "binding-raw"},
		{name: "rewrapped", ref: legacyWeixinRefPrefix + "binding-rewrapped", rewrap: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			created, err := st.SaveCredentialSecret(t.Context(), store.NewCredentialCreate(app.CredentialSecret{
				Ref: testCase.ref, Kind: legacyWeixinCredentialKind, Value: "legacy-weixin-token",
			}))
			if err != nil {
				t.Fatal(err)
			}
			if testCase.rewrap {
				opened, err := vault.Open(t.Context(), created.Ref)
				if err != nil || string(opened) != "legacy-weixin-token" {
					t.Fatalf("legacy open=%q err=%v", opened, err)
				}
				zero(opened)
			}
			if err := vault.Delete(t.Context(), created.Ref); err != nil {
				t.Fatal(err)
			}
			if stored, found, err := st.GetCredentialSecret(t.Context(), created.Ref); err != nil || found || stored.Ref != "" {
				t.Fatalf("legacy credential survived delete: %#v found=%v err=%v", stored, found, err)
			}
		})
	}

	if err := vault.Delete(t.Context(), "provider:other:binding"); ErrorCode(err) != CodeInvalid {
		t.Fatalf("unowned provider ref error=%v code=%q", err, ErrorCode(err))
	}
	wrongKindRef := legacyWeixinRefPrefix + "binding-wrong-kind"
	if _, err := st.SaveCredentialSecret(t.Context(), store.NewCredentialCreate(app.CredentialSecret{
		Ref: wrongKindRef, Kind: "other-kind", Value: "legacy-weixin-token",
	})); err != nil {
		t.Fatal(err)
	}
	if err := vault.Delete(t.Context(), wrongKindRef); ErrorCode(err) != CodeUnavailable {
		t.Fatalf("wrong-kind legacy ref error=%v code=%q", err, ErrorCode(err))
	}
	if _, found, err := st.GetCredentialSecret(t.Context(), wrongKindRef); err != nil || !found {
		t.Fatalf("wrong-kind credential was deleted: found=%v err=%v", found, err)
	}
}

type unknownOnceRepository struct {
	inner             *store.MemoryStore
	unknownSaveOnce   bool
	unknownDeleteOnce bool
}

func (r *unknownOnceRepository) SaveCredentialSecret(ctx context.Context, command store.CredentialSaveCommand) (app.CredentialSecret, error) {
	candidate, err := r.inner.SaveCredentialSecret(ctx, command)
	if err == nil && r.unknownSaveOnce {
		r.unknownSaveOnce = false
		return candidate, &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationCredentialSecretSave, Err: errors.New("injected unknown save")}
	}
	return candidate, err
}

func (r *unknownOnceRepository) GetCredentialSecret(ctx context.Context, ref string) (app.CredentialSecret, bool, error) {
	return r.inner.GetCredentialSecret(ctx, ref)
}

func (r *unknownOnceRepository) DeleteCredentialSecret(ctx context.Context, condition store.CredentialDeleteCondition) (app.CredentialSecret, error) {
	deleted, err := r.inner.DeleteCredentialSecret(ctx, condition)
	if err == nil && r.unknownDeleteOnce {
		r.unknownDeleteOnce = false
		return deleted, &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationCredentialSecretDelete, Err: errors.New("injected unknown delete")}
	}
	return deleted, err
}

func TestVaultReconcilesUnknownCreateAndCleansOrphanBeforeDifferentMutation(t *testing.T) {
	repository := &unknownOnceRepository{inner: store.NewMemoryStore(), unknownSaveOnce: true}
	vault := New(repository, Options{Key: testKey(5)})
	token := []byte("unknown-create-token")
	if ref, err := vault.Seal(t.Context(), "binding-unknown", "token", token); ref != "" || ErrorCode(err) != CodeUnavailable {
		t.Fatalf("first seal ref=%q err=%v code=%q", ref, err, ErrorCode(err))
	}
	ref, err := vault.Seal(t.Context(), "binding-unknown", "token", token)
	if err != nil || ref == "" {
		t.Fatalf("reconciled seal ref=%q err=%v", ref, err)
	}
	repository.unknownSaveOnce = true
	if _, err := vault.Seal(t.Context(), "binding-orphan", "token", []byte("orphan")); ErrorCode(err) != CodeUnavailable {
		t.Fatalf("orphan setup err=%v code=%q", err, ErrorCode(err))
	}
	other, err := vault.Seal(t.Context(), "binding-after-orphan", "token", []byte("other"))
	if err != nil || other == "" {
		t.Fatalf("post-orphan seal ref=%q err=%v", other, err)
	}
	if countAuditType(mustCredentialListAudit(t, repository.inner, ""), "credential_secret.deleted") != 1 {
		t.Fatalf("orphan was not conditionally deleted: %#v", mustCredentialListAudit(t, repository.inner, ""))
	}
}

func TestVaultReconcilesUnknownLegacyRewrap(t *testing.T) {
	repository := &unknownOnceRepository{inner: store.NewMemoryStore()}
	legacy := "legacy-rewrap-unknown-token"
	created, err := repository.inner.SaveCredentialSecret(t.Context(), store.NewCredentialCreate(app.CredentialSecret{
		Ref: "legacy-rewrap-unknown", Kind: legacyWeixinCredentialKind, Value: legacy,
	}))
	if err != nil {
		t.Fatal(err)
	}
	repository.unknownSaveOnce = true
	vault := New(repository, Options{Key: testKey(9)})
	if opened, err := vault.Open(t.Context(), created.Ref); opened != nil || ErrorCode(err) != CodeUnavailable {
		t.Fatalf("first legacy open=%q err=%v code=%q", opened, err, ErrorCode(err))
	}
	opened, err := vault.Open(t.Context(), created.Ref)
	if err != nil || string(opened) != legacy {
		t.Fatalf("reconciled legacy open=%q err=%v", opened, err)
	}
	stored, found, err := repository.inner.GetCredentialSecret(t.Context(), created.Ref)
	if err != nil || !found || stored.Value == legacy || !strings.Contains(stored.Value, "AES-256-GCM") {
		t.Fatalf("legacy rewrap did not settle to ciphertext: %#v found=%v err=%v", stored, found, err)
	}
}

func TestVaultReconcilesUnknownOrphanDeleteBeforeNextSeal(t *testing.T) {
	repository := &unknownOnceRepository{inner: store.NewMemoryStore(), unknownSaveOnce: true, unknownDeleteOnce: true}
	vault := New(repository, Options{Key: testKey(10)})
	if _, err := vault.Seal(t.Context(), "binding-orphan-delete", "token", []byte("orphan")); ErrorCode(err) != CodeUnavailable {
		t.Fatalf("orphan create err=%v code=%q", err, ErrorCode(err))
	}
	if ref, err := vault.Seal(t.Context(), "binding-after-delete", "token", []byte("next")); ref != "" || ErrorCode(err) != CodeUnavailable {
		t.Fatalf("unknown orphan delete ref=%q err=%v code=%q", ref, err, ErrorCode(err))
	}
	ref, err := vault.Seal(t.Context(), "binding-after-delete", "token", []byte("next"))
	if err != nil || ref == "" {
		t.Fatalf("post-delete reconciliation ref=%q err=%v", ref, err)
	}
	if countAuditType(mustCredentialListAudit(t, repository.inner, ""), "credential_secret.deleted") != 1 {
		t.Fatalf("unknown delete produced the wrong audit count: %#v", mustCredentialListAudit(t, repository.inner, ""))
	}
}

func TestVaultLifecycleCancellationClearsVolatilePending(t *testing.T) {
	repository := &unknownOnceRepository{inner: store.NewMemoryStore(), unknownSaveOnce: true}
	vault := New(repository, Options{Key: testKey(6)})
	lifecycle, cancel := context.WithCancel(t.Context())
	vault.BindLifecycle(lifecycle)
	if _, err := vault.Seal(t.Context(), "binding-lifecycle", "token", []byte("secret")); ErrorCode(err) != CodeUnavailable {
		t.Fatal(err)
	}
	cancel()
	for range 100 {
		vault.mu.Lock()
		pending := vault.pending
		vault.mu.Unlock()
		if pending == nil {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("lifecycle cancellation did not clear pending material")
}

func TestVaultRejectsBroadKeyFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.key")
	if err := os.WriteFile(path, []byte(testKey(7)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	vault := New(store.NewMemoryStore(), Options{KeyFile: path})
	if err := vault.Ready(); ErrorCode(err) != CodeKeyUnavailable {
		t.Fatalf("broad key permissions readiness=%v code=%q", err, ErrorCode(err))
	}
}

func TestVaultPostgresStoresCiphertext(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres credential integration")
	}
	st, err := store.NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vault := New(st, Options{Key: testKey(8)})
	token := []byte("222222222:AA-postgres-canary-token")
	ref, err := vault.Seal(t.Context(), "binding-postgres", "telegram-bot-token", token)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := st.GetCredentialSecret(t.Context(), ref)
	if err != nil || !found || strings.Contains(stored.Value, string(token)) || !strings.Contains(stored.Value, "AES-256-GCM") {
		t.Fatalf("PostgreSQL credential=%#v found=%v err=%v", stored, found, err)
	}
	opened, err := vault.Open(t.Context(), ref)
	if err != nil || !bytes.Equal(opened, token) {
		t.Fatalf("PostgreSQL open=%q err=%v", opened, err)
	}
}

func countAuditType(audits []app.AuditEvent, typ string) int {
	count := 0
	for _, audit := range audits {
		if audit.Type == typ {
			count++
		}
	}
	return count
}

func testKey(fill byte) string {
	return base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}
