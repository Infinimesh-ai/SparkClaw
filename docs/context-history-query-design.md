# Bounded Context History Retrieval And Invocation Snapshot Design

> Language: English | [简体中文](../zh-cn/docs/context-history-query-design.md)

Status: Implemented 2026-08-31 after acceptance and a minimum-architecture
review.

This design closes the unbounded Store-read problem in Agent context assembly.
SparkClaw keeps complete durable session history, but a model invocation reads
only bounded recent candidates and selects the existing fixed context of 8
messages, 6 tool calls, 4 episode summaries, and 3 images once.

The [model capacity contract](model-capacity-contract-design.md) owns token
admission. The [context assembly plan](context-assembly-plan.md) owns semantic
sections and rendering. This document owns only historical acquisition,
selection into an invocation-owned value, and reuse of that value.

## 1. Accepted Decisions

| Concern | Accepted decision |
|---|---|
| Durable history | Keep complete messages, tool calls, episodes, runs, and audit records; prompt optimization must not delete product history |
| Existing complete-list APIs | Preserve `ListMessages`, `ListToolCalls`, and `ListEpisodeSummaries` for consumers that really need complete history |
| Agent hot path | Add three session-required, newest-first, one-shot recent queries with a hard scan limit |
| Pagination | Do not add a general cursor or page abstraction; each history stream executes at most one bounded query per invocation |
| Selection | Keep the current semantic filters and fixed 8/6/4/3 selected counts |
| Snapshot | Build one immutable in-process history value and pass it to Tree, Workflow, final-answer assembly, and recent-document resolution |
| Resume | Rebuild at most once in each resumed invocation using the original run cutoff and persisted source-turn ID; do not persist a duplicate prompt or history anchor |
| Backends | PostgreSQL uses `ORDER BY ... LIMIT`; the in-memory backend maintains derived session order; File rebuilds that derived state from its existing snapshot |
| Memory product | Never query `MemoryRepository`; its current records are a scaffold and remain outside Agent context |
| External MCP | Return an empty snapshot before issuing any history query |

## 2. Why The Current Problem Exists

The problem is not that the database records every event. Complete durable
records are needed for conversation display, audit, resume, delivery, feedback,
and other product behavior. The problem is that model-context construction uses
complete-list repository methods and applies its small limits only after all
matching rows have crossed the Store boundary:

```text
durable session history
        |
        | ListMessages / ListToolCalls / ListEpisodeSummaries
        v
all matching rows are read, sorted, transferred, and decoded
        |
        v
Agent filters to 8 messages / 6 tools / 4 episodes / 3 images
```

As a session grows, PostgreSQL performs increasingly large sorts and result
transfers. The in-memory backend clones and sorts complete collections, and the
File backend delegates to the same in-memory path after loading its snapshot.
Tree, Workflow, final-answer assembly, and recent-document fallback can also
rebuild or reread overlapping history during one run.

A larger model context window does not solve this. The wasted Store work occurs
before token admission, and the product still selects only fixed recent
context. The correct boundary is therefore a bounded data-access contract, not
deletion of durable records and not expansion of model-visible history.

## 3. Scope And Non-Goals

This design covers historical inputs currently used by
`buildAgentContextSnapshot` and recent-document fallback:

- prior same-session owner and assistant messages;
- image attachments derived from those messages;
- terminal tool calls from prior runs;
- persisted episode summaries;
- reuse of those candidates and selected records during one invocation.

It does not:

- change message, tool-call, episode, run, or audit retention;
- add generic Store pagination for UI, export, or administration;
- perform semantic search, embedding retrieval, RAG, or history summarization;
- increase the fixed 8/6/4/3 context selection;
- persist rendered prompts, selected record copies, cursors, selection
  revisions, or run-history anchors;
- query the product Memory repository;
- define ContextBuilder degradation or model token budgets.

## 4. Minimal Repository Contract

The Agent hot path adds narrow methods to the repositories that already own
the corresponding records:

```go
ListRecentMessages(
    ctx context.Context,
    sessionID string,
    cutoff time.Time,
    excludeMessageID string,
    scanLimit int,
) ([]app.Message, error)

ListRecentToolCalls(
    ctx context.Context,
    sessionID string,
    cutoff time.Time,
    excludeRunID string,
    scanLimit int,
) ([]app.ToolCall, error)

ListRecentEpisodeSummaries(
    ctx context.Context,
    sessionID string,
    cutoff time.Time,
    scanLimit int,
) ([]app.EpisodeSummary, error)
```

Each method has one meaning:

- `sessionID` is required and exact; an empty value is invalid;
- `cutoff` is required and inclusive;
- `excludeMessageID` and `excludeRunID` are required for Agent acquisition;
  exclusion is applied in Store, before `LIMIT`;
- `scanLimit` is a required positive implementation limit with a configured
  maximum; zero is invalid and never means unlimited;
- results are newest-first in deterministic timestamp-plus-ID order;
- returned values are cloned under the existing repository ownership rules;
- cancellation, timeout, and typed Store errors match the existing repository
  contract.

There is no `HistoryCursor`, `Page`, `Next`, or offset. Agent does not need a
general browsing protocol: it needs a bounded recent candidate set once. If
eligible records are sparse, selection returns fewer than its quota rather
than performing an unbounded search for older records.

Initial scan ceilings are implementation constants, independent of model
capacity:

| Stream | Maximum candidates read | Maximum model-visible selection |
|---|---:|---:|
| Messages and their images | 256 | 8 messages and 3 images |
| Tool calls | 128 | 6 tool calls |
| Episode summaries | 64 | 4 episodes |

These ceilings leave room for semantic filtering without turning context
selection into a complete-history scan. Changing them requires Store latency
and long-session reference evidence; changing a model's physical window does
not change them.

## 5. Eligibility And Stable Boundaries

For an initial run:

```text
cutoff            = AgentRun.StartedAt
excludeMessageID  = current userMessage.ID
excludeRunID      = current AgentRun.ID
```

For a resumed Workflow invocation:

```text
cutoff            = original AgentRun.StartedAt
excludeMessageID  = Workflow Intent.SourceTurnID
excludeRunID      = original AgentRun.ID
```

Using the original cutoff prevents messages, episodes, or other-run activity
created after execution began from entering a resumed prompt. Explicitly
excluding the source turn prevents the unchanged owner question from appearing
both as fixed current input and as historical conversation. Excluding the run
prevents current-run tool results from leaking into cross-run context; they
remain available through the current-run observation path.

The Store-side ordering and eligibility are:

- messages: `created_at <= cutoff`, excluding `excludeMessageID`, ordered by
  `(created_at DESC, id DESC)`;
- tool calls: prior-run terminal records with `started_at <= cutoff` and
  `completed_at <= cutoff`, excluding `excludeRunID`, ordered by
  `(started_at DESC, id DESC)`;
- episodes: `created_at <= cutoff`, ordered by
  `(created_at DESC, id ASC)` to preserve the existing repository contract.

Only `user` and `assistant` messages with content or eligible attachments enter
the selected conversation. Existing image type checks, tool projection rules,
and episode validity rules remain Agent semantics. The repository provides
bounded candidates; it does not learn prompt policy.

The boundary is deterministic for normal immutable history and terminal tool
records. Administrative backfill or correction of records older than the
cutoff may change a rebuilt snapshot; exact audit replay is not a goal and must
use durable run/audit records rather than this transient context value.

## 6. Backend Implementation

### PostgreSQL

Each method is one indexed `ORDER BY ... LIMIT` query. The query applies
session, cutoff, exclusion, and terminal-state predicates before ordering and
limiting. Representative shapes are:

```sql
SELECT ...
FROM messages
WHERE session_id = $1
  AND created_at <= $2
  AND id <> $3
ORDER BY created_at DESC, id DESC
LIMIT $4;
```

```sql
SELECT ...
FROM tool_calls
WHERE session_id = $1
  AND run_id <> $2
  AND started_at <= $3
  AND completed_at IS NOT NULL
  AND completed_at <= $3
ORDER BY started_at DESC, id DESC
LIMIT $4;
```

Indexes should follow the filter and order used by the query. The migration
must be justified with `EXPLAIN (ANALYZE, BUFFERS)` against long-session
fixtures; an index is not added merely because a design names one.

### In-Memory Store Backend

The in-memory Store backend maintains per-session derived ordering for
messages, terminal tool calls, and episodes. Normal monotonic writes append;
replay or backfill inserts at the correct position; updates do not duplicate an
ID. A recent read locates the cutoff, walks only until `scanLimit`, and clones
only returned records.

This is backend indexing, not the product Memory feature. `MemoryRepository`
remains unused by Agent context.

### File Store Backend

File keeps its current durable snapshot as the single persisted representation.
On load or replacement it rebuilds the same derived in-memory ordering used by
the MemoryStore backend. It does not persist a second history index, cursor, or
selected snapshot, avoiding a new reconciliation and durability problem.

All three backends share repository contract tests for ordering, cutoff,
exclusion, limits, cloning, cancellation, and empty results.

## 7. Invocation-Owned Snapshot

After the owner-question gate succeeds, Runtime acquires bounded candidates at
most once and constructs one immutable value:

```go
type InvocationHistory struct {
    MessageCandidates  []app.Message
    ToolCandidates     []app.ToolCall
    EpisodeCandidates  []app.EpisodeSummary
    Selected           agentContextSnapshot
}
```

The exact type may remain private to `agent`; its ownership is the important
contract:

1. reject external-MCP history inheritance before any Store query;
2. resolve the initial or resume cutoff and exclusions;
3. issue one bounded query for each stream, concurrently where backend
   operation ownership permits;
4. run the existing semantic filters once and freeze selected 8/6/4/3 slices;
5. pass `Selected` explicitly to Tree, Workflow steps, conversation/final
   answer, and other model-context builders;
6. let recent-document fallback inspect the already loaded bounded candidates
   instead of calling `ListToolCalls` again.

Tree and Workflow therefore refer to the same selected historical records.
They are not required to render byte-identical prompts: each consumer owns its
semantic sections, labels, schema, and fixed instructions.

The value lives only for the current Runtime invocation. It is not a global
cache and has no invalidation protocol. Workflow steps reuse it while current-
run observations grow separately.

## 8. Resume Semantics

An approval or login resume starts a new Runtime invocation after the original
in-process value may have disappeared. Runtime reconstructs `InvocationHistory`
at most once using the persisted facts already needed by Workflow:

- original `AgentRun.StartedAt` supplies the cutoff;
- `Intent.SourceTurnID` supplies the current owner message exclusion;
- original `RunID` supplies current-run exclusion.

No `RunHistoryAnchor`, cursor, selection revision, or copied prompt is added to
the persistence schema. This keeps resume correctness tied to existing run
identity instead of introducing a second durable history representation.

Tree is not rerun on Workflow resume. The rebuilt selected snapshot is used by
the resumed Workflow and its final answer only. If required persisted facts are
missing or invalid, resume fails with a typed internal-state error; it must not
fall back to complete-history reads.

## 9. Failure And Observability

A recent-query failure fails context acquisition as the corresponding existing
Store failure does. The system does not silently substitute an empty snapshot,
except for the intentional external-MCP isolation rule.

Bounded telemetry records only safe metadata:

- backend and stream;
- candidates returned and configured scan ceiling;
- selected message, tool, episode, and image counts;
- whether the candidate ceiling was reached;
- initial versus resume invocation;
- query latency and typed failure code.

It never records history content, attachment paths, document names, query
arguments containing owner text, or selected record IDs. Reaching a candidate
ceiling is expected bounded behavior, not an automatic error and not a reason
to page further.

## 10. Implementation Record

### Implemented: characterize and register

- Lock current semantic eligibility and 8/6/4/3 selection fixtures.
- Register the three recent-query Store operations and their timeout/risk
  metadata.
- Add shared contract cases before changing Agent callers.

### Implemented: backend queries

- Implement PostgreSQL bounded queries and validate their plans.
- Add derived per-session ordering to the in-memory backend.
- Rebuild that derived state from the File snapshot without new persisted data.
- Keep complete-list methods unchanged for other consumers.

### Implemented: one acquisition path

- Build `InvocationHistory` after the owner-question gate.
- Pass the selected snapshot into Tree, Workflow, and final-answer assembly.
- Make recent-document fallback consume the same bounded candidates.
- Remove Agent hot-path calls to complete-list history methods.

### Implemented: resume

- Rebuild once from `AgentRun.StartedAt`, `Intent.SourceTurnID`, and `RunID`.
- Add missing-state and no-unbounded-fallback tests.

## 11. Verification And Acceptance

Implementation is accepted only when:

- long sessions keep complete durable records while Agent reads no more than
  the configured candidate ceiling for each stream;
- PostgreSQL query plans use bounded indexed access and do not sort or transfer
  complete sessions;
- in-memory and File reads do not clone complete session collections;
- all three backends return identical deterministic order and exclusion results;
- fixed 8/6/4/3 model-visible selection remains unchanged;
- Tree and Workflow consume the same selected record identities without a
  byte-parity requirement;
- recent-document fallback performs no second tool-history read;
- resume builds at most one snapshot and never uses a complete-list fallback;
- external-MCP invocation performs zero history queries;
- Memory repository records never enter Agent context;
- owner-question rejection happens before history acquisition;
- existing Policy, Approval, artifact authorization, and external-MCP
  isolation tests remain green.

Run focused Store contract and Agent snapshot tests, the complete Gateway
build/test/vet gate, default File coverage, configured PostgreSQL integration
when available, routing golden tests, and bilingual documentation checks.

## 12. Ownership Boundaries

- `ConversationRepository`: bounded recent-message read;
- `RunRepository`: bounded recent-tool and recent-episode reads;
- MemoryStore/FileStore/PostgreSQL: backend-specific bounded access and shared
  repository semantics;
- `internal/agent`: candidate eligibility, fixed selection, invocation value,
  and explicit consumer reuse;
- `internal/app`: existing run start and source-turn facts only; no new history
  persistence schema;
- capacity and ContextBuilder documents: token admission and rendering after
  this bounded acquisition completes.
