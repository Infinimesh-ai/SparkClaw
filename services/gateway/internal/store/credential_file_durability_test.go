package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileCredentialDefiniteFailureRollsBackButRetainsHighWater(t *testing.T) {
	for _, stage := range []string{"encode", "mkdir", "create", "write", "file_sync", "file_close"} {
		t.Run(stage, func(t *testing.T) {
			st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC)
			st.inner.credentialNow = func() time.Time { return now }
			st.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
			candidate := app.CredentialSecret{Ref: "credential-file-failure", Kind: "token", Value: "secret"}
			if got, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(candidate)); StoreErrorCodeOf(err) != StoreErrorDurability || got.Ref != "" {
				t.Fatalf("stage %s candidate=%#v err=%v code=%q", stage, got, err, StoreErrorCodeOf(err))
			}
			if _, found, err := st.GetCredentialSecret(context.Background(), candidate.Ref); err != nil || found {
				t.Fatalf("stage %s rollback found=%v err=%v", stage, found, err)
			}
			if !st.inner.credentialWriteHighWater[candidate.Ref].Equal(now) {
				t.Fatalf("stage %s high-water=%s want=%s", stage, st.inner.credentialWriteHighWater[candidate.Ref], now)
			}
			st.commitOps = osFileCommitOps{}
			saved, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(candidate))
			if err != nil || !saved.CreatedAt.Equal(now.Add(time.Microsecond)) {
				t.Fatalf("stage %s retry=%#v err=%v", stage, saved, err)
			}
		})
	}
}

func TestFileCredentialUnknownOutcomeFencesAndReconciles(t *testing.T) {
	for _, testCase := range []struct {
		name string
		ops  *controlledFileCommitOps
	}{
		{name: "rename applied", ops: &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true}},
		{name: "directory sync", ops: &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			st.commitOps = testCase.ops
			proposed := app.CredentialSecret{Ref: "credential-file-unknown", Kind: "token", Value: "secret"}
			candidate, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(proposed))
			if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || candidate.Ref != proposed.Ref || st.currentFileFence() == nil {
				t.Fatalf("candidate=%#v err=%v code=%q fence=%v", candidate, err, StoreErrorCodeOf(err), st.currentFileFence())
			}
			stored, found, err := st.GetCredentialSecret(context.Background(), proposed.Ref)
			if err != nil || !found || !credentialSecretsEqual(stored, candidate) || st.currentFileFence() != nil {
				t.Fatalf("reconciled=%#v found=%v err=%v fence=%v", stored, found, err, st.currentFileFence())
			}
		})
	}
}

func TestFileCredentialDeleteFailureRestoresSecretAndAudit(t *testing.T) {
	st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.SaveCredentialSecret(context.Background(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-file-delete", Kind: "token", Value: "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	beforeAudit := len(st.ListAudit(""))
	st.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	if deleted, err := st.DeleteCredentialSecret(context.Background(), NewCredentialDeleteCondition(created)); StoreErrorCodeOf(err) != StoreErrorDurability || deleted.Ref != "" {
		t.Fatalf("deleted=%#v err=%v code=%q", deleted, err, StoreErrorCodeOf(err))
	}
	stored, found, err := st.GetCredentialSecret(context.Background(), created.Ref)
	if err != nil || !found || !credentialSecretsEqual(stored, created) || len(st.ListAudit("")) != beforeAudit {
		t.Fatalf("stored=%#v found=%v err=%v audit=%d/%d", stored, found, err, len(st.ListAudit("")), beforeAudit)
	}
}

func TestFileCredentialDestinationReadFailureDoesNotMutate(t *testing.T) {
	st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	st.commitOps = &controlledFileCommitOps{failStage: "read", failRemaining: 1}
	_, err = st.SaveCredentialSecret(context.Background(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-read-failure", Kind: "token", Value: "secret"}))
	if StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, errFileCommitInjected) {
		t.Fatalf("read failure=%v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, found, err := st.GetCredentialSecret(context.Background(), "credential-read-failure"); err != nil || found {
		t.Fatalf("read failure mutated credential found=%v err=%v", found, err)
	}
}
