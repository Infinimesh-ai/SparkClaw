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

## Work deferred until the main-branch merge: 2026-09-30

Handoff status: this checklist originally deferred execution until the merge.
The user has now authorized execution and main is merged; the active qualification
results are recorded below. Merging code does not pass production acceptance.

The handoff baseline is SparkClaw `cf28f87`, App-CLI fork `6a46352`, and consumed
release `0.3.0-sparkclaw.2` built from `99a59e9`. Preserve the cumulative six-route
delivery evidence, seven received originals and four historical uncertain
records in the sanitized report linked above. They do not prove that every
route passed on the final release. App-CLI remains maintained in the fork;
merging it back upstream is not a prerequisite for this work.

| Order | Work after merge | Completion criteria |
| --- | --- | --- |
| 1 | Verify the merge result and release scope | Record the main-branch commit, App-CLI source, paired artifact digests and features intended for release; check conflict resolutions affecting consumers, projections and Host, and run the relevant regressions |
| 2 | Fix QQ send acknowledgment and complete unknown-outcome handling | Diagnose insufficient native Sent evidence in App-CLI; normal sends obtain reliable acknowledgment; SparkClaw correctly presents unknown and reconciliation results without reporting failure or automatically resending; retain the four historical records and separate delivery evidence |
| 3 | Build and freeze the final release; verify the complete product path | Produce matching wheel/runtime/consumer projections after fixes, record digests and pass release checks; exercise the matched Gateway, Controller, Executor and Desktop from the product UI; preserve ordinary task pages, personal pages and login state |
| 4 | Repeat all six routes on the same final artifacts | Check send status, received original, exact subject and addresses for QQ↔Gmail, QQ↔Outlook and Gmail↔Outlook; use a fresh marker for each independent case without replaying old uncertain tasks; received mail does not substitute for send-acknowledgment acceptance |
| 5 | Verify real reads and send modes intended for release | Cover cold starts, repeated reuse, account changes and expired login for all providers; test multiple recipients, Cc, reply/reply-all and original downloads within the release scope; do not mark unpassed modes accepted or expand scope to attachment sending merely because of this checklist |
| 6 | Verify incoming notifications and sustained operation | Exercise real arrival through watch, capture, deduplication, ingestion and UI; cover both read/watch startup orders, multiple authorization renewals and gap backfill after disconnection; record notification latency and page/process/resource trends |
| 7 | Rehearse recovery and compatible whole-set rollback in the target deployment | Exercise Gateway/Executor/Controller/browser restarts, disconnects, cancellation, authorization expiry and credential changes; prevent duplicate sends and unauthorized continuation, fence failed cleanup; preserve ledger, journals, authority records, originals and login state through compatible rollback |
| 8 | Assemble final evidence for user acceptance | Record final artifact digests, results and remaining limits for every item; align bilingual documentation with sanitized evidence; the user performs final acceptance before production enablement scope is finalized |

Execute 1→2→3, then 4–7 on the same final artifacts, followed by 8. If relevant
implementation changes, rerun affected cases and update evidence instead of
reusing a pass tied to old digests. Provider business fixes remain in App-CLI;
SparkClaw owns only generic Host behavior, upper-layer calls and product state.
Changes to a frozen cross-project contract still require an InfiniCenter decision.

At the end of the 2026-09-29 temporary qualification, original services were
restored, the temporary Executor was disabled and durable state retained. That
was not a compatible rollback handing new ledger state to pre-extraction code.
Resume from existing records without clearing state or replaying old mail.
This checklist schedules no background monitoring or automatic continuation.

## Post-merge qualification: 2026-09-30

**Bounded qualification has remaining acceptance gates; production is not accepted.**
The workspace was committed before main merged at `6a6f665`; the original
SparkClaw worktree and merged branches were removed with private state backed up.
The final candidate is `0.3.0-sparkclaw.11`, App-CLI source `9da7cfd`, freeze
commit `c08d396`, runtime digest
`748eb24a2a9646e274d777680f9ff54ac0b77bf6b58a8e191fddd2efd0996000`.
The post-fix consumer is `41caf5a`, including Outlook receipt precision fix
`44e0dac`. [Sanitized closeout evidence](evaluation/app-cli-closeout-20260930.json)
retains each attempt, request digest, outcome, artifact pin and remaining gate.
Scope is the existing three accounts; account switching is deferred by the user
and attachment sending is out of scope.

App-CLI fixes cover concurrent shared-page initialization, watch/finite-read
execution lanes, watch preservation during idle draining and failed partial
capture, original-task access across compatible bindings, journal-only
reconciliation, renewal replies, Gmail minimized compose restoration and the
initial live-lease window. SparkClaw fixes generic resource reservations,
product/cache subscription identities and the nested draft Nginx proxy. Outlook
retry receipts now compare timestamps at PostgreSQL microsecond precision while
preserving exact locators and all other identity fields; a new discovery candidate
is retained unless explicit source recovery requires the stored target. A live
probe uses a fresh invocation identity rather than conflicting with an earlier
immutable grant. Frozen contracts and authority windows are unchanged.

All six cold/warm read-first/watch-first combinations and explicit cancellation
pass in **196.06 seconds without sending**. Python **86**, runtime **299**,
Controller **127**, full Go build/vet/tests, projection/preload parity and paired
installation/tamper/mixed-version/live-activation rejection pass.
[App-CLI CI](https://github.com/ZZZZJJJ0928/App-CLI/actions/runs/36683544137)
is green. Full isolated Electron qualification passes ordinary MCP/CLI and
public App-CLI operations, personal-page/input isolation, cookies, downloads and
renderer/main recovery; two earlier MCP timeouts remain recorded.

The following independent routes use the same final artifacts after the consumer
fix. Each test sends once with a new marker. Earlier candidates and timed-out
attempts do not become passes after later delivery.

| Route | Native Send | Gateway original and exact From/To | Result |
| --- | --- | --- | --- |
| Gmail → Outlook | Confirmed | Not passed | FAIL |
| Gmail → QQ Mail | Confirmed | Verified | PASS |
| Outlook → Gmail | Confirmed | Verified | PASS |
| Outlook → QQ Mail | Confirmed | Verified | PASS |
| QQ Mail → Gmail | Confirmed | Verified | PASS |
| QQ Mail → Outlook | Confirmed | Not passed | FAIL |

New Outlook inbound originals returned `email_network_original_unqualified`,
preventing receipt acceptance despite native Send confirmation. The two earlier
Gmail→Outlook originals were subsequently persisted and parsed after the
microsecond fix; their original timed-out tests remain failed and neither task
was resent. After restoring the original Gateway/Controller and restarting the
browser, the two new timed-out route originals and the multi-To/Cc Outlook
original were also persisted with exact headers. This is separate legacy-recovery
evidence, not a pass on the final candidate. The failed partial captures retain
the active watch rather than
silently passing the mail. Send-to-original age includes native operations and
is not pure notification latency.

Product pairing used the existing token. A real UI compose with two To addresses
and one Cc obtained native confirmation; its received QQ original has exact
Subject/From/To/Cc and a verified file hash. Outlook receipt during the final-candidate test is not
accepted; its later original is recorded under deployment restoration. A real reply draft was saved, but sending stopped before the effect
boundary with `EMAIL_REPLY_TARGET_UNVERIFIED`; the original task was not retried.
Gmail's selected network target was not proven open in the native reply view.
Reply-all excludes every known own mailbox address, so the three-owned-account
scope has no meaningful live positive recipient case. Replies/reply-all remain
outside accepted modes. Final-candidate download was clicked and its authenticated
file endpoint returned the exact persisted original (HTTP 200); the browser did
not expose a saved file. The earlier actual browser download of a `.8` send remains
historical evidence, not a final-candidate download pass.

After the consumer fix, the final set ran for 1823.94 seconds with 181 samples. Owned pages ranged from 3 to 4, stale pages remained 0, and the task window was never focused. Whole-profile RSS ranged from 7519 to 8200 MiB; this includes personal tabs and CLI daemons. The watch task identities and renewal observations are in the sanitized evidence. This passes the bounded topology/renewal observation, while the original-capture failures remain open.

The target deployment completed an actual compatible whole-set switch from the
final digest to a distinct NOTICE-only paired fixture and back, including matched
Gateway/consumer/wheel/runtime/projections. Epochs advanced **56→90→91**, all
**39** previous Send records and **2357** immutable grants/journals/originals
were unchanged, and all three real login probes passed on the fixture and return.
The initially retained cleanup fence blocked application admission; only that
Host fence was archived after browser restart proved zero old task pages.
This qualifies a compatible fixture with unchanged runtime code and ledger v1,
not arbitrary older releases or legacy consumption of the new ledger.

A real watch reached `waiting_confirmation / AUTHORIZATION_EXPIRED` and was
explicitly canceled. An invalid browser credential was rejected. These checks do
not establish real provider-login expiry or actual credential rotation. Killing
the real Gateway after the Send effect boundary produced one uncertain task;
the UI disabled Send and reported “result pending confirmation.” Public
reconciliation left the original request and task uncertain with zero additional
Send admissions. Both recipient originals verified separately; they do not
replace missing native confirmation. The four historical uncertain tasks also
retain their request hashes under public journal-only reconciliation without
current browser credentials or replay.

The original Gateway/WebChat containers and archived pre-extraction Controller implementation have been restored and checked. The original WebChat container retains the tested one-line draft proxy fix in its template and active Nginx configuration; its image/container identity is unchanged. Pairing and the unknown draft remain visible, with Send disabled. The candidate Executor is stopped and disabled; its ledger, grants, authority records, journals and originals remain preserved. This restoration does not hand the new Executor ledger to legacy code.

Checklist 1–3 are covered; 4–7 retain the failed/deferred gates detailed above.
Evidence assembly is complete when the bounded tests and restoration are recorded;
checklist 8 still requires the user's final acceptance and production scope.
Real login expiry, credential rotation, account switching, final browser-saved
download and unpassed send modes cannot be marked accepted. Historical Gmail
coverage gaps and current processing warnings remain visible.

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

The SparkClaw extraction and follow-up fixes are merged into main; provider
implementation remains on the App-CLI maintenance fork. The 2026-09-29 service
restoration is historical; the 2026-09-30 qualification and deployment status
above is authoritative for this closeout. Restoring original services stops the
new Executor while retaining its durable state, rather than handing its ledger
to legacy code. The Gateway continues to use its canonical mailbox storage and
retained original files.

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
