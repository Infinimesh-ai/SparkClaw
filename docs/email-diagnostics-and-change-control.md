# Email diagnostics and change control

> Language: English | [简体中文](../zh-cn/docs/email-diagnostics-and-change-control.md)

## Baseline and purpose

This is the entry point for mail diagnosis: current limitations, evidence needed,
and the exact gap each change closes. Detailed evidence stays in its original
incident record. Update the limitation and change tables here instead of adding
another chronological release log.

The [current-stage stabilization plan](application-runtime-stabilization-plan.md)
defines this stage's goals, order and what is deferred until requirements rise.
This document retains the exact mail findings, repair boundaries and change card
that the plan's steps reference; it is not a separate roadmap.

Audit: **2026-10-26, Asia/Shanghai**, source `c2b85311`. The checked-in
[release manifest](../vendor/app-cli/release.json) pins App-CLI `.41` / `bcc55506`.
The original audit referenced the historical `.39` [acceptance record](desktop-browser-follow-up-2026-10-23.md) and did not inspect a live host. Current `.41` loaded-version and installation evidence is recorded in sections 12–17; it does not retroactively change the audit.

**Implementation status:** S1–S4 are source_verified and deployed. Cards 14–16 record native Linux read-only qualification, the deployed generic manual-call FK repair and exact-artifact fault acceptance. Authorized unknown cleanup is complete; final observed unknown count is zero. Full S5 mail acceptance remains incomplete: QQ body validation, Outlook extra LF, and QQ/Outlook independent Sent originals remain open. The unlocked original desktop connection recheck passed on 2026-10-27 (card 17). D05/D09 are deferred. No App-CLI `.41`, UI, public-code, scheduling or automatic-expiry change; no push.

## 1. Follow the whole chain

| Boundary | Existing evidence | Required distinction |
|---|---|---|
| Compose UI / agent → admission | Draft, frozen snapshot, send key, invocation; agent run where applicable | Intent/admission versus provider dispatch |
| Desktop → ISCP → Gateway | RPC/request IDs, execution receipt, optional timings | Transport success versus mail business success |
| Gateway → Controller → App-CLI executor | Invocation/task, lifecycle, task events | Local collection versus send; execution versus effect certainty |
| Host → logged-in page → native request | Host phase/code/reason, provider result/proof | Click/process exit versus validated provider effect |
| Send result → Store finish → ACK/cleanup | Receipt, draft state, lifecycle ACK, cleanup diagnostics | Business completion, persistence, ACK and resource cleanup |
| Watch/poll → discover → collect | Mailbox/checkpoint, job, sync failures/history, capture manifest | Notification versus complete qualified collection |
| Parse/store → analysis → publication | Source/capture/job/revision, model/analysis evidence | Original bytes, parsed body and derived result |
| API/object transfer → original UI | Message/attachment references and presentation state | Stored result versus displayed content/downloaded bytes |

Report **execution outcome, business outcome, evidence completeness, cleanup
outcome and ACK outcome** separately. These are diagnostic dimensions, not a
replacement state machine. `unknown` must retain its operation kind and original
identity; a collection unknown is not an unknown send. Never resend an uncertain
send to obtain evidence. `not_sent` still needs the existing validated negative
proof. A receipt, independent Sent original, recipient arrival, automatic intake,
exact body/attachment bytes and original UI behavior are separate checks.

## 2. Confirmed source limitations

These are original audit findings, not proven causes of every historical incident. Completed rows retain the original failing behavior for context; the status and change cards below record their repairs.
Locate by file/function if line numbers move.

| ID | Source finding | Exact repair boundary |
|---|---|---|
| D01 (S2 source_verified) | [`page_intake.go`](../services/gateway/internal/emailmanagement/page_intake.go), `commitListFailure` (523–539), omits `InvocationID`; successful commit (426) includes it. | Carry the actual failed invocation into history. Distinguish failure before invocation, collection failure and split-query failure; never invent a call that did not happen. Preserve checkpoint/retry semantics. |
| D02 (S3 source_verified) | [`compose.go`](../services/gateway/internal/emailmanagement/compose.go) (275–282) reduces most send errors to `email_send_outcome_unknown`; [`playwright_runner.go`](../services/gateway/internal/emailautomation/playwright_runner.go), `mapPlaywrightError`, also merges lower-level codes. | Retain a separate safe cause/stage/source layer. Preserve public error behavior and unknown-send fencing. |
| D03 (S2 source_verified) | [`host-driver.mjs`](../tools/browser-controller/src/host-driver.mjs) (212–213, 270–271) logs phase/code/reason/command without task identity. | Add validated task/operation correlation. Timestamp proximity cannot reliably join concurrent operations. |
| D04 (S2 source_verified) | [`service.go`](../services/gateway/internal/emailmanagement/service.go), `execute` (380–382), cancels on renewal failure without its cause; `safeCode` (434–462) collapses unlisted parse/source/model errors. | Preserve typed internal cause and stage, independently of public error text. Distinguish lease/storage failure, cancellation and deadline. |
| D05 | [`endpoint_rpc.go`](../services/gateway/internal/iscpworkbench/endpoint_rpc.go) (174–176) records handler status from `callCtx.Err()` only. `commitListFailure` returns nil after storing a business failure, so the job can succeed. | Separate transport/worker completion from business outcome. Do not change scheduling semantics just to change a log status. |
| D06 (S3 source_verified) | [`latencytrace/trace.go`](../services/gateway/internal/latencytrace/trace.go) is opt-in and bounded (1,024 queue / 64 MiB), with dropped/limit markers but silent initialization/sink failures. [`latency-trace.mjs`](../apps/desktop/src/main/latency-trace.mjs) silently stops at 100,000 records or write failure. | Report enabled/disabled/degraded state, coverage and loss. Missing events do not prove no execution. |
| D07 (S3 source_verified) | [`agent.go`](../services/gateway/internal/agent/agent.go) (1007) ignores trace-write error; [`trace/writer.go`](../services/gateway/internal/trace/writer.go) (76–78) suppresses artifact-store error; [`app-cli-client.mjs`](../tools/browser-controller/src/app-cli-client.mjs) (56) suppresses maintenance failure. | Make evidence/maintenance failure observable without rewriting mail results. Agent traces do not cover every direct compose or background sync. |
| D08 (S1 source_verified) | Validated provider rejection becomes `failed` in [`compose.go`](../services/gateway/internal/emailmanagement/compose.go) (293); first ACK failure is ignored (306). [`lifecycle_maintenance.go`](../services/gateway/internal/emailmanagement/lifecycle_maintenance.go) (86) retries only `sent`. | Source-confirmed ACK recovery coverage gap. First reproduce in isolation, then include eligible rejected terminal results; zero redispatch. Not a proven live incident cause. |
| D09 | [`analysis.go`](../services/gateway/internal/emailmanagement/analysis.go) retains failed analysis files, but failed [`EmailJob`](../services/gateway/internal/app/email_management.go) has no direct execution/artifact/ModelCall index; `SaveModelCall` failure can still return the generated ID. | Index analysis attempts and distinguish artifact/model-call persistence success from an allocated reference. Never present a nonexistent record as evidence. |
| D10 (S3 source_verified) | [`compose.go`](../services/gateway/internal/emailmanagement/compose.go) (258–264) ignores epoch lookup error and can fall back to a legacy invocation. | Record identity mode and safe epoch failure. Removing fallback would be a separate behavior decision, not a logging fix. |

Existing safeguards include immutable send snapshots, durable receipts, typed
public errors, negative-send proof, sync failure scope/history, revision/lease
fencing, original manifests, cleanup fencing and private timings. Preserve and
connect them. A generic logger alone cannot repair these gaps.

## 3. What the retained live evidence cannot establish

The [acceptance record](desktop-browser-follow-up-2026-10-23.md) preserves:

| Historical case | Proven | Missing evidence / limitation |
|---|---|---|
| `.39` QQ → Outlook | Sent-baseline click target missing; zero dispatch | Hydration versus selector cause; need task-bound stage evidence, not a blind timeout increase. |
| `.39` Outlook → Gmail, Gmail → QQ | One send each; recipient Inbox originals and attachment bytes checked | Strict whole-route acceptance incomplete. Do not resend successful cases to fill diagnostic gaps. |
| Outlook → Gmail final LF | Extra LF exists in native original; separate MessageView trimming observed | Stage introducing LF. Compare frozen input, original, parsed and displayed output; local `.41` fixes are not deployed acceptance. |
| Background collection unknowns | Protected task rows and typed network/extension failures | Exact native request/stage; nearby logs cannot prove causality. |
| Historical terminal task rows missing | Missing sets retained, with no protected send/unknown overlap in cited comparisons | Per-row deletion cause; do not call it GC without evidence. |
| Upgrade/export/replay | Resource/codec failures and later fixed-file replay success retained separately | Live `.41` activation and remaining local acceptance. Harness success is not mail success. |

Browser-native behavior depends on provider sessions and page/request shape.
Qualification must name provider, account class, folder, operation, attachment
shape and component versions. Inbox success does not qualify Sent/Spam/reply or
every attachment path; a QQ pass does not qualify Gmail or Outlook.

## 4. Proposed implementation boundary

- Join **existing identities**, including request/RPC, draft/snapshot,
  invocation/task, job/attempt, mailbox/checkpoint and capture/source/analysis.
  Preserve original business identity across reconnect/reconcile; attempt IDs
  supplement it. Background work need not invent a user request.
- Add versioned boundary metadata: component revision, stage/parent, operation
  and effect class, safe cause, the separate outcomes, local monotonic duration
  and timestamps. Cross-host clock subtraction is not a reliable duration.
  Use a shared internal vocabulary with privacy tests rather than free text.
- Propose bounded default boundary/failure metadata and optional detailed timings.
  Explicitly report disabled, dropped, expired and unavailable evidence. Do not
  promise permanent full history or make logging failure control send semantics.
- Build a **read-only local operator report** first: given an authorized existing
  operation/job, show last proved boundary, first failure, effect certainty,
  missing evidence and allowed recovery. Missing evidence means `incomplete`,
  not an inferred root cause. No ACK, resolve, retry or task mutation on reads.
  A later API needs authorization/owner-isolation tests and contract review.
- Exclude addresses, subjects, bodies, filenames, raw URLs/session values,
  headers, credentials and raw provider IDs from general diagnostics. Private
  evidence references/opaque IDs remain owner-scoped; exports remap when needed.
  Keep existing raw-artifact access controls. Routine diagnosis uses bounded
  indexed reads, not a new full-database freeze/export for every failure.
- Keep the original UI. No permanent technical status area is proposed. This
  work is within SparkClaw and its owned App-CLI; no shared ISCP wire,
  authorization or receipt contract change is proposed.

## 5. Ordered changes and acceptance

S1–S4 are **source_verified and deployed**; S5 has the current acceptance and explicit live-case limitations recorded in section 13; section 12 is historical. D05/D09 remain deferred. Independent behavior repairs and diagnostic/schema
work are separate changes. An unrelated failure gets a separate item.

| Batch | Exact change | Acceptance |
|---|---|---|
| M1 — Preserve attribution | D01/D03/D04: failed invocation, safe Host task relation, renewal first cause | Concurrent failures join correctly; before-call and split failures distinguished; memory/file/Postgres preserve failure identity; cancellation retains cause; no payload leak or scheduling change. |
| M2 — Make evidence honest | D02/D05–D07/D10: safe internal cause, separate outcomes, sink health, trace/maintenance failure, identity mode | HTTP success + business rejection distinguishable; unknown stays unknown; disabled/full/broken sinks yield incomplete evidence without changing business results; epoch success/unsupported/failure distinguishable. |
| M3 — One report | D09 plus indexed read-only aggregation, versions and stage coverage | Failed analysis locatable by attempt; missing ModelCall/artifact explicit; same operation reconstructs after restart; duplicate/out-of-order events and other owners cannot mix or invent completion; zero query side effects. |
| M4 — Bounded product repairs | Reproduce/fix D08 first. Give each QQ/body/Sent issue its own current-version reproducer/card. | Rejected result + first ACK failure + restart eventually ACKs with zero redispatch; sent/unknown protection unchanged. Provider repairs verify exact failed boundary and downstream result. |
| M5 — Installed version and UI | Qualified component set, then explicitly authorized live cases | Actual installed pins and dispatch counts; separate Sent/Inbox/body/attachment/UI results; reuse successful mail read-only where possible; partial matrix is not universal success. |

**Current authorization starts with D08 (M4), then M1**: they are independent
and are committed separately. The approved S4 design and implemented reader limits are in section 7 of the
[stabilization plan](application-runtime-stabilization-plan.md). M1 makes the next failure attributable; M2/M3 make
the evidence directly usable. Do not bundle D08 into diagnosis work. Deployment and
acceptance-harness repairs have separate cards, not mail-runtime completion claims.

## 6. Required change card

Put this in the issue/PR or existing implementation record, not a new dated report
for every attempt.

```text
ID / owner / status: planned | source_verified | deployed | accepted | blocked
Observed failure: operation, versions, environment, first failed boundary
Evidence: original reference; last proved fact; missing fact; cause or hypothesis
Objective: one observable before → after statement
Scope: exact modules/stores/adapters; preserved business invariants and UI behavior
Mechanism: why this change covers every affected path/backend
Acceptance: reproducer first; success + failure + recovery; original UI if affected
Result: source revision / deployed revision / evidence revision (separate)
Remaining limitation: exact missing proof and next evidence/action
Rollback: source boundary and persistent-data compatibility
```

Close only after scoped acceptance, preservation of prior failures, updated
limitation and truthful status. Unit tests can establish `source_verified`, not
`deployed` or `accepted`. Broaden/repeat tests only for a new change, failure or
unresolved concern. Apply the [engineering baseline](engineering-baseline.md);
cross-project changes require an accepted InfiniCenter decision.

## 7. S1 / D08 change card — source_verified

- Owner: SparkClaw email lifecycle maintenance; local source only, 2026-10-26.
- Observed failure / evidence: baseline `e580fc01`, isolated file Store and fake provider. `TestRejectedReceiptACKRecoversAfterFileStoreRestartWithoutRedispatch` failed with no ACK after restart despite a durable rejected receipt. This is not a live incident attribution.
- Objective / scope: recover pending ACK for validated rejected terminal receipts in `services/gateway/internal/emailmanagement/lifecycle_maintenance.go`; no schema, UI, public error, scheduling or App-CLI change.
- Mechanism: require failed + not_sent + matching provider/invocation/task + existing valid rejection proof; retain the sent path and reject unknown, unproven and already-ACKed receipts. Uses the existing backend-neutral ACK command.
- Acceptance / result: reproducer red then green; two rounds ACK exactly once, preserve rejection/code/identity, publish no sent mail, and leave total sends at one after same-key replay. Compose and proof-fencing regressions, isolated Linux full Go suite (75 tested packages), build/vet, and bilingual links pass. Earlier macOS path/tmpfs and incomplete-container setup failures remain in local logs; they are not counted as passes. Evidence is the committed `rejected_ack_recovery_test.go`; source revision is this card's commit, deployment/evidence from live systems: none.
- Limitation: no installed-version proof. S2/S3 and S4 remain separate.
- Rollback: revert this commit; no persistent format change, existing ACK flags remain valid.

## 8. S2 / D01, D03, D04 change card — source_verified

- Owner/status: SparkClaw intake, Host and mail worker; source only, 2026-10-26.
- Failure/evidence: at S1 source `23526369`, collection/split errors persisted empty invocation; concurrent/reused-page Host failures lacked a task relation; renewal failure vanished into cancellation. Each reproducer failed before its implementation.
- Objective/scope: `page_intake.go` carries only the invoked request into the existing sync command; `host-driver.mjs` correlates the current activity; `service.go` and `job_diagnostics.go` preserve first safe cause. Corresponding tests and `email_failure_attribution_test.go` validate these paths. No Store schema, public code, retry/backoff, original UI or App-CLI change.
- Mechanism: pre-call admission/recovery/target failures do not invent an invocation; a failed split uses its own ID. Authenticated Host request keys are validated then hashed (SHA256 of UTF-8), joined with the actual task UUID; retained pages never lend an old task. Typed renewal cancellation and a distinct cleanup sentinel preserve the first cause; original dispatch errors still determine Finish/RetryAt. Allowlisted diagnostics reject raw payloads and forged typed codes.
- Acceptance/result: collection/split red→green on memory/file; persisted failure identity and full decoded record equality after restart on memory/file/isolated PostgreSQL; concurrent Host and real pinned .41 pooled-page fixtures; renewal/cancel/deadline/source/model/privacy and unchanged scheduling tests, including race. Controller 326 pass / 148 existing optional skips; isolated Linux full Go 75 packages, build/vet. Review added red→green split admission/contract stages and known recovery/source causes; final targeted race covers these additions. Source revision is this card's commit; deployment and live evidence: none.
- Limitation: no newly durable generic diagnostic stream; first-cause/Host retention depends on existing logs. Missing logs or invalid identities remain `incomplete`. The operation join uses task ID plus `operation_key_sha256`, never time proximity. D05/D09 remain deferred.
- Rollback: revert this commit; existing optional InvocationID is backward compatible; no migration or state-machine change.

## 9. S3 / D02, D06, D07, D10 change card — source_verified

- Owner/status: SparkClaw mail adapters and existing diagnostic sinks, source only, 2026-10-26.
- Observed failure/evidence: S2 `4cde05d6` lost lower Controller/script causes and epoch fallback mode; disabled/broken/full trace sinks lacked health evidence; artifact, Agent, feedback-refresh and Controller maintenance failures were swallowed. Tests reproduced these failures before fixes, including first/maintenance ACK visibility. Additional red tests covered old-operation borrowing, epoch failure followed by failed durable begin, and truthful persistence at ACK.
- Objective: recover safe cause and missing-evidence boundaries without altering the business decision or treating missing events as no execution.
- Exact production scope: `internal/emailautomation/{errors,playwright_runner,diagnostics}.go`; `internal/emailmanagement/{compose,job_diagnostics,lifecycle_maintenance,send_diagnostics}.go`; `internal/latencytrace/trace.go`; `internal/trace/{writer,diagnostics}.go`; `internal/agent/agent.go`; `internal/gateway/traces_endpoints.go` (all under `services/gateway/`); `apps/desktop/src/main/latency-trace.mjs`; `tools/browser-controller/src/app-cli-client.mjs`. Tests live alongside these owners; no Store schema or public API change.
- Mechanism: separate allowlisted cause/stage/source from existing public error mapping; hash owner+draft references, validate operation IDs, preserve epoch fallback and record unstarted attempts without a prior snapshot. Observed send results have `business_persisted=false`; successful finish and ACK failure on a durable result remain distinguishable. ACK logs never trigger additional ACKs or sends. Trace artifact errors preserve `errors.Is/As` and fixed stages; Agent and feedback callers report incomplete evidence while keeping business/HTTP results. Maintenance callbacks cannot propagate diagnostic rejection into work.
- Sink health: `latency_trace_health` v1 reports enabled/disabled/degraded, fixed reason, `instrumented_boundaries_only`, `evidence=incomplete`, attempted/written/dropped/lost/rejected metadata and first-loss/last-write evidence. Go queue 1,024 and 64 MiB, Desktop 100,000 records remain bounded. Go producer does not await health output; Desktop stderr failure is contained. Transition counters are snapshots, not final totals or durable history.
- Acceptance/result: red→green cause/identity/unknown/ACK/persistence/privacy tests; mail, trace, Agent and feedback race tests; actual 64 MiB/100,000-record limits, blocked/broken observer and actual `WithFields` rejection; isolated Linux full Go suite (75 tested packages), build/vet; Controller 331 pass / 148 existing optional skips; WebChat 504 pass; Desktop 530 pass in full serial run. First concurrent Desktop run had one authority-change failure, then its isolated 16-test file and the complete serial run passed; that failure remains recorded, not rewritten. App-CLI projections pass; renderer/WebChat/release pins unchanged. Source revision is this card's commit; deployment/live evidence: none.
- Limitations: diagnostics are process/log-retention dependent, not a new durable audit ledger. Unknown/unlisted causes remain unclassified; absent health/Host/trace evidence remains incomplete. None of this proves installed `.41`, native provider behavior, original bytes or recipient arrival. D05/D09 are still deferred.
- Rollback: revert this commit; new data are additive diagnostic fields only, with no business schema migration. Earlier optional logs can be ignored; runtime pins and stored send protection are unchanged.

## 10. S4 / minimal M3 change card — source_verified

- Owner/status: SparkClaw mail diagnostic adapter, local source only, 2026-10-26; owner confirmed the section 7 design. Baseline `3793bfac`; source revision is this card's separate implementation commit. Deployed/installed/live evidence: none.
- Failure/objective: evidence required manual joining and constructing ordinary Store/Ledger readers could mutate it. The initial real File Store fixture reproduced rejection → failed first ACK → restart, then failed because the standalone report command did not exist. Implement one bounded scoped report that preserves the fixture's result, pending ACK and unknown fence.
- Exact scope: new `services/gateway/cmd/sparkclaw-email-diagnose/`, `services/gateway/internal/emaildiagnostics/`, `services/gateway/internal/emailmanagement/diagnostic_report_test.go`, `tools/browser-controller/scripts/email-diagnostics-ledger.mjs` and `test/email-diagnostics-ledger.test.mjs`; bilingual plan and this card. No existing business/UI/shared-core implementation, schema, public code, scheduler or App-CLI `.41` changes.
- Mechanism: explicit owner plus draft/operation/job; streaming bounded safe file projection, SELECT-only repeatable-read PostgreSQL, embedded Node ledger reader with OS write denial, retained logs with exact identity. Separate persisted/observed business, execution, effect, ACK, cleanup, expected/loaded version and missing evidence. `.41` send uses an exact persisted task/revision/digest owner binding only when request scope is absent. Conflicting receipts, unknown states, unstable sources, absent mappings and cutoffs cannot grant recovery or invent history. Recovery is descriptive; no business callbacks are linked into the command.
- Red → green evidence: command/restart/unknown fixture; owner/principal collision, legacy/op3/alias/head mapping, active WAL and required-SHM failure, malformed/changed/oversized sources, privacy, receipt revisions, unordered/conflicting boundary events, health counters and timeout/blocked child output. Additional failures exposed an executable CWD helper and Node preload side effect; embedded code plus restricted first-process environment removes both. Retained local logs are `/tmp/sparkclaw-s4-report-red.log`, `-aggregate-red.log`, `-sources-red.log`, `-ledger-edge-red.log`, `-source-consistency-red.log`, `-last-review-red.log`, `-node-env-red.log` and `-boundary-recovery-red.log` (same prefix). Committed tests are the reproducible evidence; logs are local, not bundled private artifacts.
- Acceptance/results: repeated actual command queries preserve file bytes/mode/mtime/inventory, zero ACK and zero additional dispatch; each owner's single original fixture dispatch remains 1, same-key replay remains unchanged and a new unknown key remains blocked. SELECT-only real PostgreSQL/RLS/transaction checks and affected Go race tests pass. Controller full suite: 351 pass / 150 optional or platform skips; isolated ledger suite: 21 pass / 1 platform skip, including Linux static/active-WAL read-only-mount qualification. Linux full Go build/vet/tests: 77 tested packages passed. App-CLI projections, all 134 bilingual Markdown files and diff whitespace checks pass. An early Linux source-snapshot preparation failed because the host Python lacks `tarfile.extractall(filter=...)`; it is retained separately and not counted as a product test failure or a pass. No UI files changed; prior UI test counts are not claimed as new S4 runs. External TeamSkills validation still has 37 pre-existing lesson-metadata errors, unchanged by this update; it is not counted as passed.
- Limits: the report is always incomplete for unretained history; current loaded versions, cleanup, trace/run and D09 analysis indexes remain unproved. Old job logs lack owner binding and cannot be joined. Encrypted file snapshots are unsupported. Production ledger access currently requires the qualified macOS guard; Linux reports that guard missing without opening the ledger, while its core reader was qualified only in a write-denying fixture. Diagnostic counts are logical source/projection bytes; a 10-second work deadline has bounded cleanup/output overhead. These are explicit gaps, not authorization for `.41` changes or S5.
- Rollback: remove/revert this standalone command, reader, wrapper and tests. No migrations or durable diagnostic state; existing receipts, unknown protection and S1–S3 behavior remain compatible. No deployment, real email or push occurred; S5 awaits separate authorization.

## 11. S5 deployment preflight card — not deployed

- Owner/status: SparkClaw release acceptance, 2026-10-26. The owner authorized S5 deployment and Mac installation/acceptance. Push, real email and App-CLI `.41` changes remain prohibited; unknown is never resent. S1–S4 stay `source_verified`, not `deployed` or `accepted`.
- Goal/failure: install the exact current-stage combination and verify recovery, diagnosis and original UI. Old `.41` staging contains `2396381e`, so it cannot qualify S1–S4. The complete preservation pipeline currently fails its resource precheck before decoding or stopping a service; no product failure is inferred from this environment gate.
- Exact scope: immutable SparkClaw `0f46702a18e9b8fc8bb4e4fbe43341e097fd7af3` and unchanged App-CLI `bcc55506cc933f981668e3b2d1ed1eba1bf4c6a3`. Private build/staging/backup evidence lives under `~/.cache/sparkclaw-upgrades/20261026-runtime-s5-0f46702a`; the repository change is this bilingual checkpoint, plan section 2, follow-up and the [public evidence summary](evidence/runtime-stabilization-s5-2026-10-26.json). No UI, public codes, scheduling, shared core or runtime source is changed.
- Preparation/results: Go Gateway/bridge and standalone Linux/macOS diagnostic binaries built; Node 26 Mac DMG/ZIP built with the package audit. All 2,964 tracked source blobs still match the pin. Four work2 cache artifacts passed SHA checks and bundle verification; Gateway and diagnostic `-h` succeeded without runtime bootstrap. Mac cold backup preserves all 6 profiles and 97 tables exactly; old and candidate app manifests contain 296 entries each. The old app remains installed.
- Artifact qualification: the actual Darwin diagnostic binary passed 25 synthetic invocations (24 expected exit 0, one invalid-argument exit 2), owner/principal/ledger-owner isolation, unknown `inspect_without_resend`, and exact file/static-ledger/active-WAL bytes/mode/mtime/inventory. Ten OS write-denial probes passed; no production ACK or provider operation ran. Four harness negative controls failed first. An initial inconsistent synthetic draft/snapshot produced conflicting evidence; that fixture failure is retained separately. Root rehashed 89 evidence files and independently verified before/after equality. Mac package inspection matches 82 source files and 24 built UI files; only normal package metadata omissions differ. Private admission and maintenance prototypes pass 21 and 9 tests after their retained red runs, but remain non-executable in production. All 134 bilingual docs pass; TeamSkills still reports the same 37 pre-existing metadata errors and is not counted as passed.
- Current live evidence: at 05:44:17 UTC, work2 still runs source `9eab082c` / `.39`, the same healthy Gateway container/image and three active units. This is a fresh health observation, not a full data-preservation or unknown-set check. The Mac showed a pre-existing HTTP 503 before installation; this remains separate from remote health.
- Resource boundary: local available memory was 3.76 GiB at 05:48:37 UTC; remote was 5.28 GiB at 05:44:17 UTC. Full verification still requires at least 4 / 8 GiB respectively, reserves at least half available memory and at least 2 GiB, and retains 12 GiB disk reserve. Normal Chrome/Claude/WPS exits were specifically authorized; WPS cloud helpers remain, with no forced termination. No saved-response decode, full fresh inventory, freeze, service restart, source switch or Mac installation occurred. Browser-only restart is not an accepted substitute for draining and preservation; unsaved page-state preservation remains unproved.
- Limits/next gate: exact artifact binding is not loaded-version acceptance. The private admission draft remains closed and has no deployment execution adapter. After resources qualify, complete the same saved-response receive, genuine healthy full baseline and review, then controlled freeze/activation/health and Mac UI acceptance. S1 rejected-result ACK transitions require exact task/revision/digest proof and zero additional dispatch; unknown retains full-row protection. Historical missing rows and failed runs remain recorded.
- Rollback: no active runtime or application replacement to roll back. Keep the original `.39` services, old Mac bundle, source data, backups and all evidence; never restore old database copies to hide differences. No new mail, resend or push occurred. S5 is unfinished.

## 12. S5 focused deployment card — deployed; acceptance blocked

**Historical checkpoint before issuer maintenance.** The capacity evidence and failures below remain retained. The owner subsequently directed removal of the storage limits followed by one-time expired-record cleanup, with no automatic expiry mechanism or InfiniCenter prerequisite. The completed repair and current UI results are recorded in section 13; the blocked state below is no longer current.

The owner confirmed that the browser has no unsaved content and explicitly permits
cleanup before upgrading, retaining only evidence needed for this verification.
This supersedes the full-history preservation/decode prerequisite in the earlier
S5 checkpoint; its failures remain historical, not current admission conditions.
Keep all current send/unknown task rows and their evidence, draft/send snapshots,
mailbox/identity configuration and exact rollback components. Do not delete or
resolve unknown, resend mail, modify App-CLI `.41`, push, or change product
scheduling/UI/public codes. A newly bounded focused collector must prove its own
resource limits instead of inheriting the obsolete full-export 4/8 GiB thresholds.

Eight obsolete local synthetic exports and `2396381e` DMG/ZIP/source-tar artifacts
were removed after recording exact paths, sizes and hashes: 2,191,551,482 bytes
(2.041 GiB). Small failure receipts, scripts, original core evidence, all live data
and current `0f46702a` artifacts/cold backups remain. This is cleanup of retained
test artifacts, not a claim that production business data was garbage-collected.
The stopped browser's three cache directories (98 files, 607,399,085 bytes) were
also removed; all noncache file bytes and modes matched before/after. Cookies,
IndexedDB, local storage, sessions and service-worker storage were retained.

- Scope/mechanism: deploy immutable `0f46702a` with unchanged `.41`, using private
  release operators only. The focused collector reads approximately 1.1 MB with
  about 60 MiB peak remote RSS; read-only PostgreSQL and an outer read-only mount
  protect collection. That outer qualification is not a bypass of the production
  S4 Linux ledger guard. No product UI, code, scheduling or shared contract changes.
- Preservation: all 47 send and 153 unknown tasks (one overlaps), 199 protected
  task rows, 998 events, 101 draft/send-snapshot rows, 10 devices, mailbox bindings,
  release/anchor identity and one exact expired prepared collection row/event
  survive freeze, cache cleanup, activation, diagnostic calls and the 07:01:04 UTC UI checkpoint.
  The expired row was reviewed against the unchanged constructor/recovery source;
  it was not invoked, cancelled, deleted or treated as a generic exception.
- Activation: normal drain preceded source/runtime/component selection. Native
  Reader import timed out once, then passed after the one permitted normal
  restart. `.41`, Tampermonkey `5.5.0.21`, Bridge `1.0.29`, native handshake and
  installed-release gates passed; the temporary restore-session override was
  removed. The first timeout's cause remains unproved. Selected user preferences
  survived; 36 browser metadata changes remain recorded, including a per-site
  automatic HTTPS-upgrade exception change whose cause is not established.
- Loaded-service checkpoint: the new Gateway is healthy on the exact staged
  image. The original admission verifier stopped at an incorrect assertion that
  both services carry runtime paths in `ExecStart`: Controller uses its existing
  environment variables, while Executor uses command arguments. Actual process
  evidence matches `.41`. A separate read-only continuation passed at 06:40:48 UTC,
  binding the original failure, container ID, PID/start time, actual environment,
  Python-to-Node handoff and inherited lock FD. The actual handoff first reproduced
  a failing test; all 13 continuation tests then passed independently. No admission
  rerun, service restart or fabricated original success receipt occurred.
- Actual diagnostic acceptance: 5 calls through the exact Linux binary passed
  repeated unknown reports, owner isolation (zero rows), privacy and Linux's
  explicit missing ledger guard (zero ledger rows/bytes). All 101 current
  draft/send-snapshot rows and all 9 independently captured protected sections
  stayed exact. The provider-rejection category was unavailable. Reports retain
  version/log/cleanup/trace gaps; the separate health receipt does not fill them.
- Mac installation: all 296 application entries match the candidate; 6 profiles
  and 111 existing tables stayed exact at replacement (97 originally counted,
  plus 14 preexisting Default tables). The old app remains available. Native CUA
  observations passed original layout/entries, old session history, settings/back,
  schedule navigation, sidebar shortcut, empty composer focus and normal
  quit/zero-process/relaunch. These checks did not type/send a message or save a draft.
- Remaining acceptance: the preexisting Mac connection still blocks mail/browser
  interaction checks. The actual installed helper's read-only `-check` returns
  `enrollment Relay refresh credential expired`; two ordinary startups with empty
  stdin return `control: authorization_unavailable`, with no business frames or
  profile-file changes. The selected profile remains unchanged. Original-entry
  relaunch still shows the preserved disabled mail/browser entries. Source tests and
  synthetic qualification do not replace actual provider rejection, failed-ACK
  recovery, original-browser interaction or delivery. No real email is authorized.
- Connection capacity blocker: management identity/client/device reads, the original
  template TCP/TLS handshake and one signed Authority query (HTTP 200, verified
  response) passed. A read-only issuer transaction found 547 successful control
  records/parents, complete open history, no running record and matching writer
  settings. The same encrypted state remained unchanged. Its serialized plaintext
  is 4,193,235 bytes; adding only the required next recovery record in memory,
  omitting its parent/grant/predecessor, reaches 4,195,320 bytes and exceeds the
  4,194,304-byte seal limit. No credentials were signed, requests sent or state
  persisted by this size calculation. This proves a capacity obstruction; the
  original generic 503 lacks a detailed trace, so not every historical failure
  is assigned this cause. Existing identity and authorization history are retained.
  InfiniCenter proposal 0050 and ISCP inbox 015 request a separate bounded storage
  design/review. Open-namespace deduplication and the seven-day signed-result
  retention contract prohibit clearing these records as ordinary upgrade cache.
  No issuer repair or contract change was implemented; full S5 acceptance waits
  for original-identity recovery and the remaining UI checks.
- Rollback boundary: retain old source/image/container, runtime/components, unit
  files and Mac bundle. Restore only the matched release if needed; never restore
  business databases/profile backups to conceal differences. No push, real send,
  unknown retry or App-CLI source change occurred. See the
  [focused evidence summary](evidence/runtime-stabilization-s5-scoped-2026-10-26.json).

## 13. S5 issuer capacity repair and one-time cleanup — deployed; UI checks passed

- Owner/status: SparkClaw, 2026-10-26. The owner's final instruction is to remove the
  issuer's storage capacity limits, then perform one explicit cleanup of unused expired
  credential records. **No automatic expiry/GC mechanism or pruned-generation metadata
  was added.** S5 deployment and authorized installed-version/UI acceptance passed, including normal
  quit/relaunch and the final post-mail preservation comparison.
- Failure/goal: the retained size reproduction in section 12 proved that accumulated
  signed history could exceed the existing aggregate ceiling and obstruct the next
  credential recovery. Remove the persistence/count obstruction while keeping wire and
  request validation, writer fencing, device identity and existing recovery semantics.
  The original generic authorization error is not sufficient to attribute every
  historical failure to this capacity limit.
- Exact source boundary: `services/gateway/internal/iscpnodeissuer/store.go` removes
  the 4 MiB seal/open checks and the SQL `bounded_ciphertext` CHECK, including migration
  of an existing table; `control_v2.go` removes the 1,024-record admission limit.
  `store_capacity_test.go` covers both changes. Frame/request/private-file limits remain
  unchanged. The cleanup is a separate one-time private operator, not a service loop.
  App-CLI `.41`, Gateway/Controller/Executor scheduling, original UI and public error
  codes remain unchanged; no mail branch was added to shared core code.
- Red → green/source verification: four new capacity tests cover large encrypted
  history, readback, admission past 1,024 records with stable replay, and existing-table
  migration/restart/rollback/writer fencing. Independent validation exposed a test-DSN
  isolation error; it was fixed with an exact `current_database()` assertion. The
  actual old `22da89f` baseline then failed with `legacy constraint remains`; the fixed
  four tests passed twice, eight test executions, including two real isolated PostgreSQL
  runs. The one-time selector/operator passed 27 top-level tests and 45 subtests plus
  race/vet. Full Linux gateway tests passed: 5,606 test events, 197 conditional skips,
  77 tested packages, zero failures; build/vet passed. The offline Linux arm64 run used
  Go 1.25.5, no network, 3 GiB/3 CPUs, verified existing module-cache copies and unchanged
  source hashes. Initial harness/cache/fixture failures remain recorded separately.
- Artifact/provenance: the actual old native issuer is ISCP
  `22da89f329a88bde8e12219c359e82063064798b`. Its production issuer/control sources match
  the SparkClaw copies after import-path normalization. A private export of that exact
  ISCP tree receives only the two production files and capacity test; the ISCP working
  tree is untouched. Patch SHA-256 is
  `eb1b94f559407d97c43d445eb9d74c41b7353d3e78505e96f89f312ab78b384d`.
  Go 1.25.12 / Linux amd64 / CGO=0 produced and deployed native binary SHA-256
  `a866beae2809fe30511b9e0ab010ef333c2ae2ceb7fa80a98b1cd6f36b2b383f`.
  Source-file hashes, buildinfo and logs are retained in the private `build-issuer-02`
  manifest; the exported tree's buildinfo is `(devel)`, with provenance bound by the
  manifest rather than an invented embedded VCS revision.
- Maintenance and preservation (UTC): at 08:29:31, only issuer stopped normally.
  Private cold backup verified all 100 node files, 10 encrypted rows, writer/schema,
  storage key and old binary. At 08:30:19 only `bounded_ciphertext` was dropped; at
  08:31:07 the new native issuer started. At 08:31:30 the actual binary, config,
  identities, writer and guard matched the intended state. Gateway, Controller,
  Executor and browser PIDs did not change. At 08:31:31 daemon-reload restored the
  original `Restart=on-failure`; it caused zero additional restarts.
- One-time cleanup: the exact original plan SHA-256 is
  `5df235707d0894a59709135e18048cfad7e462430e227cfc4dfaa3244d635c19`.
  One transaction removed 865 Records and 865 Parents across two active devices
  (319 + 546), retaining the latest one for each. All eight nonactive rows were
  untouched. Total ciphertext fell from 6,974,116 to 393,828 bytes; the main
  credential generation remained 548 through cleanup. A subsequent plan had zero
  candidates. Three management GETs returned HTTP 200 with the original active
  device/client/invitation identities matching. No replacement identity or new mail
  operation was used to make the connection pass.
- Current task protection: the pre-maintenance snapshot contains 47 send, 154 unknown
  and one overlapping unknown-send task. Relative to 08:00, one QQ `collect_page`
  became uncertain at 08:08:56 UTC with `EMAIL_NETWORK_LIST_UNQUALIFIED`, null
  dispatch/token and five events, **before issuer maintenance**. Original 153 unknown
  and send rows remain exact; the new row is protected without retry. It is not a
  new send and its underlying cause remains unproved.
- Actual original-UI acceptance: normal `getApp` restored the original selected Mac
  profile connection. Existing mail entry, list, retained acceptance-message detail
  and expanded body passed. Normal reading changed one message's viewed state;
  no reply, draft, send or reanalysis operation was performed. The personal browser
  opened the public Apple page; its 462 px panel, wide/restore, new-tab/close and back
  to `about:blank` passed. At 08:40 UTC normal quit left zero app processes; original main device/invitation,
  workbench/connection/profile and identity hashes matched. Only five credential/Relay
  journal files changed: `grant.json`, `relay.enrollment.json`, its `.credentials.v2`
  and `.duplex.pending.v1` journals, and `identity/node-control/operations.v2`. Normal
  recovery advanced generation from 548 to 550 with zero pending pins. Installed-helper
  `-check` exited 0 and preserved all 17 profile files; normal relaunch reloaded 121
  mail-list entries and returned to the Owner workbench. Unviewed changed from 118
  to 117 after normal reading. **The final post-mail preservation comparison passed**; see the exact counts below.
- Final post-mail preservation: at approximately 08:41 UTC, the before-freeze
  snapshot `fbab80b978b8f9d4b3e95414ec2fb0ad274ca7f271b071d8463776057296eb4f`
  and after-UI snapshot `6443ddfbfa65bd18caa719dc8610ae534206b5755cd0b1e6229b5e6b1fd30628`
  matched across all nine protected sections: 47 send / 154 unknown / one overlapping
  unknown-send, 200 task rows, 1,003 events, 101 draft/send-snapshot rows and 10 devices.
  No new unknown or send change followed issuer maintenance. Other differences were
  observation time, normal watch mailbox/checkpoint/queue/metadata progress, rolling
  runtime health logs and mount ordering. Full runtime identity matched after sorting
  mounts and excluding the rolling health-log field; those raw differences remain
  retained. The private cold backup was downloaded off-host and verified:
  16,019,552 bytes, SHA-256
  `bf908136693d44721d0c4a54eea1af5e5d5c6888d6a32ce8bf9d7177f5c5cbf2`.
- Limits/rollback: no live provider rejection or first-ACK-failure injection, and no
  real send occurred; source S1 coverage must not be presented as those live cases.
  S4's Linux ledger reader remains `incomplete` with zero opens. No push occurred.
  Retain the cold backups and old binary, but never restore deleted credential history,
  old generation or business databases to hide changes. Returning to the old binary
  requires every current aggregate to satisfy its 4 MiB/1,024-record behavior and have
  room for the required update; do not silently reinstate the removed SQL limit.
  Public redacted evidence: [issuer capacity cleanup S5 summary](evidence/issuer-capacity-cleanup-s5-2026-10-26.json).

## 14. S4 Linux read-only ledger guard — installed; scoped acceptance passed

- ID / owner / status: S4 minimal M3, SparkClaw diagnostic adapter; source_verified,
  standalone CLI installed and qualified on the actual Linux amd64 host. This card
  supersedes the earlier Linux zero-open limitation only when the built-in guard qualifies.
- Observed failure and objective: the original Linux reader always returned
  `ledger_write_guard_unavailable`. The first implementation then failed to traverse
  the actual Ubuntu-owned 0700 directory/0600 ledger after dropping every capability.
  Reading supported sources now succeeds while kernel-enforced write denial remains.
- Scope / mechanism: only the standalone diagnostic command, its Go/Node ledger
  bridge and tests change. A child creates private PID/mount/network/IPC namespaces,
  private proc and recursive read-only/nodev/nosuid mounts; no-new-privileges/seccomp
  deny sockets, handles and namespace/mount escapes. Only `CAP_DAC_READ_SEARCH`
  (mask 4) remains; `CAP_DAC_OVERRIDE` is absent. Node independently verifies these
  conditions before source access. Caller flags cannot substitute for real isolation.
  Unsupported permissions fail with zero source opens. No App-CLI, live runtime,
  public business code, scheduler, owner policy or UI is changed; no ACK/resend exists.
- Red → green: first reproduced unavailable Linux reads, occupied-PID-namespace
  escape and unreadable user-owned sources; then repaired each boundary. Clean Linux
  Go suite: 5,613 passed test events / 77 tested packages, race/build/vet passed;
  Linux and macOS Node each 20 passed / 3 conditional skips, macOS Go passed.
  The initial old-image-overlay test contamination is retained and excluded.
  Actual deployment-host native tests: 7 passed, zero skipped, 64 real writes denied,
  handle syscalls denied, private user-owned reads passed, unqualified paths zero-open.
- Actual read acceptance: at 10:26:17 UTC, the same new Gmail collection unknown was
  reported twice (6 rows / 970 bytes each); selected task plus five events stayed
  exact, normalized reports agreed and recovery remained `inspect_without_resend`.
  Another owner could not join the task (one bounded row examined, no task exposed).
  Missing versions/logs/trace remained incomplete. Runtime writers were active:
  whole live DB/WAL/SHM byte equality is not claimed. No send or ACK occurred.
- Result / provenance: source topics `dfd9428e` + `2b866299`, integrated as one main
  change on `d511a1e0`. At 10:32:37 UTC, newly installed
  `/usr/local/bin/sparkclaw-email-diagnose` targets
  `/opt/sparkclaw/releases/20261026-email-diagnostics-ad7ae6d7/bin/sparkclaw-email-diagnose`,
  SHA-256 `ad7ae6d7c8af85721d73823c642ed64893903e50cf76ba84fd66c42c24b386cf`.
  Gateway remains `0f46702a`, App-CLI remains `.41`; no service restart for this install.
  [Redacted acceptance evidence](evidence/s4-linux-readonly-acceptance-2026-10-26.json)
  separates source, native qualification and actual reads.
- Remaining limits: requires suitable namespace/mount/seccomp permissions and Node 26;
  there is no automatic privilege elevation. A cold WAL database with no WAL/SHM may
  remain incomplete; no source sidecar creation or automatic immutable fallback.
  This does not repair the newly recurring Gmail collection failure.
- Rollback: remove only the exact new symlink and release binary, or revert this
  diagnostic source change. No business migration/data restoration is required.

## 15. S5 manual-tool result persistence — source_verified

- ID / owner / status: S5 acceptance finding, Gateway generic manual invocation;
  source_verified. Deployment and final business acceptance are recorded separately.
- Observed failure: the normal typed Gmail Sent read completed on unchanged `.41`,
  but Gateway `0f46702a` returned HTTP 500 while persisting its completed tool call:
  `tool_calls_run_id_fkey` / SQLSTATE 23503. The success branch saved the child call
  before its parent run. Ordinary policy denial, execution failure and approval-pending
  paths already save the run first; no blanket qualification of other failure branches is claimed.
- Evidence: one completed native read, a completed observation artifact and the
  exact session → admission audit → observation → ledger capture → native manifest
  chain prove the original operation completed. The independent Sent original's
  full body, observed headers and attachment bytes passed. No second read or send
  was issued; the missing run/call records and original HTTP failure remain evidence.
- Objective / scope / mechanism: successful manual calls now persist the parent run
  before the child call, so the same result survives PostgreSQL's existing foreign key.
  Only this generic success ordering changes. No mail branch, schema migration,
  Store contract, public error-code, scheduler, App-CLI, UI or permission change.
- Red → green: the new ordinary `memory.search` regression uses a synthetic empty
  store with retention disabled. Original memory/file passed, real isolated PostgreSQL
  failed with the exact FK error. After the minimal order fix, all three passed;
  file and PostgreSQL reopen/readback verified one completed run, exactly one call
  and equal stored output. The targeted race run passed. Tests do not access a real
  provider. The source tests and original failed logs are retained.
- Result: source topic `adaca175d2a0bccdf3da753c901a6d750f08783f` is integrated as a
  separate main change. The actual original Sent evidence stays valid independently
  of the failed API persistence; it is not rewritten as an HTTP 200 success.
- Remaining limits: this does not create a cross-record transaction or recover the
  already failed historical run/call rows. QQ's pre-dispatch account-evidence failure,
  the Outlook original's extra LF and collection qualification failures are separate.
  Installed acceptance uses `reminders.list`, which avoids `memory.search` retention
  side effects and does not repeat the completed provider read.
- Rollback: revert the success-order source change / restore the prior Gateway image;
  no data migration, source-row deletion, App-CLI replacement or UI rollback is needed.

## 16. S5 continuation — deployed and scoped acceptance; mail matrix remains open

- ID / owner / objective: S5, SparkClaw runtime and one-time acceptance operators.
  Qualify the actual release, finish authorized business checks and delete selected
  unknown history. This card records deployment/operations separately from source
  repairs in cards 14–15. No automatic expiry or retry mechanism was added.
- Files / mechanism: final evidence and the bilingual plan/follow-up/card documents
  are the repository changes in this acceptance commit. Private operators, exact
  deleted-row backups and original failures remain outside Git. Gateway alone was
  replaced for the generic manual-call parent-before-child persistence fix; no mail
  branch, schema, UI, public-code, scheduling or App-CLI `.41` change.
- Tests first: the real PostgreSQL FK regression failed before repair, then passed
  in memory/file/PostgreSQL, including reopen and race checks. Final Linux full run:
  5,610 pass / 203 skip / 77 packages; build, vet and cross-build passed. Two earlier
  runs retained unrelated temporary-directory and expiry-clock failures; isolated
  reruns and the final serial suite passed without changing those product paths.
  One-shot cleanup/release operators also retained their failing guard tests before
  repair. Full-row canonical SHA is distinct from the operation protocol digest;
  two local review assertions stopped before remote action and were corrected using
  exact row equality. Request epoch 13 is distinct from task ledger epoch 73.
- Exact deployment artifact: source `adaca175d2a0bccdf3da753c901a6d750f08783f`,
  same production repair as main `d97cfcf1`; executable SHA-256
  `2bb5267d2908c98908cb4cad9babb7350067cb8a86001c0a192b65665f9e2fec`;
  image `sha256:f388b1958ed13bb299a372dfdb269a5437f890326598ce6350593a39fc9d5a4d`.
  Controller/remote checkout and Mac remain `0f46702a`; `.41` manifest remains
  `e315727dfe2802b92ef97f3fc0828825e343b3056f5a8d3214207c32840a7db4`.
- Installed artifact faults: eight isolated cases passed with 12 normal process
  stops and no remaining owned containers. Rejection → ACK 503 → restart produced
  one total dispatch and one later successful ACK. Unknown restart/same-key reuse
  produced zero additional dispatch/ACK; a new key returned 409. Concurrent causes,
  normal cancellation, actual file renewal failure at the unchanged 60-second
  interval, trace failure, latency initialization failure and real ENOSPC sink
  degradation passed. Controller/provider are synthetic; this does not qualify real
  provider rejection, real `.41` ledger recovery, Browser Host D03, desktop recorder
  or PostgreSQL lease faults. See [artifact evidence](evidence/s5-installed-fault-acceptance-2026-10-26.json).
- Deployment evidence: the first start failed while building semantic embeddings
  (batch 4 of 5, existing 20-second deadline). One separately reviewed additional
  start became healthy; no product timeout or scheduling change. Docker's created
  `OomKillDisable` null/false mismatch and a normal three-collection startup window
  caused retained operator stops. Final raw HostConfig, protected data and other
  services matched; `unless-stopped` was restored without another restart. Normal
  `reminders.list` returned 200 with exactly one completed parent/run and child/call
  in PostgreSQL and equal output. QA revoke 200, rejected token 401, token removed.
- Business evidence: both existing recipient originals were reanalysed once;
  original files/captures/representations/jobs remain exact, with one new v6 parse
  job and representation each. The presentation preserves the full decoded original.
  Gmail→QQ body/attachment matches the approved message. Outlook→Gmail still has
  one extra terminal LF already in the native original; its source stage is unproved.
  Gmail's independent Sent original passed exact body/header/attachment checks via
  one normal typed read. Its original HTTP 500/FK failure remains recorded despite
  successful native collection; no second read or manufactured run/call rows.
- QQ evidence: two new authorized API invocations, zero actual sends. First stopped
  at Sent-baseline account evidence; missing versus unequal account hash remains
  unproved. A later completed QQ collection supplied matching account/result and
  profile evidence. The second send progressed to `prepare_draft`, then rejected
  only the body (106 expected / 109 read back), code `email_draft_fields_unverified`.
  The task has attempt/effect 0 and null dispatch/token; a bound negative journal
  corroborates not-dispatched. Its initial finish guard encountered transient
  non-idle state; original STOP retained, subsequent classification was read-only.
  Further repair stops at the unchanged `.41` boundary. No third attempt. Both QA
  clients were revoked/401-checked/deleted. Original actual sends remain QQ 0,
  Outlook 1, Gmail 1; cases 2/3 were not resent.
- Authorized cleanup: first normal freeze removed 154 unknown tasks, 698 events,
  385 exactly bound files and two PostgreSQL unknown draft/snapshot rows. Unrelated
  data stayed exact. A later Gmail collection produced one new unknown; it was
  diagnosed and never retried. Normal epoch rotation/maintenance retired its
  request namespace; final freeze removed that one task, five events and three
  files. One already absent linked file remained an explicit gap. Final cold readback
  proved unknown 0, 48 send tasks, unchanged remaining ledger/files/authority and
  all 132,690 PostgreSQL rows across 23 kinds. No PostgreSQL deletion in round two.
  Total: **155 tasks / 703 events / 388 files / two PG rows**. Bounded private backups
  are retained off-host; no bulk history restoration or automatic GC.
- Final state: normal resume healthy at 12:29:41 UTC; at 12:39:56 UTC unknown 0,
  unknown-send 0, send 48, draft/snapshot 103, devices 10, active send/pending release
  0. Browser and issuer stayed unchanged. Source manifest/config/owner boundaries
  remain intact. Earlier card 13 original UI and quit/relaunch passes remain valid;
  the final post-maintenance Mac connection recheck could not run because the Mac
  is locked. Unlock was requested; no replacement profile or UI workaround.
- Limits / rollback: full S5 mail matrix remains open for QQ body validation,
  Outlook extra LF and QQ/Outlook independent Sent originals. Collection failure
  root cause, 381 historical terminal gaps and retained CI failures remain unproved;
  cleanup does not repair them. Exact old Gateway container/image is retained stopped
  for reviewed rollback; never restore deleted history, old epochs or credentials
  to mask changes. Deleted aliases can let a legacy caller allocate a new op3 identity;
  no claim that all old caller IDs become permanently impossible. Runtime automatic
  unknown resend protection remains unchanged. No push or InfiniCenter work.
- Evidence: [business/deployment/cleanup](evidence/s5-business-acceptance-2026-10-26.json),
  [source qualification](evidence/s5-manual-persistence-fix-2026-10-26.json).
- Documentation validation: the actual CI mirror/link check passed for 134 project Markdown files and `git diff --check` passed. The separate Team-Skills check still reports 37 pre-existing lesson frontmatter errors and 56 warnings; the new lesson is not among those failures. No unrelated lesson repair is included.

## 17. S5 final desktop connection recheck — passed after manual unlock

- ID / objective / scope: S5 original SparkX connection recheck on 2026-10-27,
  after the owner manually unlocked the Mac. Read the original UI and local profile;
  no product code, release, identity, authorization or configuration change.
- Initial failure and check: the existing Owner workbench initially displayed HTTP
  503. One click on its original retry-read control cleared the notice; refreshing
  the device list succeeded and the three previous S5 acceptance clients were shown
  revoked. No application restart, new credential or profile selection was needed.
- Evidence: the original mail entry loaded 121 items (115 unviewed), with intake
  enabled. No individual message, compose/send, reanalysis or manual synchronization
  action was opened/submitted. Closing mail returned to the original Owner workbench
  with no HTTP 503. The running helper used the original enrollment profile; all
  seven launch/profile/identity/connection configuration files matched the pre-upgrade
  cold baseline. CUA observations and a private hash comparison support these checks.
- Result / files: the final desktop connection gate is now passed; section 2 and the
  current follow-up list are updated, with [redacted evidence](evidence/s5-desktop-recheck-2026-10-27.json).
  Earlier full UI/quit-relaunch checks remain in card 13; this is a scoped follow-up,
  not a new full regression or a new remote inventory.
- Limits / rollback: the initial 503 cause is unproved; normal recovery is not a
  claimed source repair. QQ draft body, Outlook extra LF and QQ/Outlook independent
  Sent-original acceptance remain open. No `.41`, UI, public-code or scheduling
  change, deployment, mail send or push; no runtime rollback is needed.
- Validation: the project CI mirror/link check passed for 134 Markdown files, and `git diff --check` passed. The separate Team-Skills check retains 37 existing lesson metadata errors and 62 age warnings; no product test or remote fault case was rerun for this documentation-only closeout.

## 18. Authorized publication and service version reconciliation

- Owner/scope: on 2026-10-27 the owner authorized committing/pushing the current workspace and upgrading related services. Thirteen existing commits are published at `ebc35697`; work2 fast-forwarded from `0f46702a` to this revision with a clean checkout. Earlier no-push statements describe their historical checkpoints. Two local evidence-directory fields were sanitized throughout the unpublished history after retaining the original commits locally; artifact/source aliases are recorded in the [release evidence](evidence/service-version-reconciliation-2026-10-27.json).
- Loaded versions: the running Gateway binary remains SHA256 `2bb5267d…f9e2fec`, built from `adaca175d`; its complete Gateway source tree equals the published source (manual repair now `78505227`, historically `d97cfcf1`). The installed Linux diagnostic binary remains `ad7ae6d7…b386cf`. Controller, Desktop, WebChat, bridge, helper, scripts and configuration have no new runtime code since the accepted `0f46702a` deployment. All 296 Mac bundle entries match the accepted manifest. Existing services already contain the requested fixes, so source synchronization requires no rebuild or restart.
- Verification: all five remote container identities/processes/configuration hashes and three owner service processes match before/after; private configuration hashes are exact. Gateway `/healthz` and `/readyz`, WebChat and the JavaScript audio worklet return HTTP 200 with the existing production Node TLS verification. Original desktop mail navigation loads 121 items, 115 unviewed, then returns to the Owner workbench. No individual mail, new draft/send, reanalysis or manual sync was submitted. The prior exact-source 77-package Go qualification (5,610 pass / 203 skip), build/vet and targeted-race logs were rehashed; those tests were not rerun for this source-only synchronization.
- CI: the published run passes Docs, WebChat, ASR and Gateway race at this checkpoint; Gateway is still running. Compose, Electron anchor and Desktop qualification fail with the same respective signatures found in the previous `db9ca9cf` CI run. This release is not reported CI-green. Wrong-port probes and Python 3.14's stricter existing-CA rejection are retained separately; the final service checks use the existing production Node TLS client without changing trust settings.
- Limits: App-CLI `.41`, UI, public codes, scheduling, identity and data remain unchanged. No new database inventory or unknown/send totals are claimed. QQ body readback, Outlook trailing LF and QQ/Outlook independent Sent originals remain open; this reconciliation does not close the full mail matrix.
