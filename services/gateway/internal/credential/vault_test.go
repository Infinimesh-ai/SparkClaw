package credential

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestVaultSealsCredentialBeforeStore(t *testing.T) {
	st := store.NewMemoryStore()
	token := []byte("123456789:AA-canary-telegram-token")
	vault := New(st, Options{Key: testKey(1)})
	ref, err := vault.Seal(context.Background(), "telegram-bot-token", token)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ref, "123456789") || strings.Contains(ref, "telegram") {
		t.Fatalf("credential ref leaked token identity: %q", ref)
	}
	stored, ok := st.GetCredentialSecret(ref)
	if !ok {
		t.Fatal("sealed credential was not stored")
	}
	if strings.Contains(stored.Value, string(token)) || stored.Value == string(token) {
		t.Fatalf("store received plaintext credential: %q", stored.Value)
	}
	opened, err := vault.Open(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, token) {
		t.Fatalf("opened credential mismatch: %q", opened)
	}
	if err := vault.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetCredentialSecret(ref); ok {
		t.Fatal("deleted credential remains in store")
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
	ref, err := vault.Seal(context.Background(), "telegram-bot-token", token)
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
	reopened, err := New(reloaded, Options{KeyFile: keyPath}).Open(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reopened, token) {
		t.Fatalf("reopened credential mismatch: %q", reopened)
	}
}

func TestVaultReportsStableSanitizedFailures(t *testing.T) {
	st := store.NewMemoryStore()
	token := []byte("111111111:AA-error-canary-token")
	ref, err := New(st, Options{Key: testKey(2)}).Seal(context.Background(), "telegram-bot-token", token)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(st, Options{Key: testKey(3)}).Open(context.Background(), ref)
	if ErrorCode(err) != CodeUnsealFailed {
		t.Fatalf("wrong key returned unexpected error: %v code=%q", err, ErrorCode(err))
	}
	if strings.Contains(err.Error(), string(token)) || strings.Contains(err.Error(), ref) {
		t.Fatalf("unseal error leaked credential material: %v", err)
	}

	st.SaveCredentialSecret(app.CredentialSecret{Ref: "cred_plaintext", Kind: "telegram-bot-token", Value: string(token)})
	_, err = New(st, Options{Key: testKey(2)}).Open(context.Background(), "cred_plaintext")
	if ErrorCode(err) != CodeUnsealFailed || strings.Contains(err.Error(), string(token)) {
		t.Fatalf("legacy plaintext did not fail safely: %v code=%q", err, ErrorCode(err))
	}

	missing := New(st, Options{})
	if err := missing.Ready(); ErrorCode(err) != CodeKeyUnavailable {
		t.Fatalf("missing key returned unexpected readiness: %v code=%q", err, ErrorCode(err))
	}
}

func TestVaultRejectsBroadKeyFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.key")
	if err := os.WriteFile(path, []byte(testKey(4)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	vault := New(store.NewMemoryStore(), Options{KeyFile: path})
	if err := vault.Ready(); ErrorCode(err) != CodeKeyUnavailable {
		t.Fatalf("broad key permissions should fail readiness: %v code=%q", err, ErrorCode(err))
	}
}

func TestVaultPostgresStoresCiphertext(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres credential integration")
	}
	st, err := store.NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vault := New(st, Options{Key: testKey(5)})
	token := []byte("222222222:AA-postgres-canary-token")
	ref, err := vault.Seal(context.Background(), "telegram-bot-token", token)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Delete(context.Background(), ref)
	stored, ok := st.GetCredentialSecret(ref)
	if !ok || strings.Contains(stored.Value, string(token)) || !strings.Contains(stored.Value, "AES-256-GCM") {
		t.Fatalf("PostgreSQL credential was not sealed: %#v ok=%v", stored, ok)
	}
	opened, err := vault.Open(context.Background(), ref)
	if err != nil || !bytes.Equal(opened, token) {
		t.Fatalf("PostgreSQL credential did not open: %q err=%v", opened, err)
	}
}

func testKey(fill byte) string {
	return base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}
