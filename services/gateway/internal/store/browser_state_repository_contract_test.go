package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func TestBrowserStateRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend
			var restart func() testBackend
			switch backend {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				restart = func() testBackend {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			exerciseBrowserStateRepositoryContract(t, repository, restart)
		})
	}
}

func exerciseBrowserStateRepositoryContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	expiresAt := time.Date(2026, 9, 21, 12, 0, 0, 123456789, time.FixedZone("contract", 8*60*60))
	authB, err := repository.SaveBrowserAuthRecord(t.Context(), app.BrowserAuthRecord{
		ID: " auth-b ", OwnerID: " owner-browser ", BrowserProfileID: " profile-browser ",
		SiteOrigin: "HTTPS://EXAMPLE.COM/", AccountHint: " USER@EXAMPLE.COM ", ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if authB.ID != "auth-b" || authB.OwnerID != "owner-browser" || authB.BrowserProfileID != "profile-browser" ||
		authB.SiteOrigin != "https://example.com" || authB.AccountHint != "user@example.com" ||
		authB.ExpiresAt == nil || authB.ExpiresAt.Location() != time.UTC || authB.ExpiresAt.Nanosecond() != 123456000 ||
		authB.CreatedAt.Location() != time.UTC || authB.UpdatedAt.Location() != time.UTC {
		t.Fatalf("normalized browser auth = %#v", authB)
	}
	*authB.ExpiresAt = authB.ExpiresAt.Add(time.Hour)
	expiresAt = expiresAt.Add(2 * time.Hour)
	if stored, found, err := repository.GetBrowserAuthRecord(t.Context(), "auth-b"); err != nil || !found || stored.ExpiresAt == nil || stored.ExpiresAt.Hour() != 4 {
		t.Fatalf("browser auth alias isolation = %#v found=%t err=%v", stored, found, err)
	}
	authRewrite := authB
	authRewrite.CreatedAt = authB.CreatedAt.Add(24 * time.Hour)
	authB, err = repository.SaveBrowserAuthRecord(t.Context(), authRewrite)
	if err != nil || !authB.CreatedAt.Equal(authRewrite.CreatedAt.Add(-24*time.Hour)) {
		t.Fatalf("browser auth creation time changed: %#v err=%v", authB, err)
	}
	authA, err := repository.SaveBrowserAuthRecord(t.Context(), app.BrowserAuthRecord{
		ID: "auth-a", OwnerID: "owner-browser", BrowserProfileID: "profile-browser",
		SiteOrigin: "https://example.com", AccountHint: "user@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantNewestAuth := authA.ID
	if authA.UpdatedAt.Equal(authB.UpdatedAt) {
		wantNewestAuth = "auth-a"
	}
	if found, ok, err := repository.FindBrowserAuthRecord(t.Context(), "owner-browser", "profile-browser", "https://example.com/", "", "USER@EXAMPLE.COM"); err != nil || !ok || found.ID != wantNewestAuth {
		t.Fatalf("browser auth lookup = %#v found=%t err=%v", found, ok, err)
	}
	if _, ok, err := repository.FindBrowserAuthRecord(t.Context(), "other-owner", "profile-browser", "https://example.com", "", "user@example.com"); err != nil || ok {
		t.Fatalf("browser auth crossed owner scope: found=%t err=%v", ok, err)
	}
	authRecords, err := repository.ListBrowserAuthRecords(t.Context(), "owner-browser", "profile-browser")
	if err != nil || len(authRecords) != 2 {
		t.Fatalf("browser auth list = %#v err=%v", authRecords, err)
	}
	if authRecords[0].UpdatedAt.Equal(authRecords[1].UpdatedAt) && authRecords[0].ID != "auth-a" {
		t.Fatalf("browser auth tie order = %#v", authRecords)
	}
	if _, err := repository.RevokeBrowserAuthRecord(t.Context(), "missing-auth", "missing"); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("missing revoke code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	revoked, err := repository.RevokeBrowserAuthRecord(t.Context(), wantNewestAuth, "owner requested")
	if err != nil || revoked.Status != app.BrowserAuthStatusRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked auth = %#v err=%v", revoked, err)
	}
	if found, ok, err := repository.FindBrowserAuthRecord(t.Context(), "owner-browser", "profile-browser", "https://example.com", "", "user@example.com"); err != nil || !ok || found.ID == revoked.ID {
		t.Fatalf("revoked auth remained active: %#v found=%t err=%v", found, ok, err)
	}

	leaseUntil := time.Date(2026, 8, 21, 13, 0, 0, 987654321, time.FixedZone("contract", 8*60*60))
	blockInput := app.BrowserLoginBlock{
		ID: " block-b ", SessionID: " session-browser ", RunID: "run-browser", Status: app.BrowserHandoffStatusWaitingOwner,
		ResumeArgs: map[string]any{"nested": map[string]any{"value": "original"}}, TransitionLeaseUntil: &leaseUntil,
		VisibleEvidence: &app.BrowserResultEvidence{GoalEvidenceRefs: []string{"goal-original"}, SourceToolCallIDs: []string{"tool-original"}},
	}
	blockB, err := repository.SaveBrowserLoginBlock(t.Context(), blockInput)
	if err != nil {
		t.Fatal(err)
	}
	if blockB.ID != "block-b" || blockB.Version != 1 || blockB.SchemaVersion != app.BrowserHandoffSchemaVersion ||
		blockB.ResumeTool != "browser.read" || blockB.TransitionLeaseUntil != nil || blockB.CreatedAt.Location() != time.UTC {
		t.Fatalf("normalized browser block = %#v", blockB)
	}
	blockInput.ResumeArgs["nested"].(map[string]any)["value"] = "mutated-input"
	blockInput.VisibleEvidence.GoalEvidenceRefs[0] = "mutated-input"
	blockB.ResumeArgs["nested"].(map[string]any)["value"] = "mutated-output"
	blockB.VisibleEvidence.SourceToolCallIDs[0] = "mutated-output"
	storedBlock, found, err := repository.GetBrowserLoginBlock(t.Context(), blockB.ID)
	if err != nil || !found || storedBlock.ResumeArgs["nested"].(map[string]any)["value"] != "original" ||
		storedBlock.VisibleEvidence.GoalEvidenceRefs[0] != "goal-original" || storedBlock.VisibleEvidence.SourceToolCallIDs[0] != "tool-original" {
		t.Fatalf("browser block alias isolation = %#v found=%t err=%v", storedBlock, found, err)
	}
	blockA, err := repository.SaveBrowserLoginBlock(t.Context(), app.BrowserLoginBlock{
		ID: "block-a", SessionID: "session-browser", RunID: "run-a", Status: app.BrowserHandoffStatusWaitingOwner,
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := repository.ListBrowserLoginBlocks(t.Context(), "session-browser", app.BrowserHandoffStatusWaitingOwner)
	if err != nil || len(blocks) != 2 {
		t.Fatalf("browser block list = %#v err=%v", blocks, err)
	}
	wantActiveBlock := blockA.ID
	if blockA.UpdatedAt.Equal(storedBlock.UpdatedAt) {
		wantActiveBlock = "block-b"
	}
	if active, ok, err := repository.FindActiveBrowserLoginBlock(t.Context(), "session-browser"); err != nil || !ok || active.ID != wantActiveBlock {
		t.Fatalf("active browser block = %#v found=%t err=%v", active, ok, err)
	}
	updatedInput := storedBlock
	updatedInput.Status = app.BrowserHandoffStatusValidatingVisible
	updatedInput.ResumeArgs["state"] = "updated"
	updatedInput.CreatedAt = storedBlock.CreatedAt.Add(24 * time.Hour)
	updated, err := repository.UpdateBrowserLoginBlock(t.Context(), updatedInput, storedBlock.Version)
	if err != nil || updated.Version != storedBlock.Version+1 || updated.Status != app.BrowserHandoffStatusValidatingVisible || !updated.CreatedAt.Equal(storedBlock.CreatedAt) {
		t.Fatalf("updated browser block = %#v err=%v", updated, err)
	}
	if _, err := repository.UpdateBrowserLoginBlock(t.Context(), updatedInput, storedBlock.Version); !errors.Is(err, ErrBrowserHandoffConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("stale browser block code=%q err=%v", StoreErrorCodeOf(err), err)
	}

	audits, err := repository.ListAudit(t.Context(), "session-browser")
	if err != nil || !hasAuditType(audits, "browser_login_block."+app.BrowserHandoffStatusValidatingVisible) {
		t.Fatalf("browser lifecycle audits = %#v err=%v", audits, err)
	}
	events, err := repository.EventsAfter(t.Context(), "session-browser", "")
	if err != nil || !hasEventType(events, "browser_login_block."+app.BrowserHandoffStatusValidatingVisible) {
		t.Fatalf("browser lifecycle events = %#v err=%v", events, err)
	}

	if restart != nil {
		repository = restart()
		persisted, found, err := repository.GetBrowserLoginBlock(t.Context(), updated.ID)
		if err != nil || !found || persisted.Version != updated.Version || persisted.ResumeArgs["state"] != "updated" {
			t.Fatalf("browser restart = %#v found=%t err=%v", persisted, found, err)
		}
		persistedAuth, found, err := repository.GetBrowserAuthRecord(t.Context(), revoked.ID)
		if err != nil || !found || persistedAuth.Status != app.BrowserAuthStatusRevoked {
			t.Fatalf("browser auth restart = %#v found=%t err=%v", persistedAuth, found, err)
		}
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	checks := []func() error{
		func() error {
			_, err := repository.SaveBrowserAuthRecord(canceled, app.BrowserAuthRecord{})
			return err
		},
		func() error { _, _, err := repository.GetBrowserAuthRecord(canceled, "auth-a"); return err },
		func() error { _, _, err := repository.FindBrowserAuthRecord(canceled, "", "", "", "", ""); return err },
		func() error { _, err := repository.ListBrowserAuthRecords(canceled, "", ""); return err },
		func() error { _, err := repository.RevokeBrowserAuthRecord(canceled, "auth-a", ""); return err },
		func() error {
			_, err := repository.SaveBrowserLoginBlock(canceled, app.BrowserLoginBlock{})
			return err
		},
		func() error {
			_, err := repository.UpdateBrowserLoginBlock(canceled, app.BrowserLoginBlock{}, 1)
			return err
		},
		func() error { _, _, err := repository.GetBrowserLoginBlock(canceled, "block-a"); return err },
		func() error {
			_, _, err := repository.FindActiveBrowserLoginBlock(canceled, "session-browser")
			return err
		},
		func() error { _, err := repository.ListBrowserLoginBlocks(canceled, "", ""); return err },
	}
	for index, check := range checks {
		if err := check(); StoreErrorCodeOf(err) != StoreErrorCanceled {
			t.Fatalf("canceled browser operation %d code=%q err=%v", index, StoreErrorCodeOf(err), err)
		}
	}
}

type fakeBrowserStatePostgresRow struct {
	auth            app.BrowserAuthRecord
	block           app.BrowserLoginBlock
	resumeArgs      []byte
	target          []byte
	visibleEvidence []byte
	err             error
	kind            string
}

func browserAuthPostgresRow(record app.BrowserAuthRecord) fakeBrowserStatePostgresRow {
	return fakeBrowserStatePostgresRow{kind: "auth", auth: record}
}

func browserLoginBlockPostgresRow(block app.BrowserLoginBlock) fakeBrowserStatePostgresRow {
	return fakeBrowserStatePostgresRow{kind: "block", block: block, resumeArgs: []byte(`{}`), target: []byte(`{}`), visibleEvidence: []byte(`null`)}
}

func (r fakeBrowserStatePostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	switch r.kind {
	case "auth":
		if len(destinations) != 17 {
			return errors.New("unexpected browser auth row shape")
		}
		*(destinations[0].(*string)) = r.auth.ID
		*(destinations[1].(*string)) = r.auth.OwnerID
		*(destinations[2].(*string)) = r.auth.BrowserProfileID
		*(destinations[3].(*string)) = r.auth.SiteOrigin
		*(destinations[4].(*string)) = r.auth.SiteRealm
		*(destinations[5].(*string)) = r.auth.AccountHint
		*(destinations[6].(*string)) = r.auth.AuthStrategy
		*(destinations[7].(*string)) = r.auth.Status
		*(destinations[8].(*string)) = r.auth.SessionRef
		*(destinations[9].(*string)) = r.auth.CredentialRef
		*(destinations[10].(*string)) = r.auth.CookieJarRef
		if !r.auth.LastVerifiedAt.IsZero() {
			value := r.auth.LastVerifiedAt
			*(destinations[11].(**time.Time)) = &value
		}
		*(destinations[12].(**time.Time)) = cloneTimePointer(r.auth.ExpiresAt)
		*(destinations[13].(*string)) = r.auth.LastError
		*(destinations[14].(*time.Time)) = r.auth.CreatedAt
		*(destinations[15].(*time.Time)) = r.auth.UpdatedAt
		*(destinations[16].(**time.Time)) = cloneTimePointer(r.auth.RevokedAt)
		return nil
	case "block":
		if len(destinations) != 32 {
			return errors.New("unexpected browser login block row shape")
		}
		*(destinations[0].(*string)) = r.block.ID
		*(destinations[1].(*string)) = r.block.SessionID
		*(destinations[2].(*string)) = r.block.RunID
		*(destinations[3].(*int)) = r.block.SchemaVersion
		*(destinations[4].(*int64)) = r.block.Version
		*(destinations[5].(*app.WorkflowID)) = r.block.WorkflowID
		*(destinations[6].(*int)) = r.block.WorkflowRevision
		*(destinations[7].(*app.WorkflowNodeID)) = r.block.WorkflowNodeID
		*(destinations[8].(*uint64)) = r.block.SessionGeneration
		*(destinations[9].(*string)) = r.block.Status
		*(destinations[10].(*string)) = r.block.OriginalGoal
		*(destinations[11].(*string)) = r.block.ResumeTool
		*(destinations[12].(*[]byte)) = append([]byte(nil), r.resumeArgs...)
		*(destinations[13].(*string)) = r.block.LastToolCallID
		*(destinations[14].(*string)) = r.block.LoginHandoffURL
		*(destinations[15].(*string)) = r.block.LoginHandoffPageID
		*(destinations[16].(*string)) = r.block.LastVisiblePageID
		*(destinations[17].(*string)) = r.block.OwnerID
		*(destinations[18].(*string)) = r.block.BrowserProfileID
		*(destinations[19].(*string)) = r.block.SiteOrigin
		*(destinations[20].(*string)) = r.block.SiteRealm
		*(destinations[21].(*string)) = r.block.AccountHint
		*(destinations[22].(*string)) = r.block.BrowserAuthStatus
		*(destinations[23].(*[]byte)) = append([]byte(nil), r.target...)
		*(destinations[24].(*[]byte)) = append([]byte(nil), r.visibleEvidence...)
		*(destinations[25].(*string)) = r.block.LastUserReply
		*(destinations[26].(*string)) = r.block.LastError
		*(destinations[27].(*string)) = r.block.TransitionOwnerID
		*(destinations[28].(**time.Time)) = cloneTimePointer(r.block.TransitionLeaseUntil)
		*(destinations[29].(*time.Time)) = r.block.CreatedAt
		*(destinations[30].(*time.Time)) = r.block.UpdatedAt
		*(destinations[31].(**time.Time)) = cloneTimePointer(r.block.ResolvedAt)
		return nil
	default:
		return errors.New("unexpected browser state row kind")
	}
}

type fakeBrowserStatePostgresRows struct {
	rows  []fakeBrowserStatePostgresRow
	index int
	err   error
}

func (r *fakeBrowserStatePostgresRows) Next() bool { return r.index < len(r.rows) }
func (r *fakeBrowserStatePostgresRows) Scan(destinations ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}
func (r *fakeBrowserStatePostgresRows) Err() error { return r.err }
func (r *fakeBrowserStatePostgresRows) Close()     {}

func TestPostgresBrowserStateWritesUseAtomicLifecycleTransactions(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	currentAuth := app.BrowserAuthRecord{ID: "auth-postgres", OwnerID: app.DefaultOwnerID, BrowserProfileID: "default", Status: app.BrowserAuthStatusActive, CreatedAt: now, UpdatedAt: now}
	currentBlock := app.BrowserLoginBlock{ID: "block-postgres", Version: 2, SchemaVersion: app.BrowserHandoffSchemaVersion, Status: app.BrowserHandoffStatusWaitingOwner, ResumeArgs: map[string]any{}, CreatedAt: now, UpdatedAt: now}

	testCases := []struct {
		name string
		row  onboardingPostgresRow
		run  func(*PostgresStore) error
		main string
	}{
		{name: "save auth", row: fakeBrowserStatePostgresRow{err: pgx.ErrNoRows}, main: "INSERT INTO browser_auth_records", run: func(repository *PostgresStore) error {
			_, err := repository.SaveBrowserAuthRecord(t.Context(), currentAuth)
			return err
		}},
		{name: "revoke auth", row: browserAuthPostgresRow(currentAuth), main: "UPDATE browser_auth_records", run: func(repository *PostgresStore) error {
			_, err := repository.RevokeBrowserAuthRecord(t.Context(), currentAuth.ID, "owner requested")
			return err
		}},
		{name: "save block", row: fakeBrowserStatePostgresRow{err: pgx.ErrNoRows}, main: "INSERT INTO browser_login_blocks", run: func(repository *PostgresStore) error {
			_, err := repository.SaveBrowserLoginBlock(t.Context(), currentBlock)
			return err
		}},
		{name: "update block", row: browserLoginBlockPostgresRow(currentBlock), main: "UPDATE browser_login_blocks", run: func(repository *PostgresStore) error {
			_, err := repository.UpdateBrowserLoginBlock(t.Context(), currentBlock, currentBlock.Version)
			return err
		}},
	}
	for _, testCase := range testCases {
		for _, failure := range []bool{false, true} {
			name := "commit"
			if failure {
				name = "event failure rolls back"
			}
			t.Run(testCase.name+"/"+name, func(t *testing.T) {
				sentinel := errors.New("browser lifecycle failed")
				transaction := &fakeConversationPostgresTx{rowQueue: []onboardingPostgresRow{testCase.row}, execErrors: map[int]error{}}
				if failure {
					transaction.execErrors[2] = sentinel
				}
				session := &fakeConversationPostgresSession{transaction: transaction}
				repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, browserStatePostgres: &fakeConversationPostgresOps{session: session}}
				err := testCase.run(repository)
				if !failure && (err != nil || transaction.commits != 1 || transaction.rollbacks != 0) {
					t.Fatalf("err=%v commits=%d rollbacks=%d", err, transaction.commits, transaction.rollbacks)
				}
				if failure && (StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) || transaction.commits != 0 || transaction.rollbacks != 1) {
					t.Fatalf("code=%q err=%v commits=%d rollbacks=%d", StoreErrorCodeOf(err), err, transaction.commits, transaction.rollbacks)
				}
				if len(transaction.execSQL) != 3 || !strings.Contains(transaction.execSQL[0], testCase.main) ||
					!strings.Contains(transaction.execSQL[1], "INSERT INTO audit_events") || !strings.Contains(transaction.execSQL[2], "INSERT INTO events") ||
					session.releases != 1 || session.terminates != 0 {
					t.Fatalf("exec=%#v releases=%d terminates=%d", transaction.execSQL, session.releases, session.terminates)
				}
			})
		}
	}

	transaction := &fakeConversationPostgresTx{rowQueue: []onboardingPostgresRow{browserLoginBlockPostgresRow(currentBlock)}, execErrors: map[int]error{}}
	session := &fakeConversationPostgresSession{transaction: transaction}
	repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, browserStatePostgres: &fakeConversationPostgresOps{session: session}}
	if _, err := repository.UpdateBrowserLoginBlock(t.Context(), currentBlock, currentBlock.Version-1); !errors.Is(err, ErrBrowserHandoffConflict) || StoreErrorCodeOf(err) != StoreErrorConflict || transaction.rollbacks != 1 || len(transaction.execSQL) != 0 {
		t.Fatalf("conflict code=%q err=%v rollbacks=%d exec=%#v", StoreErrorCodeOf(err), err, transaction.rollbacks, transaction.execSQL)
	}
}

func TestPostgresBrowserStateReadsPropagateBackendAndDecodeErrors(t *testing.T) {
	sentinel := errors.New("browser state read failed")
	missing := &PostgresStore{operationTimeouts: defaultOperationTimeouts, browserStatePostgres: &fakeConversationPostgresOps{rowQueue: []onboardingPostgresRow{fakeBrowserStatePostgresRow{err: pgx.ErrNoRows}, fakeBrowserStatePostgresRow{err: pgx.ErrNoRows}}}}
	if _, found, err := missing.GetBrowserAuthRecord(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing auth found=%t err=%v", found, err)
	}
	if _, found, err := missing.GetBrowserLoginBlock(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing block found=%t err=%v", found, err)
	}

	authRow := browserAuthPostgresRow(app.BrowserAuthRecord{ID: "auth", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	for _, testCase := range []struct {
		name    string
		backend *fakeConversationPostgresOps
	}{
		{name: "auth query", backend: &fakeConversationPostgresOps{queryErr: sentinel}},
		{name: "auth scan", backend: &fakeConversationPostgresOps{rows: &fakeBrowserStatePostgresRows{rows: []fakeBrowserStatePostgresRow{{err: sentinel}}}}},
		{name: "auth rows", backend: &fakeConversationPostgresOps{rows: &fakeBrowserStatePostgresRows{rows: []fakeBrowserStatePostgresRow{authRow}, err: sentinel}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, browserStatePostgres: testCase.backend}
			records, err := repository.ListBrowserAuthRecords(t.Context(), "", "")
			if records != nil || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) {
				t.Fatalf("records=%#v code=%q err=%v", records, StoreErrorCodeOf(err), err)
			}
		})
	}

	validBlock := app.BrowserLoginBlock{ID: "block", Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, testCase := range []struct {
		name    string
		backend *fakeConversationPostgresOps
		code    StoreErrorCode
	}{
		{name: "block query", backend: &fakeConversationPostgresOps{queryErr: sentinel}, code: StoreErrorUnavailable},
		{name: "block scan", backend: &fakeConversationPostgresOps{rows: &fakeBrowserStatePostgresRows{rows: []fakeBrowserStatePostgresRow{{err: sentinel}}}}, code: StoreErrorUnavailable},
		{name: "block rows", backend: &fakeConversationPostgresOps{rows: &fakeBrowserStatePostgresRows{rows: []fakeBrowserStatePostgresRow{browserLoginBlockPostgresRow(validBlock)}, err: sentinel}}, code: StoreErrorUnavailable},
		{name: "corrupt resume args", backend: &fakeConversationPostgresOps{rows: &fakeBrowserStatePostgresRows{rows: []fakeBrowserStatePostgresRow{{kind: "block", block: validBlock, resumeArgs: []byte(`{`), target: []byte(`{}`), visibleEvidence: []byte(`null`)}}}}, code: StoreErrorCorrupt},
		{name: "corrupt target", backend: &fakeConversationPostgresOps{rows: &fakeBrowserStatePostgresRows{rows: []fakeBrowserStatePostgresRow{{kind: "block", block: validBlock, resumeArgs: []byte(`{}`), target: []byte(`{`), visibleEvidence: []byte(`null`)}}}}, code: StoreErrorCorrupt},
		{name: "corrupt visible evidence", backend: &fakeConversationPostgresOps{rows: &fakeBrowserStatePostgresRows{rows: []fakeBrowserStatePostgresRow{{kind: "block", block: validBlock, resumeArgs: []byte(`{}`), target: []byte(`{}`), visibleEvidence: []byte(`{`)}}}}, code: StoreErrorCorrupt},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, browserStatePostgres: testCase.backend}
			blocks, err := repository.ListBrowserLoginBlocks(t.Context(), "", "")
			if blocks != nil || StoreErrorCodeOf(err) != testCase.code {
				t.Fatalf("blocks=%#v code=%q err=%v", blocks, StoreErrorCodeOf(err), err)
			}
			if testCase.code == StoreErrorUnavailable && !errors.Is(err, sentinel) {
				t.Fatalf("backend cause was not preserved: %v", err)
			}
		})
	}
}

func TestPostgresBrowserStateRepositoryConfiguredContract(t *testing.T) {
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
	exerciseBrowserStateRepositoryContract(t, repository, nil)
}
