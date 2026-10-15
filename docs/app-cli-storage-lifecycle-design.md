# App-CLI Request, Authorization and Storage Lifecycle Design

> Language: English | [简体中文](../zh-cn/docs/app-cli-storage-lifecycle-design.md)

- Status: design proposal, associated with InfiniCenter ProjectGroup-2 decision **0035 proposed**; not implemented or deployed. Current contracts remain unchanged.
- Date: 2026-10-15.
- Inspected baseline: SparkClaw `b637c5c8`, paired App-CLI `0.3.0-sparkclaw.23`, runtime digest `734f49377026d9a6977e505147cd16693d0abf68c7236aa042e7266c17457eb0`.
- Scope: public requests, task recovery, authorization renewal, events, results, send journals, temporary artifacts, garbage collection, capacity control, migration and rollback. Business mailboxes, user-retained files and conversation history continue to follow product retention policies.

This design places long-term duplicate-execution protection in a small set of monotonically advancing request-generation fences. Unresolved tasks retain the contents needed for recovery. Delivered terminal contents, individual deduplication records and expired authorizations can be reclaimed once their fences are effective. Capacity grows with active tasks, recovery windows and unresolved issues, rather than the lifetime number of requests.

“No duplicate sends” means that retries, restarts, cleanup and supported recovery never trigger another send for the same user operation. When a provider has no idempotent sending API, automatic completion of an unknown send and an absolute no-duplicate guarantee cannot both be provided. Such operations remain `uncertain`, allow reconciliation only, and may require user action. An explicitly requested second send is a new operation; matching message bodies do not establish duplication.

## 1. Current Gaps and Design Goals

The implementation covered by the current [paired release configuration](../configs/app-cli-release.json) and [acceptance record](app-cli-implementation-validation.md) has these growth paths:

| Object | Current behavior | Target |
| --- | --- | --- |
| `tasks.request/data` and request keys | Every invoke retains a complete row, without terminal reclamation | Retain active tasks in full; reclaim resolved tasks by delivery and epoch |
| Events | At most 1,000 per task, while task count keeps growing | Rolling byte budgets per task and owner, with explicit expired-cursor gaps |
| Authorizations and `.next` indices | Creation and renewal append files and chains | Fixed authorization slots and direct indices per active task |
| Send journals and collected artifacts | Separate task directories without a coordinated reclamation transaction | Reference accounting, explicit send-evidence retention conditions and recoverable deletion |
| 512 MiB admission gate | Counts allocated database pages plus the new request; excludes other storage | Count actual storage, reservations for admitted tasks and filesystem headroom |
| Request admission | Client authorization indices may be persisted before server admission | Explicit prepare distinguishes admitted, definitively rejected and unknown outcomes |

Preserve Python Registry as the sole public entry point, application adapter responsibilities, Browser Host page/lease ownership and the existing UI. Direct handler calls, database resets and newly allocated request IDs must not bypass recovery.

Goals: reclaimable data reaches a steady state during continued operation; cleanup never reopens an old send; crashes recover the same task; renewal storage is independent of renewal count; capacity pressure gives completion, cancellation and reconciliation of admitted tasks priority.

## 2. Required Invariants

1. A product send operation ID, request identity, immutable intent and task ID have a one-to-one binding. Retries, credential replacement, renewal, upgrades and reconciliation cannot allocate a new send identity.
2. Before details are deleted, the request's epoch is durably sealed. Every admission boundary validates the fence; a missing record never means “not executed.”
3. Sealing an epoch prevents new execution admission. It neither deletes active tasks in that epoch nor prevents authorized status/cancel/resume/reconcile of the original task.
4. Before an external side effect, persist a dispatch record that cannot be consumed twice and obtain confirmation from an independent progress anchor. An unknown outcome prohibits automatic redispatch.
5. Nonterminal tasks, unproven cleanup, unknown send outcomes and send receipts without durable delivery never lose recovery evidence through TTL or disk pressure.
6. Authorization expiry stops execution. Retaining a task does not extend permission to execute. Current authorized identities may receive new, restricted authorization for reading, cancellation or reconciliation.
7. Deleting original content changes neither task state nor the fact of sending. Expired history must be reported explicitly, never disguised as completed, not_found or not_sent.
8. Finite disk cannot unconditionally accommodate unlimited permanently unresolved tasks. Preserve unresolved evidence and pause new admission by account/owner with an actionable reason.

## 3. Request Identity and Epoch Fences

### 3.1 Identity

New request identity is `(owner_id, namespace_generation, request_epoch, sequence)`, encoded as an `operation_id` containing no business data. Generation and epoch advance monotonically and are never reused. Sequence is an unsigned integer allocated by the trusted product outbox, transmitted as a decimal string without floating-point conversion. The scope also binds the principal, application, command and resource digest.

Before any network call, the product persists user send intent and operation_id in its own durable transaction. App-CLI send `invocation_id` must equal that operation_id. Old formats use an explicit legacy import table; an old invocation_id cannot be wrapped in a new request epoch. Each explicit new user send receives a new identity; automatic retries only read an existing outbox record.

The product entry point also checks the original operation credential and generation. A retry from an old client whose business record has expired returns `OPERATION_RETIRED`, without creating another send. Deleting product history cannot remove generation fences. Devices submit through one trusted product allocator; individual devices must not reinterpret old sends as new operations.

The product allocator and scheduled-trigger records also use the independent progress anchor in section 10. A scheduled occurrence is identified by `(schedule_id, schedule_revision, occurrence_time)`; its timezone-converted occurrence_time is fixed when scheduled. The due-time transaction binds that occurrence to one operation_id, confirms the product anchor, and only then permits submission. Restoring a product database from before the trigger first blocks allocation, so the same occurrence cannot become a new send. A manual rerun must be an explicit new user action. Integer exhaustion rejects allocation rather than wrapping to zero.

### 3.2 Constant State per Owner

`owner_meta` retains the current generation, single open request_epoch, `sealed_through`, execution_epoch, commit_sequence, authorization revision high-water marks and GC progress. Only the current generation's single open epoch accepts new prepare calls. Older generations reject new execution wholesale. Application/account authorization remains isolated without creating a new owner for every historical request.

Proposed rotation: keep an epoch open for at most 24 hours, or seal it early when reclaimable terminal details reach 10% of the owner's usable budget. Time triggers rotation; durable integer fences determine whether an epoch is sealed. Clock rollback cannot reopen an epoch. Long watches and unresolved sends do not prevent rotation.

Under an exclusive write transaction, rotation seals E, sets `sealed_through=E`, opens E+1 and synchronizes the independent progress anchor. Previously prepared tasks keep their original task IDs. Old sends queued only in the product and never admitted are not automatically resubmitted with a new key; they require explicit reauthorization. Scheduled sends receive execution identities when due.

### 3.3 Admission Order

Authenticate owner/principal and request signature, then look up the existing task by identity. If present, compare intent/resource digests: a match returns the original task; a mismatch returns a conflict. If absent, an old generation, sealed epoch or unknown future epoch cannot create a task. Only the current open epoch may create one after successful capacity reservation.

```text
authenticated request
  existing operation -> matching intent ? same task : REQUEST_KEY_CONFLICT
  absent + retired generation/epoch -> OPERATION_RETIRED, no execution
  absent + current open epoch -> validate + reserve + atomic prepare
  absent + future/invalid epoch -> REQUEST_EPOCH_INVALID, no execution
```

A sealed epoch rejects replay without individual request tombstones. An active exception table retains older-epoch tasks that still need recovery; its entries permit control of the original task, never a new invoke. Issuing a new authorization for an old key after sealing cannot bypass admission fences.

## 4. Public Lifecycle and Durable Receipt

Proposed versions are Lifecycle **3.0**, Host **2.0**, execution binding **2.0** and ledger **2**, with required capabilities declared by the paired release. Phase one freezes concrete JSON Schemas and cross-language fixtures. Current 2.0 consumers must not silently interpret the new semantics.

| Operation | Proposed semantics |
| --- | --- |
| `prepare` | Validate trusted authorization, complete intent, identity and resource budget; atomically create the task, recovery inputs and reservation, without side effects |
| `invoke` | Start only the original prepared task; repeated calls return its current state and cannot implicitly create a new task |
| `lookup` | Return `known`, `not_admitted`, `retired` or `unresolved`; these outcomes are not interchangeable |
| `status/events` | Query the original task/bounded events; read-only control creates neither tasks nor authorization revisions |
| `renew/resume` | Update authorization for the original task; resume must satisfy recovery-state, original-intent and safety checks |
| `cancel/reconcile` | Record stop intent/query evidence; neither permits another send |
| `ack_result` | A durable receiver confirms task, result digest, terminal revision and receiver identity, allowing policy-governed content reclamation |

`not_admitted` is returned only when authoritative storage is healthy, the progress anchor agrees, and no admission record exists in the current open epoch. The response contains generation/epoch/commit_sequence and permits retrying the same prepare only within that epoch. It expires when the epoch is sealed. An authoritative query that cannot complete returns unresolved or a retryable service error. Capacity rejection creates no admitted row; a lost response still requires lookup, because timeout does not prove rejection.

Minimum new envelope fields: `operation_id`, `request_epoch`, `namespace_generation`, `intent_digest`, `resource_digest`, `task_id` after prepare, and `authorization_revision`. Results add `terminal_revision`, `result_digest`, `retention_until` and `recovery_class`; retired lookup responses carry no successful TaskResult. ACK includes those task/result identities and the durable receiver's receipt-record ID. Every field requires schema validation and signature binding.

Product receipt order: commit the result, send state and ACK outbox in one business-database transaction → asynchronously send ack_result → App-CLI records the ACK. Lost ACKs can be retried. Displaying a result, keeping it in memory or receiving a WebSocket packet is not durable receipt. Gateway receives mail results; this does not change decision 0031 for non-mail content handled by clients.

That receipt transaction must also obtain product progress-anchor confirmation before emitting ACK. ACK binds the product_commit_sequence and its confirmation credential. Restoring an old product database after App-CLI cleanup therefore cannot mistake an acknowledged send for work requiring reexecution. Data loss on both sides keeps recovery blocked; the anchor does not replace business backups.

The product and App-CLI do not pretend that their separate databases share a distributed transaction. Original identities, outboxes, idempotent prepare/ACK and startup scans provide convergence. Lookup recovers a task after a lost prepare reply; client crash recovery must not first allocate a new ID.

## 5. Storage Layers and Reclamation Conditions

A single state service per owner is the sole writer, using SQLite WAL/FULL for tables and transactions. Clients and Hosts cannot write arbitrary SQL. Trusted issuer signatures and permission boundaries remain independent; consolidating databases does not authorize the Executor to expand permissions. Bulk data lives under a controlled blob root, connected through digests and reference records.

Bindings explicitly declare the recovery class `pure_read`, `replay_safe_local`, `external_write` or `subscription`, determined by reviewed adapter capabilities and signature binding. A command named read is not sufficient proof of no side effects. Rejected admission does not durably create grants, indices or temporary copies. Necessary temporary uploads first require a bounded upload reservation; after timeout, release content without task references. Failed requests must not create another unlimited growth path.

| Table/object | Required fields or responsibility | Lifecycle |
| --- | --- | --- |
| `owner_meta` | Generation, open/sealed epochs, execution authority, commit and migration/GC high-water marks | Constant size; ordinary cleanup never deletes it |
| `operations` | Original identity, task, immutable digests, state, phase, deadline, recovery class, result references and ACK | Active tasks retained in full; terminal records reclaimed by epoch and delivery |
| `authorization_heads` | Current/candidate/transition authorization, revision and revocation state per task | Fixed slots per active task |
| `effect_records` | Task, effect token, dispatch phase, provider correlation and evidence digests | Unresolved records pinned; resolved records reclaimed by delivery and epoch |
| `events` | Task, sequence and compact payload | Rolling task/owner budgets, retaining gap high-water marks |
| `blobs` / `blob_refs` | File digest, size, purpose, references and deletion phase | Deletable after active/delivery references are gone |
| `gc_jobs` / `reservations` | Reentrant deletion progress and resource commitments for admitted tasks | Compact into high-water marks after completion |
| Product `operation_outbox` | Original operation identity, task, user intent/result, delivery and ACK state | Reclaimed in coordination with the product's generation fences |

Proposed retention values form a versioned policy and may be tuned to measured rates before deployment. The safety conditions above cannot be relaxed:

| Data | Proposed rule |
| --- | --- |
| Ordinary terminal read-only inputs/results | Retain one hour after durable ACK; without ACK, at most 24 hours, then explicitly expire the result without reexecuting the original ID |
| Locally collected results/files | Wait for Gateway to validate digests and durably ingest/take references; remove temporary copies one hour after ACK |
| Terminal send bodies/temporary attachments | Retain 24 hours after durable receipt of the send conclusion and required business copies; files referenced by other active tasks remain |
| Minimal terminal send receipts | Retain 30 days after ACK; individual control records may be removed only after epoch sealing; the product retains user-visible send history |
| Unacknowledged send receipts | Preserve compact known conclusions and recovery material; a 30-day timeout cannot erase the sole send evidence |
| Unfinished/uncertain/unproven cleanup | No automatic eviction TTL; keep the original task and necessary inputs/evidence/references pinned |
| Events | At most 1,000 records and 2 MiB per task, with a 32 MiB owner event budget; trim excess and report gaps |
| Transitional authorization versions | Current, one candidate and one unexpired old version; remove the old version after transition |
| Diagnostic logs/audit details | Bounded rotation, proposed seven days and 64 MiB; safety facts reside separately in tasks/anchors and do not depend on logs |

Delete terminal task details only after content-policy and send-delivery obligations are satisfied, the epoch is sealed, and no active execution, Host lease or file reference remains. Short-lived terminal tasks in an open epoch retain at least a compact deduplication record until rotation. Events are not the source of recovery truth; critical phases must first be written to operations/effect_records.

Business history and runtime caches have different policies. Runtime GC must not delete mail the user requested to keep permanently or claim to solve that history's unlimited growth. The product needs a separate visible retention/export policy. The runtime must not move its own growth to another directory through unlimited cold archives.

## 6. Fixed Authorization Slots and Safe Renewal

Replace `.next` chains with a direct `task_id -> authorization_head` index. Signed authorization includes owner/generation, original operation/task, intent/resource digest, revision, allowed operations, execution/access deadlines and current credential generation. Resources hold controlled credential references rather than persistent copies of raw tokens.

Renewal uses CAS on `(task_id, expected_revision, renewal_id)`: issue and persist candidate r+1 → Registry/Executor/Host verify its signature, same task/intent/resources and valid execution permission → atomically switch the head → acknowledge. After a lost response, query the head. Repeating a renewal_id still retained in the three slots returns the same r+1 instead of producing more revisions. Only one candidate may remain unfinished; another renewal cannot start before the required Host confirmation.

Store renewal_id only in current/candidate/transition slots, without a permanent renewal-deduplication table. An older expected_revision returns `AUTHORIZATION_STALE` and points to the current head. Clients converge by reading that head, without republishing old versions. Delete a task's authorization head when the task meets reclamation conditions. Even a mathematically valid old signature cannot acquire resources without a current task/head or valid lease. Access-only credentials use short-lived signed permissions, without creating durable authorization rows for each status/lookup.

Terminal state immediately prohibits execution renewal and revokes the execution head. Release version slots after old leases exit/expire and cleanup is proven. Thirty-day result retention does not require thirty-day execution-authorization file retention. Terminal queries and receipt delivery use separate access authorization for the current principal.

Each live Host lease references a specific revision. Old authorization may only let an existing lease finish before its original expiry; it cannot acquire a new lease. Remove transition versions after all leases confirm exit, or after original expiry with proven cleanup. Missing confirmation occupies the fixed slot and stops further progress; it cannot grow the version list. Host caches are constrained by short leases and revocation high-water marks. Reconnection synchronizes revisions first; disconnected caches cannot extend execution.

Retain the current five-minute watch execution window, renewal 60 seconds early and maximum 30-second Host lease as starting values. Authorization heads may advance indefinitely, but execution stops without valid continuing permission. Resume after expiry requires new explicit, valid authorization. Thirty-day query credentials may be replaced without changing task identity or historical intent; replacement does not restore execution permission automatically. Cancellation and read-only reconciliation have separate permissions.

Revocation first durably increases the relevant owner/account/task revocation high-water mark, then notifies resource endpoints and stops new execution. Offline Hosts stop no later than expiry of their existing leases. Account rebinding advances its generation; old account credentials become invalid wholesale before account details are deleted. Rebuilding an entire owner must advance generation, never delete and reuse its old identity. Authorization audit retains current control facts and bounded detailed history.

## 7. Unfinished Tasks and Send Recovery

| Durable phase | Action after restart | Reclamation condition |
| --- | --- | --- |
| Prepared, not started | Recover the original task; queue within valid execution authorization/business deadline, otherwise await reauthorization or explicit failure | Not eligible for terminal cleanup |
| Read-only running | Fence old execution/pages first; continue from a checkpoint or reread within the same task, reporting explicit gaps | After durable business-result delivery |
| Local artifact write in progress | Deterministic artifact ID, temporary file + validation + atomic commit; validate existing results and continue the original step | After reference handoff |
| External write, dispatch not yet reserved | Continue under original permission only after the old worker is fenced and non-dispatch is provable | Preserve the original request |
| Dispatch reserved/outcome unknown | Enter uncertain; perform only side-effect-free reconcile, never resend | After a trusted conclusion and completed delivery |
| Known sent, result not delivered | Reconstruct/read the original receipt and retry delivery without calling send | Durable ACK plus retention window |
| waiting_confirmation / cleanup blocked | Preserve the task; await its existing approval or resource-recovery process | Terminal rules apply only after completion is proven |

Send critical-section order:

1. Validate the current task, user-approved immutable message, account and attachment digests; verify old execution is fenced.
2. Create a unique `effect_token` and `dispatch_reserved` in one state transaction; synchronize the progress anchor and receive confirmation.
3. The current worker performs one external send with that token. At the actual dispatch boundary, Host checks execution epoch/lease and consumes the token through CAS against the sole state service. Sending is allowed only after the consumption commit receives anchor confirmation. Repeated consumption grants no execution authority; a local Host cache cannot replace consumption. Sending clicks, network submissions and SDK automatic retries cannot be transparently retried.
4. Persist `sent` and provider evidence after obtaining a receipt, then complete business-result delivery.
5. A crash anywhere between steps 2 and 4 conservatively enters uncertain, even if no send may have occurred. Automatic request recovery using a provider's reliable idempotency key is a separately approved and tested future protocol extension; this version does not enable it.

A provider query that finds no message, a timeout, expired credentials or a missing journal cannot prove non-sending. Only evidence bound to the original task/intent/execution epoch that proves dispatch was never crossed and old execution is fenced can establish `not_sent`. Insufficient evidence preserves unknown status. This version is conservative: once `dispatch_reserved` exists, crash recovery never automatically sends again. Even later not_sent evidence only resolves the task; it does not reset the token or allocate a second attempt. Only an original task that never reached reserved and is proven undispatched may automatically resume its first send. If the user chooses to send again, show the original unknown result explicitly and create a new independent operation; recovery logic cannot make that decision for the user.

Preserve original journals until migration into effect_records. After migration, one Executor owns provider evidence and task phase. Recovery cannot depend on in-memory objects, old authorization file paths or trimmed events. Each checkpoint records adapter/binding versions; an incompatible upgrade permits reconciliation of existing evidence only, never a guessed rerun with a new script.

## 8. GC Transactions and Crash Consistency

The background single writer owns GC. Queries remain read-only; status/lookup must not opportunistically delete data. Each batch has byte, row and time budgets, giving foreground recovery and cancellation priority. Execution, renewal, ACK and GC share task revision/CAS so an old candidate snapshot cannot delete newly referenced contents.

Shared blobs also have their own revisions and `LIVE -> DELETING -> DELETED` states. In one transaction, confirm refcount=0 and no upload/execution pins, then CAS to DELETING. Every new reference must verify the blob remains LIVE; deleting objects cannot be revived. New demand for the same digest creates a new blob instance ID/physical path, referenced only after revalidation. Deletion jobs bind instance ID and revision rather than deleting a new file solely by its digest path. This covers reference races between different tasks.

```mermaid
flowchart LR
    A[Terminal task and receipt confirmation] --> B[Seal request epoch and confirm progress anchor]
    B --> C[Transactionally mark reclaimable and remove references]
    C --> D[Delete unreferenced files and sync directories]
    D --> E[Delete details and retain epoch fences]
    E --> F[Batched checkpoint and space reclamation]
```

First transactionally mark deletion candidates, write gc_job/reference changes and confirm the anchor; then delete files; finally complete the batch and remove rows. Failures at any stage allow idempotent retries. Physical deletion is permitted only after the database records that content is no longer needed. Files created but not registered become reclaimable orphans only after a creation grace period and proof that no upload/preparation task references them. Never scan unknown directories and delete by file age.

Do not assume atomicity between database and files. A missing deletion candidate means that deletion step is complete. A missing file with an active reference is a recovery error: keep the task blocked/uncertain and never resend. GC errors prevent batch completion without rewriting task conclusions.

SQLite uses batched deletion, WAL checkpoints and controlled incremental vacuum; verify auto_vacuum mode during migration. Free pages can serve later transactions but still count toward physical storage. Never run a full VACUUM requiring a full database copy during low-disk conditions; reserve maintenance space in advance and allow pausing. GC jobs and completed migration markers also compact into high-water marks instead of creating another unlimited history.

## 9. Capacity Budgets and Protection of Admitted Tasks

There is no cumulative request-count gate. Admission uses current usage and committed resources:

`admissible = used_actual + reserved_unwritten + request_peak + maintenance_headroom <= owner_budget`

Also check filesystem free bytes, inode/file-count levels and commitments across all owners on the host. Actual usage includes DB/WAL/SHM, authorizations, journals, temporary/final artifacts, GC quarantine files, logs and the current maintenance copy. Charge shared files to explicit physical owners without releasing capacity twice. Transactional reference handoff transfers ownership to the product, while the physical file still consumes host disk.

Each binding declares input, maximum result, temporary artifact, recovery-evidence and WAL peaks. Prepare reserves the worst reasonable peak before admission; actual writes consume that reservation, stopping safely before exceeding the declaration. Before dispatch, a send must have room for its minimal final receipt and unknown-outcome evidence; it cannot send first and request space later. Continuous watches reserve a fixed event ring and authorization slots.

Proposed defaults are a 2 GiB runtime budget per owner, 32 MiB for events and 64 MiB for logs. A recovery-only reserve of `max(64 MiB, owner_budget × 10%)` is included in the total budget and unavailable to new tasks. Artifact limits are configured per binding and host deployment. The adjustable 2 GiB budget is not a cumulative lifetime quota. If migration estimates show that old pinned data exceeds the budget, revise capacity planning first; never delete old evidence to fit defaults.

| Level | Behavior |
| --- | --- |
| 70% | Run eligible GC, epoch rotation and checkpoints early |
| 85% | Coalesce compatible same-account probes/synchronization; defer nonessential new requests while retaining original-task controls |
| 95% or insufficient commitments | Prepare returns `STORAGE_PRESSURE` and retry-after without creating a task; recovery, cancellation and ACK use their separate reserve |
| Other processes fill the disk / anchor unavailable | Stop new side effects and state transitions requiring persistence; preserve unknown outcomes and await recovery |

Levels include actual usage plus unwritten reservations and use recovery hysteresis. GC failure triggers backoff, not rapid new-request retries. Admitted requests always retain their identities. Unknown-send backlog first limits new writes for the same account; exhausted owner/host budgets widen the admission restriction.

Metrics include total/category bytes, unwritten reservations, oldest unresolved task, unacknowledged receipts, GC delay, open-epoch age, authorization versions per task and free disk. Preserve original UI entry points; show “insufficient space” or “send result needs confirmation” only when user action is needed, without a permanent technical status bar.

## 10. Progress Anchor and Backup Recovery

Owner exclusive process locks, execution_epoch, Host generation and authority high-water marks remain. Request epochs reject old requests; execution_epoch fences old workers. They are separate concepts.

Ordinary crashes can recover using WAL and independent local authority markers. If every local file is rolled back together, none can independently prove that rollback happened. Protection against duplicate sends from such an old backup requires a trusted monotonic progress anchor outside the backup set. The proposal is a separate SparkClaw control store implementing `compare_and_advance(owner, component, generation, previous_seq, next_seq, state_digest)`. Each owner has product/runtime progress slots plus generation/minimum-storage-protocol state, a constant-size record without request bodies.

The product slot covers identity allocation, user intent, scheduled occurrences, outbox, result receipt and ACK. The runtime slot covers admission, authorization, execution and GC. They use independent CAS, connected by original operation_id and receipt credentials without atomic cross-database commits. Before allocating any new identity, sending prepare or emitting ACK, the product checks its own anchor rather than only checking runtime freshness.

Every commit that changes admission, authorization, send phase, ACK or GC safety is persisted locally first, then advances the independent anchor by CAS. Only success permits acknowledgment or side effects. Queries do not advance it. If unreachable, read-only diagnostics/existing snapshots remain available, while new side effects and GC stop. Already granted short Host leases still expire at their original deadlines. The owner single writer serializes writes; the anchor rejects old sequences and different digests for the same sequence.

An anchor ahead of either component's database indicates possible rollback. That component enters `RECOVERY_REQUIRED`, prohibiting treatment of old pending tasks as unsent or allocation of new identities for old user actions. Each component has at most one unconfirmed commit slot containing before/after sequences, before/after digests and a local recovery record. Until confirmation, further safety changes are blocked. Only a database exactly one step ahead, whose pending previous sequence/digest matches the anchor, may retry that original CAS; repeating the same next_seq/digest must succeed idempotently. An anchor matching the local sequence/digest confirms completion. An anchor ahead, differing digests at the same sequence, missing pending evidence or skipped sequences all block. Confirmation overwrites the fixed recovery slot instead of retaining a permanent commit chain. The cross-database crash matrix includes an advanced anchor whose response was lost.

The anchor interface authenticates component writers and rejects cross-owner updates, differing digests at the same sequence and unauthorized generation advancement. Bounded group commits may combine read-only task progress, but all dependent ACKs/external side effects wait for group anchor confirmation; performance never justifies early dispatch. Status polling and event reads do not write the remote anchor. Assess anchor latency and short outages separately before release.

A production independent anchor must not be restored from the same old snapshot as the runtime. If Gateway and runtime share a host, use an anchor backend in a separate failure/recovery domain or a trusted host monotonic facility. Deployments without an independent anchor promise ordinary process-crash recovery only; backup restoration enters manual reconciliation and cannot claim full rollback protection.

Backups include a consistent task/authorization/reference/artifact manifest and anchor sequence. Keep a bounded number, for example the last two verified snapshots, within deployment storage budgets. Old backups cannot directly start as online ledgers. Backup privacy retention after body deletion follows the same policy; unlimited backups are not a steady-state design.

## 11. Existing-Data Migration and Rollback

1. Inventory ledger, grants/index, journals, captures, Host leases and product outboxes. Calculate task classes, references, capacity and per-record identities/digests; create a consistent cold backup. Separately enumerate orphan authorizations, old indices without tasks, uncertain tasks and cleanup fences.
2. Build ledger 2 in an isolated copy and calculate shadow GC candidates. Do not delete production files or change current Lifecycle 2.0 semantics. Place old random request keys in a read-only `legacy_generation` index while retaining original task IDs/intents/states; never automatically allocate new identities to old writes.
3. Validate old authorization-chain signatures, resources and revisions; extract the current head and versions still used by valid leases. Unknown chains cannot be assumed invalid. Bind old send journals to tasks individually and preserve unknown files.
4. Drain new admission, fence old workers/Hosts, durably close legacy admission, establish a new generation and independent anchor, then atomically switch matched Registry/Runtime/Host/product consumers. Existing legacy tasks use dedicated task-bound controls. Unadmitted old requests return retired and cannot be sent retroactively.
5. Unfinished legacy tasks remain pinned. Current authorized principals may issue restricted recovery authorization for expired old grants without changing task identity. Enable GC only after real recovery, ACK and epoch-rejection validation.
6. Reclaim completed legacy content only when the new contract explicitly permits it, the product has received it, the old namespace permanently rejects new execution, and retention windows are satisfied. Preserve and report records whose conditions cannot be proven rather than forcing storage to zero.

Once schema 2 enables cleanup, `.23` and other ledger 1 writers may no longer open that state directory. Package manifests and an external minimum-storage-protocol fence jointly prevent downgrade. An old binary that cannot interpret new fields must still be unable to obtain the startup lock/Host execution authority. Before first activation, migration can be cancelled and intact v1 resumed. After activation, rollback is limited to corrective releases compatible with v2 storage. Restoring an old database/authority to roll back the package is prohibited.

The fence is enforced by a bootstrap/authority gate that is not rolled back with application packages or state backups. It fixes the minimum protocol and permitted releases; all supported launchers start through it. Cutover revokes old Host registrations/leases and proves old processes and page actions are fenced. A controlled service identity owns new state and Host control credentials, so the old installation user cannot directly write the new directory or acquire pages. A manifest, new database fields or a file lock released at process exit is insufficient to guarantee downgrade rejection. Deployments without these permission/startup constraints may claim compatibility checks for supported launch paths only, not prevention of arbitrary old programs. An administrator deliberately removing isolation or sending outside the product is outside this protocol's guarantee.

Directory backups, historical artifacts and migration copies also follow bounded maintenance policy. Old backups may be removed only after acceptance, effective independent anchoring and the agreed retention period. This design initiates no production cleanup and does not replace authorization for each node's rollout.

## 12. Acceptance and Evidence

| Scenario | Required result |
| --- | --- |
| Duplicate prepare/invoke with the same ID, changed arguments, cross-owner requests | Same task, explicit conflict or rejection; no additional external sends |
| Replay after epoch sealing and deletion of every reclaimable individual record | Retired, including reissued old-key authorization, old devices and new processes |
| Old outbox/business-database backup; old invocation_id wrapped in a new request | Product fence or protocol binding rejects it without creating another send |
| Product backup from before a scheduled trigger; product rollback after ACK | Product anchor blocks further allocation/ACK; the same occurrence cannot acquire a new operation_id |
| New epoch continues while old epochs retain pending/uncertain tasks | Original tasks can be legally recovered/reconciled; missing old IDs cannot be newly admitted |
| Every boundary around prepare/DB commit/anchor confirmation/response | Recover the original task or retain unresolved; never create a duplicate send |
| Before/after dispatch marking and Host sending, lost provider reply | Only an original task that never entered reserved and is proven undispatched resumes its first send; after reserved, reconcile and resolve only, with no automatic send even after not_sent |
| Old worker, Host restart, lease expiry, SDK retry | At most one actual dispatch per effect token; surviving executions are fenced |
| Renewal concurrent with GC/revocation/crash; lost renewal reply | Same task, monotonic revision, at most three slots; old authorization cannot acquire new resources |
| Crashes around ACK; duplicate/forged/cross-principal ACK | Release only matching durable results; retain undelivered send evidence |
| Every GC mark/file deletion/row deletion/checkpoint boundary | Idempotent convergence; active inputs/attachments preserved and replay still rejected |
| Another task references the same digest while a shared blob enters DELETING | Old instance rejects new references; the old GC job cannot delete the new instance |
| WAL/temp-file/log/inode pressure and real ENOSPC | Never cross the send boundary without evidence capacity; recovery reserve works; original tasks continue after recovery |
| All history resolves normally; repeat for at least two complete longest-retention cycles | At a fixed input rate/size, database, WAL, authorization-file count and total disk reach a plateau |
| Continually produce unreconcilable sends | Pinned evidence preserved; new writes pause within bounds, without infinite expansion or automatic resend |
| Migrate every v1 data class, restart failures, start an old binary | Original identities/intents/results match record by record; legacy stays permanently sealed; downgrade rejected |
| Whole-set old backup; missing/ahead/unreachable independent anchor | Explicit recovery block; old pending sends are never redispatched |
| Original UI and real mailboxes | Preserve original pages/menus/actions; separately verify collection, long watches, send receipts and disconnect recovery |

First exhaust critical interleavings with a fault-injected fake provider that counts sends. Then use isolated real Registry/Python/Unix socket/SQLite/Host components. Advance test clocks through the 30-day window, alongside real-clock load and disk-quota environments. Load reports include logical row counts, physical file bytes, WAL peaks, authorization-file counts, free disk and failure reasons. A test suite without errors is not evidence of a storage plateau.

A million completed requests can establish that lifetime history no longer determines storage; it is not a fixed throughput commitment. Stability assumes input rate within service capacity, the active/pinned working set within configured budgets, and eventual progress of receipt and GC. Fully unwritable or lost media requires a safe stop, without promising recovery of nonexistent data.

## 13. Implementation Order and Responsibilities

| Phase | Deliverables | Exit condition |
| --- | --- | --- |
| P0 Contracts | 0035 review, versioned Schemas/states/retention and anchor interface, cross-language conformance fixtures | Acceptance by SparkClaw and App-CLI maintainers; no presumed upstream acceptance |
| P1 Identity and recovery | Product outbox, epoch fences, prepare/lookup, dispatch token, independent anchor | Replay/crash/old-worker validation passes before cleanup is enabled |
| P2 Authorization and receipt | Fixed heads, revocation, lease handoff, task-bound recovery, ack_result | Long-watch and send-receipt failure matrix passes; version count remains bounded |
| P3 Capacity and GC | Classified storage, reservations, reclamation transactions, WAL/file reclamation and monitoring | Complete-retention-window steady-state and ENOSPC tests pass |
| P4 Migration and staged rollout | Legacy import, shadow candidates, paired release, controlled single-node activation | Original-data comparison, real business flows and old-package rejection pass before authorized expansion |

App-CLI owns the public protocol, Registry, Executor, authorization-head validation, evidence/artifact references, GC and paired release. SparkClaw owns product intents/outboxes, durable receipt/ACK, trusted authorization, independent progress anchors, the generic Host and integration with the existing UI. Both share protocol fixtures and the failure matrix; neither may add a separate send retry engine.

Capacity values are proposed configuration. Deduplication, unresolved-evidence retention, ACK, epoch sealing and rollback protection are mandatory semantics to accept and test. Decision 0035 separately resolves differences from the [original extraction design](app-cli-email-extraction-design.md). Non-mail delivery proposal 0031 independently remains proposed, and deployed facts from 0032–0034 remain unchanged.
