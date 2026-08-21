package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileMCPDefiniteFailureRestoresMemoryState(t *testing.T) {
	repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	repository.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	candidate, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(time.Now().UTC(), "mcp-definite"))
	if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorDurability || !errors.Is(err, errFileCommitInjected) || repository.currentFileFence() != nil {
		t.Fatalf("candidate=%#v err=%v fence=%v", candidate, err, repository.currentFileFence())
	}
	if tickets, listErr := repository.ListMCPAccessTickets(t.Context(), ""); listErr != nil || len(tickets) != 0 {
		t.Fatalf("definite failure retained tickets=%#v err=%v", tickets, listErr)
	}
}

func TestFileMCPUnknownOutcomesReconcileAndSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	repository, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	repository.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true}
	ticket, writeErr := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(time.Now().UTC(), "mcp-ticket-unknown"))
	if ticket.ID == "" || StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || repository.currentFileFence() == nil {
		t.Fatalf("unknown ticket=%#v err=%v fence=%v", ticket, writeErr, repository.currentFileFence())
	}
	ticket, err = ReconcileMCPAccessTicketWrite(t.Context(), repository, ticket, writeErr)
	if err != nil || repository.currentFileFence() != nil {
		t.Fatalf("reconciled ticket=%#v err=%v fence=%v", ticket, err, repository.currentFileFence())
	}

	operation, created, err := repository.CreateMCPOperation(t.Context(), app.MCPOperation{
		ID: "mcp-operation-unknown", BindingID: "mcp-binding-unknown", IdempotencyKey: "unknown-create", Fingerprint: "unknown-create",
	})
	if err != nil || !created {
		t.Fatalf("baseline operation=%#v created=%t err=%v", operation, created, err)
	}
	repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	candidate := operation
	candidate.State = app.MCPOperationApprovalRequired
	updated, writeErr := repository.UpdateMCPOperation(t.Context(), candidate, operation.Version)
	if updated.Version != operation.Version+1 || StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || repository.currentFileFence() == nil {
		t.Fatalf("unknown update=%#v err=%v fence=%v", updated, writeErr, repository.currentFileFence())
	}
	updated, err = ReconcileMCPOperationUpdate(t.Context(), repository, updated, writeErr)
	if err != nil || repository.currentFileFence() != nil {
		t.Fatalf("reconciled update=%#v err=%v fence=%v", updated, err, repository.currentFileFence())
	}

	restarted, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	storedTicket, found, err := restarted.GetMCPAccessTicket(t.Context(), ticket.ID)
	if err != nil || !found || !mcpRecordsEqual(storedTicket, ticket) {
		t.Fatalf("restarted ticket=%#v found=%t err=%v", storedTicket, found, err)
	}
	storedOperation, found, err := restarted.GetMCPOperation(t.Context(), updated.ID)
	if err != nil || !found || !mcpRecordsEqual(storedOperation, updated) {
		t.Fatalf("restarted operation=%#v found=%t err=%v", storedOperation, found, err)
	}
}

func TestFileMCPOperationCreateUnknownOutcomeReconciles(t *testing.T) {
	repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	candidate, created, writeErr := repository.CreateMCPOperation(t.Context(), app.MCPOperation{
		ID: "mcp-create-unknown", BindingID: "binding", IdempotencyKey: "create-unknown", Fingerprint: "create-unknown",
	})
	if !created || StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || repository.currentFileFence() == nil {
		t.Fatalf("candidate=%#v created=%t err=%v fence=%v", candidate, created, writeErr, repository.currentFileFence())
	}
	reconciled, created, err := ReconcileMCPOperationCreate(t.Context(), repository, candidate, created, writeErr)
	if err != nil || !created || !mcpRecordsEqual(reconciled, candidate) || repository.currentFileFence() != nil {
		t.Fatalf("reconciled=%#v created=%t err=%v fence=%v", reconciled, created, err, repository.currentFileFence())
	}
}

func TestFileMCPRedemptionUnknownOutcomeReconcilesAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	repository, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ticket, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(now, "mcp-redeem-unknown"))
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "device-unknown", KeyThumbprint: "thumb-unknown", ISCPSessionID: "iscp-unknown"}
	repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	binding, writeErr := repository.RedeemMCPAccessTicket(t.Context(), ticket.SecretHash, peer, now.Add(time.Second))
	if binding.ID == "" || StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || repository.currentFileFence() == nil {
		t.Fatalf("binding=%#v err=%v fence=%v", binding, writeErr, repository.currentFileFence())
	}
	binding, err = ReconcileMCPAccessTicketRedemption(t.Context(), repository, ticket.ID, ticket.SecretHash, peer, binding, writeErr)
	if err != nil || repository.currentFileFence() != nil {
		t.Fatalf("reconciled binding=%#v err=%v fence=%v", binding, err, repository.currentFileFence())
	}
	restarted, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := restarted.GetMCPBinding(t.Context(), binding.ID)
	if err != nil || !found || !mcpRecordsEqual(stored, binding) {
		t.Fatalf("restarted binding=%#v found=%t err=%v", stored, found, err)
	}
}

func TestFileMCPRemainingUnknownOutcomesReconcileAndSurviveRestart(t *testing.T) {
	t.Run("ticket revoke", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(time.Now(), "mcp-revoke-unknown"))
		if err != nil {
			t.Fatal(err)
		}
		repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		candidate, writeErr := repository.RevokeMCPAccessTicket(t.Context(), ticket.ID, time.Now())
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || repository.currentFileFence() == nil {
			t.Fatalf("candidate=%#v err=%v fence=%v", candidate, writeErr, repository.currentFileFence())
		}
		reconciled, err := ReconcileMCPAccessTicketRevoke(t.Context(), repository, candidate, writeErr)
		if err != nil || repository.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v err=%v fence=%v", reconciled, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := restarted.GetMCPAccessTicket(t.Context(), ticket.ID)
		if err != nil || !found || !mcpRecordsEqual(stored, reconciled) {
			t.Fatalf("stored=%#v found=%t err=%v", stored, found, err)
		}
	})

	t.Run("ticket delete", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(time.Now(), "mcp-delete-unknown"))
		if err != nil {
			t.Fatal(err)
		}
		repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		candidate, writeErr := repository.DeleteMCPAccessTicket(t.Context(), ticket.OwnerID, ticket.ID)
		reconciled, err := ReconcileMCPAccessTicketDelete(t.Context(), repository, candidate, writeErr)
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || err != nil || reconciled.ID != ticket.ID || repository.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v writeErr=%v err=%v fence=%v", reconciled, writeErr, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, found, err := restarted.GetMCPAccessTicket(t.Context(), ticket.ID); err != nil || found {
			t.Fatalf("found=%t err=%v", found, err)
		}
	})

	t.Run("binding revoke", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, _, binding := newFileMCPBindingFixture(t, path, "mcp-binding-revoke-unknown")
		operation, _, err := repository.CreateMCPOperation(t.Context(), app.MCPOperation{
			ID: "operation-revoke-unknown", BindingID: binding.ID, IdempotencyKey: "revoke", Fingerprint: "revoke",
		})
		if err != nil {
			t.Fatal(err)
		}
		repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		candidate, writeErr := repository.RevokeMCPBinding(t.Context(), binding.ID, time.Now())
		reconciled, err := ReconcileMCPBindingRevoke(t.Context(), repository, candidate, writeErr)
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || err != nil || reconciled.Status != app.MCPBindingRevoked || repository.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v writeErr=%v err=%v fence=%v", reconciled, writeErr, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := restarted.GetMCPOperation(t.Context(), operation.ID)
		if err != nil || !found || stored.State != app.MCPOperationRevoked {
			t.Fatalf("stored=%#v found=%t err=%v", stored, found, err)
		}
	})

	t.Run("binding delete", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, _, binding := newFileMCPBindingFixture(t, path, "mcp-binding-delete-unknown")
		if _, err := repository.RevokeMCPBinding(t.Context(), binding.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		candidate, writeErr := repository.DeleteMCPBinding(t.Context(), binding.OwnerID, binding.ID)
		reconciled, err := ReconcileMCPBindingDelete(t.Context(), repository, candidate, writeErr)
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || err != nil || reconciled.ID != binding.ID || repository.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v writeErr=%v err=%v fence=%v", reconciled, writeErr, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, found, err := restarted.GetMCPBinding(t.Context(), binding.ID); err != nil || found {
			t.Fatalf("found=%t err=%v", found, err)
		}
	})

	t.Run("binding touch", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, _, binding := newFileMCPBindingFixture(t, path, "mcp-binding-touch-unknown")
		touchedAt := time.Now()
		repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		writeErr := repository.TouchMCPBinding(t.Context(), binding.ID, "iscp-touched", touchedAt)
		reconciled, err := ReconcileMCPBindingTouch(t.Context(), repository, binding, "iscp-touched", touchedAt, writeErr)
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || err != nil || reconciled.LatestISCPSessionID != "iscp-touched" || repository.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v writeErr=%v err=%v fence=%v", reconciled, writeErr, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := restarted.GetMCPBinding(t.Context(), binding.ID)
		if err != nil || !found || !mcpRecordsEqual(stored, reconciled) {
			t.Fatalf("stored=%#v found=%t err=%v", stored, found, err)
		}
	})

	t.Run("owner delete", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		repository, _, binding := newFileMCPBindingFixture(t, path, "mcp-owner-delete-unknown")
		pending, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(time.Now(), "mcp-owner-delete-pending"))
		if err != nil {
			t.Fatal(err)
		}
		repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		candidate, writeErr := repository.DeleteMCPAccessRecords(t.Context(), binding.OwnerID)
		reconciled, err := ReconcileMCPAccessRecordDeletion(t.Context(), repository, binding.OwnerID, candidate, writeErr)
		if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || err != nil || reconciled.DeletedTickets != 2 || reconciled.DeletedBindings != 1 || repository.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v writeErr=%v err=%v fence=%v", reconciled, writeErr, err, repository.currentFileFence())
		}
		restarted, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, found, err := restarted.GetMCPAccessTicket(t.Context(), pending.ID); err != nil || found {
			t.Fatalf("pending found=%t err=%v", found, err)
		}
	})
}

func newFileMCPBindingFixture(t testing.TB, path, secretHash string) (*FileStore, app.MCPAccessTicket, app.MCPBinding) {
	t.Helper()
	repository, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ticket, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(now, secretHash))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := repository.RedeemMCPAccessTicket(t.Context(), ticket.SecretHash, app.MCPPeerIdentity{
		DomainID: ticket.DomainID, DeviceID: "device-" + secretHash, KeyThumbprint: "thumb-" + secretHash, ISCPSessionID: "iscp-" + secretHash,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return repository, ticket, binding
}
