# SparkClaw Architecture

> Language: English | [简体中文](../zh-cn/docs/architecture.md)

This document is the current architecture source of truth. Older concept, roadmap and completion-audit notes have been folded into this file, [Deployment](deployment.md), [Development](development.md) and [the model baseline](../benchmarks/model_baseline.md).

## Product Boundary

SparkClaw is a local-first personal agent runtime for DGX Spark-class machines. It is optimized for one owner, one local Gateway and a small set of workflows that can be made reliable on local models:

- local files and workspace search
- code inspection and approval-gated patching
- browser-backed web access for public search, page reading and live page interaction
- personal memory candidates and approval-gated sensitive memory
- optional microphone and Telegram voice transcription through one bounded speech adapter
- optional owner-authorized Telegram messaging with multiple encrypted Bot bindings
- optional Infinimesh Info search with one-shot query tokens and cited untrusted evidence

SparkClaw deliberately avoids broad autonomous operation, public SaaS exposure, silent external sends/creates/deletes, hidden tool execution and custom fine-tuned model release claims that are not backed by eval evidence.

Email, calendar and workspace knowledge/RAG are outside the active product boundary until complete designs exist. Their removed prototype surfaces and reintroduction gates are recorded in [Deferred Email, Calendar, and Knowledge Capabilities](deferred-email-calendar-knowledge.md).

## Principles

- **Local-first, not local-only.** Private state, traces, tools and model serving stay local by default. External adapters are explicit boundaries with tokens, policy, audit and approvals.
- **The loop matters more than the model.** Reliability comes from routing, schema validation, tool contracts, guard review, policy, repair, approval, traces and evals.
- **Tools over hallucination.** When an answer requires data or action, the agent should use tools, retrieval or confirmation instead of inventing.
- **Approval is a product surface.** Sends, creates, deletes, shell execution, patch application and sensitive memory writes remain visible and reviewable.
- **Evaluation before model changes.** Model swaps, context changes, speculative decoding and future tuning must pass the same golden/chaos checks or document regressions.

## Runtime Topology

```text
WebChat
  -> Gateway API
      -> Agent Runtime
          -> Guard review
          -> Model Router
          -> ToolHub
          -> Policy / Approval queue
          -> Trace and artifact writers
      -> State backend
      -> Artifact backend
      -> Evaluator

ToolHub adapters:
  files, memory, browser, Infinimesh Info, shell, code, notify

Optional input/connectors:
  speech transcription
  connector Registry -> Telegram private chat, Weixin

Model lanes:
  mock, fast chat, deep chat, embedding, reranker, guard
```

The MVP keeps Gateway, Agent Runtime, Model Router and ToolHub in one Go binary. Docker Compose still separates WebChat, Gateway, sandbox-runner, Postgres, MinIO and optional model-serving processes so the project can later split services without changing product contracts.

## Service Boundaries

### WebChat

The React/Vite workbench is the owner UI. It exposes chat, run state, tool timelines, approval inbox, memory review, traces, eval reports, runtime status, model-call telemetry and settings. It should not hide risky actions or make policy decisions on its own; Gateway remains authoritative.

### Gateway

Gateway owns HTTP/WebSocket APIs, auth, pairing, rate limiting, sessions, events, model calls, tool calls, approvals, memory candidates, evals, traces and artifacts. `/healthz`, `/readyz` and `/metrics` stay available for local diagnostics.

### Speech

Speech is an optional, disabled-by-default OpenAI-compatible transcription boundary. Gateway creates one `speech.Transcriber` and uses that same instance for WebChat microphone requests and Telegram voice notes. Audio is validated as bounded mono 16 kHz PCM16 WAV, is not retained, and only metadata enters audit records. When speech is disabled or unavailable, WebChat and Telegram expose an explicit unavailable state; Telegram text and attachments continue working.

### Messaging Connector Registry

`connector.Registry` is the in-process composition boundary for third-party messaging software. A connector registers only the capabilities it implements: owner binding, outbound notification, an optional `connectorruntime.Runtime`, and binding cancellation. Gateway, reminder delivery, and process startup consume those contracts without selecting Telegram or Weixin by name. `remindertarget.Resolver` selects an outbound binding from normalized binding/session fields, so ToolHub also remains provider-neutral. Protocol-specific polling, media handling, authorization, acknowledgement, target validation, and sends remain inside each provider package.

### Telegram

Telegram is an optional, disabled-by-default owner-authorized connector. Multiple Bot bindings may coexist, including bindings claimed by different external Telegram users. Each Bot token is verified before use and sealed separately through the credential vault; file and PostgreSQL state store ciphertext envelopes rather than plaintext tokens. A verified Bot becomes active with no recipient, then the first fresh private message atomically claims its user and chat; historical updates and group messages cannot claim it. Each binding has its own cursor, inbox identity, and private-chat authorization. Long polling, inbox persistence, per-chat ordering, retries and outbound delivery are bounded. Authorized private chats can send text, supported attachments and voice notes; voice delegates to the shared speech transcriber and does not create a second ASR client.

### Infinimesh Info

Infinimesh Info is the optional production provider for `web.search`. Credentials are injected from environment variables or files and are omitted from public config. One-shot tokens live only in memory, outbound requests have bounded retries and response sizes, and returned sources remain untrusted evidence. Token exhaustion, transport errors and cloud 5xx responses fail the search request without disabling local chat or Telegram.

### Agent Runtime

The runtime handles user messages through a bounded loop:

1. Create an agent run and record the user message.
2. Ask the guard lane for a safety verdict.
3. Plan tool calls or direct answer behavior.
4. Route fast/deep model calls as needed.
5. Execute read/draft tools immediately when policy allows.
6. Queue reversible/dangerous actions for approval.
7. Attempt bounded repairs for narrow failures.
8. Produce a grounded final answer from observations, approvals and model output.
9. Persist trace snapshots, audit events and artifact references.

Guard `block` verdicts stop before tool planning and should not create tool calls or approvals.

### Model Router

Model Router supports deterministic mock mode, OpenAI-compatible chat completions, embeddings, reranking and guard calls. Chat profiles can point at served names rather than checkpoint IDs, which matters for vLLM services such as `sparkclaw-fast` and `sparkclaw-deep`.

Default lanes:

| Lane | Purpose |
|---|---|
| `fast` | interactive chat, routine planning, common drafting |
| `deep` | harder reasoning, repair verification, code and high-risk review |
| `embedding` | workspace/document vectorization |
| `reranker` | RAG evidence reordering, with vLLM generative scoring fallback |
| `guard` | pre-tool safety classification |
| `mock` | deterministic offline tests and golden evals |

### Capability Routing And Workflow Runtime

The Fast router outputs only a strict `RouteDecision`: status, catalog
revision, registered capability path, typed slots, confidence, reason, and
deterministic facts. It cannot emit tools, Skills, Workflow IDs, risk, model
lanes, or arbitrary fields. Deterministic URL/path facts are frozen during
normalization, and the catalog validates every path edge and leaf operation.

Catalog revision 2 has four production leaves: `browser.search`,
`browser.automation`, `document.information`, and `document.processing`.
`WorkflowProfileRegistry.Resolve` maps each leaf to its exact revision 1
Workflow; it performs no intent matching. The Dispatcher persists the
`RouteDecision`, `ReturnRoute`, validated plan digest, and node state.

ToolHub capability metadata is the model-visibility authority. Tool Exposure
materializes the complete fixed scope for the selected Workflow, subject to
Policy; TaskHint candidates, Skill lists, and outcomes cannot widen it.
Outcome adapters produce typed facts, while the active profile assesses
completion or activates only a predeclared transition. Approval and browser
login resumes use the persisted route and exact Workflow scope.

Missing capability, stale state, invalid plan, resource mismatch, and matched
execution failure block or fail explicitly and never fall back. Only a router
status of `unmatched` enters the transitional ReAct path for domains not yet
migrated. Legacy Web/workspace Workflow IDs are retained only as persisted
identifiers and fail closed. See the
[refactor plan](intent-routing-workflow-refactor-plan.md),
[exposure contract](intent-routing-tool-exposure-contract.md), and
[profile catalog](intent-routing-workflow-domain-profiles.md).

### ToolHub

ToolHub registers bounded tools and validates successful outputs against declared contracts. Current tools:

| Area | Tools |
|---|---|
| Files | `files.search`, `files.read`, `files.write_draft`, `file.delete` |
| Memory | `memory.search`, `memory.write_candidate`, `memory.propose`, `memory.write_sensitive` |
| Browser | `web.search`, `browser.read`, `browser.status`, `browser.list_tabs`, `browser.open`, `browser.focus`, `browser.close`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, `browser.wait`, `browser.click`, `browser.type`, `browser.select` |
| Code/shell | `shell.exec_sandboxed`, `code.apply_patch` |
| Approval/notify | `notify.ask_approval` |

Risk levels are `read`, `draft`, `reversible` and `dangerous`. Read/draft tools can run when policy allows. Reversible and dangerous tools require approval in the MVP.

### Policy And Approval

The policy engine enforces denied tools, approval-required tools, sandbox coverage and audit requirements. Approval records include tool name, risk, reason, resources, raw arguments and later resolution metadata.

Examples:

- `file.delete` moves files to `.sparkclaw/trash` with a manifest instead of permanently deleting them.
- `code.apply_patch` stores the original patch, file backups, a manifest and inverse rollback patch under `.sparkclaw/`.
- `shell.exec_sandboxed` executes through the sandbox runner with Docker `--network none`.
- `memory.write_sensitive` executes only after approval.

### State And Artifacts

State backends:

- `file`: default local state at `data/memory/gateway-state.json`.
- `memory`: throwaway process-local state.
- `postgres`: durable sessions, runs, tool calls, approvals, evals and artifacts.

Artifact backends:

- filesystem object store under `data/artifacts/{bucket}/...`
- S3-compatible object store such as MinIO

Traces are written to `data/traces` and also reference artifact URIs. Tool observations are archived as `observations/{run_id}/{tool_call_id}.json`. Browser reads archive raw `browser_snapshot` objects. Memory exports and eval failure archives also go through the artifact boundary.

Trace JSON is redacted with configured logging and memory redact patterns before it is written.

Connector secrets use a separate AES-256-GCM credential vault. The default file backend and PostgreSQL persist only encrypted envelopes and references. Speech audio is temporary, and transcript text is returned only to the requesting flow rather than written to status surfaces, traces or artifacts.

## Data Model

The durable product vocabulary is:

- `Session`
- `Message`
- `AgentRun`
- `WorkflowState`
- `ToolCall`
- `Approval`
- `Memory`
- `MemoryCandidate`
- `AuditEvent`
- `Event`
- `Artifact`
- `EvalRun`

The portable schema notes under `packages/` document stable names for future service splits and SDKs, while the Go Gateway is the authoritative implementation today.

## Memory

Long-term memory follows candidate-then-confirm. Sensitive patterns such as `api_key`, `password`, `token` or `ssh_key` are rejected on the normal candidate path unless sensitive memory is explicitly enabled; approved sensitive memory uses `memory.write_sensitive`.

## External Connector Trust Boundary

External/browser/file observations are untrusted content. They can be quoted, summarized or used as evidence, but instructions inside those observations are not runtime commands.

Browser web access uses `web.search` for discovery and `browser.read` for read-only source-page extraction. Browser automation launches configured Chromium with a SparkClaw-owned persistent profile: ordinary work is headless, while login/captcha/2FA/payment and similar human-only steps temporarily switch the same profile to visible Chromium. Visible and hidden processes never own the profile concurrently. Login state remains inside Chromium rather than being exported as JavaScript cookies, and continuation uses the selected post-login URL even when its origin differs from the original page. `browser.read` waits for rendered DOM state, captures rendered HTML, and passes that HTML through Readability before returning article text. Structure snapshots are an on-demand follow-up when body extraction is insufficient or page controls matter. The focused roadmap is maintained in [Browser Automation Improvement Plan](browser-automation-improvement.md), with profile lifecycle details in [Managed Shared Chromium Profile](managed-persistent-browser-profile.md). Browser observations refuse loopback/private literal hosts by default where URL fetching is involved, archive rendered HTML/raw responses or screenshots, and stay untrusted evidence. Local fixture hosts such as `127.0.0.1` or `host.docker.internal` must be explicitly allowlisted. The runtime must stop for human-only verification and must not invent logged-in evidence.

Authenticated data belonging to the current owner is an allowed local-first read boundary, not an automatic refusal condition. Authenticated browsing is represented in the typed `TaskHint` contract as `evidence_need=personal_data`, `data_scope=owner`, `browser_mode=collaborative`, and `requires_tool_evidence=true`; routing does not enumerate account-data categories. The runtime may use the managed profile and visible login handoff, but it must not ask the owner to paste passwords, cookies, tokens, or verification codes into chat. Third-party data access, credential disclosure, external transmission, and mutating account actions remain subject to their normal policy and approval boundaries.

Infinimesh results and Telegram inbound content follow the same untrusted-observation rule. Each Telegram binding is restricted to the external user and private chat that won its one-time first-message claim; multiple bindings do not share authorization or credentials. Infinimesh requests never include private local context. Credentials, raw authorization material and transcript text are excluded from public status and error strings.

## Ports

| Service | Port | Default bind |
|---|---:|---|
| Gateway | 18789 | `127.0.0.1` |
| WebChat | 18790 | `0.0.0.0` |
| Sandbox runner | 18889 | `127.0.0.1` |
| Fast model | 8001 | `127.0.0.1` |
| Deep model | 8002 | `127.0.0.1` |
| Embedding model | 8003 | `127.0.0.1` |
| Reranker model | 8004 | `127.0.0.1` |
| Postgres | 15432 | `127.0.0.1` |
| MinIO | 9000 / 9001 | `127.0.0.1` |

## Extension Rules

When adding capabilities:

1. Add or update the typed contract first.
2. Assign a risk level and approval behavior.
3. Add policy coverage and audit events.
4. Treat new external observations as untrusted.
5. Archive full observations separately from compressed summaries.
6. Add focused unit tests plus at least one golden or smoke eval case for user-visible behavior.
7. Update [Development](development.md) or [Deployment](deployment.md) if the change affects operators or contributors.
