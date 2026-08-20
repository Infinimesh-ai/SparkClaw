# Store Contract Foundation Design

> Language: English | [简体中文](../zh-cn/docs/store-contract-foundation-design.md)

> Status: S0 implementation accepted on 2026-08-20 at
> `207462154fa2377ed786af671f41e0f353d11ba9`. Production Store behavior remains
> unchanged; later changes follow the separately gated roadmap.

## Objective

Create the evidence and exact contracts needed to migrate Store safely. S0 does
not repair production persistence behavior. It characterizes current behavior,
assigns every method to one repository, and defines the error, context,
transaction, and reconciliation rules later stages must implement.

## Required Deliverables

1. A complete inventory of the current broad interface, all production
   consumers, all three backend methods, File `Snapshot` fields, and PostgreSQL
   tables, columns, indexes, and constraints.
2. A consumer matrix listing the minimum repositories required by every
   constructor, field, helper, and worker.
3. A command matrix listing the records and derived indexes changed, required
   event/audit records, atomic transaction boundary, idempotency or version
   evidence, effect-submission point, and reconciliation read.
4. A backend-neutral characterization harness covering Memory and File, plus
   the existing DSN-gated PostgreSQL suite.
5. An accepted repository catalog with every current Store method assigned
   exactly once.
6. One low-risk pilot repository selected for S2. It must already have explicit
   domain errors, limited caller breadth, and a durable command suitable for
   proving the File state machine; current ISCP onboarding or MCP access methods
   are candidates, not a decision made in advance.

## Repository Catalog Candidate

The catalog is a review candidate, not authoritative until S0 exits:

| Repository | Responsibility |
|---|---|
| `OwnerRepository` | owner profiles and external-owner lookup |
| `ClientRepository` | clients, token lookup, revocation, last-seen, pairing codes |
| `ISCPOnboardingRepository` | ISCP onboarding receipts only |
| `CredentialRepository` | encrypted credential secret metadata only |
| `SessionRepository` | session create/list/get/rename/delete |
| `ConversationRepository` | messages and message/session event reads |
| `RunRepository` | Agent runs, feedback, model/tool calls, episode summaries |
| `DocumentRepository` | durable document records and lineage metadata |
| `ApprovalRepository` | approval create/find/update/resolve/list |
| `ScheduleRepository` | reminders, due claims, CAS, delivery history |
| `ConnectorRepository` | connector settings and notification bindings |
| `PassiveNotificationRepository` | passive inbox, read state, prune, revision |
| `ExternalChatRepository` | external sessions/messages and provider lookup |
| `DeliveryRecordRepository` | receive, delivery, inbox, idempotency records |
| `MCPRepository` | access tickets, bindings, operations, redemption/revocation |
| `BrowserStateRepository` | browser auth and login-block lifecycle |
| `MemoryRepository` | candidates, accepted memories, search/update/delete/prune |
| `AuditRepository` | audit records and events not owned by Conversation |
| `EvaluationRepository` | evaluation run save/get/list |
| `ArtifactMetadataRepository` | artifact metadata save/list/URI lookup |

The S0 review may split or merge a candidate only from consumer and transaction
evidence. Convenience at assembly is not evidence. A consumer needing several
repositories declares a local composite interface.

## Method Shapes

Common signatures are:

```go
Create(ctx context.Context, value T) (T, error)
List(ctx context.Context, filter Filter) ([]T, error)
Get(ctx context.Context, id string) (T, bool, error)
Update(ctx context.Context, command Command) (T, error)
```

Rules:

- `found=false, error=nil` represents normal lookup absence.
- A command whose required target is absent returns a typed `not_found` error.
- CAS, deduplication, and creation retain typed result flags and add an error.
- Results never expose backend-owned mutable maps, slices, or pointers.
- Interfaces contain no pgx, SQL, filesystem, encryption, or supervisor types.
- Repository interfaces live in `store`; multi-repository composites live with
  their consumers.

## Error Contract

| Code | Meaning | Health effect |
|---|---|---|
| `not_found` | required command target is absent | none |
| `conflict` | version, CAS, idempotency, or current-state conflict | none |
| `invalid` | deterministic Store contract violation | none |
| `canceled` | caller canceled before a known effect completed | none |
| `timeout` | operation exceeded its effective deadline | thresholded for durable backends |
| `unavailable` | backend could not serve the operation | thresholded for durable backends |
| `durability_failed` | a durable commit definitely failed | immediate degradation |
| `unknown_outcome` | effect may have committed and requires reconciliation | immediate degradation |
| `corrupt` | persisted state cannot be decoded or violates an invariant | immediate degradation |
| `internal` | unclassified implementation failure | fail closed; reviewed for classification |

Errors preserve raw causes for internal diagnostics and support
`errors.Is`/`errors.As`. Public projections expose only stable safe codes and
copy. `context.Canceled` is never relabeled as timeout.

## Context Contract

Every migrated method receives a caller context. The earlier caller deadline
wins; otherwise Store applies a fallback deadline. The initial review values
are 10 seconds for reads, 30 seconds for writes, 60 seconds for multi-record
transactions, and 180 seconds for startup/schema operations. S0 must record why
these defaults are acceptable before they are declared validated.

A canceled context does not begin backend work. Gate acquisition, pool
acquisition, queries, row collection, commit, and reconciliation all use the
bounded context where the underlying operation supports cancellation.

## Characterization Cases

For every repository candidate, capture:

- success, normal absence, ordering, filtering, owner scope, and cloning;
- duplicate IDs, idempotency reuse, CAS conflict, and deletion behavior;
- required event/audit creation and sequence ordering;
- File restart behavior and unchanged snapshot shape;
- alias safety for maps, slices, pointers, and nested records;
- concurrent mutation behavior and process-local revision semantics;
- PostgreSQL row-scan and `rows.Err()` handling when the DSN suite runs.

Characterization tests freeze intended successful behavior. A test that merely
encodes a known silent failure is marked as defect evidence and replaced by a
failure assertion in the owning migration stage.

The representative backend-neutral cross-contract harness may share evidence
across repositories only when paired with a complete per-repository
applicability/evidence matrix. Every repository and dimension must reference an
exact existing/new test or record `N/A` with a contract-specific reason. Later
S2/S3 waves extend, rather than replace, this S0 evidence with failure-contract
coverage for the migrated method set.

## S0 Review Gate

Design `GO` requires the inventory templates, method/error/context rules, and
test plan to be accepted. Implementation `GO` requires completed matrices,
green characterization tests, and no unassigned or multiply assigned Store
method.

S1 cannot start with an unresolved transaction owner, reconciliation path, or
repository boundary. S2 cannot start without an accepted pilot.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| Design/start authorization | S0 plan | `GO` for S0 only | Scope, invariants, matrices, characterization plan, and human-assisted phase acceptance authorized; S1 excluded | User / 2026-08-20 |
| Implementation | `207462154fa2377ed786af671f41e0f353d11ba9` | `GO` | [Inventory](store-s0-contract-inventory.md), [PostgreSQL manifest](store-s0-postgresql-reconciliation-manifest.md), tests, baseline, and assigned residual risks accepted | User / 2026-08-20 |
