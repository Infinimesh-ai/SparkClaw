package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestOwnerRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []struct {
		name string
		new  func(testing.TB) Store
	}{
		{name: "memory", new: func(testing.TB) Store { return NewMemoryStore() }},
		{name: "file", new: func(t testing.TB) Store {
			store, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return store
		}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			createdAt := time.Date(2026, 8, 20, 1, 2, 3, 456789123, time.FixedZone("test", 8*60*60))
			preferences := map[string]string{"tone": "brief"}
			auditsBefore := len(repository.ListAudit(""))
			eventsBefore := len(repository.EventsAfter("", ""))
			saved, err := repository.SaveOwnerProfile(context.Background(), app.OwnerProfile{
				ID: "  owner-contract  ", Source: "  source  ", ExternalRef: "  external  ",
				WorkspaceRoot: "  /workspace  ", DefaultChannel: "  weixin  ",
				DefaultBindingID: "  binding  ", DisplayName: "   ", Email: "  owner@example.test  ",
				Preferences: preferences, CreatedAt: createdAt,
			})
			if err != nil {
				t.Fatal(err)
			}
			if saved.ID != "owner-contract" || saved.Source != "source" || saved.ExternalRef != "external" ||
				saved.WorkspaceRoot != "/workspace" || saved.DefaultChannel != "weixin" ||
				saved.DefaultBindingID != "binding" || saved.DisplayName != "Owner" || saved.Email != "owner@example.test" {
				t.Fatalf("normalized profile = %#v", saved)
			}
			wantCreatedAt := createdAt.UTC().Truncate(time.Microsecond)
			if !saved.CreatedAt.Equal(wantCreatedAt) || saved.CreatedAt.Location() != time.UTC ||
				saved.UpdatedAt.Nanosecond()%1000 != 0 || saved.Preferences == nil {
				t.Fatalf("assigned metadata = %#v", saved)
			}
			if len(repository.ListAudit("")) != auditsBefore+1 || len(repository.EventsAfter("", "")) != eventsBefore+1 {
				t.Fatal("owner profile, audit, and event did not commit together")
			}

			preferences["tone"] = "input-mutated"
			saved.Preferences["tone"] = "output-mutated"
			got, found, err := repository.GetOwnerProfileByID(context.Background(), saved.ID)
			if err != nil || !found || got.Preferences["tone"] != "brief" {
				t.Fatalf("input/output clone = %#v found=%v err=%v", got, found, err)
			}
			got.Preferences["tone"] = "read-mutated"
			again, found, err := repository.GetOwnerProfileByID(context.Background(), saved.ID)
			if err != nil || !found || again.Preferences["tone"] != "brief" {
				t.Fatalf("read clone = %#v found=%v err=%v", again, found, err)
			}

			if missing, found, err := repository.GetOwnerProfileByID(context.Background(), "missing"); err != nil || found || !reflect.ValueOf(missing).IsZero() {
				t.Fatalf("normal absence = %#v found=%v err=%v", missing, found, err)
			}
			defaultProfile, err := repository.GetOwnerProfile(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			byBlankID, found, err := repository.GetOwnerProfileByID(context.Background(), "   ")
			if err != nil || !found || !OwnerProfilesEqual(defaultProfile, byBlankID) {
				t.Fatalf("blank ID = %#v found=%v err=%v", byBlankID, found, err)
			}
			if _, found, err := repository.FindOwnerProfileByExternalRef(context.Background(), "", "external"); err != nil || found {
				t.Fatalf("blank source lookup found=%v err=%v", found, err)
			}
			if _, found, err := repository.FindOwnerProfileByExternalRef(context.Background(), "source", ""); err != nil || found {
				t.Fatalf("blank external ref lookup found=%v err=%v", found, err)
			}
		})
	}
}

func TestOwnerRepositoryOrderingAndExternalRefTieBreak(t *testing.T) {
	createdAt := time.Date(2026, 8, 20, 0, 0, 0, 123, time.UTC)
	newer := createdAt.Add(2 * time.Hour)
	tied := createdAt.Add(time.Hour)
	defaultProfile := app.OwnerProfile{
		ID: app.DefaultOwnerID, Source: "web", DisplayName: "Owner", Preferences: map[string]string{},
		CreatedAt: createdAt, UpdatedAt: newer,
	}
	profiles := map[string]app.OwnerProfile{
		app.DefaultOwnerID: defaultProfile,
		"owner-b":          {ID: "owner-b", Source: "weixin", ExternalRef: "same", DisplayName: "B", Preferences: map[string]string{}, CreatedAt: createdAt, UpdatedAt: tied},
		"owner-a":          {ID: "owner-a", Source: "weixin", ExternalRef: "same", DisplayName: "A", Preferences: map[string]string{}, CreatedAt: createdAt, UpdatedAt: tied},
	}

	memory := NewMemoryStore()
	memory.loadSnapshot(Snapshot{OwnerProfile: defaultProfile, OwnerProfiles: profiles})
	path := filepath.Join(t.TempDir(), "state.json")
	raw, err := json.Marshal(Snapshot{OwnerProfile: defaultProfile, OwnerProfiles: profiles})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, backend := range []struct {
		name       string
		repository OwnerRepository
	}{{name: "memory", repository: memory}, {name: "file", repository: fileStore}} {
		t.Run(backend.name, func(t *testing.T) {
			listed, err := backend.repository.ListOwnerProfiles(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != 3 || listed[0].ID != app.DefaultOwnerID || listed[1].ID != "owner-a" || listed[2].ID != "owner-b" {
				t.Fatalf("ordered owners = %#v", listed)
			}
			found, ok, err := backend.repository.FindOwnerProfileByExternalRef(context.Background(), " weixin ", " same ")
			if err != nil || !ok || found.ID != "owner-a" {
				t.Fatalf("tie-break lookup = %#v found=%v err=%v", found, ok, err)
			}
			listed[0].Preferences["mutated"] = "yes"
			fresh, err := backend.repository.ListOwnerProfiles(context.Background())
			if err != nil || len(fresh[0].Preferences) != 0 {
				t.Fatalf("list clone = %#v err=%v", fresh, err)
			}
		})
	}
}

func TestOwnerRepositoryCancellationAndTimeout(t *testing.T) {
	for _, backend := range []struct {
		name       string
		repository OwnerRepository
	}{{name: "memory", repository: NewMemoryStore()}, {name: "file", repository: mustNewOwnerFileStore(t, FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")})}} {
		t.Run(backend.name, func(t *testing.T) {
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			calls := []func(context.Context) error{
				func(ctx context.Context) error { _, err := backend.repository.GetOwnerProfile(ctx); return err },
				func(ctx context.Context) error {
					_, err := backend.repository.UpdateOwnerProfile(ctx, app.OwnerProfile{})
					return err
				},
				func(ctx context.Context) error {
					_, _, err := backend.repository.GetOwnerProfileByID(ctx, "owner")
					return err
				},
				func(ctx context.Context) error {
					_, err := backend.repository.SaveOwnerProfile(ctx, app.OwnerProfile{ID: "owner-canceled"})
					return err
				},
				func(ctx context.Context) error { _, err := backend.repository.ListOwnerProfiles(ctx); return err },
				func(ctx context.Context) error {
					_, _, err := backend.repository.FindOwnerProfileByExternalRef(ctx, "source", "ref")
					return err
				},
			}
			for index, call := range calls {
				if err := call(canceled); StoreErrorCodeOf(err) != StoreErrorCanceled {
					t.Fatalf("call %d canceled error = %v code=%q", index, err, StoreErrorCodeOf(err))
				}
			}

			timedOut, timeoutCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer timeoutCancel()
			if _, err := backend.repository.SaveOwnerProfile(timedOut, app.OwnerProfile{ID: "owner-timeout"}); StoreErrorCodeOf(err) != StoreErrorTimeout {
				t.Fatalf("timeout error = %v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}

	fileStore := mustNewOwnerFileStore(t, FileStoreOptions{
		Path: filepath.Join(t.TempDir(), "blocked.json"), TransactionTimeout: 20 * time.Millisecond,
	})
	if err := fileStore.admission.Acquire(context.Background(), fileAdmissionCapacity); err != nil {
		t.Fatal(err)
	}
	_, err := fileStore.SaveOwnerProfile(context.Background(), app.OwnerProfile{ID: "owner-blocked"})
	fileStore.admission.Release(fileAdmissionCapacity)
	if StoreErrorCodeOf(err) != StoreErrorTimeout {
		t.Fatalf("File admission timeout = %v code=%q", err, StoreErrorCodeOf(err))
	}
}

func TestOwnerTimestampHighWaterAndLegacyCreatedAtPreservation(t *testing.T) {
	store := NewMemoryStore()
	fixed := time.Date(2026, 8, 20, 1, 0, 0, 123456789, time.UTC)
	store.ownerNow = func() time.Time { return fixed }
	first := mustSaveOwnerProfile(t, store, app.OwnerProfile{ID: "owner-clock", DisplayName: "First"})
	store.ownerNow = func() time.Time { return fixed.Add(-time.Hour) }
	second := mustSaveOwnerProfile(t, store, app.OwnerProfile{ID: first.ID, DisplayName: "Second"})
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("backward clock reused timestamp: first=%s second=%s", first.UpdatedAt, second.UpdatedAt)
	}

	legacyCreatedAt := time.Date(2025, 1, 2, 3, 4, 5, 987654321, time.UTC)
	store.mu.Lock()
	legacy := app.OwnerProfile{
		ID: "owner-legacy-time", DisplayName: "Legacy", Preferences: map[string]string{},
		CreatedAt: legacyCreatedAt, UpdatedAt: legacyCreatedAt,
	}
	store.ownerProfiles[legacy.ID] = legacy
	store.ownerWriteHighWater[legacy.ID] = legacy.UpdatedAt
	store.mu.Unlock()
	updated := mustSaveOwnerProfile(t, store, app.OwnerProfile{ID: legacy.ID, DisplayName: "Updated"})
	if updated.CreatedAt != legacyCreatedAt {
		t.Fatalf("legacy CreatedAt changed: got %s want %s", updated.CreatedAt, legacyCreatedAt)
	}
	if !OwnerProfilesEqual(app.OwnerProfile{Preferences: nil}, app.OwnerProfile{Preferences: map[string]string{}}) {
		t.Fatal("nil and empty preferences should compare as the same persisted map")
	}
}

func TestFileOwnerFailureRollbackKeepsIssuedTimestampUnique(t *testing.T) {
	store := mustNewOwnerFileStore(t, FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")})
	fixed := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	store.inner.ownerNow = func() time.Time { return fixed }
	before := store.captureFileRollback()
	store.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}

	candidate, err := store.SaveOwnerProfile(context.Background(), app.OwnerProfile{ID: "owner-failed", DisplayName: "Failed"})
	if StoreErrorCodeOf(err) != StoreErrorDurability || !reflect.ValueOf(candidate).IsZero() {
		t.Fatalf("failed owner = %#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
	}
	if after := store.captureFileRollback(); !reflect.DeepEqual(after, before) {
		t.Fatal("owner/profile/audit/event state was not rolled back together")
	}
	issued := store.inner.ownerWriteHighWater["owner-failed"]
	if issued.IsZero() {
		t.Fatal("failed candidate did not advance the non-rollback high-water mark")
	}

	store.commitOps = osFileCommitOps{}
	store.inner.ownerNow = func() time.Time { return fixed.Add(-time.Hour) }
	saved := mustSaveOwnerProfile(t, store, app.OwnerProfile{ID: "owner-failed", DisplayName: "Retried"})
	if !saved.UpdatedAt.After(issued) {
		t.Fatalf("later candidate timestamp %s did not exceed failed candidate %s", saved.UpdatedAt, issued)
	}
	if got := len(store.ListAudit("")) - len(before.snapshot.AuditEvents); got != 1 {
		t.Fatalf("committed owner audit count = %d, want 1", got)
	}
	if got := len(store.EventsAfter("", "")) - len(before.snapshot.Events); got != 1 {
		t.Fatalf("committed owner event count = %d, want 1", got)
	}
}

func TestFileOwnerUnknownOutcomeReconcilesAndSurvivesEncryptedRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	options := FileStoreOptions{Path: path, EncryptAtRest: true, EncryptionKey: "owner-contract-key"}
	store := mustNewOwnerFileStore(t, options)
	store.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	candidate, writeErr := store.SaveOwnerProfile(context.Background(), app.OwnerProfile{
		ID: "owner-unknown", Source: "weixin", ExternalRef: "ref", DisplayName: "Unknown",
		Preferences: map[string]string{"key": "value"},
	})
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || candidate.ID == "" || store.currentFileFence() == nil {
		t.Fatalf("unknown result candidate=%#v err=%v fence=%v", candidate, writeErr, store.currentFileFence())
	}
	reconciled, err := ReconcileOwnerProfileWrite(context.Background(), store, candidate, writeErr)
	if err != nil || !OwnerProfilesEqual(reconciled, candidate) || store.currentFileFence() != nil {
		t.Fatalf("reconciliation = %#v err=%v fence=%v", reconciled, err, store.currentFileFence())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || !strings.Contains(string(raw), `"ciphertext"`) ||
		strings.Contains(string(raw), candidate.ID) || strings.Contains(string(raw), `"key":"value"`) {
		t.Fatalf("encrypted owner snapshot exposed plaintext: %s", raw)
	}
	reloaded := mustNewOwnerFileStore(t, options)
	got, found, err := reloaded.GetOwnerProfileByID(context.Background(), candidate.ID)
	if err != nil || !found || !OwnerProfilesEqual(got, candidate) {
		t.Fatalf("encrypted restart = %#v found=%v err=%v", got, found, err)
	}
}

func TestFileOwnerSnapshotCompatibilityAndCorruption(t *testing.T) {
	stockA := app.DefaultOwnerProfile()
	stockA.CreatedAt = time.Date(2026, 8, 20, 3, 0, 0, 1, time.UTC)
	stockA.UpdatedAt = stockA.CreatedAt
	stockB := cloneOwnerProfile(stockA)
	stockB.CreatedAt = stockA.CreatedAt.Add(time.Microsecond)
	stockB.UpdatedAt = stockB.CreatedAt

	tests := []struct {
		name        string
		snapshot    Snapshot
		wantError   bool
		wantProfile app.OwnerProfile
	}{
		{name: "older schema without owner fields", snapshot: Snapshot{}},
		{name: "old constructor timestamp mismatch", snapshot: Snapshot{
			OwnerProfile: stockA, OwnerProfiles: map[string]app.OwnerProfile{app.DefaultOwnerID: stockB},
		}, wantProfile: stockB},
		{name: "map key mismatch", snapshot: Snapshot{
			OwnerProfile: stockA, OwnerProfiles: map[string]app.OwnerProfile{"wrong": stockA},
		}, wantError: true},
		{name: "missing default map row", snapshot: Snapshot{
			OwnerProfile: stockA, OwnerProfiles: map[string]app.OwnerProfile{"owner-other": {ID: "owner-other"}},
		}, wantError: true},
		{name: "edited legacy mismatch", snapshot: Snapshot{
			OwnerProfile: stockA, OwnerProfiles: map[string]app.OwnerProfile{app.DefaultOwnerID: func() app.OwnerProfile {
				changed := cloneOwnerProfile(stockA)
				changed.DisplayName = "Changed"
				return changed
			}()},
		}, wantError: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			raw, err := json.Marshal(testCase.snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := NewFileStore(path)
			if testCase.wantError {
				if err == nil {
					t.Fatal("corrupt owner snapshot was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			profile, err := store.GetOwnerProfile(context.Background())
			if err != nil || profile.ID != app.DefaultOwnerID || profile.Preferences == nil {
				t.Fatalf("default owner = %#v err=%v", profile, err)
			}
			if testCase.wantProfile.ID != "" && !OwnerProfilesEqual(profile, testCase.wantProfile) {
				t.Fatalf("authoritative profile = %#v want %#v", profile, testCase.wantProfile)
			}
		})
	}
}

func mustNewOwnerFileStore(t testing.TB, options FileStoreOptions) *FileStore {
	t.Helper()
	store, err := NewFileStoreWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestReconcileOwnerProfileWriteRequiresExactCandidate(t *testing.T) {
	repository := NewMemoryStore()
	persisted := mustSaveOwnerProfile(t, repository, app.OwnerProfile{ID: "owner-reconcile", DisplayName: "Persisted"})
	unknown := storeError(OperationOwnerProfileSave, StoreErrorUnknownOutcome, errors.New("commit uncertain"))
	if got, err := ReconcileOwnerProfileWrite(context.Background(), repository, persisted, unknown); err != nil || !OwnerProfilesEqual(got, persisted) {
		t.Fatalf("exact reconciliation = %#v err=%v", got, err)
	}
	mismatch := cloneOwnerProfile(persisted)
	mismatch.DisplayName = "Different"
	if got, err := ReconcileOwnerProfileWrite(context.Background(), repository, mismatch, unknown); !reflect.ValueOf(got).IsZero() || !errors.Is(err, unknown) {
		t.Fatalf("mismatch reconciliation = %#v err=%v", got, err)
	}
	if got, err := ReconcileOwnerProfileWrite(context.Background(), repository, app.OwnerProfile{}, unknown); !reflect.ValueOf(got).IsZero() || !errors.Is(err, unknown) {
		t.Fatalf("zero candidate reconciliation = %#v err=%v", got, err)
	}
}
