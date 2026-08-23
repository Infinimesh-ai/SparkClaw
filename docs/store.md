# Store

> Language: English | [简体中文](../zh-cn/docs/store.md)

The Store package is SparkClaw's durable-state boundary. It exposes small,
typed repositories to business owners, implements those contracts on memory,
file, and PostgreSQL backends, and gives the Gateway assembly layer one runtime
for backend construction, supervision, readiness, metrics, recovery probes,
and shutdown.

This document describes the current implementation. It replaces the completed
Store migration plans and acceptance records.

## Ownership Boundary

Repository interfaces live in `services/gateway/internal/store/store.go`.
Consumers depend on only the repositories they use; there is no broad
`store.Store` interface. The current repository set is:

- identity and access: `OwnerRepository`, `ClientRepository`,
  `CredentialRepository`, and `SessionRepository`;
- messaging: `ConversationRepository`, `ConnectorRepository`,
  `DeliveryRecordRepository`, `ExternalChatRepository`, and
  `PassiveNotificationRepository`;
- execution and governance: `RunRepository`, `ApprovalRepository`,
  `AuditRepository`, `MCPRepository`, and `ISCPOnboardingRepository`;
- owner data: `DocumentRepository`, `MemoryRepository`, and
  `ScheduleRepository`;
- support records: `EvaluationRepository`, `ArtifactMetadataRepository`, and
  `BrowserStateRepository`.

`store.Runtime` is assembly-only. `cmd/sparkclaw` selects one backend, obtains
the typed repositories from Runtime, and injects the smallest required
interfaces into Agent, Gateway, ToolHub, connector, delivery, schedule, and
other owners. Runtime does not forward repository methods and must not escape
the assembly package.

## Risk And Aggregate Policy

Reliability follows the effect of an operation, not the size of its repository.
Do not apply the most expensive protocol to every record.

| Tier | Operations | Required evidence |
|---|---|---|
| P0 | Run/ToolCall, delivery, MCP, credential, connector, approval, and session deletion | Transactional aggregate where one local invariant spans records, stable idempotency or conditional identity, explicit unknown-outcome recovery, deterministic failure injection, real PostgreSQL coverage, and race coverage |
| P1 | Document, schedule, external chat, and passive notification | Explicit errors, caller context propagation, three-backend parity, and a transaction only for a cross-record invariant |
| P2 | Ordinary configuration, display metadata, and low-risk queries | Small typed repository, backend contract coverage, and no recovery protocol without a demonstrated invariant |

A repository may contain methods from more than one tier. For example, session
deletion is P0 because it closes dependent lifecycle state, while a session
lookup is an ordinary read. Before adding a transaction, identify the exact
records that must change together. Before adding idempotency or reconciliation,
identify the stable operation or candidate identity that proves the outcome.

## Repository Contract

Every repository method accepts `context.Context` first and returns `error`
last. Callers propagate their request, worker, or shutdown context and inspect
the error; production code must not replace it with `context.Background()` or
discard it. A lookup never turns backend failure into absence or an empty
list.

Repository interfaces expose no pgx, SQL, filesystem, encryption, or
supervisor types. A consumer that needs several repositories declares a local
composite interface next to its own code instead of widening `store`.

The package exposes bounded error codes through `StoreError`:

| Code | Meaning |
|---|---|
| `not_found` | The required record does not exist. Optional lookups normally use `(record, found, error)` instead. |
| `conflict` | A version, condition, idempotency key, or lifecycle precondition no longer matches. |
| `invalid` | The command or persisted state violates its typed contract. |
| `canceled` / `timeout` | The caller canceled the operation or its finite Store budget expired. |
| `unavailable` | The backend rejected or could not start a safely retryable operation. |
| `durability_failed` | The mutation definitely did not become the durable Store state. |
| `unknown_outcome` | Submission may have committed; the caller must reconcile by stable identity before retrying. |
| `corrupt` | Persisted state differs from every valid expected state. |
| `internal` | A classified internal failure that does not fit a more specific public code. |

Store errors preserve their raw causes for internal diagnostics and support
`errors.Is`/`errors.As`; public projections expose only the bounded codes and
safe copy. A caller cancellation is reported as `canceled` and never relabeled
as `timeout`.

Mutations return the canonical persisted record. If a backend has produced a
candidate but cannot prove whether it committed, it returns that candidate with
`unknown_outcome`; repository-specific reconciliation compares a stable ID,
version, idempotency key, or complete normalized record before reporting
success. Definite failures return no candidate and restore the prior local
state.

Validation, normalization, cloning, conditional commands, replay comparison,
and reconciliation belong in `*_contract.go` files so all backends implement
the same semantics. Backend files own storage mechanics, not competing domain
rules.

## Backends

### Memory

`MemoryStore` is the semantic reference implementation and deterministic test
backend. It owns normalized in-memory maps behind locks and returns defensive
copies where records contain mutable data. It is intentionally non-durable, so
a durability outcome does not make its Runtime unready.

### File

`FileStore` is a write-through decorator around `MemoryStore`. All operations
enter one context-aware admission gate: reads take shared capacity and commands
take the full capacity. A read therefore observes either the complete state
before a command or the complete state after it committed durably, never a
tentative in-memory mutation. A command captures the complete rollback state,
applies the memory mutation, encodes one snapshot, and commits it through a
unique same-directory temporary file created with mode `0600`:

```text
encode -> create temp -> write all -> fsync temp -> close
       -> rename over destination -> fsync parent directory
```

A failure before replacement is definite and restores the captured state. A
failure at or after replacement is `unknown_outcome`; it installs an in-process
fence. The next admitted operation reads the destination and compares its digest
with the candidate and previous snapshots. It then accepts the candidate,
restores the previous state, or marks an unexpected third state as `corrupt`.
No operation passes the fence before reconciliation completes.

Optional AES-256-GCM snapshot encryption is configured at startup. Existing
plaintext snapshots remain readable when encryption is enabled and are written
as encrypted envelopes on the next mutation. An encrypted snapshot is rejected
when no encryption configuration is present.

File admission coordinates one Gateway process. Two processes must not write
the same state path concurrently; use PostgreSQL when multiple writers are
required.

### PostgreSQL

`PostgresStore` uses a shared `pgxpool`. Cross-record P0 commands execute on one
acquired session and transaction where the local invariant requires it. The
error classifier distinguishes failures known not to have been submitted from
failures after possible submission. An uncertain session is terminated rather
than returned to the pool, and the command returns `unknown_outcome` with its
candidate when one exists.

Gateway is the sole application-schema authority. Ordered SQL files under
`internal/store/migrations` are embedded in the binary. Startup:

1. acquires the fixed PostgreSQL advisory lock;
2. creates or reads `sparkclaw_schema_migrations`;
3. verifies that recorded versions are a complete prefix with immutable
   filenames and SHA-256 checksums;
4. applies pending SQL and supported pre-ledger compatibility adoption in one
   transaction;
5. builds an expected catalog in a scratch schema and compares tables, columns,
   constraints, indexes, and predicates;
6. records ledger rows and commits, reconciling an uncertain commit once before
   a bounded retry.

Checksum drift, unknown or gapped versions, ambiguous legacy identities, and
catalog drift fail startup. PostgreSQL integration tests retain the existing
`SPARKCLAW_TEST_POSTGRES_DSN` gate and skip when it is not configured.

## Configuration

Store settings fail during `config.Load`, not at the first persistence
operation: the state backend must be exactly `memory`, `file`, or `postgres`;
the File backend requires a non-empty state path; PostgreSQL requires a
non-empty DSN; File encryption requires exactly one usable key source. Tests
that need non-durable state use `MemoryStore`, not a pathless File Store. DSNs
and encryption keys never appear in public projections or logs.

## Runtime And Supervision

`store.NewRuntime` constructs exactly one backend and repository set. Startup
is successful only after the backend probe passes. The runtime owns:

- default read, write, and transaction budgets of 10, 30, and 60 seconds,
  with a 180-second budget for startup and schema operations;
- one finite-operation registry mapping every method to repository, mode, and
  timeout class;
- active-operation admission, bounded outcome counters, and total duration;
- readiness state and periodic recovery probes;
- close admission, drain, and backend cleanup.

The states are `starting`, `ready`, `unready`, `closing`, and `closed`. A corrupt
outcome immediately makes Runtime unready. On a durable backend,
`durability_failed` or `unknown_outcome` also makes it unready. Three consecutive
`timeout` or `unavailable` outcomes for the same operation make it unready.
Only a successful recovery probe restores readiness and clears failure
streaks; unrelated successful operations do not clear degradation.

Memory probes verify initialized maps. File probes perform and clean up a
write/fsync/rename/directory-fsync/read cycle beside the snapshot. PostgreSQL
probes ping the pool and verify the complete migration ledger. Probe diagnostics
remain internal; public readiness exposes only the bounded runtime state and
reason code.

`/readyz` fails closed when Store is unready. `/metrics` exports only bounded
backend, repository, operation, mode, and outcome labels through
`sparkclaw_store_*` metrics. It never exports paths, DSNs, owners, record IDs,
or raw errors. Store telemetry never writes through the Store being
supervised.

At shutdown Runtime stops recovery, rejects new operations, waits for admitted
operations within the close context, and then closes the backend. Close is
idempotent and bounded. Callers must not retain a repository past Runtime
shutdown.

## Source Layout

Store code is organized by repository and backend:

```text
store.go                         repository interfaces and compile-time parity
operation.go                     finite operation/error registry
runtime.go / supervisor.go       assembly and lifecycle supervision
probe.go                         backend probes
<repository>_contract.go         shared semantics and command types
<repository>_memory.go           Memory implementation
<repository>_file.go             File admission and durable command wrapper
<repository>_postgres.go         PostgreSQL implementation
<repository>_*_test.go           contract, durability, failure, and guard tests
file.go / postgres.go            backend construction and shared primitives
file_durability.go               replacement, rollback, fence, reconciliation
postgres_migrations.go           embedded schema ledger and catalog validation
```

Large cohesive registries may exceed the normal file-size guideline when
splitting would duplicate one authority. `operation.go`, `mcp_access.go`, and
`mcp_access_postgres.go` are the current documented exceptions; unrelated
repository behavior must not be added to them.

## Changing Store

For a new method or record:

1. assign the operation a risk tier and state the aggregate invariant;
2. add or extend the smallest repository interface and shared contract;
3. register one operation ID, mode, timeout class, and repository owner;
4. implement Memory, File, and PostgreSQL in the same change;
5. update File `Snapshot` when the record is snapshot-backed;
6. migrate callers to the narrow interface and propagated context;
7. add parity tests, then add only the failure injection, reconciliation,
   PostgreSQL, and race evidence required by the tier;
8. retain source guards that reject broad Store dependencies, missing backend
   methods, ignored errors, and unbounded contexts.

Do not introduce an optional type assertion that silently removes a capability,
a second broad Store facade, per-caller backend branching, or a recovery
protocol without a stable proof identity.

## Verification

The proportional local gate is:

```bash
cd services/gateway
go test ./internal/store
go test -race ./internal/store
go build ./...
go vet ./...
go test ./...
```

When PostgreSQL is available, keep the existing opt-in contract:

```bash
SPARKCLAW_TEST_POSTGRES_DSN='postgres://...' go test ./internal/store
```

Changes that affect callers also run their focused package tests. The final
repository gate includes the full Go race suite, WebChat tests and production
build, ASR fake-model protocol tests, and bilingual documentation mirror/link
checks.
