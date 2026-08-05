# SparkClaw Architecture

> Language: English | [简体中文](../zh-cn/docs/architecture.md)

This document is the current system source of truth. Component contracts are
linked from the [documentation index](index.md); completed plans and superseded
designs are intentionally absent from the active documentation set.

## Product Boundary

SparkClaw is a local-first personal agent runtime for one owner and one local
Gateway on DGX Spark-class hardware. Its active product surface is:

- local files, structured documents, and approval-gated output-copy edits;
- public search, direct weather cards, managed browser open/focus, and bounded
  verified page interaction;
- ordinary conversation answers from stable request/context evidence;
- scheduled messages whose payload re-enters normal routing at due time;
- personal memory candidates and approval-gated sensitive memory;
- optional WebChat speech transcription, Telegram/Weixin messaging, and
  Infinimesh Info evidence;
- traces, artifacts, evals, policy, approval, auth, and durable state.

The exact executable leaf set is listed in
[Workflow capabilities](workflow-capabilities.md). Email, calendar, and
workspace knowledge/RAG are not active capabilities; see
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
connector activation and bindings, tools, approvals, memories, traces,
artifacts, evals, and runtime settings. It sends typed actions but does not
decide routes, Policy, or delivery. See [WebChat](webchat.md).

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

`ContextBuilder` assembles each bounded model/tool loop from prioritized,
degradable sections. Current-run observations use one uniform small envelope,
appear once in causal order, and retain their artifact references. A stage may
materialize declared, consumer-sized slices of persisted evidence into a
`PROVISIONED_EVIDENCE` section; `observation.read` supplies bounded,
session-scoped read-back when the declared slice is insufficient. Prompt
admission uses the model profile selected by the same Router task policy as
execution, an 85% context-window safety factor, and an offline-calibrated
conservative token estimate. It degrades session/tool context first, then
provisioned slices, then older observations while preserving the newest two;
the output contract remains the user-prompt tail. Run-level observation
pressure similarly compacts the oldest entries before it stops execution.

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
current `single-fast-v1` deployment, both logical chat profiles resolve to the
Fast endpoint, so no Deep model process is loaded; the lane labels remain in
traces for Workflow compatibility. Loading and capacity policy is documented
in [Model loading](model-loading.md).

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

### Browser, Documents, And Integrations

Browser execution uses pinned agent-browser with a SparkClaw-owned Chromium
profile and no alternate browser backend. Details and Workflow limits are in
[Browser runtime](browser-runtime.md).

Documents have durable first-class `DocumentRecord` identities independent of
their parsed content. Reads and edits use recent-record resolution, deterministic
format inspection, a traceable `confirm_document_target` Workflow node,
structured parsing, and an explicit `select_edit_operation` decision node.
The edit localization node invokes its single format-qualified reader directly
and exactly once with the frozen path; no model tool-choice step precedes the
read.
All document model calls currently use Fast: this includes read finalization,
multi-candidate edit decisions, and editor argument generation. A decision
persists one exact ToolHub entry before the editor can materialize; the former
inline secondary directory router remains removed. Approval, output-copy writes, and post-edit
preservation checks remain on the shared path. Parsed representations may be
incomplete, replaced, or regenerated without losing document identity or
activity lineage.
XLSX reads additionally project bounded typed sheet/row/cell evidence with
stable source hashes. Revision 6 binds every spreadsheet edit to that read
before approval and admits a successful output only after OOXML feature gating,
typed reread checks, and package-part preservation verification; unsupported
features or undeclared package drift fail closed and leave no output copy.
An optional bounded `internal/documentocr` adapter sends selected page images
to OvisOCR2 and retains Markdown as untrusted evidence. Scanned PDF pages are
rasterized under page/byte budgets and successful OCR is promoted into the
stable PDF page blocks. OCR is not a Workflow-selected chat lane and its
failure leaves the read explicitly partial.
See [Document workflows](document-workflows.md).

Telegram, Weixin, speech, and Infinimesh Info are optional adapters behind
shared connector, delivery, transcription, or search contracts. See
[External integrations](integrations.md).

JingSi App connects through the separately deployed [ISCP Bridge](iscp-bridge.md).
The Bridge terminates ISCP transport and calls one authenticated loopback Gateway
API; session state, execution, Policy, approvals, event cursors, and audit remain
owned by Gateway.

## State And Artifacts

Store interfaces own sessions, messages, Agent runs, route/fusion evidence,
Workflow state, tool/model calls, durable document records and lineage,
approvals, schedules, endpoints, deliveries, connector bindings, inbox records,
connector settings, memories, evals, and audit events. Memory, file snapshot,
and PostgreSQL backends implement the same durable state contracts.

Artifacts hold large or inspectable outputs such as tool observations, browser
evidence, replaceable parsed-document observations, generated documents/media,
memory exports, rollback files, and eval failure archives. Filesystem and
S3-compatible backends share the same metadata contract. Secrets and raw speech
audio are not artifacts.

## Trust And Safety Boundaries

- Gateway is loopback-only by default; WebChat is the only default LAN surface.
- Authenticated requests carry one owner/actor principal. Endpoint and schedule
  queries are owner-scoped.
- Reversible and dangerous effects require Policy approval; shell execution is
  sandboxed and network-disabled by default.
- Browser URLs, artifact paths, workspace paths, and provider destinations are
  normalized and validated deterministically.
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
- `ConnectorSetting`, `ConnectorStatus`, `NotificationBinding`;
- `ArtifactObject`, traces, audit events, and model calls.

Providers and UIs consume these contracts through owner packages and public
projections. They must not maintain competing literal maps or duplicate stores.

## Ports

| Service | Default |
|---|---|
| Gateway | `127.0.0.1:18789` |
| WebChat | `0.0.0.0:18790` |
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
