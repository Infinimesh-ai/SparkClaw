# LocalMind Workflows

> Language: English | [简体中文](../zh-cn/docs/localmind-task-workflow-design.md)

> Status: implemented current-state component guide, 2026-08-26.

## Confirmed Product Boundary

LocalMind owns task planning, document discovery, agent execution, and task
state transitions behind its MCP boundary. SparkClaw owns only routing,
argument binding, authorization, invocation, waiting/resume, result validation,
and delivery.

LocalMind is explicit-only and is never a default executor. The owner request
must explicitly name LocalMind. Mentioning or discussing LocalMind without
assigning work remains ordinary conversation.

The initial transfer is text-only and contains only the current message text.
It does not include prior conversation history, memory, tool output, hidden
context, images, audio, video, or file attachments. `documentIds` is omitted.

## Capability Topology

The latest approval split supersedes the earlier three-leaf proposal.
`localmind` remains a first-level branch beside `document`, but now has four
functional leaves and four r1 Workflow Profiles:

```text
capability
  |- conversation
  |- browser
  |- document
  |- localmind
  |    |- localmind.read
  |    |- localmind.write
  |    |- localmind.query
  |    `- localmind.cancel
  |- schedule
  `- coding
```

Leaf ID and Workflow Profile ID are identical.

| Leaf/Profile | Business function | Approval | Remote operation |
|---|---|---|---|
| `localmind.read` r1 | Delegate answering, reading, research, or summarization requested as non-mutating work | No | `delegate_to_localmind` |
| `localmind.write` r1 | Delegate work that may create, update, rename, or otherwise mutate LocalMind files/documents | Yes | `delegate_to_localmind` |
| `localmind.query` r1 | Read one LocalMind task's current state or result | No | `get_localmind_task` |
| `localmind.cancel` r1 | Request cancellation of one unfinished LocalMind task | Yes | `control_localmind_task(action=cancel)` |

## Workflow Shape

Each Profile has one effectful business-tool call. No Profile adds model-owned
tool selection, a model-visible directory search, LocalMind business planning,
or fallback to another capability. Runtime still materializes the single exact
capability through its normal ToolHub scope boundary; that is not another
Workflow node. The two delegation Profiles additionally use one read-only
`query_current_task` node after delegation to wait for terminal task state.

```text
localmind.read / localmind.write
  -> call one exact delegation tool
  -> terminal=true: validate and project the terminal outcome
  -> terminal=false: persist task ref and enter query_current_task
  -> call get_localmind_task with the latest known state version
  -> terminal=false: persist the new state and repeat query_current_task
  -> terminal=true, status=completed: deliver the result as success
  -> terminal=true, status=failed/cancelled: deliver that exact outcome

localmind.query
  -> bind one task ref
  -> call get_localmind_task once
  -> return current state/result

localmind.cancel
  -> bind one task ref
  -> approval
  -> call control_localmind_task once
  -> return cancellation state
```

`query_current_task` is an internal node in `localmind.read` and
`localmind.write`; it does not route into the independent `localmind.query`
leaf. The node uses the same read-only `localmind.task.get` ToolHub capability,
but its task ID and state version are frozen Runtime bindings rather than a new
intent-routing decision. Query and cancel remain independently intent-routed
capabilities while a delegation Workflow is waiting.

## Read And Write Routing

Both delegation leaves require an explicit LocalMind assignment. Routing then
classifies the requested effect:

| Route | Positive meaning | Hard negatives |
|---|---|---|
| `localmind.read` | Answer, inspect, find, research, compare, or summarize without creating or changing LocalMind content | Create/update/rename/delete/export a file; ambiguous requested effects |
| `localmind.write` | Create, update, rename, transform, or otherwise change LocalMind content | Pure answer/read/research/summary; task query; task cancellation |

An ambiguous delegation that might mutate must not enter the no-approval read
Profile. It clarifies or selects the approval-gated write Profile only when the
owner request clearly asks for a mutation.

By owner decision, `localmind.read` remains available and invokes the existing
`delegate_to_localmind` contract without approval. Server-enforced read-only
delegation is outside the r1 boundary and does not gate implementation. The
read/write distinction controls SparkClaw routing and approval behavior; it is
not represented as a LocalMind MCP effect-mode argument.

## Delegation Completion Query

The owner receives the final LocalMind result only after the task is terminal.
A non-terminal response, including queued, running, or approval-required state,
is not a terminal SparkClaw response.

After any non-terminal delegation or status-query result, Runtime persists the
following across the frozen route, Workflow state, outcome refs, and tool calls:

- `taskId`, endpoint identity, and exact MCP contract revision;
- current `stateVersion`, status, and terminal flag;
- source session, run, and tool-call provenance;
- the frozen source return route and Workflow identity.

The first `query_current_task` call binds the delegated `taskId`, the latest
`stateVersion` as `known_state_version`, and a bounded `wait_ms`. The adapter
maps these to LocalMind's `taskId`, `knownStateVersion`, and `waitMs` fields.
Subsequent calls use the newest validated state version. One long-poll request
may remain in flight per Workflow, with `wait_ms` never exceeding the MCP
contract maximum of 30,000 milliseconds; a non-terminal result re-enters the
same node instead of creating another Workflow or route.

Every result must retain the same task ID. The frozen capability scope must keep
matching the same endpoint/contract identity. A malformed result, MCP `isError`,
authentication failure, changed task ID, or changed registered snapshot cannot
complete the Workflow. `terminal=true` alone ends polling, but it does not imply
success: only a successful completed outcome delivers the LocalMind result as
success; failed and cancelled outcomes are projected as such.

The latest query-node state is persisted after every attempt so Gateway restart
resumes `query_current_task` with the same frozen task and return route. Runtime
must not keep an unbounded goroutine or open MCP call. The overall wait deadline
is 10 minutes from successful delegation acceptance. Reaching it returns an
explicit timeout with the task ID and latest validated state, never success.

## Context Task References

No new persistent LocalMind task repository is introduced in r1. Existing
persisted session, Workflow, result, and tool-call records provide the context
index.

Every validated delegation/query/cancel result emits a `localmind_task`
ResourceRef. Query and cancel resolve targets in this order:

1. an exact `taskId` literal present in the current owner message;
2. for phrases such as "the latest task" or "that task just now", the most
   recently delegated LocalMind task in the same session;
3. otherwise clarify without calling MCP.

The target must come from owner text or validated Runtime evidence. A model may
classify the intent but cannot invent or rewrite the task ID. Cross-session
relative references are not supported in r1; the owner must provide an exact
task ID.

## Query And Cancel

`localmind.query` calls `get_localmind_task` once. The recommended r1 binding is
an immediate read with `wait_ms=0`; a user can explicitly query again. It does
not auto-poll, start, retry, resume, or mutate work.

`localmind.cancel` requires approval, then calls cancel once with the Runtime-
generated stable idempotency key. The r1 Workflow does not send a separate
reason and does not query first. A running task may report that cooperative
cancellation was requested rather than already terminal; that exact state is
returned without automatic polling.

## Input Contract

The live `localmind-ai` 3.2.1 server was queried through MCP `2025-06-18` on
2026-08-26. `delegate_to_localmind` currently accepts exactly:

- required text `request`, from 1 to 12,000 characters;
- optional `documentIds`, containing at most 20 existing LocalMind document
  IDs;
- required caller-generated `idempotencyKey`.

The initial Workflows bind the current message text to `request`, omit
`documentIds`, and let the existing adapter generate `idempotencyKey`. A local
path, filename, attachment, or SparkClaw document ID is never converted into a
LocalMind document ID.

Both delegation Profiles invoke `delegate_to_localmind`. SparkClaw applies no
approval to the read Profile and requires approval for the write Profile; no
additional remote argument is used to express that distinction.

The schema sets `additionalProperties: false`, and the server advertises no MCP
Resources capability. Multimedia remains unsupported and fails before remote
invocation; SparkClaw does not convert it through OCR or ASR.

`get_localmind_task` requires `taskId` and accepts optional
`knownStateVersion` plus `waitMs` from 0 through 30,000 milliseconds. Its
validated `localmind.task.v1` result carries at least `taskId`, `stateVersion`,
`status`, and `terminal`; these fields drive `query_current_task`.

## Source And User Surface

All current human/message-runtime ingress is eligible when the message
explicitly requests LocalMind. This includes WebChat, current human messaging
connectors, and Timer content that explicitly asks for a LocalMind route.

An external-AI principal, including inbound third-party MCP, is ineligible for
all four leaves even if its text names LocalMind. Eligibility is enforced from
authenticated source/principal context, not from provider names or prompt text.

The r1 user surface is intent-only chat. No query/cancel buttons, task list, or
LocalMind management panel is added. Results include the task ID and state so
later natural-language query/cancel requests can reference them.

## Failure Behavior

| Condition | Required result |
|---|---|
| LocalMind was not explicitly requested | Do not select a LocalMind leaf |
| Source principal is external AI | Reject route eligibility |
| Current input contains unsupported media | Fail before the remote call; do not omit or convert it |
| Query/cancel target is absent | Clarify before Workflow invocation |
| Internal task query returns `terminal=false` | Persist the validated state and repeat `query_current_task` |
| Internal task query changes task ID or the registered endpoint snapshot no longer matches | Reject it without completing or delivering |
| Terminal status is failed or cancelled | Return that terminal outcome, never success |
| Overall wait deadline expires | Return an explicit timeout with the task ID and latest state, never success |
| MCP result is malformed, unauthorized, or `isError` | Return a failed tool outcome, not successful completion |

## Acceptance Criteria

1. Catalog contains four LocalMind leaves under the first-level branch: read, write, query, and
   cancel, each joined to one r1 Profile.
2. All leaves require an explicit LocalMind request and reject external-AI
   principals.
3. Read-intent delegation remains available without approval; write delegation
   and cancel require approval; query does not.
4. Delegation sends only current text, omits `documentIds`, and calls exactly
   one delegation tool.
5. Every non-terminal delegation enters the internal `query_current_task` node,
   which makes bounded `get_localmind_task` long polls until a terminal result.
6. Query-node waiting is bounded to 10 minutes from successful delegation,
   durable, restart-safe, and bound to the original task, Workflow, and return
   route.
7. Query/cancel resolve exact or same-session recent task context without a new
   task repository or model-invented IDs.
8. Only `status=completed` is delivered as success; failed, cancelled,
   malformed, unauthorized, and expired waits never become successful results.
9. No Profile uses model tool selection, model-visible directory search,
   cross-leaf fallback, or media conversion.
10. Routing eval covers read/write confusion, task-state intents, external-AI
    exclusion, every current message ingress, and neighboring local abilities.

## Decision History

| Date | Decision | Status |
|---|---|---|
| 2026-08-26 | LocalMind is an explicit first-level capability beside document | Confirmed |
| 2026-08-26 | Initial input is current-message text only and `documentIds` is omitted | Confirmed |
| 2026-08-26 | Delegation is split into read and write Profiles; read has no approval, write requires approval | Confirmed |
| 2026-08-26 | Read remains available through the current delegation tool; server-enforced read-only behavior does not gate r1 | Confirmed |
| 2026-08-26 | Query needs no approval; cancel requires approval | Confirmed |
| 2026-08-26 | Both delegation Profiles enter an internal status-query node after delegation, long-poll for at most 10 minutes overall, and return success only after a completed terminal state | Confirmed |
| 2026-08-26 | Recent/just-now task references resolve from same-session context without a new task repository | Confirmed |
| 2026-08-26 | No task buttons or panel; intent routing selects all four leaves | Confirmed |
| 2026-08-26 | All current message-runtime ingress is eligible except external-AI principals | Confirmed |
