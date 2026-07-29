# Intent Routing

> Language: English | [简体中文](../zh-cn/docs/intent-routing.md)

This document is the current contract for natural-language intent recognition.
It replaces the former Fast-only classifier, keyword recognizers, routing
refactor plans, semantic-fusion proposal, profile snapshot, and separate tool
exposure note.

## Runtime Contract

Natural-language requests are classified by two independent channels over one
registered semantic graph:

```text
current owner-authored question
  -> candidate-neutral resource resolution
  -> source-eligible semantic candidates
      -> embedding similarity scores from the question only
      -> Fast model reasoning over the question + bounded typed context
  -> weighted fusion
  -> final Top-2 decision
  -> deterministic route assembly
  -> exactly one leaf Workflow, clarify, blocked, or unmatched
```

The embedding channel receives exactly the current owner-authored question. It
receives no history, resource marker, attachment metadata, document record, or
other context. Fast/Tree receives the same question plus bounded current-turn
resource metadata and recent Agent context as typed data so it can reason over
follow-up references, omitted subjects or targets, corrections, and ambiguity.
This asymmetric contract applies to all natural-language intent recognition.

There is no model-based request normalization call, canonical request, or
persisted normalization structure. Fast scores candidates only: it cannot
rewrite the question, choose a concrete resource, emit a `RouteDecision`, or
grant authority. Context may help Fast disambiguate meaning, but deterministic
grounding and Workflow bind resources after fusion.

The semantic graph is an immutable in-memory projection. It is not a vector
database and does not own a second capability taxonomy. The graph topology,
leaf contracts, operations, and Workflow references come from
`capability.Catalog`; each Workflow Profile contributes realistic embedding
examples, one Tree description, sibling distinctions, hard negatives, and
eligible source kinds.

The Gateway builds the embedding index at startup using the configured
embedding model. A graph or calibration mismatch, invalid registration, or
unavailable required embedding index fails startup instead of silently
restoring the old classifier.

## Candidate And Score Model

One leaf may register multiple route variants. Candidate IDs are stable within
the graph and use `<capability>#<variant>`, for example:

```text
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

## Semantic Registration

Workflow-owned semantic registration follows this shape:

```go
type RoutingIntentVariant struct {
    Key                 string
    Route               RoutingRouteTemplate
    EmbedTexts          []string
    TreeDescription     string
    HardNegatives       []string
    EligibleSourceKinds []app.MessageSourceKind
}
```

- `EmbedTexts` are independent realistic utterances and paraphrases. Do not
  combine a synonym list into one comma-separated corpus item.
- `TreeDescription` explains the intent and how to distinguish it from sibling
  candidates. It is reasoning context, not a keyword list.
- `HardNegatives` cover locally confusing requests such as quoted intent words,
  troubleshooting statements, negation, and future-tense statements.
- `EligibleSourceKinds` limits candidates by Web, third-party, or Timer source.
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

Document grounding uses one recent-document resolver. An explicit current path
or current governed resource is authoritative. Otherwise, durable
`DocumentRecord` activity is the primary source; successful document tool calls
and attachment messages remain migration fallbacks for historical state.
Records expose only bounded metadata to Fast: document ID, governed path, name,
content type, format, source/source ID, and recent activity. All outputs from
the latest shared activity remain ambiguous rather than being collapsed to one.
A unique recent document can satisfy a follow-up without being attached again,
but it still has to pass workspace, regular-file, symlink, extension, and
signature preflight.

Only Agent Runtime turns one clear candidate into `RouteDecision`:

1. Resolve the candidate against the frozen graph revision.
2. Derive the full capability path from the Catalog.
3. Copy operation and fact scope from the registered variant.
4. Bind the query from the untouched owner request and resources from grounding.
5. Validate the complete route against the Catalog.
6. Resolve and persist exactly one Workflow Profile revision.

The Tree model never emits `RouteDecision`, and no routing stage selects a tool.
Tool Exposure is derived after leaf selection from the active Workflow node,
registered capability descriptors, argument bindings, and Policy.

## Decision States

| State | Meaning | Behavior |
|---|---|---|
| `clear` | Top-1 passes its calibrated score and margin gates | Run deterministic eligibility checks and dispatch at most one leaf |
| `ambiguous` | Two candidates remain plausible or required meaning is unresolved | Return `clarify`; execute neither candidate |
| `low` | No candidate has supported semantic coverage | Return `unmatched` |
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

Typed WebChat schedule edit/delete actions and persisted resumes already carry
validated route identity. They bypass natural-language classification but still
pass Catalog validation, owner checks, fresh target lookup, and Workflow
execution.

## Failure And Degradation

- Embedding and Tree execute independently within the total routing deadline.
- One failed semantic channel may serve read-only routes under stricter degraded
  thresholds; mutation or external effects fail closed.
- Both semantic channels failing, an index mismatch, or an unknown candidate
  blocks routing.
- `clarify`, `blocked`, and `unmatched` are terminal. They do not fall through to
  keyword routing or a generic fallback loop.

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
