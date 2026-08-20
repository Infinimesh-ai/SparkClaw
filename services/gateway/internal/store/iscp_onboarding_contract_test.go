package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestISCPOnboardingRepositoryContract(t *testing.T) {
	factories := map[string]func(*testing.T) ISCPOnboardingRepository{
		"memory": func(*testing.T) ISCPOnboardingRepository { return NewMemoryStore() },
		"file": func(t *testing.T) ISCPOnboardingRepository {
			repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return repository
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			repository := factory(t)
			ctx := context.Background()
			empty, err := repository.ListISCPOnboardings(ctx, "owner-empty")
			if err != nil || empty == nil || len(empty) != 0 {
				t.Fatalf("empty list = %#v err=%v", empty, err)
			}
			if _, found, err := repository.GetISCPOnboarding(ctx, "missing"); err != nil || found {
				t.Fatalf("absence = found %v err=%v", found, err)
			}

			created := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
			for _, receipt := range []app.ISCPOnboarding{
				testISCPOnboarding(created, "receipt-b", "owner-a"),
				testISCPOnboarding(created, "receipt-a", "owner-a"),
				testISCPOnboarding(created.Add(time.Minute), "receipt-other", "owner-b"),
			} {
				if _, err := repository.SaveISCPOnboarding(ctx, receipt); err != nil {
					t.Fatal(err)
				}
			}
			listed, err := repository.ListISCPOnboardings(ctx, "owner-a")
			if err != nil || len(listed) != 2 || listed[0].ID != "receipt-a" || listed[1].ID != "receipt-b" {
				t.Fatalf("ordered scoped list = %#v err=%v", listed, err)
			}
			if _, err := repository.SaveISCPOnboarding(ctx, testISCPOnboarding(created, "receipt-a", "owner-a")); !errors.Is(err, ErrISCPOnboardingConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("duplicate error = %v code=%q", err, StoreErrorCodeOf(err))
			}

			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, _, err := repository.GetISCPOnboarding(canceled, "receipt-a"); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled get error = %v code=%q", err, StoreErrorCodeOf(err))
			}
			if _, err := repository.ListISCPOnboardings(canceled, "owner-a"); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled list error = %v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}
}

func TestMemoryISCPOnboardingObservesTimeoutAfterLockAdmission(t *testing.T) {
	repository := NewMemoryStoreWithOptions(OperationTimeouts{Read: 10 * time.Millisecond, Write: 10 * time.Millisecond})
	repository.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, _, err := repository.GetISCPOnboarding(context.Background(), "missing")
		done <- err
	}()
	time.Sleep(25 * time.Millisecond)
	repository.mu.Unlock()
	if err := <-done; StoreErrorCodeOf(err) != StoreErrorTimeout {
		t.Fatalf("lock timeout error = %v code=%q", err, StoreErrorCodeOf(err))
	}
}

func TestFileISCPOnboardingConcurrentDuplicateHasOneWinner(t *testing.T) {
	repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-concurrent", app.DefaultOwnerID)
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := repository.SaveISCPOnboarding(context.Background(), receipt)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrISCPOnboardingConflict) {
			t.Fatalf("unexpected duplicate save error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful duplicate saves = %d, want 1", successes)
	}
}
