# SparkClaw Mac source synchronization and connection guide

> Language: English | [简体中文](../zh-cn/docs/macos-connection-guide.md)

Companions: [Client and backend architecture R3](client-backend-architecture-design.md) and [macOS desktop design R3](macos-lan-desktop-design.md).

Date: 2026-09-30. The user confirmed the build workflow: develop and validate on the current Linux environment, deliver source through a Git push, then the user synchronizes and compiles on their Mac. Do not compile, cross-build, package, sign or notarize Mac artifacts in the current environment. No R3 runtime implementation, Mac build or hardware qualification is claimed by this guide.

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

Current desktop scripts provide Linux ARM64 packaging only. Mac packaging scripts/configuration must be implemented and pushed before providing a concrete Mac build command. Do not describe a proposed script as available or use the Linux packaging command as Mac qualification.

## 3. Synchronize on Mac

In the Mac repository directory, first inspect the working tree and preserve any local changes. Replace DELIVERY_BRANCH with the branch specified in the handoff, then synchronize without rewriting local history:

```sh
git status --short
git fetch origin
git switch DELIVERY_BRANCH
git pull --ff-only origin DELIVERY_BRANCH
git rev-parse HEAD
```

Use the supplied remote name if it differs from origin. Verify HEAD matches the delivered commit SHA before building. If local changes, divergence or a newer remote commit prevent that match, resolve the checkout explicitly; do not force-reset or clean the user's workspace.

Install dependencies and run the actual Mac commands from that commit's handoff locally on Mac. Do not copy Linux node_modules, native modules or build artifacts as substitutes for Mac dependencies.

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

### 5.1 Where to obtain the credential (entries not implemented yet)

This section specifies the later implementation and user flow; it does not claim a runnable retrieval command or complete settings entry exists today. Follow [unified architecture section 3.2](client-backend-architecture-design.md#32-credential-retrieval-and-device-management-not-implemented).

1. For a first deployment with no authenticated user device, the completion screen/terminal supplies retrieval instructions. The deployment user runs controlled initialization on the **Linux backend host**, issuing a separate credential for this Mac. The proposed `npm run credentials:initial -- --name "My Mac"` enters executable handoff instructions only after implementation. Do not initialize the backend on Mac or directly read/copy the Token from `desktop-client.json`.
2. If an authenticated device is available, open “Settings → Devices & credentials”, enter a recognizable name such as “My Mac”, issue a separate credential and copy it. The device list exposes identity, timestamps and status, with no old-Token reveal button.
3. Confirm backend address/identity on the new Mac and enter the new credential in its login screen; desktop main stores it in Keychain after verification. Hide the issuance display once securely saved. Keep the Token out of Git, build arguments, screenshots, feedback logs and ordinary configuration.
4. Later connections from the same Mac reuse a valid credential without fetching another. Invalid/revoked credentials require login again; a lost credential is revoked and reissued from another authenticated device. Total user-device lockout requires controlled recovery by the backend deployment user. Deliver the actual recovery command with implementation, never substitute old-Token display or anonymous LAN issuance.
5. Retry the same issuance after timeout or response loss, rather than repeatedly creating devices. If backend restart or expiration makes one-time plaintext unavailable, follow instructions to revoke the unclaimed device and reissue. Retain redacted device IDs and error states for troubleshooting, never return the Token in feedback.

### 5.2 Login and acceptance

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
| Local file delivery | Download a file, receive a generated output, save an attachment copy, and verify local manifests/hashes. Simulate disk full and a lost ACK; backend completion must not falsely imply local saving |
| Failure recovery | Disconnect, sleep/lid close, exit, restart, revoke and drop replies. Verify resource-side lease expiry, immediate cleanup after durable result ACK, and undelivered-result expiry 24 hours after generation without reconnect/restart extension; preserve deduplication records and never replay uncertain writes |
| Isolation and clean start | Reject wrong Owner/client/certificate, inspect non-mail content cleanup, verify empty new non-mail storage without importing old test histories or copying the server database |

Use isolated test data and a dedicated local data directory. Existing test history is not imported; connectivity tests do not delete current data or reset service identities/recovery ledgers. Real mailbox operations retain their existing authorization and qualification boundaries; passing harmless pages does not qualify real mail.

The user runs GUI tests in their logged-in Mac session and shares redacted evidence tied to the delivered commit. Also verify Chinese input, Retina/multiple displays, embedded-site login/popup compatibility, microphone when enabled, local permissions, package allowlist, selected CPU architectures, signing/notarization and upgrade/schema rollback. Screen sharing, if needed for manual collaboration, is not product page transport.

Link evidence to R3 A01–A21 and Mac M01–M12. A successful Git synchronization/build or Linux-only tests do not complete those cases. Concrete product enrollment/build commands must be added when the corresponding implementation exists; do not invent working remote-control endpoints.

## 6. Report results and continue

The user records Mac build/GUI results against the delivered SHA and shares redacted failures for follow-up fixes. Keep required test evidence and client-local database/files. Uninstall/upgrade must state whether local user data is retained.

The product uses its LAN connection independently of Git delivery. Record failed/pending cases, temporary-data cleanup evidence and clean-start storage qualification; legacy test-data migration is excluded. Distinguish source pushed, Mac build passed, hardware qualification passed and production cutover. Until user feedback arrives, Mac-only cases remain pending.
