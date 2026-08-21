package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeConversationPostgresOps struct {
	session    onboardingPostgresSession
	rowQueue   []onboardingPostgresRow
	rows       onboardingPostgresRows
	queryErr   error
	queryCalls int
}

func (o *fakeConversationPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	return o.session, nil
}

func (o *fakeConversationPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected pool Exec")
}

func (o *fakeConversationPostgresOps) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	o.queryCalls++
	return o.rows, o.queryErr
}

func (o *fakeConversationPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	if len(o.rowQueue) == 0 {
		return fakeConversationPostgresRow{err: errors.New("unexpected pool QueryRow")}
	}
	row := o.rowQueue[0]
	o.rowQueue = o.rowQueue[1:]
	return row
}

type fakeConversationPostgresSession struct {
	transaction onboardingPostgresTx
	releases    int
	terminates  int
}

func (s *fakeConversationPostgresSession) Begin(context.Context, pgx.TxOptions) (onboardingPostgresTx, error) {
	return s.transaction, nil
}

func (s *fakeConversationPostgresSession) Release() { s.releases++ }

func (s *fakeConversationPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return nil
}

type fakeConversationPostgresTx struct {
	rowQueue   []onboardingPostgresRow
	execSQL    []string
	execErrors map[int]error
	commits    int
	rollbacks  int
}

func (t *fakeConversationPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	index := len(t.execSQL)
	t.execSQL = append(t.execSQL, sql)
	if err := t.execErrors[index]; err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (t *fakeConversationPostgresTx) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	if len(t.rowQueue) == 0 {
		return fakeConversationPostgresRow{err: errors.New("unexpected transaction QueryRow")}
	}
	row := t.rowQueue[0]
	t.rowQueue = t.rowQueue[1:]
	return row
}

func (t *fakeConversationPostgresTx) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return nil, errors.New("unexpected transaction Query")
}

func (t *fakeConversationPostgresTx) Commit(context.Context) error {
	t.commits++
	return nil
}

func (t *fakeConversationPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return nil
}

type fakeConversationPostgresRow struct {
	kind           string
	session        app.Session
	message        app.Message
	attachments    []byte
	requestedMedia []byte
	err            error
}

func fakeConversationSessionRow(session app.Session) fakeConversationPostgresRow {
	return fakeConversationPostgresRow{kind: "session", session: session}
}

func fakeConversationMessageRow(message app.Message, attachments, requestedMedia []byte) fakeConversationPostgresRow {
	return fakeConversationPostgresRow{
		kind: "message", message: message,
		attachments: attachments, requestedMedia: requestedMedia,
	}
}

func (r fakeConversationPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	switch r.kind {
	case "session":
		if len(destinations) != 8 {
			return errors.New("fake conversation session row shape mismatch")
		}
		*(destinations[0].(*string)) = r.session.ID
		*(destinations[1].(*string)) = r.session.OwnerID
		*(destinations[2].(*string)) = r.session.WorkspaceRoot
		*(destinations[3].(*string)) = r.session.Title
		*(destinations[4].(*string)) = r.session.Source
		*(destinations[5].(*bool)) = r.session.Hidden
		*(destinations[6].(*time.Time)) = r.session.CreatedAt
		*(destinations[7].(*time.Time)) = r.session.UpdatedAt
		return nil
	case "message":
		if len(destinations) != 8 {
			return errors.New("fake conversation message row shape mismatch")
		}
		*(destinations[0].(*string)) = r.message.ID
		*(destinations[1].(*string)) = r.message.SessionID
		*(destinations[2].(*string)) = r.message.RunID
		*(destinations[3].(*string)) = r.message.Role
		*(destinations[4].(*string)) = r.message.Content
		*(destinations[5].(*[]byte)) = append([]byte(nil), r.attachments...)
		*(destinations[6].(*[]byte)) = append([]byte(nil), r.requestedMedia...)
		*(destinations[7].(*time.Time)) = r.message.CreatedAt
		return nil
	default:
		return errors.New("fake conversation row kind mismatch")
	}
}

type fakeConversationPostgresRows struct {
	rows  []fakeConversationPostgresRow
	index int
	err   error
}

func (r *fakeConversationPostgresRows) Next() bool { return r.index < len(r.rows) }

func (r *fakeConversationPostgresRows) Scan(destinations ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}

func (r *fakeConversationPostgresRows) Err() error { return r.err }
func (r *fakeConversationPostgresRows) Close()     {}

func TestPostgresConversationAppendUsesOneAtomicTransaction(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	sessionRecord := app.Session{
		ID: "session-conversation", OwnerID: app.DefaultOwnerID, Title: "New SparkClaw Session",
		Source: "webchat", CreatedAt: now, UpdatedAt: now,
	}
	message := app.Message{
		ID: "message-conversation", SessionID: sessionRecord.ID, RunID: "run-conversation",
		Role: "user", Content: "atomic append", CreatedAt: now.Add(time.Second),
		Attachments: []app.MessageAttachment{{Name: "proof.txt"}},
	}

	for _, testCase := range []struct {
		name      string
		execError error
		wantCode  StoreErrorCode
	}{
		{name: "success"},
		{name: "event failure rolls back", execError: errors.New("event insert failed"), wantCode: StoreErrorUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tx := &fakeConversationPostgresTx{
				rowQueue: []onboardingPostgresRow{
					fakeConversationSessionRow(sessionRecord),
					fakeConversationMessageRow(message, []byte(`[{"name":"proof.txt"}]`), []byte(`[]`)),
				},
				execErrors: map[int]error{},
			}
			if testCase.execError != nil {
				tx.execErrors[1] = testCase.execError
			}
			session := &fakeConversationPostgresSession{transaction: tx}
			backend := &fakeConversationPostgresOps{
				session:  session,
				rowQueue: []onboardingPostgresRow{fakeConversationPostgresRow{err: pgx.ErrNoRows}},
			}
			store := &PostgresStore{operationTimeouts: defaultOperationTimeouts, conversationPostgres: backend}
			stored, err := store.AddMessage(t.Context(), message)

			if testCase.execError == nil {
				if err != nil || stored.ID != message.ID || tx.commits != 1 || tx.rollbacks != 0 {
					t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, tx.commits, tx.rollbacks)
				}
			} else if stored.ID != "" || StoreErrorCodeOf(err) != testCase.wantCode || !errors.Is(err, testCase.execError) || tx.commits != 0 || tx.rollbacks != 1 {
				t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, tx.commits, tx.rollbacks)
			}
			if len(tx.execSQL) != 2 || !strings.Contains(tx.execSQL[0], "UPDATE sessions") || !strings.Contains(tx.execSQL[1], "INSERT INTO events") || session.releases != 1 || session.terminates != 0 {
				t.Fatalf("exec=%#v releases=%d terminates=%d", tx.execSQL, session.releases, session.terminates)
			}
		})
	}
}

func TestPostgresConversationReadsPropagateBackendErrors(t *testing.T) {
	sentinel := errors.New("postgres read failed")
	for _, testCase := range []struct {
		name    string
		backend *fakeConversationPostgresOps
		want    StoreErrorCode
	}{
		{
			name:    "query error",
			backend: &fakeConversationPostgresOps{queryErr: sentinel},
			want:    StoreErrorUnavailable,
		},
		{
			name:    "rows error",
			backend: &fakeConversationPostgresOps{rows: &fakeConversationPostgresRows{err: sentinel}},
			want:    StoreErrorUnavailable,
		},
		{
			name: "corrupt message JSON",
			backend: &fakeConversationPostgresOps{rows: &fakeConversationPostgresRows{rows: []fakeConversationPostgresRow{
				fakeConversationMessageRow(app.Message{ID: "message-corrupt"}, []byte(`{`), nil),
			}}},
			want: StoreErrorCorrupt,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &PostgresStore{operationTimeouts: defaultOperationTimeouts, conversationPostgres: testCase.backend}
			messages, err := store.ListMessages(t.Context(), "session")
			if messages != nil || StoreErrorCodeOf(err) != testCase.want {
				t.Fatalf("messages=%#v err=%v code=%q", messages, err, StoreErrorCodeOf(err))
			}
			if testCase.want == StoreErrorUnavailable && !errors.Is(err, sentinel) {
				t.Fatalf("backend cause was not preserved: %v", err)
			}
		})
	}
}
