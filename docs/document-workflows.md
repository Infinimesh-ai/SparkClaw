# Document Workflows

> Language: English | [简体中文](../zh-cn/docs/document-workflows.md)

This document describes the active structured document read and edit pipeline.
It replaces the first-phase structured-enrichment design record while retaining
its durable format, evidence, and preservation contracts.

## Workflow Boundary

`document.read` revision 1 reads or summarizes one exact governed workspace
file. `document.edit` revision 2 reads one exact file, resolves one supported
operation, obtains approval for the reversible edit, and writes a new sibling
output copy named `<name>-sparkclaw-edit.<ext>`.

Input and output paths are deterministic bindings. The model cannot replace
them. Paths must remain under the configured workspace, resolve to regular
non-symlink files, and match both extension and inspected file signature/package
type. Existing output files are never overwritten.

## Pipeline

```text
inspect path and format
  -> parse with small_file_v1 high-level adapter
  -> normalize structured_document_v1
  -> enrich supported evidence categories
  -> persist the full representation
  -> build bounded context segments
  -> select one format/operation editor
  -> Policy approval
  -> write a new output
  -> reread and verify intended change plus preservation
```

DOCX and PPTX use Python high-level libraries, XLSX uses ExcelJS, PDF uses
Python PDF tooling, and text uses the native adapter. The project does not claim
a complete OOXML/PDF object model.

## Structured Representation

The normalized record keeps content, layout, assets, annotations, and charts in
separate categories with stable locations. `document_enrichment_v1` adds Fast
image semantics and bounded layout evidence where supported. Model-derived
image/OCR observations remain `untrusted` and carry provenance.

The full representation is persisted, while model context receives selected
segments with category, anchor, priority, and bounded text. Category budgets
prevent image semantics or OCR from evicting primary document content, and
repeated images are deduplicated by source hash.

Current image limits and budgets are enforced in code and tests. Any change to
them is a contract change, not a prompt-only adjustment.

## Current Operations

| Format | Supported edit operations |
|---|---|
| Text | `replace_text` |
| DOCX | `replace_text`, `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` |
| XLSX | `replace_text`, `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` |
| PPTX | `replace_text`, `add_slide`, `update_slide`, `duplicate_slide`, `delete_slide` |
| PDF | `extract_pages`, `delete_pages`, `rotate_pages`, `split` |

`pptx.update_slide` has two explicit layout policies:

- `preserve` changes exact text while retaining geometry and rejects text that
  cannot remain readable;
- `coordinated` may resize verified companion backgrounds and peer body columns,
  reports every layout change, and rejects output that still cannot fit.

Unsupported assets, annotations, charts, animations, SmartArt internals,
macros, tracked changes, scanned-PDF OCR, and package extensions may be read as
partial evidence but are not implicit mutation targets.

## Mutation Safety

- Image semantics may locate a target but cannot authorize an edit by itself.
- Every mutation is limited to the selected format/operation schema and frozen
  paths.
- The original SHA-256 must remain unchanged.
- Output is reread through the same normalized pipeline.
- Expected after-values and operation-specific deltas are checked.
- Known evidence-only assets, annotations, and layout fingerprints must be
  preserved unless the operation explicitly allows their change.
- Any unreported or unrelated change returns `preservation_mismatch` and removes
  the invalid generated output.
- Unsupported categories are reported as `unknown` or `partial`, never falsely
  marked preserved.

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

The core contracts live under `internal/document`; ToolHub owns concrete
adapters and Fast image enrichment; Workflow Runtime owns staged tool exposure,
binding, Policy, and final `WorkflowResult` projection.
