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
[Intent routing](intent-routing.md) for how a request selects a leaf, and the
active [Workflow evidence ownership and reuse](workflow-evidence-ownership.md)
migration contract for moving deterministic facts and locators into Runtime
while reusing one acquisition event across consumers without merging
independent events.

## Execution Pipeline

Every inbound message follows one pipeline (`agent.go` `handleMessage`):

1. **Normalize** — the message plane produces a channel-neutral envelope and
   resource projection.
2. **Guard + route** — the safety guard can terminate the run; intent routing
   returns exactly one `RouteDecision`. `clarify` / `blocked` / `unmatched`
   are terminal: they produce a result without ever executing tools.
   A Web request with no owner text and only image/audio/file parts takes the
   typed media-content route to the registered `conversation.answer#publish`
   candidate without synthesizing text or invoking semantic routing models.
3. **Dispatch** — `dispatchMatchedWorkflow` (`workflow_dispatcher.go`)
   resolves the matched leaf to one versioned workflow profile, freezes the
   plan (nodes, transitions, argument bindings, completion evidence, plan
   digest), persists it on the run, and materializes the tools for the first
   active scope. The normalized request `MessageContent` is persisted with the
   run so no-tool publication can govern its media and preserve media-part
   order while removing command text.
4. **Stage loop** — `runWorkflowStream` (`workflow_runtime.go`) drives
   bounded stages until the workflow succeeds, blocks, or waits on an
   approval or a browser login handoff. Each stage executes one of four
   node invocation shapes:
   - a **no-tool model answer** node (`conversation_workflow.go`),
   - a **no-tool message completion** node that governs and freezes the
     normalized multipart request without calling a model,
   - a **direct tool invocation** node (`runWorkflowDirectToolOnce`) that
     calls the single bound tool without a model step, or
   - a **model step** through `runWorkflowModelStep`, the shared step loop.
5. **Assess + transition** — every workflow tool call is adapted into a typed
   `ToolOutcome`, assessed by the profile, and applied to the persisted node
   state; profile decisions and transition instructions feed the next stage's
   observations. Tool materialization is recomputed per scope revision.
6. **Finalize** — a succeeded workflow either projects its typed outcome
   through the grounded result adapter, preserves governed multipart request
   content for message completion, or synthesizes a final answer with the model
   (`synthesizeWorkflowFinalAnswer`), depending on the frozen profile contract.

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

A detached execution never runs on the bare lifecycle context: the gateway
wraps it with a hard deadline derived from `workflow_run_max_duration_seconds`
plus the model HTTP timeout and a fixed grace (`detachedExecutionContext`).
The run budget below is the graceful stop; this deadline only backstops a
request that is still in flight when the budget trips.

## The Workflow Step Loop

`workflow_step_loop.go` holds the only model/tool execution primitive:

- `runWorkflowModelStep` is the single entry point. It honors the profile's
  stage-context lane, defaults to Deep only when no lane is supplied, and calls
  `runWorkflowStepLoop`. Its fixed stage context names the unresolved
  `semantic_variables`; tool stages derive them from the final model-visible
  schema, while no-tool answer, operation-selection, and finalization calls name
  their bounded output directly.
- One `ContextBuilder` admission pass owns ordered system and user sections.
  Sections are fixed, degradable through named variants, or UTF-8-safe
  truncatable. Current-run observations appear exactly once in causal order,
  and the fixed step output contract remains the exact user-prompt tail.
- Prompt admission estimates tokens with a calibrated 4-bytes-per-token
  coefficient. It degrades lower-value session context, provisioned evidence,
  schemas, and older observations before hard-truncating only declared
  truncatable sections. Every successful model call is at or below the
  effective input threshold; fixed-section overflow returns
  `workflow_prompt_fixed_sections_oversized` before a model call. Decisions are
  audited as `workflow_step.prompt_compressed` without recording dropped text.
  The threshold derives from the model profile chosen by the same router task
  policy as execution, with an 85% context-window safety factor.

### Tool Results And Evidence

Every tool result is archived in full, while its model-visible observation uses
the same `observation_summary_max_bytes` envelope (default 2400) regardless of
tool name. A truncated envelope retains its artifact URI and directs the model
to `observation.read`, which reads a bounded UTF-8-safe window from an artifact
owned by the current session. A model node declares this helper through frozen
`CapabilityScope.SupportRequirements`; normal exposure and exact directory
selection persist its entry beside the primary business entries. Old persisted
plans without that requirement do not gain it on resume. Direct nodes project
only primary entries, while model nodes project primary plus selected support
entries.

At most two executed support reads are allowed per stage by default. Completed
and failed ToolHub executions consume this quota and observation bytes, but do
not consume the run-wide business tool-call or repeated-call budgets. After the
quota, the helper disappears from the next model projection. One attempted
over-limit call yields a typed protocol observation; a repeated violation
blocks with `observation_read_limit_exceeded`. Runtime assesses support outcomes
without invoking the profile's business `Assess` or advancing its node.
Document and browser tool-message summaries and structured fields are consumer
projections: governed paths, source byte metadata, page/snapshot identity, URLs,
generations, and digests stay in archived Runtime state instead of being copied
into the model message. Persisted legacy envelopes are reprojected at the model
context boundary; an unparseable legacy document/browser summary degrades to its
tool and status instead of replaying unbounded locator text.

Profiles declare required or optional evidence sources in their stage context.
Before the model call, Runtime resolves each source against completed nodes or
current workflow resources, reads the archived output, and inserts a bounded
`PROVISIONED_EVIDENCE` section before the output contract. Document slices keep
whole paragraphs or rows; browser structured slices keep complete opaque control
refs. Runtime-owned document hashes and paths and browser snapshot metadata are
removed, while coverage, omission counts, candidate-local content/structure,
and eligible operation descriptions remain.
Missing required evidence blocks before the model call. `ContextBuilder` admits
the combined prompt by compacting session/tool context, then reducing
provisioned slices, then compacting older current-run observations; the newest
two observations and the output-contract tail are preserved.

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

Budgets exist at two scopes. A **stage budget** (`newWorkflowStageBudget`)
starts fresh on every step-loop entry and bounds the model retry loop inside
one scope revision. A **run budget** (`newWorkflowRunBudget`) is created once
per run by `runWorkflowWithSeedAndStream` and threaded through every stage —
model steps and direct tool invocations alike — so its counters survive the
one-tool-call-per-stage boundary. On resume after an approval, seed calls are
replayed into a fresh run budget while the run wall clock restarts (owner
decision time is not charged).

Stage budgets stop the step loop (audited as `workflow_step.budget_stopped`):

| Config key (`runtime` section) | Default | Stops the stage when |
|---|---|---|
| `workflow_stage_max_duration_seconds` | 180 | the stage's wall-clock time is exhausted |
| `workflow_stage_max_no_progress_actions` | 3 | consecutive actions produce no new evidence |
| `workflow_stage_max_observation_reads` | 2 | executed `observation.read` support calls reach the stage quota |

`workflow_stage_evidence_max_bytes` (default 8000) clamps total persisted
evidence provisioned to one stage. A required source that is missing, empty, or
cannot fit blocks the stage fail closed. The
`SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES` environment variable overrides
this limit.

Run budgets stop the whole run, checked both inside the step loop and before
each stage (the latter audited as `workflow_run.budget_stopped`):

| Config key (`runtime` section) | Default | Stops the run when |
|---|---|---|
| `workflow_run_max_duration_seconds` | 1800 | the run's wall-clock time is exhausted |
| `workflow_run_max_tool_calls` | 32 | executed business tool calls across all stages reach the cap |
| `workflow_run_observation_compaction_bytes` | 36000 | older eligible observations begin rolling compaction |
| `workflow_run_max_observation_bytes` | 48000 | current model-visible observations reach the hard stop before compaction |
| `workflow_run_max_repeated_tool_calls` | 3 | one tool repeats with an identical fingerprint in consecutive executed calls, across stage boundaries |

`SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES` and
`SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES` override the two observation
boundaries. Runtime checks the 48,000-byte hard maximum first and stops without
another compaction attempt. Below that maximum, reaching 36,000 bytes compacts
eligible older entries while preserving the newest two and causal order. A
legacy configuration that omits the lower threshold derives it as 75% of the
resolved hard maximum; explicitly configuring both values requires
`0 < compaction < maximum`. Compaction state is a typed executor field rather
than a marker inferred from untrusted observation text. The deprecated
`workflow_step_max_*` and `react_max_*` keys (and the
`SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES` /
`SPARKCLAW_REACT_MAX_OBSERVATION_BYTES` overrides) still load as fallbacks
(the newest name wins when several are present; the old step duration fills
the stage duration, never the run duration); new configuration must use the
`workflow_stage_max_*` / `workflow_run_max_*` names.

### Scope Enforcement

Model output cannot widen a workflow's boundary:

- `materializedWorkflowCapability` maps the selected tool to a capability that
  was materialized for the active node/scope revision or fails the step.
- Primary `Requirements` and generic `SupportRequirements` use the same
  exposure, Policy, exact directory-entry, qualifier, and active-scope checks;
  no tool name creates an authorization exception.
- `validateWorkflowToolPlan` (`workflow_runtime.go`) re-validates every plan
  against the persisted plan digest, active node state, stage capability
  rules, qualifier bindings, and frozen argument bindings before execution.
- `materializeWorkflowBoundArguments` / `bindWorkflowToolArguments` overwrite
  argument values from persisted intent/route/outcome state, so a later stage
  cannot substitute its own query, URL, path, or element ref.
- `workflowModelToolProjection` removes Runtime-owned qualifiers, frozen
  `ArgumentBinding` values, and format-policy proof arguments from the schema
  shown to the model. Runtime restores them before validating against the
  unchanged registered ToolHub schema.

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
| `workflow_step.evidence_provisioned` | Persisted evidence was resolved and sliced for the active stage |
| `workflow_step.evidence_blocked` | Required stage evidence could not be resolved or admitted |
| `workflow_step.observations_compacted` | Older run observations were compacted before budget enforcement |
| `workflow_step.observation_read_limited` / `workflow_step.support_assessed` | Support-read quota enforcement and Runtime-owned assessment |
| `workflow_step.budget_stopped` | A stage or run budget stopped the step loop |
| `workflow_run.budget_stopped` | The run budget stopped the stage loop before a stage |
| `workflow.required_tool_not_called` | Rejected a final answer that skipped required tool evidence |
| `workflow.transitioned` | A tool outcome was assessed and applied to node state |
| `workflow.direct_tool_invoked` | A direct-invocation node ran its single bound tool |
| `workflow.model_answer_completed` | A no-tool model-answer workflow completed |
| `workflow.message_content_governed` | Source-session multipart request parts were validated and bound to governed artifacts |
| `workflow.message_completed` | A no-tool ordinary multipart message workflow completed |
| `workflow.blocked` / `workflow.protocol_blocked` | Setup or protocol failure blocked the workflow |
| `workflow.execution_cancelled` | Gateway shutdown cancelled an active Workflow |
| `workflow.finalization_failed` | Completed evidence could not be rendered into a final answer |
| `workflow.legacy_resume_retired` / `workflow.legacy_login_resume_retired` | A pre-workflow persisted run was closed instead of resumed |

## Public Failure Projection

Execution failures carry a typed internal `FailureCode` separately from their
diagnostic. Raw model output, schema payloads, provider errors, artifact bodies,
host paths, and wrapped errors stay in audit/model-call records. Before creating
`run.Summary`, an assistant message, or `WorkflowResult`, Runtime replaces the
failure with a stable owner-safe message and exposes the typed code through
`WorkflowResult.Error.Code`.

The explicit protocol codes include `required_evidence_unavailable`,
`tool_outside_active_scope`, `semantic_preflight_failed`,
`semantic_output_invalid`, `workflow_prompt_fixed_sections_oversized`, and
`observation_read_limit_exceeded`. New failure paths must add a typed code and
safe projection rather than assigning `err.Error()` to `FinalAnswer`.

## Browser Revision 3 Execution

`browser.automation` and `browser.interaction` register revision 3. Revision 3
adds the frozen support-capability contract without changing their business
tool path. A
persisted browser r1 plan is rejected as an unregistered contract rather than
reinterpreted under current code. The shared plan owns acquisition, evidence,
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
  tool call; no-tool conversation profiles use either model-answer or message
  completion according to their frozen completion rule.
- **Tools** — register in `internal/toolhub/registry.go` (the consistency
  test forbids per-name switch registration). Declare capabilities with
  qualifiers so materialization and stage rules can bind them; add an
  outcome adapter (`workflow_outcome.go`, `tool_result_adapter.go`) when the
  workflow needs typed signals from the tool result.
- **Argument bindings** — bind tool arguments to intent targets, route slots,
  route facts, or prior outcome refs so values are materialized from
  persisted state instead of trusted from model output.
- **Semantic variables** — keep only unresolved target judgment, eligible-tool
  selection, or content arguments in the model schema. Do not expose a Runtime
  fact merely so the model can copy it back.
- **Evidence requirements** — declare persisted source nodes or current
  workflow resource kinds in `StageContext`; choose `head` or `structured`
  slicing and keep required sources fail closed.
- **Budgets** — tune the `workflow_stage_max_*` / `workflow_run_max_*` keys
  per deployment in the `runtime` config section; do not add per-workflow
  bypasses.

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
| config `react_max_*` and `workflow_step_max_*`, `SPARKCLAW_REACT_MAX_OBSERVATION_BYTES` / `SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES` | `workflow_stage_max_*` / `workflow_run_max_*`, `SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES` (old keys load as fallbacks; the old step duration maps to the stage duration only) |
| unmatched contract ref `react.unmatched` | `legacy.unmatched` |
