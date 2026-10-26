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
The latest retained [acceptance record](desktop-browser-follow-up-2026-10-23.md)
still describes `.39` running and `.41` not activated. This audit did not read a
live host; these are historical observations, not a new health check.

**Implementation status:** S1–S4 are source_verified (sections 7–10); S5 is unauthorized. D05/D09 remain deferred. No real mail, deployment, interface or UI change occurred.

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

S1–S4 are **source_verified**; S5 and D05/D09 are pending. Independent behavior repairs and diagnostic/schema
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
