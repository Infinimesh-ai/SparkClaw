package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMCPRepositoryCanceledContextContract(t *testing.T) {
	for _, backend := range newMCPContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			now := time.Now().UTC()
			checks := map[string]func() error{
				"SaveMCPAccessTicket":             func() error { _, err := backend.repository.SaveMCPAccessTicket(ctx, app.MCPAccessTicket{}); return err },
				"GetMCPAccessTicket":              func() error { _, _, err := backend.repository.GetMCPAccessTicket(ctx, "ticket"); return err },
				"FindMCPAccessTicketBySecretHash": func() error { _, _, err := backend.repository.FindMCPAccessTicketBySecretHash(ctx, "hash"); return err },
				"ListMCPAccessTickets":            func() error { _, err := backend.repository.ListMCPAccessTickets(ctx, "owner"); return err },
				"RedeemMCPAccessTicket": func() error {
					_, err := backend.repository.RedeemMCPAccessTicket(ctx, "hash", app.MCPPeerIdentity{}, now)
					return err
				},
				"RevokeMCPAccessTicket": func() error { _, err := backend.repository.RevokeMCPAccessTicket(ctx, "ticket", now); return err },
				"DeleteMCPAccessTicket": func() error { _, err := backend.repository.DeleteMCPAccessTicket(ctx, "owner", "ticket"); return err },
				"GetMCPBinding":         func() error { _, _, err := backend.repository.GetMCPBinding(ctx, "binding"); return err },
				"FindMCPBindingForPeer": func() error {
					_, _, err := backend.repository.FindMCPBindingForPeer(ctx, "domain", "device", "thumb")
					return err
				},
				"ListMCPBindings":        func() error { _, err := backend.repository.ListMCPBindings(ctx, "owner"); return err },
				"RevokeMCPBinding":       func() error { _, err := backend.repository.RevokeMCPBinding(ctx, "binding", now); return err },
				"DeleteMCPBinding":       func() error { _, err := backend.repository.DeleteMCPBinding(ctx, "owner", "binding"); return err },
				"DeleteMCPAccessRecords": func() error { _, err := backend.repository.DeleteMCPAccessRecords(ctx, "owner"); return err },
				"TouchMCPBinding":        func() error { return backend.repository.TouchMCPBinding(ctx, "binding", "session", now) },
				"CreateMCPOperation":     func() error { _, _, err := backend.repository.CreateMCPOperation(ctx, app.MCPOperation{}); return err },
				"GetMCPOperation":        func() error { _, _, err := backend.repository.GetMCPOperation(ctx, "operation"); return err },
				"FindMCPOperationByIdempotency": func() error {
					_, _, err := backend.repository.FindMCPOperationByIdempotency(ctx, "binding", "key")
					return err
				},
				"ListMCPOperations": func() error { _, err := backend.repository.ListMCPOperations(ctx, "binding"); return err },
				"UpdateMCPOperation": func() error {
					_, err := backend.repository.UpdateMCPOperation(ctx, app.MCPOperation{ID: "operation"}, 1)
					return err
				},
			}
			for name, check := range checks {
				if err := check(); StoreErrorCodeOf(err) != StoreErrorCanceled {
					t.Errorf("%s error=%v code=%q", name, err, StoreErrorCodeOf(err))
				}
			}
		})
	}
}

func TestMCPRepositoryConcurrencyContract(t *testing.T) {
	for _, backend := range newMCPContractBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			runMCPRepositoryConcurrencyContract(t, backend.repository)
		})
	}
}

func TestPostgresMCPRepositoryContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	runMCPRepositoryConcurrencyContract(t, repository)
}

func TestMCPRemainingReconciliationRejectsUnprovenOutcomes(t *testing.T) {
	repository := NewMemoryStore()
	now := time.Now().UTC()
	ticket, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(now, "reconcile-negative-ticket"))
	if err != nil {
		t.Fatal(err)
	}
	unknown := storeError(OperationMCPAccessTicketDelete, StoreErrorUnknownOutcome, errors.New("commit outcome unknown"))
	revokedCandidate := ticket
	revokedCandidate.Status = app.MCPAccessRevoked
	revokedCandidate.RevokedAt = &now
	if _, err := ReconcileMCPAccessTicketRevoke(t.Context(), repository, revokedCandidate, unknown); !errors.Is(err, unknown) {
		t.Fatalf("ticket revoke accepted unpersisted candidate: %v", err)
	}
	if _, err := ReconcileMCPAccessTicketDelete(t.Context(), repository, ticket, unknown); !errors.Is(err, unknown) {
		t.Fatalf("ticket delete accepted retained record: %v", err)
	}

	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "negative-device", KeyThumbprint: "negative-thumb", ISCPSessionID: "negative-session"}
	binding, err := repository.RedeemMCPAccessTicket(t.Context(), ticket.SecretHash, peer, now)
	if err != nil {
		t.Fatal(err)
	}
	revokedBinding := binding
	revokedBinding.Status = app.MCPBindingRevoked
	revokedBinding.RevokedAt = &now
	if _, err := ReconcileMCPBindingRevoke(t.Context(), repository, revokedBinding, unknown); !errors.Is(err, unknown) {
		t.Fatalf("binding revoke accepted unpersisted candidate: %v", err)
	}
	if _, err := ReconcileMCPBindingDelete(t.Context(), repository, binding, unknown); !errors.Is(err, unknown) {
		t.Fatalf("binding delete accepted retained record: %v", err)
	}
	if _, err := ReconcileMCPBindingTouch(t.Context(), repository, binding, "changed-session", now.Add(time.Second), unknown); !errors.Is(err, unknown) {
		t.Fatalf("binding touch accepted unpersisted candidate: %v", err)
	}
	if _, err := ReconcileMCPAccessRecordDeletion(t.Context(), repository, binding.OwnerID, MCPAccessRecordDeletion{DeletedTickets: 1, DeletedBindings: 1}, unknown); !errors.Is(err, unknown) {
		t.Fatalf("owner deletion accepted retained records: %v", err)
	}
}

type mcpContractBackend struct {
	name       string
	repository testBackend
}

func newMCPContractBackends(t *testing.T) []mcpContractBackend {
	t.Helper()
	fileRepository, err := NewFileStore(filepath.Join(t.TempDir(), "mcp-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	return []mcpContractBackend{
		{name: "memory", repository: NewMemoryStore()},
		{name: "file", repository: fileRepository},
	}
}

func runMCPRepositoryConcurrencyContract(t *testing.T, repository testBackend) {
	t.Helper()
	now := time.Date(2026, 8, 22, 9, 0, 0, 123456789, time.FixedZone("contract", 8*60*60))
	ticket, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(now, "mcp-contract-secret"))
	if err != nil || ticket.IssuedAt.Location() != time.UTC || ticket.IssuedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("SaveMCPAccessTicket=%#v err=%v", ticket, err)
	}
	duplicateHash := testMCPAccessTicket(now, ticket.SecretHash)
	duplicateHash.ID = "mcp-ticket-duplicate-hash"
	if _, err := repository.SaveMCPAccessTicket(t.Context(), duplicateHash); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("duplicate ticket hash error=%v code=%q", err, StoreErrorCodeOf(err))
	}
	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "contract-device", KeyThumbprint: "contract-thumb", ISCPSessionID: "contract-iscp"}

	const workers = 16
	redemptions := make(chan struct {
		binding app.MCPBinding
		err     error
	}, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			binding, err := repository.RedeemMCPAccessTicket(t.Context(), ticket.SecretHash, peer, now.Add(time.Second))
			redemptions <- struct {
				binding app.MCPBinding
				err     error
			}{binding: binding, err: err}
		}()
	}
	wait.Wait()
	close(redemptions)
	var binding app.MCPBinding
	succeeded := 0
	for result := range redemptions {
		if result.err == nil {
			succeeded++
			binding = result.binding
			continue
		}
		if !errors.Is(result.err, ErrMCPAccessTicketInvalid) {
			t.Fatalf("unexpected redemption error: %v", result.err)
		}
	}
	if succeeded != 1 || binding.ID == "" {
		t.Fatalf("successful redemptions=%d binding=%#v", succeeded, binding)
	}

	operations := make(chan struct {
		operation app.MCPOperation
		created   bool
		err       error
	}, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			operation, created, err := repository.CreateMCPOperation(t.Context(), app.MCPOperation{
				ID: "mcp-operation-contract", BindingID: binding.ID, IdempotencyKey: "contract-key", Fingerprint: "contract-fingerprint",
			})
			operations <- struct {
				operation app.MCPOperation
				created   bool
				err       error
			}{operation: operation, created: created, err: err}
		}()
	}
	wait.Wait()
	close(operations)
	createdCount := 0
	var operation app.MCPOperation
	for result := range operations {
		if result.err != nil || result.operation.ID != "mcp-operation-contract" {
			t.Fatalf("CreateMCPOperation=%#v created=%t err=%v", result.operation, result.created, result.err)
		}
		if result.created {
			createdCount++
		}
		operation = result.operation
	}
	if createdCount != 1 {
		t.Fatalf("created operations=%d, want 1", createdCount)
	}
	if _, _, err := repository.CreateMCPOperation(t.Context(), app.MCPOperation{
		ID: operation.ID, BindingID: binding.ID, IdempotencyKey: operation.IdempotencyKey, Fingerprint: "changed",
	}); !errors.Is(err, ErrMCPOperationConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("changed fingerprint error=%v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, _, err := repository.CreateMCPOperation(t.Context(), app.MCPOperation{
		ID: operation.ID, BindingID: binding.ID, IdempotencyKey: "different-key", Fingerprint: "different-key",
	}); !errors.Is(err, ErrMCPOperationConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("reused operation ID error=%v code=%q", err, StoreErrorCodeOf(err))
	}

	updates := make(chan error, workers)
	for index := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			candidate := operation
			candidate.ErrorCode = app.NewID("cas")
			candidate.ErrorMessage = time.Duration(index).String()
			_, err := repository.UpdateMCPOperation(t.Context(), candidate, operation.Version)
			updates <- err
		}()
	}
	wait.Wait()
	close(updates)
	updatedCount := 0
	for err := range updates {
		if err == nil {
			updatedCount++
			continue
		}
		if !errors.Is(err, ErrMCPOperationVersionConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("unexpected CAS error=%v code=%q", err, StoreErrorCodeOf(err))
		}
	}
	if updatedCount != 1 {
		t.Fatalf("successful CAS updates=%d, want 1", updatedCount)
	}
	current, found, err := repository.GetMCPOperation(t.Context(), operation.ID)
	if err != nil || !found {
		t.Fatalf("current operation=%#v found=%t err=%v", current, found, err)
	}
	changedIdentity := current
	changedIdentity.Fingerprint = "changed-after-create"
	if _, err := repository.UpdateMCPOperation(t.Context(), changedIdentity, current.Version); !errors.Is(err, ErrMCPOperationConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("changed operation identity error=%v code=%q", err, StoreErrorCodeOf(err))
	}

	newerIssue := testMCPAccessTicket(now.Add(2*time.Hour), "mcp-order-newer")
	newerIssue.ID = "mcp-ticket-order-a"
	newerIssue.ExpiresAt = now.Add(3 * time.Hour)
	olderIssue := testMCPAccessTicket(now.Add(time.Hour), "mcp-order-older")
	olderIssue.ID = "mcp-ticket-order-b"
	olderIssue.ExpiresAt = now.Add(20 * time.Hour)
	if _, err := repository.SaveMCPAccessTicket(t.Context(), newerIssue); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SaveMCPAccessTicket(t.Context(), olderIssue); err != nil {
		t.Fatal(err)
	}
	listed, err := repository.ListMCPAccessTickets(t.Context(), app.DefaultOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	newerIndex, olderIndex := -1, -1
	for index, listedTicket := range listed {
		switch listedTicket.ID {
		case newerIssue.ID:
			newerIndex = index
		case olderIssue.ID:
			olderIndex = index
		}
	}
	if newerIndex < 0 || olderIndex < 0 || newerIndex >= olderIndex {
		t.Fatalf("ticket issue ordering=%#v", listed)
	}
}
