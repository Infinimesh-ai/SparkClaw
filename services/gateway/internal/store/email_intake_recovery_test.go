package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func intakeRecoveryRepo(t *testing.T, backend string) EmailRepository {
	t.Helper()
	switch backend {
	case "memory":
		return NewMemoryStore()
	case "file":
		s, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	default:
		s, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
}
func intakeOperation(key string, version int64) EmailIntakeOperation {
	return EmailIntakeOperation{Key: strings.Repeat(key, 64), Digest: strings.Repeat("d", 64), Provider: app.EmailProviderGmail, Enabled: true, ExpectedVersion: version}
}
func intakeBind(owner, key string, operation *EmailIntakeOperation, version int64) EmailBindCommand {
	return EmailBindCommand{EmailCommand: EmailCommand{OwnerID: owner, CommandKey: key}, Provider: app.EmailProviderGmail, Address: "owner@example.test", Enabled: true, ExpectedVersion: version, IntakeOperation: operation}
}
func TestEmailIntakeRecoveryContract(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			repo := intakeRecoveryRepo(t, backend)
			ctx := t.Context()
			original := intakeOperation("a", 0)
			before, _ := repo.GetEmailOwnerStatus(ctx, "owner")
			versions, err := repo.GetEmailProviderVersions(ctx, "owner")
			if err != nil || versions[app.EmailProviderGmail] != 0 {
				t.Fatal(versions, err)
			}
			after, _ := repo.GetEmailOwnerStatus(ctx, "owner")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("version GET wrote state")
			}
			// A real delayed legacy writer retains expected_version=0 while recovery commits.
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				<-release
				_, e := repo.BindEmailMailbox(ctx, intakeBind("owner", "legacy-invocation", nil, 0))
				done <- e
			}()
			<-started
			stopped, err := repo.ResolveEmailIntake(ctx, "owner", original)
			if err != nil || stopped.State != EmailIntakeNotApplied || stopped.Version != 1 {
				t.Fatal(stopped, err)
			}
			close(release)
			if err = <-done; StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatal("late legacy commit escaped fence", err)
			}
			boxes, err := repo.ListEmailMailboxes(ctx, "owner")
			if err != nil || len(boxes) != 0 {
				t.Fatal(boxes, err)
			}
			versions, err = repo.GetEmailProviderVersions(ctx, "owner")
			if err != nil || versions[app.EmailProviderGmail] != 1 {
				t.Fatal(versions, err)
			}
			again, err := repo.ResolveEmailIntake(ctx, "owner", original)
			if err != nil || !reflect.DeepEqual(stopped, again) {
				t.Fatal("resolution is not stable", again, err)
			}
			changed := original
			changed.Digest = strings.Repeat("e", 64)
			if _, err = repo.ResolveEmailIntake(ctx, "owner", changed); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatal("changed original accepted", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err = repo.ResolveEmailIntake(canceled, "owner", original); err == nil {
				t.Fatal("canceled recovery succeeded")
			}
			other, _ := repo.GetEmailProviderVersions(ctx, "other")
			if other[app.EmailProviderGmail] != 0 {
				t.Fatal("cross owner fence")
			}
			fresh := intakeOperation("b", 1)
			box, err := repo.BindEmailMailbox(ctx, intakeBind("owner", "next", &fresh, 1))
			if err != nil || box.Version != 2 {
				t.Fatal(box, err)
			}
			committed, err := repo.ResolveEmailIntake(ctx, "owner", fresh)
			if err != nil || committed.State != EmailIntakeCommitted || !reflect.DeepEqual(committed.Mailbox, box) {
				t.Fatal(committed, err)
			}
			// Recovery returns the original result even after a separate setting changes.
			_, err = repo.BindEmailMailbox(ctx, intakeBind("owner", "newer", nil, 2))
			if err != nil {
				t.Fatal(err)
			}
			again, err = repo.ResolveEmailIntake(ctx, "owner", fresh)
			if err != nil || !reflect.DeepEqual(again, committed) {
				t.Fatal("original success changed", again, err)
			}
			ambiguous := intakeOperation("c", 0)
			if _, err = repo.ResolveEmailIntake(ctx, "owner", ambiguous); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatal("legacy changed version was guessed", err)
			}
		})
	}
}
func TestEmailIntakeConcurrentCommitAndRecovery(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			repo := intakeRecoveryRepo(t, backend)
			for n := 0; n < 8; n++ {
				owner := string(rune('a' + n))
				operation := intakeOperation("a", 0)
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				var bindErr, resolveErr error
				var resolved EmailIntakeResolution
				go func() {
					defer wg.Done()
					<-start
					_, bindErr = repo.BindEmailMailbox(t.Context(), intakeBind(owner, "bind", &operation, 0))
				}()
				go func() {
					defer wg.Done()
					<-start
					resolved, resolveErr = repo.ResolveEmailIntake(t.Context(), owner, operation)
				}()
				close(start)
				wg.Wait()
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				boxes, err := repo.ListEmailMailboxes(t.Context(), owner)
				if err != nil {
					t.Fatal(err)
				}
				if resolved.State == EmailIntakeCommitted {
					if bindErr != nil || len(boxes) != 1 || boxes[0].Version != 1 {
						t.Fatal("commit proof incomplete", bindErr, boxes)
					}
				} else if resolved.State == EmailIntakeNotApplied {
					if StoreErrorCodeOf(bindErr) != StoreErrorConflict || len(boxes) != 0 {
						t.Fatal("late commit after negative proof", bindErr, boxes)
					}
				} else {
					t.Fatal(resolved)
				}
				versions, _ := repo.GetEmailProviderVersions(t.Context(), owner)
				if versions[app.EmailProviderGmail] != 1 {
					t.Fatal("duplicate version advance", versions)
				}
			}
		})
	}
}
func TestEmailIntakeFileRecoveryDurability(t *testing.T) {
	for _, stage := range []string{"write", "dir_sync"} {
		t.Run(stage, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "state.json")
			repo, err := NewFileStore(file)
			if err != nil {
				t.Fatal(err)
			}
			operation := intakeOperation("a", 0)
			repo.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
			_, err = repo.ResolveEmailIntake(t.Context(), "owner", operation)
			want := StoreErrorDurability
			if stage == "dir_sync" {
				want = StoreErrorUnknownOutcome
			}
			if StoreErrorCodeOf(err) != want {
				t.Fatal(err)
			}
			repo, err = NewFileStore(file)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := repo.ResolveEmailIntake(t.Context(), "owner", operation)
			if err != nil || resolved.Version != 1 || resolved.State != EmailIntakeNotApplied {
				t.Fatal(resolved, err)
			}
			_, err = repo.BindEmailMailbox(t.Context(), intakeBind("owner", "late", nil, 0))
			if StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatal("restarted fence lost", err)
			}
		})
	}
}

func TestEmailIntakeBindProofSurvivesFileCommitUncertainty(t *testing.T) {
	for _, stage := range []string{"write", "dir_sync"} {
		t.Run(stage, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "state.json")
			repo, err := NewFileStore(file)
			if err != nil {
				t.Fatal(err)
			}
			operation := intakeOperation("a", 0)
			repo.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
			_, err = repo.BindEmailMailbox(t.Context(), intakeBind("owner", "original-bind", &operation, 0))
			want := StoreErrorDurability
			if stage == "dir_sync" {
				want = StoreErrorUnknownOutcome
			}
			if StoreErrorCodeOf(err) != want {
				t.Fatal(err)
			}
			repo, err = NewFileStore(file)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := repo.ResolveEmailIntake(t.Context(), "owner", operation)
			if err != nil {
				t.Fatal(err)
			}
			state := EmailIntakeNotApplied
			if stage == "dir_sync" {
				state = EmailIntakeCommitted
			}
			if resolved.State != state || resolved.Version != 1 {
				t.Fatal(resolved)
			}
		})
	}
}
