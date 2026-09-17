# Email Management Implementation And Validation

> Language: English | [简体中文](../zh-cn/docs/email-management-implementation.md)

2026-09-09 analysis/UI increment is implemented in the worktree, not deployed. See [current scope and limitations](email-management-analysis-v2.md#8-implementation-and-validation-2026-09-09). Notification routing, manual sender rules, body-only models, localized presentations and drafts/new compose are wired; native replies/CC and complete Sent matter linking remain outstanding.

## Active Mail Reading Contract (2026-09-14)

The managed Reader in the dedicated browser is the sole mail-reading source. List discovery, pagination, stable message identity and original bytes must be observed or replayed from authenticated provider network requests. DOM inspection, menu export, page-click navigation and native fallback paths are retired; an upstream request change is handled by updating the affected provider adapter from a fresh dedicated-browser observation and rerunning qualification.

There is one discovery lane, `recent_inbound`, and every discovery/page request carries `interval_start` and `interval_end`. Normal synchronization enumerates `[last_completed_fetch_time, fixed_current_time)`, with the deployment boundary as the first lower bound. Read/unread state is recorded as remote evidence only and never selects or excludes a message. Capturing and persisting an original is independent from mark-read: mark-read is a separate network capability, and its failure does not undo a successful capture or advance the sync boundary. The three provider adapters currently expose mark-read as an explicit fail-closed capability until exact live request/response evidence is available. There is no `unread` lane or `inbox_unread` coverage.

The dedicated Browser Bridge native socket and Controller are healthy. A live speed run qualified QQ's network-only list and original capture twice; Outlook currently exposes no observed `ItemRows`/Inbox `ConversationRows` Worker templates, and Gmail exposes no current `/sync/u/<account>/i/bv` list request. Those two providers remain unqualified until their current network requests are observed and their adapters are updated. The failed discovery timings are not mail-fetch timings.

QQ redesign and empty-round validation, 2026-09-09: QQ's four frozen failed
targets now produce four complete native EML captures, with all manifest/file
hashes verified and four published read receipts. The current reader removes the
selected list row; identity now comes from agreeing active mail records attached
to the visible subject and body components, including detail/individual/read
state checks. The provider's opaque internal messageId is not treated as an RFC
header; authoritative headers come from the native EML. Current proof does not
claim QQ aggregate-conversation or complete historical-folder qualification.

A bounded page with no eligible targets now returns normal empty without export,
read effects or an empty-result retry. Partial discovery coverage and its cursor
remain explicit; missing evidence or unsupported rows cannot masquerade as empty.
Required interval checks remain, so read state never excludes a newly received
message. All219Controller tests, affected Go race,
provider contract and Go build pass. QQ is enabled for this trial; Gmail/Outlook
were already OFF when this redesign began and remain OFF.
Remote Gateway `b928286b715d` and WebChat `485c61c58f04` are healthy; Controller
is refreshed and Bridge remains1.0.22. The next live round returned zero targets,
zero captures and zero failures across the required scopes. All four prior
manifest hashes/mtimes stayed unchanged, CLI tasks returned to zero and the next
scan was queued for the normal20-minute completion-based idle interval.

Earlier live trial found Gmail's actual empty-result sentence and fixed manual
sync advancing a queued idle job plus PostgreSQL locale ordering at due deadlines
(migration0013). Its initial QQ capture failure is superseded by the four successful
captures above. Full historical coverage, export throughput, attachments in this
live batch and model-quality acceptance are not implied by these checks.

2026-09-08 worktree implementation. The owner authorized continuous work across
all five stages. This report distinguishes executable code, engineering evidence,
real-provider qualification and deployment. Following implementation, the owner
requested a remote rebuild of the current `main` worktree. On 2026-09-08 the full
remote product was rebuilt and the host Controller restarted: Gateway image
`65aaa141eab7`, WebChat `485c61c58f04`. Readiness confirms external models and
PostgreSQL; WebChat and the remote Fast model inventory return HTTP 200. The email
migration is present. Outlook receiving was enabled by the owner, temporarily paused
for the polling correction, then restored with its original progress.
These deployment checks do not satisfy the semantic or provider acceptance gates.

On 2026-09-09 the owner requested concurrent receiving across the three providers.
The remote product was rebuilt with Gateway `a7e8d41e6e8b` and the host Controller
restarted; WebChat remains `485c61c58f04`. Full Go checks, affected race tests,
isolated PostgreSQL Store tests, Controller 191, Bridge 41 and WebChat 100 tests pass.
A bounded live probe observed three simultaneous CLI daemons: Gmail and Outlook
passed; QQ returned `email_login_required`. This proves concurrent task execution,
not QQ readiness or complete three-provider mail ingestion. Intake switches were
not changed by the qualification; Outlook remains enabled.

## Implemented Flow

Automatic receiving now collects a bounded mailbox page in one browser task:
enumerate the network list for a fixed interval, persist its inventory, fetch each
proved original through the managed network adapter, and save MIME parts after
durable local source verification. Remote mark-read is a separate operation.
Return to the same list between mails. A page contains at most 50 proved messages;
virtual lists and incomplete conversations remain explicitly partial coverage.

The default idle interval is 20 minutes after the entire round completes. Page
scripts have a 30-minute budget; the single lane has a 95-minute outer budget
with three-minute leases renewed every minute. Transport failures retry after at
least 20 minutes (at most five attempts). Individual export failures retain the
unacknowledged page for the next normal round while other mailboxes proceed. A partial
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
are shared across page retries and observations, avoiding repeated downloads. Actual account
and credential fences remain in every browser call. Model and parser jobs still
consume individual committed sources and preserve per-mail viewing receipts.

Only the returned checkpoint's original interval can advance progress. The lower
bound is the last completed fetch boundary (or deployment time on first sync),
and the upper bound is fixed before discovery. Unknown coverage, partial pages or
network errors cannot advance the completed boundary. No full-provider pagination
or real-mail speed qualification is claimed by the offline tests. Current switch
state and live limitations are recorded above. Observed inventories larger than
50 targets retain their remaining members across acknowledged sub-batches,
independent of remote read state.

Normalized representations and model input/output are immutable owner-scoped
artifacts. Candidate selection registers dependencies before model invocation;
input fingerprints, generations, assignment epochs and live leases reject stale
results. Model input is bounded to 48 KB with disclosed truncation. Mock model
responses cannot masquerade as semantic success. Model calls record the existing
Fast operation telemetry; there is no new model lane or cross-project contract.
Reply lookup and dependency registration share a newest-first 32-reference window;
the model receives an explicit gap while the complete source headers retain their
original order. Subject/content and counterparty retrieval exclude receiving-account
address noise before the final recent-conversation fallback.

Source summaries now snapshot the durable WebChat `zh`/`en` preference when the
job executes and persist that language on the artifact. Preference changes do
not participate in summary dependency fingerprints, so prior results stay
unchanged and only later work uses the new language. Classification first applies
a strict verification marker plus nearby digit-bearing token rule. A match is
stored as a constrained `pattern` notification and bypasses classification,
summary and event-assignment model calls; ambiguous numeric mail still uses the
semantic path.

Assignments preserve established conversation IDs and membership. Later evidence
creates or clears versioned suspected-duplicate/pending-correction concerns;
durable reverse dependencies refresh summaries without moving members. Current
work coalesces; exhausted work can be explicitly retried. Failure remains visible
while originals stay accessible.

WebChat has a responsive native email dialog with source filters, backend search,
independent pages, pending mail, concern links, synchronization status and intake
settings. Only cards actually entering the viewport submit per-mail viewing IDs.
The owner-authenticated file endpoint accepts a mail ID and part ID, resolves a
committed manifest and verifies bytes. The older document-file endpoint blocks
email paths and aliases. Attachment content is downloaded instead of executing
HTML in the WebChat origin.

## Validation Evidence

- Full Gateway build, vet and tests pass; affected runtime/repository/HTTP packages
  also pass race checks. Controller passes all 183 tests; shell syntax and the
  70-document bilingual CI check pass.
- Memory, default File and isolated PostgreSQL exercise the same repository
  contract, including account switches, expired leases, concurrent claims,
  fixed membership, per-mail viewing, immediate summary staleness and fan-out.
- File fault injection covers rollback before persistence, unknown directory-fsync
  outcomes and exact command reconciliation, plus refresh recovery after restart.
- A synthetic File service test runs the actual planner/workers from discovery
  through source publication, parsing, summary and conversation. It checks that
  explicit remote mark-read follows durable capture and rejects foreign-owner,
  arbitrary-path and tampered-original downloads.
- A simulated-clock 30-minute model outage preserves sources, exhausts retries
  visibly and recovers after explicit retry. This is not a wall-clock soak result.
- A File restart regression discovers an already-read arrival after a partial
  historical interval's fixed upper bound while retaining that old cursor/boundary.
  Production runner contracts reject the retired unread lane and require a valid
  interval for every recent inbound scan.
- HTTP tests cover authentication, strict inputs, source/body search, independent
  pagination, historical viewing gaps, pending replay, concern links, unchanged
  membership, partial-source download and legacy path-alias rejection.
- WebChat: 32 test files / 100 tests, 576 bilingual keys, production build, and
  desktop 1280×720/mobile 390×844 fixture rendering. Real IntersectionObserver
  receipts acknowledge visible cards and omit the unloaded historical card.
- QQ recent discovery uses observed provider receipt time (`totime`) and bounded
  ordinary-folder rotation with a binding-scoped cursor. Inserted advertising rows
  prevent a strict-order completion claim; coverage remains partial. A real 24-hour
  batch returned five messages/five thread anchors; the next batch entered an
  ordinary folder and retained continuation. Limits are 100 folders, 2,000 metadata
  rows and 128 native scroll steps; a 175-message/four-batch regression passes.
- Gmail recent discovery uses the observed receipt field and the ordinary-mail
  scope, independent of unread labels. Historical interval probes with zero
  candidates do not qualify pagination, ordering or already-read coverage.
- Real Outlook pinned inventory distinguishes six global items and drafts;
  selected local and global non-draft members can be captured individually. The
  local reply's 14,656-byte original matches an independent native export exactly.
  Another global member outside Inbox yielded 5,534 bytes, but its sender differs
  from the current account: this qualifies an inbound member, not Sent export.
  These samples do not qualify all folders or recent-discovery throughput.

Capacity was measured on the Linux aarch64 development host with 10,000 synthetic
mails (9,000 captured), 900 conversations and 1,000 queued jobs; the File snapshot
was 35.6 MB. Each query category used 100 requests. These are Store measurements,
not browser/model throughput or a full HTTP/popup latency qualification.

| Backend | List/detail p95 | Conversation search p95 | Restart | Recovered claim | New admission |
|---|---:|---:|---:|---:|---:|
| File | 51.80 ms | 12.91 ms | 551 ms | 548 ms | 489 ms |
| PostgreSQL | 13.63 ms | 15.90 ms | 208 ms | 4 ms | 2 ms |

The measured query subset meets the one-second target. Overflow tests preserve
the prior boundary and all previously admitted records; the fixture seed itself
is excluded from throughput timing.

All source fixtures and test database schemas are isolated. No real email was
sent, no mailbox-wide historical import was performed, and no mock result is
counted as semantic qualification. The temporary Controller, credential bridge and
launcher were removed after the bounded live trials.

## Reproduce Engineering Checks

Run document-tool setup first, then the ordinary Gateway/WebChat gates in
[Development](development.md). Focused commands from `services/gateway`:

```bash
go test ./internal/emailmanagement ./internal/emailautomation ./internal/gateway
go test -race ./internal/emailmanagement ./internal/emailautomation ./internal/store ./internal/gateway
SPARKCLAW_TEST_POSTGRES_DSN='<isolated-test-dsn>' go test ./internal/store
SPARKCLAW_TEST_EMAIL_CAPACITY=1 SPARKCLAW_TEST_POSTGRES_DSN='<isolated-test-dsn>' go test ./internal/store -run TestEmailManagementCapacityQualification -v
SPARKCLAW_TEST_EMAIL_MODEL_CONFIG='<reviewed-real-model-config>' go test ./internal/emailmanagement -run TestEmailManagementRealModelSmoke -v
```

The real-model smoke sends only synthetic correspondence, validates the production
JSON contract and records output for inspection. It skips unless explicitly
configured and is insufficient for the reviewed semantic corpus gate.

## Release Gates Still Requiring Evidence

The [stage-five thresholds](email-management-stage-5-acceptance.md) remain the
acceptance authority. Engineering pass results do not satisfy the three-run
100+20 reviewed assignment corpus, 100 individual/20 conversation summary review,
each provider's 10 new/5 already-read idle-discovery trials, or a 30-minute real
outage/overflow/UI latency soak. Complete recent-folder pagination and receipt-time
coverage must be qualified per provider; partial observations stay explicit and
retain the earlier boundary. Local Fast endpoints on ports 8000/8001 were not
available during the initial implementation session. The subsequent owner-requested
remote deployment reaches the Fast model inventory, but no real model quality pass
is claimed.
All three providers currently report partial recent coverage. QQ complete reply
enumeration, Gmail multi-reply export and actual Sent-member export still need
provider-specific live evidence. The current capture/commit flow does not require
mark-read; that operation is qualified independently per provider.

Account enablement requires the shared workspace/source paths and reviewed
provider/model configuration. Existing private capture files are
not silently bulk-imported by enabling the service.

## Analysis and Processing Increment (2026-09-09)

The [analysis-v2 design](email-management-analysis-v2.md#8-implementation-and-validation-2026-09-09)
records the current, undeployed worktree increment: purpose-based notification/interaction routing,
exact sender overrides, body-only evidence, language projections, verification expiry, fixed historical
memberships, durable drafts and sending/source reconciliation. Native editor observations remain
separate from actual Send qualification; no real Send was executed in this increment.

The 120-case real Fast classification baseline failed and is retained. General prompt corrections
passed the same frozen corpus on recheck; this is in-sample synthetic evidence, not production accuracy
or a replacement for the earlier assignment/summary/receiving gates. The model alias is not a pinned
checkpoint. See [metadata](evaluation/email-analysis-v2/qualification-metadata.json) and design section 8
for exact coverage and limits. Receiving timeliness remains explicitly deferred by the owner.

Current engineering checks include the full Go suite, build/vet, scoped race, real PostgreSQL Store,
118 frontend tests, 232 Controller tests and provider contract parity. The updated File capacity subset (10,000 mails / 900 matters / 1,000 jobs)
measured list/detail p95 94.82 ms and conversation-search p95 214.74 ms. This does not qualify end-to-end
UI or browser throughput. These implementation and test results do not deploy the increment or change
receiving switches.

## Analysis Increment Remote Deployment (2026-09-09)

The owner subsequently requested a remote rebuild. `npm run start:remote` deployed Gateway
`153bf1fc7a35` and WebChat `1a417cb3894a`, preserving data volumes. All five application services,
external/PostgreSQL readiness, WebChat HTTP 200 and refreshed host Controller smoke pass.
See [deployment evidence](email-management-analysis-v2.md#10-remote-deployment-2026-09-09).
Earlier undeployed statements describe the implementation checkpoint; native qualification limits
remain unchanged by deployment. No receiving settings were changed and no explicit mail was sent.
