# PostgreSQL Schema And Store Configuration Design

> Language: English | [简体中文](../zh-cn/docs/store-postgresql-schema-config-design.md)

> Status: S1 implementation passed independent review on 2026-08-20 after one
> implementation `REVISE`, fix commit `74b7c5e`, and re-review `GO`. The stage
> is awaiting user acceptance; S2 remains unauthorized.

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
   and freeze SHA-256
   `d16479c0830460418d27d3595a513232a688cb8bc75173b53f2f7f068f6c5382`.
2. Add `0002_reconcile_current.sql` from the exact prior `postgresSchema` SQL
   body. It remains idempotent and preserves the five compatibility DML
   statements, while adding the preflight and postconditions below. Its frozen
   SHA-256 is
   `2c1cdfc20123dfdeffe6ef72c20decef78ca2fa75287b5985ed18e36e18dd0ed`.
3. Append `0003_validate_legacy_chat_keys.sql` without changing applied
   migration 0002. It rejects session and non-empty message natural-key
   ambiguity produced by legacy adoption. Its frozen SHA-256 is
   `709cdc2063f99ded33ed0714a3e7e418ec936fdfeec7fb50b596f9e8aee5addc`.
4. Embed the ordered files with `go:embed` and execute them from Gateway Store
   startup.
5. Remove the Go `postgresSchema` domain DDL and stop copying a separate schema
   directory into the PostgreSQL image.
6. Use the same runner for a fresh database and an existing SparkClaw database.

The runner may contain only the minimal migration-ledger bootstrap DDL. It may
not restate application tables or indexes in Go. The S0 manifest guard changes
from root-SQL-versus-Go comparison to validation of the ordered embedded
migration set before the Go constant is deleted.

## Migration Ledger

`sparkclaw_schema_migrations` stores immutable version, filename, SHA-256
checksum, and application timestamp.

- filenames are exactly `NNNN_name.sql`; numeric versions are unique and match
  lexical order;
- an applied version with a different checksum fails startup;
- an unknown applied version fails startup;
- an applied set must be a complete prefix; a gap fails startup;
- all pending SQL, DML postconditions, final schema validation, and their ledger
  rows commit in one transaction, so a failed adoption leaves no partial ledger
  or schema change;
- restart after success is idempotent;
- readiness remains false until migration and post-migration validation pass.

The runner acquires one dedicated pool connection and session advisory lock key
`6003381055699113777` before creating or reading the ledger. Lock acquisition,
ledger inspection, adoption, validation, and commit all use the startup context.
The lock is held across the complete migration set. Unlock uses a separate
five-second cleanup context; if unlock cannot be confirmed, the physical
connection is closed instead of being returned to the pool.

S1 is a non-rolling Store upgrade: all old Gateway processes must stop before
the new Gateway starts migration. As an enforceable backstop,
`0002_reconcile_current.sql` acquires `SHARE ROW EXCLUSIVE` transaction locks on
`weixin_chat_sessions`, `weixin_chat_messages`, `external_chat_sessions`, and
`external_chat_messages` after creating them and before compatibility preflight.
The locks block concurrent inserts, updates, and deletes through ledger commit.
The runner advisory lock serializes new runners; the table locks contain any
old writer that violates the deployment rule.

For an existing database without a ledger, the runner treats the database as an
unversioned adoption candidate. It applies the complete embedded migration set
in one transaction, validates the final catalog against a scratch schema built
from the same embedded SQL, and only then inserts every ledger row. Catalog
comparison covers exact expected tables, columns, types, defaults, nullability,
PK/FK/UNIQUE/CHECK constraints, and named index definitions. Unexpected columns
or changed definitions on an expected object fail closed; unrelated tables are
not adopted or deleted. The scratch schema is transaction-local cleanup and is
never a second schema authority. Presence of any single table is not evidence
of successful adoption.

### Compatibility DML Adoption

`0002_reconcile_current.sql` owns the five historical DML statements, while
`0003_validate_legacy_chat_keys.sql` owns the appended ambiguity postcondition.
Together they make the adoption contract explicit:

- target primary-key existence is the already-copied criterion. Once the target
  ID exists, `external_chat_*` is authoritative and the migration never compares
  or overwrites its owner/workspace/binding/channel/provider/external IDs,
  display/status/cursor/context/content/error/run-link, or timestamp fields;
- a missing session target ID is copied from the legacy source. Before that
  insert, an existing different target ID with the canonical
  `(binding_id, external_chat_id=external_user_id, external_thread_id='')`
  fails adoption;
- a missing message target ID is copied from the legacy source. Before that
  insert, a non-empty canonical `(chat_session_id, external_message_id)` already
  held by a different target ID fails adoption;
- after copy, 0003 joins canonical targets to their legacy source IDs and rejects
  duplicate session keys or duplicate non-empty message keys only where the
  canonical key still equals the legacy projection. A same-ID target whose key
  already evolved remains authoritative and is excluded from this check;
- both copy statements retain `ON CONFLICT (id) DO NOTHING`, then require every
  source ID to have exactly one target ID and require that matched count to equal
  the source count. The transaction locks make every newly inserted target an
  exact result of the canonical `INSERT ... SELECT`; pre-existing targets may
  legitimately differ in every mutable field and are not copy postconditions;
- `waiting -> waiting_owner/schema_version=2` and
  `resuming -> validating_visible/schema_version=2` are repeatable mappings;
  postconditions reject any remaining legacy status and any resulting target
  status whose schema version is not 2;
- `version <= 0 -> 1` is repeatable and its postcondition rejects any remaining
  non-positive version.

All preflight checks, DML, postconditions, schema validation, and ledger rows are
inside the same transaction. A conflict preserves the unversioned database and
returns a safe startup error instead of silently accepting divergent data.

The S0 inventory supplies the exact
[constraint-aware reconciliation manifest](store-s0-postgresql-reconciliation-manifest.md).
The migration review compares that manifest against both the old root SQL and
current `postgresSchema` before either source is removed.

## PostgreSQL Operation Bounds

Startup migration uses the caller startup context. `StateConfig` adds
`startup_timeout_seconds` with environment override
`SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS`, default 180 seconds, and inclusive
range 1 through 900 seconds. `config.Load` validates it; the assembly root
derives the bounded startup context and passes it through `newStore` to
`NewPostgresStore`. Direct constructor callers without an earlier deadline get
the same 180-second fallback. Cancellation before pool or lock acquisition
starts no migration work.

Pool acquisition, advisory-lock acquisition, begin, every statement, catalog
scan, validation, commit, and reconciliation inherit the effective context.
Each migration transaction sets server-side `statement_timeout` to no more than
the remaining startup deadline and `lock_timeout` to 30 seconds. These are
backstops, not substitutes for caller cancellation; the advisory lock wait is
bounded directly by the startup context.

Ordinary read/write/transaction timeout fields are introduced only when the
pilot repository uses them in S2, so configuration does not advertise inert
behavior. Their accepted names and initial values come from S0.

## Store Configuration Validation

`config.Load` validates only Store-owned settings in this stage:

- `state.backend` and `SPARKCLAW_STATE_BACKEND` normalize case and whitespace,
  then must be exactly `memory`, `file`, or `postgres`;
- File requires a non-empty absolute normalized `state.path`;
- PostgreSQL requires a non-empty trimmed DSN. The canonical
  `SPARKCLAW_STATE_DSN` is loaded first and the existing legacy
  `SPARKCLAW_POSTGRES_DSN` retains its current override precedence when both are
  present;
- when File encryption is enabled, exactly one of the direct key or key-file
  source must be set. A key file is normalized, readable, and non-empty during
  `config.Load`; both sources or neither source fail;
- `state.startup_timeout_seconds` uses the 180-second default and 1..900 bound;
- malformed Store boolean/integer overrides return an error naming the exact
  environment variable. The encryption boolean accepts the existing true forms
  `1/true/yes/on/required` and false forms `0/false/no/off`, case-insensitively,
  and rejects all other values;
- DSNs and keys remain absent from public projections and logs.

Global cleanup of every permissive environment parser and artifact object
backend construction are separate work. This stage must not expand into a
whole-config rewrite.

## Database Failure Contract

The migration runner returns and preserves every pool, lock, begin, execution,
scanning, rows, validation, commit, and rollback error. Rollback errors join the
primary diagnostic without replacing it.

If commit returns a transport or cancellation error after submission, the old
physical connection is destroyed so its session lock cannot leak. With the
remaining startup context, the runner acquires a new connection and the same
advisory lock, then reads the complete ledger and reruns the read-only final
catalog/DML postconditions:

- exact versions, filenames, checksums, and postconditions mean `committed`;
- no new ledger rows after the lock is reacquired means PostgreSQL resolved the
  atomic transaction as `not_committed`; one bounded retry is allowed;
- a partial/mismatched ledger, failed postcondition, or inability to reacquire
  and inspect remains `unknown_outcome` and fails startup without retry.

`normalizeMCPBindingSessions` remains a separate, idempotent startup data
normalization after the schema runner. It uses the same bounded context and
fails startup on error, but it is not represented as a schema migration or
ledger row.

## Verification

Required deterministic and configured-PostgreSQL evidence:

- embedded migration ordering and checksum unit tests;
- source guard proving no domain DDL remains in Go;
- fresh empty database reaches the expected manifest;
- current unversioned database adopts the ledger without data loss;
- missing-ID natural-key Weixin copy conflicts fail without writing a ledger;
- duplicate legacy session keys and duplicate non-empty legacy message keys
  fail adoption without writing a ledger;
- evolved target rows survive adoption unchanged, while a missing target ID
  colliding with another target's canonical natural key fails closed;
- all five compatibility DML postconditions are proved and repeated adoption is
  stable;
- concurrent startup runners serialize on the fixed advisory lock;
- compatibility-table writes block until migration commits, and the documented
  deployment path stops old Gateways before starting the new binary;
- current versioned database restarts without DDL churn;
- changed checksum and unknown version fail startup;
- a forced migration failure rolls back and leaves no ledger row;
- injected commit uncertainty proves committed, not-committed bounded retry,
  and unresolved unknown-outcome branches;
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
| Design review 1 | draft dated 2026-08-19 | `REVISE` | Missing compatibility-DML adoption criteria, advisory lock, uncertain-commit state machine, and exact configuration/context entry path | Independent gatekeeper / 2026-08-20 |
| Design review 2 | revision 1 dated 2026-08-20 | `REVISE` | Full-field legacy-copy equality rejected valid evolved targets; runner lock did not exclude old-Gateway compatibility writes | Independent gatekeeper / 2026-08-20 |
| Design review 3 | revision 2 dated 2026-08-20 | `GO` | Target-PK already-copied authority and non-rolling/four-table lock protocol close the remaining adoption and old-writer windows; no second schema authority introduced | Independent gatekeeper; user authorized stage progression / 2026-08-20 |
| Implementation review 1 | `0c557ee` through `3f098f3` | `REVISE` | Static review and PostgreSQL 18.4 tests found that two missing legacy IDs could share one session or non-empty message natural key and both enter non-unique canonical indexes | Independent gatekeeper / 2026-08-20 |
| Implementation review 2 | fix `74b7c5e` | `GO` | Immutable 0002 retained; transactional 0003 rejects both duplicate classes with ledger remaining empty, while evolved same-ID canonical targets remain authoritative. Full Go build/test/vet, Store race, WebChat, Compose/scripts, bilingual docs, and real PostgreSQL evidence are green. Remaining low risk: the combined duplicate-source/evolved-target case is covered by SQL predicates plus separate tests rather than one dedicated test | Independent gatekeeper / 2026-08-20 |
