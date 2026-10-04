# Desktop Release And Cutover Plan

> Language: English | [简体中文](../zh-cn/docs/desktop-release-plan.md)
>
> Candidate status: rebuilt, package-qualified, and installed on the GB10 Linux desktop on 2026-10-04 (Asia/Shanghai). This plan does not authorize deleting a profile or performing real provider effects.

The [shared local backend design](local-shared-backend-design.md) is included in
this candidate: LAN Web and local desktop use one workbench port without
`18795`, with separate Client credentials. Shipping or switching a production
deployment still requires the acceptance and authorization below.

## Candidate Identity

The candidate is Linux/ARM64 SparkX Desktop `0.1.0`, built with Electron `44.4.3`, bundled Chromium `152.0.7977.130`, and Electron Node `24.21.0`. Its control compatibility baseline is Browser Bridge `1.0.28`, Playwright MCP `0.0.80`, CLI `0.1.19`, and Playwright `1.63.0-alpha-2026-08-31`.

| Artifact | Bytes | SHA-256 |
|---|---:|---|
| `SparkX-0.1.0-linux-arm64.AppImage` | 126,684,368 | `04101a9bd513f7e4601cd0f3532f89eadc511350512330cc4aa6b11da544fc29` |
| `SparkX-0.1.0-linux-arm64.deb` | 94,403,348 | `dfe304a3af233b70fd73e3eebbb19687007c9f1d223619b0487f2d1d671fb889` |

The generated sources of truth are `apps/desktop/dist/release-manifest.json` and `apps/desktop/dist/SHA256SUMS`. Rebuilding changes the hashes; regenerate this table before release. The packaged icon and target-host desktop entry still require owner acceptance.

## Evidence Already Complete

- `npm run qualify:desktop` uses a runner-owned Xvfb and disposable user data. It covers actual MCP and CLI, task/personal isolation, nested targets and popups, native input exclusion, approved automation, downloads, managed scripts, background execution, close-to-hide, renderer/main crashes, persistent browser state, generation invalidation, and no automatic replay of unknown effects.
- `npm run qualify:desktop-artifacts` checks both hashes, DEB architecture, and the stable `sparkclaw` package identity, independently extracts each artifact, launches the packaged app without launcher-provided connection environment variables, and proves owner-only connection discovery, packaged WebChat, bounded desktop IPC, installation-provisioned Gateway identity plus HTTP/SSE proxying, speech WebSocket Origin rewriting, and browser-panel creation.
- On GB10, the DEB upgraded the existing `sparkclaw` package in place, installed `/opt/SparkX/sparkx`, removed the superseded `/opt/SparkClaw` root, preserved `~/.config/@sparkclaw/desktop`, and opened an X11 window titled `SparkX Workbench` with renderer sandboxing enabled.
- `npm run qualify:local-shared-backend` uses disposable PostgreSQL and a Docker-network LAN client to prove separate desktop/Web identities for one Owner, empty startup, bidirectional CRUD, sub-two-second invalidation, identical file bytes, and rejection of forged local-source headers.
- These are isolated software-rendered tests. They do not certify a clean target OS, real GPU/DPI/native IME/audio/video devices, operating-system microphone prompts, or provider login policies.

## Install, Update, And Uninstall Policy

Verify `sha256sum -c SHA256SUMS` before using either artifact.

For a system package, install with `sudo apt install ./SparkX-0.1.0-linux-arm64.deb`. The display name, executable, and install root are SparkX, while the Debian package identity intentionally remains `sparkclaw` so an existing installation upgrades in place. Update by verifying and installing the complete newer DEB; do not mix application files from two releases. Roll back only by explicitly installing a previously retained, verified whole DEB after stopping the candidate.

For a portable trial, mark the AppImage executable and run it directly. Replace it atomically with a newly verified complete AppImage for updates. Keep the previous verified file only as an operator-controlled whole-release rollback candidate; the application does not select engines or silently fall back.

`sudo apt remove sparkclaw` removes the DEB application package. Removing an AppImage removes only that file. Both operations must preserve Electron user data and browser-session data by default. Data removal is a separate destructive operation requiring explicit owner authorization and a resolved exact user-data path; never copy, import, or delete the old standalone Chromium profile as part of install/update/uninstall.

No automatic updater is enabled in this candidate. Distribution, update, and rollback are operator-controlled.

## Pre-Cutover Acceptance

Run these checks on the target desktop before production routing changes:

1. Verify artifact hash and ARM64 architecture; install or run the candidate without changing the existing browser service.
2. Confirm normal launch, close-to-hide and reopen, high-DPI layout, GPU rendering, native IME, clipboard, download reveal/cancel, audio/video devices, and the OS microphone prompt.
3. In “My browsing”, sign in normally to every required provider. Restart the application and verify persistence. Test logout/account switching and existing task account-mismatch rejection. Record provider-specific challenges; do not import cookies or switch engines.
4. Connect to the live Gateway and speech endpoint. Verify automatic desktop identity, a separately issued Web Client, authenticated HTTP/SSE/artifacts, and microphone transcription with non-destructive test data.
5. Start Browser Controller with both `SPARKCLAW_ELECTRON_ADAPTER_SOCKET` and `SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE` set to the desktop runtime's owner-only paths. Run a read-only controller smoke and confirm the exact Electron runtime generation.
6. Confirm the application icon/desktop entry and obtain the owner's explicit go/no-go.

Any failed gate stops cutover. Preserve logs without credentials, tokens, cookies, message bodies, or adapter secrets.

## Atomic Cutover

Only after explicit release authorization:

1. Record the current deployed release, service state, `open:browser` behavior, configuration and rollback commands. Back up configuration and existing data according to the normal operator guide; do not transform or merge browser profiles.
2. Stop admission of new browser tasks and let accepted tasks reach a known terminal state. Unknown outcomes remain unknown and are not replayed.
3. Install the verified candidate and launch SparkX Desktop. Establish required website sessions through normal owner login.
4. Configure Browser Controller with the Electron adapter socket and secret-file paths, restart only the affected controller service, and pass the read-only smoke.
5. Change the single production `open:browser`/service entry to Electron as one release step. Do not expose an engine selector and do not retain an automatic old-engine fallback.
6. Validate Gateway readiness, ordinary WebChat, personal browsing, task observation, one approved non-destructive task, downloads, speech, lifecycle, and audit/error reporting.
7. Publish the release/status update only after every gate passes. Keep old application data untouched until the retention decision is separately authorized.

## Rollback

Rollback is explicit and whole-release. Stop new admission, preserve evidence for in-flight unknown outcomes, restore the recorded previous package and controller/service configuration, restart the affected services, and rerun readiness plus read-only smoke. Do not automatically reopen or replay interrupted task effects. Do not delete the Electron Session or old Chromium profile during rollback. Escalate any required data migration as a separate reviewed operation.

## Final Acceptance Record

The owner should record target host/OS, installed artifact hash, GPU/DPI/IME/audio/video results, provider login/persistence/account-switch results, live connectivity smoke, icon approval, install/update/uninstall outcome, and the cutover decision. Until that record and release authorization exist, the current production browser path remains authoritative.
