# Store Repository Migration Design

> Language: English | [简体中文](../zh-cn/docs/store-repository-migration-design.md)

> Status: S2 pilot implementation re-review is pending at `437e4bc` after a
> fresh context-isolated review found a ticket-expiry disclosure defect. S3 is
> paused; `OwnerRepository` remains the next wave only after S2 receives `GO`.

## Objective And Stage Boundary

Replace the 141-method `store.Store` with reviewed domain repositories in small
implementation waves. S2 migrates only the accepted
`ISCPOnboardingRepository` pilot while proving the File transaction model. S3
migrates every remaining repository one at a time. S4 deletes the temporary
broad interface. Runtime and Supervisor work remains S5.

S2 does not split `file.go`, `memory.go`, `postgres.go`, or another large Store
module by responsibility. File size and responsibility splitting is reviewed
only after Store migration and supervision are complete.

## Unit Of Migration

One repository is one implementation stage and normally one behavior-change
commit. A completed stage must:

1. confirm its accepted S0 method, command, reconciliation, and consumer rows;
2. define one typed repository interface and all-backend assertions;
3. add caller context and an error result to every backend-fallible method;
4. update Memory, File, PostgreSQL, and File `Snapshot` only when the record
   shape requires it;
5. update every caller to pass its request, operation, worker, startup, or
   shutdown context;
6. add shared contract, File failure, PostgreSQL classification, timeout,
   cancellation, and race tests as applicable;
7. remove the old signatures for that repository; and
8. receive implementation review before another repository begins.

No compatibility adapter, optional type assertion, duplicate method, dynamic
repository map, string-based dispatch, or `context.Background()` may survive in
a completed repository path.

## S2 Pilot Contract

The interface introduced in `internal/store` is:

```go
type ISCPOnboardingRepository interface {
    SaveISCPOnboarding(context.Context, app.ISCPOnboarding) (app.ISCPOnboarding, error)
    GetISCPOnboarding(context.Context, string) (app.ISCPOnboarding, bool, error)
    ListISCPOnboardings(context.Context, string) ([]app.ISCPOnboarding, error)
}
```

The contract is:

- save validates and normalizes the receipt, creates it exactly once by ID, and
  preserves `errors.Is(err, ErrISCPOnboardingConflict)` for a duplicate;
- get returns `(zero, false, nil)` only for normal absence;
- list returns an empty non-nil slice and nil only for a successful empty
  result, ordered newest `CreatedAt` first with deterministic ID tie-break;
- cancellation and backend failure are errors, never absence or an empty
  successful list;
- returned receipts contain scalar/time fields and therefore expose no mutable
  backend alias;
- the repository owns no audit row. `iscppairing.Service` remains the caller
  that appends `iscp.onboarding.ticket_issued` after a successful receipt save.

The ID is the idempotency/conflict boundary. `iscppairing.Service`, rather than
the HTTP caller, owns the ID and the still-undisclosed issued ticket while a
save is uncertain. It reconciles with `GetISCPOnboarding(ctx, id)` and must not
call the authority or generate another save for that owner until reconciliation
completes.

## Finite Operation Boundary

S2 introduces package-private finite operation metadata used immediately by
the pilot:

| Operation ID | Mode | Timeout class | Reconciliation |
|---|---|---|---|
| `iscp_onboarding.save` | write | write | get by ID |
| `iscp_onboarding.get` | read | read | self |
| `iscp_onboarding.list` | read | read | none |

An `OperationSpec` binds ID, repository ID, method/mode, and timeout class.
Registration tests reject duplicate IDs, missing pilot methods, unknown timeout
classes, and an unreferenced spec. Operation IDs are constants; record IDs,
owner IDs, queries, paths, DSNs, and content never become operation names or
labels.

During S2-S3 this boundary owns only deadline composition and typed error
classification. It exposes no health, metrics, repository lookup, Runtime, or
Supervisor. S5 wraps these exact call sites rather than changing repository
signatures again.

## Timeout Configuration

Only timeout classes consumed by the S2 pilot are introduced:

| Typed field | JSON | Environment | Default | Valid range |
|---|---|---|---|---|
| `StateConfig.ReadTimeoutSeconds` | `state.read_timeout_seconds` | `SPARKCLAW_STATE_READ_TIMEOUT_SECONDS` | 10 seconds | 1-900 seconds |
| `StateConfig.WriteTimeoutSeconds` | `state.write_timeout_seconds` | `SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS` | 30 seconds | 1-900 seconds |

Malformed environment values fail `config.Load` and name the exact variable;
out-of-range file or environment values fail normalization and name the JSON
field. The values are added to `config.Default`,
`configs/sparkclaw.default.json`, File/Memory/PostgreSQL backend options, command
assembly, and configuration tests in the same pilot commit. They are not
credentials and may appear in the existing public redacted configuration only
if that projection already exposes the `state` object; S2 does not create a new
public Store settings endpoint.

The effective deadline is the earlier non-zero caller deadline or configured
fallback. `context.Canceled` is never relabeled as timeout. The 60-second
multi-record transaction setting is not added in S2 because the pilot changes
only one record. PostgreSQL uses the 30-second write context for its transaction-
scoped resolution barrier below; the multi-record setting enters with the first
S3 repository that consumes it. The accepted 180-second startup setting remains
unchanged.

Existing convenience constructors use the accepted defaults so tests and local
callers remain deterministic. Production assembly passes the validated config
explicitly through backend options. There is no package-global mutable timeout.

## Typed Error Contract

The pilot introduces `StoreError` and a finite `StoreErrorCode` matching the S0
contract:

| Code | Pilot use |
|---|---|
| `not_found` | reserved for commands requiring a target; onboarding get uses normal absence instead |
| `conflict` | duplicate onboarding ID, while preserving `ErrISCPOnboardingConflict` |
| `invalid` | deterministic receipt contract violation |
| `canceled` | caller context canceled before a known completed effect |
| `timeout` | effective deadline exceeded before a known completed effect |
| `unavailable` | backend cannot currently serve the operation |
| `durability_failed` | File candidate definitely failed before/at submission and Memory was restored |
| `unknown_outcome` | submitted File/PostgreSQL effect requires reconciliation |
| `corrupt` | persisted payload cannot be decoded or violates receipt invariants |
| `internal` | unclassified failure; fail closed and review classification |

`StoreError` contains the code, finite operation ID, and wrapped cause. It
implements `Unwrap`; helpers support `errors.As`, stable code extraction, and
safe classification without parsing strings. It never includes record data in
labels or public copy. Domain sentinels remain in the chain. Backend-specific
errors such as `pgconn.PgError`, filesystem paths, DSNs, and raw payloads remain
internal causes.

## Backend Rules

### Memory

- check effective context before lock acquisition and again under the lock
  immediately before mutation or read;
- preserve normalization, duplicate, ordering, and scope behavior;
- mutate the receipt and no caller-owned audit under one lock;
- return an empty non-nil successful list;
- never return backend-owned mutable data.

Memory's existing mutex is not replaced for the pilot. Its critical sections
are in-process and contain no I/O; cancellation that occurs while waiting is
observed under the lock before any effect.

### File

- use the accepted [File Store durability](store-file-durability-design.md)
  gate and command state machine;
- acquire read/write admission with the effective caller context;
- return success only after replacement and parent-directory sync;
- restore the complete pre-snapshot plus volatile sidecar for a definite
  pre-submit failure;
- return and fence `unknown_outcome` after an uncertain submitted replacement;
- make get/list perform the defined reconciliation before returning data;
- reject a missing state path and retain snapshot/encryption schema.

### PostgreSQL

- pass the effective context to pool acquisition, every `Exec`, `Query`,
  `QueryRow`, scan loop,
  and reconciliation call; the pilot files contain no `context.Background()`;
- map SQLSTATE `23505` to conflict while preserving
  `ErrISCPOnboardingConflict`;
- map `pgx.ErrNoRows` to normal absence only in get;
- return query, scan, JSON decode/validation, and `rows.Err()` failures;
- never turn a failed query into a successful empty list;
- use one explicit transaction for the one-row save and its resolution lock; no
  caller-owned audit is included;
- classify a context outcome using both the effective context and PostgreSQL
  cause, without string matching.

Save and get share a transaction-scoped PostgreSQL advisory-lock protocol keyed
by the onboarding ID. The key is the signed 64-bit value formed from the first
eight bytes of SHA-256 over a fixed namespace plus the ID. A collision can only
serialize unrelated IDs; it cannot merge or authorize their data.

Save does not call `pgxpool.Exec` directly. It acquires one pool connection with
the effective write context, begins an explicit transaction, obtains
`pg_advisory_xact_lock($1)`, inserts the receipt, and commits. The transaction
uses the 30-second write class because it changes one record; it does not
activate the future multi-record transaction setting. The production
classification is frozen as:

| Stage/result | Classification | Retry rule |
|---|---|---|
| context ends or pool acquire/begin fails before a transaction exists | `canceled`, `timeout`, or `unavailable` | no insert transaction exists; ordinary retry allowed |
| lock or insert returns server `*pgconn.PgError` | SQLSTATE `23505` is `conflict`; other server rejections map to their definite code, then rollback | PostgreSQL rejected the statement; no unknown commit |
| pre-commit error satisfies `pgconn.SafeToRetry(err)` | `canceled`, `timeout`, or `unavailable` from context/cause, then rollback | the failing statement was not sent; the insert cannot commit without a later commit |
| unsafe lock/insert transport error or any commit error | terminate the owned session and return `unknown_outcome` | transaction state or commit is uncertain; barrier get-by-ID required |
| commit succeeds | success | transaction completed |

Every definite pre-commit branch attempts rollback. If rollback itself fails,
Store hijacks and closes the session before returning; because no successful
insert can be followed by a commit on those branches, their record effect
remains definite even though the cleanup failure is retained as a cause.

On an unsafe error, Store takes ownership with `pgxpool.Conn.Hijack`, then calls
`PgConn.Close` with a five-second context derived from
`context.WithoutCancel` of the operation context. `PgConn.Close` always closes
the underlying network connection even when its clean-close context fails. The
close result is retained only as internal diagnostic evidence; it never proves
commit or rollback.

Get-by-ID is the resolution barrier. It acquires a different pool connection
and explicitly begins with
`pgx.TxOptions{IsoLevel: pgx.ReadCommitted}` regardless of DSN, role, or database
default isolation. It obtains the same transaction-scoped advisory lock in one
statement, then selects and validates the row in a separate second statement.
PostgreSQL cannot grant the lock until the original transaction/session
releases it. Under explicit `READ COMMITTED`, the second statement takes a new
snapshot after that release. Therefore a row found after the lock is a committed
result, and absence after the lock is a final rollback/absence result even when
the configured server default is `REPEATABLE READ` or `SERIALIZABLE`.

Combining lock and query into one statement, omitting `TxOptions`, or accepting
a pre-lock snapshot is forbidden. If acquire, lock, query, or transaction
completion cannot finish within the effective read context, get returns an
error and the service retains pending `unknown_outcome`; a pre-barrier absence
is never reported.

In particular, a deadline/cancellation error after a transaction exists is not
labeled `timeout`/`canceled` when `SafeToRetry` is false, and immediate negative
reconciliation is not trusted without the lock barrier. A package-private
acquire/begin/lock/exec/commit seam drives every row of this protocol without
replacing real-DSN evidence. The configured integration test proves real
insert, duplicate, barrier absence/found results, list/rows handling,
cancellation before pool acquisition, and restart.

## Consumer And Assembly Migration

`iscppairing.Service` stops storing `store.Store`. It accepts a
consumer-owned minimal composite containing `store.ISCPOnboardingRepository`
and the existing caller-owned audit append capability. This is temporary only
for audit because `AuditRepository` is an S3 stage; it does not grant the
service unrelated Store methods.

Consumer changes are exact:

- `Start` passes its request context to save;
- `Start` uses a context-aware capacity-one service admission before any
  authority call, which is sufficient for SparkClaw's single-owner product and
  prevents concurrent requests from issuing parallel tickets;
- after the authority returns but before save begins, the service publishes an
  in-memory pending record containing owner, onboarding ID, normalized request
  fingerprint, receipt, and still-undisclosed signed ticket; the value is never
  persisted, logged, audited, or projected. The fingerprint is a SHA-256 digest
  of canonical owner, normalized display name, effective TTL, configured
  domain, and expected ticket type;
- a definite save failure clears pending, discards the signed ticket, and
  returns without another authority call. A later explicit Start is a new
  logical attempt and may issue a fresh ticket;
- if save returns `unknown_outcome`, `Start` immediately attempts get-by-ID
  reconciliation within the remaining request context. A confirmed receipt
  completes the original operation, appends the caller-owned audit, returns the
  original ticket once, and clears pending. Confirmed absence clears pending
  and returns a definite persistence failure without another authority call;
  unresolved reconciliation retains pending and returns a safe unavailable
  result;
- every later `Start` checks pending under the service admission before calling
  the authority. The same normalized request reconciles and, if committed,
  receives the retained original ticket; a different request receives a stable
  conflict until the pending operation resolves. No path issues a second
  authority ticket while pending exists;
- pending is bounded by the authority ticket expiry. If reconciliation first
  succeeds after expiry, the receipt remains visible but the expired signature
  is discarded and the service reports that a fresh explicit start is
  required; it does not issue that fresh ticket in the reconciliation call;
- `List` becomes `List(ctx, ownerID) ([]app.ISCPOnboarding, error)`;
- the GET handler passes `r.Context()` and does not serialize a backend failure
  as an empty `200` list;
- backend timeout maps to a stable gateway-timeout response, while unavailable,
  durability, unknown, corrupt, and internal failures map to a stable
  service-unavailable response without raw Store causes;
- conflict/invalid remain stable client/domain failures;
- assembly accepts the minimal composite at `newISCPPairingService`; only the
  backend factory and temporary broad assembly value retain `store.Store`.

The authority call still occurs before the receipt save because the receipt is
derived from the signed authority response. The pending coordinator closes the
same-process unknown-outcome retry window and can return the original ticket
after successful reconciliation. The authority contract still has no revocation
or idempotent request recovery. Therefore a definite local save failure, process
crash/restart after authority issuance, or ticket expiry before reconciliation
can strand an undisclosed remote ticket. Solving those remote/local atomicity
and crash-recovery cases requires an authority protocol change or a recoverable
authority request contract and is not hidden inside Store.

The audit append remains after successful receipt persistence and retains its
legacy failure behavior until `AuditRepository` migrates. The pilot does not
claim receipt-plus-audit atomicity because S0 explicitly assigns the audit to
the caller.

## Temporary Broad Interface

During S2 the broad interface embeds `ISCPOnboardingRepository` once and
declares only the other 138 legacy methods. It does not repeat the three
migrated signatures. `MemoryStore`, `FileStore`, and `PostgresStore` assert the
small interface independently as well as the temporary broad interface.

New production consumers may not accept `store.Store`. A source guard proves
that onboarding's old signatures and `context.Background()` calls are absent,
and that `iscppairing.Service` retains no broad Store field or constructor
parameter.

## S3 Planning Order

After S2 implementation and human acceptance, preferred risk order remains:

1. Owner, Client, Credential, and Session;
2. Conversation, Run, Document, Approval, Audit, Evaluation, and artifact
   metadata;
3. Schedule, Connector, Delivery Record, Passive Notification, and External
   Chat;
4. MCP, Browser State, and Memory.

Only one repository is active at a time. Session deletion, MCP redemption, and
other cross-record commands receive explicit transaction cases; a small
interface name does not make them simple CRUD.

## Pilot Verification And Commit Boundary

S2 implementation uses two independently reviewable commits:

1. mechanical all-method File admission, with no pilot signature or error
   behavior change;
2. timeout/error operation boundary plus the complete onboarding repository
   migration across three backends, consumers, assembly, and tests.

The pilot gate requires:

- shared Memory/File contract tests for success, absence, order/scope,
  duplicate conflict, cancellation, timeout, and non-nil empty list;
- default File injected-failure, rollback, fence, reconciliation, encryption,
  restart, and race tests from the File design;
- PostgreSQL unit classification for query/scan/rows/context and uncertain
  submission, including every acquired-connection/`SafeToRetry` table row, plus
  owned-session termination and both advisory-lock barrier results, plus real-
  DSN integration evidence. The barrier tests set the session/server default
  to `REPEATABLE READ` and `SERIALIZABLE` and still require the explicit
  `READ COMMITTED` two-statement result;
- `iscppairing` service tests proving context propagation, safe failure copy,
  no ticket disclosure after definite persistence failure, immediate and
  next-request unknown reconciliation, no second authority call, different-
  request conflict, pending expiry, concurrent Start serialization, and
  caller-owned audit order;
- Gateway tests proving `r.Context()` propagation and non-200 list failure;
- config default/environment/range/assembly tests for both new timeouts;
- source guards for one embedded interface, no old pilot signatures, no pilot
  `context.Background()`, no ignored pilot persistence/rows error, and no broad
  Store dependency in the consumer;
- `go test ./...`, `go build ./...`, `go vet ./...`, focused Store race, default
  File production-entry tests, WebChat tests/build, and bilingual docs CI;
- a real PostgreSQL run with the existing
  `SPARKCLAW_TEST_POSTGRES_DSN` opt-in. CI service topology and skip behavior
  remain unchanged.

S2 cannot receive implementation `GO` for interface scaffolding, the operation
registry, or File gate alone. The production caller and all three backends must
use the new contract.

## S4 Broad Store Removal

After every S3 repository implementation receives `GO`:

1. replace remaining constructor parameters and fields with minimum
   repositories or consumer-owned composites;
2. delete the broad interface and global backend assertions;
3. retain per-repository assertions for all three backends;
4. require zero production references to `store.Store`, repository type
   assertions, and dynamic repository maps; and
5. verify assembly still constructs one selected backend without a service
   locator.

S4 is reviewed independently. S5 supervision cannot start merely because the
last repository compiles.

## Rollback

The pilot does not change File snapshot or PostgreSQL schema shape. If rejected,
the behavior commit can be reverted without removing the independently accepted
mechanical gate, subject to its own review decision. S1 forward PostgreSQL
migrations remain in place.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| S2 pilot/S3 design review 1 | `3aff151` | `REVISE` | Uncertain save did not prevent a retried Start from issuing a second authority ticket, and PostgreSQL autocommit lacked a verifiable submission classifier | Independent gatekeeper / 2026-08-20 |
| S2 pilot/S3 design review 2 | `4f8b2e5` | `REVISE` | Immediate negative query after uncertain autocommit was not final because the original backend transaction could commit later | Independent gatekeeper / 2026-08-20 |
| S2 pilot/S3 design review 3 | `d88d321` | `REVISE` | Reconciliation did not freeze `READ COMMITTED`; a higher default isolation could retain the pre-lock snapshot and return false absence | Independent gatekeeper / 2026-08-20 |
| S2 pilot/S3 design review 4 | `49b0858` | `GO` | Explicit `READ COMMITTED` and separate advisory-lock/query statements make found/absence final independent of server isolation defaults; all earlier fence, pending-ticket, and submission findings are closed | Independent gatekeeper / 2026-08-20 |
| S2 pilot implementation initial review | `9d86c50` | superseded `GO` | Complete File admission and onboarding migration passed the initial evidence review; a later fresh review superseded this decision | Independent gatekeeper and primary agent under owner-delegated authority / 2026-08-20 |
| S2 pilot implementation fresh re-review | `9d86c50` | `REVISE` | A ticket could expire during persistence/reconciliation yet still be disclosed because completion reused the request-start time | Context-isolated gatekeeper / 2026-08-20 |
| S2 pilot repair candidate | `bc1bfb4`, `6f4c1bf`, `437e4bc` | pending | Completion now reads a live clock immediately before disclosure, with intra-call expiry coverage, fresh disposable real-PostgreSQL full/race runs, and verified Compose forwarding for read/write timeout overrides | Pending independent re-review / 2026-08-20 |
| Each repository implementation | pending | pending | one row per accepted repository is added during migration | pending |
| S4 Store removal | pending | pending | pending | pending |
