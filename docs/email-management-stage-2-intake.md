# Stage 2: Deterministic Intake And Thread Synchronization

> Language: English | [简体中文](../zh-cn/docs/email-management-stage-2-intake.md)

Implementation contract, 2026-09-08. Code and engineering checks are in the worktree;
real-provider and semantic release gates remain separate. See the
[implementation report](email-management-implementation.md). Depends on [stage 1](email-management-stage-1-data.md).
The owner confirmed bounded backfill of encountered threads, including available
inbound and Sent history, followed by incremental synchronization rather than
whole-mailbox historical scanning.

## Page Collection Update — 2026-09-09

Automatic receiving now collects a bounded mailbox page in one browser task:
scan the current list, persist its inventory, open each proved target, download
native EML, save MIME parts and confirm read after durable local source storage.
Return to the same list between mails. A page contains at most 50 proved messages;
virtual lists and incomplete conversations remain explicitly partial coverage.

The default idle interval is 20 minutes after the entire round completes. Page
scripts have a 30-minute budget; up to three lanes have a 95-minute outer budget
with three-minute leases renewed every minute. Transport failures retry after at
least 20 minutes (at most five attempts). Individual export failures retain the
unacknowledged page for the next normal round while other lanes proceed. A partial
attachment extraction with a saved original is a terminal browser result, keeping
its gap markers for local processing rather than endlessly downloading again.

QQ, Gmail and Outlook run concurrently; each mailbox processes pages and their
members serially. Automatic receiving starts only page jobs, superseding old
capture/mark-read/thread-sync queue entries on first page admission without
fabricating success or read state. Explicit single-mail workflows retain their
existing APIs. Encountered proved non-draft thread members are collected in the
current page; unresolved members/folders/pagination remain unqualified gaps.

Before opening a mail the local page checkpoint is durable. Gateway later verifies
and publishes individual sources before acknowledging that page. Missing acks
replay verified receipts; failed members are retained even after becoming read.
Checkpoints survive pause/re-enable for the same mailbox. Immutable source IDs
are shared across unread/recent lanes, avoiding repeated downloads. Actual account
and credential fences remain in every browser call. Model and parser jobs still
consume individual committed sources and preserve per-mail viewing receipts.

Unread priority and unread-independent recent inbound lanes remain separate, with
an extra latest-overlap observation while historical continuation is pending.
Only the returned checkpoint's original interval can advance progress. The
24-hour overlap never predates activation; unknown coverage cannot advance the
completed boundary. No full-provider pagination or real-mail speed qualification
is claimed by the offline tests. The three receiving switches remain disabled. Observed inventories larger than50 targets retain their remaining members across acknowledged sub-batches, even if opening the first member clears the thread unread state.

The sections below describe retained coverage requirements and compatibility operations.

## Scheduling

Gateway owns the timer, not the popup. At most one scan per enabled mailbox is
active; ticks coalesce and startup recovers unfinished jobs. Model backlog does
not block capture; queue capacity applies backpressure without advancing progress.
QQ, Gmail and Outlook intake run concurrently in separate task pages. The browser
worker pool has one slot per registered provider (currently three); durable Store
claims allow only one live browser job per owner/mailbox across discovery, capture,
thread enumeration and mark-read. Provider admission also serializes access to the
same site's account across owners. A slow mailbox does not block the other sites.
Generic MCP, sending, login handoff and token validation remain exclusive browser
operations; waiting exclusive work drains current intake before admitting more.

Current defaults are a 20-minute idle interval and at most 50 proved targets per page,
as specified in the update above. Tasks have deadlines and bounded backoff. Disabling intake stops new
discovery; claimed work follows the shutdown budget. Backfill has lower priority
than new arrivals and large threads must yield.

## Discovery Coverage And Account Lifecycle

Discovery has two lanes: unread discovery for priority, and recent inbound
incremental discovery regardless of read state. First activation persists an
activation boundary; the second lane covers inbound mail received since then,
including new threads read on a phone before the first tick. Older unread mail
may still be discovered; older read mail is backfilled only through encountered
threads. Inventory folders/labels carrying ordinary inbound mail, including mail
moved out of Inbox by ordinary rules; exclude Drafts, Spam and Trash and expose
the provider's qualified scope. Sent history/additions use thread synchronization;
new unrelated outbound threads are outside the inbound discovery promise.

Each recent scan fixes an upper boundary and resumes from the last fully admitted
boundary with a default 24-hour overlap, never earlier than activation. Use proved
provider receipt/change ordering, not sender RFC Date. Lack of a reliable boundary
or continuation is partial coverage, not success. Persist pages and candidates
before progress; only a fully enumerated interval advances the completed boundary.
Stable cursors or bounded overlapping rescans must cover mutable lists. An interval
larger than one batch is continued across ticks, not silently truncated to 50 rows.
While that interval remains partial, an independent recent observation checks the
latest overlap so its fixed upper bound cannot hide later already-read arrivals.
This observation never advances or replaces the historical boundary/cursor; its
limited page/folder scope remains explicit until provider coverage is qualified.
Periodic discovery waits at least one scan interval after the previous job finishes,
even when that job crosses several planner ticks. Queued work retains its original
due time across wakeups and restarts; exhausted failures require explicit retry.
One discovery round shares one verified login admission across its lanes, while
every script still validates its account, settings and browser credential generation.

During downtime, pause or backpressure, retain the old boundary and continue it
on resume even when older than 24 hours; never replace it with now-minus-overlap.
If provider retention/search limits prevent recovery, show the missing interval.
The overlap bounds routine rechecks, not a proof against arbitrary late visibility;
record demonstrated provider limitations and never claim whole-mailbox completeness.
Honor stage 1 binding fences; returning to the same account resumes its boundary,
while a new account starts independently. Intake pause stops new browser claims
and discovery, cancels the mailbox's active background browser operation and
closes its task page within the independent cleanup budget. Local analysis
continues from committed sources.

## Script Contracts

| Operation | Input | Output/effect |
|---|---|---|
| collect_page | verified account, lane, interval/cursor and prior page acknowledgement | same-page collection, source receipts and durable page checkpoint |
| discover | verified mailbox/binding, unread or recent-inbound lane, folder scope, interval/continuation and budget | individual candidates, receipt/change-order evidence, thread locators, read/folder evidence and coverage; no download or explicit mark-read |
| enumerate_thread | pinned thread, continuation and budget | individual members with draft/direction/folder evidence |
| capture | durable individual target and attempt ID | one original, MIME attachments, manifest and observed read effects |
| mark_read | individual target with committed source | independent effect receipt and read observation |

Existing discover/capture, enumerate_thread and separate mark_read remain
compatibility APIs. Register through the single Registry.
Targets must support actual folders, including Sent, instead of Inbox-only
recovery. Preserve originals with native Playwright download/saveAs and extract
MIME attachments without synthesizing originals from DOM.

Current attributes, visible markers, expanded rows, menus and the page's own
responses are valid deterministic evidence. Providers need not expose identical
evidence. No future page-version adaptation or model browser repair is designed.

## Page Checkpoints And Source Publication

Use the persistence and acknowledgement ordering in the page update above.
The following thread coverage requirements remain in scope.

## Backfill And Incremental Work

Scope thread IDs by mailbox and enqueue newly observed individual IDs, including
read inbound and Sent history. Exclude drafts from formal mail and timelines.
Enumeration, capture and analysis completion are different states. Recheck
registered threads when rediscovered and through bounded background rotation:
prioritize active/incomplete threads, then rotate others. Unread-only triggers
would miss replies sent through the provider UI.

Outlook global counts include other folders; missing rendered rows require
coverage/continuation, not an assumption that every difference is a missing
inbound message. Gmail search matches may be only part of a thread. QQ views
without an enumerable thread retain actual reply headers and use explicit links
or deterministic lookup for bounded backfill; unresolved history remains a gap,
not a subject-based invented membership.

Use bounded rescan plus durable deduplication where unread pagination lacks a
stable cursor. Never advance offsets through a shrinking unread set. Thread
recovery does not depend on remaining unread. Distinguish empty, partial and
complete_for_observation.

## Provider Work And Acceptance

| Provider | Existing evidence | Required work |
|---|---|---|
| QQ | data-mailid, individual styles and source capture | real replies, current-view relationships, individual unread/read effects |
| Gmail | message labels, legacy IDs, message download menu | remove singleton prerequisite, distinguish members/drafts, expand and export exact target |
| Outlook | ancestor row IDs match ItemIds; item download menu | prove original identity, current Chinese controls, folder/loading and unread evidence |

For each provider qualify ordinary mail, replies, multiple unread members, read
history, attachments and pinned retry. Exercise crash after enumeration,
automatic-read then failed download, partial loading and identical filenames.
Also test new threads read before discovery, automatic folder moves within scope,
more than 50 arrivals, equal timestamps, pause/restart longer than the overlap,
and changing then restoring an account. Qualify recent-scan ordering and scope
separately for all three providers. Recovery must preserve jobs and avoid duplicate
source publication. This stage
does not assign management conversations or wait for model completion.

## QQ Reader And Empty Pages (2026-09-09)

QQ page capture verifies matching active mail.id records on the visible subject
and body React hosts, individual/detail flags and consistent isUnread state. It
does not require a selected list row after the reader opens, and does not guess
RFC identity from QQ's opaque internal messageId. Native EML remains the source
for headers and body; source persistence precedes explicit read confirmation.

An observed bounded page without candidates, captures, failures or unsupported
rows can finish as empty while retaining partial discovery coverage and its
continuation. Evidence-unavailable observations remain partial. Empty pages do
not create per-message work or trigger retries. Necessary unread-independent
recent checks remain; an unread-only empty observation is not whole-mailbox proof.
