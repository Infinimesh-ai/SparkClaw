package store

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type credentialSaveMode uint8

const (
	credentialSaveCreate credentialSaveMode = iota + 1
	credentialSaveReplace
)

type CredentialSaveCommand struct {
	mode          credentialSaveMode
	secret        app.CredentialSecret
	expected      [sha256.Size]byte
	hasExpect     bool
	expectedValid bool
	expectedRef   string
}

type CredentialDeleteCondition struct {
	ref      string
	expected [sha256.Size]byte
	valid    bool
}

func NewCredentialCreate(secret app.CredentialSecret) CredentialSaveCommand {
	return CredentialSaveCommand{mode: credentialSaveCreate, secret: secret}
}

func NewCredentialReplace(previous, replacement app.CredentialSecret) CredentialSaveCommand {
	normalized, err := normalizePersistedCredentialSecret(previous)
	return CredentialSaveCommand{
		mode: credentialSaveReplace, secret: replacement,
		expected: credentialSecretDigest(previous), hasExpect: true,
		expectedValid: err == nil, expectedRef: normalized.Ref,
	}
}

func NewCredentialDeleteCondition(previous app.CredentialSecret) CredentialDeleteCondition {
	normalized, err := normalizePersistedCredentialSecret(previous)
	return CredentialDeleteCondition{
		ref: normalized.Ref, expected: credentialSecretDigest(previous), valid: err == nil,
	}
}

func normalizeCredentialSaveCommand(command CredentialSaveCommand) (CredentialSaveCommand, error) {
	command.secret.Ref = strings.TrimSpace(command.secret.Ref)
	command.secret.Kind = strings.TrimSpace(command.secret.Kind)
	command.secret.CreatedAt = time.Time{}
	command.secret.UpdatedAt = time.Time{}
	if command.secret.Ref == "" || command.secret.Kind == "" || command.secret.Value == "" {
		return CredentialSaveCommand{}, errors.New("credential ref, kind, and value are required")
	}
	switch command.mode {
	case credentialSaveCreate:
		if command.hasExpect {
			return CredentialSaveCommand{}, errors.New("credential create cannot carry an expected version")
		}
	case credentialSaveReplace:
		if !command.hasExpect || !command.expectedValid || command.expectedRef != command.secret.Ref {
			return CredentialSaveCommand{}, errors.New("credential replace requires an expected version")
		}
	default:
		return CredentialSaveCommand{}, errors.New("credential save command is invalid")
	}
	return command, nil
}

func normalizeCredentialDeleteCondition(condition CredentialDeleteCondition) (CredentialDeleteCondition, error) {
	condition.ref = strings.TrimSpace(condition.ref)
	if !condition.valid || condition.ref == "" {
		return CredentialDeleteCondition{}, errors.New("credential delete condition is invalid")
	}
	return condition, nil
}

func normalizePersistedCredentialSecret(secret app.CredentialSecret) (app.CredentialSecret, error) {
	normalizedRef := strings.TrimSpace(secret.Ref)
	if normalizedRef == "" || normalizedRef != secret.Ref {
		return app.CredentialSecret{}, errors.New("credential ref is invalid")
	}
	secret.Kind = strings.TrimSpace(secret.Kind)
	if secret.CreatedAt.IsZero() || secret.UpdatedAt.IsZero() {
		return app.CredentialSecret{}, errors.New("credential timestamps are required")
	}
	return secret, nil
}

func normalizeAndValidatePersistedCredentialSecrets(secrets map[string]app.CredentialSecret) error {
	for ref, secret := range secrets {
		if ref == "" || secret.Ref != ref {
			return errors.New("credential snapshot key does not match embedded ref")
		}
		normalized, err := normalizePersistedCredentialSecret(secret)
		if err != nil {
			return err
		}
		secrets[ref] = normalized
	}
	return nil
}

func credentialSecretsEqual(left, right app.CredentialSecret) bool {
	return left.Ref == right.Ref && left.Kind == right.Kind && left.Value == right.Value &&
		left.CreatedAt.Equal(right.CreatedAt) && left.UpdatedAt.Equal(right.UpdatedAt)
}

func latestCredentialTime(secret app.CredentialSecret) time.Time {
	if secret.UpdatedAt.After(secret.CreatedAt) {
		return secret.UpdatedAt
	}
	return secret.CreatedAt
}

func credentialSecretDigest(secret app.CredentialSecret) [sha256.Size]byte {
	digest := sha256.New()
	writeCredentialDigestBytes(digest, "sparkclaw-credential-secret-version-v1")
	writeCredentialDigestBytes(digest, strings.TrimSpace(secret.Ref))
	writeCredentialDigestBytes(digest, strings.TrimSpace(secret.Kind))
	writeCredentialDigestBytes(digest, secret.Value)
	writeCredentialDigestTime(digest, secret.CreatedAt)
	writeCredentialDigestTime(digest, secret.UpdatedAt)
	var out [sha256.Size]byte
	copy(out[:], digest.Sum(nil))
	return out
}

func writeCredentialDigestBytes(digest hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(value))
}

func writeCredentialDigestTime(digest hash.Hash, value time.Time) {
	value = value.UTC()
	parts := [...]int64{
		int64(value.Year()), int64(value.Month()), int64(value.Day()),
		int64(value.Hour()), int64(value.Minute()), int64(value.Second()), int64(value.Nanosecond()),
	}
	var encoded [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(encoded[:], uint64(part))
		_, _ = digest.Write(encoded[:])
	}
}
