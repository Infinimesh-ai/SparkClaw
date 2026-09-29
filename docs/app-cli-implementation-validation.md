# App-CLI extraction: implementation and acceptance

[简体中文](../zh-cn/docs/app-cli-implementation-validation.md) · [Design](app-cli-email-extraction-design.md)

Implementation date: 2026-09-29. SparkClaw branch:
`codex/extract-email-app-cli`; maintenance fork branch:
[`codex/sparkclaw-email`](https://github.com/ZZZZJJJ0928/App-CLI/tree/codex/sparkclaw-email).
The pinned source commit and artifact digests are in
[configs/app-cli-release.json](../configs/app-cli-release.json).

## Delivered behavior

QQ Mail, Gmail and Outlook application scripts, Reader sources/builds,
notification semantics, send journals, schemas, manifests and bindings are
maintained in App-CLI. SparkClaw contains their verified release archive and
generated Go/Reader/Desktop projections, with no independent editable mail
implementation. Old `scripts/email` and Controller provider dispatch/pools have
been removed; existing upper-layer workflows, storage and UI remain consumers.

Gateway → authenticated owner Controller → public App-CLI Python Registry →
RuntimeAdapter 2.0 → resident Executor → registered application → signed HostPort
→ existing task-owned browser page. Generic Controller/Bridge/Desktop ownership,
page isolation and personal-browser protection stay in SparkClaw. Adding a
non-mail application using existing Host capabilities requires a new App-CLI
registration/binding, not changes to the generic scheduler.

The Executor durably binds each request key to principal/owner/intent. Lost
responses look up the original task; unresolved admission cannot resend.
Expired execution authority does not remove query/cancel access. Recovery uses
original journals, effect fences and monotonically increasing epochs. Shared
read/watch activities have independent leases; actual daemon expiry, cleanup
fencing and bounded page parking survive control-process failures. A failed
application-release check disables application admission while generic browser
startup remains available.

## Initial extraction verification

Host: Linux ARM64, Python 3.12, Node 26.2.0. Actual isolated browser:
Electron 44.4.3 / Chromium 152.0.7977.130 / embedded Node 24.21.0.

| Verification | Result |
| --- | --- |
| App-CLI Python | 86 tests; original core/v1 and native lifecycle fixture retained; same suite against an independently installed wheel |
| App-CLI runtime/provider | 275 passed, no skips; real resident process, lock/restart/lost reply, schemas, grants, task recovery, Host leases and migrated mail cases |
| SparkClaw Controller | 122 passed, no skips with disposable Chromium download enabled |
| Desktop / Bridge | 17 / 67 passed |
| Go Gateway | Entire `go test ./services/gateway/...` and `go vet ./services/gateway/...` passed |
| WebChat | 43 files / 169 tests passed; production build passed with an existing bundle-size warning |
| Browser component scripts | 14 passed, one optional external Tampermonkey-background test skipped |
| Paired release | Fresh external install, actual file tamper/mixed Python-runtime/consumer rejection, live service activation rejection |
| Compatible whole-set rollback | Two distinct artifact digests; original consumer + wheel + runtime restored; same durable state and strictly increasing epoch |
| Actual Electron | Installed Python public admission → resident Executor → signed HostPort → real owned non-mail page; original task replay and page/process cleanup |
| Ordinary browser regression | MCP/CLI navigate, read, fill, click, screenshot, download, popup, worker/OOPIF scope, personal-page/input isolation, renderer/main failure and session-cookie persistence |
| Gateway container | Actual Docker image build and isolated container entrypoint check passed; no host services replaced |
| Generated/public artifacts | Schema and projection parity, managed preload check, source hygiene and bilingual Markdown links |

An intermediate Electron run failed download qualification after dependencies
were reinstalled with lifecycle scripts disabled. Reapplying the existing
`install-playwright-downloads.mjs` installation step restored the required
Playwright hook; the complete run then passed. Installation and CI run this
step explicitly. No timeout was relaxed to make the check pass.

## Live mail follow-up: 2026-09-29

The existing dedicated browser profile was temporarily attached to the matched
`0.3.0-sparkclaw.2` Host/Executor for authorized test mail. All six directed
routes received their uniquely marked original. Each capture passed manifest
and file integrity validation, exact decoded Subject matching, and From/To route
checks. Outlook's actual outgoing address was learned from a received original;
its login alias was not used as an unverified delivery address.

| Route | Received original | Native send acknowledgment |
| --- | --- | --- |
| Gmail → QQ Mail | Verified | Confirmed |
| Gmail → Outlook | Verified | Confirmed |
| QQ Mail → Gmail | Verified | Unknown after regression; original verified separately |
| QQ Mail → Outlook | Verified | Unknown; original verified separately |
| Outlook → QQ Mail | Verified | Unknown; original verified separately |
| Outlook → Gmail | Verified | Confirmed |

This is cumulative evidence across candidate builds. The final Outlook → Gmail
and QQ Mail → Gmail confirmation regression use the pinned final release; the
other directions have not all been repeated at that digest. An unknown task is
never replayed or silently changed to completed. Its original journal and ledger
state are retained alongside separate recipient-original evidence.

The live failures led to these changes:

- Reload the owned browser daemon's private form values after `setSecrets`, so
  late values, quoted text and multiline bodies reach the native editor.
- Bind managed sends to the authenticated Reader mailbox, with the required
  snapshot interval, rather than an ambiguous account menu or login alias.
- Preserve QQ's native accessible recipient editor; distinguish Bcc toggles
  from editors; verify Outlook nickname pills against its bounded committed
  recipient model and reject contradictory, unresolved or extra recipients.
- Share native Sent-folder evidence between legacy and managed QQ/Outlook
  sends, ignore hidden cached rows, and retain uncertain outcomes if evidence
  is incomplete. Poll pending watch events without implicitly renewing authority.

Follow-up checks passed: App-CLI Python **86**, runtime/mail **283** (no skips),
Controller **123** (including actual Chromium download), qualification runner
**8**, focused Go emailautomation/browsercontrol packages, generated projections,
managed preload, and fresh paired install/tamper/mixed-version/whole-set rollback.
The complete isolated Electron run also passed normal browser operations,
non-mail public Registry execution, late secret injection into textarea and
contenteditable, page isolation, cleanup and renderer/main-process recovery.

Live receipt-only tests do not establish production notification latency,
multi-recipient sends, replies, attachments or every account-switch scenario.
[Sanitized live evidence](evaluation/app-cli-live-mail-20260929.json) records each
attempt, marker, task outcome and private-log digest without account addresses,
credentials or mailbox contents. The initial extraction evidence below remains
a historical record. The [new seven-job App-CLI CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36562915676)
passed at `6a46352` (release source `99a59e9`), including Windows/macOS/Linux
Python 3.11/3.13 and the lifecycle runtime. QQ native acknowledgment remains
unqualified after the final regression; all seven actually dispatched test
messages have separately verified received originals.

## Rebuild and consume a release

In the App-CLI fork, commit the reviewed implementation and run:

```bash
.venv/bin/python release/build.py
```

In SparkClaw, copy **all four files** from that `dist/release` into
`vendor/app-cli`: wheel, runtime tgz, `python-requirements.txt` and `release.json`.
Copy the same manifest to `configs/app-cli-release.json`. If the version/file
name changed, update the exact local dependency in
`tools/browser-controller/package.json` and regenerate its npm lockfile.
For the current version, synchronize all pinned projections and archive integrity:

```bash
npm run sync:app-cli-projections
npm ci --prefix tools/browser-controller
npm run build:desktop-managed-scripts
npm run check:app-cli-projections
npm run check:desktop-managed-scripts
npm run qualify:app-cli-release
```

No sibling checkout is needed after those files are vendored. The Python wheel
pins the actual runtime release digest; the service and the Controller's own
installed runtime package are verified before application admission. Binding
and release digests are checked again in the signed Host handshake. Reader page
ABI `SparkClawMailReader` remains compatible; implementation ownership is App-CLI.
Migrated Apache-2.0 sources retain their original attribution alongside the
upstream MIT core. No credentials, mailbox contents or private qualification
captures are included in these archives.

## Installation, activation and rollback

The existing setup entrypoint now stages the matched App-CLI release, drains
Executor then Host, installs the fixed consumer, and starts the owner services:

```bash
npm run setup:browser-controller
npm run check:browser-controller
```

Run as the desktop owner with the existing browser configuration and `.env.local`.
The new `sparkclaw-app-cli-executor.service` is tied to the Controller service.
Owner-private releases live below `$XDG_DATA_HOME/sparkclaw/app-cli` (default
`~/.local/share/sparkclaw/app-cli`). `state/` contains the durable Executor ledger,
Host epoch and signed grant/index files, independently of `releases/<digest>/`.
Gateway containers keep their authenticated Controller socket; they do not need
access to arbitrary host executables or an additional Python installation.

To stage/check without changing live services, use `scripts/install-app-cli.py`
with explicit `--root`, `--host-socket`, `--host-runtime-root` and `--workspace-root`.
`--check` verifies installed files. `--activate` refuses a reachable Controller
socket or a held Executor lock. Activation writes `previous.json` and
`current.json` atomically. `qualify-app-cli-release.py` performs installation,
failure probes and rollback entirely in temporary state and never uses accounts.

For rollback, restore the matching SparkClaw checkout (consumer dependency,
vendor manifest, Go projection and Desktop preload together), then rerun setup
and check with the **same owner state and workspace paths**. Restore/rebuild the
matching Desktop and Gateway artifacts when those are deployed separately. Only
ledger version 1-compatible releases may take over this state. A pre-extraction
legacy deployment is not a compatible whole-set rollback target. Unknown formats
or missing/older authority records require recovery; never delete the ledger,
authorization index, capture files or send journal to make a rollback start.
A failed switch remains unavailable until a verified matching set is restored.

## Final user acceptance

The extraction and follow-up fixes are available on the maintenance branches.
The live phase temporarily changed owner services and sent the authorized test
messages; the original deployment is restored after qualification, with the
new Executor stopped and its durable state preserved. This ends the temporary
qualification; it is not a compatible App-CLI ledger rollback to legacy code.
The legacy Gateway does not take over the new Executor ledger or workspaces.

Final acceptance should activate a matched Gateway/Controller/Desktop release,
verify the normal browser task page, then check cold/warm reads, notifications,
account changes and the remaining send variants. The original
[seven-job App-CLI CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36552474114)
is evidence for the initial extraction only. Windows skipped four POSIX
transport tests; non-POSIX Runtime v2 cleanup and SparkClaw cloud CI remain
outside that result.

Machine-readable sanitized evidence: [app-cli-extraction.json](evaluation/app-cli-extraction.json).

If page cleanup cannot be proven, the Host writes a private `cleanup-fence.json`
in its `cli-runtime` directory. Reaping a daemon does not erase this fence;
application admission stays disabled across Host restarts while ordinary browser
startup remains available. Stop Executor/Controller, restart the dedicated
browser and verify that the previous owned task pages are gone before an operator
archives the diagnostic and removes this **Host cleanup fence only**. Then rerun
setup/check. Never remove `authority.json`, ledger, grant index or send journal.

The initial Windows CI run exposed locale-dependent Unicode fixture reads; explicit UTF-8 fixed both failures. The final matrix is green.
