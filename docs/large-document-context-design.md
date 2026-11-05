# Large Document Context And Retrieval Design

> Language: English | [简体中文](../zh-cn/docs/large-document-context-design.md)

Status: Part A (context-derived content budgets) is implemented in source on
2026-11-03 and not yet deployed. Part B (the `large_indexed` structure-first
retrieval strategy) follows the owner decisions recorded in section 7. All
three phases are implemented in source on the same day: B1 (text-layer PDF,
DOCX and text), B2 (analysis-phase OCR for scanned PDFs) and B3 (PPTX/XLSX
structure and whole-document chapter summaries). Nothing is deployed.

Next design, 2026-11-05: [Unified document structure and evaluation evidence](document-structure-and-evaluation-design.md)
proposes structural access and on-demand reads for every document, replacing
size-triggered capability selection while retaining context and resource budgets.
It also defines per-run evidence for owner-led external review, without an
in-program reviewing model. This is a future design, not a change to the
implementation status recorded here.

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

When a document is larger than the final budget, the finalizer projects
evenly spaced windows across the whole document instead of only its opening:
at most 64 windows of at least 2 KB, starting at blank-line (paragraph or PDF
page) boundaries, with the last window ending at the document's end and each
omitted span marked with its byte count. The header carries
`model_evidence_truncated=true`, `model_evidence_sampling=evenly_spaced_windows`
and `limitation_required=true`, and the answer states that only part of the
document was checked.

### 3.2.1 Measured bytes per token (2026-11-03)

Measured against the hosted Fast tokenizer (`Qwen/Qwen3.6-35B-A3B-FP8`, the
same Qwen3.6 family as the dual-light models):

| Sample | Bytes per token | Runes per token |
|---|---|---|
| Chinese lab report (DOCX text) | 4.77 | 1.97 |
| Chinese design document | 3.63 | 1.93 |
| English design document | 3.94 | 3.92 |
| Numeric sheet (8 decimal columns) | 1.00 | 1.00 |
| Product codes with digits | 1.13 | 1.13 |

Three bytes per token is safe for prose. Digits tokenize one per byte, so a
numeric sheet at the derived budget costs three times its estimate. The
tokenizer fit converges in two renders in that case.

The measurement also exposed a defect: admission posted exact counts to
`<base>/v1/tokenize`, but vLLM serves `/tokenize` at the server root, so every
prompt whose byte length exceeded its input budget failed as "model
unavailable". The router now strips a trailing `/v1` before `/tokenize`
(commit `815f2ed6`).

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

## 4. Part B: `large_indexed` Strategy (implemented in source)

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

The strategy is selected by the read outcome rather than registered in the
pipeline. The complete-read strategy defers when the source exceeds 8 MiB
(`SmallFileMaxBytes`) or the complete extraction exceeds the ceiling of
section 3.1. For PDF, DOCX, PPTX, XLSX and text, `files.read` and `pdf.extract_text`
answer that deferral with the outline pointer of section 4.7. Other formats,
such as images, keep the `strategy_deferred` error, and so does a read whose
caller asked for less than the extraction limit. When OCR is enabled, a PDF
whose text-less pages exceed one complete read's OCR budget (8 pages) also
moves to retrieval. The pages that read already recognized seed the index
cache, so they are not recognized twice. Without OCR the partial read stands,
because retrieval could not recognize the remaining pages either. Everything
that fits today keeps the `small_direct` / `full_text` path, unchanged.

### 4.3 Lifecycle: import pre-build plus analysis fallback

**Import phase (background, lightweight).** After a WebChat document upload
is stored, the gateway queues it on the ToolHub's index service: one worker,
a 32-entry queue that drops requests when full, cancelled when the hub
closes. The worker runs the text-only index extraction of PDF, DOCX and text
files. When the source is at most 8 MiB, its text fits the extraction ceiling
and no PDF page lacks text, the document fits the complete-read path and no
index is kept. Otherwise the structure map, text-layer chunks and keyword
index are persisted. The import phase:

- never runs OCR or any model call;
- shows nothing to the owner and adds no status field to the UI;
- writes nothing on failure except a log line, and the analysis phase retries
  the build.

**Analysis phase (on demand).** When a request reads the document:

| Index state | Behavior |
|---|---|
| `ready` | Retrieve directly |
| `building` (import still running) | Wait for that build instead of starting a second one |
| missing or `failed` (non-upload file, or a failed pre-build) | Build in this run, then retrieve |

OCR happens only in this phase (section 4.5).

**Progress.** The owner sees index work only when a request has to wait for
it. While a read waits on or builds the index, the agent re-saves the running
call with `progress: "正在建立文档索引"`. The note reaches WebChat as the
existing `tool_call.started` stream event, and the stream status line shows it
in place of the generic started text. Nothing is shown when the index is
ready, the completed call record carries no note, and no resident indicator
is added (AGENTS.md UI rules).

### 4.4 Index content (once per source SHA-256)

1. **Structure map** (`outline.json`), by format:
   - PDF: bookmark outline to a heading tree with page ranges. A PDF without
     bookmarks has no outline yet and is searched directly.
   - DOCX: heading styles (`Heading N`, `标题 N`, `Title`) and outline levels;
     each table row is one text unit.
   - Text / Markdown: `#` headings; paragraphs are blank-line separated.
   - PPTX (`scripts/pptx_index.py`): one level-1 node per slide titled
     `Slide N: <title>`, then text frames, table rows and speaker notes; the
     slide number is the anchor's page and range headers say `slide N`.
   - XLSX (`scripts/xlsx_index.js`): one level-1 node per sheet; every
     populated row becomes `header: value` pairs from the sheet's first row,
     so a column value is found together with its column names.
2. **Chunks** (`chunks.jsonl`): split along structure nodes at about 1,000
   characters with a small overlap. Each chunk carries an anchor (page,
   heading path, stable block / paragraph / cell ID) in the existing citation
   vocabulary.
3. **Keyword index** (`keywords.json`): pure-Go BM25 only, with no embeddings
   in this design. Chinese is tokenized into character bigrams and Latin text
   into lowercase words; heading-path terms count twice. Text and queries are
   NFKC-normalized, because PDF text layers often carry compatibility forms
   such as the Kangxi radical `⽅` for `方`. PDF line breaks between two
   ideographs are joined so words are not split. No new runtime dependency.
4. **Page map**: text-less PDF pages are marked `unindexed` until OCR fills
   them. An entirely scanned PDF is a valid index with no chunks yet (state
   `partial`), and outline nodes carry page ranges as well as chunk ranges.
5. **Extracted source** (`source.json`): the units of the last extraction.
   Merging recognized pages rebuilds chunks and keywords from it without
   parsing the document again.

Search snippets centre on the rarest matched term, so a row found by its
product code shows that code. Search also returns outline sections whose
titles match the query, which locates chapters of a scanned book before any
of their pages has text.

PDF builds run in 40-page batches (`scripts/pdf_index.py`, pypdf, text only)
so no adapter call meets the 60-second subprocess timeout. DOCX uses one
`scripts/docx_index.py` call (python-docx, body order, no assets). Text files
stream line by line, with lines longer than 64 KiB split. Builds run on the
service's own context, so a request that stops waiting does not cancel a
build another request may need.

### 4.5 OCR policy

OCR runs only in the analysis phase, through the existing OvisOCR2 runtime at
its configured concurrency. Pages are rendered as in a complete read
(`scripts/pdf_index.py` operation `render`, pypdfium2 at scale 2, JPEG of at
most 4 MiB). Every recognized page is cached in `ocr/<page>.json` as soon as
its batch finishes, so an interrupted request keeps its work and no page is
recognized twice. Running calls show `正在识别扫描页（n/m）` in the run's
progress line.

- **Targeted questions** OCR only the pages that retrieval selects. On a
  scanned PDF, body text is unknown before OCR, so selection uses outline
  titles and their page ranges: `document.read_range` on a section or page
  range recognizes that range's text-less pages first, at most 8 per call,
  and returns the rest as `unrecognized_pages` / `missing_page_indexes`.
- **Whole-document requests** (`document.summarize`), and searches on scanned
  files without an outline, recognize every page in batches when the source
  is within the existing 25 MiB upload limit. Workspace-only files above that
  size keep hit-page OCR, and the answer states which pages were not
  recognized.

### 4.6 Storage, isolation and retention

- Indexes live under the document pipeline's workspace root at
  `.sparkclaw/state/doc-index/<sha256>/` (`manifest.json`, `outline.json`,
  `chunks.jsonl`, `keywords.json`, `source.json`, `ocr/<page>.json`,
  `summaries/<key>.json`). That root is the
  hub's configured workspace root, or the owner workspace of an execution
  scope. A query needs an authorized path to a file with that hash, so index
  access follows file access. The same file uploaded twice reuses one index,
  and the four most recently used indexes stay loaded in memory.
- `.sparkclaw/state` is a control directory: output paths cannot target it,
  `files.search` skips `.sparkclaw`, and every read path now refuses it, so
  the index is reachable only through the document tools.
- The manifest records source SHA-256, size, strategy and parser versions,
  state (`building`, `ready`, `partial`, `failed`), attempts and timestamps.
  A version change rebuilds the index.
- Cleanup removes an index when none of its recorded source paths still
  exists with its recorded size, and an LRU sweep keeps the directory under a
  per-workspace size cap of 512 MiB. A sweep runs after every build.
- With the Postgres state backend the index stays on the workspace volume,
  because it is derived data. Losing it only costs a rebuild.

### 4.7 Model tools (read risk, their own capabilities)

| Tool | Returns | Bound |
|---|---|---|
| `document.outline(path)` | Heading tree, page ranges, size, index state, unindexed pages | Stage budget |
| `document.search(path, keywords[], top_k, section?)` | Ranked snippets with anchors and scores, plus outline sections whose titles match; recognizes an outline-less scan within 25 MiB first | `top_k x snippet` within the stage budget |
| `document.read_range(path, chunk_ids \| page_start..page_end \| section, context)` | Text for that range with location headers (`PDF page N`, `slide N`); recognizes up to 8 scanned pages of the range first; `next_chunk_id` when cut; neighbouring context is dropped before requested chunks when the budget is tight | At most the stage budget |
| `document.summarize(path)` | One summary per top-level section, as Markdown and as a list; `read_complete` false when sections were sampled or pages stayed unrecognized | Final-answer budget per section call |

The tools carry their own capabilities (`document.outline`, `document.search`,
`document.read_range`, `document.summarize`), so they never compete with the
format readers of `document.read`. Search and range reads time out after 15
and 10 minutes, and summaries after 20, because they may recognize pages or
call the model per section. Their budget is the Fast lane's stage evidence budget, the
lane document workflows run on. For an indexed document, `files.read` and
`pdf.extract_text` return the outline (at most 8,000 bytes) with
`retrieval_required: true`, `read_complete: false` and a hint to use
`document.search` / `document.read_range`.

### 4.8 Workflow and routing

`document.read` revision 5 keeps the revision 4 path for small documents and
adds a large branch. Persisted revision 4 plans keep their shape on resume.

1. `confirm_document_target`, unchanged.
2. `document_read`, stage `read_by_type`, still calls the format reader
   directly (a profile direct stage instead of `direct_once`, because the node
   now has transitions). A document that fits completes the node as before. A
   deferred indexable document returns the outline pointer. Its
   `retrieval_required` outcome carries a `document_outline` resource ref and
   transitions the node to stage `retrieve`, whose scope is the three
   retrieval tools, each with `path` bound to the frozen target. Routing
   therefore needs no preflight size probe; even sizes discovered only by
   parsing work.
3. In `retrieve` the Fast step model turns the owner request into keywords,
   including Chinese and English synonyms, as the arguments of its own
   `document.search` calls. No extra model call is spent on query rewriting.
   The latest outline and search results are provisioned as compact text
   (one line per hit with chunk ID, page, section and snippet). Search and
   outline outcomes re-enter the stage at most four times; the instruction
   asks for at most three searches. A `document.read_range` with content
   completes the node; the node allows six attempts.
4. Finalization over the read and the retrieved ranges, under the final
   budget, citing pages or headings and saying the answer is based on the
   retrieved sections.

Whole-document requests (summarizing a whole book) use chapter-level
map-reduce through `document.summarize`. The units are the top-level outline
sections, plus the text before the first heading; more than 16 are merged
into 16 consecutive groups, and a document without an outline is split into
windows of about 40 chunks. Each unit is summarized by a Fast call
(`document.section_summary`, `answer` output class), up to four at a time.
A unit longer than the Fast final-evidence budget is summarized in parts
(at most 8, sampled evenly beyond that) whose summaries are merged; a part
the tokenizer still rejects is halved. Summaries are cached by prompt
version, title and text, so a repeated request costs no model call. The
finalizer merges the section summaries and names sampled or unrecognized
parts.

Once retrieval or summary evidence exists, finalization leaves the outline
pointer out: its `read_complete: false` and missing pages describe the
routing read, not the passages or summaries read afterwards. A failed
retrieval query, such as an unknown section ID, counts as a miss, so the
step model can query again within the stage's bounded transitions.

**Edits are out of scope.** In `document.edit`, a locate read that returns
`retrieval_required` blocks with `large_document_edit_unsupported`. The owner
receives "这个文档太大，暂不支持直接修改，原文件保持不变。可以先就其中的内容提问或查找相关段落。"
(or its English equivalent), no editor runs, and the source is untouched.
`Pipeline.Edit` still re-reads the whole source.

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
| A2 (done in source) | Real-tokenizer bytes per token (section 3.2.1); tokenizer endpoint fix; evenly spaced finalizer windows instead of head-only truncation | Ratios recorded; a truncated 40-chapter fixture yields windows from the first to the last chapter |
| B1 (done in source) | Index, background import pre-build, analysis-phase build or wait, outline/search/read_range, revision 5 routing, `files.read` pointer, edit refusal, for text-layer PDF, DOCX and text | Unit and end-to-end tests (mock model): a large Markdown book runs read → search → range read → finalization containing the passage; the edit request is refused with the file unchanged; one progress event while building, none when ready; upload prebuild keeps an index only for the large file. A 56-page Chrome-printed PDF and a matching DOCX rank the right section first. Real hosted Fast model (product profile): a 394 KB book is read whole and answered correctly; a 790 KB book runs read → search (`慢开始门限 ssthresh 拥塞窗口 cwnd 增长`, chosen by the model) → one range read, answers correctly with its section in 15.5 s including the index build. Packaged-client acceptance is still open |
| B2 (done in source) | Analysis-phase OCR for scanned PDFs, title-matched sections, scanned small-PDF routing | Unit tests with a fake OCR adapter cover section OCR in batches of 8, whole-file OCR of an outline-less scan, seeding from a budget-exhausted read, and the OCR-disabled path. Real hosted Fast and OCR models: on the 71 MB *计算机网络* workspace file, "根据 计算机网络.pdf，总结一下 TCP 拥塞控制里慢开始和拥塞避免的内容" ran read → search (sections by title) → one section read that recognized only §5.8.2's pages, in 33 s. A 20-page outline-less scanned excerpt was recognized whole and answered from the transport-layer pages in 44 s |
| B3 (done in source) | PPTX/XLSX structure and chapter map-reduce | Unit tests cover slide headings and notes, a 6,000-row workbook found by product code, summary units, grouping and windows, the cache, and an end-to-end whole-book request. Real model: "把 book.md 全文总结一下" on the 790 KB book ran read → `document.summarize` and returned a 20-chapter summary in 50 s |

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
  qualifying upload. The queue has a single worker, and on-demand builds
  run beside it rather than behind it.
- **Summary cost.** A whole-document request makes up to 16 Fast calls the
  first time (more when sections are split); the cache makes repeats free.
- **Routing phrasing.** Typed Chinese file names are now found (they were
  not before this work), but a question phrased as general knowledge, such
  as "计算机网络.pdf 里 … 是怎么工作的？", may still be answered by the
  conversation route instead of `document.read`. That is a routing decision
  outside this design.

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
