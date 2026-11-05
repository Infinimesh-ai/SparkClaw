# Document orchestration evaluation — 2026-11-05

Language: English | [简体中文](../zh-cn/docs/document-workflow-evaluation-20261105.md)

This evaluation on `codex/document-system` starts from `db2e1841` and feeds
observed failures back into document-read revision 7. It retains one workflow;
its nodes may search, acquire continuations and process bounded source batches.
The aim is an orchestration contract the current Fast model can execute.

## Reproducible inputs and evidence

Three owner documents from `test-files` were copied into an isolated workspace:
a 58-paragraph DOCX with a simulated interview attachment, a 649-paragraph DOCX
learning manual with ten tables, and a 19-slide PPTX. Original files were never
edited. Generated fixtures add Markdown exceptions and inventories, plain-text
comparison, native and scanned PDFs, and a workbook with units, a cancelled row,
a stale formula cache and a styled empty tail. A second inventory moves the
matching items and changes their identifiers and owners.

The production Runtime, routing, ToolHub, policy and file store execute every
case. Fast is the configured Qwen/Qwen3.6-35B-A3B-FP8 endpoint; OCR uses the
configured OCR endpoint. No mock or reviewing model participates in acceptance.
Capture is enabled only in the isolated internal configurations. Every attempt
retains its terminal result, model/tool calls and sealed export; failed attempts
are not replaced by passing reruns. The 16,000-byte evidence configuration
stresses the existing budget contract without changing the physical model.

Private source snapshots, outputs, configurations and exports reside outside
Git in `test-files/document-evaluation-20261105`. Repository assets are:

- `eval/document-workflow/generate_fixtures.py`: deterministic supplementary files.
- `cases.json` in that directory: thirteen primary tasks, including two edits.
- `variant-cases.json`: changed inventory and rephrased calculation/qualification.
- `analyze.py`: archive hashes, capture completeness, routing and factual checks.
- `verify_maintenance.py`: original hashes and precise edit/format preservation.
- `TestRealDocumentWorkflowEvaluation`: opt-in production-runtime test harness.

The analyzer's keyword/negative checks are regression checks, not semantic scores.
Manual comparison against the source is required, especially for coverage claims,
qualifications, arithmetic and locator correctness.

## Findings and changes

| Observed failure | Cause in the chain | Revision 7 response |
| --- | --- | --- |
| Interview qualification missed | Only headings were read; actual disclaimers were outside the selection | Locate qualifications and exceptions, then read actual body evidence |
| Seven-item tail list returned one item | Search hits were treated as the full named section | Selecting a heading expands its source-bound descendants, preserving sibling boundaries |
| Whole manual summary rejected | Model selected exhaustive mode and copied 185 node IDs into a 64-ID operation | Distinguish whole summary from enumeration; use section summary or runtime whole-source traversal |
| Inventory invented owners from headings | Paginated outline was mistaken for complete content | Declare whole-source versus selected scope; whole-source acquisition uses runtime `all=true` |
| PPT returned only a generic failure | Unsupported visual shapes suppressed useful native text | Present acquired text with explicit limits while retaining blocked/partial state |
| Small-budget traversal exhausted tool calls | Acquisition was tied to the smaller model evidence budget | Archive bounded acquisition batches of at least 64 KiB independently of model presentation |
| XLSX lost the main batch although all evidence fit | Final evidence bytes were divided equally between unequal batches | Allocate by actual structured evidence cost, retaining values and dependencies together |
| Large acquired inventory did not fit the final model | Reading completion was confused with model exposure | Bounded task-model batches classify local aliases; runtime retains selected original text, locators and dependency closure |
| Batch classifier duplicated aliases | Copying two long partition lists overtaxed output reliability | Strict boolean schema, exact membership validation, source-order reconstruction and a 24-batch cap |
| Initial worksheet selector contained a shortened ID | Exact long-ID copying is unreliable | Prefer sheet/container selection; permit at most two corrections before freezing scope |
| A requested final checklist item disappeared and a replacement was invented | Generic section compression did not receive the user preservation requirements | Task-aware summary contract 3 and task-specific cache identity; old contracts remain frozen |
| Canonical node ID and old section selector conflicted | Current search returns native IDs, while the legacy section field expects a different namespace | Hide and discard the legacy section selector in revision 7; keep legacy plans unchanged |
| Correct extraction described itself as sampling and invented possible missing IDs | Initial outline coverage and final raw exposure conflicted with completed batch processing | Separate historical outline coverage, traversal, batch processing and final selected evidence exposure |

Search extent is not the task's reading scope: searching the entire document for
a named section does not force a whole-document content read. Source hash,
structure generation, selected scope and continuation remain runtime-owned.
An initial selector correction retains the pinned source; a frozen scope or
continuation failure cannot choose a new range. Revisions 4, 5 and 6 preserve
their previous contracts on cold resume.

Batch selection is task execution, not evaluation. Each nonempty text/typed-value
fragment must receive exactly one relevance decision; invalid output discards
the reduction and retains an explicit limitation. No model-written quotations,
cell values or locators replace source data. Selected dependencies are retained
transitively. The final capture distinguishes complete source traversal from
model-generated relevance and does not certify semantic completeness.

## Results and maintenance feedback

The revision 6 baseline passed factual checks for eight of thirteen tasks.
The five failures were qualification, tail-list extraction, PPT comparison,
whole-manual summary and complete risk extraction. The first revised five-case
rerun repaired the principal facts; manual review still found an invented range
in the risk explanation. Further small-budget testing revealed acquisition,
projection, selector-format and coverage-language failures listed above.
All intermediate failures remain in the evidence directory.

The normal-budget rerun initially passed thirteen coarse checks. Manual review
found a real remaining summary error: the section summary omitted the final
self-check about which layer to implement first, then the final answer replaced
it with a fabricated A/B-experiment check. The retained output is now **12/13**
under the strengthened fixture checks, including one intentionally partial PPT
answer. This is not a 13/13 semantic success. Targeted extraction of that same
seven-item section succeeds: the loss is in intermediate generic summarization
and final reconstruction, not the parser.

Summary contract 3 now receives the persisted, governed user task at every
section/merge call. It preserves explicitly requested list items, original
locators and qualifications, compressing other details first. The task is part
of summary-cache identity; revision 6 retains contract 2 and its cache behavior.
This repairs a real information handoff gap instead of asking the final model
to reconstruct details removed upstream. The failed output and strengthened
check remain in the record.

A changed-inventory repeat also exposed over-selection: a 32-fragment batch
kept 32 fragments, and their combined dependency bundles exceeded final
presentation capacity, hiding an early matching item. The revised maximum is
16 fragments per batch, with navigation-only headings retained as dependencies
rather than selected as matching records. This trades more task calls for a
smaller classification burden; it is still model judgment, not a correctness
proof.
The first task-aware summary rerun preserved all seven source items inside the
tool output but still selected a different checklist in the final answer. Revision
7 now returns to the original task immediately after the long evidence payload,
prioritizing the named list over optional broad detail. The next two-summary
rerun retained the seven requested items and the interview qualifications.

| Cohort | Factual checks | Interpretation |
| --- | --- | --- |
| Revision 6 primary baseline | 8/13 | Five concrete orchestration failures |
| First full revised primary run, after manual checks were strengthened | 12/13 | The summary failure remains archived |
| Latest applicable primary reruns | 13/13 | Includes one explicitly partial/blocked PPT result |
| Latest 16 KB cases: three primary pressure cases plus three variants | 6/6 | Changed inventory needed a further bounded-selection correction |
| All retained attempts, including failed iterations | 42/59 | Never replace failed attempts or present this as a statistical accuracy rate |

There are sixteen distinct tasks. Latest results combine applicable reruns of
changed behavior; they are **not one uninterrupted sixteen-case run**. The
private `latest-qualified-results.json` maps each result to its attempt directory
and run ID; `all-attempts-analysis.json` preserves all 59. All 59 exports passed
payload SHA-256, byte-count, event-count and complete/sealed capture checks.
Redacted payloads are marked in the export; verified hashes attest the exported
bytes, not unredacted originals.

Manual review retains an additional limitation: the latest manual summary has
the correct seven items but inaccurately calls their location “before the
appendices.” The exact heading is present; relative chapter wording remains
unreliable. A factual checklist pass is therefore not full semantic or citation
acceptance. Inventory extraction also remains model-generated, even when all
fixture matches are correct.

Final implementation `f1617acb` passed the isolated Linux full suite: **82 tested
packages, 6,172 passing tests, 216 skips; 12 packages have no tests**, plus build
and vet. Focused race checks passed. WebChat **595 tests in 93 files** and build
passed; **142** project Markdown mirror/link checks passed. The complete Go run
uses Linux because the established macOS suite has Linux-workspace fixture
constraints. Earlier container attempts exposed missing module-cache setup and
obsolete tests left in the image; the qualified run replaces the Gateway source
tree with the exact current source and uses the host module cache read-only.
No UI/native acceptance, deployment, install or push is claimed.

The cold task-aware manual summary took about 185 seconds; a later cached rerun
took about 34 seconds. These are different cache conditions, not a demonstrated
speedup. The final 16-fragment inventory runs took about 58 and 49 seconds,
with 16 and 13 selection model calls respectively. Smaller batches trade more
calls for a lower classification burden.

The DOCX maintenance task changes only paragraph 43's title to clarify that the
interview is simulated and has not occurred. The Markdown task changes ordinary
retention from 90 to 60 days while keeping the 14-day exception. The independent
checker compares all DOCX package entries after XML namespace normalization,
paragraph/run formatting and all other text; only the requested title changes.
Markdown must match the exact intended byte replacement. Original owner files
and workspace source copies have identical SHA-256 values. This is structural
preservation evidence, not rendered pagination or native Word acceptance.

## Remaining capability limits

- Summary chapter relationships and relative-position wording can still be
  invented even when exact list contents are retained. Source locators need review.
- Full source acquisition is not proof of correct semantic extraction. Relevance
  classification, scope choice and final wording can still be wrong. No independent
  model reviewer or automatic semantic pass is introduced.
- Large extraction is bounded to 24 task batches, each at most 16 fragments and
  constrained by the evidence budget. Oversized indivisible fragments/dependencies,
  too many batches, empty selections or invalid classifications retain limitations.
  This pass does not introduce a persistent cross-restart batch-reduction plan.
- Reduction applies to targeted, exhaustive and comparison reads. Large workbook
  aggregation still requires an appropriate calculation strategy; the finalizer
  never manufactures spreadsheet recalculation. Formula caches remain observations.
- PPT nontext objects, complex layouts and OCR uncertainty remain partial. Useful
  text can be returned without declaring the entire source understood.
- Summary fidelity remains model-dependent despite the task-aware handoff.
  The retained failed summary illustrates why a passing whole-source coverage
  flag cannot certify its facts. Whole-summary section limits and large-document editing limits remain. The two
  maintenance cases exercise supported small edits, not arbitrary document redesign.
- The sample set covers supported formats and representative task patterns; it is
  not all A01–A24 owner acceptance, a statistical accuracy estimate or a native UI
  qualification. UI components, transport and database schemas are unchanged.

## Running the evaluation

Use Node 26 and run `npm run setup:document-tools` first. Generate into a new
private directory with the project's Python environment:

```sh
.tools/document-python/bin/python eval/document-workflow/generate_fixtures.py /private/new-workspace --owner-files /private/test-files
```

Use an explicit config with real endpoints, an absolute workspace/storage path,
and `storage.execution_evidence.enabled=true`. The local fixture config can
allow supported edits outside the production container sandbox; the harness
approves only document-edit workflows in that isolated workspace. Never point
it at original owner files or a production state store.

```sh
SPARKCLAW_RUN_REAL_DOCUMENT_EVAL=1 \
SPARKCLAW_DOCUMENT_EVAL_CONFIG=/private/eval-config.json \
SPARKCLAW_DOCUMENT_EVAL_CASES="$PWD/eval/document-workflow/cases.json" \
SPARKCLAW_DOCUMENT_EVAL_OUTPUT=/private/new-results \
go test ./services/gateway/internal/agent -run '^TestRealDocumentWorkflowEvaluation$' -count=1 -timeout=40m -v

python3 eval/document-workflow/analyze.py --runs /private/new-results --output /private/new-analysis.json
python3 eval/document-workflow/verify_maintenance.py --workspace /private/new-workspace --owner-files /private/test-files --runs /private/new-results --output /private/new-maintenance.json
```

`SPARKCLAW_DOCUMENT_EVAL_FILTER` accepts comma-separated case IDs. Use a new
output directory for every attempt. For the pressure run, set
`runtime.workflow_stage_evidence_max_bytes=16000`; run the tail, inventory and
workbook tasks, then `variant-cases.json`. Review answer text and frozen evidence
before accepting a passing automated check.
