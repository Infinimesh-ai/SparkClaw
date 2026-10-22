# Network-Native Mail Architecture (v4)

> Language: English | [简体中文](../zh-cn/docs/email-network-native-architecture.md)

- Status: **proposed design; N1–N3 and the SparkClaw/ISCP parts of N4 implemented locally, not deployed (section 9)**
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
  | QQ | notify envelope `type:'0'`, `mid` | `provider_message_id=mid`; selection = message; folder from the next list row when known |
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
5. **After the gate (planned).** For a provider whose candidates match reliably
   (section 8), the Gateway persists them as a pushed-target set (bounded,
   generation-bound) in the transaction that bumps `SignalRevision`, and the next
   increment passes them as exact retry targets, so they are captured even when
   time-range discovery would miss them. Gmail Spam stays excluded by the same
   folder policy as discovery.
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
| Change listener | WebSocket notify → **exact `mid` target** | multi-watch invalidation → **rows from own sync response** | `subscribeToRowNotifications` → **exact ItemId target** |
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
| N2 | Push targets: observer extraction, watch forwarding, feed field and Gateway match measurement; use as exact retry targets after the gate | App-CLI, SparkClaw | measurement implemented; capture use planned |
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

Nothing is pushed or deployed. Work2, TEST1 and the Mac keep their current
releases until the owner approves a rollout; live gates in section 8 remain open.

## 10. Cross-Project Impact

- App-CLI (SparkClaw-owned fork) receives N1–N3 runtime changes as a paired
  release; SparkClaw pins it through `configs/app-cli-release.json`.
- ISCP: N4 Relay drain is an internal performance change with no wire or
  contract change; it is sent to the ISCP inbox for review. Authorization reuse
  (4.9) changes the ISCP–SparkClaw contract and requires a decision first.
- No other project consumes the mail operations.
