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

`connector.Registry` is the in-process composition boundary for third-party messaging software. A connector registers only the capabilities it implements: owner binding, one outbound `delivery.Provider`, an optional inbound `connectorruntime.Runtime`, and binding cancellation. The same live Provider Registry is shared by ordinary Agent results and scheduled messages; future explicit-send surfaces must enter through `RequestForMessage` and the same Gateway. Gateway, Message Control, and the Agent Runtime do not select Telegram or Weixin by name. Protocol-specific polling, media handling, credentials, acknowledgement, address validation, and sends remain inside each provider package.

### Message Ingress And Result Delivery

Web, third-party devices, and Timer events enter through the same provider-neutral message boundary. `MessageEnvelope` records source Endpoint, native message/thread identity, owner authorization, ordered `MessageContent` parts, and `ReturnRoute`. Text, image, audio, and file are message-part kinds rather than Agent capabilities. Owner-authored text/captions and governed resources are projected separately: resource metadata is JSON-encoded trusted data and is never appended to the owner request as prose or instructions. Every routed request gets one pre-route `RequestNormalization` record containing the untouched original, fact-checked canonical request, deterministic resource context, and normalization source. Normalization must preserve the original language and writing system; final-answer rendering derives its response language from the untouched original rather than the canonical execution query. This record, owner, authorization, route decision, and return route are persisted on `AgentRun`, so idempotent replay and approval/browser-login resume keep the same request, identity, resources, and destination.

Every terminal route produces a channel-neutral `WorkflowResult`. Matched Workflow failures remain explicit results; only an `unmatched` route may use the transitional ReAct fallback. `WorkflowResult -> DeliveryRequest -> Delivery Gateway` is the single ordinary outbound path. The Gateway resolves the Endpoint, checks owner authorization, negotiates the whole multimedia payload before the first send, and dispatches through the registered Provider or Web port. Provider control messages such as typing and approval buttons stay local to the connector and are not a second result path.

The Web port consumes those same ordered parts through one generic projection onto the persisted assistant `Message`: text parts become `content`, while image, audio, and file parts backed by governed `workspace_file` refs become `attachments`. WebChat resolves each attachment against the message session's workspace and fetches it through the authenticated document endpoint. Provider-specific or tool-specific summary syntax is not part of this contract; Markdown media parsing remains only for older persisted messages.

Timer is a message source, not a reminder-only feature. A `ScheduleSpec` stores either literal content or a request to execute later, plus authorization and return routing. Polling only claims due work; a bounded worker pool runs the request and returns its result through the same Delivery Gateway.

### Telegram

Telegram is an optional, disabled-by-default owner-authorized connector. Multiple Bot bindings may coexist, including bindings claimed by different external Telegram users. Each Bot token is verified before use and sealed separately through the credential vault; file and PostgreSQL state store ciphertext envelopes rather than plaintext tokens. A verified Bot becomes active with no recipient, then the first fresh private message atomically claims its user and chat; historical updates and group messages cannot claim it. Each binding has its own cursor, inbox identity, and private-chat authorization. Long polling, inbox persistence, per-chat ordering, retries and outbound delivery are bounded. Authorized private chats can send text, supported attachments and voice notes; voice delegates to the shared speech transcriber and does not create a second ASR client.

### Infinimesh Info

Infinimesh Info is the optional production provider shared by `web.search` and the direct `info.query` question boundary. Credentials are injected from environment variables or files and are omitted from public config. One-shot tokens live only in memory, and outbound requests have bounded retries and response sizes. SparkClaw maps the trimmed `answer_context.summary`, non-empty `answer_context.key_facts[].claim` values, public source metadata and snippets, and citations. These remain untrusted evidence under stable `summary:0`, `fact:N`, and `source:N:snippet:M` refs; a status-style or missing summary does not hide usable structured evidence. The provider request ID and frozen query preserve traceability. Before any model call, both generic search and direct weather extraction select a query-relevant projection with a hard byte limit; the complete mapped result remains in the tool archive for deterministic validation. Token exhaustion, transport errors, and cloud 5xx responses fail the current request without disabling local chat or messaging connectors.

### Agent Runtime

The runtime handles messages through a bounded router-first loop:

1. Normalize source, multimedia content, owner authorization, and return route.
2. Create an Agent Run and persist its message context.
3. Normalize every owner request once, validate deterministic facts, and persist the canonical request before routing.
4. Ask the guard lane for a safety verdict over the original owner-authored content.
5. Ask the Fast router for a strict capability-tree decision over the canonical request and separately typed resources.
6. Dispatch a matched leaf to its exact Workflow and fixed tool scope.
7. Use transitional ReAct only when the route is explicitly `unmatched`.
8. Pause and resume the same Workflow with the persisted canonical request for approval or browser login.
9. Produce one `WorkflowResult`, including declared file/image outputs.
10. Persist traces and return the result through the resolved Endpoint.

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
lanes, or arbitrary fields. Before this router runs, the Fast normalization
pass covers every owner request. A rewrite is accepted only when URLs, paths,
numbers, quoted literals, negation, and explicit delivery targets remain
grounded; otherwise the runtime uses the deterministic original-text fallback.
Relative-time public searches may add only the supplied current date. The
accepted request and resource projection are persisted and reused by TaskHint,
Workflow/ReAct execution, final grounding, approval resume, and browser-login
resume. Search queries are additionally materialized from the frozen route slot
into the tool call, so workflow-time models cannot rewrite them. The catalog
validates every path edge and leaf operation.

Request normalization and capability routing always use the Fast lane. After a
matched capability is dispatched, every model step inside that persisted
Workflow uses the Deep lane, including later stages and approval resumes. This
lane boundary changes only model selection: Workflow/ReAct context construction,
tool-result messages, observation ordering, compaction, and grounding keep the
shared execution pipeline. Explicitly `unmatched` requests remain on the
transitional TaskHint/ReAct lane selected for that request.

Catalog revision `2026-07-21.v5` has six production leaves:
`browser.internet_search`, `browser.weather`, `browser.automation`,
`browser.interaction`, `document.read`, and `document.edit`.
`WorkflowProfileRegistry.Resolve` maps each leaf to its exact revision 1
Workflow; it performs no intent matching. The Dispatcher persists the
`RouteDecision`, `ReturnRoute`, validated plan digest, and node state.

`browser.internet_search` owns every read-only fact whose answer depends on
current Internet state, including gold prices, exchange rates, stock or index
quotes, immediate news, current match results, and schedules. These examples do
not become vertical leaves. Fast expresses the boundary with the typed
`fact_scope=current_internet_state`; stable common knowledge stays `unmatched`.
`browser.weather` is a fixed three-stage Workflow. Before routing, weather
normalization appends the card's retrieval requirements once: current condition
and temperature, optional same-day low/high values, and zero to five available
future hourly date-time/condition/temperature entries. It then sends that frozen
route query unchanged through `info.query`. A bounded query-relevant projection
of mapped Info evidence enters the next Deep call with stable `summary:0`, `fact:N`, and
`source:N:snippet:M` refs. The Deep lane submits only fields backed by quoted
text from the referenced evidence item to `weather.structure_payload`. The
validator tolerates Markdown-marker and whitespace-only differences while
requiring the same value and unit, then passes
the resulting governed ref to the network-free `media.render_weather_card`
renderer. The runtime materializes query, location, and both outcome refs from
persisted state before execution. Current condition, current temperature,
same-day range, and future hours must each contain supported values or an
explicit `missing_fields` marker. An unsupported optional daily or hourly
section is removed and marked missing without discarding independently verified
current data. Missing values render as unavailable and are never inferred or
substituted. Successful runs project one image through the ordinary
Delivery Gateway; failures do not fall back to search, Open-Meteo, or ReAct.
Weather warnings, news, historical research, source comparison, and air-quality
research remain on `browser.internet_search`, while a direct weather request
without a grounded location returns clarification.

`browser.automation` revision 1 remains the narrow open/focus contract.
`browser.interaction` revision 1 owns explicit click requests against one
managed current tab or one frozen URL. It checks Playwright health, resolves a
reusable tab, then runs a structured snapshot/click/post-snapshot/verification
loop. The fixed nine-tool scope remains visible for the Workflow lifetime, while
stage capability rules reject out-of-order tools. Snapshot refs bind page,
snapshot, element fingerprint, and the archived result; a successful click
invalidates its source snapshot. Each click is approval-free but must be
verified before another click. Repeated states fail immediately and the third
still-in-progress click fails with `interaction_attempt_limit`. Type, select,
login, credentials, upload/download, payment, form submission, screenshots, and
arbitrary script execution remain outside this revision.

ToolHub capability metadata is the model-visibility authority. Tool Exposure
materializes the complete fixed scope for the selected Workflow, subject to
Policy. Workflow plans contain no Skill IDs and Workflow model prompts load no
Skill text; TaskHint candidates and outcomes cannot widen the scope.
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
[profile catalog](intent-routing-workflow-domain-profiles.md), and
[current capability matrix](workflow-capabilities.md).

### ToolHub

ToolHub registers bounded tools and validates successful outputs against declared contracts. Current tools:

| Area | Tools |
|---|---|
| Files | `files.search`, `files.read`, `files.write_draft`, `file.delete` |
| Documents | `office.replace_text`, `docx.*`, `xlsx.*`, `pptx.*`, `pdf.extract_text`, `pdf.transform` |
| Memory | `memory.search`, `memory.write_candidate`, `memory.propose`, `memory.write_sensitive` |
| Browser and Info | `web.search`, `info.query`, `weather.structure_payload`, `media.render_weather_card`, `browser.read`, `browser.status`, `browser.list_tabs`, `browser.open`, `browser.focus`, `browser.close`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, `browser.wait`, `browser.click`, `browser.verify`, `browser.type`, `browser.select` |
| Code/shell | `shell.exec_sandboxed`, `code.apply_patch` |
| Approval/notify | `notify.ask_approval` |

Risk levels are `read`, `draft`, `reversible` and `dangerous`. Read/draft tools can run when policy allows. Reversible and dangerous tools require approval in the MVP.

### Document Workflow

`document.read` and `document.edit` keep one fixed orchestration contract even
when the underlying strategy changes:

1. inspect the governed regular file, verify signature-bearing formats, and
   record size, media type, modification time, and SHA-256 metadata;
2. select a registered parser by canonical detected format;
3. normalize the complete parse into `structured_document_v1`, with stable
   document, block, paragraph, section, sheet, row, cell, slide, and page IDs
   plus source locations and necessary format metadata;
4. resolve the requested text or structural location and reject missing,
   ambiguous, or unexpected match counts; row and slide locators select one
   stable structural entity rather than expanding into their child blocks;
5. apply only the constrained mutation to one or more new output paths,
   inspect every reported output, re-hash the input, and return an auditable
   `change_summary` with `output_paths` proving that the original remained
   unchanged. Invalid or zero-change results remove generated output copies.

The current `small_file_v1` strategy accepts source files up to 8 MiB and a
complete extracted representation up to 200,000 bytes. It supports text,
DOCX, XLSX, PPTX, and text PDFs for reads, and the registered DOCX, XLSX,
PPTX, and PDF copy-edit operations. Exceeding either limit returns typed
`strategy_deferred`; unsupported formats and locators return their own typed
errors. Adapters must never truncate and report success.

`internal/document.Pipeline` owns the stage order, while strategies own parser
and editor registries. A future chunked, streaming, indexed, or lazy strategy
implements the same `Strategy` interface; profiles and Runtime do not branch on
that choice. ToolHub registration remains the sole model-visible capability
boundary, and document content remains untrusted evidence. Structured results
flow through existing ToolCall observation and ArtifactStore archiving; this
small-file slice does not introduce an optional DocumentStore capability.

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
- `MessageEnvelope`
- `RequestNormalization`
- `MessageEndpoint`
- `MessageSchedule`
- `WorkflowResult`
- `DeliveryRequest`
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

Browser web access uses `web.search` for discovery and `browser.read` for read-only source-page extraction. Browser automation uses Microsoft Playwright `launchPersistentContext` to launch its installed, version-matched Chromium (or one explicit custom override) with a SparkClaw-owned persistent profile: ordinary work is headless, while login/captcha/2FA/payment and similar human-only steps temporarily switch the same profile to visible Chromium. Visible and hidden processes never own the profile concurrently. Login state remains inside Chromium rather than being exported as JavaScript cookies, and continuation uses the selected post-login URL even when its origin differs from the original page. `browser.read` waits for rendered DOM state, captures rendered HTML, and passes that HTML through Readability before returning article text. Structure snapshots are an on-demand follow-up when body extraction is insufficient or page controls matter. `browser.interaction` consumes a bounded structured control projection, treats page text as untrusted evidence, and requires a new post-click snapshot plus `browser.verify` before completion or another click. The focused roadmap is maintained in [Browser Automation Improvement Plan](browser-automation-improvement.md), the transport contract in [Playwright Browser Automation Migration](playwright-browser-automation-migration.md), and profile lifecycle details in [Managed Shared Chromium Profile](managed-persistent-browser-profile.md). Browser observations refuse loopback/private literal hosts by default where URL fetching is involved, archive rendered HTML/raw responses or screenshots, and stay untrusted evidence. Local fixture hosts such as `127.0.0.1` or `host.docker.internal` must be explicitly allowlisted. The runtime must stop for human-only verification and must not invent logged-in evidence.

The Fast router may choose only registered semantic leaves and remains
tool-neutral. Normalization freezes current-owner search queries, grounds
weather locations, rejects unknown fields and unregistered edges, and never
allows Fast to invent URL/path facts. For a pure semantic query leaf, typed
resource metadata with no Workflow binding is discarded before catalog
validation instead of blocking the query. Exact leaf identity still resolves to the
exact Workflow; invalid or failed matched routes never fall back to another
Workflow or ReAct.

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
