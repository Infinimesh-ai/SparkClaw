# Store S0 Baseline And Acceptance Report

> Language: English | [简体中文](../zh-cn/docs/store-s0-acceptance-report.md)

> Status: candidate for human-assisted S0 implementation review, 2026-08-20.
> The user authorized S0 to start on 2026-08-20. The implementation decision
> remains `pending`; this report authorizes neither S1 nor S2.

## Conclusion

S0 has a complete executable 141-method/20-repository catalog, a production
consumer matrix guarded across 58 direct declaration sites, 10 named local
interfaces, and two anonymous local interfaces, command/reconciliation
evidence, an executable PostgreSQL source manifest, and a complete guarded
20-repository by 10-dimension characterization matrix. The candidate
recommendation is **GO for human S0 implementation review**, with the actual
review record deliberately left pending. Work must stop at this candidate
until the user records `GO`, `REVISE`, or `STOP`.

## Entry Baseline

The baseline was recorded before S0 code or evidence documents were edited.

| Item | Result |
|---|---|
| Commit | `df05cf58a6804c8eb4b1434a18044728c3ec2c8e` (`df05cf5`), detached worktree |
| Pre-existing worktree changes | Modified bilingual documentation indexes and 14 untracked staged-design documents (seven English and seven Chinese); preserved throughout |
| Host | Linux ARM64; Go `1.25.5`; Node `26.2.0`; npm `11.17.0`; Python `3.12.3` |
| Document setup | `npm run setup:document-tools` passed; 179 Node packages installed and Python document dependencies confirmed |
| Backend build | `cd services/gateway && go build ./...` passed |
| Full backend tests | `cd services/gateway && go test ./...` passed; document ToolHub package completed in 44.260 seconds |
| Store baseline | `go test ./internal/store -count=1 -v` passed for Memory/File; all 9 existing PostgreSQL tests skipped with their unchanged DSN gate |
| PostgreSQL gate | `SPARKCLAW_TEST_POSTGRES_DSN` was unset; PostgreSQL is **not run**, not passed |
| WebChat build | `npm --workspace @sparkclaw/webchat run build` passed; Vite produced 55.93 kB CSS and 380.59 kB JS |

No baseline failure was found. The PostgreSQL evidence gap is retained as a
risk, not converted into a success claim.

## S0 Characterization Evidence

`s0_contract_characterization_test.go` adds the representative backend-neutral
harness and static contract checks. `s0_repository_characterization_test.go`
maps each applicable success, normal-absence, order/scope, duplicate, and
conflict/delete dimension to its own assertion branch across all 20
repositories on Memory and File, records current mutable-alias defects, and
fills the two previously missing File restart cases. `s0_repository_lifecycle_test.go`
explicitly reads and asserts lifecycle audit/event evidence for the 18
repositories that own it; ISCP onboarding and MCP retain concrete caller-owned
lifecycle `N/A` rationales. `s0_repository_evidence_test.go` owns the complete
matrix and its exact-subtest-path/document guard. `s0_postgres_manifest_test.go`
compares the two current schema authorities without changing either.

The shared harness is representative, but it does not replace the accepted
per-repository gate. The S0 inventory contains a complete 20-repository by
10-dimension applicability/evidence matrix, backed by an executable
completeness and test-name guard. Existing focused tests supply repository
evidence where the shared harness does not.

| Contract | Evidence |
|---|---|
| Complete ownership | Reflection proves exactly 141 `Store` methods and exactly one owner among 20 repositories |
| Backend completeness | Existing compile assertions cover Memory, File, PostgreSQL; implementation-file map is in the S0 inventory |
| Production consumers | An AST guard freezes 58 direct constructor/field/helper/worker declaration sites, 10 flattened named local Store-compatible interfaces, and two anonymous helper-local interfaces |
| Per-repository applicability | Dimension-named Memory/File subtests plus focused tests cover all applicable cells for all 20 accepted repositories; exact full subtest paths and both documented 20 by 10 mirrors are checked against the executable authority |
| Success, ordering, filtering, owner scope | Repository-specific evidence covers document, owner, client, message, schedule, connector, passive, external-chat, delivery, run, audit, artifact, and other applicable query contracts |
| Cloning / alias behavior | Owner preferences and MCP nested values are isolated; defect evidence records current mutable alias escape for 12 other repositories on Memory and the live File decorator |
| Idempotency | Same MCP binding/key/fingerprint reuses one operation; changed fingerprint conflicts |
| CAS | MCP operation update increments version and rejects a stale expected version |
| Events / audit / sequence | Explicit Memory/File lifecycle subtests read and assert the required audit/event type for 18 repositories; Conversation event reads additionally prove session scope, order, and head sequence; ISCP onboarding and MCP document caller-owned lifecycle boundaries |
| Restart | Explicit assertions prove File sessions, messages, message events/head, owner maps, MCP operations, nested invocation arguments, result bytes, reminders/deliveries, and notification bindings survive reload; focused tests cover every remaining repository, encryption, and legacy normalization |
| Concurrency | Sixteen simultaneous identical operation creates produce exactly one creation and one stable ID on Memory and File |
| Snapshot compatibility | Reflection freezes all 38 field names and JSON tags, including legacy Weixin compatibility fields |
| PostgreSQL source reconciliation | Constraint-aware executable manifest freezes 18/16 root tables/indexes versus 37/42 in Go; it compares complete shared definitions and freezes and documentation-checks every column, type, default, nullability, inline/table constraint, and index for all 19 Go-only tables |
| PostgreSQL DSN suite | Existing 9 tests and skip behavior are unchanged; no DSN was present for this candidate |
| `rows.Err()` | Static defect evidence names 9 functions containing 10 unchecked row loops; checked paths such as `ListAllConnectorSettings`, binding revocation, deletion, normalization, and message-event paging stay distinct |
| Known production defects | Static evidence freezes 48 File error-discard sites, 33 explicit PostgreSQL discarded results, 10 unchecked row loops, and mutable alias escape in 12 repositories for later replacement by failure/isolation assertions |

The S0 tests characterize current success contracts. Their defect-evidence
tests are intentionally named `TestS0DefectEvidence...`; S1-S3 must delete or
replace each assertion when the owning behavior is repaired.

## Context Timeout Basis

The initial fallback classes remain 10 seconds for reads, 30 seconds for
writes, 60 seconds for multi-record transactions, and 180 seconds for
startup/schema work. S0 accepts these as conservative **initial bounds**, not
as measured PostgreSQL service-level objectives.

| Class | Initial value | Evidence and limitation |
|---|---:|---|
| Read | 10 s | Local Memory/File characterization completes in milliseconds; 10 s leaves several orders of magnitude of filesystem/pool-acquisition headroom and matches the existing 10 s browser-startup/Gateway-shutdown scale. A caller's earlier deadline still wins. |
| Write | 30 s | Matches existing 30 s external request and delivery bounds, while allowing one fsync/rename or one ordinary database write under transient local contention. It is three times the read fallback. |
| Transaction | 60 s | Allows cross-record session deletion, due-claim, MCP revocation, rollback, and reconciliation to use one bound without inheriting a short request default; it is twice the ordinary write fallback. |
| Startup/schema | 180 s | Startup can acquire a cold local pool and apply additive DDL before readiness. Three minutes remains finite and well below host recovery bounds, but S1 must measure fresh and adopted databases before validating it. |

The values must be configuration-validated before use. S2 cannot present
ordinary read/write/transaction fields until its pilot actually consumes them.
S1 must record server version and fresh/adopted migration durations; if a
configured local PostgreSQL p99 approaches one third of a fallback, the value
requires review rather than automatic expansion.

## Exact S1 Verification Commands

Use a dedicated disposable PostgreSQL database because the existing integration
tests truncate Store tables. Keep the environment variable and skip behavior
unchanged.

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/store -run '^TestS0PostgresReconciliationManifest$' -count=1 -v
go test ./internal/config -count=1
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw_test?sslmode=disable' go test ./internal/store -run '^TestPostgres' -count=1 -v
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw_test?sslmode=disable' go test -race ./internal/store -run '^TestPostgres' -count=1
go test ./...
go vet ./...
```

S1 must additionally run its accepted migration test names exactly:

```bash
cd services/gateway
go test ./internal/store -run '^(TestPostgresMigrationsOrderedAndChecksummed|TestPostgresMigrationFreshDatabase|TestPostgresMigrationAdoptsUnversionedDatabase|TestPostgresMigrationRestartsWithoutDDLChurn|TestPostgresMigrationRejectsChangedChecksum|TestPostgresMigrationRejectsUnknownVersion|TestPostgresMigrationRollback|TestPostgresMigrationRejectsInsufficientPrivilege)$' -count=1 -v
go test ./internal/config -run '^(TestLoad.*StateBackend|TestLoad.*StatePath|TestLoad.*StateDSN|TestLoad.*StateEncryption|TestLoad.*StoreStartupTimeout)' -count=1 -v
```

Those tests do not exist in S0; the names reserve the exact S1 acceptance
surface and do not claim implementation.

## Exact S2 Pilot Verification Commands

S2 must first prove the all-method File transaction gate mechanically, then
prove the selected ISCP onboarding pilot. The PostgreSQL command remains DSN
gated exactly as today.

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/store -run '^(TestS0StoreMethodCatalogCharacterization|TestS0RepositoryCharacterizationMatrixCompleteness|TestS0BackendNeutralRepositoryCharacterization|TestS0BackendNeutralRepositoryLifecycleEvidence|TestS0SnapshotShapeCharacterization|TestS0BackendNeutralContractCharacterization|TestFileStoreTransactionGate.*|TestFileStoreRollback.*|TestFileStoreUnknownOutcome.*|TestISCPOnboardingRepository.*)$' -count=1 -v
go test -race ./internal/store -run '^(TestS0BackendNeutralRepositoryCharacterization|TestS0BackendNeutralRepositoryLifecycleEvidence|TestS0BackendNeutralContractCharacterization|TestFileStoreTransactionGate.*|TestFileStoreRollback.*|TestISCPOnboardingRepository.*)$' -count=1
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw_test?sslmode=disable' go test ./internal/store -run '^(TestPostgresStorePersistsOnlyISCPOnboardingReceipt|TestPostgresISCPOnboardingRepository.*)$' -count=1 -v
go test ./...
go vet ./...
```

As with S1, the future gate/failure test names are acceptance requirements, not
S0 implementation claims.

## Documentation Verification

Run the exact inline Python from the `Docs` job in `.github/workflows/ci.yml`
after every English/Chinese edit. The final S0 verification also runs:

```bash
git diff --check
git status --short
cd services/gateway
go test ./internal/store -run '^TestS0' -count=1 -v
go test -race ./internal/store -run '^TestS0(BackendNeutralContractCharacterization|BackendNeutralRepositoryCharacterization|BackendNeutralRepositoryLifecycleEvidence)$' -count=1
go test ./...
go vet ./...
```

## Unresolved Risks

- PostgreSQL runtime characterization was not executed because the DSN was
  absent. Static reconciliation and the unchanged skip suite do not replace a
  configured database run.
- PostgreSQL DML effects, generated constraint names, deployed database state,
  and conflicting-object `IF NOT EXISTS` adoption are explicitly outside the
  static parser. The manifest distinguishes these from parsed categories that
  have no difference or no occurrences.
- The 48 File and 33 PostgreSQL silent failure sites remain production defects
  by S0 scope. The unchecked row-loop and lookup-to-absence paths also remain.
- Client, Conversation, Run, Approval, Schedule, Connector,
  PassiveNotification, DeliveryRecord, BrowserState, Memory, Audit, and
  Evaluation records currently expose mutable in-process aliases from Memory
  and the live File decorator. S0 records rather than repairs them; each owning
  repository wave must replace the defect evidence with isolation assertions.
- The timeout defaults are reasoned conservative bounds; only S1/S2 configured
  evidence can validate or revise them.
- `DeleteSession` is a wide cross-repository transaction. S1/S3 must not let FK
  behavior or repository extraction fragment it.
- Legacy Weixin Snapshot fields, PostgreSQL tables, and copy-forward SQL remain
  compatibility state until a separately reviewed migration proves removal.
- The production consumer matrix is broad by current design. Repository
  extraction must introduce consumer-owned composites without using optional
  type assertions or a runtime service locator.
- The shared S0 harness is representative; the guarded per-repository matrix
  is the completeness authority. S2/S3 must add failure-contract evidence as
  each repository migrates without weakening the S0 applicability record.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| Design/start authorization | user authorization | `GO` for S0 only | S0 scope and human-assisted phase acceptance authorized; no authorization for S1 | User / 2026-08-20 |
| Implementation | candidate SHA pending | `pending` | Inventory, tests, baseline, and risk record await human inspection | pending |

## Links

- [S0 contract inventory](store-s0-contract-inventory.md)
- [S0 PostgreSQL reconciliation manifest](store-s0-postgresql-reconciliation-manifest.md)
- [Store contract foundation](store-contract-foundation-design.md)
- [Store reliability roadmap](store-contract-reliability-migration-design.md)
- [Repository migration design](store-repository-migration-design.md)
