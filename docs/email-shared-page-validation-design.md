# Mail observer and Reader shared-page validation design

> Language: English | [简体中文](../zh-cn/docs/email-shared-page-validation-design.md)
>
> Status: shared pages are the default as of 2026-09-28, following the user’s
> decision to use the merged layout and address issues during use. An unset flag
> or `SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE=1` selects shared pages; explicit `0`
> selects the separate-page fallback. Paired live reads verified six → three
> task pages. Full B/C qualification remains incomplete; the rollout decision
> does not mark those cases as passed.

## 1. Objective and scope

Validate whether one owned **headed Chromium task page per eligible mailbox** can
host a resident notification observer and successive Reader collection rounds.
For the currently enabled QQ Mail, Gmail and consumer Outlook bindings, the
steady-state target is three mail task tabs instead of six. They remain in the
existing unfocused, task-only background window. Personal and login pages are
not included in this count and must never be adopted as collection pages.

The objective is less visible task-tab clutter while preserving collection
correctness and notification behavior. A smaller tab count alone is not a pass.
Listener state and Reader state remain distinct even though they share one page
and one CLI connection. Browser subprocess count need not equal tab count.

This design implements the shared-page qualification requested by
[notification wakeup lifecycle](email-notification-wakeup-design.md#7-observer-and-page-lifecycle).
It excludes headless mode, hidden CDP targets, Electron migration, new browser
instances, cookie transfer, multi-account expansion, notification-rule changes,
and changes to polling cadence, coverage rules, public APIs or cluster contracts.
Sending, login, probes and other registered operations keep their existing
admission semantics; they do not borrow the shared collection page.

## 2. Verified baseline and gaps

| Area | Original separate-page baseline | What a shared page must prove |
| --- | --- | --- |
| Observer | `ResidentMailObservers` creates its own `PlaywrightCLITask`, owned page and daemon; installs hooks before navigation | Observer can remain resident on the exact page used by Reader |
| Reader reuse | `MailReadPool` retains a separate successful `collect_page` lease; 30-minute idle expiry, no fixed two-hour age cap | Watch residency is independent of Reader idle expiry |
| Admission | Idle observation releases the provider reservation; Reader commands acquire it | Page residency never becomes a permanent reservation |
| Round cleanup | `prepareMailRound` / `parkMailRound` invoke Reader `resetRound`; failed/cancelled reads dispose their lease | Reset preserves observation; disposal cannot race a watcher or another borrower |
| Recovery | Observer health recovery and feed retirement can directly stop/rebuild an observer task | Every page mutation and retirement goes through one owner |
| Events | 15-second local heartbeat, degradation after 45 seconds without liveness; 90-second Gateway intent lease | Active reads cannot suppress lease renewal or manufacture health |
| Evidence | Existing three-provider controls qualified concurrent watching and reading on separate pages | They do **not** qualify same-page coexistence |

The Reader reset clears rows, pagination, query replies and original buffers.
Observer document identity, notification deduplication and pending delivery hints
are separate. However, coexistence also depends on hook installation order,
native subscriptions and cancellation; separate JavaScript objects are not
sufficient evidence. The original separate-page observer called Reader `snapshot` to validate
the account during reporting. The candidate uses a local-only `checkAccount`
that does not traverse retained rows; active-query coexistence still needs live proof.

Source starting points:

- [Observer manager](../tools/browser-controller/src/mail-observers.mjs), [feed](../tools/browser-controller/src/mail-observer-feed.mjs) and [page hooks](../tools/browser-controller/src/mail-observer-page.mjs).
- [Read pool](../tools/browser-controller/src/mail-read-pool.mjs), [CLI factory](../tools/browser-controller/src/cli-client.mjs) and [round lifecycle](../tools/browser-controller/src/cli-task.mjs).
- [Reader core](../scripts/email/userscripts/lib/reader-core.mjs), [Outlook transport](../scripts/email/userscripts/lib/outlook-transport.mjs) and [Controller reservations](../tools/browser-controller/src/controller.mjs).
- [Resident observer evidence](email-resident-observer.md) and [cross-round reuse](email-cross-round-reuse.md).

## 3. Candidate ownership model

The default shared-page implementation uses `MailReadPool` as the internal mailbox-page owner;
there is no new public `MailboxPageSession` API. Its lease holds the owned page,
CLI daemon and runtime directory. Its slot holds the watch pin, Reader borrower,
retirement state and shared cleanup result. The observer manager retains
notification classification, health and delivery state, but retires candidate
pages through the pool. No two CLI clients or debugger attachments are combined
onto an existing tab.

The reuse identity includes provider, owner scope, normalized expected account,
credential generation/identity, and read and observer source versions. Controller
runtime generation and document nonces separately fence live actions
and callbacks. Feed mailbox binding generation remains checked at admission and
delivery. Identity hashes are internal; secrets and account identifiers are not
added to diagnostics. A foreign active binding cannot evict or borrow this page.

Each business read still gets a new invocation identity, deadline, cancellation
signal and receipt. Reusing the physical page never reuses a previous result.
Idle observer lifetime must not inherit the previous read's expired deadline or
abort signal. A hook or managed-script version change requires retirement at a
safe boundary before a new version is admitted.

### Initialization order

1. Acquire existing Controller admission, then the mailbox pool slot. Resolve
   identity and any fenced cleanup before allocating a page.
2. Install both compatible instrumentation paths before provider navigation.
   If Reader starts first, the observation hook stays dormant: no admitted hints
   or pre-registration backlog until a valid watch registration activates it.
3. Navigate once, prove the owned page, origin, expected account and capabilities,
   and bind document identities. A watch registration requests normal catch-up.
4. Later matching watch starts or reads reuse this page without navigation.
   A legacy read page lacking early hooks is retired and recreated at an idle
   boundary; late wrapping of an already-created Outlook Worker is not assumed safe.

## 4. Lifecycle, serialization and failure policy

These candidate internal states do not add public Gateway states.

| Situation | Page and reservation behavior |
| --- | --- |
| Watching, no active read | Watch pin retains the page; no provider reservation and no CLI polling loop |
| Watching plus read | One Reader borrower holds normal admission; observer callbacks and feed delivery continue independently |
| Successful read completes | Local reset and identity check; release borrower/reservation, keep the watch pin and the same page |
| Read-only page, no watch registration | Preserve the current 30-minute idle reuse rule |
| Read-idle timer fires with a live watch pin | Keep the page; an old timer must not retire it |
| Watch stop or 90-second intent expiry | Revoke event admission immediately and mark retirement; never close under an active Reader. After that borrower quiesces, close rather than returning the lease to the pool |
| Receiving disabled or binding removed | Existing Gateway cancellation applies; reject new matching scheduled reads/hints, quiesce any admitted borrower and retire. Other independently authorized requests keep their normal admission checks |
| Reader cancellation, failure or uncertain reset | Conservatively invalidate and retire the shared page after bounded quiescence; if watching is still desired, rebuild once through the normal supervisor and request catch-up |
| Observer transport degraded, Reader otherwise healthy | Report degraded observation; periodic reading may continue only with independently valid Reader/page/account checks. Do not call the listener healthy because reads succeed |
| Page close, crash, document replacement or identity failure | Fence old callbacks/results, invalidate the affected round, retire/rebuild owned resources, reinstall before subscription, and request catch-up from the retained checkpoint |
| Cleanup cannot prove owned daemon termination | Keep fenced metadata, block replacement and report failure; do not create a second page and lose ownership of the first |

The conservative failed-read policy deliberately avoids a new claim that every
aborted provider request can be reset safely. Future failure-specific retention
would need separate evidence. Rebuild does not replay the failed provider query
inside the old invocation or certify an incomplete list as complete. Existing
qualified partial receipts, saved originals, failure budgets and timeline rules
remain authoritative; retiring a page never deletes already committed mail.

Use a fixed acquisition order: Controller reservation, then mailbox lifecycle
lock. Recovery schedules work through the same admission path; it must not take
the page lock and then wait for a reservation held by Reader. Event receipt,
acknowledgement and lease renewal never wait for Reader's CLI command lock.
Close requests can mark retirement immediately; destruction waits for commands
and pending page operations to settle or their bounded cancellation to finish.
All retirement callers share one idempotent cleanup promise.

Preserve exclusive-operation priority. Before login, send, validation or generic
exclusive work begins, suspend and drain shared pages under exclusive admission.
Resume still-enabled watches only after the exclusive reservation is released.
Reconciliation cannot recreate them in the middle of that operation. Deterministic tests cover
shared-page suspension and replacement; the complete live exclusivity and
fault matrix remains to be exercised.

Temporary task/probe pages are closed by their exact owner. A product-created
temporary login page is cleaned only after the existing flow proves completion
and ownership; personal pages and explicit user handoffs are not auto-closed or
reclassified as shared pages. Failed login leaves watch state visibly waiting.

## 5. Same-page invariants

1. **One physical owner:** for an eligible binding there is at most one live mail
   page and one retained CLI daemon; replacement retires the old owner first.
   Simultaneous watch/read starts converge on one creation.
2. **Independent state:** `resetRound` clears only round rows, pagination, query
   state, original bytes/Blob URLs and transient download state. It preserves
   observer hooks, document sequence, classifier state, deduplication and pending
   hints. Observer account checks do not change the active Reader query.
3. **Transparent instrumentation:** native requests, callbacks, return values,
   errors and stream consumers behave as before. Reset does not reinstall
   wrappers or subscriptions; repeated rounds cannot stack them.
4. **No missed work at completion:** a hint during reading remains pending under
   the existing signal revision rules. Finishing round R cannot settle a newer
   hint beyond R's reconciled revision. ACK follows durable admission. Hints are
   not message counts, completeness proofs or polling watermarks.
5. **Generation safety:** delayed query replies, old document events, old
   credential work and completions after disable cannot affect a newer round or
   binding. The old document nonce is never stamped onto a replacement document
   merely to make a stale check pass.
6. **Bounded resources:** retain existing buffer limits, recovery backoff and
   attempt limits. Idle cache, watch pin, borrower and shutdown all have explicit
   release paths; task count cannot grow after repeated reads or recovery.

Keep the existing 60-second periodic fallback and current `qualified:false`
notification setting. This qualification does not authorize the five-minute
quiet tier or expand Inbox/folder coverage, Microsoft 365 support, or send scope.

## 6. Provider-specific coexistence checks

| Provider | Native observation and read interaction | Required evidence |
| --- | --- | --- |
| QQ Mail | WebSocket notifications coexist with range list/original requests; startup may replace the document | Native accepted arrival survives list and original phases; startup replacement is fenced and reinstalled; no duplicate socket wrappers after reset |
| Gmail | XHR notification stream and Reader's fetch/XHR hooks share the page | Fragmented frames, ACKs and keepalives remain correctly handled during query/download; Reader does not consume or truncate the notification stream; query-only controls do not manufacture arrivals |
| Consumer Outlook | Worker/MessageChannel notification subscriptions coexist with range search, pagination, original preparation and round reset | Search does not silently replace the watched subscription with a query-only view; late replies stay in their old round; `RowAdded` and changed delivery signatures remain visible; read-flag-only changes stay excluded |

For Outlook, a healthy local heartbeat is insufficient: prove actual accepted
delivery notifications after returning from range search and after several
resets. If shared-page searches consistently remove the needed subscription,
record a provider-specific failure instead of loosening notification rules.

## 7. Validation stages and test matrix

The original matrix began as `NOT_RUN`. Fixture tests establish ordering and fencing;
real-provider tests establish provider compatibility. One cannot replace the
other. A run with no controlled arrival cannot pass positive notification cases.

### A. Deterministic lifecycle and transport tests

Use fake clocks, controllable promises and structural notification fixtures.
Check exact ownership, command counts and state transitions, not screenshots
alone. Extend the existing pool, observer, feed, CLI cleanup and userscript tests.

| ID | Scenario | Pass condition |
| --- | --- | --- |
| A01 | Watch-first, Reader-first and simultaneous first acquisition; concurrent duplicate watch starts | One creation/attach/navigation; dormant hook emits nothing before registration; repeated starts preserve identity |
| A02 | Two reads for one provider, independent reads across providers, exclusive waiter during read | Same-provider single flight; cross-provider capacity preserved; exclusive work is not starved; normal `browser_busy` handling remains intact |
| A03 | Arrival at reset, during list/download, and between result and ACK; duplicate delivery and reconnect | Later signal remains pending; no duplicate durable work or early cursor advance; reset preserves observer state |
| A04 | Heartbeat/account validation during active query, idle expiry with/without pin, stop/lease expiry during read | No Reader state mutation; renewal remains independent; exact retention/retirement behavior from section 4 |
| A05 | Abort, timeout, partial result, original failure and reset failure; delayed callbacks afterward | No unsafe reuse or fabricated completion/watermark; preserve existing partial-receipt rules; one retirement; bounded rebuild/catch-up only for current watch intent |
| A06 | Old document, changed owner/account/credential/source, disable/re-enable and delayed old completions | Old work rejected; no foreign reuse; new binding cannot receive old events/results |
| A07 | Recovery concurrent with read/start/stop, cleanup failure, repeated shutdown | No lock inversion, double close/reap or orphan adoption; failed cleanup fences replacement |
| A08 | Hook ordering, repeated reset, fragmented/malformed streams and full event buffers | Native semantics preserved; no wrapper/listener growth; overflow remains visible and requests catch-up |

### B. Headed live baseline and candidate comparison

Use the same pinned Chromium, Bridge, CLI, managed scripts, mailbox fixtures and
frozen read intervals. Record actual installed hashes. Baseline explicitly sets
`SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE=0`; the shared layout differs only in page
ownership/coexistence.
Run them sequentially against an authorized qualification profile, never as
competing Controllers on one production binding. Do not copy cookies or profiles.
This document does not authorize test sends, receiving-setting changes, service
restarts or production migration; execution uses explicitly arranged test accounts
and actions. Offline and read-only cases can be completed separately.

| ID | Workload | Pass condition |
| --- | --- | --- |
| B01 | Three enabled qualified providers; cold start, warm empty reads, then 0/1/N-original rounds and replay | Steady-state mail tabs 6 → 3, one task-only unfocused window; no personal-tab/focus changes; same frozen eligible results and verified original hashes; replay adds no duplicate originals |
| B02 | Native positive arrivals while idle, while a list query is in flight, and while an original is in flight | For each provider, at least three confirmed arrivals in each phase; accepted hints and eventual exact originals independently evidenced; reading phases overlap the native event in timestamps |
| B03 | Query-only controls and known non-arrival events, including Outlook read-flag-only changes | No read/reset-induced accepted arrival; true external events are identified, otherwise the control is inconclusive and repeated |
| B04 | At least 45 minutes with active watch renewal and **no reads**, then a read; separately, read-only idle beyond 30 minutes | Pinned page/daemon survives and resumes without attach/navigation; unpinned read-only page expires under the unchanged idle policy |
| B05 | At least two continuous hours and 100 completed warm rounds per provider, including empty and nonempty rounds | Stable page/daemon identity outside intentional faults, no growing tab/wrapper/buffer count or leaked round data; periodic progress and notification delivery continue |

Positive sends, if arranged, originate outside the Controller/profile under test.
Using its exclusive send operation would suspend the very Reader/observer
overlap being tested. Keep private unique fixture identities and sender receipts;
an uncertain send is investigated rather than automatically resent. Classifiers
may coalesce notifications, so do not require exactly one hint per message.

Correlate native event receipt with list/original begin/end and durable admission.
A message merely sent during a read is insufficient overlap evidence. Fixture
barriers can exercise precise reset/ACK races in A03; instrumented delays in a
live run are labeled separately and do not substitute for natural overlap.
An original discovered only by periodic fallback proves collection continuity,
not successful notification coexistence. Record both outcomes independently.

### C. Fault, exclusivity and lifecycle runs

Run each applicable case for all three providers in the qualification environment.

| ID | Fault/action | Pass condition |
| --- | --- | --- |
| C01 | Close owned page; reload/replace document; terminate owned CLI; restart Controller | Old work fenced; bounded recovery produces one replacement and one coalesced catch-up intent; no leaked pages, daemons or runtime files |
| C02 | Drop notification transport or local feed; delay ACK; overflow diagnostic/delivery buffers | Degraded/lost history is visible, unacknowledged events replay, catch-up uses existing checkpoints; periodic Reader remains independently assessed |
| C03 | Disable receiving, remove binding, rotate credential, or let Gateway intent expire during list/original/reset | Event admission revoked promptly; no late success for revoked work; close after bounded quiescence; no automatic revival without valid current intent |
| C04 | Existing login/probe/send/validation/generic exclusive paths; unrelated foreground application | Proper suspension/drain, no recovery inside exclusivity, owned temporary pages cleaned, enabled watches restored after release; no unintended foregrounding |
| C05 | Inject cleanup/reap failure; then retry cleanup and shut down | No replacement while cleanup is fenced; metadata survives failure, retry is idempotent, final owned resource count is zero |

## 8. Evidence and decision criteria

Retain a machine-readable run manifest plus a short report, under a
`data/qualifications/mail-shared-page-<run-id>/` directory. Do not create a passing
evidence file before execution. Record:

- Candidate/baseline source and installed artifact hashes, provider scope,
  environment, run timestamps and each case's `PASS`, `FAIL`, `BLOCKED`,
  `INCONCLUSIVE` or `NOT_RUN` status.
- Run-local aliases for page/session/document/round identities; lifecycle and
  reservation transitions; monotonic timing for query, original, hint receipt,
  admission, ACK, reset, cancellation and cleanup.
- Task-window/tab counts, daemon/runtime counts, hook/subscription counts where
  observable, bounded queue high-water marks, and cold/warm duration distributions.
- Private fixture/original verification receipts; export only sanitized counts,
  hashes and outcomes. No bodies, account addresses, tokens, raw URLs or native
  payloads in the normal qualification report.

Original qualification targets, fixed before running the candidate (the user’s
subsequent rollout decision is recorded in section 9):

| Gate | Required result |
| --- | --- |
| Correctness and ownership | All applicable A/B/C cases pass for all three providers; zero wrong-account reads, stale result admissions, unowned-page actions, lost controlled originals or leaked owned resources |
| Coexistence | B02 succeeds for every required phase/provider; fallback-only recovery cannot hide a notification regression |
| Presentation | Three steady-state mail task tabs, no tab-count growth after warm rounds or recovery, preserved background focus/window boundary |
| Liveness | Existing 15-second local heartbeat and 45-second stale threshold remain; account for the 15-second supervisor sampling interval when measuring detection. Renewal is not delayed by Reader |
| Performance | At least 30 successful paired warm samples per provider/workload; candidate p95 no greater than `max(baseline p95 × 1.20, baseline p95 + 1 second)`. Compare cold setup separately; report failures separately rather than dropping them |
| Scope | No polling-tier, notification-rule, folder-coverage or public-contract changes needed to obtain a pass |

The performance threshold and soak sizes are proposed acceptance budgets, not
existing measurements or a delivery SLA. Resource usage is recorded, but a tab
reduction is not evidence of a proportional memory/CPU saving. A missed critical
case, unsupported provider or insufficient arrivals leaves qualification incomplete.

## 9. Implementation handoff and rollback

On 2026-09-28 the user explicitly chose shared pages as the default for QQ Mail,
Gmail and consumer Outlook, with issues addressed during use. This decision
supersedes the original rollout sequence; remaining A/B/C cases stay incomplete.
Observer start, feed retirement, health recovery, Reader reuse and exclusive
drain use the pool owner. Preserve failed-run evidence when fixing issues;
reruns never erase earlier failures. A failing shared session does not silently
open a second page as automatic fallback.

The existing environment variable is retained for compatibility: unset or `1`
uses shared pages; explicit `0` selects separate pages. For deployment rollback,
set `SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE=0` on the Controller service and
gracefully restart it, draining the shared resources before replacement.
Checkpoints, receipts, pending durable hints and mailbox settings remain intact;
rollback does not mark failed work as reconciled or resend mail. No public
API/contract migration is planned.

## 10. Candidate implementation and current evidence

The candidate joins the observer watch pin and Reader borrower in `MailReadPool`.
Both start orders install a dormant native observer hook before navigation; a
valid watch activates it and requests the existing catch-up. A pinned page is
not subject to Reader's 30-minute idle timer. Stop, binding retirement, failed
rounds, recovery and exclusive admission fence the same owned lease before
replacement. Reader account checks use the non-mutating `checkAccount` method.
Shared pages are now the default; the separate-page baseline requires explicit
`SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE=0`.

The new deterministic tests exercise watch-first and Reader-first reuse,
pin retention, revocation during a borrowed read, exclusive drain, document
replacement, failed cleanup fencing and dormant-hook admission. Existing
transport, feed, Reader reset and Controller reservation suites also pass.
These are partial A evidence: simultaneous real starts, all fault races, native
transport coexistence and full provider-specific A01–A08 remain open.

The [2026-09-28 redacted smoke manifest](../data/qualifications/mail-shared-page-20260928-smoke/manifest.json)
records sequential runs on the existing dedicated browser with the same three
signed-in mailboxes, managed scripts and frozen one-second empty intervals.
Each run proved account identity, started all three watches, waited 30 idle
seconds, completed two empty Reader rounds per provider, checked that watching
remained active with no query-induced hints, and held the pages for another
80 seconds. The baseline retained six task tabs/sessions; the opt-in candidate
retained three. The task window stayed unfocused, no stale task tabs were
reported, and the pre-existing non-task tab stayed in its separate window.
Both smoke tests passed, and the separate-page Controller was restored.

The [2026-09-28 positive and paired-read manifest](../data/qualifications/mail-shared-page-20260928-positive/manifest.json)
adds two frozen historical originals per provider. Baseline and candidate each
completed one-target and two-target `collect_page` rounds twice, with original
hash verification on every capture and equality across replay and layouts.
Each run retained all three watches; the baseline retained six task
pages/sessions and the candidate three. Both task windows stayed unfocused and
separate from the pre-existing non-task tab. This supports B01's ownership and
read-result portions, but the frozen retry targets are not a naturally nonempty
interval and the run has too few performance samples to complete B01's release
assessment.

With a separate isolated sender Controller attached to the same dedicated
browser, three user-authorized test messages reached QQ Mail, Gmail and Outlook
respectively. Each produced one provider-native mailbox-change hint while the
candidate receiver watched idly, and an independent Reader path verified the
unique original afterward. An Outlook-to-QQ send attempt returned
`email_page_contract_changed`; no matching recipient original or hint was
found, so that uncertain attempt was not resent or counted as an arrival.
The successful receipt verification used `Discover`/capture, not the shared
`collect_page` borrower, and did not overlap a list or original phase.

B01–B03 therefore remain **INCONCLUSIVE**. A later
[phase-overlap follow-up](../data/qualifications/mail-shared-page-20260928-overlap/manifest.json)
recorded one genuine Outlook `outlook_delivery_change` during the candidate's
`prepare_original` phase; six shared-page rounds and the arriving original
were verified, with the watch still active. QQ then completed 80 rapid
original rounds during a confirmed incoming send before
`email_network_original_unqualified` caused fail-closed page retirement. The
incoming original was verified later, but that isolated run ended before a
native QQ hint could be recorded. In read-only comparison, the separate-page
baseline completed 100 rapid QQ rounds; the candidate failed at round 78 on
one run and completed 100 on another. Those runs did not retain the provider
response and could not identify the cause.

The [QQ response diagnosis and pacing follow-up](../data/qualifications/mail-shared-page-20260928-qq-pacing/manifest.json)
reproduced the same error on candidate round 149 and separate-page round 134.
Both returned HTTP 200 with a 127-byte JSON error, `head.ret=-20003`; private
inspection of the baseline response found `Block by spam`. Thus this failure
also occurs without shared-page observation. QQ does not disclose its quota in
that response. The repair leaves one cancellable second after each QQ original
request before another can start, retaining the deadline across round resets
and rechecking identity before the request. No failed request is automatically
reissued. This specific JSON rejection maps to the existing provider operational
failure `email_network_read_failed`; unknown errors and malformed originals
still fail qualification. Opt-in diagnostics retain only bounded response
metadata, never the raw error envelope. Post-fix results are recorded in the
follow-up manifest; earlier failures remain in their original evidence.

The installed repair passed the same read-only workload in both layouts: each
completed 150 fresh QQ original rounds without a provider rejection, plus all
three providers' empty rounds, one/two-original reads and verified replays.
Frozen-original hashes matched across layouts and watches remained active.
The baseline retained six task pages/sessions and the candidate three; neither
task window was focused, and both isolated Controllers removed all owned session
directories on shutdown. For this QQ one-original workload only, capture-stage
p95 was 1.083 seconds on baseline and 1.076 seconds on candidate (150 samples
each). These are not the full per-provider/workload performance or soak gates.
At the end of that qualification, production was restored with the patched
Reader, six task pages, all three watches `watching`, and shared pages disabled.
The later user decision in section 9 changes the default to shared pages.

B02 still has only one confirmed idle arrival per provider and one Outlook
original-phase arrival, below its three-per-phase requirement; list-phase
arrivals remain untested. B04 and C01–C05 remain **NOT_RUN**; B05 is
**INCONCLUSIVE** after the rapid-round comparison, without a two-hour soak.
The 45-minute idle checks, fault injection and 30 paired warm samples per
workload also remain. Those qualification runs restored the then-default separate-page production
layout before the subsequent rollout decision. The read-only comparison can be rerun with
`SPARKCLAW_TEST_SHARED_PAGE=1` and the optional nonempty settings described by
`scripts/qualify-playwright-email.sh`.

## 11. Default rollout and deployment verification

The [default rollout record](../data/qualifications/mail-shared-page-20260928-default/manifest.json)
confirms an unset shared-page environment variable, three retained mail pages and
CLI sessions, no stale task tabs, and an unfocused task-only window after normal
Gateway reads. QQ, Gmail and Outlook all report `watching`, have reconciled their
current notification revisions, and have no current read error. Gmail retains
its pre-existing historical coverage gap.

Deployment verification also found a separate existing fault: Gateway still
mounted workspace data from a deleted old worktree while the Controller wrote
to the current repository. Device/inode checks proved that these were different
directories; successful browser captures therefore failed Gateway verification.
The repair preserved a private snapshot, recovered missing data and newer read
state, and recreated only Gateway with the same image, environment and credential
key, correcting its seven repository bind paths. PostgreSQL was unchanged.
Afterward, the host and Gateway workspace device/inode pairs match, all three
polling watermarks advanced, and Gateway/WebChat readiness passed. The
[mount repair record](../data/qualifications/mail-shared-page-20260928-default/mount-repair.json)
contains sanitized counts; private originals and credentials are excluded.

Controller tests passed 449 with one environment skip. Go build/test/vet,
provider contract, generated/installed script checks and 93 Markdown checks also
passed. This default rollout does not complete the remaining qualification matrix.
