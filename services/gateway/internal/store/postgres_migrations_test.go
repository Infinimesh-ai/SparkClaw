package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMigrationManifest(t *testing.T) {
	migrations, err := loadPostgresMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 3 {
		t.Fatalf("migration count = %d, want 3", len(migrations))
	}
	wantNames := []string{"0001_core.sql", "0002_reconcile_current.sql", "0003_validate_legacy_chat_keys.sql"}
	for index, migration := range migrations {
		if migration.Version != index+1 || migration.Filename != wantNames[index] {
			t.Fatalf("migration %d = %#v", index, migration)
		}
		digest := sha256.Sum256([]byte(migration.SQL))
		if got := fmt.Sprintf("%x", digest); got != migration.Checksum {
			t.Fatalf("migration %s checksum = %s, want %s", migration.Filename, migration.Checksum, got)
		}
	}
	if got := migrations[0].Checksum; got != "d16479c0830460418d27d3595a513232a688cb8bc75173b53f2f7f068f6c5382" {
		t.Fatalf("0001 checksum = %s", got)
	}
}

func TestPostgresDomainDDLExistsOnlyInEmbeddedMigrations(t *testing.T) {
	postgresSource, err := os.ReadFile("postgres.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(string(postgresSource)), "CREATE TABLE") {
		t.Fatal("postgres.go still contains domain DDL")
	}
	runnerSource, err := os.ReadFile("postgres_migrations.go")
	if err != nil {
		t.Fatal(err)
	}
	upper := strings.ToUpper(string(runnerSource))
	if count := strings.Count(upper, "CREATE TABLE IF NOT EXISTS"); count != 1 {
		t.Fatalf("runner CREATE TABLE count = %d, want only the ledger bootstrap", count)
	}
	if !strings.Contains(string(runnerSource), "sparkclaw_schema_migrations") {
		t.Fatal("runner does not contain the migration ledger bootstrap")
	}
}

func TestPostgresMigrationFreshDatabaseAndRestart(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	st, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var firstApplied time.Time
	if err := st.db.QueryRow(context.Background(), `
SELECT count(*), min(applied_at) FROM sparkclaw_schema_migrations
`).Scan(&count, &firstApplied); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if count != 3 {
		st.Close()
		t.Fatalf("ledger count = %d, want 3", count)
	}
	st.Close()

	restarted, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	var secondApplied time.Time
	if err := restarted.db.QueryRow(context.Background(), `SELECT min(applied_at) FROM sparkclaw_schema_migrations`).Scan(&secondApplied); err != nil {
		t.Fatal(err)
	}
	if !secondApplied.Equal(firstApplied) {
		t.Fatalf("restart rewrote ledger timestamp: first=%s second=%s", firstApplied, secondApplied)
	}
}

func TestPostgresMigrationAdoptsCurrentSchemaWithoutDataLoss(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	pool := openPostgresMigrationTestPool(t, dsn)
	defer pool.Close()
	prepareUnversionedCurrentPostgresSchema(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
INSERT INTO weixin_chat_sessions (id, owner_id, binding_id, external_user_id, display_name, status)
VALUES
  ('legacy-session-evolved', 'legacy-owner', 'legacy-binding', 'legacy-user', 'legacy display', 'active'),
  ('legacy-session-copy', 'copy-owner', 'copy-binding', 'copy-user', 'copy display', 'active');
INSERT INTO external_chat_sessions (
  id, owner_id, authorized_owner_id, authorized_actor_id, binding_id, channel,
  external_user_id, external_chat_id, external_thread_id, display_name, status
) VALUES (
  'legacy-session-evolved', 'target-owner', 'authorized-owner', 'authorized-actor',
  'target-binding', 'weixin', 'target-user', 'target-chat', 'target-thread',
  'target display', 'target-active'
);
INSERT INTO weixin_chat_messages (
  id, chat_session_id, binding_id, direction, role, external_message_id, content, status
) VALUES
  ('legacy-message-evolved', 'legacy-session-evolved', 'legacy-binding', 'inbound', 'user', 'native-evolved', 'legacy content', 'received'),
  ('legacy-message-copy', 'legacy-session-copy', 'copy-binding', 'inbound', 'user', 'native-copy', 'copy content', 'received');
INSERT INTO external_chat_messages (
  id, chat_session_id, binding_id, channel, direction, role, external_message_id,
  content, status, pending_reply_kind, pending_reply, dispatch_attempts
) VALUES (
  'legacy-message-evolved', 'target-session', 'target-binding', 'weixin', 'outbound',
  'assistant', 'target-native', 'target content', 'delivered', 'text', 'target reply', 7
);
INSERT INTO sessions (id, title) VALUES ('browser-session', 'browser migration');
INSERT INTO agent_runs (id, session_id, state, model_lane, risk_level)
VALUES ('browser-run', 'browser-session', 'waiting', 'fast', 'read');
INSERT INTO browser_login_blocks (id, session_id, run_id, schema_version, version, status)
VALUES
  ('browser-waiting', 'browser-session', 'browser-run', 1, 0, 'waiting'),
  ('browser-resuming', 'browser-session', 'browser-run', 1, -1, 'resuming');
`)
	if err != nil {
		t.Fatal(err)
	}

	st, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	assertPostgresAdoptedData(t, st.db)

	restarted, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	restarted.Close()
	assertPostgresAdoptedData(t, st.db)
}

func TestPostgresMigrationRejectsMissingIDNaturalKeyConflicts(t *testing.T) {
	tests := []struct {
		name  string
		setup string
	}{
		{
			name: "session",
			setup: `
INSERT INTO weixin_chat_sessions (id, binding_id, external_user_id, status)
VALUES ('source-session', 'binding', 'canonical-user', 'active');
INSERT INTO external_chat_sessions (id, binding_id, channel, external_chat_id, external_thread_id, status)
VALUES ('other-session', 'binding', 'weixin', 'canonical-user', '', 'active');
`,
		},
		{
			name: "message",
			setup: `
INSERT INTO weixin_chat_messages (
  id, chat_session_id, binding_id, direction, role, external_message_id, content, status
) VALUES ('source-message', 'chat', 'binding', 'inbound', 'user', 'canonical-message', 'source', 'received');
INSERT INTO external_chat_messages (
  id, chat_session_id, binding_id, channel, direction, role, external_message_id, content, status
) VALUES ('other-message', 'chat', 'binding', 'weixin', 'inbound', 'user', 'canonical-message', 'target', 'received');
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := newPostgresMigrationTestSchema(t)
			pool := openPostgresMigrationTestPool(t, dsn)
			defer pool.Close()
			prepareUnversionedCurrentPostgresSchema(t, pool)
			if _, err := pool.Exec(context.Background(), test.setup); err != nil {
				t.Fatal(err)
			}
			if _, err := NewPostgresStore(context.Background(), dsn); err == nil || !strings.Contains(err.Error(), "canonical natural key") {
				t.Fatalf("migration error = %v", err)
			}
			assertPostgresLedgerCount(t, pool, 0)
		})
	}
}

func TestPostgresMigrationRejectsDuplicateLegacyNaturalKeys(t *testing.T) {
	tests := []struct {
		name  string
		setup string
	}{
		{
			name: "session",
			setup: `
INSERT INTO weixin_chat_sessions (id, binding_id, external_user_id, status)
VALUES
  ('source-session-a', 'binding', 'duplicate-user', 'active'),
  ('source-session-b', 'binding', 'duplicate-user', 'active');
`,
		},
		{
			name: "non-empty message key",
			setup: `
INSERT INTO weixin_chat_messages (
  id, chat_session_id, binding_id, direction, role, external_message_id, content, status
) VALUES
  ('source-message-a', 'chat', 'binding', 'inbound', 'user', 'duplicate-message', 'first', 'received'),
  ('source-message-b', 'chat', 'binding', 'inbound', 'user', 'duplicate-message', 'second', 'received');
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := newPostgresMigrationTestSchema(t)
			pool := openPostgresMigrationTestPool(t, dsn)
			defer pool.Close()
			prepareUnversionedCurrentPostgresSchema(t, pool)
			if _, err := pool.Exec(context.Background(), test.setup); err != nil {
				t.Fatal(err)
			}
			if _, err := NewPostgresStore(context.Background(), dsn); err == nil || !strings.Contains(err.Error(), "duplicate canonical natural key") {
				t.Fatalf("migration error = %v", err)
			}
			assertPostgresLedgerCount(t, pool, 0)
		})
	}
}

func TestPostgresMigrationRejectsChangedUnknownAndGappedLedger(t *testing.T) {
	tests := []struct {
		name   string
		mutate string
		want   string
	}{
		{name: "checksum", mutate: `UPDATE sparkclaw_schema_migrations SET checksum_sha256 = 'changed' WHERE version = 1`, want: "checksum mismatch"},
		{name: "unknown", mutate: `INSERT INTO sparkclaw_schema_migrations (version, filename, checksum_sha256) VALUES (99, '0099_unknown.sql', 'unknown')`, want: "binary knows"},
		{name: "gap", mutate: `DELETE FROM sparkclaw_schema_migrations WHERE version = 1`, want: "complete prefix"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := newPostgresMigrationTestSchema(t)
			st, err := NewPostgresStore(context.Background(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.Exec(context.Background(), test.mutate); err != nil {
				st.Close()
				t.Fatal(err)
			}
			st.Close()
			if _, err := NewPostgresStore(context.Background(), dsn); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("migration error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPostgresMigrationFailureRollsBackSchemaAndLedgerRows(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	pool := openPostgresMigrationTestPool(t, dsn)
	defer pool.Close()
	migrations, err := loadPostgresMigrations()
	if err != nil {
		t.Fatal(err)
	}
	broken := append([]postgresMigration(nil), migrations...)
	broken[1].SQL += `
CREATE TABLE migration_should_rollback (id INTEGER PRIMARY KEY);
SELECT 1 / 0;
`
	if err := runPostgresMigrationsWith(context.Background(), pool, broken, postgresMigrationHooks{}); err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	assertPostgresLedgerCount(t, pool, 0)
	for _, tableName := range []string{"migration_should_rollback", "owners"} {
		var relation *string
		if err := pool.QueryRow(context.Background(), `SELECT to_regclass($1)::text`, tableName).Scan(&relation); err != nil {
			t.Fatal(err)
		}
		if relation != nil {
			t.Fatalf("failed migration table survived rollback: %s", *relation)
		}
	}
}

func TestPostgresMigrationCatalogRejectsExpectedTableDriftAndAllowsUnrelatedTables(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	st, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(context.Background(), `CREATE TABLE operator_unrelated (id INTEGER PRIMARY KEY)`); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	restarted, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("unrelated table was rejected: %v", err)
	}
	if _, err := restarted.db.Exec(context.Background(), `ALTER TABLE owners ADD COLUMN unexpected_column TEXT`); err != nil {
		restarted.Close()
		t.Fatal(err)
	}
	restarted.Close()
	if _, err := NewPostgresStore(context.Background(), dsn); err == nil || !strings.Contains(err.Error(), `columns differ for table "owners"`) {
		t.Fatalf("expected-table drift error = %v", err)
	}
}

func TestPostgresMigrationCanceledBeforeAcquisitionCreatesNothing(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	pool := openPostgresMigrationTestPool(t, dsn)
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runPostgresMigrations(ctx, pool); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled migration error = %v", err)
	}
	var ledger *string
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass('sparkclaw_schema_migrations')::text`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != nil {
		t.Fatalf("pre-canceled migration created ledger %q", *ledger)
	}
}

func TestPostgresMigrationInsufficientDDLPrivilegeFails(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	baseDSN := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	admin := openPostgresMigrationTestPool(t, baseDSN)
	role := fmt.Sprintf("sparkclaw_s1_restricted_%d", time.Now().UnixNano())
	if _, err := admin.Exec(context.Background(), `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` NOLOGIN`); err != nil {
		t.Fatalf("create restricted migration test role: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Errorf("drop restricted migration test role: %v", err)
		}
		admin.Close()
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE `+pgx.Identifier{role}.Sanitize())
		return err
	}
	restricted, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer restricted.Close()
	if err := runPostgresMigrations(context.Background(), restricted); err == nil {
		t.Fatalf("restricted migration error = %v", err)
	}
	observer := openPostgresMigrationTestPool(t, dsn)
	defer observer.Close()
	var ledger *string
	if err := observer.QueryRow(context.Background(), `SELECT to_regclass('sparkclaw_schema_migrations')::text`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != nil {
		t.Fatalf("restricted migration created ledger %q", *ledger)
	}
}

func TestPostgresMigrationConcurrentRunnersSerialize(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	pool := openPostgresMigrationTestPool(t, dsn)
	defer pool.Close()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- runPostgresMigrationsWith(context.Background(), pool, nil, postgresMigrationHooks{
			beforeCommit: func() {
				close(firstEntered)
				<-releaseFirst
			},
		})
	}()
	<-firstEntered
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- runPostgresMigrations(context.Background(), pool)
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second runner bypassed advisory lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestPostgresMigrationBlocksCompatibilityWritesUntilCommit(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	pool := openPostgresMigrationTestPool(t, dsn)
	defer pool.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runPostgresMigrationsWith(context.Background(), pool, nil, postgresMigrationHooks{
			beforeCommit: func() {
				close(entered)
				<-release
			},
		})
	}()
	<-entered
	writeCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, writeErr := pool.Exec(writeCtx, `
INSERT INTO weixin_chat_sessions (id, binding_id, external_user_id, status)
VALUES ('blocked-old-writer', 'binding', 'user', 'active')
`)
	cancel()
	if writeErr == nil {
		t.Fatal("compatibility write was not blocked by migration table locks")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO weixin_chat_sessions (id, binding_id, external_user_id, status)
VALUES ('post-migration-writer', 'binding', 'user', 'active')
`); err != nil {
		t.Fatalf("compatibility write stayed blocked after commit: %v", err)
	}
}

func TestPostgresMigrationCommitUncertainty(t *testing.T) {
	sentinel := errors.New("injected commit transport error")
	t.Run("committed", func(t *testing.T) {
		dsn := newPostgresMigrationTestSchema(t)
		pool := openPostgresMigrationTestPool(t, dsn)
		defer pool.Close()
		err := runPostgresMigrationsWith(context.Background(), pool, nil, postgresMigrationHooks{
			commit: func(ctx context.Context, tx pgx.Tx) error {
				if err := tx.Commit(ctx); err != nil {
					return err
				}
				return sentinel
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertPostgresLedgerCount(t, pool, 3)
	})

	t.Run("not committed retries once", func(t *testing.T) {
		dsn := newPostgresMigrationTestSchema(t)
		pool := openPostgresMigrationTestPool(t, dsn)
		defer pool.Close()
		var commits atomic.Int32
		err := runPostgresMigrationsWith(context.Background(), pool, nil, postgresMigrationHooks{
			commit: func(ctx context.Context, tx pgx.Tx) error {
				if commits.Add(1) == 1 {
					if err := tx.Rollback(ctx); err != nil {
						return err
					}
					return sentinel
				}
				return tx.Commit(ctx)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := commits.Load(); got != 2 {
			t.Fatalf("commit attempts = %d, want 2", got)
		}
		assertPostgresLedgerCount(t, pool, 3)
	})

	t.Run("complete ledger validates as committed", func(t *testing.T) {
		dsn := newPostgresMigrationTestSchema(t)
		pool := openPostgresMigrationTestPool(t, dsn)
		defer pool.Close()
		if err := runPostgresMigrations(context.Background(), pool); err != nil {
			t.Fatal(err)
		}
		var commits atomic.Int32
		err := runPostgresMigrationsWith(context.Background(), pool, nil, postgresMigrationHooks{
			commit: func(ctx context.Context, tx pgx.Tx) error {
				commits.Add(1)
				if err := tx.Rollback(ctx); err != nil {
					return err
				}
				return sentinel
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := commits.Load(); got != 1 {
			t.Fatalf("complete-ledger commit attempts = %d, want 1", got)
		}
	})

	t.Run("unknown outcome fails closed", func(t *testing.T) {
		dsn := newPostgresMigrationTestSchema(t)
		pool := openPostgresMigrationTestPool(t, dsn)
		defer pool.Close()
		err := runPostgresMigrationsWith(context.Background(), pool, nil, postgresMigrationHooks{
			commit: func(ctx context.Context, tx pgx.Tx) error {
				if err := tx.Rollback(ctx); err != nil {
					return err
				}
				return sentinel
			},
			afterCommitError: func(ctx context.Context, pool *pgxpool.Pool) {
				if _, insertErr := pool.Exec(ctx, `
INSERT INTO sparkclaw_schema_migrations (version, filename, checksum_sha256)
VALUES (99, '0099_unknown.sql', 'unknown')
`); insertErr != nil {
					t.Errorf("seed unknown ledger outcome: %v", insertErr)
				}
			},
		})
		if err == nil || !strings.Contains(err.Error(), "unknown outcome") {
			t.Fatalf("migration error = %v", err)
		}
	})
}

func newPostgresMigrationTestSchema(t *testing.T) string {
	t.Helper()
	baseDSN := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres migration integration tests")
	}
	admin, err := pgxpool.New(context.Background(), baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("sparkclaw_s1_%d", time.Now().UnixNano())
	if _, err := admin.Exec(context.Background(), `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
			t.Errorf("drop migration test schema %s: %v", schema, err)
		}
		admin.Close()
	})
	scopedDSN := baseDSN + " search_path=" + schema
	parsed, err := url.Parse(baseDSN)
	if err == nil && parsed.Scheme != "" {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		scopedDSN = parsed.String()
	}
	verification := openPostgresMigrationTestPool(t, scopedDSN)
	var currentSchema string
	if err := verification.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&currentSchema); err != nil {
		verification.Close()
		t.Fatal(err)
	}
	verification.Close()
	if currentSchema != schema {
		t.Fatalf("scoped migration DSN resolved schema %q, want %q", currentSchema, schema)
	}
	return scopedDSN
}

func openPostgresMigrationTestPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func prepareUnversionedCurrentPostgresSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	migrations, err := loadPostgresMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), migrations[1].SQL); err != nil {
		t.Fatal(err)
	}
}

func assertPostgresLedgerCount(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM sparkclaw_schema_migrations`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ledger count = %d, want %d", got, want)
	}
}

func assertPostgresAdoptedData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var ownerID, externalChatID, status string
	if err := pool.QueryRow(context.Background(), `
SELECT owner_id, external_chat_id, status
FROM external_chat_sessions WHERE id = 'legacy-session-evolved'
`).Scan(&ownerID, &externalChatID, &status); err != nil {
		t.Fatal(err)
	}
	if ownerID != "target-owner" || externalChatID != "target-chat" || status != "target-active" {
		t.Fatalf("evolved session was overwritten: owner=%q chat=%q status=%q", ownerID, externalChatID, status)
	}
	var copiedOwner, copiedChat string
	if err := pool.QueryRow(context.Background(), `
SELECT owner_id, external_chat_id FROM external_chat_sessions WHERE id = 'legacy-session-copy'
`).Scan(&copiedOwner, &copiedChat); err != nil {
		t.Fatal(err)
	}
	if copiedOwner != "copy-owner" || copiedChat != "copy-user" {
		t.Fatalf("missing session was not copied canonically: owner=%q chat=%q", copiedOwner, copiedChat)
	}
	var content, messageStatus string
	var attempts int
	if err := pool.QueryRow(context.Background(), `
SELECT content, status, dispatch_attempts FROM external_chat_messages WHERE id = 'legacy-message-evolved'
`).Scan(&content, &messageStatus, &attempts); err != nil {
		t.Fatal(err)
	}
	if content != "target content" || messageStatus != "delivered" || attempts != 7 {
		t.Fatalf("evolved message was overwritten: content=%q status=%q attempts=%d", content, messageStatus, attempts)
	}
	var copiedContent string
	if err := pool.QueryRow(context.Background(), `
SELECT content FROM external_chat_messages WHERE id = 'legacy-message-copy'
`).Scan(&copiedContent); err != nil {
		t.Fatal(err)
	}
	if copiedContent != "copy content" {
		t.Fatalf("missing message content = %q", copiedContent)
	}
	rows, err := pool.Query(context.Background(), `
SELECT status, schema_version, version FROM browser_login_blocks ORDER BY id
`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantStatuses := []string{"validating_visible", "waiting_owner"}
	index := 0
	for rows.Next() {
		var schemaVersion, version int
		var gotStatus string
		if err := rows.Scan(&gotStatus, &schemaVersion, &version); err != nil {
			t.Fatal(err)
		}
		if index >= len(wantStatuses) || gotStatus != wantStatuses[index] || schemaVersion != 2 || version != 1 {
			t.Fatalf("browser row %d = status %q schema %d version %d", index, gotStatus, schemaVersion, version)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(wantStatuses) {
		t.Fatalf("browser row count = %d", index)
	}
	assertPostgresLedgerCount(t, pool, 3)
}
