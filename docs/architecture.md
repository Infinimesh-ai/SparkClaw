# SparkClaw Architecture

> Language: English | [简体中文](../zh-cn/docs/architecture.md)

This document is the current system source of truth. Component contracts are
linked from the [documentation index](index.md); completed plans and superseded
designs are intentionally absent from the active documentation set.

## Product Boundary

SparkClaw is a local-first personal agent runtime for one owner and one local
Gateway on DGX Spark-class hardware. Its active product surface is:

- local files, structured documents, and approval-gated output-copy edits;
- public search, direct weather cards, managed browser open/focus and page
  reading, bounded verified clicks, and approval-gated reversible form drafts;
- approval-gated send-only browser email for a freshly validated configured QQ
  Mail, Outlook, or Gmail account;
- ordinary conversation answers from stable request/context evidence;
- scheduled messages whose payload re-enters normal routing at due time;
- optional WebChat speech transcription, Telegram/Weixin messaging, and
  Infinimesh Info evidence;
- optional fixed-session JingSi text presentation on an explicitly bound
  private-LAN port;
- optional Happy Team task and personal bridge MCP access, with supervised plan
  decisions synchronized into the durable human approval inbox;
- optional workspace-scoped LocalMind MCP access and passive ISCP mention inbox;
- traces, artifacts, evals, policy, approval, auth, and durable state.

The exact executable leaf set is listed in
[Workflow capabilities](workflow-capabilities.md). Browser email sending is an
active bounded capability. Email reading and expansion, calendar, and built-in
workspace knowledge/RAG remain deferred; see
[Deferred capabilities](deferred-email-calendar-knowledge.md).

SparkClaw is not an unrestricted autonomous agent or a public multi-tenant SaaS.
It does not permit silent external sends, creates, deletes, arbitrary browser
interaction, hidden tool execution, or unevaluated model claims.

## Principles

- Local-first state and explicit external adapter boundaries.
- Registered capability and tool contracts over model improvisation.
- One routed leaf Workflow per request, with explicit clarification or failure.
- Deterministic resource binding and Policy-owned effects.
- Approval as a durable product surface for reversible and dangerous actions.
- External/file/browser observations treated as untrusted evidence.
- Traceable execution and evaluation before model or contract changes.

## Runtime Topology

```text
WebChat / Telegram / Weixin / Timer
  -> Gateway + Message Plane
      -> owner request + current/recent resource resolution
      -> semantic intent router
          -> embedding channel: current owner question only
          -> Fast/Tree channel: same question + bounded typed context
          -> weighted fusion + Top-2 decision
      -> Catalog validation and one Workflow Profile
      -> Workflow Runtime
          -> stage-scoped Tool Exposure
          -> Model Router (profile-selected execution calls)
          -> ToolHub -> Policy -> Approval
      -> WorkflowResult
      -> Delivery Gateway -> Web or connector Provider

Foundation:
  Store, trace, artifact, auth, pairing, config, evaluator
```

Gateway, Agent Runtime, Model Router, ToolHub, Message Control, and Delivery run
in one Go binary. Compose separates WebChat, Gateway, sandbox, Postgres, MinIO,
and optional model endpoints so process topology can evolve without changing
the domain contracts.

## Service Ownership

### WebChat

WebChat is the owner workbench. It presents chat, schedules, direct delivery,
connector activation and bindings, tools, approvals, passive collaboration
notifications, memories, traces, artifacts, evals, and runtime settings. It
sends typed actions but does not decide routes, Policy, or delivery. See
[WebChat](webchat.md).

Memory records remain a management scaffold while their product, retrieval,
and safety contract is under design. Agent Runtime does not query them or place
them in Tree, Workflow, or final-answer model context.

### Gateway And Message Plane

Gateway owns HTTP/event APIs, auth, pairing, rate limiting, sessions, public
projections, and service assembly. The Message Plane converts Web, connector,
and Timer input into one provider-neutral `MessageEnvelope`, preserving ordered
parts, source identity, authorization, and `ReturnRoute`.

Each run persists the untouched owner request, deterministic resource context,
route evidence, owner/actor identity, and return route. There is no canonical
execution request, request-normalization model call, or normalization audit
structure. Resume and replay recover the owner request from the persisted
message instead of reinterpreting it.

### Capability And Intent Routing

`capability.Catalog` is the structural authority for branches, leaves, route
contracts, operations, target policy, and Workflow references. Workflow
Profiles own semantic examples and Tree distinctions for their leaf.

Natural-language recognition uses an asymmetric pair of channels over the same
compiled graph. Embedding receives only the current owner-authored question.
Fast/Tree receives that same question plus bounded typed context, including
recent conversation and document-record metadata such as name, format, source,
and recent activity. This contract applies to every natural-language intent,
not only document requests. Fast reasons about ambiguity and scores candidates;
it cannot rewrite the request, bind a resource, or emit a `RouteDecision`.
Its initial score call and optional single repair force thinking off and use the
same strict dynamic JSON Schema. Runtime still verifies graph revision, the
exact candidate set and uniqueness, unknown fields, and score bounds before
fusion; malformed output after the one repair fails the Tree channel. This is
structural hardening only, with Tree temperature, score calibration, and output
token allowance unchanged.
Their calibrated scores are combined as:

```text
fusion_score = alpha * embedding_score
             + (1 - alpha) * tree_score
```

Both channels score every eligible candidate. Weighted fusion sorts that complete
set and retains the final Top-2, which produces clear, ambiguous, or low
coverage; only a clear eligible candidate is assembled into one `RouteDecision`.
Typed UI actions and persisted resumes bypass semantic classification but not
Catalog validation. See [Intent routing](intent-routing.md).

### Workflow Runtime And ToolHub

The Workflow Registry resolves exactly one versioned Profile for a matched leaf.
The Profile creates a frozen plan with bounded nodes, transitions, completion
evidence, risks, and argument bindings. Each active node materializes only the
ToolHub capabilities needed for its current stage.

ToolHub registration is the execution/schema/risk/effect authority for a tool.
Policy and Approval run after Workflow selection and before effects. Model output
cannot add tools, change frozen resource bindings, or bypass approval. A matched
Workflow failure remains explicit and never falls through to another router or
a generic fallback loop; the workflow step loop is the only execution
primitive and the legacy ReAct path has been removed.

Dynamic MCP providers atomically replace their own namespaced ToolHub entries;
they cannot overwrite static tools or another provider. Their schema-free
directory entries participate in the same bounded search as static tools, and
only one persisted selection materializes its complete server-advertised schema.
The source and original remote name remain queryable for audit. A capability
snapshot revision binds endpoint identity, server metadata, tool
schemas/annotations, and credential scope so a refresh invalidates stale views.

Document format variation is registered independently at three owner boundaries.
ToolHub format providers own parsers, editors, schemas, invocation validation,
locators, and result projection. The `document` package owns normalization,
fixed lifecycle hooks, and exact `(format, operation)` preservation policy.
Agent format policies own route grounding, directory scope, decision evidence,
argument binding, schema materialization, and result evidence projection. These
registries join through canonical capability `format` and `operation` qualifiers;
they do not share implementation types or form a cross-package registry. The
common Workflow, Policy/Approval, inspection, output-copy, cleanup, and audit
paths remain format-neutral.

The ordered executable format/operation matrix is defined once by
`internal/app.DocumentFormatOperationSpecs`. ToolHub, `document`, and Agent
retain their own behavior registries, but each registry must exactly join that
catalog during construction and rejects a missing format, missing operation,
extra implementation, duplicate key, or ordering drift with the offending key.
The catalog contains text, DOCX, XLSX, PPTX, and PDF operations only; Agent's
non-executable image routing policy remains outside it.

Office mutation schemas use one public whole-source argument,
`source_sha256`, required for every DOCX, XLSX, and PPTX edit. Agent binds it
from the current Workflow localization observation and keeps
`source_evidence` and `evidence_targets` as Runtime-only provenance. Those
fields are not ToolHub schema inputs and a direct caller cannot use them to
gain authority. `document.Pipeline.Edit` is the sole owner of comparing the
bound whole-source hash with freshly inspected metadata; format providers keep
only target-level evidence checks. The Pipeline's later reinspection remains a
separate within-call time-of-check/time-of-use guard. Text edits and PDF
transforms do not require a source hash.

ToolHub's registered schema remains the execution authority, while each model
stage receives a derived schema projection. Runtime removes frozen bindings and
provable operation-specific fields from that projection, records the remaining
model-visible arguments as `semantic_variables`, then restores selected
capability qualifiers, paths, identities, locators, hashes, generations, and
other authoritative values before ToolHub validation and Policy. A deterministic
acquisition or single-entry decision skips only that model judgment; later
target, tool, content-generation, approval, and effect nodes remain in the same
Workflow.

`ContextBuilder` assembles each bounded model/tool loop from prioritized,
degradable sections. Current-run observations use one uniform small envelope,
appear once in causal order, and retain their artifact references. A stage may
materialize declared, consumer-sized slices of persisted evidence into a
`PROVISIONED_EVIDENCE` section; a frozen generic `SupportRequirements` entry
may expose `observation.read` for bounded, session-scoped read-back when the
declared slice is insufficient. Support entries use ordinary exposure,
selection, Policy, and persisted-scope validation; old plans are not widened
on resume. Each initial or resumed invocation reads at most 256 recent message
candidates, 128 terminal tool-call candidates, and 64 episode candidates, then
selects one immutable 8-message/6-tool/4-episode/3-image snapshot shared by
Tree, Workflow steps, final answers, and recent-document resolution. External
MCP receives an empty snapshot before any history query. Prompt admission uses
the model profile selected by the same Router task policy as execution and
reserves the operation's output-class budget from the physical
`context_tokens`; there is no independent input ceiling or profile-wide output
limit. ContextBuilder degrades session/tool context first, then
provisioned slices, then older observations while preserving the newest two;
the output contract remains the user-prompt tail, and an oversized fixed tail
fails before model invocation. Run-level observations begin compaction at
36,000 bytes but hard-stop at 48,000 bytes before another compaction attempt.
Support reads have a separate two-execution stage quota and do not consume the
business tool-call or repetition budgets.
Execution failures keep a typed reason separate from their internal diagnostic;
only stable safe messages reach run summaries, assistant messages, and
`WorkflowResult`, while raw diagnostics remain in audit.
Migrated document and browser-control model views additionally omit governed paths, source and
target hashes, page/snapshot identity, URLs, generations, and digests when those
facts are already bound in Runtime. They retain coverage and omission metadata,
candidate-local content and structure, opaque selectable refs, and eligible
operation descriptions needed by the current semantic variable.

One Agent-owned `workflow_evidence_projection_record_v1` abstraction manages
consumer projection telemetry across document decisions, document/browser model
stages, and finalization. The persisted audit event records source and derived
lineage, consumer/stage/semantic variable, all coverage dimensions, exact model
payload digest/bytes, archived bytes, binding reference, repair errors, and
reuse. Domain policies construct typed units and invariants; they do not create
parallel projection stores or independent audit formats.

### Model Router

The current model lanes are:

| Lane | Role |
|---|---|
| `fast` | Tree routing, document read/edit reasoning and finalization, and other bounded responsive reasoning |
| `deep` | Default lane for non-document Workflow planning, assessment, repair, and final answers |
| `embedding` | Startup semantic graph index and embedding channel queries |
| `guard` | Dedicated Qwen3Guard prompt moderation before routing or tool execution |
| `mock` | Deterministic local development/eval behavior |

Gateway selects logical lanes. Model output never chooses its own lane. In the
current Local deployment with `sparkclaw-product-v1`, both logical chat profiles resolve to the
Fast endpoint, so no Deep model process is loaded; the lane labels remain in
traces for Workflow compatibility. `configs/model.profiles.json` is the only
capacity source: it maps each lane to a physical model window and positive
output-class budgets. Gateway fails selected-profile loading on missing, zero,
unknown, or illegal capacity, while each local vLLM entrypoint derives
`--max-model-len` from that same profile. Loading and capacity policy is
documented in [Model loading](model-loading.md).

### Message Control And Delivery

Endpoint Registry resolves Web and third-party destinations. Schedule Registry
persists schema-v2 due messages and performs compare-and-swap changes. Timer only
claims due work and republishes it through Message Runtime.

Every terminal route produces one channel-neutral `WorkflowResult`. Ordinary
results and explicit Web direct sends both create a `DeliveryRequest` and call
the Delivery Gateway. `LocalWebDelivery` is the Web provider adapter inside that
same gateway, not a parallel Web send API. See
[Messaging and scheduling](messaging-and-scheduling.md).

Connector Registry exposes one provider-neutral control plane for every
registered third-party message channel. A durable owner `ConnectorSetting`
controls inbound Runtime, Endpoint Registry visibility, and outbound Provider
access. Binding records and encrypted credentials are separate retained account
state and never imply that a channel is enabled.

At startup the Registry loads every owner's settings before Gateway listens.
The static channel `Enabled` value is only the default for owners without a
record; an explicit owner setting overrides it. The process-local write-through
cache is the read authority, and Connector Registry is the only supported
writer. A preload error fails startup instead of falling back to static state.

Telegram and Weixin each retain one physical worker per channel. The worker runs
while the static default or any persisted owner requires it, and an owner gate
filters both acquisition and pre-dispatch. Disabling one owner immediately
blocks new endpoint resolution, binding setup, polling, and outbound sends only
for that owner. Already-dispatched work drains through its admitted source reply;
persisted but undispatched work remains pending until re-enable. This is logical
household isolation within one trusted Gateway, not a hostile-tenant process or
Store boundary. See [Per-owner connector activation](connector-owner-runtime-design.md).

### Browser, Documents, And Integrations

Browser execution uses the checksum-pinned SparkClaw Browser Bridge and an
owner-scoped Playwright Controller with one persistent SparkClaw Chromium
profile and no alternate backend. Each client is confined to a task-owned tab;
background work does not focus owner tabs, while explicit handoff can foreground
the task tab. The existing destination registry is the candidate-independent
named-target fast path; after its miss and browser leaf selection, the Workflow
may use Info's ordered structured URLs without another semantic classifier.

Info search responses enter ToolHub as one versioned aggregate rather than
parallel answer copies. The `websearch` owner validates the source graph once
and derives two read-only views: an order-preserving, whole-unit answer
projection for deterministic grounded rendering, and the independent ordered
URL view used by browser target identification. Upstream action advice remains
raw untrusted data, while conflict, freshness, uncertainty, and projection
omissions remain typed answer limitations. Persisted legacy results are
normalized only at the decoder boundary.

Page reads remain fully hidden and session-required, while clicks and approved
form drafts retain fresh page-generation evidence and visible result
verification. After a click, semantic assessment receives an action/transition
projection and Runtime rejects repetition of an already validated semantic
action. A visible result equivalent in profile, route, and rendered content
reuses the hidden verdict through a derived assertion; a material difference is
reassessed. Details and Workflow limits are in
[Browser runtime](browser-runtime.md).

Documents have durable first-class `DocumentRecord` identities independent of
their parsed content. Reads and edits use recent-record resolution, deterministic
format inspection, a traceable `confirm_document_target` Workflow node,
structured parsing, and an explicit `select_edit_operation` decision node.
The edit localization node invokes its single format-qualified reader directly
and exactly once with the frozen path; no model tool-choice step precedes the
read.
All document model calls currently use Fast: this includes read finalization,
multi-candidate edit decisions, and editor argument generation. Multi-candidate
decisions use normalized projection-local candidates with typed selected/no-match
output and one same-projection repair, then persist one exact ToolHub entry
before the editor can materialize. PPTX semantic generation similarly filters
no-ops and permits one typed repair for invalid mutation output before approval.
The former inline secondary directory router remains removed. Approval,
output-copy writes, and post-edit
preservation checks remain on the shared path. Parsed representations may be
incomplete, replaced, or regenerated without losing document identity or
activity lineage.
XLSX reads additionally project bounded typed sheet/row/cell evidence with
stable source hashes. Revision 7 binds every spreadsheet edit to that read
before approval and admits a successful output only after OOXML feature gating,
typed reread checks, and package-part preservation verification; unsupported
features or undeclared package drift fail closed and leave no output copy.
An optional bounded `internal/documentocr` adapter sends selected page images
to OvisOCR2 and retains Markdown as untrusted evidence. Scanned PDF pages are
rasterized under page/byte budgets and successful OCR is promoted into the
stable PDF page blocks. OCR is not a Workflow-selected chat lane and its
failure leaves the read explicitly partial.
Document-read finalization records source and claim coverage independently;
partial sources or truncated finalizer projections require an explicit
limitation and prohibit whole-document or absence claims.
See [Document workflows](document-workflows.md).

LocalMind uses the shared MCP 2025-06-18 Streamable HTTP client behind a
workspace-scoped manager. The manager resolves environment credentials on every
refresh, verifies the `localmind-ai` identity and workspace endpoint, rejects
Resources and any schema outside the exact delegate/get/cancel task contract,
and atomically projects those three remote tools into four local registrations:
read delegation, write delegation, get, and cancel. A refresh failure removes
all stale LocalMind tools. The four explicit-only r1 Catalog leaves and
Workflows delegate current-message text, query one referenced task, or cancel
one referenced task. Read delegation and query need no approval; write
delegation and cancel do. Delegation waits through a frozen endpoint/contract
snapshot and bounded status polls for at most 10 minutes, and only `completed`
is success. Task results enter the bounded untrusted observation and artifact
path without claiming local sandbox execution. External-AI principals cannot
select these routes. See [LocalMind Workflows](localmind-task-workflow-design.md).

The current legacy reverse direction uses ISCP: an authenticated LocalMind peer
can submit a structured mention through `agent.notification.deliver.v1`.
Gateway validates the untrusted deep link and durably deduplicates the record by
peer and idempotency key before acknowledging it. This passive path feeds an
owner-scoped global WebChat inbox and SSE stream without creating conversation
or Agent Runtime state. The older session/message request pair remains available
to LocalMind only inside that legacy chain until target cutover; LocalMind access
through both paths is then deleted. Shared request types still required by
JingSi remain unchanged while its current Bridge path is retained. The
implemented SparkClaw side of the
[JingSi LAN Web client design](jingsi-lan-connection-design.md) binds one
configured Web-visible session behind a dedicated allowlisted presentation
port, with text send and a filtered projection of existing durable session
message events but no mobile session/history API. It is not a connector or
provider. JingSi client work and the physical LAN proof remain pending.

Telegram, Weixin, speech, Infinimesh Info, and LocalMind are optional adapters
behind shared connector, delivery, transcription, search, or MCP contracts. See
[External integrations](integrations.md).

JingSi App and LocalMind currently connect through the separately deployed
[ISCP Bridge](iscp-bridge.md). The Bridge terminates ISCP transport and calls one
loopback Gateway API; session state, execution, Policy, approvals, passive
notifications, event cursors, and audit remain owned by Gateway. Bearer auth is
required when Gateway auth is enabled; the default no-auth Gateway still accepts
only loopback Bridge dispatch. LocalMind's use of this path is legacy and its
bootstrap expects the external LocalMind controller to return the bundle used by
SparkClaw's Bridge, which reverses the target LocalMind authority direction.
JingSi's direct-LAN Web client migration is separate. Its SparkClaw surface is
implemented, but the current Bridge path remains until JingSi client work and
the physical LAN proof are complete.

### Unified Third-Party Access (SparkClaw Surface Implemented)

The target inbound architecture gives LocalMind and future third-party systems
that choose this contract one provider-neutral ordinary-conversation MCP
surface instead of adding an adapter or API for each provider. A generic ISCP
MCP Access Gateway carries the MCP session between an enrolled external gateway
device and SparkClaw. The local service exposes one business tool,
`sparkclaw.conversation.send`; its message enters ordinary semantic routing and
the selected Workflow executes through the existing Runtime, Policy, approval,
and audit core.

JingSi is out of scope because it is a WebChat mobile client rather than a
third-party MCP caller. It receives no MCP enrollment, tool projection,
third-party endpoint, or sender. Its implemented SparkClaw direct-LAN surface
reuses Web sessions, Web message ingress, and LocalWebDelivery; JingSi client
work remains pending. This project leaves the minimum current JingSi Bridge
path unchanged until validation.

MCP protocol negotiation and capability listing remain in its dedicated
adapter, but MCP business calls join the managed third-party chain. A
`tools/call` creates a `third_party_device` `MessageEnvelope`, an owner-scoped
MCP source endpoint, and a frozen source `ReturnRoute`. The common router uses
the server-owned leaf binding for deterministic Top-1 selection; Runtime,
Policy, approval, Store, and audit remain shared.

Waiting and terminal results become ordinary `WorkflowResult` and
`DeliveryRequest` records. Delivery Gateway resolves the MCP source endpoint and
invokes one generic MCP sender/provider, which maps the result to correlated MCP
result, progress, or binding-scoped SparkClaw operation frames over ISCP. The
first version remains MCP `2025-06-18` and does not advertise standard MCP Tasks.
This keeps one managed receive/run/send lifecycle without treating MCP control
traffic as chat.
MCP also reuses provider-neutral third-party enable/suspend, endpoint visibility,
and provider-availability management; polling and other inapplicable connector
internals remain adapter-specific.

SparkClaw locally starts the ISCP pairing flow and presents the resulting
one-time ISCP Pairing Ticket to the owner. The owner transfers it once to the
external Access Gateway, which connects to ISCP and redeems it through standard
PairingTicket/Provisioning to join the same ISCP Domain as SparkClaw. ISCP
defines, signs, verifies, and consumes the ticket; verifies device proof; issues
Trust Grants and Relay credentials; establishes encrypted sessions; rotates
credentials; and enforces transport revocation. SparkClaw does not duplicate
those protocol services. Once the authenticated ISCP session is ready,
SparkClaw separately issues a short-lived, single-use MCP Access Ticket. The
enrolled external device redeems it only through that session, and SparkClaw
atomically consumes it to activate a durable conversation-scoped MCP Binding.
Ordinary MCP use relies on the session identity plus the Binding and reuses
neither ticket. The external device
remains requester/source provenance while SparkClaw remains the Workflow
executor. Both gateway roles connect outbound to the Relay, so ISCP opens no
public inbound port. The owner may independently enable direct LAN MCP through
the WebChat `18790` `/mcp` ingress; WebChat proxies that exact route to the
Docker-internal Gateway, and the route is absent while the switch is off.

The full trust, conversation capability, invocation, LocalMind migration, and
acceptance contract is in [Unified third-party ISCP MCP access](unified-third-party-access-design.md).
The owner-facing External MCP settings surface and configured authority adapter
are implemented. The adapter makes one authenticated, bounded outbound request
for `iscp.pairing_ticket.v2`; it never owns Trust Root signing material. Only
the non-secret onboarding receipt survives in memory, file, or PostgreSQL. The
signed ticket is returned once. MCP Access Tickets and conversation-scoped
Bindings can be listed or revoked locally; they contain no Catalog grants.

The SparkClaw-owned local runtime is implemented: strict MCP `2025-06-18`,
hash-only single-use MCP Access Tickets, durable peer Bindings with schema-v2
conversation scope, the single `sparkclaw.conversation.send` business tool,
ordinary semantic routing, bounded filename-only Top-1 response-media
resolution behind a pre-discovery owner approval, shared Delivery, binding-scoped
operation recovery, default-off channel gates, and redacted lifecycle audit.
Each Binding owns a visible `AI · <short device ID>` conversation whose title
and content lifecycle cannot be changed through ordinary session controls;
requirements enter only through the authenticated Binding, and WebChat presents
the conversation as read-only. Inbound media locators appear as unverified
non-downloadable requirements. Binding revocation or record deletion retains
the read-only conversation history. Workspace
approvals expose a derived human-readable review projection while authorization
continues to bind only the frozen tool arguments and authenticated policy
context. Approval resolution returns after the decision is durable; detached
Gateway work advances the MCP operation from `approval_required` through
`running` to a distinct execution or delivery outcome while remaining bound to
operation cancellation, Binding revocation, the invocation deadline, and
Gateway shutdown.
Encrypted Bridge tests carry the MCP request and response through an established
ISCP session. Production external onboarding is not active until the configured
ISCP authority implementing the configured PairingTicket endpoint, a deployable external
Access Gateway, and live Relay validation are completed. The current
`agent.*.v1` Bridge therefore remains temporarily executable. Once LocalMind
passes the new path after fresh
ISCP PairingTicket/Provisioning into SparkClaw's Domain followed by SparkClaw
MCP Access Ticket redemption, its external enrollment bundle flow, manifest
entries, dispatch branches, fallbacks, configuration,
tests, and guidance must be deleted. Shared Bridge components still required by
JingSi remain frozen until its shared Web client connection is implemented and
validated; they must not retain a hidden LocalMind fallback. The outbound
workspace-scoped SparkClaw-to-LocalMind MCP
client is a separate direction and remains unchanged.

## State And Artifacts

Typed Store repositories own sessions, messages, Agent runs, route/fusion evidence,
Workflow state, tool/model calls, durable document records and lineage,
approvals, schedules, endpoints, deliveries, connector bindings, inbox records,
passive notifications and read state, connector settings, memories, evals, and
audit events. Memory, file snapshot, and PostgreSQL backends implement the same
state contracts. Consumers depend only on the repositories they use; the broad
Store interface has been removed.

An assembly-only Store Runtime selects one backend, exposes its typed
repositories to `cmd/sparkclaw`, and owns finite operation budgets, readiness,
bounded metrics, recovery probes, drain, and close. It does not forward business
methods or escape assembly. File commands serialize snapshot replacement and
fence uncertain outcomes until digest-based reconciliation. PostgreSQL commands
use transactions where one local aggregate spans records. Reliability depth is
assigned by operation risk instead of applying the P0 protocol to every
repository. See [Store](store.md).

Gateway is the only PostgreSQL application-schema owner. Ordered SQL embedded
in the Store package is applied under a fixed startup advisory lock and recorded
in `sparkclaw_schema_migrations` with immutable filenames and checksums. A fresh
database and a pre-ledger SparkClaw database use the same transaction: pending
SQL, compatibility reconciliation, scratch-derived catalog validation, and
ledger rows either commit together or leave readiness false. The PostgreSQL
image does not install a second schema copy.

Artifacts hold large or inspectable outputs such as tool observations, browser
evidence, replaceable parsed-document observations, generated documents/media,
memory exports, rollback files, and eval failure archives. Filesystem and
S3-compatible backends share the same metadata contract. Secrets and raw speech
audio are not artifacts.

## Trust And Safety Boundaries

- Product Compose keeps Gateway at Docker-internal `gateway:18789` and
  publishes WebChat on `18790`; direct MCP ingress on `/mcp` remains
  owner-controlled and default-off. The optional JingSi overlay publishes only
  its exact presentation allowlist on one selected RFC1918 address at `18793`.
  Host-process Gateway debugging remains loopback-only.
- Authenticated requests carry one owner/actor principal. Endpoint and schedule
  queries are owner-scoped.
- ToolHub registration is the baseline approval authority. Policy may only
  strengthen `RequiresApproval` and risk decisions through typed execution
  context; requester text, channel, destination, and local-model participation
  cannot lower or independently raise that baseline.
- A persisted inbound MCP invocation represents an external-AI principal. Its
  access to original or derived SparkClaw workspace data requires owner
  approval before file/index discovery, metadata inspection, symlink
  resolution, hashing, parsing, or content reads. The approval binds the MCP
  identity, local owner/authorized principal, locator/query, Workflow plan,
  output class, and return route.
- Inbound MCP runs do not receive prior session messages, tool summaries,
  memories, images, or episode summaries as implicit model context. Approved
  current-run evidence remains available; explicit workspace locators enter the
  governed access contract instead of inheriting cached derivatives.
- One approved external-MCP workspace data contract covers its exact bound read
  and frozen return/send. Delivery does not request a second approval. A changed
  identity, locator, Workflow, output, or target fails closed; tool-native
  reversible or dangerous approvals still apply and cannot be downgraded.
- A frozen cross-channel result updates the originating durable MCP operation
  after target delivery. A waiting result suppressed from that target still
  records `approval_required`, so operation polling cannot remain stuck in
  `running`; the MCP result does not duplicate the payload delivered elsewhere.
- An authenticated human's explicit text, image, audio, file, mixed, or
  multipart send is already authorized and gains no destination-only approval.
  Persisted legacy `message_control.external_send` approvals cannot resume
  delivery.
- Reversible and dangerous effects require Policy approval; shell execution is
  sandboxed and network-disabled by default.
- Browser URLs, artifact paths, workspace paths, and provider destinations are
  normalized and validated deterministically.
- The LocalMind MCP endpoint comes only from operator configuration, remains
  bound to one workspace path, rejects redirects, and requires HTTPS unless
  private HTTP is explicitly enabled.
- External content, documents, browser pages, and tool output remain untrusted
  data with provenance.
- Credentials are injected through environment/files or encrypted vault
  envelopes and are omitted from public config, logs, traces, and artifacts.
- Timeouts, size limits, concurrency limits, retries, idempotency, and explicit
  unknown-outcome states are part of adapter contracts.

## Stable Data Contracts

Important cross-package contracts live in `internal/app`:

- `MessageEnvelope`, `MessageContent`, `MessageIngressContext`, `ReturnRoute`;
- `RouteDecision`, `IntentFusionDecision`, `IntentEnvelope`;
- `WorkflowPlan`, `WorkflowState`, `WorkflowResult`;
- `ToolCall`, `ToolOutcome`, `Approval`;
- `DocumentRecord`;
- `MessageEndpoint`, `MessageSchedule`, `DeliveryRequest`, `DeliveryReceipt`;
- `ConnectorSetting`, `ConnectorStatus`, `NotificationBinding`,
  `PassiveNotification`;
- `ArtifactObject`, traces, audit events, and model calls.

Providers and UIs consume these contracts through owner packages and public
projections. They must not maintain competing literal maps or duplicate stores.

## Ports

| Service | Default |
|---|---|
| Gateway | `gateway:18789` (Docker internal, no host publication) |
| WebChat | `0.0.0.0:18790` |
| WebChat pairing bootstrap | `127.0.0.1:18795` (exact routes only) |
| JingSi LAN presentation | `<selected RFC1918 host>:18793` (optional overlay only) |
| Browser eval fixture | `127.0.0.1:18791` |
| Sandbox runner | `127.0.0.1:18889` |
| Fast / Deep / Embedding / Guard | `8001` / `8002` / `8003` / `8005` |
| Optional ASR / OvisOCR2 | `8006` / `8007` |
| PostgreSQL / MinIO | `15432` / `19000` (`19001` console) |

## Extension Rules

1. Define the owner package and typed contract before wiring adapters.
2. Register capability topology and one exact Workflow Profile for user-facing
   execution; a ToolHub registration alone is not enough.
3. Add deterministic grounding and argument binding for every resource.
4. Keep Policy, approval, delivery, and persistence on shared paths.
5. Add focused contract tests, backend parity tests where relevant, and eval
   cases scaled to the behavior risk.
6. Update [Workflow capabilities](workflow-capabilities.md), the relevant
   component guide, and [Development](development.md) or
   [Deployment](deployment.md) in the same change.
