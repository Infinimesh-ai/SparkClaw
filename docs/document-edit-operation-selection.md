# Document Edit Operation Selection Node

> Language: English | [简体中文](../zh-cn/docs/document-edit-operation-selection.md)

Implemented design record (2026-07-27) for moving the document editor operation
choice out of the fast secondary routing call and into a dedicated
`select_edit_operation` workflow node placed after evidence localization. It
amends [Document workflows](document-workflows.md) and raises `document.edit`
from revision 3 to revision 4.

## Problem

`document.edit` revision 3 ran as one `document_edit` node with two stages.
After the structured read completed (`read_for_edit`), a scope transition
activated `edit_by_type`, and tool materialization searched the directory for
every registered editor of the frozen format. Because that view contained more
than one entry (for DOCX: `replace_text`, `replace_paragraph`,
`insert_paragraph`, `delete_paragraph`, `set_text_style`), the runtime resolved
the ambiguity inside `workflowDirectorySelection` with an inline
`ChatWithProfile("fast", ...)` call.

That fast secondary routing had three structural defects:

1. **Wrong capability class.** Distinguishing replace from insert is a semantic
   judgment over the owner request plus the full structured observation. The
   fast lane repeatedly chose `insert_*` when the request ("修改/完善/润色 an
   existing block") required `replace_*`.
2. **Starved evidence.** The selection prompt only received compact observation
   summaries trimmed to 6000 runes, not the located evidence the deep executor
   would later see.
3. **Invisible to the plan.** The choice happened inside tool materialization:
   no plan node, no attempt bounds of its own, no dedicated audit trail, and no
   way for the frozen plan to state that selection must precede mutation.

## Decision

Operation selection becomes a first-class **decision node** in the frozen plan,
executed by the Workflow Runtime on the workflow execution model lane (`deep`),
after evidence localization and before the editor stage. The fast secondary
routing path is closed for `document.edit`: if the edit node ever reaches
materialization without a persisted decision, the workflow blocks explicitly
instead of falling back to a fast model call.

### New plan shape (`document.edit` revision 4)

```text
confirm_document_target        (deterministic)
  -> document_locate_evidence  (evidence: structured read + location)
  -> select_edit_operation     (decision: choose exactly one registered editor)
  -> document_edit             (evidence: bounded mutation via the chosen editor)
```

- `document_locate_evidence` owns the former `read_for_edit` stage: it reads
  the frozen governed path through the format-qualified `document.read`
  capability and completes on `content_available`.
- `select_edit_operation` is a new node with the new completion rule
  `decision`. Its capability scope is the same format-qualified `document.edit`
  requirement the editor stage uses, so the candidate boundary stays frozen and
  identical to what will be materialized.
- `document_edit` keeps the `edit_by_type` stage, argument bindings, approval
  risk, and verification behavior unchanged, but starts at scope revision 1 of
  its own node instead of inheriting a transitioned scope.

### Decision node contract

`CompletionDecision` (`"decision"`) is a new `NodeGoal` completion rule with
plan validation rules of its own:

- must declare a non-empty initial capability scope (the candidate boundary);
- must not declare transitions, argument bindings, or stage capability rules;
- never materializes ReAct-visible tools — the runtime resolves it directly;
- must depend on at least one evidence node so located evidence exists first.

The runtime resolves an active decision node before computing the next
execution hint:

1. Search the tool directory with the node's frozen scope (same
   `ExposureRequest` audit path as any other node).
2. Zero candidates: block the workflow (`no registered editor matches the
   requested document change`).
3. One candidate: select it deterministically — no model call (text edits,
   for example, only register `replace_text`).
4. Multiple candidates: one `workflow_operation_selection` model call on the
   `deep` profile. The prompt carries the owner request, the node goal, the
   full structured observations of the dependency evidence nodes under a wider
   budget, and the eligible entries. The strict single-field
   `{"entry_id":"..."}` output contract and minimum-change semantics
   (modify/improve/polish → replace, never insert unless the target is absent
   or explicitly requested as new) are unchanged from the retired fast prompt.
5. The selected entry is persisted on the decision node as an outcome
   reference (`kind=tool_directory_entry` with capability, format, and
   operation attributes), the node completes, and the editor node activates.
   Invalid model output consumes one attempt; exhausting `MaxAttempts` blocks
   the workflow.

Resolution emits the existing `tools.directory.selected` audit event (actor
`workflow-decision`) plus a `workflow.decision_resolved` event, and appends a
`workflow_stage: edit_operation_selected operation=...` observation so the deep
executor knows which operation was frozen and why.

### Consuming the decision

`workflowDirectorySelection` recognizes nodes that depend on a decision
node: the persisted `tool_directory_entry` reference is the only admissible
selection, it must still be present in the active directory view, and a missing
or ambiguous reference is a hard error. The generic Fast model fallback has
been deleted. `MaterializeAll`, an exact persisted decision, and a deterministic
single candidate are now the only directory-selection paths; any other
multi-candidate scope fails and must add an explicit decision node.

## Stability properties

- Operation choice runs on the deep lane with the same evidence the executor
  sees, bounded by the frozen format scope.
- The choice is persisted, auditable, retry-bounded, and enforced: the edit
  tool call must match the decided entry or it is rejected by the existing
  materialized-boundary validation.
- `document.edit` can no longer silently degrade to fast secondary routing;
  every ambiguity either resolves through the decision node or blocks visibly.
- Single-candidate formats skip the model call entirely, so the new node adds
  no latency to text edits.

## Implementation Record

The refactor landed in five reviewable steps; each kept the build and the
`internal/agent` test suite green.

### 1. Contract (`internal/app`, plan validation)

- `internal/app/workflow.go`: add `CompletionDecision CompletionRule =
  "decision"`.
- `internal/agent/workflow_plan.go` (`validateWorkflowPlan`): a decision node
  must declare a non-empty frozen `InitialScope.Requirements` and must not
  declare transitions, argument bindings, stage capability rules, or
  `MaterializeAll`; it must depend on at least one `CompletionEvidence` node.

### 2. Profile (`internal/agent/workflow_profiles.go`)

- `documentEditProfile.Revision()` 3 → 4.
- `Resolve()` emits the four-node plan from the section above. The former
  `document_evidence_resolved` scope transition disappears; `document_edit`
  starts directly in `edit_by_type` with the edit scope and keeps the
  path/`output_path` bindings, allowed risks, and `MaxAttempts: 2`. The read
  binding moves to `document_locate_evidence`.
- `Assess()` dispatches on `outcome.NodeID` instead of node stage:
  locate + `content_available` → complete (`document_evidence_located`);
  edit + `edit_completed` → complete (`document_edit_completed`); anything
  else blocks.
- `Hint()` keys `inspect`/`modify` off `state.ActiveNodeIDs[0]`; the old
  `TransitionInstruction` text is retired in favor of the decision-resolved
  observation.
- The profile implements a new optional interface
  `workflowDecisionSemantics { DecisionRules(app.WorkflowNode) []string;
  DecisionResolvedInstruction(app.ToolDirectoryEntry) string }` carrying the
  replace-vs-insert minimum-change rules from the retired fast prompt.

### 3. Runtime decision executor (new `internal/agent/workflow_decision.go`)

- `activeWorkflowDecisionNode(state)` and
  `resolveActiveWorkflowDecisions(ctx, *run, profile)`; the runtime calls the
  resolver whenever the single active node is a decision node.
- Resolution: directory search under the node's frozen scope (existing
  `tools.directory.searched` audit); zero candidates block; one candidate is
  selected without a model call; multiple candidates trigger one
  `workflow_operation_selection` call via `ChatWithProfile(ctx, "deep", ...)`.
- The prompt uses the `WORKFLOW_OPERATION_SELECTION_REQUEST` header and owner
  request segment. The mock-router injection channel is
  `MOCK_OPERATION_SELECTION_RESPONSE`; output uses the strict
  `parseWorkflowDecisionSelection` contract. Dependency-node
  observations feed the prompt under a 20000-rune budget (a generalization of
  `workflowDirectoryEvidence`).
- Attempt accounting uses `node.MaxAttempts`; invalid output retries, an empty
  `entry_id` blocks with `no_registered_editor_matches`, exhausted attempts
  block with `edit_operation_selection_invalid`.
- Completion persists the outcome reference
  (`kind=tool_directory_entry`, attributes capability/format/operation/via),
  activates dependents via `activateReadyWorkflowNodes`, emits
  `tools.directory.selected` (actor `workflow-decision`) plus
  `workflow.decision_resolved`, and returns the
  `workflow_stage: edit_operation_selected operation=...` observation.

### 4. Consumption and fail-closed wiring

- `internal/agent/workflow_directory_selection.go`:
  `workflowDirectorySelection` resolves nodes with a decision dependency
  through the persisted `tool_directory_entry` reference; a missing,
  ambiguous, or no-longer-eligible reference is a hard error. The old Fast
  selector and its compact 6000-rune evidence path are deleted; unexpected
  multi-candidate scopes fail closed.
- `internal/agent/workflow_registry.go`
  (`materializeActiveWorkflowTools`): an active decision node returns an
  error — it must be resolved, never materialized.
- `internal/agent/workflow_runtime.go` (`runWorkflowWithSeedAndStream`): after
  the per-stage status check, resolve active decisions; append the returned
  observation and treat resolution as a transition; a blocked resolution exits
  through the existing `workflowBlockedMessage` path.
- `internal/agent/workflow_dispatcher.go` (`resumeMatchedWorkflow`): resolve
  active decisions before computing the hint and materializing tools so a
  crash between localization and selection recovers.

### 5. Tests and docs

- Update `workflow_preflight_test.go` (`advanceDocumentEditToEditor` posts the
  read call to `document_locate_evidence`, then calls the resolver; edit-node
  scope revision drops 2 → 1; the model-call assertion becomes
  `workflow_operation_selection` on `deep`), plus the node-ID/scope-revision
  references in `document_edit_workflow_test.go`,
  `message_control_routing_test.go`, and `web_workflow_test.go`.
- New coverage: deep-lane multi-candidate selection; single-candidate text
  edit asserts zero selection model calls; empty `entry_id` blocks; missing
  decision fails materialization closed; invalid output retries then blocks;
  plan validation rejects decision nodes without evidence dependencies or
  scope.
- [Document workflows](document-workflows.md) and its `zh-cn` mirror now
  describe revision 4 and link back to this record.

Validation: `go build ./...` and `go test ./internal/agent/ ./internal/app/`
in `services/gateway` (baseline recorded green on 2026-07-27), plus the docs
language-mirror check from the CI workflow.

## Out of scope

- No new editor operations, formats, or preservation rules.
- `document.read`, browser, weather, schedule, and conversation workflows keep
  their existing plans. Their current scopes resolve through `MaterializeAll`
  or one exact candidate and do not need a model-owned directory fallback.
- The intent router's first-pass routing (`task_hint`, capability matching) is
  unchanged; this design only removes the second fast routing hop inside the
  document edit workflow.
