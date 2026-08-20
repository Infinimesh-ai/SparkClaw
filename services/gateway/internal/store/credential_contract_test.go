package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestCredentialRepositoryMemoryAndFileContract(t *testing.T) {
	backends := []struct {
		name string
		new  func(*testing.T) CredentialRepository
	}{
		{name: "memory", new: func(*testing.T) CredentialRepository { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) CredentialRepository {
			st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return st
		}},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			ctx := context.Background()
			proposed := app.CredentialSecret{
				Ref: " credential-contract ", Kind: " token ", Value: " value with surrounding space ",
				CreatedAt: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
			}
			created, err := repository.SaveCredentialSecret(ctx, NewCredentialCreate(proposed))
			if err != nil {
				t.Fatal(err)
			}
			if created.Ref != "credential-contract" || created.Kind != "token" || created.Value != proposed.Value || created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
				t.Fatalf("created credential = %#v", created)
			}
			if _, err := repository.SaveCredentialSecret(ctx, NewCredentialCreate(proposed)); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("duplicate create = %v code=%q", err, StoreErrorCodeOf(err))
			}
			replaced, err := repository.SaveCredentialSecret(ctx, NewCredentialReplace(created, app.CredentialSecret{Ref: created.Ref, Kind: "token-v2", Value: "replacement"}))
			if err != nil {
				t.Fatal(err)
			}
			if !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
				t.Fatalf("replace timestamps created=%s updated=%s prior=%s", replaced.CreatedAt, replaced.UpdatedAt, created.UpdatedAt)
			}
			if _, err := repository.SaveCredentialSecret(ctx, NewCredentialReplace(created, app.CredentialSecret{Ref: created.Ref, Kind: "stale", Value: "stale"})); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("stale replace = %v code=%q", err, StoreErrorCodeOf(err))
			}
			if _, err := repository.DeleteCredentialSecret(ctx, NewCredentialDeleteCondition(created)); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("stale delete = %v code=%q", err, StoreErrorCodeOf(err))
			}
			deleted, err := repository.DeleteCredentialSecret(ctx, NewCredentialDeleteCondition(replaced))
			if err != nil || !credentialSecretsEqual(deleted, replaced) {
				t.Fatalf("delete = %#v err=%v", deleted, err)
			}
			if _, found, err := repository.GetCredentialSecret(ctx, created.Ref); err != nil || found {
				t.Fatalf("deleted get found=%v err=%v", found, err)
			}
			if _, err := repository.DeleteCredentialSecret(ctx, NewCredentialDeleteCondition(replaced)); StoreErrorCodeOf(err) != StoreErrorNotFound {
				t.Fatalf("repeat delete = %v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}
}

func TestCredentialRepositoryValidationAndCancellationPrecedence(t *testing.T) {
	for _, repository := range []CredentialRepository{NewMemoryStore()} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := repository.SaveCredentialSecret(ctx, CredentialSaveCommand{}); StoreErrorCodeOf(err) != StoreErrorCanceled {
			t.Fatalf("save cancellation precedence = %v code=%q", err, StoreErrorCodeOf(err))
		}
		if _, _, err := repository.GetCredentialSecret(ctx, ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
			t.Fatalf("get cancellation precedence = %v code=%q", err, StoreErrorCodeOf(err))
		}
		if _, err := repository.DeleteCredentialSecret(ctx, CredentialDeleteCondition{}); StoreErrorCodeOf(err) != StoreErrorCanceled {
			t.Fatalf("delete cancellation precedence = %v code=%q", err, StoreErrorCodeOf(err))
		}
	}
	invalidPrevious := app.CredentialSecret{Ref: "ref", Kind: "kind", Value: "value"}
	if _, err := NewMemoryStore().SaveCredentialSecret(context.Background(), NewCredentialReplace(invalidPrevious, invalidPrevious)); StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("incomplete replace prior = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, err := NewMemoryStore().DeleteCredentialSecret(context.Background(), NewCredentialDeleteCondition(invalidPrevious)); StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("incomplete delete prior = %v code=%q", err, StoreErrorCodeOf(err))
	}
}

func TestCredentialSecretJSONRedactionAndDigestCompleteness(t *testing.T) {
	redacted := app.CredentialSecret{
		Ref: "ref", Kind: "kind", Value: "credential-canary",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), redacted.Value) || strings.Contains(string(raw), `"value"`) {
		t.Fatalf("credential JSON exposed value: %s", raw)
	}
	secret := app.CredentialSecret{
		Ref: "ref", Kind: "kind", Value: "credential-canary",
		CreatedAt: time.Date(-12345, 2, 3, 4, 5, 6, 7, time.FixedZone("offset", 3600)),
		UpdatedAt: time.Date(23456, 7, 8, 9, 10, 11, 12, time.FixedZone("offset", -7200)),
	}
	base := credentialSecretDigest(secret)
	variants := []app.CredentialSecret{secret, secret, secret, secret, secret}
	variants[0].Ref += "x"
	variants[1].Kind += "x"
	variants[2].Value += "x"
	variants[3].CreatedAt = variants[3].CreatedAt.Add(time.Nanosecond)
	variants[4].UpdatedAt = variants[4].UpdatedAt.Add(time.Nanosecond)
	for index, variant := range variants {
		if credentialSecretDigest(variant) == base {
			t.Fatalf("digest ignored field variant %d", index)
		}
	}
}

func TestFileStoreRejectsEncryptedCredentialEnvelopeWithoutKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"ciphertext":"opaque"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(path); err == nil || !strings.Contains(err.Error(), "requires state encryption") {
		t.Fatalf("encrypted snapshot without key error = %v", err)
	}
}
