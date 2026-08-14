# Info Aggregated Result Consumption Design

> Language: English | [简体中文](../zh-cn/docs/info-aggregate-result-consumption-design.md)
>
> Status: Implemented

## Purpose

Infinimesh Info returns one already-aggregated `AgentContextResponse`: it fans
in eligible search backends, applies Info-owned policy and final source
ordering, synthesizes an `answer_context`, and binds facts and conflict
viewpoints to response-local source IDs. `agent_context` is the requested
response mode, not a pass-through payload from one search provider. SparkClaw
must consume the returned aggregate as the semantic authority and must not run
a second relevance ranker or synthesizer over the same facts.

This design replaces the current lossy Info result handling with a typed,
validated, bounded, and deterministically rendered aggregate while preserving
the existing untrusted-evidence boundary.

## Scope

This design covers successful `POST /v1/info/query` responses used by:

- the `browser.internet_search` Workflow;
- the Info-backed public-target identification stage shared by managed browser
  Workflows;
- ToolHub persistence, observation projection, trace, and audit metadata for
  `web.search`.

It does not change Info retrieval, provider fan-in, ranking, verification, or
synthesis. It does not add page opening to Internet search, change browser URL
safety checks, replace the typed weather path, add deep research, or execute
Info-supplied follow-up actions.

## Current Behavior And Gaps

The current path is:

```text
Info agent_context
  -> infinimeshinfo.QueryResponse
  -> websearch.Result
  -> ToolHub web.search output
  -> query-term-ranked InfoEvidenceProjection v3
  -> groundedWebSearchSummary
  -> user answer
```

That path loses or changes upstream aggregate semantics in several places:

1. `infinimeshinfo.AnswerContext` decodes only `summary`, `key_facts`, and a
   legacy citations field. It silently drops upstream `conflicts`, `freshness`,
   `uncertainty`, and `recommended_next_actions`.
2. `websearch.Result.Answer` duplicates `Summary`. This obscures whether a
   value is an aggregate synopsis, a provider status sentence, or a fallback
   assembled from snippets.
3. `ProjectInfoEvidence` lexically ranks facts, sources, and snippets against
   the query, keeps at most four facts and three sources, and may select
   substrings from individual claims. That is a second local relevance pass
   over an already ranked and synthesized aggregate.
4. Projection status is `complete` only when summary, facts, and source
   snippets are all present. Those are not all mandatory answer components;
   absence and projection loss are currently conflated.
5. `groundedWebSearchSummary` normally returns `answer` directly. It falls
   back to facts only when the summary matches one hard-coded English display
   string pattern, and then renders at most three claims and three URLs.
6. Conflicts, uncertainty, freshness risk, and exact claim-to-source edges do
   not reach the user. A fluent answer can therefore look more certain or
   current than the upstream aggregate supports.
7. Browser public-target identification correctly consumes ordered structured
   result URLs. Info's final source order must not be coupled to an
   answer-oriented projection or changed by local lexical ranking.

## Design Principles

1. **Info owns aggregation semantics.** SparkClaw preserves upstream fact,
   conflict, source, and ordering decisions.
2. **SparkClaw owns the trust boundary.** It validates transport identity,
   references, public URLs, bounds, and output safety before use.
3. **Validation is not re-aggregation.** Invalid references may be rejected or
   marked omitted; valid claims are not rewritten, merged, deduplicated by
   meaning, or rescored.
4. **Semantic units stay intact.** Facts and conflict viewpoints are admitted
   or omitted as whole units. They are not cut into query-matching substrings.
5. **Coverage and quality are separate.** SparkClaw projection fidelity is
   distinct from Info-supplied uncertainty or staleness risk and says nothing
   about coverage of Info's internal providers.
6. **Citations remain edges, not a URL bag.** User-visible citations derive
   from each fact or viewpoint's source IDs. Linkability is a separate property
   of the referenced source.
7. **Upstream advice is data, never control.** `recommended_next_actions` may
   be retained in the raw result but cannot become a Workflow transition,
   `next_step_hint`, tool call, or model instruction.
8. **One result, consumer-specific views.** Answer rendering and browser target
   selection consume separate read-only projections of the same persisted
   result.

## Reconciled Info Snapshot

This design is reconciled to Infinimesh Info `b70c08c` (2026-08-14). The
relevant implementation has these observable semantics:

- the synchronous query assembly may fan out to several enabled, sync-capable
  providers and continues with successful results when only some providers
  fail;
- Info merges those results, applies its own source policy and `WeightedRouter`
  score, stable-sorts the merged set, and assigns `src_NNN` IDs from that final
  sequence;
- the rule pipeline creates summary, facts, freshness, uncertainty, and
  conflicts; an optional LLM enhancer may replace summary/facts/conflicts only
  after its source IDs pass Info's citation guard;
- `sources[].authority_score` is the Info routing score exposed on the final
  source record. It is not a SparkClaw score and is not guaranteed to preserve
  any backend provider's native score;
- backend provider name, provider coverage, provider failure, and a separate
  aggregate quality score are not present in `AgentContextResponse`.

Consequently, SparkClaw's ordered input is the returned `sources[]` sequence,
not an Exa, Parallel, Doubao, Kimi, Ark, or other backend-native ordering. An
Info response with `status=ok` also does not prove that every configured
backend participated. SparkClaw must not infer provider coverage from source
count or convert its projection status into such a claim.

## Upstream Contract

SparkClaw should mirror the current Info `response_mode=agent_context` contract
instead of maintaining a reduced approximation:

```go
type AgentContextResponse struct {
    RequestID     string
    Status        string
    AnswerContext AnswerContext
    Sources       []SourceRef
    Usage         Usage
}

type AnswerContext struct {
    Summary                string
    KeyFacts               []KeyFact
    Conflicts              []Conflict
    Freshness              FreshnessStatus
    RecommendedNextActions []string
    Uncertainty            []string
}

type KeyFact struct {
    Claim      string
    Confidence string
    Sources    []string
}

type Conflict struct {
    Topic      string
    Viewpoints []Viewpoint
}

type Viewpoint struct {
    Claim   string
    Sources []string
}

type FreshnessStatus struct {
    Status           string
    LatestSourceDate *string
    StalenessRisk    string
}

type SourceRef struct {
    ID             string
    Title          string
    URL            string
    SourceType     string
    PublishedAt    *string
    RetrievedAt    string
    AuthorityScore float64
    Snippets       []string
}

type Usage struct {
    CostCredits int
    TokenType   string
    CacheHit    bool
}
```

The current wire presence rules are:

| Fields | Wire rule |
|---|---|
| `request_id`, `status`, `answer_context`, `sources`, `usage` | Required response envelope; successful query status is exactly `ok`. |
| `summary`, `key_facts`, `freshness` | Non-optional members of `answer_context`; empty collections are normalized without inventing content. |
| `conflicts`, `recommended_next_actions`, `uncertainty` | Optional members; omission is not projection loss. |
| `latest_source_date` | Optional freshness member. Current outputs may use RFC 3339 or date-only form. |
| source `id`, `title`, `url`, `source_type`, `retrieved_at`, `authority_score` | Declared source members. `url` is a string but may be empty on the query-path weather response. |
| source `published_at`, `snippets`; usage `cache_hit` | Optional members. |

There is no current `answer_context.citations` field. Citation edges are only
`key_facts[].sources` and `conflicts[].viewpoints[].sources`. There are also no
provider provenance, provider coverage, or aggregate quality fields to mirror.
Unknown additive JSON fields remain ignored for forward compatibility, but
every documented answer-bearing or limitation field above must be represented
and covered by contract tests.

Info's OpenAPI currently marks `answer_context` and each `sources[]` entry only
as generic objects. Until that upstream schema is expanded, SparkClaw contract
fixtures must pin the concrete wire shape from Info's domain type and API
documentation and fail focused tests when that shape drifts.

SparkClaw continues to request `citation_required=true`, public-only context,
a bounded source count, and `response_mode=agent_context`.

## Target Data Model

### Raw Info Aggregate Result

The exact decoded Info response is converted once into a versioned
ToolHub result:

```text
info_search_result_v2
  request_id
  status
  query
  provider                       # adapter identity: infinimesh-info
  retrieved_at
  took_ms
  aggregate
    summary
    facts[]
    conflicts[]
    freshness
    uncertainty[]
    recommended_next_actions[]   # retained as upstream advisory only
  sources[]                      # Info final order preserved
  usage                          # non-secret bounded metadata
  untrusted=true
```

New output has one answer source of truth under `aggregate`; it does not
produce parallel top-level `summary`, `answer`, `key_facts`, and `citations`
copies. `sources[]` remains top-level because browser target identification
needs Info's ordered structured source list independently of answer
rendering.

The full bounded result remains in `ToolCall.Result` and the normal observation
artifact path. Model-visible and user-visible projections never replace that
persisted source record.

### Validated Aggregate Directory

`websearch` constructs one immutable validated directory from the raw result.
It contains:

- the frozen query and provider request ID;
- aggregate units in Info-returned order;
- a unique source directory keyed by source ID;
- claim and viewpoint edges to source IDs;
- typed freshness and upstream uncertainty;
- validation findings and projection coverage;
- an ordered subset of linkable public source URLs for the browser consumer.

It does not contain a local relevance score.

## Validation Rules

Validation runs before either consumer projection.

### Envelope

- The frozen ToolCall query must be non-empty and exactly equal the persisted
  result query.
- Provider must be `infinimesh-info`, request ID must be non-empty, status must
  be exactly `ok`, and `untrusted` must be true.
- The response must fit the configured transport body limit and the mapped
  result must fit the ToolHub observation/artifact limits.

### Sources

- Source IDs must be non-empty and unique.
- Source identity and linkability are validated separately. A source with a
  valid ID but an empty or invalid URL may remain a non-linkable citation
  record; SparkClaw must not invent or substitute a URL.
- A rendered hyperlink requires an absolute public HTTP(S) URL. Browser target
  selection retains its stricter HTTPS, DNS, IP, and redirect validation and
  ignores non-linkable sources.
- Source order and original indexes are preserved after invalid entries are
  marked. An invalid URL is not silently replaced with another URL.
- Dates are parsed when present, accepting the current RFC 3339/RFC 3339 Nano
  and date-only outputs. Invalid dates produce typed validation findings;
  SparkClaw does not infer replacement dates.
- Authority score is Info metadata only. SparkClaw does not use it for
  reranking or describe it as a backend-native score.

### Facts And Conflicts

- Empty claims are invalid.
- Every source ID on a fact or viewpoint must resolve to the source directory.
- A citation-required fact with no edge to a valid source record is not answer
  evidence. It remains available in the raw result and is reported as an
  omitted invalid unit. A valid but non-linkable source record still satisfies
  source-ID resolution; it produces a non-hyperlinked citation label.
- A conflict is usable only when its topic is non-empty and at least two
  non-empty viewpoints retain valid source edges.
- Fact, conflict, viewpoint, and source order remain the upstream order.
- Confidence values are retained as Info metadata and never converted into
  local ranking weights.

### Freshness And Advice

- Freshness status and staleness risk are preserved as strings with the current
  known vocabulary (`current`; `low`, `medium`, `high`). Unknown non-empty
  values are preserved with a contract finding rather than guessed.
- `uncertainty[]` is retained verbatim as untrusted limitation evidence.
- `recommended_next_actions[]` is retained only in the raw aggregate. It is
  excluded from model prompts, deterministic answers, Workflow transitions,
  and control metadata.
- The current rule pipeline and LLM draft do not normally populate
  `recommended_next_actions`, but the documented optional field remains
  quarantined for forward compatibility.
- SparkClaw does not synthesize provider-degradation or provider-coverage
  warnings. It surfaces only Info's returned uncertainty plus its own
  validation and projection omissions.

## Consumer Projections

### Answer Projection

`info_aggregate_projection_v4` replaces the lexical v3 projection. It contains:

```text
schema_version=4
status=complete | partial | failed | no_results
request_id
query
summary?
facts[]
  ref
  claim
  confidence?
  source_ids[]
conflicts[]
  ref
  topic
  viewpoints[] { ref, claim, source_ids[] }
freshness
uncertainty[]
sources[] { id, title, url?, linkable, source_type, published_at, retrieved_at }
omissions[]
limitation_required
untrusted=true
```

Projection order is deterministic:

1. freshness and uncertainty metadata;
2. aggregate summary as one whole unit when it fits and at least one cited
   answer unit is usable;
3. facts in Info-returned order;
4. conflicts in Info-returned order;
5. only sources referenced by admitted units, filtered without changing their
   relative order in Info's final `sources[]` sequence.

No query terms participate in selection. Whole units are admitted until the
byte budget is exhausted. Oversized or remaining units are omitted with exact
reason and count; claim text is never excerpted into a different assertion.
Source snippets are not needed by the normal deterministic answer and are
omitted from that projection. They remain in the persisted result for audit,
debugging, and explicitly designed future consumers.

Projection status means:

- `complete`: the contract is valid and every usable upstream answer unit and
  required source edge was admitted;
- `partial`: at least one usable unit or edge was omitted because of an invalid
  reference or projection capacity;
- `failed`: identity, trust, or structural validation makes the aggregate
  unusable;
- `no_results`: the valid aggregate contains no citation-backed fact or
  conflict viewpoint. An unbound summary or an unreferenced URL does not become
  answer evidence.

Missing optional conflicts or uncertainty does not make a projection partial.
Non-empty upstream uncertainty or high staleness risk sets
`limitation_required=true` without changing a fidelity-complete projection to
partial. `complete` means that SparkClaw faithfully projected the returned
aggregate; it never means that all Info backends succeeded or that upstream
coverage was exhaustive.

### Browser Target Projection

The browser consumer does not use the answer projection. It reads `sources[]`
from the validated directory in Info's final source order, skips non-linkable
entries, and selects the first URL that passes the existing public-target
safety gate.

The persisted evidence continues to bind:

- Info request ID;
- original result index and source ref;
- canonical entry URL and normalized final URL;
- redirect chain and safety result.

Answer budget, facts, conflicts, and local display choices cannot reorder this
view.

## Deterministic User Rendering

`browser.internet_search` remains a grounded Workflow and adds no second model
finalizer after Info returns. A typed renderer consumes only
`info_aggregate_projection_v4`.

Rendering rules are:

1. Render the upstream summary as the lead only when cited facts or conflict
   viewpoints make the answer usable. There is no display-string sniffing, no
   local summary rewrite, and no citation edge is invented for the summary.
2. Render admitted facts in Info-returned order. Each fact carries citation
   markers resolved from its own source IDs.
3. Render conflicts explicitly with their independently cited viewpoints.
4. Render freshness when staleness risk is not low or when a latest source date
   materially bounds a current-information answer.
5. Render every admitted uncertainty item in a limitations section.
6. Render a compact source list keyed by the same citation markers. A valid
   non-linkable source is shown as a plain label, never as a fabricated link.
7. State projection omissions when status is partial. Never silently present a
   partial projection as complete.
8. For `no_results` or `failed`, return a typed unavailable/no-reliable-results
   response instead of exposing a provider status sentence as an answer.

Info-supplied text is normalized to plain display text. HTML is escaped or
removed, Markdown control syntax from upstream text is not executed, and
hyperlinks are created only from validated source metadata. Upstream content
can never emit a tool call or alter Workflow state.

## Observation And Audit Contract

The normal untrusted `toolResultMessage` envelope remains. Its Info structured
fields should expose only:

- result and projection schema versions;
- projection status and `limitation_required`;
- counts for facts, conflicts, sources, linkable sources, uncertainty, and
  omissions;
- request and artifact references already allowed by current policy;
- a static SparkClaw-owned instruction describing the evidence boundary.

It must not copy `recommended_next_actions` into `next_step_hint` or other
control-like fields.

Use the existing Agent-owned workflow evidence projection audit abstraction
for model-visible projection telemetry when the projection enters a model
consumer. The grounded deterministic answer path records result version,
projection digest and bytes, coverage status, omission reason codes, and source
lineage without putting raw query or answer text into audit fields.

## Persisted Compatibility

Active or resumable browser runs may already contain the current unversioned
ToolHub result shape. A read-only compatibility decoder is therefore required:

- new calls produce only `info_search_result_v2`;
- persisted legacy results may be decoded into the validated directory;
- legacy `summary` and `key_facts` map to aggregate units;
- legacy URL citations are resolved against persisted result sources;
- missing typed conflict, freshness, and uncertainty fields are recorded as
  legacy omissions, not invented;
- no new producer writes the legacy top-level `answer` duplication.

Compatibility exists at the persisted-result decoder only. It must not create
two live projection or rendering paths. Once the maximum resumable run lifetime
and supported upgrade window have elapsed, remove the decoder and its fixtures
in a separate cleanup change.

## Implementation Boundaries

| Owner | Change |
|---|---|
| `internal/infinimeshinfo` | Mirror the full upstream `AgentContextResponse` and add decode/contract tests. |
| `internal/websearch` | Normalize once, validate the aggregate/source graph, preserve order, and build bounded consumer projections without lexical ranking. |
| `internal/toolhub` | Publish the versioned result schema while keeping ordered `sources[]` available to browser target identification. |
| `internal/agent` | Project typed observations, render the deterministic answer, report limitations, and decode legacy persisted results. |
| Workflow Profile | Keep the frozen query and one `web.search` completion rule; do not add a second search, page read, or finalizer call. |
| Docs/eval | Update the active integration contract and add deterministic aggregate fixtures and user-flow cases. |

Do not create a parallel aggregate registry in Agent or ToolHub. The response
type and normalization rules have one owner in `infinimeshinfo` and
`websearch`; downstream packages consume projections.

## Implementation Record

- `infinimeshinfo` now pins the complete aggregate wire shape, required member
  presence, optional nulls, and additive-field tolerance.
- ToolHub writes only `info_search_result_v2`; one read-only decoder normalizes
  persisted legacy results before shared validation.
- Aggregate projection v4 replaced lexical ranking and substring extraction.
  It admits whole units in Info order and quarantines upstream actions.
- The grounded renderer exposes per-unit citations, conflicts, freshness,
  uncertainty, linkless sources, and projection omissions without a second
  model finalizer.
- Browser target identification consumes the independent ordered source view
  and retains original source identity plus the existing URL safety gates.
- Focused contract, projection, renderer, Workflow, and browser-order tests
  cover the implemented boundary. Current-state behavior is also recorded in
  `integrations.md`, `workflow-capabilities.md`, and `architecture.md`.

## Test Matrix

| Layer | Required cases |
|---|---|
| Info client | Full aggregate decode, required/optional/null field handling, additive unknown fields, response mismatch, body bound, current and malformed date/value forms. |
| Websearch adapter | Exact fact/conflict/Info-final source order, valid source edges, invalid/missing edges, duplicate IDs, linkable/non-linkable source separation, public URL filtering, no local scoring. |
| Projection | Complete, partial, failed, no-results, whole-unit byte admission, deterministic output, limitation propagation, no snippets or upstream actions. |
| Renderer | Summary plus cited facts, conflicts with separately cited viewpoints, freshness warning, uncertainty section, partial omission notice, safe plain text. |
| Security | HTML/Markdown injection, instruction text in every upstream field, malicious recommended action, unsafe citation URL, source-ID spoofing. |
| Compatibility | Legacy persisted result decode for file and PostgreSQL snapshots; no new legacy producer. |
| Browser | Info-final-order URL selection, non-linkable and unsafe-first result skipping, stable original index/ref, answer projection cannot reorder target candidates. |
| Workflow | Exactly one `web.search`, frozen query unchanged, typed outcome, no page read, no extra finalizer, grounded delivery through Web and connector paths. |
| Eval | Current price/news/catalog, multi-source comparison, explicit conflict, stale evidence, upstream uncertainty, zero result, oversized aggregate. |

## Acceptance Criteria

Implementation is complete when:

1. Every documented upstream aggregate field is typed and tested in
   SparkClaw.
2. No production path lexically reranks, semantically merges, or rewrites Info
   facts, conflicts, or sources.
3. Every rendered factual claim or conflict viewpoint resolves only to its own
   valid source IDs.
4. Upstream uncertainty, staleness risk, and projection omissions are visible
   whenever they require a limitation.
5. Upstream recommended actions cannot enter model instructions, Workflow
   control, or deterministic user output.
6. Browser public-target selection preserves Info's final source order, skips
   non-linkable entries, and retains all existing HTTPS/DNS/IP/redirect safety
   checks.
7. Raw results remain bounded and persisted; answer projections remain within
   the configured envelope without cutting claims into new assertions.
8. Focused Go tests, the full Gateway test/vet gate, deterministic evals, and
   bilingual docs checks pass.

## Rejected Alternatives

- **Run a second local ranker over Info facts.** Rejected because it overrides
  the upstream aggregate and can detach claims from the intended evidence set.
- **Send the complete aggregate to a SparkClaw finalizer model.** Rejected for
  the normal search path because it adds latency and another synthesis step
  that can change conflicts, uncertainty, and citations.
- **Use only the upstream summary.** Rejected because it discards structured
  support, conflict, freshness, and limitation semantics.
- **Expose upstream recommended actions as next-step hints.** Rejected because
  untrusted upstream data must not control Workflow execution.
- **Use one projection for answers and browser target selection.** Rejected
  because answer byte budgets and source admission must not alter the
  Info-ranked URL sequence used by the safety-gated browser consumer.
