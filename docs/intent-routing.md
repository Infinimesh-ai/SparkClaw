# Intent Routing

> Language: English | [简体中文](../zh-cn/docs/intent-routing.md)

This document defines the current source routing contract, including the
2026-10-30 integration of [conversation files, request splitting and clarification](conversation-file-context-and-clarification-design.md).
The source now connects validated request splitting, deterministic publication,
and signed selection dispatch. The linked design records final calibration,
paired native acceptance and release status separately; source implementation
does not establish the installed application or deployed service version.

## Runtime Contract

```text
original owner text + admitted conversation/resource snapshot
  -> input guard and full-original-question capacity gate
  -> optional verified adjacent clarification selection
  -> one request split, at most one repair (no rewriting)
  -> deterministic external-send and deferred checks
      -> deferred: whole original request -> schedule validation
      -> pure delivery: bound object -> Catalog publish route
      -> business: verbatim business_span
          -> source-eligible candidates, excluding publish
              -> embedding: business_span only
              -> Fast/Tree: same business_span + bounded typed context
          -> weighted fusion -> Top-2 -> grounding
  -> policy, approval and current-state checks
  -> at most one leaf Workflow, or a terminal clarification/block
```

Textless media bypasses both split and scoring and directly constructs the Catalog
publish route. Typed schedule actions retain their existing validated path.
For natural-language business scoring, embedding receives only the exact
`business_span`, with no resource metadata or history. Tree receives the same span
and bounded typed context from the admitted snapshot. Failed splits without
external evidence use the whole original text; failed splits with external
evidence block. No canonical rewritten request is created or persisted.

The separate Fast split call sees the original request and admitted context,
without candidates or scores. It may emit only original spans and typed flags.
Fast/Tree scores candidates; neither call chooses a concrete resource, emits a
`RouteDecision`, or grants authority. Guard, risk assessment, explicit LocalMind
assignment and Workflow goal retain the full original text. Query slots and
business grounding consume the business span; object grounding consumes the
object span. The main design's section 4.5 is the consumer mapping authority.

The semantic graph remains an immutable projection of `capability.Catalog` and
Workflow-owned examples, descriptions, distinctions and eligible sources, not a
second taxonomy or vector database. Invalid calibration, invalid
registration or an unavailable required embedding index fails startup. Signed
choices with stale graph, catalog or calibration revisions are rejected. Removing
`conversation.answer#publish` from scoring does not remove the Catalog publish
operation, its Workflow, content governance or external-MCP approvals.

## Candidate And Score Model

One leaf may register multiple route variants. Candidate IDs are stable within
the graph and use `<capability>#<variant>`, for example:

```text
conversation.answer#answer
browser.automation#open
browser.page_read#read
browser.interaction#interact
browser.form_draft#draft
schedule.manage#create
schedule.manage#read
schedule.manage#edit
schedule.manage#delete
```

Variants choose a Catalog-validated operation and fact-scope template; they do
not create extra Workflows. Both semantic channels score every source-eligible
candidate and return candidate IDs and scores only:

```text
Embedding: candidate_id + embedding_score
Fast/Tree: candidate_id + tree_score

fusion_score = alpha * embedding_score
             + (1 - alpha) * tree_score
```

The embedded calibration artifact currently uses `alpha = 0.50`. Fusion
deterministically sorts the complete eligible candidate set by `fusion_score`
and retains the final Top-2. The weight belongs to
`internal/semanticrouting/default_calibration.json`; changing it requires
calibration evidence and focused routing tests. Scores rank candidates but are
not probabilities. The persisted decision confidence is produced separately
from score, margin, negative conflict, and channel state.

Fast/Tree retains one candidate-scoring JSON envelope:

```json
{
  "graph_revision": "...",
  "candidates": [
    {"candidate_id": "...", "tree_score": 0.0}
  ]
}
```

The initial scoring call and its optional single repair both force thinking off
and request the same strict dynamic JSON Schema. The schema constrains the graph
revision, candidate count and eligible IDs, score range, required fields, and
unknown fields. Runtime remains authoritative for the exact candidate set and
uniqueness, then admits scores to fusion only after validation. If an invalid
initial response and its one repair are both malformed, the Tree channel fails
after exactly two calls. This hardening targets JSON structure only:
`temperature=0.2`, score variance, calibration, seed behavior, and the Fast
profile output-token allowance are unchanged.

## Semantic Registration

Workflow-owned semantic registration follows this shape:

```go
type ClarifyLabel struct {
    ZH string `json:"zh-CN"`
    EN string `json:"en"`
}

type IntentVariant struct {
    Key             string
    Route           RouteTemplate
    EmbedTexts      []string
    TreeDescription string
    HardNegatives   []string
    SourceKinds     []app.MessageSourceKind
    ClarifyLabel    ClarifyLabel
}
```

- `EmbedTexts` are independent realistic utterances and paraphrases. Do not
  combine a synonym list into one comma-separated corpus item.
- `TreeDescription` explains the intent and how to distinguish it from sibling
  candidates. It is reasoning context, not a keyword list.
- `HardNegatives` cover locally confusing requests such as quoted intent words,
  troubleshooting statements, negation, and future-tense statements.
- `SourceKinds` limits candidates by Web, third-party, or Timer source.
- `ClarifyLabel` supplies nonempty `zh-CN` and `en` labels. Graph compilation rejects invalid UTF-8, controls and labels over 160 runes; provider-specific variants name their provider. Labels explain a candidate and never grant authority.
- The Catalog registers operation-level `OperationSourceKinds`; `SupportsSource(operation, source)` remains authoritative for deterministic publication after its scoring candidate is removed.
- Tool names, approval policy, model lanes, delivery endpoints, and Workflow
  steps do not belong in semantic registration.

Graph compilation rejects duplicate IDs or examples, unreachable candidates,
invalid operations or fact scopes, missing Workflow ownership, and unresolved
multi-valued route fields.

## Grounding And Route Assembly

Routing decides meaning; deterministic grounding decides resources. URLs,
workspace paths, attachments, locations, schedule IDs, endpoint IDs, and typed
toolbar values are extracted before or after semantic ranking by bounded,
candidate-neutral projectors. The model cannot invent them.

Document grounding uses one recent-document resolver. Explicit file names first
match admitted current resources, then all admitted conversation handles;
different IDs with the same name remain distinct. The latest message with files
or `files_unavailable` is the history boundary: an incomplete group cannot fall
back to an older file, and selecting a file does not itself authorize a Workflow.
Handle ID, SHA-256 and size remain in Runtime binding state, without pre-reading
bytes. Structured block locators such as `document.p[25]` are operation qualifiers,
not file paths. Otherwise, durable
`DocumentRecord` activity is the primary source; successful document tool calls
and attachment messages remain migration fallbacks for historical state.
Records expose only bounded metadata to Fast: document ID, governed path, name,
content type, format, source/source ID, and recent activity. All outputs from
the latest shared activity remain ambiguous rather than being collapsed to one.
A unique recent document can satisfy a follow-up without being attached again,
but it still has to pass workspace, regular-file, symlink, extension, and
signature preflight.

Requests such as "extract the text in this image verbatim" remain the
`document.read` candidate for the bound image. OCR availability never creates a
candidate, changes semantic scores, or selects a model lane: the frozen image
format selects `images.inspect`, which may add classified OCR evidence inside
the already selected Workflow. Channel images use this same path only when the
owner asks to inspect or transcribe them; ingestion itself does not run OCR.

PDF page-language stays inside the same two document candidates. Asking what a
page says, extracting page text, or recognizing a scan selects
`document.read#read`; exporting, deleting, rotating, or splitting pages selects
`document.edit#transform`. Negated transforms, quoted instructions, completed
actions, and troubleshooting statements do not authorize an edit. A phrase
that does not distinguish page text from a page-file export remains ambiguous
and asks for clarification. Merge remains unmatched/blocked because no current
Workflow owns ordered multi-document grounding. These distinctions are semantic
examples and hard negatives, not a keyword fallback, and did not change fusion
weights or thresholds.

Managed-browser routing keeps meaning selection separate from public target
identification. An explicit URL or current managed tab is grounded directly,
while the unchanged destination registry is checked during candidate-independent
grounding. On a registry miss, Runtime may freeze the owner's named target as
`public_named_target`; only after a browser leaf that permits public Workflow
target resolution is selected does that Workflow run one Info-backed search.
Only ordered structured `results[].url` values can become the bound target, and
Runtime applies public-HTTPS, DNS/IP, redirect, and final-URL safety checks
without adding another relevance classifier. This resource-resolution stage
does not create or rescore semantic candidates, so adding registry entries does
not increase Top-2 selection cost.

Only Agent Runtime turns one clear candidate into `RouteDecision`:

1. Resolve the candidate against the frozen graph revision.
2. Derive the full capability path from the Catalog.
3. Copy operation and fact scope from the registered variant.
4. Bind the query from the verbatim business span (whole text on allowed fallback), and resources from deterministic grounding.
5. Validate the complete route against the Catalog.
6. Resolve and persist exactly one Workflow Profile revision.

The Tree model never emits `RouteDecision`, and no routing stage selects a tool.
Tool Exposure is derived after leaf selection from the active Workflow node,
registered capability descriptors, argument bindings, and Policy.

There is no post-fusion browser keyword veto. Search, page read, open, click,
and form-draft meaning is distinguished by Profile semantics and hard negatives;
after a clear route, the selected Profile and concrete ToolHub control checks
enforce effect boundaries. Unsupported or forbidden effects block within that
route instead of being reclassified as `unmatched`.

## Decision States

| State | Meaning | Behavior |
|---|---|---|
| `clear` | Top-1 passes its calibrated score and margin gates | Run deterministic eligibility checks and dispatch at most one leaf |
| `ambiguous` | Two candidates remain plausible or required meaning is unresolved | Return `clarify`; execute neither candidate |
| `low` | No candidate has sufficient execution confidence | Produce terminal clarification: executable Top-2 options only above `clarify_floor` or with a grounded file; otherwise ask for a full description |
| infrastructure failure | Required channels, graph, or calibration are unavailable | Return `blocked`; do not describe it as user ambiguity |

Mutation and external-delivery routes use stricter score and margin thresholds.
A missing resource after a semantically clear result is `clarify` or `blocked`,
not `unmatched`.

`reason_code` records which deterministic decision rule produced the terminal
state. It is for audit, evaluation, metrics, and stable UI handling; it is not
a third intent signal and must not alter ranking.

Final Top-2 evidence is persisted with graph/calibration revisions, model
fingerprints, channel state, per-channel scores, fusion scores, confidence,
margin, verdict, and reason code. Top-2 supports clarification and diagnostics;
it never authorizes two Workflows.

Filter only the fused Top-2 against current source, policy and degraded state; do not backfill lower-ranked choices. Desktop and host WebChat save signed questions and accept a verified adjacent selection as a new request. File choices resolve a resource and still require ordinary scoring. Third-party sources use plain restatement prompts, external MCP returns its existing clarify result, and timers terminate without interaction. Record binding, expiry and atomic single-use consumption follow sections 5.4–5.6 of the main design.

## Scheduling And Delivery Semantics

Scheduling classifies future message publication, not the future payload's
business capability. For example, `半小时后查一下上海天气` selects
`schedule.manage#create`; at due time the stored weather request re-enters this
same router. A Timer payload containing plain `吃饭` can therefore select
`conversation.answer` instead of becoming a second reminder operation.

Delivery selection is independent from business intent. An explicit third-party
destination is projected and resolved through Message Control; it does not
change semantic candidate scores. The selected Workflow always returns one
channel-neutral `WorkflowResult`, which then enters the Delivery Gateway.
For example, `发送文件` with a uniquely bound attachment is pure delivery and
constructs the Catalog publish route without scoring. The target changes only
delivery: external pure-media results contain governed media parts and have no
source-WebChat assistant projection. A question such as `为什么文件发送失败`
remains a business question, never a resend command. The split names the
channel and recipient, and Message Control must resolve a unique registered
endpoint. A text-requested external send then waits for owner approval of the
exact result and endpoint before delivery; returning to the current
conversation needs no approval
([conversation file context design](conversation-file-context-and-clarification-design.md#46-external-send-approval)).

Deferred requests retain the whole text, must ground to supported schedule
creation, and cannot execute immediately. Due-time work is split again without
recursively creating the same schedule. Historical local-file handles are not
implicitly persisted as schedule inputs; see [Desktop scheduling](desktop-scheduling.md).

Typed WebChat schedule edit/delete actions and persisted resumes already carry
validated route identity. They bypass natural-language classification but still
pass Catalog validation, owner checks, fresh target lookup, and Workflow
execution.

An attachment-only Web message is also typed input rather than natural
language. When it has no owner text and every part is image, audio, or file,
Runtime constructs the Catalog `conversation.answer` publish route directly, without a semantic candidate.
The Catalog permits an empty query only for that `publish` operation; ordinary
conversation `answer` still requires the untouched owner query.

## Failure And Degradation

- Split and its one repair share `split_timeout_ms` (15000 ms initially); embedding and Tree then execute independently within the routing deadline. All are bounded by the execution deadline.
- One failed semantic channel may serve read-only routes under stricter degraded
  thresholds; mutation or external effects fail closed.
- Both semantic channels failing, an index mismatch, or an unknown candidate
  blocks routing.
- `clarify`, `blocked`, and `unmatched` are terminal. They do not fall through to
  keyword routing or a generic fallback loop. A signed choice is a new request with fresh checks, not a resumed old execution.
- Pure delivery does not depend on Tree, but still requires readable resources, source eligibility, governance, delivery health and permission. Unknown upload, claim or delivery outcomes never authorize a new ID or replay.

## Extending Routing

1. Add or change the leaf and route contract in `internal/capability`.
2. Register exactly one versioned Workflow Profile for the leaf.
3. Add realistic multilingual examples, Tree boundaries, hard negatives, and
   source eligibility to that Profile.
4. Add deterministic grounding for any new resource kind.
5. Add direct, paraphrased, sibling-confusion, negation, quotation,
   troubleshooting, compound, source-specific, and out-of-domain cases.
6. Rebuild calibration evidence before changing weights or thresholds.
7. Update [Workflow capabilities](workflow-capabilities.md) and
   [Architecture](architecture.md) when the user-visible boundary changes.

The implementation lives in `internal/semanticrouting`,
`internal/agent/intent_router.go`, Workflow Profile registration, and the
Catalog. Focused tests cover graph validation, score fusion, Top-2 decisions,
source eligibility, schedule paraphrases, and one-leaf dispatch.
