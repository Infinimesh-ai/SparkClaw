# Model Input And Output Capacity Contract Design

> Language: English | [简体中文](../zh-cn/docs/model-capacity-contract-design.md)

Status: Implemented 2026-08-31 after acceptance and a minimum-architecture
review.

This design makes the selected deployment profile the executable source for
model capacity, rejects owner questions that mandatory pre-routing models
cannot parse, gives a small number of output capability classes explicit
budgets, and applies one final admission check to every rendered model request.

Bounded historical acquisition is owned by the
[context history retrieval design](context-history-query-design.md). Semantic
section selection and degradation remain owned by Agent ContextBuilder. The
[Tree same-session context design](tree-session-context-parity-design.md) uses
the same boundaries without changing the fixed history selection counts.

## 1. Accepted Decisions

| Concern | Accepted decision |
|---|---|
| Physical capacity | Each selected physical model declares one required positive `context_tokens` value |
| Output planning | Use a small set of profile-specific output capability classes, not one global limit and not one independently tuned limit per invocation |
| Operation ownership | Every typed model operation maps explicitly in code to one output class and an allowed logical lane set |
| Effective input | `context_tokens - output_class_budget`; no duplicate profile input ceiling or profile-wide output ceiling |
| Invalid configuration | Missing, zero, negative, unknown, incomplete, or illegally related capacity fails selected-profile loading |
| Capacity fallback | Never repair capacity with Go defaults, legacy constants, environment defaults, provider defaults, or another class |
| Owner question | Check the unchanged text independently against Guard and Embedding; reject before history, routing, Workflow, or tools when either cannot admit it |
| Semantic compression | Agent decides `full -> compact -> drop`; Router never truncates or rewrites prompt content |
| Final boundary | Router counts the complete rendered request and rejects it before provider dispatch when it cannot fit |
| Output completion | Preserve `finish_reason`; `length` is incomplete and cannot complete a run |
| Profile validation | Runtime validates only the selected executable profile; CI validates the complete catalog and promotion evidence |
| Fast/Deep failure | Do not perform an implicit cross-lane retry; any same-lane transport retry is explicit and limited to retryable failures |
| Memory | Memory repository records remain outside all Agent model context |

## 2. Scope And Non-Goals

This design applies to Fast and Deep chat, Tree scoring and repair, Workflow
model steps, operation selection, conversation and final answers, direct chat,
Guard, Embedding, and Fast image-understanding requests. A model-backed adapter
outside Model Router, such as OCR, may consume the same selected capacity
catalog while retaining its own transport and media limits.

This design does not:

- dynamically increase the 8-message, 6-tool, 4-episode, or 3-image selection;
- chunk, summarize, rewrite, or truncate an oversized owner question;
- create a separate budget for every Workflow, step number, or repair attempt;
- make Runtime read benchmark or evaluation result files;
- add a capacity ticket, persisted admission record, global cache, profile hot
  reload, or second model router;
- make `observation.read`, episode generation, or cross-lane failover part of
  capacity correctness;
- activate Memory or place it in Tree, Workflow, or final-answer context.

Large governed documents remain tool/evidence inputs rather than owner-question
text. Their existing byte, artifact, authorization, and projection limits stay
in force.

## 3. Minimal Capacity Model

Capacity separates physical model facts from logical model work:

```text
physical model
  context_tokens
  endpoint/model identity

logical lane
  physical model reference
  output_budgets[class]

typed operation
  allowed logical lanes
  output capability class
```

The logical `deep` lane may explicitly reference the same physical model as
`fast`. That alias shares physical context and endpoint identity, but it still
declares the output classes required by operations routed through `deep`.
Aliasing is configuration, not a fallback.

For a generating call:

```text
context_tokens > 0
output_budget = selected_lane.output_budgets[operation.output_class]
output_budget > 0
output_budget < context_tokens
input_budget = context_tokens - output_budget
actual_input_tokens <= input_budget
```

There is no required `max_input_tokens`: a second application ceiling would
prevent a changed physical window from automatically changing the admissible
input. There is no profile-wide `max_output_tokens`: the output-class budgets
already own that fact. If a promoted provider later has a separately proven
completion hard limit, it may be added as a physical provider fact and every
class must fit it; it is not introduced speculatively.

Changing Fast from one physical window to another therefore changes one
`context_tokens` declaration. Fixed context selection does not expand, but the
larger input budget allows ContextBuilder to retain more full variants before
degradation.

## 4. Output Capability Classes

The initial design uses a small stable classification based on response shape
and current model ability:

| Class | Representative work |
|---|---|
| `guard` | Compact Guard classification |
| `compact_structured` | Tree candidate scores and bounded selection JSON |
| `workflow_structured` | Workflow action/final envelopes and generated tool arguments |
| `answer` | Conversation, direct-chat, and completed-workflow user answers |
| `vision_structured` | Fast image inspection and document-image semantic extraction |
| `ocr_document` | OCR adapter output when that adapter consumes the shared catalog |

The exact positive values are profile-specific conservative planning limits.
Evaluation exercises representative worst cases for each class and confirms
that the chosen ceiling supports the current model capability. It does not
search for a unique minimum for every operation.

The typed operation registry conceptually contains:

```go
type OperationSpec struct {
    Operation    ModelOperation
    OutputClass  OutputBudgetClass
    AllowedLanes []ModelLane
    Generates    bool
}
```

An operation must name one class; callers cannot provide a numeric output
budget. Initial and repair calls reuse a class when they have the same response
shape. Attempt number and audit operation remain separate metadata. A class is
split only after evaluation shows that one response family has materially
different output needs.

Adding an operation that fits an existing class requires a registry mapping and
coverage tests, not a new profile field. Adding a genuinely new response family
requires a new class, representative evaluation, and a positive value in every
selected lane that can execute it.

## 5. Selected Profile As Executable Input

`configs/model.profiles.json` becomes the only executable source for physical
model context and logical-lane output-class budgets. It may also own model
launch settings that describe the same physical deployment. It must not absorb
unrelated Agent limits such as observation bytes or Workflow step counts.

The loader performs these steps before Router, Agent, or model-backed adapter
construction:

1. resolve the explicitly selected executable profile;
2. resolve logical lane aliases to complete physical model records;
3. validate positive physical context values;
4. validate the exact output classes required by the binary for each selected
   lane and their relation to that lane's physical context;
5. validate every typed operation's lane and class mapping;
6. freeze one immutable in-process capacity catalog.

Loading rejects at least:

- a missing selected profile, physical model, lane, class, or operation mapping;
- zero, negative, non-integral, overflowing, or malformed capacity values;
- an output class budget greater than or equal to physical context;
- an unknown class, unknown operation, illegal lane mapping, alias cycle, or
  alias with no physical target;
- a selectable mock or external profile without explicit capacity;
- legacy capacity fields in Go defaults, application JSON, Compose defaults,
  or environment overrides after migration.

Runtime validates only the selected profile. CI validates every executable
profile in the catalog and verifies that promoted class values have accepted
evaluation evidence. A reusable external template that lacks concrete capacity
must be non-executable and cannot be selected.

Endpoint URLs, credentials, and the selected profile ID may still come from
deployment configuration. They cannot override `context_tokens` or output
budgets. Local model launch derives `--max-model-len` from the same selected
physical model record. Provider metadata and `/tokenize` verify declarations;
they never supply missing runtime defaults or rewrite the catalog.

## 6. Token Counting And Final Admission

The old four-bytes-per-token estimate is not an admission authority. One
model-aware counting boundary beside Model Router counts the same roles,
content, response schema, chat-template options, and model identity used by the
request.

The minimum implementation is:

1. use a locally tested conservative upper bound when it proves the request
   fits;
2. when that bound would reject a potentially valid request, use the selected
   endpoint's bounded `/tokenize` capability or an exact local tokenizer;
3. if exact counting is needed but unavailable, fail closed rather than use the
   old estimator.

This does not require a persisted token decision or request ticket. Agent may
use the same counter while choosing semantic variants. Router always repeats
the final check after every call option is known; configuration lookup and
budget arithmetic are immutable and cheap, while actual input must be counted
for each distinct prompt.

Embedding admission checks each input sequence against its physical window;
batch token totals are not treated as one sequence length. Multimodal adapters
must supply a model-specific conservative image-token contribution after their
existing image preprocessing. Raw Base64 request bytes are not image tokens.

Router returns a typed `model_input_too_long` error with safe measurements. It
does not remove messages, cut strings, switch lanes, or retry with a larger
class.

## 7. Owner-Question Admission

After Message Plane normalization and persistence of the inbound message/run
boundary, but before history acquisition, Guard, Embedding, Tree, Workflow,
resume execution, or tools, Runtime checks the unchanged owner-authored text.

Guard and Embedding use different tokenizers, so their token counts are never
compared or combined. Runtime proves both independently:

```text
embedding_count(owner_question) <= embedding_context_tokens

guard_count(guard_system_prompt, owner_question)
  + output_budgets[guard]
  <= guard_context_tokens
```

Tree is protected later by whole-request admission because its graph, schema,
resources, and history are not part of the early question check.

An oversized question produces `owner_question_too_long`, makes zero history,
Guard, Embedding, Tree, Workflow-model, or tool calls, and persists one stable
safe result:

```text
问题过长，当前无法解析。请缩短问题后重试。
The question is too long to parse. Shorten it and try again.
```

No tokenizer, endpoint, count, or internal limit appears in the public result.
Audit may record safe counts and profile identities, never the question body.
Attachment-only input is not rejected merely because textual owner content is
empty.

## 8. Agent, Router, And Provider Responsibilities

The final responsibility chain is:

```text
Agent / ContextBuilder
  owns semantic sections and full/compact/drop variants
        |
        v
Model Router
  resolves lane + operation class
  reserves the class output budget
  counts the complete rendered request
  rejects overflow without modifying content
        |
        v
Provider adapter
  sends the explicit output budget
  enforces bounded transport and physical-server compatibility
```

ContextBuilder must preserve the owner question unchanged. Structured schema,
resource, and evidence sections use valid predeclared variants rather than
arbitrary character truncation. Fixed content that cannot fit after optional
semantic degradation fails before dispatch.

The Router API receives a typed operation and transport options. Numeric output
budgets are absent from caller options. Direct chat, image chat, stream calls,
Guard, and Embedding use the same final boundary rather than bypass entry
points.

Implicit Fast-to-Deep fallback is removed. It changes lane semantics, capacity,
latency, and output policy and currently retries non-retryable errors. A provider
adapter may perform a bounded same-lane retry only for an explicitly classified
transient transport failure.

## 9. Completion And Response Bounds

`ChatResult` preserves the provider finish reason in streaming and non-streaming
paths. At minimum:

- `stop` is a completed response;
- `length` is incomplete and cannot complete, persist, or deliver a run as
  successful;
- empty or unknown reasons are handled explicitly by the provider contract;
- reasoning-only or empty assistant output remains an error.

A structured call may use its existing one-repair contract, but it uses the
same explicitly mapped output class unless the response contract has been split
by evidence. It never borrows a larger class after `length`.

Router also bounds response bytes and cumulative stream bytes as a transport
safety guard. That ceiling is not another semantic task budget and does not
belong in the model-capacity profile.

## 10. Implementation Record

### Implemented: profile and registry

- Inventory production model calls and group them by stable response contract.
- Add typed operations, output classes, allowed lanes, and registry coverage
  tests.
- Replace the mixed profile schema with physical models, logical lane mapping,
  and output-class budgets.
- Make Gateway and model-service launch consume the selected profile, then
  remove duplicate capacity defaults and overrides.

### Implemented: counting and owner gate

- Add adversarial English, Chinese, code, UUID, JSON, hash, Base64-like, and
  high-entropy counting fixtures.
- Introduce conservative and exact counting paths.
- Add the independent Guard and Embedding owner-question checks before history
  or execution work.

### Implemented: Router boundary and completion

- Require a typed operation at every model call site.
- Resolve and send the selected lane's class budget.
- Apply final admission to chat, stream, image, Guard, and Embedding paths.
- Preserve finish reasons and bound transport response bytes.
- Remove implicit cross-lane fallback and caller-selected numeric budgets.

### Implemented: Agent integration

- Replace Workflow fallback arithmetic and the four-bytes estimator.
- Make owner text fixed and separate it from degradable resource context.
- Integrate Tree whole-prompt admission after bounded history acquisition.

## 11. Verification And Acceptance

Implementation is accepted only when:

- changing only a selected physical model's `context_tokens` changes the
  computed input budget without changing fixed history selection counts;
- every production model call has a typed operation, allowed lane, and explicit
  output class;
- one class can serve multiple operations without caller-supplied numbers;
- no capacity path uses a default, old constant, provider omission, another
  class, or another lane to repair invalid configuration;
- invalid selected-profile capacity fails before Router construction;
- CI validates all executable profiles and their class-promotion evidence;
- owner-question checks are independent across Guard and Embedding tokenizers;
- an oversized owner question causes no history or execution calls and is never
  truncated;
- every Router entry point rejects a rendered request that cannot fit before
  HTTP dispatch;
- `finish_reason=length` cannot produce a successful persisted or delivered
  answer;
- fixed structured sections remain valid after semantic degradation;
- existing Policy, Approval, external-MCP isolation, Workflow evidence, and
  claim-coverage tests remain green.

Run focused config, Model Router, Agent, and deployment tests; the complete
Gateway build/test/vet gates; routing/model golden evals; default File and
configured PostgreSQL coverage where available; Compose/profile validation;
WebChat tests for terminal incomplete presentation if required; and bilingual
documentation checks.

## 12. Ownership Boundaries

- `internal/config`: selected-profile loading and immutable capacity catalog;
- `internal/modelrouter`: typed operation/class registry, counting, final
  admission, finish reason, transport response bounds, and same-lane retry
  classification;
- `internal/agent`: owner-question failure projection, semantic section
  degradation, and Tree/Workflow integration;
- model-backed adapters: their media-specific token contribution and transport
  bounds while consuming shared capacity facts;
- `configs/model.profiles.json`: physical model context, logical aliases,
  output-class budgets, and related model launch facts;
- deployment scripts: selected-profile consumption and provider declaration
  verification;
- CI/evaluation: whole-catalog validation and representative class promotion
  evidence.

This design deliberately does not add a second capacity registry, a dynamic
budget planner, or a task-by-task tuning system.
