# SparkClaw Documentation

> Language: English | [简体中文](../zh-cn/docs/index.md)

This index lists the current documentation set. Documents describe the active
runtime and supported extension boundaries; completed migration plans and
superseded implementation proposals belong in Git history, not in the active
documentation tree.

## Start Here

| Document | Purpose | Authority |
|---|---|---|
| [README](../README.md) | Project overview, quick start, and current status | Product entry point |
| [Architecture](architecture.md) | Product boundary, runtime topology, ownership, and invariants | System source of truth |
| [Deployment](deployment.md) | Local, Compose, DGX Spark, state, backup, and troubleshooting | Operator guide |
| [Development](development.md) | Repository map, implementation rules, validation, and extension workflow | Contributor guide |
| [Workflow capability matrix](workflow-capabilities.md) | Exactly what the current Workflow runtime can execute | User-visible capability inventory |

## Runtime Guides

| Document | Scope |
|---|---|
| [Workflow execution](workflow-execution.md) | Workflow-native execution pipeline, the step loop and its protocol, budgets, resume semantics, and extension points |
| [JingSi Runtime v1 provider](jingsi-runtime-v1.md) | Dedicated authenticated submit/lookup/status/cancel/events surface, durable request-key reconciliation, scope projection, recovery, and operator configuration |
| [Workflow evidence ownership and reuse](workflow-evidence-ownership.md) | Active Runtime/model ownership migration, single-acquisition multi-consumer reuse, typed locator binding, and profile migration gates |
| [Intent routing](intent-routing.md) | Semantic graph, embedding and Fast/Tree fusion, Top-2 grounding, and one-leaf dispatch |
| [Bounded context history retrieval and invocation snapshot design](context-history-query-design.md) | Accepted one-shot bounded Store reads, fixed 8/6/4/3 selection, and one reusable in-process invocation snapshot without cursors or persisted anchors |
| [Tree same-session context consistency design](tree-session-context-parity-design.md) | Accepted shared selected-history source and whole-prompt admission, plus implemented strict Tree scoring JSON structure hardening |
| [Model input and output capacity contract](model-capacity-contract-design.md) | Accepted physical-window and output-capability-class contract, oversized-question rejection, final admission, completion handling, and fail-fast profile authority |
| [Messaging and scheduling](messaging-and-scheduling.md) | Message ingress, Endpoint/Schedule registries, Delivery Gateway, Web direct sends, and Timer execution |
| [Browser runtime](browser-runtime.md) | agent-browser transport, managed Chromium profiles, browser Workflow boundaries, and security |
| [Document workflows](document-workflows.md) | Structured reads, bounded edits, enrichment, preservation, and format coverage |
| [External integrations](integrations.md) | LocalMind task MCP, Telegram, Weixin, speech transcription, and Infinimesh Info |
| [LocalMind Workflows](localmind-task-workflow-design.md) | Implemented explicit text delegation with read/write approval separation, bounded status-query completion, contextual query, and cancel |
| [WebChat voice input design](webchat-voice-input-design.md) | Phase 1 stable capture loop and Phase 2 native record-time ASR, partial/final reconciliation, silence stop, and batch fallback design; LLM polish deferred to Phase 3 |
| [WebChat voice Phase 2 design](webchat-voice-phase2-design.md) | Normative native record-time Qwen ASR transport, revisioned partial/final output, silence auto-stop, complete-WAV fallback, and acceptance gates |
| [JingSi LAN Web client connection](jingsi-lan-connection-design.md) | SparkClaw side implemented for one server-bound WebChat session, text send, and filtered realtime/catch-up message projection on a dedicated allowlisted LAN port; JingSi client work and physical validation remain pending |
| [ISCP Bridge](iscp-bridge.md) | Current shared Bridge; LocalMind use is legacy, while JingSi use remains until its direct-LAN client and physical validation are complete |
| [Unified third-party ISCP MCP access](unified-third-party-access-design.md) | Implemented local Route MCP runtime with separate ISCP pairing and SparkClaw MCP access tickets; production provisioning, external gateway validation, and LocalMind legacy removal remain pending; JingSi excluded |
| [Generic external MCP safeguards](generic-mcp-safeguards-design.md) | Generic catalog filtering/classification plus bounded redacted results and approval persistence shared with the fixed LocalMind task adapter |
| [Per-owner connector activation](connector-owner-runtime-design.md) | Accepted issue #13 design for owner-isolated settings, shared channel workers, cache coherence, drain semantics, and restart reconciliation |
| [WebChat](webchat.md) | Owner workbench responsibilities, API ownership, refresh model, and frontend verification |
| [Settings and integration configuration](settings-integration-configuration-design.md) | Implemented settings directory and encrypted household multi-credential configuration for Info and outbound LocalMind MCP |

## Operations And Governance

| Document | Scope |
|---|---|
| [Model loading](model-loading.md) | Single-machine and multi-machine model-loading strategy and validation status |
| [Issue #15 deployment startup reliability](issue-15-deployment-reliability-design.md) | Implemented state-backend, model reconciliation, configurable WebChat port, self-contained readiness, and finite systemd startup contract |
| [Issue #16 tool-policy approval](issue-16-external-media-approval-design.md) | Implemented ToolDefinition/Policy boundary for external-MCP-AI workspace access without treating the local model as an external principal |
| [Issue #18 document operation contract](issue-18-document-operation-contract-design.md) | Implemented canonical format-operation catalog, source-hash contract, and runtime-only provenance boundary |
| [Issue #20 god-file splits](issue-20-god-file-split-design.md) | Implemented behavior-preserving panel, CSS, i18n, ToolHub test, and embedded PPTX package split |
| [Model baseline](../benchmarks/model_baseline.md) | Measured model endpoint evidence and operating limits |
| [Chat model comparison (2026-08-25)](../benchmarks/model_comparison_2026-08-25.md) | Production-shaped Fast/Deep quality, latency, residency, and promotion evidence for four local chat models |
| [Engineering baseline](engineering-baseline.md) | Non-negotiable implementation rules |
| [Refactoring playbook](refactor-playbook.md) | Periodic architecture review procedure |
| [Store](store.md) | Typed repositories, risk-tiered reliability, three backends, embedded PostgreSQL migrations, Runtime supervision, source layout, and verification; the shipped S0-S5 migration's durable rules live here and the stage plans live in Git history |
| [ASR runtime CI](asr-runtime-ci-design.md) | Independent lightweight fake-model ASR dependency, protocol-test, cleanup, and CI contract |
| [Context assembly plan](context-assembly-plan.md) | Proposed Phase 0–1 optimization of prompt assembly and tool-result composition |
| [Info aggregated result consumption](info-aggregate-result-consumption-design.md) | Implemented typed, non-reaggregating consumption of Info `answer_context`, including citation, limitation, and Info-final browser-order contracts |
| [PPTX final-render visual quality gate](pptx-final-render-visual-qa-design.md) | Phase 1 shadow implementation of pinned LibreOffice/pypdfium2/configured-Fast changed-page review; automatic repair and sealed publication remain gated |
| [DOCX editing](docx-editing-optimization.md) | Current DOCX style verification, evidence binding, run preservation, coverage, target-aware decision projection, and eval contracts |
| [Observation compression redesign](observation-compression-redesign.md) | Implemented uniform tool-result envelope, runtime evidence provisioning, and lossless compaction |
| [Deferred capabilities](deferred-email-calendar-knowledge.md) | Removed email, calendar, and workspace knowledge prototypes and reintroduction gates |

Repository process is documented in [Contributing](../CONTRIBUTING.md),
[Security](../SECURITY.md), [Support](../SUPPORT.md), and
[Changelog](../CHANGELOG.md).

## Documentation Rules

- `architecture.md` owns cross-component boundaries. A component guide may
  explain its implementation but must not redefine those boundaries.
- `workflow-capabilities.md` lists only registered, executable Workflow
  Profiles. A registered ToolHub tool alone is not a supported user feature.
- `deployment.md` owns commands and environment setup. Component guides link to
  it instead of copying full deployment procedures.
- Current behavior is written in present tense. Plans that have completed or
  been replaced are removed after their durable decisions are merged here.
- Every English Markdown document has a Simplified Chinese mirror under
  `zh-cn/`, and both versions link to each other.
- Code, schemas, generated API types, and tests remain the executable authority.
  Documentation changes with the same patch whenever a public contract changes.
