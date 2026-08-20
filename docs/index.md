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
| [Workflow evidence ownership and reuse](workflow-evidence-ownership.md) | Active Runtime/model ownership migration, single-acquisition multi-consumer reuse, typed locator binding, and profile migration gates |
| [Intent routing](intent-routing.md) | Semantic graph, embedding and Fast/Tree fusion, Top-2 grounding, and one-leaf dispatch |
| [Messaging and scheduling](messaging-and-scheduling.md) | Message ingress, Endpoint/Schedule registries, Delivery Gateway, Web direct sends, and Timer execution |
| [Browser runtime](browser-runtime.md) | agent-browser transport, managed Chromium profiles, browser Workflow boundaries, and security |
| [Document workflows](document-workflows.md) | Structured reads, bounded edits, enrichment, preservation, and format coverage |
| [External integrations](integrations.md) | LocalMind task MCP, Telegram, Weixin, speech transcription, and Infinimesh Info |
| [WebChat voice input design](webchat-voice-input-design.md) | Phase 1 stable capture loop and Phase 2 native record-time ASR, partial/final reconciliation, silence stop, and batch fallback design; LLM polish deferred to Phase 3 |
| [WebChat voice Phase 2 design](webchat-voice-phase2-design.md) | Normative native record-time Qwen ASR transport, revisioned partial/final output, silence auto-stop, complete-WAV fallback, and acceptance gates |
| [JingSi LAN Web client connection](jingsi-lan-connection-design.md) | SparkClaw side implemented for one server-bound WebChat session, text send, and filtered realtime/catch-up message projection on a dedicated allowlisted LAN port; JingSi client work and physical validation remain pending |
| [ISCP Bridge](iscp-bridge.md) | Current shared Bridge; LocalMind use is legacy, while JingSi use remains until its direct-LAN client and physical validation are complete |
| [Unified third-party ISCP MCP access](unified-third-party-access-design.md) | Implemented local Route MCP runtime with separate ISCP pairing and SparkClaw MCP access tickets; production provisioning, external gateway validation, and LocalMind legacy removal remain pending; JingSi excluded |
| [Generic external MCP safeguards](generic-mcp-safeguards-design.md) | Generic catalog filtering/classification plus bounded redacted results and approval persistence shared with the fixed LocalMind task adapter |
| [Per-owner connector activation](connector-owner-runtime-design.md) | Accepted issue #13 design for owner-isolated settings, shared channel workers, cache coherence, drain semantics, and restart reconciliation |
| [WebChat](webchat.md) | Owner workbench responsibilities, API ownership, refresh model, and frontend verification |

## Operations And Governance

| Document | Scope |
|---|---|
| [Model loading](model-loading.md) | Single-machine and multi-machine model-loading strategy and validation status |
| [Issue #15 deployment startup reliability](issue-15-deployment-reliability-design.md) | Implemented state-backend, model reconciliation, configurable WebChat port, self-contained readiness, and finite systemd startup contract |
| [Issue #16 tool-policy approval](issue-16-external-media-approval-design.md) | Implemented ToolDefinition/Policy boundary for external-MCP-AI workspace access without treating the local model as an external principal |
| [Issue #18 document operation contract](issue-18-document-operation-contract-design.md) | Implemented canonical format-operation catalog, source-hash contract, and runtime-only provenance boundary |
| [Issue #20 god-file splits](issue-20-god-file-split-design.md) | Implemented behavior-preserving panel, CSS, i18n, ToolHub test, and embedded PPTX package split |
| [Model baseline](../benchmarks/model_baseline.md) | Measured model endpoint evidence and operating limits |
| [Engineering baseline](engineering-baseline.md) | Non-negotiable implementation rules |
| [Refactoring playbook](refactor-playbook.md) | Periodic architecture review procedure |
| [Store reliability migration roadmap](store-contract-reliability-migration-design.md) | Draft stage order and mandatory design/implementation review gates; large-file splits are deferred until Store closeout |
| [Store contract foundation](store-contract-foundation-design.md) | S0 inventory, repository ownership, error/context contract, mutation matrix, and characterization gate |
| [Store S0 contract inventory](store-s0-contract-inventory.md) | Executable 141-method ownership, backend state, production consumer, mutation, and pilot evidence |
| [Store S0 PostgreSQL reconciliation manifest](store-s0-postgresql-reconciliation-manifest.md) | Constraint-aware root migration versus embedded schema comparison and explicit parser limits |
| [Store S0 baseline and acceptance report](store-s0-acceptance-report.md) | Entry baseline, verification evidence, timeout basis, unresolved risks, and pending human review record |
| [File Store durability](store-file-durability-design.md) | S2 read isolation, context-aware transaction gate, pilot repository, durable replacement, rollback, and unknown-outcome contract |
| [PostgreSQL schema and Store configuration](store-postgresql-schema-config-design.md) | S1 embedded migration authority, ledger adoption, strict Store configuration, and unchanged DSN-gated CI setup |
| [Store repository migration](store-repository-migration-design.md) | S2 pilot, S3 one-repository waves across all backends/callers, and S4 removal of the broad Store interface |
| [Store Runtime and supervision](store-runtime-supervision-design.md) | S5 assembly-only Runtime, finite supervision, health, probes, metrics, and lifecycle |
| [Context assembly plan](context-assembly-plan.md) | Proposed Phase 0–1 optimization of prompt assembly and tool-result composition |
| [Info aggregated result consumption](info-aggregate-result-consumption-design.md) | Implemented typed, non-reaggregating consumption of Info `answer_context`, including citation, limitation, and Info-final browser-order contracts |
| [Resilient PPTX overlength adaptation](pptx-overlength-resilience-design.md) | Phase 0 No-Go report and bounded render-check design; production behavior remains unchanged |
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
