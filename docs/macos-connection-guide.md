# SparkClaw physical Mac connection guide

> Language: English | [简体中文](../zh-cn/docs/macos-connection-guide.md)

Companions: [Client and backend architecture R3](client-backend-architecture-design.md) and [macOS desktop design R3](macos-lan-desktop-design.md).

Date: 2026-09-30. This guide prepares development and physical-Mac qualification. No remote access, R3 implementation or migration is claimed. The Linux backend performs business processing and stores mail; Mac stores its own conversations, messages, task history, generated files and other non-mail user data. Only mail synchronizes between clients. Mac browser automation operates its actual embedded browser through the unified backend adapter.

Product connectivity uses LAN HTTPS plus an outbound authenticated WSS host channel. SSH below is optional development access. A colocated Linux client may use loopback; a remote Mac must address the Linux host's reachable LAN origin, not Mac's own localhost. Pairing, ClientStore and the remote adapter remain to be implemented, so this is not yet an end-user installation recipe.

Mac builds include the UI, local database/file storage, Browser Host Agent, Electron/Bridge execution core and verified managed page assets. Do not transfer Gateway, models, backend service configuration, App-CLI Registry/Executor, server database dumps or server credentials. Local client storage is required; it is not a shared backend database replica.

## 1. Prepare the Mac

Put Mac and Linux on a mutually reachable LAN, log into the Mac desktop, and connect power. Avoid guest Wi-Fi with client isolation. Keep a GUI session logged in for GUI tests. For sustained tests, run `caffeinate -i` and stop it with Ctrl+C afterward. Lid close, sleep, or exit can remove Mac task execution resources: verify waiting/unknown outcomes and lease expiry. The backend mail collector should continue; other tasks follow R3's execution and delivery budgets.

Open **System Settings → General → Sharing → Remote Login**. Enable it for “Only these users” and add the development user. Basic builds/log inspection do not require enabling full disk access for remote users. Record the displayed `ssh username@hostname`. [Apple Remote Login instructions](https://support.apple.com/guide/mac-help/allow-a-remote-computer-to-access-your-mac-mchlp1066/mac)

## 2. Provide non-secret connection details

Run these separately in the Mac terminal:

```sh
sw_vers
uname -m
id -un
scutil --get LocalHostName
networksetup -listallhardwareports
```

Find the LAN IP under System Settings → Network → active connection → Details → TCP/IP. For terminal lookup, first identify the active interface from the last command, then run `ipconfig getifaddr en0` only if it really is en0.

Provide the following without sharing passwords or private keys:

```text
Mac LAN IP:
Mac username:
macOS version:
CPU architecture (arm64 / x86_64):
Remote Login enabled:
Mac GUI session currently logged in:
```

If the Remote Login `.local` hostname fails, use the verified LAN IP. Hostname failure does not prove SSH is unavailable.

## 3. Configure an SSH key

Once those details are available, a dedicated key can be created in this task's Linux `work/` directory. Give the user its **public key** to append to the target Mac user's `~/.ssh/authorized_keys`. An existing authorized SSH connection can be reused. No Mac password or private key needs to enter the conversation.

For manual setup, run this template as the target Mac user only after replacing the placeholder line with the actual public key:

```sh
mkdir -p ~/.ssh
chmod 700 ~/.ssh
cat >> ~/.ssh/authorized_keys <<'PUBLIC_KEY'
REPLACE_WITH_THE_COMPLETE_SSH_ED25519_PUBLIC_KEY_LINE
PUBLIC_KEY
chmod 600 ~/.ssh/authorized_keys
```

Append only; do not replace existing authorized keys. Use SSH/SFTP for transfers. Prefer `~/Documents/SparkClaw-dev` to avoid overwriting an existing Mac working copy.

Verify the host fingerprint on first connection. On Mac, show the ED25519 host public key's SHA-256 fingerprint:

```sh
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

The ED25519 fingerprint shown by Linux must match. If a different algorithm is negotiated, verify that corresponding host public key. Never disable host-key checking to bypass a mismatch.

## 4. Start with read-only inspection

After determining username, address, and key path, connect using:

```sh
ssh -i /absolute/path/to/task-key MAC_USER@MAC_LAN_IP
```

Inspect the remote environment:

```sh
sw_vers
uname -m
xcode-select -p
command -v node
command -v npm
```

Before development builds, verify locked client Node/npm requirements, free disk space, signing environment, and Linux reachability. Development tools are not released-app external dependencies. Check paired Bridge/page-asset versions, Mac Browser Host capabilities, ClientStore/local data paths, and the package allowlist. Do not install Linux business-backend components on Mac.

SSH supports client file transfer, build dependency installation, client builds, unit tests, and log retrieval. Start GUI tests in the logged-in Mac user's session, initially through their Terminal/Finder. Remote launch requires separate verification of launchd GUI-session access and permissions; an SSH shell does not imply GUI access.

## 5. R3 product connection and qualification

After the R3 components exist, record the backend's reachable HTTPS origin, deployment identity/certificate fingerprint, paired Owner/client/host identifiers, selected build versions, and the actual client-local data root. Inspect free space and the local schema version. Keep secrets out of the record. Application pairing and host permission are distinct; an SSH login does not provision either.

Verify server trust before credential exchange. Mac main then opens the outbound WSS channel, registers host_id/runtime_generation/capabilities and receives its role-scoped host lease. Keep raw CDP and private Linux Adapter secrets inaccessible. An optional SSH tunnel may assist connectivity debugging but must not bypass certificate-hostname validation or become the product control channel.

| Check | Expected evidence |
|---|---|
| Local non-mail data | Create Mac conversation A and Linux conversation B. Restart each client and verify its own messages, task history and files persist. Neither conversation automatically appears on the other client |
| Backend mail sync | Both clients receive authorized mailbox revisions/deletions; offline cache and cursor reset recover. Clearing a local cache does not delete backend mail |
| Browser adapter | Use harmless test pages to navigate, type a random marker, read DOM, receive events and cancel through the same backend adapter on Mac and Linux |
| Embedded-only activity | Mac browser requests originate on Mac and operate the visible embedded page. Reject standalone/system-browser automation and silent backend substitution; verify permitted child pages remain embedded |
| Conversation binding | Rapidly switch A/C on one Mac; each retains its own page, and late events cannot cross-bind. No-page conversations show an empty state |
| Backend acquisition role | Verify the backend dedicated browser is an acquisition resource, with a separate profile. Its mail collector continues when clients quit without creating duplicate collectors |
| Local file delivery | Download a file, receive a generated output, save an attachment copy, and verify local manifests/hashes. Simulate disk full and a lost ACK; backend completion must not falsely imply local saving |
| Failure recovery | Disconnect, sleep/lid close, exit, restart, revoke and drop replies. Verify resource-side lease expiry, bounded temporary retention and reconciliation without replaying uncertain writes |
| Isolation and migration | Reject wrong Owner/client/certificate, inspect non-mail content cleanup, verify local backup/import and assigned legacy data without copying the server database |

Use isolated test data and a dedicated local data directory. Existing production history is not migrated or deleted during connectivity tests. Real mailbox operations retain their existing authorization and qualification boundaries; passing harmless pages does not qualify real mail.

Run GUI tests in the logged-in Mac session and collect redacted evidence through SSH. Also verify Chinese input, Retina/multiple displays, embedded-site login/popup compatibility, microphone when enabled, local permissions, package allowlist, selected CPU architectures, signing/notarization and upgrade/schema rollback. Screen sharing, if needed for manual collaboration, is not product page transport.

Link evidence to R3 A01–A16 and Mac M01–M10. SSH reachability, a successful build or Linux-only tests do not complete those cases. Concrete product enrollment/build commands must be added when the corresponding implementation exists; do not invent working remote-control endpoints.

## 6. Finish qualification

Remove only this task's dedicated authorized_keys entry and disable Remote Login if no longer needed, preserving other keys/access settings. Keep required development artifacts, test records and client-local database/files. Uninstall/upgrade must state whether local user data is retained; do not remove it as SSH cleanup.

The product subsequently uses its LAN connection without SSH. Record remaining failed/pending cases, temporary-data cleanup evidence and any separately proposed legacy-data migration. Keep runtime implementation, hardware qualification and production cutover as distinct statuses.
