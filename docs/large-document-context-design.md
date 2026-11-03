# Large Document Context And Retrieval Design

> Language: English | [简体中文](../zh-cn/docs/large-document-context-design.md)

Status: Part A (context-derived content budgets) is implemented in source on
2026-11-03 and not yet deployed. Part B (the `large_indexed` structure-first
retrieval strategy) is an accepted design, not implemented; the owner
decisions it follows are recorded in section 7.

Scope: the document read path (`internal/document`, `internal/toolhub`
document tools), workflow evidence provisioning, operation decisions and
finalization (`internal/agent`), and the runtime budget configuration. The
observation envelope contract in
[Observation compression](observation-compression-redesign.md) is kept.
[Workflow execution](workflow-execution.md) remains the runtime authority.

## 1. Problems

### 1.1 Documents are read in one piece

`document.read` revision 4 confirms the target, runs one format reader
directly, and hands the complete extraction to the Fast finalizer. Only
`small_file_v1` is registered:

- Sources above 8 MiB (`SmallFileMaxBytes`) and extractions above 200,000
  bytes (`SmallExtractedMaxBytes`) fail with `strategy_deferred`
  ("no registered large-document strategy"). The agent has no recovery for
  this code, so the owner only sees a failure.
- The pipeline envelope already reserves the retrieval vocabulary
  (`strategy`, `context_mode`, `index_status`, `keyword_index_id`,
  `vector_index_id`), but only `small_direct` / `full_text` / `skipped`
  exist.
- The 71 MB *计算机网络（第 7 版）* PDF used for testing has 464 pages, no
  text layer (every page extracts 0 characters) and a 246-entry bookmark
  outline that `pypdf` reads in 0.1 s. A useful strategy for it must locate
  by structure first and OCR only the pages it needs; the PDF adapter
  currently OCRs at most 8 pages per read.

### 1.2 Byte limits were fixed, not derived from the model

Before Part A, the content channels used fixed numbers regardless of the
selected capacity profile:

| Channel | Dual-light default (32K Fast) | Hosted product (256K Fast) |
|---|---|---|
| Document text sent to the finalizer | 8,000 bytes | 200,000 bytes (manual env pin) |
| Evidence provisioned to a stage / decision | 8,000 bytes | 200,000 bytes (same knob) |
| Complete extraction accepted by a read | 200,000 bytes | 200,000 bytes |

On the 32K profile, an 8,000-byte finalizer (about 2,700 Chinese characters)
summarized documents from a small prefix even though the window holds about
six times more. On the 256K profile, documents between 200,000 bytes and what
the window can carry were rejected outright. The product pin was also an
overflow hazard: the same 200,000-byte value on a 32K model is about three
times its input window, and nothing tied the number to the profile in use.

## 2. Reference: DeepSeek Harness 0.2.1-rc.3

The installed DeepSeek Harness app was unpacked and inspected (read-only). It
does **not** implement structure detection or retrieval for documents:

- Uploaded non-image files are saved verbatim; the model only receives a line
  naming the saved path and is told to read it with file tools when needed.
- `read(file_path, offset, limit)` pages UTF-8 text by line (2,000 lines /
  50 KB per call) and ends with a continuation footer such as
  `Use offset=… to continue`. Binary files such as PDF are refused; PDF text
  support is listed as deferred in its own README, and its bundled Python has
  no PDF library.
- `grep` is ripgrep with at most 250 unranked matches and no context lines.
- Any tool result over about 12,500 tokens keeps its head and tail, stores the
  full result on disk and tells the model to `read` or `grep` that file.
- Office files are handled by skills that instruct the model to write
  python-docx / python-pptx / openpyxl scripts.

Three ideas carry over: hand the model a pointer instead of the whole file,
page reads with an explicit continuation hint, and keep oversized results on
disk where they can still be searched. The structure-first retrieval below is
SparkClaw's own design.

## 3. Part A: Context-Derived Content Budgets (implemented)

### 3.1 Formula

A content budget is a share of the consuming model's input window:

```text
input_tokens = context_tokens(lane) - output_budget(lane, operation class)
budget_bytes = input_tokens x share x 3 bytes/token
```

Three bytes per token is a near-lower bound for Chinese text (one rune is
three UTF-8 bytes and costs about one token) and conservative for Latin text.
`config/context_budget.go` owns the constants and helpers:

| Channel | Consumer operation (output class) | Share | 256K product Fast | 32K dual-light Fast | 64K dual-light Deep |
|---|---|---|---|---|---|
| Final answer evidence | `workflow.final_answer` (`answer`) | 60% | 457,113 B | 51,609 B | 103,218 B |
| Stage / decision evidence | `workflow.step`, `workflow.decision` (`workflow_structured`) | 30% | 228,555 B | 25,803 B | 51,609 B |
| Complete extraction ceiling | largest Fast/Deep final-evidence budget, floor 200,000 B, ceiling 2 MiB | — | 457,113 B | 200,000 B | 200,000 B |

The lane is the one that owns the model call: document workflows use Fast,
other workflows Deep, and a step uses its stage lane hint.

### 3.2 Never exceeding the window

The byte estimate can be wrong for dense content; numeric sheets can cost one
token per byte. Each consumer therefore confirms the rendered prompt:

- **Finalization and operation decisions** call `fitEvidencePrompt`: render at
  the derived budget, count the full request with the lane's tokenizer
  (`CountProfileChatInput`, exact near the boundary), and when it exceeds the
  input window minus a 256-token reserve, re-render at
  `budget x window / tokens x 0.9`. After four attempts, or once the budget
  reaches 512 bytes, the call fails closed with `InputTooLongError`; nothing
  oversized is dispatched. Decision evidence is re-projected at each budget,
  so structured slices are never cut in the middle.
- **Step provisioning** already goes through ContextBuilder admission, which
  degrades provisioned evidence from full to compact (half) to minimal
  (quarter) before refusing.

When a document is larger than the final budget, the finalizer keeps the
existing behavior: it projects the leading part, marks
`model_evidence_truncated=true` / `limitation_required=true`, and the answer
states that only part of the document was checked.

### 3.3 Operator ceiling

`runtime.workflow_stage_evidence_max_bytes` changes meaning:

- `0` (new default in Go, `configs/sparkclaw.default.json`, the compose
  fallback and the hosted product env) derives the budget from capacity.
- A positive value is a ceiling that can only lower the derived stage and
  final budgets, for operators who want to cap prefill cost. It can never
  raise a budget past the model window.
- It does not cap the extraction ceiling, which is not model-visible.

The deployment profile validator now pins the product value to `0` instead
of `200000`. An older binary reading `0` falls back to its 8,000-byte default,
so gateway and env must be released together.

### 3.4 Budgets that stay fixed, on purpose

| Budget | Value | Why it is not context-derived |
|---|---|---|
| `observation_summary_max_bytes` / evidence excerpt | 2,400 B / 1,400 runes | The envelope is an index over persisted evidence, uniform for every tool ([Observation compression](observation-compression-redesign.md) §3.1); content travels through provisioning and finalization |
| `observation.read` window | 8,000 B default, 32,768 B max | A support escape hatch whose result passes the same envelope |
| `workflow_run_max_observation_bytes` / compaction | 48,000 / 36,000 B (product 96,000 / 72,000) | Accumulation stop for envelopes, not a content channel |
| Session history trims | 4,000 / 1,200 per tool summary | Cross-run history is an index (§3.6 of the observation design) |
| Browser snapshot requirements | 8,000 B | Component cap of browser evidence |
| Source size gate, PDF OCR pages | 8 MiB, 8 pages | Parse and OCR cost limits; Part B replaces them for large files |

## 4. Part B: `large_indexed` Strategy (accepted, not implemented)

### 4.1 Scope and entry points

Upload limits stay as they are: WebChat uploads accept 25 MiB per file
(`gateway/documents_endpoints.go`) and desktop ISCP execution inputs 8 MiB per
file (`iscpobjects/store.go`), without chunked upload. Large documents
therefore reach the strategy in two ways:

- **Uploads** up to 25 MiB whose text cannot be read in one piece.
- **Workspace files** that never went through upload (placed in the workspace
  directly, materialized desktop inputs, edit outputs). These may be larger
  than 25 MiB; the 71 MB *计算机网络* PDF is only reachable this way.

### 4.2 Selection

`inspectAndSelect` already iterates strategies. Register `large_indexed_v1`
after `small_file_v1`. A document needs it when any of these holds:

- the source exceeds 8 MiB (`SmallFileMaxBytes`);
- the complete extraction would exceed the extraction ceiling of section 3.1;
- a PDF has more pages without a text layer than one bounded read OCRs
  (currently 8).

Everything that fits today keeps the `small_direct` / `full_text` path,
unchanged.

### 4.3 Lifecycle: import pre-build plus analysis fallback

**Import phase (background, lightweight).** After an upload is stored, a
gateway-owned background queue (one worker, cancelled on shutdown) checks
whether the file needs `large_indexed`. Sources above 8 MiB qualify directly;
smaller sources get one text-only parse with enrichment disabled to measure
the extraction and count text-less PDF pages. Qualifying files get an index
of the structure map, text-layer chunks and keyword index. The import phase:

- never runs OCR or any model call;
- shows nothing to the owner and adds no status field to the UI;
- records failures only in the index manifest and audit log, and the analysis
  phase retries them.

**Analysis phase (on demand).** When a request reads the document:

| Index state | Behavior |
|---|---|
| `ready` | Retrieve directly |
| `building` (import still running) | Wait for that build instead of starting a second one |
| missing or `failed` (non-upload file, or a failed pre-build) | Build in this run, then retrieve |

OCR happens only in this phase (section 4.5).

**Progress.** The owner sees index work only when a request has to wait for
it: the run's existing tool progress shows "正在建立文档索引" while the run waits
on or builds the index. Nothing is shown when the index is ready, and no
resident indicator is added (AGENTS.md UI rules).

### 4.4 Index content (once per source SHA-256)

1. **Structure map** (`outline.json`), by format:
   - PDF: bookmark outline to a heading tree with page ranges; without
     bookmarks, fixed page windows plus large-font line candidates.
   - DOCX: heading styles and outline levels; tables as their own nodes.
   - PPTX: one node per slide, keyed by title.
   - XLSX: one node per sheet, with header rows and used ranges.
   - Text / Markdown: `#` headings, otherwise fixed line windows.
2. **Chunks** (`chunks.jsonl`): split along structure nodes at about 1,000
   characters with a small overlap. Each chunk carries an anchor (page,
   heading path, stable block / paragraph / cell ID) in the existing citation
   vocabulary.
3. **Keyword index**: pure-Go BM25 only, with no embeddings in this design.
   Chinese is tokenized into character bigrams and Latin text into words;
   heading text is weighted above body text. No new runtime dependency.
4. **Page map**: text-less PDF pages are marked `unindexed` until OCR fills
   them.

Builds run in page batches so no adapter call meets the 60-second subprocess
timeout. Each batch stays within the 2 MiB extraction ceiling of section 3.1
and is streamed to disk, so the whole text is never held in memory.

### 4.5 OCR policy

OCR runs only in the analysis phase, through the existing OvisOCR2 runtime
and its per-call page limits, and every recognized page is written back to
the index cache, so no page is recognized twice.

- **Targeted questions** OCR only the pages that retrieval selects. On a
  scanned PDF, body text is unknown before OCR, so selection uses outline
  titles and their page ranges.
- **Whole-document requests**, and targeted questions on scanned files without
  a usable outline, may OCR every page, in batches, when the source is within
  the existing 25 MiB upload limit. Workspace-only files above that size keep
  hit-page OCR and the answer states which pages were not recognized.

### 4.6 Storage, isolation and retention

- Indexes live under the owning workspace root at
  `.sparkclaw/state/doc-index/<sha256>/` (`manifest.json`, `outline.json`,
  `chunks.jsonl`, `keywords.bin`, `ocr/<page>.json`). Workspace roots are
  already owner- or session-scoped, so isolation follows the workspace; the
  same file uploaded twice in one workspace reuses one index.
- `.sparkclaw/state` is already a control directory: output paths cannot
  target it and `files.search` skips `.sparkclaw`. Read tools do not refuse it
  yet; B1 adds that refusal, so the index is reachable only through the
  document tools.
- The manifest records source SHA-256, size, strategy and parser versions,
  state (`building`, `ready`, `partial`, `failed`), attempts and timestamps.
  A version change rebuilds the index.
- Cleanup removes an index when no file in the workspace has its hash, and an
  LRU sweep keeps the directory under a per-workspace size cap (initially
  512 MiB). Sweeps run when the queue is idle and at gateway start.
- With the Postgres state backend the index stays on the workspace volume,
  because it is derived data. Losing it only costs a rebuild.

### 4.7 Model tools (read risk, `document.read` capability)

| Tool | Returns | Bound |
|---|---|---|
| `document.outline(path)` | Heading tree, page ranges, size, index state, unindexed pages | Stage budget |
| `document.search(path, keywords[], top_k, section?)` | Ranked snippets with anchors and scores | `top_k x snippet` within the stage budget |
| `document.read_range(path, anchor \| pages \| chunk_ids, max_bytes)` | Text for that range, with a continuation hint when cut | At most the stage budget |

For a `large_indexed` document, `files.read` and `pdf.extract_text` stop
returning `strategy_deferred` and instead return the outline plus a hint to
use `document.search` / `document.read_range`.

### 4.8 Workflow and routing

`document.read` revision 5 keeps the revision 4 path for small documents and
adds a large branch. Persisted revision 4 plans keep their shape on resume.

1. `confirm_document_target`, unchanged. Its deterministic preflight already
   knows the path and format. It now also reads the index manifest and the
   source size, so a document with a `ready` / `building` index, or a source
   above 8 MiB, enters the large branch directly.
2. Otherwise the existing `document_read` node runs. If it returns
   `strategy_deferred`, the assessment routes to the large branch instead of
   blocking, so files whose size is only discovered by parsing still work.
3. Large branch: `document_outline` (`direct_once`, waits for or builds the
   index), then a retrieval stage. In that stage the Fast step model turns the
   owner request into keywords, including Chinese and English synonyms, as the
   arguments of its own `document.search` calls. No extra model call is spent
   on query rewriting, and searches are limited to three rounds per stage.
   Retrieved ranges reach the step through provisioning under the stage
   budget. When searches miss, the model falls back to browsing outline
   titles.
4. Finalization over the retrieved ranges, under the final budget, citing
   pages or anchors and stating which sections were covered.

Whole-document requests (summarizing a whole book) use chapter-level
map-reduce: one summary per outline node, cached in the index directory by
node and source hash, then a final merge whose evidence is the cached
summaries.

**Edits are out of scope.** A `document.edit` request on a `large_indexed`
document returns an explicit unsupported reply and leaves the source
untouched. `Pipeline.Edit` still re-reads the whole source.

### 4.9 Follow-up questions

Cross-run history stays an index (section 3.4). A follow-up about the same
document re-resolves it through the existing recent-document resolver,
reuses the persisted index by hash, and searches again, which is cheap. It
does not reuse model-visible excerpts from the previous run. Cached OCR pages
and chapter summaries are reused.

### 4.10 Envelope vocabulary

Add `DocumentStrategyLargeIndexed = "large_indexed"`,
`DocumentContextRetrieved = "retrieved"`, and index statuses `building`,
`ready`, `partial` and `failed` to `app/document_pipeline.go`, keeping the
existing wire values.

## 5. Delivery And Validation

| Phase | Content | Acceptance |
|---|---|---|
| A (done in source) | Context-derived budgets, tokenizer fit, product env `0` | Unit tests for the formula, ceiling, refit and fail-closed paths; a document that fits the 32K window reaches the finalizer whole; extraction follows the finalizer |
| A2 | Measure real-tokenizer bytes per token for Chinese prose, English prose and numeric sheets on the product and dual-light models; replace head-only truncation in finalization with per-section sampling | Measured ratios recorded; a truncated summary covers every chapter instead of only the opening |
| B1 | Index, background import pre-build, analysis-phase build or wait, outline/search/read_range, revision 5 routing, `files.read` pointer, for text-layer PDF, DOCX and text | A 300-page text-layer PDF and a 1 MB DOCX answer located questions with correct page citations on dual-light and product profiles; a ready index adds no visible progress; an index still building shows only the run's progress line |
| B2 | Analysis-phase OCR for scanned PDFs | *计算机网络* (workspace file) chapter questions OCR only outline-selected pages and reuse them on repeat; a scanned PDF of at most 25 MiB can be summarized whole |
| B3 | XLSX/PPTX structure and chapter map-reduce | A whole-book summary states its coverage |

Before each phase, record the baseline for `internal/config`,
`internal/document`, `internal/toolhub` (after `npm run setup:document-tools`)
and `internal/agent`, then rerun them. UI checks must cover the existing
document interactions as well as the new progress line, per the repository
UI rules.

## 6. Risks

- **Prefill cost grows with the window.** A 256K finalizer can now receive
  about 450 KB. Accepted for now and revisited after hands-on use; the
  operator ceiling remains available, and evidence projection records keep
  the admitted payload and archived byte counts.
- **Tokenizer round trips.** Prompts near the boundary are counted exactly
  once in the fit and once at dispatch. This is accepted for correctness.
- **Keyword misses on paraphrased questions.** Mitigated by synonyms in the
  step model's search arguments, heading weighting and outline browsing.
  Embeddings are deliberately out of scope until evaluation shows a gap.
- **Full OCR latency.** Whole-document OCR of a scanned file up to 25 MiB can
  take minutes. It is batched, cached per page and visible only through the
  run's progress line.
- **Background parse load.** Import pre-build adds one text-only parse per
  qualifying upload. The queue has a single worker and yields to interactive
  reads.

## 7. Decisions (owner, 2026-11-03)

| Question | Decision |
|---|---|
| Upload limits | Keep 25 MiB (WebChat) and 8 MiB (ISCP); no chunked upload |
| Retrieval | Keyword (BM25) only for now |
| Large-document edits | Not supported for now; explicit unsupported reply |
| OCR | Whole-document OCR allowed within the existing file size limit; otherwise only for pages that retrieval selects |
| Index timing | Import pre-build plus analysis fallback, as in section 4.3; pre-build is invisible and silent on failure |
| Progress | Only when a request waits on the index, inside the run's existing tool progress |
| Context cost | Accept derived budgets without a ceiling for now; revisit after hands-on use |
