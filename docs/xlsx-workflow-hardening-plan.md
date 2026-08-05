# XLSX Workflow Hardening Plan

> Language: English | [简体中文](../zh-cn/docs/xlsx-workflow-hardening-plan.md)

Status: proposed implementation plan. No runtime behavior described here is
implemented until the corresponding code, tests, evaluations, and current-state
documentation land.

This plan hardens the existing XLSX path before adding wider spreadsheet
features. It covers four changes: XLSX-specific structured evidence, safe
`update_row` semantics and evidence binding, fail-closed package preservation,
and measured operation selection. It amends the implementation direction in
[Document workflows](document-workflows.md) without creating a second document
router or a new user-visible capability leaf.

When implementation is complete, merge the durable contracts into
`document-workflows.md`, `workflow-capabilities.md`, and the architecture guide,
then delete this plan and its Chinese mirror.

## Outcome

After this plan lands:

- `document.read` and `document.edit` receive bounded XLSX evidence containing
  canonical sheet, row, and cell locations instead of relying on tab-separated
  workbook text.
- `xlsx.update_row` updates only the explicitly supplied leading cells and
  cannot silently clear trailing cells or formulas.
- every XLSX mutation is bound to the exact read evidence that authorized it;
  stale or conflicting workbook, row, or cell evidence blocks before approval.
- successful XLSX edits have a verified package-preservation state. Workbooks
  containing unsupported or unverified package features block before mutation
  rather than returning an output with `package_preservation=unknown`.
- the existing `select_edit_operation` node chooses among the six XLSX editor
  operations using precise directory boundaries, and a labeled evaluation
  measures the real Fast model instead of only injecting an expected entry ID.

The semantic route remains:

```text
owner request
  -> document.read r4 or document.edit r6
  -> deterministic document target and XLSX format binding
  -> direct_once files.read
  -> select_edit_operation (edit only)
  -> one exact XLSX editor
  -> Policy and Approval
  -> output-copy write
  -> typed reread and package-preservation verification
```

`document.edit` advances from revision 5 to revision 6 because editor input
schemas, mutation semantics, evidence bindings, operation-selection rules, and
success criteria change. `document.read` remains revision 4: its plan and
user-visible boundary do not change, while its internal XLSX evidence projection
becomes more precise.

## Current Gaps

### XLSX evidence is parsed but not provisioned

`xlsx_read.js` already records sheet names, row indexes, A1 addresses, displayed
values, formulas, number formats, hidden state, style hints, merged ranges,
comments, hyperlinks, and embedded images. `blocksFromSheets`, however, reduces
each non-empty cell to text plus a location. The structured workflow evidence
slicer projects document operation, paragraph, table, and page evidence but has
no sheet projection. For XLSX it therefore falls back to one large content
string; when that string does not fit the stage evidence budget, it may be
omitted as a whole.

The operation-selection and editor stages can consequently receive workbook
metadata without the exact row or cell needed to choose and execute an edit.

### `update_row` clears more than its contract declares

The registered description says that `xlsx.update_row` replaces a row's leading
cells. The adapter currently clears `row.values` before writing the supplied
array, so omitted trailing cells are deleted. Current preservation checks permit
the target row to change and verify only the supplied value prefix, which cannot
detect that loss.

### Package preservation is not a success invariant

The high-level reread checks visible content and parser-exposed enrichment.
Formula identity, complete style state, tables, charts, conditional formatting,
pivots, external links, connections, and other OOXML parts are not all covered.
Successful edits therefore report high-level preservation as verified while
package preservation remains unknown.

### Operation selection is structurally tested but not calibrated

The decision node correctly freezes one directory entry and fails closed on an
invalid selection. XLSX workflow tests mostly inject the desired `entry_id`, so
they prove orchestration and persistence but not the Fast model's ability to
distinguish `replace_text`, `update_cell`, `update_row`, `insert_row`,
`append_row`, and `delete_row` over realistic workbook evidence.

## Invariants

Implementation must preserve these existing boundaries:

- `capability.Catalog` remains the only capability topology. Do not add an XLSX
  route leaf, keyword fallback, model-owned `RouteDecision`, or second operation
  map.
- recent-document resolution and deterministic preflight continue to freeze one
  governed path, format, document ID, and output path.
- `document_locate_evidence` remains `direct_once`; it invokes only the
  format-qualified `files.read` entry and never asks a model whether to read.
- `select_edit_operation` remains the only multi-candidate editor decision and
  persists one exact directory entry before materialization.
- the editor model cannot replace bound paths, source hashes, or target hashes.
- every mutation remains reversible, approval-gated, written to a new output,
  reread, verified, linked to its parent `DocumentRecord`, and cleaned up on any
  validation failure.
- the complete tool observation remains an artifact. Model context receives a
  bounded consumer-specific projection, not an unbounded workbook dump.

## Workstream 1: XLSX Structured Evidence

### Contract

Add one internal projection named `xlsx_sheet_evidence_v1`. It is derived from
`structured_document_v1.sheets`; it is not a new persisted domain record or a
ToolHub capability.

The projection contains:

```json
{
  "schema_version": "xlsx_sheet_evidence_v1",
  "source_complete": true,
  "selection_complete": true,
  "sheets": [
    {
      "name": "Data",
      "index": 1,
      "state": "visible",
      "rows": [
        {
          "index": 12,
          "hidden": false,
          "source_hash": "sha256:...",
          "cells": [
            {
              "address": "B12",
              "column": 2,
              "value_kind": "number",
              "raw_value": 42,
              "display_text": "42.00",
              "formula": "",
              "number_format": "0.00",
              "hidden": false,
              "style_hash": "sha256:...",
              "merge_anchor": ""
            }
          ]
        }
      ]
    }
  ],
  "omitted": {
    "sheets": 0,
    "rows": 0,
    "cells": 0,
    "reason": ""
  }
}
```

`value_kind` is one of `blank`, `string`, `number`, `boolean`, `date`, `error`,
`formula`, `rich_text`, or `unknown`. Dates use an ISO-8601 raw value. Formula
cells keep the formula separately from their cached/displayed result. Rich text
may be read as display evidence but is not a mutation value in this revision.

Every row and cell receives a canonical source hash. Hash input is stable JSON
over the exact semantic fields used by mutation and preservation; object key
order, volatile parser metadata, and display-only warnings are excluded. A row
hash includes ordered cell addresses, value kinds, raw values, formulas, number
formats, style hashes, hidden state, and merge anchors.

### Selection and budgets

One deterministic projector serves both operation selection and editor argument
generation. Given the frozen owner request and the complete structured
observation, it packs evidence in this order:

1. workbook manifest and every sheet name/state;
2. explicitly named sheet, A1 cell, row, or range anchors from the untouched
   owner request;
3. exact text/value matches from the complete structured representation;
4. the first two non-empty rows of relevant sheets for column/header context;
5. the last two non-empty rows when append/end/last-row meaning is present;
6. neighboring rows around selected cells or row anchors;
7. remaining rows in stable sheet/row order until the byte budget is full.

Explicitly resolved targets are mandatory. If a requested sheet, cell, or row
exists in the complete representation but cannot fit in the stage budget, the
projector returns `selection_complete=false` and the decision/editor stage
blocks. It must never silently substitute an unrelated prefix. Omitted counts
remain explicit so a model cannot claim full workbook coverage from a slice.

The projector is candidate-neutral: it extracts spreadsheet anchors and values
but does not choose `document.read`, `document.edit`, or an editor operation.

### Integration points

- retain typed XLSX value/formula/format fields when normalizing sheet cells;
- add XLSX sheet evidence to `documentReadEvidence` for ordinary observations;
- add the same projection to `sliceDocumentStructuredEvidence` so
  `workflow_operation_selection` and the edit step receive identical anchors;
- keep full structured output in the observation artifact for trace/read-back;
- record selected and omitted sheet/row/cell counts in the existing evidence
  provisioning audit event.

No new configuration knob is required. The projection obeys the existing
`workflow_stage_evidence_max_bytes` budget.

## Workstream 2: Safe Row Updates and Evidence Binding

### `xlsx.update_row` semantics

Keep the public tool name and align execution with its existing description:

- `values[0]` updates column 1, `values[1]` updates column 2, and so on;
- the array must be non-empty;
- explicit `null` clears that supplied cell;
- omitted trailing cells remain byte- and semantically unchanged;
- formula, style, comment, hyperlink, merge, row height, and hidden-state data
  outside the supplied prefix are not mutation targets;
- the result reports the exact changed cell addresses and before/after typed
  values, not a constant `changed=1` row summary.

Remove the adapter's whole-row clear. If a future feature needs full replacement,
it must register a separate `replace_row` operation with a complete-row schema;
that operation is outside this plan.

### Frozen evidence

Revision 6 binds editor calls to the completed current-run XLSX read:

- all XLSX editors receive a Runtime-owned `source_sha256` for the input
  workbook. A model-supplied conflicting value blocks before Policy/Approval.
- `update_cell` receives `source_cell_hash` when the cell already exists.
- `update_row`, `delete_row`, and position-based `insert_row` receive
  `source_row_hash` for their exact row anchor.
- `append_row` receives `source_sheet_hash`, including the last structured row
  boundary used to compute the append location.
- a missing required source hash, a hash from another node/run, or a hash that
  does not match the editor's fresh reread blocks without creating approval.

Runtime owns these bindings just as it owns input/output paths. The model may
select new values but cannot author or replace evidence identity.

Sheet names are canonicalized from read evidence before execution. Matching may
be case-insensitive, but the adapter always receives the exact workbook sheet
name. Cell addresses are normalized to uppercase A1 form.

### Typed mutation verification

Do not compare the requested value only to ExcelJS `cell.text`. Verification
uses the same typed cell projection as Workstream 1:

- raw scalar values compare by type and value;
- displayed text and number format are independent fields;
- replacing a formula requires a future explicit formula operation; a scalar
  `update_cell` may replace a formula only when the request targets that exact
  formula cell and approval shows that destructive delta;
- `update_row` verifies every supplied cell and verifies that trailing cells
  retain their before hashes;
- unchanged cells in the target row are no longer excluded wholesale from
  preservation checks.

Changing `update_row` from whole-row replacement to prefix-only update is a
user-visible defect fix and must land in its own commit and release note.

## Workstream 3: Fail-Closed XLSX Package Preservation

### Package manifest

Add an XLSX package inspector under `internal/document`. It reads the OOXML ZIP
centrally and returns a typed manifest used by both preflight and post-edit
verification. Tool scripts must not create a second feature taxonomy.

The manifest records:

- content types and the relationship graph;
- worksheets, shared strings, styles, themes, workbook properties, and calc
  chain presence;
- comments, hyperlinks, merged ranges, drawings, and images;
- tables, charts, conditional formatting, data validation, pivots/caches,
  slicers, external links, connections, embedded objects, custom XML, macros,
  signatures, and protection/encryption markers;
- raw hashes for opaque parts that the operation must preserve exactly;
- canonical semantic fingerprints for known mutable worksheet/style parts.

### Capability gate

Define one code-owned support table keyed by feature class and operation. It is
the single source of truth for whether the current ExcelJS path can safely
round-trip a package feature.

Initial policy is conservative:

- a feature is `verified` only after a fixture proves read, write, reread, and
  package preservation for every affected operation;
- unknown, unsupported, encrypted, signed, macro-bearing, external-link,
  connection, pivot, slicer, embedded-object, or unverified chart/table feature
  blocks mutation before approval;
- read-only workflows remain allowed and report partial/unsupported coverage;
- parser absence or malformed relationship data is an explicit package error,
  never a warning-only success.

The support table is derived by implementation registration, not synchronized
name lists across the parser, editor, and Workflow packages.

### Delta verification

Post-edit validation compares before and after manifests with an operation
allowlist:

- `update_cell` may change only the selected cell's typed value/formula state,
  the containing worksheet serialization, and required calculation metadata;
- prefix `update_row` may change only supplied cells;
- insert/delete/append may change the selected sheet's row structure, affected
  merges and formula references only when explicitly supported and verified;
- comments, hyperlinks, images, opaque parts, relationships, content types,
  unrelated sheets, styles, and workbook properties must remain unchanged
  unless the operation declares and verifies that delta.

Any unreported delta removes the output and returns `preservation_mismatch`.
Successful XLSX edits set `package_preservation=verified`; they must not return
`unknown`. The change summary includes typed target deltas, checked feature
classes, and any read-only coverage notes.

## Workstream 4: Operation-Selection Boundaries and Evaluation

### Directory metadata

Keep the existing exact format/operation capability descriptors. Replace the
generic XLSX directory guidance with operation-specific metadata:

| Operation | Use when | Do not use when |
|---|---|---|
| `replace_text` | The owner gives explicit old/new text and intends matching text cells to change. | A cell/row location is explicit, values are typed, or the change is structural. |
| `update_cell` | Exactly one cell or one field in an evidence-located row changes. | Multiple cells in a row change or a row is inserted/deleted/appended. |
| `update_row` | Multiple leading cells of one existing evidence-bound row change while trailing cells remain. | The request creates a new row, removes a row, or replaces arbitrary workbook text. |
| `insert_row` | A new row is required before or after an explicit existing row. | The request says end/append with no positional anchor, or modifies an existing row. |
| `append_row` | A new row belongs after the final structured row of one sheet. | A before/after anchor is explicit or an existing row changes. |
| `delete_row` | The owner explicitly removes one complete evidence-bound row. | The owner clears one cell, deletes text, or deletes the workbook itself. |

Add the same distinctions to `documentEditProfile.DecisionRules`. Rules remain
format-neutral where possible, while XLSX-only details come from directory
entries. Do not add keywords to the semantic router or freeze an operation in
`RouteDecision`.

### Evaluation corpus

Add a labeled XLSX operation-selection corpus consumed by the existing eval
infrastructure. It contains at least:

- eight direct/paraphrased cases per operation, split evenly across Chinese and
  English;
- explicit A1, row-number, header/value, before/after, end-of-sheet, and exact
  old/new-text forms;
- sibling-confusion cases such as one-cell versus row update, insert versus
  append, clear-cell versus delete-row, and replace-text versus typed update;
- negation, quotation, troubleshooting, unsupported-operation, ambiguous-target,
  and whole-file deletion hard negatives;
- small and near-budget workbook evidence, formulas, formatted numbers, hidden
  rows, merged cells, and multiple sheets.

Deterministic unit tests continue to verify directory scope, strict JSON,
persisted decisions, retries, and fail-closed behavior. A configured Fast-model
evaluation executes the real `workflow_operation_selection` prompt without
`MOCK_OPERATION_SELECTION_RESPONSE` injection and records model/profile
fingerprints, selected entry, retries, and reason.

Release gates:

- 100% correct selection for `delete_row` and every unsupported/ambiguous case;
- at least 95% exact operation accuracy overall on the labeled holdout;
- zero cross-format selections and zero mutation after an empty/invalid choice;
- no regression in the existing document-vs-conversation, read-vs-edit, or
  file-lifecycle routing corpus.

Prompt or directory changes that miss these gates do not ship. Routing fusion
weights and thresholds are outside this evaluation and must not change without
their own calibration evidence.

## Implementation Map

| Area | Primary files | Responsibility |
|---|---|---|
| XLSX parsing and adapter | `internal/toolhub/scripts/xlsx_read.js`, `xlsx_structure.js` | Preserve typed values and apply prefix-only row updates. |
| Document contracts | `internal/document/normalize.go`, `preservation.go`, new XLSX package inspector | Canonical hashes, typed comparisons, feature gate, package deltas. |
| Tool schemas and execution | `internal/toolhub/toolhub.go`, `registry.go`, `document_tools.go`, `document_workflow.go` | Exact schemas, directory metadata, trusted source bindings, output details. |
| Workflow evidence and binding | `internal/agent/tool_result_adapter.go`, `workflow_evidence.go`, `workflow_profiles.go`, a focused XLSX binding helper | Bounded sheet projection, r6 bindings, selection rules. |
| Verification | `internal/document/*_test.go`, `internal/toolhub/*_test.go`, `internal/agent/*_test.go`, eval fixtures | Safety, package parity, selection accuracy, end-to-end behavior. |
| Current-state docs | `document-workflows.md`, `workflow-capabilities.md`, `architecture.md`, Chinese mirrors | Publish only behavior that passes all gates. |

No Store interface change is expected. If implementation discovers a durable
state requirement, it must be reviewed separately and implemented in memory,
file, and PostgreSQL backends together.

## Delivery Sequence

Each behavior change is a separate topic and commit:

1. Add failing fixtures and projection tests for XLSX sheet evidence.
2. Implement `xlsx_sheet_evidence_v1` and stage provisioning.
3. Add failing trailing-cell/formula and stale-evidence tests.
4. Fix `update_row`, add trusted hashes, and mark the defect behavior change.
5. Add package-feature fixtures and the conservative preflight manifest.
6. Enforce the support gate and post-edit package delta verification.
7. Add operation-specific directory metadata and deterministic decision tests.
8. Add the real-model labeled eval and meet its release gates.
9. Update current-state English/Chinese docs, remove this completed plan, and
   inspect the final diff for generated or test-run artifacts.

Mechanical evidence plumbing, mutation behavior, package gating, and prompt
metadata must not be combined into one commit.

## Verification Matrix

Implementation is not complete without all of the following:

- XLSX projection tests for multiple sheets, case-preserved names, A1 anchors,
  row indexes, typed values, formulas, number formats, hidden rows/columns,
  merged ranges, comments, hyperlinks, images, and byte-budget omission counts;
- stage-evidence tests proving an explicit target near the end of a workbook is
  present under the 8000-byte default budget;
- mutation tests proving `update_row` preserves trailing values, formulas,
  styles, comments, hyperlinks, and merges;
- stale/missing/conflicting source hash tests proving no approval is created;
- formatted-number, Boolean, date, blank, error, and formula-cell verification;
- package fixtures for every supported and blocked feature class, including
  cleanup of rejected outputs;
- real operation-selection evaluation and sibling/hard-negative coverage;
- end-to-end `document.edit` r6 approval, resume, output lineage, and delivery;
- existing DOCX, PPTX, PDF, text, document-read, routing, and tool-registry tests
  remain green.

Run the proportional checks during implementation, then the full gate:

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/document ./internal/toolhub ./internal/agent ./internal/gateway
go test ./...
go vet ./...
cd ../..
bash scripts/run-eval.sh
```

Also run the bilingual documentation and local-link check from CI. No model
quality claim is valid from a single smoke call; retain the labeled corpus,
model fingerprint, repeated results, and failures as review evidence.

## Acceptance Criteria

The plan is complete only when:

1. both document stages receive canonical XLSX sheet/row/cell evidence and an
   explicit requested target cannot disappear because of the byte budget;
2. `update_row` changes only supplied cells and all XLSX mutations reject stale
   or conflicting evidence before approval;
3. every successful XLSX output reports verified package preservation, while
   unsupported packages block before mutation and leave no output artifact;
4. the real Fast-model holdout meets the selection gates and the control-flow
   tests no longer serve as the only evidence of operation accuracy;
5. `document.edit` r6 is documented in English and Chinese, no old r5 run
   resumes under the new mutation contract, and all proportional/full checks
   are green.

## Out of Scope

- large-workbook chunking or streaming beyond the existing complete-read limit;
- batch/range edits, column operations, worksheet creation/rename/delete,
  formula authoring, or style authoring;
- WebChat-specific diff rendering or approval UI redesign;
- stale general fallback-copy cleanup outside the r6 Workflow path;
- routing-fusion weight or threshold changes;
- a second spreadsheet library or a generic arbitrary-OOXML patch tool.

Those items require separate evidence and contracts after this safety baseline
is complete.
