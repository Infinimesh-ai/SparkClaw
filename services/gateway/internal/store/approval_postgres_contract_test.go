package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeApprovalPostgresOps struct {
	session  onboardingPostgresSession
	row      onboardingPostgresRow
	rows     onboardingPostgresRows
	queryErr error
}

func (o *fakeApprovalPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	return o.session, nil
}

func (*fakeApprovalPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected approval pool Exec")
}

func (o *fakeApprovalPostgresOps) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return o.rows, o.queryErr
}

func (o *fakeApprovalPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	if o.row == nil {
		return fakeApprovalPostgresRow{err: errors.New("unexpected approval pool QueryRow")}
	}
	return o.row
}

type fakeApprovalPostgresRow struct {
	approval app.Approval
	err      error
	corrupt  string
}

func (r fakeApprovalPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanFakeApproval(destinations, r.approval, r.corrupt)
}

type fakeApprovalPostgresRows struct {
	values  []app.Approval
	index   int
	scanErr error
	err     error
	closed  bool
}

func (r *fakeApprovalPostgresRows) Next() bool { return r.index < len(r.values) }

func (r *fakeApprovalPostgresRows) Scan(destinations ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	value := r.values[r.index]
	r.index++
	return scanFakeApproval(destinations, value, "")
}

func (r *fakeApprovalPostgresRows) Err() error { return r.err }
func (r *fakeApprovalPostgresRows) Close()     { r.closed = true }

func scanFakeApproval(destinations []any, approval app.Approval, corrupt string) error {
	marshal := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		return raw
	}
	externalContext := marshal(approval.ExternalContext)
	resources := marshal(approval.Resources)
	arguments := marshal(approval.Arguments)
	policyContext := marshal(approval.PolicyContext)
	presentation := marshal(approval.Presentation)
	switch corrupt {
	case "external_context":
		externalContext = []byte(`{"broken"`)
	case "resources":
		resources = []byte(`{"broken"`)
	case "arguments":
		arguments = []byte(`{"broken"`)
	case "policy_context":
		policyContext = []byte(`{"broken"`)
	case "presentation":
		presentation = []byte(`{"broken"`)
	}
	*destinations[0].(*string) = approval.ID
	*destinations[1].(*string) = string(approval.Source)
	*destinations[2].(*string) = approval.ExternalID
	*destinations[3].(*[]byte) = externalContext
	*destinations[4].(*string) = approval.SessionID
	*destinations[5].(*string) = approval.RunID
	*destinations[6].(*string) = approval.ToolCallID
	*destinations[7].(*string) = approval.Tool
	*destinations[8].(*string) = string(approval.Risk)
	*destinations[9].(*string) = approval.Status
	*destinations[10].(*string) = approval.Summary
	*destinations[11].(*string) = approval.Reason
	*destinations[12].(*[]byte) = resources
	*destinations[13].(*[]byte) = arguments
	*destinations[14].(*time.Time) = approval.CreatedAt
	*destinations[15].(**time.Time) = approval.ResolvedAt
	*destinations[16].(*string) = approval.ResolutionNote
	*destinations[17].(*[]byte) = policyContext
	*destinations[18].(*[]byte) = presentation
	return nil
}

func newFakeApprovalPostgresStore(transaction *fakeRunPostgresTx) (*PostgresStore, *fakeRunPostgresSession, *fakeApprovalPostgresOps) {
	session := &fakeRunPostgresSession{transaction: transaction}
	operations := &fakeApprovalPostgresOps{session: session}
	return &PostgresStore{approvalPostgres: operations, operationTimeouts: defaultOperationTimeouts}, session, operations
}

func TestPostgresApprovalWritesAreAtomicLifecycleTransactions(t *testing.T) {
	existing := approvalContractFixture("approval-postgres-atomic", "external-atomic", time.Now().UTC())
	tests := []struct {
		name    string
		row     onboardingPostgresRow
		invoke  func(*PostgresStore) error
		wantSQL []string
	}{
		{
			name: "save", row: fakeApprovalPostgresRow{err: pgx.ErrNoRows},
			invoke: func(repository *PostgresStore) error {
				_, err := repository.SaveApproval(t.Context(), existing)
				return err
			},
			wantSQL: []string{"pg_advisory_xact_lock", "INSERT INTO approvals", "INSERT INTO audit_events", "INSERT INTO events"},
		},
		{
			name: "update", row: fakeApprovalPostgresRow{approval: existing},
			invoke: func(repository *PostgresStore) error {
				candidate := existing
				candidate.Summary = "updated"
				_, err := repository.UpdatePendingApproval(t.Context(), NewApprovalUpdateWithNote(existing, candidate, "edit"))
				return err
			},
			wantSQL: []string{"pg_advisory_xact_lock", "UPDATE approvals", "INSERT INTO audit_events", "INSERT INTO events"},
		},
		{
			name: "resolve", row: fakeApprovalPostgresRow{approval: existing},
			invoke: func(repository *PostgresStore) error {
				_, err := repository.ResolveApproval(t.Context(), existing.ID, "approved", "approved")
				return err
			},
			wantSQL: []string{"pg_advisory_xact_lock", "UPDATE approvals", "INSERT INTO audit_events", "INSERT INTO events"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := &fakeRunPostgresTx{row: test.row}
			repository, session, _ := newFakeApprovalPostgresStore(transaction)
			if err := test.invoke(repository); err != nil {
				t.Fatal(err)
			}
			if len(transaction.execSQL) != len(test.wantSQL) || transaction.commits != 1 || transaction.rollbacks != 0 || session.releases != 1 || session.terminates != 0 {
				t.Fatalf("transaction sql=%d commits=%d rollbacks=%d releases=%d terminates=%d", len(transaction.execSQL), transaction.commits, transaction.rollbacks, session.releases, session.terminates)
			}
			for index, fragment := range test.wantSQL {
				if !strings.Contains(transaction.execSQL[index], fragment) {
					t.Fatalf("statement %d = %q, want %q", index, transaction.execSQL[index], fragment)
				}
			}
		})
	}
}

func TestPostgresApprovalUnknownOutcomesReturnCandidateAndTerminate(t *testing.T) {
	unsafeFailure := errors.New("statement outcome unknown")
	commitFailure := errors.New("commit outcome unknown")
	tests := []struct {
		name       string
		execErrors map[int]error
		commitErr  error
		wantCause  error
	}{
		{name: "record statement", execErrors: map[int]error{1: unsafeFailure}, wantCause: unsafeFailure},
		{name: "audit statement", execErrors: map[int]error{2: unsafeFailure}, wantCause: unsafeFailure},
		{name: "event statement", execErrors: map[int]error{3: unsafeFailure}, wantCause: unsafeFailure},
		{name: "commit", commitErr: commitFailure, wantCause: commitFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := &fakeRunPostgresTx{row: fakeApprovalPostgresRow{err: pgx.ErrNoRows}, execErrors: test.execErrors, commitErr: test.commitErr}
			repository, session, _ := newFakeApprovalPostgresStore(transaction)
			candidate, err := repository.SaveApproval(t.Context(), approvalContractFixture("approval-postgres-unknown", "external-unknown", time.Now()))
			if candidate.ID != "approval-postgres-unknown" || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || !errors.Is(err, test.wantCause) {
				t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}
			if transaction.rollbacks != 0 || session.releases != 0 || session.terminates != 1 {
				t.Fatalf("rollback=%d release=%d terminate=%d", transaction.rollbacks, session.releases, session.terminates)
			}
		})
	}
}

func TestPostgresApprovalSafeStatementFailuresRollback(t *testing.T) {
	postgresFailure := &pgconn.PgError{Code: "23503", Message: "foreign key violation"}
	transaction := &fakeRunPostgresTx{row: fakeApprovalPostgresRow{err: pgx.ErrNoRows}, execErrors: map[int]error{1: postgresFailure}}
	repository, session, _ := newFakeApprovalPostgresStore(transaction)
	candidate, err := repository.SaveApproval(t.Context(), approvalContractFixture("approval-postgres-safe", "external-safe", time.Now()))
	if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorInternal || !errors.Is(err, postgresFailure) || transaction.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
		t.Fatalf("candidate=%#v err=%v rollback=%d release=%d terminate=%d", candidate, err, transaction.rollbacks, session.releases, session.terminates)
	}
}

func TestPostgresApprovalCorruptExistingRowRollsBack(t *testing.T) {
	existing := approvalContractFixture("approval-postgres-corrupt-existing", "external-corrupt-existing", time.Now())
	transaction := &fakeRunPostgresTx{row: fakeApprovalPostgresRow{approval: existing, corrupt: "presentation"}}
	repository, session, _ := newFakeApprovalPostgresStore(transaction)
	candidate, err := repository.SaveApproval(t.Context(), existing)
	if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorCorrupt || transaction.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
		t.Fatalf("candidate=%#v err=%v rollback=%d release=%d terminate=%d", candidate, err, transaction.rollbacks, session.releases, session.terminates)
	}
}

func TestPostgresApprovalUnknownCommitCanReconcileByID(t *testing.T) {
	transaction := &fakeRunPostgresTx{row: fakeApprovalPostgresRow{err: pgx.ErrNoRows}, commitErr: errors.New("commit uncertain")}
	repository, _, operations := newFakeApprovalPostgresStore(transaction)
	candidate, writeErr := repository.SaveApproval(t.Context(), approvalContractFixture("approval-postgres-reconcile", "external-reconcile", time.Now()))
	operations.row = fakeApprovalPostgresRow{approval: candidate}
	reconciled, err := ReconcileApprovalWrite(t.Context(), repository, candidate, writeErr)
	if err != nil || !approvalsEqual(reconciled, candidate) {
		t.Fatalf("reconciled=%#v err=%v writeErr=%v", reconciled, err, writeErr)
	}
}

func TestPostgresApprovalReadsClassifyQueryScanRowsAndCorruptJSON(t *testing.T) {
	approval := approvalContractFixture("approval-postgres-read", "external-read", time.Now().UTC())
	t.Run("query", func(t *testing.T) {
		repository := &PostgresStore{approvalPostgres: &fakeApprovalPostgresOps{queryErr: errors.New("query failed")}, operationTimeouts: defaultOperationTimeouts}
		if _, err := repository.ListApprovals(t.Context(), ""); StoreErrorCodeOf(err) != StoreErrorUnavailable {
			t.Fatalf("query error=%v code=%q", err, StoreErrorCodeOf(err))
		}
	})
	t.Run("scan", func(t *testing.T) {
		rows := &fakeApprovalPostgresRows{values: []app.Approval{approval}, scanErr: errors.New("scan failed")}
		repository := &PostgresStore{approvalPostgres: &fakeApprovalPostgresOps{rows: rows}, operationTimeouts: defaultOperationTimeouts}
		if _, err := repository.ListApprovals(t.Context(), ""); StoreErrorCodeOf(err) != StoreErrorUnavailable || !rows.closed {
			t.Fatalf("scan error=%v code=%q closed=%t", err, StoreErrorCodeOf(err), rows.closed)
		}
	})
	t.Run("rows", func(t *testing.T) {
		rows := &fakeApprovalPostgresRows{err: errors.New("rows failed")}
		repository := &PostgresStore{approvalPostgres: &fakeApprovalPostgresOps{rows: rows}, operationTimeouts: defaultOperationTimeouts}
		if _, err := repository.ListApprovals(t.Context(), ""); StoreErrorCodeOf(err) != StoreErrorUnavailable || !rows.closed {
			t.Fatalf("rows error=%v code=%q closed=%t", err, StoreErrorCodeOf(err), rows.closed)
		}
	})
	for _, field := range []string{"external_context", "resources", "arguments", "policy_context", "presentation"} {
		t.Run("corrupt "+field, func(t *testing.T) {
			repository := &PostgresStore{approvalPostgres: &fakeApprovalPostgresOps{row: fakeApprovalPostgresRow{approval: approval, corrupt: field}}, operationTimeouts: defaultOperationTimeouts}
			if _, _, err := repository.GetApproval(t.Context(), approval.ID); StoreErrorCodeOf(err) != StoreErrorCorrupt {
				t.Fatalf("corrupt %s error=%v code=%q", field, err, StoreErrorCodeOf(err))
			}
		})
	}
}
