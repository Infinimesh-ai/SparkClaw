# Repository mail runtime

> Language: English | [简体中文](../zh-cn/docs/local-mail-runtime.md)

As of 2026-10-15, SparkClaw owns its QQ Mail, Outlook and Gmail implementation in
`tools/browser-controller/mail-runtime`. Gateway calls the existing authenticated
Controller API; `LocalMailRuntimeFactory` invokes the local handlers through the
existing signed Browser Host, Playwright and Bridge. There is no App-CLI package,
Python Registry, separate Executor or paired release installation in this path.
The public mail API, account ownership, approval rules, original EmailPopup and
EmailCompose, attachments, browser profiles and desktop data remain unchanged.

## Source and generated assets

- Provider handlers, mail parsing, send/capture journals and page hooks:
  `tools/browser-controller/mail-runtime/applications/mail/`.
- Network Reader source: that directory's `userscripts/`; generated assets:
  `tools/browser-controller/mail-runtime/assets/mail/`.
- Generated bindings: `tools/browser-controller/mail-runtime/bindings/`.
- Gateway projection: `services/gateway/internal/emailautomation/provider_scripts.json`.

Use Node 26 and npm 11. Install the controller's pinned dependencies, then rebuild
from this repository when source changes:

```sh
npm ci --prefix tools/browser-controller
node tools/browser-controller/mail-runtime/applications/mail/userscripts/build.mjs
npm run build:mail-bindings
npm run sync:mail-projections
npm run build:desktop-managed-scripts
npm run check:mail-projections
npm run check:desktop-managed-scripts
```

The check verifies generated source bindings, Reader bytes/checksums/versions,
browser component registration and the Gateway projection. Existing Reader UUIDs,
names and page behavior are retained. MIT notices for the reused generic Host
code and Apache-2.0 notices for the mail implementation remain with the source.

## Persistence and recovery

`SPARKCLAW_MAIL_RUNTIME_STATE_DIR` selects private durable runtime state. The Linux
installer uses `$XDG_DATA_HOME/sparkclaw/browser-controller/mail-runtime`, with
the usual `$HOME/.local/share` fallback. The standalone entry defaults to a
profile-specific directory under the same data root. Keep it together with the
existing `SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT` during backup and restoration.

Reads, probes and watches release their per-call memory; they do not accumulate
an App-CLI task ledger or inherit its 10,000-task/512-MiB admission quota. Watches
retain independent provider lanes, signed leases, renewal and resync events.
Existing request-size, attachment, ownership and browser-operation bounds remain.

Only sends retain private durable operation records. Each binds the original
owner/profile/provider/task, approved input digest, execution evidence and outcome;
message text and browser credentials are not stored there. Creation and effect
markers are synced before execution/dispatch. Completed calls replay the receipt.
Interrupted or uncertain sends never reissue the original send. A proved
pre-effect blocked operation may resume within its original deadline and identity.
Reconciliation can use fresh credentials but must match the original approved
message, including attachment bytes; expired upload paths are not reread.

The existing workspace send journal stays in place. Older verified `sent` and
`not_sent` receipts can be recovered without opening a browser or resending. A
non-send proof keeps its original task, binding and evidence epoch. Missing evidence remains
unknown. Historical legacy failures without a journal are not proof of non-send.
The old App-CLI ledger is preserved but not imported. Gateway does not resume
interrupted legacy chat sends; do not manually replay a pre-migration legacy send
directly against Controller, because its old ledger tombstone is unavailable here.

## Installation and verification

`npm run setup:browser-controller` stops and disables an existing
`sparkclaw-app-cli-executor.service`, preserves its installation/unit/ledger and
the existing workspace/profile, then starts the ordinary Controller with local
mail state. `npm run check:browser-controller` verifies this state. No old ledger
or send journal is deleted or reset. The existing diagnostic `versions.app_cli`
field now reports `local-mail-runtime` for wire compatibility.

```sh
node tools/browser-controller/test/qualify-local-mail-deployment.mjs
SPARKCLAW_MAIL_BROWSER_TEST=1 node --test --test-timeout=120000 tools/browser-controller/test/*.test.mjs
python3 -m unittest scripts/test_local_mail_installation.py
```

With the root Electron dependencies installed (`npm ci`) and an isolated display
on Linux, the real browser gate is:

```sh
SPARKCLAW_MAIL_BROWSER_TEST=1 node tools/browser-controller/test/qualify-local-mail-browser.mjs
```

These use disposable storage and synthetic mail/browser fixtures. They do not
establish new live-provider delivery acceptance or upgrade an installed Gateway
or SparkX application. Prior provider delivery records remain historical. This
change is confined to SparkClaw; no upstream App-CLI or other-project protocol is
modified. InfiniCenter was absent at the configured ancestor paths, so no new
cross-project acceptance is claimed. The older App-CLI extraction documents are
historical; this page describes the current implementation and build path.

### Validation record, 2026-10-15

On macOS arm64 with Node 26.2.0 / npm 11, the full Controller suite with synthetic
mail browser tests enabled passed 324 tests (18 platform-specific or opt-in tests skipped).
Desktop passed 373 tests; WebChat passed 318 tests and its strict production build.
The browser installation/component suite passed 52 tests (one optional skip),
including fresh install, upgrade preservation and retirement failure handling.
Bindings, Reader source/asset parity, Gateway projection and desktop preload checks
passed. WebChat UI source and the desktop managed-script bundle are byte-identical
to the starting revision `1a4dfb0a`.

The disposable Electron 44.4.3 / Chromium 152 gate passed through
`Controller.runScript`, the local factory, signed Host and real Playwright CLI.
It verified private fields, concurrent retry joining, completed retry reuse and
normal cleanup against a loopback-only synthetic page. On macOS the fixture waits
for the real CLI close and verifies its recorded daemon PID has exited before
removing its private metadata. This does not qualify Linux `/proc` ownership or
crash recovery; production cleanup code is unchanged.

The four affected Go packages passed: `emailautomation`, `browsercontrol`,
`emailmanagement` and `mailsync`; full `go build ./...` and `go vet ./...` passed.
The full macOS Go suite still has 14 failures. All 14 were rerun against unchanged
Gateway source from `1a4dfb0a` and reproduced identical error locations and text
(temporary-directory symlinks, Linux `/dev/shm` and existing workflow/host failures).
This is no-new-failure evidence, not a claim that the full Go suite is green.
