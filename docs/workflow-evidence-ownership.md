# Workflow Evidence Ownership And Reuse

> Language: English | [简体中文](../zh-cn/docs/workflow-evidence-ownership.md)

Status: Active migration, 2026-08-07. The first document/browser projection and
Runtime-binding slice is implemented without a new persisted schema. The wider
per-Profile source-event, typed-completion, measurement, and cleanup work below
remains a migration contract rather than a claim of completed runtime behavior.

Scope: Workflow Profiles, direct tool invocation, model decisions, outcome
adaptation, runtime evidence provisioning, finalization, and audit references in
`services/gateway/internal/agent`. This proposal refines the ownership boundary
documented in [Workflow execution](workflow-execution.md) and builds on the
implemented archival and provisioning path in
[Observation compression redesign](observation-compression-redesign.md).

## 1. Decision Summary

Evidence is a runtime-managed data lifecycle, not a default kind of Workflow
node and not a payload that each consumer recreates.

The target contract is:

1. Every source acquisition creates one immutable observation event with its own
   provenance. Runtime archives the full untrusted payload once for that event.
2. Runtime extracts, validates, and binds every fact that can be established by
   typed parsing, registry lookup, state-machine state, version comparison,
   hashing, or exact structural rules, and removes those fields from model-facing
   input and output contracts when no judgment remains.
3. Workflow retains model calls that are necessary for semantic target judgment,
   tool selection, and content generation. Each call receives only the minimum
   source projection, candidate IDs, or eligible operations needed to resolve its
   current semantic variables, never unrelated Runtime facts.
4. Runtime resolves a model-selected candidate or operation back to the source
   event, revalidates it, and binds identity, locators, scope, hashes, freshness,
   and other provable execution arguments. The model may still provide
   operation-specific semantic arguments and new content, but model prose is not
   a locator.
5. Workflow state, model context, audit, approval, and finalization reference
   the same observation event or explicitly derived assertion. Their summaries
   and slices are projections, not new evidence records.
6. A node completes against a typed predicate. Generic `CompletionEvidence`
   must not remain a catch-all for tool success, semantic judgment, effect
   verification, and final-answer readiness.
7. Reuse removes duplicate representations and unnecessary rereads of the same
   event. It never collapses two independent acquisition events merely because
   their payloads or claims are equal. Optional physical blob deduplication is a
   storage concern, not logical evidence identity.

The normative rule is: **Runtime removes and binds provable facts; Workflow
retains necessary model target judgment, tool selection, and content generation,
while each model call receives only the minimum projection required to resolve
its current semantic variables.** This boundary reduces redundant model
judgment and prompt volume while making stale or ambiguous resource binding
fail closed in Runtime.

## 2. Why The Current Vocabulary Is Overloaded

The current runtime uses "evidence" for several different things:

| Current surface | Actual responsibility |
|---|---|
| A node with `CompletionEvidence` | A broad completion rule, often meaning only that a tool outcome was accepted |
| `ToolOutcome.Refs` / `WorkflowNodeState.OutcomeRefs` | Persisted references used for state transitions and later bindings |
| An archived tool observation | The complete untrusted source payload |
| `ObservationSummary` | A small cross-step or cross-run index over that payload |
| `EvidenceRequirements` / `PROVISIONED_EVIDENCE` | A consumer-sized model projection of persisted source content |
| A profile assessment | A verdict that the observed outcome satisfies a node predicate |
| Finalization evidence | Input used to render a user-visible result |
| Audit fields | A trace of which inputs and decisions affected execution |

These layers are all necessary, but treating each as independently produced
"evidence" creates three problems:

- a Workflow gains nodes whose only purpose is to copy, reformat, or restate an
  earlier result;
- deterministic location and integrity facts are serialized through a model
  even though Runtime already has the authoritative values;
- the same payload or claim is repeated in outcome refs, observation summaries,
  provisioned prompts, decision prompts, finalization, and audit.

The design below separates acquisition events, payload storage, deterministic
facts, semantic verdicts, and consumer projections.

## 3. Normative Vocabulary

| Term | Meaning | Owner |
|---|---|---|
| **Observation event** | One acquisition attempt and the raw output returned by a tool, adapter, owner handoff, or external provider. Each event has unique provenance and remains untrusted data. | Source adapter and Runtime archival |
| **Payload blob** | Immutable archived bytes referenced by an observation event. Equal bytes may share physical storage without sharing provenance. | Artifact store |
| **Locator** | Typed identity needed to find or bind the subject again, such as a document ID and source hash, a cell address, a browser generation and control ref, or a schedule ID and version. | Runtime |
| **Fact** | A value proven by deterministic parsing or state, such as "hash matches", "entry belongs to this directory revision", or "tool call completed". | Runtime |
| **Derived assertion** | A deterministic fact or semantic verdict that adds an executable claim and records its rule/predicate version and input event IDs. | Runtime records; deterministic validator or model produces |
| **Candidate** | One runtime-validated, ID-addressable option that a semantic decision may select. | Runtime generates; model may select |
| **Semantic variable** | A value that one model call must resolve and Runtime cannot establish from current typed facts, such as a target candidate, eligible operation, goal verdict, or generated content. | Workflow/Profile declares; model resolves; Runtime validates the boundary |
| **Semantic verdict** | A bounded judgment that cannot be established structurally, such as whether rendered content satisfies the owner's natural-language goal. | Model, constrained by Runtime |
| **Projection** | A bounded view of an observation event, snapshot, or assertion for one consumer. A projection may omit content but cannot change identity or provenance. | Runtime |
| **Completion predicate** | The exact fact, effect verification, semantic verdict, or output required before a node or Workflow succeeds. | Profile declares; Runtime enforces |

An observation event is not automatically "truth". It states what was observed,
where it came from, which version was observed, and what deterministic or
semantic process derived any later assertion. An event remains distinct from a
later event even when both report the same snapshot or payload.

## 4. Ownership Boundary

### 4.1 Runtime-only decisions

The following data must never be delegated to a model when Runtime has the
typed inputs needed to decide it:

- resource identity, normalized path/URL, provider endpoint, owner/session/run,
  Workflow node, scope revision, tool call, and directory revision;
- document format, source hash, paragraph/block/cell/row/slide/shape/page
  locator, parent lineage, and output-copy path;
- browser profile, tab, hidden/visible mode, session/page generation, snapshot
  digest, control ref membership, settled state, route consistency, and
  before/after transition identity;
- schedule/task/message/remote-object ID, record version, compare-and-swap
  match, approval state, delivery receipt, and idempotency key;
- tool registration and qualifiers, schema validation, capability scope,
  Policy result, allowed risk/effect, and exact argument binding;
- success/error/timeout status, byte or item counts, source coverage,
  truncation, supported-feature gates, and integrity/preservation checks;
- freshness and staleness derived from versions, generations, timestamps, or
  hashes under an explicit profile rule;
- event identity, lineage, access control, prompt projection, artifact
  retrieval, and any optional physical blob deduplication.

These are executable facts. Asking a model to repeat them adds latency and a
new failure mode without adding information.
Runtime must remove them when building model schemas and projections and bind
them after model output from authoritative state, source events, or derived
assertions. Prompt instructions that merely ask a model to copy them correctly
do not satisfy this boundary.

### 4.2 Model-eligible decisions

A model call is justified only when the unresolved predicate is semantic and a
deterministic rule would be materially incomplete, for example:

- selecting a target from runtime-validated candidates according to the meaning
  of the owner's request;
- choosing among two or more semantically distinct eligible operations/tools;
- using context when the owner's meaning remains unclear even after structural
  filtering leaves only a small candidate set;
- judging whether current page content semantically satisfies a requested goal
  after Runtime has already validated route, generation, and transition;
- summarizing, comparing, translating, extracting meaning from, rewriting, or
  generating unstructured content;
- interpreting visual content where no supported deterministic extractor owns
  the requested semantics.

The model output contract must match the current semantic variable: target or
goal decisions return a verdict or candidate ID from the supplied bounded set;
tool selection returns an eligible entry ID; content generation returns only
the operation-specific semantic arguments and new content. Runtime rejects an
unknown, stale, out-of-scope, or structurally invalid selection and ignores or
rejects model-supplied Runtime-owned fields. Therefore, "the model returns only
an ID" applies to selection calls, not to every Workflow model call.

### 4.3 Hybrid decisions

Hybrid work follows a fixed order **for each semantic variable**; it does not
skip the rest of the Workflow:

1. Runtime generates candidates from source events and removes entries
   that fail identity, scope, schema, freshness, safety, or structural rules.
2. If exact rules and the owner request uniquely determine the current variable,
   Runtime records or binds the result and advances inside the existing
   Workflow. Only that variable's model judgment is skipped.
3. If the meaning is unclear or the target is not unique, the model receives
   opaque candidate/operation IDs and only the context needed to distinguish
   them. Candidate count alone is not the model-call condition.
4. After the variable is resolved, Workflow continues through later nodes. A
   downstream target judgment, multi-tool choice, or content-generation variable
   still receives its own model call.
5. Runtime resolves model output, revalidates the current version, and binds
   only provable locator, scope, hash, freshness, and execution arguments. Model
   semantic arguments remain auditable and constrained by schema and Policy.

For a variable that must select an existing object, zero eligible candidates
causes Runtime to block or clarify. If target absence is a valid precondition
for insertion or creation, the Profile explicitly defines how that fact
advances. The model never invents a path, hash, browser ref, remote ID, or
mutation scope that Runtime did not supply. Deterministic acquisition also
never bypasses later decision/edit nodes or creates a second executor.

## 5. Source Observation Lifecycle

```text
tool / adapter / owner handoff
  -> create one immutable observation-event identity for this acquisition
  -> archive the full untrusted payload once for this event
       -> deterministic fact + locator extraction
       -> minimum projection for the current semantic variable
            -> model returns candidate/verdict, eligible tool, or generated content
            -> Runtime validates semantic output and binds provable execution facts
       -> Workflow completion predicate
       -> Policy / approval / exact argument binding
       -> final result and audit reference the same event or assertion
```

The source-observation event must preserve at least:

- event ID and kind;
- source identity: session, run, Workflow, node, scope revision, and tool call or
  equivalent owner/runtime event;
- subject locator and source version/generation;
- the acquisition mode, request binding, coverage, and adapter/contract version
  needed to interpret the event;
- artifact reference and raw payload digest where a payload exists;
- provenance, trust classification, creation time, and explicit freshness rule;
- derivation links when a fact or semantic verdict depends on earlier records;
- coverage and omission metadata for partial observations.

Every independently executed source acquisition creates a distinct event, even
when it returns the same bytes or claim as an earlier call. A crash-safe replay
of the same persisted tool-call result may reuse that call's event; a new retry
attempt may not. This preserves observation time, retry history, credential and
scope context, and provenance.

This is a logical contract, not a commitment to a new evidence graph, Go type,
or store table. Implementation should first reuse `ResourceRef`, `ToolOutcome`,
artifact metadata, and existing typed Workflow state. New persisted structures
require the Phase 0 decision gate in section 10.

### Consumer projections

Every consumer receives a projection or typed reference to the same source
event or derived assertion:

- Workflow transition code receives typed facts and event/assertion IDs;
- argument binding receives locators, scope, hashes, versions, and generations
  from authoritative state/events; operation-specific semantic arguments and
  generated content may come from constrained model output;
- a model receives only the bounded content slice, candidate/evidence IDs,
  eligible operation descriptions, or generation context required by its
  current semantic variables; separate calls do not receive the union of
  unrelated fields;
- approval receives the exact source ID, subject version/generation, and bound
  argument digest it authorizes;
- audit receives IDs, decision codes, digests, counts, and lineage, not a second
  copy of the payload;
- finalization receives the minimum source projection needed for the answer and
  retains citations to the source IDs.

`ObservationSummary` and `PROVISIONED_EVIDENCE` therefore remain useful, but
they are transport views. Neither creates a new claim, locator, or evidence
identity.

### Consumption-time freshness

Acquisition and archival are historical facts; freshness is not. Runtime checks
the bound locator, version/generation, scope, and argument digest when creating
an approval and checks them again immediately before executing the effect after
an approval or resume. If any authoritative value changes, the prior approval
does not authorize the changed operation. Runtime blocks or reacquires the
source and requests a new approval when required.

A completed acquisition node does not need to be rewritten as incomplete after
a wait. The consumer that requires a fresh binding is responsible for
revalidation before use.

## 6. Reuse And Duplication Rules

### 6.1 Logical identity

One observation event represents one acquisition attempt:

```text
(event ID, source call or handoff, owner/session/run, Workflow node and scope)
```

Independent events are never merged based on payload digest, equal prose, equal
locators, or equal source versions. Different byte budgets, excerpts, prompt
sections, audit events, approval views, or finalization formats over one event
are projections and reuse that event's reference.

A derived fact gets its own identity only when it adds a new executable claim.
It records its input event/assertion IDs and the versioned derivation rule.
Reformatting, truncating, summarizing, or copying an existing claim does not
create another assertion. A semantic verdict remains a decision event; equal
model prose from separate decisions is not a reason to merge them.

An implementation may physically deduplicate payload blobs by a cryptographic
digest of the exact archived bytes. That optimization changes storage only: all
observation events retain their own provenance, access scope, and timestamps.

### 6.2 Payload rules

- Archive one complete tool output for one observation event. Do not archive or
  persist another large copy because another stage, model call, finalizer,
  approval view, or audit consumer needs it.
- Persist references in `OutcomeRefs`; do not embed a second large payload in
  the ref attributes.
- Generate observation summaries and model projections from the source event
  under explicit budgets. They must retain the source ID, coverage,
  and omission markers.
- Audit events reference evidence and verdict IDs. Sensitive or large source
  content is not duplicated into audit fields.
- A later consumer that needs more content reads another slice of the same
  artifact. It does not repeat the source tool call unless the source may have
  changed and the Workflow explicitly requires a fresh observation.
- If measurement shows the same unchanged source is being called again only
  because an earlier projection was too small, fix the projection or use
  `observation.read`; do not hide the redundant call through identity merging.

### 6.3 Records that must remain distinct

Reuse must not collapse:

- any two independently executed acquisition events, even from the same
  provider with equal payloads;
- observations from different source versions, hashes, page generations, or
  directory revisions;
- before and after mutation observations;
- hidden and visible browser observations;
- observations made under different provider, credential, authority, or owner
  scopes;
- two different subjects with equal payload text;
- partial and complete coverage when the complete result came from a new
  acquisition;
- a raw observation and a model-derived semantic verdict over it.

Conflicting records remain side by side with provenance. Runtime applies the
profile's freshness and authority rules; it does not erase disagreement by
merging equal-looking prose.

## 7. Locator Contracts By Domain

| Domain | Runtime-owned locator and proof | Model-visible semantic input |
|---|---|---|
| Documents | Document ID, frozen governed path, format, source hash, stable block/paragraph/cell/row/slide/shape/page locator, package feature gate, parent lineage | Candidate-local text/structure, eligible editor descriptions, or generation context required by the current call; never a free-form replacement locator |
| Browser | Profile/tab, normalized URL, hidden/visible mode, session/page generation, settled snapshot digest, control ref membership, transition before/after digests | Bounded rendered text, control labels/state, and opaque candidate refs required by the current goal/control/tool judgment; never generation/digest transcription |
| Schedules and messages | Owner-scoped record ID, version, endpoint, return route, idempotency key, CAS result, delivery receipt | Candidate labels or content only when the owner's wording leaves target ambiguity |
| External MCP and coding agents | Configured endpoint identity, credential scope snapshot, catalog revision, namespaced entry ID, remote object ID/version, mutation class | Bounded eligible operation/object descriptions and untrusted returned content |
| Search and weather | Provider, request/result ID, query binding, source URL, observation time, response/card status | Result snippets or payload facts needed for comparison, synthesis, or explanation |
| Artifacts and multipart messages | Session ownership, artifact URI/key, media kind, digest, ordered part index, governed source message | Content needed for interpretation; ordering and ownership never depend on model output |

Adding a new domain requires defining its locator and staleness rule before a
model may select or act on its resources.

## 8. Workflow Node And Completion Design

### 8.1 Valid node purposes

A node may exist to:

- acquire a new external observation;
- perform and verify an effect;
- resolve a genuine target judgment or tool choice over runtime-generated
  candidates;
- generate, rewrite, or transform content required by the selected operation;
- wait for an owner-controlled handoff or approval state;
- produce the final model answer or governed message result.

Runtime-only preparation may complete a node without a model or tool when the
node is useful as a persisted state-machine boundary, such as freezing one
document identity. It still records typed facts and references instead of an
"evidence" prose payload.

### 8.2 Invalid node purposes

Do not add a node solely to:

- rename a prior observation as evidence;
- copy an artifact into prompt text;
- ask a model to repeat or validate Runtime-owned IDs, paths, hashes, versions,
  generations, schema checks, or Policy results;
- convert a tool success status into another success status;
- create a second evidence record for finalization or audit;
- reread an unchanged source because an earlier projection was too small;
- bypass downstream decision, tool-selection, content-generation, or effect
  nodes that still have semantic variables merely because upstream acquisition
  was deterministic.

When an earlier projection is too small, expand that projection or use
`observation.read` instead of reacquiring the unchanged source.

### 8.3 Typed completion predicates

Profiles should eventually replace generic evidence completion with predicates
that state what actually completes the work, conceptually:

- `observation_available(kind, coverage)`;
- `fact_bound(kind, evidence_id)`;
- `semantic_verdict(kind, evidence_ids)`;
- `effect_verified(kind, before_id, after_id)`;
- `output_ready(kind)`.

Exact schema and enum names are deferred to implementation design. The
invariant is: every node using one of these conditions must identify its
required record, validator, coverage/freshness rule, and consumer. A successful
tool call alone is not completion unless the declared predicate says it is
sufficient.

Historical predicates such as "an observation was acquired" are monotonic.
Freshness and authorization predicates are evaluated at consumption time and
may become false while a run waits for approval or owner handoff. Typed
completion cleanup is justified only where it removes an overloaded rule or a
measured failure; it is not a reason to replace every Profile mechanically.

## 9. Application To Current Profiles

| Profile area | Target treatment |
|---|---|
| `conversation.answer` | Keep the no-tool answer path. It has no external evidence requirement and must not gain a ceremonial evidence node. |
| Internet search and weather | Each provider invocation produces one source-observation event. Runtime validates query binding, provider status, result/card identity, and freshness metadata. A model is used only for synthesis that the grounded projection cannot produce directly. |
| `document.read` | `confirm_document_target` remains deterministic. The direct reader creates one source observation with typed locators and coverage. Finalization reads a projection of that record; it does not create "finalization evidence". |
| `document.edit` | Treat the current `document_locate_evidence` work as "read document for edit": the reader and format policy produce authoritative locators, hashes, coverage, and structural candidates. Exact rules may establish a target assertion directly, but only advance into the existing `select_edit_operation`/`document_edit` nodes. One eligible editor may be selected deterministically; semantically distinct editors are model-selected. The edit model retains unresolved target semantics, operation-specific arguments, and content generation. Runtime binds only provable path/output, identity, locator, scope, hash, and freshness arguments. |
| Browser r2 | Runtime owns acquisition, settle, snapshots, generations, ref membership, route checks, transition digests, and hidden/visible distinction. Models retain goal assessment, control selection, tool selection where semantically distinct tools remain, and visual/semantic interpretation. Each assessment/action receives only the control/state projection needed for its current judgment. Presentation evidence remains distinct from hidden evidence because mode and generation differ. |
| Schedule management | Runtime owns list results, IDs, versions, due state, CAS, and mutation outcome. A model may disambiguate bounded owner-visible candidates, but it cannot invent or copy a record ID. |
| External MCP and coding-agent management | Runtime owns endpoint identity, credential scope, catalog revision, eligible namespaced tools, approval class, and remote IDs. The model may choose among bounded eligible operations or interpret returned content; returned content cannot authorize another operation. |

The migration must be profile-by-profile and pass the Phase 0 decision gate. It
must not introduce a second generic executor, a parallel evidence store, or a
global evidence graph.

## 10. Migration Plan

### Phase 0: decision gate

- Inventory every Profile node, completion rule, model call, `OutcomeRef`,
  evidence requirement, finalization read, and audit payload.
- For each model call, list the semantic variables it owns, its allowed output,
  and the source fields it requires. Calls with no unresolved variable are
  Runtime-conversion candidates; calls that still own target judgment, tool
  selection, or content generation remain and receive a narrower projection.
- Measure model calls with no unresolved semantic predicate, repeated unchanged
  source calls, duplicated persisted payload bytes, prompt projection bytes,
  stale-locator or post-approval version failures, and finalization omissions
  caused by inconsistent projections.
- Define the expected reduction and correctness assertion for the Profile before
  changing types, persistence, or plan shape.
- A Profile advances only when at least one measured problem exists. If the
  measurements are effectively zero, stop: retain the ownership rules and make
  no broad type or schema change.

### Phase 1: source references and projections

- Define the smallest source-event reference that can be represented by the
  existing state and artifact contracts.
- Make outcome, provisioning, decision, approval, audit, and finalization paths
  carry that reference consistently.
- Remove embedded payload copies from refs and audit; keep bounded projections
  generated from the source artifact. Remove redundant large `ToolCall.Result`
  persistence only after all of its consumers use the source reference.
- Establish per-domain locator and staleness validators before changing model
  behavior.

### Phase 2: move deterministic work into Runtime

- Convert deterministic target confirmation, candidate filtering, provable
  target selection, Runtime-owned argument binding, freshness, transition, and
  completion checks first. Deterministically resolving one variable skips only
  that variable's model call, never downstream Workflow nodes.
- Keep source acquisition direct where the tool and arguments are already
  frozen.
- Preserve existing model behavior behind focused tests until each Runtime
  predicate is proven equivalent or intentionally stricter.

### Phase 3: narrow remaining model contracts

- Replace free-form locator generation with candidate-ID selection for target
  choice, allow only eligible entry IDs for tool choice, and allow only
  operation-specific semantic fields for content generation.
- Supply only the bounded content required by the named semantic variable, and
  remove Runtime-bound fields and unrelated observation content from the schema
  and projection.
- Persist a semantic verdict with the predicate version, owner-request binding,
  candidate/source IDs, projection coverage, model-call provenance, and a reason
  code; do not copy its source payload or merge separate decision events.
- Remove redundant "evidence" nodes and rename surviving acquisition nodes by
  the work they perform.

### Phase 4: typed completion cleanup

- Replace catch-all `CompletionEvidence` uses with explicit completion
  predicates only in Profiles that passed the decision gate.
- Remove compatibility fields only after persisted-run resume behavior is
  defined and tested for every registered Profile revision.
- Merge the durable ownership rules into [Architecture](architecture.md) and
  [Workflow execution](workflow-execution.md), update the capability matrix,
  and remove this proposal when implementation is complete.

## 11. Acceptance Criteria

The implementation is complete only when:

- every migrated Profile records its Phase 0 problem and before/after result;
- every model call names semantic variables that Runtime cannot establish
  deterministically, its allowed output, and its minimum input projection;
- deterministic stages progress correctly with execution/finalization models
  unavailable;
- a current semantic variable uniquely resolved by exact rules performs no model
  call, while Workflow still runs any necessary downstream target judgment,
  tool selection, content generation, and effect nodes;
- semantically distinct eligible tools remain model-selected and generated
  content can enter the selected operation's schema, while Runtime-owned fields
  remain absent from that model contract;
- model output cannot introduce a locator or scope value that was absent from
  the candidate set;
- one source acquisition produces one observation event and one authoritative
  archived payload reference; Workflow state, model projections, approval,
  audit, and finalization reuse that reference;
- independent retries remain independent observation events even when payloads
  are equal; any physical payload deduplication preserves every event's
  provenance and access scope;
- two projections of one record do not count as two evidence records;
- approval binds the source ID, subject version/generation, and exact argument
  digest; stale hashes, versions, generations, directory views, remote scopes,
  or changed arguments block both at approval creation and immediately before
  effect execution after approval or resume;
- before/after, hidden/visible, different-provider, and different-version
  observations remain distinct;
- the measured redundant model calls, repeated source calls, duplicate payload
  bytes, stale-binding failures, or projection omissions decrease for the
  migrated Profile without reducing required source coverage;
- existing Policy, Approval, artifact retention, resume, backend parity, and
  user-visible capability contracts remain intact;
- focused unit, persisted-resume, default file-backend scenario, and golden
  eval coverage exists for every migrated Profile.

## 12. Non-goals And Deferred Choices

This proposal does not:

- make external, document, browser, MCP, or model content trusted;
- remove artifacts, provenance, audit, approval, or model-visible source
  content;
- require every semantic decision to become a handcrafted heuristic;
- remove necessary model target judgment, tool selection, or content generation
  merely because Runtime established the upstream evidence facts;
- merge independent observation events based on equal payloads, prose,
  locators, or source versions;
- create a global evidence graph or parallel evidence store;
- require physical blob deduplication when Phase 0 does not show meaningful
  duplicated payload cost;
- define the final Go type, store table, API projection, enum names, or persisted
  migration format;
- claim unmigrated Profiles, typed completion predicates, or persisted source
  identities as implemented before their focused migration gates pass.

Those implementation choices must preserve Runtime ownership, per-acquisition
event identity, consumer reuse, and fail-closed binding defined here.
