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
| [Intent routing](intent-routing.md) | Semantic graph, embedding and Fast/Tree fusion, Top-2 grounding, and one-leaf dispatch |
| [Messaging and scheduling](messaging-and-scheduling.md) | Message ingress, Endpoint/Schedule registries, Delivery Gateway, Web direct sends, and Timer execution |
| [Browser runtime](browser-runtime.md) | agent-browser transport, managed Chromium profiles, browser Workflow boundaries, and security |
| [Document workflows](document-workflows.md) | Structured reads, bounded edits, enrichment, preservation, and format coverage |
| [External integrations](integrations.md) | Telegram, Weixin, speech transcription, and Infinimesh Info |
| [ISCP Bridge](iscp-bridge.md) | JingSi App secure sessions, enrollment boundary, agent protocol, and GB10 operation |
| [WebChat](webchat.md) | Owner workbench responsibilities, API ownership, refresh model, and frontend verification |

## Operations And Governance

| Document | Scope |
|---|---|
| [Model loading](model-loading.md) | Single-machine and multi-machine model-loading strategy and validation status |
| [Model baseline](../benchmarks/model_baseline.md) | Measured model endpoint evidence and operating limits |
| [Engineering baseline](engineering-baseline.md) | Non-negotiable implementation rules |
| [Refactoring playbook](refactor-playbook.md) | Periodic architecture review procedure |
| [Context assembly plan](context-assembly-plan.md) | Proposed Phase 0–1 optimization of prompt assembly and tool-result composition |
| [DOCX editing](docx-editing-optimization.md) | Current DOCX style verification, evidence binding, run preservation, coverage, target-aware decision projection, and eval contracts |
| [XLSX workflow hardening plan](xlsx-workflow-hardening-plan.md) | Proposed XLSX evidence, mutation safety, package-preservation, and operation-selection hardening |
| [PPTX workflow optimization plan](pptx-workflow-optimization-plan.md) | Proposed routing, preservation, evidence, insertion, timeout, and validation improvements for PPTX workflows |
| [Observation compression redesign](observation-compression-redesign.md) | Implemented uniform tool-result envelope, runtime evidence provisioning, and lossless compaction |
| [PDF workflow optimization record](pdf-workflow-optimization-plan.md) | Implemented page-level coverage, scan classification, exact transforms, routing calibration, and OCR observability |
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
