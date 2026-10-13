package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestClientDeletionRemovesManagementMetadataAndNeverReactivates(t *testing.T) {
	for _, backend := range []string{"memory", "file", "encrypted-file"} {
		t.Run(backend, func(t *testing.T) {
			var repository ClientRepository = NewMemoryStore()
			options := FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json"), EncryptAtRest: backend == "encrypted-file", EncryptionKey: "deletion-contract-key"}
			if backend != "memory" {
				repository = mustNewClientFileStore(t, options)
			}
			original := app.Client{ID: "deleted-device", Name: "Old device", TokenHash: "deleted-token-hash"}
			client := mustClaimTestClient(t, repository, original)
			if _, err := repository.DeleteClient(t.Context(), client.ID); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("active deletion error = %v", err)
			}
			if _, _, err := repository.TouchClient(t.Context(), client.ID); err != nil {
				t.Fatal(err)
			}
			revoked, err := repository.RevokeClient(t.Context(), client.ID)
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := repository.DeleteClient(t.Context(), client.ID)
			if err != nil || deleted.DeletedAt == nil || !deleted.DeletedAt.After(*revoked.RevokedAt) || deleted.Name != "" || deleted.LastSeenAt != nil {
				t.Fatalf("delete = %#v err=%v", deleted, err)
			}
			if backend != "memory" {
				repository = mustNewClientFileStore(t, options)
			}
			clients, err := repository.ListClients(t.Context())
			if err != nil || clients == nil || len(clients) != 0 {
				t.Fatalf("management list after deletion/restart = %#v err=%v", clients, err)
			}
			if _, found, err := repository.FindClientByTokenHash(t.Context(), original.TokenHash); err != nil || found {
				t.Fatalf("old token found=%v err=%v", found, err)
			}
			if _, err := repository.RegisterClient(t.Context(), original); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("stale registration revived identity: %v", err)
			}
			original.ID = "different-device"
			if _, err := repository.RegisterClient(t.Context(), original); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("token reused for new identity: %v", err)
			}
			if again, err := repository.DeleteClient(t.Context(), client.ID); err != nil || !ClientsEqual(again, deleted) {
				t.Fatalf("idempotent delete = %#v err=%v", again, err)
			}
			if again, err := repository.RevokeClient(t.Context(), client.ID); err != nil || !ClientsEqual(again, deleted) {
				t.Fatalf("revoke after deletion = %#v err=%v", again, err)
			}
			// Returned lifecycle pointers cannot mutate the persisted tombstone.
			*deleted.DeletedAt = deleted.DeletedAt.Add(time.Hour)
			stored, found, err := repository.GetClient(t.Context(), client.ID)
			if err != nil || !found || stored.DeletedAt.Equal(*deleted.DeletedAt) {
				t.Fatalf("tombstone pointer escaped: %#v found=%v err=%v", stored, found, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := repository.DeleteClient(ctx, client.ID); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled delete = %v", err)
			}
		})
	}
}

func TestFileClientDeletionRollbackAndUnknownOutcome(t *testing.T) {
	options := FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")}
	repository := mustNewClientFileStore(t, options)
	client := mustClaimTestClient(t, repository, app.Client{ID: "delete-durability", Name: "Old device", TokenHash: "durability-hash"})
	if _, err := repository.RevokeClient(t.Context(), client.ID); err != nil {
		t.Fatal(err)
	}
	before := repository.captureFileRollback()
	repository.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	if candidate, err := repository.DeleteClient(t.Context(), client.ID); StoreErrorCodeOf(err) != StoreErrorDurability || candidate.ID != "" {
		t.Fatalf("failed deletion candidate=%#v err=%v", candidate, err)
	}
	if after := repository.captureFileRollback(); !reflect.DeepEqual(before, after) {
		t.Fatal("failed deletion changed device/audit/event state")
	}
	repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	deleted, err := repository.DeleteClient(t.Context(), client.ID)
	if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || deleted.DeletedAt == nil {
		t.Fatalf("unknown deletion = %#v err=%v", deleted, err)
	}
	stored, found, err := repository.GetClient(t.Context(), client.ID)
	if err != nil || !found || !ClientsEqual(stored, deleted) || repository.currentFileFence() != nil {
		t.Fatalf("deletion reconciliation = %#v found=%v err=%v", stored, found, err)
	}
	reloaded := mustNewClientFileStore(t, options)
	if clients, err := reloaded.ListClients(t.Context()); err != nil || len(clients) != 0 {
		t.Fatalf("restart resurrected deleted row = %#v err=%v", clients, err)
	}
}

func TestPostgresClientDeletionTransactionOutcomes(t *testing.T) {
	now := time.Now().UTC()
	active := validPostgresClient(now)
	transaction := &fakeClientPostgresTx{rowQueue: []onboardingPostgresRow{clientPostgresRow(active)}}
	repository, _, session := newFakePostgresClientStore(now, transaction)
	if _, err := repository.DeleteClient(t.Context(), active.ID); StoreErrorCodeOf(err) != StoreErrorConflict || transaction.commits != 0 || transaction.rollbacks != 1 || session.releases != 1 {
		t.Fatalf("active deletion err=%v commits=%d rollbacks=%d releases=%d", err, transaction.commits, transaction.rollbacks, session.releases)
	}
	for _, outcome := range []string{"success", "commit-unknown"} {
		t.Run(outcome, func(t *testing.T) {
			revoked := active
			at := now.Add(-time.Minute)
			revoked.RevokedAt = &at
			transaction := &fakeClientPostgresTx{rowQueue: []onboardingPostgresRow{clientPostgresRow(revoked)}}
			if outcome == "commit-unknown" {
				transaction.commitErr = errors.New("commit response lost")
			}
			repository, _, session := newFakePostgresClientStore(now, transaction)
			deleted, err := repository.DeleteClient(t.Context(), revoked.ID)
			if deleted.DeletedAt == nil || deleted.Name != "" || len(transaction.execSQL) != 4 || transaction.commits != 1 {
				t.Fatalf("deletion candidate=%#v err=%v SQL=%v", deleted, err, transaction.execSQL)
			}
			if outcome == "success" && (err != nil || session.releases != 1 || session.terminates != 0) {
				t.Fatalf("success err=%v releases=%d terminates=%d", err, session.releases, session.terminates)
			}
			if outcome == "commit-unknown" && (StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || session.releases != 0 || session.terminates != 1) {
				t.Fatalf("unknown commit err=%v releases=%d terminates=%d", err, session.releases, session.terminates)
			}
		})
	}
}
