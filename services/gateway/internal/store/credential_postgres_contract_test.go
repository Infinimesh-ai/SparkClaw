package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/semaphore"
)

type fakeCredentialPostgresOps struct {
	session    *fakeCredentialPostgresSession
	acquireErr error
}

func (o *fakeCredentialPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	if o.acquireErr != nil {
		return nil, o.acquireErr
	}
	return o.session, nil
}

func (*fakeCredentialPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (*fakeCredentialPostgresOps) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return &fakeCredentialPostgresRows{}, nil
}

func (*fakeCredentialPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	return fakeCredentialPostgresRow{err: pgx.ErrNoRows}
}

type fakeCredentialPostgresSession struct {
	transaction  *fakeCredentialPostgresTx
	beginErr     error
	options      []pgx.TxOptions
	releases     int
	terminates   int
	terminateErr error
}

func (s *fakeCredentialPostgresSession) Begin(_ context.Context, options pgx.TxOptions) (onboardingPostgresTx, error) {
	s.options = append(s.options, options)
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return s.transaction, nil
}

func (s *fakeCredentialPostgresSession) Release() { s.releases++ }

func (s *fakeCredentialPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return s.terminateErr
}

type fakeCredentialPostgresTx struct {
	execErrors   []error
	execSQL      []string
	rowsAffected []int64
	rowQueue     []onboardingPostgresRow
	rowSQL       []string
	commitErr    error
	rollbackErr  error
	commits      int
	rollbacks    int
}

func (t *fakeCredentialPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	index := len(t.execSQL)
	t.execSQL = append(t.execSQL, sql)
	if index < len(t.execErrors) && t.execErrors[index] != nil {
		return pgconn.CommandTag{}, t.execErrors[index]
	}
	rows := int64(1)
	if index < len(t.rowsAffected) {
		rows = t.rowsAffected[index]
	}
	if rows == 0 {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (t *fakeCredentialPostgresTx) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	t.rowSQL = append(t.rowSQL, sql)
	if len(t.rowQueue) == 0 {
		return fakeCredentialPostgresRow{err: pgx.ErrNoRows}
	}
	row := t.rowQueue[0]
	t.rowQueue = t.rowQueue[1:]
	return row
}

func (*fakeCredentialPostgresTx) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return &fakeCredentialPostgresRows{}, nil
}

func (t *fakeCredentialPostgresTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeCredentialPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return t.rollbackErr
}

type fakeCredentialPostgresRow struct {
	secret app.CredentialSecret
	err    error
}

func (r fakeCredentialPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	*(destinations[0].(*string)) = r.secret.Ref
	*(destinations[1].(*string)) = r.secret.Kind
	*(destinations[2].(*string)) = r.secret.Value
	*(destinations[3].(*time.Time)) = r.secret.CreatedAt
	*(destinations[4].(*time.Time)) = r.secret.UpdatedAt
	return nil
}

type fakeCredentialPostgresRows struct{}

func (*fakeCredentialPostgresRows) Next() bool        { return false }
func (*fakeCredentialPostgresRows) Scan(...any) error { return nil }
func (*fakeCredentialPostgresRows) Err() error        { return nil }
func (*fakeCredentialPostgresRows) Close()            {}

func newFakeCredentialPostgresStore(now time.Time, transaction *fakeCredentialPostgresTx) (*PostgresStore, *fakeCredentialPostgresSession) {
	session := &fakeCredentialPostgresSession{transaction: transaction}
	operations := &fakeCredentialPostgresOps{session: session}
	return &PostgresStore{
		operationTimeouts: defaultOperationTimeouts, credentialPostgres: operations,
		credentialCommandGate: semaphore.NewWeighted(1), credentialWriteHighWater: map[string]time.Time{},
		credentialNow: func() time.Time { return now },
	}, session
}

func validPostgresCredential(now time.Time) app.CredentialSecret {
	return app.CredentialSecret{Ref: "credential-postgres", Kind: "token", Value: "secret", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)}
}

func TestPostgresCredentialBeginFailureOwnsAcquiredSession(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	unsafeFailure := errors.New("begin outcome unknown")
	for _, testCase := range []struct {
		name          string
		failure       error
		wantCode      StoreErrorCode
		wantRelease   int
		wantTerminate int
	}{
		{name: "safe transport", failure: safePostgresRetryError{errors.New("not sent")}, wantCode: StoreErrorUnavailable, wantRelease: 1},
		{name: "server rejection", failure: &pgconn.PgError{Code: "40001"}, wantCode: StoreErrorInternal, wantRelease: 1},
		{name: "unsafe transport", failure: unsafeFailure, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
		{name: "context after acquire", failure: context.Canceled, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			st, session := newFakeCredentialPostgresStore(now, &fakeCredentialPostgresTx{})
			session.beginErr = testCase.failure
			_, err := st.SaveCredentialSecret(t.Context(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-postgres", Kind: "token", Value: "secret"}))
			if StoreErrorCodeOf(err) != testCase.wantCode || session.releases != testCase.wantRelease || session.terminates != testCase.wantTerminate {
				t.Fatalf("err=%v code=%q release=%d terminate=%d", err, StoreErrorCodeOf(err), session.releases, session.terminates)
			}
		})
	}
}

func TestPostgresCredentialGetUsesBarrierAndOwnsUnknownFailures(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	secret := validPostgresCredential(now)
	t.Run("success", func(t *testing.T) {
		tx := &fakeCredentialPostgresTx{rowQueue: []onboardingPostgresRow{fakeCredentialPostgresRow{secret: secret}}}
		st, session := newFakeCredentialPostgresStore(now, tx)
		got, found, err := st.GetCredentialSecret(t.Context(), secret.Ref)
		if err != nil || !found || !credentialSecretsEqual(got, secret) || len(session.options) != 1 || session.options[0].IsoLevel != pgx.ReadCommitted || len(tx.execSQL) != 1 || len(tx.rowSQL) != 1 || tx.commits != 1 || session.releases != 1 {
			t.Fatalf("got=%#v found=%v err=%v options=%#v exec=%v rows=%v commit=%d release=%d", got, found, err, session.options, tx.execSQL, tx.rowSQL, tx.commits, session.releases)
		}
	})
	for _, testCase := range []struct {
		name         string
		rowErr       error
		commitErr    error
		wantCode     StoreErrorCode
		wantRollback int
	}{
		{name: "unsafe scan", rowErr: errors.New("scan outcome unknown"), wantCode: StoreErrorUnknownOutcome},
		{name: "context scan", rowErr: context.Canceled, wantCode: StoreErrorUnknownOutcome},
		{name: "server scan", rowErr: &pgconn.PgError{Code: "XX000"}, wantCode: StoreErrorInternal, wantRollback: 1},
		{name: "commit", commitErr: errors.New("commit unknown"), wantCode: StoreErrorUnknownOutcome},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			row := onboardingPostgresRow(fakeCredentialPostgresRow{secret: secret, err: testCase.rowErr})
			tx := &fakeCredentialPostgresTx{rowQueue: []onboardingPostgresRow{row}, commitErr: testCase.commitErr}
			st, session := newFakeCredentialPostgresStore(now, tx)
			_, _, err := st.GetCredentialSecret(t.Context(), secret.Ref)
			if StoreErrorCodeOf(err) != testCase.wantCode || tx.rollbacks != testCase.wantRollback {
				t.Fatalf("err=%v code=%q rollback=%d", err, StoreErrorCodeOf(err), tx.rollbacks)
			}
			if testCase.wantCode == StoreErrorUnknownOutcome && (session.terminates != 1 || session.releases != 0) {
				t.Fatalf("terminate=%d release=%d", session.terminates, session.releases)
			}
		})
	}
}

func TestPostgresCredentialUnknownWriteReturnsExactCandidate(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name       string
		execErrors []error
		commitErr  error
	}{
		{name: "mutation", execErrors: []error{nil, errors.New("mutation unknown")}},
		{name: "audit", execErrors: []error{nil, nil, errors.New("audit unknown")}},
		{name: "commit", commitErr: errors.New("commit unknown")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tx := &fakeCredentialPostgresTx{rowQueue: []onboardingPostgresRow{fakeCredentialPostgresRow{err: pgx.ErrNoRows}}, execErrors: testCase.execErrors, commitErr: testCase.commitErr}
			st, session := newFakeCredentialPostgresStore(now, tx)
			candidate, err := st.SaveCredentialSecret(t.Context(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-postgres", Kind: "token", Value: "secret"}))
			if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || candidate.Ref != "credential-postgres" || candidate.Value != "secret" || !candidate.CreatedAt.Equal(now) || session.terminates != 1 || session.releases != 0 {
				t.Fatalf("candidate=%#v err=%v code=%q terminate=%d release=%d", candidate, err, StoreErrorCodeOf(err), session.terminates, session.releases)
			}
		})
	}
}

func TestPostgresCredentialDefiniteStatementAndRollbackFailure(t *testing.T) {
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)
	rollbackFailure := errors.New("rollback failed")
	terminateFailure := errors.New("terminate failed")
	tx := &fakeCredentialPostgresTx{
		rowQueue:   []onboardingPostgresRow{fakeCredentialPostgresRow{err: pgx.ErrNoRows}},
		execErrors: []error{nil, nil, &pgconn.PgError{Code: "XX000"}}, rollbackErr: rollbackFailure,
	}
	st, session := newFakeCredentialPostgresStore(now, tx)
	session.terminateErr = terminateFailure
	candidate, err := st.SaveCredentialSecret(t.Context(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-postgres", Kind: "token", Value: "secret"}))
	if candidate.Ref != "" || StoreErrorCodeOf(err) != StoreErrorInternal || tx.rollbacks != 1 || session.terminates != 1 || session.releases != 0 || !errors.Is(err, rollbackFailure) || !errors.Is(err, terminateFailure) {
		t.Fatalf("candidate=%#v err=%v code=%q rollback=%d terminate=%d release=%d", candidate, err, StoreErrorCodeOf(err), tx.rollbacks, session.terminates, session.releases)
	}
}

func TestPostgresCredentialCommandAdmissionHonorsDeadline(t *testing.T) {
	st, _ := newFakeCredentialPostgresStore(time.Now().UTC(), &fakeCredentialPostgresTx{})
	if err := st.credentialCommandGate.Acquire(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := st.SaveCredentialSecret(ctx, NewCredentialCreate(app.CredentialSecret{Ref: "credential-postgres", Kind: "token", Value: "secret"}))
		done <- err
	}()
	err := <-done
	st.credentialCommandGate.Release(1)
	if StoreErrorCodeOf(err) != StoreErrorTimeout {
		t.Fatalf("admission err=%v code=%q", err, StoreErrorCodeOf(err))
	}
}

func TestPostgresCredentialRealDatabaseContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	st, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.db.Exec(t.Context(), `TRUNCATE credential_secrets, audit_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	created, err := st.SaveCredentialSecret(t.Context(), NewCredentialCreate(app.CredentialSecret{Ref: "credential-real", Kind: "token", Value: "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveCredentialSecret(t.Context(), NewCredentialCreate(app.CredentialSecret{Ref: created.Ref, Kind: "token", Value: "other"})); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("duplicate create=%v code=%q", err, StoreErrorCodeOf(err))
	}
	replaced, err := st.SaveCredentialSecret(t.Context(), NewCredentialReplace(created, app.CredentialSecret{Ref: created.Ref, Kind: "token-v2", Value: "replacement"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteCredentialSecret(t.Context(), NewCredentialDeleteCondition(created)); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("stale delete=%v code=%q", err, StoreErrorCodeOf(err))
	}
	deleted, err := st.DeleteCredentialSecret(t.Context(), NewCredentialDeleteCondition(replaced))
	if err != nil || !credentialSecretsEqual(deleted, replaced) {
		t.Fatalf("deleted=%#v err=%v", deleted, err)
	}
	if _, found, err := st.GetCredentialSecret(t.Context(), created.Ref); err != nil || found {
		t.Fatalf("deleted found=%v err=%v", found, err)
	}
	audits := st.ListAudit("")
	counts := map[string]int{}
	for _, audit := range audits {
		counts[audit.Type]++
		if audit.Type == "credential_secret.saved" {
			if len(audit.Fields) != 2 || audit.Fields["ref"] != created.Ref || audit.Fields["kind"] == nil {
				t.Fatalf("save audit=%#v", audit)
			}
		}
		if audit.Type == "credential_secret.deleted" && (len(audit.Fields) != 1 || audit.Fields["ref"] != created.Ref) {
			t.Fatalf("delete audit=%#v", audit)
		}
	}
	if counts["credential_secret.saved"] != 2 || counts["credential_secret.deleted"] != 1 {
		t.Fatalf("credential audits=%v", counts)
	}
}
