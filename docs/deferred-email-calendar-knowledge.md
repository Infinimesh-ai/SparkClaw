# Deferred Email Expansion, Calendar, and Knowledge Capabilities

> Language: English | [简体中文](../zh-cn/docs/deferred-email-calendar-knowledge.md)

## Current Status

The generic email, calendar, and workspace knowledge/RAG prototypes were
removed from the active SparkClaw architecture on 2026-07-16. They were
cross-layer placeholders rather than complete product capabilities.

On 2026-09-03, SparkClaw introduced a new and deliberately narrower active
email slice: `browser.email` r1 can send one approved plain-text message through
a freshly validated configured QQ Mail, Outlook, or Gmail browser account. Its
login, Browser Bridge provider-handler, approval, and unknown-outcome contracts are
defined in [Browser email Workflow](browser-email-workflow-design.md).

That send-only capability does not reactivate the old personal-data connector.
Email reading and all broader mailbox operations remain deferred. Calendar and
built-in workspace knowledge/RAG also remain deferred.

The standalone Embedding lane remains active for semantic routing. It is not
owned or extended by the removed knowledge prototype.

## Retired Prototype Surface

The 2026-07-16 removal covered:

- legacy personal-data email operations such as search, thread reading, draft
  reply, and mock send;
- calendar read, propose-event, and create-event operations;
- knowledge workspace indexing and search operations;
- file-fixture and assumed HTTP adapters under `internal/personaldata`;
- mock outbox and created-event logs;
- the local keyword index, optional embedding/reranking glue, document/chunk
  state, and PostgreSQL vector schema;
- dedicated routing heuristics, mock-model actions, answer formatters, Skills,
  configuration rows, policy entries, and golden cases.

The current ToolHub name `email.send` is a new strict browser-bound tool. It can
run only inside `browser.email` after fresh admission and exact-content owner
approval. It is not compatible with the retired mock/HTTP adapter contract.

## Why The Old Prototypes Were Insufficient

### Generic Email

The file adapter searched JSON fixtures and appended sends to a local JSONL
file. The HTTP adapter assumed provider endpoints without defining account
authorization, mailbox identity, pagination, MIME, attachments, draft state,
delivery semantics, provider error mapping, or reconciliation after an unknown
send result. Approval around a mock append did not make it a real email system.

The active browser send slice closes only the bounded one-recipient send
contract. It does not imply that inbox reading, search, replies, attachments,
draft synchronization, or multi-account semantics have been designed.

### Calendar

The file adapter filtered fixture strings and appended created events. The HTTP
adapter assumed a generic event endpoint. There was no account lifecycle,
provider capability model, timezone/daylight-saving contract, recurrence model,
attendee/update semantics, conflict policy, idempotent create key, or
reconciliation after partial failure.

### Knowledge/RAG

The implementation combined workspace crawling, text chunking, a local JSON
index, optional embeddings, reranking, artifact archival, and three storage
backends behind two tools. It lacked a corpus model, source ownership and access
rules, supported-format policy, incremental update/delete lifecycle, embedding
migration, quality and latency budgets, operator observability, and a stable
citation contract.

## Existing Data

The 2026-07-16 removal did not automatically delete historical data:

- old file-state `documents` and `document_chunks` fields are ignored when
  state is loaded;
- existing `.sparkclaw/knowledge.json`, personal-data fixtures, mock drafts,
  outbox/event logs, and archived knowledge artifacts remain ordinary files
  until an operator backs them up or removes them;
- existing PostgreSQL `documents` and `document_chunks` tables are not dropped
  by startup migration, while fresh databases do not create them.

The active browser email provider settings are separate non-secret records.
They contain enable/default state, masked readiness metadata, and versions;
provider authentication remains only in the dedicated Chromium profile.

## Future Expansion Gate

Any new email-reading or mailbox-management capability, calendar capability, or
workspace knowledge capability must begin with a focused design and land as a
complete vertical slice. At minimum it must define:

1. Owner/account identity, authorization, credential ownership, lifecycle, and
   trust boundaries.
2. Provider-neutral contracts, error taxonomy, deadlines, retry/idempotency
   rules, and reconciliation for uncertain external effects.
3. Policy and approval semantics based on real provider behavior.
4. Storage ownership and migrations across memory, the default file backend,
   and PostgreSQL, including deletion and upgrade behavior.
5. Catalog, semantic routing, Workflow Profile, Tool Exposure, and result
   projection without parallel name lists or generic fallback execution.
6. End-to-end tests under the default configuration plus deterministic provider
   contract tests and operator-visible health states.
7. For email expansion: read side effects, mailbox/message identity, MIME and
   attachment boundaries, reply/draft behavior, and account selection.
8. For knowledge: corpus lifecycle, formats, incremental indexing,
   embedding/version migration, retrieval evaluation, citations, and resource
   budgets.

Until those gates are met, keep the active browser email send slice, browser
page workflows, file/document workflows, calendar requests, and memory as
separate bounded domains. Do not recreate the retired prototypes as placeholders.
