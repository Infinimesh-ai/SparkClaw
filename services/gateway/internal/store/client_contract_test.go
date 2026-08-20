package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestClientRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []struct {
		name string
		new  func(*testing.T) ClientRepository
	}{
		{name: "memory", new: func(*testing.T) ClientRepository { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) ClientRepository {
			repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return repository
		}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			clients, err := repository.ListClients(t.Context())
			if err != nil || clients == nil || len(clients) != 0 {
				t.Fatalf("empty client list = %#v err=%v", clients, err)
			}
			if _, found, err := repository.GetClient(t.Context(), " "); err != nil || found {
				t.Fatalf("empty client get found=%v err=%v", found, err)
			}
			if _, err := repository.SavePairingCode(t.Context(), app.PairingCode{}); StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("invalid pairing save = %v", err)
			}
			pairing, err := repository.SavePairingCode(t.Context(), app.PairingCode{
				ID: " pair-a ", CodeHash: " pair-hash-a ", ExpiresAt: time.Now().UTC().Add(time.Hour),
			})
			if err != nil || pairing.ID != "pair-a" || pairing.CodeHash != "pair-hash-a" || pairing.Status != "pending" || pairing.CreatedAt.IsZero() {
				t.Fatalf("normalized pairing = %#v err=%v", pairing, err)
			}
			if _, err := repository.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-a", CodeHash: "other", ExpiresAt: time.Now().UTC().Add(time.Hour)}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("duplicate pairing ID = %v", err)
			}
			claimedPairing, client, err := repository.ClaimPairingCode(t.Context(), pairing.ID, app.Client{
				ID: " client-a ", Name: " Client A ", TokenHash: " token-a ",
			})
			if err != nil || client.ID != "client-a" || client.Name != "Client A" || client.TokenHash != "token-a" ||
				client.CreatedAt.IsZero() || claimedPairing.Status != "claimed" || claimedPairing.ClientID != client.ID || claimedPairing.ClaimedAt == nil {
				t.Fatalf("atomic claim pairing=%#v client=%#v err=%v", claimedPairing, client, err)
			}
			if _, _, err := repository.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-b", Name: "Client B", TokenHash: "token-b"}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("repeat claim = %v", err)
			}
			found, ok, err := repository.FindClientByTokenHash(t.Context(), client.TokenHash)
			if err != nil || !ok || !ClientsEqual(found, client) {
				t.Fatalf("token lookup = %#v found=%v err=%v", found, ok, err)
			}
			touched, ok, err := repository.TouchClient(t.Context(), client.ID)
			if err != nil || !ok || touched.LastSeenAt == nil {
				t.Fatalf("touch = %#v found=%v err=%v", touched, ok, err)
			}
			revoked, err := repository.RevokeClient(t.Context(), client.ID)
			if err != nil || revoked.RevokedAt == nil {
				t.Fatalf("revoke = %#v err=%v", revoked, err)
			}
			if _, ok, err := repository.FindClientByTokenHash(t.Context(), client.TokenHash); err != nil || ok {
				t.Fatalf("revoked token lookup found=%v err=%v", ok, err)
			}
			if _, ok, err := repository.TouchClient(t.Context(), client.ID); err != nil || ok {
				t.Fatalf("revoked touch found=%v err=%v", ok, err)
			}
		})
	}
}

func TestClientRepositoryPointerIsolation(t *testing.T) {
	for _, backend := range []struct {
		name string
		new  func(*testing.T) ClientRepository
	}{
		{name: "memory", new: func(*testing.T) ClientRepository { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) ClientRepository {
			repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return repository
		}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			pairing, err := repository.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-alias", CodeHash: "pair-alias-hash", ExpiresAt: time.Now().UTC().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			claimed, client, err := repository.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-alias", Name: "Alias", TokenHash: "client-alias-hash"})
			if err != nil {
				t.Fatal(err)
			}
			touched, _, err := repository.TouchClient(t.Context(), client.ID)
			if err != nil {
				t.Fatal(err)
			}
			revoked, err := repository.RevokeClient(t.Context(), client.ID)
			if err != nil {
				t.Fatal(err)
			}
			originalClaimed := *claimed.ClaimedAt
			originalSeen := *touched.LastSeenAt
			originalRevoked := *revoked.RevokedAt
			*claimed.ClaimedAt = claimed.ClaimedAt.Add(time.Hour)
			*touched.LastSeenAt = touched.LastSeenAt.Add(time.Hour)
			*revoked.RevokedAt = revoked.RevokedAt.Add(time.Hour)
			againPairing, _, err := repository.GetPairingCode(t.Context(), pairing.ID)
			if err != nil || againPairing.ClaimedAt == nil || !againPairing.ClaimedAt.Equal(originalClaimed) {
				t.Fatalf("pairing pointer alias = %#v err=%v", againPairing, err)
			}
			againClient, _, err := repository.GetClient(t.Context(), client.ID)
			if err != nil || againClient.LastSeenAt == nil || againClient.RevokedAt == nil ||
				!againClient.LastSeenAt.Equal(originalSeen) || !againClient.RevokedAt.Equal(originalRevoked) {
				t.Fatalf("client pointer alias = %#v err=%v", againClient, err)
			}
		})
	}
}

func TestClientRepositoryCancellationPrecedesAbsence(t *testing.T) {
	repository := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := repository.GetClient(ctx, ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled empty get = %v", err)
	}
	if _, err := repository.RevokeClient(ctx, "missing"); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled revoke = %v", err)
	}
}

func TestClientRepositoryTypedOutcomesAndValidationPrecedence(t *testing.T) {
	for _, backend := range []struct {
		name string
		new  func(*testing.T) ClientRepository
	}{
		{name: "memory", new: func(*testing.T) ClientRepository { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) ClientRepository {
			return mustNewClientFileStore(t, FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")})
		}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			now := time.Now().UTC()
			invalidPairings := []app.PairingCode{
				{ID: "invalid-status", CodeHash: "hash", Status: "claimed", ExpiresAt: now.Add(time.Hour)},
				{ID: "invalid-hash", ExpiresAt: now.Add(time.Hour)},
				{ID: "invalid-expiry", CodeHash: "hash"},
				{ID: "invalid-client", CodeHash: "hash", ExpiresAt: now.Add(time.Hour), ClientID: "client"},
			}
			for index, pairing := range invalidPairings {
				if candidate, err := repository.SavePairingCode(t.Context(), pairing); StoreErrorCodeOf(err) != StoreErrorInvalid || !reflect.ValueOf(candidate).IsZero() {
					t.Fatalf("invalid pairing %d candidate=%#v err=%v", index, candidate, err)
				}
			}

			if _, _, err := repository.ClaimPairingCode(t.Context(), "missing", app.Client{}); StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("client validation did not precede pairing lookup: %v", err)
			}
			if _, _, err := repository.ClaimPairingCode(t.Context(), "missing", app.Client{Name: "Valid", TokenHash: "valid-hash"}); StoreErrorCodeOf(err) != StoreErrorNotFound {
				t.Fatalf("missing pairing = %v", err)
			}
			if _, err := repository.RevokeClient(t.Context(), " "); StoreErrorCodeOf(err) != StoreErrorNotFound {
				t.Fatalf("blank revoke = %v", err)
			}

			pairing, err := repository.SavePairingCode(t.Context(), app.PairingCode{
				ID: "pair-outcome", CodeHash: "pair-outcome-hash", ExpiresAt: now.Add(time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repository.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-other", CodeHash: pairing.CodeHash, ExpiresAt: now.Add(time.Hour)}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("duplicate pairing hash = %v", err)
			}
			claimed, client, err := repository.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-outcome", Name: "Client", TokenHash: "client-outcome-hash"})
			if err != nil {
				t.Fatal(err)
			}
			if claimed.ClaimedAt == nil || client.CreatedAt.IsZero() {
				t.Fatalf("claim candidates = %#v %#v", claimed, client)
			}

			for index, candidate := range []app.Client{
				{ID: client.ID, Name: "Other", TokenHash: "new-token"},
				{ID: "new-client", Name: "Other", TokenHash: client.TokenHash},
			} {
				pending, saveErr := repository.SavePairingCode(t.Context(), app.PairingCode{
					ID: app.NewID("pair_duplicate"), CodeHash: app.NewID("pair_duplicate_hash"), ExpiresAt: now.Add(time.Hour),
				})
				if saveErr != nil {
					t.Fatal(saveErr)
				}
				pairCandidate, clientCandidate, claimErr := repository.ClaimPairingCode(t.Context(), pending.ID, candidate)
				if StoreErrorCodeOf(claimErr) != StoreErrorConflict || !reflect.ValueOf(pairCandidate).IsZero() || !reflect.ValueOf(clientCandidate).IsZero() {
					t.Fatalf("duplicate client %d pairing=%#v client=%#v err=%v", index, pairCandidate, clientCandidate, claimErr)
				}
			}

			expired, err := repository.SavePairingCode(t.Context(), app.PairingCode{
				ID: "pair-expired", CodeHash: "pair-expired-hash", ExpiresAt: now.Add(-time.Second),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := repository.ClaimPairingCode(t.Context(), expired.ID, app.Client{Name: "Expired", TokenHash: "expired-token"}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("expired claim = %v", err)
			}
		})
	}
}

func TestClientRepositoryCancellationAndTimeoutAcrossEveryMethod(t *testing.T) {
	for _, backend := range []struct {
		name string
		new  func(*testing.T) ClientRepository
	}{
		{name: "memory", new: func(*testing.T) ClientRepository { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) ClientRepository {
			return mustNewClientFileStore(t, FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")})
		}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.new(t)
			calls := clientRepositoryErrorCalls(repository)
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			for index, call := range calls {
				if err := call(canceled); StoreErrorCodeOf(err) != StoreErrorCanceled {
					t.Fatalf("call %d canceled error = %v code=%q", index, err, StoreErrorCodeOf(err))
				}
			}
			timedOut, timeoutCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer timeoutCancel()
			for index, call := range calls {
				if err := call(timedOut); StoreErrorCodeOf(err) != StoreErrorTimeout {
					t.Fatalf("call %d timeout error = %v code=%q", index, err, StoreErrorCodeOf(err))
				}
			}
		})
	}

	fileStore := mustNewClientFileStore(t, FileStoreOptions{
		Path: filepath.Join(t.TempDir(), "blocked.json"), TransactionTimeout: 20 * time.Millisecond,
	})
	if err := fileStore.admission.Acquire(context.Background(), fileAdmissionCapacity); err != nil {
		t.Fatal(err)
	}
	_, err := fileStore.SavePairingCode(context.Background(), app.PairingCode{ID: "blocked", CodeHash: "blocked", ExpiresAt: time.Now().Add(time.Hour)})
	fileStore.admission.Release(fileAdmissionCapacity)
	if StoreErrorCodeOf(err) != StoreErrorTimeout {
		t.Fatalf("File admission timeout = %v code=%q", err, StoreErrorCodeOf(err))
	}
}

func clientRepositoryErrorCalls(repository ClientRepository) []func(context.Context) error {
	return []func(context.Context) error{
		func(ctx context.Context) error { _, _, err := repository.GetClient(ctx, ""); return err },
		func(ctx context.Context) error { _, err := repository.ListClients(ctx); return err },
		func(ctx context.Context) error { _, err := repository.RevokeClient(ctx, ""); return err },
		func(ctx context.Context) error { _, _, err := repository.FindClientByTokenHash(ctx, ""); return err },
		func(ctx context.Context) error { _, _, err := repository.TouchClient(ctx, ""); return err },
		func(ctx context.Context) error {
			_, err := repository.SavePairingCode(ctx, app.PairingCode{})
			return err
		},
		func(ctx context.Context) error { _, _, err := repository.GetPairingCode(ctx, ""); return err },
		func(ctx context.Context) error {
			_, _, err := repository.ClaimPairingCode(ctx, "", app.Client{})
			return err
		},
	}
}

func TestClientRepositoryOrderingLifecycleAndEventPointerIsolation(t *testing.T) {
	created := time.Date(2026, 8, 20, 5, 0, 0, 123456789, time.UTC)
	clients := map[string]app.Client{
		"client-b": {ID: "client-b", Name: "B", TokenHash: "hash-b", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, CreatedAt: created},
		"client-a": {ID: "client-a", Name: "A", TokenHash: "hash-a", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, CreatedAt: created},
		"client-c": {ID: "client-c", Name: "C", TokenHash: "hash-c", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, CreatedAt: created.Add(time.Hour)},
	}
	memory := NewMemoryStore()
	memory.loadSnapshot(Snapshot{Clients: clients})
	path := filepath.Join(t.TempDir(), "ordered.json")
	raw, err := json.Marshal(Snapshot{Clients: clients})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore := mustNewClientFileStore(t, FileStoreOptions{Path: path})
	for _, backend := range []struct {
		name       string
		repository ClientRepository
	}{{name: "memory", repository: memory}, {name: "file", repository: fileStore}} {
		t.Run(backend.name+"/ordering", func(t *testing.T) {
			listed, err := backend.repository.ListClients(t.Context())
			if err != nil || len(listed) != 3 || listed[0].ID != "client-c" || listed[1].ID != "client-a" || listed[2].ID != "client-b" {
				t.Fatalf("ordered clients = %#v err=%v", listed, err)
			}
		})
	}

	for _, backend := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(*testing.T) Store { return NewMemoryStore() }},
		{name: "file", new: func(t *testing.T) Store {
			return mustNewClientFileStore(t, FileStoreOptions{Path: filepath.Join(t.TempDir(), "events.json")})
		}},
	} {
		t.Run(backend.name+"/lifecycle", func(t *testing.T) {
			repository := backend.new(t)
			pairing, err := repository.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-events", CodeHash: "pair-events-hash", ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			claimed, client, err := repository.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-events", Name: "Events", TokenHash: "client-events-hash"})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := repository.TouchClient(t.Context(), client.ID); err != nil {
				t.Fatal(err)
			}
			revoked, err := repository.RevokeClient(t.Context(), client.ID)
			if err != nil {
				t.Fatal(err)
			}

			events := repository.EventsAfter("", "")
			if got := eventTypes(events); !slices.Equal(got, []string{"pairing_code.created", "client.saved", "pairing_code.claimed", "client.revoked"}) {
				t.Fatalf("event order = %v", got)
			}
			audits := repository.ListAudit("")
			auditTypes := make([]string, 0, len(audits))
			for _, audit := range audits {
				auditTypes = append(auditTypes, audit.Type)
			}
			slices.Sort(auditTypes)
			if !slices.Equal(auditTypes, []string{"client.revoked", "client.saved", "pairing_code.claimed", "pairing_code.created"}) {
				t.Fatalf("audit set = %v", auditTypes)
			}
			if !events[1].Time.Equal(events[2].Time) || !events[1].Time.Equal(client.CreatedAt) || !events[2].Time.Equal(*claimed.ClaimedAt) {
				t.Fatalf("claim lifecycle timestamps = %#v", events)
			}

			claimedPayload, ok := events[2].Payload.(app.PairingCode)
			if !ok || claimedPayload.ClaimedAt == nil {
				t.Fatalf("claimed event payload = %#v", events[2].Payload)
			}
			revokedPayload, ok := events[3].Payload.(app.Client)
			if !ok || revokedPayload.RevokedAt == nil {
				t.Fatalf("revoked event payload = %#v", events[3].Payload)
			}
			originalClaimed := *claimedPayload.ClaimedAt
			originalRevoked := *revokedPayload.RevokedAt
			*claimedPayload.ClaimedAt = claimedPayload.ClaimedAt.Add(time.Hour)
			*revokedPayload.RevokedAt = revokedPayload.RevokedAt.Add(time.Hour)
			fresh := repository.EventsAfter("", "")
			freshClaimed := fresh[2].Payload.(app.PairingCode)
			freshRevoked := fresh[3].Payload.(app.Client)
			if !freshClaimed.ClaimedAt.Equal(originalClaimed) || !freshRevoked.RevokedAt.Equal(originalRevoked) || !revoked.RevokedAt.Equal(originalRevoked) {
				t.Fatalf("event payload alias claimed=%#v revoked=%#v", freshClaimed, freshRevoked)
			}
		})
	}
}

func eventTypes(events []app.Event) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func TestClientRepositoryNonRollbackHighWater(t *testing.T) {
	for _, backend := range []struct {
		name string
		new  func(*testing.T) (*MemoryStore, ClientRepository)
	}{
		{name: "memory", new: func(*testing.T) (*MemoryStore, ClientRepository) {
			memory := NewMemoryStore()
			return memory, memory
		}},
		{name: "file", new: func(t *testing.T) (*MemoryStore, ClientRepository) {
			fileStore := mustNewClientFileStore(t, FileStoreOptions{Path: filepath.Join(t.TempDir(), "clock.json")})
			return fileStore.inner, fileStore
		}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			memory, repository := backend.new(t)
			fixed := time.Date(2026, 8, 20, 6, 0, 0, 987654321, time.UTC)
			memory.clientNow = func() time.Time { return fixed }
			pairing, err := repository.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-clock", CodeHash: "pair-clock-hash", ExpiresAt: fixed.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if pairing.CreatedAt.Nanosecond()%1000 != 0 {
				t.Fatalf("pairing timestamp precision = %s", pairing.CreatedAt)
			}

			memory.clientNow = func() time.Time { return fixed.Add(-time.Hour) }
			claimed, client, err := repository.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-clock", Name: "Clock", TokenHash: "client-clock-hash"})
			if err != nil {
				t.Fatal(err)
			}
			if claimed.ClaimedAt == nil || !claimed.ClaimedAt.After(pairing.CreatedAt) || !client.CreatedAt.Equal(*claimed.ClaimedAt) {
				t.Fatalf("claim high-water pairing=%#v client=%#v", claimed, client)
			}
			touched, found, err := repository.TouchClient(t.Context(), client.ID)
			if err != nil || !found || touched.LastSeenAt == nil || !touched.LastSeenAt.After(client.CreatedAt) {
				t.Fatalf("touch high-water = %#v found=%v err=%v", touched, found, err)
			}
			revoked, err := repository.RevokeClient(t.Context(), client.ID)
			if err != nil || revoked.RevokedAt == nil || !revoked.RevokedAt.After(*touched.LastSeenAt) {
				t.Fatalf("revoke high-water = %#v err=%v", revoked, err)
			}
			repeated, err := repository.RevokeClient(t.Context(), client.ID)
			if err != nil || repeated.RevokedAt == nil || !repeated.RevokedAt.After(*revoked.RevokedAt) {
				t.Fatalf("repeated revoke high-water = %#v err=%v", repeated, err)
			}
		})
	}
}

func TestFileClientCommandsRollbackAllOwnedState(t *testing.T) {
	newStore := func(t *testing.T) *FileStore {
		return mustNewClientFileStore(t, FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")})
	}
	assertRollback := func(t *testing.T, store *FileStore, before fileRollbackState) {
		t.Helper()
		if after := store.captureFileRollback(); !reflect.DeepEqual(after, before) {
			t.Fatalf("client/pairing/audit/event state was not rolled back\nbefore=%#v\nafter=%#v", before, after)
		}
	}

	t.Run("save pairing", func(t *testing.T) {
		store := newStore(t)
		fixed := time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)
		store.inner.clientNow = func() time.Time { return fixed }
		before := store.captureFileRollback()
		store.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
		candidate, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-fail", CodeHash: "pair-fail-hash", ExpiresAt: fixed.Add(time.Hour)})
		if StoreErrorCodeOf(err) != StoreErrorDurability || !reflect.ValueOf(candidate).IsZero() {
			t.Fatalf("save candidate=%#v err=%v", candidate, err)
		}
		assertRollback(t, store, before)
		issued := store.inner.pairingWriteHighWater["pair-fail"]
		if issued.IsZero() {
			t.Fatal("failed pairing candidate did not advance high-water")
		}
		store.commitOps = osFileCommitOps{}
		store.inner.clientNow = func() time.Time { return fixed.Add(-time.Hour) }
		saved, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-fail", CodeHash: "pair-fail-hash", ExpiresAt: fixed.Add(time.Hour)})
		if err != nil || !saved.CreatedAt.After(issued) {
			t.Fatalf("retry = %#v err=%v issued=%s", saved, err, issued)
		}
	})

	t.Run("claim pairing", func(t *testing.T) {
		store := newStore(t)
		pairing, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-claim-fail", CodeHash: "pair-claim-fail-hash", ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		before := store.captureFileRollback()
		store.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
		pairCandidate, clientCandidate, err := store.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-claim-fail", Name: "Claim", TokenHash: "client-claim-fail-hash"})
		if StoreErrorCodeOf(err) != StoreErrorDurability || !reflect.ValueOf(pairCandidate).IsZero() || !reflect.ValueOf(clientCandidate).IsZero() {
			t.Fatalf("claim pairing=%#v client=%#v err=%v", pairCandidate, clientCandidate, err)
		}
		assertRollback(t, store, before)
		if store.inner.clientWriteHighWater["client-claim-fail"].IsZero() || !store.inner.pairingWriteHighWater[pairing.ID].After(pairing.CreatedAt) {
			t.Fatalf("failed claim high-water client=%s pairing=%s", store.inner.clientWriteHighWater["client-claim-fail"], store.inner.pairingWriteHighWater[pairing.ID])
		}
	})

	for _, command := range []string{"touch", "revoke"} {
		t.Run(command, func(t *testing.T) {
			store := newStore(t)
			client := mustClaimTestClient(t, store, app.Client{ID: "client-" + command, Name: command, TokenHash: "hash-" + command})
			before := store.captureFileRollback()
			store.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
			if command == "touch" {
				candidate, found, err := store.TouchClient(t.Context(), client.ID)
				if StoreErrorCodeOf(err) != StoreErrorDurability || found || !reflect.ValueOf(candidate).IsZero() {
					t.Fatalf("touch candidate=%#v found=%v err=%v", candidate, found, err)
				}
			} else {
				candidate, err := store.RevokeClient(t.Context(), client.ID)
				if StoreErrorCodeOf(err) != StoreErrorDurability || !reflect.ValueOf(candidate).IsZero() {
					t.Fatalf("revoke candidate=%#v err=%v", candidate, err)
				}
			}
			assertRollback(t, store, before)
			if store.inner.clientWriteHighWater[client.ID].IsZero() {
				t.Fatal("failed client mutation did not retain high-water")
			}
		})
	}
}

func TestFileClientUnknownOutcomesReconcileAndSurviveEncryptedRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.enc")
	options := FileStoreOptions{Path: path, EncryptAtRest: true, EncryptionKey: "client-contract-key"}
	fileStore := mustNewClientFileStore(t, options)

	fileStore.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	pairing, err := fileStore.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-unknown", CodeHash: "pair-unknown-hash", ExpiresAt: time.Now().Add(time.Hour)})
	if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || pairing.ID == "" || fileStore.currentFileFence() == nil {
		t.Fatalf("unknown save pairing=%#v err=%v fence=%v", pairing, err, fileStore.currentFileFence())
	}
	persistedPairing, found, err := fileStore.GetPairingCode(t.Context(), pairing.ID)
	if err != nil || !found || !PairingCodesEqual(persistedPairing, pairing) || fileStore.currentFileFence() != nil {
		t.Fatalf("save reconciliation=%#v found=%v err=%v fence=%v", persistedPairing, found, err, fileStore.currentFileFence())
	}

	fileStore.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	claimedPairing, client, err := fileStore.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-unknown", Name: "Unknown", TokenHash: "client-unknown-hash"})
	if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || claimedPairing.ID == "" || client.ID == "" || fileStore.currentFileFence() == nil {
		t.Fatalf("unknown claim pairing=%#v client=%#v err=%v fence=%v", claimedPairing, client, err, fileStore.currentFileFence())
	}
	persistedClient, found, err := fileStore.GetClient(t.Context(), client.ID)
	if err != nil || !found || !ClientsEqual(persistedClient, client) || fileStore.currentFileFence() != nil {
		t.Fatalf("claim reconciliation=%#v found=%v err=%v fence=%v", persistedClient, found, err, fileStore.currentFileFence())
	}
	persistedPairing, found, err = fileStore.GetPairingCode(t.Context(), pairing.ID)
	if err != nil || !found || !PairingCodesEqual(persistedPairing, claimedPairing) {
		t.Fatalf("claimed pairing=%#v found=%v err=%v", persistedPairing, found, err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || !strings.Contains(string(raw), `"ciphertext"`) || strings.Contains(string(raw), client.TokenHash) || strings.Contains(string(raw), client.Name) {
		t.Fatalf("encrypted client snapshot exposed plaintext: %s", raw)
	}
	reloaded := mustNewClientFileStore(t, options)
	gotClient, found, err := reloaded.GetClient(t.Context(), client.ID)
	if err != nil || !found || !ClientsEqual(gotClient, client) {
		t.Fatalf("encrypted client restart=%#v found=%v err=%v", gotClient, found, err)
	}
	gotPairing, found, err := reloaded.GetPairingCode(t.Context(), pairing.ID)
	if err != nil || !found || !PairingCodesEqual(gotPairing, claimedPairing) {
		t.Fatalf("encrypted pairing restart=%#v found=%v err=%v", gotPairing, found, err)
	}
}

func TestFileClientSnapshotCompatibilityAndCorruption(t *testing.T) {
	now := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	zero := time.Time{}
	validClient := app.Client{
		ID: "client-valid", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		Name: "Valid", TokenHash: "client-valid-hash", CreatedAt: now,
	}
	validPairing := app.PairingCode{
		ID: "pair-valid", CodeHash: "pair-valid-hash", Status: "pending",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	tests := []struct {
		name      string
		snapshot  Snapshot
		wantError bool
	}{
		{name: "older schema without client fields", snapshot: Snapshot{}},
		{name: "legacy blank client projection and hashes", snapshot: Snapshot{
			Clients: map[string]app.Client{"client-legacy": {
				ID: "client-legacy", Name: "", TokenHash: "", CreatedAt: now,
			}},
			PairingCodes: map[string]app.PairingCode{"pair-legacy": {
				ID: "pair-legacy", CodeHash: "", Status: "pending", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
			}},
		}},
		{name: "client key mismatch", snapshot: Snapshot{Clients: map[string]app.Client{"wrong": validClient}}, wantError: true},
		{name: "pairing key mismatch", snapshot: Snapshot{PairingCodes: map[string]app.PairingCode{"wrong": validPairing}}, wantError: true},
		{name: "duplicate client token", snapshot: Snapshot{Clients: map[string]app.Client{
			validClient.ID: validClient,
			"client-other": {ID: "client-other", TokenHash: validClient.TokenHash, CreatedAt: now},
		}}, wantError: true},
		{name: "duplicate pairing hash", snapshot: Snapshot{PairingCodes: map[string]app.PairingCode{
			validPairing.ID: validPairing,
			"pair-other":    {ID: "pair-other", CodeHash: validPairing.CodeHash, Status: "pending", ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		}}, wantError: true},
		{name: "zero client pointer", snapshot: Snapshot{Clients: map[string]app.Client{
			validClient.ID: func() app.Client { changed := validClient; changed.LastSeenAt = &zero; return changed }(),
		}}, wantError: true},
		{name: "zero pairing pointer", snapshot: Snapshot{
			Clients: map[string]app.Client{validClient.ID: validClient},
			PairingCodes: map[string]app.PairingCode{"pair-claimed": {
				ID: "pair-claimed", CodeHash: "claimed-hash", Status: "claimed", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
				ClaimedAt: &zero, ClientID: validClient.ID,
			}},
		}, wantError: true},
		{name: "claimed pairing missing client", snapshot: Snapshot{PairingCodes: map[string]app.PairingCode{"pair-claimed": {
			ID: "pair-claimed", CodeHash: "claimed-hash", Status: "claimed", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
			ClaimedAt: func() *time.Time { value := now; return &value }(), ClientID: "missing",
		}}}, wantError: true},
		{name: "invalid pairing status", snapshot: Snapshot{PairingCodes: map[string]app.PairingCode{"pair-invalid": {
			ID: "pair-invalid", CodeHash: "invalid-hash", Status: "other", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
		}}}, wantError: true},
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
			fileStore, err := NewFileStore(path)
			if testCase.wantError {
				if err == nil {
					t.Fatal("corrupt client snapshot was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if testCase.name != "legacy blank client projection and hashes" {
				return
			}
			client, found, err := fileStore.GetClient(t.Context(), "client-legacy")
			if err != nil || !found || client.OwnerID != app.DefaultOwnerID || client.ActorID != app.DefaultOwnerID || client.Name != "" || client.TokenHash != "" {
				t.Fatalf("legacy client=%#v found=%v err=%v", client, found, err)
			}
			if _, found, err := fileStore.FindClientByTokenHash(t.Context(), ""); err != nil || found {
				t.Fatalf("blank legacy token authenticated found=%v err=%v", found, err)
			}
			pairing, found, err := fileStore.GetPairingCode(t.Context(), "pair-legacy")
			if err != nil || !found || pairing.CodeHash != "" {
				t.Fatalf("legacy pairing=%#v found=%v err=%v", pairing, found, err)
			}
			if _, _, err := fileStore.ClaimPairingCode(t.Context(), pairing.ID, app.Client{Name: "Legacy", TokenHash: "legacy-token"}); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("blank legacy pairing claimed: %v", err)
			}
		})
	}
}

func TestClientRepositoryUnknownErrorCandidatesAreExact(t *testing.T) {
	pairing := app.PairingCode{ID: "pair-candidate", CodeHash: "hash", Status: "pending", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	client := app.Client{ID: "client-candidate", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Name: "Client", TokenHash: "token", CreatedAt: time.Now()}
	if !PairingCodesEqual(pairing, clonePairingCode(pairing)) || !ClientsEqual(client, cloneClient(client)) {
		t.Fatal("exact candidate equality rejected clones")
	}
	changedPairing := clonePairingCode(pairing)
	changedPairing.CodeHash = "different"
	changedClient := cloneClient(client)
	changedClient.Name = "Different"
	if PairingCodesEqual(pairing, changedPairing) || ClientsEqual(client, changedClient) {
		t.Fatal("candidate equality accepted different persisted fields")
	}
	unknown := storeError(OperationPairingCodeClaim, StoreErrorUnknownOutcome, errors.New("commit uncertain"))
	if StoreErrorCodeOf(unknown) != StoreErrorUnknownOutcome {
		t.Fatalf("unknown candidate error = %v", unknown)
	}
}

func mustNewClientFileStore(t testing.TB, options FileStoreOptions) *FileStore {
	t.Helper()
	fileStore, err := NewFileStoreWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	return fileStore
}
