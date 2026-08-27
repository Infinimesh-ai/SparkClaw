package credential

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

const (
	CodeInvalid        = "credential_invalid"
	CodeCanceled       = "credential_canceled"
	CodeUnavailable    = "credential_unavailable"
	CodeKeyUnavailable = "credential_key_unavailable"
	CodeUnsealFailed   = "credential_unseal_failed"

	legacyWeixinCredentialKind = "openclaw-weixin-bot-token"
	legacyWeixinRefPrefix      = "provider:" + weixinproto.QRProvider + ":"
	maxSealIdentityBytes       = 256
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
	case CodeInvalid:
		return "credential input is invalid"
	case CodeCanceled:
		return "credential operation was canceled"
	case CodeUnavailable:
		return "credential is temporarily unavailable"
	case CodeKeyUnavailable:
		return "credential encryption key is unavailable"
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

func (e *Error) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
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
	BindLifecycle(context.Context)
	Seal(context.Context, string, string, []byte) (string, error)
	Open(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
	AbortSeal(context.Context, string, string) error
}

type pendingMode uint8

const (
	pendingCreate pendingMode = iota + 1
	pendingDelete
	pendingLegacyRewrap
	pendingReplace
)

type pendingCommand struct {
	mode        pendingMode
	generation  uint64
	bindingID   string
	kind        string
	ref         string
	fingerprint [sha256.Size]byte
	command     store.CredentialSaveCommand
	condition   store.CredentialDeleteCondition
	previous    app.CredentialSecret
	candidate   app.CredentialSecret
}

type Vault struct {
	repository store.CredentialRepository
	aead       cipher.AEAD
	refKey     [sha256.Size]byte
	readyErr   error

	mu                  sync.Mutex
	lifecycleGeneration uint64
	pending             *pendingCommand
}

type envelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"alg"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func New(repository store.CredentialRepository, options Options) *Vault {
	vault := &Vault{repository: repository}
	if repository == nil {
		vault.readyErr = &Error{Code: CodeKeyUnavailable, cause: errors.New("credential repository is nil")}
		return vault
	}
	key, err := loadKey(options)
	if err != nil {
		vault.readyErr = &Error{Code: CodeKeyUnavailable, cause: err}
		return vault
	}
	refKey := hmac.New(sha256.New, key)
	_, _ = refKey.Write([]byte("sparkclaw-credential-ref-key-v1"))
	copy(vault.refKey[:], refKey.Sum(nil))
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
	if v == nil || v.repository == nil || v.aead == nil {
		if v != nil && v.readyErr != nil {
			return v.readyErr
		}
		return &Error{Code: CodeKeyUnavailable, cause: errors.New("credential vault is not initialized")}
	}
	return nil
}

func (v *Vault) BindLifecycle(ctx context.Context) {
	if v == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	v.mu.Lock()
	v.lifecycleGeneration++
	generation := v.lifecycleGeneration
	v.clearPendingLocked()
	v.mu.Unlock()
	go func() {
		<-ctx.Done()
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.lifecycleGeneration == generation {
			v.lifecycleGeneration++
			v.clearPendingLocked()
		}
	}()
}

func (v *Vault) Seal(ctx context.Context, bindingID, kind string, plaintext []byte) (string, error) {
	if err := v.Ready(); err != nil {
		return "", err
	}
	bindingID = strings.TrimSpace(bindingID)
	kind = strings.TrimSpace(kind)
	if bindingID == "" || len([]byte(bindingID)) > maxSealIdentityBytes || kind == "" || len(plaintext) == 0 {
		return "", credentialError(CodeInvalid, errors.New("credential binding, kind, and value are required"))
	}
	if err := ctx.Err(); err != nil {
		return "", credentialError(CodeCanceled, err)
	}
	fingerprint := v.plaintextFingerprint(kind, plaintext)
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", credentialError(CodeCanceled, err)
	}
	intent := pendingCommand{mode: pendingCreate, bindingID: bindingID, kind: kind, fingerprint: fingerprint}
	if ref, completed, err := v.resolvePendingLocked(ctx, intent); err != nil || completed {
		return ref, err
	}
	ref := v.credentialRef(bindingID)
	stored, found, err := v.repository.GetCredentialSecret(ctx, ref)
	if err != nil {
		return "", v.mapRepositoryError(err)
	}
	if found {
		return v.resolveExistingSeal(ref, kind, plaintext, stored)
	}
	sealedValue, err := v.sealEnvelope(ref, kind, plaintext)
	if err != nil {
		return "", credentialError(CodeUnavailable, err)
	}
	command := store.NewCredentialCreate(app.CredentialSecret{Ref: ref, Kind: kind, Value: sealedValue})
	candidate, err := v.repository.SaveCredentialSecret(ctx, command)
	if err == nil {
		return ref, nil
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorConflict {
		stored, found, readErr := v.repository.GetCredentialSecret(ctx, ref)
		if readErr != nil {
			return "", v.mapRepositoryError(readErr)
		}
		if found {
			return v.resolveExistingSeal(ref, kind, plaintext, stored)
		}
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome && candidate.Ref != "" {
		v.pending = &pendingCommand{
			mode: pendingCreate, generation: v.lifecycleGeneration, bindingID: bindingID, kind: kind,
			ref: ref, fingerprint: fingerprint, command: command, candidate: candidate,
		}
	}
	return "", v.mapRepositoryError(err)
}

func (v *Vault) Open(ctx context.Context, ref string) ([]byte, error) {
	if err := v.Ready(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, credentialError(CodeCanceled, err)
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, credentialError(CodeUnsealFailed, errors.New("credential reference is required"))
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, credentialError(CodeCanceled, err)
	}
	if _, _, err := v.resolvePendingLocked(ctx, pendingCommand{ref: ref}); err != nil {
		return nil, err
	}
	stored, found, err := v.repository.GetCredentialSecret(ctx, ref)
	if err != nil {
		return nil, v.mapRepositoryError(err)
	}
	if !found {
		return nil, credentialError(CodeUnsealFailed, errors.New("credential reference was not found"))
	}
	plaintext, isEnvelope, openErr := v.openEnvelope(stored)
	if isEnvelope {
		if openErr != nil {
			return nil, credentialError(CodeUnsealFailed, openErr)
		}
		return plaintext, nil
	}
	if stored.Kind != legacyWeixinCredentialKind || stored.Value == "" {
		return nil, credentialError(CodeUnsealFailed, errors.New("credential envelope is invalid"))
	}
	plaintext = []byte(stored.Value)
	sealedValue, err := v.sealEnvelope(stored.Ref, stored.Kind, plaintext)
	if err != nil {
		zero(plaintext)
		return nil, credentialError(CodeUnavailable, err)
	}
	replacement := app.CredentialSecret{Ref: stored.Ref, Kind: stored.Kind, Value: sealedValue}
	command := store.NewCredentialReplace(stored, replacement)
	candidate, err := v.repository.SaveCredentialSecret(ctx, command)
	if err == nil {
		return plaintext, nil
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome && candidate.Ref != "" {
		v.pending = &pendingCommand{
			mode: pendingLegacyRewrap, generation: v.lifecycleGeneration, kind: stored.Kind, ref: stored.Ref,
			fingerprint: v.plaintextFingerprint(stored.Kind, plaintext), command: command, candidate: candidate,
		}
	}
	zero(plaintext)
	return nil, v.mapRepositoryError(err)
}

// OpenBinding opens a Vault-owned stable binding without exposing its
// deterministic repository reference. The boolean is false when the binding
// has never been created.
func (v *Vault) OpenBinding(ctx context.Context, bindingID, kind string) ([]byte, bool, error) {
	if err := v.Ready(); err != nil {
		return nil, false, err
	}
	bindingID = strings.TrimSpace(bindingID)
	kind = strings.TrimSpace(kind)
	if bindingID == "" || len([]byte(bindingID)) > maxSealIdentityBytes || kind == "" {
		return nil, false, credentialError(CodeInvalid, errors.New("credential binding and kind are required"))
	}
	if err := ctx.Err(); err != nil {
		return nil, false, credentialError(CodeCanceled, err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	ref := v.credentialRef(bindingID)
	if _, _, err := v.resolvePendingLocked(ctx, pendingCommand{ref: ref}); err != nil {
		return nil, false, err
	}
	stored, found, err := v.repository.GetCredentialSecret(ctx, ref)
	if err != nil {
		return nil, false, v.mapRepositoryError(err)
	}
	if !found {
		return nil, false, nil
	}
	if stored.Kind != kind {
		return nil, false, credentialError(CodeUnsealFailed, errors.New("credential binding kind does not match"))
	}
	plaintext, isEnvelope, err := v.openEnvelope(stored)
	if !isEnvelope || err != nil {
		zero(plaintext)
		return nil, false, credentialError(CodeUnsealFailed, errors.New("credential envelope is invalid"))
	}
	return plaintext, true, nil
}

// ReplaceBinding atomically creates or replaces the encrypted value at a
// stable binding. Connector Seal semantics intentionally remain immutable.
func (v *Vault) ReplaceBinding(ctx context.Context, bindingID, kind string, plaintext []byte) error {
	if err := v.Ready(); err != nil {
		return err
	}
	bindingID = strings.TrimSpace(bindingID)
	kind = strings.TrimSpace(kind)
	if bindingID == "" || len([]byte(bindingID)) > maxSealIdentityBytes || kind == "" || len(plaintext) == 0 {
		return credentialError(CodeInvalid, errors.New("credential binding, kind, and value are required"))
	}
	if err := ctx.Err(); err != nil {
		return credentialError(CodeCanceled, err)
	}
	fingerprint := v.plaintextFingerprint(kind, plaintext)
	v.mu.Lock()
	defer v.mu.Unlock()
	intent := pendingCommand{mode: pendingReplace, bindingID: bindingID, kind: kind, fingerprint: fingerprint}
	if _, completed, err := v.resolvePendingLocked(ctx, intent); err != nil || completed {
		return err
	}
	ref := v.credentialRef(bindingID)
	stored, found, err := v.repository.GetCredentialSecret(ctx, ref)
	if err != nil {
		return v.mapRepositoryError(err)
	}
	sealedValue, err := v.sealEnvelope(ref, kind, plaintext)
	if err != nil {
		return credentialError(CodeUnavailable, err)
	}
	replacement := app.CredentialSecret{Ref: ref, Kind: kind, Value: sealedValue}
	var command store.CredentialSaveCommand
	if found {
		if stored.Kind != kind {
			return credentialError(CodeInvalid, errors.New("credential binding is already in use"))
		}
		command = store.NewCredentialReplace(stored, replacement)
	} else {
		command = store.NewCredentialCreate(replacement)
	}
	candidate, err := v.repository.SaveCredentialSecret(ctx, command)
	if err == nil {
		return nil
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome && candidate.Ref != "" {
		v.pending = &pendingCommand{
			mode: pendingReplace, generation: v.lifecycleGeneration, bindingID: bindingID, kind: kind,
			ref: ref, fingerprint: fingerprint, command: command, previous: stored, candidate: candidate,
		}
	}
	return v.mapRepositoryError(err)
}

// DeleteBinding deletes only the value owned by the supplied stable binding
// and kind. A missing binding is already in the desired state.
func (v *Vault) DeleteBinding(ctx context.Context, bindingID, kind string) error {
	if err := v.Ready(); err != nil {
		return err
	}
	bindingID = strings.TrimSpace(bindingID)
	kind = strings.TrimSpace(kind)
	if bindingID == "" || len([]byte(bindingID)) > maxSealIdentityBytes || kind == "" {
		return credentialError(CodeInvalid, errors.New("credential binding and kind are required"))
	}
	return v.deleteAuthenticated(ctx, v.credentialRef(bindingID), kind, false)
}

func (v *Vault) Delete(ctx context.Context, ref string) error {
	if err := v.Ready(); err != nil {
		return err
	}
	ref = strings.TrimSpace(ref)
	legacyWeixin := isLegacyWeixinCredentialRef(ref)
	if !strings.HasPrefix(ref, "cred_") && !legacyWeixin {
		return credentialError(CodeInvalid, errors.New("credential reference is not Vault-owned"))
	}
	return v.deleteAuthenticated(ctx, ref, "", legacyWeixin)
}

func (v *Vault) AbortSeal(ctx context.Context, bindingID, kind string) error {
	if err := v.Ready(); err != nil {
		return err
	}
	bindingID = strings.TrimSpace(bindingID)
	kind = strings.TrimSpace(kind)
	if bindingID == "" || len([]byte(bindingID)) > maxSealIdentityBytes || kind == "" {
		return credentialError(CodeInvalid, errors.New("credential binding and kind are required"))
	}
	return v.deleteAuthenticated(ctx, v.credentialRef(bindingID), kind, false)
}

func (v *Vault) deleteAuthenticated(ctx context.Context, ref, expectedKind string, allowLegacyWeixinPlaintext bool) error {
	if err := ctx.Err(); err != nil {
		return credentialError(CodeCanceled, err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return credentialError(CodeCanceled, err)
	}
	if _, _, err := v.resolvePendingLocked(ctx, pendingCommand{ref: ref}); err != nil {
		return err
	}
	stored, found, err := v.repository.GetCredentialSecret(ctx, ref)
	if err != nil {
		return v.mapRepositoryError(err)
	}
	if !found {
		return nil
	}
	if expectedKind != "" && stored.Kind != expectedKind {
		return credentialError(CodeUnavailable, errors.New("credential kind does not match cleanup proof"))
	}
	if allowLegacyWeixinPlaintext && stored.Kind != legacyWeixinCredentialKind {
		return credentialError(CodeUnavailable, errors.New("legacy credential kind does not match cleanup proof"))
	}
	plaintext, isEnvelope, openErr := v.openEnvelope(stored)
	zero(plaintext)
	if openErr != nil || (!isEnvelope && (!allowLegacyWeixinPlaintext || stored.Value == "")) {
		return credentialError(CodeUnavailable, errors.New("credential envelope does not match cleanup proof"))
	}
	condition := store.NewCredentialDeleteCondition(stored)
	deleted, err := v.repository.DeleteCredentialSecret(ctx, condition)
	deleted.Value = ""
	if err == nil || store.StoreErrorCodeOf(err) == store.StoreErrorNotFound {
		return nil
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
		v.pending = &pendingCommand{
			mode: pendingDelete, generation: v.lifecycleGeneration, ref: ref, condition: condition,
		}
	}
	return v.mapRepositoryError(err)
}

func isLegacyWeixinCredentialRef(ref string) bool {
	bindingID, ok := strings.CutPrefix(ref, legacyWeixinRefPrefix)
	return ok && bindingID != "" && bindingID == strings.TrimSpace(bindingID) && len([]byte(bindingID)) <= maxSealIdentityBytes
}

func (v *Vault) resolveExistingSeal(ref, kind string, plaintext []byte, stored app.CredentialSecret) (string, error) {
	if stored.Kind != kind {
		return "", credentialError(CodeInvalid, errors.New("credential binding is already in use"))
	}
	opened, isEnvelope, err := v.openEnvelope(stored)
	if !isEnvelope || err != nil {
		zero(opened)
		return "", credentialError(CodeInvalid, errors.New("credential binding is already in use"))
	}
	equal := len(opened) == len(plaintext) && subtle.ConstantTimeCompare(opened, plaintext) == 1
	zero(opened)
	if !equal {
		return "", credentialError(CodeInvalid, errors.New("credential binding is already in use"))
	}
	return ref, nil
}

func (v *Vault) resolvePendingLocked(ctx context.Context, intent pendingCommand) (string, bool, error) {
	if v.pending == nil {
		return "", false, nil
	}
	pending := v.pending
	switch pending.mode {
	case pendingCreate:
		stored, found, err := v.repository.GetCredentialSecret(ctx, pending.ref)
		if err != nil {
			return "", false, v.mapRepositoryError(err)
		}
		same := intent.mode == pendingCreate && intent.bindingID == pending.bindingID && intent.kind == pending.kind && hmac.Equal(intent.fingerprint[:], pending.fingerprint[:])
		if !found {
			if !same {
				v.clearPendingLocked()
				return "", false, nil
			}
			candidate, err := v.repository.SaveCredentialSecret(ctx, pending.command)
			if err == nil {
				ref := pending.ref
				v.clearPendingLocked()
				return ref, true, nil
			}
			if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome && candidate.Ref != "" {
				pending.candidate = candidate
			}
			return "", false, v.mapRepositoryError(err)
		}
		if credentialSecretsEqual(stored, pending.candidate) {
			if same {
				ref := pending.ref
				v.clearPendingLocked()
				return ref, true, nil
			}
			condition := store.NewCredentialDeleteCondition(stored)
			deleted, err := v.repository.DeleteCredentialSecret(ctx, condition)
			deleted.Value = ""
			if err == nil || store.StoreErrorCodeOf(err) == store.StoreErrorNotFound {
				v.clearPendingLocked()
				return "", false, nil
			}
			v.pending = &pendingCommand{mode: pendingDelete, generation: pending.generation, ref: pending.ref, condition: condition}
			return "", false, v.mapRepositoryError(err)
		}
		if same {
			return "", false, credentialError(CodeInvalid, errors.New("credential binding is already in use"))
		}
		return "", false, credentialError(CodeUnavailable, errors.New("pending credential create conflicts with stored state"))
	case pendingDelete:
		deleted, err := v.repository.DeleteCredentialSecret(ctx, pending.condition)
		deleted.Value = ""
		if err == nil || store.StoreErrorCodeOf(err) == store.StoreErrorNotFound {
			v.clearPendingLocked()
			return "", false, nil
		}
		return "", false, v.mapRepositoryError(err)
	case pendingLegacyRewrap:
		stored, found, err := v.repository.GetCredentialSecret(ctx, pending.ref)
		if err != nil {
			return "", false, v.mapRepositoryError(err)
		}
		if !found {
			v.clearPendingLocked()
			return "", false, credentialError(CodeUnsealFailed, errors.New("credential reference was not found"))
		}
		if credentialSecretsEqual(stored, pending.candidate) {
			v.clearPendingLocked()
			return "", false, nil
		}
		opened, isEnvelope, openErr := v.openEnvelope(stored)
		if isEnvelope {
			fingerprint := v.plaintextFingerprint(stored.Kind, opened)
			matches := openErr == nil && stored.Kind == pending.kind && hmac.Equal(fingerprint[:], pending.fingerprint[:])
			zero(opened)
			if matches {
				v.clearPendingLocked()
				return "", false, nil
			}
			return "", false, credentialError(CodeUnavailable, errors.New("pending credential rewrap conflicts with stored state"))
		}
		candidate, err := v.repository.SaveCredentialSecret(ctx, pending.command)
		if err == nil {
			v.clearPendingLocked()
			return "", false, nil
		}
		if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome && candidate.Ref != "" {
			pending.candidate = candidate
		}
		return "", false, v.mapRepositoryError(err)
	case pendingReplace:
		stored, found, err := v.repository.GetCredentialSecret(ctx, pending.ref)
		if err != nil {
			return "", false, v.mapRepositoryError(err)
		}
		same := intent.mode == pendingReplace && intent.bindingID == pending.bindingID && intent.kind == pending.kind && hmac.Equal(intent.fingerprint[:], pending.fingerprint[:])
		if found && credentialSecretsEqual(stored, pending.candidate) {
			v.clearPendingLocked()
			return "", same, nil
		}
		previousStillCurrent := (!found && pending.previous.Ref == "") || (found && credentialSecretsEqual(stored, pending.previous))
		if !previousStillCurrent {
			return "", false, credentialError(CodeUnavailable, errors.New("pending credential replace conflicts with stored state"))
		}
		if !same {
			v.clearPendingLocked()
			return "", false, nil
		}
		candidate, err := v.repository.SaveCredentialSecret(ctx, pending.command)
		if err == nil {
			v.clearPendingLocked()
			return "", true, nil
		}
		if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome && candidate.Ref != "" {
			pending.candidate = candidate
		}
		return "", false, v.mapRepositoryError(err)
	default:
		return "", false, credentialError(CodeUnavailable, errors.New("credential pending state is invalid"))
	}
}

func (v *Vault) clearPendingLocked() {
	if v.pending == nil {
		return
	}
	v.pending.candidate.Value = ""
	v.pending.previous.Value = ""
	v.pending.command = store.CredentialSaveCommand{}
	v.pending.condition = store.CredentialDeleteCondition{}
	zero(v.pending.fingerprint[:])
	v.pending = nil
}

func (v *Vault) sealEnvelope(ref, kind string, plaintext []byte) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	secretCopy := append([]byte(nil), plaintext...)
	defer zero(secretCopy)
	ciphertext := v.aead.Seal(nil, nonce, secretCopy, associatedData(ref, kind))
	raw, err := json.Marshal(envelope{
		Version: 1, Algorithm: "AES-256-GCM",
		Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (v *Vault) openEnvelope(stored app.CredentialSecret) ([]byte, bool, error) {
	var sealed envelope
	if err := json.Unmarshal([]byte(stored.Value), &sealed); err != nil {
		return nil, false, nil
	}
	isEnvelope := sealed.Version != 0 || sealed.Algorithm != "" || sealed.Nonce != "" || sealed.Ciphertext != ""
	if !isEnvelope {
		return nil, false, nil
	}
	if sealed.Version != 1 || sealed.Algorithm != "AES-256-GCM" {
		return nil, true, errors.New("credential envelope version is unsupported")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(sealed.Nonce)
	if err != nil || len(nonce) != v.aead.NonceSize() {
		return nil, true, errors.New("credential nonce is invalid")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(sealed.Ciphertext)
	if err != nil {
		return nil, true, errors.New("credential ciphertext is invalid")
	}
	plaintext, err := v.aead.Open(nil, nonce, ciphertext, associatedData(stored.Ref, stored.Kind))
	if err != nil {
		return nil, true, errors.New("credential authentication failed")
	}
	return plaintext, true, nil
}

func (v *Vault) credentialRef(bindingID string) string {
	digest := hmac.New(sha256.New, v.refKey[:])
	_, _ = digest.Write([]byte("sparkclaw-credential-ref-v1"))
	writeLengthDelimited(digest, []byte(bindingID))
	return "cred_" + base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

func (v *Vault) plaintextFingerprint(kind string, plaintext []byte) [sha256.Size]byte {
	digest := hmac.New(sha256.New, v.refKey[:])
	_, _ = digest.Write([]byte("sparkclaw-credential-plaintext-fingerprint-v1"))
	writeLengthDelimited(digest, []byte(kind))
	writeLengthDelimited(digest, plaintext)
	var out [sha256.Size]byte
	copy(out[:], digest.Sum(nil))
	return out
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeLengthDelimited(writer byteWriter, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}

func (v *Vault) mapRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	if store.StoreErrorCodeOf(err) == store.StoreErrorCanceled || errors.Is(err, context.Canceled) {
		return credentialError(CodeCanceled, err)
	}
	return credentialError(CodeUnavailable, err)
}

func credentialError(code string, cause error) *Error {
	return &Error{Code: code, cause: cause}
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

func associatedData(ref, kind string) []byte {
	return []byte(ref + "\x00" + kind)
}

func credentialSecretsEqual(left, right app.CredentialSecret) bool {
	return left.Ref == right.Ref && left.Kind == right.Kind && left.Value == right.Value &&
		left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt)
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
