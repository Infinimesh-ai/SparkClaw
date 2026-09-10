# Stage 1: Identity, Storage And State

> Language: English | [简体中文](../zh-cn/docs/email-management-stage-1-data.md)

Implementation contract, 2026-09-08. Code and engineering checks are in the worktree;
real-provider and semantic release gates remain separate. See the
[implementation report](email-management-implementation.md). Parent: [system design](email-management-design.md).
Establish one durable authority for intake, analysis and UI, rather than treating
script directories as an implicit database.

## Records

Keep the [three-layer workspace](email-read-design.md): source, deterministic
representations and immutable analysis. Store owns management records through a
proposed EmailRepository, separate from Agent ConversationRepository and Sessions.
Do not introduce another SQLite or queue database.

| Record | Minimum contract |
|---|---|
| Mailbox | owner, stable mailbox ID, verified address, provider, binding version, active/paused binding, intake settings and admitted discovery boundary |
| Mail | stable mail ID, source mailbox/provider message ID, direction, discovery time and committed pointers; unique owner/mailbox/provider-message key |
| ProviderThread | scoped mailbox/thread ID, membership observation version, cursor, coverage and last check |
| ThreadMember | individual identity, folder/direction/draft evidence, discovery reason and capture state; multiple locators only for proved identical mail |
| Capture/Representation/Analysis | immutable version IDs, manifests, hashes and exact input versions |
| EmailConversation | independent ID, owner, title, participants, source mailboxes, membership version and summary pointer |
| Membership | mail, conversation, decision, evidence and version; at most one current assignment per mail, with history retained |
| AssignmentDecision | create/append/pending, candidate version, model/rule version, reasons, evidence, inputs and commit result |
| AssignmentConcern | owner, affected mail/conversation IDs, suspected_duplicate/pending_correction, evidence and input versions; independent of committed membership |
| AnalysisDependency / RefreshIntent | summary/assignment/check target, committed and unresolved input references, desired generation, input fingerprint and durable fan-out progress |
| Job | ID, kind, target, idempotency key, state, attempt, lease token/expiry, next attempt and error |
| SyncRun | mailbox, trigger, scan/backfill progress and gaps; separate from analysis and user state |
| ViewReceipt | unique owner/mail ID, first_viewed_at; one receipt per actually presented mail, independent of conversation and remote read state |
| OwnerAssignmentEpoch | assignment-index version rejecting stale concurrent proposals; does not guarantee semantic grouping correctness |

Preserve address originals and normalized lookup forms. Normalize domains by a
central policy; do not strip plus tags/dots or merge display names. Keep verified
source-binding rules. Runtime allocates conversation IDs, not models or address
hashes. Conversations can span receiving accounts within one owner.

Provider threads and management conversations are not constrained one-to-one:
a thread can change topic and multiple threads can concern the same topic.
Thread lookup returns candidate conversations derived from memberships.
RFC Message-ID alone is not a deduplication key. Copies in different accounts
retain originals; only proved copies gain display associations. Ambiguous copies
remain distinct.

## Account Binding

The first release supports one active browser account per owner/provider, matching
the existing default-account settings. Different providers can receive together;
multiple simultaneous accounts of the same provider are outside this release.
Previously admitted mailboxes remain queryable and can share conversations.
Extend the existing settings/login flow with verified mailbox identity and a
separate default-off intake switch; enabling sending does not enable intake.

Changing the actual account pauses the old mailbox's browser jobs and fences its
binding generation before activating the verified replacement. Already running
browser work stops within its shutdown budget; new work waits for the Controller
lock. Check mailbox/generation before every new browser operation and publication.
An operation already in flight may have affected the old account; retain its
receipt for reconciliation, without admitting it under the replacement binding.
Unexpected account mismatch pauses intake and requires explicit verified rebinding.
Keep old jobs, sources, analysis and viewing receipts; local analysis can continue.
Rebinding the same verified account preserves mailbox/mail IDs and resumes pinned
work under a fresh generation. A new account gets a different mailbox ID and its
own discovery boundary. Credential rotation alone does not change mailbox identity.

## State Boundaries

| State | Meaning |
|---|---|
| Remote unread/read/unknown | Timestamped observation, never queue state |
| Job queued/running/retry_wait/succeeded/failed | Running requires a valid lease |
| Capture complete/partial/failed | Source coverage, not successful interpretation |
| Parse ready/partial/unsupported/failed | Includes attachment extraction coverage |
| Assignment pending/assigned | Received sources stay accessible during uncertainty |
| Assignment concern | suspected_duplicate/pending_correction is an additional visible record, never a membership move or an unassignment |
| Summary pending/current/stale/failed | New members invalidate the current pointer, preserving history |
| Local unseen | An admitted mail has no owner/mail ViewReceipt; conversation count is the count of its unseen members |
| Thread pending/partial/complete_for_observation | Coverage of a bounded observation, not eternal completeness |

Do not add user task states such as completed or awaiting reply here. A late
historical message receives a new arrival sequence even though its timeline
position uses source time. Arrival sequence is for refresh/event ordering only,
never a viewing watermark. Receipts also apply to pending mail and survive later
assignment; repeated discovery or summary refresh does not reset them. Viewing one
cross-account copy does not implicitly acknowledge another source mail ID.

## Atomic Repository Commands

These are proposed semantics; implementation must define typed Go contracts with
context and stable idempotency keys.

| Command | Atomic invariant |
|---|---|
| AdmitDiscoveryBatch | identities, observed members, deduplicated capture jobs and admitted cursor |
| BindMailboxForIntake | verified account selection, binding generation, paused old browser work and preserved/new discovery boundary through the existing settings flow |
| ClaimJob / RenewJob / FinishJob | conditional leases and stale-worker exclusion |
| PublishCapture | verified source pointer, parse intent and mark-read intent only for complete capture; changed inputs durably invalidate dependent analysis |
| PublishRepresentation / PublishContext | immutable representation or relation/coverage version, invalidation generation and durable refresh intent, including already assigned mail |
| ExpandRefreshIntent | bounded dependency lookup and refresh-job admission with its continuation in the same commit; includes unresolved reply references and affected conversations |
| CommitAssignment | input/owner-epoch check, creation or first assignment, decision, member version and summary refresh intent; cannot move an assigned member |
| PublishAssignmentConcern | validated same-owner evidence and versioned concern links; no membership mutation |
| PublishMessageSummary / PublishConversationSummary | compare the desired generation and full input fingerprint, including extraction/context/coverage and relevant summary versions; stale output remains history and latest refresh stays pending |
| MarkMailsViewed | idempotently insert receipts for at most 100 explicit admitted mail IDs after owner validation; no range expansion or provider mutation |
| ReconcileCommand | reconcile unknown outcomes by stable key/content before retry |

Complete and verify attempt-scoped files before publishing Store references.
There is no distributed filesystem/database transaction. Unadmitted files stay
out of UI; missing committed files are integrity errors, not unseen mail.
Memory/File/PostgreSQL share contract tests. File uses one locked durable
replacement per aggregate command; PostgreSQL uses a transaction. Browser and
model work run outside transactions.

Input publication and refresh intent are one atomic command. Large dependency
fan-out runs in resumable batches; readers and publishers compare committed input
versions even before fan-out finishes, so an old summary cannot appear current.
Jobs deduplicate by owner/kind/target/input fingerprint and use the latest desired
generation. Superseding work cannot be cleared by completion of an old attempt.
Unchanged-input manual requests coalesce with running/current work; an exhausted
failure can be explicitly rearmed as a bounded attempt cycle. Startup resumes
unfinished intents/jobs; exhausted failures stay visible for retry. Dependency
failure never restarts browser capture. Stage 3 defines each refresh trigger.

## Acceptance And Existing Data

Cover duplicate discovery, restart, lost commit receipts, expired leases,
concurrent new-address decisions, cross-account topics, late arrivals and stale
summaries on all backends. Only one concurrent create can commit against an old
assignment epoch; the other retrieves fresh candidates and re-evaluates.
This is a concurrency guarantee, not a guarantee that incomplete context never
creates semantically duplicate conversations. Exercise retained memberships plus
concerns after late evidence, per-mail receipts with pagination gaps, binding
switches, and crashes between invalidation and refresh fan-out.

Existing script directories require explicit verified admission. Map existing
mail IDs when necessary without rewriting source paths. Private qualification
directories are excluded by default. No cross-project interface or existing send
behavior changes.
