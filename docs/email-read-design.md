# Local Email Data Design

> Language: English | [简体中文](../zh-cn/docs/email-read-design.md)

The active read contract is the [email pipeline optimization](email-pipeline-optimization-design.md):
the managed network Reader is the only source, discovery uses one
`recent_inbound` lane and a persisted received-time interval, and read/unread is
observation evidence rather than a selection rule. The [system and staged management design](email-management-design.md) governs
cross-account topic conversations and bounded encountered-thread backfill. This
document retains source layers and historical qualification. The source-stage
implementation and qualification details below are historical and superseded.
Earlier deferred
conversation/no-backfill scope is superseded; current script limitations remain.
The reviewed stages additionally govern unread-independent recent intake, per-mail
viewing, fixed existing memberships with visible concerns, and durable automatic
summary refresh. Earlier unread-only discovery/manual-only reanalysis passages
describe the source-stage scope, not the target management system.

Historical source-stage status (superseded by the current
[management implementation](email-management-implementation.md)): foundation authorized
2026-09-07. That delivery implemented only
fixed provider scripts that capture one unread message into workspace source
files. The three-layer Store and model-analysis design below is not implemented.
Script capture is not a committed Gateway mail record or completed analysis.
Gmail's singleton unread capture and pinned recovery passed live verification on
2026-09-08. Subsequent QQ/Gmail self-mail attachments passed capture and read
confirmation. Outlook self-mail has two global conversation items and remains
unsupported. Independent discovery/capture progress, qualification and pending
queue boundaries are recorded below.

The three layers define a durable data foundation: explicit structures, stable
references and clear state ownership. The structure can remain in use as designed;
any additions should be driven by concrete requirements.

## Historical Source Delivery (Superseded)

The fixed `qqmail.read`, `outlook.read`, and `gmail.read` scripts select and pin
one unread message, acquire its available body and in-scope attachment/inline
bytes, and publish a source manifest under the configured host workspace.
The operation captures one unread message and takes no model parameters.
An empty inbox selection returns an explicit empty outcome without a message.

QQ Mail supplies per-message unread identity. Gmail joins visible singleton row
markers to the UI's own list response: the message ID, thread ID and unread/inbox
labels must agree. `thread-f/msg-f` retains decimal/hex consistency checks;
self-sent `thread-a/msg-a` uses an explicit singleton list record and its legacy
message ID matched to the DOM, never a thread ID substituted for a message ID.
Gmail installs evidence observation at task-page initialization and reloads that
page to avoid missing already-issued requests. QQ locators accept the observed `~`.
Outlook requires explicit local/global message counts of one,
matching ItemIds/GlobalItemIds, and agreeing unread counts. Its conversation ID
is a navigation locator; the ItemId is the message identity. Passive observers
run only inside the owned task page and do not replay private provider requests.
Unknown response shapes supply no proof. Gmail/Outlook select only a proven
singleton on the loaded list; a nonempty list without a proven candidate returns an error before
opening, never `empty`. Multi-message conversations are not supported.
Pinned retries recheck singleton identity on the loaded inbox list, including
read rows; a missing or changed target fails rather than selecting another mail.

Source collection uses immutable source IDs and references. Each manifest
identifies the exact provider message and captured files by governed relative
path, byte count and SHA-256. Provider thread IDs must remain distinct from
message IDs. A successful script result returns capture count/status and a
relative source-manifest reference; it does not return all source bytes through
the tool envelope or claim that attachment contents have been understood.

When a provider exposes the original email, the script preserves its bytes as
`message.eml` and uses `mailparser` to decode headers, text/HTML and MIME parts.
The capture mode is `rfc822`. The shared collector accepts explicitly tagged
`browser_dom` observations without constructing an EML or inventing missing
headers, but current provider adapters require a verified original export and
fail if that export is unavailable. MIME decoding is implemented; attachment document extraction,
the independent normalized-data parser layer and model analysis are not.

Structured source records carry `schema_version: 1` as a parser discriminator.
Optional fields have explicit
missing-value semantics, and readers tolerate compatible optional additions.
Existing field meanings and immutable source IDs must not be reused for different
content. Transport script revisions are a separate contract.

Set host `SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT` to the existing workspace
directory shared with the Gateway workspace. Container and host absolute paths
may differ, but the same relative paths must resolve to the same bytes. This
sets the Controller's `emailWorkspaceRoot`; it is never a model argument.
Validate configured roots, invocation-owned paths and final references;
do not publish arbitrary host paths, credentials or signed download URLs.
Persist and validate source files before publishing their manifest. Unsupported
material, unavailable source fields, incomplete inventories and download failures
remain explicit instead of being represented as empty or complete content.

| Acquisition limit | Boundary |
|---|---|
| Original EML download | 110 MiB |
| Body | 2 MiB |
| Binary parts | At most 20 attachment/inline parts |
| Each binary part | 25 MiB |
| Total binary parts | 100 MiB |

These are acquisition bounds, not permission to silently truncate a source.
An individual failed or limited attachment produces a `partial` capture with
explicit part status. When the inventory exceeds 20 parts, acquire the first 20
and record the omitted count in `coverage.skipped_parts`; the capture is partial.
Body size violations fail capture. Remote images
and cloud links are not fetched.

The owner-scoped `email/<owner-scope>/invocations/<invocation-hash>.json` journal
persists the selected account and message identity before opening it. Current
provider adapters require this identity before opening. A retry stays pinned to the
same selection; a different account or selection is rejected.
Completed `capture.json` and source files are immutable. `read-state.json` records
mutable mark-read intent/observations separately and is not part of the immutable
content manifest. Files and their manifest are flushed before atomic publication,
and the read-state intent is persisted before the explicit provider action.

Receipts distinguish `empty`, `collected` and `partial`; attachment counts include
only successfully acquired files. A journal receipt replay verifies the manifest
digest and every file's length/hash, and rejects missing, altered or symlinked
source files. Confirmed read actions are not repeated.
If source publication completed but the final journal receipt was lost, recovery
reuses that capture. An uncertain read action is reconciled on the same pinned
message, with durable intent recorded before any repeated action. Recovery of an
already confirmed capture verifies files and rebuilds a missing receipt without
opening the mailbox. Uncertain confirmation reuses the source without opening an
export menu or downloading it again. Failed staging is removed; crash recovery
cleans only the same invocation's unfinished staging directory.

`script_capture` describes the durable script-level file/manifest result only.
Gateway Store publication, stable mailbox/message records, relationship
resolution, normalized representations, document registration and the separate
model-analysis node are not implemented by this delivery. A file manifest alone
must not be reported as a Store-committed mail record. The three-layer design
requires verified immutable references before Store admission; its implementation
is outside this task.

After durable complete capture, record mark-read intent, perform the provider
action on the pinned message and verify the outcome. Partial/failed capture does
not authorize a new mark-read action. Provider automatic marking on open remains
an independently reported observation; it cannot establish capture success.
Mark-read failure or uncertainty does not erase durable files. Recovery must use
the same pinned message, even if it is now read, rather than consume the next one.

Historical source-stage verification (superseded by the read-loop results below): all 144 Controller tests passed, including source capture, provider
contracts and real CLI subprocess transfer using synthetic mail and temporary
files. Focused Go tests and vet, 47 golden cases plus extended checks, and 8
qualification-helper tests pass. QQ/Outlook empty paths passed live checks.
Outlook's final pinned, already-read message returned `collected`, zero attachments,
`read_state=read`, and passed file verification. This verifies source acquisition,
not fresh unread selection. Gmail's last pinned attempt returned
`email_capture_unavailable`; no successful live attachment download is claimed.
The host Controller is updated; the Gateway is not deployed.
The sections below define data ownership and integration
requirements, including Store/analysis components outside this delivery.

## Native Original Downloads

Decision (2026-09-08): use provider-original EML acquisition through Playwright.
The web-information replacement below is withdrawn following successful QQ and
Gmail native download verification. It is historical evidence, not an active
fallback or a prerequisite for later work on the three-layer data structure.
Current provider scripts require a verified original; an unavailable export fails
explicitly and does not silently publish reconstructed mail as the original.

The script registers `page.waitForEvent('download')` before activating the export
control and saves the resulting `Download` with `saveAs()`. Export popups and
targeted anchors are temporarily routed into the owned task page. Chromium
performs the response navigation, redirects and native download. The Bridge
forwards that page's native GUID/frame events to Playwright and the host hands
over verified file bytes. No browser-wide download directory setting is changed.
The host correlates a unique final-URL download record within the task interval;
concurrent identical URLs are rejected as ambiguous and those files are left
untouched. The original temporary
download is removed only after a private artifact copy succeeds. Cancellation,
size limits, destination collisions and task disconnects have explicit cleanup.

QQ's pinned read sample yielded 38,146 bytes, Gmail's 45,731 bytes and Outlook's
58,069 bytes; all match the independently acquired originals byte for byte and
pass MIME/header checks.
Gmail's allowed attachment-domain redirect now succeeds through this path.
These samples have zero attachments. A single-export Blob fixture also preserved
a 1,435,412-byte EML and its 1,048,613-byte decoded binary attachment exactly;
direct and routed-popup exports passed. This matches the one-message-per-run
scope; bulk downloads within one page are not qualified and remain subject to
Chromium's automatic-download restrictions. Controller 149, Bridge 39 and
artifact 9 tests pass. This result establishes original download
capability; it does not qualify actual provider attachment inventories, fresh
unread selection or mark-read effects.
Private originals and redacted receipts are in
`data/qualifications/native-download-20260908/`. The host Bridge/Controller are
updated; this task does not deploy the Gateway or implement Store/model analysis.

## Read-loop Verification (2026-09-08)

| Provider | Live evidence |
|---|---|
| Gmail | One message initially proved unread; 51,459-byte native EML, source hashes and read confirmation passed. Retries stayed on that message; a final read-row singleton identity check also passed. No attachments. |
| Outlook | Unread filtering returned empty. A known read singleton was captured using its actual ItemId, with confirmed read state; the 58,069-byte EML matches the independent original exactly. This does not qualify a fresh unread transition. |
| QQ Mail | Empty unread selection verified; previous native original and synthetic MIME attachment evidence remains applicable. A fresh unread transition was not available in this round. |

Previous Outlook diagnostic captures mistook the conversation ID in the detail
URL for a message ID. They remain historical download evidence only; new adapters
require the provider's singleton ItemId before pinning. No old diagnostic record
is silently migrated or admitted to Store.

Recovery covers interrupted originals, incomplete attachments, lost receipts
after durable confirmation, and uncertain read outcomes. Read-state checks pass
only identity fields, keeping large bodies out of CLI code arguments. Cleanup
has its own bounded cancellation/timeout budget and closes owned child pages
after topology failure. Owner-page fingerprint checks remain. The known synthetic
fixture URL is absent from the current browser session snapshot and appears only
in recently closed history; task runtime directories are empty after verification.
These checks do not claim arbitrary unowned tabs can be controlled.

All 160 Controller tests, full Gateway build/test/vet, and 63 mirrored document
checks pass. Temporary credential test/launcher entries are removed.
Evidence is under `data/qualifications/email-read-loop-20260908/`. The web-information
alternative stays withdrawn. This stage adds no Store ingestion or model summary
node, and does not deploy the Gateway.

## Withdrawn Candidate: Capture Web Information

Decision status: withdrawn on 2026-09-08 after native-original download succeeded.
The owner requested this alternative on 2026-09-07 and required an information
trial before adoption. The following trial records describe that earlier
evaluation; they are not the current implementation decision.

The candidate acquires information from one precisely identified message in the
provider UI and produces a consistent local record. Original EML export becomes
optional evidence rather than a prerequisite. Consistency means a common storage
format and faithful observations, not reconstruction of a byte-identical original.

Within the existing three layers, source captures would preserve a message-scoped
`mail.json` observation record, actual body text/HTML, individually acquired
attachments, and `capture.json` coverage/provenance. Normalized facts and model
analysis keep their existing ownership. Never save a full mailbox page as one
message or invent unavailable headers, timestamps, recipients, or reply edges.
Missing fields differ from confirmed empty fields. Attachment bytes still require
independent acquisition; this alternative does not resolve download, individual
unread identity, or automatic mark-read behavior by itself.

### Evaluation Before Adoption

Use existing pinned, already-read messages for the initial information trial.
Do not send fixtures or open additional unread messages. Keep the test harness and
private observations separate from production scripts and admitted email records.
Use an existing original EML as an independent reference where available;
otherwise compare with the expanded message UI and explicitly note the weaker
reference. Repeated DOM extraction alone establishes repeatability, not accuracy.

| Check | Evidence required |
|---|---|
| Account and message | Exact identity is stable and belongs to the intended message, not just its conversation |
| Header information | Subject, sender, recipients, CC and displayed time agree with the reference; absent fields are explicit |
| Body | Compare text, paragraph order and quoted history after expansion; distinguish rendering differences from omissions |
| Attachments | Inventory and individual file bytes agree with the reference; zero-attachment samples do not qualify downloads |
| Convenience | Record elapsed time, browser operations, expansions, retries and provider-specific handling; do not assume faster operation |
| State | Observe only the already-read sample in this trial; fresh unread selection and mark-read effects require separate evidence |

Only recommend adoption after the supported provider/sample set has adequate
evidence. Complex HTML, quoted threads, same-name attachments, inline resources
and different recipient/time presentations need coverage before broad claims.
A limited successful sample can establish feasibility, not complete readiness.
Record the findings below before discussing adoption with the owner.

### Information Trial Results (2026-09-07)

An isolated harness used the existing `PlaywrightCLIClientFactory` and private
script registry to inspect one previously pinned, already-read message per
provider. Account and exact message locators matched their existing pins. No
new unread message was selected, original export invoked, mail sent or explicit
read-state mutation attempted. Production scripts and workflow routes were not
changed. These samples qualify access to known messages only.

| Provider | Observed information and evidence | Elapsed time / script browser calls |
|---|---|---|
| Outlook | Subject, sender/recipient addresses and displayed time agree with an existing EML, time to the minute. Two body reads are stable. Rendered-text comparison passes after removing one provider icon; all 4 original link targets remain. Sample has one recipient, no CC and no attachments. | 23.108 s / 3 |
| Gmail | Body and expanded details are stable across two reads. One details expansion exposes From, Reply-To, To, Date, Subject, mailed-by and security; its subject agrees with the message heading. No independent original reference. | 23.177 s / 4 |
| QQ Mail | Acquired subject, 418 body-text characters and 26,443 HTML characters from the pinned message; observed state remains read. Attachment inventory is explicitly incomplete. No independent original reference. | 14.879 s / 5 |

Outlook's reference EML was checked against the existing immutable manifest and
journal hashes. Its plain-text MIME part differs substantially from its HTML
presentation, including URL representations, so raw plain-text equality is not a
valid visible-body test for this sample. The original HTML was independently
rendered offline with JavaScript disabled and all network requests blocked.
After Unicode NFC and whitespace normalization, it contains 488 visible
characters; the captured webpage contains 490. The only difference is a provider
icon, U+E113, and a space. Removing that `aria-hidden` icon only in the local
comparison produces equal normalized visible text, with no missing original
visible text. Raw observations remain unchanged. This does not prove identical
HTML, paragraph layout or complete MIME content. Outlook's recipient address
comes from a recipient control's `aria-label`, not its direct visible text;
multiple To/CC grouping remains unverified.

Elapsed times include preparation, connection/navigation and collection or
comparison, but exclude cleanup. Call counts are harness-level browser calls,
not total underlying CLI operations. QQ reused the collector on its existing pin,
including export-menu discovery, without exporting. These are single samples
with different duties from original-source collection; they establish neither a
speedup nor a reliability rate. Basic information can be obtained without an
original download, but provider-specific extraction and details expansion remain
necessary.

The evidence supports this as a feasible candidate for local information capture.
At the close of the 2026-09-07 trial, independent accuracy evidence covered only the Outlook sample; Gmail
and QQ establish availability or repeatability, not completeness. Real attachment
downloads and complete inventories, complex quoted threads, recipient grouping,
fresh individual unread selection and mark-read behavior still need separate
qualification. Do not adopt the alternative as the complete reading workflow on
this evidence alone. Discuss these results with the owner before implementation
or switching the acquisition path; the three-layer ownership remains unchanged.

The redacted local report is
`data/qualifications/email-dom-20260907/report.json`. Private observations remain
outside admitted email records; no mail contents are copied into this document.
The temporary Gateway test bridge and its launcher have been removed.

### Independent QQ Mail And Gmail Originals (2026-09-08)

At the owner's request, an isolated export harness obtained the same two pinned
samples through QQ Mail's `Export as eml file` and Gmail's `Download message`.
It saved the provider response bytes as EML, without constructing mail from DOM
fields. MIME parsing, byte counts and SHA-256 verify the retained files; account
and exact message pins were checked before export. The original 2026-09-07
observations were preserved for comparison.

| Sample | Original size | Independent comparison |
|---|---|---|
| QQ Mail | 38,146 bytes | Subject agrees. Original HTML and saved webpage text/HTML produce the same 399 normalized visible characters; all 6 original link targets remain. A separate details expansion exposes matching From/To addresses and a display time matching the Date header to the minute. |
| Gmail | 45,731 bytes | Subject, From/To/Reply-To addresses agree with the saved details. Original HTML and saved webpage text/HTML produce the same 765 normalized visible characters; all 10 original link targets remain. Display time differs from the Date header as described below. |

The comparison uses the same offline, script-disabled rendering and NFC/whitespace
normalization as the Outlook trial. Neither sample needs a provider-icon filter.
Current body reads also agree with the prior observations. Both originals contain
zero attachment MIME parts, so this qualifies neither attachment downloading nor
the completeness detection of the DOM attachment inventory. Remote image bytes,
complex quoted history, multiple-recipient grouping and layout equivalence remain
outside these samples.

Two field boundaries are now demonstrated. QQ's earlier observation did not
capture From/To/Date; these were obtained separately from the UI for this check.
The expanded details do not expose Reply-To as a distinct field, although it is
present in the EML. Keep it unavailable in a DOM source; finding the same address
elsewhere does not establish its role. Gmail's displayed minute is one minute
later than the RFC Date header and matches the receiving trace's minute instead.
The browser timezone was verified as Asia/Shanghai. This agreement does not prove
Gmail's general timestamp definition. Preserve display text, timezone evidence
and provenance separately; do not label this observation as the original sent
time or silently equate it to a canonical received time.

The successful export runs took 22.432 seconds for QQ and 30.888 seconds for
Gmail, including preparation/navigation and acquisition, excluding cleanup.
Gmail's preceding native-click attempt timed out. The successful Gmail response
redirected from `mail.google.com` to the already allowed
`mail-attachment.googleusercontent.com` with `application/force-download` content;
QQ returned `application/octet-stream`. The isolated harness followed the export
response and checked its final origin. At that time, the downloader's `redirect:
"error"` rejected that Gmail redirect. The native download implementation above
subsequently replaced it. These
observations establish a working reference-acquisition route, not a speed or
reliability benchmark against the DOM workflow.

Conclusion: these samples support the feasibility of capturing body and basic
information into consistent local records, with explicit field provenance and
gaps. All three initial provider samples now have independent original evidence.
This is not full reading-workflow acceptance: real attachments, reliable fresh
unread identity and mark-read effects remain unqualified. This earlier trial did
not adopt the candidate; the subsequent native download decision withdraws it.

The originals are `data/qualifications/email-eml-20260908/qq_mail.eml` and
`gmail.eml` in that directory; its `report.json` contains redacted comparison
results and digests. Raw evidence remains private and outside admitted email
records. The temporary Gateway bridge and launcher were removed after verification.

## Scope And Ownership

Implementation follow-up: independent `qqmail.discover` / `outlook.discover` /
`gmail.discover` and corresponding `.capture` scripts now exist. Gateway exposes
account-setting-checked discovery; capture requests carrying a Runtime-owned
`target` select `.capture`. Existing manual `.read` remains compatible.
Discovery never opens or marks mail. It returns at most 100 stable candidates
from loaded rows (48 KiB total candidate budget), account, observation time,
scanned/unsupported counts and coverage limits. Nonempty lists report only
`listed/limited`; they cannot advance whole-mailbox completion. Only established
empty-list evidence reports `empty/scan_complete`. Complete pagination is not
implemented and multi-message conversations remain unsupported; this is not
intake of all unread mail.

Self-mail qualification (2026-09-08): QQ and Gmail each sent a synthetic message
to their own account with Chinese body text and a Unicode attachment filename.
Discovery followed by pinned capture returned `collected/read`; both 59-byte
attachments match the pre-send bytes by hash. Body, From/To, subject and every
source-file digest were verified. This adds QQ fresh unread/read and attachment
evidence, and Gmail self-sent singleton/attachment evidence. The Outlook self-mail
was observed in Inbox initially read; only that test conversation was marked
unread for qualification. Its list response reports one local and two global
messages. Discovery returns one unsupported row rather than empty. This verifies
multi-message rejection, not Outlook attachment capture or a fresh unread/read
loop. Per-ItemId selection and export must be added before relaxing global counts.
Private evidence and repeatable offline checks are in
`data/qualifications/email-intake-20260908/report.mjs`.
Host Controller was refreshed. All three formal Gateway→Controller `discover`
calls passed receipt validation; QQ/Gmail capture retries also passed Gateway
source/target verification. Controller 166 tests, full Go build/test/vet and 63
bilingual docs pass. Temporary Go credential bridge and launcher were removed.
The browser retained seven existing tabs, no Bridge task tabs and no file chooser
dialogs after verification. The resident Gateway was not deployed.

Capture targets bind actual account address, provider message ID and navigation
ID. The target is persisted before opening the message; both IDs must match the list
and the account must agree. An invocation cannot be reused for another target;
already-read targets recover by identity. Gateway verifies manifest account and
message identity against the request. These are background-program inputs, not
new model-controlled queries or target parameters. This step separates capture
interfaces only: no durable queue, timer, mail Store, normalization or model
analysis integration, and no resident Gateway deployment.

The unified popup, cross-account topic conversations and encountered-thread
backfill are defined in the [system design](email-management-design.md) and its
stages. This document owns source, normalized and analysis data foundations.
Tasks below remain background capture/analysis jobs, not user to-do items.

The window queries committed mail data and processing states. Opening, closing
or reopening it neither starts duplicate intake nor determines background job
lifetime. Window interactions and public APIs belong to the later management design.

Gateway owns canonical records through the existing [typed Store](store.md).
Use the configured governed local workspace for immutable file payloads; do not
introduce a separate SQLite database, graph service or vector database.
The file and PostgreSQL Store backends must have equivalent durable semantics;
Memory remains the non-durable reference/test backend. Proposed email-specific
repository methods are design requirements, not existing APIs.

## Intake Responsibilities And Execution Boundaries

These foundation requirements are not a complete background service; independent
discover/capture internal entry points already exist. Timers
trigger discovery; they are not the durable queue. Workflow may organize model
analysis steps but is not a prerequisite for deterministic intake. The delivery
section above describes the existing source stage; its first-unread Workflow is
not the required future background entry. The prior review was documentation-only;
the implementation follow-up above records subsequent code changes.

```text
Scheduled discovery → durable identities and capture jobs → capture by identity → commit verified local data
                                                                                         ↓
                                                                               independent model jobs
                                                                                         ↓
                                                                  management window queries data and states
```

| Responsibility | Input/output boundary | Execution owner |
|---|---|---|
| Discovery | Verified mailbox, scope and progress; bounded candidates, stable message identities, evidence and scan coverage | Deterministic program triggered by background scheduling |
| Capture | Persisted target identity; original, body, parts, manifest and separate read-state result | Worker calls provider script; never substitutes another message |
| Normalization and analysis | Committed verified source versions; deterministic parsing followed by model jobs bound to input versions | Runtime owns parsing/analysis; Workflow is optional |
| Management presentation | Committed records, job progress, coverage gaps and analysis | Future pop-out window; no competing state authority |

One message is the capture execution unit, not a discovery batch limit. First-unread
may remain a manual convenience, but cannot be the sole background queue source.
Independent capture-by-identity and same-invocation pinned recovery already exist,
but durable queues and formal Store admission remain unimplemented.

Unread is a discovery condition, not durable task state. Persist candidates and
capture jobs before opening mail. Provider auto-read or user read-state changes
cannot erase existing jobs. Deduplicate repeated discovery by
`(owner_id, mailbox_id, provider_message_id)`; marking captured mail unread again
does not automatically create duplicate capture or analysis. Advance scan progress
only past durably recorded candidates. Do not page by offsets through a shrinking
unread list: use stable cursors or bounded rescanning with identity deduplication.
Report limited coverage, ambiguous identity and actual emptiness separately;
the end of visible rows does not prove whole-mailbox synchronization. Validate
provider pagination and recovery interfaces separately before implementation.

Serialize browser jobs within each mailbox by default. Different mailboxes may
run concurrently where browser-session isolation and Controller constraints permit.
Model jobs use independent bounded concurrency and do not block the next capture.
Jobs require durable state, leases or equivalent exclusive claims, stale-worker
protection, bounded retries and next-run times. One failing message must not block
others. Queue capacity and backlog need backpressure, not unbounded browser/model work.

Conditionally commit pending analysis intent with source admission in Store;
workers claim it idempotently. A lossy in-memory notification after admission is
insufficient. Intent waits for required parsing versions, and the resulting
analysis job pins actual immutable inputs. Capture, parsing, marking read and
analysis recover independently; model failure does not redownload originals.
Repository methods, task fields and parameters must be settled before coding;
no separate queue database is introduced.

Unread discovery does not guarantee complete history. Read history, Sent intake
and cross-mailbox management require later explicit policies. Preserve reply
headers, unresolved references and gaps without inventing complete context for
the UI. Current Gmail/Outlook singleton restrictions must be resolved before
claiming intake of all unread mail in scope.

### Capture Individual Replies Within Conversations

QQ Mail, Gmail and Outlook must each support capturing an individual reply that
belongs to a conversation containing multiple messages. A capture remains bound
to one message; it neither downloads the entire exchange nor recursively fetches
history because the target is a reply. The singleton restrictions above are
implementation gaps to remove, not product scope restrictions.

Discovery identifies individual provider messages; conversation IDs supply only
grouping or navigation. Different unread messages in one conversation can become
separate candidates. Read history, Sent copies and drafts must not inherit an
inbound unread identity from a conversation-level marker. Capture must match the
specified message to its detail and original-export control, without substituting
a conversation subject or the latest message. Quoted history remains part of the
target original, not independently captured historical messages.

Preserve observed `Message-ID`, `In-Reply-To` and `References` headers for later
association. Do not invent missing fields or automatically fetch referenced mail.
Read confirmation belongs to the individual target. Verify whether opening a
conversation also changes other messages, and do not treat conversation-level
read state as evidence of individual processing.

Each provider needs a real reply sample verifying target identity, original,
body, attachments, reply headers and retries pinned to the same target. A
conversation with multiple unread messages also needs candidate separation and
non-target read-state checks. Record each provider's qualified scope separately;
one provider or singleton sample cannot qualify the other scenarios.

#### 2026-09-08 Reconnaissance Results (Not Implemented)

The owner selected an existing conversation in the currently signed-in Outlook
account. It reports 2 Inbox messages and 6 global items, including 1 draft; the
expanded list renders 5 items. Ancestor DOM IDs of individual rows match response
`ItemIds`, `GlobalItemIds` and `DraftItemIds`, separating local members, other
folder members and drafts. The missing item is unexplained; a single expansion
does not prove complete enumeration. Selecting a specific Inbox reply leaves
exactly one individual row checkbox selected. Its context menu offers Download,
View message source and Mark as unread; the focused detail has its own menu.
This establishes identity and action entry points, not successful original
download or a fresh unread-to-read transition.

Next, qualify original export from the row bound to the verified ItemId and
compare reply headers and target content, without requiring a single body in the
entire conversation. Establish individual unread evidence, menu effect scope and
expanded-list coverage before changing production discovery/capture. The current
UI is Chinese; filter, download and read menus need Chinese/English coverage.

Gmail retains the previous account. The pinned synthetic self-mail was opened
and its reply editor inspected; no new mail was sent. That inspection may leave
an empty reply draft attached to the synthetic sample. Real multi-message replies,
unread labels and original identity remain unqualified. QQ Mail had no new live
reply validation in this round; earlier singleton evidence cannot qualify it.
Subsequent checks for all three providers must cover replies, draft exclusion,
fixed targets, body/attachments and read effects. This round changes only the
design and retains private diagnostic evidence. Production scripts and services
are unchanged; the temporary credential bridge and launcher have been removed.

## Three Layers

| Layer | Authoritative contents | Writer | Version rule |
|---|---|---|---|
| 1. Source | Actual acquired headers, original-format content when available, browser text/HTML observations, inline images and attachment bytes | Fixed provider script and Runtime ingestion | A committed capture is immutable; missing material is explicit |
| 2. Normalized facts | Provider-independent message/part records, decoded body, extraction results, identities and evidence-backed relations | Deterministic ingestion/parser | New parser or changed source creates a new immutable representation |
| 3. Model analysis | Summary, contextual explanation, evidence references and coverage gaps | Independent model job, optionally organized by Workflow, validated and persisted by Runtime | Each analysis has an immutable input manifest and output |

Layer 1 is what was actually obtained, not necessarily the complete original mail.
Save an RFC message as `message.eml` only when obtained from the provider. A DOM
capture is tagged `browser_dom`; never fabricate an EML or claim missing headers
were observed. Credentials, cookies, signed download URLs and full-page mailbox
snapshots are not email evidence and must not enter stored source payloads.

Layer 2 preserves missing values and ambiguities. It does not fill unknown sender,
timezone, original message ID or attachment status using model guesses.
Layer 3 may explain or infer, but must label inference and cannot update factual
headers, message identity, authoritative relationships or provider state.
Mail bodies and attachments remain untrusted analysis inputs. Instructions inside
them cannot change workflow authority, select tools or authorize external actions.

## Identity

| Identity | Definition |
|---|---|
| `owner_id` | Existing authenticated local owner boundary |
| `mailbox_id` | Stable local ID for one verified provider account; not the mutable default alias or credential generation |
| `mail_id` | Runtime-generated stable local message ID, independent of workflow run |
| `capture_id` | One acquired source revision for the pinned message |
| `part_id` | One attachment/inline-part occurrence in a specific capture |
| `representation_id` | One immutable normalized/extracted revision |
| `analysis_id` | One immutable model analysis result |

The primary ingestion uniqueness key is
`(owner_id, mailbox_id, provider_message_id)`. Provider IDs are opaque and use
provider-declared scope/stability rules. A thread ID is not a message ID.
If the script cannot obtain a trustworthy stable message locator, retain its
attempt as incomplete evidence; do not admit a deduplicated complete message.

Store RFC `Message-ID` as observed evidence and a lookup key, not a globally
unique primary key: it can be absent, duplicated or forged. Do not deduplicate
messages by subject, sender/date combinations or body/attachment hashes.
Never merge accounts merely because they receive the same RFC ID. Replacing a
configured account creates a distinct mailbox binding; credential rotation for
the same verified account does not change message identity.

Repeat ingestion of the same primary key reuses `mail_id`. Identical payload
digests reuse committed source content; changed or newly acquired parts create
a capture revision without overwriting history. Contradictory identity evidence
produces a conflict, not a silent merge. `run_id` records provenance only.

## Physical Storage And Authority

Under the configured workspace, use opaque IDs as path components:

```text
email/<owner-scope>/<mailbox_id>/<mail_id>/
  source/<capture_id>/
    capture.json
    message.eml                 # only if actually available
    headers.json
    body.html                   # only if actually acquired
    body.txt                    # actual observed text when available
    parts/<part_id>/<safe-name>
  normalized/<representation_id>/
    message.json
    body.txt
    parts/<part_id>/content.json
  analysis/<analysis_id>/
    inputs.json
    result.json
  staging/<attempt_id>/          # never a completed source
```

Files use UTF-8 JSON/text where applicable; original binary bytes are preserved.
Every immutable payload reference includes schema version, governed relative
path, byte length and SHA-256. Filenames are sanitized and never used as identity.
Identical attachment names cannot overwrite each other. Content hashes verify
bytes and may support owner-scoped byte reuse later; they do not merge part or
message identities. Physical deduplication is not required.

The workspace files hold immutable payload content. Store alone owns message
identity, committed version pointers, relationships and mutable lifecycle state.
A JSON file on disk without a committed Store reference is not an admitted
message. Query indexes are rebuildable projections from committed records and
payloads, not a competing source of truth. Folder enumeration and editable
`latest.json` files must not become alternate state authorities.

Runtime allocates storage paths. Controller downloads into an invocation-owned
staging directory; Gateway verifies a shared host/container mapping or transfers
files through an owned bounded channel into its governed workspace. The model
receives scoped document/file refs, never an arbitrary host download path.
Register downloaded parts through the existing [document records](document-workflows.md)
where applicable; that record describes the file, not a second email lifecycle.

This is persistent source storage, not temporary run artifacts. Analysis output
and execution trace are distinct: mail data survives normal run-log cleanup.
No deletion or retention product is specified here. External file modification
or loss is an integrity failure, not an automatic new source revision.

## Minimum Logical Records

| Record | Required information |
|---|---|
| Mailbox | Owner, provider, stable account binding, binding revision |
| Discovery progress and candidates | Mailbox/scope, progress and coverage limits, stable message identity/evidence, durable enqueue outcome |
| Background job | Capture or analysis target, deduplication basis, state, attempts/claim token, retry time and failure reason; analysis pins immutable inputs |
| Message identity | Local IDs, provider message/thread IDs, observed RFC IDs, first-seen time |
| Capture | Capture/attempt/run IDs, script revision, capture mode/time, file manifests, header/body/part-inventory coverage |
| Normalized message | Source capture + hashes, parser revision, subject, From/To/CC and available Reply-To/BCC, sent/received time evidence, decoded body ref, part refs, raw reply headers, missing fields |
| Part | Parent mail/capture, occurrence ID, provider locator when available, original filename, disposition, Content-ID, declared/detected type, bytes/hash/ref, acquisition outcome |
| Extraction | Part/body input hash, extractor revision, text/layout ref, source page/paragraph/slide/sheet locators, parse outcome and limitations |
| Relationship | Scoped source, target ID or unresolved external key, type, source evidence, resolution state and version |
| Analysis | Model/prompt revisions, immutable input manifest, structured summary/context, evidence refs, inference labels and coverage gaps |
| Operational state | Capture attempts, expected record versions, read-state observations and mark-read intent/result, analysis attempts |

Keep displayed date text even when a timestamp exists; normalized timestamp and
timezone are nullable and retain parsing evidence. Empty text differs from
missing or truncated text. An empty attachment list is valid only when inventory
enumeration completed. Preserve quoted history as body spans; it does not create
independent source messages without their own captures.

All structured records carry `schema_version`; mutable Store records also have
a conditional `record_version`. Runtime observation times use UTC, while unknown
source timezones remain unknown. Record versions, content hashes and parser/model
revisions serve different purposes and cannot substitute for each other.

Ordinary attachments and inline images are explicit parts; HTML `cid:` references
link to matching Content-ID parts in the same capture. Remote images, body URLs
and cloud sharing links remain external references and are not auto-fetched.
Limits and unavailable items are recorded; this design does not silently truncate
a source to fit model context or assume unsupported attachments were analyzed.

## Relationship Rules

The canonical graph is typed edge records in Store. It needs no graph database.

| Type | Evidence | Meaning |
|---|---|---|
| `replies_to` | Parsed explicit `In-Reply-To` | The message declares a reply to the referenced message |
| `references` | Ordered parsed `References` | Declared ancestor/context references; not every entry is a direct parent |
| `provider_thread_member` | Provider thread ID within this mailbox | Provider grouping only; not reply direction |
| `has_part` / `inline_part` | Verified capture inventory and MIME/DOM evidence | Exact part occurrence belongs to this message |
| `derived_from` | Exact input refs and digests | Representation/analysis provenance |

Quoted history, subject similarity, participants and semantic similarity cannot
create authoritative reply edges. Model-proposed relatedness stays in layer 3,
tagged as a suggestion with evidence; it neither merges identities nor rewrites
the canonical graph. RFC edges reflect an observed claim, not sender authentication.

Resolve targets only inside the same owner and mailbox.
A missing target produces an `unresolved` external key; a later acquisition may
resolve it by exact evidence without downloading history automatically. Duplicate
targets produce `ambiguous`; invalid/self/cyclic reply evidence is retained as
`invalid` and excluded from context traversal. Never choose arbitrarily.
Provider group membership and parentage may coexist without forcing a unique
parent. Cross-mailbox linking is outside this design.

Traversal is bounded and visited-ID checked. The analysis input manifest records
the exact local messages/versions used and missing references, so “available
context analyzed” never means all historical context has been obtained.

For example, acquiring reply B before original A records B's declared reference
as unresolved. Acquiring A later resolves that edge; neither source is rewritten.
A prior analysis of B becomes stale if A enters its declared context selection.

## State Boundaries

Do not collapse remote unread, discovery progress, background jobs, local collection
and model understanding into one status. Store durably owns discovery/job states;
workers advance them and the management window displays them. Distinguish waiting,
running, retry delay and terminal outcomes. Job completion does not replace source
coverage or analysis freshness.

| State owner | Values / boundary |
|---|---|
| Collection attempt | `pending -> running -> complete / partial / failed`; retries create attempts for the same pinned mail |
| Source coverage | Header/body availability and part-inventory completeness, plus each part outcome; explicit missing/limited fields |
| Part acquisition | `pending / downloading / available / failed / skipped_limit`; available requires durable verified bytes |
| Extraction attempt | `pending / running / succeeded / partial / unsupported / encrypted / failed`; download success does not imply readable content |
| Analysis attempt | `pending / running / succeeded / failed`; success records coverage as `complete_for_inputs` or `partial` |
| Analysis freshness | `current / stale`, derived from input versions/context selection, separate from attempt success |
| Provider read observation | `unknown / unread / read`, with observed time and evidence |
| Mark-read effect | `not_requested / pending / confirmed / failed / unknown`, bound to the exact message |

Collection is complete only when the exact message is verified, body and required
metadata are persisted, the attachment/inline inventory is complete and every
in-scope binary part is available. Optional absent RFC headers or unavailable
raw EML do not block a valid DOM capture; identity/body/inventory gaps do.
Unsupported parsing does not change successful binary acquisition.

On complete collection, persist an intent to mark that mail read, perform the
provider action and verify the resulting state. Model analysis need not finish
first. A partial collection can support an explicitly partial analysis but does
not authorize a new mark-read action. The mark-read effect and analysis are
separate outcomes: a read-action failure does not invalidate durable mail data.

Some provider UIs mark read on open. Record the actual observation and cause
`provider_auto`; do not claim the message remained unread or that a later failed
collection succeeded. When complete collection finds it already read, verify
that state instead of toggling it. Automatic restoration to unread is not assumed.
Mark-read timeout means `unknown`, not success; reconcile the same message.
A later user change back to unread updates observation, not local capture status.

A model failure leaves captured material and remote read state intact. Rerunning
analysis uses existing files; retrying capture uses the pinned mail even if it is
now read. `empty` is an invocation outcome, not a fabricated message/state record.
None of these fields means “user has handled this email”; no such product state
is introduced in this design.

## Publication, Recovery And Analysis Versions

1. Bind an invocation to one mail identity before opening/downloading. Use a
   per-mail active attempt token and conditional Store versions to reject stale workers.
2. Write into staging, finalize files atomically, verify bytes/hashes and flush
   them durably before publishing their manifest and version pointer in Store.
3. A single message publication conditionally records the committed capture,
   coverage, required mark-read intent and pending analysis intent. No database/filesystem distributed
   transaction is claimed. A crash before publication leaves unadmitted files;
   recovery checks the same attempt instead of selecting another message.
4. A lost Store acknowledgement is reconciled by attempt ID, expected version
   and manifest digest before any external action. Committed references with
   missing or mismatched files block use and marking read.
5. A retry never overwrites a complete capture with a partial one; source and
   parser improvements append immutable versions. Relation resolution is
   versioned independently from source bytes.

An analysis `inputs.json` pins message/capture/representation versions, hashes of
actually read body/parts and extractions, exact related-mail inputs, relationship
snapshot/selection-rule revision, and model/prompt revisions. Evidence references
locate source spans or document pages/paragraphs, not just a filename. A summary
from an earlier analysis may be used only as a labelled derivative with traceable
source coverage, never promoted to source truth.

New content, improved extraction or newly resolved context can make an analysis
stale. Compare the saved input/context fingerprint when reading it; append a new
analysis only when explicitly rerun, keeping the old result. Read/unread changes,
timestamps of polling and credential rotations alone do not stale content analysis.
No automatic reanalysis or context-fetch worker is designed here.

## Design Acceptance

Verify account isolation and deduplication, immutable/versioned payloads, same-name
parts, source fidelity and missing-field semantics, unresolved/ambiguous/cyclic
edges, evidence provenance, the independent state transitions, crash recovery,
read-action uncertainty, stale analysis and host/container file visibility.
The file/PostgreSQL implementations must share these contracts.

Foundation integration must also verify repeated-discovery deduplication,
scan-progress/enqueue consistency, changing unread lists retaining discovered
candidates, recovery after auto-read, admission/analysis-intent consistency,
stale-worker rejection, failing-message isolation, model concurrency independent
of capture, and recovery after window closure or backend restart. These are
design acceptance requirements, not implemented or qualified behavior.

Delivery acceptance covers the source scripts, manifest/file validation,
acquisition limits, partial coverage and mark-read ordering. Script tests do not
validate the unimplemented three-layer Store or model-analysis node. Those components
remain outside the current implementation scope.
No cross-project contracts change, and other cluster projects need no follow-up.
