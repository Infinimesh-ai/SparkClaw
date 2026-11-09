# Client-embedded Browser Controller over ISCP

> Language: English | [简体中文](../zh-cn/docs/desktop-embedded-browser-controller-iscp-design.md)
>
> Date: 2026-11-10. Status: implementation in progress. Clean-source `c6948132` candidates fix
> visual snapshot binding and hidden screenshot rendering; scoped Mac/work2 native checks pass.
> A shared-disk exhaustion incident interrupted work2 PostgreSQL; original service recovered,
> with the failed preservation audit and changed runtime baseline retained in §14.
> TEST1, the remaining acceptance matrix and paired cutover remain open; no production package replacement.
> Owner decisions D1–D3 are retained in §12.
> Source reviewed: `main` at `e031a453` (initial proposal); browser/ISCP/desktop implementation
> started at `b5c21e14`. This is SparkClaw-local work, with no project-cluster coordination.
> Scope: generic browser automation in SparkX through ISCP. Mail, provider scripts and AI chat
> export retain their current paths.

## 1. Decision

Move the generic Browser Controller and Playwright MCP into each SparkX package. Carry the
Gateway–Controller boundary over the existing authenticated ISCP device session:

```mermaid
flowchart LR
  G[Gateway browser tools] --> S[ScopedAdapter: trusted task scope]
  S --> H[ClientControllerHost + PlaywrightExtensionAdapter]
  H --> C[ISCPControllerClient]
  C <-->|browser.controller.*| F[SparkX main: forwarder + journal]
  F -->|private Unix socket| B[Packaged Controller]
  B --> M[Local Playwright MCP]
  M --> A[Local Electron adapter]
  A --> V[Task WebContentsView debugger]
```

Reuse the Controller execution contract, Playwright operations and Electron isolation core.
Explicit adaptations are required at the scope/presentation boundary, process assembly and
lifecycle; “reuse” does not mean these files can remain byte-for-byte unchanged. The backend
acquisition Controller and its Unix socket remain independent.

Each **Controller request** crosses ISCP once, excluding chunk retrieval. A public browser tool
can issue several Controller requests: opening, reading, snapshotting and stable-state waits
already do so. Measure the full tool latency rather than assuming one round trip per model tool.
CDP stays local: tunnelling it would add many serial round trips and expose a much broader remote
execution interface. No remote JavaScript, CDP, selector or Playwright-code API is added.

## 2. Source baseline and gaps

This table records the proposal's source baseline, not the current implementation status (§10/§14).

| Boundary | Proposal baseline fact | Required change |
|---|---|---|
| Backend acquisition | `AcquisitionAdapter` → `BackendHost` → `PlaywrightExtensionAdapter` → `browsercontrol.Service` → Unix-socket `HTTPControllerClient` | Keep this role and path |
| Client embedded | `Broker.ForScope` → `ScopedAdapter` → reverse `browser.command` → `BrowserHostAgent` → `native-page-commands.mjs` | Replace its executor and transport |
| Native executor | Eight page operations; at most 120 controls; DOM click; no equivalent frame/popup support; 39 KiB capture limit | Qualify the shared MCP semantics, without assuming all click behavior improves automatically |
| Scoped adapter | Reduces arguments, synthesizes one-page lists, handles focus locally, has frozen-target settle rules | Preserve the public contract while forwarding complete validated tool arguments (§4.3) |
| Controller | One active generic session; `scriptFactory=null` accepted, but `controller.mjs` imports/constructs `MailObserverFeed` | Separate optional assembly without changing backend mail behavior |
| MCP module | `mcp-client.mjs` statically imports AI login/export modules | Extract/inject optional handlers so the client package excludes them |
| HTTP assembly | `http-server.mjs` statically imports the App-CLI protocol and exposes application/mail routes | Share bounded Unix-server primitives, assemble only the four client routes without that import |
| Electron adapter | Started only for Linux qualification in `main.mjs`; its task connection has no workbench conversation binding | Production startup on both platforms and a trusted local binding map |
| Page release | Host path retains the conversation view; MCP cleanup closes owned tabs | Preserve the existing retained view on normal release (§5.4) |
| Recovery | `HostJournal` and Gateway fences persist IDs/digests/states, not results; `browser.receipt` reads Gateway fences | Reuse those stores, extend admission/state handling; do not assume result replay exists |
| Wire/package | 63,488-byte operation budget; 30 s RPC; 25 s desktop deadline; Electron pinned to 44.4.3; Controller package declares Node `>=26 <27` | Qualify the packaged Electron runtime (proposal baseline Node 24.21), bounded transfer and cold start |

The [client/backend R3 design](client-backend-architecture-design.md) owns role separation.
The [desktop design](desktop-client-embedded-browser-design.md) supplies historical rationale;
this document governs the new client control hop. Source locations are listed in §13.

## 3. Scope

Included: macOS ARM64/x64 and Linux ARM64/x64 packaging; production Electron adapter startup;
ISCP forwarder/client/service; the existing `browser.status`, `open`, `navigate`, `read`,
`snapshot`, `click`, `type`, `select`, `screenshot`, `wait`, `list_tabs`, `focus`, `close` tools;
client task-page presentation and recovery. The work2 colocated desktop is also a client (D3).

Excluded: `RunScript`, App-CLI, provider scripts, mail observers, QQ/Gmail/Outlook operations,
`OpenProviderLogin`, `ai_chat.export`, `ai_platform.check` and remote `tabs.handoff`. Existing
personal-page scripts/export features and backend mail acquisition stay on their current paths.
Exclusion from this package does not remove those existing product entries.

After P3, delete reverse `browser.command`, the native executor and LAN direct browser-host
transport (D2). LAN-only browser automation becomes unavailable; other LAN capabilities and
personal browsing remain as previously authorized. No fallback to work2's acquisition browser.

## 4. Gateway integration

### 4.1 `ISCPControllerClient`

Implement `browsercontrol.ControllerClient` for one exact ISCP session. `Acquire`, `Execute`
and `Release` map to their same-named reverse operations; the session service obtains readiness
through `browser.controller.status`. `Status` is not currently a `ControllerClient` method:
keep the probe on the new service/transport, without adding an unused backend interface method.
`RunScript`, `OpenProviderLogin`, `ValidateToken` return typed `browser_operation_unavailable`
locally. No Vault token crosses the network.

Extend `browserhost.ReverseSession` to name the operation; the underlying
`iscpworkbench.PeerSession.Command` already supports this. Every call is pinned to its original
session. A reconnect never retargets an in-flight command. Decode the same Controller result
schema after envelope and chunk validation, within the existing 10 MiB execution-response cap.

### 4.2 Session service and generations

One session-owned manager holds transport, readiness, leases and serialization; each task owns
its own Playwright adapter/snapshot state. Implement `PlaywrightController.Status/AcquireSession`
and `browsercontrol.Session` without the Vault-dependent `Service`. Avoid sharing a mutable
Playwright adapter across waiting tasks; that adapter currently rejects a different active scope.

The session credential generation is the positive, JS-safe `authorization_revision`; values
outside the representable range fail negotiation. Controller/session/page generations come from
the actual Controller result and are validated before advancing cached state. They are distinct
from the outer host `runtime_generation=iscp_<session ID>` and the desktop process/adapter
incarnation. A Controller restart invalidates its leases even when ISCP remains connected.

Keep the authenticated host-ID and authorization-digest derivation. An outer `Binding` is an
immutable ownership handle; Controller page generations and real page IDs live in its private
lease map. Never overwrite a Controller page generation with a host generation, or restore a
stale snapshot after reacquisition. Controller TTL is 10 minutes, matching the current adapter;
it does not renew device authority. Expiry invalidates the lease and refs. A later call may
acquire anew only after cleanup and unknown-write checks, with no replay of the old action.

### 4.3 Scope and tool semantics

`Broker.ForScope` resolves the authenticated installation to `ScopedAdapter(ClientControllerHost)`.
The scoped boundary continues to derive identity from the task, rejects foreign pages and checks
fences. Extend its internal typed forwarding path (the backend has an `incumbentCall` precedent)
so the client's Playwright adapter receives the original validated arguments. Do not send the
current lossy `ref/value` native-command projection into Playwright or trust model-supplied scope.

| Public behavior | Client implementation requirement |
|---|---|
| status | Local negotiated readiness plus a side-effect-free status probe; no page, subprocess or acquire created by reading status |
| open/navigate | Shared Playwright URL/tab rules; map only pages owned by this lease, including adopted task popups |
| read/snapshot | Keep read windows, snapshot identity, ref validation, auth evidence and generation checks |
| click/type/select | Keep shared argument semantics, including fill versus type/submit; journal each mutating Controller subcall |
| screenshot | Preserve image MIME and contents; use bounded transfer, without the native 39 KiB ceiling |
| wait | Keep frozen-target and bounded same-origin rebind rules; share settle logic and preserve all typed wait fields |
| list_tabs | No implicit acquisition when no task lease exists; thereafter project only task-owned pages, never connection/bootstrap pages |
| focus | Keep current client logical focus/explicit user presentation; never map to shared adapter `tabs.handoff` or change selected conversation |
| close | Preserve the current client close/release behavior; do not inherit the backend adapter's replacement-blank-tab workaround |

Workflow result presentation uses `browser.open` with `reason=workflow_result_presentation`.
In the client role this must observe/select the existing task page through `page.info`, retaining
its DOM and control state. It must not create a second tab or navigate the result URL again.
A missing page or changed route fails closed. Explicit new opens and backend acquisition keep
their existing tab semantics; this adaptation does not change the selected conversation or UI.

Snapshot projection preserves bounded rendered text, values, selected options and control states
already supported by the workflow consumer. Disabled controls are not offered as click actions;
truncated evidence is marked. Frame stability includes fresh owned-frame semantic evidence in
the same content digest used by snapshots. Opaque ref IDs and focus bookkeeping are excluded
from that digest. Pages without frames retain the existing read-only polling path and digest;
the quiet period, overall deadline and frozen-route checks remain unchanged.

The common host interface remains role-explicit. Readiness, release, lease-active checks and
fence ownership must stay callable after old transport deletion. “Unchanged UI/tool semantics”
is an acceptance requirement, not a claim that today's `ScopedAdapter` already supports this.

## 5. Desktop packaging, process and page ownership

### 5.1 Reproducible package

Build `apps/desktop/bin/browser-controller/` from a versioned client-only manifest/lockfile and an
explicit transitive source list. Pin the current MCP `0.0.80` and Playwright / playwright-core
`1.63.0-alpha-2026-08-31`; run the existing download/adapter patch during build and verify its
patched-file hashes. Install with `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`; exclude downloaded
browsers, `@playwright/cli`, App-CLI, provider scripts and userscripts. Do not run `npm ci` on the
backend manifest and prune it ad hoc.

Copy the tree outside asar to `resources/browser-controller`, and exclude `bin/browser-controller`
from both common and macOS asar file lists to avoid a second copy. Wire macOS package scripts and
Linux `package:desktop`, audit the unpacked tree, and record source revision, Electron/Node/MCP/
Playwright versions, patch hashes, file hashes and measured size in the release manifest. The
initial 20 MB figure is only an estimate. A broken manifest makes this capability unavailable.

### 5.2 Assembly and runtime qualification

Add `tools/browser-controller/src/client-main.mjs`: fixed profile `client_embedded`, MCP factory,
private Electron adapter and only health/acquire/execute/release HTTP routes. Extract optional
mail observer and AI handlers from core assembly; backend `main.mjs` injects its existing handlers,
client entry does not import them. Shared generic execution and backend contracts retain tests.
The existing HTTP server cannot be copied unchanged: its App-CLI decoder import must stay in
backend route assembly. Reuse bounded body parsing/socket helpers and install the four fixed
client routes. No second fork of Controller operations is introduced.

Use Electron's `process.execPath` with `ELECTRON_RUN_AS_NODE=1` for Controller and MCP, with a
minimal allowlisted environment, fixed entry/cwd/output paths and no inherited `NODE_OPTIONS`,
loader or browser/profile override. Build-time Node 26 compatibility does not qualify Electron's
Node runtime. P0 tests actual packaged macOS/Linux binaries, records `process.versions`, audits
used APIs and confirms that the app's signing/fuse configuration permits this launch mode.
If incompatible, stop this phase and record a revised packaging decision before implementation
continues; silently relying on a system Node or downloading it at runtime is not an option.

### 5.3 Process supervisor

State flow: `stopped → starting → ready/busy → stopping → stopped`; a start/integrity failure
reports unavailable. A lazy `stopped` process with verified files is available for acquisition;
status is not a successful business health check. First acquire coalesces a single start.
Create a random short private directory under a verified owner-only `/tmp/sparkx-<uid>` (0700),
reject symlinks/wrong owners and bind sockets as 0600 from creation. Check encoded path length
against the macOS limit. Tokens and adapter secret rotate at each start and stay in main/private
child configuration, outside renderer, remote pages, Gateway and logs.

Start the adapter on both production platforms. Qualification methods remain unavailable in
production. On task/session shutdown: stop admission, cancel queued work, revoke registrations
and debugger access, release the active Controller session, await MCP/Controller exit with a
bounded grace period and terminate remaining owned children, then remove runtime files. Socket
closure alone does not prove cleanup. Admission remains blocked while cleanup is unproved.
Controller/MCP crash invalidates its incarnation and in-flight writes; restart occurs on a later
acquire. ISCP loss, authorization change, lock or revocation invalidates the corresponding leases;
normal authority recovery does not resurrect them. Personal pages/profile data are not deleted.
Late request failure cleanup is bound to its original authority generation and Controller
incarnation. It must settle the old write journal without stopping a replacement runtime.

### 5.4 Conversation ownership and retained views

Before forwarding acquire, main registers a one-use pending association from the local Controller
task token to `(owner, client, installation, local conversation, local task, host lease, ISCP
session, supervisor incarnation)`. The token is an opaque ID within Controller's 128-character
limit; it is not a truncated user ID. Adapter `registerConnection/openConnection` must consume
that association and bind the returned Controller session and local connection ID. A missing,
duplicate or stale association fails closed. Neither remote page input nor a model argument can
register a conversation. Revoke pending associations on timeout and cancellation.

Extend `PageRegistry` with this main-owned association. Task-created popups/frames inherit the
same connection allowlist; all real pages map to opaque public task page IDs. Bootstrap pages
stay hidden. Render through the existing `conversationPages`, page projection and presentation
hooks. Creating a background task page never selects its conversation or raises the window.

Normal task release retains the current conversation view, as `releaseHostPage` does today, while
removing all remote authority: revoke credentials, detach debugger/facade and drop lease/ref maps.
Implement explicit local begin-release/retain and acquire/adopt hooks; MCP's existing
`closeTaskPage` must not accidentally destroy the retained view. Cleanup of other owned task tabs
and connection pages still completes. A later lease can adopt the retained view only for the same
conversation after a fresh binding, and obtains fresh snapshot refs. Explicit close still closes
the requested task view. Crash, revoke and session-loss cleanup retain their existing protected
page behavior; they never turn a task page into a controllable personal page. Native tests must
exercise normal completion, explicit close and abnormal teardown separately.

### 5.5 Forwarder responsibility

Replace the reverse whitelist with exact registered operation names, never a wildcard prefix.
The forwarder validates schema, scope, digest, deadline and live authority, enters the journal
and maps to fixed local routes. Reject caller-controlled transport paths/hosts, tokens, scripts,
CDP and unknown keys. Navigation URLs remain allowed in typed `page.navigate` / `tabs.new`
arguments under the existing URL policy; “no arbitrary URL” refers to transport destinations.

Serialize Controller state-changing calls per lease; chunk reads/status do not enter that slot.
Move journal, fence refresh and reconcile ownership out of `host-agent.mjs` before deleting it.
Keep their existing IPC/UI projections. Do not couple release/reconciliation to acquire queue
capacity or require a working Controller to read an existing fence.

## 6. Wire contract, budgets and recovery

### 6.1 Request and response schema

New fields below are design contracts, not already shipped APIs. Each body uses `schema_version: 1`.
Task calls carry `command_id`, `digest`, `binding` (the full existing `browserhost.Binding`),
`timeout_ms` and the operation-specific fields below. `timeout_ms` is a positive budget at most
25,000; the caller's deadline can shorten it. `status` uses the authenticated installation/session
and no task binding. `chunk` uses that session plus the original binding and command/result IDs;
it cannot re-execute anything.

| Reverse operation | Additional input | Successful body payload |
|---|---|---|
| status | None | protocol `browser.controller.v1`, available/process state, supervisor incarnation, Controller generation when started, pinned versions and limits |
| acquire | `wait_timeout_ms` (0–30,000, further clamped to remaining budget), `session_ttl_ms` (1–600,000) | Controller `SessionLease`; forwarder fixes profile/lane/token/task token/credential generation |
| execute | `lease` (Controller lease), `operation`, `arguments` | Controller `ExecutionResult`, inline or via result descriptor |
| release | `lease` | Controller `ReleaseResult`; release identity is taken from the validated lease map |
| chunk | `command_id`, `result_id`, `offset`, `length` | matching IDs, offset, total bytes, base64 bytes; no mutable cursor |

Task replies echo `command_id`, `digest`, `binding` and supervisor incarnation, and contain
`outcome: not_dispatched | completed | failed | unknown`, optional typed `error_code`, and exactly
one of inline `payload` or `result` descriptor on success. The descriptor is
`{result_id, total_bytes, sha256, expires_at}` for immutable UTF-8 JSON bytes of the Controller
response. Missing/mismatched identity or malformed payload is never success. A chunk request
checks the retained result's scope, not whether its old page generation is still current.

Bodies reject unknown keys. `arguments` follows the shared MCP operation schema, at most 32 KiB;
the injected local Controller request must fit its 48 KiB limit and the remote message its
negotiated limit. Enforce both before journaling/dispatch. Public IDs retain their existing
bounds; only generated local Controller tokens use its narrower ID grammar. Tokens, profile,
lane and transport route are never accepted as free caller configuration.

Digest is SHA-256 of canonical `{operation: <outer ISCP operation>, body: <body with digest="">}`,
using the existing recursively sorted JSON/Go-compatible escaping rules. It covers binding and
budget as well as the inner execute operation. Add shared Go/JS vectors for Unicode, escaping,
arrays and safe integers. RPC IDs are transport-only. Each mutating Controller subcall has its own
stable command ID; no retry layer may replace that ID to bypass a fence. Reject conflicting
same-ID input before execution. Reserve a per-ID slot atomically before awaiting journal I/O;
`HostJournal`'s serialized file writes alone do not prevent concurrent duplicate admission.

### 6.2 Deadlines and queuing

| Budget | Rule |
|---|---|
| RPC | At most 30 s and bounded by caller context; no reset on reconnect |
| Desktop call | At most 25 s from receipt, including queue/start/execute/serialization |
| Navigation / action / explicit wait | Caps of 20 s / 10 s / 15 s, additionally clamped to remaining call time |
| Acquire | FIFO at desktop, at most 32 waiting requests per client; wait includes cold start; local Controller gets `wait_timeout_ms=0` after the slot is granted |
| Result assembly | At most 30 s from descriptor, also within the original caller's remaining deadline |
| Cleanup | Separate bounded cleanup budget (5 s grace before forced child termination); cleanup never permits continued remote input |

Queue expiry/cancellation removes the entry before it can acquire. Recheck task, authority,
incarnation and remaining time immediately before dispatch. The original local cancel action
atomically persists `cancel_pending` and a content-free `execution_cancel:<local-task-id>`
marker in existing ClientStore metadata, then synchronously signals the exact owner/client/installation/
conversation/task before requesting Gateway cancellation. Abort matching queued/in-flight calls;
stop an active matching lease, and grant the next FIFO slot only after runtime cleanup settles.
Retain canceled task keys for the authority generation (maximum 1,024; exhaustion disables
Controller admission until a new authority session). Every subsequent dispatch also checks the
scoped durable marker, so a running server projection, reconnect or main restart cannot erase
the user's local stop intent. Wire operations and database schema version stay unchanged.
A Gateway cancellation may still yield an unknown fence if it stopped waiting
before the not-dispatched reply; local cancellation never clears that authoritative fence.

Reserve cleanup/response headroom;
expiry cannot merely stop waiting on a still-running child. Late acquires are released and fenced,
not handed to a replacement session. Preserve the request deadline through acquisition: today's
Playwright adapter uses `context.WithoutCancel`; separate acquisition context from the longer-lived
session context in the client integration so it cannot discard this budget. One active task per
client remains D1; queue-full/wait expiry returns the existing `browser_busy` before dispatch.
On a dispatched call's deadline, cancel local work and revoke that lease's adapter access; if the
child cannot stop the operation, terminate its owned MCP execution before admitting another task.
Returning a timeout alone is insufficient. This cleanup does not close unrelated healthy ISCP calls.

### 6.3 Large results

The negotiated operation limit is at most 63,488 bytes. Measure the **entire encoded wire message**,
including wrappers/escaping; a 48 KiB inline payload is only a preliminary threshold. Larger
results use the descriptor. Accept at most **10 MiB per decoded Controller response**, including
JSON/base64 overhead; reserve **20 MiB total retained result bytes per desktop**, not the original
proposal's contradictory 8 MiB total. Reserve capacity before dispatch, with bounded streaming
response parsing and allocation accounting; no eviction of live results to admit a new write.
If unavailable, reject before dispatch with typed capacity failure. A response beyond 10 MiB
fails explicitly, never truncates or silently changes format.
Each execute reserves the full 10 MiB worst case. A retained maximum-size result plus earlier
live results can therefore prevent the next execute even before the 20 MiB cache is full.
Qualify maximum-size transfer separately from workflow success under that capacity pressure;
do not evict live bytes, extend deadlines or count a delivered blocked answer as task success.
The large-result reservation applies to execute. Status/acquire/release use bounded small
responses within the wire budget and no blob reservation; a full result cache must not prevent
release or read-only receipt/reconciliation. Temporary parse/base64 buffers also have bounded
allocation, with one execute and at most four chunks in flight.

Chunks carry at most **32 KiB raw bytes** (base64 ≈43 KiB); reduce to fit any smaller negotiated
limit and check the final envelope. At most four requests run concurrently, further limited by
negotiated capacity. Validate integer ranges, bounds, IDs, exact chunk lengths, final size and
SHA-256. Repeated reads of the same offset return identical bytes. Reject foreign-session IDs.
The whole result is parsed only after verification, never published as partial success.

Retain immutable bytes for 60 s from creation or until ISCP session ends/revocation; repeated
reads do not extend TTL. **Do not delete on “last chunk read”**: a lost final reply must remain
retryable. No extra result ACK operation is required. Expired results return typed unavailable;
reacquiring a result cannot repeat the original write. Separate action outcome from payload
availability: persist a verified completion receipt before fetching chunks; payload loss/hash
failure then reports result unavailable without claiming the action failed or replaying it.
MCP's existing 8 MiB output budget is a separate inner limit. P0 measures worst-case transfer
within both TTL and caller budget; format changes such as JPEG require an explicit contract
revision and image-quality acceptance, not an automatic latency workaround.

### 6.4 Durable outcome and reconciliation

Classify acquire/release and `page.click/fill/navigate/select/type`, `tabs.new/close` as mutations.
`page.info/read/snapshot/screenshot/wait` and `tabs.list` are read-only at this boundary. A public
read/snapshot that first navigates therefore still contains a separately journaled mutation.
Keep this classification in one shared operation contract, tested against Go and JS consumers.

| Observation | Durable handling |
|---|---|
| Proven refusal before local Controller dispatch | `not_dispatched`; no business effect claimed; retry only with current authority and no existing fence |
| Dispatch may have happened; response lost, malformed, cancelled or timed out | Gateway fence + desktop journal remain `unknown`; stop further mutations in affected task scope |
| Valid completed response | Persist `completed` before responding; release mutation slot; output transfer can still fail independently |
| Error after dispatch | `failed` only when structured evidence proves no effect; otherwise `unknown`, even for HTTP 4xx/5xx or a retryable Controller code |
| Duplicate ID/digest | Return stored state/live cached result when available; never re-execute; completed result missing is explicit unavailability |
| Restart with `dispatched` row | Load as `unknown`; preserve original ID/digest and installation scope |
| Owner reconciliation | Existing `observed_completed` / `observed_not_applied` action with matching digest; no automatic action replay |

Persist the Gateway fence before sending and `HostJournal.begin` before local dispatch; disk/full
capacity failure blocks admission. A journal finalization failure cannot yield success. Keep the
current content-free journal format and legacy rows, with no TTL deletion of deduplication
history. Empty receipt lists, 404, cache expiry and successful socket writes prove no outcome.
Generic status does not resolve a specific command. `browser.receipt` remains a **forward** query
of Gateway fences, not a reverse desktop result lookup. Reconciliation remains idempotent and
must settle both existing local/remote records before reopening writes. Manual reconciliation
uses existing owner-visible pages/personal browsing under current authority. If automated
observation needs a new acquire or navigation mutation, it stays blocked until the owner resolves
the fence; do not introduce a recovery flag that bypasses it or revives the old write lease.

## 7. Registry and negotiation

Use `services/gateway/internal/iscpworkbench/operations.json` as the registry and
`scripts/sync-iscp-operations.mjs` for projections. All new entries are v2, `browser.control`,
`business`, limit 63,488, no business HTTP mapping:

| Operation | Direction | `mutation` | Recovery |
|---|---|---|---|
| browser.controller.status | reverse | false | read |
| browser.controller.acquire | reverse | true | operation_receipt |
| browser.controller.execute | reverse | true | operation_receipt |
| browser.controller.release | reverse | true | operation_receipt |
| browser.controller.chunk | reverse | false | read |

`mutation` is a boolean in today's registry: execute must conservatively be `true`, not “per
operation”. The inner operation table controls read retries and write journaling. Registry
`operation_receipt` is a policy label, not proof of generic reverse durable replay; §6.4 provides
that behavior. Keep `browser.receipt` (forward, `browser.control`) and `browser.reconcile`
(forward, existing `browser.read` scope); do not silently widen or rename their scopes.

`browser.controller.v1` is the protocol value returned by status, not an invented ISCP operation
name or new Relay feature. Require all five registered operations in the authenticated negotiated
operation set and the expected status schema/limits. Update Gateway qualification/capability
projection and desktop surface dependencies together. Keep `capabilities.browser` and the Gateway
browser family as gates; probe availability on connection/use, not with a new permanent poll.
Bootstrap status is authorized by the negotiated session/operation scope, before enabling the
browser UI capability; it cannot depend on a process-ready browser flag that it must itself establish.
An old/mixed peer fails closed for browser automation while unrelated capabilities keep working.
Malformed app requests receive a typed rejection without destroying an otherwise healthy session.

The application negotiation offer explicitly opts in with `browser_controller: true`. A new
responder omits all five Controller operations when the peer does not opt in. A new initiator
retries only the read-only negotiation after an old strict responder rejects the extra field,
dropping this extension before settings, progress or concurrency features. Accept only complete
current or preceding operation tables, including the existing extension combinations; partial
Controller tables and qualified-but-unadvertised Controller operations remain invalid. Both
request admission directions reject Controller operations absent from that negotiated table.
The domain capability projection receives the actual peer's negotiated operations and disables
browser for old clients while retaining independent surfaces. This adds no Relay feature.

## 8. Security and traffic boundary

Authorization is the existing device session; no new device grant, heartbeat, remote debugging
port or transport bypass. Before **every** queued dispatch/chunk, check owner/client/installation,
exact ISCP session, authorization revision, task/lease and relevant incarnation. Tokens, local
socket paths and connection credentials never appear in the remote schema or renderer IPC.

The Electron adapter restricts debugger targets to owned task pages/frames/popups and blocks
personal/workbench/foreign-task targets and global cookie access. Local audited MCP internals
may evaluate fixed page-reading logic; the caller cannot provide code through generic arguments.
Pin source/import closure and executable paths, reject runtime loaders and package escapes;
file hashes establish package integrity, not a new OS sandbox.

“Zero direct business HTTP/WS” means no Gateway-to-client control path or client-to-Gateway
business bypass outside ISCP. Local Controller HTTP over UDS, local Playwright extension loopback
WS and normal browser website HTTPS are expected. Acceptance records destinations and process
owners so those legitimate flows are not mistaken for bypasses. Blocking ISCP must make remote
control unavailable while personal browsing can continue under its existing policy.

## 9. UI preservation

Preserve original components, menus, layout, styles and interactions. Adapt underneath existing
browser panel/task tabs, explicit conversation selection, read-only task view, hide/widen controls
and reconcile actions. Preserve the completed conversation view and macOS close/Dock reopen
behavior. Workbench navigation/crash hides native views until a fresh explicit selection/bounds;
it must not grant a new Controller access to an old page.

Unsupported/temporarily unavailable automation keeps its original entry disabled. Busy tasks use
existing task activity presentation; no permanent technical status, transport chooser or new
simplified page. Failure/recovery text appears only when user action is required. Validate original
UI behavior as well as the new transport; functional tests alone do not establish preservation.
Remote permission alone cannot enable the browser surface: main also requires a verified local
Controller bundle. Start with that local gate closed, reproject the existing connection when
verification changes, and leave independently qualified files/mail/settings surfaces unchanged.
Failed re-verification clears cached bundle proof; subsequent cleanup cannot restore that proof.

## 10. Implementation phases and cutover

| Phase | Work and exit evidence | Current status |
|---|---|---|
| P0 Feasibility | Actual packaged Electron Node/API/fuse checks; Mac adapter; generic MCP without optional imports; fixtures for frame/popup/ref/input/capture; cold-start and full-tool/chunk timings on Mac ↔ work2 | Source and Mac/Linux packaged runtime passed; full remote timing gate open |
| P1 Local runtime | Reproducible manifest, client entry, process supervision, association map, retained-view lifecycle; installed package runs local fixtures offline | Source lifecycle and isolated Mac/work2 x64 packages passed; installed/offline matrix remains |
| P2 ISCP | Registry/projections, service/client/host, strict forwarder, journal/fence integration, chunks; isolated Relay failure-injection and no business bypass | Integrated; fault recovery gate open |
| P3 Native acceptance | Installed Mac and work2 Linux desktop through TEST1 to real work2; G1–G4 of the [ISCP expansion design](desktop-iscp-capability-expansion-design.md), §11 matrix, latency versus recorded old-path baseline | Pending |
| P4 Cutover | Paired Gateway + Mac/Linux packages, old-path deletion, verified rollback and retained-data checks | Pending |

Capture the old-path performance/UI baseline before deletion. P0–P3 may use isolated candidate
builds; production has no dual-path runtime selector. The owner requested continued implementation
and final owner acceptance. Candidate preparation is active; production rollout follows the
owner's authorization at execution time.

P4 removes active `browser.command` registry/generated entries, `Broker` reverse dispatch,
desktop handler/native executor/`host-socket.mjs`, LAN host grant/connect/revoke/fences routes
and their dedicated grant/heartbeat machinery. **Keep** broker scope/receipt/fence services,
`HostJournal`, workbench IPC/reconcile UI, backend `BackendHost` and shared page/isolation helpers.
Move surviving helpers first; replace obsolete tests with new ownership/recovery coverage.
Delete LAN HTTP endpoints only after forward ISCP receipt/reconcile no longer depends on their
handlers. Active runtime/build references must be gone; historical docs/evidence need not be erased.

Rollout: stop new browser admission, drain active tasks, preserve unresolved fences and original
ClientStore/profile/installation binding, install matched artifacts, then qualify capability before
enabling it. Rollback: stop admission and children, restore the entire prior Gateway/client/helper
set and compatible receipt data, then verify original connection/UI. An old binary must not
reinterpret new unresolved writes as retryable; if compatibility cannot be proved, browser stays
disabled while a forward repair/reconciliation is completed. No data reset, new test identity or
per-command fallback substitutes for rollback.

## 11. Acceptance matrix and evidence

Every row requires complete acceptance evidence; §14 records partial source/fixture coverage. Record source/package hashes, architecture,
Electron/Node versions, fixture/provider, exact request IDs (redacted), expected/actual outcomes,
latencies and failure artifacts. Source tests, native fixture, installed package and live provider
are separate evidence scopes. Report failures and skips; do not turn an unavailable platform
into a pass.

| ID | Scenario | Required observation |
|---|---|---|
| A01 | Old/new peer and missing capability; LAN-only; Controller absent | Unavailable automation entry retained/disabled, typed refusal, no backend fallback; LAN personal browsing stays authorized (§3), other features unaffected |
| A02 | Open/read/snapshot/fill/type/click/select/wait/capture | Same public schemas and actual page effects; stale ref rejection; redirects/frozen-target rules preserved |
| A03 | Iframe, cross-origin frame, popup, dynamic controls | Owned target scope, correct refs/effects, no personal/foreign target enumeration; unsupported cases explicitly block release |
| A04 | Two tasks/conversations, queue full/expiry/cancel, cold start | One active Controller session; no expired request starts later; no conversation switch; bounded queue and cleanup |
| A05 | Duplicate concurrent ID, changed digest, response loss, final journal failure | At most one execution; durable state/fence, no replay; fresh ID cannot bypass unresolved task fence |
| A06 | Controller/MCP crash, ISCP reconnect, Gateway/desktop restart, revoke/lock | Old generations rejected; pending writes unknown; no late admission or orphan process; personal data preserved |
| A07 | Inline boundary, escaped JSON, >39 KiB screenshot, 10 MiB cap, cache full/expiry | Final envelopes fit, correct size/hash/MIME; capacity refusal before writes; no silent truncation |
| A08 | Lost/duplicate/out-of-order final chunks, forged scope/offset/hash | Stable retryable bytes until expiry; reject corruption/foreign access; result loss never repeats completed write |
| A09 | Normal completion versus explicit close and abnormal teardown | Retained conversation view on completion, fresh refs on adoption, explicit close honored, old debugger credentials unusable |
| A10 | Original panel/tabs/select/hide/widen/reconcile; renderer crash; Mac Dock reopen | Original visuals, geometry and native interaction preserved; no permanent status additions |
| A11 | Installed offline package and process exit | No system Node/download dependency; all owned children/socket files cleaned; manifest/permission failures fail closed |
| A12 | Real work2 over TEST1, Mac and colocated Linux; blocked Relay | Correct device executes; no direct business HTTP/WS fallback; backend mail path and personal scripts remain operational |
| A13 | Paired upgrade/rollback with pending fences | Same installation/profile/data; old/new writes cannot escape reconciliation; obsolete active source references gone |

Run focused Go `browsercontrol/browserhost/browserautomation/iscpworkbench/gateway`, Controller
Node and desktop transport/journal/registry/presentation tests, registry projection check, package
audit and bilingual docs checks as their code changes land. Add backend mail/AI-handler regressions
for optional assembly extraction. Full repository checks gate implementation integration; this
initial design revision established no native test result; current implementation evidence is scoped in §14.
P0 timing records include sample count, cold/warm starts, full tool subcall count, payload sizes,
median/tail latency and chunk throughput; a single successful RPC is not an end-to-end benchmark.

## 12. Recorded owner decisions

Recorded on 2026-11-09; this revision does not add new owner approvals.

| ID | Decision | Consequence |
|---|---|---|
| D1 Concurrency | One active browser task per client; others queue at acquire | Preserve Controller's single-session model; bounded desktop admission and existing busy failure (§6.2) |
| D2 Old path | Delete after P3 | Remove reverse `browser.command`, native executor and LAN direct host as one cutover; retain journal/receipt/reconcile and existing UI (§10) |
| D3 Linux client | Work2 colocated desktop also uses packaged Controller through ISCP | Backend systemd Controller only serves acquisition; qualify and release the Linux client package alongside Mac |

P0 uncertainties are validation gates, not missing product decisions: packaged Electron runtime
compatibility, import closure, task-view retention and real transfer/whole-tool performance. A
failed gate requires a documented technical revision; it cannot be waived by marking the design
or a unit test complete.

## 13. Implementation ownership and source map

Paths are repository-relative. The Controller client, host, supervisor and forwarder below are now present in source; packaging and native acceptance remain separate gates.

| Area | Source ownership |
|---|---|
| Client contract | `services/gateway/internal/browsercontrol/client.go`, `runtime_session.go`; `iscp_client.go` and `iscp_session.go` |
| Scope, generations, fences | `services/gateway/internal/browserhost/adapter.go`, `reverse.go`, `fences.go`; `client_controller_host.go` |
| Shared tool semantics | `services/gateway/internal/browserautomation/playwright_extension_{adapter,session,snapshot,settle}.go` |
| Transport integration | `services/gateway/internal/gateway/workbench_iscp_browser.go`, `internal/iscpworkbench/{reverse_session.go,operations.json,negotiation.go}` |
| Main ownership | `apps/desktop/src/main/{main,iscp-transport,iscp-capabilities}.mjs`; `browser/{controller-runtime,controller-forwarder}.mjs` |
| Page/journal lifecycle | `apps/desktop/src/browser/{adapter-server,page-registry,host-agent,host-journal}.mjs` |
| Controller assembly | `tools/browser-controller/src/{controller,mcp-client,http-server,electron-adapter-client}.mjs`; `client-main.mjs` and client package manifest/lockfile |
| Packaging | `apps/desktop/package.json`, `apps/desktop/scripts/{package-macos,audit-package}.mjs`, root desktop build scripts |

Dependencies are P0 → local assembly/lifecycle → transport/receipts → native acceptance → deletion.
Keep mechanical optional-handler extraction separate from transport/behavior changes, and keep
backend acquisition tests green throughout.

## 14. Implementation checkpoint — 2026-11-10

The generic Controller/MCP core and Unix-server primitives are now shared by explicit backend
and client assemblies. The backend still injects mail and AI handlers. The client lockfile pins
only MCP, Playwright and Playwright Core. `build:client-controller` builds the private client
payload; Mac/Linux package entry points include it outside ASAR, and `afterPack` checks its
inventory, executable bits, pinned versions and manifest hash. No installer is released yet.

`ClientControllerRuntime` verifies files before lazy startup, uses Electron as Node with a fixed
environment, checks private short socket paths, and owns a separate process group with bounded
shutdown. The production-mode adapter accepts only a one-use main-issued task association.
PageRegistry supports explicit normal-release retention, fresh-lease adoption and logical tab
selection without foreground handoff. These components are exercised by the native fixture;
that initial checkpoint preceded the `main.mjs` and Gateway integration described below.

Local Mac ARM64 evidence uses Electron 44.4.3 / Node 24.21.0 / Chromium 152.0.7977.130 with a
disposable profile and `/usr/bin:/bin` child PATH. All 12 native checks pass: read/fill/click/select/
snapshot/capture; renderer sandbox; no system Node; iframe refs/effects; owned popup; normal
retention; fresh adoption; explicit close; crash revocation and child cleanup; new incarnation;
and clean stop. This is one source-runtime scenario, not installed-package or latency acceptance.

Initial failed attempts remain diagnostic evidence: `f1e2` frame refs were rejected by both JS
and Go parsers; a long macOS temporary path was rejected; an inactive popup displaced the retained
view; acquire navigated the adopted view to `about:blank`. The fixes preserve frame namespaces,
use the specified short path, track logical selection without activating a window, and omit
initial navigation only in the client assembly. Backend task-page creation remains unchanged.

Validation: Controller 358 passed / 151 skipped; desktop 632 passed / 1 skipped; focused shared
MCP, relay, association, package, runtime and page-lifecycle checks pass. Go browserautomation
passes with child-frame coverage. These were the initial local-assembly counts, before the transport integration below.

The source path now uses `ClientControllerHost` and the shared typed Playwright adapter through
`ISCPControllerClient`, the five registered `browser.controller.*` operations and the desktop
forwarder. Production main uses the packaged runtime lazily. Readiness does not acquire a page;
legacy peers cannot fall back to a LAN host. The old command/native implementation remains only
until the P3 deletion gate. Backend acquisition/mail assembly and original UI components remain.

The wire validates full trusted scope, generation, incarnation and canonical digest. Durable
mutation fences precede dispatch; queueing and execution share a deadline. Result bytes have
10 MiB/20 MiB execution/cache limits and a reservation before mutation, immutable chunks and
bounded download concurrency. Missing output after a completed receipt stays completed and is
not replayed. Inline retries share the same bounded byte cache. A failed terminal journal write
retains a visible unknown fence. Release remains possible behind that fence.

Normal application Quit now waits for owned cleanup; Electron does not await an asynchronous
`before-quit` callback by itself. A private parent-liveness pipe also ends Controller/MCP when
the desktop main process dies. Source-native tests pass both normal Quit and forced main-process
death; one forced-death sample observed process-group disappearance in about 52 ms. This is not
a latency percentile. The local forwarder fixture now passes 14 checks, including native large
screenshot chunk reconstruction/hash and final-chunk reread.

A disposable Docker Relay/Gateway and the real desktop main entry complete opening and form
filling through encrypted ISCP with the mock model. The original per-field approval gates the
single fill; DOM value and displayed text both become `Alice`. Release retains the view without
a debugger, sandbox stays enabled, and normal Quit stops the Controller. A separate large-page
run performs 84 chunk reads successfully. Direct business HTTP attempts remain zero. The fixture
retains its original installation and earlier unresolved fence; successful runs preserve that
fence exactly and add no unknown writes. These results qualify transport/native effects, not
real-model quality, installed packages, remote TEST1/work2 or the full original-UI matrix.

The retained failing attempts exposed two independent mock/workflow defects: obsolete runtime
fields in model-visible arguments, and treating unchanged visible draft content as proof of a
completed goal before any field action. Mock arguments now follow the current short-ref schema;
form drafts always assess visible goals before selecting or completing field actions. Regression
tests retain the existing approval and no-submit boundary. A fresh fixture profile also failed to reach the workbench; the test continues with its original identity and preserves old fences.

Current automated evidence: Linux Go 6,270 test nodes passed / 216 skipped, build and vet passed;
focused browser packages passed with the race detector; desktop 649 passed / 1 skipped; WebChat
596 passed and build passed; Controller 358 passed / 151 skipped. The Controller rerun uses a
canonical private temp directory; WebChat uses Node 26 with native Web Storage disabled so jsdom
owns `localStorage`. Initial environment failures remain recorded. Remaining gates include the
complete fault matrix, Mac/Linux packaged and original-UI acceptance, TEST1/work2 measurements,
paired rollback preservation and P3-gated deletion of the old path.

The first 31-second delayed-write-reply injection failed: a late cancel of a completed long
progress stream was checked against the new-open time window and disconnected the session.
Completed stream identities now retain their original opening horizon for that authenticated
session; exact late cancels are harmless, while changed identities and unknown expired cancels
remain invalid. Full `iscpworkbench` race tests pass. The corrected native rerun passes 11 checks:
one real fill, task `unknown`, no replay, unrelated encrypted read succeeds, and original IPC
reconciles only the injected command after its effect is observed. Earlier unresolved fences
remain exact. Initial fixture failures (waiting for delivered instead of unknown, and comparing
unordered fences as an ordered array) remain recorded. This closes the observed delay defect;
the rest of the fault matrix and packaged/remote acceptance gates remain separate.

Mac candidate packaging caught and fixed a builder omission: a separate resource fileset is
required for the private Controller's root `node_modules`. A regression uses the actual builder
copy filters, and both Mac and Linux audits verify all 207 files / 18,734,326 bytes. Linux ARM64
AppImage/deb artifacts build; its packaged Electron runs offline in a Linux container with the
expected Node/Chromium versions, SQLite, fetch and WebSocket. This does not qualify its GUI.

The Mac packaged candidate passes six native checks through a separate disposable encrypted
Relay: actual packaged main/resources, live packaged Controller/MCP, one approved form fill,
retained sandboxed view, baseline fences, original panel/tab/hide/reopen/widen/restore, and Quit
cleanup (grouped checks). Workbench selection after reload uses the original sidebar. Native AX
and visual inspection show the filled `Alice` task page under the original read-only panel.
Initial harness failures are retained: developer-profile credential unlock, choosing the shield
target as the first window, expecting MCP to survive normal release, missing the sidebar refresh,
looking for titlebar-portalled controls inside the panel, and an expired observation pause.

Native inspection also exposed a real selection race: a pending personal blank could override
a later explicit task selection and hide it. The existing component now rejects that stale
selection callback while retaining the created tab. The new regression fails old source and
passes the fix; all 596 WebChat tests pass. Work2 read-only preflight confirms x86_64 with an
active desktop session; a matched x64 packaging entry and per-architecture manifests were added.
Remote candidate qualification is continuing; production services and the installed Mac app
have not been replaced, and D2 deletion remains gated by P3.

Source-native crash injection now passes 12 checks: observe the approved fill, discard its local
reply before durable completion, kill the actual Controller, retain an unknown outcome without
replay, close only owned task views, preserve the personal tab and all earlier fences, reconcile
the observed effect through the original IPC, then execute a fresh task in a new incarnation.
The first crash run failed its baseline assertion because it sampled before remote fence
synchronization; the fixture now waits for that synchronization and retains the failed evidence.

The actual Mac package also passes a blocked-Relay/reconnect run (seven grouped checks). Work2's
hash-verified x64 AppImage extraction passes eight groups through its own isolated Relay/Gateway:
packaged Controller/MCP, approval and native effect, original panel controls, retained fences,
blocked transport without business HTTP rescue, same-profile recovery and Quit cleanup. Linux
uses `gnome_libsecret`; the actual task renderer has Seccomp filter mode, NoNewPrivs and a user
namespace distinct from main. Keyring, Go and buildx dependencies are extracted into the private
test directory. A temporary AppArmor rule matches only the candidate executable; no
`--no-sandbox` or plaintext credential fallback is used. Initial missing BuildKit, cold Go build
timeout and sandbox-start failures remain recorded. Gateway/WebChat/PostgreSQL IDs, images,
configuration, complete mount records, networks and start times remain equal to the pre-test
snapshot; an order-only mount-array comparison failure is retained separately. These runs use
the mock model and disposable Relay, not TEST1 or live-provider acceptance.

Work2's existing GNOME session remains locked. Its screenshot interface cannot capture that
locked desktop, and a main-WebContents capture omits the native child view. A separate private
X11 display therefore supplies the composed-window visual evidence: the original sidebar,
conversation, browser tab, read-only header and native `Alice` form are visible together. The
same candidate/profile passes all eight groups there; the user desktop is not unlocked.

Additional recovery evidence: actual MCP-child crash passes 12 native checks; injected rejection
of both terminal journal writes passes 13, including the durable `dispatched` row, visible
`unknown`, no replay and a fresh incarnation after explicit reconciliation. An initial injector
incorrectly threw synchronously instead of returning the journal API's rejected Promise; that
failure and its unresolved fence are retained. The corrected run preserves every prior fence.
Gateway chunk race tests also cover lost final chunks, foreign command/result IDs, wrong offsets
and totals, duplicate content, and deliberately out-of-order assembly; completed mutations stay
completed and execute only once.

Work2 then runs the same candidate/profile against its configured `sparkclaw-product-v1` real
provider (`mock: false`), still through the disposable encrypted Relay. All eight groups pass,
including the approved fill, original controls and blocked-Relay recovery. One complete workflow
takes 32,828 ms, including model calls, automated approval and reconciliation polling; this is
not a transport percentile or old/new-path comparison. The first real-provider startup failed
because the isolated network could not resolve the provider; only the owned candidate Gateway
was attached to its lab ingress network before restart, with no business port published. The
three production container/configuration/mount/network/start baselines still match afterward.
TEST1 authentication, Mac-to-work2 live-provider coverage, full latency comparison, remaining
installed/offline and fault scenarios, and paired rollback/cutover remain open.

Two source-native conversations also pass seven grouped checks: one active lease, observed FIFO
waiting, the unchanged 10-second acquire budget expiring without journal entry or late dispatch,
and a separate explicitly submitted task succeeding after release. Distinct sandboxed task views
retain their conversation ownership without changing the selected conversation; personal pages
and every prior unknown fence remain intact. A transport-level `delivered` blocked response is
not counted as successful browser work. The first fixture incorrectly expected both initial
workflows to finish within the queue budget; that failed evidence is retained.

An actual main-process SIGKILL after the approved native fill but before terminal journal
persistence exposed a cleanup defect: the child group exited at its watchdog deadline, but
private sockets, adapter secret and output directories remained. The client-only entry now
retires its MCP children before shutdown when its parent-liveness pipe ends, then removes only
the original private directory after verifying its inode/device identity. Normal main-owned
shutdown and backend assembly retain their existing cleanup paths. The same-profile restart
test now passes: no owned process/files remain, the durable `dispatched` row becomes `unknown`,
task history and earlier fences survive, no fill is replayed, and a new incarnation works after
explicit reconciliation of the observed effect. One cleanup sample is 90 ms, not a percentile.
The initial failure remains archived. Lifecycle tests reject shared/symlinked/split directories
and preserve a replaced directory. Controller tests pass 361/151 skipped; desktop remains
649/1 skipped. Refreshed package evidence follows below.

Mac ARM64 and Linux x64 candidates are rebuilt from `9c7905aa`; both contain the same verified
207-file Controller manifest (18,736,478 bytes). Actual packaged main-process SIGKILL while the
original field approval is pending removes the owned process group and private runtime files
on both platforms (single observations: Mac 161 ms, Linux 157 ms). This package test grants no
fill approval and is distinct from the source post-effect/unknown-write test above. Restarting
the same profiles then passes the complete form/UI/blocked-Relay groups again: Mac seven and
work2 real-provider Linux eight. Production containers, configuration, mounts, networks and
start times still match the original baseline; the temporary AppArmor rule, private display,
keyring children and candidate Gateway processes are stopped when idle. The GNOME lock remains.

A separate newly enrolled lab on work2 now qualifies the updated Mac package against its real
provider: six groups pass, approved native `Alice` fill, original controls, retained fences,
normal Quit, and zero direct business HTTP. Its encrypted ISCP Relay and issuer are reached
through loopback SSH forwards; the Linux lab's identity/profile is not cloned or reset. The
single complete workflow is 43,243 ms; the refreshed colocated Linux run is 55,193 ms. These
include model calls and approval/polling and are not performance percentiles or a comparison
with the legacy path. This closes that scoped Mac-to-work2 candidate check, not the TEST1 public
Relay gate. TEST1 authentication, remaining acceptance-matrix rows, installed/offline coverage,
full latency comparison and paired rollback/cutover remain open.

The refreshed Mac package also passes nine groups with workbench-renderer termination: the
owned native view hides immediately, main reloads a different renderer PID, the restored draft
and conversation list allow explicit selection of the same completed native page, and history,
sandbox and detached debugger remain correct. Closing hides the existing window; dispatching
the application's activation event shows that same window again. Relay-block recovery still
passes afterward. This tests the activation handler, not a manual Dock click. The initial
fixture failures are retained: this Electron reports `killed` for the injected renderer death,
the pinned Playwright Page permanently treats that target as crashed even after main restores
it, and selection before draft/list rehydration does not establish a recovered interaction.
The corrected fixture observes the replacement renderer through main's WebContents and waits
for the original controls to become ready; it does not reload or repair the app itself. A fresh
run passes without an observation pause. Native visual inspection of this latest recovery is
unavailable while the Mac is locked; prior composed-window evidence remains separately scoped.

The verified Mac DMG is also mounted read-only and copied to a separate user Applications
location. That actual installed candidate passes seven normal packaged groups with the same
profile; the existing system application remains untouched. With `PATH=/usr/bin:/bin`, its
Electron Node runtime passes SQLite/fetch/WebSocket probes under an outbound-denying Seatbelt
profile, with loopback allowed and a non-loopback connection rejected with `EPERM`. Applying
that outer profile to the full GUI conflicts with Chromium's nested GPU/network-service
sandbox initialization, so the GUI attempt fails and remains recorded. No Chromium sandbox is
disabled. The normal installed GUI and restricted Node probe are separate evidence, not a
completed Mac offline GUI gate.

On work2, the verified Linux deb payload is extracted into a private prefix and its actual
packaged main runs inside a network namespace containing only loopback, no external routes,
and an `ENETUNREACH` result for an external connection. Fixed private Unix-socket bridges carry
only the lab Relay/issuer ports; encrypted ISCP is unchanged. Eight groups pass with the mock
Gateway, including approved native fill, original controls and blocked-Relay recovery, with
zero direct business HTTP. Renderer Seccomp mode 2, NoNewPrivs and a separate user namespace
remain verified. The bundled Node API probe also passes with the system-Node-free PATH.
This verifies the deb payload, not package-manager installation or its maintainer scripts.
Owned bridges/sockets/display/keyring and the exact candidate AppArmor rule are cleaned;
the isolated Gateway's real-provider configuration is restored and it is stopped when idle.
Production container/configuration/mount/network/start baselines still match. A11 therefore
has additional installed/offline coverage but is not closed across all target environments.

A source-main comparison now runs the same independently approved form task through two local
encrypted Relays and mock Gateways. The legacy native executor remains at `e031a453`, with
only the same mock-schema and form-completion corrections applied to both sides; the exact
two-file patch is archived. Uncorrected legacy attempts returned success without a fill and
are retained as failed attempts, not fast samples. Each measured sample verifies one approval,
one native `Alice` fill, sandbox, detached debugger, retained fences and owned shutdown. Times
span enqueue/submit through durable delivery and observed page effect, including automated
approval and reconciliation polling. The first task uses a freshly launched main with the
lab's existing enrolled profile; the following nine use that same main.

| Local mock form path | Samples | First task | Median, all 10 | p95, nearest rank | Median, following 9 | Reverse calls/task |
| --- | --- | --- | --- | --- | --- | --- |
| Legacy native executor with shared corrections | 10 | 2,747 ms | 2,684 ms | 2,764 ms | 2,667 ms | 11 |
| Controller before settle adjustment | 10 | 17,865 ms | 16,587 ms | 17,865 ms | 16,562 ms | 36 |
| Controller with client MCP settle set to zero | 10 | 5,440 ms | 4,281 ms | 5,440 ms | 4,267 ms | 36 |

The main measured overhead was the MCP's fixed 500 ms delay on every observation: 21 reads
per task consumed about 10.8 seconds locally, in addition to the shared Gateway adapter's
quiet-period polling. Only the client entry now selects zero MCP settle delay; the Gateway's
ready-state, quiet-period, route and deadline checks stay unchanged, and backend/mail factories
retain their 500 ms default. The adjusted median is about 74% lower, still about 60% above
the corrected legacy path. Median decoded request/response totals before adjustment are
12,364/17,252 bytes for legacy and 47,999/53,807 for Controller. Per-operation dispatch times
and bytes are retained; these local handler timings exclude encrypted framing and Gateway
round-trip time. Ten samples make the reported nearest-rank p95 the observed maximum, not a
production tail estimate. TEST1/WAN timing, chunk throughput and the full operation matrix
remain open. Archived `9c7905aa` packages predate this source adjustment; refreshed evidence
follows below.

Three additional tasks delay the fixture's visible draft update by 250 ms after input. All
pass, and the post-write snapshot starts only after the updated text has exceeded the unchanged
500 ms quiet period. The direct native forwarder fixture also passes all 14 groups, including
frame effects, owned popup, retained-page adoption, explicit close and crash cleanup. Full
Controller tests pass 362/151 skipped and desktop tests remain 649/1 skipped. These source
checks validate the settle adjustment; they do not qualify it inside the older packages.
Normal two-task FIFO execution passes six groups. A separate completed-read reply hold forces
the other task's unchanged 10-second acquire budget to expire; seven groups verify no journal
entry/late dispatch and success only for a separately submitted task. Post-fill main SIGKILL
and same-profile recovery pass again, preserving unknown fences without replay and removing
owned processes/files (one 92 ms cleanup observation).

Both candidates are now refreshed from `4615ad82` with the same verified 207-file Controller
manifest (18,737,017 bytes). Mac passes nine packaged renderer/activation/Relay groups and
parent-exit cleanup; a separate copy from the verified DMG in user Applications then passes
seven same-profile restart groups. That installed copy also passes six real-provider groups
against the existing independently enrolled work2 lab via SSH loopback forwards. Linux x64
passes eight real-provider AppImage groups, parent-exit cleanup, then eight same-profile
offline deb-payload groups in the isolated network namespace. The restricted bundled-Node
API probes pass again on both platforms. The Mac full offline GUI limitation and private-prefix
deb installation scope remain unchanged; neither result closes the entire A11 row.

The previous four artifacts remain archived with their original hashes. New artifact hashes,
private evidence paths and open gates are indexed together. Both work2 candidate Gateways are
stopped when idle, the original real-provider configuration is restored, temporary AppArmor
rules/private display/keyring/bridges are gone and the SSH forwards are closed. The three
production container/configuration/mount/network/start baselines still match; GNOME remains
locked. TEST1 public Relay/authentication, the remaining matrix, WAN/chunk timing, native
Mac recovery/Dock visual inspection and paired rollback/P3-gated deletion remain open.

Source `9e372018` adds actual Gateway hard-restart and authorization lifecycle qualification.
A delayed old read/write failure could previously stop a replacement Controller; four red
regressions reproduce that across ISCP reconnect and runtime retirement. Cleanup now checks
both authority generation and incarnation while still persisting uncertain writes. All four
regressions pass, full desktop tests pass 653/1 skipped, and the native forwarder passes 14 groups.

The existing source profile survives hard restart of its exact labelled Gateway container.
An awaiting approval becomes terminal and cannot fill. In a second run, `Alice` is observed
before terminal journal persistence; after restart, a separate new task opens a native page
while the old reply remains held. Releasing that failed continuation preserves the replacement
Controller and page, leaves the old write durable `unknown`, and rejects both the old authority
and old binding. Only that observed fixture effect is reconciled; prior fences and personal
pages remain. No direct business HTTP is used. The first pre-approval restart attempt had a
delivered-but-blocked fresh task; its failure is retained, and later native runs pass. Its exact
cause is not established by the separate deterministic cleanup regression.

A separately enrolled disposable authorization lab uses the original Settings/Devices controls.
Logout during pending approval blocks the decision and performs no fill. Authorization deletion
after an observed fill obtains a signed revocation receipt, removes task pages/processes/runtime
files, preserves personal pages, persistent cookies and immutable local history/installation,
and leaves the original write `unknown` without replay. A fresh main using that same profile
retains the revoked state, history, cookie and unknown journal, starts no Controller, and rejects
checking for a newer authorization when none is installed. This is local-test profile evidence;
logout followed by a local-test launch is not evidence of node-managed locked restart semantics.
The initial lab attempt had the browser tool disabled in its disposable configuration and is
retained as a failed fixture setup; the corrected configuration enables the tool itself.

These are source-main observations against mock Gateway/local encrypted Relay, not TEST1 or
packaged A06 completion. The four `4615ad82` artifacts are retained with verified hashes and
predate this cleanup repair; refreshed candidate qualification is tracked separately.

The refreshed `9e372018` Mac ARM64 and Linux x64 packages carry the same 207-file Controller
inventory (18,737,017 bytes). Mac passes nine renderer/activation/Relay groups, hard-parent-exit
cleanup and seven same-profile groups from a separate DMG installation in user Applications;
the restricted bundled-Node probe passes too. Linux AppImage passes eight real-work2-provider
groups and hard-parent-exit cleanup. The new deb's executable, ASAR and Controller manifest
match that AppImage, but its package-manager installation and a repeated full offline GUI run
are not claimed. The system Mac app is unchanged. New artifact/evidence hashes are indexed;
earlier benchmarks/offline/cross-machine evidence remains scoped to its original candidate.

An additional source-native fixture passes 16 groups with a cross-origin HTTPS iframe in a
separate renderer process. Controller click creates an asynchronous field, bounded snapshot
polling finds its ref, and `page.type` produces the observed value inside that frame. A personal
page stays outside the task's tab inventory and never attaches its debugger; owned popups,
retention/adoption and teardown still pass. Two earlier attempts assumed a fixed 350 ms wait
proved dynamic readiness and failed to find the field; they remain retained. This extends A02/A03
source coverage, not the full packaged site/operation matrix. The work2 candidate Gateway is
stopped again, its real-provider configuration remains intact, temporary AppArmor/display/keyring
resources are gone and GNOME remains locked. Production identities/images/configuration,
complete mount entries, networks and start times match the baseline. An initial mount-list
order comparison failed; comparing every complete entry by destination proves only ordering
changed. TEST1, the remaining matrix, native visual/offline GUI, WAN/chunks and paired rollback
with P3-gated deletion remain open.

Further A04 qualification found a real gap after `9e372018`: an original ClientStore cancel of
a queued task still allowed its native acquire to complete later. The local cancel signal now
removes that waiter and suppresses subsequent commands for the canceled task; a separate task
continues normally. A second deterministic red regression showed that handing off inside
`onStopped` could encounter the supervisor's still-pending stop promise. FIFO release now waits
for that promise to settle. Desktop tests pass 659/1 skipped. The encrypted native queued-cancel
scenario passes nine groups, and active cancellation during approval passes six groups while
another queued task completes with a new Controller and no field fill. The direct native fixture
passes 18 groups, including 32 waiting acquires, refusal of the 33rd before journal/dispatch,
cancellation of all waiters without stopping the active lease, and cross-origin dynamic effects.

Retain both failed cancellation attempts and the intermediate fixture failure: Gateway cancel
can produce `unknown`, and transport delivery alone does not prove a fresh task acquired a page.
The successful fixture reconciles only the exact canceled acquire after proving no native
dispatch, then checks the new task's actual page; all earlier unknown fences remain. This repair
is source-only until a later candidate refresh; the qualified `9e372018` packages do not contain it.
The original task's cancellation marker survives running reconciliation and store reopen;
foreign identities cannot read it, and a new authority session still refuses that task. A new
explicit task has a distinct local ID. Paired rollback must preserve this stop intent as well as
the existing write fences; an older binary that ignores cancellation metadata is not qualified.

Refreshed `05fa3c1a` Mac ARM64 and Linux x64 candidates now contain the cancellation repair.
Both pass six packaged cancellation groups: the original pending approval is rejected, owned
execution is removed, the cancellation marker is read from the actual packaged SQLite store,
and a distinct task succeeds while personal pages and prior fences remain. Mac also passes
nine renderer/activation/Relay groups and seven same-profile groups from a separate verified
DMG copy in user Applications; its restricted bundled-Node probe passes. Linux AppImage passes
eight real-work2-provider groups with renderer sandbox checks. Its deb executable, ASAR and
Controller manifest match the AppImage. Four artifact hashes and 17 evidence files are indexed
privately. The initial packaged cancellation observer used unsupported dynamic import inside
Playwright's evaluation VM; that failed attempt is retained, and the corrected observer reads
the built-in SQLite module without changing product execution.

The system Mac app and three work2 production container/configuration/mount/network/start
baselines remain unchanged. The owned candidate Gateway is stopped; temporary AppArmor,
display, keyring and candidate processes are absent, and GNOME remains locked. Current-package
hard-main-kill and full offline GUI were not repeated; previous results remain scoped to their
original revision. TEST1, remaining packaged operations/peer matrix, native visual/offline GUI,
WAN/chunks and paired rollback with cancellation-marker preservation remain open. D2 deletion
still follows P3.

Expanded real-provider packaged operation checks expose an additional Gateway presentation bug:
`workflow_result_presentation` opened a second page at the same URL and discarded the verified
DOM state. The client adapter now observes/selects the existing page, refuses missing/changed
routes, and leaves explicit-open and backend semantics intact. Red regressions reproduce the
replacement; focused browser packages, race checks, workflow tests, vet and Linux build pass.
The unchanged `05fa3c1a` Linux package with this isolated Gateway repair retains the actual
dropdown and delayed-click effects. That run still fails overall: raw Controller snapshots have
paragraph text and selected options that the public projection drops, and iframe clicks change
the frame while the stable wait observes only the outer page. Those defects remain under repair;
delivered tasks or success text do not establish workflow completion. The first operation fixture
also incorrectly required approval for every low-risk click; another incorrectly assumed two
cross-origin subdomains must use different renderer processes. Both failed observations are
retained, and separate-site frame qualification remains pending. No production binary changed.

The subsequent Gateway repair preserves those snapshot fields and includes frame semantics in
stable waits. Deterministic red/green tests cover lost text/selection/disabled state and a frame
effect versus ref-only changes. Browser packages, race checks, all browser agent/ToolHub tests,
vet and Linux build pass. Initial focused host tests without the canonical `TMPDIR` hit the
existing private-path guard; the corrected environment passes and both logs remain.

Actual `05fa3c1a` Linux AppImage and independently installed Mac ARM64 candidates now each pass
six groups against real work2 providers: runtime, dropdown, delayed dynamic control, cross-site
iframe with separate renderer PID, owned popup lifecycle and normal exit. All four operations
verify both native effects and workflow completion, preserve personal pages/prior fences and
attempt zero direct business HTTP. These final runs do not enable the diagnostic Controller
observer. They use the same isolated Gateway repair (SHA-256 recorded privately); the desktop
artifacts remain unchanged. The Mac route uses the separately enrolled lab and SSH loopback
forwards, not TEST1. Individual task durations are observations, not percentile benchmarks.

The intermediate popup check incorrectly required every owned tab to survive normal release;
§5.4 retains the current conversation view and cleans other tabs. The corrected check observes
the sandboxed task-owned popup while running, then verifies its cleanup and retained parent.
Earlier failed attempts remain archived. Both candidate Gateways are stopped, SSH forwards and
temporary AppArmor/display/keyring/process resources are removed, and all three work2 production
baselines still match with GNOME locked. TEST1, the rest of the operation/peer/install matrix,
WAN/chunk timing and paired rollback/P3 cutover remain open.

Clean detached source `d4eae4c6` now produces matching Mac ARM64 DMG/ZIP, Linux x64
AppImage/deb and Gateway binaries. The Controller inventory remains 207 files / 18,737,017
bytes, manifest `2f7d00277cda633773229752661989942cffcead15997c2ed11ea5d6e4dc048a`.
The independently installed Mac copy has exactly the same ASAR, WebChat, local-preview and
Controller file hashes as `05fa3c1a`; the helper and manifest source revision change. No system
application is replaced. Mac installed qualification passes seven groups, including original
panel/tab/hide/widen behavior and blocked-Relay recovery.

The actual old/new candidates now pass a paired same-profile upgrade and rollback: Mac through
the local encrypted Relay with the mock provider, and colocated Linux through the work2 lab
with the real provider. The old desktop is `05fa3c1a`; each original lab Gateway is restored
for rollback. New Gateway hashes are `383a7fedbe8a58d467a301ca792f8418dee93a016cec85f5f2ef91d22f9e0b27`
(Linux ARM64 for the Mac lab) and `25edecb42da4885fdfaf3d509c7cc2d365f5c43d1582b27e6a12eed6e4d89124`
(Linux x64). Writers stop before replacement; no identity, database, credentials or receipt
state is restored from an older copy. Each preparation passes four groups; upgrade and rollback
pass seven each. An actually applied fill loses its local reply and remains unknown throughout.
Cold and reopened checks preserve installation, history including newer messages, an unsent
draft, personal cookie, journal rows and cancellation markers. The retained file inventory is
empty, so this run makes no attachment-payload preservation claim. Distinct explicit
tasks still complete. Synthetic fresh acquire IDs at the actual packaged forwarder cannot
bypass either the original unknown or canceled task, and make zero native calls. This covers
these candidate pairs only; pre-cancellation desktops remain unqualified rollback targets,
and the complete A13/P4 cutover still depends on P3 and D2 deletion.

Retained failures include a missing workspace dependency copy during clean assembly, the
fixture's wrong draft scope field and prematurely sampled observer, and Linux fence sampling
before Controller readiness. A separate real-provider open task returned a workflow setup
failure without native dispatch. Its exact cause is unestablished: an isolated diagnostic
Gateway attempt and then the unchanged clean candidate both succeeded. Diagnostic artifacts
are separate; final upgrade/rollback uses the clean release binary. A delivered failure message
is never counted as a successful browser action, and no unknown was reconciled to make the
comparison pass. TEST1, remaining matrix/native/offline/WAN gates and production acceptance
remain open.

The refreshed Linux AppImage also passes eight normal real-provider groups, including renderer
Seccomp/NoNewPrivs/user-namespace isolation, actual approved fill, original UI and Relay recovery.
Its first normal run captured an empty fence baseline before the Controller had loaded; waiting
for Controller readiness fixes the fixture, and the original unknown remains intact. The deb
private payload has identical executable, ASAR, helper and manifest; this is not a package-manager
installation claim. Both work2 candidate Gateways and the local Mac candidate Gateway are stopped.
Owned candidate/X11/keyring processes and both exact temporary AppArmor profiles are absent;
production container IDs/images/configuration/HostConfig/full mounts/networks/start times and
real-provider configuration remain equal to their original baselines. The original GNOME seat
is still locked. Four artifact hashes and scoped success/failure evidence are in the private
`candidate-d4eae4c6-index.json`; no deploy, system application replacement or old-path deletion.

A subsequent actual Mac package-copy test removes only its Controller manifest. `d4eae4c6`
incorrectly keeps the browser surface enabled from the remote qualification report while its local
Controller is unavailable; retain this failed A01 evidence. Source now combines local bundle
verification with the existing capability projection and updates the original disabled entry. It
keeps independent files/mail/settings qualification and cannot revive expired remote authority.
A second red regression shows that cleanup could expose a previously cached manifest after failed
verification; invalidating that cache fixes it. Three red cases become green; Desktop 661 pass /
1 skip and the 38 existing LocalWorkbench tests pass. This is a source repair awaiting refreshed
package/native missing/corrupt/recovery qualification; the prior `d4eae4c6` package claims do not
include it. No UI component or wire/database schema changes.

The refreshed clean-source `a9dcbb98` Mac and Linux packages pass actual missing-manifest,
corrupt-file and restored-package checks (5/5/4 assertions per platform). The original browser
entry is retained and correctly disabled, while original file/draft IPC preserves a newly saved
attachment with SHA-256 `11b27d84cdd6cb110272d2e7143d7e1ef2bf6c7d633919325e362bd0c11fa663`.
Mac paired rollback to `05fa3c1a` and its old Gateway preserves those bytes and the prior durable
unknown-write/cancellation fences. Linux paired rollback to the same old desktop and its original Gateway also passes (4 assertions).

Do not mark `a9dcbb98` ready: its Mac renderer crash/reload and close/activate checks pass, but
the subsequent Relay interruption causes an availability notification feedback loop and renderer
OOM. Local capability reprojection was invoking the connection lifecycle handler, which suspended
the Controller and projected availability again. A bounded regression reproduces 20 unwanted
lifecycle calls. Source now publishes local capability changes separately from connection lifecycle
events; 28 focused tests and Desktop 662 pass / 1 skip. This repair still needs a new clean package
and repetition of the combined renderer/Relay test. Retain the failed package, logs and evidence.

Clean-source `81f796f4` rebuilds all four artifacts. The independently installed Mac DMG passes
the same combined renderer recovery, close/activate, blocked-Relay recovery and original-panel
scenario (9 groups); work2 x64 passes its real-provider normal/Relay/sandbox scenario (8 groups).
Both platforms repeat actual missing/corrupt/restored-copy checks (5/5/4) against the same retained
attachment checkpoint; the original entry, draft, file bytes and durable fences survive. The test
can explicitly reuse that checkpoint across later packages without replacing its data. The new
deb's privately extracted executable, ASAR, helper and Controller manifest equal the AppImage;
this is not a system dpkg-install result. Clean Gateway builds remain byte-identical to `d4eae4c6`.

`candidate-81f796f4-index.json` records the four package hashes, clean revision, Gateway hashes and
scoped evidence. Controller manifest SHA-256 is
`cf2ff9f9fc8b9bdc4c78abbf1c4edffec21b264129d035929dc7dcf7512d4e7b`.
Owned test Gateways are stopped; candidate processes, private X107 display and exact temporary
AppArmor profiles are gone. All three work2 production containers retain their original IDs,
images, configuration, HostConfig, full mounts, networks and start times; real-provider config
and locked GNOME seat are preserved. TEST1, old/mixed-peer/LAN matrix, remaining native/offline
and timing gates, complete P3 and D2 deletion are still open. No production installation or push.

Actual `81f796f4` desktop against a clean pre-Controller `b5c21e14` Gateway fails A01: the new
helper rejects the exact preceding operation table and never connects. The private responder
configuration omits only the five operations unknown to that old binary; its new Gateway/helper
files are restored afterward, with the owned Gateway stopped. Preserve this failure separately
from the successful same-generation checks above. Source now adds the explicit Controller
negotiation extension described in §7 and passes three red-to-green regressions, exact-table and
bidirectional admission checks, unrelated-file capability checks, complete `iscpworkbench`,
targeted race, vet and Gateway ISCP tests. The first Gateway run fails the existing bounded receipt
sweep at 274/276; its isolated rerun and the complete ISCP rerun pass without receipt code changes.
The initial failure remains unclassified. New clean packages and native mixed-peer reruns are pending.

Clean `ac57fa8b` packages and Gateways now pass the same-generation Mac renderer/Relay scenario
(9 groups) and Linux real-provider/sandbox/Relay scenario (8). Native cross-version checks use
the actual pre-Controller `b5c21e14` desktop and Gateway binaries: new client/old Gateway and old
client/new Gateway each pass 5 checks on both platforms. Only browser is disabled; its original
entry and refusal remain, while files/settings, attachment bytes, draft and prior durable fences
are preserved without business HTTP. Returning to the new pair re-enables the entry and passes
the 4-check attachment/preservation scenario on both platforms.

Use a qualification inventory supported by each binary. The first Mac legacy-client attempt
used the new private helper configuration, whose five unknown operation names fail old strict
configuration validation; retain that fixture failure. The corrected old-peer launchers omit
exactly those names, retain identity/authorization/data, and restore the original helper files
afterward. Validation was not relaxed. These results name the frozen `b5c21e14` compatibility
baseline; they do not claim arbitrary intermediate development builds are mutually compatible.

Four artifact hashes and scoped test/setup-restoration/cleanup evidence are indexed in
`candidate-ac57fa8b-index.json`; Controller manifest SHA-256 is
`f8e7eac1ae2aa805c5b70a015723d231f77e5bc8b5838329ef021a1e9b18fd4f`.
The ASAR is byte-identical to `81f796f4`; helper and Gateway carry the negotiation repair.
Linux deb payload equality passes as private extraction only. Test Gateways are stopped,
candidate/X107/keyring processes and exact temporary AppArmor profiles are cleared, and the
three production container baselines, provider configuration and locked GNOME seat remain exact.
TEST1, LAN-only and remaining fault/native/offline/timing gates and P3/D2 cutover remain open.

The unchanged `ac57fa8b` packages now pass four additional encrypted-ISCP scenarios on Mac
(mock provider) and work2 Linux (real provider): out-of-order chunks, a final reply held for
31 seconds to exceed the Gateway deadline, an incorrect returned offset, and changed chunk bytes
that fail the result hash. The opt-in `run-client-controller-chunk-qualification.mjs` instruments
reverse replies in the actual packaged main; no ASAR, Controller or Gateway file is replaced.
The synthetic page changes its title after the approved fill, producing a real 79,434–79,436-byte
mutation result containing JSON-escaped characters. Normal unordered transfer completes the
workflow. Each failed transfer preserves one actual fill/input event and the retained sandboxed
view, releases the lease, and leaves the original request durably `unknown` with no assistant
success answer or automatic write replay. Reconciliation reads that same request; it does not
submit a new task. Unrelated ISCP reads succeed with zero direct business HTTP.

`verify-client-controller-chunk-receipts.py` independently checks retained files while both apps
are stopped. The receipts verify that Gateway and desktop write records remain `completed`
with the original task/digest, and that the Gateway completion timestamp precedes the first
chunk request. This completed action is distinct from the conservative unknown workflow result.
Local probes through the packaged forwarder additionally verify duplicate-command descriptor
reuse, changed-digest fencing, repeated final-chunk reads and rejection of another task, command,
result or out-of-range offset. These probes are explicitly not claimed as extra encrypted-wire
requests. Source checks pass: 16 forwarder tests and the Gateway `TestISCPController` group.

Retain the initial fixture failures: it first required failure text although the existing execution
path returns a durable unknown task, and then waited for a different terminal state; Linux also
sampled desktop state before authentication connected. The corrected assertions check actual
durable execution and write records, and startup waits for the original connection state. A
receipt-reader timestamp parser was separately adapted to nanosecond Go timestamps on Python 3.9.
The eight final native receipts, two durable-receipt comparisons and cleanup/preservation receipts
are indexed in `candidate-ac57fa8b-index.json`. Both owned Gateways are stopped; candidate,
X107/keyring and temporary AppArmor resources are cleared. Production containers, provider
configuration and the locked GNOME session retain their baselines. This is scoped A07/A08
evidence for large escaped mutation results; it does not complete screenshot, 10 MiB, capacity,
expiry, every fault combination, TEST1, offline/native installation or public-WAN timing gates.


Packaged screenshot qualification exposed a separate visual-workflow defect: presentation
selection discarded the trusted page/snapshot refs before `browser.visual_inspect` bound its
arguments. Preserve those two refs only through the requested visual stage; afterward and for
ordinary presentation, retain only the result URL. Three profile regressions verify the frozen
transition and replacement of forged page/generation/digest inputs with current trusted values.
The working-source Gateway with the unchanged Mac package delivers a 391,703-byte PNG through
16 encrypted chunks, with identical image/file hashes. This is not a clean-release result.

On work2 Linux, the corrected Gateway reaches screenshot execution, but the hidden view's CDP
capture stalls until MCP closes the target after 10 seconds. Changing `captureBeyondViewport`
alone still fails. A diagnostic native `capturePage` request with `stayHidden`/`stayAwake`, reduced
to a 1×1 pixel region, unblocks the original CDP capture: the 412,276-byte PNG arrives in 80 ms
(single sample), and its delivered file hash matches. The native hint's image is discarded.
Source now applies this hint only to hidden screenshots after task/session validation; the
original CDP parameters, image, errors and deadline remain authoritative. No panel, focus or
window visibility operation is added. The opt-in packaged screenshot fixture records capture
metadata, MIME/dimensions, chunk reassembly/file hashes and presentation preservation. Keep the
failed attempts and diagnostic overrides distinct from qualification of the pending clean pair.


Clean `d5fb3d80` Mac DMG installation passes all 7 screenshot checks, including unchanged
hidden view/panel/window/focus and exact delivered PNG. Linux's first clean-package screenshot
still times out; a second run observing native captures passes, with a 1×1 hint completing in
4 ms and CDP in 87 ms. Preserve both: a single hint is not sufficiently reliable. Source now
keeps non-overlapping 1×1 hints 50 ms apart until the original CDP command resolves or rejects;
late native results cannot schedule another hint. The image, format, clip and MCP deadline still
come from that single original command. A bounded lifecycle regression covers pending hints,
completion and failure. Clean-package reruns of this refinement remain pending.

The unchanged Gateway code passes the complete Linux suite (6,301 test nodes, 216 skips),
build and vet, with all 3,409 frozen source files verified before/after. Mac full-suite attempts
retain environment failures: overlong Unix socket paths, then a symlinked `/tmp`; the canonical
short path passes `cmd/sparkclaw`. Linux-only `/dev/shm` tests remain unsupported on Mac. Desktop
passes 663 tests with one skip, WebChat builds, and bilingual docs check 145 files. Mac native
visual observation is blocked by the locked desktop; its timeout is retained separately.


Clean `c6948132` candidates pass the Mac DMG screenshot scenario (7 checks), three separate
work2 Linux AppImage launches (7 screenshot checks each), and the original workflow/Relay/UI
regressions (Mac 9, Linux 8). Mac's 391,703-byte PNG uses 16 chunks; Linux's 412,276-byte PNG
uses 17, with all three Linux image hashes equal. Each delivered file matches the actual CDP
PNG, and hidden view, panel, window and focus remain unchanged during capture. Native child
cleanup, retained task views, prior unknown-write fences and zero business-HTTP bypass pass.
The final refinement passes 664 desktop tests with one skip. Its Gateway source and both Linux
Gateway binaries are byte-identical to the fully tested `d5fb3d80` versions. Four candidate
artifacts, hashes and scoped receipts are indexed in `candidate-c6948132-index.json`. The private
deb payload matches the extracted AppImage; this does not qualify system package installation.
Controller manifest SHA-256: `1194ecb3c1ea9fff2fc0afb89b06f21463b578f248f217c22c0719941f3ac31c`.

**Environment incident:** the work2 shared root filesystem filled during candidate extraction.
Test-cache growth materially contributed. PostgreSQL reports ENOSPC at 17:43:14 UTC on November 9,
then a checkpointer PANIC at 17:44:07; Docker's automatic restart also failed for lack of space.
Twelve owned, reproducible unpacked app/deb trees were removed only after verifying stopped
processes and retained source archives with hashes, freeing 8,769,241,088 bytes. Profiles,
evidence and compressed artifacts remain. The final preservation audit detected the stopped
production database; retain that failed audit instead of claiming unchanged production.

After checking the exact original container ID, image, configuration and full mounts, the same
PostgreSQL container was started at 17:49:57 UTC. It is healthy and a read-only transaction returns
`1` and `pg_is_in_recovery() = false`. Gateway/WebChat runtime baselines remain equal; PostgreSQL's
start time, endpoint ID and MAC changed. No container recreation, schema/volume change or
transaction replay was performed. These checks establish availability, not completeness of
interrupted business requests. Recovery logs and before/after receipts remain in the candidate
inventory. Owned test Gateways are stopped, temporary native/AppArmor resources are cleared,
and the original GNOME session remains locked. Check shared-disk reserve and production health
immediately on ENOSPC; retain at least 4 GiB before extracting further candidates. TEST1,
10 MiB/capacity/expiry, remaining fault/native/offline/WAN gates and P3/D2 cutover stay open.

The unchanged `c6948132` installed Mac and private work2 Linux packages now pass two more
result-state scenarios: full cache (7 checks per platform) and expired result (6). Explicit
synthetic occupancy fills the real 20 MiB cache without changing its limits. The approved fill
is refused before native dispatch and before a local journal row exists; its Gateway action
fence is `failed`, while the enclosing execution remains conservatively `unknown`. Release and
unrelated ISCP reads still work. After the original 60-second wall-clock expiry, expired buffers
are zeroed and a newly submitted, separately approved task completes with exactly one fill.
The original task is never replayed.

For result expiry, the real approved fill completes and its journal is durable before only the
cache clock is advanced by 60,001 ms. The clock is immediately restored. Encrypted chunk reads
return unavailable, the workflow becomes unknown, and both durable write records stay completed.
After the original command slot's real wall-clock TTL also expires, a local duplicate-command
probe still returns completed/result-unavailable without redispatch. That last probe is not an
additional encrypted-wire request. Independent readers verify both platforms' retained Gateway
and desktop files after the applications stop, including the capacity refusal's absent local row.

Separate synthetic UDS replies exercise the actual installed Electron/ASAR parser: 10,485,760
bytes of UTF-8 JSON are accepted; one extra streamed byte is rejected without truncated success,
and a later health request works. This does not claim a 10 MiB browser/MCP result or transfer over
ISCP; MCP's 8 MiB inner budget is unchanged. Two new source regressions cover this boundary and
fixed-expiry cache accounting. Desktop now passes 666 tests with one skip. Product binaries and
the original UI are unchanged; scoped receipts and fixture hashes are added to the same candidate
index. Owned Gateways are stopped and temporary native/AppArmor resources are cleared. The new
work2 audit verifies post-recovery stability and original configuration/mounts, explicitly retaining
the earlier PostgreSQL start/network changes and failed preservation audit. Free disk space is
8,281,169,920 bytes. TEST1, remaining fault/native/offline/WAN evidence and P3/D2 cutover remain open.

The same packages additionally pass terminal-journal-failure qualification on Mac and work2
(7 checks each). This fault rejects the journal's terminal persistence method for one approved
fill, including its attempted unknown finalization; it does not fill a filesystem. The page
receives exactly one input event while its durable row is still dispatched. Concurrent local
duplicate-command and changed-digest probes share the unknown outcome or are fenced, with no
second native write. The encrypted original reply cannot report success; owned execution stops,
and a fresh command ID in the affected conversation is refused before native admission.

After a clean close, the original dispatched row is still on disk. Reopening the same installed
application and profile promotes it to durable unknown with identical command/digest/owner/task
identity. No native calls occur on reopen, the fresh-command refusal persists, prior unknown
fences remain, and unrelated ISCP reads work. Independent stopped-state readers verify the
Gateway action fence, desktop journal and enclosing execution are all unknown. Duplicate/fresh-ID
probes are explicitly local; only the original workflow uses encrypted transport. Both test
Gateways are stopped and restored, temporary native/AppArmor resources are absent, and work2's
post-recovery health/configuration checks pass with 8,298,221,568 bytes free. These opt-in fixtures
change no product binary or UI and do not complete the remaining acceptance/cutover gates.

LAN-only qualification of the unchanged `c6948132` packages now passes 5 groups on each platform.
An isolated, pinned HTTPS identity backend and a dedicated retained LAN profile exercise actual
main/preload/UI code. Original personal tabs, address navigation, hide/reopen and width controls
work; the page remains sandboxed with no debugger attached. Local probes of Controller status
and the old reverse command return typed 503/403 refusals, with no Controller start or adapter.
No browser-host grant or execution dispatch reaches the synthetic backend. Reopening the same
profile retains its installation, encrypted login, attachment bytes and unsent draft, and an
ordinary LAN owner read succeeds. This is client-side LAN admission/personal-browsing evidence,
not a real-Gateway workflow or TEST1 run. The existing ISCP profiles are untouched. A01 now
explicitly distinguishes the authorized personal-browsing exception already specified in §3;
disabling that entry on LAN would violate the design.

Retain the first Mac/Linux navigation timeouts: opening the panel already creates a blank tab,
and the fixture submitted while its separately requested tab was still being selected. Waiting
for that new renderer tab selection makes the unchanged packages pass. A Linux launcher attempt
also failed before app startup because a version substitution left the wrong fixture filename.
Both failures remain separate from successful native evidence. Private TLS listeners, candidate
processes and temporary AppArmor/X11 resources are gone; production post-recovery checks pass.

A separate read-only work2 observation finds its backend browser, Controller and App-CLI executor
services active. In the preceding 30 minutes, retained executor rows record 29 Gmail, 25 Outlook
and 29 QQ `collect_page` completions, with zero pending releases. These aggregate process/ledger
checks establish continuing acquisition activity after recovery, not inbound-message completeness,
mail delivery or a replay of interrupted work. No mail operation was dispatched by this check.

Maximum-size encrypted transfer is now measured separately from the earlier UDS/parser proof.
An opt-in fixture pads the result-cache title after one real approved fill and its completed
journal, producing exactly 10,485,760 bytes. This is explicitly synthetic cache data, not a
10 MiB native browser/MCP output. The unchanged `c6948132` installed Mac and work2 Linux packages
each pass 6 checks: 320 real encrypted 32 KiB chunk replies, complete hash/size verification,
one native fill, retained write records, release, and unrelated ISCP reads. A subsequent page
read is refused before native dispatch because earlier live results plus the maximum result
leave less than the next 10 MiB reservation. The delivered blocked answer is not workflow success.
Retain the first Mac failure, whose fixture incorrectly required that workflow to succeed.

Two Mac samples take 12,893 and 12,827 ms; one colocated work2 sample takes 9,717 ms, measured from
the desktop descriptor becoming ready to receipt of the next same-task execute. This includes
Gateway verification and downstream scheduling, not just network time. The largest measured
encoded envelope is 44,008 bytes. These samples do not establish WAN percentiles or TEST1 results.
Independent stopped-state readers verify both completed write records and the subsequent
pre-dispatch refusal. Six focused source boundary/wire tests and the 145-file docs check pass;
product binaries, limits and original UI remain unchanged.

The same maximum-size fixture **fails** across the retained Mac-to-work2 isolated route with
SSH loopback forwarding. Only 194 chunk replies / 6,356,992 raw bytes are observed before the
existing approximately 30-second budget ends; no complete hash-verified result or follow-up
execute is observed. The actual fill occurs once, both durable write records remain completed,
the enclosing execution is unknown, and release completes. Independent stopped-state checks
confirm those outcomes without comparing wall clocks across hosts. This is a retained transfer
qualification failure, not a successful large-result test and not proof of which network or
transport component caused the delay. It leaves maximum-size cross-machine timing open.

All tested isolated Gateways are stopped and restored to their prior binaries, the SSH forwards are
closed, and the new unknown execution is retained. The work2 post-recovery configuration,
mounts and health audit passes with 8,240,865,280 bytes free; the earlier production PostgreSQL
incident and runtime changes remain in the record. TEST1, remaining fault/native/offline/WAN
gates, paired cutover and D2 deletion remain unqualified.

A second failed cross-machine attempt enables the installed helper's existing private latency
trace and records bounded, content-free desktop chunk timings. All 195 observed chunk RPC IDs
match four helper stages; 6,389,760 raw bytes return before the unchanged budget ends. Negotiation
actually grants four chunks in flight at 32 KiB each. In this one run, request authorization
averages 230.94 ms and send authorization 253.23 ms; the helper's handler averages 1.94 ms, and
the desktop forwarder 0.284 ms. Authorization durations include mutex waiting, network and proof
verification. They overlap across calls and are not added or described as network RTT; Gateway
stages are not captured. The analyzer reports complete correlation for the observed chunks while
preserving `native_qualification_passed: false`.

The retained helper configuration identifies this route as **local-test/local-lab**. Its
`GrantLifecycleClient` serializes fresh authorization network checks under a mutex. This is a
measured bottleneck in that test route; the production `node-managed` client instead has a
separate implementation that coalesces overlapping fresh checks of one authority generation.
The local-test result neither qualifies nor disproves node-managed maximum-size performance.
Keep both native failures, all unknown execution rows and the unmodified limits. Do not change
the production authority contract or optimize the legacy test route merely to turn its check
green. The next required performance evidence must identify its actual authorization mode and
include the node-managed path; TEST1 remains independently required.

Opt-in `SPARKCLAW_CONTROLLER_CHUNK_TRACE=1` observations and
`analyze-client-controller-chunk-trace.py` now preserve this distinction in reusable tooling.
The analyzer reads existing evidence without changing application or Gateway state. The isolated
Gateway is restored/stopped, forwarding is closed, and post-recovery work2 checks pass with
8,194,330,624 bytes free. No product binary, UI, timeout or authorization check is changed.

The next qualification uses a new, isolated **node-managed/production** deployment on work2.
An independently built ISCP `f6037224` public duplex Relay runs with persistent PostgreSQL,
separate signing identities, pinned TLS, operator-approved service registrations, encrypted
issuer forwarding and ControlV2. The issuer, forwarder, Gateway and installed clients retain
the clean `c6948132` source/binaries. This uses SSH loopback forwarding to work2; it is not TEST1.
No production package or production authorization database is replaced.

The unchanged Linux packaged helper completes invitation enrollment with an explicitly
file-backed qualification key. Separately, the installed Mac completes native-keyring
enrollment/reconciliation and reopens the same selected profile, original workbench and
client Controller; four native readiness/storage checks pass with no business HTTP.
Retain the initial setup failures: PostgreSQL 18 data-mount layout, fixture configuration
encoding/schema mistakes, waiting for a signed-in workbench before enrollment, and reading a
stale capability snapshot. One native enrollment attempt fails after registration and before
the connection profile is written; its cause is unclassified. Reconciliation uses the same
invitation, installation and device key, without a replacement identity or authorization.
The successful restart does not establish the cause of that earlier failure.

Two Mac-to-work2 node-managed maximum-result runs each pass six checks: 320 encrypted 32 KiB
chunks, verified 10,485,760-byte result, one real approved fill, durable completed write records,
normal release and unrelated reads. The result is still **synthetically padded after the
native fill**, not a 10 MiB MCP output. The next read is capacity-refused before native dispatch;
the delivered blocked answer is not workflow success. Descriptor-ready to next same-task
execute takes 25,118 and 25,394 ms, including Gateway verification and subsequent scheduling.
Both fit the unchanged transfer budget; two samples are not WAN percentiles.

Private traces match all 320 chunk RPCs per run and identify the actual node-managed helper
configuration. In the first sample, mean request/send authorization durations are 56.69/82.67 ms;
these overlap and include local waiting, network and proof verification, so they are neither
summed nor labeled RTT. The prior local-test failures remain failures. The native runners now
accept `SPARKCLAW_CONTROLLER_NODE_MANAGED=1` only without local-test environment selection,
reopen the profile saved by native enrollment, and assert the observed production mode.
The analyzer resolves that selected helper; the independent receipt reader supports
`--cross-host` without comparing clocks or claiming cross-host timestamp ordering.

Both samples' stopped Gateway records and Mac journals independently preserve the original
task/digest and completed write; enclosing executions are delivered. The isolated Gateway,
issuer and forwarder are stopped, Mac candidate processes and SSH forwarding are closed,
and database/history/profiles remain retained. Production configuration, mounts, health and
runtime start times match the **post-recovery** baseline, with 7,867,572,224 bytes free; the
earlier production PostgreSQL incident remains recorded. Installed Linux node-managed UI,
TEST1, remaining fault/offline/WAN gates, paired cutover and D2 deletion remain open.


A fresh installed-Mac diagnostic reproduces first enrollment failing private runtime validation.
The unchanged helper writes its runtime configuration at 19:38:42.009 UTC, before the original
signed Grant's NotBefore of 19:38:42.038371808 UTC; an immediate read-only check starts at
19:38:42.042 and succeeds. This establishes the activation race for this new reproduction;
it does not reconstruct the overwritten timestamps of the earlier attempt. The same signed
result, native key and pending identity remain retained.

The helper now waits for that already authenticated Grant's NotBefore within the original
enrollment context, then performs the unchanged strict runtime checks. Cancellation/deadline
publishes no new connection profile; retry reuses the original encrypted pending result,
installation, device and Grant. There is no clock-skew allowance, re-registration, widened
validity or UI change. Two regressions reproduce the failure before repair; the helper package,
existing expiry recovery and focused race checks pass, as do node-client/workbench packages,
full Go build and vet. Initial test-fixture permission/encrypted-storage assumptions remain
recorded separately. Refreshed packaged/native verification is still pending.

A separate unchanged Linux native-keyring attempt completes the helper enrollment and writes
a valid connection profile, but fails subsequent desktop activation. Preserve its original
invitation/device and failure evidence; this is a different boundary and is not qualified by
the Mac activation repair. Linux node-managed native acceptance remains open.
