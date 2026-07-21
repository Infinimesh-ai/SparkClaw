# Intent Routing Tool Exposure Contract

> Language: English | [简体中文](../zh-cn/docs/intent-routing-tool-exposure-contract.md)

This document is authoritative for progressive Tool Directory search and schema
materialization in the [routing refactor](intent-routing-workflow-refactor-plan.md).
Profiles and transition semantics are defined in the
[Workflow Profile Catalog](intent-routing-workflow-domain-profiles.md).

## Registration Authority

One ToolHub registration owns execution, schema, capability descriptors,
trusted directory text, risk/effects, and the outcome adapter. The logical
directory is derived from enabled registrations in memory. It is neither a
second manifest nor a ToolHub meta-tool.

```go
type ToolDefinition struct {
    // Existing execution and schema fields remain.
    Capabilities  []CapabilityDescriptor `json:"capabilities"`
    OutcomeAdapter ToolOutcomeAdapter     `json:"outcome_adapter"`
    Directory     ToolDirectoryMetadata  `json:"directory"`
}

type ToolDirectoryMetadata struct {
    Summary      string       `json:"summary"`
    WhenToUse    string       `json:"when_to_use"`
    WhenNotToUse string       `json:"when_not_to_use,omitempty"`
    InputKinds   []TargetKind `json:"input_kinds,omitempty"`
    OutputKinds  []OutputKind `json:"output_kinds,omitempty"`
    Effects      []ToolEffect `json:"effects"`
}

type ToolOutcome struct {
    ID         string          `json:"id"`
    ToolCallID string          `json:"tool_call_id"`
    NodeID     WorkflowNodeID  `json:"node_id"`
    Status     string          `json:"status"`
    Signals    []OutcomeSignal `json:"signals,omitempty"`
    Refs       []ResourceRef   `json:"refs,omitempty"`
    Retryable  bool            `json:"retryable,omitempty"`
}

type NodeAssessment struct {
    OutcomeID  string           `json:"outcome_id"`
    NodeID     WorkflowNodeID   `json:"node_id"`
    Status     AssessmentStatus `json:"status"`
    Signals    []OutcomeSignal  `json:"signals,omitempty"`
    ReasonCode string           `json:"reason_code,omitempty"`
}
```

`ToolOutcome` reports objective execution facts. It never claims that the user
goal is complete. The profile-specific assessor compares the outcome with the
frozen `NodeGoal` and produces `NodeAssessment`. This separation lets one
outcome satisfy one profile but request more evidence in another.

## Converged Interface

Agent Runtime has one interface for tool visibility:

```go
type ToolExposure interface {
    Search(context.Context, app.ExposureRequest) (app.DirectoryView, error)
    Materialize(context.Context, app.MaterializeRequest) (app.ExposureView, error)
}

type ExposureRequest struct {
    RunID         string         `json:"run_id"`
    WorkflowID    WorkflowID     `json:"workflow_id"`
    NodeID        WorkflowNodeID `json:"node_id"`
    ScopeRevision int            `json:"scope_revision"`
    ActorRef      string         `json:"actor_ref"`
    Limit         int            `json:"limit"`
}

type ToolDirectoryEntry struct {
    ID            ToolDirectoryEntryID `json:"id"`
    Capability    CapabilityDescriptor `json:"capability"`
    Summary       string               `json:"summary"`
    WhenToUse     string               `json:"when_to_use"`
    WhenNotToUse  string               `json:"when_not_to_use,omitempty"`
    Effects       []ToolEffect         `json:"effects"`
    Risk          RiskLevel            `json:"risk"`
    RelevanceRank int                  `json:"relevance_rank"`
}

type MaterializeRequest struct {
    ViewID        string                 `json:"view_id"`
    RunID         string                 `json:"run_id"`
    WorkflowID    WorkflowID             `json:"workflow_id"`
    NodeID        WorkflowNodeID         `json:"node_id"`
    ScopeRevision int                    `json:"scope_revision"`
    EntryIDs      []ToolDirectoryEntryID `json:"entry_ids"`
    ActorRef      string                 `json:"actor_ref"`
}
```

The caller cannot submit `CapabilityScope`, `NodeGoal`, or outcome text.
`Search` loads the persisted run, verifies workflow/node/scope revision, and
derives the active scope and goal from the frozen plan and node state.

Eligibility is computed before relevance:

```text
LOAD active scope and node goal from persisted WorkflowState
MATCH enabled ToolDefinitions by capability name and qualifier subset
FILTER node allowed risks and scope denied effects
FILTER Policy.MayExpose using actor/workflow/node context
RANK trusted compact registration descriptions
RETURN a bounded DirectoryView without full schemas
```

Semantic ranking can reorder only the eligible set. Skill text, TaskHint
candidates, tool observations, and model output cannot add an entry. A
requirement qualifier map must be a subset of a registered capability's
qualifiers; adding a qualifier therefore narrows eligibility.

Each directory entry identifies exactly one concrete registered definition and
one matched capability. The tool name stays inside Runtime until materialized;
the model sees only the compact entry. Automatic materialization is allowed only
when structured filtering returns exactly one entry. With zero entries Runtime
blocks explicitly; with multiple entries the model may select only returned IDs.

## View Binding

`ViewID` is an opaque HMAC over the complete directory view. Runtime retains
only the latest view for each run/node and `Materialize` rechecks actor,
workflow, node, scope revision, entry membership, current registration, and
`Policy.MayExpose`. Unknown, stale, restarted-process, or out-of-view selection
fails explicitly.

`WorkflowState` persists a `DirectoryViewRef` for audit and recovery, not a
reusable authorization token. After restart Runtime performs a fresh `Search`
inside the same persisted scope. It never accepts the old `ViewID` or silently
maps an old entry to changed effects.

Directory selection is a Runtime control action, not a ToolCall and not an
authorization decision. The resulting `ToolCall` is bound to workflow ID, node
ID, scope revision, and capability. Immediately before execution,
argument-aware Policy still evaluates the exact definition, arguments, actor,
and resources; only that decision can allow, require approval, or deny.

## Outcome-Driven Expansion

Outcome adapters are selected through registration metadata, not a per-tool
router switch. They emit typed signals and governed resource references without
copying arbitrary payloads into workflow state. The assessor can activate only
a `ScopeTransition` already present in the frozen plan. Activation changes the
scope, increments `ScopeRevision`, clears the previous directory selection, and
runs `Search` again. It never directly inserts a tool.

Applied outcome IDs and per-transition activation counts are persisted. A
duplicate outcome is a no-op; an undeclared signal or exhausted transition
blocks explicitly. Visibility remains narrower than authorization at every
revision.

## Current-stage Exposure Views

The table is a snapshot of current Workflow registrations, not a global tool
allowlist. Future branches add registered stages and scopes without changing
`Search` or `Materialize` control flow.

| Workflow stage | Active capability boundary | Materializable definition(s) |
|---|---|---|
| `browser.internet_search/search_info` | Read-only facts that depend on current Internet state through the configured Info provider; no page read or live interaction | `web.search` |
| `browser.weather/render_weather_card` | One grounded location's current conditions or short forecast; no alerts, news, history, or comparison research | `media.render_weather_card` |
| `browser.automation/scan_tabs` | List managed browser tabs only | `browser.list_tabs` |
| `browser.automation/focus_existing` | Focus the exact page ID selected from the persisted tab outcome | `browser.focus` |
| `browser.automation/open_new` | Open the exact frozen URL only | `browser.open` |
| `document.read/inspect_type` | Deterministic path and type preflight | no Agent tool; a future registered type inspector may be the sole entry |
| `document.read/read_by_type` | Read the exact path with the detected format | only the compatible file/document/PDF/image reader registration |
| `document.edit/read_for_edit` | Read the authoritative frozen path using the detected format | only the compatible file/document/PDF reader registration |
| `document.edit/edit_by_type` | Select and apply the requested operation only after structured evidence, bounded by detected format | compatible text, DOCX, XLSX, PPTX, or PDF editor entries; one exact entry is materialized |

Moving to another row replaces the view; it never unions rows. Search results
do not expose page readers. Weather-card execution does not expose Internet
search, and alert/news/comparison requests never expose the weather-card tool.
Tab scanning does not expose focus and open together. Document read never
exposes editors. Document edit replaces its reader view with a format-bounded
editor directory, selects one exact operation-qualified entry from the owner's
request plus structured evidence, and never exposes another format's tool
family. Router output does not freeze the concrete editor operation.

These migrated slices never consult TaskHint candidates, Skill allow/deny
lists, fallback tool lists, or observation-string expansion for visibility.
Legacy context assembly remains available as input evidence only. Exact URL and
path arguments are checked against frozen `ArgumentBinding` rules before
execution. Unmigrated domains are transitional callers of the old path, not
fallback routes for a failed migrated Workflow.
