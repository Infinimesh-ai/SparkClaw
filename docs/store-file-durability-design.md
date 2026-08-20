# File Store Durability Design

> Language: English | [简体中文](../zh-cn/docs/store-file-durability-design.md)

> Status: S2 implementation re-review is pending at `437e4bc`. The repaired
> candidate adds the missing destination-read and directory-open/close failure
> evidence; S3 remains paused until independent `GO`.

## Problem And S2 Claim

The File backend currently mutates `MemoryStore`, serializes afterward, and
discards 48 legacy persistence results. Its mutex protects snapshot writing but
not the preceding mutation or ordinary reads. A reader can observe state that
is not durable, and a later successful snapshot can persist dirty state left by
an earlier silent failure.

S2 deliberately makes two different claims:

| Surface | S2 guarantee |
|---|---|
| Every FileStore interface method | One in-process admission gate prevents a read or another command from interleaving with a command's Memory mutation and persistence attempt. |
| Migrated `ISCPOnboardingRepository` methods | Caller context, typed backend errors, full pre-submit rollback, durable replacement, unknown-outcome fencing, and reconciliation. |
| Remaining legacy repositories | Existing signatures and persistence-error behavior remain known defects for S3. S2 does not claim their post-failure state is durable. |

The strong commit-visibility invariant therefore applies only to a migrated
repository while the File backend is not fenced by an unresolved submitted
outcome: its read observes either the complete pre-command state or the complete
durably committed post-command state. The all-method gate alone is not evidence
that a legacy repository has earned that invariant.

## One Admission Gate

`FileStore` uses `golang.org/x/sync/semaphore.Weighted` as a direct dependency.
The semaphore is initialized with one fixed private capacity. A read acquires
weight 1 and a command acquires the full capacity. The dependency's FIFO waiter
queue prevents new readers from bypassing a queued writer, and `Acquire` can
return the waiting context's cancellation or deadline error.

The gate rules are:

- all 141 public FileStore methods are classified once as read or command and
  acquire this gate before touching `inner`;
- commands retain exclusive admission from before Memory pre-state capture
  through persistence, rollback, or unknown-outcome registration;
- migrated methods acquire with their effective operation context;
- legacy methods have no error channel, so they acquire with
  `context.Background()` and may wait without a deadline until their repository
  migrates in S3;
- a legacy method does not invent a zero result, silently skip a command, or
  panic merely to simulate admission cancellation;
- the old File persistence mutex is removed; helpers called under exclusive
  admission never recursively acquire the gate;
- `inner` remains private, and production code outside an admitted File method
  cannot access it.

Fence observation and semaphore acquisition use an explicit double-check loop;
they are not two unrelated checks:

1. read the current fence pointer under `fenceMu`;
2. if present, a legacy method waits on its completion channel outside the
   semaphore and retries, while a migrated method enters reconciliation;
3. if absent, acquire the requested semaphore weight;
4. read the fence pointer again under `fenceMu`;
5. return the admission lease only when the second read is still nil;
6. otherwise release the semaphore immediately, wait or reconcile outside that
   lease, and retry from step 1.

Fence installation occurs under exclusive admission and `fenceMu`. The fence
payload is immutable after publication. Clearing compares the same pointer
under `fenceMu`, removes it, and closes its completion channel exactly once.
A dedicated reconciliation path acquires full semaphore capacity without the
ordinary fence precondition, then rechecks pointer identity before inspecting
the destination. It releases admission before reporting or retrying. This is
the only operation allowed to acquire admission while a fence exists.

Consequently, a legacy waiter already queued before fence installation may be
woken after the pilot releases exclusive admission, but its mandatory
post-acquire check sees the fence, releases immediately, and waits outside the
gate. It cannot pass the fence or hold the gate needed by the reconciler.

The gate commit is reviewed independently and contains no pilot signature or
error-classification change. An AST source guard enumerates the accepted S0
method set and proves that every public File method enters exactly one read or
command wrapper before an `inner` access. Focused concurrency tests block a
command between Memory mutation and persistence and prove that neither a read
nor another command passes it.

This is an in-process boundary. SparkClaw's product topology runs one Gateway
against one File path; S2 does not add a cross-process file lease. Two processes
writing the same path remain unsupported.

## Migrated Command Algorithm

While holding exclusive admission, migrated `SaveISCPOnboarding` performs:

1. derive the effective caller-or-30-second write context and reject it if
   already done;
2. resolve any older submitted outcome for this FileStore or return that typed
   error without mutation;
3. capture the complete rollback state, the exact current on-disk bytes and
   existence bit, and the current snapshot JSON shape;
4. apply the Memory command under its lock and preserve domain/validation
   errors without starting persistence;
5. capture the candidate snapshot, JSON-encode it, and optionally encrypt it;
6. ensure the parent directory exists, then create a unique same-directory
   temporary file with `O_EXCL` and mode `0600`;
7. write all bytes, fsync the temporary file, and close it;
8. check the effective context once more before submission;
9. atomically rename the temporary file over the state path;
10. open and fsync the parent directory, then close it;
11. clear temporary state, release admission, and report success.

The replacement rename is the effect-submission point. Cancellation is checked
before it; cancellation observed after a submitted rename cannot be reported as
a definite rollback. A configured File backend and both File constructors
require a non-empty path. Tests requiring intentionally non-durable state use
`MemoryStore`.

The new primitive does not change `Snapshot`, JSON field names, omission rules,
plaintext formatting, or encrypted-envelope version. Plaintext and encrypted
paths use the same create/write/file-sync/rename/directory-sync stages.

## Filesystem Seam

A package-private `fileCommitOps` dependency owns only the operations needed by
the algorithm: encode/encrypt, mkdir, create-exclusive temp, write, file sync,
file close, rename, read destination, remove temp, open directory, directory
sync, and directory close. Production uses `os` and the existing encryption
implementation. Tests inject deterministic results and partial writes; they do
not depend on permissions, a full disk, mount behavior, sleeps, or races.

Temporary files are unique and remain in the destination directory so rename
does not cross filesystems. Every failure before rename attempts cleanup; a
cleanup failure is joined to internal diagnostics but never replaces the
primary classified outcome. A process crash may leave an unreferenced temp
file, but startup reads only the exact configured state path.

## Failure State Machine

| Stage | In-memory action | Returned result |
|---|---|---|
| before Memory mutation | none | preserved `canceled`, `timeout`, or validation error |
| Memory command rejects | unchanged by command contract | preserved typed domain error, including `ErrISCPOnboardingConflict` |
| encode/encrypt/mkdir/create/write/file-sync/file-close fails before rename | restore complete rollback state | `durability_failed` with cause |
| context ends before rename | restore complete rollback state | `canceled` or `timeout` |
| rename reports failure and destination is provably the previous bytes/absence | restore complete rollback state | `durability_failed` |
| rename reports failure but destination is candidate or cannot be identified | keep candidate and install submitted-outcome fence | `unknown_outcome` |
| rename succeeds, then directory open/sync/close fails | keep candidate and install submitted-outcome fence | `unknown_outcome` |
| replacement and directory sync succeed | keep candidate | nil |

`durability_failed` means the candidate is definitely not the active durable
state and normal caller retry is permitted. `unknown_outcome` means the effect
may be active and normal retry is forbidden until reconciliation. No path
claims that an already submitted rename was rolled back merely by changing
Memory state.

## Submitted-Outcome Fence And Reconciliation

The fence stores no business label. It holds the operation ID, candidate byte
digest, previous exact-byte digest/existence, rollback state, and a completion
channel. It never changes snapshot JSON.

Before any later operation proceeds:

- a migrated method may take exclusive admission and reconcile within its
  effective context;
- a legacy method waits outside the admission gate for the fence completion
  channel, leaving a migrated reconciliation call able to acquire the gate;
- no legacy command mutates Memory or writes a newer snapshot through an
  unresolved fence.

These rules are implemented by the admission double-check loop above, not by a
single pre-acquire fence test. Fence state is owned only by `fenceMu`; semaphore
ownership protects Memory/snapshot transitions but is not used as the fence
pointer lock.

Reconciliation reads the exact destination bytes. If they match the candidate,
it retries only the parent-directory sync; success confirms the candidate and
clears the fence. If they match the captured previous bytes or previous
absence, Memory restores the rollback state and the fence resolves as a
definite failed command. If neither version can be established, or directory
sync still fails, the fence remains and the migrated call returns
`unknown_outcome` or `corrupt` as appropriate.

For the pilot, `GetISCPOnboarding(id)` and `ListISCPOnboardings(ownerID)` are
the caller-visible reconciliation reads. They never return candidate data with
a nil error while the fence remains unresolved. S5 later adds proactive
recovery probing; S2 intentionally provides reconciliation-on-operation only.

## Complete Rollback State

Rollback captures the whole persisted `Snapshot` plus a typed volatile sidecar.
The current sidecar contains a clone of `passiveNotificationRevs`. Restoration
calls the same `loadSnapshot` normalization used at startup so notification and
artifact URI indexes and other derived maps are rebuilt, then restores the
volatile revision sidecar under the Memory lock.

Hand-written restoration of the onboarding map entry is deleted. Such rollback
is not accepted because future commands can also change indexes, events,
revisions, or related records. Tests compare the full snapshot and volatile
sidecar before and after every injected pre-submit failure.

## Transitional Legacy Rule

The mechanical gate prevents concurrent interleaving for every File method,
but the 48 accepted S0 discarded-persistence sites remain explicit defect
evidence. Their existing return values cannot surface admission timeout or
durability failure. They therefore use unbounded admission, retain their current
post-failure contract, and do not use the migrated rollback helper.

The S2 source guard is scoped accordingly: it rejects ignored commit errors and
hand-written rollback in migrated onboarding methods, while separately
asserting that the known legacy defect inventory has not grown. Each S3
repository replaces its own defect evidence with positive error, rollback, and
reconciliation assertions when its signatures and callers migrate together.

## Verification And Commit Boundary

The first implementation commit is mechanical admission coverage only. It must
pass Store tests and `go test -race ./internal/store` and show no pilot API or
snapshot diff.

The second implementation commit migrates the pilot and includes deterministic
tests for:

- cancellation before admission and before rename;
- a reader blocked from a tentative mutation;
- every pre-rename failure restoring the full snapshot and volatile revision;
- partial-write cleanup and preservation of the primary error;
- rename failure classified by exact destination reconciliation;
- directory-sync failure fencing all legacy commands and rejecting migrated
  data reads until reconciliation;
- legacy read and command waiters queued before fence publication failing the
  post-acquire check, releasing admission, and leaving the reconciler live;
- candidate and previous-state reconciliation branches;
- successful restart reproducing exactly the committed onboarding receipt;
- concurrent duplicate saves preserving one winner and
  `ErrISCPOnboardingConflict`;
- plaintext/encrypted stage parity and state-file mode `0600`;
- unchanged snapshot JSON keys and absence of plaintext secrets;
- race coverage across read, command, rollback, fence, and reload paths.

The default File backend production-entry tests, full Go build/test/vet, WebChat
tests/build, bilingual docs CI, and a real configured PostgreSQL Store run gate
S2. PostgreSQL CI topology and `SPARKCLAW_TEST_POSTGRES_DSN` skip semantics do
not change.

## Residual Risks Accepted For S2 Review

- A submitted outcome can block legacy File commands until a migrated
  onboarding read reconciles it. This is deliberate fail-closed behavior until
  S5 adds proactive recovery; silently overwriting uncertain state is worse.
- The gate does not protect against a second process using the same File path.
- The ISCP authority effect occurs before local receipt persistence and exposes
  no revocation/idempotent-recovery operation. A definite local failure can
  strand an undisclosed authority ticket; Store cannot make the remote and
  local effects atomic. The pilot makes this failure visible but does not claim
  to compensate it.
- Unmigrated repositories retain their known silent persistence failures until
  their S3 stage.

## S2 Review Gate

Design `GO` requires acceptance of the scoped invariant, semaphore strategy,
legacy behavior, submission/fence state machine, rollback state, filesystem
seam, pilot reconciliation, and residual risks. Implementation `GO` requires
both commits, deterministic failure and race evidence, unchanged snapshot JSON,
no default-backend regression, and no unused gate or operation helper. S3
cannot start on gate scaffolding alone.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| Design review 1 | `3aff151` | `REVISE` | Fence observation and semaphore acquisition lacked an atomic handshake, allowing a pre-queued legacy waiter either to pass the fence or deadlock reconciliation | Independent gatekeeper / 2026-08-20 |
| Design review 2 | `49b0858` | `GO` | Double-check admission, immutable fence ownership, and the dedicated reconciliation lease close the queued-waiter pass/deadlock window; later linked reviews closed pilot/PostgreSQL blockers | Independent gatekeeper / 2026-08-20 |
| Implementation initial review | `0e7817b`, `9d86c50` | superseded `GO` | All-method admission, rollback/fence/reconciliation, plaintext/encrypted restart, race, default File, and full regression evidence passed; a later fresh review superseded this decision | Independent gatekeeper and primary agent under owner-delegated authority / 2026-08-20 |
| Implementation fresh re-review | `9d86c50` | `REVISE` | The review identified unexercised destination-read and directory-open/close File failure branches in addition to the pairing-service blocker | Context-isolated gatekeeper / 2026-08-20 |
| Repair candidate | `6f4c1bf` | pending | Deterministic tests now cover destination-read isolation and directory-open/close uncertainty with plaintext/encrypted reconciliation parity | Pending independent re-review / 2026-08-20 |
