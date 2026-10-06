# Workbench convergence implementation

> Language: English | [简体中文](../zh-cn/docs/workbench-convergence-implementation.md)

Date: 2026-10-06. Implementation branch: `codex/workbench-runtime-convergence`.
Starting commit: `9b92968d`. This record covers source and isolated verification,
not a running-service upgrade. The [design](workbench-runtime-convergence-design.md)
defines the confirmed product behavior.

## Phase 0: baseline and boundaries

All eight design/architecture/index documents exactly matched the source checkout
before editing, including section 6.1. The source checkout remains untouched.
InfiniCenter was found on the known Linux host at `/home/infinimesh/InfiniCenter`.
Registry, empty inbox, central C0001, proposed 0031, and the accepted JingSi,
App-CLI and IMMS contracts were reviewed. The implementation review is appended
to 0031; it remains proposed. No external protocol or retention change is accepted
by this internal plan.

| Area | Starting behavior | Disposition |
|---|---|---|
| History | Host Store versus desktop SQLite/files | Preserve local ownership and authenticated scope; unify selection rules |
| Runtime | Desktop constructs a second ToolHub and policy | Share live providers, policy and registry; isolate repositories and resource adapters |
| Context | Host typed history versus desktop 32-message, 96 KiB envelope | Separate transport bounds from common model-context selection |
| Host resources | Host workspace and acquisition browser versus explicitly bound desktop Host | Preserve resource authorization; no implicit access to another workbench |
| Delivery | Host result persistence versus desktop verified files and ACK | Preserve durable completion and query-only recovery; no retry of uncertain writes |
| Schedules | Host overdue scan; desktop renewable backend lease | Explicit behavior change: local occurrence claims, permanently missed offline rounds |
| Mail | Backend authoritative; desktop cache | Preserve one collector and service authority |
| External callers | JingSi durable results/events, App-CLI journals, IMMS evidence | Unchanged frozen contracts and retention; no reset/import |

Baseline environment: macOS ARM64, Go 1.25.12, Node 26.2.0/npm 11; isolated
Linux ARM64 Go 1.25.5/Node 26.2.0. Declared document dependencies were installed
with `npm run setup:document-tools` before tests on both hosts.

| Baseline check | Result |
|---|---|
| Mac Go build | Passed |
| Mac Go full tests | Existing failures in Gateway, cmd/sparkclaw and browser-host tests: canonical temporary-path guards and Linux `/dev/shm` requirement; remaining packages passed |
| Linux Go build / vet / full tests | Passed in a fresh isolated source snapshot, with central JingSi conformance manifest |
| Desktop | 102 passed, no skips |
| WebChat | 196 passed; TypeScript/Vite build passed |
| Bilingual docs/local links | 103 English documents checked after phase-1 guide updates |

Mac logs are under ignored `.cache/convergence/`; Linux baseline logs and snapshot
are under `/home/infinimesh/.cache/sparkclaw-convergence-20261006-DIrlhW`.
Tests use isolated files and do not connect to production business services.

## Phase 1: current guides

The README, architecture/index, Store and WebChat guides now distinguish host
workbench persistence from execution content. The Mac guide uses `main`, points
to dated native evidence, and no longer describes already-delivered facilities
as unimplemented. Existing hardware acceptance gaps remain explicit.

## Common context admission

`configs/workbench-limits.json` is the limit source; the checked generator projects
constants into Go and the packaged desktop client. Both workbenches accept up to
64 KiB UTF-8 owner input; submitted context is at most 1 MiB/32 messages. The model
context selector uses the same last-eight conversation rule and UTF-8 message
bounds for host and submitted repositories. Active model token admission remains
authoritative: these byte caps do not promise that every 64 KiB input fits the
configured embedding/guard models. The larger transport allowance removes the
old desktop-only 16 KiB bottleneck and leaves room for the selected history.
Result/file and 24-hour delivery limits are unchanged.

Focused tests compare host FileStore and submitted MemoryStore selection, reject
cross-conversation history, verify UTF-8 boundaries and equal 64 KiB HTTP/envelope
admission, and check generated contract freshness. Go build/vet plus agent,
workbench and execution packages passed; all 116 desktop tests passed after the
new limits and scheduler integration.

## Shared composition and workbench behavior

| Area | Implemented result | Representative changes |
|---|---|---|
| Runtime | `agent.WithExecutionScope` derives repositories/resources from the live runtime; active providers, policy and registry stay shared | `3c488fb5`, `59a65de9` |
| Context | One selected-history rule and generated admission limits; snapshot retained for the admitted host request | `980d2801`, `5f15a34d` |
| Drafts | Shared revision queue with host Store/desktop SQLite adapters; restore text/file references, explicit conflict recovery, identity and StrictMode guards | `7c1520a1`, `d5893e44`, `ae0338b0`, `6c253d0f`, `73547c98`, `c38e86f2` |
| Submission | Stable request identity, persisted input before draft clear/acceptance, original-ID lookup/list/cancel; unknown never replays, including lookup 404 | `faf7551d`, `5f15a34d` |
| Schedules | Desktop local schema 6 and host Memory/File/PostgreSQL definitions/occurrences; atomic claims, permanent offline misses, future recurring rounds retained | `282e1cab`, `46a94cd4`, `9d5c20e4` |
| Backpressure | Bounded production dispatch observes scheduler pulses while workers are full; 105-task regression distinguishes a healthy backlog from an actual availability gap | `ac2c70f9` |
| Read-only recovery | Installed execution status/files, mail authorization and ordinary browser fence GETs read private snapshots without initializing writable services or pruning | `00203a23`, `1547c2ed` |

The scoped ToolHub has the same registry and live providers, not an unrestricted
union of tools. Durable owner memory/schedules, sandbox workspaces, acquisition
browser and external connector ledgers need explicit resource bindings; absent
bindings return `resource_unavailable`. Submitted execution keeps temporary
repositories and Linux memory-backed files. Host workbench history stays in its
own Store, including with PostgreSQL; it is not copied into execution control.

Host draft clear is a CAS step after durable admission and input persistence,
not a transaction across the Store and execution control file. A crash between
those steps leaves an original request to reconcile, without silently losing the
user's text or authorizing another submission. Desktop enqueue/clear is one local
SQLite transaction. Neither local saving nor reconnection submits a draft.

## Neutral names and matched release

Mechanical rename `014b0132` is separate from behavioral wire/storage cutover
`9e1c3fbe`. The product now uses `internal/execution`, `internal/browserhost`,
`internal/mailsync`, `/api/v1` resources and `X-SparkClaw-Digest`. Browser host
transport is `/api/v1/browser/hosts`; desktop storage is `userData/workbench`;
backend control roots are `State.Path + ".execution"` and `State.Path + ".mailsync"`.
Retired routes return 404 and the retired digest cannot submit an execution.
A regression plants invalid historical control data and verifies that the new
binding/execution succeeds without opening, copying or modifying the old data.

There is no path alias, old-device support or schema import. The [release guide](workbench-release.md)
requires a matched backend/WebChat/desktop build and fresh selected storage and
enrollment. Within that release, normal restart reopens saved history, drafts,
files and schedules. Source main and running installations have not been changed.

## Reviewed retained revision and historical references

| References kept | Reason |
|---|---|
| `docs/client-r3-implementation.md`, `docs/macos-r3-acceptance.md`, `docs/macos-r3-dual-host-acceptance.md` and Chinese mirrors | Dated source/deployment/hardware evidence; hashes, old commands and acceptance IDs retain their historical meaning |
| Client/backend, Mac, desktop embedded and shared-backend design documents; this design's baseline inventory and mapping tables | Original design revisions explicitly linked to current architecture/release guidance |
| Workflow/profile revision 3, `workflow_registry_test.go`, `workflow-capabilities.md`, external MCP and finance designs | Actual protocol/Workflow revisions, not a product namespace |
| App-CLI contracts/projections/vendor assets and JingSi/IMMS ledgers | Frozen external interfaces and retention obligations; untouched by internal cutover |
| `gateway/workbench_cutover_test.go`, `gateway/retired_schedule_routes_test.go` | Negative proof that retired routes/header/storage do not resume execution |
| Store test run ID `r3` | Unrelated fixture identifier, not a storage or transport path |

The audit targets active production names, not a zero-match search that would
corrupt real revision identifiers or historical evidence.

## Final source verification and delivery

Executable source revision: `bdcd5f6a081eb7f5761d325006efb96be154b981`. Later closeout commits only update this
record. Shared approval/login cleanup in `e84fb5be` and `bdcd5f6a` now retires only
definitively closed authority, preserves fresh input and session serialization,
and records a known delivery failure distinctly from an unknown outcome. An
independent review found no additional blockers and reran the new regressions.

| Check | Result |
|---|---|
| macOS Go build/vet and portable focused recovery/cutover tests | PASS; installed-execution tmpfs tests still require Linux, as in the baseline |
| Fresh Linux ARM64 Go build/vet/full suite | PASS, all 63 package result lines; default File/Memory and real disposable PostgreSQL 17 enabled |
| Linux race checks | PASS: cmd/sparkclaw, gateway, agent, toolhub, policy, execution, browserhost, mailsync, reminder, messagecontrol and store; PostgreSQL enabled |
| Default File/mock golden | PASS: 47 cases and extended checks in a separate fresh snapshot, synthetic ports 28889/28891 |
| Desktop unit/contract tests | PASS: 122, zero skips |
| WebChat tests/build | PASS: 218 tests in 52 files; TypeScript/Vite production build |
| Generated contracts/preload | PASS: common workbench limits and managed scripts |
| Mac and Linux outbound host native fixtures | PASS: Electron 44.4.3 real embedded pages, click/fill, 20 switches, scoped page ownership and lost-response write fencing |
| Mac restart fixture | PASS: three fresh Electron processes, real safeStorage, drafts/file references and recurring schedules restored; offline due occurrence missed, future kept, zero execution POSTs or lease calls |
| Mac ARM64 packaging | PASS: unsigned DMG/ZIP, public client allowlist audit (66 application entries, 5 UI files), DMG checksum verification |
| Frozen Linux UDS qualification | BLOCKED by host sandbox environment, after dependencies and UI build; not counted as passed |
| Bilingual docs/local links | PASS: 108 English project Markdown files and mirrors |
| Team-Skills lesson validation | PASS: 9 skills, zero errors; 20 pre-existing dated-status/pending-lesson warnings |

The desktop/WebChat executable trees are unchanged since their complete tests at
`9e1c3fbe`; native fixtures and packaging were rerun at the executable source
revision above. Mac package SHA-256 values:

| Artifact | SHA-256 |
|---|---|
| `apps/desktop/dist/SparkX-0.1.0-mac-arm64.dmg` | `31a51f0bbd66a5a4d0b0dcc91c86439b5beb979d99c64e121426ab7ce492fb54` |
| `apps/desktop/dist/SparkX-0.1.0-mac-arm64.zip` | `a3bbf3fed97ee0601e450197e637b064b79879f8392a0de238b126761327c592` |

The frozen UDS runner cannot start its sandboxed Electron: the isolated npm
`chrome-sandbox` is user-owned mode 0755, and AppArmor denies unprivileged user
namespaces. No sandbox restriction was disabled or host security configuration
changed. The separate outbound-host runner's Linux fixture uses its existing
`--no-sandbox` test option; its PASS establishes transport/page semantics, not
sandbox qualification. See `host-review-native-uds-stderr.log` for the captured
SIGTRAP and setup diagnostics.

These checks do not establish real OS sleep/wake, signed/notarized packages,
installed upgrade behavior, live model/Info/mail provider acceptance, x64 Mac or
full M01–M12 hardware acceptance. Shared active-provider behavior is covered by
unit/integration tests; the golden suite uses mock models. Actual production
cutover remains a separate authorized operation under the release guide.

Local logs and packages remain in this primary worktree's ignored
`.cache/convergence/` and `apps/desktop/dist/`. Linux evidence and isolated source
snapshots remain under `/home/infinimesh/.cache/sparkclaw-convergence-20261006-DIrlhW`,
with `release-*` logs for final verification. Only disposable test containers and
fixtures were stopped; production services, real profiles and source main were
not modified. No push, main merge, deployment, installation or real-data deletion
was performed. InfiniCenter status and the pending reusable lesson record the
same source-only boundary; external consumers need no code changes.
