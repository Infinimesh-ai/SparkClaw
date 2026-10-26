# Application runtime: current-stage stabilization plan

> Language: English | [简体中文](../zh-cn/docs/application-runtime-stabilization-plan.md)

## 1. Decision and scope

**Status: implementation in progress, 2026-10-26, Asia/Shanghai. S1–S3 source_verified; S4 design requires confirmation, S5 unauthorized.**

App-CLI currently has a single application module (`runtimes/browser/applications/mail`) and no
approved second consumer. This stage is therefore limited to: **the existing mail path, on the
existing foundation, must not lose results during recovery, must make one failure attributable,
must be diagnosable without a full-database export, and must complete `.41` installed-version
acceptance.** Building an "any module changes only module files, never the core" platform is out
of scope; it is designed separately when requirements rise (section 5).

Audit baseline: SparkClaw `db9ca9cf`; pinned App-CLI `.41`, source
`bcc55506cc933f981668e3b2d1ed1eba1bf4c6a3`. The original audit read source and retained evidence without rerunning tests or inspecting a live system. Current source tests are recorded in section 2 and the change cards. The
[deferred acceptance list](desktop-browser-follow-up-2026-10-23.md) still records `.41` as not
activated and `.39` as the last verified deployment.

Existing ownership is kept: the App-CLI Registry owns capabilities, schemas and authorization; the
Executor and ledger own task identity, effects, results, ACK and recovery; the Controller Browser
Host owns pages/profiles, resource queues and leases; the mail module owns provider operations and
their result proofs; the Gateway owns business state and the original UI. Each gap is fixed inside
its current owner; no new layer is added.

## 2. Goals for this stage

| Goal | Current state (confirmed in source) | Exit |
|---|---|---|
| G1 Recovery keeps results | **S1 source_verified (2026-10-26)**: eligible rejected terminal receipts now recover ACK. File Store restart reproducer changed red → green; two maintenance rounds ACK once and keep total dispatches at 1. sent/unknown and proof identity fencing pass. Not deployed or live accepted. | Source exit met; deployment and installed acceptance remain separately authorized. |
| G2 One failure is attributable | **S2/S3 source_verified (2026-10-26)**: actual failed/split invocation survives all three backends; current Host activity correlation; renewal first cause; safe send causes, epoch identity mode, ACK/trace/maintenance failure and bounded sink health. Public codes and scheduling unchanged. | Source exit met within instrumented boundaries; missing logs remain incomplete. No deployment/live acceptance. |
| G3 Minimal read-only diagnosis | Evidence is spread across receipts, task events, Host logs and traces and must be joined by hand. | Query by draft/operation/job and return the loaded versions, last confirmed stage, first cause, execution result, business result, ACK and missing evidence; anything unprovable is `incomplete`. |
| G4 Installed-version acceptance | `.41` is qualified and packaged but not activated. | After separate authorization, completed on a supported configuration per the deferred acceptance list, with each conclusion recorded separately. |

Exact repair boundaries for each D item are in
[mail diagnostics and change control](email-diagnostics-and-change-control.md) and are not repeated here.
D05 (job completion vs. business result) and D09 (analysis attempt index) do not block this stage's
exit and proceed separately under the mail document.

## 3. Order

1. **S1 Fix D08**: first add a reproduction test (rejection → failed first ACK → restart/maintenance
   round), then let the maintenance loop cover terminal results carrying a valid rejection proof.
   Commit separately from diagnostics changes.
2. **S2 Preserve attribution** (mail M1): D01, D03, D04. Independent of S1; either may go first.
3. **S3 Report evidence truthfully** (the necessary part of mail M2): D02 internal cause, D06/D07
   visible write failures, D10 identity mode.
4. **S4 Read-only report** (minimal mail M3): reuse existing receipts/events/diagnostic entry points
   through a command or existing diagnostic entry; no persistent UI, no new external API. Reading must
   not ACK, resolve, retry or modify tasks.
5. **S5 Installed-version acceptance** (mail M5): only after owner authorization; this plan grants no
   deployment or live-system permission.

Each step uses the change card in section 6 of the mail document; source verification, deployment
and live acceptance are recorded separately.

## 4. Acceptance cases

| Case | Required observation |
|---|---|
| Provider rejection, failed first ACK, restart | ACKed later; business result stays rejected; dispatch count unchanged. |
| sent / unknown regression | Existing ACK and unknown-write protection unchanged; unknown is never resent. |
| Two concurrent failures | Each linked to its correct invocation/task, not joined by time. |
| Failure before admission | Clearly shows no task was created; no invocation is invented. |
| Cancellation or lease-renewal failure | First cause retained; later cancellation does not overwrite it. |
| Diagnostics disabled, full or failing | Business result unchanged; report marks `incomplete` and the missing boundary. |
| Changes touching the Store | memory/file/Postgres backends all retain failure identity. |
| Privacy | Ordinary diagnostics contain no addresses, subjects, bodies, filenames, raw URLs, credentials or provider payloads. |
| Original UI | Existing pages, entry points and interactions unchanged. |

## 5. Not in this stage; design when requirements rise

**Kept as is** in this stage: production assembly keeps wiring the mail handlers directly; the
Controller client, installer and release projections stay mail-specific; no generic facade,
qualification-only test module or module-replacement acceptance is added; Python is not removed,
Executor/Controller processes are not merged and the communication mode is not changed; shared
ISCP/JingSi/IMMS contracts are not changed.

The one constraint is **do not deepen coupling**: new changes must not add mail or provider branches
to the Executor, ledger, Registry policy, HostDriver or HostPage (none exist there today), so later
generalization stays cheap.

| Trigger | What to design then | Starting point (known facts) |
|---|---|---|
| A second real application module is required | Production assembly, installation and projections driven by a reviewed module list; a shared application entry; mail routes become an adapter | Known coupling: `A:runtimes/browser/src/assembly.mjs` imports `mailHandlers()` directly; [app-cli-client.mjs](../tools/browser-controller/src/app-cli-client.mjs) always constructs MailboxClient; [controller.mjs](../tools/browser-controller/src/controller.mjs) creates MailObserverFeed; [main.mjs](../tools/browser-controller/src/main.mjs) mixes mail options into Host startup; [install-app-cli.py](../scripts/install-app-cli.py) looks up `bindings/mail-*.json`; [sync-app-cli-projections.mjs](../scripts/sync-app-cli-projections.mjs) fixes the mail asset list. |
| User-visible latency or a measured bottleneck | Simplified control/page communication and waiting | Persistent machine session, direct socket, page helper and read batching exist in source and tests but are not qualified on an installed version. A request that may have been dispatched must never fall back and resend. |
| Independent module release or upgrade replacement | Module version replacement and compatible reconciliation of old pending/uncertain tasks | Existing recovery classes `pure_read`, `replay_safe_local`, `external_write`, `subscription` already express execution semantics. |
| Remote or API diagnostics | Authorization, owner isolation and contract review | Start from the S4 read-only report fields. |

At that point write the corresponding design first and implement after review; cross-project
interfaces or permissions first need an accepted InfiniCenter decision.

## 6. Reporting

Each implementation records the goal, exact files, preserved semantics, reproduction, result evidence,
remaining limitations and rollback boundary, and updates the goal table in section 2. A test count
never closes a goal; unit tests prove only `source_verified`, not `deployed` or `accepted`.
