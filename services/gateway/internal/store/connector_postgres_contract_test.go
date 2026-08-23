package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/semaphore"
)

type fakeConnectorPostgresRow struct {
	scan func(...any) error
}

func (r fakeConnectorPostgresRow) Scan(destinations ...any) error {
	if r.scan == nil {
		return pgx.ErrNoRows
	}
	return r.scan(destinations...)
}

type fakeConnectorPostgresRows struct {
	rows    []fakeConnectorPostgresRow
	index   int
	scanErr error
	err     error
	closed  bool
}

func (r *fakeConnectorPostgresRows) Next() bool { return r.index < len(r.rows) }

func (r *fakeConnectorPostgresRows) Scan(destinations ...any) error {
	if r.index >= len(r.rows) {
		return errors.New("fake connector row is exhausted")
	}
	row := r.rows[r.index]
	r.index++
	if r.scanErr != nil {
		return r.scanErr
	}
	return row.Scan(destinations...)
}

func (r *fakeConnectorPostgresRows) Err() error { return r.err }
func (r *fakeConnectorPostgresRows) Close()     { r.closed = true }

type fakeConnectorRowsResult struct {
	rows onboardingPostgresRows
	err  error
}

type fakeConnectorPostgresOps struct {
	session      *fakeConnectorPostgresSession
	acquireErr   error
	queryResults []fakeConnectorRowsResult
	querySQL     []string
}

func (o *fakeConnectorPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	if o.acquireErr != nil {
		return nil, o.acquireErr
	}
	return o.session, nil
}

func (*fakeConnectorPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (o *fakeConnectorPostgresOps) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	o.querySQL = append(o.querySQL, sql)
	if len(o.queryResults) == 0 {
		return &fakeConnectorPostgresRows{}, nil
	}
	result := o.queryResults[0]
	o.queryResults = o.queryResults[1:]
	return result.rows, result.err
}

func (*fakeConnectorPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	return fakeConnectorPostgresRow{}
}

type fakeConnectorPostgresSession struct {
	transaction  *fakeConnectorPostgresTx
	beginErr     error
	options      []pgx.TxOptions
	releases     int
	terminates   int
	terminateErr error
}

func (s *fakeConnectorPostgresSession) Begin(_ context.Context, options pgx.TxOptions) (onboardingPostgresTx, error) {
	s.options = append(s.options, options)
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return s.transaction, nil
}

func (s *fakeConnectorPostgresSession) Release() { s.releases++ }

func (s *fakeConnectorPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return s.terminateErr
}

type fakeConnectorPostgresTx struct {
	execErrors  map[int]error
	execTags    map[int]pgconn.CommandTag
	execSQL     []string
	execArgs    [][]any
	rowQueue    []onboardingPostgresRow
	rowSQL      []string
	rowsQueue   []fakeConnectorRowsResult
	querySQL    []string
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (t *fakeConnectorPostgresTx) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	index := len(t.execSQL)
	t.execSQL = append(t.execSQL, sql)
	t.execArgs = append(t.execArgs, append([]any(nil), arguments...))
	if err := t.execErrors[index]; err != nil {
		return pgconn.CommandTag{}, err
	}
	if tag, ok := t.execTags[index]; ok {
		return tag, nil
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (t *fakeConnectorPostgresTx) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	t.rowSQL = append(t.rowSQL, sql)
	if len(t.rowQueue) == 0 {
		return fakeConnectorPostgresRow{}
	}
	row := t.rowQueue[0]
	t.rowQueue = t.rowQueue[1:]
	return row
}

func (t *fakeConnectorPostgresTx) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	t.querySQL = append(t.querySQL, sql)
	if len(t.rowsQueue) == 0 {
		return &fakeConnectorPostgresRows{}, nil
	}
	result := t.rowsQueue[0]
	t.rowsQueue = t.rowsQueue[1:]
	return result.rows, result.err
}

func (t *fakeConnectorPostgresTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeConnectorPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return t.rollbackErr
}

func newFakePostgresConnectorStore(transaction *fakeConnectorPostgresTx) (*PostgresStore, *fakeConnectorPostgresOps, *fakeConnectorPostgresSession) {
	session := &fakeConnectorPostgresSession{transaction: transaction}
	operations := &fakeConnectorPostgresOps{session: session}
	return &PostgresStore{
		operationTimeouts: defaultOperationTimeouts, connectorPostgres: operations,
		connectorCommandGate: semaphore.NewWeighted(1), connectorSettingWriteHighWater: map[string]time.Time{},
		notificationBindingWriteHighWater: map[string]time.Time{}, connectorNow: time.Now,
	}, operations, session
}

func connectorSettingPostgresRow(setting app.ConnectorSetting) fakeConnectorPostgresRow {
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		*(destinations[0].(*string)) = setting.OwnerID
		*(destinations[1].(*string)) = setting.Channel
		*(destinations[2].(*bool)) = setting.Enabled
		*(destinations[3].(*bool)) = setting.ISCPEnabled
		*(destinations[4].(*bool)) = setting.LANAccessEnabled
		*(destinations[5].(*int64)) = setting.Version
		*(destinations[6].(*string)) = setting.UpdatedBy
		*(destinations[7].(*time.Time)) = setting.UpdatedAt
		return nil
	}}
}

func notificationBindingPostgresRow(binding app.NotificationBinding) fakeConnectorPostgresRow {
	scopes, err := json.Marshal(binding.Scopes)
	if err != nil {
		panic(err)
	}
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		stringsOut := []string{
			binding.ID, binding.OwnerID, binding.ActorID, binding.Channel, binding.Provider, binding.Status,
			binding.DisplayName, binding.ExternalUserID, binding.ExternalChatID, binding.ExternalThreadID,
			binding.AccountID, binding.CredentialRef, binding.BaseURL, binding.ProviderSessionID,
			binding.ProviderState, binding.ContextToken, binding.ProviderCursor, binding.QRCodeURL,
			binding.QRCodeImage,
		}
		for index, value := range stringsOut {
			*(destinations[index].(*string)) = value
		}
		*(destinations[19].(*bool)) = binding.DefaultForChannel
		*(destinations[20].(*[]byte)) = append([]byte(nil), scopes...)
		*(destinations[21].(*time.Time)) = binding.CreatedAt
		*(destinations[22].(*time.Time)) = binding.UpdatedAt
		*(destinations[23].(**time.Time)) = cloneTimePointer(binding.ExpiresAt)
		*(destinations[24].(**time.Time)) = cloneTimePointer(binding.RevokedAt)
		*(destinations[25].(*string)) = binding.LastError
		*(destinations[26].(*int64)) = binding.Version
		*(destinations[27].(*string)) = binding.CredentialKind
		return nil
	}}
}

func testPostgresStartingBinding(id string, now time.Time) app.NotificationBinding {
	return app.NotificationBinding{
		ID: id, OwnerID: "owner", ActorID: "actor", Channel: "telegram", Provider: "telegram-bot-api",
		Status: app.NotificationBindingStarting, Scopes: app.DefaultMessagingBindingScopes(),
		CredentialKind: "bot-token", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func TestPostgresConnectorSettingWriteFaultMatrix(t *testing.T) {
	now := time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)
	t.Run("success is atomic", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{}
		st, _, session := newFakePostgresConnectorStore(tx)
		st.connectorNow = func() time.Time { return now }
		setting, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha", Enabled: true}, 0)
		if err != nil || setting.Version != 1 || !setting.UpdatedAt.Equal(now) || tx.commits != 1 || tx.rollbacks != 0 || session.releases != 1 || session.terminates != 0 {
			t.Fatalf("setting=%#v err=%v commit=%d rollback=%d release=%d terminate=%d", setting, err, tx.commits, tx.rollbacks, session.releases, session.terminates)
		}
		joined := strings.Join(tx.execSQL, "\n")
		for _, required := range []string{"pg_advisory_xact_lock", "connector_settings", "audit_events", "events"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("atomic setting transaction omitted %q: %v", required, tx.execSQL)
			}
		}
	})

	for index, name := range []string{"barrier", "mutation", "audit", "event"} {
		t.Run("unsafe "+name, func(t *testing.T) {
			tx := &fakeConnectorPostgresTx{execErrors: map[int]error{index: errors.New("submission uncertain")}}
			st, _, session := newFakePostgresConnectorStore(tx)
			candidate, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
			wantCandidate := index > 0
			if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || (candidate.Channel != "") != wantCandidate || session.terminates != 1 || session.releases != 0 || tx.rollbacks != 0 {
				t.Fatalf("candidate=%#v err=%v code=%q terminate=%d release=%d rollback=%d", candidate, err, StoreErrorCodeOf(err), session.terminates, session.releases, tx.rollbacks)
			}
		})
	}

	t.Run("safe statement rolls back and releases", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{execErrors: map[int]error{1: safePostgresRetryError{errors.New("not sent")}}}
		st, _, session := newFakePostgresConnectorStore(tx)
		candidate, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
		if candidate.Channel != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
			t.Fatalf("candidate=%#v err=%v code=%q rollback=%d release=%d terminate=%d", candidate, err, StoreErrorCodeOf(err), tx.rollbacks, session.releases, session.terminates)
		}
	})

	t.Run("rows affected conflict", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{execTags: map[int]pgconn.CommandTag{1: pgconn.NewCommandTag("UPDATE 0")}}
		st, _, session := newFakePostgresConnectorStore(tx)
		candidate, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
		if candidate.Channel != "" || StoreErrorCodeOf(err) != StoreErrorConflict || tx.rollbacks != 1 || session.releases != 1 {
			t.Fatalf("candidate=%#v err=%v code=%q rollback=%d release=%d", candidate, err, StoreErrorCodeOf(err), tx.rollbacks, session.releases)
		}
	})

	t.Run("commit uncertainty returns exact candidate", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{commitErr: errors.New("commit uncertain")}
		st, _, session := newFakePostgresConnectorStore(tx)
		candidate, err := st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
		if candidate.Channel != "alpha" || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || session.terminates != 1 || session.releases != 0 {
			t.Fatalf("candidate=%#v err=%v code=%q terminate=%d release=%d", candidate, err, StoreErrorCodeOf(err), session.terminates, session.releases)
		}
	})
}

func TestPostgresConnectorBindingWriteFaultMatrix(t *testing.T) {
	now := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	request := app.NotificationBinding{
		ID: "binding-create", OwnerID: "owner", ActorID: "actor", Channel: "telegram",
		Provider: "telegram-bot-api", Status: app.NotificationBindingStarting, CredentialKind: "bot-token",
	}
	t.Run("create is atomic behind both barriers", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{}
		st, _, session := newFakePostgresConnectorStore(tx)
		st.connectorNow = func() time.Time { return now }
		created, err := st.CreateNotificationBinding(t.Context(), request)
		if err != nil || created.Version != 1 || len(tx.execSQL) != 5 || tx.commits != 1 || session.releases != 1 {
			t.Fatalf("created=%#v err=%v exec=%v commit=%d release=%d", created, err, tx.execSQL, tx.commits, session.releases)
		}
		if !strings.Contains(tx.execSQL[0], "pg_advisory_xact_lock") || !strings.Contains(tx.execSQL[1], "pg_advisory_xact_lock") || !strings.Contains(tx.execSQL[2], "notification_bindings") || !strings.Contains(tx.execSQL[3], "audit_events") || !strings.Contains(tx.execSQL[4], "events") {
			t.Fatalf("binding create statement order=%v", tx.execSQL)
		}
	})

	for index, name := range []string{"owner barrier", "binding barrier", "mutation", "audit", "event"} {
		t.Run("unsafe "+name, func(t *testing.T) {
			tx := &fakeConnectorPostgresTx{execErrors: map[int]error{index: errors.New("submission uncertain")}}
			st, _, session := newFakePostgresConnectorStore(tx)
			candidate, err := st.CreateNotificationBinding(t.Context(), request)
			wantCandidate := index >= 2
			if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || (candidate.ID != "") != wantCandidate || session.terminates != 1 || session.releases != 0 {
				t.Fatalf("candidate=%#v err=%v code=%q terminate=%d release=%d", candidate, err, StoreErrorCodeOf(err), session.terminates, session.releases)
			}
		})
	}

	t.Run("update locks ref and commits lifecycle", func(t *testing.T) {
		previous := testPostgresStartingBinding("binding-update", now.Add(-time.Minute))
		next := previous
		next.Status = app.NotificationBindingActive
		next.CredentialRef = "cred_binding-update"
		next.DefaultForChannel = true
		tx := &fakeConnectorPostgresTx{
			rowQueue:  []onboardingPostgresRow{notificationBindingPostgresRow(previous), fakeConnectorPostgresRow{}},
			rowsQueue: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{}}},
		}
		st, _, session := newFakePostgresConnectorStore(tx)
		st.connectorNow = func() time.Time { return now }
		updated, err := st.UpdateNotificationBinding(t.Context(), NewNotificationBindingUpdate(previous, next))
		if err != nil || updated.Status != app.NotificationBindingActive || updated.Version != 2 || tx.commits != 1 || session.releases != 1 {
			t.Fatalf("updated=%#v err=%v commit=%d release=%d", updated, err, tx.commits, session.releases)
		}
		if len(tx.execSQL) != 6 || !strings.Contains(tx.execSQL[2], "pg_advisory_xact_lock") || len(tx.rowSQL) != 2 || !strings.Contains(tx.rowSQL[1], "credential_ref") {
			t.Fatalf("binding update barriers=%v rows=%v", tx.execSQL, tx.rowSQL)
		}
	})
}

func TestPostgresConnectorReadContracts(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	setting := app.ConnectorSetting{OwnerID: "owner", Channel: "alpha", Enabled: true, Version: 1, UpdatedBy: "owner", UpdatedAt: now}
	tx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{connectorSettingPostgresRow(setting)}}
	st, operations, session := newFakePostgresConnectorStore(tx)
	got, found, err := st.GetConnectorSetting(t.Context(), setting.OwnerID, setting.Channel)
	if err != nil || !found || got != setting || len(session.options) != 1 || session.options[0].IsoLevel != pgx.ReadCommitted || tx.commits != 1 || len(tx.execSQL) != 1 || len(tx.rowSQL) != 1 {
		t.Fatalf("setting=%#v found=%v err=%v options=%#v exec=%v rows=%v", got, found, err, session.options, tx.execSQL, tx.rowSQL)
	}
	if strings.Contains(strings.Join(tx.execSQL, "\n"), "INSERT") || strings.Contains(strings.Join(tx.execSQL, "\n"), "UPDATE") {
		t.Fatalf("connector GET issued a write: %v", tx.execSQL)
	}

	rowsFailure := errors.New("rows failed")
	operations.queryResults = []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{err: rowsFailure}}}
	if listed, err := st.ListConnectorSettings(t.Context(), "owner"); listed != nil || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, rowsFailure) {
		t.Fatalf("list=%#v err=%v code=%q", listed, err, StoreErrorCodeOf(err))
	}

	badScopes := notificationBindingPostgresRow(testPostgresStartingBinding("binding-bad-scopes", now))
	badScopes.scan = func(destinations ...any) error {
		valid := notificationBindingPostgresRow(testPostgresStartingBinding("binding-bad-scopes", now))
		if err := valid.Scan(destinations...); err != nil {
			return err
		}
		*(destinations[20].(*[]byte)) = []byte("not-json")
		return nil
	}
	operations.queryResults = []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{badScopes}}}}
	if listed, err := st.ListNotificationBindings(t.Context(), "", ""); listed != nil || StoreErrorCodeOf(err) != StoreErrorCorrupt {
		t.Fatalf("corrupt list=%#v err=%v code=%q", listed, err, StoreErrorCodeOf(err))
	}

	getTx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{badScopes}}
	getStore, _, getSession := newFakePostgresConnectorStore(getTx)
	if binding, found, err := getStore.GetNotificationBinding(t.Context(), "binding-bad-scopes"); binding.ID != "" || found || StoreErrorCodeOf(err) != StoreErrorCorrupt || getTx.rollbacks != 1 || getSession.releases != 1 || getSession.terminates != 0 {
		t.Fatalf("corrupt get=%#v found=%v err=%v code=%q rollback=%d release=%d terminate=%d", binding, found, err, StoreErrorCodeOf(err), getTx.rollbacks, getSession.releases, getSession.terminates)
	}
}

func TestPostgresConnectorStartupRejectsGlobalInvariantViolations(t *testing.T) {
	now := time.Date(2026, 8, 21, 11, 0, 0, 0, time.UTC)
	first := testPostgresStartingBinding("binding-first", now)
	first.Status = app.NotificationBindingActive
	first.CredentialRef = "cred_duplicate"
	second := first
	second.ID = "binding-second"
	operations := &fakeConnectorPostgresOps{queryResults: []fakeConnectorRowsResult{
		{rows: &fakeConnectorPostgresRows{}},
		{rows: &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{notificationBindingPostgresRow(first), notificationBindingPostgresRow(second)}}},
	}}
	st := &PostgresStore{
		operationTimeouts: defaultOperationTimeouts, connectorPostgres: operations,
		connectorSettingWriteHighWater: map[string]time.Time{}, notificationBindingWriteHighWater: map[string]time.Time{},
	}
	if err := st.validateConnectorState(t.Context()); err == nil || !strings.Contains(err.Error(), "share Vault credential ref") {
		t.Fatalf("duplicate Vault ownership validation error=%v", err)
	}
}

func TestPostgresConnectorCommandAdmissionHonorsDeadline(t *testing.T) {
	st, _, _ := newFakePostgresConnectorStore(&fakeConnectorPostgresTx{})
	if err := st.connectorCommandGate.Acquire(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := st.UpdateConnectorSetting(ctx, app.ConnectorSetting{OwnerID: "owner", Channel: "alpha"}, 0)
		done <- err
	}()
	err := <-done
	st.connectorCommandGate.Release(1)
	if StoreErrorCodeOf(err) != StoreErrorTimeout {
		t.Fatalf("admission err=%v code=%q", err, StoreErrorCodeOf(err))
	}
}

func TestPostgresConnectorRealDatabaseConcurrency(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	firstStore, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer firstStore.Close()
	truncatePostgresStore(t, firstStore)
	secondStore, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()

	ownerID := app.NewID("owner-connector-race")
	stores := []*PostgresStore{firstStore, secondStore}

	t.Run("setting create has one winner", func(t *testing.T) {
		type result struct {
			setting app.ConnectorSetting
			err     error
		}
		results := make([]result, len(stores))
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index, st := range stores {
			wait.Add(1)
			go func(index int, st *PostgresStore) {
				defer wait.Done()
				<-start
				results[index].setting, results[index].err = st.UpdateConnectorSetting(t.Context(), app.ConnectorSetting{
					OwnerID: ownerID, Channel: "alpha", Enabled: true,
				}, 0)
			}(index, st)
		}
		close(start)
		wait.Wait()
		successes, conflicts := 0, 0
		for _, result := range results {
			switch StoreErrorCodeOf(result.err) {
			case "":
				successes++
			case StoreErrorConflict:
				conflicts++
			default:
				t.Fatalf("setting result=%#v err=%v", result.setting, result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("setting successes=%d conflicts=%d results=%#v", successes, conflicts, results)
		}
	})

	t.Run("binding ID has one winner", func(t *testing.T) {
		request := app.NotificationBinding{
			ID: app.NewID("binding-connector-race"), OwnerID: ownerID, ActorID: ownerID,
			Channel: "telegram", Provider: "telegram-bot-api", Status: app.NotificationBindingStarting,
			CredentialKind: "bot-token",
		}
		type result struct {
			binding app.NotificationBinding
			err     error
		}
		results := make([]result, len(stores))
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index, st := range stores {
			wait.Add(1)
			go func(index int, st *PostgresStore) {
				defer wait.Done()
				<-start
				results[index].binding, results[index].err = st.CreateNotificationBinding(t.Context(), request)
			}(index, st)
		}
		close(start)
		wait.Wait()
		successes, conflicts := 0, 0
		for _, result := range results {
			switch StoreErrorCodeOf(result.err) {
			case "":
				successes++
			case StoreErrorConflict:
				conflicts++
			default:
				t.Fatalf("binding result=%#v err=%v", result.binding, result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("binding successes=%d conflicts=%d results=%#v", successes, conflicts, results)
		}
	})

	createStarting := func(id string) app.NotificationBinding {
		t.Helper()
		created, err := firstStore.CreateNotificationBinding(t.Context(), app.NotificationBinding{
			ID: id, OwnerID: ownerID, ActorID: ownerID, Channel: "telegram", Provider: "telegram-bot-api",
			Status: app.NotificationBindingStarting, CredentialKind: "bot-token",
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}

	t.Run("concurrent defaults converge to one", func(t *testing.T) {
		left := createStarting(app.NewID("binding-default-left"))
		right := createStarting(app.NewID("binding-default-right"))
		leftNext := left
		leftNext.Status, leftNext.CredentialRef, leftNext.DefaultForChannel = app.NotificationBindingActive, "config:left", true
		rightNext := right
		rightNext.Status, rightNext.CredentialRef, rightNext.DefaultForChannel = app.NotificationBindingActive, "config:right", true
		commands := []NotificationBindingUpdateCommand{
			NewNotificationBindingUpdate(left, leftNext), NewNotificationBindingUpdate(right, rightNext),
		}
		errorsByIndex := make([]error, len(stores))
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index, st := range stores {
			wait.Add(1)
			go func(index int, st *PostgresStore) {
				defer wait.Done()
				<-start
				_, errorsByIndex[index] = st.UpdateNotificationBinding(t.Context(), commands[index])
			}(index, st)
		}
		close(start)
		wait.Wait()
		for _, err := range errorsByIndex {
			if err != nil {
				t.Fatal(err)
			}
		}
		bindings, err := firstStore.ListNotificationBindings(t.Context(), "telegram", app.NotificationBindingActive)
		if err != nil {
			t.Fatal(err)
		}
		defaults := 0
		for _, record := range bindings {
			if record.ID == left.ID || record.ID == right.ID {
				if record.DefaultForChannel {
					defaults++
				}
			}
		}
		if defaults != 1 {
			t.Fatalf("active defaults=%d bindings=%#v", defaults, bindings)
		}
	})

	t.Run("Vault ref and prior CAS each have one winner", func(t *testing.T) {
		left := createStarting(app.NewID("binding-ref-left"))
		right := createStarting(app.NewID("binding-ref-right"))
		sharedRef := "cred_" + app.NewID("connector-race-ref")
		leftNext := left
		leftNext.Status, leftNext.CredentialRef = app.NotificationBindingActive, sharedRef
		rightNext := right
		rightNext.Status, rightNext.CredentialRef = app.NotificationBindingActive, sharedRef
		commands := []NotificationBindingUpdateCommand{
			NewNotificationBindingUpdate(left, leftNext), NewNotificationBindingUpdate(right, rightNext),
		}
		type result struct {
			binding app.NotificationBinding
			err     error
		}
		results := make([]result, len(stores))
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index, st := range stores {
			wait.Add(1)
			go func(index int, st *PostgresStore) {
				defer wait.Done()
				<-start
				results[index].binding, results[index].err = st.UpdateNotificationBinding(t.Context(), commands[index])
			}(index, st)
		}
		close(start)
		wait.Wait()
		var winner app.NotificationBinding
		successes, conflicts := 0, 0
		for _, result := range results {
			switch StoreErrorCodeOf(result.err) {
			case "":
				successes++
				winner = result.binding
			case StoreErrorConflict:
				conflicts++
			default:
				t.Fatalf("ref result=%#v err=%v", result.binding, result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("ref successes=%d conflicts=%d results=%#v", successes, conflicts, results)
		}

		firstUpdate := winner
		firstUpdate.ProviderCursor = "10"
		secondUpdate := winner
		secondUpdate.ProviderCursor = "11"
		casCommands := []NotificationBindingUpdateCommand{
			NewNotificationBindingUpdate(winner, firstUpdate), NewNotificationBindingUpdate(winner, secondUpdate),
		}
		results = make([]result, len(stores))
		start = make(chan struct{})
		wait = sync.WaitGroup{}
		for index, st := range stores {
			wait.Add(1)
			go func(index int, st *PostgresStore) {
				defer wait.Done()
				<-start
				results[index].binding, results[index].err = st.UpdateNotificationBinding(t.Context(), casCommands[index])
			}(index, st)
		}
		close(start)
		wait.Wait()
		successes, conflicts = 0, 0
		for _, result := range results {
			switch StoreErrorCodeOf(result.err) {
			case "":
				successes++
			case StoreErrorConflict:
				conflicts++
			default:
				t.Fatalf("CAS result=%#v err=%v", result.binding, result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("CAS successes=%d conflicts=%d results=%#v", successes, conflicts, results)
		}
	})
}
