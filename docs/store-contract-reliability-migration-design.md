# Store Reliability Migration Roadmap

> Language: English | [简体中文](../zh-cn/docs/store-contract-reliability-migration-design.md)

> Status: S1 implementation accepted by the user on 2026-08-20 at `b2f9115`;
> S2 design is the active gate. S2 code starts only after its linked File and
> pilot repository design records receive `GO`.

## Purpose

The Store reliability work is split into reviewable problems instead of one
implementation plan. This roadmap owns only sequencing, dependencies, and
stage gates. The linked designs own their individual contracts.

The Store work fixes silently discarded File/PostgreSQL failures, introduces
small domain repositories, deletes the broad `store.Store`, and only then adds
assembly-owned Runtime supervision. PostgreSQL schema/configuration work is a
Store prerequisite.

Large-file splitting is deliberately excluded. After Store migration and
supervision are complete, the repository will be inventoried again and any
responsibility-based split will receive a new design. No current file split is
pre-approved by this roadmap.

## Documents

| Document | Decision owned |
|---|---|
| [Store contract foundation](store-contract-foundation-design.md) | Current behavior characterization, repository ownership, error/context rules, and mutation/reconciliation matrix |
| [S0 PostgreSQL reconciliation manifest](store-s0-postgresql-reconciliation-manifest.md) | Constraint-aware static comparison of the root migration and embedded Go schema, including explicit parser limits |
| [File Store durability](store-file-durability-design.md) | Read isolation, serialized commits, crash stages, rollback, and deterministic failure injection |
| [PostgreSQL schema and state configuration](store-postgresql-schema-config-design.md) | Embedded migration authority, existing-database adoption, strict state configuration, and unchanged DSN-gated tests |
| [Repository migration](store-repository-migration-design.md) | One-repository implementation waves, temporary broad-interface rules, and final `store.Store` deletion |
| [Runtime and supervision](store-runtime-supervision-design.md) | Post-migration Runtime assembly, finite operation supervision, health, metrics, probes, and lifecycle |

## Review Protocol

Every Store stage has two mandatory decisions:

1. **Design review**: confirm scope, invariants, failure semantics, test plan,
   rollback, and compatibility. Code for that stage starts only after a
   recorded `GO`.
2. **Implementation review**: inspect the diff and evidence against the
   accepted design. The next stage starts only after a recorded `GO`.

A review result is one of:

- `GO`: all mandatory evidence is present and no unresolved correctness issue
  crosses the next stage boundary;
- `REVISE`: the stage remains active and its design or code must change;
- `STOP`: an assumption is invalid and later stages must be replanned.

Review records include date, reviewed commit or document revision, decision,
evidence, unresolved risks, and reviewer. A verbal assumption, a passing build,
or an absent PostgreSQL DSN is not a `GO`.

## Store Stage Order

| Stage | Deliverable | Entry condition | Exit condition |
|---|---|---|---|
| S0 | Contract foundation | Roadmap reviewed | Repository catalog and command matrix accepted; characterization evidence green |
| S1 | PostgreSQL schema/state foundation | S0 implementation `GO` | One migration authority, strict Store config, fresh/current database evidence |
| S2 | File transaction isolation and pilot repository | S1 implementation `GO` | All File methods use one transaction gate; one accepted low-risk repository proves commit/rollback and all-backend migration |
| S3 | Remaining repository waves | S2 implementation `GO` | Every remaining domain repository migrated across Memory, File, PostgreSQL, and callers |
| S4 | Broad Store removal | Final S3 repository `GO` | `store.Store` deleted; consumers use minimum repositories or local composites |
| S5 | Runtime/Supervisor | S4 implementation `GO` | Assembly-only Runtime, bounded supervision, health, metrics, probes, and close are accepted |
| S6 | Store closeout | S5 implementation `GO` | Durable rules merged into current guides; temporary plans removed |

Stage labels are dependencies, not permission to combine commits. Behavior
fixes, interface migrations, schema changes, and mechanical moves remain
separate topics.

## Global Invariants

- Memory, File, and PostgreSQL change together for every migrated repository.
- No migrated lookup turns backend failure into absence or an empty list.
- A File read never observes a mutation that may still roll back.
- A successful durable command means the authoritative state and its required
  lifecycle records are durable.
- Unknown outcomes are reconciled before retry; they are never reported as
  success or confirmed rollback.
- Production consumers do not discover repositories by type assertion or retain
  `*store.Runtime`.
- PostgreSQL CI configuration and `SPARKCLAW_TEST_POSTGRES_DSN` skip behavior
  remain unchanged. A stage requiring PostgreSQL evidence stays unapproved
  until an actual configured run is recorded.
- Store behavior repair completes before any responsibility-based large-file
  split is designed or implemented.

## Scope Boundaries

Included:

- Store contracts, callers, all three backends, File snapshot durability,
  PostgreSQL migrations, Store-specific configuration, operation supervision,
  readiness, and Store lifecycle;
- artifact metadata records currently owned by Store.

Excluded until separately designed:

- splitting `memory.go`, `file.go`, `postgres.go`, Gateway handlers,
  `useVoiceInput.ts`, or general config files;
- global replacement of every permissive environment parser;
- artifact object backend construction outside Store metadata;
- ORM, event sourcing, distributed transactions, dependency-injection
  frameworks, or a generic repository generator;
- changing PostgreSQL CI service topology.

## Rollback

S0-S4 preserve the File snapshot layout. Each repository wave is independently
revertible while it introduces no persistent schema change. PostgreSQL
migrations are forward-only, additive, transactional, and checksum protected;
application rollback never deletes a successfully migrated database.

No later-stage rollback may restore silently discarded persistence errors or a
second schema authority.

## Completion

Store work is complete only after S6. At that point the durable rules move into
`architecture.md`, `development.md`, deployment guidance, and the owning Store
guide. These temporary Store design documents and their Chinese mirrors are
then deleted together.

Only after that closeout may a new file-size and responsibility inventory decide
whether any module split is justified.

## Current Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| S0 design/start authorization | S0 plan | `GO` for S0 only | Human-assisted S0 implementation authorized; all later stages excluded | User / 2026-08-20 |
| S0 implementation | `207462154fa2377ed786af671f41e0f353d11ba9` | `GO` | Inventory, manifest, tests, baseline, and assigned residual risks accepted; S1 may start | User / 2026-08-20 |
| S1 design | `361612c` | `GO` | Three independent design reviews accepted migration ownership, adoption, configuration, failure, and PostgreSQL verification contracts | Independent gatekeeper; user authorized implementation / 2026-08-20 |
| S1 implementation | `b2f9115` | `GO` | Independent implementation review found and closed the duplicate legacy-key blocker; user accepted the green implementation and authorized S2 design | Independent gatekeeper and user / 2026-08-20 |
