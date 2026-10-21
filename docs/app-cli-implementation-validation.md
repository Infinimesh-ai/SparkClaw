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

## Concurrent Host lease retirement: 2026-10-09

The local paired release `0.3.0-sparkclaw.14` fixes simultaneous watch/read
retirement. Both leases previously became retired before either cleanup resumed;
counting their remaining set entries published an empty activity stamp with no
idle authority. The daemon watchdog could then close the page before explicit
Host cleanup and correctly leave an unproven-cleanup fence.

Retirement now aborts each lease immediately and serializes cleanup decisions for
the shared resource. Only non-retired activities count toward retention; the last
release closes directly, and subsequent releases cannot republish a disposed
resource. Explicit parking remains bounded. Actual expiry and epoch revocation
still close the page, and unknown cleanup failures remain fenced, including
`page_closed`; the fix does not reinterpret that error as proven cleanup.

The deterministic fixture combines the real Host port, atomic lease file writer
and daemon watchdog, inspecting every publication without sleeps. Both concurrent
orders fail against `.13` and pass against `.14`. All eight retirement, active
watch, parked expiry, immediate abort, epoch revocation and cleanup-fence cases
pass. Isolated Linux Controller verification passes **157** tests with **8**
real-Chromium cases skipped; macOS passes **140** with **25** platform/browser
cases skipped. Paired installation, tamper/mixed-release rejection, compatible
whole-set rollback, retained durable state and increasing epochs pass in disposable
state. No installed service or provider business acceptance is claimed here.

Reproduce from the retained `.13` bundle with
`python3 scripts/build-lease-retirement-release.py --check` using Node 26 and
installed Controller dependencies. Only `src/host-port.mjs`, package versions and
release metadata change in the runtime; the wheel updates its version and bound
runtime digest. All **14** Reader source/assets files remain byte-identical,
including their versions; browser component policy, Go provider projection and
Desktop managed Reader projection have no changes. Projection checks pass.
The `.14` runtime manifest digest is
`ae1786b8140cb456ed8183ba7b275a3611be67c5d4ba7c3c4214b6967f642fea`;
artifact digests and the reproducible local patch chain are recorded in the
consumer release manifest. Existing `.13` artifacts remain unchanged. The
separate watch error-code normalization issue is outside this patch.

## Controller shutdown failure cleanup: 2026-10-09

The work2 stop journal records Executor exit at 09:14:26 UTC followed by
Controller's 60-second systemd deadline and SIGKILL. It has no shutdown phase
trace, so it cannot identify a unique internal blocking point. An isolated
reproduction establishes a matching defect: cancelling a watch after Executor
exit rejects `MailObserverFeed.close`, skips factory/Host cleanup and leaves the
private event listener referenced. Public HTTP closes and shutdown rejects in
1 ms, but the process cannot exit naturally. The 60 seconds is the external kill
deadline, not a successful bounded drain.

Controller now attempts factory and ordinary reservation cleanup even after feed
failure. Host-owned application reservations finish through factory cleanup;
failed cleanup remains fenced without falsely completing business work. The
driver settles all owned handles and closes private event connections/listener in
`finally`, preserving the original failure and persistent cleanup fence. App-CLI
`.15`, Reader assets, authority rules and service deadlines are unchanged.

Six real-class regressions include both Unix listeners, an incomplete actual HTTP
request, an ordinary MCP reservation, stopped Executor cancellation, normal
shutdown, page/reaping failures and multiple handle cleanup. macOS passes **152**
tests with **25** platform/browser skips; isolated Linux passes **169** with **8**
actual-Chromium skips. No running service was changed during this check; a new
target-host graceful stop still needs coordinated validation. See the
[sanitized evidence and inference limits](evidence/browser-controller-shutdown-2026-10-09.json).

## Controller systemd stop ordering: 2026-10-10

The generated Controller unit now uses `KillMode=mixed` with explicit
`SendSIGKILL=yes`, retaining `TimeoutStopSec=60`. Initial SIGTERM reaches the
Controller, allowing it to close its owned CLI daemons. If the Controller exits
while children remain, or exceeds the stop deadline, systemd still kills the
remaining cgroup. Executor and Browser unit policies are unchanged. This prevents
`control-group` from independently terminating a daemon while its Controller is
still draining work and about to use that daemon for explicit page cleanup.

[The executable qualifier](../scripts/qualify-controller-stop-order.py) runs
four disposable `systemd --user` transient units with a pure Python parent,
detached child and private Unix socket. It never connects to a Browser or reads
application data. An explicit drain barrier makes signal delivery observable:
`control-group` terminates the child and cleanup fails (exit 1); `mixed` lets the
parent close its owned resource and both processes exit normally. With `mixed`,
parent failure (exit 7) and a three-second fixture timeout both trigger final
SIGKILL of remaining processes. All four cases pass on systemd 259, with empty
cgroups, no remaining fixture PIDs/units, and no existing service changes.
Production keeps its 60-second deadline. Run
`python3 scripts/qualify-controller-stop-order.py` on Linux with a user manager;
the matching setup script must be beside it. Setup tests **13**, Python compile
and shell syntax checks also pass.

The archived QQ `tab-list / process_exit_page_closed` event lacks an old-PID
signal trace and original CLI error; this is a demonstrated configuration hazard,
not proof of its unique historical cause. The error still cannot prove that an
owned browser page is absent; cleanup fences and their operator recovery rules
are unchanged. Real deployed graceful shutdown remains a separate acceptance
gate. See [sanitized systemd evidence](evidence/browser-controller-stop-order-2026-10-10.json).

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
admissions are rejected with `browser_cleanup_pending` (Gateway
`email_browser_cleanup_pending`, shown as a readable hint on Check connection)
and logged as `application_cleanup_fenced` at most every 10 minutes.

The fence records its scope. A `page` fence means every owned process was reaped
and only the task page closure was unproven, for example a page already closed
under the CLI. A later admission clears it, logging
`application_cleanup_fence_cleared`, only when this Host owns no other resource
and the Bridge reports no task-owned or stale task tab in any window (asked at
most every 15 seconds, and only when `SPARKCLAW_BROWSER_BRIDGE_NATIVE_SOCKET` is
configured). A `page` fence does not disable Host startup. A `process` fence
(reaping failed) or a fence without a scope still disables admission across
restarts and is never cleared automatically: stop Executor/Controller, restart
the dedicated browser and verify that the previous owned task pages are gone
before an operator archives the diagnostic and removes this **Host cleanup fence
only**. Then rerun setup/check. Never remove `authority.json`, ledger, grant
index or send journal.

The initial Windows CI run exposed locale-dependent Unicode fixture reads; explicit UTF-8 fixed both failures. The final matrix is green.

## 2026-10-09 native attachment and send recovery candidate

The matched `.16` runtime associates Outlook's shared Ribbon file action with the
single owned composer and the exact input activated by its native file chooser.
It rejects ambiguous composers/actions, image-only inputs, foreign chooser events
and replaced inputs before uploading verified desktop-origin bytes. QQ's ready-row
contract remains subject to actual provider qualification.

Receipt reconciliation compares immutable reviewed attachment metadata without
reopening an expired staging path. An authenticated inactive original task may
return a durable `not_sent` proof bound to invocation, task, intent, resource,
release binding and ledger epoch. The `.15` QQ migration is limited to the exact
pinned binding and recorded pre-dispatch failure; missing journals alone never
prove non-delivery. Gateway resolves only the same draft version and send attempt,
retains the original evidence, then allows a new review. Unknown or dispatching
attempts remain fenced and are never resent automatically.

[Candidate evidence](evidence/mail-native-attachments-candidate-2026-10-09.json)
records 59 combination tests, 12 real Chromium fixtures, paired artifact
reproduction and install/rollback checks. These checks do not establish actual
provider dispatch, delivery or incoming attachment acceptance.


## Unlimited cumulative task history (2026-10-15)

Release `0.3.0-sparkclaw.21` removes the default 10,000-task admission cap.
`maxTasks=0` skips history counting; a positive integer retains an optional cap.
The independent 512 MiB allocated-data watermark, event retention, OS lock,
execution epoch, request-key records, authorization index and send journals remain.
Existing ledger v1 databases need no migration or deletion. Capacity rejection
and the Host cleanup fence are distinct: removing a cleanup fence cannot repair
a full ledger, and changing capacity does not prove old pages were cleaned up.

The paired release applies `ledger-unbounded-history.patch` to the exact `.20`
archive, preserving all earlier mail and Host fixes. Reproduce it with
`python3 scripts/build-ledger-unbounded-release.py --check` under Node 26.
The upstream fork receives the same Ledger change and standalone regression tests.

Validation: Node 26.2.0 runtime 301/301, Python 86/86, installed Controller
206 passed with 124 platform/opt-in skips; separate installed-Python reconciliation
and task-history checks exercise the real public client. The new regression
fails on the old release, retains 10,000 synthetic completed tasks across restart,
admits task 10,001 and preserves replay, conflicts and original events. Explicit
task caps and byte admission remain covered. Disposable paired installation,
tamper/mixed-release rejection and state-preserving rollback pass. No real mail
is sent by these tests. macOS suites use canonical `/private/tmp`; the default
temporary-directory alias otherwise triggers existing path-privacy guards.


Work2 activation completed on 2026-10-15 at 16:50 China time after a cold paired
backup. All 10,000 original task rows were unchanged; the execution epoch advanced
from 26 to 27. The business database and workspace matched before Gateway restart,
and existing configuration, the Gateway container and its image were retained.
Only the Controller/Executor integration switched to `.21`; the Gateway source
difference involved comments and tests, so no binary replacement was needed.
The other four servers and the installed desktop application were unchanged.

Live verification subsequently observed 10,008 tasks, including three completed
QQ page collections and two completed Outlook page collections, with both sync
watermarks advancing and no last-sync error. The original 10,000 row hashes still
matched and the cleanup fence remained absent. Existing observer status remained
`degraded`; this verifies page synchronization, not a recovered push notification
watch. No test send was performed. Source: SparkClaw `13a2a4fe`, maintenance fork
`c4ea157`; these commits have not been published to GitHub by this operation.


## Expired watch admission recovery (2026-10-15)

Paired `.22` fixes a watch that remained `degraded` after ledger capacity was
restored. The signed authorization index was durable before the refused admission,
so every retry found the same unresolved request. A successful public lookup must
confirm an unresolved outcome, and the original signed grant must be an expired,
unbound, renewable, read-only watch for this application. Only then may the mail
client create another watch with `previous_request_key` in its signed resource.
It preserves the original index and all history, emits an explicit gap and requests
resynchronization. Live grants and known tasks retain their existing recovery
rules. Generic ProductClient unresolved admission and send retry rules are unchanged.

`watch-admission-recovery.patch` applies to the exact `.21` archive; reproduce with
`python3 scripts/build-watch-recovery-release.py --check` under Node 26. Real
Python/Unix-socket/SQLite tests cover capacity rejection, cold restart with unchanged
or rotated credentials, expiry, prior-row/index preservation, linked gap,
notification delivery, renewal and known-task reuse. Seven authority rejection
cases fail closed. Controller: 213 pass, 126 platform/opt-in skips; the two installed
Python watch scenarios additionally pass when explicitly enabled. Paired install,
tamper/mismatch rejection and state-preserving rollback pass. These tests use
synthetic messages and never send mail. Production results are recorded separately.


Work2 production acceptance on 2026-10-15: `.22` activated at 17:11 China time,
advancing epoch 27→28. All 10,040 pre-upgrade task rows remain hash-identical;
business database and workspace matched before Gateway resumed. The Gateway
container/image and configuration are retained. QQ and Outlook both emitted
`watch_ready` and a gap linked to the original unresolved request; Gateway state
is `watching`, catch-up completed and sync errors are absent. Both retain their
original new task IDs, renew authorization revision 1→2 and remain running beyond
the original five-minute authorization expiry, without degradation or a cleanup
fence. Heartbeats keep the real watches alive. No test email was sent to validate
a fresh real inbound notification; isolated regression covers synthetic delivery.

The cold rollback checkpoint is `/var/backups/sparkclaw-app-cli-watch-20261015`;
never restore old databases or authority. The other four servers and installed
Mac client are unchanged. SparkClaw `faacf177`, maintenance fork `d56dc9a`; no
upstream push was performed.


## Real incoming mail and Outlook original authorization (2026-10-15)

User-authorized real QQ/Outlook cross-mail exposed an additional `.22` failure.
QQ delivered its incoming notification to the Gateway in 2.86 seconds and the
message was discovered in 8.54 seconds, with complete capture and ready parsing
observed by 11.41 seconds. Outlook emitted an incoming notification in about one
second and listed the exact received item, but original download returned HTTP
400 and `email_network_original_unqualified`; this was not a successful sync.
The initial QQ send used an internal Outlook account identifier and bounced. A
distinct corrected send used the externally deliverable address proven by the
other test message's From header. No unknown-outcome send was retried.

The live consumer mailbox uses Outlook's native Entra header authorization. Its
old query download token is expired, and the native legacy refresh function does
nothing in this mode. Both the site's native downloader and its native header
authorizer followed by the existing bounded fetch returned HTTP 200 and the
exact test original. Paired `.23` therefore resolves the native Authorization or
X-Token header inside the mailbox page. The returned URL must equal the verified
DownloadMessage target with only the token parameter removed. Account, original
message ID, origin/path, header bounds, cancellation, redirect rejection and
original-byte validation remain enforced. Credentials never enter results or
diagnostics. Legacy tokens still use the native expiry/refresh policy.

A separate consumer mapping preserves `browser_busy` for explicit blocked
read/capture/collect_page results, so the existing Gateway short yield retains
the original invocation instead of classifying contention as a provider outage.
Uncertain results and sends keep their original behavior. This addresses the
contention observed while the test sender held the shared browser.

Reproduce with `python3 scripts/build-outlook-authorization-release.py --check`.
The delta applies to the exact `.22` archive, preserving its deployed account
identity behavior; the maintenance fork also receives that existing identity
fix before regenerating Readers. Source: SparkClaw `cd3de78a`, fork `3376fdf`.
Validation: runtime 321 pass / 2 opt-in skips; Controller 258 pass / 126 platform
or opt-in skips; work2 staged tests 78 pass, including installed Python recovery;
Gateway busy-yield regression passes. Deterministic paired build, independent
installation, mixed/tampered release rejection and state-preserving whole-set
rollback pass. Public Lifecycle 2.0, Host 1.0, ledger 1 and UI are unchanged.

Three activation attempts rejected a nonzero Controller shutdown and restored
`.22` without replacing code or rolling back data. The Executor has systemd
PartOf/ordering ties to the Controller; changing stop order alone cannot drain
in-flight watch registration. The successful retry reconciled the observer set
to empty, waited for all active application tasks to retire, then required clean
service exits, no owned processes, no cleanup fence and a free Executor lock.
The earlier Outlook test original was recovered after a `.22` page restart at
17:48:09 China time, before `.23` activation; that recovery alone does not prove
the new authorization path or its long-lived-page behavior.

Runtime activation alone initially left the browser-managed Readers unchanged.
The acceptance gate detected stale imported source hashes. Chromium retained an
older worker registration, so the managed extension was advanced to product r11
and the desktop source projections were regenerated (`c9de358b`). Three component
attempts still failed the imported-policy gate and restored public components,
without rolling back data. The native Chrome extension UI then exposed the cause:
the unpacked CRX contained `_metadata/verified_contents.json`, which Chromium
rejects as a reserved name. Source `5eed92a5` excludes only this signed-package
metadata after archive path validation, retains the verified CRX digest and all
extension code, and rejects reuse of an installation containing that directory.
All 20 deployment regressions pass locally and on work2, including metadata
exclusion, traversal rejection and rejecting an otherwise current bad install.
Native policy inspection confirmed the correct CfT policy location and no policy
error; replacing profiles, deleting extension databases or changing mailbox
authorization scopes was unnecessary.

The corrected package then encountered Chrome's persisted disabled state from
that failed native load. With Gateway admission paused, observer tasks drained,
Controller/Executor stopped, no live CLI processes and no cleanup fence, the
operator restored Chrome's native developer mode for this trusted unpacked
product extension, enabled it and reloaded it through the native extension UI.
The exact four managed UUID/source/dependency checks finally passed. A nonzero
Controller exit during the preceding broken-extension shutdown was retained as
evidence; no fence or process was bypassed. Runtime history, authority and mail
storage were not reset. This is specific to the dedicated product Profile.

At 18:36 China time, the services resumed on `.23` with browser product r11 and
SparkClaw source `5eed92a5`. Epoch 36→37; all 10,233 paused task rows matched
before Gateway restart. The original 10,000 and pre-watch 10,040 rows still match
exactly. All 10,141 pre-authorization task identities and immutable intent fields
remain; one existing uncertain QQ collection legitimately reconciled to complete.
The Gateway container/image/configuration remain unchanged. Cold checkpoint:
`/var/backups/sparkclaw-app-cli-managed-readers-20261015-r11-clean`; the final
native reload receipt and ledger checks are under the corresponding
`-native-reload` checkpoint. Other servers and the installed Mac are unchanged.

Final real-mail acceptance used unique test marker
`SC-W2-LIVE-20261015T103657Z-18b319`, with one native browser send in each
direction and a durable `sent` receipt for each. Timing starts at the provider's
received timestamp, not the sender's MIME Date. The complete column is the first
bounded-monitor observation of complete capture and ready parsing:

| Receiving mailbox | Native notification observed | Gateway notified | Message discovered | Complete sync observed |
| --- | --- | --- | --- | --- |
| QQ | 2.27 s | 2.87 s | 9.34 s | 11.43 s |
| Outlook | 0.53 s | 3.25 s | 23.88 s | 31.51 s |

Both subjects matched exactly once; original/manifest SHA256, MIME From/To,
subject, parsed body and inbound identity all passed. Outlook's unchanged page
started at 10:36:34.790 UTC. Its obsolete query token expired at 10:40:54, before
the incoming message at 10:42:31.253 and successful capture at 10:43:02.262.
The loaded Reader reports native authorization stage `authorized`; the final
page age was 455 seconds. This verifies the actual managed fix after expiry.

At 18:44 China time both mailboxes are `watching`, sync is idle and last sync
errors are absent. Both retain their original epoch-37 watch IDs and have renewed
revision 1→2→3. Gateway is healthy; Browser, Controller and Executor are active
with zero restarts, and no cleanup fence exists. All fixed configuration and
authority hashes match. The temporary test monitor was stopped; diagnostic tabs
were closed and test emails/history retained. A separate QQ `mark_read` task
returned `INVALID_REQUEST` and remains fenced; it was not retried or included in
this incoming-sync acceptance. No claim is made about repairing mark-read.

Private receipts, monitor samples and redacted timing acceptance are retained in
`/Users/dev/.cache/app-cli-live-inbound-20261015/managed/`; no mailbox addresses,
tokens or original email content are committed. Final documentation mirror/link
checks cover 113 files; generated desktop Reader checks pass. No GitHub push or
other-node/installed-desktop deployment was performed.

## Lifecycle 3.0 and ledger 2 (2026-10-16)

Paired release `0.3.0-sparkclaw.24` (runtime digest `bc177aa3…`, App-CLI `fc18087` on branch `codex/lifecycle-v3`) implements the revised [request and storage lifecycle design](app-cli-storage-lifecycle-design.md): op3 operation identities in request epochs with product-confirmed drain and seal, prepare/invoke admission, reserved → consume_pending → released dispatch confirmed by an independent progress anchor, automatic rollback fence-forward, durable ACK, user resolution of unknown sends, three fixed authorization slots, sealed-epoch reclamation and storage-based admission. SparkClaw `78035c55` adds the Gateway outbox identity, ACK retry, drained-seal loop, `POST /api/email/drafts/{draft}/resolve` and ISCP `mail.drafts.resolve`, the resolution choices inside the existing unknown-send status, and the standalone `progress-anchor` service.

Local results: App-CLI Node 348/348 (including Python-gated recovery tests) and Python 86; Controller 265 pass with 119 platform or opt-in skips; Desktop 373; WebChat 319; Go packages pass except 13 macOS-only TLS/`/dev/shm` tests that also fail on the unchanged baseline. Release qualification, staged install and deployment qualification pass; the JavaScript anchor client was checked against the Go anchor service.

work2 shadow migration copied the live ledger (12,352 tasks) and opened it with ledger 2: all rows matched except the single running row, which became `blocked/EXECUTOR_RESTARTED` as on any restart. Migration took 58 ms, legacy keys still resolve, absent legacy keys are `retired(legacy)`, and no legacy row is reclaimable.

The first activation attempt failed after the migration checks had passed, on a path error in the verification script; automatic rollback restored the drained state, source `5eed92a5`, release `.23` and the original Gateway without errors. The second attempt completed at 06:38 UTC: 12,368 tasks preserved with zero rows changed, schema 2, execution epoch 38 → 39, business database and mail workspace unchanged, anchor seeded at generation 1. Three minutes later the new Gateway was healthy with no restarts or errors, both watches were running with `watch_ready`, collections and probes were completing under op3 identities, the Controller reported `lifecycle-3.0` and epoch control answered. No mail was sent; a real send, its ACK and a user resolution remain for user acceptance. Capacity and retention values remain design placeholders until shadow measurement. The anchor runs on the same host, so it detects only rollbacks that do not include its own state directory (design profile B requires another host).

### Release 26 follow-up (2026-10-16)

Paired release `0.3.0-sparkclaw.26` (App-CLI `90b0f8a`) and SparkClaw `49875de6` close two gaps found after the first activation. A send that never released its dispatch is cancelled in the runtime and reported as `email_send_not_dispatched`, a definite failure that can be reviewed and sent again; reconciliation closes such an unknown draft instead of leaving it stuck, while a retired remote write stays unknown. The lifecycle maintenance loop reaches epoch control through any bound mailbox, so a deployment without drafts still seals drained epochs. Anchor release retries back off and fail fast during a short cooldown. App-CLI lifecycle documents describe protocol 3.0.

work2 was upgraded from `.24` at 07:14 UTC without migration: 12,433 tasks preserved with zero rows changed, execution epoch 39 → 40, business database and workspace unchanged. A rotation then opened request epoch 2; the Gateway confirmed the drain and sealed epoch 1 with reason `drained` within one maintenance cycle. The anchor advanced to sequence 1 on a real `mark_read` release. That `mark_read` remains uncertain because the command releases before page preparation (an existing limitation). Startup QQ watch failures were replaced automatically, as in earlier epochs.

### Release 27: mark_read release point (2026-10-16)

Paired release `0.3.0-sparkclaw.27` (App-CLI `24c71cb`) and SparkClaw `5e96498a` move the `mark_read` dispatch release from handler start to immediately before the native read-state request, after the exact target is reselected and the pending read intent is durable. Preparation failures, such as an unavailable account identity, now stay provably undispatched instead of leaving an uncertain remote write; a failed release never reaches the provider and is not recorded as an unknown read. work2 was upgraded at 07:29 UTC without changing the Gateway container: 12,458 tasks preserved with zero rows changed, execution epoch 40 → 41, both watches running and all services without restarts. No `mark_read` ran during verification because it marks real messages; it remains part of user acceptance. The earlier uncertain `mark_read` (task `c490b6ce`) was then resolved through the lifecycle `resolve` operation as `confirmed_not_sent`: its `read-state.json` was never written, and the pre-27 flow writes it as pending before any native read request. The resolution records an evidence digest, dispatches nothing, and leaves no uncertain lifecycle 3.0 task on work2.


### Release 27 rollout to work1, work3, work4 and work5 (2026-10-16)

At the user's request, the four remaining nodes now use source `2e7814db` and paired release `0.3.0-sparkclaw.27`. Their Gateway binary is byte-identical to the already deployed work2 binary from `49875de6` (SHA256 `0bd9ed2874875e1079a97f4e513e7ef183ac0a58adc13cc6abf2bd32f20ebf2e`). work2 retains its original running container, start time and source `5e96498a`; it was checked without an upgrade or restart.

The four nodes had no bound mailboxes, drafts or send snapshots, and their dormant App-CLI ledgers contained zero tasks. Each ledger was migrated on a shadow copy before activation, then upgraded from schema 1 to 2 while retaining authority: execution epochs became work1 1 → 2, work3 1 → 2, work4 9 → 10 and work5 6 → 7. The old local-mail runtime state remains intact. Controller, Executor, Browser and the same-host progress anchor are enabled and active.

All four Gateways are healthy with zero restarts. Original rows in all 41 business tables, workspace files, model/configuration and authorization identities are preserved; Docker environment, HostConfig, commands and network addresses match the original deployments. Each node passed a Relay HTTPS health request with its existing pinned certificate. Matching functional and UI acceptance was reused; no new model benchmark, browser UI suite or real email operation was performed.

Each upgraded node retains `/var/backups/sparkclaw-r27-20261016` with a verified PostgreSQL dump, paired state archive, configuration/row fingerprints and receipt, plus a stopped rollback container. App-CLI ledger 2 must not be started with the old `.20` executor. A software rollback must keep new execution evidence; do not restore old authority or database snapshots as a routine downgrade. The progress anchor retains work2's same-host scope and does not detect a whole-host rollback that includes its own state. InfiniCenter status records this authorized rollout; proposals 0031/0035 and the public contracts were not changed to accepted by this deployment.

### Release 28: receive reliability and resident pages (2026-10-21)

Paired App-CLI `0.3.0-sparkclaw.28` uses source `6fe3b6c` and runtime digest `0991c8bd3d6dba5ffbef6414830438c0cb61aee7eaf30504514b338c4342d191`. QQ mark-read now forwards the original receipt timestamp when reselecting its exact target; the Gateway compares decoded target times by value, including nanoseconds. Identity drift still rejects before dispatch.

Periodic authorization maintenance formerly scanned every live index synchronously despite its deletion limit. A read-only work2 observation measured an 8.29-second Controller stall. An isolated same-host experiment with 45,352 synthetic files reproduced the keep-alive `ECONNRESET` with the old scan; bounded asynchronous scanning completed with 55 successful HTTP requests and a maximum 20.48 ms response time. Watch polling now reads the original task state before renewal and resolves termination races by querying that same task. No Host effect is retried.

Receive watches, network discovery, original capture, thread inventory and current-watch-bound probes share a page scoped to the exact owner/account/credentials/binding. Each round verifies document and account identity and resets temporary reader data. Send and mark-read retain independent pages. Stopping a watch lets an admitted Reader finish and then closes the page, regardless of release order; renewing a valid watch keeps the page resident beyond the idle window.

The existing per-provider Check connection feedback reports actual synchronization progress, including empty successful rounds. It does not add persistent UI state. Cleanup fences keep their distinct error and diagnostics; authentication and cleanup failures remain authoritative. Existing mailbox records tolerate the additive internal enable-time field without a migration.

Local gates: Linux full Go test/build/vet; WebChat 418; Desktop 499; App-CLI 371 Node cases including separately enabled Python recovery cases and Python 86. Fresh paired installation, tamper/mixed-release rejection, live-service activation rejection and state-preserving whole-set rollback pass. Work2 activation and long-running observation are recorded separately after deployment; these source checks alone do not claim live acceptance.
