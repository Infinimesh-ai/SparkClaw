# Observation Compression And Evidence Provisioning Redesign

> Language: English | [简体中文](../zh-cn/docs/observation-compression-redesign.md)

Status: Implemented 2026-08-03. The uniform observation envelope, runtime
evidence provisioning, `observation.read`, rolling compaction, ContextBuilder
integration, and multi-observation finalization are delivered. Context-plan
item 1.3 (model-generated episode summaries) remains separate and open.

Scope: tool-result compression (`services/gateway/internal/agent/tool_result_adapter.go`),
the per-tool observation budget (`agent.go` `toolResultObservationBudget`),
workflow step prompt assembly, workflow finalization evidence, and the pending
items of the [Context assembly plan](context-assembly-plan.md). No external
API, store schema, or artifact-archive write changes. The internal artifact
store read interface is extended for bounded retrieval. The
`modelrouter.Chat(ctx, task, system, user)` two-string interface and the
JSON-in-text step protocol are kept.

Relationship to the Context assembly plan: this document is the design
authority for that plan's pending items 0.2 (rolling compaction), 1.1
(ContextBuilder), and 1.2 (`observation.read`), and adds two decisions the
plan did not cover: the uniform observation envelope and runtime evidence
provisioning. Item 1.3 (model-generated episode summaries) remains as
specified there and is unaffected.

## 1. Design Principle

A correct compression mechanism removes the need for large-budget tools.

Today two tools (`files.read`, `browser.snapshot`) are exempted from the
observation envelope cap and may inject up to ~44 KB of content into the
model-visible observation list. That exemption exists only because
compression is lossy and irreversible: whatever the envelope drops, the model
can never see again, so the tools whose content the workflow genuinely needs
were given "no compression" instead. The exemption is the symptom; the
irreversibility is the disease.

This redesign inverts the contract:

- The **observation envelope is uniformly small for every tool**. It is an
  index over persisted evidence — status, key structured fields, a short
  excerpt, truncation flags, and the artifact reference — never a bulk
  content carrier. No tool, capability, or configuration can widen it.
- **Content reaches a model step only through deliberate, consumer-sized
  channels**:
  1. **Runtime evidence provisioning** (primary, deterministic): a workflow
     stage declares which persisted evidence it needs and at what budget; the
     runtime materializes that slice from the artifact store into the step
     prompt. The model neither requests nor transcribes it.
  2. **`observation.read`** (secondary, model-driven): a read-only tool that
     returns a bounded window of any current-session artifact, for the cases
     a profile did not anticipate.

This mirrors the argument-binding philosophy already in the runtime: values
the execution depends on are materialized from persisted state, not trusted
from — or starved by — model-visible text.

## 2. Baseline Problems

- **P1 — The large-budget exemption is a name list and it is wrong.**
  `toolResultObservationBudget` (`agent.go`) grants the enlarged budget to
  `files.read` and `browser.snapshot` by tool name. `pdf.extract_text` and
  `images.inspect` are registered with the same `document.read` capability
  (`internal/toolhub/registry.go`) but fall through to the default 2400-byte
  envelope with a 1400-byte evidence cap: a PDF body reaches the model as a
  ~1.4 KB fragment while a text file arrives at ~44 KB. Downstream judgments
  (edit-operation selection, content questions) fail on exactly the formats
  the name list forgot.
- **P2 — The envelope serves two consumers with opposite size needs.** The
  same `ObservationSummary` string is (a) the current step's working
  evidence and (b) the run's and session's history record. Sized for (a) it
  explodes budgets; sized for (b) it starves (a). One size cannot be correct.
- **P3 — Large envelopes destroy the run budget.** A single exempted
  `files.read` can consume ~44 KB of the 48 KB
  `workflow_run_max_observation_bytes` cap, forcing early compact-mode
  swaps and `workflow_step.budget_stopped` terminations on multi-step
  document and browser tasks.
- **P4 — Compression is irreversible for the model.** Full outputs are
  archived with an `ObservationRef`, but only `files.read` has re-read
  semantics. Truncated PDF, web, and browser evidence cannot be recovered,
  which is the root reason P1's exemption was introduced.
- **P5 — Non-document finalization sees one observation.**
  `workflowFinalEvidence` (`workflow_runtime.go`) reads full persisted
  results for document-read calls but falls back to a single observation for
  every other workflow shape, degrading multi-step browser/search synthesis.

## 3. Target Design

### 3.1 Uniform observation envelope

- Delete the `files.read` / `browser.snapshot` branch from
  `toolResultObservationBudget`. Every tool call is adapted with
  `observation_summary_max_bytes` (default 2400) and the default evidence
  limit (1400). The existing three-tier degradation and
  `structured.message_truncated` marking are unchanged.
- The envelope always carries the artifact reference (`artifact_uri` /
  `ObservationRef`) and a standardized `next_step_hint` naming
  `observation.read` when evidence was truncated.
- `workflow_run_max_observation_bytes` stops being an envelope-size input;
  it remains only the run-level accumulation budget.

### 3.2 Runtime evidence provisioning

`workflowStageContext` gains declared evidence requirements:

- A requirement names a **source** (the completed outcome ref of a named
  plan node, or the newest artifact of a kind produced in this run — the
  same persisted-ref vocabulary argument bindings use), a **slicing mode**,
  and a **byte budget**.
- Slicing modes: `head` (leading bytes), and `structured`
  (artifact-kind-aware: a browser snapshot slice keeps whole element
  entries and never cuts a ref mid-entry; a document slice keeps whole
  paragraphs/rows). Mode implementations live next to the evidence
  extractors in `tool_result_adapter.go`.
- At step start the runtime resolves each requirement against persisted
  state, reads the archived full output, renders the slices into a
  dedicated `PROVISIONED_EVIDENCE` section of the user prompt (between the
  observation list and the output contract, preserving the
  contract-is-the-tail invariant), and audits
  `workflow_step.evidence_provisioned` with the source ref, the provisioned
  byte count, and the total artifact byte count, so provisioned coverage
  of large artifacts stays queryable.
- Requirements are validated like tool plans: the source must resolve to a
  persisted ref of the active plan; a missing required source blocks the
  stage (fail closed). Profiles may mark a requirement optional.
- Per-stage totals are clamped by a new `runtime` config key
  `workflow_stage_evidence_max_bytes` (default 8000).

Converted consumers (the ones that today depend on the P1 exemption):

- `document.edit` — the `select_edit_operation` and `document_edit` stages
  declare the completed `document_locate_evidence` outcome as provisioned
  evidence instead of relying on an oversized `files.read` envelope.
- `browser.interaction` / `browser.automation` — stages that choose or
  validate element refs declare the current generation's settled snapshot
  artifact with `structured` slicing.
- Finalization — `workflowFinalEvidence` becomes a provisioning consumer:
  document-read content keeps its existing 8000-rune budget, and the
  non-document fallback packs **multiple** observations under the same
  total budget instead of exactly one (fixes P5).

### 3.3 `observation.read`

As specified in the Context assembly plan item 1.2, unchanged in substance:
arguments `artifact_uri` (required), `offset`, `max_bytes`; read risk, no
approval; session-scoped opaque keys; registered in
`internal/toolhub/registry.go` so the registry consistency test covers it;
exposed in every model-step scope. Its result passes through the same
uniform envelope, with the requested window as the evidence excerpt.

### 3.4 Rolling observation compaction

As specified in plan item 0.2: when the run observation budget is reached,
compact the oldest half to one-line entries (tool, status, key fields,
artifact ref, `compacted=true`), never the newest two, order preserved,
audited as `workflow_step.observations_compacted` with byte counts; stop
only if still over budget after full compaction. With uniform envelopes the
worst case is bounded (32 calls x ~2.4 KB ≈ 77 KB), so compaction is the
normal long-run path rather than a cliff, and it is lossless because 3.3
provides read-back.

### 3.5 ContextBuilder integration

Plan item 1.1 proceeds as specified, with one addition: provisioned
evidence registers as its own section with its own budget, ordered between
current-run observations and the output contract. The builder enforces the
combined admission budget (lane context x 0.85 from plan item 0.4); when
degradation is required, provisioned evidence degrades by reducing slice
budgets before observations are compacted further.

### 3.6 Cross-run context is an index, on purpose

The session-context trims (8 messages x 360 chars, capped tool summaries,
compact variants) are correct for their role as a history index and are not
enlarged. Follow-up quality improvements come from plan item 1.3
(model-generated episode digests) and existing deterministic resolvers
(recent-document resolution re-reads the file rather than trusting a
summary), not from carrying more prose across runs.

## 4. What Is Removed

- The tool-name exemption branch in `toolResultObservationBudget`, and with
  it every path by which an envelope can exceed
  `observation_summary_max_bytes`.
- The coupling between `workflow_run_max_observation_bytes` and per-message
  envelope size.
- The single-observation finalization fallback.

## 5. Configuration And Audit Surface

| Key (`runtime` section) | Default | Role after this redesign |
|---|---|---|
| `observation_summary_max_bytes` | 2400 | The only envelope size knob, applied to every tool |
| `workflow_stage_evidence_max_bytes` | 8000 | New: per-stage provisioned-evidence ceiling |
| `workflow_run_max_observation_bytes` | 48000 | Run accumulation budget only; triggers compaction (3.4), no longer inflates envelopes |

New audit events: `workflow_step.evidence_provisioned`,
`workflow_step.evidence_blocked`, and
`workflow_step.observations_compacted`. Existing events
(`workflow_step.prompt_compressed`, `workflow_step.budget_stopped`,
`structured.message_truncated` marking) are unchanged in meaning;
`budget_stopped` frequency is expected to drop and becomes a regression
signal.

## 6. Compatibility

- Persisted runs whose observations were written under the old enlarged
  envelopes load unchanged; the read path does not re-normalize them
  (normalize-on-read is a known store anti-pattern in this repo).
- Artifact archival (`store.ArchiveToolObservation`) is unchanged. The
  artifact store interface adds bounded retrieval, implemented by the
  filesystem and S3-compatible backends; the unsupported backend continues
  to fail explicitly.
- Browser r2 plan shapes are unchanged; only their stage contexts gain
  evidence requirements.

## 7. Risks

- **Local models may not use `observation.read` well.** Accepted: the
  primary content channel is deterministic provisioning; `observation.read`
  is the escape hatch, prompted by the standardized `next_step_hint`.
- **Structured slicing must never emit unusable fragments** (a cut element
  ref is worse than a smaller whole list). Slicing is artifact-kind-aware
  and tested per kind.
- **Provisioning changes document and browser behavior.** Both are
  validated by scenario runs on the default `file` backend and the golden
  eval before the exemption is removed (see delivery order — provisioning
  lands first, removal second, so no intermediate state loses evidence).
- **Larger provisioned slices raise prefill cost.** Bounded by
  `workflow_stage_evidence_max_bytes` and offset by the stable-prefix
  ordering already implemented (plan item 0.3).

## 8. Validation

Per the engineering baseline and refactoring playbook: record the full test
baseline first (`npm run setup:document-tools` before judging
`internal/toolhub`), then per stage:

- Unit: uniform envelope cap for all tools including `pdf.extract_text` /
  `images.inspect`; requirement resolution, fail-closed behavior, and
  slicing per artifact kind; compaction trigger/ordering/byte accounting;
  registry consistency covers `observation.read` automatically; finalizer
  multi-observation packing.
- Scenario, on the default `file` backend: long PDF read + edit
  (previously starved by P1), 16+ step document/browser runs completing
  without `workflow_step.budget_stopped`, browser ref-click flows with
  provisioned snapshots, truncated-evidence recovery via `observation.read`.
- Golden eval and the audit checks in section 5.
- Performance: per-step prefill measurements on the dual-light profile
  recorded in [Model baseline](../benchmarks/model_baseline.md).

## 9. Delivery Order

Per-topic commits; mechanical moves never mixed with behavior changes.
Ordering is load-bearing: provisioning must land before the exemption is
removed, otherwise document/browser judgments lose their evidence channel.

1. `observation.read` (independent; enables lossless compression).
2. Evidence provisioning: stage-context requirements, resolution,
   slicing, prompt section, audit; convert `document.edit`, browser r2
   stages, and finalization (includes the P5 multi-observation fix).
3. Remove the large-budget exemption (small behavior change; P1 and P3
   resolved).
4. Rolling compaction (behavior; depends on 1).
5. ContextBuilder consolidation with the provisioned-evidence section
   (mechanical first, then budget wiring).
6. Episode digests proceed independently under the Context assembly plan.
