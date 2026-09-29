# Application Control Extensions Built on App-CLI

> Language: English | [简体中文](../zh-cn/docs/app-cli-email-extraction-design.md)

- Status: **R3 implementation in progress; first public lifecycle core delivered; mail migration incomplete**
- Date: 2026-09-29
- Initial applications: QQ Mail, Gmail, Outlook
- SparkClaw inspection baseline: `6f4c97e4d0d7e03fab2de48661ddd6aed3034cd9`
- App-CLI inspection baseline: `c9e49acd486fcbbbed0c0c3f398d6c7041a5a66f`

This replaces the earlier proposal to extract mail implementations through direct npm imports. The objective is a sustainable application control system built on App-CLI's existing architecture. Mail validates the first complete integration. The target architecture determines implementation scope; reducing changed files does not justify two public registries or bypassing App-CLI's execution entry point.

The user has authorized coding. This fork now implements the public lifecycle core and explicit Runtime v2 client transport, with scope recorded in section 14; upstream has not merged these changes. The resident service, BrowserHostPort, mail adapters and production deployment remain subsequent work, not capabilities established by this design.

## 1. Architecture Decisions

1. **Python CLI / Registry remains the sole public command entry point and capability catalog.** Applications use the existing Manifest, Adapter, Registry, Schema validation and TaskResult abstractions.
2. **JavaScript is an execution backend.** Existing mail implementations run through RuntimeAdapter. There is no parallel public JavaScript registration framework.
3. **New capabilities extend App-CLI's formal contracts.** Authorized mutations, asynchronous operations and task control follow a backend-independent lifecycle extension, first implemented by RuntimeAdapter / Runtime v2, without an npm bypass around Registry admission.
4. **SparkClaw retains the generic browser host; its application business layer only invokes public commands.** App-CLI owns application control logic. SparkClaw retains task-page ownership, profiles, Browser Controller / Bridge and browser infrastructure, without provider scripts, page protocols or mail operation executors.
5. **The executor owns operation tasks; SparkClaw owns product tasks.** The former records admission, execution, evidence and recovery for an application operation. The latter owns user goals, approvals, synchronization, ingestion, model analysis and UI.
6. **Mail is one group of application adapters.** Future applications use the same public contracts. API, native and existing CLI adapters need no browser, mail modules or Node execution service.

## 2. Existing Architecture to Reuse

The baseline is App-CLI's [Architecture](https://github.com/Infinimesh-ai/App-CLI/blob/c9e49acd486fcbbbed0c0c3f398d6c7041a5a66f/docs/ARCHITECTURE.md), [Runtime](https://github.com/Infinimesh-ai/App-CLI/blob/c9e49acd486fcbbbed0c0c3f398d6c7041a5a66f/docs/RUNTIME.md) and corresponding source.

| Existing component | Retained responsibility | Required extension |
| --- | --- | --- |
| `cli.py` | apps / describe / schema, argument parsing, JSON output, exit codes | Explicitly versioned machine entry separating execution context from business arguments |
| `Registry` | Sole catalog, explicit registration, Manifest snapshots, input/output validation, dispatch | Trusted context and controlled mutation admission at the existing boundary; default denial remains |
| `Adapter` | Application Manifest and invocation boundary | Preserve old invoke; explicitly extend trusted context and common LifecycleAdapter capabilities |
| Manifest 1.0 | Application, commands, platforms, input/output schemas, effect category | Preserve format; use a separate versioned execution binding for deployment and host requirements |
| `RuntimeAdapter` | Trusted fixed executable, JSON exchange, response identity checks | Explicit v2 branch within the existing component |
| Runtime protocol 1.0 | One subprocess invocation, read-only execution, strict messages | Preserve v1; v2 adds admission, query, cancellation, reconciliation, events and authorization binding |
| `TaskResult` | completed / pending / running / waiting_confirmation / uncertain / failed / cancelled / blocked | Preserve all eight states and business data only for completed tasks |
| `builtin_adapters()` | Explicit composition of trusted applications | Reviewed registrations and installation profiles; no arbitrary plugin scanning/execution |

At the inspected baseline, both Registry and RuntimeAdapter reject mutations. Runtime v1 limits requests to 64 KiB and subprocess timeouts to 300 seconds. Existing mail accepts bodies up to 200 KiB, collect_page has an 1800-second budget, and observation must survive individual calls. **These require execution-contract extensions, not relabeling commands as read_only or changing one timeout.**

Keep the upstream separation of application semantics, access backend and transport: mail defines business behavior, the browser backend performs page operations, and Runtime transport reaches the executor. Applications using RuntimeAdapter declare `adapter.kind=runtime`; their browser backend is specified in the execution binding.

## 3. Target Call Chain and Runtime Topology

```text
SparkClaw Workflow / emailmanagement / other product capabilities
                         | business command + controlled context
                         v
App-CLI Python CLI -> Registry -> application Adapter
                         |
                RuntimeAdapter v2
                         | fixed Node client, one stdin/stdout exchange
                         v
App-CLI owner-local Executor
  operation ledger / command execution / validation / status and events
                         |
          QQ Mail / Gmail / Outlook / other implementations
                         | generic BrowserHostPort (browser backend)
                         v
SparkClaw Browser Controller / Desktop / Browser Bridge
  task-page ownership, leases, browser primitives, generic cleanup
                         |
                  actual application
```

### 3.1 Why a Resident Executor Is Needed

The design selects an App-CLI execution process isolated per local owner for long operations and observation. This implements the upstream boundary in which the executor owns durable tasks: exiting the CLI does not terminate observation, querying a task does not resend mail, and a 30-minute collection does not require a 30-minute Runtime v1 subprocess.

RuntimeAdapter still launches a fixed client for one bounded exchange. The client connects to a configured owner-local service; business arguments cannot select an executable or arbitrary endpoint. Deployment explicitly manages the service. Imports and apps / describe / schema never start it. An offline service returns an explicit unavailable result.

This service is a proposed new component. It manages invoked application operations, not another agent, workflow planner, mailbox synchronization scheduler or product database. Native/Python/ordinary CLI adapters keep their existing direct invocation paths.

### 3.2 Preventing Dependency Cycles

- SparkClaw product consumers depend only on public App-CLI commands and the machine protocol.
- App-CLI's browser backend depends only on BrowserHostPort, never SparkClaw source imports.
- The host handles generic requests within granted permissions; it does not re-enter product Workflow execution for the same command.
- Authorization providers are separate from browser primitives. Page content cannot grant additional authority.
- npm delivers executors, backends and JavaScript assets, without a public product-facing provider-handler shortcut.

### 3.3 Confirmed Product Boundary

The user adopted this division: App-CLI owns application control logic; SparkClaw owns browser pages and infrastructure. “Only upper-level calls” means product code no longer directly executes provider scripts, not removal of the browser subsystem. Browser Controller, Bridge, Desktop host integration and user-page protection remain in SparkClaw; App-CLI accesses them through the generic protocol. This work neither relocates browser infrastructure nor duplicates the production host. The independent reference host serves protocol and portability acceptance only.

## 4. One Capability Catalog and Application Extension Path

Each application has one Manifest and explicitly registered Adapter. Proposed initial IDs are `qq-mail`, `gmail` and `outlook`. A consumer compatibility mapping converts existing SparkClaw values such as `qq_mail`; that mapping contains no provider page logic.

Proposed commands include `account.inspect`, `messages.discover`, `messages.capture`, `threads.inspect`, `messages.mark-read`, `messages.send` and `mailbox.watch`. Existing collect_page can become a bounded collection command with a defined completion condition. The final supported inventory must match existing implementations and tests; these examples do not add unimplemented business features.

Target discovery examples, not currently available commands:

```text
app-cli apps
app-cli describe gmail
app-cli schema gmail messages.send
```

The extension path is:

1. Define business inputs, outputs, effects and completion evidence in a Manifest.
2. Select an existing Adapter / RuntimeAdapter and API, CLI or browser backend. Extend a shared backend contract only when existing primitives cannot express the requirement.
3. Add an execution binding and implementation when an external executor is required.
4. Add the application to the reviewed explicit registration set with application and conformance tests.

Adding an application supported by existing host capabilities should not change SparkClaw's provider/operation scheduling allowlists. New product entry points, UI or host primitives can require consumer work; this is not a promise that every application requires zero consumer changes.

### 4.1 Authoritative Sources

App-CLI owns public Manifests, Runtime schemas, execution bindings and conformance fixtures. Python registers from them. Node loads the same release, validates admission and dispatches through fixed handler mappings. Its internal mapping is not a second public catalog.

Generated Go types, documentation, error projections and host asset inventories record source versions and digests. Do not maintain manual duplicate definitions. Validation may run in multiple processes, but rules and fixtures must match. Specify duplicate JSON keys, non-finite numbers, unknown fields and cross-language precision limits instead of trusting default parsers.

## 5. Manifest and Execution Binding

Keep the closed Manifest 1.0 schema unchanged. Proposed `execution-binding/v1` binds the following to an application's Manifest digest:

| Field group | Content and constraints |
| --- | --- |
| Application identity | App ID, version and Manifest digest; mismatch blocks execution registration |
| Execution | Explicit Runtime protocol major, trusted executor type/release identity, backend kind |
| Command mapping | Public command to fixed internal handler; no caller-supplied script path |
| Arguments and budgets | Canonical argument digest rules, request/result sizes, business deadline, RPC budget |
| Host requirements | Host protocol/version, required primitives, page/account/session modes |
| Resources | Allowed origins, download origins, resource-lock key structure, shared/exclusive requirements |
| Effects | Local page/file changes, remote mutations, approved business effects and required evidence |
| Initialization assets | Versioned/digested Reader and pre-navigation scripts, execution worlds and phases |
| Compatibility | Supported application/backend versions and verified host combinations |

side_effect describes the entire operation. Page preparation or file writes cannot be hidden as read_only. Operations with remote changes use remote_mutation admission; the binding also describes local effects. A business name containing “read” does not determine effect classification.

Discovery reads installed local contracts without connecting to mail, checking login or opening pages. Environment/account preflight is a separate invocation; page-changing preflight needs the corresponding authorization.

## 6. Public Entry and Runtime v2

### 6.1 Entry and Context

Extend the Python CLI with an explicit `--machine` entry: one versioned stdin request, one stdout response. Existing named arguments, --input, discovery and v1 output retain their semantics. Machine input cannot be mixed with legacy arguments. Credentials, bodies and complete authorization material must not enter argv.

Registry gains optional trusted execution context. Existing callers without it keep their restrictions. Context-aware adapters explicitly implement the extension; Registry neither injects extra arguments into old invoke methods nor catches TypeError to guess compatibility.

The machine request needs at least these logical fields. Exact JSON schemas must be reviewed and frozen under decision 0030 before implementation.

| Field | Responsibility |
| --- | --- |
| protocol_version, operation | Explicit version and operation; no silent downgrade |
| app, command, arguments | Business identity and Manifest-validated arguments |
| request_key | Stable original-intent key scoped to authenticated principal/owner; never replaced after timeout |
| authorization_ref | Opaque reference verifiable by a trusted authorization provider, not a self-reported approval |
| deadline, limits | Fixed normal-task budgets that execution may only reduce; only an explicitly renewable watch may omit the total deadline, with finite authorization windows and resource leases |
| task_id, cursor | Control/query an original task; identifiers are not access grants |

Identity comes from an authenticated connection/authorization provider. Browser socket addresses, executables and filesystem roots come from trusted configuration or resolved grants, never business argument overrides.

### 6.2 Operation Set

| v2 operation | Behavior |
| --- | --- |
| invoke | Validate and durably admit an original intent; complete promptly or return a task ID |
| lookup | Locate admission by original request key without resubmitting |
| status | Query the task; completed business output must pass the original command schema |
| cancel | Request stopping; acknowledgement does not mean cancelled |
| resume | Supply refreshed authorization for the original waiting_confirmation / blocked task, then re-admit continuation; cannot change business arguments or replay an uncertain write |
| renew | Update the authorization window only for an active task declared renewable; bind the original task/intent/account scope, without changing arguments, reviving an expired task or extending a fixed business deadline |
| events | Bounded task-local sequence/cursor events without raw pages or secrets |
| reconcile | Check independent completion evidence for the same task, never create another business write |

All operations use the same public entry and authorization path and share one task-control API across applications. Event pages and cancellation acknowledgements are distinct from completion of the original task.

These semantics come from the common lifecycle contract; Runtime v2 is its cross-process mapping. Other reviewed adapters implementing that extension receive Registry lifecycle dispatch directly, without pretending to be runtime backends.

Machine responses distinguish successful RPC handling from business state. Valid pending/running outcomes may be successful control responses; only TaskResult determines business completion. A new Registry control interface returns full state while preserving existing execute / execute_with_metadata behavior that converts non-completion to AppCLIError. Stable failure reasons belong in v2 error metadata, without arbitrary fields added to legacy TaskResult. Query/cancel use separate task-access permissions: expired execution authority blocks new effects without preventing a legitimate owner from viewing or stopping the task.

### 6.3 Bounded Transport and Long Operations

- Runtime v1 keeps its 64 KiB request, 1 MiB response and timeout rules.
- Proposed v2 limits are 2 MiB per machine request and 1.5 MiB for serialized arguments. Existing per-entry body limits remain, up to 200 KiB. Budget for JSON escaping: body bytes are not serialized request bytes. Files and attachments use validated references rather than inline contents.
- Admission/control RPCs have a proposed 15-second limit and the fixed client a 30-second subprocess budget. Business deadlines are separate; collect_page can retain its authorized 1800 seconds. Poll the original ID rather than blocking RPCs until business completion.
- Each v2 response is limited to 1 MiB. Bound stdin/stdout/socket reads while reading, rather than checking only after unbounded capture.
- The executor stores its ledger/logs in owner-private directories. Persist only necessary sending content with private permissions and redacted diagnostics. Expired credential references block continuation without expanding recovery privileges.

These proposed limits require multibyte, escaping and real fixture boundary validation. Do not reduce an existing business input range without recording the compatibility change.

## 7. Mutation Admission and Ownership

### 7.1 Extending Default Denial Formally

Default mutation denial remains. New commands execute only when:

1. The explicitly registered, trusted Adapter implements the conformance-verified common lifecycle extension, called `LifecycleAdapter` here, with matching contract/release identity. Registry checks extension capabilities and reviewed registration, not the RuntimeAdapter class name or adapter.kind.
2. Registry policy resolves trusted authorization bound to the caller, app/command, canonical argument digest, account/resource scope, deadline and request key.
3. Its execution backend validates that binding and durably admits the operation, rechecking execution authority and permissions at resource acquisition and before effects. Executor / Browser Host performs this for browser backends; other backends have equivalent obligations.

`LifecycleAdapter` explicitly extends the existing Adapter without another Registry. The shared contract covers trusted context, durable intent/task binding, target serialization, stale-execution exclusion, deadlines, cancellation, authorization resumption, completion evidence and reconciliation. Persistent observation additionally declares renew/events capabilities. A self-reported capability flag is insufficient; the reviewed registration set binds implementation, version and conformance evidence.

RuntimeAdapter v2 is the first production implementation; Node Executor is the first execution backend for browser applications. Future native-api / http-api / cli adapters may implement the same contract in Python or their own controlled backend, reusing Registry policy without Node, BrowserHostPort or changing their kind to runtime. A one-shot Python invocation must not leave background work without an owner; asynchronous tasks require a durable backend. Existing read-only adapters need no mandatory retrofit.

--allow-write, approved:true, changing adapter.kind or directly importing a handler does not constitute admission. Missing context, expired authorization, legacy v1 Runtime and adapters without lifecycle support continue to reject mutations.

SparkClaw projects existing user approvals and policy into constrained authorization references. Page content, model-produced arguments and error text are not authority. Other products can implement the same provider contract; App-CLI does not read SparkClaw's internal database.

Distinguish effects: existing policy may grant routine local page preparation within an authorized account/task scope, while remote sends remain bound to exact approved recipients and content. Correct local_mutation classification does not require a new human prompt for every read. Authenticate the actual principal across container/host connections; access to a shared socket is not blanket authority over all owners.

### 7.2 Avoiding Duplicate State Ownership

| State/resource | Authority | What the other party may retain |
| --- | --- | --- |
| User goals, approval, synchronization jobs, analysis/ingestion | SparkClaw | App-CLI receives only necessary arguments and authority |
| Operation tasks, request-key binding, attempts, events, receipts | App-CLI Executor | SparkClaw retains task ID, last observed state and result references |
| Task pages, profiles, host generation, locks, process cleanup | Browser Host | Executor holds revocable limited leases |
| Provider send journal, capture format, interpretation of completion evidence | App-CLI application | SparkClaw validates public results/artifacts without another recovery interpreter |
| Artifact roots, access policy, retention/deletion | Host/product configuration | Executor accesses only granted task space |

The generic ledger records execution identity/state; the provider journal records submission/evidence steps, both managed and linked by one executor. The two products must not independently decide to resend. Page cleanup does not delete the durable ledger or send journal.

## 8. Tasks, Timeouts, Cancellation and Reconciliation

Persist request_key -> canonical intent digest -> task_id before queueing or acquiring pages. Same key and intent returns the original task; changed semantics produce a conflict. Consumers retrieve the original operation instead of creating a second attempt.

Keys are scoped by principal/owner. Content and artifacts may expire, but retain minimal deduplication records or tombstones. Expiration cannot make an old key new again. Lost/corrupt ledgers stop affected writes; an empty replacement ledger is not proof of non-admission. Freeze retention periods, event capacities and storage quotas with the protocol.

| Situation | Result and next action |
| --- | --- |
| Input/authorization/compatibility rejected before admission | Explicit rejection, no execution; distinct from transport failure |
| Admission response lost before receiving task ID | Error retains request key and requires lookup; never fabricate uncertain TaskResult without an ID |
| lookup finds original task | Return its ID; continue status/events |
| lookup cannot establish historical admission | Remain unresolved; no new invoke. Only durable proof of non-admission can close the request |
| Admitted and queued/executing | pending / running with task ID, no successful data |
| Approval or refreshed authorization needed | waiting_confirmation before the effect, without creating another intent |
| Effect may have occurred but outcome is unknown | uncertain; preserve task/journal and reconcile evidence |
| Cancellation requested | ACK means stop intent recorded; determine state after host stop/cleanup |
| Cancellation/deadline after possible submission | uncertain until independent evidence; do not claim cancelled or unsent |
| Completion evidence precedes cancellation | Keep completed and its receipt immutable |
| Business work completed and output valid | completed plus original-schema data; ingestion/analysis is a later SparkClaw stage |

Reuse the eight TaskResult states without a competing success boolean. Known deadline failures use failed with a stable reason; unknown effects use uncertain. On restart, tasks proven not started may be scheduled after fresh authorization checks; potentially executed tasks reconcile first. Never replay all invokes indiscriminately.

mailbox.watch remains running while active and delivers bounded events. It becomes cancelled after cancellation and cleanup. Receiving an ID is not observation completion. SparkClaw's synchronization scheduler continues to consume hints; the executor does not independently scan the full mailbox periodically.

Events may be delivered at least once; consumers deduplicate by task/sequence. Compaction, overflow or expired cursors must report an explicit gap. SparkClaw reconciles through its existing synchronization mechanism rather than interpreting missing events as no new mail. Event/log/result retention must not break request-key deduplication.

### 8.1 Executor Handoff and Stale-Execution Exclusion

The initial deployment is single-host with one effective executor per owner; it adds no multi-host election. Execution authority combines an exclusive process lock on the same owner state directory with a durable, monotonically increasing `execution_epoch`. PID, socket files or start times alone are insufficient. Process exit may release the lock while descendants or dispatched page actions survive.

Handoff order is fixed:

1. Acquire the exclusive lock. If the previous executor still owns it, return busy; never delete the lock file to run concurrently.
2. Atomically allocate and persist a new execution_epoch in the original state, retaining installation identity, request keys and journals. Unverifiable state blocks takeover.
3. Handshake with Host. Host durably records the effective epoch for that owner/installation identity and atomically revokes old-epoch leases and queued actions. Calls, events and task-state commits check generation. The epoch itself is not an authorization credential.
4. Host stops old observation hooks, controlled descendants and related page actions. Revocation cannot recall dispatched remote requests: preserve evidence, mark uncertain original outcomes accordingly and reconcile first. Unproven cleanup fences affected resources against new work on conflicting targets.
5. After isolating old execution, recover qualified original tasks and admit new work. Recovery creates no new sending intent. Old-epoch results cannot overwrite the active ledger but may be attributed reconciliation evidence.

Host restart advances its own host generation, invalidating prior leases; new leases require a fresh handshake and checks of surviving task pages. Software rollback never rolls back execution_epoch or deletes authority records. Stale ledger backups or Host/ledger generation conflicts require explicit recovery, not changing installation IDs and treating the system as new. Other LifecycleAdapter backends must provide equivalent exclusion: inability to stop old execution blocks new execution, without an exactly-once claim.

### 8.2 Persistent Observation, Renewal and Disconnection

Distinguish business deadline, execution authorization window and Host resource lease. Normal command deadlines cannot be extended. Only a watch explicitly declared renewable in its binding may omit a fixed total lifetime; every authorization window and resource lease remains finite. Continuous observation is not indefinite authority.

Initial proposed defaults are release configuration and contract-test inputs, to be frozen in phase 1: Executor heartbeats to Host every 10 seconds, resource leases at most 30 seconds, watch authorization windows at most 5 minutes, and SparkClaw requests renewal 60 seconds before expiry. Host uses a monotonic clock for local lease duration, capped by authority expiry and any fixed deadline. Absolute authorization expiry follows the authorization-provider contract.

| Event | Required behavior |
| --- | --- |
| Normal renewal | SparkClaw checks that observation remains enabled and account/permissions remain valid, then submits fresh authority through public renew, bound to the original task, argument digest and scope; Executor / Host verify before updating the window |
| Executor heartbeat | Establishes liveness of the current execution_epoch only; maintains Host leases within existing authority without creating grants or extending deadlines |
| Lost renew response | Query the effective window by original task and authorization version; do not assume extension or create a second watch |
| Execution authority expires | Stop new page collection/event forwarding, clean up observation hooks and leave the original task waiting_confirmation; independent task-access authority still permits query/cancel, and fresh authorization uses resume |
| Credential generation changes or authority is revoked | On notification or detection, immediately revoke affected activity leases without waiting for heartbeat grace; credential changes require revalidation before resume. Explicit product disablement records cancellation and triggers cleanup |
| Executor / Host connection fails | Accept no new actions; lease expiry triggers Host cleanup and resource fencing at the latest. Observable task state is blocked with a reason; when state is inaccessible, report unreachable rather than assuming stopped |
| Reconnection | The same epoch may continue after validation within a live lease. Expired leases require cleanup, reacquisition and page validation; never revive the old lease. New epochs follow section 8.1 |
| Recovery budget exhausted | Proposed initial reconnect budget is 60 seconds from fault detection, never permission to continue beyond lease/authority expiry. Proven stopped work becomes failed; unknown outcomes or incomplete cleanup remain queryable blocked/uncertain with resource fencing, not fabricated terminal states |

Evidence resolves concurrent failures: established completed outcomes remain immutable; possible remote effects take precedence as uncertain; unproven cleanup or stale-execution exclusion remains blocked with resource fencing. Only proven stopping or exclusion permits waiting_confirmation, failed or cancelled according to the cause. Expired authority or a cancellation ACK alone does not establish stopped execution.

Authorization versions prevent stale renewals overwriting newer windows. Active watches use renew; waiting_confirmation / blocked tasks use resume. cancelled / completed / failed tasks are not revived. If observation remains enabled after a terminal outcome, SparkClaw explicitly starts a new observation session, linking the prior task and reconciliation boundary. This applies only to observation, never automatic repetition of remote writes.

Every pause, page reacquisition or generation change records an event gap and last durable cursor. On recovery, SparkClaw reconciles using existing synchronization watermarks/lookback before consuming further hints. Reads and watches on a shared page hold separate activity authority and lease references. Expiring a watch revokes only that activity; retain the page when other activity remains valid and cleanup succeeds. Cleanup that cannot be isolated fences the whole task page and notifies affected tasks, without closing user pages. Cleanup must not depend on SparkClaw or Executor staying online: Host must enforce lease expiry autonomously.

Expiry enforcement must also reach the controlled components actually holding resources: page observation hooks, Bridge/preload and workers need lease watchdogs or verifiable supervised termination, not merely an in-memory Controller timer. Controller crash or suspension must not allow indefinite stale work. Dispatched remote requests still require uncertain-outcome handling/reconciliation; expiry cannot recall them. A backend unable to prove bounded exclusion cannot pass persistent-observation acceptance.

## 9. Browser Host Protocol and Task-Page Ordering

BrowserHostPort is a generic versioned owner-local protocol accepting authenticated, limited executor requests bound to owner/installation identity, execution_epoch, task, resource scope, release identity, host generation and lease. It exposes neither arbitrary owner-tab enumeration nor the entire browser controller.

### 9.1 Public Primitives

- capabilities: Host protocol, supported primitives, browser/Bridge/preload capabilities and release digests.
- acquire: obtain a host-owned task page under granted resource declarations; return opaque lease/generation.
- handshake / heartbeat / renew_lease: validate execution authority and host generation and maintain bounded activity leases within verified grants; these cannot replace public renew to obtain new authority.
- install_assets: install trusted fixed asset IDs/digests at declared phases, without arbitrary source URLs.
- navigate / read / input / execute / download: lease-scoped operations bounded by origins, output, roots and budgets.
- subscribe: bounded page events with document identity, sequence and gap indicators.
- effect: validate exact approval and live authority before execution, recording attempted effects; the adapter verifies business completion.
- park / release / revoke: host-controlled retention, release, revocation and fencing after failed cleanup.

These responsibilities reuse Controller/Bridge implementations. Host implements generic primitives; App-CLI release assets supply provider initialization and behavior. Scripts are reviewed trusted code, not an OS sandbox created by npm or a restricted object interface.

### 9.2 Fixed Ordering

| Phase | App-CLI Executor | SparkClaw Browser Host |
| --- | --- | --- |
| 0. Admission | Registry/executor check command, authority, version and task binding | Check capabilities and release compatibility |
| 1. Declaration | Read resources/initialization assets from binding | No navigation or provider code yet |
| 2. Acquire | Request lease using task/grant | Create/match task page, preserving owner-page isolation |
| 3. Before navigation | Select registered assets and phases | Install dormant observer, background preparation and Outlook early bridge |
| 4. Navigation | Select allowed entry and readiness conditions | Navigate with Reader document-start and correct execution world |
| 5. Preparation | Account/Reader checks and resetRound | Validate document/generation; reject stale lease |
| 6. Execution | Handler, journal, completion verification | Limited browser actions and effect gate |
| 7. Retain/end | Declare continued watch or task end | Decide sharing/park/release and perform final cleanup |

Reused pages can skip creation/navigation but not account, document, credential-generation and release-digest checks. The host creates/destroys pages; the executor cannot launch another browser or close user pages.

### 9.3 Existing Behavior to Preserve

- Keep Chromium/Playwright, Browser Bridge, profiles and Electron preload execution environments.
- QQ/Gmail bounded native reads and Outlook UI waits use different completion strategies, declared by application bindings and executed through generic host mechanisms.
- Reads and observation continue sharing qualified pages in either startup order; ending a read must not close an observation page.
- Preserve account/owner/credential-generation, document nonce and script/observer digests; revoke stale leases.
- Preserve controlled ordering for page close, CLI stop, daemon cleanup and temporary state removal. Failed resources remain unavailable until successfully cleaned up.
- Changed code/asset digests require draining old execution and rebuilding background pages; no zero-interruption hot-swap promise.

## 10. Code Ownership and Consumer Changes

| Current location | Target responsibility |
| --- | --- |
| scripts/email, Reader build sources | App-CLI application implementation, backend and asset build |
| provider-scripts.mjs | Business definitions in Manifest/binding; fixed handler mapping in Executor |
| mail-notification-rules.mjs, mail-observer-page.mjs | Application notification parsing and assets |
| mail-observer-runtime.cjs, awaited-mail-read.cjs | Provider policy in App-CLI; generic hooks/install points in Host |
| cli-task.mjs | Move application bridge, provider waits and queries; retain generic Playwright execution in Host |
| controller.mjs | Replace three-provider/mail-operation hardcoding with admitted binding-driven resource allocation, without removing admission controls |
| cli-runtime.mjs | Move mail secret-field definitions/input transforms; retain generic process and secret-slot mechanisms |
| cli-client.mjs | Move app dispatch, Reader mapping and mail flow; retain generic connection/call/resource delivery |
| mail-observers.mjs, mail-read-pool.mjs, mail-observer-feed.mjs | Observation semantics in Executor; generic leases, resource sharing, event transport and backpressure in Host |
| tools/browser-userscripts/*-mail-reader.user.js | App-CLI release assets; no independently editable consumer copies |
| Go emailautomation execution integration | App-CLI machine client/result mapping; retain product approval/account/error mapping |
| provider_scripts.json, source-scanned error tests | Generate projections from Manifest/binding/fixtures rather than old script paths |
| Browser component / Desktop preload build | Consume matching release assets; host-generated artifacts may remain |
| Gateway Dockerfile, host setup, CI | Install/configure CLI, fixed client and service; remove old source-path assumptions |
| emailmanagement, Store, model analysis, WebChat | Retain product duties; adapt task-ID binding/event consumption |

Existing Go RunScript is an internal compatibility surface that may temporarily use a thin mapping. Final product calls must enter Registry; direct Node handlers cannot remain a permanent bypass. Existing script IDs/revisions support transition and traceability rather than define the new public business API.

## 11. Repository Layout, Installation and Consistency

Fork: ZZZZJJJ0928/App-CLI; upstream: Infinimesh-ai/App-CLI. Local branch codex/sparkclaw-email names this batch, not the fork's future capability scope. SparkClaw uses codex/extract-email-app-cli. No migration commits have been pushed.

Proposed layout:

```text
App-CLI/
  src/app_cli/
    core.py, cli.py, tasks.py          # retained public core
    adapters/runtime.py               # explicit v1 / v2 support
    adapters/...                      # reviewed app registration/bindings
    manifests/...                     # packaged authoritative contract copies
  schemas/                            # Manifest, Runtime, binding, Host schemas
  applications/
    gmail/manifest.json, binding.json
    qq-mail/manifest.json, binding.json
    outlook/manifest.json, binding.json
  executors/node/
    stdio-client.mjs                   # fixed RuntimeAdapter target
    service/                          # operation ledger, control and recovery
    backends/browser/                 # HostPort client, no SparkClaw imports
    applications/...                  # provider code, Reader, journal, validation
    assets/                           # generated scripts/digests
  tests/conformance/                  # shared core/executor/host fixtures
  release/                            # release build instructions/manifests
```

One public core supports multiple backends; no parallel js/core catalog. npm may distribute the Node executor; the wheel and Node package are built from one release. Package naming does not define command contracts or require immediate public npm publication.

### 11.1 Matched Releases

A release manifest includes source commit, Python/Node package identities and hashes, Manifest/binding digests, Runtime/Host versions, Reader/initialization-asset digests and test evidence versions. A source SHA alone is not a build artifact digest.

Build and validate packaged Python contracts, Go projections, Reader and Desktop preload. Install in this order: prepare artifacts, validate manifest, stop admission/drain old work, install matching components, handshake, resume consumption. Failure retains the previous matched set rather than running mixed versions.

Startup/reconnection checks Registry Manifest, executor binding, host capabilities and actual Reader/preload identities. Incompatible majors or missing capabilities block the affected adapter before page acquisition with a stable error; unrelated browser functionality stays available.

### 11.2 Deployment and Rollback

Go Gateway keeps the authenticated owner Controller socket mounted into its container. The owner-host Controller invokes the fixed App-CLI Python client and resident Executor; Python/Node are installed in the host release directory. The container does not execute arbitrary host paths. This preserves the product boundary while making Registry the application admission authority.

Rollback restores a complete matching release and checks ledger/journal readability. Unknown state formats block takeover. Never delete records, clear captures or resend tasks to recover. Credentials, profiles, login state and user mail stay out of source/release packages.

Moved SparkClaw code, tests and derived assets retain Apache-2.0 and provenance. Existing App-CLI code retains MIT. Identify both clearly in directories and release manifests.

## 12. Reassessment of the Six Earlier Findings

| Earlier finding | Architectural cause | Current response |
| --- | --- | --- |
| Duplicate Python/JS cores | Started from npm file extraction rather than the public entry | Sole Registry catalog; Node only executes Runtime operations |
| Mail hardcoding remains in Host | Incomplete consumer/scheduler inventory | Include controller, cli-runtime, cli-client and Go execution integration |
| Lifecycle described only as principles | Unclear declaration/acquisition/pre-navigation order | HostPort primitives, fixed phases and explicit host ownership |
| Mutation/recovery ambiguity | Direct old-script calls bypass existing denial | Runtime v2 admission, lookup/cancel/reconcile and state ownership |
| Pinned source can still mix releases | Git pin does not identify loaded artifacts | Matched digests, startup/reconnect handshake, whole-release rollback |
| Validation proves extraction only | No proof of reusable application/backend architecture | Non-mail, non-browser and independent-host acceptance |

Request size, long operations and resident observation are now protocol constraints. These are design-level resolutions, not claims of implemented or passing behavior.

## 13. Implementation and Acceptance Order

| Phase | Deliverables | Exit condition |
| --- | --- | --- |
| 1. Freeze contracts | Common LifecycleAdapter, Runtime v2 mapping, context, binding, HostPort, authority and state fixtures | Decision 0030 accepted; explicit execution generations, renewal/disconnection budgets and identity/effect/state ownership |
| 2. Extend existing core | Compatible Registry/CLI/RuntimeAdapter and one catalog | Original 60 tests pass; unauthorized mutation still denied; v1 responses unchanged |
| 3. Executor and Host | Node client/service, persistent tasks, HostPort, independent reference host | Non-mail application works through app-cli; lost responses, restart, cancellation and cleanup satisfy contracts |
| 4. Mail applications | Three Manifests/bindings, scripts, Reader, journals and verification | Migrated provider tests; independent installation without SparkClaw checkout |
| 5. SparkClaw cutover | Generic client/mapping, old-script removal and deployment | Product calls use Registry; task-page/product regressions pass; matched release can roll back |

This follows architectural dependencies. Keep old paths only during cutover, not as permanent duplicate public execution paths awaiting unspecified future unification.

### 13.1 Architecture Acceptance

- apps / describe / schema discover new apps without starting services or browsers.
- A non-mail fixture using existing HostPort primitives registers, executes and validates without changes to SparkClaw scheduling or generic Registry logic.
- Native/CLI fixtures install without browser/mail dependencies.
- A Python native adapter implements the same LifecycleAdapter admission/task contract against a self-owned mutable local fixture: authorized writes succeed, missing authority is rejected, with no RuntimeAdapter, Node or browser requirement. Adapters without that contract still reject mutations.
- App-CLI's own real local fixture page and reference host execute complete commands after installation outside the repository, proving independence from SparkClaw internals.
- Python and Node share schema/fixtures and identity/size/error/state validation; internal Node entry cannot accept unregistered or unauthorized operations.

### 13.2 Reliability Acceptance

- Key replay/drift, lost admission responses, unresolved lookup and restart never create a second remote write.
- Not-started, known failure, uncertain and successful states are not confused by timeout/cancel; original task/journal remain traceable.
- Independently test 200 KiB bodies, multibyte content, attachment references, 30-minute operations and persistent observation; distinguish RPC and business budgets.
- Test authorized resumption of the original task, query/cancel after execution grants expire, event-gap reconciliation, owner isolation and deduplication retention.
- Duplicate executors, surviving descendants after crashes, Host restart, delayed old-epoch calls/results and stale ledger rollback must not obtain concurrent execution authority; failed cleanup prevents conflicting resource reallocation.
- Test watch renewal and lost replies, SparkClaw offline until grant expiry, heartbeat loss, credential changes, revocation, exhausted recovery budgets and gap reconciliation with explicit state/time boundaries. Watch cleanup must preserve shared pages with valid read leases.
- Reject incompatible mixed releases, stale authority, wrong accounts, expired leases and unauthorized task access.

### 13.3 Browser and Product Acceptance

- Three-provider cold/reused startup, both read/watch startup orders, Outlook pre-navigation injection, QQ document replacement, disconnect and failed-cleanup recovery.
- Exact send approval, unknown-outcome reconciliation, download completeness and actual page/process state after cancellation.
- Preserve ordinary browser tasks, user pages, login state, ingestion/classification/UI and JingSi Runtime v1 behavior.
- Verify clean installation, container-to-host connection, Reader, Desktop preload, generated projections and complete rollback.

Offline fixtures establish only their contracts and simulated scenarios. Record live mail sending, downloading and observation separately. Design review and documentation checks do not execute live mailbox actions.

## 14. Implemented release and acceptance boundary

The approved R3 implementation is complete on `codex/extract-email-app-cli`
(SparkClaw) and `codex/sparkclaw-email` (App-CLI fork). The complete paired
release is `0.3.0-sparkclaw.1`; Python uses `0.3.0+sparkclaw.1`.

- Python Registry remains the sole public catalog/admission point. Explicit
  LifecycleAdapter registration, signed authority and Runtime 2.0 extend the
  existing interfaces; Manifest 1.0, Runtime v1 and default write denial remain.
- The resident Executor owns durable tasks, scoped immutable request keys,
  events, effect fences and recovery. POSIX lock, persisted epoch/high-water
  marker, Host generation and actual daemon lease expiry enforce ownership.
- QQ/Gmail/Outlook scripts, Reader sources/assets, notification rules, send
  journals, schemas and bindings live in App-CLI. SparkClaw's old application
  scripts and private dispatch implementation have been removed.
- Product calls use the public Python entry through a fixed embedding client.
  Controller/Bridge/Desktop retain generic page ownership and scheduling.
  Read/watch share a resource with separate leases; pre-navigation assets,
  account/document checks and cleanup fencing remain enforced.
- Vendor wheel/npm/dependency artifacts, installed-file verification, generated
  Go/Reader/preload projections, service installation and whole-set rollback
  are implemented. State and request history survive compatible rollback.

See the [implementation and acceptance record](app-cli-implementation-validation.md)
for executed checks and reproducible commands. Actual isolated Electron covers
non-mail application reuse and ordinary browser task/personal-page regression;
mail fixtures cover migrated provider semantics. Live mailbox acceptance and
production activation are separate final acceptance steps. No real mail was
sent and no shared service was deployed by this work.
