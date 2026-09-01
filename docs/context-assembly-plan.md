# Context Assembly Optimization Plan

> Language: English | [简体中文](../zh-cn/docs/context-assembly-plan.md)

Status: Implemented 2026-08-31. Observation deduplication, rolling observation
compaction, stable prefix ordering, ContextBuilder, uniform observation
envelopes, `observation.read`, profile capacity integration, bounded historical
acquisition, fixed owner-text rejection, and legal structured-data variants are
all active.

This plan owns semantic prompt sections and legal degradation inside
`services/gateway/internal/agent`. The
[model capacity contract](model-capacity-contract-design.md) owns executable
model capacity and final Router admission. The
[context history design](context-history-query-design.md) owns bounded Store
reads and invocation snapshot lifecycle. The
[Tree context design](tree-session-context-parity-design.md) owns Tree-specific
section policy.

Owner decision: Memory is a product scaffold and is not an Agent context
source. Context assembly must not call `MemoryRepository` or reserve a Memory
section.

## 1. Design Outcome

The implemented flow is deliberately small:

```text
selected profile + typed operation
        |
        v
context_tokens - output_class_budget
        |
        v
Agent ContextBuilder
  fixed semantic sections
  legal full/compact/minimal/drop variants
        |
        v
complete rendered request
        |
        v
Model Router final token check
        |
        v
provider physical window
```

This separates three responsibilities:

- Agent knows which semantic content must remain, may compact, or may drop;
- Model Router checks the complete system/user/schema/image request against one
  resolved physical capacity contract;
- Provider remains the last physical defense, not the first place overflow is
  discovered.

The design does not choose new history because a model window grows. Historical
selection remains 8 messages, 6 tool calls, 4 episodes, and 3 images. A larger
window only lets the same selected information retain richer legal variants.

## 2. Current Baseline

### Implemented behavior retained

- `buildAgentContextSnapshot` selects fixed 8/6/4/3 session context and exposes
  Workflow and routing render paths.
- Observations have one model-visible copy per step rather than appearing in
  both system and user prompts.
- `adaptToolResult` produces a uniform summary/structured/evidence envelope;
  full output is stored as an artifact and referenced by `artifact_uri` or
  `ObservationRef`.
- Older current-run observations compact in place while the newest two remain
  protected; only an irreducible observation overflow stops the run.
- Stable within-loop content precedes per-step observations, enabling model
  prefix-cache reuse.
- ContextBuilder supports explicit section variants rather than one monolithic
  prompt string.
- `observation.read` provides support-scope-governed recovery of persisted
  evidence under its existing read limits and Policy checks.

### Implemented changes

- Workflow admission resolves typed operation capacity from the selected
  profile; old constants and compatibility arithmetic are absent.
- Workflow, Tree, and conversation-answer builders register the unchanged owner
  question as a fixed section after the early oversized-question gate.
- Owner text, current resources, resolved documents, history, observations,
  schemas, and output contracts remain separate semantic sections.
- JSON, resource, document, evidence, and tool-schema sections switch only
  between complete registered variants. ContextBuilder has no arbitrary hard
  truncation policy.
- Agent performs bounded Store reads once per invocation and recent-document
  resolution reuses those candidates.
- The selected catalog, typed operation registry, ContextBuilder, Router, and
  local model entrypoint share one capacity contract without a fallback source.

## 3. Capacity Input To ContextBuilder

Every generating operation maps in code to one output capability class and an
allowed lane set. The selected profile supplies one physical context and
positive class budgets:

```text
output_budget = selected_lane.output_budgets[operation.output_class]
input_budget = selected_physical.context_tokens - output_budget
```

ContextBuilder receives the resolved `input_budget` from the immutable selected
capacity catalog. Callers do not pass a numeric output allowance. There is no
independent `max_input_tokens`, profile-wide output maximum, or one field per
Workflow/step/repair invocation.

The capacity catalog is resolved and validated once at load. Runtime does not
re-evaluate a unique upper limit for every request. It performs only immutable
operation-to-class lookup and subtraction, which may also be precomputed. What
must be repeated for every distinct request is token counting of the actual
rendered system content, user content, response schema, template options, and
images.

Missing, zero, negative, unknown, or illegally related profile values fail
loading before Agent or Router construction. Semantic degradation cannot repair
invalid capacity. The active path must not substitute legacy constants, another
class, another lane, an environment default, or omitted provider behavior.

## 4. Section Contract

A section is a semantic unit, not an arbitrary string slice:

```go
type ContextSection struct {
    Kind      SectionKind
    Policy    SectionPolicy
    Variants  []RenderedVariant
}
```

Each consumer registers its own section order and priorities. ContextBuilder
provides the common mechanics for choosing legal variants; it does not impose
one global degradation order on Tree, Workflow, conversation answer, and
direct chat.

The implemented Workflow contract is:

| Section | Required behavior |
|---|---|
| System safety and execution rules | fixed; never content-trimmed |
| Output schema/contract | fixed and structurally complete |
| Unchanged owner question | fixed; never truncated, summarized, or merged with resources |
| Current governed resources | protected `full -> minimal`; current explicit resources do not silently disappear |
| Tool definitions | valid `full -> compact`; each selected definition remains valid JSON/schema |
| Current-run observations | `full -> compact`; latest execution evidence protected; artifact reference retained |
| Selected session tool results | `full -> compact -> drop` |
| Selected recent conversation | `full -> compact -> drop` |
| Selected images and episodes | consumer-specific compact/drop variants |

Tree has its own policy in the Tree design. Final-answer assembly can use
different priorities because it needs evidence and user-facing continuity
rather than route ambiguity. All consumers start from the same selected
historical records but need not render the same sections.

### Fixed content

Fixed means exact semantic preservation, not that the provider must accept it.
If fixed instructions, unchanged owner question, complete schema, and required
structured metadata do not fit after optional sections reach their smallest
legal variants, assembly fails with a typed input-too-long error. It never
truncates fixed content to force dispatch.

The early `owner_question_too_long` gate remains separate because Guard and
Embedding must parse the unchanged text before history or routing. The later
whole-request check is still required because system rules, schemas, resources,
history, observations, and image tokens also consume capacity.

### Structured variants

JSON, schema, resource, tool-definition, and evidence sections must remain
syntactically and semantically valid at every level. Allowed transformations
include:

- omit optional fields using a typed compact projector;
- replace evidence bodies with bounded summaries plus `artifact_uri`;
- retain only required tool arguments in a compact schema;
- choose a complete routing-minimal resource record;
- drop a section explicitly marked optional.

Byte or character prefixes, suffixes, middle cuts, invalid JSON fragments, and
silent owner-text trimming are forbidden. Per-section byte ceilings may remain
as guards on already valid variants, but cannot authorize malformed output.

## 5. Assembly And Admission Algorithm

For each model request, Agent performs:

1. resolve typed operation, allowed lane, output class, and `input_budget` from
   the selected immutable capacity catalog;
2. create fixed sections and all legal variants for optional/protected
   sections;
3. render the complete full request in consumer-defined order;
4. count it with the shared model-aware counter;
5. if it does not fit, change the lowest-priority eligible section to its next
   legal variant and count again;
6. stop when the request fits or no legal degradation remains;
7. pass the complete rendered request and typed operation to Model Router;
8. let Router repeat authoritative counting after every transport option and
   schema is known.

Router never makes semantic choices. It rejects overflow without deleting
messages, truncating strings, selecting a larger output class, or switching
lanes. Provider receives the explicit class budget and is only the last window
enforcement layer.

The model-aware counter uses a tested conservative upper bound first and the
selected model tokenizer when the request approaches the boundary. The old
chars/3 and bytes/4 estimator remains only in isolated snapshot tests and does
not decide production admission.

## 6. Stable Prefix And Current-Run Observations

Within one bounded Workflow loop, prompt ordering remains:

```text
system: static rules -> selected tool definitions -> frozen session context
        -> frozen Workflow stage context

user:   step header -> current-run observation tail -> fixed output contract
```

Content that is constant for the loop must remain byte-stable after its initial
variant is selected. Growing observations stay at the tail. If a later prompt
requires a smaller frozen-context variant, that loop transitions explicitly
and records the prefix change; prefix-cache performance never overrides
correct admission.

Observation compaction retains tool identity, status, key structured fields,
and artifact reference. The newest two observations remain uncompressed while
older entries are eligible. If legal compaction still exceeds the independent
run observation-byte ceiling, the existing deterministic budget stop remains.

`observation.read` exposure is not changed by this capacity project. Its
implemented support-scope authorization, read count, byte window, artifact
ownership, and audit behavior remain authoritative. Conditional exposure may be
evaluated later as an independent tool-surface optimization; capacity
correctness does not depend on it.

## 7. Historical Snapshot Integration

After the owner-question gate, Runtime builds one bounded
`InvocationHistory`. Agent selects 8 messages, 6 tool calls, 4 episodes, and 3
images once and passes the immutable selected value to Tree, Workflow, and
final-answer consumers. Recent-document fallback reuses already loaded bounded
candidates.

ContextBuilder owns rendering and legal degradation of this selected value. It
does not query Store, page for more history, or change selection counts based on
the physical model window. Tree and Workflow share selected record identities,
not a byte-identical final prompt.

On resume, Runtime rebuilds the snapshot at most once from original
`AgentRun.StartedAt`, `Intent.SourceTurnID`, and `RunID`. No persistent history
anchor, selected-record copy, or prompt cache is added.

Memory repository results are absent from the acquisition and section
registry. The existing MemoryStore backend remains just a Store backend and
must not be confused with the unused Memory product feature.

## 8. Episode Summary Decision

The previous proposal to make one Fast model call after every run for an
episode summary is withdrawn. It would add queue load, latency, failure states,
capacity classification, and evaluation work without evidence that the current
mechanical summary causes material cross-run failures.

The current bounded mechanical episode summary remains. Improving it requires
a separate measured problem statement and comparison against simpler
deterministic projection changes. This plan adds no async summary queue, model
operation, output class, retry, or model-error fallback.

## 9. Explicit Non-Goals

- Native messages-array conversion or provider-native function calling.
- Embedding-based history retrieval or full-history RAG.
- Dynamic output planning per task, step, candidate count, or prompt size.
- Automatic handling of an oversized owner question.
- Memory activation or memory-to-context projection.
- A second ContextBuilder, Router, capacity registry, or global prompt cache.
- Changing Workflow time, step, action, duplicate, observation, support-read,
  or concurrency limits.

## 10. Failure And Observability

Failure reasons remain typed and separate:

- `owner_question_too_long`: unchanged owner text cannot pass mandatory early
  Guard or Embedding admission;
- `model_input_too_long`: complete request cannot fit after every legal semantic
  degradation;
- `invalid_model_capacity`: selected profile is missing or illegal and startup
  fails;
- `model_output_incomplete`: provider reports `finish_reason=length`;
- existing observation-byte stop: current-run evidence cannot fit even after
  legal compaction.

Audit records operation, lane, output class, physical context, input budget,
count source, initial/final token counts, selected variants, and before/after
bytes. It does not record discarded content or owner text. Existing
`workflow_step.prompt_compressed` and
`workflow_step.observations_compacted` events remain the behavior signals.

## 11. Delivery Order

### Already implemented and retained

- single-copy observations;
- rolling observation compaction and artifact recovery;
- stable prompt-prefix ordering;
- ContextBuilder mechanics and uniform observation envelopes;
- support-scope-governed `observation.read`.

### Implemented: capacity migration

1. Typed operations, output classes, and selected-profile validation are active.
2. Resolved class input budgets replace Agent constants and caller budgets.
3. Owner text is a separate fixed section.
4. Legal projectors replace arbitrary structured-data trims.
5. Model Router applies final admission and finish-reason handling at every entry point.

### Implemented: bounded history integration

1. Three bounded recent-query repository methods own Agent acquisition.
2. One invocation-owned candidate and selected snapshot is built.
3. Tree, Workflow, final answer, and document resolution receive it explicitly.
4. Agent hot paths contain no complete-list or duplicate history reads.

### Measurement only

- Record prefix-cache prefill behavior on the active local profile.
- Use routing and workflow evaluation to adjust section policies only when
  evidence shows a quality or latency regression.

Model-generated episode summaries are not on the delivery path.

## 12. Verification And Acceptance

Implementation is accepted only when:

- every production model call maps to one allowed lane and output capability
  class, while a class may serve multiple operations;
- invalid selected-profile capacity fails loading and no default or borrowed
  value appears in tests;
- changing physical `context_tokens` changes the available input budget without
  changing fixed history counts;
- unchanged owner text is a fixed section and oversized text is rejected before
  history or execution;
- every JSON/schema/resource/evidence variant remains valid after degradation;
- Router rejects an oversized complete request before provider dispatch;
- observations appear once, remain recoverable under current authorization,
  and compact in deterministic order;
- Tree, Workflow, and final answer reuse one selected history value while
  retaining consumer-specific prompt policies;
- recent-document fallback performs no duplicate tool-history read;
- external-MCP and Memory context remain empty;
- `finish_reason=length` cannot produce successful persistence or delivery;
- existing Policy, Approval, artifact scope, claim coverage, Workflow budget,
  and external-MCP isolation tests remain green.

Run focused config, Agent, Model Router, and Store contract tests; the complete
Gateway build/test/vet gate; default File and available PostgreSQL coverage;
routing/model golden evaluation; prefix-cache measurement where applicable;
and bilingual documentation checks.

## 13. Ownership Boundaries

- `internal/agent/context_builder.go`: common legal-variant selection mechanics;
- Agent consumers: their own section membership, order, priority, and fixed
  content;
- `internal/agent/context_snapshot.go`: selected bounded historical value, not
  Store acquisition policy;
- `internal/modelrouter`: immutable capacity lookup, model-aware final counting,
  finish reason, and transport bounds;
- Store repositories: bounded historical candidate access;
- `configs/model.profiles.json`: physical context and output-class budgets;
- Policy/ToolHub: `observation.read` authorization and limits.

Durable current behavior is mirrored in Architecture, Workflow execution,
Intent routing, Store, Model loading, and Deployment. This accepted record is
retained with the owner's design documentation.
