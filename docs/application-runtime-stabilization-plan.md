# Application runtime: current-stage stabilization plan

> Language: English | [简体中文](../zh-cn/docs/application-runtime-stabilization-plan.md)

## 1. Decision and scope

**Status: 2026-10-27, Asia/Shanghai. S1–S4 deployed; exact-artifact fault acceptance and Linux read-only qualification passed. S5 deployment and original UI checks passed, but full mail acceptance remains incomplete. The generic manual persistence repair is deployed; authorized unknown cleanup is complete. See section 2 and cards 14–17 for precise limits.**

App-CLI currently has a single application module (`runtimes/browser/applications/mail`) and no
approved second consumer. This stage is therefore limited to: **the existing mail path, on the
existing foundation, must not lose results during recovery, must make one failure attributable,
must be diagnosable without a full-database export, and must complete `.41` installed-version
acceptance.** Building an "any module changes only module files, never the core" platform is out
of scope; it is designed separately when requirements rise (section 5).

Audit baseline: SparkClaw `db9ca9cf`; pinned App-CLI `.41`, source
`bcc55506cc933f981668e3b2d1ed1eba1bf4c6a3`. The original audit read source and retained evidence without rerunning tests or inspecting a live system. Current source tests are recorded in section 2 and the change cards. The
[acceptance list](desktop-browser-follow-up-2026-10-23.md) retains the historical `.39` checkpoints; current loaded `.41` evidence is recorded in section 2 and change cards 12–17.

Existing ownership is kept: the App-CLI Registry owns capabilities, schemas and authorization; the
Executor and ledger own task identity, effects, results, ACK and recovery; the Controller Browser
Host owns pages/profiles, resource queues and leases; the mail module owns provider operations and
their result proofs; the Gateway owns business state and the original UI. Each gap is fixed inside
its current owner; no new layer is added.

## 2. Goals for this stage

| Goal | Current evidence and deployment state | Exit |
|---|---|---|
| G1 Recovery keeps results | **S1 source_verified, deployed; exact-artifact isolated acceptance passed (card 16).** Gateway `adaca175d` (same repair as main `d97cfcf1`) survives rejection → first ACK 503 → normal restart with the same file Store: one dispatch, two ACK attempts, one later successful ACK. Unknown survives restart with no additional dispatch, zero ACKs and HTTP 409 for a new send key. | Passed for the actual deployment binary with synthetic Controller/provider, not live provider fault injection or actual `.41` ledger recovery. Explicit one-time deletion of unknown history is separate from unchanged automatic unknown fencing. |
| G2 One failure is attributable | **S2/S3 source_verified, deployed; isolated binary cases passed.** Two concurrent failures retain distinct invocations/causes; ordinary cancellation and actual file-store renewal failure retain the first cause. Trace, latency initialization and ENOSPC sink failures leave business results unchanged and expose incomplete/degraded evidence. | Original 60-second renewal and product limits retained. Actual Browser Host D03, desktop recorder and PostgreSQL lease fault injection remain outside this installed test scope. Missing evidence stays incomplete; public codes and scheduling are unchanged. |
| G3 Minimal read-only diagnosis | **S4 source_verified, standalone CLI installed; native Linux acceptance passed (card 14).** Seven amd64 qualification tests denied 64 actual writes. Two real reports each read six rows of the Gmail collection unknown, preserved task/events and isolated owners. That reviewed historical target was subsequently deleted by explicit authorization. | Qualified Linux reads, zero ACK/resend and unavailable-guard zero opens passed. Missing logs/version/trace and cold WAL without sidecars still have explicit limits. No App-CLI change or automatic immutable fallback. |
| G4 Installed-version acceptance | **S5 deployment/installation and scoped checks passed; full mail acceptance remains open (cards 13–17).** Gateway `adaca175d` binary is healthy; Controller/remote checkout and Mac remain `0f46702a`, App-CLI `.41` is unchanged. The generic manual-call FK repair passes real PostgreSQL `reminders.list` HTTP 200 with one saved run/call. Existing recipient reanalysis and Gmail independent Sent-original body/attachment evidence passed. | Two fresh QQ requests both prove zero dispatch; latest fails `.41` draft body readback (106 expected / 109 actual), so further repair stops at the no-App-CLI-change boundary. Outlook's extra LF and QQ/Outlook independent Sent originals remain open. Authorized cleanup removed 155 unknown tasks in two rounds; 2026-10-26 12:39 UTC observation: unknown 0, send 48, draft/snapshot 103, devices 10, healthy. Earlier original UI/quit-relaunch acceptance passed. On 2026-10-27, the unlocked original desktop recheck also passed: normal retry cleared an initial HTTP 503, device refresh and 121-mail list reads succeeded, and seven original profile/identity/configuration files matched. The initial 503 cause remains unproved (card 17). No automatic expiry mechanism, UI/public-code/scheduling change or push. |

Exact repair boundaries for each D item are in
[mail diagnostics and change control](email-diagnostics-and-change-control.md) and are not repeated here.
D05 (job completion vs. business result) and D09 (analysis attempt index) do not block this stage's
exit and proceed separately under the mail document.

## 3. Order

1. **S1 Fix D08**: first add a reproduction test (rejection → failed first ACK → restart/maintenance
   round), then let the maintenance loop cover terminal results carrying a valid rejection proof.
   Commit separately from diagnostics changes.
2. **S2 Preserve attribution** (mail M1): D01, D03, D04. Per the current authorization, follow S1.
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

## 7. S4 approved design and implementation — source_verified

### 7.1 Entry and scope

The local operator command is `sparkclaw-email-diagnose`, with required `--owner`
and exactly one of `--draft`, `--operation`, `--job`; output `--format text|json` to stdout.
For `--operation`, require `--identity-kind invocation|request-key` to avoid an implicit legacy mapping.
Input sources are explicit local paths or an existing PostgreSQL connection configuration;
credentials are read from an existing protected configuration or environment, never printed or
accepted as a command-line password. Ledger input also requires `--ledger-principal` and `--ledger-owner` for its exact scope;
Gateway owner and ledger owner are not assumed interchangeable. A missing verified mapping is a gap.

This is a SparkClaw mail diagnostic adapter under `services/gateway/cmd/sparkclaw-email-diagnose`
and `services/gateway/internal/emaildiagnostics`, with the canonical Node 26 SQLite reader embedded from
`internal/emaildiagnostics/ledger_reader.mjs` and a thin standalone wrapper under
`tools/browser-controller/scripts/`. The Go entry executes embedded bytes with a restricted Node
environment; neither a CWD helper nor preload/coverage/cache environment settings can run before the guard. It uses existing Go/Node runtimes and schema fixtures. No
runtime service is started, no external API/UI is added, and App-CLI `.41` stays unchanged. The
operator must already have source read access; `--owner` narrows joins, not grants permission.
Memory Store is an in-process test fixture only, not a newly exposed live-memory endpoint.

The report never ACKs, resolves, retries, reconciles, prunes, migrates, renews, creates an epoch or
imports production bootstrap code. Recovery suggestions are descriptive and contain no executable
actions. Unknown always stays fenced against resend. D05/D09 remain outside this implementation:
existing worker completion and business results are displayed separately; missing analysis attempts
remain missing, with no attempt-index schema or scheduling change.

### 7.2 Source readers and bounds

| Source | Implemented read path | Missing or unsupported case |
|---|---|---|
| Gateway PostgreSQL | Read-only, repeatable-read transaction; parameterized owner/kind/id and parent/related queries over existing `email_management_records` indexes; select only required safe projections. No Store constructor/migrations. | Operation → snapshot has no dedicated invocation index: bounded owner-scoped scan of `send_snapshot`, reported as a scan. Timeout/limit/unsupported schema gives incomplete, never a new index or unbounded scan. |
| Default file Store | Open existing snapshot read-only, stream JSON and retain only selected owner records/fields. Bounded scan is necessary: this monolithic format has no random-access index. Do not call `NewFileStore` or migrated Store read methods. | Changed/truncated file, budget cutoff or unsupported schema gives incomplete. Encrypted snapshots initially report `encrypted_source_unsupported`; no key handling, whole-file plaintext export or decryption change is included. |
| App-CLI `.41` ledger | Standalone Node native SQLite read-only connection, query-only mode and fixed SELECT/schema-read allowlist. Exact `(principal, owner, request_key)` / task and `(task, sequence)` reads; select safe fields rather than request/data/payload wholesale. | Never instantiate `Ledger`: its constructor changes permissions, schema, epoch and unfinished tasks. No create, journal-mode change, checkpoint, recovery or immutable mode that ignores an active WAL. If the DB/WAL/shared-memory combination cannot be opened without source writes/creation, return incomplete. |
| Receipts, Host/worker logs, traces and health | Operator-selected retained files, bounded streaming, schema/field allowlists and exact identity matches; use already recorded health events. | No remote journal crawl, provider lookup, live `latencytrace.Status()` initialization or runtime lifecycle calls. Unavailable/expired/malformed/unread evidence is explicit. |

Per-query limits: a 10-second work deadline, 2,000 selected domain/event rows, 256 MiB
snapshot input, 32 MiB total log/trace input, 1 MiB per parsed record and 1 MiB output. Large skipped
JSON values must be streamed with bounded memory. The result records the limits, sources examined,
records/bytes read and cutoffs; hitting any limit produces `incomplete`. No automatic budget increase,
full-database export or persistence of raw input is allowed. Limits are diagnostic resource bounds,
not business timeouts or evidence-retention policy. Each source has its own snapshot/provenance;
the report does not claim an atomic cross-store snapshot while services are active. The CLI supervises
an owned process group, including SQLite children; cleanup and timeout output have separate bounded
200/100 ms waits. Counts describe logical source/projection bytes, not physical SQLite page I/O.
Changed/truncated snapshots and changed logs/ledger are not accepted as proof; cutoff or conflicting
evidence cannot supply receipt owner binding or ACK eligibility.

The macOS reader uses `sandbox-exec`. The standalone Linux Go CLI first creates private PID/mount/network/IPC
namespaces and procfs, recursively makes mounts read-only/nodev/nosuid, and applies no-new-privileges and seccomp.
It retains only `CAP_DAC_READ_SEARCH` (mask 4) for application-owned 0700/0600 evidence, never the write-bypassing
`CAP_DAC_OVERRIDE`. Sockets, file-handle access and namespace/mount escapes are denied. Node verifies actual kernel
state and denial before reading a source; a private flag alone cannot bypass the guard. Seven target-host qualification
tests and actual unknown/owner reports passed.

Existing Node 26 and appropriate kernel permissions, normally sudo/root, are required; there is no automatic elevation.
An unavailable guard returns `ledger_write_guard_unavailable` with zero source opens. The direct Node wrapper still
provides no Linux bypass. Default DELETE static databases and active WAL are qualified; a cold WAL-mode database whose
WAL/SHM disappeared on normal close can still be incomplete. No automatic immutable mode, source sidecar creation or
weaker write guard is used. Live runtime writers may progress: actual selected rows/events stayed exact, not every byte
of the live DB/WAL/SHM. Separate native qualification establishes the kernel's actual write denial.

### 7.3 Joins and interpretation

- Draft → frozen send snapshot → exact invocation/request key → scoped ledger task → ordered task
  events. Historical snapshots are separate attempts; the current draft cannot overwrite their
  identity or result. An operation-only query may locate a task while its Gateway reverse lookup
  is incomplete; that does not prove the draft never existed.
- Invocation and request key are distinct fields. In `.41`, `op3` passes through; legacy invocation
  maps through the pinned canonical digest or an existing alias/head record. Read those records and
  validate owner/app/command binding; never call `MailboxClient.requestKey()`, which can allocate an
  alias/epoch. Missing or conflicting mapping is `legacy_identity_mapping_missing`/`incomplete`.
  Hash the verified ledger request key for Host matching, not the unmapped Gateway invocation.
- Job → existing attempt/generation. Existing v1 job logs lack owner binding, so they are not joined
  by job ID/attempt alone (`job_log_owner_binding_missing`); operation queries can read exact persisted
  collection invocation history separately. A pre-call failure
  has no task only when the existing boundary evidence proves it; absence of a task row alone is
  insufficient. D09's absent analysis-attempt index is reported, never synthesized from nearby logs.
- Host requires the exact task ID plus SHA256 of the UTF-8 request key from S2. Gateway draft
  diagnostics use SHA256 of owner + NUL + draft ID. Hashes assist correlation, not authorization.
  Other-owner/principal rows and malformed identities are excluded.
  Also verify the Gateway owner against the task's existing `request.arguments.owner_scope` or
  equivalent retained binding; a shared ledger owner alone does not prove product-owner isolation.
  The request scope is SHA256(Gateway owner). `.41` send lacks that field: its fallback requires a
  matching owner-scoped persisted receipt with exact task, terminal revision and result digest.
  A present mismatched scope cannot use this fallback.
- Use task sequence, receipt revision and explicit causal relations. Duplicate events are
  deduplicated by their original identity/revision; conflicting duplicates and out-of-order gaps
  remain visible. Never join by timestamp proximity or subtract cross-host clocks for duration.
- `first_safe_cause` means the earliest proved cause on that exact attempt's causal chain. If
  competing causes cannot be ordered, show candidates and `incomplete`, not an invented first cause.
  A Controller/extension code proves its reported boundary, not the provider/network root cause.

### 7.4 Output contract (local report v1)

| Field group | Meaning |
|---|---|
| Query/provenance | Remapped owner-scoped references, source kind/schema, read interval and observation provenance; no private path, credentials or raw query text in ordinary output. |
| Versions | `expected` from release/config manifests separately from `observed_loaded` supported by retained task/run/process evidence. Installed files or a `.41` pin alone do not prove what executed; unavailable observed versions are incomplete. |
| Progress/cause | Last proved boundary per attempt; first safe code/stage/source or unordered candidates; every claim points to its exact source evidence reference. |
| Outcomes | Separate execution state, persisted business state, observed business result, effect certainty, ACK state and cleanup state. HTTP/worker success is not sent; an ACK failure is not a new send failure. |
| Evidence | `incomplete` with fixed missing/conflict reasons per boundary, instrumented coverage and source cutoffs. `present` means only the named evidence was found, never complete end-to-end history. Health transition counters are process-local snapshots, not final totals or continuity across restart. |
| Recovery | Descriptive eligibility based only on valid existing proof: pending rejected-result ACK may be eligible for existing maintenance; unknown may be inspected without resend. Report invocation performs neither action. |

Example interpretation: durable business `failed/not_sent`, execution `failed` if proved by its
task, ACK `pending`, safe cause `browser_extension_unavailable` at ACK, evidence `incomplete` where
Host/version events are missing. This is not proof that sending failed because of the extension:
the cause is attached to ACK. The report does not flatten this into one success/failure flag.

No addresses, subjects, bodies, filenames, raw URLs, credential/session values, provider payloads
or raw provider IDs enter output. Private source content is filtered in the reader before
aggregation; unsafe arbitrary IDs are remapped, never copied into error text. Existing private raw
artifact access remains separate. No permanent diagnostic store or automatic export is added.

### 7.5 Test-first acceptance and change card

Following owner confirmation, failing fixtures were added before implementation to prove:

1. The S1 rejection/failed-ACK/restart fixture is reported as durable rejection plus pending ACK;
   repeated queries leave ACK count, dispatch count, operation identity and unknown fences unchanged.
2. Before/after domain records, ledger rows/events/epoch/authority marker and DB/WAL/shared-memory
   bytes and source file inventory are unchanged in quiescent fixtures. File modes/content stay
   unchanged; access-time changes from OS reads are not business mutation. Write-denying fixtures
   and mutation spies reject all source writes, including constructor side effects. Exercise active
   WAL reads separately; unsupported write-free access must fail incomplete rather than repair it.
3. Memory fixture, default file and isolated PostgreSQL produce equivalent safe evidence; principal/
   owner mismatch cannot join; concurrent attempts, restart, duplicates/conflicts and out-of-order
   events cannot lend each other a cause or manufacture completion.
   Cover direct `op3`, legacy digest, existing alias, missing/conflicting alias and shared ledger-owner
   fixtures; query must not allocate or delete an alias/head.
4. Disabled/full/broken/missing/expired logs, unknown schema, encrypted file, oversized input,
   query timeout and missing actual loaded version produce explicit incomplete reasons. Safe cause
   allowlists and malicious payload fixtures prove privacy; all limits hold under oversized values.
5. Expected/observed versions remain separate, read does not instantiate App-CLI/Store runtime,
   and original public errors, retry/backoff, unknown protection and UI sources stay unchanged.

**S4 implementation card:** see [mail change card, section 10](email-diagnostics-and-change-control.md#10-s4--minimal-m3-change-card--source_verified). Source baseline `3793bfac` retained the approved design; the implementation is committed separately. D05/D09 and S5 remain outside scope.

### 7.6 Local use and evidence limits

From the repository root, using the existing Go toolchain and Node 26 on PATH:

```sh
cd services/gateway
go run ./cmd/sparkclaw-email-diagnose --owner OWNER --draft DRAFT --snapshot /existing/state.json --format json
```

Replace `--snapshot` with `--postgres-env EXISTING_DSN_ENV` for an existing PostgreSQL connection;
only SELECT-capable credentials are needed. Use `--operation KEY --identity-kind invocation|request-key`
or `--job JOB` instead of `--draft`. Optional retained inputs: repeated `--log /existing/events.log`,
`--release-manifest ../../vendor/app-cli/release.json`, and `--ledger /existing/ledger.sqlite`
with `--ledger-principal`, `--ledger-owner` and, for legacy identity, `--index-directory`.
No source is downloaded or initialized. The operator chooses existing files; paths/passwords do not
appear in report output. Exit 0 means a bounded report was produced, not mail success or full evidence;
2 is invalid input, 1 is a command/output failure. Timeout returns an incomplete report when writable.

The v1 report always declares `evidence=incomplete` and instrumented-boundary coverage. It has no
trustworthy retained loaded-version schema, cleanup history, trace/run link or analysis attempt index;
those fields remain incomplete. An expected `.41` manifest never proves installation. Encrypted
snapshots, missing/expired history and unsupported owner mapping are explicit gaps. No provider
verification, live-memory endpoint, new audit store, recovery action or S5 acceptance is included.
