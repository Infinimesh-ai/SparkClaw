# Deferred Email, Calendar, and Knowledge Capabilities

> Language: English | [简体中文](../zh-cn/docs/deferred-email-calendar-knowledge.md)

## Status

Email, calendar, and workspace knowledge/RAG were removed from the active SparkClaw architecture on 2026-07-16. They were prototype shells rather than designed product capabilities, and keeping them registered made the runtime, storage layer, policy, UI, and eval matrix look more complete than they were.

They are not available through ToolHub, Agent Runtime routing, Skills, public configuration, WebChat settings, or the golden eval suite. The owner profile may still contain an email-shaped identity field; that field is profile metadata and is not an email integration. Authenticated personal sites may still be accessed through the governed browser workflow; that does not restore a dedicated email or calendar connector.

The standalone Embedding lane remains part of the architecture and configuration for semantic routing. The removed knowledge prototype does not own or extend that lane.

## What Was Removed

The removed surface included:

- Tool contracts and executors: `email.search`, `email.read_thread`, `email.draft_reply`, `email.send`, `calendar.read`, `calendar.propose_event`, `calendar.create`, `knowledge.index_workspace`, and `knowledge.search`.
- File-fixture and assumed HTTP adapters under `internal/personaldata`, including mock outbox and created-event logs.
- The local keyword index, embedding/reranking glue, `DocumentStore`, document/chunk state, and PostgreSQL document/vector schema.
- TaskHint heuristics, mock-model actions, grounded answer formatters, schema/repair special cases, and approval labels dedicated to these tools.
- The `email_triage` and `calendar_assistant` Skills, personal-data fixtures, WebChat adapter rows, environment variables, policy entries, and dedicated unit/golden cases.

The source history remains the code backup. This document preserves the previous boundary and the reasons it must not be restored piecemeal.

## Why the Prototypes Were Insufficient

### Email

The file adapter searched JSON fixtures and appended sends to a local JSONL file. The HTTP adapter assumed three endpoints but did not define account authorization, mailbox identity, pagination/cursors, MIME and attachment behavior, draft synchronization, delivery/idempotency semantics, provider error mapping, or reconciliation after uncertain sends. Approval around a mock append did not make this a real email capability.

### Calendar

The file adapter filtered fixture strings and appended created events. The HTTP adapter assumed a generic event endpoint. There was no account lifecycle, provider capability model, timezone and daylight-saving contract, recurrence model, attendee/update semantics, conflict policy, idempotent create key, or reconciliation after partial failures.

### Knowledge/RAG

The implementation combined workspace crawling, text chunking, a local JSON index, optional embeddings, reranking, artifact archival, and three storage backends behind two tools. It lacked a corpus/collection model, source ownership and access rules, format strategy, incremental update/delete lifecycle, embedding migration policy, quality/latency budgets, operator observability, and a stable citation contract. The result was substantial cross-layer coupling without a product-level design.

## Existing Data

Removal does not delete user data automatically.

- Old file-state `documents` and `document_chunks` fields are ignored when state is loaded.
- Existing `.sparkclaw/knowledge.json`, mock personal-data fixtures, drafts, outbox logs, event logs, and archived knowledge artifacts remain ordinary files until an operator backs them up or removes them.
- Existing PostgreSQL `documents` and `document_chunks` tables are not dropped by startup migration. Fresh databases no longer create them. Operators should export any needed data before dropping those tables manually.

## Reintroduction Gate

Any future implementation should begin with a focused design document and land as a complete vertical slice. At minimum it must define:

1. Owner/account identity, authorization, credential storage, connector lifecycle, and explicit trust boundaries.
2. Typed provider-neutral contracts, error taxonomy, timeout/retry/idempotency rules, and reconciliation for uncertain external effects.
3. Policy and approval semantics based on real provider behavior, not mock file writes.
4. Storage ownership and migrations across the default file backend, memory, and PostgreSQL, including deletion and upgrade behavior.
5. Intent/Profile/Tool Exposure integration without parallel tool-name routing lists.
6. End-to-end tests against the default configuration plus connector contract tests and operator-visible health states.
7. For knowledge specifically: corpus lifecycle, supported formats, incremental indexing, embedding/version migration, retrieval quality evaluation, citation guarantees, and resource budgets.

Until those gates are met, use existing file search/read, browser workflows, and memory as separate bounded capabilities rather than recreating these names as placeholders.
