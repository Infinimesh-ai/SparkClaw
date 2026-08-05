# DOCX Editing Optimization Plan

> Language: English | [简体中文](../zh-cn/docs/docx-editing-optimization.md)
>
> Status: Proposed implementation specification. This document does not describe
> shipped behavior. The documentation-only change that introduced it does not
> modify runtime code, schemas, configuration, or tests.

## 1. Purpose And Scope

This plan hardens the six highest-priority gaps in the existing DOCX read and
edit path:

1. align `docx.set_text_style` input, readback, and preservation contracts;
2. bind every DOCX mutation to current localization evidence and source version;
3. preserve run-level formatting during text replacement or fail closed;
4. report DOCX content coverage truthfully and expose high-value omitted parts;
5. make operation-selection evidence target-aware within one byte budget;
6. add multilingual routing, operation-selection, approval, and mutation evals.

The existing semantic and Workflow architecture remains authoritative. This
plan does not add keyword routing, a second capability map, a generic document
mutation tool, or another model-owned directory selector. It also does not add
table-cell editing or a large-document strategy; those remain later extensions.

## 2. Preserved Workflow Boundary

The edit path remains:

```text
semantic fusion
  -> document.edit revision 5
  -> confirm_document_target
  -> document_locate_evidence (direct_once files.read)
  -> select_edit_operation (one persisted directory entry)
  -> document_edit (only the selected editor is materialized)
  -> Policy and Approval
  -> output copy
  -> reread and preservation validation
```

The implementation must preserve these invariants:

- The governed input and output paths are Runtime bindings, never model choices.
- Operation selection stays inside the frozen DOCX scope and persists exactly
  one eligible directory entry.
- Evidence used to authorize a mutation comes from the single completed
  `document_locate_evidence` call in the same run, session, node revision, and
  governed path.
- Approval authorizes only the persisted operation, arguments, source version,
  and evidence-bound target that the owner reviewed.
- The original file remains unchanged; only a new sibling output is accepted.
- The output is reread through the same parser before the run can succeed.

## 3. Gap 1: Complete The Text-Style Contract

### Current gap

`docx.set_text_style` accepts `builtin_style`, `bold`, and `font_size_pt`, but
the normalized DOCX representation and preservation validator only prove the
paragraph's built-in style name. Bold-only and size-only edits therefore do not
have a valid round-trip success condition, while a request containing all three
properties can pass without independently proving bold and font size.

The input schema also permits an empty `style` object and does not require a
valid paragraph locator alternative, allowing invalid calls to progress farther
than necessary.

### Target input contract

- `style` is a strict object with at least one of `builtin_style`, `bold`, or
  `font_size_pt`; unknown fields are rejected.
- `font_size_pt` remains an integer in the inclusive range `1..200`.
- At least one of `paragraph_index` or `location` is required. If both are
  present, Runtime requires them to identify the same paragraph.
- `location` must identify a top-level DOCX body paragraph until another
  editable story part is explicitly registered.
- Invalid style or target arguments fail before Policy and do not create an
  approval.

### Parser and normalized representation

The DOCX reader must expose enough deterministic evidence to verify the three
properties after save and reload:

```json
{
  "index": 3,
  "style": "Heading 1",
  "runs": [
    {
      "index": 1,
      "text": "Quarterly summary",
      "bold": true,
      "font_size_pt": 18.0
    }
  ]
}
```

Run fields represent effective values after the document has been reopened,
not merely values reported by the editor subprocess. Missing or inherited
values stay explicit as `null` or as a separately named effective field; they
must not be guessed from display text.

### Post-edit verification

- `builtin_style` is compared case-insensitively with the reread paragraph
  style.
- Requested `bold` must match every non-empty run in the target paragraph.
- Requested `font_size_pt` must match every non-empty run using a small numeric
  tolerance suitable for OOXML point conversion.
- Unrequested style properties must retain their before-edit fingerprints.
- Target text and location must remain unchanged.
- A mismatch returns the existing typed preservation failure and removes the
  generated output.

### Acceptance cases

- Built-in style only, bold only, font size only, and all supported combinations.
- Explicit `bold: false` is distinguished from an omitted `bold` property.
- Empty style objects, missing targets, conflicting target forms, and out-of-range
  sizes fail before approval.
- Save/reload disagreement fails preservation even if the editor reports success.

## 4. Gap 2: Evidence-Bind Every DOCX Mutation

### Target evidence contract

Replace the operation-specific `docx.replace_paragraph` special case with one
DOCX mutation binding owned by Workflow Runtime. The persisted binding contains:

```text
source_tool_call_id
source_node_id and scope_revision
session_id and run_id
governed input path
source_document_sha256
operation
target or anchor location
target or anchor source_hash when applicable
normalized before text when applicable
```

`source_document_sha256` is the whole input-file version. `source_hash` remains
the normalized paragraph or match fingerprint. They are separate facts and
must not be overloaded.

The binding is derived only from the completed `files.read` observation in the
current `document_locate_evidence` node. A model may omit evidence-owned fields;
Runtime fills them. A model-supplied value is accepted only when it equals the
current evidence.

### Operation matrix

| Operation | Required bound evidence |
|---|---|
| `replace_text` | input SHA-256, exact matched locations and hashes, expected match counts |
| `replace_paragraph` | input SHA-256, paragraph location, paragraph source hash, optional exact old-text guard |
| `insert_paragraph` at `before` or `after` | input SHA-256 and anchor paragraph location/hash |
| `insert_paragraph` at `start` or `end` | input SHA-256 and the corresponding document boundary |
| `delete_paragraph` | input SHA-256, paragraph location/hash, normalized before text |
| `set_text_style` | input SHA-256, paragraph location/hash, before-format fingerprint |

Schema validation must express target alternatives. `before` and `after`
require an anchor; `start` and `end` reject an unrelated anchor. Delete, style,
and paragraph replacement always require exactly one resolvable paragraph.

### Two validation points

1. **Before Policy and Approval:** validate source provenance, path, operation,
   locator uniqueness, source hash, and model-supplied consistency. Failure does
   not create an approval.
2. **Immediately after approval, before adapter execution:** reload the run and
   call, recompute the governed input SHA-256, resolve the bound target again,
   and compare its hash and before value. A stale source fails without invoking
   the editor or writing an output.

The second check closes the approval wait-time race. Approved calls must not
execute solely because their persisted argument map was valid earlier.

### Acceptance cases

- Every DOCX operation rejects evidence from another run, session, node, path,
  or scope revision.
- Missing and conflicting evidence fails before approval.
- Changing the source file or bound paragraph after approval is requested makes
  approval execution fail closed with no output.
- An unrelated approved call cannot reuse a prior DOCX localization artifact.
- Start/end insertion remains usable without inventing a paragraph index.

## 5. Gap 3: Preserve Run-Level Formatting

### Replacement algorithm

DOCX text replacement must edit the minimum affected run spans instead of
clearing every run and writing all text into the first run.

For each paragraph:

1. Build the logical paragraph text and a mapping from character offsets to
   run indices and offsets.
2. Resolve all non-overlapping exact matches before mutating the paragraph.
3. For a match contained in one run, splice only that run's text; preserve its
   run properties and all surrounding runs at the OOXML property level supported
   by the parser.
4. For a match crossing runs, permit replacement only when every affected text
   run has the same formatting fingerprint and relationship boundary.
5. If a match crosses mixed formatting, hyperlink, field, drawing, tracked
   change, or another unsupported boundary, fail explicitly instead of
   flattening the paragraph.

For whole-paragraph replacement, preserve paragraph properties and use the
existing run style only when the source paragraph has one homogeneous text-run
fingerprint. A mixed-format paragraph is rejected until an explicit replacement
style policy is added to the public contract.

### Preservation fingerprints

The reader and normalized representation must expose stable run spans and the
parser-visible formatting needed to detect damage, including at minimum:

- bold, italic, underline, font name, font size, and color;
- hyperlink or relationship ownership;
- paragraph style and paragraph properties relevant to layout;
- unsupported-boundary markers for fields, drawings, and tracked changes.

Post-edit validation compares unaffected run fingerprints and relationship
boundaries. Only the expected text span and an explicitly requested style delta
may differ. Parser coverage remains explicit: unknown formatting cannot be
reported as preserved.

### Acceptance cases

- Replacement inside one bold or linked run preserves that run and its siblings.
- Replacement spanning homogeneous runs preserves the common formatting.
- Replacement spanning bold and non-bold runs fails without leaving an output.
- Paragraph replacement preserves paragraph style, numbering, indentation, and
  spacing when the source run formatting is homogeneous.
- Unchanged hyperlinks, fields, images, and relationships survive save/reload.

## 6. Gap 4: Make DOCX Coverage Truthful

### Coverage semantics

`coverage.content = complete` means every text-bearing DOCX story part detected
by package inspection is represented in normalized evidence. It must not mean
only that the adapter finished reading body paragraphs.

Until that condition is met, the reader reports `partial` and lists why. The
structured result adds per-scope status and omitted part evidence, for example:

```json
{
  "coverage": {
    "content": "partial",
    "content_scopes": {
      "body": "complete",
      "tables": "complete",
      "headers": "complete",
      "footers": "complete",
      "footnotes": "unsupported",
      "endnotes": "unsupported",
      "text_boxes": "unsupported",
      "tracked_changes": "partial"
    }
  },
  "extensions": {
    "status": "partial",
    "unparsed_parts": ["word/footnotes.xml"]
  }
}
```

The exact vocabulary must be shared with existing coverage normalization; no
parallel coverage enum is introduced.

### Delivery stages

1. Correct the current declaration to `partial` whenever package inspection
   cannot prove full text coverage.
2. Extract header and footer paragraphs, tables, hyperlinks, and images with
   stable section/story-part locations. Shared linked headers or footers are
   deduplicated by package part identity while retaining section references.
3. Inventory text-bearing OOXML parts and markers, including footnotes,
   endnotes, text boxes, tracked insertions/deletions, and `altChunk` content.
4. Add parsers in value order. Unsupported parts remain visible in coverage and
   never become implicit mutation targets.

Plain `content` may remain body-oriented for answer quality, but the structured
representation and operation-selection projection must include labeled
header/footer evidence when present.

### Acceptance cases

- A body-only fixture can be marked complete only after package inventory proves
  no omitted text-bearing parts.
- Header/footer text receives stable locations and appears once even when linked
  across sections.
- Documents containing footnotes, text boxes, or tracked changes report partial
  coverage until those parts are represented.
- Output reread preserves the same or better coverage status; a parser omission
  cannot silently become a preservation success.

## 7. Gap 5: Target-Aware Decision Evidence

### One budget and one unit

`Runtime.StageEvidenceMaxBytes`, backed by
`workflow_stage_evidence_max_bytes`, is the single source of truth. Operation
selection must not hardcode another `8000` limit, and documentation must describe
the configured byte budget rather than a separate 20,000-rune contract.

The default remains 8,000 bytes in this plan. The optimization changes evidence
selection, not model context size.

### DOCX decision projection

Add a document decision projection that receives the frozen owner request,
route target, eligible operation entries, and structured read result. It packs
whole evidence records in this order:

1. source metadata, format, coverage, and truncation state;
2. exact route-bound locations and explicit quoted-text matches;
3. matching paragraph/table anchors with bounded neighboring blocks;
4. operation context needed to distinguish replace, insert, delete, and style;
5. deterministic head/tail structural fallback when no target anchor exists.

The projector may rank existing evidence but must not select a capability or
operation. It is not a keyword router. It must never add a second catalog,
invoke another model, or make a mutation target authoritative without Workflow
binding.

Only complete UTF-8 records are packed. The projection reports selected and
omitted record counts, omitted location ranges, byte usage, and the anchors that
caused prioritization. Compact and minimal projections repeat the same ordering
at smaller budgets rather than reverting to a raw prefix.

### Acceptance cases

- A requested paragraph near the end of a long DOCX remains visible within the
  default budget.
- Chinese and English explicit anchors select the same stable location.
- No-anchor requests receive metadata, operation context, and head/tail samples.
- Reordering unrelated early paragraphs does not evict an explicitly targeted
  late paragraph.
- Every full, compact, and minimal projection remains valid UTF-8 and within its
  byte ceiling.

## 8. Gap 6: Route And End-To-End Evaluation Matrix

Coverage is required at five layers. ToolHub-only tests do not prove that a
user request can route, select, approve, execute, reread, and preserve a DOCX.

### Layer A: parser, schema, and preservation units

- Strict style object and target alternatives.
- Run extraction, effective style readback, coverage inventory, and stable
  locations.
- Operation-specific expected deltas and unrelated run/relationship rejection.
- Target-aware evidence packing at full, compact, and minimal budgets.

### Layer B: ToolHub adapter integration

Each of the five registered DOCX editors needs success, malformed input,
target-not-found, ambiguity, stale hash, preservation mismatch, and save/reload
cases against real fixtures. Fixtures include mixed runs, hyperlinks, multiple
sections, headers/footers, tables, and unsupported OOXML parts.

### Layer C: Workflow and approval integration

For every operation, exercise:

```text
route -> confirm target -> direct_once read -> operation decision
      -> one materialized editor -> approval_pending -> approve
      -> adapter -> output reread -> completed WorkflowResult
```

Add negative cases for cross-run evidence, conflicting locators, source changes
before approval, source changes while approval is pending, rejected approval,
adapter failure, and preservation cleanup. At least one approved path runs with
the default file-backed state configuration.

### Layer D: multilingual semantic and operation selection

Maintain deterministic labeled cases for Chinese and English, including:

| Intent | Chinese example | English example | Expected operation |
|---|---|---|---|
| Replace text | 把“旧名称”改成“新名称” | Replace "Old Name" with "New Name" | `replace_text` |
| Replace paragraph | 把第三段改写为这句话 | Rewrite paragraph three with this sentence | `replace_paragraph` |
| Insert paragraph | 在结论前新增一段 | Insert a paragraph before the conclusion | `insert_paragraph` |
| Delete paragraph | 删除重复的第二段 | Delete the duplicated second paragraph | `delete_paragraph` |
| Set style | 把标题设为一级标题并加粗 | Make the title Heading 1 and bold | `set_text_style` |

Include paraphrases, follow-ups using the latest edited document, explicit
locations near the end of a long file, and confusion pairs such as replace vs
insert and replace vs style. Hard negatives cover table-cell edits, tracked
change acceptance, image replacement, and other unregistered operations. Those
requests may enter `document.edit`, but operation selection must return no
matching editor and block clearly without materializing a substitute tool.

Real-model calibration remains opt-in evidence. Deterministic mock-router and
golden cases are the merge gate.

### Layer E: golden owner workflows

Add approved end-to-end golden cases for all five operations and negative golden
cases for unsupported mutation, stale approval, late-document evidence, mixed
run rejection, and partial coverage disclosure. Assert the selected Workflow,
operation, approval surface, output lineage, preservation result, and final
owner-facing status instead of matching only tool names.

## 9. Delivery Order And Ownership

Implement in six reviewable behavior changes, each with focused tests and
bilingual current-state documentation updates:

1. **Coverage truthfulness first:** prevents later preservation work from
   trusting a false completeness claim.
2. **Run-aware representation:** provides shared readback evidence for style and
   replacement preservation.
3. **Style contract:** aligns schema, adapter result, reread, and preservation.
4. **Generic DOCX evidence binding:** applies pre-approval and post-approval
   source/target validation to every mutation.
5. **Run-preserving editors:** changes replacement behavior only after the
   parser can prove preservation.
6. **Target-aware projection and full eval matrix:** removes the evidence budget
   mismatch and closes routing plus end-to-end coverage.

Ownership stays within existing boundaries:

| Concern | Owner |
|---|---|
| DOCX schemas, adapters, parser scripts | `internal/toolhub` |
| normalized run/coverage contract and preservation | `internal/document` |
| evidence binding, approval revalidation, decision projection | `internal/agent` |
| runtime budget default and validation | `internal/config` plus the existing default config |
| semantic, Workflow, ToolHub, and golden cases | existing package tests and `eval/golden` |

No new package or cross-layer import is required.

## 10. Completion Gate

The optimization is complete only when all of the following are true:

- all five DOCX operations are evidence-bound before approval and revalidated
  before approved execution;
- style-only requests round-trip and verify every requested property;
- supported text replacements retain parser-visible run formatting, while
  ambiguous mixed-format changes fail closed;
- DOCX content coverage never claims completeness for detected omitted text;
- operation selection uses target-aware evidence within the configured byte
  budget and the docs no longer claim a separate rune limit;
- deterministic Chinese and English route, decision, approval, mutation,
  reread, and negative golden cases pass;
- the default file-backed runtime is included in validation;
- current-state English and Chinese documentation is updated after behavior
  lands, and this temporary plan is then removed according to the documentation
  maintenance rules.
