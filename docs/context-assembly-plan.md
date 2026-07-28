# Context Assembly Optimization Plan (Phases 0–1)

> Language: English | [简体中文](../zh-cn/docs/context-assembly-plan.md)

Status: Partially implemented 2026-07-27. Phase 0.1, the code portion of 0.3,
and Phase 0.4 are implemented. The dual-light prefix-cache performance
measurement for 0.3, rolling compaction in 0.2, and all Phase 1 items remain
open. This document remains the design authority for those pending changes;
after delivery its durable decisions merge into
[Architecture](architecture.md) and this plan is removed per the documentation
rules.

Scope: prompt assembly and tool-result composition inside
`services/gateway/internal/agent`. No external API, store schema, or Workflow
Profile contract changes. The `modelrouter.Chat(ctx, task, system, user)`
two-string interface and the JSON-in-text step protocol are intentionally
kept: the current Qwen lanes handle that protocol acceptably and the
parse-error recovery path already covers its failure mode.

## 1. Baseline Before This Plan

- Model interface: one system string plus one user string per call
  (`services/gateway/internal/modelrouter/router.go`, `Chat`). No messages
  array, no native function calling. Bounded tool loops parse a JSON action
  from raw model text (`agent/workflow_step_output.go`).
- Session context: `buildAgentContextSnapshot`
  (`agent/context_snapshot.go`) selects fixed windows — last 8
  user/assistant messages (each trimmed to 360 chars), last 6 cross-run tool
  results (summary cap 4000 chars), 4 episode summaries, 4 memories, 3
  images — and renders them into one text block per variant
  (`ForWorkflowStep` / `ForWorkflowStepCompact` / `ForTaskHint`).
- Tool results: `adaptToolResult` (`agent/tool_result_adapter.go`) builds a
  structured JSON envelope (summary / structured / evidence) with
  per-category evidence extractors and three-tier degradation, default cap
  1600 bytes per tool message. Full outputs are persisted as artifacts and
  referenced by `artifact_uri` / `ObservationRef`.
- Loop budgets (`agent/workflow_step_loop.go`): self-imposed prompt cap
  `defaultWorkflowStepContextTokens = 12288` tokens; one-shot compact compression at
  80% of the available input budget; run-level observation cap 48000 bytes;
  token estimation is a chars/3 vs bytes/4 heuristic.

## 2. Problems Identified At Planning Time (P0)

- **P0-1 — Observations are sent twice per step.** In non-compact mode the
  full observation list is embedded in the system prompt
  (`contextualSystemPromptForWorkflowStep`, `agent/workflow_step_loop.go` around line 634) and
  again in the user prompt (`workflowStepUserPrompt`). Every tool step re-sends
  the whole list twice, which burns the 12k budget, triggers compression
  earlier, and reaches the hard stop sooner.
- **P0-2 — Observation growth ends the run instead of being compacted.**
  `shouldStopWorkflowStepLoop` terminates the run once accumulated observations reach
  `MaxObservationBytes` (48000). A single `files.read` document envelope can
  contribute 5–6 KB of evidence, so multi-step document or browser tasks can
  die mid-task with a budget message. There is no in-run compaction path —
  only stop.
- **P0-3 — The prompt prefix changes every step, defeating vLLM prefix
  caching.** The system prompt is rebuilt per step with observations inside
  it, so no stable prefix exists across steps. On the single-GB10
  `dgx-spark-dual-light-v1` profile (serialized generation, 8 GB fast-lane KV
  cache) re-prefilling 8–10k tokens per step is user-visible latency.
- **P0-4 — Token budgets are disconnected from the deployed profiles.**
  `effectiveWorkflowStepPromptBudget` clamps every lane to 12288 tokens even though
  the profiles serve 32k (fast) and 64k (deep), and the chars/3 estimate is
  uncalibrated for Chinese-heavy prompts.

Deliberately *not* P0 (deferred to Phase 1): fixed-count context windows,
mechanical episode summaries, scattered per-section byte caps. They degrade
long-session quality but do not fail tasks.

## 3. Phase 0 — Production-Blocking Fixes

Each item is an independent, per-topic commit.

### 0.1 Single-copy observations (implemented 2026-07-27)

Remove the observation block from the non-compact system prompt; the user
prompt (`workflowStepUserPrompt`) becomes the only carrier, matching what
compact mode already does. No format change in the surviving copy, so no
model-behavior retraining risk.

Acceptance: per-step prompt size drops by the full observation payload;
existing `agent` package tests stay green.

### 0.2 Rolling observation compaction instead of hard stop

When `observationsBytes` reaches the budget, compact the oldest half of the
observation list in place instead of stopping:

- Each compacted entry is reduced to one line via the existing
  `compactObservationSummaryForContext` logic, keeping `tool`, `status`,
  key structured fields, and `artifact_uri`, and tagged `compacted=true`.
- The most recent 2 observations are never compacted (current execution
  state must stay verbatim).
- Order is preserved. Emit an audit event `workflow_step.observations_compacted`
  with before/after byte counts.
- Only if the budget is still exceeded after all eligible entries are
  compacted does the run stop with the existing message.

Persisted artifacts alone are not sufficient: the model must be able to
recover compacted evidence. Therefore this item must not ship before Phase
1.2 provides the uniform read-back tool.

Acceptance: a scripted long run (16+ tool steps of document/browser reads)
completes instead of stopping on `workflow_step.budget_stopped`; audit shows
compaction events.

### 0.3 Stable prompt prefix ordering (code implemented 2026-07-27)

Fix the prompt layout so everything that is constant within one bounded loop
comes first and only per-step material is appended at the tail:

- System prompt: static rules → skills → tool definition JSON → session
  context snapshot (frozen at run start) → TaskHint. Nothing in the system
  prompt may vary between steps of the same loop.
- User prompt: step header → observation list (growing tail) → output
  contract.

This is a reordering only; content is unchanged. vLLM automatic prefix
caching then reuses the KV for the static prefix on every step. The
compression fallback (switching to compact variants) still invalidates the
prefix; that is an accepted trade-off because compression is the exception
path.

Acceptance: with the dual-light profile, per-step prefill token counts in
model logs drop after step 1; record the before/after measurement in
[Model baseline](../benchmarks/model_baseline.md).

### 0.4 Lane-aligned token budgets and calibrated estimation (implemented 2026-07-27)

- `effectiveWorkflowStepPromptBudget` uses the active lane profile's
  `context_tokens` multiplied by a 0.85 safety factor, instead of the 12288
  clamp. 12288 remains only as the fallback when the profile does not
  declare a limit.
- Calibrate the `estimatePromptTokens` coefficients once offline against the
  vLLM `/tokenize` endpoint using representative Chinese, English, and JSON
  samples; keep the runtime estimator coefficient-based (no online
  tokenizer dependency) and note the calibration date and script next to the
  constants.
- The 0.80 compression threshold is unchanged.

Risk note: larger admitted prompts increase prefill cost; 0.3 offsets this
for the static prefix, and the per-message caps from the adapter still bound
the tail.

## 4. Phase 1 — Extensible Assembly Structure

### 1.1 ContextBuilder

Today the budget decisions live in scattered constants (360 / 4000 / 1600 /
1400 / 48000 …) and three hand-written render variants. Phase 1 collapses
them into one explicit builder in the `agent` package:

- A **section** is a registered unit with: kind, priority, render function,
  and a degradation chain `full → compact → drop`.
- Default registry (highest priority first):

| Section | Degradation chain | Today's equivalent |
|---|---|---|
| Output contract and safety rules | never degrades | step output contract block |
| Tool definition JSON | full → compact (names + required args) | compact tool defs |
| Current-run observations | full → rolling compaction (0.2) | observations list |
| Session tool results | full → compact → drop | `formatContextToolResults` |
| Recent conversation | full → tail(4) → drop | `formatContextMessages` |
| Skills | full → compact → drop | skill block |
| Memories / images / episodes | compact → drop | remaining sections |

- The builder receives the lane's real input budget (from 0.4), allocates
  top-down by priority, and degrades from the lowest-priority section
  upward until the estimate fits.
- `ForWorkflowStep`, `ForWorkflowStepCompact`, and `ForTaskHint` become three budget
  configurations of the same builder; the rendered text at the `full` level
  is byte-identical to today's output, so model-facing behavior does not
  change at introduction time.
- Adding a future context source (calendar, mail digest — see
  [Deferred capabilities](deferred-email-calendar-knowledge.md)) means
  registering one section, not editing three render functions.

### 1.2 `observation.read` tool

Tool messages already carry `artifact_uri`, but only `files.read` has
re-read semantics; truncated web/browser evidence is unrecoverable by the
model. Add one read-only tool:

- Name `observation.read`; arguments `artifact_uri` (required), `offset`,
  `max_bytes`; risk `read`, no approval.
- Returns the persisted full tool output through the same
  `adaptToolResult` envelope, subject to the same per-message caps.
- Access is limited to artifacts of the current session; the URI is an
  opaque store key, so no path semantics exist.
- Registered in `internal/toolhub/registry.go` (the registry consistency
  test then covers it) and exposed to bounded loops as an always-available
  read tool.

This closes the truncation-recovery loop and is what makes the 0.2
compaction safe rather than lossy.

### 1.3 Model-generated episode summaries

`summarizeEpisode` currently concatenates tool:status pairs and a trimmed
final answer. Replace the `Summary` field content with a fast-lane generated
digest:

- After a run completes, enqueue an async summarization request (the
  serialized-generation queue absorbs it; it never blocks the interactive
  path): input is the goal, tool list, and final answer; output is a ≤200
  character digest in the owner's language stating what was done, to which
  files/URLs, and what remains open.
- On model error or timeout, keep the current mechanical summary as the
  fallback — the field format and consumers are unchanged.

Cost: one small fast-lane request per run. Benefit: cross-run references
("continue editing that file") survive the 8-message window because episode
summaries become genuinely informative.

## 5. Explicit Non-Goals

- **Native messages array + function calling.** Worth doing only after the
  ContextBuilder stabilizes; then swapping the builder output from two
  strings to a messages array is a local change. Not part of this plan.
- **Embedding-based history retrieval.** The embedding lane exists, but
  single-owner sessions are short; revisit only with evidence of long-session
  reference misses.
- **Full-history RAG / hierarchical memory graphs.** The 27B/35B-A3B lanes
  do not reliably drive complex retrieval-assembly protocols, and the
  single-user local deployment does not justify the complexity.

## 6. Validation

Follow [Development](development.md) and the refactoring playbook baseline
discipline: record the test baseline before any change (run
`npm run setup:document-tools` first, per the standing guardrail), then per
phase:

- Unit: compaction trigger/ordering/byte accounting for 0.2; budget math for
  0.4; builder degradation order and full-level byte-identity for 1.1;
  registry consistency covers 1.2 automatically.
- Scenario: long document and browser runs on the **default `file` state
  backend**; verify completion without `workflow_step.budget_stopped` and correct
  `observation.read` recovery of truncated evidence.
- Performance: before/after per-step prefill measurements on the dual-light
  profile recorded in [Model baseline](../benchmarks/model_baseline.md).
- Audit: `workflow_step.prompt_compressed` continues to fire; new
  `workflow_step.observations_compacted` events carry byte counts.

## 7. Delivery Order

Per-topic commits, mechanical moves never mixed with behavior changes:

1. 0.1 observation de-duplication (behavior, small).
2. 0.3 prefix reordering (mechanical reorder + tests).
3. 0.4 budget alignment + calibration constants.
4. 1.2 `observation.read` (independent; enables lossless compaction).
5. 0.2 rolling compaction (behavior, depends on 0.1 and 1.2).
6. 1.1 ContextBuilder (mechanical consolidation first, then budget wiring).
7. 1.3 episode summarization (independent).
