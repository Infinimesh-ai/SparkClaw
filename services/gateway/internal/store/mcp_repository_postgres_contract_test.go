package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

type fakeMCPPostgresOps struct {
	*fakeConnectorPostgresOps
	rowQueue []onboardingPostgresRow
	rowSQL   []string
}

func (o *fakeMCPPostgresOps) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	o.rowSQL = append(o.rowSQL, sql)
	if len(o.rowQueue) == 0 {
		return fakeConnectorPostgresRow{}
	}
	row := o.rowQueue[0]
	o.rowQueue = o.rowQueue[1:]
	return row
}

func newFakePostgresMCPStore(transaction *fakeConnectorPostgresTx) (*PostgresStore, *fakeMCPPostgresOps, *fakeConnectorPostgresSession) {
	session := &fakeConnectorPostgresSession{transaction: transaction}
	base := &fakeConnectorPostgresOps{session: session}
	operations := &fakeMCPPostgresOps{fakeConnectorPostgresOps: base}
	return &PostgresStore{operationTimeouts: defaultOperationTimeouts, mcpPostgres: operations}, operations, session
}

func mcpPayloadPostgresRow(payload any) fakeConnectorPostgresRow {
	raw := mustJSON(payload)
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		*destinations[0].(*[]byte) = append([]byte(nil), raw...)
		return nil
	}}
}

func mcpRawPostgresRow(raw []byte) fakeConnectorPostgresRow {
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		*destinations[0].(*[]byte) = append([]byte(nil), raw...)
		return nil
	}}
}

func mcpListPostgresRow(id string, payload any) fakeConnectorPostgresRow {
	raw := mustJSON(payload)
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		*destinations[0].(*string) = id
		*destinations[1].(*[]byte) = append([]byte(nil), raw...)
		return nil
	}}
}

func mcpIDPostgresRow(id string) fakeConnectorPostgresRow {
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		*destinations[0].(*string) = id
		return nil
	}}
}

func TestPostgresMCPReadsClassifyAbsenceTransportCorruptionAndRowsErrors(t *testing.T) {
	now := time.Now().UTC()
	ticket := normalizeMCPAccessTicket(testMCPAccessTicket(now, "postgres-read-ticket"), now)
	binding := normalizeMCPBinding(app.MCPBinding{
		ID: "postgres-read-binding", OwnerID: app.DefaultOwnerID, DomainID: "domain-a", RequesterDeviceID: "device-a",
		RequesterKeyThumbprint: "thumb-a", Scope: app.MCPAccessConversation, Status: app.MCPBindingActive,
	}, now)
	operation := normalizeMCPOperation(app.MCPOperation{
		ID: "postgres-read-operation", BindingID: binding.ID, IdempotencyKey: "read", Fingerprint: "read",
	}, now)

	getters := []struct {
		name    string
		payload any
		invoke  func(*PostgresStore) error
	}{
		{name: "ticket", payload: ticket, invoke: func(repository *PostgresStore) error {
			_, _, err := repository.GetMCPAccessTicket(t.Context(), ticket.ID)
			return err
		}},
		{name: "binding", payload: binding, invoke: func(repository *PostgresStore) error {
			_, _, err := repository.GetMCPBinding(t.Context(), binding.ID)
			return err
		}},
		{name: "operation", payload: operation, invoke: func(repository *PostgresStore) error {
			_, _, err := repository.GetMCPOperation(t.Context(), operation.ID)
			return err
		}},
	}
	for _, getter := range getters {
		t.Run(getter.name+"/absence", func(t *testing.T) {
			transaction := &fakeConnectorPostgresTx{}
			repository, _, session := newFakePostgresMCPStore(transaction)
			if err := getter.invoke(repository); err != nil {
				t.Fatal(err)
			}
			if len(session.options) != 1 || session.options[0].IsoLevel != pgx.ReadCommitted || len(transaction.execSQL) != 1 || !strings.Contains(transaction.execSQL[0], "pg_advisory_xact_lock") || transaction.commits != 1 {
				t.Fatalf("options=%#v exec=%v commits=%d", session.options, transaction.execSQL, transaction.commits)
			}
		})
		t.Run(getter.name+"/transport", func(t *testing.T) {
			transaction := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{fakeConnectorPostgresRow{scan: func(...any) error { return errors.New("read failed") }}}}
			repository, _, _ := newFakePostgresMCPStore(transaction)
			if err := getter.invoke(repository); StoreErrorCodeOf(err) != StoreErrorUnavailable {
				t.Fatalf("error=%v code=%q", err, StoreErrorCodeOf(err))
			}
		})
		t.Run(getter.name+"/corrupt", func(t *testing.T) {
			transaction := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpRawPostgresRow([]byte(`{"broken"`))}}
			repository, _, _ := newFakePostgresMCPStore(transaction)
			if err := getter.invoke(repository); StoreErrorCodeOf(err) != StoreErrorCorrupt {
				t.Fatalf("error=%v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}

	lists := []struct {
		name   string
		row    fakeConnectorPostgresRow
		invoke func(*PostgresStore) error
	}{
		{name: "tickets", row: mcpListPostgresRow(ticket.ID, ticket), invoke: func(repository *PostgresStore) error {
			_, err := repository.ListMCPAccessTickets(t.Context(), "")
			return err
		}},
		{name: "bindings", row: mcpListPostgresRow(binding.ID, binding), invoke: func(repository *PostgresStore) error {
			_, err := repository.ListMCPBindings(t.Context(), "")
			return err
		}},
		{name: "operations", row: mcpListPostgresRow(operation.ID, operation), invoke: func(repository *PostgresStore) error {
			_, err := repository.ListMCPOperations(t.Context(), "")
			return err
		}},
	}
	for _, list := range lists {
		for _, failure := range []struct {
			name string
			rows *fakeConnectorPostgresRows
			err  error
		}{
			{name: "query", err: errors.New("query failed")},
			{name: "scan", rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{list.row}, scanErr: errors.New("scan failed")}},
			{name: "rows", rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{list.row}, err: errors.New("rows failed")}},
		} {
			t.Run(list.name+"/"+failure.name, func(t *testing.T) {
				transaction := &fakeConnectorPostgresTx{rowsQueue: []fakeConnectorRowsResult{{rows: failure.rows, err: failure.err}}}
				repository, _, _ := newFakePostgresMCPStore(transaction)
				if err := list.invoke(repository); StoreErrorCodeOf(err) != StoreErrorUnavailable {
					t.Fatalf("error=%v code=%q", err, StoreErrorCodeOf(err))
				}
			})
		}
	}
}

func TestPostgresMCPWriteFailureClassificationAndOwnership(t *testing.T) {
	unsafeFailure := errors.New("statement outcome unknown")
	commitFailure := errors.New("commit outcome unknown")
	safeFailure := safePostgresRetryError{errors.New("statement was not sent")}
	tests := []struct {
		name          string
		beginErr      error
		execErr       error
		commitErr     error
		execIndex     int
		wantCode      StoreErrorCode
		wantCandidate bool
		wantRelease   int
		wantTerminate int
		wantRollback  int
	}{
		{name: "success", wantCandidate: true, wantRelease: 1},
		{name: "safe begin", beginErr: safeFailure, wantCode: StoreErrorUnavailable, wantRelease: 1},
		{name: "unsafe begin", beginErr: unsafeFailure, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
		{name: "safe statement", execErr: safeFailure, execIndex: 3, wantCode: StoreErrorUnavailable, wantRelease: 1, wantRollback: 1},
		{name: "unsafe statement", execErr: unsafeFailure, execIndex: 3, wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantTerminate: 1},
		{name: "commit", commitErr: commitFailure, wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantTerminate: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := &fakeConnectorPostgresTx{execErrors: map[int]error{test.execIndex: test.execErr}, commitErr: test.commitErr}
			repository, _, session := newFakePostgresMCPStore(transaction)
			session.beginErr = test.beginErr
			candidate, err := repository.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(time.Now().UTC(), "postgres-write-"+test.name))
			if StoreErrorCodeOf(err) != test.wantCode || (candidate.ID != "") != test.wantCandidate {
				t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}
			if session.releases != test.wantRelease || session.terminates != test.wantTerminate || transaction.rollbacks != test.wantRollback {
				t.Fatalf("release=%d terminate=%d rollback=%d", session.releases, session.terminates, transaction.rollbacks)
			}
		})
	}
}

func TestPostgresMCPWritesAndReconciliationReadsShareBarriers(t *testing.T) {
	now := time.Now().UTC()

	t.Run("ticket ID and secret", func(t *testing.T) {
		writeTx := &fakeConnectorPostgresTx{}
		writer, _, _ := newFakePostgresMCPStore(writeTx)
		ticket, err := writer.SaveMCPAccessTicket(t.Context(), testMCPAccessTicket(now, "barrier-ticket-secret"))
		if err != nil {
			t.Fatal(err)
		}
		readIDTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(ticket)}}
		reader, _, _ := newFakePostgresMCPStore(readIDTx)
		if _, found, err := reader.GetMCPAccessTicket(t.Context(), ticket.ID); err != nil || !found {
			t.Fatalf("GetMCPAccessTicket found=%t err=%v", found, err)
		}
		readSecretTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(ticket)}}
		reader, _, _ = newFakePostgresMCPStore(readSecretTx)
		if _, found, err := reader.FindMCPAccessTicketBySecretHash(t.Context(), ticket.SecretHash); err != nil || !found {
			t.Fatalf("FindMCPAccessTicketBySecretHash found=%t err=%v", found, err)
		}
		assertMCPPostgresBarrierShared(t, writeTx, readIDTx, mcpAdvisoryKey("ticket-id", ticket.ID))
		assertMCPPostgresBarrierShared(t, writeTx, readSecretTx, mcpAdvisoryKey("ticket-secret", ticket.SecretHash))
	})

	t.Run("redemption peer", func(t *testing.T) {
		ticket := normalizeMCPAccessTicket(testMCPAccessTicket(now, "barrier-redeem-secret"), now)
		peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "barrier-device", KeyThumbprint: "barrier-thumb", ISCPSessionID: "barrier-session"}
		writeTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(ticket), fakeConnectorPostgresRow{}}}
		writer, _, _ := newFakePostgresMCPStore(writeTx)
		binding, err := writer.RedeemMCPAccessTicket(t.Context(), ticket.SecretHash, peer, now)
		if err != nil {
			t.Fatal(err)
		}
		readTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(binding)}}
		reader, _, _ := newFakePostgresMCPStore(readTx)
		if _, found, err := reader.FindMCPBindingForPeer(t.Context(), peer.DomainID, peer.DeviceID, peer.KeyThumbprint); err != nil || !found {
			t.Fatalf("FindMCPBindingForPeer found=%t err=%v", found, err)
		}
		peerKey := peer.DomainID + "\x00" + peer.DeviceID + "\x00" + peer.KeyThumbprint
		assertMCPPostgresBarrierShared(t, writeTx, readTx, mcpAdvisoryKey("peer", peerKey))
	})

	t.Run("operation idempotency and ID", func(t *testing.T) {
		writeTx := &fakeConnectorPostgresTx{}
		writer, _, _ := newFakePostgresMCPStore(writeTx)
		operation, created, err := writer.CreateMCPOperation(t.Context(), app.MCPOperation{
			ID: "barrier-operation", BindingID: "barrier-binding", IdempotencyKey: "barrier-key", Fingerprint: "barrier-fingerprint",
		})
		if err != nil || !created {
			t.Fatalf("operation=%#v created=%t err=%v", operation, created, err)
		}
		readIdempotencyTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(operation)}}
		reader, _, _ := newFakePostgresMCPStore(readIdempotencyTx)
		if _, found, err := reader.FindMCPOperationByIdempotency(t.Context(), operation.BindingID, operation.IdempotencyKey); err != nil || !found {
			t.Fatalf("FindMCPOperationByIdempotency found=%t err=%v", found, err)
		}
		idempotencyKey := operation.BindingID + "\x00" + operation.IdempotencyKey
		assertMCPPostgresBarrierShared(t, writeTx, readIdempotencyTx, mcpAdvisoryKey("operation-idempotency", idempotencyKey))

		updateTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(operation)}}
		updater, _, _ := newFakePostgresMCPStore(updateTx)
		candidate := operation
		candidate.State = app.MCPOperationApprovalRequired
		updated, err := updater.UpdateMCPOperation(t.Context(), candidate, operation.Version)
		if err != nil {
			t.Fatal(err)
		}
		readIDTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(updated)}}
		reader, _, _ = newFakePostgresMCPStore(readIDTx)
		if _, found, err := reader.GetMCPOperation(t.Context(), updated.ID); err != nil || !found {
			t.Fatalf("GetMCPOperation found=%t err=%v", found, err)
		}
		assertMCPPostgresBarrierShared(t, updateTx, readIDTx, mcpAdvisoryKey("operation", updated.ID))
	})
}

func assertMCPPostgresBarrierShared(t testing.TB, writeTx, readTx *fakeConnectorPostgresTx, want int64) {
	t.Helper()
	contains := func(transaction *fakeConnectorPostgresTx) bool {
		for index, sql := range transaction.execSQL {
			if strings.Contains(sql, "pg_advisory_xact_lock") && len(transaction.execArgs[index]) == 1 && transaction.execArgs[index][0] == want {
				return true
			}
		}
		return false
	}
	if !contains(writeTx) || !contains(readTx) {
		t.Fatalf("barrier %d not shared: write SQL=%v args=%v read SQL=%v args=%v", want, writeTx.execSQL, writeTx.execArgs, readTx.execSQL, readTx.execArgs)
	}
}

func TestPostgresMCPMultiRecordWritesAreAtomic(t *testing.T) {
	now := time.Now().UTC()
	ticket := normalizeMCPAccessTicket(testMCPAccessTicket(now, "postgres-redeem-atomic"), now)
	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}

	t.Run("redeem commits ticket session and binding together", func(t *testing.T) {
		transaction := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{
			mcpPayloadPostgresRow(ticket), fakeConnectorPostgresRow{},
		}}
		repository, _, session := newFakePostgresMCPStore(transaction)
		binding, err := repository.RedeemMCPAccessTicket(t.Context(), ticket.SecretHash, peer, now)
		if err != nil || binding.ID == "" || len(transaction.execSQL) != 5 || transaction.commits != 1 || transaction.rollbacks != 0 || session.releases != 1 {
			t.Fatalf("binding=%#v err=%v sql=%d commit=%d rollback=%d release=%d", binding, err, len(transaction.execSQL), transaction.commits, transaction.rollbacks, session.releases)
		}
		for index, fragment := range []string{"UPDATE mcp_access_tickets", "INSERT INTO sessions", "INSERT INTO mcp_bindings"} {
			index += 2
			if !strings.Contains(transaction.execSQL[index], fragment) {
				t.Fatalf("statement %d=%q, want %q", index, transaction.execSQL[index], fragment)
			}
		}
	})

	t.Run("redeem safe failure rolls back every record", func(t *testing.T) {
		transaction := &fakeConnectorPostgresTx{
			rowQueue:   []onboardingPostgresRow{mcpPayloadPostgresRow(ticket), fakeConnectorPostgresRow{}},
			execErrors: map[int]error{3: safePostgresRetryError{errors.New("session insert not sent")}},
		}
		repository, _, session := newFakePostgresMCPStore(transaction)
		binding, err := repository.RedeemMCPAccessTicket(t.Context(), ticket.SecretHash, peer, now)
		if binding.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || transaction.commits != 0 || transaction.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
			t.Fatalf("binding=%#v err=%v commit=%d rollback=%d release=%d terminate=%d", binding, err, transaction.commits, transaction.rollbacks, session.releases, session.terminates)
		}
	})

	t.Run("binding revoke includes nonterminal operations", func(t *testing.T) {
		binding := normalizeMCPBinding(app.MCPBinding{
			ID: "binding-revoke", OwnerID: app.DefaultOwnerID, DomainID: ticket.DomainID, RequesterDeviceID: peer.DeviceID,
			RequesterKeyThumbprint: peer.KeyThumbprint, Status: app.MCPBindingActive, Scope: app.MCPAccessConversation,
		}, now)
		operation := normalizeMCPOperation(app.MCPOperation{ID: "operation-revoke", BindingID: binding.ID, IdempotencyKey: "revoke", Fingerprint: "revoke"}, now)
		transaction := &fakeConnectorPostgresTx{
			rowQueue:  []onboardingPostgresRow{mcpPayloadPostgresRow(binding)},
			rowsQueue: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{mcpListPostgresRow(operation.ID, operation)}}}},
		}
		repository, _, session := newFakePostgresMCPStore(transaction)
		revoked, err := repository.RevokeMCPBinding(t.Context(), binding.ID, now.Add(time.Second))
		if err != nil || revoked.Status != app.MCPBindingRevoked || len(transaction.execSQL) != 3 || transaction.commits != 1 || session.releases != 1 {
			t.Fatalf("revoked=%#v err=%v sql=%d commit=%d release=%d", revoked, err, len(transaction.execSQL), transaction.commits, session.releases)
		}
	})

	t.Run("owner bulk delete is one transaction", func(t *testing.T) {
		transaction := &fakeConnectorPostgresTx{rowsQueue: []fakeConnectorRowsResult{
			{rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{mcpIDPostgresRow("ticket")}}},
			{rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{mcpIDPostgresRow("binding")}}},
		}}
		repository, _, session := newFakePostgresMCPStore(transaction)
		deleted, err := repository.DeleteMCPAccessRecords(t.Context(), app.DefaultOwnerID)
		if err != nil || deleted.DeletedTickets != 1 || deleted.DeletedBindings != 1 || len(transaction.querySQL) != 2 || len(transaction.execSQL) != 4 || transaction.commits != 1 || session.releases != 1 {
			t.Fatalf("deleted=%#v err=%v queries=%d exec=%d commit=%d release=%d", deleted, err, len(transaction.querySQL), len(transaction.execSQL), transaction.commits, session.releases)
		}
	})
}

func TestPostgresMCPOperationUnknownUpdateReturnsReconciliationCandidate(t *testing.T) {
	now := time.Now().UTC()
	existing := normalizeMCPOperation(app.MCPOperation{
		ID: "operation-update-unknown", BindingID: "binding", IdempotencyKey: "update", Fingerprint: "update",
	}, now)
	transaction := &fakeConnectorPostgresTx{
		rowQueue:   []onboardingPostgresRow{mcpPayloadPostgresRow(existing)},
		execErrors: map[int]error{2: errors.New("update outcome unknown")},
	}
	repository, _, session := newFakePostgresMCPStore(transaction)
	candidate := existing
	candidate.State = app.MCPOperationApprovalRequired
	updated, err := repository.UpdateMCPOperation(t.Context(), candidate, existing.Version)
	if updated.ID != existing.ID || updated.Version != existing.Version+1 || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || transaction.rollbacks != 0 || session.releases != 0 || session.terminates != 1 {
		t.Fatalf("updated=%#v err=%v rollback=%d release=%d terminate=%d", updated, err, transaction.rollbacks, session.releases, session.terminates)
	}
}

func TestPostgresMCPTransactionRowsErrorRollsBackWhenSafe(t *testing.T) {
	now := time.Now().UTC()
	binding := normalizeMCPBinding(app.MCPBinding{ID: "binding-rows", OwnerID: app.DefaultOwnerID, Status: app.MCPBindingActive}, now)
	transaction := &fakeConnectorPostgresTx{
		rowQueue: []onboardingPostgresRow{mcpPayloadPostgresRow(binding)},
		rowsQueue: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{
			err: safePostgresRetryError{errors.New("rows were not received")},
		}}},
	}
	repository, _, session := newFakePostgresMCPStore(transaction)
	candidate, err := repository.RevokeMCPBinding(t.Context(), binding.ID, now.Add(time.Second))
	if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || transaction.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
		t.Fatalf("candidate=%#v err=%v rollback=%d release=%d terminate=%d", candidate, err, transaction.rollbacks, session.releases, session.terminates)
	}
}

var _ ownerPostgresOps = (*fakeMCPPostgresOps)(nil)
var _ onboardingPostgresTx = (*fakeConnectorPostgresTx)(nil)
var _ onboardingPostgresRows = (*fakeConnectorPostgresRows)(nil)
var _ onboardingPostgresRow = fakeConnectorPostgresRow{}
