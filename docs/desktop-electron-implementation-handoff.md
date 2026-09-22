# Electron Desktop Browser Implementation Handoff

> Language: English | [简体中文](../zh-cn/docs/desktop-electron-implementation-handoff.md)
>
> Updated 2026-09-21 (Asia/Shanghai). All four implementation phases that can be completed without owner credentials, target-hardware acceptance, or release authorization are implemented and qualified. The candidate has not been deployed or cut over.

## Resume Here

Workspace: `/home/infinimesh/Documents/SparkClaw-workbench-page`. Branch at handoff: `codex/sparkclaw-workbench-page`; HEAD: `718e1e720f413d46a405016228842560e244d4ae`. Inspect and preserve the uncommitted changes before editing; do not reset or overwrite them.

Read root `AGENTS.md`, this handoff, and the [desktop client and embedded browser design](desktop-client-embedded-browser-design.md). The design holds the full requirements; this handoff establishes the implementation starting point. Follow `AGENTS.md` to read the InfiniCenter cluster registry, inbox, and proposed decisions. There were no active SparkClaw letters or proposed decisions at handoff; check again on resumption.

Proceed with the accepted architecture without asking again about Electron, repository separation, or task-page takeover. Raise newly discovered product decisions while continuing independent work. Committing, pushing, deployment, real email sends, and deleting old profiles are not current tasks.

## Accepted Requirements

1. **Electron's bundled Chromium is the sole engine**, with `WebContentsView` for personal and task pages. Chromium manages renderer/helper processes; there is no one-process-per-tab requirement.
2. **One repository, separate responsibilities**: reuse `apps/webchat`; add `apps/desktop` for main process, browser adapter, script host, and packaging. WebChat uses an optional desktop capability and remains functional in ordinary browsers.
3. **Preserve the control approach**: Gateway → Browser Controller → Playwright MCP / fixed CLI scripts → task-scoped Bridge. Port Bridge behavior through an Electron Browser Adapter; do not assume Chrome extensions or Tampermonkey work unchanged or expose arbitrary JS/CDP tools to the model.
4. **My browsing / Task observation in the right panel**: personal pages are interactive; task pages are read-only, without takeover/handoff/resume. Users may select observed task tabs without changing automation targets, leases, task viewport, or focus. Separate WebContents and ownership registries distinguish personal and task pages.
5. **Shared website login**: personal and task pages share a dedicated persistent Electron browser Session; the privileged workbench has another Session. Establish the new Session through normal website login. Do not reuse/copy/export the old Chromium profile, cookies, or passwords. Account switching/logout requires existing task account checks.
6. **Login clarification**: the current workflow signs into websites and retains cookies; it does not obtain OAuth API tokens for SparkClaw. OAuth embedded-agent policy alone does not prove failure. Actual Electron website login and persistence remain untested. Test providers individually; do not silently change engines, switch to API workflows, or import cookies.
7. **Lifecycle**: closing the workbench hides it and preserves runtime/pages; reopening uses the same runtime. Main-process failure or explicit quit may interrupt tasks. Revoke generations and report honestly; never replay effects with unknown outcomes automatically.
8. **Retired routes**: external Chromium embedding, `surface-host`, X11 reparenting, dedicated/nested X servers, Xephyr/Xpra, browser streaming, CEF, and custom Chromium are neither stages nor fallbacks. Xvfb for isolated tests is not product architecture.

Retain the design's 1440×900 initial and 1180×720 minimum window sizes; sidebar 244 px, conversation minimum 520 px, divider 6 px, browser default 640 px (480–760 px). Insufficient space uses browser focus layout without resizing an active task viewport.

## Completed And Outstanding Work

Completed before implementation: bilingual architecture and related runtime/component/migration documents specify the sole Electron route. The retired X11 Helper has a diagnostic-only, process-owned-Xvfb qualification covering an Electron anchor's identity, focus, clipping, and detach/crash behavior.

**Implementation checkpoint (2026-09-21)**: `apps/desktop` pins Electron `44.4.3` with ARM64 Chromium `152.0.7977.130` and Electron Node `24.21.0`. The product uses sandboxed `WebContentsView` pages, separate workbench/browser Sessions, opaque ownership and generation records, task-scoped `webContents.debugger`, and an owner-only Unix socket with expiring single-use credentials. The existing Controller can opt into the adapter while retaining its extension path. The managed script host installs the exact QQ/Gmail/Outlook readers at document start and the fixed AI exporter with bounded GM storage/menu capabilities. Task and owner downloads have explicit ownership, limits, cancellation, cleanup/receipt or UI semantics.

The optional WebChat desktop capability now provides “My browsing / Task observation”, bounded personal navigation, read-only task selection, permissions and downloads. Main-process IPC validates the exact workbench frame, custom origin, opaque references, generation/revision, and bounds. Packaged resources use `sparkclaw-app://`; the same-origin proxy injects the installation-provisioned Client only toward the controlled loopback Gateway and covers HTTP/SSE/files, while speech uses that same configured origin. Close-to-hide, workbench/task/main-process failures, browser-session persistence, and no replay of unknown effects are qualified.

The isolated runner exercises actual MCP `0.0.80`, CLI `0.1.19`, and Playwright `1.63.0-alpha-2026-08-31`; worker/OOPIF/popup scoping; the complete software-testable native-input boundary; background execution; fixed `640×720` task viewports; managed scripts; downloads; and lifecycle recovery. ARM64 AppImage and DEB candidates, SHA-256 records, and independently extracted packaged-app probes are complete. See the [desktop release and cutover plan](desktop-release-plan.md).

**Owner acceptance remains**: real provider website login, persistence and account switching; real GPU/DPI/native-IME/audio/video and operating-system microphone permission; clean target-host install/update/uninstall; branded application icon; live Gateway/controller smoke; and explicit production cutover authorization. These are acceptance/release gates, not silently claimed by Xvfb fixtures. No production service, old browser profile, or login data was changed.

Uncommitted changes to preserve include:

- Bilingual design, index, changelog, and runtime documentation; the new main design is still untracked.
- `scripts/install-browser.sh`, `scripts/sparkclaw-browser-launcher.sh`, `scripts/test_browser_bridge.py`, and `tools/browser-bridge/test/relay-connection.test.mjs`: preceding standalone Chromium debugger-banner/lifecycle changes, not Electron implementation.
- `.gitignore`, untracked `tools/browser-surface-host/`, and its Chinese mirror: historical diagnostic code only. Run its tests only through the checked-in Xvfb runner; do not connect it to production.

Environment observed: Linux ARM64, host Node `v26.2.0`, npm `11.17.0`, Electron `44.4.3`, bundled Chromium `152.0.7977.130`, and Electron Node `24.21.0`. Xvfb is used solely for disposable qualification. The historical anchor fixture and the product runtime currently use the same Electron version but retain separate purposes and dependency scopes.

## Code Entry Points

Paths below are relative to the repository root.

| Entry | Current behavior and follow-up |
|---|---|
| `package.json`, `apps/desktop/package.json`, `apps/webchat/package.json` | The root now includes desktop and Bridge workspaces plus explicit desktop development/test/qualification commands; production startup is unchanged |
| `apps/webchat/src/App.tsx`, `apps/webchat/src/desktop/` | Ordinary WebChat retains its existing inspector; the optional typed desktop capability renders personal browsing, read-only task observation, downloads, and permissions |
| `apps/webchat/src/api/client.ts`, `apps/webchat/src/audio/realtimeSpeech.ts` | Ordinary-browser URLs remain unchanged; packaged desktop requests use the authenticated same-origin custom-scheme HTTP/SSE/file proxy and the configured speech WebSocket base |
| `apps/desktop/src/main/desktop-capability.mjs`, `owner-browser-services.mjs` | Exact-frame bounded IPC, layout, personal permissions, and owner download tracking; no arbitrary CDP/source/process capability reaches WebChat |
| `tools/browser-controller/src/mcp-client.mjs`, `electron-adapter-client.mjs` | MCP registers an exact task/generation binding and token hash before launching; the optional Electron path uses a single-use credential while the extension path remains available |
| `tools/browser-controller/src/cli-client.mjs`, `cli-task.mjs` | CLI and personal-page launch paths use the same optional adapter registration and have been qualified independently from MCP on the local fixture |
| `apps/desktop/src/browser/protocol.mjs`, `adapter-server.mjs` | The Electron channel declares its own runtime kind/version/generation, owner-only socket/secret files, strict request schemas, and one-use credentials; it does not impersonate the extension |
| `tools/browser-controller/src/mcp-tabs.mjs`, `cli-page-guards.mjs` | The internal Electron connection URL is recognized without relaxing task topology or adopting personal pages |
| `tools/browser-bridge/src/relay-connection.mjs` | The Electron facade reuses the scoped relay with handoff disabled, focus-changing `bringToFront` inert, and direct global target operations denied |
| `tools/browser-bridge/src/protocol.mjs` | Handoff/background-input markers exist; desktop task handoff must be rejected and background automation must not steal focus |
| `tools/browser-controller/src/extension-downloads.cjs`, `install-playwright-downloads.mjs` | Existing pinned Playwright download patch requires Electron integration with bounded artifacts, cancellation, and receipts |
| `tools/browser-controller/src/provider-scripts.mjs`, `scripts/email/userscripts/`, `tools/browser-userscripts/` | Script registry, sources, and bundles: migrate document-start/page-world hooks, matching, readiness, hashes/revisions, and exporter GM capabilities |
| `tools/browser-controller/test/fixtures/adapter-live.html` | Reusable local fixture; fake MCP/CLI fixtures are unit-test aids, not actual client compatibility evidence |

Current compatibility baseline: Bridge `1.0.26`, MCP `0.0.80`, CLI `0.1.19`, Playwright `1.63.0-alpha-2026-08-31`. The standalone Chromium pin does not apply to Electron; recheck installed dependencies when implementing.

## Implementation Order

### 1. Minimal Runtime And Actual Control Proof

- [x] Add the desktop workspace, pin Electron, and provide isolated-display/disposable-user-data tests. Preserve running production services and browsers.
- [x] Implement main-owned page registries binding owner, role, task/session, and runtime/page generations. Sandbox remote pages, enable context isolation, disable Node integration, and separate workbench Session/preload.
- [x] Implement an authenticated owner-local channel and launcher with explicit runtime identity, task/generation binding, and single-use credentials. Keep secrets out of pages/logs. Existing relay method names may remain transport envelopes; no browser-wide debugging port.
- [x] Translate task-scoped attach/detach/sendCommand/events to `webContents.debugger`; qualify worker, OOPIF and popup scoping plus foreign/stale/personal/global-target rejection.
- [x] Use **actual pinned MCP and CLI** to create/navigate/read/snapshot/fill/click/screenshot/open/close pages and tear down. Both lanes run against the local fixture rather than facade mocks.
- [x] Prove the software-testable native-input boundary: pointer, keyboard/IME key sequence, shortcuts, paste, drag/drop, context menu, dialogs and DevTools are blocked while approved automation remains functional. Final native IME/hardware behavior remains an owner gate.
- [x] Verify observation switching preserves the exact opaque target reference and fixed `640×720` viewport while hidden/background automation continues.
- [ ] Qualify normal provider website login and Session persistence early. Local website-cookie sharing passes; real providers remain untested and require owner-entered credentials.

**Delivery for this phase**: a minimal running Electron runtime, both actual control lanes, and isolation/read-only evidence. Opening a web page in a shell is not completed compatibility qualification.

### 2. Scripts, Accounts, And Downloads

- [x] Install QQ/Outlook/Gmail readers with exact origin/frame matching, document-start main-world injection, readiness/version reporting, and fixed hashes.
- [x] Implement scoped exporter `GM_getValue`, `GM_setValue`, and `GM_registerMenuCommand` persistence/menu semantics without filesystem or arbitrary IPC access.
- [x] Generate and check the exact script registry, revisions and hashes; preserve existing account checks and provider gates.
- [x] Bind task downloads to the exact connection with size/count limits, cancellation, source cleanup and receipts; expose separately scoped owner downloads through bounded UI references.
- [x] Qualify shared persistent login state and failure-closed task boundaries on local fixtures. Real provider login/account mismatch/challenge effects remain owner-approved acceptance work.

### 3. Workbench, UI, And Lifecycle

- [x] Add optional typed capabilities under `apps/webchat/src/desktop/`, personal browsing controls, downloads/permissions, and read-only task selection while ordinary WebChat remains functional.
- [x] Validate IPC sender, origin, role, opaque refs, generation, bounds, and revision in main. Workbench cannot issue arbitrary CDP, executable source, or process commands.
- [x] Resize personal pages with layout; preserve the task viewport and report insufficient space. Observation cannot invoke handoff, task focus/control, or debugger rebinding.
- [x] Implement packaged resource serving, installation-provisioned Gateway HTTP/SSE/file authentication, and speech WebSocket connectivity without workbench pairing. Microphone requests are allowed only for the trusted workbench audio frame; real hardware/OS permission remains an acceptance gate.
- [x] Implement close-to-hide, same-runtime reopening, persistent personal Session, and distinct workbench/task/main failure handling with generation invalidation and no replay of unknown effects.

### 4. Packaging And Cutover Preparation

- [x] Produce ARM64 `.deb`/AppImage, checksums and dependency/version records; document install/update/uninstall policy and independently extract and launch both artifacts. Real target-host GPU/DPI/IME/audio/video acceptance remains outstanding.
- [x] Prepare the atomic installer/service/`open:browser`/login/documentation migration procedure without adding an engine selector or automatic fallback. It has not been executed.
- [x] Deliver the evidence and concrete [release/cutover plan](desktop-release-plan.md). Deployment, old-profile deletion, and real effects remain excluded; rollback is an explicit whole-release operation under later authorization.

## Tests And Desktop Failure Boundaries

Previous X11 experiments manipulated visibility, reparenting, focus, and stacking on the active `DISPLAY=:1`. During those runs, the system journal logged repeated disposed `MetaWindowActorX11` state, stage/allocation failures and a Clutter assertion, followed at `2026-09-21T10:10:40.586013+08:00` by `GNOME Shell crashed with signal 11`. Xorg later ended cleanly, with no surrounding NVIDIA Xid, GPU reset, or OOM evidence. The immediate failure was therefore a Mutter/GNOME Shell compositor crash triggered through the active-display window-manipulation path, not a compile or demonstrated GPU failure. The logs do not isolate the exact X11 operation or defective Mutter code path. A later revision also manipulated a window-manager frame directly, but it postdates the recorded crash and is only additional evidence that the test path was unsafe.

Run failure injection on an independent display with disposable user-data, local fixtures, and controlled child processes. Xvfb is allowed for testing; an environment variable claiming isolation is not proof. Never run window-moving/hiding/reparenting/killing tests on the user's active `DISPLAY` or manipulate their desktop WM. The retired surface host may run only through its checked-in runner, which creates and verifies its own Xvfb process. Cleanup must target only test-created processes/files. App failures must be recoverable without Linux logout.

Required evidence covers actual MCP+CLI; concurrent task/personal isolation; stale/nested-target rejection; blocked user input with working automation; observation target invariance; background execution/fixed viewport; early script injection/GM; website login/account switching; downloads; close-to-hide; crash invalidation/no effect replay; packaged connectivity; and ordinary WebChat regressions. Passing one item does not establish the others.

Existing regression commands, after checking their environment; these do not qualify Electron:

```sh
npm test --prefix tools/browser-bridge
npm test --prefix tools/browser-controller
npm run test:email-scripts
npm run test:webchat
npm run build:webchat
npm run test:desktop
npm run qualify:desktop
npm run package:desktop
npm run qualify:desktop-artifacts
git diff --check
```

Historical native-window boundary diagnostics, isolated from the live Bridge and
production profile:

```sh
npm ci --prefix tools/browser-surface-host/test/electron-anchor
make -C tools/browser-surface-host test
make -C tools/browser-surface-host test-electron-anchor
```

New Electron tests/CI must specify isolated displays and temporary data directories. Run Gateway tests appropriate to actual changes. Use the Docs mirror/link check in `.github/workflows/ci.yml`; new Markdown documents need English/Chinese counterparts.

## Remaining Owner Acceptance And Release Gates

The implementation questions are resolved by automated evidence. Final acceptance still requires owner credentials or physical/release authority: provider-by-provider website login, persistence, logout/account switching and any human challenge; real GPU/DPI/native IME/audio/video/microphone behavior; a clean target-host install/update/uninstall; branded icon approval; live Gateway/controller smoke; and the explicit go/no-go for production cutover. Failures must record exact versions and affected gates without switching engines, importing an old profile, or silently reducing scope.

InfiniCenter cluster path: `/home/infinimesh/InfiniCenter/clusters/ProjectGroup-2/`. Update `status/sparkclaw.md` when finishing a stage. Preserve SparkClaw--JingSi Runtime v1 and SparkClaw--IMMS evidence v2 contracts; cross-project interface changes require an accepted decision first. No current interface change requires another project's participation.

## Suggested New-Conversation Prompt

> Read root AGENTS.md, docs/desktop-electron-implementation-handoff.md, and docs/desktop-client-embedded-browser-design.md. Inspect and preserve the uncommitted work, then implement phase 1 of the handoff. The sole architecture is Electron bundled Chromium + WebContentsView, reusing WebChat and the existing Controller/Playwright control chain. Do not restore X11 embedding, run destructive active-desktop tests, or switch production. The architecture is already accepted; raise newly discovered questions and report actual implementation/test evidence without treating unverified capabilities as complete.
