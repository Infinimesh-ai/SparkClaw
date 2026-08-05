# DOCX Editing

> Language: English | [简体中文](../zh-cn/docs/docx-editing-optimization.md)
>
> Status: Current behavior. This document describes the shipped DOCX read,
> edit, approval, preservation, and evaluation contracts.

## Scope

SparkClaw supports five approval-gated DOCX operations:

| Operation | Current editable scope |
|---|---|
| `replace_text` | Exact text in top-level body paragraphs |
| `replace_paragraph` | One top-level body paragraph |
| `insert_paragraph` | Start/end boundary or before/after one body paragraph |
| `delete_paragraph` | One top-level body paragraph |
| `set_text_style` | Built-in style, bold, and font size on one body paragraph |

Headers, footers, tables, hyperlinks, images, fields, comments, and unsupported
OOXML parts are read or inventoried where described below, but they are not
implicit mutation targets. Table-cell, header/footer, footnote/endnote,
text-box, tracked-change, field, drawing, image-replacement, and other
unregistered mutations block at operation selection instead of materializing a
substitute editor.

SparkClaw does not use keyword routing, a second capability catalog, a generic
document mutation tool, or a model-owned resource path. The semantic graph,
Workflow profile, ToolHub directory, Policy, and Approval remain the only
authorities.

## Workflow Boundary

Every DOCX edit follows the same staged path:

```text
semantic fusion
  -> document.edit revision 5
  -> confirm_document_target
  -> document_locate_evidence (direct_once files.read)
  -> select_edit_operation (one persisted directory entry)
  -> document_edit (only the selected editor is materialized)
  -> Policy and Approval
  -> source and target revalidation
  -> new sibling output copy
  -> reread and preservation validation
  -> completed WorkflowResult
```

Runtime freezes the governed input path and the next available sibling output
path. The model cannot replace either path. The localization reader runs once
and its archived structured observation is the only evidence source for
operation selection and mutation binding. The original file remains unchanged.

## Structured Read And Coverage

The DOCX reader exposes body paragraphs, table cells, run spans, hyperlinks,
images, section layout, comments, and deduplicated header/footer story parts.
Body and story blocks receive stable locations such as `document.p[25]` and a
story-part-qualified header/footer path. Shared linked headers and footers are
represented once by package part identity while retaining all section
references.

Each paragraph run reports parser-visible text offsets and formatting:

```json
{
  "index": 1,
  "text": "Quarterly summary",
  "bold": true,
  "italic": false,
  "underline": null,
  "font_name": "Aptos",
  "font_size_pt": 18.0,
  "font_color": "1F4E78",
  "effective_bold": true,
  "effective_font_size_pt": 18.0,
  "relationship_id": "",
  "boundaries": []
}
```

Explicit and effective values remain separate. Missing or inherited values are
not guessed from rendered text.

`coverage.content = complete` is emitted only when package inventory finds no
text-bearing content outside the normalized representation. Coverage is
reported per scope for body, tables, headers, footers, footnotes, endnotes,
text boxes, and tracked changes. Footnotes, endnotes, text boxes, tracked
changes, `altChunk`, content controls, nested tables, and unrecognized Word
text parts remain visible in `content_omissions` and `unparsed_parts` until a
parser represents them. Their presence makes content or the affected story
scope `partial`/`unsupported`; it never silently becomes complete.

## Text Style Contract

`docx.set_text_style` accepts a strict `style` object with at least one of:

- `builtin_style`;
- `bold`, including explicit `false`;
- integer `font_size_pt` in the inclusive range `1..200`.

Unknown properties, an empty style object, a missing target, conflicting
`paragraph_index` and `location`, a non-body location, or an invalid size fail
before Policy and create no approval.

After editing, the output is reopened through the same reader. The validator
checks the built-in style case-insensitively, requires every non-empty target
run to match requested bold and font size, preserves unrequested properties,
and verifies that text and location did not change. A mismatch returns the
typed preservation failure and removes the generated output.

## Evidence-Bound Mutations

Before approval, Runtime derives one binding from the completed
`document_locate_evidence` call in the same run, session, node, scope revision,
and governed path. It persists the source tool call, source node, run/session,
operation, input SHA-256, and the applicable target or boundary evidence.
Model-supplied evidence fields are accepted only when they equal current
evidence.

| Operation | Bound evidence |
|---|---|
| `replace_text` | Input SHA-256, exact match locations/hashes, expected counts |
| `replace_paragraph` | Input SHA-256, paragraph location/hash, optional exact old text |
| `insert_paragraph` before/after | Input SHA-256 and anchor location/hash |
| `insert_paragraph` start/end | Input SHA-256 and the matching document boundary |
| `delete_paragraph` | Input SHA-256, paragraph location/hash, normalized before text |
| `set_text_style` | Input SHA-256, paragraph location/hash, before-format fingerprint |

Missing, conflicting, cross-run, cross-session, stale-node, wrong-path, or
ambiguous evidence fails before approval. Immediately after approval and before
adapter execution, Runtime reloads the persisted call, recomputes the governed
file SHA-256, resolves the target again, and compares its current hash and
before value. A source changed during the approval wait fails without invoking
the editor or leaving an output.

## Run-Level Preservation

Text replacement maps logical paragraph offsets back to the minimum affected
run spans. A match inside one run splices only that run. A cross-run match is
allowed only when all affected text runs have the same formatting fingerprint
and relationship ownership. Mixed formatting, hyperlink relationship
crossings, fields, drawings, tracked changes, and other unsupported boundaries
fail closed rather than flattening the paragraph.

Whole-paragraph replacement preserves paragraph properties and reuses the
source run formatting only when source text runs are homogeneous. Mixed-format
paragraph replacement is rejected. Output validation compares unaffected run
text, run formatting, paragraph properties, hyperlinks/relationships, fields,
and images after save/reload. Only the requested text or style delta may differ.

Successful edits report `high_level_preservation = verified` and
`original_unchanged = true`. Any unrelated parser-visible change returns
`preservation_mismatch` and deletes the output copy.

## Target-Aware Decision Evidence

`Runtime.StageEvidenceMaxBytes`, configured by
`workflow_stage_evidence_max_bytes`, is the single evidence budget. The default
is 8,000 bytes. Decision and editor stages do not carry a second DOCX-specific
byte or rune limit.

For a structured DOCX read, the decision projector ranks complete JSON records
in this order:

1. source metadata, format, coverage, truncation, and parser statistics;
2. explicit `document.p[N]`, English `paragraph N`, Chinese `第 N 段`,
   route-bound location, and quoted-text matches;
3. the matched block and up to two same-story neighbors on each side;
4. representative header/footer story blocks and eligible operation context;
5. deterministic body head/tail samples when no explicit anchor matches;
6. remaining records in stable document order when budget remains.

The projector ranks existing evidence only. It cannot select an operation or
authorize a mutation target. Every byte ceiling uses the same ordering and
packs only complete UTF-8 JSON records. Its first record reports selected and
omitted counts, omitted body ranges, exact bytes used, and the anchors that
caused prioritization. Legacy archived observations without a structured DOCX
map retain the bounded generic evidence fallback.

## Deterministic Evaluation

The merge-gating suite includes:

- strict style schema and save/reload checks for each supported style field;
- current-evidence binding for all five operations, cross-run rejection, and
  source mutation during approval wait;
- real DOCX fixtures for bold, italic, underline, font, color, mixed runs,
  hyperlinks, fields, drawings, images, indentation, spacing, and coverage;
- late-target, Chinese/English anchor, early-reorder, no-anchor head/tail,
  story-part, UTF-8, and 8K/4K/2K projector cases;
- Chinese and English route/selection cases for all five operations;
- read-vs-edit, paragraph-delete-vs-file-delete, create-vs-edit, and
  browser-vs-local-document confusion pairs;
- unsupported header, table-cell, footnote, and tracked-change mutations that
  block without approval;
- one real default file-backed owner path covering route, direct read,
  operation selection, approval pending, approved execution, output reread,
  preservation, Workflow resume, attachment result, and state reload.

These tests use the deterministic mock model. Live-model calibration is
optional supplementary evidence and is not required for correctness.

Run the focused and full gates after document-tool setup:

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/document ./internal/toolhub ./internal/agent ./internal/modelrouter ./internal/semanticrouting
go test ./...
go vet ./...
go build ./...
```

## Current Boundaries

- DOCX mutations target top-level body paragraphs only.
- Header/footer content is available to decision evidence but remains read-only.
- Table-cell editing, footnote/endnote editing, tracked-change acceptance,
  image replacement, and arbitrary OOXML mutation are not registered.
- Cross-run formatting preservation is limited to parser-visible OOXML
  properties; unsupported boundaries are explicit failures or partial coverage.
- Large-document retrieval/indexing is separate from this bounded decision
  projection and is not added by the DOCX editor.

The owning component contracts remain in
[Document workflows](document-workflows.md); user-visible operations remain in
[Workflow capabilities](workflow-capabilities.md).
