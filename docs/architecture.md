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
      -> request normalization and deterministic grounding
      -> semantic intent router
          -> embedding channel
          -> Fast/Tree channel
          -> fusion + bounded reranker + Top-2 decision
      -> Catalog validation and one Workflow Profile
      -> Workflow Runtime
          -> stage-scoped Tool Exposure
          -> Model Router (Deep execution calls)
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
connector bindings, tools, approvals, memories, traces, artifacts, evals, and
runtime settings. It sends typed actions but does not decide routes, Policy, or
delivery. See [WebChat](webchat.md).

### Gateway And Message Plane

Gateway owns HTTP/event APIs, auth, pairing, rate limiting, sessions, public
projections, and service assembly. The Message Plane converts Web, connector,
and Timer input into one provider-neutral `MessageEnvelope`, preserving ordered
parts, source identity, authorization, and `ReturnRoute`.

Each run persists the untouched owner request, normalized execution request,
deterministic resource context, route evidence, owner/actor identity, and return
route. Resume and replay reuse that state instead of reinterpreting it.

### Capability And Intent Routing

`capability.Catalog` is the structural authority for branches, leaves, route
contracts, operations, target policy, and Workflow references. Workflow
Profiles own semantic examples and Tree distinctions for their leaf.

Natural-language recognition uses an embedding channel and a Fast model Tree
channel over the same compiled graph. Their calibrated scores are combined as:

```text
fusion_score = alpha * embedding_score
             + (1 - alpha) * tree_score
```

A bounded reranker reorders only the fused shortlist. The final Top-2 produces
clear, ambiguous, or low coverage; only a clear eligible candidate is assembled
into one `RouteDecision`. Typed UI actions and persisted resumes bypass semantic
classification but not Catalog validation. See [Intent routing](intent-routing.md).

### Workflow Runtime And ToolHub

The Workflow Registry resolves exactly one versioned Profile for a matched leaf.
The Profile creates a frozen plan with bounded nodes, transitions, completion
evidence, risks, and argument bindings. Each active node materializes only the
ToolHub capabilities needed for its current stage.

ToolHub registration is the execution/schema/risk/effect authority for a tool.
Policy and Approval run after Workflow selection and before effects. Model output
cannot add tools, change frozen resource bindings, or bypass approval. A matched
Workflow failure remains explicit and never falls through to another router or
legacy ReAct.

### Model Router

The current model lanes are:

| Lane | Role |
|---|---|
| `fast` | Tree routing and other bounded responsive reasoning |
| `deep` | Selected Workflow planning, assessment, repair, and final answers |
| `embedding` | Startup semantic graph index and embedding channel queries |
| `reranker` | Bounded fused-shortlist scoring |
| `guard` | Optional review profile; may share a served chat model |
| `mock` | Deterministic local development/eval behavior |

Gateway selects lanes. Model output never chooses its own lane. Loading and
capacity policy is documented in [Model loading](model-loading.md).

### Message Control And Delivery

Endpoint Registry resolves Web and third-party destinations. Schedule Registry
persists schema-v2 due messages and performs compare-and-swap changes. Timer only
claims due work and republishes it through Message Runtime.

Every terminal route produces one channel-neutral `WorkflowResult`. Ordinary
results and explicit Web direct sends both create a `DeliveryRequest` and call
the Delivery Gateway. `LocalWebDelivery` is the Web provider adapter inside that
same gateway, not a parallel Web send API. See
[Messaging and scheduling](messaging-and-scheduling.md).

### Browser, Documents, And Integrations

Browser execution uses pinned agent-browser with a SparkClaw-owned Chromium
profile and no alternate browser backend. Details and Workflow limits are in
[Browser runtime](browser-runtime.md).

Document reads and edits use format inspection, structured normalization,
bounded enrichment, exact editor selection, approval, output-copy writes, and
post-edit preservation checks. See [Document workflows](document-workflows.md).

Telegram, Weixin, speech, and Infinimesh Info are optional adapters behind
shared connector, delivery, transcription, or search contracts. See
[External integrations](integrations.md).

JingSi App connects through the separately deployed [ISCP Bridge](iscp-bridge.md).
The Bridge terminates ISCP transport and calls one authenticated loopback Gateway
API; session state, execution, Policy, approvals, event cursors, and audit remain
owned by Gateway.

## State And Artifacts

Store interfaces own sessions, messages, Agent runs, route/fusion evidence,
Workflow state, tool/model calls, approvals, schedules, endpoints, deliveries,
connector bindings, inbox records, memories, evals, and audit events. File state
supports local use; PostgreSQL supports durable operation.

Artifacts hold large or inspectable outputs such as tool observations, browser
evidence, generated documents/media, memory exports, rollback files, and eval
failure archives. Filesystem and S3-compatible backends share the same metadata
contract. Secrets and raw speech audio are not artifacts.

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
- `MessageEndpoint`, `MessageSchedule`, `DeliveryRequest`, `DeliveryReceipt`;
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
| Fast / Deep / Embedding / Reranker | `8001` / `8002` / `8003` / `8004` |
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
