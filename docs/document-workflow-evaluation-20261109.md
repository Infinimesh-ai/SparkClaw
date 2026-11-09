# Source locations and slide visuals — 2026-11-09

Language: English | [简体中文](../zh-cn/docs/document-workflow-evaluation-20261109.md)

This follow-up on `codex/document-system` starts from `13402280` and implements
`document.read` revision 8 with summary contract 4 (source commit `518fab07`). It keeps one workflow and the
existing UI. Internal node work may summarize source parts or inspect selected
slide images. Previous failures and revisions 4/5/6/7 remain available.

## What the captures showed

The [previous evaluation](document-workflow-evaluation-20261105.md) recorded a
correct seven-item checklist with the wrong location, “section 21 and before the
appendices.” The source places its heading at `document.p[641]`, under
“快速复习与仓库阅读路线”, after the appendices. Section summaries compressed native
locators into model prose, and the final model inferred chapter membership and
conventional appendix order. Grouped summary units are not original chapters.

A new duplicate-heading fixture exposed another stage error: a worker receiving
one section treated a requirement elsewhere in the document as globally missing.
The final answer then called the source incomplete and contradictory despite
complete acquisition. Its keyword checks passed. The failure is retained. A rephrased owner-manual request then chose targeted
reading: the initial location map existed only on summarize, so range reading
still located the wrong subject and mistook a checklist mentioning limitations
for the explanatory source. The range path is now covered too.

The original PPTX slide 14 contains 65 native nodes, including 28 unsupported
nontext shapes. Those are mainly rectangles/backgrounds and a freeform shape;
28 is not a count of 28 unobserved factual statements. The read chain never
passed a rendered slide to the model. A left-arrow fixture therefore returned
ALPHA and BETA but could not determine direction; keywords passed despite the
unfulfilled task. An image-only chart could not supply its values.

## Implemented changes

- Summary contract 4 carries native heading titles, paragraph blocks, parent
  blocks and original order directly into final evidence. Duplicate titles keep
  separate locations. Range reads also carry native location metadata, without
claiming the corresponding bodies were read. This map bypasses section and merge prose, uses the same
  bounded budget, and is never silently sliced into partial JSON. Section
  workers summarize local evidence without making whole-document absence claims.
  Scope selection must acquire actual explanations rather than checklist mentions.
- Revision 8 requests images for selected PPTX pages, using an immutable source
  snapshot and the existing pinned Gotenberg/PDFium rendering path. Each actual
  image call retains its source hash, page, PDF/PNG hashes and model call ID.
  The reader independently validates the renderer endpoint allowlist even when
  write visual QA is disabled. No mutation-QA judge is used for reading.
- The image path is bounded to eight selected pages, or a smaller configured
  renderer cap. Missing pages, invalid observations and unavailable infrastructure
  remain explicit. Source/version or render integrity failures stop the read.
  Continuations do not repeat image calls. Native object coverage remains partial;
  processing a page image does not certify exact object parsing or understanding.
- Final projection retains both the native source and visual observations with
  separate coverage. The partial-answer prefix no longer mislabels native shape
  gaps as visually unread objects. Existing persisted revisions keep old behavior.
- The offline analyzer now verifies the actual `model.http.dispatched` events,
  image hashes and source/page/call bindings. Semantic acceptance requires a
  review tied to the exact run ID and answer hash, covering task fulfillment,
  locations, visual relations and limitations. Keyword checks remain separate.

## Cases and review method

Owner files are copies under `test-files/document-evaluation-20261109/workspace`.
New DOCX fixtures place duplicate headings under different parents, then reverse
appendix order and remove chapter numbering. New PPTX fixtures include a grouped
decorative rectangle, a left arrow whose text order suggests the opposite
relationship, an image-only chart (37 and 82 units/hour, difference 45), a decoy
neighbor slide (999 and 111), and a renamed right-arrow variant.

The production Runtime, ToolHub, real Fast model, OCR and file store run serially
with isolated execution capture enabled. Review compares DOCX paragraphs/native
structure and the actual PNG bytes dispatched to the model, not a model claim
that it saw an image. All result JSON files and sealed ZIP exports are retained
outside Git, including failed baselines and unsuccessful prompt changes.
Source comparison is performed by the coding agent; it is not an independent
human score or a statistical accuracy estimate.

Repository entry points are `eval/document-workflow/generate_location_visual_fixtures.py`,
`location-visual-cases.json`, `location-visual-variants.json`, `analyze.py`, and the
existing `TestRealDocumentWorkflowEvaluation` harness. `analyze.py --reviews`
accepts per-run source review records; it never treats a missing review as a pass.
The initial fixture metadata used an incorrect DOCX locator prefix; the corrected
native `document.p[N]` ground truth is kept separately, and original records remain.

## Results and retained limitations

There are **33 retained real attempts across 13 distinct requests**. Coarse
checks pass in 32 attempts; source/answer review accepts 21. All 33 exports are
sealed and complete: 2,357 payload hashes, 242 HTTP model dispatches and 11 actual
PNG inputs were verified. These are repeated diagnostic attempts, not 33
independent samples or an accuracy estimate.

| Attempt directory | Attempts | Coarse pass | Semantic acceptance |
| --- | ---: | ---: | ---: |
| `baseline-results` | 3 | 2 | 0 |
| `fix1-results` | 3 | 3 | 3 |
| `owner-fix1-results` | 4 | 4 | 3 |
| `final-results` (first broad rerun) | 11 | 11 | 9 |
| `qualified-results` (additional probes) | 2 | 2 | 0 |
| `range-fix-results` | 3 | 3 | 2 |
| `order-fix-results` | 1 | 1 | 0 |
| `regression-results` | 6 | 6 | 4 |

Selecting the **latest**, never the best, attempt for each request leaves **8 of
13 accepted**. The remaining five are the broad manual summary, the compound
summary/location task, the slide comparison's unsupported explanatory additions,
the interview summary's evidence-status grouping, and the workbook's formula
explanation. The later regression calls the final checklist an appendix; earlier
correct answers do not erase this instability. Native source locations improve
the input contract but do not guarantee correct final wording.

The selected-range probe now correctly identifies p641 under p580 after appendix
C p630 using runtime-computed sibling relations (up to eight neighbors per side).
Its overall task still fails because it reads a checklist mentioning CloakBrowser
limitations instead of their explanatory paragraphs. Search no longer freezes
content task mode in r8; actual content selection and continuations remain frozen.
This permits a later summary decision without pretending the model reliably makes it.

Visual acquisition improves consistently on the observed left/right arrows,
image-only chart, and original slide 14. The latest slide comparison nevertheless
adds unsupported advice about big-data/complex-network/order-insensitive use cases.
Correct visual extraction is not sufficient for source-bound synthesis. Some
answers also expose internal node/status names, a remaining presentation defect.

Following the user's instruction to avoid overfitting, further case-specific
prompt changes and forced answer repair were stopped. Unsuccessful repeated
status instructions were removed; no additional model rerun was performed for
that wording cleanup. Captures retain the exact prompts and responses of each
exploration version. Final source qualification is separate from semantic acceptance.
Private `semantic-reviews.json`, `all-attempts-analysis.json`, `latest-results.json`,
`review-ground-truth.json`, and `review-images/` retain the review trail.

The existing conservative workflow state still reports `blocked` for native
PPT shape gaps, even when the selected page image supports a useful answer.
That state is not counted as proof of semantic failure or success; its source
and visual coverage are reviewed separately. Text-only extraction, image
processing and exact object parsing remain different capabilities.

Two failures outside the main location/visual repair demonstrate the remaining
model limits. Interview summaries retain the simulation disclaimer but still
place simulated interview content under “existing facts”; explicit repeated
instructions did not reliably repair this category error. A workbook answer
correctly computes 220 and flags stale cache 999, yet falsely says `SUM(B3:B5)`
fails to exclude cancelled B6. This is an unsupported causal explanation, not
an arithmetic failure. Both pass coarse facts and fail semantic acceptance.
They require stronger typed evidence-status/formula-membership handling; this
change does not claim to solve them.

Final wording remains model-generated: the native location map constrains its
inputs but is not a deterministic citation validator. Visual observations may
misread small text, ambiguous arrows, complex groups or charts. The current
renderer retains its resource/structure limits, and reads beyond the selected
page cap explicitly remain incomplete. Large location maps can exhaust the
summary/final budget and produce a limitation rather than guessed locations.
The changes improve the observed fixtures; they do not certify arbitrary
slide comprehension, perfect summaries or unrestricted whole-deck reading.

## Engineering verification

Final Linux qualification passes **6,185 tests**, skips 216, and completes
`go build ./...` and `go vet ./...`; 82 packages have passing tests and 12 have
no tests. All 1,756 gateway snapshot files are checked against the final source.
Affected host packages and the analyzer's review-identity test pass. The original
UI's frontend production build passes. Bilingual Markdown/link checks pass.

Negative tests cover missing/unallowlisted renderer configuration, unavailable
renderer, invalid PDF, invalid model observations, image/page bounds, wrong
source/selection, frozen legacy behavior, duplicate heading parents, final
projection budgets, and search-versus-content mode commitment. The renderer's
existing integrity tests remain in the full suite. Earlier stale revision
expectations and a malformed new test fixture caused verification failures;
those logs remain alongside the corrected passing run.

No UI components, menus, layouts or interactions changed. Original owner files
remain unchanged. Changes are local source/test/documentation only; no push,
installation or production configuration change was performed. The isolated
renderer used by evaluation is removed after testing.
