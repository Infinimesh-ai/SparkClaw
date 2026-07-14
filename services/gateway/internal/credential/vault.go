package credential

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	CodeKeyUnavailable = "credential_key_unavailable"
	CodeSealFailed     = "credential_seal_failed"
	CodeUnsealFailed   = "credential_unseal_failed"
)

type Error struct {
	Code  string
	cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "credential operation failed"
	}
	switch e.Code {
	case CodeKeyUnavailable:
		return "credential encryption key is unavailable"
	case CodeSealFailed:
		return "credential could not be stored securely"
	case CodeUnsealFailed:
		return "credential could not be decrypted"
	default:
		return "credential operation failed"
	}
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func ErrorCode(err error) string {
	var credentialErr *Error
	if errors.As(err, &credentialErr) {
		return credentialErr.Code
	}
	return ""
}

type Options struct {
	Key        string
	KeyFile    string
	AutoCreate bool
}

type CredentialVault interface {
	Ready() error
	Seal(context.Context, string, []byte) (string, error)
	Open(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

type Vault struct {
	store    store.Store
	aead     cipher.AEAD
	readyErr error
}

type envelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"alg"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func New(st store.Store, options Options) *Vault {
	vault := &Vault{store: st}
	if st == nil {
		vault.readyErr = &Error{Code: CodeKeyUnavailable, cause: errors.New("credential store is nil")}
		return vault
	}
	key, err := loadKey(options)
	if err != nil {
		vault.readyErr = &Error{Code: CodeKeyUnavailable, cause: err}
		return vault
	}
	block, err := aes.NewCipher(key)
	zero(key)
	if err != nil {
		vault.readyErr = &Error{Code: CodeKeyUnavailable, cause: err}
		return vault
	}
	vault.aead, err = cipher.NewGCM(block)
	if err != nil {
		vault.readyErr = &Error{Code: CodeKeyUnavailable, cause: err}
	}
	return vault
}

func (v *Vault) Ready() error {
	if v == nil || v.aead == nil {
		if v != nil && v.readyErr != nil {
			return v.readyErr
		}
		return &Error{Code: CodeKeyUnavailable, cause: errors.New("credential vault is not initialized")}
	}
	return nil
}

func (v *Vault) Seal(ctx context.Context, kind string, plaintext []byte) (string, error) {
	if err := v.Ready(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", &Error{Code: CodeSealFailed, cause: err}
	}
	kind = strings.TrimSpace(kind)
	if kind == "" || len(plaintext) == 0 {
		return "", &Error{Code: CodeSealFailed, cause: errors.New("credential kind and value are required")}
	}
	ref, err := randomRef()
	if err != nil {
		return "", &Error{Code: CodeSealFailed, cause: err}
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", &Error{Code: CodeSealFailed, cause: err}
	}
	secretCopy := append([]byte(nil), plaintext...)
	defer zero(secretCopy)
	ciphertext := v.aead.Seal(nil, nonce, secretCopy, associatedData(ref, kind))
	raw, err := json.Marshal(envelope{
		Version:    1,
		Algorithm:  "AES-256-GCM",
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return "", &Error{Code: CodeSealFailed, cause: err}
	}
	saved := v.store.SaveCredentialSecret(app.CredentialSecret{Ref: ref, Kind: kind, Value: string(raw)})
	stored, ok := v.store.GetCredentialSecret(ref)
	if !ok || stored.Ref != saved.Ref || stored.Kind != kind || stored.Value != string(raw) {
		_ = v.store.DeleteCredentialSecret(ref)
		return "", &Error{Code: CodeSealFailed, cause: errors.New("credential store verification failed")}
	}
	return ref, nil
}

func (v *Vault) Open(ctx context.Context, ref string) ([]byte, error) {
	if err := v.Ready(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Code: CodeUnsealFailed, cause: err}
	}
	ref = strings.TrimSpace(ref)
	stored, ok := v.store.GetCredentialSecret(ref)
	if !ok {
		return nil, &Error{Code: CodeUnsealFailed, cause: errors.New("credential reference was not found")}
	}
	var sealed envelope
	if err := json.Unmarshal([]byte(stored.Value), &sealed); err != nil {
		return nil, &Error{Code: CodeUnsealFailed, cause: errors.New("credential envelope is invalid")}
	}
	if sealed.Version != 1 || sealed.Algorithm != "AES-256-GCM" {
		return nil, &Error{Code: CodeUnsealFailed, cause: errors.New("credential envelope version is unsupported")}
	}
	nonce, err := base64.RawStdEncoding.DecodeString(sealed.Nonce)
	if err != nil || len(nonce) != v.aead.NonceSize() {
		return nil, &Error{Code: CodeUnsealFailed, cause: errors.New("credential nonce is invalid")}
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(sealed.Ciphertext)
	if err != nil {
		return nil, &Error{Code: CodeUnsealFailed, cause: errors.New("credential ciphertext is invalid")}
	}
	plaintext, err := v.aead.Open(nil, nonce, ciphertext, associatedData(ref, stored.Kind))
	if err != nil {
		return nil, &Error{Code: CodeUnsealFailed, cause: errors.New("credential authentication failed")}
	}
	return plaintext, nil
}

func (v *Vault) Delete(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := v.store.DeleteCredentialSecret(strings.TrimSpace(ref))
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return &Error{Code: CodeSealFailed, cause: errors.New("credential deletion failed")}
	}
	return nil
}

func loadKey(options Options) ([]byte, error) {
	if value := strings.TrimSpace(options.Key); value != "" {
		return decodeKey(value)
	}
	path := strings.TrimSpace(options.KeyFile)
	if path == "" {
		return nil, errors.New("credential key is not configured")
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		if info, statErr := os.Stat(path); statErr != nil {
			return nil, statErr
		} else if info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("credential key file permissions must be 0600 or stricter")
		}
		return decodeKey(strings.TrimSpace(string(raw)))
	}
	if !errors.Is(err, os.ErrNotExist) || !options.AutoCreate {
		return nil, errors.New("credential key file is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("credential key directory could not be created")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	encoded := base64.RawStdEncoding.EncodeToString(key) + "\n"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		zero(key)
		return loadKey(Options{KeyFile: path})
	}
	if err != nil {
		zero(key)
		return nil, errors.New("credential key file could not be created")
	}
	_, writeErr := file.WriteString(encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		zero(key)
		_ = os.Remove(path)
		return nil, errors.New("credential key file could not be written")
	}
	return key, nil
}

func decodeKey(value string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len([]byte(value)) == 32 {
		return []byte(value), nil
	}
	return nil, fmt.Errorf("credential key must contain exactly 32 bytes")
}

func randomRef() (string, error) {
	raw := make([]byte, 18)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return "cred_" + hex.EncodeToString(raw), nil
}

func associatedData(ref, kind string) []byte {
	return []byte(ref + "\x00" + kind)
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
