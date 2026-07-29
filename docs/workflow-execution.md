# Workflow Execution Runtime

> Language: English | [简体中文](../zh-cn/docs/workflow-execution.md)

This is the contributor guide to the workflow-native execution runtime in
`services/gateway/internal/agent`. It documents the execution path every
matched request takes, the single model/tool step primitive, its budgets and
protocol, resume semantics, and the extension points for new workflow code.
The legacy generic ReAct loop has been fully removed: workflows are the only
way tools execute, and there is no fallback executor behind them.

Related documents: [Architecture](architecture.md) for the system boundary,
[Workflow capability matrix](workflow-capabilities.md) for what is registered
today, [Development](development.md) for the step-by-step extension workflow,
and [Intent routing](intent-routing.md) for how a request selects a leaf.

## Execution Pipeline

Every inbound message follows one pipeline (`agent.go` `handleMessage`):

1. **Normalize** — the message plane produces a channel-neutral envelope and
   resource projection.
2. **Guard + route** — the safety guard can terminate the run; intent routing
   returns exactly one `RouteDecision`. `clarify` / `blocked` / `unmatched`
   are terminal: they produce a result without ever executing tools.
3. **Dispatch** — `dispatchMatchedWorkflow` (`workflow_dispatcher.go`)
   resolves the matched leaf to one versioned workflow profile, freezes the
   plan (nodes, transitions, argument bindings, completion evidence, plan
   digest), persists it on the run, and materializes the tools for the first
   active scope.
4. **Stage loop** — `runWorkflowStream` (`workflow_runtime.go`) drives
   bounded stages until the workflow succeeds, blocks, or waits on an
   approval or a browser login handoff. Each stage executes one of three
   node invocation shapes:
   - a **no-tool model answer** node (`conversation_workflow.go`),
   - a **direct tool invocation** node (`runWorkflowDirectToolOnce`) that
     calls the single bound tool without a model step, or
   - a **model step** through `runWorkflowModelStep`, the shared step loop.
5. **Assess + transition** — every workflow tool call is adapted into a typed
   `ToolOutcome`, assessed by the profile, and applied to the persisted node
   state; profile decisions and transition instructions feed the next stage's
   observations. Tool materialization is recomputed per scope revision.
6. **Finalize** — a succeeded workflow either projects its typed outcome
   through the grounded result adapter or synthesizes a final answer with the
   model (`synthesizeWorkflowFinalAnswer`), depending on
   `profile.Finalization()`.

### Streaming Ownership

After `message.stream.started` is flushed, the accepted Workflow runs on the
Gateway lifecycle context rather than the HTTP request context. A browser
refresh, navigation, or broken SSE connection stops event delivery only; the
Workflow continues to its persisted result, approval, or model timeout, and
WebChat recovers that state through its normal polling. The model router's
`http_timeout_seconds` remains the upper bound for each Fast or Deep request.

Gateway shutdown cancels the lifecycle context and waits for detached stream
work to exit. A true lifecycle cancellation stops the Workflow once and records
the run as `cancelled`; a model timeout or other execution error records
`failed`. Neither state may be projected as `completed`, and a running Workflow
is complete only after its persisted status is `succeeded`.

## The Workflow Step Loop

`workflow_step_loop.go` holds the only model/tool execution primitive:

- `runWorkflowModelStep` is the single entry point. It honors the profile's
  stage-context lane, defaults to Deep only when no lane is supplied, and calls
  `runWorkflowStepLoop`.
- The loop freezes a full and a compact system prompt at loop start
  (`workflowStepSystemPrompt` over the context snapshot's `ForWorkflowStep` /
  `ForWorkflowStepCompact` views). Current-run observations appear exactly
  once, in causal order, in the user prompt, followed by the step output
  contract.
- Prompt admission estimates tokens with a calibrated 4-bytes-per-token
  coefficient and swaps in the compact system prompt at 80% of the available
  input budget (`compressWorkflowStepPromptIfNeeded`, audited as
  `workflow_step.prompt_compressed`). The budget derives from the model
  profile chosen by the same router task policy as execution, with an 85%
  context-window safety factor (`effectiveWorkflowStepPromptBudget`).

### Step Protocol

Each step sends a user prompt headed `WORKFLOW_STEP_REQUEST` and requires the
model to return exactly one JSON object:

```json
{"type":"action","tool":"tool.name","arguments":{},"reason":"short reason"}
{"type":"final","answer":"answer for the user"}
```

`parseWorkflowStepOutput` (`workflow_step_output.go`, `action_parser.go`)
validates the envelope and rejects tools that are not model-visible
(`tool_not_visible`). A parse failure is recoverable: the loop appends a
`workflow_step.parse_error` observation, audits `workflow_step.parse_failed`,
and lets the model correct itself; the bad action is never executed.

Inside a workflow scope the loop returns to the workflow runtime after every
tool call so the outcome can be assessed before another tool may run under the
same scope revision. If a stage requires tool evidence
(`workflowStageContext.RequiresToolEvidence`), a premature `final` is rejected with a
`workflow_protocol_violation` observation (audited as
`workflow.required_tool_not_called`); after two rejected attempts the node is
blocked with reason `required_tool_not_called`.

### Budgets And Stop Conditions

`stepBudget()` reads `RuntimeConfig` and stops the loop on whichever limit
trips first (audited as `workflow_step.budget_stopped`):

| Config key (`runtime` section) | Default | Stops the loop when |
|---|---|---|
| `workflow_step_max_duration_seconds` | 180 | wall-clock time is exhausted |
| `workflow_step_max_tool_calls` | 16 | executed tool calls reach the cap |
| `workflow_step_max_observation_bytes` | 48000 | accumulated observations reach the cap |
| `workflow_step_max_no_progress_actions` | 3 | consecutive actions produce no new evidence |
| `workflow_step_max_repeated_tool_calls` | 3 | one tool repeats with identical fingerprint |

`SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES` overrides the observation
budget. The deprecated `react_max_*` keys and
`SPARKCLAW_REACT_MAX_OBSERVATION_BYTES` still load as fallbacks (the new
names win when both are present); new configuration must use the
`workflow_step_max_*` names.

### Scope Enforcement

Model output cannot widen a workflow's boundary:

- `materializedWorkflowCapability` maps the selected tool to a capability that
  was materialized for the active node/scope revision or fails the step.
- `validateWorkflowToolPlan` (`workflow_runtime.go`) re-validates every plan
  against the persisted plan digest, active node state, stage capability
  rules, qualifier bindings, and frozen argument bindings before execution.
- `materializeWorkflowBoundArguments` / `bindWorkflowToolArguments` overwrite
  argument values from persisted intent/route/outcome state, so a later stage
  cannot substitute its own query, URL, path, or element ref.

## Run States, Model Calls, And Audit Events

Run states: `received` → `routing` → `executing` → `workflow_step` (set per
model step) and terminal/waiting states `completed`, `blocked`,
`failed`, `cancelled`, `clarification_required`, `approval_pending`,
`browser_login_blocked`.

Model calls made by the step loop use operation `workflow_step_<n>`; resume
gating (`hasWorkflowStepModelCall`) also recognizes the pre-rename
`react_step_<n>` operations from persisted data.

Key audit event types emitted by the executor:

| Type | Meaning |
|---|---|
| `workflow.dispatched` / `gateway.dispatch` | Matched leaf bound to its frozen workflow contract |
| `workflow_step.output` | Parsed one step envelope (action or final) |
| `workflow_step.parse_failed` | Recoverable step-protocol parse failure |
| `workflow_step.prompt_compressed` | Compact system prompt substituted before a model call |
| `workflow_step.budget_stopped` | A step budget stopped the loop |
| `workflow.required_tool_not_called` | Rejected a final answer that skipped required tool evidence |
| `workflow.transitioned` | A tool outcome was assessed and applied to node state |
| `workflow.direct_tool_invoked` | A direct-invocation node ran its single bound tool |
| `workflow.model_answer_completed` | A no-tool model-answer workflow completed |
| `workflow.blocked` / `workflow.protocol_blocked` | Setup or protocol failure blocked the workflow |
| `workflow.execution_cancelled` | Gateway shutdown cancelled an active Workflow |
| `workflow.finalization_failed` | Completed evidence could not be rendered into a final answer |
| `workflow.legacy_resume_retired` / `workflow.legacy_login_resume_retired` | A pre-workflow persisted run was closed instead of resumed |

## Browser Revision 2 Execution

`browser.automation` and `browser.interaction` register only revision 2. A
persisted browser r1 plan is rejected as an unregistered contract rather than
reinterpreted under current code. The shared r2 plan owns acquisition, evidence,
interaction, and presentation:

- Runtime directly invokes passive environment preflight, tab discovery,
  focus/open/navigation, settle, snapshot, and visible presentation stages.
- Hidden acquisition always settles and snapshots before semantic validation.
  Every navigation or click invalidates prior refs and requires another settle
  plus generation-scoped snapshot.
- Interaction uses separate `browser.validate_transition` and
  `browser.assess_goal` capabilities. Goal assessment occurs before an action,
  after every validated action, and again on the visible result.
- Presentation is a required Workflow stage, not a completion callback. It
  transfers the managed profile to visible Chromium, opens or focuses the exact
  result, settles, snapshots, and validates it. The persisted result record
  binds hidden and visible evidence; the run cannot succeed without the visible
  evidence and leaves that page open.

Failure boundaries are explicit: passive preflight, acquisition, settle,
snapshot identity/generation, route validation, transition validation, goal
assessment, profile transfer, and presentation can each block independently.
Retries remain bounded by plan transitions and are idempotent against persisted
state.

## Resume Semantics

`ResumeRunAfterApproval` (`agent.go`) handles a run whose approval was
resolved:

- **External send approvals** resume through the dedicated send path.
- **Workflow runs** resume with `resumeMatchedWorkflowAfterApproval`
  (`workflow_dispatcher.go`): approved seed calls are re-assessed into the
  persisted plan, then the stage loop continues inside the same frozen scope.
- **Pre-workflow persisted runs**: if the approved action was terminal, the
  run completes with a grounded summary; otherwise the run is closed with the
  retired-runtime message and audited as `workflow.legacy_resume_retired`.
  The same applies to browser login resume for runs without a workflow plan
  (`workflow.legacy_login_resume_retired`). There is no path that re-enters a
  generic loop.

Browser login handoffs are persisted revision-2 state machines. While
`waiting_owner`, ambiguous replies perform no browser work, cancellation leaves
the visible page open, and a wrong-page reply reopens the frozen target. Only an
explicit completion confirmation claims the transition lease and enters
`validating_visible`.

Runtime then lists visible tabs, selects and settles the handoff page, captures a
fresh visible snapshot, and independently validates route, authentication, and
the frozen task page. A mismatch returns to `waiting_owner` with an explicit
owner-facing explanation. Success transfers the exclusive managed profile to
hidden Chromium, reacquires and settles the selected page, and captures a fresh
hidden snapshot before `resuming_workflow`. Pre-login refs are discarded while
the click budget remains unchanged; loss of profile continuity returns to
`waiting_owner`.

Every transition is compare-and-swap persisted with a transition owner and
bounded lease. A second Runtime cannot duplicate an active transition, and a
new Runtime can claim an expired transition after restart. The same contract is
implemented by memory, file, and PostgreSQL stores.

## Extension Points

Follow the extension workflow in [Development](development.md); the code
anchors are:

- **Capability leaf and routing** — `internal/capability` catalog plus the
  semantic graph compiled from the profile registry.
- **Workflow profile** — implement the profile and register it in
  `defaultWorkflowProfileRegistry` (`workflow_profiles.go`,
  `workflow_registry.go`). A profile owns its plan shape (nodes, stages,
  transitions, `MaxAttempts`/`MaxActivations` bounds feeding
  `workflowStageLimit`), its `Assess`/`TransitionInstruction`/`StageContext`
  functions, decisions, and its finalization mode
  (`workflowFinalizationModel` vs. grounded projection).
- **Node invocation modes** — default model step; set
  `InvocationMode: app.WorkflowInvocationDirectOnce` for a no-model single
  tool call; model-answer nodes for no-tool conversation profiles.
- **Tools** — register in `internal/toolhub/registry.go` (the consistency
  test forbids per-name switch registration). Declare capabilities with
  qualifiers so materialization and stage rules can bind them; add an
  outcome adapter (`workflow_outcome.go`, `tool_result_adapter.go`) when the
  workflow needs typed signals from the tool result.
- **Argument bindings** — bind tool arguments to intent targets, route slots,
  route facts, or prior outcome refs so values are materialized from
  persisted state instead of trusted from model output.
- **Budgets** — tune the `workflow_step_max_*` keys per deployment in the
  `runtime` config section; do not add per-workflow bypasses.

When changing prompt assembly, keep the invariants covered by
`agent_test.go`: observations appear once in the user prompt, the step output
contract is the user-prompt tail, and the system prompt sections keep a
stable order for cache-friendly prefixes.

## Legacy Migration Notes

The 2026-07 migration renamed the execution surface and removed the generic
loop. Old identifiers appear only in persisted data compatibility shims:

| Old | New |
|---|---|
| `react.go` / `react_output.go` | `workflow_step_loop.go` / `workflow_step_output.go` |
| `runReActLoop` / `runReActLoopWithSeed` | removed (`runWorkflowModelStep` is the only entry) |
| `REACT_OUTPUT_REQUEST` prompt marker | `WORKFLOW_STEP_REQUEST` |
| `react.parse_error` observation marker | `workflow_step.parse_error` |
| `react.*` audit events | `workflow_step.*` |
| run states `reacting` / `react_step` | `executing` / `workflow_step` |
| model call operation `react_step_<n>` | `workflow_step_<n>` (old prefix still recognized on resume) |
| config `react_max_*`, `SPARKCLAW_REACT_MAX_OBSERVATION_BYTES` | `workflow_step_max_*`, `SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES` (old keys load as fallbacks) |
| unmatched contract ref `react.unmatched` | `legacy.unmatched` |
