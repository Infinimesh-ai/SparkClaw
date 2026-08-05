# PPTX Workflow Optimization Implementation Record

> Language: English | [简体中文](../zh-cn/docs/pptx-workflow-optimization-plan.md)

Status: implemented and deterministically validated on 2026-08-05. This record
describes the first six PPTX workflow improvements that shipped together in
`document.edit` revision 6. The active user-facing contract is maintained in
[Document workflows](document-workflows.md) and the
[Workflow capability matrix](workflow-capabilities.md).

One cross-format integration item remains outside this PPTX change: the shared
`document.Pipeline` does not yet retain distinct `reread` and `preserve` timeout
stages. The PPTX boundary provides one end-to-end deadline, stable error codes,
subprocess cancellation, cleanup, and adapter-level `read`/`apply` stages
without changing other document formats.

## Shipped Architecture

PPTX remains inside the existing `document` capability branch. No PPTX
top-level route, keyword fallback, generic presentation mutation tool, or
second capability map was added. The Catalog revision is `2026-08-05.v16`, and
the active edit profile is revision 6.

```text
semantic document.edit match
  -> deterministic document and PPTX scope grounding
  -> confirm_document_target
  -> document_locate_evidence (one format-qualified read)
  -> select_edit_operation (one persisted exact entry)
  -> document_edit (one approval-gated atomic mutation)
  -> full reread and preservation verification
  -> one traceable output copy
```

The existing safety boundary still applies: one governed input, preallocated
output family, no overwrite, exact format/operation registration, reversible
approval, complete output reread, unchanged source hash, and explicit failure
for unsupported content.

## 1. Routing And Executable Scope

PPTX scope is a typed grounding result recorded after the semantic
`document.edit` candidate wins. It narrows the operation directory but does not
select the operation in the route.

| Grounded scope | Current behavior |
|---|---|
| `single_slide` | Requires an explicit 1-based slide reference and exposes only `pptx.update_slide`. |
| `whole_deck` | Requires explicit whole-presentation intent and exposes only atomic `pptx.update_deck`. |
| `exact_text` | Requires explicit replacement intent and exposes only `pptx.replace_text`. |
| `structural` | Exposes `pptx.add_slide`, `pptx.duplicate_slide`, and `pptx.delete_slide` for explicit structural requests. |
| `unspecified` | Clarifies before reading for mutation or creating approval. |
| `unsupported_target` | Blocks SmartArt, animation, chart-data, master, and macro edits with `pptx_edit_target_unsupported`. |

English numeric forms, common slide/page forms, Arabic numerals, and Chinese
ordinals are normalized deterministically. Explicit slide scope is rebound
over model arguments, and a conflicting model index is rejected by current-read
validation. Read, new-presentation creation, send, and file-delete requests are
hard negatives for the PPTX edit path.

Whole-deck updates are a single reversible operation. Current bounds are 12
slides, 64 updated shapes, and 32 KiB of replacement text. Duplicate slide
indexes, stale targets, oversized batches, and partial success are rejected.

## 2. Rich Text And Paragraph Preservation

The PPTX reader exposes bounded paragraph/run trees at stable shape paths. A
paragraph records level, bullet state, alignment, spacing, soft breaks, and run
order. A run records text plus supported font, size, emphasis, color, language,
and hyperlink properties. Text containing unsupported fields is marked
non-editable instead of being reported as safely preservable.

Two shape-update modes are active:

- `exact_span` replaces one evidence-bound span. Cross-run replacement text is
  redistributed deterministically while unaffected runs and styles remain in
  place.
- `rewrite_shape` replaces the selected shape text while retaining its
  paragraph skeleton and supported run styles. `break_mode=soft_break` and
  `break_mode=paragraph` make newline semantics explicit.

`pptx.replace_text` uses the same run-aware replacement path. Preservation
verification compares paragraph properties, run styles, hyperlink targets,
target-only text deltas, and explicitly reported layout changes. Formatting
loss, field-bearing targets, or unrelated changes return
`preservation_mismatch`; the invalid output is removed and the source remains
byte-identical.

## 3. Target Evidence And Editability

The 8,000-byte PPTX operation projection is scope-aware. It emits frozen
document identity and slide count, then complete target slide records and
editable shape records, followed by optional layout inventory records. A late
requested slide is prioritized over earlier non-target content.

Group children, records with `editable=false`, shapes without usable text
frames, and unsupported text are excluded from editor arguments. Their read-only
context is retained only where useful. Records are packed atomically; JSON and
shape records are never cut in half. The workflow blocks with stable
`pptx_target_evidence_missing`, `pptx_target_evidence_exceeds_budget`, or
`pptx_whole_deck_exceeds_batch_bound` errors when required evidence cannot be
provided safely.

Before Policy and Approval, Runtime verifies the input path, slide and shape
coordinates, `old_text`, exact replacement targets, layout/template refs,
insertion position, frozen scope, read ownership, run ID, node ID, and scope
revision against the single completed localization read. Stale, grouped,
non-editable, cross-run, or cross-scope targets create neither approval nor
output.

## 4. Template-Aware Slide Insertion

`pptx.add_slide` accepts exactly one evidence-owned source:

- `layout_ref`, from the stable current-read layout inventory; or
- `template_slide_ref`, from an existing slide in the same completed read.

`after_slide_index` defines the physical insertion position; omission appends.
A template clone and its optional `template_updates` execute in the same
adapter invocation, output copy, and approval. Relationship IDs are remapped
while supported text, groups, images, charts, hyperlinks, and package
relationships are copied. Auto-created destination placeholders are removed
before template shapes are cloned. A template or duplicate source with speaker
notes is rejected before approval because notes cannot currently be cloned
without loss.

Insertion, duplication, and deletion re-evaluate physical slide markers. The
editor does not rewrite unrelated footer text; a marker whose declared total no
longer matches the deck is surfaced as a preservation warning.

## 5. End-To-End Timeout Governance

Every registered PPTX edit definition uses one 125,000 ms deadline for input
inspection, complete read, constrain, adapter mutation, output inspection,
complete reread, preservation verification, and cleanup margin. Every Python
subprocess inherits a child deadline no later than the caller deadline. A hung
adapter is terminated promptly, and expired operations remove partial outputs
without touching the source.

PPTX timeouts retain document code `operation_timeout` and map to tool code
`document_operation_timeout`. Reader and mutation adapters retain `read` and
`apply` stage evidence. A parent deadline outside those adapters is currently
classified conservatively as `read`.

Exact `reread` and `preserve` timeout classification requires stage ownership
inside the shared `document.Pipeline`. That change would alter the common
cross-format contract and is therefore an integration task rather than part of
this PPTX-specific implementation.

## 6. Route-To-Output Validation

The deterministic gate covers the following layers.

| Layer | Covered behavior |
|---|---|
| Catalog/profile/semantic graph | Revision consistency, no new PPTX leaf, Chinese/English scope cases, and read/create/send hard negatives. |
| Grounding | Arabic and Chinese ordinals, whole-deck scope, ambiguity clarification, and unsupported target blocking. |
| Directory decision | Exact scope filtering for replace/add/update/update-deck/duplicate/delete and persisted-entry enforcement. |
| Reader/evidence | Rich runs, bullets, hyperlinks, groups, images, charts, notes, stable layout refs, late-slide priority, and complete-record overflow. |
| Editors | Run-aware replacement, exact-span and paragraph rewrite, atomic whole-deck success/failure/bounds, insertion order, and relationship-preserving template clone. |
| Preservation | Source hash, target-only text/style deltas, layout allowlists, hyperlinks, assets, charts, relationships, warnings, and invalid-output cleanup. |
| Policy/approval | Frozen input/output/operation/scope, affected-slide summary, and stale-evidence rejection before approval. |
| Timeout | Expired parent deadline, hung subprocess termination, stable codes, and no surviving output. |
| End to end | Route -> read -> operation selection -> approval -> execute -> reread -> output attachment -> durable parent lineage. |
| Golden eval | Deterministic single-slide, whole-deck, ambiguity, unsupported-target, late-evidence, and route-to-lineage cases. |

The real PPTX fixture contains ten slides, with rich mixed-style runs, bullets,
multiple paragraphs, soft breaks, hyperlinks, group shapes, an image, a chart,
speaker notes, complex layout relationships, and a target on slide 10. Tests
read the source, edit an output copy, reread it, compare preservation evidence,
and verify source integrity. A separate 12-slide fixture exercises the maximum
successful whole-deck batch. Tests use isolated temporary workspaces and
artifact paths and do not start a shared model or service.

## User-Visible Changes

- Explicit single-slide and whole-deck requests now reach operations that match
  their declared scope; generic presentation polishing clarifies.
- PPTX text edits preserve supported rich-text structure instead of flattening
  paragraphs and runs.
- Later slides and only editable top-level shapes are offered as edit targets.
- New slides can use a current layout or an existing slide template at a bound
  position in one approved output.
- PPTX edits have a realistic unified timeout and stable timeout failure code.

## Explicit Non-Goals

- Presentation creation from scratch, free-form slide design, theme generation,
  animations, SmartArt, chart-data editing, master editing, macros, or arbitrary
  OOXML mutation.
- A PPTX-specific top-level capability, keyword router, fallback executor, or
  generic mutation tool.
- Silent font shrinking, implicit page-marker rewriting, source overwrite, or
  partial whole-deck success.
- Approval UI, WebChat result-card, or attachment-surface redesign.
- Shared `document.Pipeline` stage refactoring; this remains the integration
  item described above.
