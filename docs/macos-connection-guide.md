# SparkClaw Mac source synchronization and connection guide

> Language: English | [简体中文](../zh-cn/docs/macos-connection-guide.md)

See the [Mac acceptance record](macos-r3-acceptance.md) for the subsequent native ARM64 build, launch, qualification-tool repairs and actual M01–M12 status. This guide's original “current Linux environment” statements describe source delivery and do not restrict a later local Mac session explicitly authorized by the user.

Companions: [Client and backend architecture R3](client-backend-architecture-design.md) and [macOS desktop design R3](macos-lan-desktop-design.md).

Date: 2026-10-03. The user confirmed the build workflow: develop and validate on the current Linux environment, deliver source through a Git push, then the user synchronizes and compiles on their Mac. Do not compile, cross-build, package, sign or notarize Mac artifacts in the current environment. P0–P5 Linux/shared runtime source is complete; Mac build and hardware qualification are not claimed by this guide.

The Linux backend processes business work and stores mail. Each client stores its own non-mail data; existing test data is not migrated. Product connectivity uses LAN HTTPS and an outbound authenticated WSS host channel. A colocated Linux client may use loopback; a remote Mac addresses the Linux host's reachable LAN origin, not Mac's own localhost.

## 1. Work split

| Owner | Responsibility |
|---|---|
| Current Linux environment | Implement backend/client source, build scripts and configuration; run applicable Linux, shared-code and protocol checks; prepare source delivery through Git |
| User on Mac | Synchronize the delivered commit; install locked build dependencies; compile/package locally, launch the GUI and perform Mac hardware checks; signing/notarization, when required, also runs on Mac |
| Follow-up fixes | Diagnose supplied build logs/test results, fix and push another identified commit; the user synchronizes and rebuilds on Mac |

Mac compilation is not a prerequisite for Linux implementation or source delivery. Label results separately as Linux/shared checks, user-reported Mac build, and Mac hardware qualification. Passing one does not prove the others.

This workflow does not require Mac SSH access, Remote Login, SFTP delivery or an agent-run remote build. Do not request Mac login details or configure remote access for this delivery path; any later remote debugging would be a separately requested activity. Git authentication on Mac uses the user's existing setup.

## 2. Source delivery record

Each build handoff identifies the pushed remote/branch and exact commit SHA, required Node/npm and other dependency versions, actual Mac install/build/start commands, completed Linux/shared checks, and pending Mac cases. The user reports the same SHA with build/test results; a branch name alone is not sufficient evidence.

Include lockfiles, necessary build configuration, source and versioned managed assets in the delivery. The Mac may check out the full repository, including backend source, but builds the client without deploying Linux Gateway, models or App-CLI Registry/Executor. The client package includes UI, ClientStore, Browser Host Agent, Electron/Bridge and verified page assets; exclude server database dumps, credentials, private test data and platform-incompatible binaries.

Mac ARM64 and x64 packaging scripts now exist. This tranche supports local conversations/files, device management, secure login, explicit task execution/output delivery, mailbox cache/sync, single-run online schedules and browser Host control; see the [phase ledger](client-r3-implementation.md). Mac build/signing/hardware are unverified. Packages are unsigned development builds (`mac.identity=null`), audited against the public client allowlist before distributables are created.

## 3. Synchronize and build on Mac

Preserve local changes and synchronize without rewriting history:

```sh
git status --short
git fetch origin
git switch codex/sparkclaw-r3
git pull --ff-only origin codex/sparkclaw-r3
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

Mac does not build the Go backend. Linux calls to the Mac packager are rejected; no Mac artifact was built in this delivery. Record the exact SHA and command even if the build fails.

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

## 5. R3 product connection and qualification

After the R3 components exist, record the backend's reachable HTTPS origin, deployment identity/certificate fingerprint, paired Owner/client/host identifiers, selected build versions, and the actual client-local data root. Inspect free space and the local schema version. Keep secrets out of the record. Application pairing and host permission are distinct; Git synchronization does not provision either.

### 5.1 Where to obtain the credential

These tools require this commit's Gateway/provisioning on the **Linux backend host**, including independent `local-management.json` and a live private socket. An old deployment must first be updated through its established process; source delivery does not modify a running deployment. See [private management prerequisites](deployment.md#initial-device-credential-and-recovery).

1. With no usable signed-in device, the backend deployment user runs the initial tool in an interactive terminal from the backend checkout:

   ```sh
   npm run credentials:initial -- --name "My Mac"
   ```

   It displays a new Token once, with deployment, Owner and device ID. All three standard streams must be TTYs. A non-default private runtime directory uses `--runtime-dir /absolute/runtime`. Do not run this on Mac or copy a preprovisioned Desktop Token. Management authority works only over the private Unix socket.
2. With a usable device, open **Settings → Devices & credentials**, enter a device name, issue/copy the credential, and hide it after secure login. The list shows metadata and revocation only; it cannot reveal old Tokens.
3. Paste the administrator's **public v2 connection JSON** into Mac login, then enter this device's credential:

   ```json
   {
     "schema_version": 2,
     "origin": "https://your-backend.lan",
     "deployment_id": "ACTUAL_DEPLOYMENT_ID",
     "owner_id": "ACTUAL_OWNER_ID",
     "tls_certificate_sha256": "ACTUAL_LOWERCASE_64_HEX_LEAF_CERTIFICATE_SHA256"
   }
   ```

   Replace every placeholder. Private PKI may also supply `tls_ca_pem`, containing the public CA certificate. No Token/private key belongs in this JSON. Mac requires v2 HTTPS and verifies chain, hostname and leaf pin before bearer transmission. A pin does not bypass certificate validation. Configure the optional native Gateway TLS listener with paired absolute `gateway.tls_cert_file` / `gateway.tls_key_file` paths (or `SPARKCLAW_GATEWAY_TLS_CERT_FILE` / `SPARKCLAW_GATEWAY_TLS_KEY_FILE`). The certificate must cover the LAN hostname, and the key must be a regular owner-only file owned by the Gateway user, not a symlink. Alternatively, a reverse proxy must connect to Gateway through HTTPS upstream so WSS Host requests arrive with real TLS; forwarding headers do not qualify. A proxy terminating TLS and forwarding HTTP cannot admit the Host. This delivery does not create certificates, modify a running service or configure production ingress.
4. Main saves encrypted credentials protected by OS secure storage (Mac Keychain through Electron safeStorage). The same device reuses its credential. Logout/revocation/backend changes clear credentials and lock UI, preserving local conversations/files. Server installation-ID registration is implemented and checks the issued client before accepting R3 transports.
5. Unknown issuance outcomes reuse the original command/name/request key. Completed retrievals never redisplay a Token. Total device lockout or unrecoverable plaintext after restart/ten-minute expiry requires recovery of the **exact lost device ID** (replace `LOST_DEVICE_ID`):

   ```sh
   npm run credentials:recover -- --revoke-id LOST_DEVICE_ID --name "Replacement Mac"
   ```

   Recovery revokes that device before issuing a replacement. Keep Tokens out of Git, builds, screenshots and feedback logs; retain only redacted IDs/error states.

### 5.2 Login and acceptance

The table below is the complete target acceptance matrix. Linux/shared source enables local saving, device management/login, explicit execution/output delivery, mailbox sync and Host WSS. Production cutover remains unperformed. All Mac M01–M12 require user evidence on the delivered SHA.

On first launch, confirm the backend address and verify its certificate/service identity, then enter the SparkClaw service credential in the client login UI. The backend verifies it and binds this installation before unlocking authorized tasks, mail synchronization and other services. Mac main securely persists login credentials in Keychain, never in Git, build configuration or feedback logs. Valid credentials support later reconnection; invalidation/revocation, logout or backend changes require login again.

After service unlock, browser features still require a separate host grant. Mac main then opens the outbound WSS channel, registers host_id/runtime_generation/capabilities and receives its role-scoped host lease. Keep raw CDP and private Linux Adapter secrets inaccessible. Use the actual LAN entrance and verify certificate-hostname matching; Git access proves neither product connectivity nor service permission.

| Check | Expected evidence |
|---|---|
| Credential retrieval and management | Backend initialization and actual “Settings → Devices & credentials” navigation work; separate per-device issuance, one-time display/copy, same-device reuse, same-key retries, loss recovery and effective revocation, with no plaintext in logs/Git |
| First credential unlock | A fresh install requires SparkClaw credential entry and backend verification; invalid/revoked credentials cannot access tasks, mail or browser control; restart recovery, logout, backend change and Keychain behavior work correctly |
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
