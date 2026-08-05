# Document Workflows

> Language: English | [简体中文](../zh-cn/docs/document-workflows.md)

This document describes the active structured document read and edit pipeline.
It replaces the first-phase structured-enrichment design record while retaining
its durable format, evidence, and preservation contracts.

## Workflow Boundary

`document.read` revision 4 reads, summarizes, or extracts verbatim in-image text from one exact governed workspace
file. Its format-qualified reader is a `direct_once` node: Runtime invokes the
single reader with the frozen path, and Fast only synthesizes the final
answer from completed evidence. `document.edit` revision 6 reads one exact
file, resolves one supported
operation through an explicit Workflow decision node, obtains approval for the
reversible edit, and writes a new sibling output copy named
`<name>-sparkclaw-edit.<ext>`. If that name already exists, preflight selects
the first available numbered sibling such as `<name>-sparkclaw-edit-2.<ext>`.
Further edits to one of those copies continue the same numbered family. All
model calls owned by both document profiles currently use Fast; the non-document
Workflow default remains Deep.

Input and output paths are deterministic bindings. The model cannot replace
them. Paths must remain under the configured workspace, resolve to regular
non-symlink files, and match both extension and inspected file signature/package
type. Existing output files are never overwritten.

Every document Workflow begins with `confirm_document_target`. Deterministic
preflight has already selected one durable document ID, governed path, format,
provenance, and source ID; the node persists that evidence in `OutcomeRefs`
before activating the read or edit node. The node is therefore an executable
state transition, not a prompt instruction or decorative plan entry.

The edit plan is frozen as:

```text
confirm_document_target
  -> document_locate_evidence
  -> select_edit_operation
  -> document_edit
```

`document_locate_evidence` is a `direct_once` node. Runtime invokes its single
format-qualified reader exactly once with the frozen path; no model
`action | final` step runs before localization. The resulting structured
observation is the only evidence passed to operation selection. A failed read
blocks instead of falling through to another read.

At a model-driven tool stage, a model `final` response before the materialized
tool call is a protocol violation, not completion. Runtime returns one
stage-scoped correction; a repeated premature `final` blocks the active node
with `required_tool_not_called` without starting a third model call.

`select_edit_operation` never exposes a tool to the step model. Runtime searches its
format-qualified `document.edit` scope directly. A single candidate is selected
deterministically; multiple candidates are resolved by one retry-bounded Fast
model decision over the owner request and up to 20,000 runes of dependency
evidence. The selected directory entry, capability, format, operation, and
selection path are persisted in the node's `OutcomeRefs`. The edit node can
materialize only that entry. A missing, stale, ambiguous, or invalid decision
blocks the Workflow. The former inline secondary directory router has been
removed; any other multi-candidate scope must declare its own decision node.
See the [operation-selection design record](document-edit-operation-selection.md).

For PPTX edits, deterministic grounding freezes one typed scope before the
read: `single_slide`, `whole_deck`, `exact_text`, or `structural`. English,
Arabic-number, and Chinese slide ordinals are normalized to stable 1-based
slide indexes. A request that does not identify a slide, the whole deck, exact
replacement text, or a structural action clarifies before mutation. SmartArt,
animation, chart-data, slide-master, and macro edits block as unsupported targets. The
scope narrows the decision directory to the corresponding exact operation; it
does not create a second route or let grounding choose the operation.

## Durable Document Records

`DocumentRecord` is the first-class identity and activity record for a governed
document. It stores stable ID, owner/session scope, governed path, name, content
type, format, size/hash when available, status, source message/run/tool IDs,
optional parent document ID, and recent activity ID/time. Memory, file snapshot,
and PostgreSQL stores implement the same contract.

Attachments are recorded immediately after the owner message is persisted,
before parsing. Deterministic preflight enriches the record; successful reads
update its activity; each successful edit output becomes a new record linked to
its input through `parent_document_id`. A single edited output remains a
recent-document candidate for a later request such as "continue editing the
modified file"; its identity, lineage, source, and `edited` activity are
projected into routing context. It is bound only when the current request
semantically selects a document Workflow; unrelated turns do not inherit it.
Split operations retain every output under the same activity ID, so later
reference resolution keeps that set ambiguous.

Document identity and provenance must be durable. Parsed text, summaries,
layout enrichment, and other derived representations are deliberately not part
of `DocumentRecord`: they may be incomplete, archived as tool observations,
replaced, or regenerated.

## Pipeline

```text
record or resolve durable document identity
  -> inspect governed path and format
  -> persist confirm_document_target evidence
  -> invoke the bound reader once without a model step
  -> parse with small_file_v1 high-level adapter
  -> normalize structured_document_v1
  -> enrich supported evidence categories
  -> promote successful scanned-page OCR into PDF page blocks and content
  -> archive/project replaceable parse evidence as needed
  -> complete document_locate_evidence with structured observations
  -> resolve select_edit_operation inside the frozen format scope
  -> persist one exact tool_directory_entry decision
  -> materialize only the decided format/operation editor
  -> Policy approval
  -> write a new output
  -> reread and verify intended change plus preservation
```

DOCX and PPTX use Python high-level libraries, XLSX uses ExcelJS, PDF uses
Python PDF tooling, and text uses the native adapter. Scanned PDF pages are
rasterized with bounded `pypdfium2`/Pillow resources for the optional OvisOCR2
adapter. The project does not claim a complete OOXML/PDF object model.

## Structured Representation

The normalized record keeps content, layout, assets, annotations, and charts in
separate categories with stable locations. `document_enrichment_v1` adds Fast
image semantics, optional OvisOCR2 Markdown, and bounded layout evidence where
supported. OvisOCR2 is a dedicated document adapter rather than a Model Router
lane: Fast still owns visual description and Workflow reasoning. Successful
scanned-page OCR is promoted into the matching stable PDF page and block;
formulas and table markup remain in the Markdown evidence. Model-derived
image/OCR observations remain `untrusted` and carry model-call provenance.

For a directly inspected image, `images.inspect` runs optional OCR in parallel
with Fast visual understanding. Non-empty cleaned Markdown sets
`text_detected=true` and is retained beside Fast layout/semantic evidence; an
empty cleaned result sets `text_detected=false` and omits every `ocr_*` field.
Disabled or failed OCR stays explicit through `ocr_status` and, on failure, a
bounded warning. In document context, successful non-empty OCR is the verbatim
text source, so the Fast semantic segment omits its duplicate `Visible text`;
Fast text remains the fallback for disabled, failed, or empty OCR.

The full tool observation may be archived for traceability, while model context
receives selected segments with category, anchor, priority, and bounded text.
Exact preservation of the parsed representation is not required for document
identity. Category budgets prevent image semantics or OCR from evicting primary
document content, and repeated images are deduplicated by source hash.

Current image limits and budgets are enforced in code and tests. Any change to
them is a contract change, not a prompt-only adjustment.

PPTX reads additionally expose stable slide/template and layout references,
plus bounded paragraph and run trees for editable top-level text shapes. The
tree records paragraph level, bullets, alignment and spacing, and supported run
font, color, language, and hyperlink properties. Field-bearing text and group
children are explicitly non-editable. For slide-scoped edits, the 8,000-byte
operation evidence projection places every required target-slide record before
optional layout inventory records, excludes non-editable shapes, and never cuts
a record. Missing or oversized required evidence blocks instead of truncating a
target or asking the model to guess.

## Current Operations

| Format | Supported edit operations |
|---|---|
| Text | `replace_text` |
| DOCX | `replace_text`, `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` |
| XLSX | `replace_text`, `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` |
| PPTX | `replace_text`, `add_slide`, `update_slide`, `update_deck`, `duplicate_slide`, `delete_slide` |
| PDF | `extract_pages`, `delete_pages`, `rotate_pages`, `split` |

`pptx.update_slide` has two explicit layout policies:

- `preserve` changes exact text while retaining geometry and rejects text that
  cannot remain readable;
- `coordinated` may resize verified companion backgrounds and peer body columns,
  reports every layout change, and rejects output that still cannot fit.

PPTX text mutation supports `exact_span` and `rewrite_shape`. Exact-span
replacement keeps unaffected runs and redistributes a cross-run replacement
without flattening the paragraph. Shape rewrite retains the paragraph skeleton
and supported run styles; `break_mode` maps explicit newlines to either
PowerPoint soft breaks or paragraphs. Field-bearing targets fail closed.
Post-edit verification compares the paragraph/run tree and hyperlink targets,
so an unrequested formatting loss is a preservation mismatch.

Runtime owns all deterministic layout decisions. Under `coordinated`,
peer text boxes share the required height and font, verified backgrounds grow
with their body text, and full-height accent bars extend with card backgrounds.
Every geometry, font, or `word_wrap` mutation is reported from whole-slide
before/after evidence. The edit fails closed if the resulting text still does
not fit, a companion relationship is inconsistent, or a changed shape would
cross nearby content or the slide canvas. The model only selects evidence-bound
shape targets and supplies replacement text; it does not choose layout values.

`pptx.update_deck` applies a bounded batch as one atomic edit. The current
limits are 12 slides, 64 updated shapes, and 32 KiB of replacement text; any
stale target or failed update removes the entire output. `pptx.add_slide`
requires exactly one current-read `layout_ref` or `template_slide_ref`, accepts
an evidence-bound insertion position, and can clone supported text, groups,
images, charts, hyperlinks, and package relationships while applying template
text updates in the same invocation. A template or duplicate source with
speaker notes is rejected rather than copied with loss. Structural edits
recompute physical slide evidence and report stale page-marker text as a
warning instead of rewriting it implicitly.

Unsupported assets, annotations, charts, animations, SmartArt internals,
macros, tracked changes, and package extensions may be read as partial evidence
but are not implicit mutation targets. Scanned PDF reads invoke OvisOCR2
automatically when the adapter is enabled; if page rendering, OCR, or the page
budget is unavailable, the read remains explicitly `partial` with
`scanned_unsupported=true`.

## Mutation Safety

- Image semantics may locate a target but cannot authorize an edit by itself.
- Every mutation must match the persisted operation decision, selected
  format/operation schema, and frozen paths.
- PPTX slide, shape, old text, layout, template, and insertion references must
  all exist in the single completed read for the current run. Stale,
  non-editable, grouped, cross-scope, or note-bearing clone targets block before
  an approval record is created.
- The model-visible `docx.replace_paragraph` schema declares `source_hash` as
  required before model execution. Before Policy and Approval, Runtime binds a
  missing value from the single completed `document_locate_evidence` read in
  the current run and rejects a supplied value that conflicts with that
  evidence. Missing, conflicting, unrelated-node, or prior-run evidence blocks
  without creating an approval.
- The original SHA-256 must remain unchanged.
- Output is reread through the same normalized pipeline.
- Expected after-values and operation-specific deltas are checked.
- Known evidence-only assets, annotations, and layout fingerprints must be
  preserved unless the operation explicitly allows their change.
- Any unreported or unrelated change returns `preservation_mismatch` and removes
  the invalid generated output.
- Unsupported categories are reported as `unknown` or `partial`, never falsely
  marked preserved.

All registered PPTX edit operations use one 125,000 ms end-to-end tool
deadline, and subprocesses inherit the shorter remaining caller deadline. A
deadline failure removes partial outputs and maps to the stable
`document_operation_timeout` tool error. Reader and mutation-adapter expiry
retain `read` and `apply` stage evidence. Exact `reread` versus `preserve`
classification remains pending in the shared document Pipeline; a parent
deadline outside the PPTX adapters is currently reported conservatively as a
read-stage operation timeout.

## Extension Rules

1. Extend format inspection and high-level parsing before exposing an editor.
2. Add stable locations and bounded context projection for new evidence.
3. Register the editor by exact format and operation; do not expose a generic
   document mutation tool.
4. Define operation-specific argument binding, approval risk, delta allowlist,
   and post-edit verification.
5. Test malformed packages, path escapes, output conflicts, model-derived
   evidence, preservation failures, and successful rereads.
6. Update [Workflow capabilities](workflow-capabilities.md) for user-visible
   operations.

The core contracts live under `internal/document`; `internal/documentocr` owns
the bounded OvisOCR2 HTTP contract; ToolHub owns concrete readers plus Fast/OCR
enrichment; Workflow Runtime owns staged tool exposure, binding, Policy, and
final `WorkflowResult` projection.
