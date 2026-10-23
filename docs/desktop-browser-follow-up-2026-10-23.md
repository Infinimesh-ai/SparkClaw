# Desktop browser findings and retained follow-up — 2026-10-23

> Language: English | [简体中文](../zh-cn/docs/desktop-browser-follow-up-2026-10-23.md)

## Implementation and live acceptance update

The owner subsequently authorized a combined implementation, preserving the original
UI, followed by real-machine acceptance. Product revision `cf1b8100` is now installed
on the Mac and deployed to work2. Test-only capacity correction `36240150` does not
change the deployed binary. The original investigation below remains a historical
checkpoint; its “all open” statements are not the current status.

| ID | Current result | Evidence and remaining boundary |
| --- | --- | --- |
| BR-01 | Adaptive display implemented; native layout acceptance passed. | Both active and released task pages fill the panel. Local revisions reject stale actions before dispatch. Actual original renderer/native-view qualification covers 48 existing layout cases plus 12 leased/released cases, narrow panels, 1×/1.5× zoom, occlusion and page identity. Installed main UI shows the real Apple page at 240 px, normal split width and full browser width. |
| BR-02 | Requested title/body reading succeeds on the deployed version. | The former workflow navigated twice. Same-target reads now reuse the live binding and do not navigate again or silently rebind. Real work2 comparison changes `acquire → navigate → navigate → read → release` into `acquire → navigate → read → release`; title/body and final business completion pass. The exact older request `5748f181…` is not fully reproducible, so duplicate navigation is not claimed as its sole proven cause. |
| BR-03 | Explicit verified revocation reason and existing login UI passed. | A bounded issuer authorization check preserves the typed signed revocation signal across reconnect/restart. Ordinary disconnection and generic 401/403 do not manufacture revocation. Existing credential form/history remain; Reconnect is disabled for revoked authorization. |
| BR-04 | Real in-flight revocation passed using a disposable identity. | Installed helper/UI hashes and product source match. One live read had executed but its result remained in flight. Revocation clears the active lease/page, fences the old result, rejects the old binding and returns business 401. Restart remains revoked. Already-read content cannot be undone. |
| BR-05 | Native navigation diagnosis implemented and verified. | The original example.com log contains `ERR_NAME_NOT_RESOLVED`; it is an environmental DNS failure whose reason was lost. Bounded native error codes and command deadlines now remain distinct from true fencing. Installed UI reports `browser dns resolution failed` for a controlled `.invalid` domain; Apple success is independently verified. |
| BR-06 | Optional qualification passed; real send/receive/attachment acceptance remains open. | Linux full Go/build/vet, 526 desktop tests, 504 WebChat tests, all 131 optional browser/recovery cases and file/PostgreSQL 10,000-mail capacity pass. The initial three attempts and the three `.37` continuations all have proven zero dispatches. The live blockers are QQ editor validation, Outlook strict body verification and Gmail attachment confirmation. Isolated fixture success does not qualify actual delivery. |

The optional capacity fixture previously put 10,000 mails and related records in
one transaction despite production admission allowing only 100 per command.
Bounded 100-mail fixture transactions retain all 10,000 mails, 9,000 captured mails,
900 conversations, 1,000 queued jobs, original assertions and the 60-second limit.
Mac and Linux PostgreSQL list/detail P95 were 34.85 ms and 13.38 ms. Original failed
runs remain recorded rather than being replaced with successful logs.

The installed application matches 296 package entries; the stopped cutover preserved
6 existing profiles and 97 tables exactly. Work2 preserves 41 business tables,
77 original draft/send snapshots and 106 historical unknown records. The first
strict cutover comparison failed on three periodic discovery jobs advancing through
normal restart states; field-level classification and the original failed receipt
are retained. No historical send was retried. Revocation acceptance leaves all six
QA identities revoked and all four original invitation records exactly unchanged.

The approved mail matrix used normal progress-report bodies with unchanged tiny
synthetic text attachments: QQ → Outlook, Outlook → Gmail, Gmail → QQ, once each.
Formal HTTPS object upload and saved draft attachment metadata passed, but native
attachment upload, sender Sent proof, receiver arrival, Inbox/Spam classification,
automatic intake and received-file SHA checks were not reached. This is not Mac
ISCP local-file upload acceptance. The owner already approved these three actual deliveries. Because all original
attempts are confirmed not dispatched, that authorization remains valid for the
same exact approved messages after repair, with at most one actual dispatch each.
Use fresh internal execution IDs while preserving the original failed records;
never replay an uncertain send or broaden the recipient/content scope.

App-CLI `7371628` fixes Chromium plaintext editors whose empty-line `<div><br></div>`
structure was double-counted by `innerText`. The reader recognizes only the exact
native plaintext shape, preserves empty/trailing lines and all text bytes, and
keeps the original strict body digest comparison. Other rich markup retains the
existing fallback. It does not modify the editor or trim mismatches away. The
old live logs lack per-field hashes, so the original three failures cannot be
attributed solely to this cause; the isolated reproduction and fix are verified.
Frozen release `.37` is App-CLI `91c3a160`, consumed by SparkClaw `29097af7`.
The minimal work2 cutover completed healthy at 10:43:47 UTC. Gateway binary
`cf1b8100`, Gateway/WebChat containers, browser PID, managed components and the
installed Mac application stay unchanged. All 41 tables, 83 draft/send snapshots
and 108 existing unknown records pass preservation checks.
Runtime 581 tests pass (2 pre-existing skips), Python 90 pass, and Controller
469/469 cases run with no skips across the initial 356 passes and a 113-case
rerun after missing Chromium OS dependencies were installed. That environmental
failure remains retained. All three frozen subject/body/attachment combinations
reach the actual send adapter's pre-dispatch gate in an isolated Chromium fixture;
no dispatch is released. This is preparation, not provider delivery acceptance.

The first `.37` continuation stopped at QQ `EMAIL_REPLY_EDITOR_UNVERIFIED`,
before field filling or attachment upload. Its receipt has `effect=0`,
`attempted=false` and `confirmed=false`; two observed clicks do not establish
which editor guard failed. The failed runner was closed and its QA identity
revoked (old token 401), preserving all 83 old draft/send records and adding two.
The independently continued Outlook case also has proven zero dispatches and
fails `EMAIL_DRAFT_FIELDS_UNVERIFIED` on the body alone: expected length 106,
actual 109. Its actual hash equals the earlier synthetic `innerText` failure,
which proves the same wrong readback text, not the live DOM shape or fallback
branch. All 85 preceding draft/send payload hashes and 108 unknown records remain exact;
two Outlook records bring the observed draft/send total to 87. The original
message content and attachment bytes stay unchanged. Gmail was continued only
under its original authorization; neither failed case was silently replayed.

The Gmail continuation passes body preparation but fails
`EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED`, again with `effect=0`, `attempted=false`
and `confirmed=false`. The existing logs do not identify the exact attachment
readiness guard, so this does not establish complete provider attachment upload.
All preceding 87 draft/send payload hashes and 108 unknown records remain exact. Both
continuation entry points are closed; the QA identity was revoked at 11:15:08 UTC,
the old token returned 401, and local credentials were deleted. None of the three
approved original messages has yet been dispatched. The final full inventory proves
all original 83 draft/send rows unchanged and provides full-row fingerprints for
the current 89 rows; later per-case 85/87 checks used payload hashes, not all columns.

Privacy-bounded editor, body and attachment diagnostics are integrated locally in
App-CLI `dad885dd` (including `902e3cb` and `6a9b546`). Fixed guard/error enums,
bounded structure and counts identify future failures; strict account, ownership,
body and attachment checks remain intact. The last attachment readiness result
survives the unchanged 20-second wait. Optional structure logs retain a bounded
first context or a minimal record when the 16 KB limit is exceeded. That source
risk does not establish why the prior live Gmail diagnostic was absent.
Runtime 602 tests pass (2 existing skips), Python 90 and 24 focused checks pass.
Final isolated QQ/editor Chromium qualification passes 12 cases; the preceding
`6a9b546` passes 9 cases through the actual pinned MCP wrapper, including full
synthetic file transfer and a real 20-second rejection. No live page or email is
used by these fixtures. Reader assets remain exact. The paired `.38` release is
frozen at App-CLI `aa93070b` and integrated in SparkClaw `58c347b5`. Two independent
builds produce four byte-identical artifacts; 236 affected Controller cases,
526 desktop tests, 3 projection tests and 11 paired-release checks pass without
skips. Reader/preload, native provisioning, desktop, WebChat and production Go
remain unchanged. The third cutover is healthy at 12:10:35 UTC, preserving
41 tables, 89 draft/send rows and all 108 historical unknown rows. The Gateway
binary remains `cf1b8100`; Mac, WebChat and browser components remain exact.
None of these diagnostic checks proves a live root cause or closes the three
mail blockers.

The first `.38` cutover stopped before activation when Gateway shutdown cancelled
an in-flight Gmail `collect_page` task. Its local capture effect became transiently
uncertain, raising the observed count to 109. After restoring `.37`, the existing
mail client reconciled the same local task to completed; no manual replay or
resolution occurred. The original 108 unknown rows remain exact. This is not a
mail dispatch and is not added to the permanent unknown baseline. The failed
cutover is retained; subsequent attempts first wait for a short stable idle window.
The second attempt passes the frozen database/ledger checks and starts `.38`, but
rolls back because one operation-receipt file changed. Authenticated decryption
then proves that exact file is the mutable `events.pull` / `event-subscription`
state, including a matching cursor scope and derived filename. All other 85,454
original receipts are exact and none are missing. Only this proved subscription
file is allowed to advance in the recovery attempt; the initial strict failure
and successful `.37` rollback remain recorded. The third attempt passes with
this exact exception. The live-mail preflight then identifies a harness-only
JSON hashing difference (`ensure_ascii`): recomputing all 108 current full rows
with the original algorithm matches every protected hash. It stops before QA
creation or draft/send; the original baseline is retained.

A read-only preflight before the `.37` deployment found one additional Outlook
`collect_page` / `PROVIDER_SCRIPT_FAILED` uncertain record at 10:21:52 UTC.
The original 106 unknown records remain exact; the complete protection baseline
was 107. A second pre-stage Outlook collection failure at 10:28:17 UTC raised
it to 108 and correctly blocked the first staging attempt. The original failure
and the independently reviewed protection appendix remain retained; the resumed
stage and final cutover preserve all 108 records. Each new record is a local collection operation, not `send` or
`prepare_draft`, and predates any `.37` deployment. It is retained, not resolved
or replayed, and must not be described as a new send-unknown outcome.

The two Outlook collection failures remain a separate retained diagnostic gap.
The available ledger only preserves `PROVIDER_SCRIPT_FAILED`; the mail handler
replaces uncoded errors and loses their original class and phase. Nine nearby
successful discovery rounds were empty and captured no originals, so they prove
recovery of discovery only. The receive modules are unchanged in `.37`; do not
claim the send-body repair fixes collection or that empty rounds qualify delivery.
The `.38` runtime adds bounded phase/exception diagnostics for future failures
without exposing message content or changing unknown/retry semantics; it does not
retroactively identify those two earlier causes.

The [redacted combined receipt](evidence/desktop-browser-six-fixes-2026-10-23.json) indexes the immutable local evidence. During the installed conversation-switch check, the helper was replaced and released task pages disappeared. Existing session-fence code clears those pages; Grant timing supports renewal as the trigger, but no continuous transport log establishes the initiating event. Chat history remained. Do not count this cross-reconnect check as retained-page acceptance; the separate same-connection native fixture passes 20 switches. This retention boundary remains recorded for follow-up.

Remote CI run `39937253127` passes Docs, Gateway, Gateway race, WebChat and ASR. Desktop unit tests, native LAN host and paired-release steps also pass. Overall CI remains red: Compose error-precedence and Electron anchor sandbox setup match the prior baseline; the separate phase-one Electron exits before readiness with discarded stderr, so its exact cause is unproved. These are retained limits, not a full-green CI claim.

The later `.37` run `39939779045` retains those three failures and adds one
Gateway test cleanup failure: `TestSessionMessageAdmissionRejectsConcurrentSubmission`
cannot remove a nonempty temporary execution directory. That test and production
Go source are unchanged from the prior passing run; this log alone does not prove
the cause. Docs, Gateway race, WebChat and ASR pass. Retain the new failure as well;
the earlier Linux 75-package pass does not make this later CI run green.

## Historical investigation: scope and acceptance correction

The owner requires an automated page to display like a normal browser page:
fill the available browser area and adapt when the window/panel changes size.
The owner asked for diagnosis, Codex open-source research and preservation of
the outstanding work for a later combined fix. This checkpoint changes records
only; it does not change, install or deploy product code.

The earlier [acceptance continuation](desktop-iscp-capability-expansion-design.md#920-reverse-browser-command-acceptance-continuation-2026-10-23)
proved retained controls and successful native page opening. Fullscreen/widening
was a workaround, not acceptance of the viewport experience. Reclassify the
fixed-size, centered, disappearing task view as **open product defect BR-01**.
The earlier command, restart and data-preservation observations remain valid.

## Confirmed local cause

SparkX creates actual local Electron `WebContentsView` pages in
[main.mjs](../apps/desktop/src/main/main.mjs), for both personal and task roles.
ISCP transports commands/results; the visible page is a local browser surface.
The disappearance is explained by local presentation code, without assuming a
transport failure or any implementation detail of Codex's desktop browser.

- [presentation.mjs](../apps/desktop/src/main/presentation.mjs), lines 1–2,
  hardcodes a 640 × 720 task view. Lines 145–150 center that rectangle and return
  no rectangle when either panel dimension is smaller.
- `showTask` and `setPanelBounds` leave/hide the native page when that check fails.
  Personal pages instead receive the complete panel bounds, including small sizes.
- [BrowserPanel.tsx](../apps/webchat/src/desktop/BrowserPanel.tsx) already measures
  the browser host with `ResizeObserver` and sends bounds to the main process.
  The task-specific branch discards that adaptive sizing; adding another observer
  or changing a CSS alignment alone will not remove the restriction.
- [page-registry.mjs](../apps/desktop/src/browser/page-registry.mjs), lines 70–78,
  releases controller ownership without changing the retained page's task role.
  Thus the same restriction also applies after a task has finished.

The [earlier embedded-browser design](desktop-client-embedded-browser-design.md#observation-and-input)
deliberately fixed an active task's viewport and protected it from user input.
Its engineering rationale was stable automation layout and fresh evidence at
viewport changes. The implementation carried that choice into the owner-facing
view, including completed pages. Automation does not inherently require a fixed
visible rectangle. The owner's new requirement supersedes that display target;
ownership, authorization and uncertain-write protection remain separate concerns.

A diagnostic invoking the production presentation class with fake native views
reproduced these decisions. It is a logic check, not a new native rendering test:

| Role | Available panel | Result |
| --- | --- | --- |
| Task | 800 × 900 | 640 × 720 at offset (80, 90), centered |
| Task | 640 × 720 | Visible, exact fit |
| Task | 639 × 720 | Hidden, insufficient space |
| Task | 640 × 719 | Hidden, insufficient space |
| Personal | 480 × 600 | Visible, fills 480 × 600 |

These are native view layout units (Electron DIPs); they are not physical Retina
pixels. The minimum 1180 × 720 application window cannot reserve a 720-high page
plus its browser/workbench controls, explaining why widening alone can fail.
The earlier real Mac observation is recorded separately in section 9.20.

## Codex open-source research

Checked on 2026-10-23 using official documentation and the official GitHub
repositories. `openai/codex` main was `d6817454d76885e9d0e118bbf82e507aed6f715a`;
the inspected `openai/plugins` tree was `0722921d5542fc593105c27bd52630babd8b8c2a`.

| Component | Verified scope |
| --- | --- |
| CLI, SDK and App Server | Listed as open source in the [official inventory](https://learn.chatgpt.com/docs/open-source). The [Codex repository](https://github.com/openai/codex) is Apache-2.0 licensed. |
| Agent integration | The [App Server documentation](https://learn.chatgpt.com/docs/app-server) describes authentication, history, approvals and streamed agent events. This is useful backend integration code, not a complete embedded-browser UI. |
| Browser policy/configuration | Public source includes [in-app browser requirements](https://github.com/openai/codex/blob/d6817454d76885e9d0e118bbf82e507aed6f715a/codex-rs/config/src/in_app_browser_requirements.rs) and browser-use policy types. Their presence does not establish public viewport/control implementation. |
| Desktop browser surface and control implementation | No complete reusable implementation was found in the inspected public repositories. The [browser documentation](https://learn.chatgpt.com/docs/browser?surface=app) describes a shared desktop page and says this browser is unavailable in CLI/IDE. It does not publish the internal layout algorithm. |

This is a bounded finding, not proof that no browser-related OpenAI code exists.
Do not describe the whole Codex product as open source, assume its internal view
class, or copy installed application assets as if the CLI license covered them.
Its documented user experience is a reference; SparkX already has a native
browser engine and does not need Codex's desktop source to resize it.

## Outstanding work retained for a combined fix

Every row remains open. Passing unit tests, delivered/ACK messages, or a successful
navigation cannot close a missing end-to-end business result.

| ID | Status and impact | Next work and closure evidence |
| --- | --- | --- |
| BR-01 | **Confirmed display defect; first priority.** Task pages are centered at 640 × 720 and disappear in smaller panels, including retained completed pages. | Keep the existing browser panel/tabs and use its available area. Qualify native rendering at minimum/default/large windows, panel dragging, zoom/DPI, hide/show, task completion and conversation switching. A smaller panel may cause normal page reflow/scrolling, but must not hide the whole page solely because it is below 640 × 720. |
| BR-02 | **Observed business failure; cause unresolved.** Extra Apple read task `5748f181-74af-476f-b00c-abbd6ac6cdee` completed navigation but returned `browser_open_failed`; no read/snapshot ran. | Reproduce through the normal desktop path and retain the tool outcome plus stage assessment. Inspect completion signal, page reference and target matching. Close only when requested heading/body content is actually returned. Do not equate this with the already-fixed browser-open summary defect. |
| BR-03 | **Observed reason/presentation gap.** Revoked test device is denied access, but shows generic disconnected/reconnecting and “Reconnect to continue.” | Locate where revocation cause is lost; distinguish temporary connection loss from deleted authorization in the existing UI. Confirm restart cannot restore revoked access and recovery guidance is accurate. |
| BR-04 | **Live acceptance gap.** Revocation happened after the browser lease was released. | After BR-02 is usable, revoke during a deliberately observable active operation on a disposable device. Prove cancellation/fencing, cleanup, and rejection of delayed old bindings; report any already-completed effects accurately. Local fixtures do not replace this live check. |
| BR-05 | **Navigation failure awaiting attribution.** Separate example.com attempt reached about 25 seconds and returned `command_fenced`. | Correlate native load stage, deadline, network and result handling. Determine whether the failure is environmental or product logic; test an appropriate success/failure path. Preserve the original fenced request without replay. |
| BR-06 | **Business acceptance outstanding.** Real mail send/receive/attachment delivery and optional provider/PostgreSQL cases were not established by this browser run. | Track each capability separately with real business evidence. Existing instruction to confirm before a real email send still applies; no email is sent by this investigation. |

For BR-01, an active resize can invalidate prior screenshots, element positions
or layout-dependent evidence. The later implementation should coordinate layout
changes with commands and obtain fresh observations before further effects.
Do not solve the display defect by disabling authorization/ownership checks,
replaying unknown writes, replacing the original UI, or presenting a new login
session at a copied URL as if it were the same task page. The exact coordination
design remains to be implemented and verified.

Existing [acceptance evidence](evidence/desktop-browser-reverse-2026-10-23.json)
remains unchanged. Protected data, historical unknown receipts and the installed
Mac/work2 versions are not modified. Shared protocol changes, if later needed,
must go through InfiniCenter; this investigation establishes none.
