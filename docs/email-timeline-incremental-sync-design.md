# Email Timeline Incremental Sync, Carry-Forward Retry, Refresh, and Audit Design

> Language: English | [简体中文](../zh-cn/docs/email-timeline-incremental-sync-design.md)

Status: 2026-09-16, implemented and deployed with managed Reader 0.2 installed. QQ, Gmail and consumer Outlook have passed the installed, non-injected live acceptance matrix. Microsoft 365 is unverified and unsupported. See sections 15–16 for evidence and limits; deployment is not a claim that every acceptance or performance target below has passed.

The owner decisions remain: failures do not stop later new-mail polling; two consecutive qualified mail-specific failures suppress that exact mail permanently from ordinary polling and Refresh while retaining an acknowledged-or-unacknowledged warning. Operational failures do not consume this allowance. A non-empty batch publishes one recovery journal before renames; an empty round writes none. Overflow receives one frozen-interval confirmation before becoming a visible terminal coverage gap. This document supersedes normal polling, continuation and hot-path durability in [Email pipeline optimization](email-pipeline-optimization-design.md); source layout, authorization and retention follow [Email local download, processing and window display](email-local-download-storage-design.md).

## 1. Owner Requirements and Scope

This design freezes the following product decisions:

- A normal poll continues from the last successfully completed timeline boundary to a newly fixed upper bound. It does not repeatedly scan a mailbox page and then discard old rows.
- Normal expected volume fits in one provider response. Normal polling does not follow page continuations or re-read prior pages to certify a prefix.
- A power loss, process crash, or network outage is exceptional. The hot path does not pay a full audit, repeated hash pass, or per-file durability barrier on every poll. The system records a durable failure and the next scheduled poll carries a first-time failed mail once while still looking for newer mail. A second qualified failure for that same stable mail ID ends synchronization for that mail and warns the owner. The email-window Refresh button merely runs eligible bounded work immediately.
- Original messages are downloaded directly into the local email workspace. Classification, event assignment, and summaries are asynchronous and do not block the synchronization boundary.
- The September 11 deployment anchor remains the lower product boundary. The deleted legacy history and legacy receive chain are not migrated or restored.
- QQ Mail, Gmail, and Outlook share one state machine and result contract, but each provider keeps its own verified network adapter. A capability observed for one provider is never assumed for another.

The central objective is:

```text
live-tail poll watermark + contiguous-coverage watermark + retry-eligible exact IDs
  -> one qualified provider change/range request through a new fixed upper bound
  -> union of new stable message IDs and exact pending failures
  -> download or adopt only missing originals
  -> one local batch commit
  -> advance the live tail; advance contiguous coverage only across proved intervals
  -> update the retry backlog without loading terminal warning details
```

Polling cannot discover that nothing changed with zero upstream I/O. The zero-new-mail target is therefore one bounded incremental request, zero original downloads, zero source-file writes, zero parsing, and zero model calls.

## 2. Current Implementation and Historical Mismatch

Gateway and Reader now use one half-open time interval per normal round. The previous QQ head-page and Outlook folder-offset scans are not the timeline-v2 polling path.

| Provider | Current range transport | Qualification boundary |
|---|---|---|
| QQ Mail | Native `/list/search` with observed `after` / `before` predicates and exact `totime` validation | Installed, non-injected live matrix passed |
| Gmail | Native bounded `after:` / `before:` search, consuming the observed response without duplicate replay, then exact internal receipt validation | Installed, non-injected live matrix passed |
| Outlook consumer | `/searchservice/api/v2/query`, `EntityType: Message`, explicit receipt lower AND upper bound and complete inbound-folder scope | Installed Reader live matrix passed; details in section 16 |
| Microsoft 365 | No accepted transport | Unverified and unsupported; consumer evidence is not inherited |

These are `time_range` adapters, not provider change cursors. Second-resolution server envelopes may overlap adjacent precise intervals; exact receipt validation and stable-ID deduplication prevent repeated original downloads. They cannot discover arbitrarily late-visible mail whose old receipt time precedes the retained boundary. Ordinary polling never follows continuation pages or rechecks historical prefixes.

## 3. Terminology and Durable State

“Last read time” is not the timestamp of the latest message. It is the end of the last interval whose provider listing was proved complete. A discovered mail whose original is still incomplete does not pull that watermark back; it lives in the exact failure backlog.

Each owner/provider/mailbox/scope stores one `EmailSyncCheckpoint` equivalent:

| Field | Meaning |
|---|---|
| `scope_version` | Fixed value such as `timeline-v2`; prevents legacy page cursors from entering the new state machine |
| `deployment_anchor` | Immutable lower product boundary; September 11 for the current clean deployment |
| `discovered_through` | End of the latest contiguous, fully proved coverage; it stops at the oldest unresolved coverage gap |
| `poll_through` | Exclusive start of the next live-tail query; normally equals `discovered_through`, but may advance beyond a terminal overflow gap so new-mail polling does not stop |
| `inflight_until` | Fixed exclusive upper bound of the currently running attempt; a later scheduled attempt may choose a newer upper bound while retaining the same unproved lower bound |
| `provider_mode` | One typed value: `change_cursor`, `time_range`, or `anchored_head` |
| `provider_cursor` | Opaque provider change token only when its semantics have been live-qualified; never a synthetic page offset |
| `state` | `idle`, `running`, `incomplete`, `overflow_confirmation`, `coverage_gap`, `unqualified`, or `login_required`; it reports the last round and any coverage truth, while only `unqualified` blocks ordinary provider polling |
| `pending_failure_count` | Number of first-time exact failures eligible for one carry-forward attempt, plus unclosed discovery failures |
| `suppressed_mail_count` | Number of stable mail IDs permanently excluded after two qualified mail-level failures; these are warnings, not retry work |
| `coverage_gap_count` | Frozen overflow ranges that could not be proved complete after one confirmation attempt; these are warnings, not ordinary retry work |
| `last_error_code` | Stable safe reason for an incomplete boundary |
| `updated_at` | Store commit time for status and stale-work detection |

With no coverage gap, the next authoritative interval is:

```text
[poll_through, inflight_until)
```

Use provider receipt/change time, never the RFC `Date:` header, for discovery. Stable provider message ID remains the idempotency identity. For second-resolution provider predicates, query an inclusive one-second lower overlap and then apply the exact half-open interval and stable-ID deduplication locally. This bounded overlap prevents tied timestamps from being lost without reopening old history.

An original-download failure does not move either watermark backwards: its stable provider ID is durably retained in `EmailSyncFailure` and retried independently. A list failure has no proved candidate set, so `poll_through` remains unchanged; the next poll extends the same lower bound to a newly fixed upper bound and therefore covers both the failed range and newly arrived mail in one request. A terminal overflow gap is the only case in which `poll_through` may advance while `discovered_through` remains at the gap start; the exact frozen gap remains visible and the mailbox must not claim contiguous coverage.

`provider_cursor` is an optimization, not the only recovery authority. If it expires or is rejected, live-tail discovery restarts from `poll_through`, not from the current time; any older terminal coverage gap remains visible and is not silently reopened.

Before provider I/O begins, Store persists the attempt revision and `inflight_until`. A terminal failure clears the current in-flight marker while retaining its lower/upper bounds in the failure audit. If power is lost before an ordinary error can be written, startup converts that stale `running` attempt into a durable `interrupted` failure. Startup itself performs no provider call; the next scheduled poll or an earlier Refresh carries the interrupted work.

## 4. Typed Provider Contract

The Reader v2 contract replaces `listPage` on the normal path with a typed `listChanges` operation. The following is a conceptual internal contract, not a public HTTP API:

```json
{
  "account_address": "owner@example.com",
  "after": "2026-09-16T01:00:00Z",
  "before": "2026-09-16T01:20:00Z",
  "limit": 50,
  "provider_cursor": "optional-qualified-token"
}
```

The result carries:

```json
{
  "provider_mode": "time_range",
  "account_address": "owner@example.com",
  "items": [],
  "complete": true,
  "overflow": false,
  "next_provider_cursor": "optional-qualified-token",
  "provider_time": "2026-09-16T01:20:00Z",
  "unsupported_items": 0
}
```

Required invariants:

- Account, provider, interval, scope, stable IDs, and receipt times are checked before any candidate leaves the signed-in page.
- `complete=true` means the provider has proved the requested interval complete within this single response. It never means “the first page loaded.”
- `overflow=true`, an unsupported row, a malformed response, a changed account, or an unavailable request template prevents boundary advancement.
- A Reader advertises exactly one qualified `provider_mode` for the current account type. Gateway does not discover optional capabilities through a silent type assertion.
- Credentials, SID, cookies, bearer values, request templates, and provider download URLs never enter checkpoints, logs, or public status.

The three modes are:

| Mode | Semantics | Meets “do not ask for old mail again” |
|---|---|---|
| `change_cursor` | Provider returns changes strictly after an opaque committed token | Yes, when the token and reset behavior are live-qualified |
| `time_range` | Provider executes a receipt/change-time range predicate and returns only that interval | Yes, except a bounded precision overlap that is locally deduplicated |
| `anchored_head` | Provider cannot filter by time; one head response is read and stopped at the persisted anchor | No. It is a manual diagnostic mode only and is not enabled for ordinary automatic polling because it repeats old metadata |

## 5. Normal Poll State Machine

### 5.1 Admission

1. Acquire the existing single-mailbox capture lease. Different mailboxes may run concurrently; one mailbox cannot overlap itself.
2. Verify the signed-in account and the exact managed Reader generation.
3. Read the checkpoint, retry-eligible failure index, and any one open overflow confirmation. Set a new `inflight_until` once, preferring verified provider server time and otherwise using the bounded Gateway clock. The scheduler never loads suppressed-mail detail rows.
4. Normally call `listChanges(poll_through, inflight_until, limit=50)` exactly once. After a previous list failure, the unchanged lower bound automatically includes that failed range. If the immediately previous response overflowed, this one request instead repeats the frozen original interval as its sole confirmation attempt; it does not widen the interval.

### 5.2 Candidate selection

- Reject an account or scope mismatch.
- Normalize and sort by `(received_at, provider_message_id)` only for deterministic processing; do not infer provider coverage from that local sort.
- Form one deduplicated work set from newly returned stable IDs and exact-ID failures whose consecutive qualified mail-level failure count is exactly one.
- Check that work set against EmailRepository before requesting originals.
- Existing complete captures, deliberately purged captures, `sync_suppressed` stable IDs, and already landed adoptable captures are skipped. They are not downloaded, parsed, hashed, or analyzed again.
- Only missing originals enter the batch.

### 5.3 Local fast path

For the normal small-message batch:

1. The provider adapter obtains missing original bytes through its already qualified original operation.
2. Small originals may be returned in one bounded in-page batch; large originals use the existing bounded download receipt path.
3. One capture pass calculates size and SHA-256 while performing only a bounded header probe needed for `Date:`, identity evidence, and gross envelope checks. It does not fully parse bodies or attachments.
4. `message.eml` and `capture.json` are written into a fresh staging directory. Before any source rename, one bounded batch recovery journal records every stable ID, staging path, final path, byte count, and hash. A non-empty batch pays one journal-file durability barrier and one journal-directory barrier; an empty poll writes no journal and performs no `fsync`.
5. After the journal is durable, each staging directory is atomically renamed into its final owner-scoped directory. Source files and per-mail directories do not perform their own synchronous `fsync`. A crash can therefore lose source bytes, but it cannot lose the exact recovery target: startup follows the journal to adopt a verified final source, finish a valid staging move, or schedule a targeted reread without scanning the date tree.
6. One `PublishEmailSyncBatch`-equivalent Store transaction admits observations, registers every complete capture, enqueues exactly one full asynchronous MIME parse per newly committed source, and advances the checkpoint. The journal is removed only after the deterministic Store command is confirmed committed; a leftover journal is harmless and replayable. Memory, File, and PostgreSQL implementations must have identical semantics.

This deliberately trades synchronous power-loss durability of refetchable source bytes for lower normal latency. It does not silently trust missing bytes: after restart, a database capture whose file is missing or hash-invalid becomes `source_missing`, creates or updates an open failure, and is not advertised as available. Detection is an operational recovery event and does not itself consume a qualified mail failure. The exact ID becomes a target in the next scheduled poll and may be repaired sooner by Refresh; only a subsequent per-mail reread failure after all shared prerequisites pass starts that mail's two-attempt sequence.

### 5.4 Commit

Discovery and source materialization commit separately but atomically record their relationship:

- When `complete=true`, `overflow=false`, and `unsupported_items=0`, every returned stable ID must first be durably recorded as already complete, deliberately purged, newly committed, retry-eligible, or `sync_suppressed`. Only then may the same Store transaction set `poll_through = inflight_until`; if no earlier coverage gap exists it also sets `discovered_through = inflight_until`, installs the qualified next cursor, and clears `inflight_until`.
- Original, persistence, or verification failure does not block that discovery advance once the exact stable ID and retry evidence are durable. It updates the retry backlog instead.
- A malformed, partial, wrong-account, login-required, or failed list result does not advance either watermark. The next scheduled poll keeps the same `poll_through`, chooses a new upper bound, and reissues one combined range request. Overflow uses the separately bounded freeze-and-confirm transition below and never retries an ever-growing range.
- An unknown Store outcome is reconciled by the deterministic command key before a later round mutates the same mailbox state.

Parsing or model failures do not block either discovery or source commit. Every later poll still runs on schedule. A mail-level failure receives at most one carry-forward attempt: success resolves it without deleting audit history; a second qualified failure atomically marks the stable ID `sync_suppressed`, removes it from retry eligibility, and retains the terminal warning.

## 6. Provider-Specific Design

### 6.1 QQ Mail

Authoritative receipt evidence is `totime`; RFC sender time is not used for synchronization.

Implemented transport and qualification rules:

1. Native `/list/search` now executes observed `after` / `before` bounds, followed by exact `totime` validation. The current-source live matrix passed; installed-path qualification and synthetic-only edge cases remain explicitly separated in section 16.
2. The mode is `time_range`; original bytes continue through `/read/readmail?func=5`.
3. An account without accepted range evidence remains unqualified. `anchored_head` is diagnostic only, never an automatic fallback that repeats old metadata.

Do not simulate an incremental cursor with `page_now`, a row offset, total count, or a hash of the first page. New arrivals shift those values and can cause gaps. Normal v2 does not rotate folders or request a second page. A result larger than the one-response limit becomes `overflow` and leaves a failure trace. Normal business acceptance requires that one response covers the expected round volume; overflow follows the bounded freeze-and-confirm rule in section 7 and never grows the failed interval forever.

### 6.2 Gmail

Gmail implements `time_range`: the managed Reader binds native bounded search to an `after:` / `before:` query and validates the internal receipt timestamp. If no observed `/sync/u/<account>/i/bv` template exists, it triggers the native bounded search and consumes that response without a duplicate replay.

The v2 adapter must:

- keep the provider-side query as the primary filter;
- account for second-resolution/exclusive search semantics with at most a one-second lower overlap;
- apply the exact millisecond half-open interval and stable-ID deduplication locally;
- accept one normal response only; an opaque next token means `overflow`, not automatic pagination;
- fail explicitly when the observed list request template or account identity is unavailable;
- continue using the qualified Gmail original request and never fall back to DOM/menu export.

Gmail History API is not assumed: the current product uses a signed-in browser session, not separately provisioned Gmail OAuth scopes. A future History API adapter would be a separately qualified `change_cursor` mode.

### 6.3 Outlook

Consumer Outlook uses the observed native `/searchservice/api/v2/query` with `EntityType: Message`, explicit `received>=start AND received<end` semantics, and the complete inbound folder set proved by `findFolders`. Startup `findConversation` folder evidence and account identity must agree. Partial results, count mismatches, unknown entities, wrong folders/accounts and malformed responses fail closed; HTTP 200 alone is not coverage proof.

The stable provider identity is `Source.ImmutableId`, normalized by the native URL-safe Base64 mapping (`_` to `+`, `-` to `/`). Ordinary `Source.ItemId` may change after a folder move and is not an identity fallback. Missing immutable identity is unsupported. Folder is an observation, not a second-mail key.

Originals use the page's native webpack EML URL builder and native mailbox configuration. Routing aliases and tokens remain page-local; current DOM account, qualified startup identity and native configuration must match. The resolved URL is checked for the accepted origin/path, exact immutable ID and EML output format before download. The old assumption that `ItemExport` returns a `downloadUrl`, its template replay and arbitrary returned-URL ID rewriting have been removed. Originals are delegated only to the native resolver.

The earlier failed probe used collapsed `Conversation` results and adjacent receipt predicates without an explicit AND. That probe did not qualify its syntax; it did not prove that Outlook lacks range support. The corrected message-level AND request and immutable-ID original operation passed live acceptance (section 16).

Consumer Outlook is accepted as `time_range`, not Microsoft Graph delta. Microsoft 365 remains separately unverified and unsupported; no consumer evidence is inherited and no head-page fallback is enabled.

## 7. Scheduled Carry-Forward and Refresh

Scheduled polling and the owner-authenticated **Refresh** button invoke the same composite mailbox command. The only difference is trigger time: the scheduler waits for `next_poll_at`, while Refresh requests an immediate round.

Each round is deterministic:

1. Load only the retry-eligible first-failure index, at most one open overflow confirmation, and the current checkpoint. The worker never loads or scans suppressed-mail or terminal-gap detail rows. `suppressed_mail_count` and `coverage_gap_count` are transactionally maintained checkpoint aggregates; warning details belong to a separate paginated UI query.
2. Admit only exact failed stable IDs with one prior qualified mail-level failure. A fully verified landed source is adopted first; only a missing or invalid source is downloaded again. If this second qualified attempt fails, atomically mark the mail `sync_suppressed` and never admit it again.
3. Independently issue one `listChanges(poll_through, new_fixed_upper)` request for new mail. Thus known original failures do not block new discovery. The only exception is the one frozen overflow confirmation, which consumes this round's sole list request without widening its upper bound.
4. If the previous ordinary list failure occurred before stable IDs were known, `poll_through` was not advanced; the one new request naturally spans the prior failed range through the new upper bound.
5. Deduplicate retry IDs and newly discovered IDs before any original request, then commit successes, updated failures, and discovery state together.
6. On the first `overflow`, freeze exactly `[poll_through, inflight_until)` and set confirmation count to one. The next scheduled round or an earlier Refresh repeats only that frozen range once. If it overflows again, mark the gap terminal, increment `coverage_gap_count`, keep `discovered_through` at the gap start, set `poll_through` to the frozen gap end, and resume live-tail polling on the following round. Returned stable IDs may still be committed, but coverage remains explicitly incomplete. The terminal gap is never retried by schedule or Refresh and no hidden continuation loop starts.

Overflow means that one response cannot prove the frozen interval complete. It occurs when the provider returns more than the qualified limit, returns a next-page/continuation token, marks the result partial, contains unsupported rows whose omission prevents completeness, or fills an `anchored_head` response before reaching the requested lower boundary. A burst of more than 50 qualifying messages inside one poll interval is the normal example; more than 50 messages with the same provider timestamp is the boundary case. Provider throttling, login failure, malformed transport, and template loss are operational failures, not overflow.

The two watermarks deliberately separate truth from liveness. `discovered_through` never crosses the frozen gap, so the product cannot claim full historical coverage. `poll_through` may cross only after the single confirmation also overflows, so later new mail continues without paying for the failed range every time. This does not silently repair the gap; a future explicit audit or a shorter-range repair design would be a separate feature.

The conceptual manual command is `POST /api/email/sync/refresh`. The client supplies only the mailbox selection and checkpoint revision it displayed. The server resolves retry-eligible failures, the lower bound, and provider cursor from Store; the client cannot choose an arbitrary historical range or provider ID. A Refresh that meets an already queued or running scheduled round coalesces into that job. The button is disabled with visible progress until the round reaches a terminal state. Suppressed mail is read-only warning history and is never requeued by Refresh.

Login failures remain open until the original account is verified. Both a scheduled round and Refresh may verify or surface the existing sign-in handoff, but a wrong account or another login error does not clear the failure and does not disable future scheduling.

## 8. Failure Trace and Repair Model

Every special failure is durable. `EmailSyncFailure` is an internal conceptual record with:

- failure ID, owner, mailbox, binding generation, provider mode, Reader revision, and checkpoint revision;
- discovery lower bound and last attempted upper bound, plus failure stage: `list`, `original`, `persist`, `store_commit`, `verify`, `login`, `overflow`, or `interrupted`;
- safe error code, affected stable message IDs when known, failed/succeeded counts, and byte counts;
- consecutive qualified mail-level failure count, `first_failed_at`, `last_failed_at`, `last_attempt_at`, and state `open`, `retrying`, `resolved`, or `suppressed`;
- resolution or suppression time, method `scheduled_retry`, `manual_refresh`, or `two_failure_suppression`, actor (`system` or authenticated owner), and final safe result.

The exact provider IDs remain internal so targeted reread is possible; public UI and logs expose only a local warning reference, mailbox label, observed receipt time when available, stage, counts, timestamps, and a safe error. They never expose subjects, bodies, addresses, provider IDs or cursors, credentials, request templates, or download URLs.

A deterministic key merges repeated occurrences into the same failure. Exact-ID failures key on mailbox binding and stable ID, with stage retained as attempt evidence. An ordinary list-range failure keys on mailbox binding, stage, unchanged `poll_through`, and provider-cursor generation; its attempted upper bound may grow every round without creating a new active failure. An overflow record instead freezes both bounds and allows exactly one confirmation. Every scheduled retry and manual Refresh appends an audit event with trigger, actor, failure revision, invocation ID, start/end time, and outcome, while only qualified mail-level failures increment the suppression counter.

Every adapter outcome carries a required typed `failure_scope`: `mail_specific`, `provider_operational`, or `local_operational`. The two-failure threshold applies only to `mail_specific` after all shared prerequisites have passed: verified account and provider session, qualified list identity, writable local workspace with capacity, and available Store. Network, timeout, DNS, Controller unavailable, login/account, rate limit, provider 5xx, provider-wide template, disk-capacity, permission, or Store failures are operational; they do not increment a mail's qualified failure count and cannot suppress it.

A result may be `mail_specific` only after an exact stable ID was bound to an individual original operation and the failure evidence is item-scoped. Examples are a provider's per-item not-found/export rejection, per-mail size rejection, a malformed original, or a repeated per-mail hash/MIME verification failure. Stable-ID/receipt mismatch counts only when other exact operations using the same Reader revision succeed in the same round, or an isolated replay proves the mismatch belongs to that one ID. If two or more IDs in one round share the same supposedly mail-specific stage and safe error, the whole cohort is promoted to `provider_operational` before Store commit; no counters increment. The Store transaction persists the typed scope, cohort evidence, and counter transition together, so a later heuristic cannot retroactively suppress mail.

The first qualified mail-level failure remains `open` and is eligible for one later scheduled or manual attempt. Success changes it to `resolved` and ends the consecutive-failure sequence; the audit remains, but any later independent incident starts from zero. A second consecutive qualified failure atomically changes the failure and mail projection to `suppressed` / `sync_suppressed`; it is removed from pending work, rediscovery skips it by stable ID, and neither scheduled polling nor Refresh retries it. A successful materialization can therefore never be bridged by older failures into suppression. The transition also increments the mailbox aggregate exactly once and creates a local warning reference; it does not put the detail row back into the scheduler index.

| Failure | Next scheduled poll | Refresh |
|---|---|---|
| Network fails before a qualified list result | Keep `poll_through`; issue one range request through the new upper bound | Run the same combined range request immediately |
| A mail's first qualified original/sync attempt fails | Keep successful captures; retry that exact stable ID once while also listing new mail | Run the one remaining qualified attempt immediately |
| The same mail's second qualified attempt fails | Atomically mark `sync_suppressed`; never retry it and continue polling new mail | Show the terminal warning; do not requeue it |
| Process stops with staging present | Startup records `interrupted`; next poll removes invalid staging or adopts a complete verified source before targeted reread | Trigger the same bounded reconciliation immediately |
| Files land but Store commit fails | Reconcile the deterministic Store command, then re-hash only bounded landed files and adopt without downloading | Trigger the same reconciliation immediately |
| Store says complete but a file is missing/corrupt | Hide the original; if prerequisites are healthy, this becomes the mail's next qualified attempt | Trigger the remaining eligible attempt immediately; never override suppression |
| Provider cursor expires | Retry from `poll_through` using a qualified time range; keep any older terminal gap visible | Trigger the same fallback immediately |
| Login expires or account changes | Verify the original account once; retain failures and the discovery lower bound if unavailable | Trigger the same verification immediately |
| One response overflows for the first time | Freeze the exact range and use the next round's sole list request as one confirmation | Run the one confirmation immediately |
| The frozen range overflows again | Record a terminal coverage gap, advance only `poll_through` to its end, and resume later live-tail polling | Show/acknowledge the gap warning; do not requeue it |

There is no separate high-frequency retry timer, scheduled historical reread, daily mailbox repair, full-tree walk, or full re-hash. Exact-mail retry work piggybacks only once on the ordinary polling cadence; after two qualified failures it becomes warning-only state. Ordinary unknown list failures continue from the one unclosed `poll_through`; confirmed overflow becomes a frozen terminal coverage gap and leaves the hot path. A late-visible old mail that was never part of a known failed attempt remains an explicit limitation or a separate user-invoked audit feature.

## 9. Scheduling

- Compute `next_poll_at = completed_at + configured_interval`.
- Never start the same mailbox while its lease or unfinished attempt is active.
- Open failures never pause the mailbox scheduler. The next ordinary round carries each first-time mail failure once while also querying through a new fixed upper bound.
- There is no extra failure retry schedule. A mail can consume at most two qualified sync attempts in total; after the second failure it is terminal warning state and costs zero work in later polls.
- Suppressed-mail and terminal-gap detail rows are excluded from every scheduler index. Normal polling reads only the checkpoint, retry-eligible failures, and newly returned stable IDs; a batch repository lookup skips any returned suppressed ID without scanning the suppressed set.
- Refresh coalesces with an existing queued/running mailbox job and only advances the same schedule; it never creates a parallel retry storm.
- Different mailbox providers may run concurrently; model analysis remains on separate workers.
- Polling cadence can be recalibrated from measured direct-local synchronization time. Section 19 implements the selected 60-second post-completion default; this does not advertise a mailbox-settings interval control. Checkpoint semantics do not embed the previous 20-minute assumption.

## 10. Observability Without Content Leakage

Per round, record only bounded operational fields:

- provider/account binding ID, provider mode, Reader revision, checkpoint revision;
- `discovered_through`, `poll_through`, `inflight_until`, final state, safe error code;
- provider request count, returned item count, new stable-ID count, already-complete count, repaired count, overflow flag;
- original count and total bytes;
- `list_ms`, `download_ms`, `journal_ms`, `persist_ms`, `store_commit_ms`, and `total_ms`;
- trigger (`scheduled` or `manual_refresh`), failure revision, qualified attempt count, suppression count, and repaired/missing/corrupt counts.

Do not log addresses, subjects, bodies, cookies, SIDs, request templates, provider cursor contents, or download URLs. Public sync status may expose the mode and a redacted degraded reason, but not the cursor.

The status surface must distinguish:

- `incremental`: qualified `change_cursor` or `time_range`;
- `unqualified`: no live-qualified `change_cursor` or `time_range`; ordinary polling remains paused, while `anchored_head` is diagnostic only;
- `incomplete` / `overflow_confirmation` / `coverage_gap` / `login_required`;
- retry-eligible failure count, suppressed-mail count, terminal-gap count, oldest failure time, last attempt time, safe stage, next scheduled attempt, and whether Refresh is available;
- an owner-visible persistent warning such as “1 mail failed synchronization twice and will no longer be synchronized,” with a local warning reference, mailbox label, observed receipt time when available, safe error, and attempt timestamps; no subject, body, address, provider ID, or credential appears in logs or public status;
- source capture progress versus asynchronous parse/model progress.

Warning persistence and visual interruption are separate. Suppression and coverage-gap audit records are immutable. The owner may acknowledge a warning, which records `acknowledged_at` and `acknowledged_by`, removes it from the unacknowledged badge/banner count, and leaves the detail available in paginated history. Acknowledgement never resets `sync_suppressed`, closes a coverage gap, or makes the item eligible for Refresh. Sync status reads aggregate counts; detail rows are fetched only when the owner opens warning history.

## 11. Migration and Legacy Removal

The v2 cutover does not restore deleted history.

1. Keep the pinned September 11 deployment anchor.
2. If the current mailbox boundary is complete and its cursor is empty, copy it to both `discovered_through` and `poll_through`.
3. If a legacy interval is partial, use its start as both watermarks, discard `n1:`, `q1:`, page numbers, fingerprints, and page acknowledgements, and create one open migration failure. The next normal poll spans that lower bound through its new upper bound; Refresh can trigger the same round sooner.
4. Set `scope_version=timeline-v2` only after the provider capability and Store checkpoint write both succeed.
5. Delete the normal `collect_page` continuation loop, prefix-chain verification, legacy page checkpoints, and compatibility branches after all three configured providers have an explicit v2 mode. Do not keep two schedulers.
6. Implement the new checkpoint and batch command in Memory, File snapshot, and PostgreSQL together.

A provider without an accepted incremental capability remains `unqualified` and paused for ordinary polling; migration must not silently label a head-page adapter as `time_range` or enable `anchored_head` as a release fallback.

## 12. Implementation Plan and Acceptance Checklist

Stages 2–4 are implemented; managed Reader 0.2 and the runtime are deployed. The list below retains the intended gates, not a claim that every live/performance case is complete. Current provider acceptance is in section 16.

### Stage 1 — Provider qualification

- Capture fresh authenticated QQ, Gmail, consumer Outlook, and Microsoft 365 request evidence without exporting credentials.
- Prove exact interval/change semantics, empty interval, same-timestamp behavior, overflow signal, account binding, and original operation.
- Record a capability result per account type: accepted `change_cursor`, accepted `time_range`, diagnostic-only `anchored_head`, or unavailable.
- Produce an owner-facing acceptance report after live qualification. For each provider/account type it states `incremental_usable=yes/no`, accepted mode, exact tested request predicate or cursor semantics, empty/tied-time/overflow results, folder scope, old-row count, original-identity result, and any remaining limitation. Only accepted `change_cursor` or `time_range` may report `yes`.

### Stage 2 — Reader and Controller contract

- Add `listChanges` and provider-mode declarations to the managed Reader source.
- Replace common page-number/continuation logic in `scripts/email/lib/network-reader.mjs` with one-response validation.
- Add a bounded original batch operation where the provider permits it; keep large-message fallback explicit.
- Replace `collect_page` with `collect_changes`; remove per-mail page checkpoint replay from the normal path.
- Rebuild and bump all managed QQ/Gmail/Outlook userscripts and verify exact installed hashes.

### Stage 3 — Gateway and Store

- Add the timeline-v2 dual-watermark checkpoint, frozen overflow-gap ledger, one-retry exact failure backlog, terminal `sync_suppressed` registry, scheduler-excluding terminal indexes, warning aggregates, and deterministic composite batch command.
- Replace the 128-page loop in `page_intake.go` with the one-request state machine.
- Atomically record every discovered stable ID as complete, purged, or pending before advancing `poll_through`; advance `discovered_through` only across contiguous proved intervals, and publish captures and parse jobs in the same aggregate command.
- Implement backend parity and restart/unknown-outcome reconciliation for Memory, File, and PostgreSQL.

### Stage 4 — Local fast path, failure trace, and Refresh

- Add the bounded capture header probe, exactly one full asynchronous MIME parse, and no standalone reopen/re-hash pass. Integrity hashing that is required while parsing or serving must be computed inline with that read, not by another pass.
- Add one durable batch recovery journal for each non-empty source batch, with one file and one directory durability barrier per batch and none per source file; reconcile journals before targeted reread.
- Add durable failure records, typed `mail_specific` / `provider_operational` / `local_operational` classification, cohort promotion, the two-failure suppression transition, deterministic merge keys, attempt audit, and backend parity.
- Keep completion markers, owner/path checks, atomic rename, Store durability, availability probes, bounded targeted adoption, and targeted redownload.
- Make the ordinary scheduler carry each first-time mail failure exactly once; add freeze-and-confirm overflow handling, owner-authenticated Refresh for eligible work, scheduler-free terminal warning projections, paginated warning history, and acknowledgement. Startup reconciles bounded journals and stale attempts without scanning suppressed details or the date tree.

### Stage 5 — Cutover and live acceptance

- Enable one provider at a time from the retained boundary.
- Verify zero history before September 11, exact new originals, both-watermark advancement, and no old-original downloads.
- Publish the per-provider `incremental_usable=yes/no` report; do not enable ordinary polling for a provider that reports `no`.
- Measure empty and five-small-mail rounds before choosing the polling interval.
- Remove legacy page code only after rollback can restore the previous binary and checkpoint snapshot, not through a permanent runtime compatibility branch.

Likely implementation owners are:

- `scripts/email/userscripts/lib/reader-core.mjs` and provider transports;
- `scripts/email/lib/network-reader.mjs` and `read-capture.mjs`;
- Browser Controller lifecycle/batch invocation;
- `services/gateway/internal/emailmanagement` intake/recovery;
- typed EmailRepository contracts and all three backends;
- managed userscript manifest/build/deployment checks;
- sync-status/failure projections and the WebChat Refresh button, progress, safe failure history, and persistent suppressed-mail warning.

## 13. Acceptance Gates

### Common deterministic gates

- One hundred zero-new-mail polls advance distinct completed intervals and produce zero original requests, zero file writes, zero parse jobs, and zero model jobs.
- A normal poll makes exactly one provider list/change request and never requests a continuation page.
- Five new small messages produce exactly five logical missing-original operations and one Store batch commit; the adapter may transport those five operations in one bounded physical batch request. Rediscovery produces none.
- Multiple messages with the same provider receipt second are neither lost nor duplicated across a boundary.
- Empty intervals advance both watermarks when no earlier gap exists; malformed, partial, unsupported, wrong-account, and login-required results do not. First overflow freezes the interval; second overflow creates a terminal gap and advances only `poll_through`.
- Every network, original, staging, Store, login, and overflow failure creates or updates one durable record without disabling the mailbox scheduler.
- If one original fails in round N, round N+1 makes one new-mail list request and one targeted attempt for that exact ID; it neither lists old history nor redownloads successful originals.
- If that same mail's second qualified attempt fails, its state becomes `sync_suppressed`, later scheduled rounds and Refresh make zero requests for it, and the owner sees a persistent warning.
- Network, timeout, DNS, Controller unavailable, login/account, rate-limit, provider 5xx, provider-wide template, disk-capacity, permission, and Store failures are typed operational failures and never increment the two-failure mail counter.
- Two IDs with the same purported mail-specific stage/error in one round are cohort-promoted to provider-operational before commit; neither counter increments. A stable-ID/receipt mismatch counts only with same-revision successful peers or isolated per-ID replay proof.
- If the list request fails in round N, round N+1 uses the unchanged `poll_through` and a new upper bound, covering the failed range and later mail in one request.
- After a complete list response, source failure may advance `poll_through` only when every returned stable ID is durably represented as complete, purged, retry-eligible, or suppressed; `discovered_through` advances only across contiguous proved coverage.
- A Refresh at each failure point runs the same retained-range or exact-ID composite round immediately, without a silent gap or duplicate source.
- Repeated clicks for the same failure revision coalesce; success marks the failure resolved, a second qualified failure suppresses the mail, and neither transition deletes history.
- For a non-empty batch, the recovery journal is durable before any source rename. A process kill before journal publication leaves only bounded non-visible staging for the stale sweep; after publication, every journal/rename/Store/delete boundary recovers by exact journal path, produces no unindexed date-tree orphan, and adopts verified landed bytes before any provider reread. Empty rounds perform zero journal writes and zero `fsync`.
- Externally deleted/corrupt bytes are not advertised and become exact targets for one later attempt when the provider still has the original; a second qualified failure suppresses further synchronization.
- Login failures survive restart and wrong-account attempts; future polls continue on schedule, but only verified original-account recovery can resolve them.
- Parse and model failures do not move or roll back discovery checkpoints.
- Memory, File, and real PostgreSQL produce identical checkpoint and replay outcomes.
- With 10,000 suppressed warnings and no retry-eligible failure, one empty poll loads zero suppressed detail rows and performs no query proportional to the suppressed set. Warning history remains paginated and acknowledgement changes only presentation state.
- A first overflow repeats the same frozen `[start,end)` once without widening it. A second overflow creates one terminal gap, later polls never request that gap again, live-tail polling resumes from `end`, and status never claims contiguous coverage.

### Provider gates

| Provider | Required live evidence before `incremental` status |
|---|---|
| QQ Mail | Native server-side `after`/`before` receipt interval; empty interval; tied times; one-response new-mail set; `/read/readmail` exact originals |
| Gmail | Observed interval search replay; exact internal receipt filter; second-boundary overlap; empty response; opaque-token overflow; exact original replay |
| Outlook consumer | Observed server-side receipt filter or delta token; complete inbound-folder scope; stable ImmutableId; empty interval; native EML original |
| Microsoft 365 Outlook | The same gates independently on its own observed transport; consumer evidence is not inherited |

After these gates, the acceptance artifact must state `incremental_usable=yes/no` separately for QQ Mail, Gmail, Outlook consumer, and Microsoft 365 Outlook. `yes` requires live evidence for `change_cursor` or `time_range`; code existence, an installed userscript, a successful original download, or `anchored_head` is insufficient. Until that artifact exists, no provider in this design is claimed production-ready for true incremental polling.

### Performance targets

These are release targets, not current measurements:

- warm zero-new-mail round: P95 at or below 2 seconds;
- warm batch of five sub-1-MiB messages: P95 at or below 5 seconds through local source commit;
- normal round: one provider list request, one Controller task, zero continuation pages, one optional constant-cost recovery-journal commit only when sources are present, and one Store batch transaction;
- provider-returned old rows: zero in accepted `change_cursor` / `time_range` mode, except the documented one-second precision overlap;
- a first-failure carry round reports `retry_ms` and retry bytes separately from `list_ms`; `journal_ms` is also separate. Suppressed mail and terminal gaps add no later polling work. Refresh and model work are reported separately from the no-backlog polling baseline.

## 14. Explicit Non-Claims

- The accepted live evidence is limited to the account types and cases in section 16. No provider change cursor or Microsoft 365 support is claimed.
- It does not claim Gmail's observed request template is permanently available or equivalent to the official Gmail API.
- It does not guarantee discovery of arbitrarily late-visible mail with an old receipt time; bounded repair makes that limitation visible.
- Scheduled rounds and Refresh give an exact mail at most its one remaining qualified attempt, or retry an ordinary unclosed list lower bound when no mail ID is known. A confirmed overflow gap is terminal and excluded. Neither path is a hidden historical scan; suppression, acknowledgement, resolution, and terminal gaps never erase the audit trail.
- This design intentionally provides no automatic or Refresh override for `sync_suppressed`; a later product decision would need an explicit, audited reset operation.
- It does not add IMAP credentials, provider OAuth, push subscriptions, or a public cross-project email API.
- Updating this document does not itself change mailbox switches or originals; implementation and deployment evidence is reported separately below.

This is an internal SparkClaw change. It does not modify an InfiniCenter cross-project contract; other projects require no implementation follow-up.

## 15. Implemented State and Recovery — 2026-09-16

Implemented: dual watermarks, fixed in-flight intervals, bounded exact-ID failure carry, two-qualified-mail-failure suppression, persistent warning acknowledgement, one overflow confirmation followed by a terminal gap, and one composite Store transaction for observations, captures, parse jobs and checkpoint advancement. Non-empty source batches write one recovery journal before renames; empty rounds write none. Full MIME parsing is asynchronous. Refresh uses the same scheduler with authenticated actor evidence.

Normal polls and Refresh no longer request or claim legacy capture, thread-sync, mark-read or source-recovery jobs. A bounded, resumable and idempotent timeline policy retires persisted legacy queued/running/failed jobs while retaining prior error/attempt audit and persistent sync warnings. Late old leases cannot publish sources or complete into new parse work. Discover, parse and semantic jobs remain intact. Memory, File and real isolated PostgreSQL tests cover this migration.

Startup drains explicit purge tombstones, replays bounded journals and checks committed Store source pointers once; normal polls never scan historical uncaptured indexes, staging or the date tree. Unmarked staging is retained so cleanup cannot destroy an in-flight journal's only source. Legacy helpers remaining for explicit operations/tests are outside normal scheduling.

Exact retry targets retain provider-native identity and can fetch an original outside the new interval without a historical list lookup. In-flight journal replay precedes browser admission. A separate once-per-startup Store-pointer verification pass detects committed originals missing or corrupt after journal removal; it is not a polling scan or date-tree walk. A CAS command preserves the receipt, records an operational failure (also for paused mailboxes), and hides the unavailable original. Trusted exact manifest bytes saved in Store support recovery when both local manifest and index are gone. Damaged files and conflicting replacement bytes are quarantined; repair must match the first original hash and cannot revive suppression or user purges. Operational recovery never consumes the two-qualified-mail-failure threshold. Retry descriptors use a 64 KiB aggregate soft budget with a bounded 160 KiB single-head exception to prevent starvation; deferred targets remain open and the provider list remains 50. The 1 MiB whole-journal gate still applies. Automated recovery tests and real isolated PostgreSQL Store tests pass; destructive real-machine power-loss and full provider recovery acceptance are not claimed. Legacy helpers used by explicit source operations/tests remain outside the normal scheduler.

## 16. Latest Deployment and Live Acceptance — 2026-09-16

Managed Reader 0.2 is installed and the current implementation is deployed. Source-injected tests and actual installed-script tests remain distinct:

| Provider/account type | Current evidence | Incremental qualification |
|---|---|---|
| QQ Mail | Full live matrix passed using installed Reader 0.2.0 without source injection: 14 broad-range messages, 1 boundary-second message, adjacent intervals empty, limit-1 overflow, one fresh original and zero downloads on both replays | `incremental_usable=yes`, `time_range` |
| Gmail | Full live matrix passed using installed Reader 0.2.0 without source injection: 19 broad-range messages, 1 boundary-second message, adjacent intervals empty, limit-1 overflow, one fresh original and zero downloads on both replays | `incremental_usable=yes`, `time_range` |
| Outlook consumer | Full live matrix passed using the installed Reader without source injection | `incremental_usable=yes`, `time_range`, within tested scope below |
| Microsoft 365 | Not exercised | `incremental_usable=no`; unsupported |

Outlook's installed-path matrix returned 2 messages in the selected range, 1 in a one-second interval, and 0 in adjacent excluded intervals. A request limit of 1 produced overflow. A fresh immutable-ID original downloaded once and passed capture verification; both same-batch replay and next-batch rediscovery downloaded zero additional originals. These checks validate server-side message-range selection plus stable original reuse, not a change feed or every possible mailbox configuration.

The pre-navigation background-task marker and a local, readiness-driven account wait bounded at 5 seconds are implemented. The wait finishes immediately when ready; it is not an unconditional sleep or extra provider fetch. Installed-path diagnostic tasks measured QQ range queries at 13.7–14.3 seconds and one fresh original round at 16.5 seconds; Gmail range queries took 20.9–24.7 seconds and one fresh original round 26.8 seconds. These include browser startup, ownership checks, evidence instrumentation and cleanup, not just network/download time. Cleanup was approximately 0.6 seconds, not a fixed 26-second tail. These single-account samples do not establish the warm P95 targets above, which remain unproved; cold-page totals exceed them.

Production-shaped regression coverage also checks the empty retry list after JSON `omitempty` round-trip: absent and empty lists are equivalent, while nonempty identities, order and recovery descriptors still compare strictly. Otherwise a healthy no-retry round was incorrectly rejected as `email_script_invalid_output` despite passing isolated browser tests.

The final consumer-Outlook task samples measured range queries at 20.7–26.6 seconds and a fresh original round at 29.8 seconds, with the same measurement boundary and no second original download on replay. These totals must not be described as pure EML transfer time.

Production post-deployment check: Gateway `445c465388f1`, WebChat `0e16058fee0b`; all service readiness checks passed. After a controlled Gateway restart, the existing enabled QQ mailbox automatically queued the next eligible round, ran from 09:03:18 to 09:03:36 UTC (approximately 17.4 seconds), cleared the prior output-contract error, returned to `idle` and advanced `poll_through` to `2026-09-16T09:03:18.877449Z`. No direct database state correction was used. QQ stayed enabled and Gmail/Outlook stayed disabled; their installed-path qualification used isolated local capture workspaces. Full Go tests/build/vet, focused race, isolated PostgreSQL, Controller 360 passed plus one environment skip, WebChat 135 tests/build/707 translation keys and 80 bilingual document checks passed. Changes remain uncommitted/unpushed.

The live limit-1 overflow check is not a real burst of more than 50 messages. Real >50-message overflow and multiple messages sharing the same receipt second remain covered only by synthetic tests. Precision-envelope overlap is bounded metadata overlap, not permission to re-download completed originals. No production claim is made for destructive power-loss recovery, arbitrary late-visible old mail, or Microsoft 365. Persistent warnings, operational retries and two-qualified-failure suppression retain the semantics above; terminal warning details do not re-enter subsequent polling.

## 17. Read-path performance follow-up

See the [2026-09-16 performance report](email-sync-performance-20260916.md) for same-workload before/after results, retained failures, local durability timings and measurement boundaries. The older diagnostic totals above are historical observations, not the new comparison baseline. This follow-up does not change timeline/retry semantics or claim cross-poll session caching.

## 18. Cross-round page and connection reuse

The next [cross-round reuse optimization](email-cross-round-reuse.md) adds bounded owned-page/CLI reuse, transient Reader reset and freshness renewal of the intake-only admission proof. It supersedes section 17's no-cross-poll-cache limitation without changing time-range selection, checkpointing, retry budgets or persistent warnings. Cold starts still occur on expiry, identity changes or invalidated pages; independent warm-path measurements exclude model analysis.

## 19. One-minute cadence and single-flight Refresh

The owner has selected a continuously online operating scope and a default **60-second idle interval after collection finishes**. This is not a fixed wall-clock start every minute: a ten-second collection followed by sixty seconds idle has approximately seventy seconds between starts. Parsing/model work remains asynchronous and does not lock the Refresh button.

Required behavior:

- Automatic polls never overlap within one mailbox and missed timer ticks do not accumulate work.
- An idle manual Refresh advances the same mailbox task to run now, bypassing only the normal polling delay.
- A Refresh requested during an automatic collection retains at most one follow-up round, executed after the active round. Its upper bound is selected when that follow-up starts, so new arrivals after the active round's frozen bound are not silently excluded.
- Further Refresh requests coalesce while that manual request is pending or running. The UI locks immediately on the first click and remains locked until the backend reports that exact request finished; HTTP acceptance alone does not unlock it. Backend state is authoritative across tabs/remounts.
- After the manual round completes, the next automatic round is due after sixty seconds. Provider/operational retry backoff remains authoritative; Refresh does not override it or revive suppressed messages.
- Disabling receiving or invalidating the mailbox binding prevents a pending follow-up from starting. Existing originals, warnings, progress and receiving switches are not reset by the cadence change.

Polling must not use a time-slot idempotency key that suppresses rearming after completion. Persist the next automatic deadline in the completion transaction; do not introduce a new full planner/write cycle every second. Existing waiting automatic jobs may be reconciled to the new interval, but retry backoff and active leases must be preserved.

Shorter intervals reduce gradual accumulation but do not establish sustained throughput or solve >50-message bursts. The existing bounded overflow/terminal-gap policy remains. No offline catch-up redesign is included. Implementation and deployment acceptance for this follow-up are tracked separately from the historical performance tables above.

### Implemented scheduling and refresh contract

The Gateway default is now one minute. A stable activation command scoped to mailbox, binding generation and interval installs `poll_interval` on the existing discover job. The shared Memory/File/PostgreSQL Store engine persists the next deadline inside `FinishEmailJob`: successful automatic or completed manual rounds become idle queued work at completion plus 60 seconds; an automatic round with a pending Refresh schedules that single follow-up immediately. No new planner generation is needed after completion. The planner keeps its existing interval/wake mechanism; the existing worker's one-second due-job checks introduce no new per-second planner or command-log writes.

A healthy idle automatic job is excluded from the backlog counter. An upgrade shortens only a legacy ordinary queued deadline using its recorded completion/rearm time; it never advances a running lease or `retry_wait`. Operational failures retain their bounded retry deadline and error evidence. Exhausting a round's operational retry budget releases its manual lock and schedules the next normal heartbeat; this does not reset or revisit any mail's `sync_suppressed` state.

The refresh contract is mailbox-scoped:

- `POST /api/email/sync` retains `scheduled` and adds `refresh_requests: [{mailbox_id, refresh_request_id}]`. Concurrent requests receive the same existing pending ID, rather than adding jobs.
- `GET /api/email/sync-status` exposes each mailbox's `refresh_pending` and latest `refresh_request_id`. The ID remains after completion, so clients can associate an observed completion with the accepted request instead of mistaking an older idle response for completion.
- A request during an automatic round stays pending until the follow-up finishes. The job's separate active-refresh ID distinguishes that follow-up from the automatic round already in progress. A requested refresh undergoing operational retry stays pending through `retry_wait`; success or terminal exhaustion clears pending. Pause and binding replacement also clear it and fence old leases.
- The frontend must disable immediately before dispatch and reconcile the durable status; a network-uncertain HTTP result is not proof of completion. Multiple tabs and HTTP callers are ultimately serialized by the Store owner transaction, not by a browser-local lock alone.

Safe job timing fields `round_started_at`, `round_finished_at`, `next_attempt_at` and `poll_interval` support cadence acceptance without logging mailbox addresses, original bytes or credentials. These are collection/job timestamps, not model-completion timestamps. The automated contracts cover completion without another planner call, same-minute manual resets, restart, concurrent request coalescing, retry preservation, old-deadline upgrade and pause during an active round. These implementation checks are not a claim of completed live deployment, real one-minute soak, or new provider throughput measurements.

### Deployment acceptance — 2026-09-16

After the final Controller update and recovery, two consecutive automatic QQ rounds started 60.172 and 60.178 seconds after their preceding finishes. Their job durations were 0.373 and 0.424 seconds; both returned to idle without mailbox errors, advanced `poll_through`, and set the next deadline to finish plus exactly 60 seconds. The final observed watermark was `2026-09-16T11:31:13.443492Z`. These were empty incremental rounds, not downloaded-message timings.

Gateway `179425567cfd` and WebChat `c061675c08e4` were deployed, followed by the host Controller update. Browser PID `1620182` and receiving settings (QQ on, Gmail/Outlook off) were preserved. Readiness, external/PostgreSQL mode and container-to-Controller smoke passed. Full Go tests/build/vet, email race tests, isolated real PostgreSQL Store tests, Controller 419 pass plus one existing skip, WebChat 146 tests/build/707 translation keys, generated Reader consistency and 82 bilingual document checks passed.

In the actual UI, Refresh immediately became disabled/aria-busy/spinning and returned to enabled only after completion. One accepted refresh ran 11:26:30.628939–11:26:32.646191 UTC; the next automatic start was 11:27:32.812399, a 60.166-second idle interval. Its next deadline again equalled finish plus exactly 60 seconds. Automatic-running follow-up and duplicate cross-client submissions are covered by deterministic Store tests, not claimed as manually reproduced UI timing races.

The Controller restart briefly overlapped a due round: `email_provider_unavailable` was retained and the mailbox watermark did not advance. After readiness recovered, an actual UI Refresh ran 11:29:09.092009–11:29:12.715055 UTC, cleared the error, returned to idle, advanced the watermark and persisted its next deadline at 11:30:12.715055. No direct database correction or receiving toggle was used. A separate attach-timeout cleanup defect was fixed using exact private launch identity plus PID start-time checks; uncertain ownership retains recovery evidence instead of deleting it. The single confirmed orphan from the isolated test was terminated without restarting the dedicated browser. These short live checks are not a long-duration soak or destructive power-loss acceptance. New QQ/Gmail collection results are in the [command-batching comparison](email-cross-round-reuse.md#subsequent-same-version-command-batching-comparison); Outlook downloads remain unchanged and originals remain serial.

## 20. Retired-path removal and dependency separation — 2026-09-17

This is a code cleanup, not deletion of mail history or existing downloaded originals.

- Removed unreachable Gateway capture/mark-read/thread-sync/source-recovery executors and their legacy discovery/adoption implementation. The live timeline collector remains the only automatic intake path; a missing incremental reader fails explicitly instead of falling back.
- Removed the no-mode page checkpoint/ack loop. Internal `collect_page` requires an explicit `time_range` or `change_cursor` and empty continuation; old `ack_page_id` requests are rejected. Existing checkpoint files are neither read nor deleted.
- Extracted shared account identity evidence into `provider-account.mjs` before deleting obsolete DOM read/detail branches. Native sending retains its existing account checks.
- Extracted pure origin/tab/topology guards into `cli-page-guards.mjs`. The batched CLI helper no longer imports the entire task/DOM/download runtime; a dependency-closure regression prevents re-coupling.
- Removed the standalone QQ experimental userscript and its experiment-only test. Current managed Reader sources, installation version and generated outputs are unchanged. Historical trial documents are marked historical.
- Moved `mailparser` to development dependencies for historical offline tools. Production MIME parsing remains in the Gateway.

Retained deliberately: timeline crash/unknown-commit recovery, next-round failed-target retry, committed-source repair, startup purge-tombstone cleanup, historical warning projection and Store migration/claim fences. Explicit reply-target/thread lookup still needs network pagination; it is not the removed polling checkpoint loop. No sending, browser safety, persistent warning or two-failure suppression behavior is removed.

Validation: full Go tests/build/vet and email race tests pass; Controller 398 pass plus one existing skip. Tests cover retired executor rejection, lost commit-response reconciliation without another download, original checkpoint preservation and next-round retry. Reader generation and provider source contract checks pass. WebChat was unchanged; its baseline 146 tests/build/707 translation-key checks passed. Live and deployment acceptance are recorded separately below; these automated checks alone do not establish provider performance.

### Installed-Reader cleanup qualification

The corrected isolated run passed for QQ, Gmail and consumer Outlook in 258.29 seconds total. Each provider passed native interval results (15/20/2 respectively), exact-second inclusion/exclusions, limit-1 overflow, one verified fresh original, and same/next-batch zero-download replay with unchanged manifest hash. Reader 0.2.0 was already installed; no source injection or receiving-switch change was used. This is a functional matrix with extra download-observer browser calls, not a new latency benchmark or real >50-message burst qualification.

Retained failures: the first harness run omitted the required existing private workspace directory, so QQ/Gmail could not enter local capture; that setup error was corrected before rerunning. First-run Outlook returned `email_network_read_failed` on a boundary query. The unchanged-policy rerun passed, but does not establish that Outlook variability disappeared. Fresh original stages including instrumentation were 4.144/7.513/29.254 seconds; they are not comparable to the earlier uninstrumented warm benchmarks. Isolated real PostgreSQL Store tests also passed (84.144 seconds), and the disposable test container/volume was removed; production data was not used for destructive tests.

### Cleanup deployment acceptance

Gateway `e0946c8f280d` and the host Controller were updated; WebChat `c061675c08e4`, browser PID `1620182`, managed Reader installation and QQ-on/Gmail-Outlook-off settings remained unchanged. External/PostgreSQL readiness, container-to-Controller smoke and installation checks passed.

After the Controller restart, automatic QQ rounds ran at UTC 02:29:15.348957–02:29:19.288606 and 02:30:19.485480–02:30:19.890784 on September 17. Both returned idle without errors and advanced the watermark. The latter began 60.197 seconds after the preceding finish and took 0.405 seconds for an empty incremental collection; both persisted the next deadline at finish plus exactly 60 seconds. The first cold round took 3.940 seconds. These are short collection checks, not model completion timings or a sustained-throughput claim. No direct production database repair, receiving toggle, browser restart or production email deletion was performed.
