# Workflow Minimum Evidence Projection Redesign

> Language: English | [简体中文](../zh-cn/docs/workflow-minimum-evidence-projection-design.md)

Status: Implemented on 2026-08-11 for the shared projection audit lifecycle,
document operation selection, PPTX semantic repair, document-read finalization,
and Browser r2 transition/presentation paths. The live results below remain the
pre-implementation baseline. Phase 4's broad generic-observation and typed
completion cleanup remains deferred.

This document refines [Workflow evidence ownership and reuse](workflow-evidence-ownership.md).
It does not replace that ownership model. It defines how Runtime should build
the smallest *sufficient* projection for each semantic consumer, how it should
prove that the projection is sufficient, and how invalid semantic output should
be repaired without reacquiring the source.

## 1. Decision

Minimum evidence is not the fewest bytes that fit in a prompt. It is the
smallest typed projection that lets one consumer resolve one declared semantic
variable without guessing, while Runtime retains and later binds every
deterministic identity, locator, version, hash, and freshness fact.

The target contract is:

1. One acquisition creates one immutable observation event and archives its
   complete output once.
2. Every model call declares exactly which semantic variable it resolves.
3. Runtime derives a consumer-specific projection from the observation event.
   Selection, generation, verification, and finalization do not share a generic
   evidence dump merely because they share a source.
4. Every projection carries source lineage, consumer coverage, omissions, and
   a machine-enforced output schema. `source_complete` alone is not sufficient.
5. Runtime resolves opaque model choices back to authoritative locators and
   rejects stale, foreign, structurally invalid, contradictory, or no-op output.
6. A recoverable typed output error gets at most one same-stage repair against
   the same projection. It does not trigger a second document read or browser
   snapshot unless the source is actually stale or the projection declares a
   coverage gap.
7. Workflow completion is evaluated against a typed completion predicate, not
   tool success, free-form rationale, or the presence of a final answer.

The design is format- and site-general. Document adapters contribute typed
structure; browser adapters contribute typed state transitions. They do not
hard-code a slide number, paragraph heading, workbook contents, site folder,
or test wording.

## 2. Live Test Basis

The tests used the running Gateway at `http://127.0.0.1:18789`, real workspace
attachments, the configured local models, real approvals, and the persistent
QQ Mail browser profile. The DOCX and PPTX inputs were selected from the
attachments with the largest existing edited-copy history. No XLSX attachment
was present, so the user-authorized fixture was created and uploaded.

The browser retest used the already modified, running worktree deployment. Its
result describes the current runtime, not a released baseline, and none of
those pre-existing browser changes were authored or altered during this design
work.

| Case | Request and source | Run | Result |
|---|---|---|---|
| DOCX | `完善心得与体会`; `upload_69cfdb7ca13f2b82-file-3.docx` | `run_f88a2970abe051f0` | Passed after approval; wrote an edited copy and preserved the source |
| PPTX | `完善第三页ppt`; `upload_8a1ac66704f22841-file.pptx` | `run_46ae13ce8c3cc478` | Blocked before approval because one generated replacement was empty |
| XLSX | Append student `2026004 / 陈曦` at the end of `学生信息`; `upload_9eebb0ddb7105a2e-student-roster.xlsx` | `run_b7ed0f7334561020` | Blocked after two operation-selection calls returned an empty `entry_id` |
| PDF | Summarize the official Structured Outputs search result and state whether the PDF contains a complete JSON Schema | `run_e5f6dab07e9edf3e` | Passed; correctly reported that the four-page Kagi result snapshot contains no complete schema example |
| Browser, reported failure | `打开qq邮箱的草稿箱` | `run_47f0f0b33ad1f5b2` | Click reached the drafts route and transition validation passed, but a repeated click caused a settle timeout |
| Browser, current retest | Same request in a new session | `run_91135f1cf7231ad9` | Opened the drafts route in hidden and visible browsers, but accepted contradictory semantic output |

The XLSX fixture contains one visible sheet named `学生信息`, a two-column
`学号 | 姓名` header, and three data rows. Its small size is intentional: the
test distinguishes evidence insufficiency from operation-selection failure.

## 3. Measurements And Findings

### 3.1 Cross-case measurements

| Consumer | Archived observation | Model projection | Coverage observed | Finding |
|---|---:|---:|---|---|
| DOCX operation selection | 94,190 bytes | 7,733 bytes (8.2%) | Target heading and body were present | Correctly selected `replace_paragraph` |
| DOCX edit argument generation | Same 94,190-byte event | 6,393 bytes (6.8%) | Located paragraph was complete | Generated one valid replacement; no reread |
| PPTX edit generation | 8,394 bytes | 4,659 bytes (55.5%) | All 14 editable shapes on the frozen slide | Projection was sufficient; output contract was not |
| XLSX operation selection, each attempt | 15,773 bytes | 1,650 bytes (10.5%) | 1 sheet, 4 rows, 8 cells; no omissions | Evidence was sufficient; candidate selection failed twice |
| PDF finalization | 57,383 bytes | Full 6,954-byte extracted text plus coverage manifest | 4/4 native-text pages, no missing pages | Negative claim was supported by complete finalizer content |
| Browser snapshot, current retest | 20,375-20,545 bytes | 1,705 bytes (about 8.3%) | Returned controls fit; transition facts were outside the snapshot projection | Smaller refs helped execution, but semantic consistency remained weak |

Projection bytes are useful telemetry, not a success criterion. The PPTX and
XLSX failures had complete target evidence. The old browser failure had a
compact projection but omitted the discriminating relationship between the
selected action and the verified result.

### 3.2 DOCX: successful event reuse

The structured read located `五、心得与体会` and its following body paragraph.
Operation selection and edit generation reused the same archived event but
received different projections because they resolved different variables:

- selection resolved `eligible_document_operation`;
- generation resolved the new paragraph content.

Runtime supplied the path, output path, source SHA-256, paragraph locator,
source hash, old text, and source evidence identity after model output. The
source was read once, the edit required approval, and the original file was not
overwritten. This is the baseline behavior to preserve.

The remaining inefficiency is representation duplication: the same target text
appears in two separately rendered projections. A shared derived target record
should back both views, while each view remains a distinct consumer projection.

### 3.3 PPTX: sufficient evidence, invalid generation contract

Runtime correctly froze `single_slide`, bound slide index 3, deterministically
selected `update_slide`, and projected only that slide's 14 editable text
shapes. The model then:

- merged the subtitle into the title;
- emitted an empty replacement for the original subtitle shape;
- emitted several updates whose replacement equaled the existing text.

The adapter rejected the empty replacement before approval with
`PPTX slide 3 shape 2 replacement text is empty`. This is a good fail-closed
boundary, but the whole Workflow terminated on a recoverable typed generation
error.

The general defect is not missing slide evidence. The model-visible schema does
not express the mutation invariants strongly enough, and Runtime has no bounded
same-stage repair contract. Non-delete text replacement must be non-empty;
semantic no-ops must be removed deterministically; and a remaining invalid
item must produce a typed repair request rather than an immediate Workflow
failure.

### 3.4 XLSX: complete evidence, weak candidate contract

The `xlsx_sheet_evidence_v1` projection was complete on both attempts:

- `selection_complete=true`;
- one selected sheet, four selected rows, and eight selected cells;
- zero omitted sheets, rows, or cells;
- the header, current final row, and owner-supplied new values were present.

The active directory contained six eligible XLSX editor entries. Both Fast
operation-selection calls returned an empty `entry_id`, and Runtime eventually
reported `no_registered_editor_matches`. Adding more workbook cells cannot fix
this failure.

The model is currently asked to map semantic mutation intent directly to an
opaque registry entry. The proposed projection instead supplies normalized,
bounded candidate contracts such as target kind, change kind, placement
capability, required owner content, and preservation behavior. The model still
selects only a candidate ID; Runtime maps that ID to the current directory
entry. This remains semantic selection, but removes irrelevant registry
representation from the decision.

### 3.5 PDF: correct final answer, incomplete projection telemetry

The PDF reader extracted all 4 pages as usable native text. The finalizer
reloaded the archived observation and received all 6,954 extracted bytes,
because the content was below its 8,000-rune bound. It therefore had sufficient
evidence to state that the attachment was a Kagi search-results snapshot and
did not contain a complete JSON Schema example.

The compact 2,366-byte tool observation summary was not the finalizer's actual
evidence. This distinction matters: the finalizer currently has no equivalent
`workflow_step.evidence_provisioned` audit record, so the actual finalization
projection and its claim coverage must be reconstructed from code and token
counts. Finalization needs the same explicit projection record and coverage
audit as other semantic consumers.

For larger documents, `read_complete=true` must not be confused with model
claim coverage. A negative claim such as “the document contains no example” is
allowed only when the finalizer projection declares that the relevant claim was
checked across all required pages or windows.

### 3.6 Browser: functional recovery without semantic integrity

In the reported failure, the first click on `Drafts` changed the rendered
content digest, reached `#/list/4`, settled, and passed deterministic transition
validation. The after-snapshot projection still looked like the same persistent
sidebar. The model concluded that the click had not worked, requested the same
semantic action again, and the second wait timed out because no new state could
occur.

The current retest reduced each model snapshot projection from 2,833 to 1,705
bytes and used short control refs. It reached `#/list/4` in hidden and visible
sessions and completed. It is not a clean semantic pass:

- five Deep workflow model calls were made after routing;
- the initial snapshot was projected twice, once for assessment and once for
  click selection;
- the visible assessment stage first returned an invalid final answer, which
  Runtime rejected because the required assessment tool had not been called;
- the hidden assessment produced a long self-conflicting rationale before
  choosing `satisfied`;
- the visible assessment returned `verdict=satisfied` while its required
  `reason` explicitly said the goal was not satisfied. Runtime accepted the
  verdict and completed.

The core issue is that the snapshot projection is control-centric while the
consumer is transition-centric. Runtime knew the selected control, before/after
digests, route consistency, settled state, and hidden-to-visible handoff state,
but did not expose one compact derived transition assertion as the semantic
input. Free-form rationale is then treated as audit evidence even when it
contradicts the typed verdict.

## 4. Required Projection Model

### 4.1 Projection record and model payload

Runtime should persist one `EvidenceProjectionRecord` for every semantic call:

```text
EvidenceProjectionRecord
  projection_id
  projection_schema_version
  source_event_ids[]
  derived_assertion_ids[]
  consumer
    workflow_id, node_id, stage
    semantic_variable
    consumer_schema_version
  coverage
    source_coverage
    target_coverage
    claim_coverage
    candidate_coverage
    complete_for_consumer
    omissions[]
  model_payload_digest
  model_payload_bytes
  runtime_binding_manifest_ref
  created_at
```

The model receives only `model_payload`. The binding manifest is not prompt
content. It stores authoritative paths, hashes, document locators, full browser
refs, generations, versions, directory revisions, and freshness predicates so
Runtime can resolve and revalidate model output.

### 4.2 Coverage is consumer-specific

Each projection must distinguish these dimensions:

| Coverage dimension | Question answered |
|---|---|
| Source | Did the adapter acquire all source units it claims to have read? |
| Target | Are all source units needed to identify the requested target included? |
| Claim | Is enough content present to support the requested positive or negative claim? |
| Candidate | Are all currently eligible choices represented in normalized form? |
| Transition | Are the action, before/after state, and deterministic validation facts represented? |
| Presentation | Is the visible result proven equivalent to, or safely derived from, the verified hidden result? |

`complete_for_consumer` is true only when all dimensions required by the
declared semantic variable are complete. If it is false, Runtime must acquire a
new bounded window, clarify, or block. The model must not be asked to compensate
for an undisclosed omission.

### 4.3 Projection by semantic consumer

| Consumer | Model-visible payload | Model output | Runtime-only data |
|---|---|---|---|
| Target selection | Owner intent, bounded target candidates, distinguishing semantic text, coverage | `candidate_id` or typed `no_match` | Paths, locators, hashes, versions |
| Operation selection | Normalized mutation intent and normalized eligible operation contracts | `candidate_id` | Directory entry IDs/revision and full tool definitions |
| Content generation | Selected operation contract, exact target content, nearby structure needed for coherence, output constraints | Operation-specific semantic arguments | Source/output paths, old-text guards, hashes, frozen scope |
| Effect verification | Frozen goal, relevant after-state, compact deterministic action/transition assertions, contradiction set | Verdict and evidence candidate IDs | Full refs, URLs, digests, generations, timestamps |
| Finalization | Claim-complete content/assertions and coverage/limitation manifest | User-visible answer only | Artifact paths, internal IDs, unrelated Workflow history |

Opaque IDs are projection-local. A model-selected ID is never directly
executable until Runtime resolves it against the exact source events and scope
revision recorded by the binding manifest.

## 5. Projection Construction

### 5.1 Coverage-first budgeting

Budgeting happens in this order:

1. Reserve space for projection identity, source lineage, semantic variable,
   and the coverage/omission manifest.
2. Include all mandatory candidates or target units. A mandatory unit that
   does not fit makes `complete_for_consumer=false`; it is never silently
   skipped.
3. Include exact owner-mentioned units and structurally adjacent context.
4. Include boundary context when the operation depends on a beginning, end,
   insertion point, or before/after state.
5. Spend remaining budget on diverse supporting context, not repeated copies
   of the same text.

This algorithm applies to paragraph blocks, table rows, slide shapes, PDF
pages/windows, browser controls, and transition deltas. Each adapter defines
typed units and adjacency, while the Runtime algorithm and omission rules stay
the same.

### 5.2 Reuse without representation duplication

One observation event may produce several projections, but repeated consumers
must share derived records:

```text
observation event
  -> typed source index + coverage manifest
  -> derived target set
       -> selection projection
       -> generation projection
       -> verification projection
       -> finalization projection
```

Reusing a derived target set avoids reparsing and repeated large strings. It
does not make the projections identical: the DOCX selector needs operation
distinctions, while the generator needs target content and writing constraints.

### 5.3 Browser transition projection

After an action, a browser semantic verifier should receive a compact derived
record rather than another independent control list:

```json
{
  "goal": "<owner goal>",
  "action": {
    "kind": "click",
    "candidate_id": "control_1",
    "semantic_label": "<accessible label>"
  },
  "transition": {
    "settled": true,
    "rendered_content_changed": true,
    "route_consistent": true,
    "same_session": true,
    "repeated_action": false
  },
  "after_state": {
    "relevant_controls": [],
    "relevant_status_text": [],
    "selected_state_known": false
  },
  "coverage": {
    "transition": "complete",
    "after_target_region": "bounded",
    "complete_for_consumer": true
  }
}
```

This is not a QQ Mail rule. It applies to any persistent navigation, tab,
accordion, menu, route, or client-rendered view. Full URLs, digests, snapshot
IDs, generations, and executable refs remain Runtime-owned.

For visible presentation, Runtime should first compare the hidden result and
visible state deterministically. If route, profile transfer, settled state, and
content equivalence meet the Profile predicate and no contradiction is
detected, it records a presentation-equivalence assertion and reuses the hidden
semantic verdict. A second model assessment is required only when the visible
state contains a material semantic delta.

## 6. Typed Output And Repair

### 6.1 Output schemas

Every semantic variable gets a discriminated output schema. Examples:

- selection: `{"status":"selected","candidate_id":"..."}` or
  `{"status":"no_match","reason_code":"..."}`;
- generation: an operation-specific object containing only semantic fields;
- verification: `{"verdict":"satisfied|progress|failed","evidence_ids":[...]}`;
- finalization: plain answer text, only after claim coverage is complete.

Free-form rationale must not determine execution. Prefer stable reason codes.
If explanatory text is retained for observability, it is non-authoritative and
must not be stored as a derived assertion. A direct contradiction between a
verdict and explanatory text is a validation error, not successful evidence.

### 6.2 Deterministic validators

Before Policy or approval, Runtime validates:

- selected IDs belong to the current projection and directory/snapshot scope;
- non-delete replacement text is non-empty after normalization;
- generated mutation arrays meet cardinality and size limits;
- semantic no-ops are removed using authoritative current values;
- at least one effective mutation remains when a mutation is required;
- citations belong to the bound current event;
- verdict and structured reason code are compatible;
- source versions, hashes, generations, and freshness still match.

These checks are generic invariants. They do not inspect a particular heading,
slide number, cell value, or web destination.

### 6.3 One bounded repair

A recoverable validation failure creates a typed `RepairRequest`:

```text
projection_id
invalid_output_digest
error_codes[]
invalid_item_indexes[]
original_output_schema_version
repair_attempt = 1
```

The repair call receives the same model payload, the invalid semantic output,
and only these typed errors. Runtime does not reread the source and does not
widen candidates. A second structural failure blocks. Stale source, incomplete
coverage, Policy denial, and approval rejection are not repairable generation
errors and follow their own paths.

Applied to the live failures:

- PPTX empty text and remaining no-op-only output trigger one generation
  repair, not a document reread or immediate Workflow termination.
- XLSX `no_match` while compatible normalized candidates remain triggers one
  selection repair against the same 1,650-byte evidence projection and reduced
  candidate schema.
- Browser contradictory verdict/rationale is rejected; removing free-form
  rationale from the executable contract eliminates this failure class.

## 7. Workflow Completion

The current generic `CompletionEvidence` vocabulary is too broad for these
cases. Profiles should declare a typed predicate composed from assertions:

| Workflow stage | Required completion predicate |
|---|---|
| Document localization | Target coverage complete and source version bound |
| Operation selection | One current eligible operation selected or a justified typed no-match |
| Mutation generation | Output schema valid and at least one effective change |
| Document execution | Approved mutation completed and preservation checks passed |
| Browser action | Action bound to current snapshot and completed |
| Browser effect | Before/action/after transition valid plus semantic goal verdict |
| Browser presentation | Visible state equivalent to verified hidden result, or separately reverified |
| Document finalization | Claim coverage complete or limitations explicitly required in the answer |

A model cannot return a final answer in a stage whose predicate requires a
tool or semantic verdict. The stage output schema should expose only the legal
variant, rather than offering a generic `final` alternative and rejecting it
after generation. This removes the wasted visible-browser call seen in the
retest.

## 8. Format Profiles Without Test-specific Rules

The common projection engine operates on typed units. Format policies only
provide structure and invariants:

- **DOCX:** blocks, headings, paragraphs, tables, story parts, and adjacency;
  Runtime owns paragraph locators and before-text guards.
- **PPTX:** slides, editable shapes, layout roles, and operation scope;
  Runtime owns slide/shape locators and rejects empty or no-op updates.
- **XLSX:** sheets, structured boundaries, rows, columns, cells, and styles;
  candidate contracts express cell/row and placement semantics, while Runtime
  binds sheet hashes and row/cell addresses.
- **PDF:** pages/windows, extraction source, and page coverage; finalizer claim
  coverage controls whether whole-document and negative claims are allowed.
- **Browser:** controls, semantic labels, rendered regions, actions,
  transitions, and presentation equivalence; Runtime owns full refs, route
  identity, digests, generations, and freshness.

Tests must include different paragraph positions, slide numbers, workbook
schemas, PDF lengths, sites, languages, and control labels. Passing by matching
the five literal prompts is explicitly insufficient.

## 9. Observability

Every semantic call should emit one `workflow.evidence_projection.created`
audit event containing:

- projection ID and schema versions;
- source event and derived assertion IDs;
- consumer Workflow/node/stage and semantic variable;
- archived bytes, projected bytes, and ratio;
- coverage dimensions, omissions, and `complete_for_consumer`;
- candidate and selected-item counts;
- repair attempt and validation error codes;
- whether the source event or a derived record was reused.

Audit must also record why a model call was skipped. Examples include one
deterministically eligible operation, presentation equivalence, and an already
satisfied structural predicate. This makes reduced model usage distinguishable
from silently missing work.

Finalization uses the same audit surface and now records the exact finalizer
payload bytes, source lineage, coverage, omissions, and binding reference.

## 10. Migration Plan

Phases 0 through 2 and the claim-aware finalization contract in Phase 3 are
implemented and covered by generalized contract tests. Additional persisted
window expansion and Phase 4 remain deliberately scoped to measured future
migrations; the current implementation fails closed with an explicit limitation
instead of overstating incomplete claim coverage, and does not add a second
evidence store or mechanically replace every `CompletionEvidence` use.

### Phase 0: measurement only

- Add projection records and uniform audit events around existing selection,
  generation, assessment, and finalization calls.
- Record all coverage dimensions without changing behavior.
- Establish fixture-independent baselines for bytes, calls, rereads, repairs,
  and failures.

### Phase 1: document contracts

- Normalize document operation candidates into projection-local contracts.
- Add typed selection output and one bounded repair.
- Strengthen mutation schemas and deterministic no-op filtering.
- Preserve current Runtime binding, approval, and output-copy behavior.

### Phase 2: browser transition evidence

- Introduce action/transition projections and repeated-semantic-action guards.
- Separate initial action choice from after-action goal verification.
- Replace visible semantic reassessment with deterministic presentation
  equivalence when no material delta exists.
- Remove free-form rationale from executable goal evidence.

### Phase 3: claim-aware finalization

- Give finalization an explicit projection and audit record.
- Add claim coverage for negative and whole-document claims.
- Request additional persisted windows when claim coverage is incomplete;
  reread only when the archived source itself is incomplete or stale.

### Phase 4: cleanup

- Retire generic observation representations that duplicate projection records.
- Replace overloaded `CompletionEvidence` usages with typed predicates.
- Keep full observation artifacts for provenance and replay, not prompt reuse.

## 11. Acceptance Criteria

The redesign is accepted only when both the five live scenarios and generalized
variants satisfy these invariants:

1. An unchanged document or browser event is acquired once; later consumers
   reuse it or an explicit derived assertion.
2. Every semantic model call has one declared variable, one projection record,
   complete consumer coverage, and a typed output schema.
3. No model output supplies an executable path, full browser ref, hash,
   generation, output path, or source version.
4. DOCX editing preserves the current one-read, copy-on-write, approval, and
   integrity behavior.
5. PPTX generation cannot submit empty non-delete replacements or effective
   no-op-only mutations; a first recoverable failure is repaired once.
6. XLSX selection resolves an explicit new end-boundary row request without
   additional workbook evidence, while ambiguous requests still fail closed.
7. PDF whole-document and negative claims require complete claim coverage and
   report limitations whenever that coverage is unavailable.
8. Browser workflows never repeat the same semantic action after a validated
   state change unless a fresh projection proves a distinct retry condition.
9. A browser verdict cannot complete when its typed evidence is contradictory;
   visible presentation either reuses a proven equivalent hidden result or is
   independently reverified.
10. Projection-size reduction never changes a required coverage dimension from
    complete to implicit or unknown.

The initial live targets are: one document read for each document mutation,
zero source rereads for repair, at most one repair per semantic stage, no
contradictory completion evidence, and complete projection telemetry for every
model call. Byte ratios are monitored but are not hard acceptance thresholds.

## 12. Implementation Boundaries

Implemented ownership is:

- shared projection record and audit lifecycle: [`workflow_evidence_projection.go`](../services/gateway/internal/agent/workflow_evidence_projection.go);
- source provisioning and coverage derivation: [`workflow_evidence.go`](../services/gateway/internal/agent/workflow_evidence.go);
- operation selection contract: [`workflow_decision.go`](../services/gateway/internal/agent/workflow_decision.go);
- Runtime/model argument boundary: [`workflow_model_projection.go`](../services/gateway/internal/agent/workflow_model_projection.go);
- XLSX and PPTX source projections: [`tool_result_xlsx.go`](../services/gateway/internal/agent/tool_result_xlsx.go) and [`tool_result_pptx.go`](../services/gateway/internal/agent/tool_result_pptx.go);
- typed format policies and binding: [`workflow_document_format_policy.go`](../services/gateway/internal/agent/workflow_document_format_policy.go);
- bounded semantic repair: [`workflow_semantic_repair.go`](../services/gateway/internal/agent/workflow_semantic_repair.go);
- browser state machine and transition projections: [`browser_workflow_r2.go`](../services/gateway/internal/agent/browser_workflow_r2.go) and [`workflow_browser_evidence_projection.go`](../services/gateway/internal/agent/workflow_browser_evidence_projection.go);
- finalization projection: [`workflow_final_evidence_projection.go`](../services/gateway/internal/agent/workflow_final_evidence_projection.go).

The implementation adds no literal prompt keyword for any live request. All
behavior is expressed through consumer, format, operation, coverage, candidate,
transition, validation, and binding contracts.
