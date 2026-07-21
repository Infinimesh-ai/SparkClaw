# Structured Document Enrichment Design

> Language: English | [简体中文](../zh-cn/docs/document-structured-enrichment-design.md)

Status: implemented first-phase baseline. The high-level category model, Fast
image enrichment, bounded context projection, and post-edit preservation checks
described here are active for `small_file_v1`; deferred package-level work
remains explicitly out of scope.

## 1. Decision Summary

SparkClaw should continue to use the existing high-level document libraries as
the primary parser for small documents. The first implementation should enrich
only the common structures these libraries support well. It should not attempt
to build a complete OOXML or PDF object model.

The normalized representation should separate content by category instead of
flattening every extracted item into one block list. Image binaries should be
stored as artifacts, inspected by the Fast multimodal model, and represented in
the document as bounded semantic observations. A common enrichment interface
should be used by the image path now and remain open to future package-level
OOXML or PDF enrichers.

The original file remains the fidelity source. Structured data is an indexed,
auditable view of the supported content, not a lossless replacement for the
source package.

Accepted scope decisions:

- Keep `representation_version=structured_document_v1`; enrichment categories
  are optional and carry their own `document_enrichment_v1` schema version.
- Register and hash every supported embedded image. Invoke Fast only for
  target-relevant images by default; full-document understanding may inspect
  all images within the configured budget.
- Use only the Fast model for first-phase image semantics.
- An image-semantic failure returns `assets=partial` unless the request
  explicitly depends on the image, in which case the workflow must block or
  report insufficient evidence.
- Scanned PDF and rendered-page image understanding are deferred.
- Only the `content` category is editable in the current scope. Assets,
  annotations, layout, and extensions are evidence-only categories used for
  understanding and target location. A content editor may make a bounded,
  explicitly reported secondary layout adjustment when that is required to
  keep edited content readable; layout is not an independent edit target.

## 2. Goals

- Keep the current `small_file_v1` size and completeness behavior.
- Preserve the existing stable content anchors and format-specific entities.
- Add category-specific fields for visual assets, annotations, layout, and
  extension metadata.
- Extract embedded images through the high-level library where possible.
- Send image content to the Fast multimodal lane and persist a strict semantic
  result with provenance.
- Build model context from bounded, categorized context segments.
- Reserve a real extension point for future OOXML/PDF package inspection
  without adding an unused production interface.
- Keep every document mutation approval-gated and bound to stable locations.

## 3. Non-Goals For The First Implementation

- Full OOXML part parsing or arbitrary XML mutation.
- Animation, SmartArt, tracked-change, macro, or embedded-object editing.
- Replacing the original file with the structured representation.
- Storing image bytes or base64 data inside ToolCall JSON or model context.
- Allowing Fast image descriptions to act as trusted instructions.
- Scanned-PDF rendering, OCR, or Fast page-image understanding.
- Direct asset, annotation, arbitrary layout, chart, or extension mutation.
- Large-document chunking, streaming, or indexing.

## 4. Category Model

The existing fields remain the compatibility surface:

- `blocks`
- `paragraphs`
- `tables`
- `sheets`
- `slides`
- `sections`
- `pages`

The enriched representation keeps `structured_document_v1` and adds one
optional, independently versioned `enrichment` field:

```json
{
  "representation_version": "structured_document_v1",
  "enrichment": {
    "schema_version": "document_enrichment_v1",
    "assets": {
      "images": [],
      "charts": [],
      "embedded_objects": []
    },
    "annotations": {
      "comments": [],
      "notes": [],
      "hyperlinks": []
    },
    "layout": {
      "sections": [],
      "page_settings": [],
      "slide_layouts": [],
      "merged_ranges": [],
      "shapes": [],
      "companion_groups": [],
      "page_markers": []
    },
    "extensions": {
      "status": "deferred",
      "parts": []
    },
    "coverage": {
      "content": "complete",
      "assets": "complete",
      "annotations": "partial",
      "layout": "partial",
      "extensions": "deferred"
    },
    "category_policy": {
      "content": "editable",
      "assets": "evidence_only",
      "annotations": "evidence_only",
      "layout": "evidence_only",
      "extensions": "evidence_only"
    }
  }
}
```

`complete` means complete within the declared category contract. It must not
imply that every source-package feature is understood.

## 5. Common Record Contract

Every category item should carry a common identity and provenance envelope:

```json
{
  "id": "asset_...",
  "kind": "image",
  "parent_path": "presentation.slide[3]",
  "location": {
    "path": "presentation.slide[3].shape[6]",
    "slide_index": 3,
    "shape_index": 6
  },
  "source": {
    "parser": "python_pptx",
    "relationship_id": "rId7",
    "part_name": "ppt/media/image2.png"
  },
  "content_type": "image/png",
  "bytes": 184220,
  "sha256": "...",
  "artifact_ref": "artifact://..."
}
```

The location fields are format-specific, but `id`, `kind`, `parent_path`,
`source`, `sha256`, and `artifact_ref` have the same meaning across formats.

## 6. Image Semantic Contract

An image record may include a Fast-model observation:

```json
{
  "semantic": {
    "status": "succeeded",
    "description": "A five-layer network model diagram.",
    "ocr_text": ["Application", "Transport", "Network"],
    "content_class": "diagram",
    "visible_entities": ["layer labels", "directional arrows"],
    "warnings": [],
    "model_lane": "fast",
    "model_call_id": "mcall_...",
    "source_sha256": "...",
    "untrusted": true
  }
}
```

Rules:

1. Extract and hash the image before model invocation.
2. Deduplicate repeated images by SHA-256 within one document read.
3. Store bytes in ArtifactStore, never inline them in document JSON.
4. Require strict structured output from the Fast model.
5. Treat descriptions and OCR as untrusted evidence.
6. Persist failed, skipped, or unsupported states explicitly.
7. Reuse a semantic result only when its source hash and model contract match.
8. Register and hash all supported images, but invoke Fast only for images
   relevant to the explicit target unless full-document understanding is
   requested.
9. Record a failed image analysis as `coverage.assets=partial`. Block only when
   the requested answer or edit decision explicitly depends on that image.

Recommended first-phase semantic scope:

- Supported: content class, short factual description, key visible text,
  diagram/flow summary, chart trend summary, and relationship to nearby text.
- Evidence-only: OCR text, visible entities, and inferred image purpose.
- Not guaranteed: dense-table reconstruction, small numeric labels, complete
  transcription, handwriting, identity recognition, or complex visual
  reasoning.
- Skip Fast calls for tiny decorative icons, backgrounds, and repeated logos;
  still register their asset identity and hash.

Recommended first-phase budgets, based on the current one-image request path,
12 MiB image limit, 2400-pixel tested long edge, 12,288-token Fast context,
1024-token Fast output, and 180-second workflow budget:

- Targeted understanding: at most 4 unique images.
- Full-document understanding: at most 8 unique images.
- Original image: at most 12 MiB, matching the existing image tool.
- Model input: long edge at most 2400 pixels and encoded bytes at most 4 MiB.
- Concurrency: at most 2 Fast calls.
- Timeout: 30 seconds per image and 120 seconds for the enrichment stage.
- Structured response: at most 512 output tokens and 800 Chinese characters
  of context text per image.
- Combined image-semantic context: at most 4,000 characters so primary document
  content retains most of the model context budget.

The implemented preparation path uses high-quality Catmull-Rom downscaling,
flattens transparency onto white before JPEG encoding, and enforces both the
2,400-pixel long-edge and 4 MiB encoded-input limits. OCR remains evidence-only.

## 7. Initial High-Level Coverage By Format

### Plain Text

- Content: lines and exact text.
- Layout: encoding, BOM, and newline style may be recorded.
- Assets and annotations: not applicable.

### DOCX (`python-docx`)

- Content: body paragraphs and table-cell paragraphs.
- Assets: inline images with relationship, media type, size, hash, and anchor.
- Annotations: comments and hyperlinks where exposed by the library.
- Layout: sections, headers, footers, and paragraph styles at a bounded level.
- Deferred: footnotes, endnotes, tracked changes, floating text boxes, complex
  drawing objects, and unsupported package extensions.

### XLSX (`ExcelJS`)

The current XLSX adapter is JavaScript, not Python, and should remain on its
existing high-level library for the first implementation.

- Content: sheets, rows, cells, formulas, and cached/display values.
- Assets: workbook images and worksheet anchors.
- Annotations: notes/comments and hyperlinks.
- Layout: merged ranges, row/column dimensions, number formats, and selected
  styles needed to interpret values.
- Deferred: charts, slicers, external connections, macros, and unsupported
  extension parts.

### PPTX (`python-pptx`)

- Content: slides, text shapes, groups, and table cells.
- Assets: pictures with slide/shape anchor, geometry, media type, and hash.
- Charts: chart title, type, categories, series, and source relationship where
  available through the high-level library.
- Annotations: speaker notes and hyperlinks.
- Layout: every high-level shape, including empty decorative shapes, with
  geometry, fill/line evidence, text style, single-line capacity, and stable
  high-confidence `background`/`label`/`body` companion groups.
- Page markers: embedded `n/total` tokens are compared with the physical slide
  position and deck size. Mismatches are warnings, not implicit whole-deck
  edits.
- Deferred: animations, comments, SmartArt internals, macros, and unsupported
  extension parts.

`pptx.update_slide` supports two explicit policies:

- `preserve`: replace exact shape text without changing geometry; reject text
  that cannot remain readable in the original shape.
- `coordinated` (default): for high-confidence repeated label/body bands,
  resize peer body columns and companion backgrounds together, preserve a
  common readable font size, and reject the output if the coordinated layout
  still cannot fit. Other text shapes may expand only into verified free space.

Every coordinated change returns `layout_changes`,
`layout_adjusted_shape_indexes`, and deterministic `layout_checks`. The
post-edit re-read accepts only those reported shape changes and verifies their
before/after geometry and style; any unreported slide-layout delta fails with
`preservation_mismatch`.

### PDF (`pypdf`)

- Content: page text, page index, and optional text-position observations.
- Assets: page images where extraction is supported.
- Annotations: PDF annotations, links, outlines, and attachments.
- Layout: page boxes and rotation.
- Deferred: scanned PDFs, rendered-page Fast analysis, OCR engine integration,
  and complete visual reading-order recovery.

### First-Phase Layout Field Recommendation

Only fields needed for content interpretation, evidence linkage, and location
should be recorded:

- DOCX: paragraph/table/row/cell/section indexes, part kind, style name,
  heading or outline level, list identity, and header/footer identity. Do not
  attempt physical page coordinates.
- XLSX: sheet name/index, cell address, row/column index, merged range, formula,
  displayed value, number format, hidden state, and bounded header-style hints.
- PPTX: slide/shape index, parent group, shape/placeholder type, x/y/width/
  height, z-order, rotation, alternative text, and slide-layout identity.
- Text PDF only: page index, rotation, media/crop boxes, and optional text
  bounding boxes when extraction provides stable coordinates.

Full font, color, theme, master, drawing, and pagination models remain outside
the first phase.

## 8. Enrichment Interface

The implementation should use one active enrichment interface for image
semantics and future package-level inspection:

```go
type DocumentEnricher interface {
    Name() string
    Supports(format string, category string) bool
    Enrich(context.Context, EnrichmentRequest) (EnrichmentResult, error)
}
```

The first registered implementation is the Fast image-semantic enricher. A
future OOXML package-part inventory or PDF object enricher can implement the
same interface. The registry, timeout, result validation, and coverage status
therefore exist for a current production caller rather than as dead future
code.

Conceptual stage order:

```text
inspect
  -> high-level complete parse
  -> normalize stable locations
  -> category enrichers
  -> coverage validation
  -> persist full representation
  -> build bounded context projection
```

Enrichment failure must not silently disappear. Image failure returns
`coverage.assets=partial` by default. It blocks or reports insufficient
evidence only when the request explicitly depends on the failed image. The
same explicit workflow-assessment rule applies to future enrichers.

## 9. Context Assembly

The full structured representation is persisted, but the model receives a
bounded projection assembled from category-specific segments:

```json
{
  "context_segments": [
    {
      "category": "content",
      "anchor": "presentation.slide[3].shape[2]",
      "text": "...",
      "priority": 100
    },
    {
      "category": "image_semantic",
      "anchor": "presentation.slide[3].shape[6]",
      "text": "Diagram showing five network layers.",
      "priority": 80,
      "provenance": "mcall_..."
    },
    {
      "category": "annotation",
      "anchor": "presentation.slide[3].notes",
      "text": "...",
      "priority": 60
    }
  ]
}
```

Projection rules:

1. Preserve category and anchor; do not concatenate everything into anonymous
   text.
2. Prioritize the explicit user target and its structural neighbors.
3. Apply separate budgets per category so OCR cannot evict primary content.
4. Deduplicate repeated image semantics by source hash.
5. Keep provenance and `untrusted=true` on model-derived observations.
6. Persist the full result independently from prompt compaction.

## 10. Mutation Safety

- Only high-level, content-related locations are mutation targets in the
  current implementation scope.
- Assets, annotations, layout, charts, and extensions are evidence-only. They
  may improve understanding and target selection but must not be mutated.
- Image semantics may help select a content target but cannot authorize an edit
  by itself.
- The original input hash must remain unchanged.
- Unrelated category items should be compared before and after mutation.
- A future package-part manifest should fail the edit if an unrelated,
  unsupported part disappears or changes unexpectedly.

The current pipeline already verifies a separate regular output file, matching
format, a positive change count, and an unchanged input SHA-256. It only
performs format inspection on the output, not a complete structured reread.

Recommended first-phase additions:

1. Fully reread and normalize every output through the same high-level parser.
2. Verify the expected after-value at each mutation target.
3. Apply an operation-specific delta allowlist so only intended content or
   structural changes are accepted.
4. Compare evidence-only category fingerprints before and after: image
   SHA-256 and relationship, annotation text hash and anchor, and the selected
   layout fields.
5. For row/slide insertion or deletion, compare by content/resource identity
   with an operation-aware index mapping rather than requiring unchanged paths.
6. Delete the generated output and return `preservation_mismatch` when a known
   evidence-only item disappears or changes unexpectedly.
7. Report preservation honestly: `high_level_preservation=verified` and
   `package_preservation=unknown` until package-level OOXML/PDF checks exist.

If a category was unsupported before the edit, its preservation remains
unknown rather than passing. This should not block ordinary content edits in
the first phase, but the limitation must remain visible in `change_summary`.

## 11. Compatibility And Versioning

Recommended approach:

- Keep all current fields and consumers working.
- Keep `representation_version=structured_document_v1`.
- Add optional categories under `enrichment` with
  `schema_version=document_enrichment_v1`.
- Missing enrichment in an older v1 record means `unknown`, not that the source
  contains no assets or annotations.
- Introduce `structured_document_v2` only if category coverage becomes a
  mandatory core contract or existing field semantics become incompatible.
- Context assembly must accept both the current representation and the enriched
  form during migration.
- Tool schemas should expose only validated category data, never raw library
  objects.

## 12. Implemented Defaults

The Fast image scope and budgets in section 6, the minimal layout fields in
section 7, and the high-level preservation checks in section 10 are the active
first-phase defaults. A future change to those limits is a contract change and
must update focused tests plus this bilingual design record.
