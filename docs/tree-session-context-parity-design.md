# Tree Same-Session Context Consistency Design

> Language: English | [简体中文](../zh-cn/docs/tree-session-context-parity-design.md)

Status: Implemented 2026-08-31 after acceptance and a minimum-architecture
review, including same-session context integration and the structural Tree
output hardening in Section 8.

This document refines the Fast/Tree context contract in
[Intent routing](intent-routing.md). Bounded Store acquisition and invocation
snapshot lifecycle are owned by the
[context history design](context-history-query-design.md). Physical capacity,
output capability classes, and final admission are owned by the
[model capacity contract](model-capacity-contract-design.md).

The goal is consistency of selected historical facts, not identical prompt
bytes. Tree and Workflow consume the same invocation-owned selected records,
then independently render the semantic sections required by their different
model contracts.

## 1. Accepted Decisions

| Topic | Accepted decision |
|---|---|
| Historical source | Tree and Workflow receive the same immutable selected 8/6/4/3 record set from one bounded acquisition |
| Prompt parity | Do not require byte-identical prompts, section labels, channels, or final degraded subsets |
| Selection growth | Do not increase history counts when the Fast physical window grows |
| Consumer policy | Tree owns Tree-specific section variants and priorities; Workflow may make different semantic choices from the same source snapshot |
| Resource metadata | Expose only routing-relevant fields to Tree and keep authoritative binding fields in Runtime |
| Capacity | Tree uses Fast `context_tokens` minus the `compact_structured` output-class budget; no Tree-only input cap |
| Initial and repair | Both use the same strict response schema and `compact_structured` class; repair is an attempt, not a new capacity family |
| Structural output | Preserve the candidate array, force thinking off, request strict JSON Schema, retain Runtime validation, and allow at most one repair |
| Memory | Memory repository records remain outside Tree and Workflow context |

## 2. Scope And Non-Goals

This design covers natural-language routing within one human-owned session:

- selected prior messages, tool results, episode summaries, and images;
- current governed resources and resolved document references;
- complete Tree prompt assembly and admission;
- the implemented Tree candidate-scoring JSON contract.

It does not change:

- cross-session retrieval or Memory behavior;
- the fixed 8-message, 6-tool, 4-episode, and 3-image selection;
- embedding input, which remains the unchanged current owner question;
- semantic graph ownership, fusion weights, thresholds, or route binding;
- Policy, Approval, artifact authorization, or external-MCP isolation;
- temperature, seed, repeated sampling, consensus, or score calibration;
- output budgets dynamically based on candidate count;
- Workflow's prompt layout or degradation policy.

Observations created after routing are not historical Tree input. Inbound
external-MCP routing inherits no session messages, tools, images, or episodes;
approved current-run evidence continues through its existing governed path.

## 3. Implemented Boundary

Tree and Workflow now start from the same invocation-owned snapshot, and Tree
admits the complete routing request only after all fixed and degradable sections
are known:

| Concern | Implemented behavior |
|---|---|
| History acquisition | One bounded invocation acquisition, then fixed Agent selection |
| History budget | No separate historical token allowance |
| Resources/documents | Typed sections in the same Tree builder |
| Overflow | Complete legal semantic variants; no arbitrary structured-data trim |
| Final authority | Router checks the complete request before provider dispatch |

The boundary avoids early history compaction followed by late resource
overflow. Structured resource projections remain valid under every legal
variant; no second history source or prompt copy was introduced.

## 4. Shared Invocation Context

After the early owner-question gate and governed-resource resolution, Runtime
constructs one routing context from the invocation-owned history value:

```go
type TreePromptContext struct {
    CurrentQuestion  string
    CurrentResources ResourceRoutingProjection
    Documents        DocumentRoutingProjection
    History          agentContextSnapshot
}
```

`History` contains the same selected record identities passed to Workflow:

- at most 8 prior eligible user/assistant messages;
- at most 6 terminal tool calls from prior runs;
- at most 4 episode summaries;
- at most 3 recent session images.

The current source message and current run are excluded under the
[history design](context-history-query-design.md). Runtime does not reload
history separately for Tree and Workflow.

This is source consistency, not rendered parity. Tree may omit episodes during
overflow while Workflow retains them, use a routing-only compact projection,
or place data under different labels. Those choices do not create historical
disagreement because both consumers start from the same frozen selected
records. A shared cross-consumer degradation order would incorrectly couple two
different prompt contracts and is not introduced.

History remains untrusted data. Prior user or assistant text, tool output,
image metadata, and episode text never gain system-instruction authority.
`MemoryRepository` is never queried while the Memory product remains a
scaffold.

## 5. Resource And Document Projection

Current resources and resolved documents are separate typed sections because
their provenance, freshness, precedence, and authorization differ from
conversation history.

Tree-visible current resources contain only routing-relevant fields:

```text
kind, name, ref, content_type, caption
```

Tree-visible resolved documents contain only:

```text
name, ref, content_type, format, source, activity, provenance
```

Hashes, byte counts, dimensions, internal message-part IDs, document IDs,
parent IDs, and source IDs remain in authoritative Runtime state. Tree cannot
turn a visible reference into authorization or bind the final target.

Both projections use structured serializers. Their variants are complete
valid values such as `full` and `minimal`; an arbitrary character prefix is
never a variant. The minimal form retains `name`, `ref`, `kind` or `format`, and
`provenance` when present.

Current-turn resources take precedence over resolved recent-document metadata,
which takes precedence over historical references. Conflicts remain explicit
for the model, while deterministic Runtime logic retains final binding
authority.

## 6. Tree Prompt Builder

Tree assembles its complete request only after graph, question, resources,
documents, history, response schema, and call options are known:

| Section | Policy |
|---|---|
| Tree instructions and injection boundary | fixed system content |
| Complete eligible semantic graph and revision | fixed structured data |
| Source kind and unchanged owner question | fixed data |
| Current governed resources | protected `full -> minimal`; never arbitrary truncation |
| Resolved governed documents | protected `full -> minimal`; never arbitrary truncation |
| Same-session history | Tree-specific `full -> compact -> drop` variants |
| Strict output contract/schema | fixed tail contract |

The graph, current question, and output contract are never truncated. Every
eligible semantic candidate appears exactly once. The output contract remains
the final prompt section so untrusted data cannot follow it.

Tree first renders every selected history section at its full valid variant.
Only on genuine whole-prompt overflow does it apply Tree-local degradation:

1. drop or compact episode summaries;
2. drop older image projections;
3. compact or drop older tool-result projections;
4. compact or drop older conversation projections.

The newest two conversation turns and newest relevant historical tool result
remain protected while any optional historical content remains. This order is
not a global ContextBuilder rule and does not constrain Workflow.

If all optional history is removed and protected resource/document sections
are minimal but fixed content still does not fit, Tree fails before provider
dispatch with a typed prompt-overflow error.

## 7. Capacity And Admission

Tree's typed initial and repair operations both map to the
`compact_structured` output capability class on the Fast lane:

```text
tree_output_budget =
  selected_fast_lane.output_budgets["compact_structured"]

tree_input_budget =
  selected_fast_physical.context_tokens - tree_output_budget
```

Both values are required positive profile facts and the output budget must be
smaller than the physical context. Missing, zero, malformed, unknown, or
illegally related capacity prevents selected-profile loading before Router or
Agent construction.

There is no `max_input_tokens`, profile-wide output maximum, fixed 3,000-token
Tree history allowance, caller-provided output number, provider default, or
legacy constant in the active calculation. A physical Fast window change
therefore changes `tree_input_budget` automatically while selected history
counts remain fixed.

Agent may use the shared model-aware counter while choosing Tree variants.
Model Router repeats the authoritative check against the complete rendered
system content, user content, strict response schema, chat-template options,
and any image-token reserve. Router does not compress content or switch lanes.
Provider physical rejection remains a last defense, not normal admission.

Initial and repair share one output class because their response shape is the
same. They keep distinct operation/audit identities. A larger candidate set is
covered by evaluation up to the semantic graph's configured maximum; Runtime
does not calculate a new output budget per request. Changing that maximum or
the response schema requires representative class reevaluation.

## 8. Implemented Tree Scoring JSON Hardening

The governed output is the raw Fast/Tree response from the initial scoring call
and its optional repair:

```json
{
  "graph_revision": "...",
  "candidates": [
    {"candidate_id": "...", "tree_score": 0.0}
  ]
}
```

It is not the semantic graph input, Workflow action/final JSON, persisted
fusion record, or File Store JSON.

Both calls request the same OpenAI-compatible strict JSON Schema derived from
the frozen graph revision and eligible candidate set. The schema requires:

- the exact current graph revision;
- an array length equal to the eligible candidate count;
- IDs drawn only from the eligible set;
- one numeric `tree_score` in `[0, 1]` per item;
- all declared fields and no additional fields.

Both calls force thinking off through a per-request chat-template option. An
endpoint that rejects or ignores structured output does not cause a fallback
to unconstrained text.

Runtime validation remains authoritative because JSON Schema cannot guarantee
that every dynamic candidate appears exactly once. Runtime rejects unknown
fields, stale revision, wrong candidate count or set, duplicate IDs, missing
scores, and out-of-range scores. One invalid initial response may trigger one
repair with the same schema and output class. A second invalid response fails
Tree after exactly two model calls and admits no Tree score to fusion.

`finish_reason=length` is incomplete even if its prefix parses. It cannot be
accepted as a score set or obtain a larger output class. Existing repair policy
may spend its one repair attempt with the same class; otherwise Tree fails.

This contract hardens structure only. Temperature remains `0.2`; no seed,
multi-sample consensus, calibration, variance threshold, or request-time output
scaling is added.

## 9. Observability And Failure

When Tree changes a section variant, Runtime emits
`intent_tree.prompt_compressed`. Safe audit fields include profile, physical
context, output class, input budget, initial/admitted token counts, selected
variants, before/after byte counts, and section digests. It does not store
dropped history text or owner-question content.

Failure classes remain distinct:

- `owner_question_too_long`: early Guard or Embedding gate failed before
  history acquisition;
- `model_input_too_long`: complete Tree request cannot fit after legal semantic
  degradation;
- structured-output invalid: initial and optional repair failed schema or
  Runtime candidate validation;
- transport/provider error: handled under Model Router's explicit same-lane
  retry contract.

None of these errors permit a larger output class, cross-lane retry, arbitrary
truncation, or unconstrained Tree response.

## 10. Implementation Record

### Implemented: structured output

- Carry strict JSON Schema and non-thinking options through Model Router.
- Derive one dynamic schema and reuse it for initial and repair calls.
- Retain exact candidate validation and the single-repair fail-closed boundary.
- Cover transport, schema construction, valid initial output, successful
  repair, and invalid repair termination.

### Implemented: bounded shared source

- Complete bounded history queries and invocation snapshot selection.
- Lock eligibility, 8/6/4/3 selection, current-turn exclusion, and
  external-MCP empty-history fixtures.
- Pass the same selected snapshot value to Tree and Workflow without requiring
  byte-identical rendering.

### Implemented: typed Tree assembly

- Replace the pre-rendered routing-context string with typed graph, resource,
  document, and history inputs.
- Add the complete Tree builder and legal structured variants.
- Remove the 3,000-token history convention and character trims.
- Keep owner question and output contract fixed.

### Implemented: capacity integration

- Map both Tree operations to `compact_structured` on Fast.
- Consume selected Fast physical context and class budget.
- Apply Router final admission to initial and repair calls.
- Add invalid-profile, fixed-overflow, and section-degradation tests.

### Post-implementation validation and current-state docs

- Run routing golden evaluation plus ambiguity, correction, unrelated-history,
  multilingual, and injection cases on local and hosted Fast.
- Measure prompt size, prefill latency, Tree timeout, repair rate, and routing
  changes.
- Merge durable behavior into English and Chinese intent-routing and
  architecture guides after implementation.

## 11. Acceptance Criteria

- Tree and Workflow start from the same selected historical record identities
  for one invocation; prompt-byte parity is not required.
- Fixed 8/6/4/3 selection does not change with physical model capacity.
- A full Tree prompt that fits is admitted without degrading history.
- Tree performs no history-only admission before graph, question, resources,
  documents, and response schema are known.
- Every degraded structured section remains valid and the owner question is
  unchanged.
- Follow-up pronouns, omitted targets, corrections, and prior-tool references
  route at least as well as the baseline holdout.
- Current owner input takes precedence over conflicting older context.
- Untrusted resource, document, tool, assistant, image, and episode text cannot
  inject Tree instructions.
- External-MCP routing inherits no prior session derivatives, and Memory is not
  queried.
- Invalid selected-profile capacity fails before any Tree call without a
  default or borrowed value.
- Router rejects an oversized complete Tree request before HTTP dispatch.
- Initial and repair calls use the same strict schema and
  `compact_structured` class while retaining distinct audit identities.
- Runtime validates exact candidate membership and uniqueness.
- Two malformed responses produce exactly two calls, failed Tree, and no score
  admitted to fusion.

## 12. Ownership Boundaries

- `internal/agent/context_snapshot.go`: bounded selected source shared by
  consumers;
- `internal/agent/context_builder.go`: generic legal-variant admission
  mechanics, not a global consumer priority order;
- `internal/agent/intent_router.go`: Tree-specific sections, priorities,
  prompt tail, and candidate validation;
- `internal/modelrouter`: typed operation/class mapping, final request
  admission, strict schema, non-thinking option, finish reason, and transport;
- message/resource projection code: routing-only fields while Runtime retains
  binding authority;
- context-history design: Store reads and invocation snapshot lifecycle;
- capacity design: profile facts and shared counting contract.

The design adds no second history store, capacity registry, router, model lane,
or model-owned route decision.
