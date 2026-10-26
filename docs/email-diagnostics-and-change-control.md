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

**This change is documentation only.** Repairs and diagnostic fields below are
planned, not implemented. No mail, deployment, interface or UI change occurred.

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

These are source findings, not proven causes of every historical incident.
Locate by file/function if line numbers move.

| ID | Source finding | Exact repair boundary |
|---|---|---|
| D01 | [`page_intake.go`](../services/gateway/internal/emailmanagement/page_intake.go), `commitListFailure` (523–539), omits `InvocationID`; successful commit (426) includes it. | Carry the actual failed invocation into history. Distinguish failure before invocation, collection failure and split-query failure; never invent a call that did not happen. Preserve checkpoint/retry semantics. |
| D02 | [`compose.go`](../services/gateway/internal/emailmanagement/compose.go) (275–282) reduces most send errors to `email_send_outcome_unknown`; [`playwright_runner.go`](../services/gateway/internal/emailautomation/playwright_runner.go), `mapPlaywrightError`, also merges lower-level codes. | Retain a separate safe cause/stage/source layer. Preserve public error behavior and unknown-send fencing. |
| D03 | [`host-driver.mjs`](../tools/browser-controller/src/host-driver.mjs) (212–213, 270–271) logs phase/code/reason/command without task identity. | Add validated task/operation correlation. Timestamp proximity cannot reliably join concurrent operations. |
| D04 | [`service.go`](../services/gateway/internal/emailmanagement/service.go), `execute` (380–382), cancels on renewal failure without its cause; `safeCode` (434–462) collapses unlisted parse/source/model errors. | Preserve typed internal cause and stage, independently of public error text. Distinguish lease/storage failure, cancellation and deadline. |
| D05 | [`endpoint_rpc.go`](../services/gateway/internal/iscpworkbench/endpoint_rpc.go) (174–176) records handler status from `callCtx.Err()` only. `commitListFailure` returns nil after storing a business failure, so the job can succeed. | Separate transport/worker completion from business outcome. Do not change scheduling semantics just to change a log status. |
| D06 | [`latencytrace/trace.go`](../services/gateway/internal/latencytrace/trace.go) is opt-in and bounded (1,024 queue / 64 MiB), with dropped/limit markers but silent initialization/sink failures. [`latency-trace.mjs`](../apps/desktop/src/main/latency-trace.mjs) silently stops at 100,000 records or write failure. | Report enabled/disabled/degraded state, coverage and loss. Missing events do not prove no execution. |
| D07 | [`agent.go`](../services/gateway/internal/agent/agent.go) (1007) ignores trace-write error; [`trace/writer.go`](../services/gateway/internal/trace/writer.go) (76–78) suppresses artifact-store error; [`app-cli-client.mjs`](../tools/browser-controller/src/app-cli-client.mjs) (56) suppresses maintenance failure. | Make evidence/maintenance failure observable without rewriting mail results. Agent traces do not cover every direct compose or background sync. |
| D08 | Validated provider rejection becomes `failed` in [`compose.go`](../services/gateway/internal/emailmanagement/compose.go) (293); first ACK failure is ignored (306). [`lifecycle_maintenance.go`](../services/gateway/internal/emailmanagement/lifecycle_maintenance.go) (86) retries only `sent`. | Source-confirmed ACK recovery coverage gap. First reproduce in isolation, then include eligible rejected terminal results; zero redispatch. Not a proven live incident cause. |
| D09 | [`analysis.go`](../services/gateway/internal/emailmanagement/analysis.go) retains failed analysis files, but failed [`EmailJob`](../services/gateway/internal/app/email_management.go) has no direct execution/artifact/ModelCall index; `SaveModelCall` failure can still return the generated ID. | Index analysis attempts and distinguish artifact/model-call persistence success from an allocated reference. Never present a nonexistent record as evidence. |
| D10 | [`compose.go`](../services/gateway/internal/emailmanagement/compose.go) (258–264) ignores epoch lookup error and can fall back to a legacy invocation. | Record identity mode and safe epoch failure. Removing fallback would be a separate behavior decision, not a logging fix. |

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

All batches are **planned**. Independent behavior repairs and diagnostic/schema
work are separate changes. An unrelated failure gets a separate item.

| Batch | Exact change | Acceptance |
|---|---|---|
| M1 — Preserve attribution | D01/D03/D04: failed invocation, safe Host task relation, renewal first cause | Concurrent failures join correctly; before-call and split failures distinguished; memory/file/Postgres preserve failure identity; cancellation retains cause; no payload leak or scheduling change. |
| M2 — Make evidence honest | D02/D05–D07/D10: safe internal cause, separate outcomes, sink health, trace/maintenance failure, identity mode | HTTP success + business rejection distinguishable; unknown stays unknown; disabled/full/broken sinks yield incomplete evidence without changing business results; epoch success/unsupported/failure distinguishable. |
| M3 — One report | D09 plus indexed read-only aggregation, versions and stage coverage | Failed analysis locatable by attempt; missing ModelCall/artifact explicit; same operation reconstructs after restart; duplicate/out-of-order events and other owners cannot mix or invent completion; zero query side effects. |
| M4 — Bounded product repairs | Reproduce/fix D08 first. Give each QQ/body/Sent issue its own current-version reproducer/card. | Rejected result + first ACK failure + restart eventually ACKs with zero redispatch; sent/unknown protection unchanged. Provider repairs verify exact failed boundary and downstream result. |
| M5 — Installed version and UI | Qualified component set, then explicitly authorized live cases | Actual installed pins and dispatch counts; separate Sent/Inbox/body/attachment/UI results; reuse successful mail read-only where possible; partial matrix is not universal success. |

**Start with the D08 repair (M4) or M1, in either order**: they are independent
and are committed separately. M1 makes the next failure attributable; M2/M3 make
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
