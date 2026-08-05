# PDF Workflow Optimization Implementation Record

> Language: English | [简体中文](../zh-cn/docs/pdf-workflow-optimization-plan.md)

Status: Implemented and deterministically verified on 2026-08-05.

This record covers optimization items 1, 3, 4, 5, and 6 for ordinary,
scanned, and mixed-layer PDF workflows. Item 2, pagination and large-file
strategy, remains explicitly out of scope. The existing source-size boundary
and the 8000-rune finalization excerpt remain bounded; this work makes their
coverage claims truthful without adding page windows, continuation tokens, or
a document index.

The stable product contract also lives in
[Document workflows](document-workflows.md),
[Intent routing](intent-routing.md), and the
[Workflow capability matrix](workflow-capabilities.md).

## Delivered Boundary

- `document.read#read` owns reading, summarization, page-text extraction, and
  scanned-page OCR for one governed PDF. OCR remains internal, untrusted
  evidence, not a route, Workflow leaf, or Model Router lane.
- `document.edit#transform` owns the registered `extract_pages`,
  `delete_pages`, `rotate_pages`, and `split` operations after deterministic
  document grounding, operation selection, approval, output-copy writing, and
  reread verification.
- `merge` is absent from the ToolHub definition, executor, Python adapter,
  Workflow directory, and user-visible capability matrix. It requires a future
  multi-document grounding and lineage design before it can return.
- OCR evidence never authorizes mutation. The original PDF remains unchanged,
  and every transform writes one or more governed output copies.

## Truthful Page Reads

`pdf.extract_text` uses the typed workspace-read outcome adapter. Its final
structured result reports page evidence and coverage directly instead of
inferring read semantics from the `pdf.*` tool-name prefix.

Each page exposes `text_source`, `text_status`, deterministic native-text
quality evidence, and OCR provenance when applicable. Document statistics
include:

```json
{
  "read_complete": false,
  "coverage_status": "partial",
  "page_status_counts": {
    "native": 2,
    "ocr_succeeded": 1,
    "ocr_failed": 1
  },
  "missing_page_indexes": [4],
  "scanned_unsupported": true
}
```

The canonical page statuses are:

| Status | Meaning | Covered |
|---|---|---|
| `native` | The versioned native-text classifier accepted the extracted layer | yes |
| `ocr_pending` | Internal parser state before OCR enrichment completes | no |
| `ocr_succeeded` | Validated OCR supplied usable page evidence | yes |
| `ocr_disabled` | The page required OCR but the runtime adapter was disabled | no |
| `ocr_failed` | OCR failed, timed out, was busy, or returned no usable text | no |
| `render_failed` | The page could not be rasterized within the renderer contract | no |
| `budget_omitted` | The page exceeded the existing OCR page or render-byte budget | no |

`read_complete` is true only when every page ends as `native` or
`ocr_succeeded`. Missing page indexes are sorted and exact. A partial result
with at least one covered page can continue to finalization; a result with no
usable page evidence is unavailable and blocks rather than inviting a model to
invent content.

Archived tool output remains the finalizer's content source. The final evidence
starts with a compact coverage manifest and keeps page coverage separate from
`model_evidence_truncated`. When coverage is incomplete, the finalizer must
name the missing pages and bounded reason classes and may summarize only the
covered evidence.

## Deterministic Scan Classification

The PDF adapter classifies every page with
`pdf_native_text_quality_v1` before deciding whether to render it. The
classifier is local and deterministic. It considers trimmed character and
meaningful-character counts, replacement and control characters, repeated
glyph runs, and sparse text combined with page images.

The classifier emits `usable`, `empty`, or `degraded` plus reason codes and
bounded numeric features. `usable` pages remain native. `empty` and `degraded`
pages enter the bounded `pdf_page_render_v1` OCR path.

For a degraded mixed page, native and OCR blocks keep separate provenance and
the page reports `text_source=native+ocr`. Only exactly normalized duplicate
blocks are collapsed. Empty or image-tag-only OCR output is a validated
no-text result for caching, but it is not usable PDF text and therefore leaves
the page as `ocr_failed` with `no_usable_text`.

## Exact Transform Contract

After `select_edit_operation` persists one directory entry, Runtime exposes an
operation-specific strict schema:

| Operation | Required arguments | Constraints |
|---|---|---|
| `extract_pages` | `operation`, `path`, `pages`, `output_path` | `pages` is a non-empty array of unique positive one-based integers |
| `delete_pages` | `operation`, `path`, `pages`, `output_path` | same page contract; the output may not be empty |
| `rotate_pages` | `operation`, `path`, `pages`, `rotation`, `output_path` | rotation is `-270`, `-180`, `-90`, `90`, `180`, or `270` |
| `split` | `operation`, `path`, `output_path` | `pages`, `rotation`, and `inputs` are rejected |

Irrelevant fields, duplicate pages, non-integer pages, zero/unsupported
rotation, and a qualifier/operation contradiction fail before approval.
Out-of-range pages require the source page count, so governed execution rejects
them after approval but before creating or writing output. Page-selection
transforms preserve source order. The original hash is checked after execution,
and every output is reread and checked against the operation-specific
preservation delta.

## Routing Calibration

Routing keeps the existing candidate ownership:

- `document.read#read` covers reading, summarizing, recognizing scanned text,
  and extracting text from a page.
- `document.edit#transform` covers producing a changed or derived PDF through
  page extraction, deletion, rotation, or splitting.

The bilingual semantic corpus now covers page text versus page-file export,
scan recognition, negation, quotation, completed-action statements,
troubleshooting, recent-document follow-ups, merge requests, and ambiguous
forms such as `提取 report.pdf 的第 3 页`. Ambiguous requests clarify;
quoted, negated, historical, or troubleshooting text does not authorize a
transform. No keyword fallback, candidate ID, fusion weight, or threshold was
changed.

## OCR Runtime Readiness

The public adapter projection hides the endpoint and host allowlist while
separating requested configuration from actual construction state:

```json
{
  "configured_enabled": true,
  "adapter_ready": false,
  "runtime_status": "degraded",
  "reason_code": "constructor_failed",
  "provider": "openai-http",
  "model": "ATH-MaaS/OvisOCR2"
}
```

`runtime_status` is `disabled`, `ready`, or `degraded`. `ready` means the
adapter was constructed and can accept work; it does not prove warm first
inference or guarantee the next provider request. Bounded last-call status,
reason, and timestamp update after fresh calls without rewriting configured
state.

## Owner-Scoped Cache And Provenance

The OCR cache is process-local, owner-scoped, and bounded to 128 entries and
32 MiB. It does not add a Store method or a fourth persistence contract. A
Gateway restart naturally clears it.

The logical key contains only:

```text
prepared image SHA-256
+ configured provider and exact model
+ OCR prompt/response contract version
+ render/preprocessing version
+ output-normalization version
```

No path appears in the key. The owner ID is part of the internal lookup scope,
so identical bytes submitted by different owners do not share entries.
Successful and validated no-text results are cached; busy, timeout, cancellation,
render, and provider failures are not.

Concurrent misses for the same owner and logical key are coalesced. Every
consumer reports `hit`, `miss`, `coalesced`, or `bypass`. A fresh adapter call
gets a generated `ModelCall` ID and saves one durable `ModelCall` under the
current session/run with operation `document_ocr`. A cache hit creates no fake
call and points to the cache record plus its originating model call.

## Audit, Trace, And Metrics

OCR audit records contain bounded status, reason code, cache result, duration,
queue wait, page index, classifier/preprocessing versions, model-call/cache
references, and source/prepared hashes. They never contain OCR Markdown or page
text. Existing trace export includes these owner-scoped audit and model-call
records; governed archived tool output remains the content evidence.

The `/metrics` endpoint publishes low-cardinality process counters without
paths, text, hashes, session IDs, or run IDs as labels:

- `sparkclaw_document_ocr_pages_total{status,cache_result}`
- `sparkclaw_document_ocr_duration_seconds{provider,model,status}`
- `sparkclaw_document_ocr_queue_wait_seconds{provider,model}`
- `sparkclaw_document_ocr_cache_total{result}`
- `sparkclaw_pdf_page_classifications_total{classification}`
- `sparkclaw_pdf_reads_total{coverage}`

## Verification Evidence

Deterministic tests cover:

- native, image-only scanned, and sparse mixed-layer real PDF fixtures;
- OCR success, disabled, constructor-degraded, failed, timeout, busy, trivial
  output, render failure, and page-budget omission;
- complete, partial, and unavailable coverage plus bounded final evidence;
- owner isolation, cache hit/miss, coalesced misses, failure non-caching,
  no-text caching, version invalidation, and cache bounds;
- durable fresh `ModelCall` records, cache-hit provenance, audit text exclusion,
  readiness projection, and all six metric families;
- all four transforms, strict per-operation schemas, qualifier conflicts,
  invalid page/rotation inputs, merge absence, output copies, reread, original
  preservation, approval/resume, and routing boundaries.

The normal deterministic gate uses fixture OCR adapters and generated PDFs; it
does not load OvisOCR2. Live OvisOCR2 evaluation remains separately scheduled
because shared model loading must not overlap other document-format work.

## Remaining Boundaries

- Item 2 is not implemented. There is no page-window API, continuation token,
  retrieval index, or revised large-file strategy.
- OCR does not make unsupported PDF assets, annotations, forms, signatures,
  extensions, or layout rewrites fully preserved.
- PDF merge remains unavailable until ordered multi-document grounding, frozen
  hashes, multi-parent lineage, approval, collision, and preservation contracts
  are designed together.
- Cache contents are intentionally ephemeral. Durable model-call and audit
  provenance survives through the existing Store backends; OCR text remains in
  governed tool evidence rather than audit fields.
