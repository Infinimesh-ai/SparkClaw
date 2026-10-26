# Network-Native Mail Architecture (v4)

> Language: English | [简体中文](../zh-cn/docs/email-network-native-architecture.md)

For current source limitations and the next bounded changes, start with the
[mail diagnostics and change-control baseline](email-diagnostics-and-change-control.md).
The dated deployment observations below remain historical evidence.

- Status: **proposed design; N1–N3 and the SparkClaw/ISCP parts of N4 deployed 2026-10-22; live gates partly open (section 9.2)**
- Date: 2026-10-22
- Baselines: SparkClaw `456b9fb3`, App-CLI `a91396e` (release 31), ISCP `2a252fe`
- Supersedes as direction (keeps every accepted invariant of):
  [email pipeline optimization](email-pipeline-optimization-design.md),
  [timeline incremental sync](email-timeline-incremental-sync-design.md),
  [notification wakeups](email-notification-wakeup-design.md),
  [cross-round reuse](email-cross-round-reuse.md) and the mail parts of the
  [App-CLI extension design](app-cli-email-extraction-design.md).

This document reorganizes the mail system around one idea: **the signed-in web
client already performs every operation SparkClaw needs over its own network
interface, and already tells the page when mail changes.** SparkClaw should
listen to those notifications, reuse the web client's own requests as
templates, prove every outcome from the web client's own responses, and keep a
small resident control plane around the page. It should stop wrapping a
push-capable page in a pull loop, stop driving network calls through the UI,
and stop paying process and fsync costs per control message.

## 1. Scope and Constraints

- **Browser session only.** On 2026-10-22 the owner rejected standard-protocol
  connectors (IMAP/SMTP with authorization codes or app passwords, Gmail API,
  Microsoft Graph) and every OAuth registration: they introduce request quotas
  and credential handling. Mail remains inside the dedicated signed-in browser
  profile, as recorded in the [browser email design](browser-email-workflow-design.md)
  and [integrations](integrations.md). No cookie export, no credential store.
- **Derive from the browser's own requests.** Every listener is built on a
  notification the web client already receives; every acquisition or mutation
  script is built on a request the web client itself issues. DOM interaction is
  allowed only as an explicit *trigger* of a native request that is not yet
  learned, never as the source of evidence.
- **Unchanged invariants** (section 6): no resend after an unknown outcome,
  exact account identity on every call, Gateway Store as the only durability
  owner of business state, bounded/private evidence leaving the page, original
  UI and entries preserved, unqualified coverage stays visible.

## 2. Current Architecture (as built)

### 2.1 Topology correction

Mail acquisition does **not** run on the Mac. Two planes exist:

| Plane | Where | Path |
|---|---|---|
| Acquisition | work2 (Linux), all local | Gateway (container) → Controller unix socket → `tools/browser-controller` → App-CLI `MailboxClient` → `ProductClient` → `python -m app_cli --machine` → `node client.mjs` → `executor.sock` → mail handler → HostPort HTTP back to the Controller → Playwright CLI → Browser Bridge → Chromium page → managed Tampermonkey Reader → provider endpoints |
| Presentation | Mac SparkX ↔ work2 over ISCP | Electron → Go helper → Relay (TEST1) → work2 `iscpworkbench` → Gateway HTTP handlers; mirror via `mail.sync`; originals/attachments as 8 KiB ISCP objects |

ISCP never carries provider traffic. The Mac desktop `BrowserHostAgent` is not on any mail path.

### 2.2 What the page already does well

Receive is already network-based: the Reader patches `fetch`/XHR, keeps the
web client's own list request as a template and replays it in the page; originals
come from native endpoints (QQ `/read/readmail?func=5`, Gmail `view=att…disp=comp`,
Outlook `service.svc/s/DownloadMessage` with the page's own header token). Push
channels are tapped: QQ `wss://wx.mail.qq.com/socket`, Gmail
`punctual/multi-watch/channel`, Outlook worker `subscribeToRowNotifications`.

### 2.3 Measured behavior

| Measurement (source) | QQ | Gmail | Outlook |
|---|---:|---:|---:|
| Warm empty / 1 new / N new round, s ([cross-round reuse](email-cross-round-reuse.md)) | 1.93 / 2.71 / 10.29 | 2.16 / 4.65 / 15.13 | 6.10 / 13.59 / 22.65 |
| Range query with watcher resident, s ([notification wakeups](email-notification-wakeup-design.md)) | 4.36 | 9.24 | 17.22 |
| Provider receipt → message discovered → committed, s (2026-10-15 lesson) | 9.34 → 11.43 | — | 23.88 → 31.51 |
| Login check, s (2026-10-21, release 31) | 15.10 | 7.88 | 11.24 |

A QQ single-mail round spent 2.94 s preparing the page, 0.76 s discovering,
0.86 s fetching the original and under 10 ms writing — durability is not the
bottleneck; orchestration is.

### 2.4 Incident profile

73 recorded mail problems (2026-09 → 2026-10-21): fence/unknown-outcome handling
18, DOM/page contract 15, release pairing 12, ISCP transport 11, latency 8,
identity 7. Recurring patterns: generic errors hiding the cause (4×), Reader
deployment drift (5×), cleanup fences left by closed pages (4×), "completed"
treated as proof of the next step, Outlook account ID ≠ delivery address.

## 3. Diagnosis: Six Structural Causes

**D1 — A pull loop wrapped around a push-capable page.** The provider tells the
page exactly what arrived (QQ notify carries the new `mid`; Outlook `RowAdded`
carries `ItemId` and `DateTimeReceived`), but the observer reduces it to a bare
`mailbox_changed` hint
(`App-CLI …/runtime/mail-notification-rules.mjs:7-15`). The hint then waits for
the Controller's 5 s `tick()` and a process spawn
(`tools/browser-controller/src/mail-observer-feed.mjs`), a Gateway job claim,
intake admission, 2 s increment spacing and a full interval discovery before the
original is fetched. Measured Outlook: 20.6 s from Gateway notification to
discovery.

**D2 — Network calls driven through the UI.** Outlook range discovery fills
`#topSearchInput` and presses Enter on *every* round to obtain one rewritable
search request (`network-reader.mjs:107-120`). Gmail falls back to hash
navigation without a template. Send, mark-read, login probe and account
identity use DOM selectors and English/Chinese label regexes
(`managed-send-dom.mjs`, `qqmail-mark-read.mjs`, `*-login-probe.mjs`). These
are the dominant source of `email_page_contract_changed` and unknown outcomes.

**D3 — A per-message process model in the control plane.** Each control message
spawns Python, which re-verifies the 82-file release (~1 MB hashed), rebuilds
the Registry and validators, then spawns Node to reach `executor.sock`
(`product-client.mjs:43`, `runtime_transport.py:18`). `ProductClient.wait`
repeats this every 250 ms, so a 10 s read costs about 85 spawns. Each ledger
transaction adds about three fsyncs.

**D4 — Coarse serialization.** The Gateway provider lock covers admission,
collection, mark-read and send across owners and can be held for a
`collect_page` budget of 1 800 s; every operation for one owner and provider
shares one executor FIFO lane.

**D5 — Proof gaps on mutations.** Gmail send is proved only by a toast text match
within ~3 s; QQ and single-recipient Outlook by a new first Sent row; Gmail and
Outlook mark-read always return `unknown`; reply targets are not proved in the
native editor. Every gap becomes an `unknown` outcome, a fence and manual
resolution.

**D6 — Presentation-plane transport costs.** ISCP objects move as 8 KiB chunks,
strictly one RPC at a time (a 10 MiB message: 1 280 round trips, 103.7 s); each
envelope rewrites the sealed Relay journal three times; each work2 mail mutation
lists and stats the whole receipt directory (≈71 k files) under one mutex; the
Relay delivers one message per 25 ms tick and opens a claim transaction on every
tick even when idle.

## 4. Target Architecture

### 4.1 Principles

1. **The page is the network contract owner.** One provider module in the page
   owns taps, templates, target extraction and native calls. Node code never
   reconstructs provider requests.
2. **Every operation = native request template + native response proof.** A
   template is learned from a request the web client itself issued, validated
   by shape, and replayed in the page with the page's own credentials. The
   outcome is proved by the provider's response (status, provider code, returned
   identifiers), not by UI text.
3. **Push first, sweep second.** Notifications carry exact targets that are
   captured immediately; interval sweeps remain the completeness guarantee and
   run on the fallback cadence.
4. **A resident control plane.** Long-lived sessions replace per-message
   processes; waiting is cheap; durability is spent only where recovery needs it.
5. **One durability owner.** The Gateway Store, the batch journal and the
   App-CLI ledger keep their current responsibilities; no new durable state is
   introduced in the page.

### 4.2 Component view

```
provider push channel ──► page tap ──► target extractor ──┐
web client's own list/sync responses ─► passive recorder ─┤
                                                          ▼
                          page agent (Reader + observer, one provider module)
                          ├─ template registry: learned → qualified → stale
                          ├─ original fetch queue (provider pacing)
                          └─ native mutation calls + response proof
                                                          │ bounded events / results
executor watch task ──► Controller feed (event-driven) ──► Gateway observer
Gateway pushed-target set ──► exact-target capture round ──► CommitEmailSync
interval sweep (fallback cadence) ──────────────────────────► CommitEmailSync
```

### 4.3 Page agent

- **Taps.** Push channel per provider (unchanged endpoints), plus the passive
  recorder that already parses the web client's own list/sync responses into
  rows.
- **Target extraction.** Each notification yields at most 16 *exact targets*:

  | Provider | Source | Target fields |
  |---|---|---|
  | QQ | notify envelope `type:'0'`, field `e` (`mid` only deduplicates) | `provider_message_id=e`, the list `emailid`; selection = message; folder from the next list row when known |
  | Outlook | `RowAdded`/`RowModified` with `Item.ItemId` + `DateTimeReceived`, or conversation `ItemIds` | `provider_message_id` resolved through the recorded row (ImmutableId form), `received_at` |
  | Gmail | topic invalidation is content-free; the page's own follow-up `bv`/sync response rows newer than the last emitted row | `provider_message_id`, `provider_native_id`, thread |

  A target is a *candidate*: it is accepted only if it satisfies the same
  `validateMailTarget` rules as a retry target and the original's headers pass
  the existing checks. The page never sends subject, address or body text to the
  host.
- **Template registry.** For each native request kind (list, range search,
  original, mark-read, send, identity) the agent stores the last request the web
  client issued, keyed by a shape fingerprint (method, path, header names, body
  key structure). States: `learned` (seen once), `qualified` (a qualification
  run proved the response contract), `stale` (a 401/403/shape mismatch). A stale
  template triggers exactly one UI re-arm to relearn, never a blind retry.
- **Original fetch queue.** One in-flight original per page; QQ keeps its 1 s
  pacing (`-20003` anti-abuse). Push targets and sweep targets share the queue.

### 4.4 Receive event path

1. The observer emits `mailbox_changed` with `targets`.
2. The executor watch forwards targets on its task events (bounded, deduplicated
   per document).
3. The Controller feed reads watch events on a 1 s cadence instead of 5 s once
   control calls are resident (N1), and publishes them immediately.
4. **Qualification first (implemented).** The Gateway accepts the optional
   `targets` field, keeps the candidates in memory per mailbox (bounded at 64,
   15-minute expiry) and, after every committed round, logs whether each
   candidate named a round member — by message id, item id or receipt time — and
   with what latency. Identifiers are never logged. Nothing changes what a round
   captures. This matters because the identifiers are not proven equal: Outlook
   notifications carry the movable `ItemId` while qualified captures key on
   `ImmutableId`, and Gmail invalidations carry no identifier at all.
5. **After the gate (implemented, default off; section 9.7).** For a mailbox
   whose candidates matched the listed row for three consecutive natural
   arrivals, the Gateway keeps them as a bounded, generation-bound pending set in
   its own persisted gate, and the next increment passes them as exact retry
   targets, so they are captured even when time-range discovery would miss them.
   Gmail Spam stays excluded by the same folder policy as discovery.
6. The interval sweep is unchanged and remains authoritative for coverage. Once
   a provider's push path is qualified, its periodic cadence may use the
   existing five-minute quiet tier. Notifications and new plans now wake idle
   email workers immediately instead of after their one-second idle sleep.

A fully page-resident capture stream (bytes produced by the watch itself and
committed without a Gateway round) is the next step (N5). It is deferred until
N1–N4 are measured, because it needs a new Store commit command across all three
backends and reopens fence semantics that took 18 incidents to stabilize.

### 4.5 Native mutation path

- **Learning window.** Before a DOM-triggered mutation (mark-read click, Send
  click) the agent arms a bounded window that records the web client's own
  same-origin requests issued within it: method, path, status, provider result
  code and body *key shape* only. Evidence never includes values.
- **Network-confirmed outcomes.** Once a provider's mutation request is
  identified, its response becomes the outcome proof: a send is `sent` when the
  provider's own send response succeeds with a returned message identifier, and
  `not_sent` when it fails before acceptance. Toast and Sent-row proofs remain as
  secondary evidence until then.
- **Template replay.** After a qualification run proves the request contract,
  the click is replaced by an in-page replay of the learned request with the
  target substituted (mark-read first: low risk, idempotent). Send replay is
  promoted per provider only after mark-read replay is stable, and never retried
  after an ambiguous response.
- **Reply.** The reply target is the stored provider identifiers of the original
  message; a native reply request (or the native reply route opened directly)
  replaces UI selection of the target.

### 4.6 Identity from network

Account identity comes from the web client's own bootstrap/startup responses
(Outlook `startupdata.ashx` `SessionSettings.UserEmailAddress`, already used as a
fallback), with DOM labels as a qualified fallback only. QQ and Gmail network
sources are identified through the learning window and promoted when qualified.

### 4.7 Resident control plane

- **Session pool.** `python -m app_cli --machine --session` reads one JSON
  request per line and answers one line per request, verifying the release and
  building the Registry once per process. `ProductClient` keeps a small pool of
  session processes (one request in flight per process, so Registry code need
  not be thread-safe) and falls back to the one-shot entry when a session fails.
- **Direct executor transport.** The Python runtime adapter may be configured
  with the executor socket path and exchange the existing EOF-delimited message
  directly, removing the Node client spawn. The wire message, grant resolution
  and validation are unchanged.
- **Cheap waiting.** With resident sessions, status polling costs a socket round
  trip; `wait` polls adaptively (50 ms rising to 250 ms).
- The public execution entry point, grant model and lifecycle 3.0 wire stay as
  they are; this changes process lifetime, not protocol.

### 4.8 Concurrency

Lanes are keyed by owner, provider and page class: the shared inbound page
(watch, discovery, capture) and the mutation page (send, mark-read) no longer
queue behind each other. The Gateway provider lock is narrowed to admission of a
single page operation rather than whole collection budgets.

In concrete terms (9.3): send and mark-read declare the binding activity
`mutation` in the family and executor lane `<provider>.mutation`; the Browser
Controller reserves that family like any provider page, so it neither waits for
nor drains inbound pages of any provider. Only login, validation and generic
browser access stay whole-profile `exclusive`. The Gateway keeps one inbound and
one mutation lock per provider.

### 4.9 Presentation plane (ISCP)

- **Object pipelining.** The server already advertises a credit window; desktop
  clients send/receive up to the advertised window concurrently, and the server
  window grows from 2 to 8. Older clients keep working sequentially.
- **Receipt capacity counters.** work2 keeps receipt count/bytes in memory,
  rebuilt at startup and reconciled by the existing hourly scheduler, instead of
  scanning the directory on each mutation.
- **Relay delivery drain (ISCP).** After a successful delivery the Relay drains
  further pending messages in the same tick (bounded), and admissions wake the
  recipient connection's delivery loop; the idle tick lengthens. No wire change.
- **Authorization reuse (needs decision).** Each desktop RPC performs two fresh
  issuer checks (≈1.1 s each in isolation). A bounded reuse like the existing
  stream authorization snapshot would change the ISCP–SparkClaw contract and is
  proposed separately, not implemented here.

## 5. Provider Matrix

| Operation | QQ Mail | Gmail | Outlook |
|---|---|---|---|
| Change listener | WebSocket notify → **exact `e` (emailid) target** | multi-watch invalidation → **rows from own sync response** | `subscribeToRowNotifications` → **exact ItemId target** |
| Discovery | `/list/search` replay (kept) | `bv` replay (kept) | UI-typed search → **learned search request replay**, UI only to relearn |
| Original | `/read/readmail func=5` (kept, 1 s pacing) | `view=att` (kept) | `DownloadMessage` + page header token (kept) |
| Mark-read | DOM row click + search confirm → **learned native request** | `unknown` → **learned native request** | `unknown` → **learned native request** |
| Send proof | first Sent row → **native send response** | toast regex → **native send response** | Sent row / toast → **native send response** |
| Identity | DOM header text → **network source when qualified** | DOM account button → **network source when qualified** | startup response (kept) + DOM fallback |

Bold entries are changes; each needs the evidence gate in section 8.

## 6. Safety Invariants (unchanged)

1. A send whose outcome is ambiguous is never resent; reconciliation is
   evidence-only. Template replay inherits this.
2. Every page call re-checks the exact account; a mismatch blocks.
3. Push targets are hints for exact capture; only committed captures change the
   mailbox, and only sweeps advance `PollThrough`.
4. The page exposes only bounded identifiers, hashes, sizes, status codes and
   key shapes; never subject, body, addresses, headers or tokens.
5. App-CLI lifecycle 3.0 (epochs, dispatch release, ACK, user resolution) and
   the Gateway journal/commit semantics are unchanged.
6. Original UI, menus and entries remain; no permanent technical status display.

## 7. Rejected Alternatives

| Alternative | Reason |
|---|---|
| IMAP/SMTP, Gmail API, Graph, any OAuth | Owner decision 2026-10-22: quotas and credentials; outside the browser-session boundary |
| UI export/download of originals | Slower and fragile; native original endpoints already work |
| Playwright/CDP `response` listeners | Reproduced page loss on Gmail/Outlook (2026-09); in-page taps are stable |
| Rewriting the commit model first (page-resident stream) | Highest risk; deferred to N5 behind measurements |

## 8. Gates and Measurements

| Gate | Evidence |
|---|---|
| Push target (per provider) | Three natural arrivals where the pushed target commits before the sweep; the target ID equals the sweep's row ID; no duplicate capture |
| Learned search (Outlook) | Ten consecutive warm rounds without UI typing; relearn after forced 401 exactly once |
| Native mark-read (per provider) | Learning-window shape recorded; replay confirmed by an independent list read; no repeat on ambiguity |
| Network send proof (per provider) | Real send approved by the owner; provider response identifier matches the delivered original |
| Control plane | No Python/Node spawn per control message in steady state; existing lifecycle tests pass |

Targets after N1–N4: notification → committed p95 ≤ 5 s (QQ, Gmail) and ≤ 8 s
(Outlook); warm empty sweep ≤ 1 s (QQ, Gmail) and ≤ 2 s (Outlook); control
message ≤ 20 ms on work2; 10 MiB object transfer ≤ 30 s.

## 9. Stages

| Stage | Scope | Projects | State |
|---|---|---|---|
| N1 | Resident control plane: session pool, direct executor socket, adaptive wait; Controller watch-event cadence 1 s | App-CLI, SparkClaw | implemented locally |
| N2 | Push targets: observer extraction, watch forwarding, feed field and Gateway match measurement; use as exact retry targets after the gate | App-CLI, SparkClaw | measurement deployed; gated capture use implemented locally, default off (9.7) |
| N3 | Learned Outlook search replay; learning window recorder (evidence only) | App-CLI | implemented locally |
| N4 | Presentation plane: object pipelining, receipt counters; Relay drain (ISCP) | SparkClaw, ISCP | implemented locally |
| N5 | Page-resident capture stream with a dedicated Store commit | App-CLI, SparkClaw | deferred until N1–N4 measured |
| N6 | Native mark-read/send replay and network identity per provider | App-CLI, SparkClaw | after live qualification |

Live qualification on work2 (natural arrivals, owner-approved test sends) is
required before any stage is declared accepted; automated tests do not
establish provider behavior.

### 9.1 Implementation record (2026-10-22)

| Change | Commits | Local evidence |
|---|---|---|
| Resident sessions, direct socket, adaptive wait | App-CLI `f391ccc` | 20 status reads: 2 056 ms one-shot vs 40 ms resident; Python 90 |
| Outlook learned-search replay | App-CLI `26a0a44` | 40 range and 4 reader fallback cases |
| Notification candidate targets | App-CLI `22754ce`; SparkClaw `5fb241be`, `fe89ca9a` | real executor/Registry watch case, strict-decode contract case |
| Learning-window recorder | App-CLI `dee39a1` | shape-only, redaction and bound cases |
| Release `.32`, worker r15 | App-CLI `c718108`; SparkClaw `fe89ca9a`, `57e7ad6f` | runtime 477 (recovery cases enabled); Controller 291 pass / 126 skip; affected Go packages and vet pass on Linux |
| Receipt capacity cache | SparkClaw `7dc9b003` | cache equals fresh scan after save/overwrite/retire |
| Object pipelining, window 8 | SparkClaw `9595f139` | Desktop 510; resume and completed-transfer regressions |
| Relay drain and wake | ISCP `f603722` | 24 back-to-back admissions in 217 ms with idle tick disabled; fails with batch 1 |

The owner approved the rollout and real test sends on 2026-10-22; see 9.2.

### 9.2 Deployment and live gates (2026-10-22)

Deployed: TEST1 Relay ISCP `f603722` (program only, 1.5 s downtime; 12 devices
delivered 317 messages within 40 s); work2 source `dceb0308` with App-CLI `.32`,
Tampermonkey 5.5.0.15 (r15) and Bridge 1.0.29 (03:51 UTC; 41 tables, 2 132
workspace files and 75 815 receipts preserved; 92 historical unknown executor rows
unchanged); Mac SparkX built from `8701f4e7`, which regenerates the desktop managed
reader preload that `.32` left stale (6 profiles, 97 tables, 22 messages
preserved). On work2 the native Tampermonkey import timed out twice because
Chromium's managed policy became readable only after the worker's retry budget; a
third start imported it.

Nine real sends from work2 (three rounds, each provider sending and receiving
three times) all ended `sent`; none was ambiguous or resent.

| Gate | Result |
|---|---|
| Push target | **Open.** Candidates reach the Gateway, but none matched: the QQ notify `mid` and the list `provider_message_id` are different identifier spaces; Outlook `ItemId` candidates were unmatched; Gmail carries none. Capture use stays blocked. A live envelope then showed QQ's own `e` field equals the list `emailid` (SHA-256 match); App-CLI `a849d2d` (not released) emits `e` and returns the Outlook search ItemId as the native locator, so both can be measured again after a release. |
| Learned search (Outlook) | **Partly met.** 13 consecutive rounds (04:22–04:35 UTC) left the search box unchanged, so none typed into the UI; the forced-401 relearn was not exercised. |
| Native mark-read | **Shape only.** QQ recorded `/read/readmail` and related requests; Gmail and Outlook issue none yet. Replay is N6. |
| Network send proof | **Shape only.** QQ recorded `POST /send/sendmail` and Gmail `/sync/u/:id/i/s`; Outlook's send left no same-origin page request. Identifier matching is N6. |
| Control plane | **Partly met.** Resident `--machine --session` processes and the direct executor socket are in use; no Python/Node spawn per control message. Each Playwright page command still spawns `playwright-cli.js` (about 24 per minute). Per-message latency could not be isolated. |

Measured against the targets: notification → committed was 5.1–8.0 s for QQ and
2.4–11.3 s for Gmail (85 s when a send held the browser); Outlook did not commit,
because its original resolver stops at `modules` (the web client's chunk IDs
changed, which `.32` did not cause), so Outlook arrivals stay discovered without
an original. Outlook rounds take 13–15 s and each send 60–100 s; sends block
inbound work for every provider (section 4.8 not yet effective). The 10 MiB
object transfer was not exercised.

### 9.3 Local learning and QQ replay prototype (2026-10-22)

App-CLI `874d1ff` on `claude/mail-native-send` resumes the interrupted work.
Response-shape learning now covers bounded XHR/fetch responses and Outlook
Worker/MessagePort callbacks; the early hook is also bound to Outlook send pages.
Requests and callbacks must belong to the same active learning window.

The QQ prototype is opt-in (`APP_CLI_MAIL_NATIVE_SEND=qq_mail`), same-account,
single-recipient, plain-compose only. An independently confirmed DOM send can
teach one page-private template; expiry/account changes discard it. Old message
fields and draft identity are cleared, and unsupported fields/modes are rejected.
The existing authority release and durable journal precede its single dispatch.
Every replay result remains `unknown`, even HTTP 200 / `ret=0`, until the actual
response identifier and independent native Sent read are qualified. Lost replies
and reconciliation never resend. A warm mutation page is required; this change
does not introduce page retention, and cold pages keep the existing DOM flow.

Local macOS validation: Node 26.2.0 runtime **508/508**, no skips, with serial
file scheduling and recovery cases enabled; Python 3.11 **90/90**; generated
userscripts/bindings and public-tree checks pass. Baseline and candidate parallel
runs both exposed two short-deadline executor failures; the serial candidate run
passes. Canonical `TMPDIR=/private/tmp` avoids the baseline's symlink-path failures.
All provider transports in the added tests are synthetic. No live send, push,
release, merge into main, Mac/work2 upgrade or UI change occurred. The separate
mutation-lane commits remain independent; a future paired release must integrate
and qualify them. The owner's prior instruction still requires approval before
deployment or real test mail. These results do not close N6 or establish latency.
### 9.4 Send blocking inbound work: root cause and fix (2026-10-22)

The work2 executor ledger for 04:00–04:50 UTC shows the block directly. A QQ send
started at 04:02:05.8 and ran 77 s; Gmail and Outlook `collect_page` tasks created
in the same second finished 78.8 s and 78.6 s later (warm rounds take 1.5–3 s and
10–13 s). The same happened around every send and around each QQ mark-read
(33–38 s). The first round on each provider after a send then took 13–29 s, because
its Reader page was cold.

Different providers have independent executor lanes, but browser-wide reservations
still serialize them. Same-provider executor work also shares a lane. The block
has four layers:

| Layer | Location (before fix) | Effect |
|---|---|---|
| Binding | App-CLI `applications/mail/build-bindings.mjs:38` | send/mark-read declared `activity: 'exclusive'` in families `send`/`mark_read` |
| Browser Controller | `tools/browser-controller/src/host-driver.mjs:161-164,199` → `controller.mjs:443-447` | `exclusive` takes the whole-profile reservation: every provider's page reservation waits for the entire send, and `drainIdle()` disposes every parked Reader page |
| Executor | App-CLI `runtimes/browser/src/executor.mjs:278` | lane `owner + spec.resource`; send's resource was the provider, so the same provider's inbound tasks queued behind it |
| Gateway | `emailautomation/controller.go:214`, `intake_operations.go:109` | `SendForOwner`/`MarkReadForOwner` held the single per-provider lock (`provider_lock.go`) for the whole operation |

The paired mutation-lane change gives send and mark-read
`resource` and `host.family` `<provider>.mutation` and the new binding activity
`mutation` (schema enum extended). The Controller derives a reservation from the
binding through `reservationPolicy()`: a mutation is a provider-keyed
reservation, does not drain, and still makes login, validation and generic
browser access wait. The Gateway splits the provider lock into inbound and
mutation lanes. Task pages are inactive tabs in the Bridge's unfocused task
window, with `Page.bringToFront` blocked unless a handoff is granted, so a send
page shares no focus with Reader pages.

Why one send takes 60–100 s: proof waits are small (Gmail polls the toast at most
12 × 250 ms; QQ waits at most 10 s for the list). The time goes to spawning
processes. A compose send makes 23–27 page calls (a fixture count over
`sendManagedMail`). `host-page.mjs` runs each call as separate `playwright-cli.js`
processes: a `tab-list` topology check before every command (`#withTaskSelected`)
and a `location.href` eval after every click, fill and press. That comes to about
70–85 process spawns per send. Starting the CLI process alone takes 0.42 s on work2.
Batching a mutation's guarded steps into one process, as `cli-read-batch.mjs` does
for reads, is a separate follow-up; with the lane fix, it affects send latency
only, not inbound work.
That follow-up is implemented on a branch, together with login-check reuse
before sends, in [mail send latency](email-send-latency.md); it is not deployed.

### 9.5 QQ replay qualification and work2 release 33 (2026-10-22)

The owner explicitly authorized publication, work2 deployment and new brief test
messages, while prohibiting resends of historical unknown mail. App-CLI main
`8a35bef` is published as immutable `0.3.0-sparkclaw.33`, paired with SparkClaw
`28f87ea5`, Tampermonkey `5.5.0.16` and unchanged Bridge `1.0.29`. This round does
not upgrade the Mac installation or ISCP. The mutation lanes in 9.4 are integrated.

The QQ prototype now learns only a qualified `func=1` send; `func=2` draft saves
cannot supply a template. A successful same-origin response must contain
`head.ret=0` and `body.messageid`. After DOM confirmation, an independent
`POST /list/maillist` read must join that identifier to a unique Sent row with
`dirid=3`, the exact subject, sender and sole recipient. The normal receipt uses
that row's `emailid`; the private journal retains both identifiers and the proof.
A replay also takes a pre-send Sent baseline and rejects existing or ambiguous
IDs. HTTP success alone is insufficient. The template is single-use, stays only
in the same page's memory for at most 120 seconds, and clears old content,
recipient and draft ID. Unknown outcomes never fall back or dispatch again.

Qualified QQ send pages may remain warm under the explicit work2 opt-in. They
have a separate mutation pool, verify the same document/account on reuse and
clear round secrets and diagnostics while parked. The Controller refreshes the
lease registration, abort signal and deadline before reloading secrets, so a
previous lease's cancelled signal cannot poison a new send. Read pages retain
their independent lane. Default deployments keep native send disabled.

Release validation: App-CLI runtime **533/533**, Python **90/90**, generated assets,
public-tree and exact wheel/runtime/source closure pass. Controller **294 passed,
126 skipped**; Linux Go tests across **75 packages**, build/vet and WebChat build
pass. The served WebChat's five files match the original deployment byte for
byte; no UI source changed.

work2 passed final release, service-health and preservation gates at
**07:25:09 UTC**. All 41 business tables, 2,446 original workspace files, 76,892
original receipt files and 92 historical unknown rows remain. Initial managed
Reader import timed out twice. A temporary loopback-only diagnostic observed
the new worker receiving empty managed storage, retrying eight times and then
importing successfully. This supports a startup timing race, but the first two
attempts lacked worker-console evidence and their exact cause is unproven.
Normal startup then passed the complete installed/native gates; temporary
launch overrides and the debug port were removed. Admission was paused during
recovery; no database, ledger or browser profile was restored.

The first fresh .33 DOM test was confirmed `sent` in **83.393 s** at the API
(**63.686 s** inside the send adapter). Its template was rejected with
`response_identifier`, so the second send was not attempted. The actual loaded
QQ bundle defaults its send XHR to `responseType=json`; the .33 observer passed
an empty string for that response type. This is a live qualification failure,
not successful replay. Historical unknown rows stayed at 92 with no additions.
The diagnostic collector also failed independently: Playwright's request client
requested browser-wide cookies, which the formal Bridge rejects. Its zero
records cannot establish that no network send occurred. The temporary client
was revoked (HTTP 401 verified), and its credential file was deleted.

### 9.6 JSON XHR response fix and release 34 (2026-10-22)

App-CLI `e147d45` freezes immutable `.34`, paired with SparkClaw `1def2c5d`
and Tampermonkey `5.5.0.17`. The actual QQ site bundle was inspected without
sending another mail: `/send/sendmail` uses `post`'s default JSON response type.
The Reader now normalizes that browser-decoded JSON only in the current learning
window. Serialization is limited to 1 MiB UTF-8, 4,096 nodes and 32 levels; it
rejects accessors, cycles and invalid values without invoking `toJSON`. Both
response-shape learning and the send-template observer consume the same bounded
text. The text XHR path remains covered. The old implementation fails the new
valid-JSON regression; the fixed complete runtime suite passes **547/547**.

The paired release closure, fresh-install/tamper/mixed-version/rollback gates,
Controller **294 passed/126 skipped**, component tests **33 passed/1 skipped**,
Linux emailautomation tests, binary build and original WebChat build pass.
The Linux binary and five served UI files are byte-identical to .33. Python
application logic is unchanged from its .33 **90/90** validation; the full
75-package Go/build/vet result from .33 remains the baseline. The release also
extends the bounded managed-policy and profile readiness budgets; that alone
does not establish the cause or resolution of the earlier import failures.

work2 became healthy at **08:11:08 UTC**, preserving 41 business tables, 2,472
original workspace files and 76,978 original receipt files. The larger import
budget still timed out twice. During stopped-admission diagnosis, managed policy
changed from absent to the expected value before an explicit policy refresh;
its timing cause remains unresolved. One guarded TM reload then passed the
four-script import check in **2.388 s**. Normal startup passed import and Bridge
handshake, but an early Preferences snapshot still held the reload's temporary
registration. After Chrome flushed its normal registration, the **original
strict preference gate** passed, with no exception or profile edits. Four
browser starts and one extension reload are recorded. The temporary launcher
override and debug port were removed before admission; no database, ledger or
profile backup was restored.

Before this cutover, a diagnostic temporary page in an active Outlook context
was strongly correlated with `page_topology_changed` and one `collect_page`
task becoming uncertain. Exact cross-process linkage is inferred from timing
and operations, because Controller records lacked the task ID. All original
92 unknown rows remain unchanged; the total is now 93. The additional row is
a read operation, not a send, and has not been retried or resolved. Future
diagnostics must not create pages in an active production task's context.

The final .34 live qualification **passed** using two fresh, brief QQ→Outlook
messages. The first produced a qualified template; the second consumed it once
in the same page. Each emitted exactly one `/send/sendmail` `func=1` request,
returned `head.ret=0`, and matched the independent Sent listing by both IDs,
folder, sender, recipient and subject. The native journal is durably `sent`,
with `qq_sent_list_v1`, `fresh=true`, both identifiers and the normal receipt's
matching emailid. The two messages have different response and Sent identifiers.

| Measurement | DOM learning | Native replay |
| --- | ---: | ---: |
| Send API wall time | 83.823 s | 23.874 s |
| Managed send adapter | 59.361 s | 10.386 s |
| Passive request→response | 735 ms | 484 ms |
| First matching Sent response after send response | 1,553 ms | 4,232 ms |

The native adapter's own timing is **486 ms** for the request and **4,617 ms**
for its Sent verification loop; these have different measurement boundaries
from passive network timestamps. This single pair measures about 72% lower API
wall time, not a latency guarantee. Raw response IDs stay in private evidence;
their hashes and exact equality checks are in
[the qualification evidence](evidence/mail-native-replay-2026-10-22.json).

The .34 run preserved all 93 unknown rows and added none. Across this authorized
continuation, four fresh test messages were sent (a .32 baseline, the .33
learning failure, and the final .34 pair); none resent historical mail. The
temporary client was revoked, HTTP 401 verified, and its credential file removed.
Active diagnostic listeners were removed; the native mutation context closed.
One transient observer-install error is retained in evidence; both sends'
request/response/Sent proofs were collected. Outlook inbox arrival remains
unverified: no captured record or exact subject-to-item-ID link was available.
That is not evidence of nondelivery and does not establish inbound acceptance.

The later all-task-ID preservation check did **not** pass: 102 terminal rows
from preflight were absent (97 completed, four cancelled, one failed). Nine
disappeared before the .34 freeze and 93 after the healthy cutover. All frozen
task identities were present at cutover; unknown rows remain intact. Unchanged
.33/.34 GC code, its five-minute schedule and the observed timing support normal
retention cleanup, but old per-row epoch/ACK/finish data and itemized GC logs
were unavailable. That attribution is an inference, not proof of each row's
expiry. The original failed check is retained; no database was restored and no
general exception for missing terminal tasks was introduced.
An independent **08:20:53 UTC** read-only check confirmed healthy services,
no active sends or pending release, unchanged 93 unknown rows, and both durable
send proofs. At that snapshot, 110 original terminal rows were absent; the
full-identity check remains explicitly failed.

### 9.7 Notification-target qualification, arrivals and gated direct fetch (2026-10-22)

**Evidence (work2, read-only, about 09:20 UTC).** Sources were the logs of the
three Gateway containers (.32 03:49–07:24, .33 07:24–08:10, .34 since 08:10) and
read-only SQL over `email_management_records`. Nothing on work2 changed.
Identifiers were not exported; subjects were compared by SHA-256 inside the
probe. Details are in
[the candidate and arrival evidence](evidence/mail-push-candidates-2026-10-22.json).

| Release | QQ | Outlook | Gmail |
| --- | --- | --- | --- |
| .32 (`mid`, movable `ItemId`) | 8 wakes; 0 matched, 7 unmatched | 7 wakes; 0 matched, 5 unmatched | 3 wakes, no identifier |
| .33 | no arrival | 1 matched by `item_id` (18.8 s) | none |
| .34 (`e`, search `ItemId` as native ID) | 2 matched by `message_id` (5.7 s, 6.7 s), 0 unmatched | 2 matched by `item_id` (20.4 s, 23.9 s), 0 unmatched | none |

Unmatched lines are written lazily after the 15-minute expiry, and candidates
still pending when a container stops are lost, so the .32 unmatched counts are
lower bounds. The two .34 QQ matches are the first live proof that QQ's `e`
equals the listed row's identity; both were natural arrivals (08:27:54 and
09:02:28). Outlook matches only by `ItemId` against the row's native locator;
its canonical identity remains the `ImmutableId`, which notifications do not
carry. Outlook delivery-change wakes also fired at its own three sends
(04:06, 04:15 and 04:26), while QQ envelope wakes occurred only for inbound
mail. Gmail invalidations carried no identifier.

**Receiver arrival of the 15 real sends of 2026-10-22** (the nine 9.2 sends, two
more Gmail→QQ sends at 05:20 and 05:23, the .32 baseline, the .33 learning send
and the .34 pair):

- Eight sends to QQ or Gmail: each has one inbound row with the identical
  subject digest and a complete original, discovered 1–78 s after the local
  Sent row (the 78 s case is the 04:03 send that still blocked inbound work,
  section 9.4).
- Seven sends to Outlook (the recipient is the Outlook mailbox's delivery
  alias): each has exactly one Outlook inbound row whose provider receipt time
  lies within 11 s of the local Sent row, in send order. The Sent row is
  written after the send API returns. Outlook had no other inbound row that
  day. All seven rows remain `pending` with an open failure at stage
  `original` (`email_network_original_unqualified`, 16 attempts each). Without
  an original there is no subject, so delivery is established by time
  correlation only; for the last three it is also linked through the
  notification `ItemId` matched to the row's native locator.
- Message-ID comparison was not possible for any provider: no local Sent row
  has a captured original.

**Gated direct fetch (this branch, default off).** The Gateway now keeps a
per-mailbox gate in `email/<owner scope>/push/qualification.json` (atomic
replace; unreadable or unsaved state is list-only). A candidate advances it only
when it matched by `message_id` and the listed row is rebuildable from that
identifier: folder `inbox`, selection (and thread) equal to the identifier, no
native locator. `SPARKCLAW_EMAIL_PUSH_QUALIFICATION_THRESHOLD` (default 3)
consecutive advances qualify the mailbox. Any unmatched candidate, a non-canonical
match, a changed row shape, a failed direct capture or a failed round that
carried direct targets resets it to zero. A new binding generation also resets
it. Counting runs regardless of the switch; only
`SPARKCLAW_EMAIL_PUSH_DIRECT_FETCH=true` turns qualified identifiers into
targets.

While qualified and switched on, notification identifiers are kept as pending
targets (at most 16, 15-minute expiry). The next non-final increment passes at
most 8 as ordinary Reader retry targets, frozen per checkpoint revision so a
yielded round replays the same request. Identifiers already retried, skipped or
captured in Store are left to the list. When the Reader also lists the
identifier, it captures its own row; the Gateway accepts that row for a marked
direct target, and a learned shape that no longer rebuilds it degrades the gate.
An unlisted identifier is fetched through the existing retained-original endpoint
and admitted in the same commit, timed by the original's `received_at`. Its
failure is not recorded as a mail failure and degrades the gate. Both paths use
the existing manifest verification, `PageCaptureInvocationID`, Store identity
and receipts. Only list rows advance the counter; a direct-only capture is
counted separately. The time-range sweep still runs in every round.

`SPARKCLAW_EMAIL_QUALIFIED_POLL_INTERVAL_SECONDS` (0, or 60–300) relaxes the
periodic sweep only while the switch is on, the mailbox is qualified and its
watch is `watching`. With the default 0, cadence and schedule keys are unchanged.
When it is configured, the gate's transition epoch is added to the schedule
command key, because command receipts replay and the earlier one-minute key
would otherwise be a no-op after a degradation. The gate is projected, without
identifiers, as `sync_health.push_target` on the existing provider check
response; the settings view does not render it.

Tests cover threshold counting and restart persistence, each reset path, Gmail
without identifiers, Outlook `ItemId` matches, frozen and bounded targets,
interval switching with distinct schedule keys, and end-to-end unlisted,
listed-and-direct and failed direct rounds. The Reader-validation case proves an
unmarked retry target still binds exactly.

**Conditions before enabling on a deployment.** (1) Deploy with the switch off.
After cutover, wait until the QQ gate reports `qualified` from at least three
natural arrivals, with `unmatched` 0. The two .34 matches are not carried into
the new state. (2) Turn on direct fetch only. Then require three further
natural arrivals where `direct_failed` stays 0 and the counter keeps advancing,
plus a Store check showing one mail per provider identifier. That is the
section 8 push-target gate. (3) Only then set the relaxed interval (300) for
QQ, and confirm that a forced degradation returns the schedule to one minute.
Outlook cannot qualify until its notifications carry the canonical identity and
its originals capture again; its own-send wakes would also reset the gate.
Gmail cannot qualify.

### 9.8 Leftover .31 mail issues: root causes and fixes (2026-10-22)

Branches: App-CLI `claude/mail-r31-leftovers` (`5210205`, `c013cfd`, `5e43ba5`)
and SparkClaw `claude/mail-r31-leftovers-a91ace`. Evidence came from source,
history, earlier private evidence and read-only work2 ledger, journal and
Gateway queries. No mail was sent and no work2 state changed. Every fix has a
regression case that fails on the old code. None is released. All App-CLI
changes and the Tampermonkey worker change need one paired release with a new
Tampermonkey product version.

| Issue | Root cause | Fix |
|---|---|---|
| Gmail normal-Inbox sync | **Not broken.** On work2, every normal-Inbox arrival since `.31` was captured automatically. Examples: 10-21 15:41; 10-22 04:03, 04:15 and 04:25, each discovered within 7–89 s. 1,245 of 1,246 `collect_page` rounds completed. The two `.30` messages that were "not accepted" went to Spam, which the Reader excludes by design. | None. One real gap remains, described below. |
| Attachment send | Outlook was only ever qualified with a 158-byte file. Its ready check required the card to read exactly `<bytes> 字节/bytes`. Outlook shows that only for files under 1 KiB; larger files get a rounded KB/MB label. So every larger attachment timed out with `email_attachment_upload_unverified`, and the draft was discarded. | `5e43ba5` accepts KB/MB/GB labels within their displayed precision. Byte identity is still proven by the SHA-256 manifest of the input's actual Files. |
| Outlook reply target | Since reads became network-only, `openNativeReplyTarget` only lists the item and never opens it on the send page. The editor proof still reads `globalThis[evidence_key].records`, a store the network Reader never creates. The reply could therefore never be linked. | `c013cfd`: see the reply list below. |
| Outlook account check timeout | The reply's sender check was the first evaluation on a freshly navigated inbox. It polled for up to 5 s inside one CLI `eval`. That eval's fixed 10 s budget also covers process start, two 500 ms settles and Playwright's wait of up to 5 s for requests the booting page issued meanwhile. Compose sends passed only because their Sent-baseline wait ran first. | `5210205`: one page turn per read, up to 12 reads or 12 s; then the existing account-menu fallback runs, and Escape is always pressed. |
| First Tampermonkey import | The worker's first managed read returns `{}`, and the policy appears later: 2.5 s, 6 s, and still absent after 51 s on `.34`. Upstream imports only at worker startup. Our patch only raised its retry count, so a late policy waited for the next worker start or a TM reload. The same loop also held every later start for about 50 s. | SparkClaw `scripts/browser_components.py`; see the Tampermonkey list below. |

How the Outlook reply now works (`c013cfd`):

- The send page opens the target conversation's own route.
- The early bridge now keeps the startup conversation IDs and counts, but no subjects or bodies. The Reader's new `replyTarget` uses them to prove that the item is the conversation's only local, non-draft message. Otherwise the send fails closed with `email_reply_target_unverified`. Multi-message conversations are not supported yet.
- The open step refuses when the route, or a selected list row, names another conversation.
- The editor must sit inside the pane whose Reply control was clicked.

How the Tampermonkey worker now waits for policy:

- It does not wait at all when the provisioning hash is already consumed.
- Otherwise it re-reads every 2 s, or at once when a top-level `storage.onChanged('managed')` event arrives. The wait is bounded at 240 s, longer than Chromium's 180 s after-startup failsafe.
- The listener also restarts a stopped worker when the policy arrives.
- `wait-profile` now allows 150 × 2 s.

Tests:

| Area | Result |
|---|---|
| App-CLI runtime | 550 of 553 passed and 2 skipped in a parallel run. The one failure is the known short-deadline executor case from 9.3. `executor` and `machine-session` pass 26/26 serially. |
| App-CLI Python | 90/90 |
| App-CLI public tree | pass |
| Controller | 294 passed, 137 skipped. The 11 new opt-in attachment cases pass against the fixed runtime, and the six accepted labels fail against vendored `.34`. |
| SparkClaw `test_browser*` | 64 passed, 1 skipped. The new Node harness runs upstream's own read text: a policy arriving at 60.5 s is lost by the old patch and imported at arrival by the new one. |
| Real pinned CRX | Still stages and passes `node --check`. |
| Go | `go test ./...` passes with `TMPDIR=/private/tmp`. Without it, two packages reject macOS's symlinked temporary directory. |

Live verification (not done here):

1. **Tampermonkey, no mail.** Install the paired version, then do one cold start with admission stopped. Pass means the import succeeds on the first start, with no TM reload. Record the worker's `managed policy available after N ms` line. A further restart must not wait.
2. **Outlook reply (real send).** Reply to a fresh single-message Inbox mail. Confirm:
   - the sender check passes without `browser_script_timeout`;
   - the route and editor proofs pass;
   - the reply arrives threaded.
   A multi-message conversation must fail with `email_reply_target_unverified` and leave no draft.
3. **Attachments (real sends).** About 100 KB and 5–10 MiB files on each provider. Check delivery, attachment hash and total time. Outlook's KB/MB label format is inferred and not yet observed; an `APP_CLI_MAIL_SEND_EVIDENCE=1` draft diagnostic can confirm it before any send.

Still open:

- **Gmail candidates carry no receipt time** (`network-reader.mjs` `candidate()`). The Gateway therefore never marks a Gmail arrival as found only by polling. The fix also needs the Gateway retry-target comparison (`collect_page.go`) to ignore Gmail receipt time. Those files are being changed by the parallel inbound session, so it is left for after that merge.
- The Gmail file input's placement in the composer is unverified; Gmail attachment sends have never run.
- After a failed upload, a chooser modal is deliberately left in place, so the draft discard can fail.
- Two opt-in attachment fixtures already fail on `.34` (`INPUT_VALIDATION_FAILED`, and a hook string that no longer exists).
- Gmail and QQ reply proofs were not changed. They depend on the target being open in the page, which the network lookup also does not do.

### 9.9 Section 8 control message and 10 MiB object gates (2026-10-22)

Tools: `scripts/mail-control-gate.mjs` and `scripts/iscp-object-gate.mjs`
(tests in `scripts/test/mail-gate-measurements.test.mjs`). Samples are
nearest-rank percentiles. No mail was sent, no release, configuration or
service changed, and nothing was restarted.

**Control message ≤ 20 ms on work2: met.** The control message is the
Lifecycle 3.0 `lookup` the Gateway outbox drain sends, here for three already
admitted sends (one per provider). It is posted to the Browser Controller
socket and passes the Controller, MailboxClient, the resident
`app_cli --machine --session` pool and the direct executor socket. `lookup`
only reads the ledger. Every sample counted only when the executor returned
`known` for that row.

| Run (work2, 09:22–09:24 UTC) | n | p50 | p95 | max |
| --- | ---: | ---: | ---: | ---: |
| QQ | 200 | 6.828 ms | 8.184 ms | 359.276 ms |
| Gmail | 200 | 6.895 ms | 7.971 ms | 9.581 ms |
| Outlook | 200 | 6.877 ms | 8.147 ms | 441.579 ms |
| All | 600 | 6.861 ms | 8.171 ms | 441.579 ms |

Time is client-side, from request start to the full JSON response, on one
keep-alive connection with 100 ms spacing and three warm-up requests per run.
Two of 600 samples exceeded 20 ms; their cause was not isolated. The four
resident session PIDs were the same before and after each run, so no process
was spawned per message. The Gateway's own lock and container hop are outside
this boundary.

**10 MiB object transfer ≤ 30 s: not met in the local lab; not measured
live.** The lab (`iscp-local-lab prepare-expansion`, Relay built from ISCP
`f603722`) runs the real desktop transport and Go helper, a reference Relay
container and the Gateway container on this Mac. Each sample uploads 10 MiB
of random bytes with purpose `file`, downloads them and verifies the digest,
then releases the object. The final run used unchanged code:

| Lab, unchanged code (5 samples, 537 s) | p50 | p95 |
| --- | ---: | ---: |
| Upload | 49.619 s | 51.292 s |
| Download | 56.914 s | 57.369 s |
| `capabilities.get`, idle (n = 30) | 51.148 ms | 62.530 ms |
| `capabilities.get` during a transfer (n = 843) | 199.794 ms | 333.490 ms |

Earlier runs on unchanged code gave 45.9–50.5 s up and 52.7–57.9 s down.
Other sessions' containers were loading the Docker VM during all lab runs.

The window was not the limit. Uploads kept 8 chunks in flight and downloads
4, yet each direction moved about 23–28 chunks per second. Joined desktop,
helper and Gateway traces show this:

- An idle RPC took about 12 ms each way through the Relay.
- Under load (first unchanged-code run), a chunk's request took p50 177 ms
  to reach the Gateway.
  The Gateway handler took 24.5 ms, under one store mutex with two fsync'd
  writes.

**Bottleneck: the helper's sealed Relay pending journal.** For every
submitted envelope, `iscpbridge/relay_pending.go` decrypts the whole journal,
re-encrypts it and rewrites it with file and directory sync. It does this three
times (before write, on the Relay receipt, on the waiter's acknowledgement),
under one journal mutex and an OS lock. The single socket writer waits for the
first rewrite, and the reader waits for the second. A 10 MiB transfer is 1,280
envelopes from the desktop, so every chunk pays this on the Mac. Go's sync on
macOS is a full fsync.

| fsync measurement | Result |
| --- | ---: |
| Env-gated journal qualification, 1,280 sequential 12 KB envelopes, Mac | 59.5 s |
| Same, window 8 (new `TestRelayJournalWindowedChunksQualification`), Mac | 33.7 s, 35.5 s |
| work2 ext4, 16 KB journal-style rewrite (file and directory fsync), n = 300 | p50 0.726 ms, p95 1.042 ms |
| work2 ext4, store chunk plus `state.json` rewrite, n = 1,280 | p50 1.883 ms, p95 2.573 ms |
| Mac → TEST1, ICMP RTT / warm HTTPS `/readyz` | 38.6 ms / 38.5–51.5 ms |

work2's disk is cheap. The Mac initiator's journal alone is close to the
whole lab transfer time. In the lab the Gateway's state is also on the Mac
disk, through a Docker bind mount, so the lab charges full fsyncs at both
ends. Live, the responder writes to work2 ext4 instead, but the desktop-side
cost and the 38 ms Relay RTT apply.

**Trial, reverted.** A group-commit journal (concurrent changes share one
sealed checkpoint) and writer-side batching of queued submits were tried
locally.

- Instrumented, one windowed run used 2,416 checkpoints for 3,840 changes.
  Persisting took 31.9 s of its 32.5 s.
- The windowed qualification ran in 21.3–47.5 s (noisy).
- The lab gave 36.0–39.9 s up and 45.9–59.9 s down (p50). That is still over
  30 s.
- Twice, after 2–3 samples, the transport dropped with "ISCP transport is
  unavailable". The 5-sample run on unchanged code did not drop.

The change was not kept. The journal stays as at `cd5bb79a`; the fix is a
separate task.

**Live 10 MiB was not measured.** The only live desktop is the user's running
SparkX. A second helper on its device identity would share its sealed
credential lease and Relay journal (with an OS lock) and its keychain key, so
it could interfere with the user's connection. Relaunching SparkX under the
tool needs the owner's approval. `iscp-object-gate.mjs --helper-config …
--allow-production` supports that run once approved.

From passive work2 Gateway traces of the live SparkX, the server-side
`capabilities.get` handler took p50 138.9 ms and p95 150.0 ms (n = 585),
against 2.6 ms in the lab. That cost was not investigated.

### 9.10 Release 35 deployment and live results (2026-10-22)

Shipped: App-CLI `d8b2a01` (release 35: function-based Outlook original
resolver, reusable preflighted QQ native send, Outlook reply/sender/attachment
fixes), SparkClaw `d85233fb` (pairing, Tampermonkey 5.5.0.18, resident page
transport, login-check reuse, gated notification-target fetch, default off),
and SparkX `aaf98ea9` on the Mac (`aaf98ea9` only adds WebP to the package UI
audit; the Spark X glass design ships WebP icons). work2 also serves the Spark X
glass WebChat from this release.

Cutover (work2, 10:47–10:54 UTC) became healthy on the first attempt. All 41
business tables, 2,583 workspace files and 77,785 receipt files were preserved;
the 93 historical unknown tasks were unchanged before and after every step.

| Item | Live result |
|---|---|
| Tampermonkey managed-policy import | **Not fixed.** Browser start 1 waited the full 300 s `wait-profile` without importing; start 2 imported in 6 s, the same pattern as .34. The start-1 journal carries no Tampermonkey worker output, so the cause is still open. |
| Resident page transport (send/mark-read pages) | **Regressed QQ sends; disabled on work2.** One page ran 36 commands in 1 CLI + 1 transport process (formerly 70–85 processes), but the faster commands reached the unchanged QQ `sent_baseline` click before the Sent folder rendered: `click` failed `process_exit_target_missing` (`browser_extension_unavailable`), and the send ended `email_send_not_dispatched` with `attempted:false`. The documented switch `SPARKCLAW_BROWSER_PAGE_TRANSPORT=off` is now set by the Controller drop-in `96-r35-page-transport-off.conf`. |
| QQ native send (release 35) | **Works.** With the transport off: DOM teaching send API 79.9 s (executor 62.1 s); next native send API 18.6 s (executor 6.4 s, request 566 ms, Sent proof 1.7 s); a third native send after more than 150 s (past the old 2-minute template age) API 19.1 s (executor 7.0 s, request 518 ms, Sent proof 2.2 s). Each had one confirmed Sent proof. |
| Login-check reuse (120 s) | **Not effective.** A QQ `probe` still ran before every send, including the second send 85 s after the first probe. |
| Outlook original capture | **Works.** All three QQ -> Outlook messages arrived inbound exactly once, with `capture_state=complete` and a parsed representation, within 21 s after the last send. |
| Outlook reply to a single-message conversation | **Fails safely.** `prepare_draft` ended `email_reply_editor_unverified`; `attempted:false`, `email_send_not_dispatched`, nothing sent. |
| Notification targets | Outlook matched by `item_id` 3/3 but as `non_canonical_match`, so the consecutive counter stays 0. No QQ inbound arrived during the run. Direct fetch stays off. |
| Attachments >= 1 KiB, 10 MiB transfer | Not run. Attachment sends start from the SparkX composer (local file, ISCP object, approval), not from the Gateway API. |

Each send ran once with a fresh idempotency key; there were no retries and no
reconcile or resolve calls. The temporary API clients were revoked (HTTP 401
afterwards) and their credential files removed.

Open items: a page-readiness fix for the QQ send flow before the transport can be
re-enabled; why the send's own page activity invalidates login reuse; the Outlook
reply editor check; and the first-start Tampermonkey import.

## 10. Cross-Project Impact

- App-CLI (SparkClaw-owned fork) receives N1–N3 runtime changes as a paired
  release; SparkClaw pins it through `configs/app-cli-release.json`.
- ISCP: N4 Relay drain is an internal performance change with no wire or
  contract change; it is sent to the ISCP inbox for review. Authorization reuse
  (4.9) changes the ISCP–SparkClaw contract and requires a decision first.
- No other project consumes the mail operations.
