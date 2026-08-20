package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	postgresMigrationLockKey        int64 = 6003381055699113777
	postgresStartupTimeout                = 180 * time.Second
	postgresMigrationLockTimeout          = 30 * time.Second
	postgresMigrationCleanupTimeout       = 5 * time.Second
)

const postgresMigrationLedgerDDL = `
CREATE TABLE IF NOT EXISTS sparkclaw_schema_migrations (
  version INTEGER PRIMARY KEY CHECK (version > 0),
  filename TEXT NOT NULL UNIQUE,
  checksum_sha256 TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

var postgresMigrationFilenamePattern = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// postgresMigrationFiles is the single authority for PostgreSQL application
// schema. Go contains only the migration-ledger bootstrap above.
//
//go:embed migrations/*.sql
var postgresMigrationFiles embed.FS

type postgresMigration struct {
	Version  int
	Filename string
	Path     string
	Checksum string
	SQL      string
}

type postgresMigrationHooks struct {
	beforeCommit     func()
	commit           func(context.Context, pgx.Tx) error
	afterCommitError func(context.Context, *pgxpool.Pool)
}

type postgresAppliedMigration struct {
	Version  int
	Filename string
	Checksum string
}

type postgresCommitResult struct {
	Err           error
	DestroyErr    error
	AppliedBefore int
}

type postgresCommitState uint8

const (
	postgresCommitUnknown postgresCommitState = iota
	postgresCommitCommitted
	postgresCommitNotCommitted
)

func loadPostgresMigrations() ([]postgresMigration, error) {
	entries, err := fs.ReadDir(postgresMigrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded postgres migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	migrations := make([]postgresMigration, 0, len(entries))
	for index, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("postgres migration path %q must be a file", entry.Name())
		}
		match := postgresMigrationFilenamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid postgres migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("parse postgres migration version %q: %w", entry.Name(), err)
		}
		if version != index+1 {
			return nil, fmt.Errorf("postgres migration %q has version %d, want %d", entry.Name(), version, index+1)
		}
		path := "migrations/" + entry.Name()
		raw, err := postgresMigrationFiles.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read postgres migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(raw)
		migrations = append(migrations, postgresMigration{
			Version:  version,
			Filename: entry.Name(),
			Path:     path,
			Checksum: fmt.Sprintf("%x", digest),
			SQL:      string(raw),
		})
	}
	if len(migrations) == 0 {
		return nil, errors.New("no embedded postgres migrations")
	}
	return migrations, nil
}

func runPostgresMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	return runPostgresMigrationsWith(ctx, pool, nil, postgresMigrationHooks{})
}

func runPostgresMigrationsWith(ctx context.Context, pool *pgxpool.Pool, supplied []postgresMigration, hooks postgresMigrationHooks) error {
	ctx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("postgres migration canceled before acquisition: %w", err)
	}
	migrations := supplied
	if migrations == nil {
		var err error
		migrations, err = loadPostgresMigrations()
		if err != nil {
			return err
		}
	}

	commitErrors := make([]error, 0, 2)
	for attempt := 0; attempt < 2; attempt++ {
		commitResult, err := runPostgresMigrationAttempt(ctx, pool, migrations, hooks)
		if err != nil {
			return err
		}
		if commitResult.Err == nil {
			return nil
		}
		commitErrors = append(commitErrors, fmt.Errorf("commit postgres migrations: %w", commitResult.Err))
		if hooks.afterCommitError != nil {
			hooks.afterCommitError(ctx, pool)
		}
		state, reconcileErr := reconcilePostgresCommit(ctx, pool, migrations, commitResult.AppliedBefore)
		switch state {
		case postgresCommitCommitted:
			return nil
		case postgresCommitNotCommitted:
			if attempt == 0 {
				continue
			}
			return errors.Join(append(commitErrors,
				errors.New("postgres migration commit was not committed after one bounded retry"),
				commitResult.DestroyErr,
				reconcileErr,
			)...)
		default:
			return errors.Join(append(commitErrors,
				errors.New("postgres migration commit has unknown outcome"),
				commitResult.DestroyErr,
				reconcileErr,
			)...)
		}
	}
	return errors.New("postgres migration retry state exhausted")
}

func runPostgresMigrationAttempt(ctx context.Context, pool *pgxpool.Pool, migrations []postgresMigration, hooks postgresMigrationHooks) (postgresCommitResult, error) {
	locked, err := acquirePostgresMigrationLock(ctx, pool)
	if err != nil {
		return postgresCommitResult{}, err
	}
	finish := func(primary error) error {
		return errors.Join(primary, locked.unlockAndRelease())
	}

	if _, err := locked.conn.Exec(ctx, postgresMigrationLedgerDDL); err != nil {
		return postgresCommitResult{}, finish(fmt.Errorf("create postgres migration ledger: %w", err))
	}
	applied, err := readPostgresAppliedMigrations(ctx, locked.conn, migrations)
	if err != nil {
		return postgresCommitResult{}, finish(err)
	}
	tx, err := locked.conn.Begin(ctx)
	if err != nil {
		return postgresCommitResult{}, finish(fmt.Errorf("begin postgres migration transaction: %w", err))
	}
	if err := setPostgresMigrationTimeouts(ctx, tx); err != nil {
		return postgresCommitResult{}, rollbackPostgresMigration(locked, tx, fmt.Errorf("set postgres migration timeouts: %w", err))
	}
	for _, migration := range migrations[len(applied):] {
		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			return postgresCommitResult{}, rollbackPostgresMigration(locked, tx,
				fmt.Errorf("apply postgres migration %s: %w", migration.Filename, err))
		}
	}
	if err := validatePostgresMigrationState(ctx, tx, migrations); err != nil {
		return postgresCommitResult{}, rollbackPostgresMigration(locked, tx, err)
	}
	for _, migration := range migrations[len(applied):] {
		if _, err := tx.Exec(ctx, `
INSERT INTO sparkclaw_schema_migrations (version, filename, checksum_sha256)
VALUES ($1, $2, $3)
`, migration.Version, migration.Filename, migration.Checksum); err != nil {
			return postgresCommitResult{}, rollbackPostgresMigration(locked, tx,
				fmt.Errorf("record postgres migration %s: %w", migration.Filename, err))
		}
	}
	if hooks.beforeCommit != nil {
		hooks.beforeCommit()
	}
	var commitErr error
	if hooks.commit != nil {
		commitErr = hooks.commit(ctx, tx)
	} else {
		commitErr = tx.Commit(ctx)
	}
	if commitErr != nil {
		destroyErr := locked.destroy()
		return postgresCommitResult{Err: commitErr, DestroyErr: destroyErr, AppliedBefore: len(applied)}, nil
	}
	return postgresCommitResult{}, finish(nil)
}

func rollbackPostgresMigration(locked *postgresLockedConnection, tx pgx.Tx, primary error) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), postgresMigrationCleanupTimeout)
	defer cancel()
	rollbackErr := tx.Rollback(cleanupCtx)
	if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
		destroyErr := locked.destroy()
		return errors.Join(primary, fmt.Errorf("rollback postgres migration: %w", rollbackErr), destroyErr)
	}
	return errors.Join(primary, locked.unlockAndRelease())
}

func reconcilePostgresCommit(ctx context.Context, pool *pgxpool.Pool, migrations []postgresMigration, appliedBefore int) (postgresCommitState, error) {
	locked, err := acquirePostgresMigrationLock(ctx, pool)
	if err != nil {
		return postgresCommitUnknown, fmt.Errorf("reacquire postgres migration lock: %w", err)
	}
	finish := func(primary error) error {
		return errors.Join(primary, locked.unlockAndRelease())
	}
	applied, err := readPostgresAppliedMigrations(ctx, locked.conn, migrations)
	if err != nil {
		return postgresCommitUnknown, finish(fmt.Errorf("inspect postgres migration commit: %w", err))
	}
	if len(applied) != len(migrations) && len(applied) != appliedBefore {
		return postgresCommitUnknown, finish(fmt.Errorf(
			"inspect postgres migration commit: ledger has %d rows, want prior prefix %d or complete prefix %d",
			len(applied), appliedBefore, len(migrations)))
	}
	if len(applied) == appliedBefore && len(applied) != len(migrations) {
		return postgresCommitNotCommitted, finish(nil)
	}
	tx, err := locked.conn.Begin(ctx)
	if err != nil {
		return postgresCommitUnknown, finish(fmt.Errorf("begin postgres commit reconciliation: %w", err))
	}
	if err := setPostgresMigrationTimeouts(ctx, tx); err != nil {
		return postgresCommitUnknown, rollbackPostgresReconciliation(locked, tx,
			fmt.Errorf("set postgres reconciliation timeouts: %w", err))
	}
	if err := validatePostgresMigrationState(ctx, tx, migrations); err != nil {
		return postgresCommitUnknown, rollbackPostgresReconciliation(locked, tx, err)
	}
	if err := rollbackPostgresReconciliation(locked, tx, nil); err != nil {
		return postgresCommitUnknown, err
	}
	return postgresCommitCommitted, nil
}

func postgresMigrationStartupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, postgresStartupTimeout)
}

func rollbackPostgresReconciliation(locked *postgresLockedConnection, tx pgx.Tx, primary error) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), postgresMigrationCleanupTimeout)
	defer cancel()
	rollbackErr := tx.Rollback(cleanupCtx)
	if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
		destroyErr := locked.destroy()
		return errors.Join(primary, fmt.Errorf("rollback postgres reconciliation: %w", rollbackErr), destroyErr)
	}
	return errors.Join(primary, locked.unlockAndRelease())
}

type postgresLockedConnection struct {
	conn *pgxpool.Conn
}

func acquirePostgresMigrationLock(ctx context.Context, pool *pgxpool.Pool) (*postgresLockedConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("postgres migration canceled before pool acquisition: %w", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire postgres migration connection: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, postgresMigrationLockKey); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire postgres migration advisory lock: %w", err)
	}
	return &postgresLockedConnection{conn: conn}, nil
}

func (locked *postgresLockedConnection) unlockAndRelease() error {
	if locked == nil || locked.conn == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), postgresMigrationCleanupTimeout)
	defer cancel()
	var unlocked bool
	err := locked.conn.QueryRow(cleanupCtx, `SELECT pg_advisory_unlock($1)`, postgresMigrationLockKey).Scan(&unlocked)
	if err == nil && unlocked {
		locked.conn.Release()
		locked.conn = nil
		return nil
	}
	unlockErr := err
	if unlockErr == nil {
		unlockErr = errors.New("postgres migration advisory lock was not held")
	}
	return errors.Join(fmt.Errorf("release postgres migration advisory lock: %w", unlockErr), locked.destroy())
}

func (locked *postgresLockedConnection) destroy() error {
	if locked == nil || locked.conn == nil {
		return nil
	}
	raw := locked.conn.Hijack()
	locked.conn = nil
	cleanupCtx, cancel := context.WithTimeout(context.Background(), postgresMigrationCleanupTimeout)
	defer cancel()
	if err := raw.Close(cleanupCtx); err != nil {
		return fmt.Errorf("close postgres migration connection: %w", err)
	}
	return nil
}

func readPostgresAppliedMigrations(ctx context.Context, conn interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, migrations []postgresMigration) ([]postgresAppliedMigration, error) {
	rows, err := conn.Query(ctx, `
SELECT version, filename, checksum_sha256
FROM sparkclaw_schema_migrations
ORDER BY version
`)
	if err != nil {
		return nil, fmt.Errorf("read postgres migration ledger: %w", err)
	}
	defer rows.Close()
	applied := make([]postgresAppliedMigration, 0, len(migrations))
	for rows.Next() {
		var migration postgresAppliedMigration
		if err := rows.Scan(&migration.Version, &migration.Filename, &migration.Checksum); err != nil {
			return nil, fmt.Errorf("scan postgres migration ledger: %w", err)
		}
		applied = append(applied, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres migration ledger: %w", err)
	}
	if len(applied) > len(migrations) {
		return nil, fmt.Errorf("postgres migration ledger contains %d entries but binary knows %d", len(applied), len(migrations))
	}
	for index, got := range applied {
		want := migrations[index]
		if got.Version != want.Version {
			return nil, fmt.Errorf("postgres migration ledger is not a complete prefix at position %d: got version %d, want %d", index+1, got.Version, want.Version)
		}
		if got.Filename != want.Filename {
			return nil, fmt.Errorf("postgres migration version %d filename mismatch", got.Version)
		}
		if got.Checksum != want.Checksum {
			return nil, fmt.Errorf("postgres migration version %d checksum mismatch", got.Version)
		}
	}
	return applied, nil
}

func setPostgresMigrationTimeouts(ctx context.Context, tx pgx.Tx) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("postgres migration context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.DeadlineExceeded
	}
	statementMS := max(1, remaining.Milliseconds())
	lockDuration := min(remaining, postgresMigrationLockTimeout)
	lockMS := max(1, lockDuration.Milliseconds())
	if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout', $1, true)`, fmt.Sprintf("%dms", statementMS)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, fmt.Sprintf("%dms", lockMS)); err != nil {
		return err
	}
	return nil
}

type postgresSchemaCatalog struct {
	Tables map[string]postgresTableCatalog
}

type postgresTableCatalog struct {
	Columns     []postgresColumnCatalog
	Constraints []postgresConstraintCatalog
	Indexes     []postgresIndexCatalog
}

type postgresColumnCatalog struct {
	Name       string
	Type       string
	NotNull    bool
	DefaultSQL string
}

type postgresConstraintCatalog struct {
	Name       string
	Type       string
	Definition string
}

type postgresIndexCatalog struct {
	Name       string
	Unique     bool
	Definition string
	Predicate  string
}

func validatePostgresMigrationState(ctx context.Context, tx pgx.Tx, migrations []postgresMigration) error {
	targetSchema, err := currentPostgresSchema(ctx, tx)
	if err != nil {
		return err
	}
	var backendPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		return fmt.Errorf("read postgres migration backend identity: %w", err)
	}
	scratchSchema := fmt.Sprintf("sparkclaw_migration_%d", backendPID)
	if _, err := tx.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{scratchSchema}.Sanitize()); err != nil {
		return fmt.Errorf("create postgres migration scratch schema: %w", err)
	}
	if err := setPostgresSearchPath(ctx, tx, scratchSchema); err != nil {
		return err
	}
	for _, migration := range migrations {
		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			return fmt.Errorf("build expected postgres catalog from %s: %w", migration.Filename, err)
		}
	}
	expected, err := readPostgresSchemaCatalog(ctx, tx, scratchSchema)
	if err != nil {
		return fmt.Errorf("read expected postgres catalog: %w", err)
	}
	if err := setPostgresSearchPath(ctx, tx, targetSchema); err != nil {
		return err
	}
	actual, err := readPostgresSchemaCatalog(ctx, tx, targetSchema)
	if err != nil {
		return fmt.Errorf("read current postgres catalog: %w", err)
	}
	if err := comparePostgresSchemaCatalog(expected, actual); err != nil {
		return err
	}
	if err := validatePostgresCompatibilityPostconditions(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{scratchSchema}.Sanitize()+` CASCADE`); err != nil {
		return fmt.Errorf("drop postgres migration scratch schema: %w", err)
	}
	return nil
}

func currentPostgresSchema(ctx context.Context, tx pgx.Tx) (string, error) {
	var schema string
	if err := tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return "", fmt.Errorf("resolve postgres application schema: %w", err)
	}
	if strings.TrimSpace(schema) == "" {
		return "", errors.New("postgres application schema is empty")
	}
	return schema, nil
}

func setPostgresSearchPath(ctx context.Context, tx pgx.Tx, schema string) error {
	searchPath := pgx.Identifier{schema}.Sanitize() + ", pg_catalog"
	if _, err := tx.Exec(ctx, `SELECT set_config('search_path', $1, true)`, searchPath); err != nil {
		return fmt.Errorf("set postgres migration search path: %w", err)
	}
	return nil
}

func readPostgresSchemaCatalog(ctx context.Context, tx pgx.Tx, schema string) (postgresSchemaCatalog, error) {
	if err := setPostgresSearchPath(ctx, tx, schema); err != nil {
		return postgresSchemaCatalog{}, err
	}
	catalog := postgresSchemaCatalog{Tables: map[string]postgresTableCatalog{}}
	rows, err := tx.Query(ctx, `
SELECT table_class.relname,
       attribute.attname,
       pg_catalog.format_type(attribute.atttypid, attribute.atttypmod),
       attribute.attnotnull,
       COALESCE(pg_catalog.pg_get_expr(default_value.adbin, default_value.adrelid, true), '')
FROM pg_catalog.pg_class AS table_class
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = table_class.relnamespace
JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid = table_class.oid
LEFT JOIN pg_catalog.pg_attrdef AS default_value
  ON default_value.adrelid = table_class.oid AND default_value.adnum = attribute.attnum
WHERE namespace.nspname = $1
  AND table_class.relkind IN ('r', 'p')
  AND attribute.attnum > 0
  AND NOT attribute.attisdropped
ORDER BY table_class.relname, attribute.attnum
`, schema)
	if err != nil {
		return postgresSchemaCatalog{}, err
	}
	for rows.Next() {
		var tableName string
		var column postgresColumnCatalog
		if err := rows.Scan(&tableName, &column.Name, &column.Type, &column.NotNull, &column.DefaultSQL); err != nil {
			rows.Close()
			return postgresSchemaCatalog{}, err
		}
		column.DefaultSQL = normalizePostgresCatalogDefinition(column.DefaultSQL, schema)
		table := catalog.Tables[tableName]
		table.Columns = append(table.Columns, column)
		catalog.Tables[tableName] = table
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return postgresSchemaCatalog{}, err
	}
	rows.Close()
	for tableName, table := range catalog.Tables {
		sort.Slice(table.Columns, func(i, j int) bool { return table.Columns[i].Name < table.Columns[j].Name })
		catalog.Tables[tableName] = table
	}

	rows, err = tx.Query(ctx, `
SELECT table_class.relname,
       constraint_entry.conname,
       constraint_entry.contype::text,
       pg_catalog.pg_get_constraintdef(constraint_entry.oid, true)
FROM pg_catalog.pg_constraint AS constraint_entry
JOIN pg_catalog.pg_class AS table_class ON table_class.oid = constraint_entry.conrelid
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = table_class.relnamespace
WHERE namespace.nspname = $1
  AND constraint_entry.contype IN ('p', 'f', 'u', 'c')
ORDER BY table_class.relname, constraint_entry.conname
`, schema)
	if err != nil {
		return postgresSchemaCatalog{}, err
	}
	for rows.Next() {
		var tableName string
		var constraint postgresConstraintCatalog
		if err := rows.Scan(&tableName, &constraint.Name, &constraint.Type, &constraint.Definition); err != nil {
			rows.Close()
			return postgresSchemaCatalog{}, err
		}
		constraint.Definition = normalizePostgresCatalogDefinition(constraint.Definition, schema)
		table := catalog.Tables[tableName]
		table.Constraints = append(table.Constraints, constraint)
		catalog.Tables[tableName] = table
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return postgresSchemaCatalog{}, err
	}
	rows.Close()

	rows, err = tx.Query(ctx, `
SELECT table_class.relname,
       index_class.relname,
       index_entry.indisunique,
       pg_catalog.pg_get_indexdef(index_entry.indexrelid),
       COALESCE(pg_catalog.pg_get_expr(index_entry.indpred, index_entry.indrelid, true), '')
FROM pg_catalog.pg_index AS index_entry
JOIN pg_catalog.pg_class AS table_class ON table_class.oid = index_entry.indrelid
JOIN pg_catalog.pg_class AS index_class ON index_class.oid = index_entry.indexrelid
JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = table_class.relnamespace
LEFT JOIN pg_catalog.pg_constraint AS constraint_entry ON constraint_entry.conindid = index_entry.indexrelid
WHERE namespace.nspname = $1
  AND constraint_entry.oid IS NULL
ORDER BY table_class.relname, index_class.relname
`, schema)
	if err != nil {
		return postgresSchemaCatalog{}, err
	}
	for rows.Next() {
		var tableName string
		var index postgresIndexCatalog
		if err := rows.Scan(&tableName, &index.Name, &index.Unique, &index.Definition, &index.Predicate); err != nil {
			rows.Close()
			return postgresSchemaCatalog{}, err
		}
		index.Definition = normalizePostgresCatalogDefinition(index.Definition, schema)
		index.Predicate = normalizePostgresCatalogDefinition(index.Predicate, schema)
		table := catalog.Tables[tableName]
		table.Indexes = append(table.Indexes, index)
		catalog.Tables[tableName] = table
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return postgresSchemaCatalog{}, err
	}
	rows.Close()
	return catalog, nil
}

func normalizePostgresCatalogDefinition(definition, schema string) string {
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	definition = strings.ReplaceAll(definition, quotedSchema+".", "")
	definition = strings.ReplaceAll(definition, schema+".", "")
	return strings.Join(strings.Fields(definition), " ")
}

func comparePostgresSchemaCatalog(expected, actual postgresSchemaCatalog) error {
	tableNames := make([]string, 0, len(expected.Tables))
	for tableName := range expected.Tables {
		tableNames = append(tableNames, tableName)
	}
	sort.Strings(tableNames)
	for _, tableName := range tableNames {
		want := expected.Tables[tableName]
		got, exists := actual.Tables[tableName]
		if !exists {
			return fmt.Errorf("postgres schema validation: expected table %q is missing", tableName)
		}
		if !reflect.DeepEqual(got.Columns, want.Columns) {
			return fmt.Errorf("postgres schema validation: columns differ for table %q: got %#v, want %#v", tableName, got.Columns, want.Columns)
		}
		if !reflect.DeepEqual(got.Constraints, want.Constraints) {
			return fmt.Errorf("postgres schema validation: constraints differ for table %q: got %#v, want %#v", tableName, got.Constraints, want.Constraints)
		}
		if !reflect.DeepEqual(got.Indexes, want.Indexes) {
			return fmt.Errorf("postgres schema validation: indexes differ for table %q: got %#v, want %#v", tableName, got.Indexes, want.Indexes)
		}
	}
	return nil
}

func validatePostgresCompatibilityPostconditions(ctx context.Context, tx pgx.Tx) error {
	checks := []struct {
		name  string
		query string
	}{
		{
			name: "external chat session source IDs",
			query: `SELECT count(*) = count(target.id)
FROM weixin_chat_sessions AS source
LEFT JOIN external_chat_sessions AS target ON target.id = source.id`,
		},
		{
			name: "external chat message source IDs",
			query: `SELECT count(*) = count(target.id)
FROM weixin_chat_messages AS source
LEFT JOIN external_chat_messages AS target ON target.id = source.id`,
		},
		{
			name:  "browser login block legacy statuses",
			query: `SELECT NOT EXISTS (SELECT 1 FROM browser_login_blocks WHERE status IN ('waiting', 'resuming'))`,
		},
		{
			name: "browser login block migrated schema versions",
			query: `SELECT NOT EXISTS (
  SELECT 1 FROM browser_login_blocks
  WHERE status IN ('waiting_owner', 'validating_visible') AND schema_version <> 2
)`,
		},
		{
			name:  "browser login block positive versions",
			query: `SELECT NOT EXISTS (SELECT 1 FROM browser_login_blocks WHERE version <= 0)`,
		},
	}
	for _, check := range checks {
		var valid bool
		if err := tx.QueryRow(ctx, check.query).Scan(&valid); err != nil {
			return fmt.Errorf("validate postgres compatibility postcondition %q: %w", check.name, err)
		}
		if !valid {
			return fmt.Errorf("postgres compatibility postcondition failed: %s", check.name)
		}
	}
	return nil
}
