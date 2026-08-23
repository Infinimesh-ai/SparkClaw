# File Store Durability Design

> Language: English | [简体中文](../zh-cn/docs/store-file-durability-design.md)

> Status: draft for S2 design review, 2026-08-19. Implementation starts only
> after S0 and S1 implementation reviews and this design receive `GO`. S2 also
> migrates the low-risk pilot repository accepted in S0.

## Problem

The File backend currently mutates `MemoryStore`, serializes afterward, and
discards most persistence errors. Its mutex protects snapshot writing but not
the preceding mutation or ordinary reads. A reader can therefore observe state
that is not durable and may later need rollback. A later successful snapshot
can also persist dirty state left by an earlier silent failure.

S2 first establishes one transaction boundary for every File method, then uses
the accepted pilot repository to prove the complete durability behavior in a
production path. Remaining repositories migrate in S3.

## Commit Visibility Invariant

A File read observes either the complete state before a command or the complete
state after that command is durably committed. It never observes a tentative
Memory mutation.

All File reads and writes use one context-aware transaction gate:

- reads acquire shared admission;
- commands acquire exclusive admission before reading pre-state;
- migrated methods use their caller context while legacy methods temporarily
  use a bounded internal context;
- direct access to `inner` outside an admitted File method is forbidden;
- waiting for admission honors cancellation and deadlines.

The gate foundation is a mechanical commit reviewed separately from the pilot
command's error behavior. It must cover every existing File method, including
the newer MCP/ISCP methods, before the pilot migration commit begins.

## Command Algorithm

While holding exclusive admission, a migrated command:

1. rejects an already canceled context;
2. captures a complete pre-command rollback state containing the persisted
   `Snapshot` plus a non-serialized sidecar for process-local revisions and any
   other volatile derived state;
3. applies the Memory command and its required event/audit changes;
4. captures the candidate snapshot;
5. encodes and optionally encrypts the candidate;
6. creates a unique same-directory temporary file with mode `0600`;
7. writes all bytes, fsyncs the file, and closes it;
8. checks cancellation before the replacement submission point;
9. atomically renames the temporary file over the state path;
10. fsyncs the parent directory;
11. releases admission and reports success.

The configured File backend requires a non-empty path. Tests that need
non-durable state use `MemoryStore`, not a pathless File Store.

## Failure State Machine

| Stage | Memory action | Result |
|---|---|---|
| before Memory mutation | none | original domain, canceled, or timeout error |
| Memory command rejects | unchanged by command contract | typed domain error |
| encode/encrypt/create/write/file-sync/close fails before rename | restore complete pre-snapshot | `durability_failed` with cause |
| context ends before rename | restore complete pre-snapshot | `canceled` or `timeout` |
| rename reports failure | restore pre-snapshot when replacement is known not to have occurred | `durability_failed`; otherwise `unknown_outcome` |
| rename succeeds, directory sync fails or completion becomes uncertain | keep candidate state; do not claim rollback | `unknown_outcome` |
| commit succeeds | keep candidate state | nil |

After `unknown_outcome`, normal retries are forbidden until the command's
repository reconciliation read establishes the durable version. Temporary-file
cleanup is best effort and never hides the primary error.

## Rollback Correctness

Rollback loads the captured persisted snapshot through the same normalization
path used at startup so durable derived indexes are rebuilt consistently, then
restores the volatile sidecar. The sidecar never changes snapshot JSON.
Hand-written restoration of one map entry is not accepted because a command
may also change events, indexes, revisions, or related records.

No read is admitted while rollback or post-rename outcome handling is active.

## Failure Injection

File persistence uses a package-local filesystem seam supporting deterministic
failures at:

- encode and encryption;
- temporary creation and partial write;
- file fsync and close;
- rename;
- directory open/fsync/close;
- cleanup.

Tests do not depend on permissions, full disks, mount behavior, or timing.

## Verification

Required tests include:

- reader cannot observe a blocked tentative mutation;
- queued read/write admission respects cancellation;
- every pre-rename failure restores the full in-memory snapshot;
- event, index, and revision state also rolls back;
- post-rename directory-sync failure returns `unknown_outcome` without memory
  rollback;
- restart after successful commit reproduces the exact committed state;
- concurrent commands serialize without lost updates;
- encryption and plaintext use identical commit stages;
- race tests cover read, command, rollback, and reload paths;
- source guards reject discarded persistence errors and direct ungated `inner`
  access.

## Transitional Rule

After the S2 pilot and until all remaining repositories migrate, legacy commands retain their old public
signatures but must use the same gate and bounded persistence path. System-wide
reliability is not claimed during this interval. A repository earns the new
contract only when its signatures, callers, all backends, failure tests, and
reconciliation behavior migrate together.

## S2 Review Gate

Design `GO` requires an accepted gate implementation strategy, pilot,
submission point, failure table, and injection seam. Implementation proceeds as
two reviewed commits: mechanical gate coverage for every File method, followed
by the pilot repository across Memory, File, PostgreSQL, and callers.

S2 implementation `GO` requires deterministic pilot failure tests, race
evidence, unchanged snapshot JSON shape, no default-backend regression, and no
unused transaction helper. S3 cannot start on gate scaffolding alone.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| Design | pending | pending | pending | pending |
| Implementation | pending | pending | pending | pending |
