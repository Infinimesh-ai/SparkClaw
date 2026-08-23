# PostgreSQL Schema And Store Configuration Design

> Language: English | [简体中文](../zh-cn/docs/store-postgresql-schema-config-design.md)

> Status: draft for S1 design review, 2026-08-19. This stage precedes File and
> repository migration so PostgreSQL has one known schema authority first.

## Objective

Replace the duplicated PostgreSQL schema with one embedded, versioned migration
source and make Store-specific configuration fail during `config.Load` instead
of during the first persistence operation.

This stage does not change the PostgreSQL CI service, the
`SPARKCLAW_TEST_POSTGRES_DSN` name, or test skip behavior.

## Schema Authority

The ordered SQL files under
`services/gateway/internal/store/migrations/*.sql` become the only domain-schema
authority.

1. Move root `migrations/0001_core.sql` byte-identically into the Store package
   and freeze its checksum.
2. Add an idempotent next migration that reconciles every table, column, index,
   and constraint currently present only in the Go `postgresSchema` string.
3. Embed the ordered files with `go:embed` and execute them from Gateway Store
   startup.
4. Remove the Go `postgresSchema` domain DDL and stop copying a separate schema
   directory into the PostgreSQL image.
5. Use the same runner for a fresh database and an existing SparkClaw database.

The runner may contain only the minimal migration-ledger bootstrap DDL. It may
not restate application tables or indexes in Go.

## Migration Ledger

`sparkclaw_schema_migrations` stores immutable version, filename, SHA-256
checksum, and application timestamp.

- migrations apply in lexical/version order inside transactions;
- an applied version with a different checksum fails startup;
- an unknown applied version fails startup;
- a failed migration does not write its ledger row;
- restart after success is idempotent;
- readiness remains false until migration and post-migration validation pass.

For an existing database without a ledger, the runner applies the idempotent
baseline and reconciliation migrations, verifies the resulting schema, and
then records them. It does not infer success only from the presence of one
table.

The S0 inventory supplies the exact reconciliation manifest. The migration
review compares that manifest against both the old root SQL and current
`postgresSchema` before either source is removed.

## PostgreSQL Operation Bounds

Startup migration uses the caller startup context and a validated fallback
deadline. Pool acquisition and every migration statement inherit that context.
Server-side `statement_timeout` and `lock_timeout` are backstops, not substitutes
for caller cancellation.

Ordinary read/write/transaction timeout fields are introduced only when the
pilot repository uses them in S2, so configuration does not advertise inert
behavior. Their accepted names and initial values come from S0.

## Store Configuration Validation

`config.Load` validates only Store-owned settings in this stage:

- state backend is exactly `memory`, `file`, or `postgres`;
- File requires a non-empty normalized path;
- PostgreSQL requires a non-empty DSN;
- File encryption requires exactly one usable key source according to the
  existing precedence contract;
- startup timeout is positive and bounded;
- malformed Store boolean/integer overrides return an error naming the exact
  environment variable;
- DSNs and keys remain absent from public projections and logs.

Global cleanup of every permissive environment parser and artifact object
backend construction are separate work. This stage must not expand into a
whole-config rewrite.

## Database Failure Contract

The migration runner returns and preserves every pool, begin, execution,
scanning, rows, commit, and rollback error. Rollback errors join the primary
diagnostic without replacing it. A transport failure after commit submission
is `unknown_outcome`; startup reconciles through the migration ledger and
checksum before deciding whether to retry.

## Verification

Required deterministic and configured-PostgreSQL evidence:

- embedded migration ordering and checksum unit tests;
- source guard proving no domain DDL remains in Go;
- fresh empty database reaches the expected manifest;
- current unversioned database adopts the ledger without data loss;
- current versioned database restarts without DDL churn;
- changed checksum and unknown version fail startup;
- a forced migration failure rolls back and leaves no ledger row;
- insufficient DDL privilege fails before readiness;
- default File and Memory configurations still load;
- malformed backend, missing File path, missing DSN, malformed timeout, and
  invalid encryption configuration fail with safe diagnostics;
- existing DSN-gated Store tests pass in the configured environment.

PostgreSQL integration evidence records the tested commit, server version,
migration starting state, and command result. If a DSN is unavailable, the
stage remains unapproved; it is reported as not run, not passed.

## S1 Review Gate

Design `GO` requires the reconciliation manifest, ledger adoption rules,
configuration scope, rollback, and test environments to be accepted.
Implementation `GO` requires all verification above, removal of both old schema
authorities, and a real PostgreSQL run while retaining the existing CI gate.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| Design | pending | pending | pending | pending |
| Implementation | pending | pending | pending | pending |
