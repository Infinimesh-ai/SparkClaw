# Store Reliability Migration Roadmap

> Language: English | [简体中文](../zh-cn/docs/store-contract-reliability-migration-design.md)

> Status: S2 was accepted at `42b62bd`; S3 OwnerRepository at `0b85cc4`; and
> S3 ClientRepository at `a4ddc83` on 2026-08-20. CredentialRepository contract
> revision 8 received `GO` at `b0884f6` after reviews 1-7 returned `REVISE`.
> The GO authorizes a live Credential foundation checkpoint,
> then complete ConnectorRepository lifecycle migration, then the final
> integrated Credential gate. On 2026-08-21 the owner adopted the risk-tiered
> S3 policy in the Repository migration design; completed waves remain final.

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
| [CredentialRepository contract](store-credential-repository-design.md) | Credential persistence semantics, redaction, Vault/Weixin migration, reconciliation, and all-backend implementation gate |
| [Runtime and supervision](store-runtime-supervision-design.md) | Post-migration Runtime assembly, finite operation supervision, health, metrics, probes, and lifecycle |

## Review Protocol

Every Store stage has two mandatory decisions:

1. **Design review**: confirm scope, invariants, failure semantics, test plan,
   rollback, and compatibility. Code for that stage starts only after a
   recorded `GO`.
2. **Implementation review**: inspect the diff and evidence against the
   accepted design. The next stage starts only after a recorded `GO`.

The amount of mandatory evidence is set by the P0/P1/P2 operation tier in the
Repository migration design. A P2 review must not require P0 recovery,
fault-injection, configured-PostgreSQL, or race evidence merely for uniformity.

A review result is one of:

- `GO`: all mandatory evidence is present and no unresolved correctness issue
  crosses the next stage boundary;
- `REVISE`: the stage remains active and its design or code must change;
- `STOP`: an assumption is invalid and later stages must be replanned.

Review records include date, reviewed commit or document revision, decision,
evidence, unresolved risks, and reviewer. A verbal assumption, a passing build,
or an absent PostgreSQL DSN is not a `GO`.

After S2, the owner delegated intermediate `GO`/`REVISE` decisions to the
primary implementation agent. Every delegated decision still requires the
accepted contract, complete automated evidence, and a context-isolated
gatekeeper review. Cross-stage risks remain recorded and are consolidated for
owner review when the full plan closes; only a newly discovered product-boundary
decision returns to the owner before then.

## Store Stage Order

| Stage | Deliverable | Entry condition | Exit condition |
|---|---|---|---|
| S0 | Contract foundation | Roadmap reviewed | Repository catalog and command matrix accepted; characterization evidence green |
| S1 | PostgreSQL schema/state foundation | S0 implementation `GO` | One migration authority, strict Store config, fresh/current database evidence |
| S2 | File transaction isolation and pilot repository | S1 implementation `GO` | All File methods use one transaction gate; one accepted low-risk repository proves commit/rollback and all-backend migration |
| S3 | Risk-tiered remaining repository waves | S2 implementation `GO` | Every remaining domain repository migrated across Memory, File, PostgreSQL, and callers with its P0/P1/P2 gate |
| S4 | Broad Store removal | Final S3 repository `GO` | `store.Store` deleted; consumers use minimum repositories or local composites |
| S5 | Runtime/Supervisor | S4 implementation `GO` | Assembly-only Runtime, bounded supervision, health, metrics, probes, and close are accepted |
| S6 | Responsibility split and Store closeout | S5 implementation `GO` | Complex Store modules split by accepted responsibility boundaries; durable rules merged into current guides; temporary plans removed |

Stage labels are dependencies, not permission to combine commits. Behavior
fixes, interface migrations, schema changes, and mechanical moves remain
separate topics.

## Global Invariants

- Memory, File, and PostgreSQL change together for every migrated repository.
- No migrated lookup turns backend failure into absence or an empty list.
- A File read never observes a mutation that may still roll back.
- A successful durable command means the authoritative state and its required
  lifecycle records are durable.
- P0 unknown outcomes are reconciled before retry. P1/P2 propagate a truthful
  unknown outcome and may retry only through an existing idempotency/CAS key;
  they do not add bespoke recovery protocols.
- Production consumers do not discover repositories by type assertion or retain
  `*store.Runtime`.
- PostgreSQL CI configuration and `SPARKCLAW_TEST_POSTGRES_DSN` skip behavior
  remain unchanged. P0 requires a configured run. P1/P2 require one per wave
  only when changing PostgreSQL schema or concurrency semantics; all tiers still
  run the configured final integration gate.
- Store behavior repair completes before any responsibility-based large-file
  split is designed or implemented.

## Scope Boundaries

Included:

- Store contracts, callers, all three backends, File snapshot durability,
  PostgreSQL migrations, Store-specific configuration, operation supervision,
  readiness, and Store lifecycle;
- artifact metadata records currently owned by Store.

Excluded until S6 or separately designed:

- splitting `memory.go`, `file.go`, and `postgres.go` before S6;
- splitting Gateway handlers, `useVoiceInput.ts`, or general config files;
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

S6 begins with a fresh file-size and responsibility inventory. It splits only
Store modules whose ownership boundaries are already stable after S4/S5; pure
moves and behavior changes remain separate commits.

## Current Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| S0 design/start authorization | S0 plan | `GO` for S0 only | Human-assisted S0 implementation authorized; all later stages excluded | User / 2026-08-20 |
| S0 implementation | `207462154fa2377ed786af671f41e0f353d11ba9` | `GO` | Inventory, manifest, tests, baseline, and assigned residual risks accepted; S1 may start | User / 2026-08-20 |
| S1 design | `361612c` | `GO` | Three independent design reviews accepted migration ownership, adoption, configuration, failure, and PostgreSQL verification contracts | Independent gatekeeper; user authorized implementation / 2026-08-20 |
| S1 implementation | `b2f9115` | `GO` | Independent implementation review found and closed the duplicate legacy-key blocker; user accepted the green implementation and authorized S2 design | Independent gatekeeper and user / 2026-08-20 |
| S2 design | `49b0858` | `GO` | Four review rounds closed File fence admission, pending authority-ticket retry, PostgreSQL final reconciliation, and isolation-default blockers; implementation may start | Independent gatekeeper / 2026-08-20 |
| S2 implementation initial review | `9d86c50` | superseded `GO` | The initial reviewer accepted the implementation evidence; a later fresh review superseded this decision | Independent gatekeeper and primary agent under owner-delegated authority / 2026-08-20 |
| S2 implementation fresh re-review | `9d86c50` | `REVISE` | Ticket expiry used the request-start time after persistence/reconciliation, and the fresh review lacked recorded real-DSN evidence | Context-isolated gatekeeper / 2026-08-20 |
| S2 repair implementation | `bc1bfb4`, `6f4c1bf`, `437e4bc`, `42b62bd` | `GO` | Rechecks a live clock immediately before ticket disclosure; adds intra-call expiry and missing File destination/directory failure coverage; forwards read/write timeout overrides through Compose with an expansion test; focused/full/race/default-File/WebChat/docs/Compose and independently repeated disposable real-PostgreSQL gates passed | Context-isolated gatekeeper and primary agent under owner-delegated authority / 2026-08-20 |
| S3 Owner implementation | `0b85cc4`, gate record `fc5acba` | `GO` | Context-isolated repair review closed unsafe pre-candidate ownership and terminate-not-release evidence; complete local and disposable PostgreSQL gates passed | Context-isolated gatekeeper and primary agent under owner-delegated authority / 2026-08-20 |
| S3 Client implementation | `1acdd2f`, repair `a4ddc83` | `GO` after `REVISE` | Repair replaced non-cancelable PostgreSQL admission and fixed acquired-session `Begin` classification; exact repaired candidate passed full normal/race and configured PostgreSQL full/race gates | Context-isolated gatekeeper and primary agent under owner-delegated authority / 2026-08-20 |
| S3 Credential contract review 1 | `de4cd93` | `REVISE` | Revision 1 lacked operation identity, conditional/pending deletion, non-rollback File high-water consistency, lifecycle ownership, and exact safe error projection | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract review 2 | `1d646f0` | `REVISE` | Revision 2 did not preserve operation replay after success, separate active rewrap from orphan cleanup, or encode every accepted time in delete versions | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract review 3 | `b6def5d` | `REVISE` | Revision 3 overpromised post-delete operation identity without a durable tombstone and let Delete reuse a caller identity for another ref | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract review 4 | `30cbf24` | `REVISE` | Binding identity was process-local until after adapter Start/Seal, so a crash could orphan the secret and lose replay identity; Weixin compensation had no durable terminal transition preventing Poll/Seal reuse | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract review 5 | `4d54acf` | `REVISE` | Credential code was blocked until Connector GO even though Connector recovery required the new AbortSeal, and stale text still assigned cross-restart cleanup to volatile Vault state | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract review 6 | `3c86739` | `REVISE` | Foundation called AbortSeal without the required exact Connector proof, so concurrent stale compensation could delete an active credential; the repository order also duplicated Connector | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract review 7 | `8ef063f` | `REVISE` | The migration roadmap still authorized foundation AbortSeal; legacy revoke could delete a credential without durable transition proof, and deferral would leave public Vault Delete without a legal caller | Context-isolated gatekeepers / 2026-08-20 |
| S3 Credential contract review 8 | `b0884f6` | `GO` | Foundation has no public cleanup dead code; private reconciliation remains live; ambiguous legacy start/revoke retains credentials; Connector owns Delete/AbortSeal with exact durable barriers | Context-isolated gatekeeper / 2026-08-20 |
| S3 validation policy | owner instruction | `GO` | Future waves use P0/P1/P2 operation risk plus aggregate boundaries; completed waves are not reopened and P1/P2 no longer inherit maximum-strength recovery and evidence | User / 2026-08-21 |
