# SparkClaw Mac source synchronization and connection guide

> Language: English | [简体中文](../zh-cn/docs/macos-connection-guide.md)

Current source release: follow the [matched workbench release guide](workbench-release.md) for fresh storage, enrollment, API paths and restart behavior; see the [implementation ledger](workbench-convergence-implementation.md) for exact validation boundaries.

See the [Mac acceptance record](macos-r3-acceptance.md) for the subsequent native ARM64 build, launch, qualification-tool repairs and actual M01–M12 status. This guide's original “current Linux environment” statements describe source delivery and do not restrict a later local Mac session explicitly authorized by the user.

Companions: [Client and backend architecture R3](client-backend-architecture-design.md) and [macOS desktop design R3](macos-lan-desktop-design.md).

Date: 2026-10-06. Main contains the desktop workbench and packaging entrypoints. WebChat stores local data on its deployment host, desktop stores it on the desktop host, and mail stays backend-authoritative; see the [convergence plan](workbench-runtime-convergence-design.md). Product connectivity uses HTTPS and an outbound authenticated WSS host channel. A remote Mac uses the deployment host's reachable LAN origin, not Mac's own localhost.

## 1. Build and qualification responsibilities

Validate backend/shared behavior on Linux; build the matching desktop architecture and qualify native behavior on Mac. Record source delivery, build, deployment and physical qualification separately with exact SHAs. Existing ARM64 builds and GUI evidence are in the [dual-host record](macos-r3-dual-host-acceptance.md); full M01–M12 remains incomplete, and each release requires its own validation. Normal source builds do not require remote Mac access.

## 2. Source delivery record

Each build handoff identifies the pushed remote/branch and exact commit SHA, required Node/npm and other dependency versions, actual Mac install/build/start commands, completed Linux/shared checks, and pending Mac cases. The user reports the same SHA with build/test results; a branch name alone is not sufficient evidence.

Include lockfiles, necessary build configuration, source and versioned managed assets in the delivery. The Mac may check out the full repository, including backend source, but builds the client without deploying Linux Gateway, models or App-CLI Registry/Executor. The client package includes UI, ClientStore, Browser Host Agent, Electron/Bridge and verified page assets; exclude server database dumps, credentials, private test data and platform-incompatible binaries.

Mac ARM64 and x64 packaging scripts now exist. This tranche supports local conversations/files, device management, secure login, explicit task execution/output delivery, mailbox cache/sync, one-time and recurring workbench-local schedules and browser Host control; see the [phase ledger](client-r3-implementation.md). Mac ARM64 builds and partial physical qualification have evidence; signing, notarization and full physical qualification remain incomplete. Packages are unsigned development builds (`mac.identity=null`), audited against the public client allowlist before distributables are created.

## 3. Synchronize and build on Mac

Preserve local changes and synchronize without rewriting history:

```sh
git status --short
git fetch origin
git switch main
git pull --ff-only origin main
git rev-parse HEAD
```

Verify HEAD against the handoff SHA. Do not force-reset or clean the user's workspace. Use Node 26.x and npm 11.x. Install from the lockfile on Mac; do not copy Linux node_modules or native artifacts. Choose **one** command matching the CPU and Node architecture:

```sh
npm ci
npm --workspace @sparkclaw/desktop run dist:mac-arm64
# Intel alternative: npm --workspace @sparkclaw/desktop run dist:mac-x64
```

The script builds UI with old WebChat Token/origin variables cleared, checks managed assets and runs local electron-builder with publishing disabled. Outputs are under `apps/desktop/dist/`: `SparkX-0.1.0-mac-ARCH.dmg` and `.zip`. Source GUI diagnosis on Mac uses:

```sh
npm run build:desktop-ui
npm run dev:desktop
```

Mac does not build the Go backend. Linux calls to the Mac packager are rejected; previous artifacts do not replace rebuilding the current version. Record the exact SHA and command even if the build fails.

## 4. Local Mac preparation

The user runs these checks in their Mac terminal:

```sh
sw_vers
uname -m
xcode-select -p
node --version
npm --version
```

Compare versions with the delivered commit's manifests/lockfiles and inspect free disk space, native toolchain, selected CPU target and signing setup if needed. Build-tool dependencies are separate from released-app runtime requirements.

For GUI qualification, log into the Mac desktop, connect power, and put Mac and Linux on a mutually reachable LAN. Avoid guest Wi-Fi isolation. The user launches the app from their own Terminal/Finder. Sustained tests may use `caffeinate -i`, stopped with Ctrl+C afterward; also explicitly test sleep/lid-close behavior.

Build-error feedback should include commit SHA, macOS/CPU architecture, Node/npm versions, exact failed command and redacted error output. GUI/network failures additionally need the case ID and backend version. Do not include passwords, keys, tokens or personal mail contents.

## 5. Product connection and qualification

Before connecting, record the backend's reachable HTTPS origin, deployment identity/certificate fingerprint, paired Owner/client/host identifiers, selected build versions, and the actual client-local data root. Inspect free space and the local schema version. Keep secrets out of the record. Application pairing and host permission are distinct; Git synchronization does not provision either.

### 5.1 Where to obtain the credential

These tools require this commit's Gateway/provisioning on the **Linux backend host**, including independent `local-management.json` and a live private socket. An old deployment must first be updated through its established process; source delivery does not modify a running deployment. See [private management prerequisites](deployment.md#initial-device-credential-and-recovery).

1. With no usable signed-in device, the backend deployment user runs the initial tool in an interactive terminal from the backend checkout:

   ```sh
   npm run credentials:initial -- --name "My Mac"
   ```

   It displays one `sparkclaw-connect-v1...` connection credential once, with deployment, Owner and device ID. The value already contains the public pinned backend identity and this device's bearer; the raw bearer is not printed separately. All three standard streams must be TTYs. A non-default private runtime directory uses `--runtime-dir /absolute/runtime`. Do not run this on Mac or copy a preprovisioned Desktop Token. Management authority works only over the private Unix socket.
2. With a usable SparkX desktop, open **Settings → Devices & credentials**, enter a device name, issue/copy the connection credential, and hide it after secure login. A normal browser can still issue only a raw access Token; use a signed-in desktop when the target is another downloaded desktop. The list shows metadata and revocation only and cannot reveal an old secret.
3. On the target Mac, paste the complete connection credential into the single **Connection credential** field and choose **Unlock**. There is no separate backend-description step. Main decodes the bundle locally and still verifies the HTTPS chain, hostname and leaf certificate pin before sending the bearer; the bundle is a secret, while a pin never bypasses certificate validation. The backend deployment must set both `SPARKCLAW_DESKTOP_TLS_DIR` and `SPARKCLAW_DESKTOP_PUBLIC_ORIGIN` as described in [Deployment](deployment.md#product-entrypoints), so provisioning can derive the public CA and leaf fingerprint. This does not create certificates or configure production ingress.
4. Main saves encrypted credentials protected by OS secure storage (Mac Keychain through Electron safeStorage). The same device reuses its credential. Logout/revocation or enrollment with another connection credential clears the old credential and locks UI as appropriate, preserving local conversations/files. Server installation-ID registration checks the issued client before accepting workbench execution transports.
5. Unknown issuance outcomes reuse the original command/name/request key. Completed retrievals never redisplay a connection credential. Total device lockout or unrecoverable plaintext after restart/ten-minute expiry requires recovery of the **exact lost device ID** (replace `LOST_DEVICE_ID`):

   ```sh
   npm run credentials:recover -- --revoke-id LOST_DEVICE_ID --name "Replacement Mac"
   ```

   Recovery revokes that device before issuing a replacement. Keep connection credentials and Tokens out of Git, builds, screenshots and feedback logs; retain only redacted IDs/error states.

### 5.2 Login and acceptance

The table below is the complete target acceptance matrix. Linux/shared source enables local saving, device management/login, explicit execution/output delivery, mailbox sync and Host WSS. Production cutover remains unperformed. All Mac M01–M12 require user evidence on the delivered SHA.

On first launch, paste the single SparkClaw connection credential. Main extracts the backend identity and bearer, verifies TLS, and lets the backend bind this installation before unlocking authorized tasks, mail synchronization and other services. Mac main securely persists login credentials in Keychain, never in Git, build configuration or feedback logs. Valid credentials support later reconnection; invalidation/revocation, logout or a different backend credential requires login again.

After service unlock, browser features still require a separate host grant. Mac main then opens the outbound WSS channel, registers host_id/runtime_generation/capabilities and receives its role-scoped host lease. Keep raw CDP and private Linux Adapter secrets inaccessible. Use the actual LAN entrance and verify certificate-hostname matching; Git access proves neither product connectivity nor service permission.

| Check | Expected evidence |
|---|---|
| Credential retrieval and management | Backend initialization and actual “Settings → Devices & credentials” navigation work; separate per-device issuance, one-time display/copy, same-device reuse, same-key retries, loss recovery and effective revocation, with no plaintext in logs/Git |
| First credential unlock | A fresh install has one connection-credential field and no separate backend JSON step; invalid/revoked credentials cannot access tasks, mail or browser control; restart recovery, logout, backend change and Keychain behavior work correctly |
| Local non-mail data | Create Mac conversation A and Linux conversation B. Restart each client and verify its own messages, task history and files persist. Neither conversation automatically appears on the other client |
| Backend mail sync | Both clients receive authorized mailbox revisions/deletions; offline cache and cursor reset recover. Clearing a local cache does not delete backend mail |
| Browser adapter | Use harmless test pages to navigate, type a random marker, read DOM, receive events and cancel through the same backend adapter on Mac and Linux |
| Embedded-only activity | Mac browser requests originate on Mac and operate the visible embedded page. Reject standalone/system-browser automation and silent backend substitution; verify permitted child pages remain embedded |
| Conversation binding | Rapidly switch A/C on one Mac; each retains its own page, and late events cannot cross-bind. No-page conversations show an empty state |
| Backend acquisition role | Verify the backend dedicated browser is an acquisition resource, with a separate profile. Its mail collector continues when clients quit without creating duplicate collectors |
| Explicit operation approval | A pending browser/document operation shows its immutable tool/arguments; explicit approve resumes the original request, reject does not execute it; offline/restarted snapshots stay read-only until a fresh running-status check |
| Local file delivery | Download a file, receive a generated output, save an attachment copy, and verify local manifests/hashes. Simulate disk full and a lost ACK; backend completion must not falsely imply local saving |
| Failure recovery | Disconnect, sleep/lid close, exit, restart, revoke and drop replies. Verify resource-side lease expiry, immediate cleanup after durable result ACK, and undelivered-result expiry 24 hours after generation without reconnect/restart extension; preserve deduplication records and never replay uncertain writes |
| Isolation and clean start | Reject wrong Owner/client/certificate, inspect non-mail content cleanup, verify empty new non-mail storage without importing old test histories or copying the server database |

Use isolated test data and a dedicated local data directory. Existing test history is not imported; connectivity tests do not delete current data or reset service identities/recovery ledgers. Real mailbox operations retain their existing authorization and qualification boundaries; passing harmless pages does not qualify real mail.

The user runs GUI tests in their logged-in Mac session and shares redacted evidence tied to the delivered commit. Also verify Chinese input, Retina/multiple displays, embedded-site login/popup compatibility, microphone when enabled, local permissions, package allowlist, selected CPU architectures, signing/notarization and upgrade/schema rollback. Screen sharing, if needed for manual collaboration, is not product page transport.

Link evidence to R3 A01–A21 and Mac M01–M12. A successful Git synchronization/build or Linux-only tests do not complete those cases. Concrete product enrollment/build commands must be added when the corresponding implementation exists; do not invent working remote-control endpoints.

## 6. Report results and continue

The user records Mac build/GUI results against the delivered SHA and shares redacted failures for follow-up fixes. Keep required test evidence and client-local database/files. Uninstall/upgrade must state whether local user data is retained.

The product uses its LAN connection independently of Git delivery. Record failed/pending cases, temporary-data cleanup evidence and clean-start storage qualification; legacy test-data migration is excluded. Distinguish source pushed, Mac build passed, hardware qualification passed and production cutover. Until user feedback arrives, Mac-only cases remain pending.
