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

## Executed verification

Host: Linux ARM64, Python 3.12, Node 26.2.0. Actual isolated browser:
Electron 44.4.3 / Chromium 152.0.7977.130 / embedded Node 24.21.0.

| Verification | Result |
| --- | --- |
| App-CLI Python | 86 tests; original core/v1 and native lifecycle fixture retained; same suite against an independently installed wheel |
| App-CLI runtime/provider | 273 passed, no skips; real resident process, lock/restart/lost reply, schemas, grants, task recovery, Host leases and migrated mail cases |
| SparkClaw Controller | 121 passed, no skips with disposable Chromium download enabled |
| Desktop / Bridge | 17 / 67 passed |
| Go Gateway | Entire `go test ./services/gateway/...` and `go vet ./services/gateway/...` passed |
| WebChat | 43 files / 169 tests passed; production build passed with an existing bundle-size warning |
| Browser component scripts | 14 passed, one optional external Tampermonkey-background test skipped |
| Paired release | Fresh external install, actual file tamper/mixed Python-runtime/consumer rejection, live service activation rejection |
| Compatible whole-set rollback | Two distinct artifact digests; original consumer + wheel + runtime restored; same durable state and strictly increasing epoch |
| Actual Electron | Installed Python public admission → resident Executor → signed HostPort → real owned non-mail page; original task replay and page/process cleanup |
| Ordinary browser regression | MCP/CLI navigate, read, fill, click, screenshot, download, popup, worker/OOPIF scope, personal-page/input isolation, renderer/main failure and session-cookie persistence |
| Generated/public artifacts | Schema and projection parity, managed preload check, source hygiene and bilingual Markdown links |

An intermediate Electron run failed download qualification after dependencies
were reinstalled with lifecycle scripts disabled. Reapplying the existing
`install-playwright-downloads.mjs` installation step restored the required
Playwright hook; the complete run then passed. Installation and CI run this
step explicitly. No timeout was relaxed to make the check pass.

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

Implementation and the checks above are complete. Production services and real
mailboxes have not been changed by this work. Final acceptance should activate
the matched release in the intended environment, verify the normal browser task
page, then check QQ/Gmail/Outlook cold/warm reads, notifications, original files,
account changes and explicitly approved send/reconciliation. Synthetic provider
tests and actual local-browser tests are recorded separately from live-provider
results. Cloud CI and non-POSIX Runtime v2 cleanup are not claimed as executed.

Machine-readable sanitized evidence: [app-cli-extraction.json](evaluation/app-cli-extraction.json).
