# Client-embedded Browser Controller over ISCP

> Language: English | [简体中文](../zh-cn/docs/desktop-embedded-browser-controller-iscp-design.md)
>
> Date: 2026-11-09. Status: proposed design, not implemented; owner decisions D1–D3 recorded on
> 2026-11-09 (§12). Source baseline: `main` at `b5c21e14`; its browser, ISCP and desktop sources are
> identical to `cddda7d1`.
> Scope: generic browser automation for SparkX clients connected through ISCP. Mail, provider
> scripts and AI chat export are explicitly out of scope and keep their current paths.

## 1. Decision

Keep the existing control chain and change only where one hop runs and how it is carried:

```text
Gateway browser tools
  -> browserhost ScopedAdapter (unchanged tool semantics)
  -> PlaywrightExtensionAdapter (unchanged)
  -> browsercontrol ControllerClient        <-- new ISCP implementation
  -> ISCP reverse browser.controller.*      <-- new transport, same authenticated device session
  -> SparkX main process forwarder (fixed routes only)
  -> packaged Browser Controller (same source, generic MCP lane only)
  -> Playwright MCP
  -> Electron Browser Adapter (existing, now started in production)
  -> task WebContentsView debugger
```

The Controller, Playwright MCP and Electron adapter keep their code and contracts. ISCP replaces
only the owner-local Unix socket between Gateway and Controller. The cut is placed at the
Gateway–Controller hop because that hop costs one round trip per tool call. Tunnelling the
Playwright–Electron CDP hop instead was rejected: CDP needs many serial round trips per action
and emits hundreds of events per page load, and it would give the remote host arbitrary
`Runtime.evaluate` on task pages that share the personal login Session.

## 2. Current baseline on `main`

| Role | Path today |
|---|---|
| Backend acquisition (work2) | `AcquisitionAdapter` → `BackendHost` → `PlaywrightExtensionAdapter` → `browsercontrol.Service` → `HTTPControllerClient` over the Controller Unix socket → work2 Controller → backend browser |
| Client embedded over ISCP | `Broker.ForScope` → `ScopedAdapter` → `Broker.Dispatch` → reverse `browser.command` → `host-agent.mjs` → `native-page-commands.mjs` |

The client path has no Playwright. `native-page-commands.mjs` implements eight operations
(navigate, read, snapshot, click, fill, select, screenshot, wait) with `executeJavaScript`:
at most 120 controls, `HTMLElement.click` instead of input events, no frame or popup handling,
and screenshots fail above 39 KiB. The backend and client therefore behave differently for the
same model tool, which contradicts the equivalent-semantics requirement (A08) of the
[client/backend R3 design](client-backend-architecture-design.md).

Relevant facts the design depends on:

- `browsercontrol.ControllerClient` already abstracts the Controller (`Acquire`, `Execute`,
  `Release`, `RunScript`, `OpenProviderLogin`, `ValidateToken`); `PlaywrightExtensionAdapter`
  consumes it through `Status` and `AcquireSession`.
- `browserhost.BrowserHostAdapter` is the role-explicit boundary used by both `Broker`
  (client embedded) and `BackendHost` (backend acquisition).
- The Controller's generic lane accepts `page.click/fill/info/navigate/read/screenshot/select/
  snapshot/type/wait` and `tabs.close/handoff/list/new`, plus export operations
  `ai_chat.export` and `ai_platform.check`. It holds one active session at a time.
- `ElectronAdapterServer` (`apps/desktop/src/browser/adapter-server.mjs`) is started only for
  Linux qualification; macOS production never starts it. Playwright through the adapter has
  therefore never run on macOS.
- Every ISCP workbench operation is limited to 63,488 bytes per message, the ISCP call times out
  at 30 seconds and desktop browser commands use a 25-second deadline. Production reads over the
  TEST1 Relay measured 219–537 ms per call ([public ISCP record](public-iscp-node-deployment.md)).
- Electron 44.4.3 embeds Node 24.21.0; the Controller package declares `node >=26 <27`.

## 3. Scope

In scope:

- Packaging the Browser Controller generic lane and Playwright MCP in SparkX (macOS and Linux).
- ISCP operations, Gateway client and desktop forwarder for that lane.
- The model browser tools served by `ScopedAdapter` today: `browser.status`, `open`, `navigate`,
  `read`, `snapshot`, `click`, `type`, `select`, `screenshot`, `wait`, `list_tabs`, `focus`,
  `close`.
- Every SparkX client role, including the Linux client colocated with work2 (D3).
- Removal of the old client path: reverse `browser.command`, `native-page-commands.mjs` and the LAN
  direct browser host (D2).

Out of scope (unchanged, not packaged or not exposed on the client):

- `RunScript`, provider scripts and App-CLI runtime; QQ/Gmail/Outlook probes and sends.
- Mail observers (`/v1/mail-observers/*`) and resident mail collection on work2.
- `OpenProviderLogin`, `ai_chat.export`, `ai_platform.check`.
- `tabs.handoff` (already rejected for Electron task pages).
- The backend acquisition role, work2 Controller service and its Unix socket. After D3 they serve
  only backend acquisition; no client task uses them.

## 4. Gateway changes

### 4.1 `ISCPControllerClient`

A new `browsercontrol.ControllerClient` implementation bound to one ISCP session. It maps:

| Method | ISCP reverse operation | Notes |
|---|---|---|
| `Acquire` | `browser.controller.acquire` | Sends no token; the desktop injects its local token |
| `Execute` | `browser.controller.execute` | Only the generic operations listed in §3 |
| `Release` | `browser.controller.release` | |
| status | `browser.controller.status` | Readiness and pinned versions only |
| `RunScript`, `OpenProviderLogin`, `ValidateToken` | none | Return a typed `browser_operation_unavailable` without a call |

`ReverseSession` gains an operation-named call (today it only sends `browser.command`).
Responses larger than one message are fetched with `browser.controller.chunk` (§6.2) and
reassembled before the 10 MiB response limit already enforced by `HTTPControllerClient`.

### 4.2 Session-bound controller service

`browsercontrol.Service` requires the Gateway Vault's Bridge token. The client route instead uses
a session-bound implementation of `PlaywrightController` (`Status`, `AcquireSession`) with no
Vault: the credential generation is the ISCP `authorization_revision`, and the controller
generation is the one reported by the desktop. One instance exists per ISCP session and closes
with it.

### 4.3 `ClientControllerHost`

A `BrowserHostAdapter` with role `client_embedded` that wraps
`PlaywrightExtensionAdapter(cfg, sessionBoundService)`, in the same way `BackendHost` wraps the
backend adapter. Bindings are derived from the ISCP session exactly as today
(`connection_epoch` = session ID, `runtime_generation` = `iscp_<session ID>`, `host_id` and
`authorization_digest` hashes), so `ScopedAdapter`, the frozen-target route rule and fence
handling stay unchanged. `Broker.ForScope` returns `ScopedAdapter(ClientControllerHost)` for every
ISCP client session. There is no runtime selector and no fallback: the release is paired, a
controller failure is reported as such, and a client that does not advertise
`browser.controller.v1` gets the browser capability as unavailable, with its original entry
disabled.

## 5. Desktop changes

### 5.1 Packaging

- A build script produces `apps/desktop/bin/browser-controller/`: the Controller source subset,
  production `node_modules` for `playwright`, `playwright-core` and `@playwright/mcp` installed
  with `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`, the existing `install-playwright-downloads.mjs`
  patch applied at build time and verified by hash, and a file-hash manifest.
- `electron-builder` copies it to `extraResources/browser-controller` (outside asar, like
  `iscp-workbench`). No browser binary is downloaded; Electron's bundled Chromium is the only
  engine.
- `@playwright/cli`, App-CLI runtime, provider scripts and userscripts are not packaged.
- Both the macOS packages (`dist:mac-arm64`, `dist:mac-x64`) and the Linux ARM64 packages
  (`package:desktop`) include the tree; the work2 colocated desktop installs the same Linux
  package (D3).
- `audit-package.mjs` allowlists the tree; the release manifest records Controller, MCP and
  Playwright versions with hashes. Expected size: about 20 MB.

### 5.2 Client Controller entry

A new `tools/browser-controller/src/client-main.mjs` assembles `BrowserController` with only the
MCP factory and the Electron adapter configuration. It does not import App-CLI, the script
factory or the mail observer feed, and its HTTP server exposes only health, acquire, execute and
release. `controller.mjs`, `mcp-client.mjs` and `electron-adapter-client.mjs` are reused as is.

### 5.3 Process and adapter lifecycle

- The main process starts the Controller on the first `browser.controller.acquire`, using the
  existing `ELECTRON_RUN_AS_NODE` child pattern (as local preview does). Playwright MCP is spawned
  by the Controller through `process.execPath` with the same variable.
- A private runtime directory under a short path (`/tmp/sparkx-<uid>/bc-<random>`, mode 0700)
  keeps socket paths under the 104-byte macOS limit; sockets are 0600.
- `ElectronAdapterServer` starts in production on both platforms with a socket and secret in that
  directory. Its `qualification*` operations stay disabled outside qualification.
- The Controller token and adapter secret are generated per start, held by the main process and
  never sent to Gateway, the renderer or remote pages.
- If the Controller or MCP exits, the main process bumps the generation, closes its task pages and
  reports in-flight writes as unknown. It restarts only on the next acquire.
- ISCP session loss, device revocation or authorization change stops admission, releases leases
  and cancels in-flight calls, as for `browser.command` today.

### 5.4 Reverse forwarder

`iscp-transport.mjs` replaces `browser.command` with the `browser.controller.*` operations in its
reverse whitelist; anything else is still refused with 403 without ending the session. The forwarder:

- validates each body against an exact schema and maps it to one fixed Controller route (no path,
  URL, script or CDP field is accepted);
- rejects export operations and `tabs.handoff` before reaching the Controller;
- injects the local token on acquire;
- journals every mutating execute in the existing `HostJournal` by command ID and argument digest
  before forwarding, so `browser.receipt` and `browser.reconcile` work unchanged (the journal moves
  from `host-agent.mjs` to the forwarder when the old path is deleted);
- applies the 25-second desktop deadline (§6.1);
- binds acquired task pages to the conversation and task from the reverse body, so they appear in
  the existing browser panel as `browser.command` pages do now.

## 6. Limits, timeouts and outcomes

### 6.1 Deadlines

| Item | Value |
|---|---|
| ISCP call timeout | 30 s (unchanged) |
| Desktop forwarder deadline | 25 s, so the desktop's own outcome arrives first |
| Client Controller navigation timeout | 20 s (`SPARKCLAW_BROWSER_NAVIGATION_TIMEOUT_MS`) |
| Client Controller action timeout | 10 s (current default) |
| `page.wait` | at most 15 s per call |

A read that times out or returns `outcome_unknown` fails and may be retried by the model. A write
(`page.click/fill/navigate/select/type`, `tabs.new/close`) enters an unknown fence keyed by its
command ID and digest and is never resent. Only a refusal before execution (no session, or the
desktop's own 403/503) proves that a write did not run.

### 6.2 Large results

Results up to the message budget are returned inline. A larger result is held by the desktop in a
bounded buffer (8 MiB total, 60-second lifetime, released on read completion or session end) and
the reply carries `result_id`, size and SHA-256. Gateway reads it with
`browser.controller.chunk` (read-only, offset and length, up to four chunks in flight) and
verifies the hash. This removes the current 39 KiB screenshot limit. Chunk throughput is measured
in P0; if it is too slow, screenshots switch to JPEG at a fixed quality rather than a new channel.

### 6.3 Concurrency

The Controller holds one active session. Concurrent browser tasks on one client queue at acquire
up to the Controller's wait timeout; today's `Broker` allows several task pages per host. The owner
accepted this queueing (D1). A task whose wait expires fails with the Controller's existing busy
error; it is not admitted to a second session.

## 7. ISCP operation registry

New entries in `services/gateway/internal/iscpworkbench/operations.json`, projected to the desktop
by `scripts/sync-iscp-operations.mjs`:

| Operation | Direction | Scope | Mutation | Recovery |
|---|---|---|---|---|
| `browser.controller.status` | reverse | `browser.control` | no | read |
| `browser.controller.acquire` | reverse | `browser.control` | yes | operation_receipt |
| `browser.controller.execute` | reverse | `browser.control` | per operation | operation_receipt |
| `browser.controller.release` | reverse | `browser.control` | yes | operation_receipt |
| `browser.controller.chunk` | reverse | `browser.control` | no | read |

All use `limit_bytes` 63,488 and capacity class `business`. `browser.receipt` and
`browser.reconcile` are reused. The `browser.command` entry is removed in the same release (D2).
Gating stays on the Gateway `browser` capability family and the desktop `capabilities.browser`
switch.

## 8. Security

- Authority is the ISCP device session, as for `browser.command`. No new grant, heartbeat or poll.
- Remote input reaches Playwright only through the Controller's validated generic operations; the
  model still receives no selector, JavaScript, CDP or Playwright code tool.
- Task-page isolation remains in the Electron adapter: task-scoped debugger, no personal or
  workbench targets, blocked global cookie access, stale-generation rejection.
- The Controller and adapter listen only on private sockets; the renderer and remote pages cannot
  reach them.
- Packaged files are hash-pinned and audited; the Controller cannot load code from outside its
  directory.

## 9. UI

No page, menu, layout, style or interaction changes. The browser panel, task tabs, read-only task
views, explicit selection and the unknown-write notice with its reconcile actions stay as they are.
If the packaged Controller is unavailable, the existing browser entry is disabled. Failure and
recovery messages appear only when the user must act. No permanent technical status is added.

## 10. Phases

| Phase | Work | Exit evidence |
|---|---|---|
| P0 Feasibility | Run the Controller under Electron Node 24.21 (API audit against `>=26`); start the adapter on macOS; run MCP read/fill/click/snapshot/screenshot on a fixture page; check `BrowserController` without a script factory; measure one ISCP reverse round trip and chunk throughput on Mac and work2 | Fixture passes on installed Mac build; recorded latency figures |
| P1 Packaging and local process | Build script, `client-main.mjs`, process supervision, production adapter start, audit and manifest | Installed DMG passes the Controller fixture locally with no network |
| P2 ISCP transport | Registry entries, `ISCPControllerClient`, session-bound service, `ClientControllerHost`, forwarder, journal, chunks | Local Docker Relay isolation tests; injected loss leaves one effect and one unknown fence; zero direct HTTP/WS |
| P3 Native acceptance | Installed Mac and Linux clients with real work2 through TEST1 | Gates G1–G4 of the [ISCP expansion design](desktop-iscp-capability-expansion-design.md); fixed scenario latency compared with `browser.command`; original UI interactions exercised |
| P4 Cutover | Paired Gateway and client release (Mac clients and the work2 Linux desktop together); delete the old path per D2 | Source scan finds no remaining reference; whole-release rollback recipe recorded |

Each push, deployment and installation requires separate owner authorization.

## 11. Acceptance

- **Business:** on a real page, open, snapshot, fill, click, select and screenshot through the new
  path, with verified page state; frames and popups covered by Playwright pass where the current
  path fails.
- **Recovery:** loss of a write response, Controller crash, MCP crash, ISCP reconnect, Gateway
  restart and device revocation each leave no repeated effect and a correct fence.
- **Transport:** no direct business HTTP/WS from either process; with the Relay blocked, calls fail
  without fallback.
- **Isolation:** no personal, workbench or foreign task target is reachable; export, script and
  handoff operations are refused at the forwarder.
- **UI:** original panel, tabs, selection, hiding, widening and reconcile actions behave as before.

## 12. Decisions

Recorded from the owner on 2026-11-09.

| ID | Decision | Consequence |
|---|---|---|
| D1 Concurrency | Accept one active browser task per client; others queue at acquire | No Controller change; a queued task that waits past the timeout fails with the existing busy error |
| D2 Old path | Delete it after P3 | Removed together: reverse `browser.command` (registry entry, `Broker` reverse dispatch, desktop reverse handler), `native-page-commands.mjs`, and the LAN direct browser host (the `/api/v1/browser/hosts` grant, connect, revoke and fences routes, `Broker.ServeHost`/`IssueGrant`, and the desktop `host-socket.mjs`). Over a LAN-only connection browser automation is unavailable and its entry is disabled. `browser.receipt`, `browser.reconcile` and `HostJournal` remain |
| D3 Linux client | The work2 colocated desktop also uses the packaged Controller over ISCP | The work2 systemd Controller serves only backend acquisition; Linux packages carry the Controller and are qualified in P3 |
