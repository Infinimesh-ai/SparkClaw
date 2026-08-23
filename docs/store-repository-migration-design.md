# Store Repository Migration Design

> Language: English | [简体中文](../zh-cn/docs/store-repository-migration-design.md)

> Status: draft for S2 pilot and S3/S4 design review, 2026-08-19. The pilot
> starts only after S0-S1 implementation reviews and the File design receive
> `GO`; remaining repository code starts only after the S2 implementation `GO`.

## Objective

Replace the 141-method `store.Store` with reviewed domain repositories in small
implementation waves. S2 migrates one accepted low-risk pilot while proving the
File transaction model. S3 migrates every remaining repository. Each repository
changes all callers and all three backends atomically. After the final
repository, S4 removes the broad interface before Runtime/Supervisor work begins.

## Unit Of Migration

One repository is one implementation stage and normally one behavior-change
commit. Planning batches below do not authorize multi-repository commits.

Each repository stage contains:

1. confirm its accepted S0 method and consumer matrix;
2. add the repository interface and compile assertions;
3. change every method to accept context and return backend failure;
4. update Memory, File, PostgreSQL, and File `Snapshot` when applicable;
5. update every caller to pass its request, operation, worker, startup, or
   shutdown context;
6. add shared contract, File failure, PostgreSQL classification, timeout,
   cancellation, and race tests as applicable;
7. remove the old signatures for that repository;
8. run its implementation review before another repository starts.

No compatibility adapter, optional type assertion, duplicate method, dynamic
repository map, or string-based dispatch may survive a completed stage.

## Stable Operation Boundary

The S2 pilot introduces a package-private, typed operation
boundary used immediately by migrated methods:

- finite `OperationID` values identify repository and method;
- operation specs select read, write, transaction, or startup fallback timeout;
- caller deadlines always win when earlier;
- the boundary preserves domain errors and classifies backend errors;
- labels never contain owner IDs, record IDs, queries, paths, or content.

During S2-S3 this boundary owns only deadline composition and classification. It
does not expose health, metrics, repository lookup, or Runtime. S5 reuses the
same operation IDs and call sites when adding supervision, avoiding a second
rewrite of every repository method.

## Planning Order

The exact repository names are frozen by S0. The preferred risk order is:

1. one S0-accepted pilot from an already-strong bounded domain such as ISCP
   onboarding or MCP access, establishing context/error plumbing with limited
   caller breadth in S2;
2. Owner, Client, Credential, and Session;
3. Conversation, Run, Document, Approval, Audit, Evaluation, and artifact
   metadata;
4. Schedule, Connector, Delivery Record, Passive Notification, and External
   Chat;
5. Browser State and Memory.

Within a planning group, only one repository is active. Session deletion and
other cross-record commands receive their own explicit transaction cases; they
are not treated as simple CRUD because the interface name is small.

## Backend Rules

### Memory

- preserves current successful ordering, scoping, CAS, and event semantics;
- returns cloned data and never backend-owned mutable references;
- checks cancellation before mutation;
- applies record and required lifecycle changes under one lock.

### File

- uses the accepted [File Store durability](store-file-durability-design.md)
  gate and command state machine;
- returns success only after durable replacement and directory sync;
- restores the complete pre-snapshot for confirmed pre-submit failures;
- returns `unknown_outcome` after an uncertain submitted replacement;
- passes deterministic failure cases in the default Go test gate.

### PostgreSQL

- uses no `context.Background()` in ordinary repository operations;
- returns every `Exec`, `Query`, `QueryRow`, begin, scan, rows, commit, and
  rollback failure;
- maps `pgx.ErrNoRows` to normal absence only for lookups;
- writes a command and its required event/audit records in one transaction;
- reports uncertain commit as `unknown_outcome` and requires reconciliation.

## Temporary Broad Interface

While unmigrated methods remain, the broad interface embeds completed
repositories and declares only the remaining legacy methods. It does not repeat
migrated signatures. A completed repository has exactly one implementation
path.

The temporary interface is migration scaffolding, not a supported abstraction.
New production consumers may not accept it.

## Per-Repository Review Gate

Design confirmation checks:

- method ownership and minimum consumer dependencies;
- command transaction/reconciliation rows;
- intended behavior changes and rollback;
- focused verification commands.

Implementation `GO` requires:

- no old signatures or unbounded contexts for the repository;
- all-backend compile assertions;
- contract and default File injected-failure tests;
- affected package tests, Go build, vet, and race tests where concurrent;
- configured PostgreSQL evidence when SQL behavior changed;
- diff review confirming no unrelated repository or mechanical split.

## S4: Delete `store.Store`

After the final repository implementation receives `GO`:

1. replace remaining constructor parameters and fields with the minimum
   repository or a consumer-owned composite;
2. delete the broad interface and global backend assertions;
3. retain per-repository assertions for Memory, File, and PostgreSQL;
4. add source guards requiring zero production references to `store.Store`,
   repository type assertions, and dynamic repository maps;
5. verify the assembly root still constructs one selected backend without a
   Runtime service locator.

S4 is reviewed independently. S5 supervision cannot start merely because the
last repository compiles.

## Rollback

Repository stages do not change the File snapshot shape. A failed stage is
reverted as one topic without reverting previously accepted repositories.
Forward PostgreSQL migrations from S1 remain in place and additive.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| S2 pilot/S3 design | pending | pending | pending | pending |
| Each repository implementation | pending | pending | one row per accepted repository is added during migration | pending |
| S4 Store removal | pending | pending | pending | pending |
