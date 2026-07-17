# Intent Routing and Workflow Tool Exposure Refactor Plan

> Language: English | [简体中文](../zh-cn/docs/intent-routing-workflow-refactor-plan.md)

Design status as of 2026-07-17: the shared runtime foundation exists, and the
next target is a registration-driven capability tree whose current snapshot
contains browser Internet search, browser automation, document read, and
document edit. This four-leaf snapshot is not a fixed enum. Existing
Web/workspace profiles are migration input and must be reconciled into these
registered Workflows without reintroducing Router switches. Context assembly
remains on the legacy path in this phase.

## Decision

SparkClaw keeps Fast intent classification, but limits its output to stable
semantics. It cannot name tools, Skills, workflow IDs, model lanes, risk levels,
approval decisions, or execution steps.

Execution has four ownership boundaries:

1. `IntentRouter` combines deterministic facts with Fast semantic output and
   produces a normalized `IntentEnvelope`.
2. `WorkflowProfileRegistry` uniquely matches that envelope and resolves a
   frozen, versioned `WorkflowPlan`.
3. `ToolExposure.Search/Materialize` is the only authority that turns an active
   capability scope into model-visible ToolDefinitions.
4. Typed `ToolOutcome` and profile-specific assessment may advance or expand
   only transitions already present in the frozen plan.

Skills provide procedure only. Policy and exact-argument authorization remain
authoritative at execution time. ReAct chooses an action from the currently
materialized definitions; it does not determine reachability.

This is an incremental replacement, not a permanent dual-routing design. A
migrated intent never falls back to TaskHint because a capability is missing,
a directory view is stale, or execution is blocked. It records the blocker.

## Why The Previous Design Kept Regressing

The old path placed semantic and realization decisions in the same `TaskHint`:

- Fast classification emitted candidate Skills, tools, risk, and model lane;
- heuristic fallback repeated those choices;
- Skill keyword matching and allow/deny lists changed them again;
- evidence fallback and browser expansion unioned more tools later;
- outcome-specific helpers appended exceptional follow-up actions.

There was no stable intermediate contract and no single owner for visibility.
A fix to one layer changed another layer's input, so Web, browser, document,
email, and workspace routes repeatedly repaired each other's regressions.

The refactor separates three questions:

- Intent: what does the owner want?
- Workflow: which capability closure may satisfy it?
- Exposure: which registered implementation may the model see now?

## Runtime Shape

```mermaid
flowchart TD
    U["Owner message"] --> F["Deterministic fact extraction\nURLs, paths, attachments, actor, channel"]
    U --> M["Fast semantic classifier\nIntentEnvelope fields only"]
    F --> N["Typed normalization and support gate"]
    M --> N
    N --> R["WorkflowProfileRegistry.Route"]
    R --> V["Validate and freeze WorkflowPlan"]
    V --> S["Persist WorkflowState and PlanDigest"]
    S --> D["ToolExposure.Search"]
    D --> C["Bounded directory descriptions"]
    C --> E{"One eligible entry?"}
    E -->|yes| X["Materialize automatically"]
    E -->|no| B["Fast selects one returned entry ID"]
    B --> X
    X --> T["Small ToolDefinition set"]
    T --> O["ToolOutcome adapter"]
    O --> A["Profile assessment"]
    A --> G{"Complete, blocked, or declared transition"}
    G -->|transition| D
```

Core runtime code must not switch on workflow IDs or concrete tool names to
choose scopes, resources, assessments, or next steps. Domain behavior belongs
to registered profiles and ToolHub outcome adapters.

## Stable Intent Contract

The model-facing envelope contains semantics only:

```go
type IntentEnvelope struct {
    Version      int               `json:"version"`
    SourceTurnID string            `json:"source_turn_id"`
    Objectives   []Objective       `json:"objectives"`
    Constraints  IntentConstraints `json:"constraints"`
    Resolution   IntentResolution  `json:"resolution"`
}

type Objective struct {
    ID        string          `json:"id"`
    Domain    IntentDomain    `json:"domain"`
    Operation IntentOperation `json:"operation"`
    Target    TargetRef       `json:"target"`
    Output    OutputKind      `json:"output"`
    Explicit  bool            `json:"explicit"`
}
```

Fast returns the stable envelope and may normalize evidence depth and
clarification state. For the current narrow target slices, the deterministic
support gate also fixes domain and operation; a later broader classifier may
choose them only among fact-compatible registered profiles. Normalization owns
these non-negotiable rules:

- explicit URLs, workspace paths, attachment refs, actor, and source turn come
  from deterministic facts and cannot be invented or changed by the model;
- authorization provenance and `Explicit` cannot be created from retrieved or
  quoted text;
- unsupported enums, target combinations, and unresolved profiles fail closed;
- the final envelope is routed again through the registry after Fast output;
- audit stores a redacted semantic projection, not raw URL/path content.

A deterministic fallback exists for model failure and a bounded support gate,
not as a second tool router. It must produce the same stable contract and pass
the same registry and plan validation.

## Workflow Profile Contract

Profiles are registered by stable ID and revision. Implementations may be
code-defined, but the Registry, not a Router switch, owns which profiles and
tree leaves exist:

```go
type WorkflowProfile interface {
    ID() WorkflowID
    Revision() int
    Recognize(sourceTurnID, content string) (IntentEnvelope, bool)
    Match(IntentEnvelope) bool
    Resolve(IntentEnvelope) (WorkflowPlan, error)
    Assess(*WorkflowState, ToolOutcome) NodeAssessment
    Hint(*WorkflowState) workflowExecutionHint
    TransitionInstruction(ToolOutcome, NodeAssessment) string
}
```

`Recognize` is the deterministic support/fallback gate. `Match` is the actual
typed routing predicate applied after Fast normalization. New work should keep
these decisions aligned through a shared decision corpus; neither method may
return tools. `workflowExecutionHint` contains only model/evidence mode and
workflow/node/scope binding fields; the type has no candidate-tool or Skill
fields.

`WorkflowProfileRegistry.Resolve` validates before persistence:

- profile ID and revision match the registered implementation;
- intent is resolved and every objective ID is unique;
- node, transition, and dependency IDs are valid and acyclic;
- initial nodes have no dependencies and later nodes have declared ones;
- every node has a goal, capability scope, risk set, and attempt bound;
- argument bindings refer only to capabilities reachable in frozen scopes;
- transition predicates and add/replace scopes are complete and bounded.

Non-initial nodes start `pending`. Completing a node activates only nodes whose
declared dependencies succeeded. The workflow succeeds only when every node
succeeds; it cannot complete merely because the current active list became
empty.

## Frozen Scope And Resource Boundaries

The plan contains capability requirements, not tool sequences. A scope
transition may add or replace requirements only when its typed predicate and
activation bound match. The plan is hashed and persisted before exposure.

Resource arguments are also part of the plan:

```go
type ArgumentBinding struct {
    Capability   string
    Argument     string
    ResourceKind string
    Source       ArgumentBindingSource // intent_target or outcome_ref
    TargetKinds  []TargetKind
}
```

Before execution, Runtime requires the argument to equal either a deterministic
intent target or a governed typed reference persisted from a prior outcome.
This prevents an otherwise eligible page reader or file reader from accessing
an unrelated URL or path.

## Single Tool Exposure Authority

The authoritative contract is documented in
[Tool Exposure Contract](intent-routing-tool-exposure-contract.md).

`Search` loads the persisted run and active node. It computes eligibility from:

- frozen capability requirements and denied effects;
- enabled ToolHub registrations and their capability descriptors;
- node allowed risks;
- `Policy.MayExpose` with actor, workflow, and node context.

It then ranks only the eligible set using trusted registration descriptions.
The first view contains entry IDs, capability, summary, usage boundaries,
effects, and risk, but no concrete schema or hidden tool name.

One entry is materialized automatically. With multiple entries, Fast sees only
that bounded directory view and must return one listed entry ID. Unknown,
out-of-view, stale, actor-mismatched, workflow-mismatched, or scope-mismatched
selection fails. `Materialize` is the sole path to model-visible definitions.

Visibility never authorizes execution. ToolHub and Policy re-evaluate the
concrete definition, exact arguments, actor, and resources before a call runs
or enters approval.

## Outcome-Driven Adaptation

ToolHub registration owns the outcome adapter. Adapters emit typed signals and
governed refs; they do not decide whether the user goal is complete. The active
profile assesses the outcome against the frozen node goal.

The only valid results are:

- `complete`: mark the node succeeded and activate ready dependents;
- `blocked`: persist the reason and stop the workflow;
- `needs_more_evidence`: activate one matching declared scope transition.

Outcome IDs and transition activation counts are persisted. Duplicate outcomes
are no-ops. An unknown signal, exhausted transition, missing ref, or digest
mismatch blocks instead of widening exposure.

## Current-stage Target Profiles

| Registered leaf/Profile | Stage sequence | Tool exposure boundary |
|---|---|---|
| `browser.internet_search` r1 | `search_info -> complete` | Expose only `web.search` backed by configured Infinimesh Info; return its typed result without page-read expansion. |
| `browser.automation` r1 | `scan_tabs -> focus_existing/open_new -> complete` | First only `browser.list_tabs`; then only `browser.focus` for an exact existing URL or `browser.open` for an absent URL. |
| `document.read` r1 | `inspect_type -> read_by_type -> complete` | Type inspection is deterministic; then expose only the compatible reader bound to the exact path. |
| `document.edit` r1 | `inspect_type -> edit_by_type -> complete` | Type inspection is deterministic; then expose only format- and operation-compatible editors and return the output copy. |

These are default Registry entries, not branches embedded in Runtime. Future
branches register new nodes, decision cases, and Workflow contracts. Core
routing and traversal code remains unchanged.

Each transition replaces the active Exposure view. The previous stage's
ToolDefinitions are cleared, `ScopeRevision` advances, and stale selections or
calls fail. The Agent never sees the union of scan, focus/open, read, edit, or
future-stage tools.

For “search SparkClaw”, Fast selects the registered
`browser/internet_search` path. The Workflow exposes only the Info-backed
`web.search`, executes the frozen query, and returns that result. Revision 1
does not add `browser.read` after search.

For “open https://example.com”, the browser automation Workflow first exposes
only `browser.list_tabs`. It deterministically compares normalized tab URLs to
the frozen target. An exact match activates a view containing only
`browser.focus`; no match activates a view containing only `browser.open`.

## Legacy Context Assembly

Conversation history, owner context, current user text, attachments, and
compact context formatting continue to use the existing assembler. This plan
does not introduce a new context graph or per-Workflow context builder. The
new Runtime binds route, active stage, and Exposure metadata around the legacy
assembled content. Legacy tool/Skill candidate lists are ignored for migrated
profiles so context reuse cannot widen stage visibility.

Email, calendar, and workspace knowledge/RAG are not migration targets in this
plan; see [Deferred Capabilities](deferred-email-calendar-knowledge.md).

## Migration Procedure

Migrate one semantic domain slice end to end:

1. Add stable enums only when existing vocabulary cannot express the intent.
2. Add deterministic facts and a redacted classification corpus.
3. Implement one unique `Match`, fallback support gate, plan resolver,
   assessor, and completion behavior.
4. Register semantic capabilities, trusted directory metadata, effects, and an
   outcome adapter with each implementing ToolDefinition.
5. Add frozen argument bindings for URLs, paths, records, recipients, or other
   governed resources.
6. Remove the migrated intent's TaskHint candidate logic and Skill tool lists.
7. Test recognition uniqueness, invalid plan rejection, exposure bounds,
   argument escape attempts, outcome idempotency, transitions, persistence,
   and end-to-end behavior.
8. Update this plan, the profile catalog, exposure contract, architecture, and
   Chinese mirrors in the same change.

The preferred order is:

1. Generic dynamic catalog registration and edge/leaf validation.
2. `browser.internet_search` with Info-only result return.
3. `browser.automation` with list-tabs then focus/open stage isolation.
4. `document.read` with type inspection and compatible reader exposure.
5. `document.edit` with type inspection and compatible editor exposure.
6. Register future branches independently, then remove remaining legacy tool
   authority only after their complete slices migrate.

## Non-Negotiable Invariants

- Fast output contains no realization choices.
- Capability branches and leaves come from Registry data; the current four
  leaves are not hard-coded into Fast, Runtime, or validation switches.
- Profile matching is unique or fails closed.
- A frozen plan is validated, versioned, hashed, and persisted.
- Core exposure and runtime contain no workflow-ID routing switch.
- Search ranks only eligible registered entries and cannot widen scope.
- Materialize accepts only the latest bounded view.
- An active stage exposes only its own scope; transitions clear prior tool
  definitions rather than unioning them.
- Every call is bound to workflow, node, scope revision, capability, and exact
  governed resource where required.
- Skills cannot grant, deny, union, or subtract tools.
- Outcomes activate only declared transitions and never inject tools.
- Read-only intent cannot reach mutation or external effects.
- Approval and exact-argument Policy remain authoritative.
- Migrated intent failure never re-enters the legacy route.
- Resume uses the persisted plan and profile revision, never reclassifies.

## Validation And Exit Gates

Each migrated slice requires:

- a positive and negative semantic decision corpus;
- ambiguity and profile identity tests;
- one-entry and multi-entry directory selection tests;
- stale/out-of-view materialization tests;
- exact resource binding tests;
- ToolOutcome adapter, assessment, transition, retry, and idempotency tests;
- file and PostgreSQL persistence coverage when state shape changes;
- an end-to-end API or Runtime test proving TaskHint was not used;
- external-model smoke before calling a domain production-ready.

Repository handoff runs:

```bash
cd services/gateway
go build ./...
go vet ./...
go test ./...
cd ../..
npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
bash scripts/run-eval.sh
git diff --check
```

## Recovery And Rollback

An active workflow resumes its persisted intent, profile revision, plan, node
state, and outcome history. If the implementation no longer supports that
schema or revision, it fails explicitly. Runtime does not silently re-resolve
or route through TaskHint.

Operational rollback means deploying a source version that supports active
plans or explicitly terminating incompatible runs. A permanent feature flag,
shadow router, or dual exposure authority is not a rollback strategy.
