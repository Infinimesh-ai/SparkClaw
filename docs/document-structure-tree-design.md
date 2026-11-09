# Document Structure Tree And Layered Expansion Design

> Language: English | [简体中文](../zh-cn/docs/document-structure-tree-design.md)

Status: T1–T5 implemented in local source, 2026-11-09. It extends the
[unified document structure design](document-structure-and-evaluation-design.md)
and is evaluated separately from the
[2026-11-09 location/visual evaluation](document-workflow-evaluation-20261109.md).

## 1. Problem And Scope

Reading-scope quality has two independent dimensions:

1. **Structure supply**: whether the program divides a source into a clear,
   truthful structure and delivers it in a form a model can navigate.
2. **Model selection**: whether a model chooses the right ranges from that
   structure and recognizes when the acquired text is insufficient.

This design covers only the first dimension. A title-only outline gives too
little navigation signal. A table of contents that embeds body text overflows
the stage budget and still requires one global choice from a weak model.
The design avoids both by building one structure tree from the source and
exposing it level by level: each step is a bounded, local choice.

Post-read sufficiency checks, information-need decomposition, candidate
generation and relevance scans belong to the second dimension. They are out of
scope here; section 8 only reserves the facts they will need.

## 2. Verified Source Baseline

Observed at `296b3389`:

| Area | Current behavior | Consequence |
|---|---|---|
| Two structures | `index.Outline` ([chunk.go](../services/gateway/internal/docindex/chunk.go)) holds heading/bookmark sections with chunk and page spans; `index.Nodes` ([structure.go](../services/gateway/internal/docindex/structure.go)) holds flat canonical units whose `ParentID` comes only from heading levels | PDF bookmarks exist only in `Outline`; the model-visible PDF node list is pages without hierarchy |
| Model-visible outline | `OutlinePage` ([traversal.go](../services/gateway/internal/docindex/traversal.go)) walks every node in source order, clears `Text`/`Values`, and pages at `MaxOutlineNodes = 200` or the byte budget | The model sees leading paragraphs, often as `document.p[N]` locator text, and must continue linearly to discover late sections |
| Legacy consumers | Search section matches, the `section` selector and `SummaryUnits` read `index.Outline`; `include_descendants` and source locations read `index.Nodes` | The same document has two inconsistent notions of a section |
| DOCX headings | `heading_level` ([docx_index.py](../services/gateway/internal/toolhub/scripts/docx_index.py)) accepts `Title`, `Heading N` styles and `w:outlineLvl` | Documents using bold or numbered body paragraphs as headings have no tree |
| PDF headings | `pdf_index.py` reads the bookmark outline through pypdf; pages are units | No hierarchy without bookmarks; no layout-based heading evidence |
| Text and slides | Markdown `#` headings; plain text has none; PPTX slides and XLSX sheets are level-1 units | Plain text and long slides/sheets are flat |

`StructureSchemaVersion` is `1` and `StructureParserVersion` is
`native_structure_v1` in [structure.go](../services/gateway/internal/document/structure.go).

## 3. Goals And Non-Goals

Goals:

- One canonical tree per source version: every leaf belongs to exactly one
  container, in source order, with no gap or overlap.
- Structure is divided only by native evidence. Ranges without it fall back to
  labelled physical groups. The program does not infer headings in this phase.
- The whole document's top-level shape is visible in the first outline call
  within a bounded budget; late sections never depend on linear paging.
- Any container can be expanded on demand. Content signal comes from a bounded
  extracted lead per container where the level's budget allows, and from
  bounded leaf previews at the lowest container level, never from a body dump.
- A selected container reads its full subtree through the existing range path.

Non-goals:

- No model call to build or label structure; no generated chapter titles.
- No heading inference from emphasis, font size or numbering patterns in this
  phase (section 5.1).
- No change to the coverage, frozen-scope or continuation contracts.
- No new UI, transport or store schema. Persisted revisions keep their behavior.
- No claim that a better tree makes model scope selection correct.

## 4. Tree Contract

### 4.1 Node Classes

| Class | Kinds | Role |
|---|---|---|
| Root | `document` | One per source version |
| Container | `section`, `bookmark`, `sheet`, `slide`, `slide_section`, `table_region`, `page_group`, `group`, `front_matter` | Navigation and selection units |
| Leaf | `paragraph`, `heading_label`, `table`, `row`, `shape`, `page` | Canonical content; unchanged text/value contract |

A heading paragraph becomes a container whose own label leaf keeps the native
text and locator. Leaf node IDs keep today's derivation, which includes the
schema and parser versions: they are stable within one version and all change
when section 7 bumps it. Container IDs are derived from source hash, schema
version, kind, first leaf and depth.

### 4.2 Container Fields

`StructureNode` gains optional fields; leaves omit them.

| Field | Meaning |
|---|---|
| `level` | Tree depth below the root |
| `structure_source` | `native_style`, `outline_level`, `markdown_heading`, `pdf_bookmark`, `slide_section`, `slide`, `sheet`, `physical` |
| `span` | First/last leaf block, page range and ordinal range |
| `stats` | Child containers, leaves, text bytes, estimated tokens, tables, unavailable/unrecognized leaves |
| `children_complete` | Whether all children are known at this generation |
| `lead` | Optional extracted preview: the first sentence of the container's first body leaf, bounded in runes, marked as a preview; included only under section 6.3 rule 4 |

Titles remain native text bounded by `maxHeadingRunes`. A `group` or
`page_group` title is a factual range label such as `p.12–18` or
`document.p[200]–p[260]`, never invented wording.

### 4.3 Invariants

Runtime build verifies, and rejects a tree that fails:

1. Leaves in pre-order equal source order and cover every canonical unit once.
2. A container span equals the union of its children.
3. Content before the first top-level container is `front_matter`.
4. Duplicate titles remain separate containers with separate locators.
5. Physical containers never become parents of native ones.

`index.Outline` becomes a projection of the tree for legacy consumers. A tree
build never yields two sectionings of one generation.

## 5. Structure Division

Each format uses an ordered ladder of native evidence. A lower rung applies
only where higher rungs provide no structure for that range, and it records its
source. Physical grouping is the final rung for every format.

| Format | Ladder |
|---|---|
| DOCX | `Title`/`Heading N` styles and `w:outlineLvl` → physical groups |
| PDF | Bookmarks mapped to page spans → page groups |
| Markdown | `#` headings → physical groups |
| Plain text | Physical groups at blank-line block boundaries |
| PPTX | `p:sectionLst` sections → slides → title and shapes as leaves |
| XLSX | Sheets → row-range groups; table regions are deferred (section 10) |

### 5.1 No Heading Inference In This Phase

The program does not promote body paragraphs to headings from bold, font size,
numbering text such as `第X章`, `一、` or `1.1`, or list numbering. Such
documents receive physical groups whose leads and leaf previews expose their
opening text. Inference is a separate later design, decided from evaluation
evidence of how often native structure is missing; if adopted it must remain
labelled, threshold-bound and subordinate to native structure. OCR does not
create headings.

### 5.2 Physical Groups

A container whose direct leaves exceed the expansion budget is split into
consecutive `group` children by leaf boundaries and byte size. Groups never split
a table or worksheet row. Shared headers, merged anchors and native labels may
belong to another group; their explicit dependency closure accompanies each
content read without duplicating canonical leaf ownership. PDFs without usable
headings use `page_group` containers. These are explicitly navigation windows,
not chapters, consistent with section 4.2 of the unified design.

## 6. Delivery: Layered Expansion

### 6.1 `document.outline` Request

New optional arguments (revision 9 projections only):

| Argument | Meaning |
|---|---|
| `node_id` | Expand this container; default is the root |
| `depth` | Requested depth below `node_id`; runtime caps it |
| `cursor` | Continue a wide child list of the same container and generation |

The legacy linear pagination remains for persisted revisions and direct
callers that omit these arguments.

### 6.2 Response

- The requested container with its ancestors (path), then its subtree in
  breadth-first order.
- Each container: `id`, `kind`, `title`, `level`, `structure_source`, `span`,
  `stats`, `expandable`, `children_complete` and, under rule 4 of section 6.3,
  `lead`.
- At a container without child containers, its leaves with a bounded native
  preview (the first runes of text, the header row of a table), marked as
  previews. Preview presence never counts as acquisition or coverage.
- `source_sha256`, `structure_generation`, `next_cursor` and coverage with
  scope `structure_discovery`, as today.

### 6.3 Budget Rules

1. The structure budget is one quarter of the Fast stage evidence budget
   (`StageEvidenceBytes`). The share is configurable, recorded in the response,
   and revisited from evaluation results.
2. Breadth first: one complete level is emitted before any deeper level. The
   initial call auto-deepens only while the next whole level fits.
3. If even the first level does not fit (for example, hundreds of slides), the
   runtime inserts source-bound `group` navigation windows over that level until it fits.
   These are views of consecutive siblings, not new canonical parents of native
   containers; their IDs select the same complete leaf span. The model
   always sees the whole document's extent in one call.
4. Leads are included for a level only when that whole level fits with them;
   otherwise the level is emitted without leads for every container, never with
   leads for only some. Leads are added before the next level is deepened.
5. Leaf previews are trimmed uniformly per leaf before any leaf is dropped; a
   dropped leaf list returns a cursor for the same container.
6. Leads and previews never count as acquisition or coverage.

### 6.4 Reading Selected Containers

`document.read_range` with `node_ids` and `include_descendants=true` already
expands through `ParentID`. Container nodes join that relation, so selecting a
section, group or sheet reads its whole subtree with dependencies under the
existing frozen selector, cursor and coverage rules. The `section` selector
resolves through the tree projection of `index.Outline`.

## 7. Versioning And Compatibility

- `StructureSchemaVersion` becomes `2` and the parser version becomes
  `structure_tree_v1`. New tree indexes use a separate `-tree-v1` cache directory. Legacy v1 indexes,
  generations and cursors remain available to persisted revisions; cross-version
  cursors are rejected, never silently rebased.
- `document.read` revision 9 uses tree expansion in its instructions and
  projections. Revisions 4–8 keep their persisted behavior; their stored
  generations are not rebased onto the tree.
- `SummaryUnits` keeps its unit limit but is fed top-level tree containers, so
  bookmark and physical containers improve summaries without a new summarizer.
- Search section hits and source-location relations read the tree; returned
  IDs remain exact copyable node IDs.
- Tool names and tool-policy keys are unchanged.

## 8. Workflow Integration Boundary

Revision 9 changes only structure discovery:

- The structure stage calls the root outline directly, as today.
- The scope stage may call `document.outline` with `node_id` before any content
  read; expansion calls do not freeze `task_mode` or the selector.
- Runtime records the expansion path (`expanded_node_ids`) and structure-budget
  degradation in `document_read_` facts. Second-dimension mechanisms will use
  these facts; this design does not add their decisions.

## 9. Validation

Structure quality is checked deterministically, without a model:

| Check | Fixtures |
|---|---|
| Tree equals expected hierarchy and provenance | DOCX with styles; duplicate headings under different parents; PDF with and without bookmarks; PPTX with sections; multi-sheet XLSX |
| No inferred containers | DOCX with bold/numbered headings only and plain text with `一、/1.1` yield physical groups only |
| Leads are uniform per level and bounded | Wide and narrow levels at the configured share |
| Invariants of section 4.3 | All fixtures plus existing owner copies under `test-files/` |
| Whole extent visible in the first call | Long flat DOCX, 300-slide deck, 500-page PDF |
| Expansion bounded and complete | Every container's cursor walk returns each child once |
| Selected container reads its whole subtree | Section, group, sheet and slide selections |
| Legacy behavior unchanged | Revision 4–8 cold-resume and existing outline/search/summary suites |

Real-model runs on the existing evaluation harness are recorded as observations
of scope selection with the new tree, separately from these checks, and never
as proof that the structure work fixed model selection.

## 10. Implementation Phases

| Phase | Content | Main files |
|---|---|---|
| T1 | Tree contract, container fields, invariants, `Outline` projection | `document/structure.go`, `docindex/structure.go`, `docindex/chunk.go`, `docindex/index.go` |
| T2 | First-batch format ladders: DOCX styles/`outlineLvl`, Markdown headings, PDF bookmarks into the tree, PPTX sections, XLSX sheets, physical groups for every format | `toolhub/scripts/*_index.*`, `toolhub/document_index_extract.go`, `docindex/structure.go` |
| T3 | Layered `document.outline`, budget rules, leads, previews, container reads | `docindex/traversal.go`, `toolhub/document_retrieval*.go` |
| T4 | Revision 9 profile and facts; legacy consumers on the tree | `agent/document_read_profile.go`, `agent/document_read_scope.go`, `docindex/summary.go` |
| T5 | Deterministic fixtures and checks; bilingual docs | `docindex`/`toolhub` tests, `eval/document-workflow/` |
| Later | XLSX table regions with header rows; heading inference only through a separate design | `toolhub/scripts/xlsx_index.js`, adapters |

## 11. Risks And Open Questions

- Documents whose headings are only bold or numbered body text, and PDFs
  without bookmarks, get physical groups in this phase. Leads and previews are
  their only content signal; how often this occurs must be measured before any
  inference design.
- A lead is the opening sentence, not a summary: it can be uninformative for
  sections that open with boilerplate. It is a navigation aid, never evidence.
- Layered expansion adds discovery calls. The initial auto-deepening and the
  existing attempt budgets bound them; the actual round-trip cost must be
  measured on the evaluation harness.
- A clearer tree does not ensure correct selection. Section 1 leaves that to a
  separate design.


## 12. Implementation And Validation Record

The tree builder and invariant checks are in `docindex/tree_build.go` and
`tree_contract.go`; `tree_outline.go` implements bounded breadth-first views.
Adjacency, leaf ordinals and node-ID lookup are initialized once on build/load
and shared immutably. `index.Outline`, summary units, search sections and source
locations derive from that tree. Summary location metadata keeps native
containers; physical navigation windows do not consume the summary body budget.

`runtime.document_structure_share_percent` defaults to 25 and invalid values
outside 1–100 backfill to 25. The response records the effective byte ceiling,
share and degradation. Depth is capped at 8, a delivered level at 200 entries,
leads/previews at 120 runes, physical groups at 24 leaves or 16 KiB before atomic
table/row constraints. PDF grouping depends on page counts, not OCR text size,
so enrichment cannot move an already-selected navigation range. Native depth
above 64 is rejected explicitly. A budget below the metadata/progress minimum
returns an error; it never returns a self-repeating window or truncated JSON.
The already-budgeted tree projection remains intact in compact model evidence.

Revision 9 binds `structure_version=2` and exposes `node_id`/`depth`; direct
callers may opt in with these arguments. Calls that omit them retain v1 linear
behavior, as do revisions 4–8. `document_read_expanded_node_ids` records each
expansion, `document_read_structure_budgets` records its budget decisions, and
`document_read_outline_node_id` binds a leaf-list cursor to its own container.
Discovery never freezes content mode or counts previews as acquired leaves.
No UI, transport or database schema changed.

Deterministic tests cover native DOCX styles and outline levels, duplicate
headings, non-inference, PPTX sections, XLSX sheets/row boundaries, Markdown
fences and lossless text, native PDF bookmarks/unrecognized pages, 300 slides,
500 pages, full cursor walks, selected subtree reads, same-page empty bookmarks,
OCR stability, small-budget previews and summaries, and v4–8 FileStore cold
resume. A FileStore-backed runtime test executes root → container expansion →
content read and verifies durable facts. Independent review findings on empty
sources, window progress, previews, summary boundaries/metadata and repeated
whole-index scans were corrected and rechecked.

Validation completed on 2026-11-09:

- Isolated Linux `go test -json ./...`: 82 packages passed, 6,230 test/subtest
  passes, 216 conditional skips, zero failures (12 packages have no tests).
  `go build ./...` and `go vet ./...` passed.
- Final Linux affected-package run after supplementary boundary tests:
  `docindex`, `document`, `config`, `capability`, `toolhub`, `agent` passed
  (1,801 test/subtest passes, 11 conditional skips), plus full build/vet.
- macOS affected packages passed; focused `-race` tests for structure/tree,
  document reads and legacy resume passed. The full macOS run retained the
  pre-change `cmd/sparkclaw`, `browserhost` and `gateway` temporary-path/host
  admission failures; the two new revision-expectation test failures were
  updated to revision 9 and passed subsequent affected-package runs.
- The CI Markdown/link/mirror check passed for 144 source documents.

The build and outline orchestration functions keep the full invariant/publication
and breadth-first budget transaction together; their length exceeds the usual
100-line function guideline. Format adapters, projection, navigation lookup,
selection, validation and compatibility helpers remain separate files/functions.
This checkout has no `test-files/` directory, so owner-copy checks were not run.
No new real-model evaluation was performed; no model-selection improvement is
claimed. This is local source work, without push, installation or deployment.

## 13. Revision 10: Model View And One Size Measure

The revision 9 evaluation (2026-11-09) found that model prompts carried the
program's records. A whole-source read of a 649-paragraph DOCX sent complete
node JSON to the final model: 169,100 prompt tokens, of which source text was
about 7–8% of the characters. IDs, dependency ID lists, parent IDs, offsets,
availability, provenance and duplicated table values made up the rest. The
outline estimated the same source as about 21,000 tokens from text bytes alone,
so navigation and delivery used different measures.

Revision 10 separates the two roles:

- **Program records stay complete.** Tool outputs, persisted facts, cursors,
  coverage and execution evidence keep node IDs, hashes, offsets and
  availability. Nothing that records what was read is removed.
- **Models read a model view** (`document.ModelView`, `document_model_view_v1`).
  Native headings are `#` lines with their locator; every other node is one
  `[native locator] text` line in source order. Dependencies appear once, marked
  `(context)` when they are not selected content. A continued node is marked
  `partial text bytes a-b`. Unsupported or unavailable nodes state their status
  and reason. Typed values keep only facts that change meaning (formula, cached
  result and recalculation state, hidden rows/columns, merged-from cells); parser
  bookkeeping and copies of visible text are dropped.
- The view is used by the final answer evidence, the bounded task-selection
  batches (aliases remain `n1`, `n2`; the runtime maps them back to nodes) and
  summary contract 5 section inputs. Source locations keep `block`,
  `parent_block`, range, page and title; node IDs, ordinals and structure
  provenance are omitted. Slide visual observations omit source/PDF/PNG hashes
  and model call IDs, which remain in the tool output.
- The outline stays a navigation surface. With runtime-bound `model_view`, its
  content is an indented text tree. Node IDs remain because they are the handles
  for `node_id` and `node_ids`; parent IDs, ordinals and zero counters stay in the
  structured output.

One size measure: each outline container reports `read_tokens`, computed from
the model-view bytes of its canonical leaves (`Index.ModelViewBytes`, cached with
the immutable navigation maps) divided by 3 bytes per token. The structure
tree, its stats and its generation are unchanged, so revision 9 cursors and
outputs stay valid. A deterministic test checks that the whole-document
estimate equals the bytes of a delivered whole-source read, and that a
subtree estimate plus its ancestor heading context equals that subtree's read.

The first revision 10 run exposed a source-location ambiguity that the JSON
view had also carried. The document root is not a heading, but its locator is
the first source block, so top-level headings reported
`parent_block=document.p[1]` — the same block as a first heading such as
“Section 21”. One answer concluded that a later checklist belonged to that
section. Under `model_view`, location maps omit the root as a parent and mark
`parent_block=none(top-level heading)`, and relations state that a top-level
heading belongs to no parent heading. Calls without `model_view` keep the
previous map and relations.

Compatibility: revision 9 is frozen as a legacy profile and still binds summary
contract 4 and the JSON outline; revisions 4–8 are unchanged. Revision 10 binds
contract 5 (cache key `document_section_summary_v5`). Final-answer budgets,
truncation markers and task-selection triggers keep their meaning; they are
measured on the delivered view. The final-answer byte budget still assumes 3
bytes per token, which is conservative for the text view.

Validation on 2026-11-09 (local source, not committed):

- New deterministic tests cover the model view (heading depth, deduplicated
  context, partial fragments, gaps, typed values, table merges), the
  estimate-equals-delivery invariant, revision 10 final evidence and task
  selection without node IDs, frozen revision 9 projections and bindings,
  contract 5 parts (context once, typed atomicity, lossless text split), the
  outline model view and the top-level location contract.
- macOS: `document`, `docindex`, `toolhub`, `agent`, `capability`, `config`
  passed; `go build ./...` and `go vet ./internal/...` passed. The full macOS run
  passed 79 packages and retained the known `cmd/sparkclaw`, `browserhost` and
  `gateway` host/temporary-path failures, which also fail at the unmodified
  commit. Linux full qualification was not rerun.
- Real model, the same 20 read requests as the revision 9 evaluation, one
  attempt each: total prompt tokens fell from 1,078,690 to 491,432; the largest
  final call (whole-source summary of the 649-paragraph DOCX) fell from 169,100
  to 27,098 tokens; total elapsed time fell from 655 s to 510 s. Coarse checks
  passed 20/20 and source review accepted 18/20 (revision 9: 16/20). One
  compound request still under-read the explanatory section and made an
  unsupported absence claim; one summary still repeats runtime extraction
  limitations. These are single attempts and not an accuracy estimate.
