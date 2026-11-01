# Conversation File Context, Request Delivery Split and Low-Confidence Clarification Design

> Language: English | [简体中文](../zh-cn/docs/conversation-file-context-and-clarification-design.md)
>
> Date: 2026-10-30. Status: P1–P3 are integrated in source, with isolated acceptance below; P4 paired release and joint qualification remain incomplete. Scope: conversation file references, business/delivery splitting and low-confidence clarification. No request completion or rewriting.

## Implementation and acceptance record (2026-10-30)

The workspace is based on `022ed7706380f63cad27b577aa92cb31d80b4633`; that commit excludes this turn's uncommitted changes. The normal dispatcher now uses the implementation, with shared split, delivery-only and signed-selection rules for host WebChat and desktop. The [acceptance manifest](evidence/conversation-file-context-2026-10-30.json) summarizes evidence. Section 1 preserves the original failure.

### Completed implementation

- Execution envelope schema 2 carries stable message IDs, file handles and `files_unavailable`. An incomplete group caused by trimming or loss cannot silently fall back to an older file or treat a surviving same-name peer as unique; explicit reattachment creates a new identity.
- Post-admission history uploads bind request, identity, size and digest. Shared read waits, cancellation, deadlines, revocation, late terminal uploads and unknown outcomes have explicit lifecycles. Input directories are read-only; pure delivery and edits create independent versioned root outputs, reserve task/owner budgets and verify copied bytes, cleaning up on failure.
- The independent split precedes scoring, with strict verbatim-span validation, at most one repair and a shared deadline. Only business text enters embedding/Tree. External-send rules and the unique endpoint are checked before work and again at final dispatch. Pure delivery bypasses scoring; deferred requests retain the entire text without borrowing historical local handles.
- `conversation.answer#publish` no longer participates in the semantic graph; Catalog retains publish and source restrictions. Final graph: `2026-10-30.v28.804ad56ee3ef4ceb`; Catalog: `2026-10-30.v28`; calibration: `intent-fusion-2026-10-30.v4`, `clarify_floor=0.46`. Existing execution thresholds are unchanged. Options use only the original Top2; filtering cannot backfill from third place.
- Clarification record 2 binds the original message, context, displayed question, candidate/file/endpoint, versions and 24-hour expiry. A numbered answer continues only the immediately preceding valid question and rechecks the original request and authority. Durable single-use claims prevent replay after concurrency, restart or unknown outcomes. Third-party, MCP and timer sources emit no numbered record.
- ClientStore schema 15, execution control schema 4, FileStore message container schema 2 and PostgreSQL `0020_conversation_context.sql` are implemented. Memory/file/PostgreSQL preserve records and unavailable-file flags, existing messages/drafts/attachments/unknown outcomes; old schema 1 clarification is readable but cannot continue. Approval clients validate the existing HTTP and ISCP receipt shapes separately, including the original approval and digest binding.

### Acceptance results

| Check | Result and boundary |
|---|---|
| Full Agent suite | 801 passed, 6 environment-gated skips; the additional incomplete-current-group regression and related race checks passed. |
| Desktop/WebChat | Desktop: 609 passed, 1 existing platform skip; WebChat: 561 passed and production UI build passed. Two short-deadline cases failed under concurrent load; the full serial rerun passed, with initial logs preserved. |
| Linux Gateway | Final: 5849 passed, 206 environment-gated skips, zero failures; 78 packages passed, build/vet passed. Logs retain 10 initial failing nodes from old split fixtures, call counts and input paths. After freezing the snapshot, only qualification assertions changed: actual pure-delivery model-call names and native rendered result controls, both retested; product sources match the qualified snapshot. |
| Store/migrations | Isolated full PostgreSQL: 1825 passed, 1 existing skip. Default file and memory also verify persistence, restart, migration rollback and old-format protection. Host HTTP same-name selection, FileStore/service restart and original-request replay pass. |
| Real-model split | Six cases repeated three times: final 18/18 passed. Initial overlapping-span/object-type failures and later deferred-span truncation failures remain recorded; stricter prompt and repair feedback were then retested. |
| Real-model calibration | Final graph: 23/23 Top1 correct; default fusion parameters and alpha sweeps passed. This does not establish all natural-language coverage. |
| Native desktop | Scoped mock and real-model modes each passed 7 groups/14 executions: upload/read, second-turn summary with return delivery, history delivery, both same-name choices, missing files and reattachment. Originals retain their hashes; zero wrong files, duplicate executions or direct business HTTP. Native build fingerprints match current source; see the manifest. |
| Original UI | Production main/UI run with a temporary identity and independent Linux Gateway/PostgreSQL/ISCP Relay. Sidebar, new chat, Files, attachments, history reopening and existing interaction regressions are checked; screenshots stay in private fixture evidence. Window occlusion paused refresh in an early run; background throttling was disabled only in the fixture, without product layout changes. |

### Capability and release boundaries

The owner explicitly deferred document modification to a later detailed design and asked to leave its issues aside. Document edit capability, generated replacement text and editor limitations are excluded from this turn's implementation and final acceptance; the dedicated edit prevalidation was withdrawn. Diagnostic failures remain future design inputs, not claimed fixes. Final native acceptance uses “概括一下发给我” (summarize it and return it here) to verify historical reads and business/delivery splitting, plus pure file delivery, same-name clarification and file recovery. Earlier editing runs are process evidence, not the final scoped acceptance.

No packaging, installation, deployment or push occurred. The old installed client cannot be mixed with the new envelope. P4 still requires paired release, existing-user-data cutover and hosted fault qualification. InfiniCenter **0051 remains proposed**, without independent ISCP review or completed joint acceptance. Testing against independent Relay source is not counterpart acceptance; no counterpart/frozen contract changes or substituted consensus are made.

The earlier 596 desktop passes/1 skip, schema 14, unwired split and macOS `/dev/shm` failures are historical checkpoints superseded by this source and acceptance record. The old browser-host 503 was traced to a `/var` symlink in the test temporary path; canonicalizing the fixture path passes without relaxing product path validation. Original failure logs remain; they are not rewritten as first-attempt passes.

## 1 Background and failing case

Local desktop conversation "这个讲了什么" (2026-10-30):

| Turn | Owner input | Submitted execution envelope | Result |
|---|---|---|---|
| 1 | "这个讲了什么" + `Ubuntu服务器运维学习计划_从概念到实操.md` | One message; `input_files` contains the file | Normal summary |
| 2 | "完善一下发给我" ("improve it and send it to me") | Three plain-text messages, no `input_files`, no file reference of any kind | `Blocked: the request cannot be routed under the current capability boundary. semantic_coverage_low` |

There are three root causes:

- **R1 File references belong only to the current turn.** [`executeWorkbenchWorkflow`](../services/gateway/internal/gateway/execution_workflow.go) creates a new MemoryStore session for every execution. It imports history as `role/content` only and writes only the current `input_files` into the workspace. The gateway's [recent document resolver](../services/gateway/internal/agent/recent_document_resolver.go) needs document records, tool calls or history messages with attachments; all three are empty on this path. The desktop ClientStore records file-to-message links in `message_files` and `delivery_files`, but the envelope builder drops them. The existing workaround, `requestsPreviousFileEdit`, requires an edit verb, a "previous/earlier" word and the full file name together; turn 2 satisfies none of them.
- **R2 Delivery is not separated from the request.** `projectDeliveryDirective` (since removed, see 4.6) strips delivery text only for an explicit external send that names a channel and a recipient. "发给我" contains a send verb but is not external, so the whole sentence enters both scoring channels and competes with the `conversation.publish` example "发送这个文件" ("send this file").
- **R3 Low confidence terminates the run.** `VerdictLow` maps to `RouteUnmatched`. [`completeTerminalRoute`](../services/gateway/internal/agent/workflow_dispatcher.go) returns a fixed English sentence plus the reason code as the final answer. The fused candidates and grounded files are discarded, and the owner can only rephrase.

## 2 Confirmed decisions

Confirmed by the owner on 2026-10-30:

1. **No request completion or rewriting.** Routing remains anchored on the owner's own words. The split only marks spans of the original text and never adds content. Context reaches routing only through typed resources and history.
2. **Files are durable conversation references; content is read on demand.** The client records file ID, path and digest; the backend obtains content only when route grounding or a read needs it. Content is not injected into prompts in advance.
3. **The business operation and the delivery mode in a request are separated.** Before scoring, one separate model call uses the full context to split the request: the delivery mode and delivery object go to the delivery layer, and the business part goes to embedding and Tree scoring. The owner accepts the added call time. The former send-only scoring candidate is removed, and sending is handled by a deterministic delivery-only path.
4. **Low confidence asks the owner instead of failing.**
5. **All issues raised by the independent review are adopted with their suggested fixes** (reflected in sections 3–8).

Reference: [deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) keeps files as `FileBlock` references in the session log and rebuilds context from that log every step (`AGENTS.md`, "Model-visible ⟺ logged"). The model sees only a file-handle line and reads content with file tools when needed. When unsure, it calls `ask_user_question`. SparkClaw keeps its capability catalog classifier and governance boundaries; it adopts the data organization of the first two points and the failure handling of the third.

## 3 Layer 1: conversation file handles

### 3.1 Handle model

A handle is an immutable reference to an existing conversation file, `{id, name, size, sha256}`, attached to the message it belongs to. There are two sources:

| Source | ClientStore relation | Source marker after gateway import |
|---|---|---|
| Owner attachment | `message_files` | `conversation_attachment` |
| Delivered result file | `message_deliveries` → `delivery_files` | `conversation_result` |

A handle is not an upload. No file bytes are transferred at submission. Handle metadata counts toward the context budget, and file bytes count toward the task budget when materialization is first requested; owner-level concurrent-task reservation is separate (3.7). Handles are read-only on the gateway. Client absolute paths never enter the envelope or model prompt.

### 3.2 Execution envelope schema 2

- `messages[i]` gains an optional `files` array of handles. Assistant messages may also carry `clarification` (section 5.4). Final schema 2 also requires a stable message `id` for binding the original clarification request. Allocate it before serializing and hashing the envelope; reconnects and replays cannot regenerate it. Both strict decoders and serialization now implement it.
- If missing or trimmed files make a message's file set incomplete, retain optional boolean `files_unavailable=true`. It says only that this group cannot be resolved safely, carries no unavailable path or content, and grants no read access. Both schema 2 decoders now validate this field.
- `input_files` keeps its meaning: files explicitly selected for the current turn, still uploaded before admission.
- Handle IDs are unique within one envelope. One message carries at most `resultFiles` handles, and one envelope carries at most 32 handles in total. When a handle ID also appears in the current `input_files`, the current input wins and that handle is not materialized separately.
- A handle's workspace locator is `conversation-files/<file-id>/<name>`; current inputs keep their file names.
- **Text-only executions and sessions without file authorization carry no handles.** The desktop attaches no handles when `capabilities.files` is unavailable. The gateway rejects an envelope that carries handles in a text-only execution (`!expanded || !AllowFiles` in [`workbench_iscp.go`](../services/gateway/internal/gateway/workbench_iscp.go)), and the materializer is not enabled there.

Following the direct cutover rule of the [workbench convergence design](workbench-runtime-convergence-design.md), schema 1 is retired without dual parsing. Gateway and desktop must ship as a matched release. Gateway decoding keeps `DisallowUnknownFields`.

### 3.3 Gateway import and target resolution

- History messages are imported with their handles: `app.MessageAttachment{Name, RelPath: conversation-files/<id>/<name>, Bytes, SHA256, Source}`.
- `recentDocumentMessageReferences` considers both owner attachments and delivered result files and, within the routing history selection window, selects the most recent message that carries files. When that message carries several files, it remains ambiguous and leads to clarification.
- **Explicit file names match handles first.** When `documentRoutePaths` recognizes a file name in the request (for example "修改 report.md"), it first matches the name against all handles in the envelope: a unique match becomes the target; several matches lead to clarification; no match falls back to the existing workspace path rule. Files older than the selection window can be referenced this way by name.
- The document projections for Tree and for the split call contain name, format, source and activity only, never content. File names and history text are always data in prompts, never instructions.
- External MCP invocations keep their current restriction: no recent-record lookup and no handle projection, only locators explicitly supplied in the current request.

### 3.4 On-demand materialization

Materialization is triggered only by **reads**:

1. After route grounding, when the selected `workspace_path` target is a handle and preflight needs it (format sniffing, document record registration).
2. When a read tool reads a handle path: `files.read`, each format's document read, and document edit tools reading their input file.

Writes and deletes under `conversation-files/` are always rejected. Materialization does not go through the existing ctx-less `resolvePath`, which also serves writes and deletes; instead a new ctx-aware read-only resolution entry is added and implemented by `HandleMaterializer`.

Flow:

1. **Handle staging.** At admission, the execution gets its own staging area for declared handles; a wait channel is created only on first read. This is separate from `s.inputs`, so `Submit` does not delete it. Admission reserves one `TaskBytes` allocation against owner capacity; a post-admission upload does not reserve another task. Within-task accounting follows 3.7.
2. **Requesting an upload.** The materializer writes the handles it needs into execution `Status.pending_inputs: [{file_id, size, sha256}]`, next to `pending_approvals`, and waits until a deadline (proposed 60 seconds, never beyond the execution budget). The progress stream stays text-only and unchanged; the desktop obtains `pending_inputs` through its existing lookup every 5 seconds, so materialization adds up to about 5 seconds of discovery delay plus one upload.
3. **Client upload.** The desktop ExecutionClient accepts only handle IDs declared in that request's own `context_json`. It reads the local file, verifies size and digest, and uploads it with the existing `PUT /api/v1/inputs/{request}/files/{file}` (`execution.input.put`).
4. **Gateway acceptance.** After admission, `execution.Service.Upload` accepts a new upload only for a handle in the active execution's current `pending_inputs`. It verifies digest and size, stages the bytes and wakes the waiter. An already accepted identical (request, file, digest) returns idempotent success during the active execution even after leaving pending; terminal state or invalid authorization never reopens upload admission.
5. **Result.** On success the bytes are written to the handle path and the read continues. On timeout, a locally deleted file or a digest mismatch, the read returns a "file unavailable" observation and route preflight returns `conversation_file_unavailable`, which section 5 turns into an owner-facing message.

`execution.input.put` is extended from "upload before admission only" to "may upload requested handles after admission". The operation's inventory fields do not change, but when it is used does; what ISCP needs to check is in section 8.1.

Rejected alternatives:

- **Uploading every handle on every turn.** This repeats byte transfer each turn, adds latency and relay traffic, and gives the backend inputs it does not need. That conflicts with the [client/backend ownership boundary](client-backend-architecture-design.md), under which the backend processes only inputs that are explicitly needed.
- **A new reverse file-read command.** It would add an operation to the business inventory frozen by the ISCP–SparkClaw contract.

Materialized bytes are temporary inputs. They are deleted with the execution workspace (the existing `RemoveAll`) and never enter audit records or logs.

### 3.5 Result collection and edit outputs

- Result collection at the end of execution (the workspace walk in [`execution_workflow.go`](../services/gateway/internal/gateway/execution_workflow.go)) skips the whole `conversation-files/` directory, so materialized originals are never returned as new results.
- An edit whose input is a handle writes its output to the workspace root: `nextDocumentOutputRef` uses the workspace root instead of the handle directory, and the name follows the `VersionFamily` sequence (for example `报告.md` → `报告-2.md`), advancing on conflicts with files already at the root. Result names are therefore unique at the root and never trigger the name-based `ErrConflict`.
- When the delivery-only path delivers a previous conversation file, it copies the materialized file to the root under its original name; if that name already exists at the root, it advances along the version sequence (the existing `OpenVersionedFile`). The copy is returned as a result file (section 4.9).

### 3.6 Desktop changes

- The envelope builder attaches each history message's handles. Files deleted locally or larger than 8 MiB are not offered as handles, and no handles are attached when `capabilities.files` is unavailable.
- Remove the `requestsPreviousFileEdit` / `explicitlyNamesFile` rebinding; named files are matched against handles by the gateway as in 3.3. Files explicitly selected in the current turn still take precedence.
- Handle `pending_inputs` in lookup processing. On verification failure, upload nothing and let the gateway deadline close the wait.
- This layer changes no UI.

### 3.7 Context admission, budgets and lifecycle

**Fix the turn's snapshot.** Build it from messages accessible to the current identity, installation and conversation before splitting or scoring. Split and both scoring channels use the same snapshot and resource set; independent prompt compression cannot silently discard an explicit target or change the file set. Compression does not pre-read file content. Bind resources by `id/name/size/sha256/source`; do not merge different IDs by name or treat a changed digest under the same ID as the original file.

- Apply existing history message and byte limits while retaining the current request and explicit inputs. Keep or remove handles with their messages; project an ID only at its latest association. Select at most 32 handles in newest-to-oldest message order, then serialize chronologically so retries have a stable digest.
- The most recent message with files or `files_unavailable` is the reference boundary. An incomplete group asks for reattachment or an explicit target rather than silently selecting an older file. Removing a same-name member also marks the remaining same-name group incomplete. Deduplicating an ID fully projected in a newer message does not mark it unavailable.
- A file "older than the window" means older than the routing selection window but still admitted in the envelope. There is no whole-conversation search or automatic recovery outside that envelope. If trimming removes one of several same-name files, the remaining one must not appear unique: the client checks conversation-level name collisions while building the snapshot and asks the owner to reattach a target that cannot be represented completely.
- A selection reply must retain the original request snapshot, signed question and current reply as one bounded bundle. If it does not fit, ask for a restatement rather than submitting only "1" and an isolated signature. Missing, deleted, oversized or unauthorized history files never become current attachments automatically, nor does this flow open other files from the local Files preview.
- Reject an ID shared by current input and history when name, size or digest differs. Current-input precedence applies only to the same immutable file; a new version needs a new file ID.

| Budget | Rule |
|---|---|
| Context | The serialized envelope, including handles, message IDs and clarification records, fits `ContextBytes`; do not truncate JSON or individual owner requests |
| One file | Keep 8 MiB; no chunking around the limit or automatic conversion |
| Task | Keep 32 MiB; context reservation, current inputs and unique requested history files must fit; concurrent reads of one file count once |
| Owner | Keep 256 MiB and existing concurrent admission; staging and execution transfer the same reservation rather than counting it twice |
| Temporary workspace and results | Materialized inputs, edit outputs and delivery-only copies use the existing workspace budget; copies consume additional space. Existing aggregate result-size and file-count limits still apply even when each file is below 8 MiB |

These logical byte limits are not process RSS limits; avoid retaining upload and materialized copies indefinitely. Coalesce waits by `(request_id, file_id, sha256)`. Use the smaller of 60 seconds and the remaining execution time; cancellation, terminal state, revocation and shutdown wake every waiter. Release an unreceived file's reservation when its last waiter exits; reuse materialized files within the execution and release all temporary data on cleanup.

| Client upload state | Allowed next action |
|---|---|
| `ready`, proven not sent | Upload with the persisted operation ID |
| `unknown`, durable send intent without a definite outcome | Query the original key or use contract-authorized recovery; pending alone does not authorize resend or a new ID |
| Definite success | Continue lookup; repeated pending does not create another upload |
| Changed file, unavailable authorization, timeout or execution termination | Stop uploading; let the original execution return file unavailable or its existing terminal result; do not change the digest, borrow another conversation's file or rerun business |

A gateway restart does not restore process-local waits or file bytes. Apply existing execution restart termination, retaining control deduplication evidence rather than reconstructing and running history. Late uploads cannot revive execution. Transient credential refresh uses existing authentication recovery; a single 401 or timeout does not prove revocation or expand authorization.

## 4 Separating the business operation from the delivery mode

Before scoring, one separate model call splits the request into a business part and a delivery part. The delivery mode and delivery object go to the delivery layer for deterministic handling; the business part goes to embedding and Tree scoring. Once split, "send" is only a delivery mode, so the former send-only scoring candidate is removed.

### 4.1 Why the split is not folded into the Tree call

- The Tree strict schema is assembled as `map[string]any`. Go serializes its keys alphabetically, so `split` lands after `candidates` and the model would score before splitting. Even with ordered serialization, the order would depend on vLLM constrained decoding following the schema order, which the gateway cannot guarantee.
- "Scores apply only to the business span" could only be enforced through the prompt and cannot be validated.
- Split and scoring would fail together: whenever Tree fails, the delivery information would be lost as well.

The owner has accepted the latency of one extra model call.

### 4.2 Split call

- Add the model capacity operation `intent.request_split` (and `intent.request_split_repair` for repair). It runs on the Fast lane, produces compact structured output, and uses a strict JSON schema.
- The input is the full context: the current owner text, current-turn resources, the same history snapshot Tree receives, and the handle projection from section 3 (metadata only). External MCP invocations get no handle or history-record projection. Prompt admission reuses the Tree context builder and compression, without the semantic graph.
- The split call sees no capability candidates and produces no scores.
- The calibration file gains `split_timeout_ms` (proposed 15000). The split is timed separately before the scoring stage and does not consume `routing_timeout_ms`.
- The model call is recorded in model_calls as `intent_request_split`.
- Every source (web, third_party_device, timer) runs the split, except media-only messages without text (4.9).

Output fields:

| Field | Values | Meaning |
|---|---|---|
| `deferred` | Boolean | Execution or delivery is tied to a time, recurrence or condition (4.7) |
| `business_span` | Span of the original text, may be empty | What to do; empty means pure delivery |
| `delivery_mode` | `none` / `current_route` / `explicit_external` | No delivery, return to the current conversation, or an explicit external send |
| `delivery_object_kind` | `business_result` / `current_message` / `conversation_file` / `assistant_text` / `named_locator` | What to deliver: the business result, the current turn's content, a previous conversation file, the text of a previous assistant answer, or a path/locator named in the request |
| `object_span` | Span of the original text, may be empty | Words referring to the delivery object, such as "它", "上面的总结", "report.png" |
| `provider_span` / `recipient_span` | Spans of the original text, only with `explicit_external` | Channel and recipient |

The model returns only spans of the original text, a boolean and enum values. It does not rewrite text or choose concrete resources.

### 4.3 Validation and repair

1. **Spans and enums.** Each trimmed span must occur verbatim in the original text; enum values must be valid. Provider and recipient spans appear only with `explicit_external`, which needs at least one of them; Message Control asks for a missing channel or recipient. Match UTF-8 boundaries without case, Unicode or punctuation rewriting. Repeated text needs nonoverlapping occurrences; repair or fail if alternative assignments change meaning. Strict decoding rejects unknown or missing fields, duplicate JSON keys, wrong types and trailing objects.
2. **Combination constraints.** With an empty `business_span`, `delivery_mode` cannot be `none` and `delivery_object_kind` cannot be `business_result`. With `deferred=true`, `business_span` must equal the trimmed whole request and `delivery_mode` must be `none`. External MCP invocations cannot use `conversation_file` or `assistant_text`.
3. **Residual-text constraint (prevents meaning loss).** After removing `business_span`, `object_span`, `provider_span` and `recipient_span` from the original text, the remainder may consist only of: delivery verbs (发, 发送, 发给, 发到, 传, 传给, 转发, 投递, 给, 交回, send, deliver, forward, share, return, ...), self-references (我, 我这里, me, back), connectors (然后, 再, 并, 后, 之后, 通过, 用, 经由, 到, and, then, to, via, ...), particles (一下, 吧, 呢, please, ...), punctuation and whitespace. The word lists are code constants used only for validation, never for splitting. For example, if "不要删除 report.md，只读一下" is split with business "删除 report.md", the remainder contains "不要" and "只读", so validation fails.
4. **Repair.** On failure, make one repair call with the original output and the error, using the same schema and validation. If repair also fails, the split has failed (4.6).

The residual allowlist only checks literal coverage, not semantic correctness. `object_span` locates a delivery object and cannot absorb constraints such as "do not", "read only" or "confirm first". With business present, the object kind is `business_result`; other kinds apply only to pure delivery. Requests with discontinuous business spans, multiple business actions or multiple recipients that cannot fit this single-span schema stay whole within existing capability boundaries or lead to clarification; do not concatenate, invent text or partially execute them. Negated sending, such as "修改后不要发给张三", must not become external authorization from a lexical match; ask for restatement when an affirmative instruction cannot be established.

### 4.4 Dispatch after the split

1. **Pure delivery (empty business).** Neither embedding nor Tree is called; the request goes straight to the delivery-only path in 4.9. Such requests therefore make one fewer Tree call than today.
2. **With business.** `business_span` becomes both the embedding query and the Tree query, and the two channels still run in parallel. Tree also receives typed fields for delivery mode and delivery object kind; these are data, not instructions. Tree's schema, fusion and verdict logic are unchanged.
3. **Delivery mode.** `current_route` uses the existing ReturnRoute. `explicit_external` is resolved deterministically by existing message control from the split's channel and recipient; if no registered target resolves, the request goes to clarification. After the business work completes, its result waits for owner approval (4.6) and is then delivered as `business_result`.

### 4.5 Text used by each downstream consumer

| Consumer | Today | Becomes |
|---|---|---|
| Embedding query, Tree query | Whole request (delivery text stripped for explicit external sends) | `business_span` |
| Resource projection for business targets: URLs, workspace paths, weather location, browser destination, current tab | Whole request | `business_span` |
| Path and locator projection for the delivery object | None | `object_span` |
| Query slot `materializeRoutedQuery`, public named target Ref, document format `GroundRoute`, screenshot detection `asksForBrowserScreenshot` | Whole request | `business_span` |
| Explicit LocalMind assignment check | Whole request | Whole request, unchanged |
| Workflow goal text and model answer prompt | Whole request | Whole request, unchanged, plus typed delivery fields stating that delivery is handled by the delivery layer |
| Risk classification `classifyRisk` and guard | Whole request | Whole request, unchanged (safety checks must see all of the text) |
| Clarification templates and audit | — | Never record the original text |

When splitting fails and routing may continue, business consumers fall back to the whole request. Discard all delivery spans from failed output; they grant no external authorization. Audit still contains no text.

### 4.6 External-send approval

Revised 2026-10-30 by owner decision: the rule parser and its comparison guard (`externalSendEvidenceFromMessage`, `guardRequestSplit`) are removed. A delivery back to the current conversation is never restricted. An external send, meaning an endpoint other than the current conversation, is approved by the owner instead of being judged from text. This supersedes the Issue #16 rule that an owner's explicit send instruction needs no send approval ([Issue #16](issue-16-external-media-approval-design.md)).

| Case | Handling |
|---|---|
| Split says `explicit_external` | The split's channel and recipient spans form the delivery directive. Message Control must resolve exactly one registered endpoint; a missing channel or recipient, ambiguity or no match leads to its existing clarification |
| Endpoint resolved | Business runs. The completed result is not delivered: the run becomes `approval_pending` with one approval for the exact endpoint and the SHA-256 of the exact result content, with immutable arguments and a content preview |
| Owner approves | The run completes and the same content goes to that endpoint (HTTP, connector and ISCP approval paths all deliver) |
| Owner rejects, or the endpoint or content no longer matches | The run is blocked; nothing is delivered |
| The model marks an external send, but a constraint or purpose clause such as "不要附带草稿" or "to review" does not fit one business span | If the model's business joins verbatim pieces that, with the channel and recipient, cover the request exactly as an ordinary split does, routing scores that joined business. Otherwise, when there is a business and the channel and recipient are verbatim, the whole request becomes the business. Either way the target still comes from the spans and the send is approved |
| The model marks an external send that can be neither joined nor kept whole, such as a pure file delivery with a constraint | Ask the owner to restate the content and recipient; never return the result to this conversation instead |
| Split says not external, or the split fails without marking an external send | The result returns to the current conversation; no external target is inferred |
| Inbound SparkClaw MCP | Its single workspace-data approval already covers the send; no second approval |
| Composer-selected target | The owner picked the endpoint in the UI; no send approval |
| Timer source at due time | The target was approved when the schedule was created (4.7); no further approval |

Every result builder checks the approval before an endpoint route survives, so a completion path that does not queue the approval still cannot deliver externally. Degraded semantic routing no longer blocks external delivery, because the owner reviews the exact result. Delivery-kind clarification options are no longer offered; an outstanding one is treated as a changed binding.

Validation on 2026-10-30 (source only), using the same private model configuration as the earlier acceptance:

- **Real-model split**: 10 cases, 3 runs each, 30/30 passed. The cases cover a channel only, a recipient only, a pure history-file send and English. To make them pass, the residual allowlist gained 出去 and it, and the split prompt gained a channel-only example and an English example.
- **Native desktop real-model run**: 7 groups and 14 requests passed.
- **WebChat approval card**: tested against a production Gateway assembly with isolated memory state and a synthetic Weixin endpoint. The card shows the target, the reason, the content preview and the digest, and it has no edit control. Approving it delivered to that endpoint, which failed locally as expected because the synthetic binding has no credential.
- **Locked arguments**: the server now refuses edits to any approval marked `arguments_immutable`.
- **Business model instructions**: when a run waits for a send approval, the business model is told that SparkClaw sends the approved result. Without this, a real model began an English answer with "I cannot send messages via Weixin…", and that text would have been sent.
- **Constraint retest**: with the constraint and purpose cases added, the real split acceptance passed 12 cases × 3 runs, 36/36. End to end, the Chinese constraint case reached the send approval 2/2 with content that followed the constraint. The English purpose case reached it 1/2; the other run scored low on the whole request and asked the owner to clarify. A pure file delivery with a constraint asked the owner to clarify 2/2. None of these runs returned silently to the current conversation.
- **Channel aliases**: Message Control now matches 微信, wechat and vx to `weixin`, and 电报 and tg to `telegram`. The requested channel text is still reported unchanged. Before this, 微信 never resolved to a Weixin endpoint.

Known limits:

- **Desktop workbench**: it has no Message Control, so it never sends externally.

### 4.7 Scheduled and conditional requests

Under the current contract, scheduling classifies only future publication: `半小时后查一下上海天气` selects `schedule.manage#create`, and at due time the stored request re-enters the router with the timer source. The split follows the same principle:

- When execution or delivery is tied to a time, recurrence or condition ("一分钟后把这个文件发给我", "每天 8 点通过微信把天气发给张三"), `deferred=true`: the whole request is scored as business and selects `schedule.manage#create`; this request itself gets no delivery split.
- `deferred` is not scheduling authorization: the Catalog must support the time/condition semantics. If grounding does not produce a valid schedule creation, clarify or explain the unsupported request instead of executing immediately. This design adds no arbitrary conditional triggers.
- The stored schedule content follows the existing rules. At due time it re-enters the router with the timer source and the stored content is split then. A schedule whose `reminders.create` or `reminders.update` target is another conversation requires owner approval when it is created: Policy marks it `delivery_class=external_endpoint` and the approval arguments are immutable. Due-time delivery to that frozen endpoint needs no further approval.
- Clarification rules for the timer source are in 5.2.

Future access to desktop-local files cannot borrow this turn's temporary handles or current connection. Only explicit attachment snapshots and permissions already supported by schedule storage may be persisted with a schedule; history handles never become scheduled attachments automatically. Before creation, ask for reattachment when a required file cannot be durably bound. This design does not promise a future "it" still denotes the same file. Due execution uses the scheduler's existing task body and trigger metadata and cannot recreate the same schedule. Unresolved temporal ambiguity in the stored body terminates the timer with an explanation rather than recursively scheduling or waiting for interaction.

### 4.8 Removing the send-only scoring candidate

- Remove the examples and scoring eligibility of the `conversation.answer#publish` semantic variant, so it no longer appears in the embedding index or among Tree candidates.
- Clean up examples and hard negatives in other variants whose subject is sending (such as "发送这个文件", "发送演示文稿", "一分钟后发送这个文件"), rewriting them for the split semantics.
- Keep the Catalog publish route (`RouteOperationPublish` of `conversation.answer`), `CompletionMessage`, media governance, source checks and the external MCP workspace approval. The delivery-only path now generates and dispatches the publish route deterministically.
- Bump the semantic graph and calibration revisions and re-measure thresholds.

### 4.9 Delivery-only path

The delivery-only path generates a Catalog publish route (`Slots.Operation=publish`) and dispatches it through normal Catalog validation and `dispatchMatchedWorkflow`. The external MCP workspace approval (which requires `Operation==publish` and reads `Slots.Query`) and source eligibility checks therefore remain in force.

| `delivery_object_kind` | Deterministic binding | `Slots.Query` |
|---|---|---|
| `current_message` | Current-turn attachments and text, using the existing publishing content governance | Empty (publish allows an empty query) |
| `conversation_file` | The unique handle from the resolution in 3.3, materialized on demand and copied to the root as a result per 3.5; several or none leads to clarification | Empty |
| `assistant_text` | The text of the assistant answer in history that `object_span` refers to; clarification if it cannot be determined uniquely | Empty |
| `named_locator` | Extracted from `object_span` by the existing path and locator rules, including external MCP's `implicitResponseMediaLocator` | `object_span` |
| `business_result` | Not valid with an empty business (rejected in 4.3) | — |

Media-only messages without text keep the existing fast path: no split call and no Tree call. They directly generate the publish route above, and source checks read the Catalog publish route instead of depending on the `conversation.answer#publish` candidate in the semantic graph.

### 4.10 Contract amendment and audit

[Intent routing](intent-routing.md) now reflects these P3 amendments in both languages:

| Current statement | Amended to |
|---|---|
| There is no model-based request normalization call, canonical request, or persisted normalization structure | One model-based split stage precedes scoring. It only marks spans of the original text, a boolean and enum values, never rewrites the question or chooses resources. The backend does not persist split results; clarification records are kept by the workbench with the conversation and signed by the gateway |
| Fast scores candidates only and cannot rewrite the question or choose a concrete resource | Unchanged; Fast's input becomes the business span instead of the whole request |
| `clarify`, `blocked` and `unmatched` are terminal and do not fall through to keyword routing or a generic loop | Still terminal for that run. When the owner's next message selects an option by number, it is a new request routed by that signature-verified explicit selection (5.5) |
| `发送文件` with an attachment selects `conversation.answer#publish`; an external endpoint changes only delivery | "Send" is a delivery mode recognized by the split; pure delivery generates a publish route through the delivery-only path, and an external endpoint still changes only delivery |
| A message with no owner text and only media parts selects the registered `conversation.answer#publish` candidate directly | It directly generates the Catalog publish route, without a semantic graph candidate |

Split business audit records only `deferred`, `delivery_mode`, `delivery_object_kind`, pure-delivery status, split failure and guard outcome, never text. `model_calls` retains operation, model, tokens, timings and bounded error codes, not prompts, raw split/repair JSON or original provider error text. IDs/digests needed for deduplication belong only in dedicated control storage.

### 4.11 Execution order and failure boundaries

1. **Admission.** Validate source, identity, envelope, resource permissions and byte limits, then fix the snapshot. File names, historical text and model output remain data.
2. **Selection check.** Only a pure reply immediately following a valid clarification enters 5.5; otherwise use the current owner text. Input guards and risk checks cover the full original request and current selection.
3. **Split.** Pure media uses the existing publish fast path. Other requests make one split call with at most one repair. Both calls, compression and admission share `split_timeout_ms` (initially 15000 ms) and the execution deadline; repair gets no fresh full budget.
4. **External and deferred handling.** Resolve an external target through Message Control (4.6). Deferred requests retain the whole text for scheduling and cannot send immediately. A required clarification terminates routing without business or delivery effects.
5. **Delivery or scoring.** Non-deferred empty business resolves an object and generates a Catalog publish route. Nonempty business sends only its verbatim span to embedding/Tree. A failed split sends the whole request to existing scoring, never a new sending-keyword router.
6. **Grounding and execution.** Check source eligibility, current state, unique target, policy and approvals; materialize only when a read is needed. An object, target or read failure cannot select a different file or continue with empty content.
7. **Delivery.** After business succeeds with deliverable evidence, use existing CompletionMessage/ReturnRoute/message control; an external send first waits for the approval in 4.6. Business failure cannot send a false success result. An unknown external outcome retains the original delivery ID and cannot rerun business merely to resend.

A valid signed selection replaces only fusion-confidence thresholds. Pure delivery does not call Tree, so Tree failure alone does not affect it; file reads, source, content governance, delivery health and authorization remain independent checks. Each execution produces one terminal result, committing clarification text and its record together.

## 5 Layer 3: low-confidence clarification

### 5.1 Decision table

| Case | Today | New behavior |
|---|---|---|
| `blocked` (degraded mutation, policy) | English sentence + reason code | Still blocked; explanation in the owner's language, reason code only in audit |
| `ambiguous` | "I need more information…" + reason code | Offer the executable candidates among the top two |
| `low`, top score ≥ `clarify_floor` or a conversation file is grounded | Terminates | Offer the executable candidates among the top two, or ask to confirm a single one |
| `low`, below `clarify_floor`, nothing grounded | Terminates | Ask the owner what they want; no reason code shown |
| `required_grounded_target_missing` | Generic sentence | State which kind of file is needed and ask the owner to attach or name one; list same-kind handles by name if the conversation has any |
| `required_grounded_target_ambiguous` | Generic sentence | List the candidate files by name |

"Executable" means not blocked in the current pipeline state: mutation and external candidates are excluded when degraded, and LocalMind candidates appear only when the owner explicitly assigned LocalMind.

Take the top two fused candidates first, then filter them individually, keeping their order without backfilling lower-ranked candidates. With none, explain the capability or input requirement; with one, ask to confirm that candidate. Ask about ambiguous files separately rather than taking a Cartesian product of capabilities and files. Offer file choices for at most two files; with more, provide bounded name/source hints and ask for a precise description rather than pretending only two exist.

`clarify_floor` is a new calibration field and requires a new calibration revision. Its initial value is measured on the evaluation set in section 6; the existing clear, mutation and degraded thresholds do not change.

### 5.2 Clarification by source

| Source | Clarification |
|---|---|
| Desktop workbench | Numbered options with a signed clarification record (5.4), stored with the assistant message in ClientStore |
| Host WebChat | Same; the clarification record is stored with the assistant message in the host Store |
| third_party_device (WeChat, etc.) | No numbered options and no clarification record; describe the possible handling in words and ask the owner to restate |
| External MCP invocation | No numbered options; return the existing `router.clarify` waiting result with an explanation, and the caller issues a new request |
| timer | Stays terminal with no clarification; explain in the owner's language that the scheduled content could not run |

### 5.3 Clarification content

Clarification text comes from a deterministic template with no model call:

> 我不确定你想怎么处理《Ubuntu服务器运维学习计划_从概念到实操.md》：
> 1. 修改文档内容并返回新版本
> 2. 阅读并说明文档内容
>
> 回复序号，或直接说明你的需求。

- Each semantic variant gains `clarify_label` (one Chinese, one English), validated as required in `semanticrouting.Compile`.
- Each option states the action and return mode. Distinguish same-name files by attachment/result source, turn or short ID, without client absolute paths.
- The reply language follows the owner's request: Chinese when it contains Han characters.
- This phase emits ordinary assistant text only and adds no buttons or other UI elements. Buttons need separate owner approval.

### 5.4 Clarification record and signature

The current record uses schema 2, replacing the old schema 1. This is a field illustration; digest placeholders are not valid records:

```json
{
  "schema_version": 2,
  "id": "<uuid>",
  "request_id": "<request that asked the clarification>",
  "source_message_id": "<original owner message>",
  "source_text_sha256": "<digest of the clarified owner text>",
  "context_sha256": "<original admitted context and resource manifest digest>",
  "prompt_sha256": "<complete displayed clarification text digest>",
  "issued_at": "<UTC time>", "expires_at": "<UTC time>",
  "graph_revision": "...", "catalog_revision": "...", "calibration_revision": "...",
  "options": [
    {"n": 1, "kind": "route", "candidate_id": "...", "file_id": "...", "binding_sha256": "..."},
    {"n": 2, "kind": "route", "candidate_id": "...", "file_id": "...", "binding_sha256": "..."}
  ],
  "mac": "..."
}
```

- `mac` is computed with a dedicated gateway-held key (new, stored with execution control state, never sent to clients). It covers every record field plus owner, client, installation and local conversation ID.
- Records contain neither business text nor plaintext endpoints. A `delivery` option must carry enum `delivery_mode`, which describes the selection, not external authorization. Every option has `binding_sha256`, binding the candidate and resource type, ID/name/size/digest/source. Assistant text binds message ID and content digest; locators bind the original span and deterministic normalized locator. Delivery also binds mode and unique registered endpoint identity. A `file` option selects only a file and grants no business capability.
- `context_sha256` covers ordered message IDs, roles, text digests, resource manifests, incomplete-set flags and request source from the original admitted snapshot, excluding the later question and reply. Normalize original `input_files` into the original owner message's file manifest; preserve their original source and digest when carried as history handles, excluding current selection inputs. The owning workbench keeps the snapshot in existing task data; desktop backends need not persist chat content. `prompt_sha256` covers the complete displayed question.
- The MAC covers version, timestamps, all digests and options under a separate schema 2 domain. Records expire 24 hours after issue, checked on gateway time and never extending identity authorization. Lost or rotated keys invalidate old records; never re-sign a client-supplied record.
- Desktop: retain the optional result key `clarification` and schema 14's `message_clarifications`, updating strict allowlists. Assistant text, record and result ACK are committed atomically. Schema 15 adds selection intents binding clarification ID, option and successor request ID; restart cannot turn one selection into a new request.
- Host WebChat stores the same record with the assistant message and uses equivalent selection deduplication. Update interfaces, memory/file/PostgreSQL, snapshots and migrations together. Third-party, external MCP and timer sources produce no record.

Use one UTF-8 canonical JSON encoding for signatures and digests: schema-defined ASCII object keys in ascending byte order; ordered arrays; no whitespace; nonnegative decimal integers without leading zeroes; no Unicode normalization. Escape quotes and backslashes, encode U+0000–001F as lowercase `\u00xx`, and emit all other characters directly as UTF-8, including HTML, slashes and U+2028/U+2029. Reject duplicate keys, invalid UTF-8 and unpaired surrogates. Omit absent optional fields rather than emitting null. Timestamps use UTC seconds, `YYYY-MM-DDTHH:mm:ssZ`. Hash original bytes for files and original UTF-8 bytes for `source_text_sha256`, `prompt_sha256` and other text digests, without trimming or newline normalization; use canonical JSON for structured digests. Sign `HMAC-SHA256(key, canonical({domain:"sparkclaw.clarification.v2",binding,record}))`, omitting `mac` during calculation and returning lowercase hex. Implementation must include shared Go/JS Chinese/English positive and negative vectors.

| Option kind | Required fields, besides `n` and `binding_sha256` | Binding and forbidden combinations |
|---|---|---|
| `route` | `candidate_id`, plus `file_id` when a file is required | Bind business-span digest, candidate, input target and delivery intent; prohibit `delivery_mode` |
| `file` | `file_id` | Bind only the immutable file and original request; prohibit candidate and `delivery_mode` |
| `delivery` | `delivery_mode` | Bind original business-span digest, object kind/resources and displayed endpoint (empty for current route); prohibit candidate and `file_id`, with resources covered by the binding digest |

One question contains 1–2 options of one kind, numbered consecutively from 1. When a `business_result` does not yet exist, bind the business text digest and input targets, without inventing a result-file digest. If the original split or object cannot provide a stable binding, emit plain clarification text without executable choices.

### 5.5 Handling the answer

- Recognize only a pure selection immediately after the latest clarification assistant message: `1`/`2`, "第一个／第二个", `first/second`, or a verbatim match to a complete signed option label. Trim outer whitespace, without fuzzy matching. "1, but send it to Li Si" is a new request. An intervening owner request ends the old clarification's numbered continuation eligibility.
- The gateway performs these checks in order:
  1. Validate schema, MAC, identity/conversation binding, lifetime, displayed-text digest and graph/catalog/calibration revisions. Forged records cannot steer routing. A "1" without a valid adjacent clarification is ordinary input; never search older history for a usable signature. Expired or changed revisions explicitly request restatement.
  2. Find exactly one original request by `source_message_id` and text digest, then verify context and resource digests. Matching text in another turn is insufficient; missing files cannot be replaced with new same-name files.
  3. **Re-run splitting** on the original text and admitted snapshot, then compare the option binding. Changed operation, object or endpoint requires another clarification; an old choice cannot acquire a new meaning.
  4. A `route` choice fixes the candidate; a `file` choice fixes the resource and reruns normal scoring; a `delivery` choice fixes the displayed mode. Explicit delivery selection resolves only the prior rule/split disagreement. Rules and endpoint must still match the signed binding; do not loop unconditionally back to the same confirmation.
  5. Recheck source, readable file, current degradation, policy, approvals and external authorization, then acquire the unique consumption claim in 5.6 before any side effect.
- An explicit owner selection replaces the fusion thresholds but bypasses no policy, approval, degraded blocking or external-delivery authorization.
- Any other reply is routed as an ordinary request. The clarification stays in history, and nothing is rewritten.

### 5.6 Single-use selection, concurrency and restart

Clarification is terminal for the original execution. Selection creates a new execution with its own request ID, not a resumed workflow stack. An original execution with business effects or pending delivery cannot be presented through this mechanism as an unexecuted choice.

In one transaction the client saves the selection message, original clarification ID and successor request ID. After validation and before dispatch, the gateway atomically binds `(owner, client, installation, conversation, clarification_id)` to that successor request and option. This control record contains only IDs, digests, state and expiry, not owner text, file content or model context. It participates in existing admission persistence and capacity gates; never execute first and record later.

- Reconnecting the same successor request ID queries its existing outcome. Different request IDs competing for one clarification permit at most one claim; the other does not execute and refers to the existing request status. Duplicate submission and concurrent clients cannot send twice.
- A crash or unknown delivery after claiming keeps the claim and uses original-request lookup/termination; it does not release the choice for another request. Validation failure before a claim produces no business effects.
- Keep consumption markers through record expiry, longer while a successor is nonterminal or unknown, and never shorten existing control-deduplication retention. Capacity exhaustion rejects new selections rather than deleting live old evidence.
- A further independent ambiguity may create a new signed question if the previous attempt had no effects. Repeating the same ambiguity for the same original request, snapshot and candidate set instead asks for a full restatement, avoiding an endless numbered dialogue.

A MAC proves that gateway-issued content was not altered, not that the client displayed it, and does not replace owner, tool or messaging permissions. The displayed-text digest, adjacent reply and durable deduplication jointly constrain this flow; it is not a new send-approval channel.

## 6 Evaluation and acceptance

Under the owner's scope adjustment, scenarios concerning document modification outcomes remain for later design acceptance and do not gate this turn's completion. Request splitting, file binding and authority boundaries remain covered. The scoped record above identifies the final feature set and evidence.

Regression cases go into `document_context_routing_test.go`, `intent_fusion_calibration_test.go`, the execution service tests, and the desktop `client-store` / `execution-client` tests:

| Scenario | Expected |
|---|---|
| Turn 1: md attachment with "这个讲了什么"; turn 2: "完善一下发给我" | Turn 2 routes to `document.edit` targeting the turn-1 file, with a uniquely named output at the root; if below threshold, it asks a clarification listing that file. `semantic_coverage_low` never reaches the owner |
| Turn 2: "再精简点", "翻译成英文发给我", "改一下发我" | Same, with delivery `current_route` |
| Turn 3 refers to turn 2's result file | Target is the result-file handle |
| Previous turn has a file; this turn says "把它发给我" | Pure delivery of the previous file, copied to the root and returned |
| "把上面的总结通过微信发给张三" | Pure delivery of `assistant_text`, resolved by message control and sent after owner approval |
| "发送这个文件" with a current-turn file; a media-only message | Pure delivery, same result as today's publishing |
| "通过微信把这个文件发给张三" | Message control resolves the endpoint; the result is sent after owner approval |
| "把张三的报告通过微信发给李四" where the model marks the recipient as "张三" | The approval shows the resolved recipient and content; the owner rejects it and nothing is sent |
| "不要删除 report.md，只读一下" | The residual-text check rejects a wrong split; nothing is deleted |
| "交给 LocalMind 处理" | The LocalMind assignment stays business and is not recognized as delivery |
| "一分钟后把这个文件发给我", "每天 8 点通过微信把天气发给张三" | `deferred`; the whole request selects `schedule.manage#create`; split happens at due time |
| "搜一下金价发给我" | Query slot is "搜一下金价" |
| "修改 report.md" where report.md exists only as a handle | Matched to the handle by name and grounded |
| Two same-named handles; a handle with the same ID as a current input | Naming the file asks a clarification and result names stay unique; for the same ID, the current input wins |
| A file older than the selection window | Not selected by recent resolution; resolvable by name |
| Text-only ISCP session carrying handles | Gateway rejects the envelope; the desktop attaches no handles in that state |
| A split span that is not a substring; split failure for "通过微信发给张三" | The former gets one repair and is then treated as a failure; the latter is blocked |
| Pure-delivery request when the split succeeds and Tree fails | Unaffected; delivered as usual |
| A degraded mutation candidate ranked second | Not offered as a clarification option |
| A forged or tampered clarification record; an old record after a revision change | Signature failure routes as an ordinary request; a revision change asks to restate |
| Reply "1" after a clarification | Re-split, then run the selected candidate and file |
| Low-confidence requests from WeChat, external MCP or timer | Handled per 5.2, never with numbered options |
| Follow-up after the local file was deleted | That handle is not offered; with no other file, clarification or an explanation |
| `pending_inputs` timeout, digest mismatch, file ID outside the request; writes or deletes under `conversation-files/` | No upload or rejection; file unavailable; writes and deletes are rejected |

Real-model evaluation: replay these conversations on work2 with the matched release and record the outcome distribution and latency. Report the time added by the split call and by on-demand materialization separately. For reference, the Tree scoring call on work2 currently has a median of about 6.5 seconds and a P90 of about 9.5 seconds (7 samples). UI acceptance must cover existing conversations, attachments, result downloads, and how clarification text is presented.

### 6.1 Additional boundary acceptance

| ID | Scenario | Required evidence |
|---|---|---|
| C01 | More than 32 handles, history trimming, one same-name file omitted | Stable bounded output; incomplete sets never imply uniqueness; no reads outside the envelope |
| C02 | Same ID with changed digest/name; same name with different IDs; current/history conflict | Reject conflicts, clarify duplicate names and preserve originals |
| C03 | Concurrent reads of one file, files approaching 32 MiB, oversized result copies | One wait/reservation per file, cleanup on failure and unchanged aggregate result limits |
| C04 | Lost upload success response, restart after unknown, late terminal upload, revocation race | Original key, no blind resend or business replay, no reads after termination or effective revocation |
| S01 | Repeated words, discontinuous business, negated sending, wrong JSON keys/types, repair timeout | No rewriting, lost negation or constraints hidden in object; repair does not double the budget |
| S02 | Model-guessed endpoints, ambiguous aliases, endpoint/file changes after clarification | No unconfirmed send; changed bindings invalidate the old selection |
| S03 | Pure delivery with Tree failure, unavailable external path, failed result read | Tree failure alone does not block deterministic publish; independent gates still block their respective effects |
| S04 | Scheduled history files, temporal ambiguity remaining at due time | No implicit local-file transfer, recursive schedule creation or interactive timer wait |
| Q01 | Identical requests in different turns, swapped option text, cross-identity/conversation copy, expiry/revision change | Verify original message ID, context, displayed text and revisions |
| Q02 | Selection replay, two request IDs choosing "1", restart before/after dispatch | At most one consumption claim; unknown retains it; original outcome is queryable or explicitly terminated |
| Q03 | "1, but send to Li Si", a new request after the old question, three same-name files | Treat as new input or ask for precision; never reuse an old choice or silently narrow the set |
| Q04 | memory/file/PostgreSQL and desktop migration, failure while saving a question | Text/signature consistency, default file backend support, readable but non-executable old records, no partial-commit ACK |
| U01 | Existing conversations, drafts, attachments, downloads, file panel/tabs, menus and reconnect after login | Preserve original pages and interactions; show actionable notices only, without permanent technical status |

### 6.2 Calibration, evidence and pass criteria

Freeze bilingual positive cases, negated/ambiguous/external counterexamples and source matrices before tuning; keep tuning and independent acceptance sets separate. Record graph, candidate examples, model configuration, calibration version and test-source digest together. Removing publish requires measuring the new fusion distribution, not declaring old scores valid. This revision uses `clarify_floor=0.46`, checked by real graph samples and independent boundary tests; it controls whether to offer a candidate, without lowering mutation or external execution thresholds.

Automated acceptance must assert the real dispatcher chain; helper tests alone do not prove integration. Report split accuracy, correct execution/clarification and false-rejection counts, irrelevant clarification prompts and erroneous effects. Acceptance cases require zero wrong-recipient sends, wrong-file deliveries, duplicate execution or permission bypass. This turn verifies historical reads, business/delivery splitting, executable same-name choices and reattachment recovery. The original improvement's edit outcome is deferred at the owner's request; hiding an English error cannot count as successful editing.

Real-model observations (nearest-rank percentiles): 18 independent splits have P50/P90 of 570/1560 ms; 14 native send-to-local-result requests have 4534/8127 ms; four historical uploads have 100/107 ms, with pending detection to upload start at 1/3 ms. Final result visibility is additionally measured at 4534/8128 ms (14 requests). Upload timing includes transport and the service response, not separate materialization. Context construction, repair, embedding/Tree, materialization and first visible feedback were not measured separately and are not filled with zeroes. Complete stage-level sampling remains in P4; this is not production performance acceptance.

Measure context building, split, repair, embedding/Tree, pending discovery, upload, materialization and first visible feedback separately, with sample counts, P50/P90 and failures/timeouts. The old seven Tree samples are a historical reference, not acceptance of this design. Repeat core real-model cases at least three times under fixed configuration, preserving every outcome and original failure; retries cannot overwrite first failures.

The evidence manifest includes source/build versions, both schemas, model/graph/calibration versions, source, anonymized case ID, routing/clarification/delivery results, operation counts and stage timings. Sensitive text, files and endpoints remain in controlled test data rather than project audit logs. Track design completion, integrated source, isolated tests, real model, original UI and paired release separately; close each phase only when all its required gates pass.

## 7 Implementation phases

| Phase | Content | Exit criteria |
|---|---|---|
| P1 Gateway | Envelope schema 2, text-only handle rejection, history handle import, explicit name matching, resolver extension, handle staging and materializer, `pending_inputs`, post-admission upload, read-only resolution entry, result-collection exclusion and root outputs | Go unit tests and execution service contract tests pass; post-admission upload is enabled externally only after its fault-injection acceptance and the ISCP checks (8.1) complete in P4 |
| P2 Desktop | Handles in envelopes, stable message IDs, `pending_inputs` handling, removal of the old heuristic, clarification records and selection intents (schema 14→15 and payload allowlist) | Desktop tests and original-data migration pass; ships with the corresponding gateway |
| P3 Routing | Split call (capacity operation, prompt admission, validation and repair, timeout), external-send safety guard, downstream text switch, scheduled-request handling, removal of publish scoring eligibility and example cleanup, delivery-only path, clarification decision table and per-source handling, `clarify_label`, record signing and answer handling, terminal text, graph and calibration revisions, intent routing document amendment | Regression set and calibration tests pass |
| P4 Release acceptance | Matched release on work2 and Mac; replay the original failing conversation | Section 6 acceptance passes and is recorded |

P1 and P2 envelope changes must ship together because schema 1 is retired. P3 depends on their file foundations and includes clarification payloads on both sides, selection intents and host Store changes; it cannot ship as a gateway-only update to an old desktop. Acceptance may be staged, but the first complete user-facing release must satisfy the paired gates for every enabled phase.

### 7.1 Integration and migration checklist

| Boundary | Implementation locations | Delivery check |
|---|---|---|
| Envelope/history | `execution/protocol.go`, `gateway/execution_workflow.go`, desktop `client-store.mjs` | Stable message IDs, handles and context digests; matching strict allowlists and serialization vectors |
| File reads | `execution/handle_inputs.go`, `gateway/execution_conversation_files.go`, toolhub read-only entry | Task/owner budgets, shared waits, cancellation/late upload/unknown, read-only directory and result exclusion |
| Routing | Agent dispatcher, split call/validation/guard, grounding, semantic graph/calibration | One entry following 4.11; downstream text use follows 4.5; publish loses scoring eligibility but keeps Catalog capability |
| Clarification | `clarification`, execution control persistence, agent generation/selection, ClientStore, host Store | Schema 2 signatures/bindings and unique consumption, all three host backends, no test-only dead code |
| Delivery | Publish route, result copies, CompletionMessage/ReturnRoute | Existing governance/deduplication, no repeated business, wrong targets or false success |

Schema 15 adds a selection-intent table on schema 14 while preserving messages, drafts, file relationships, schedules, unknown uploads and signed text. This revision reserves 15 for selection intents; later migrations advance without reusing it. Preserve old schema 1 JSON and display text, marking them ineligible for continuation; never invent signatures, expiry or consumption evidence. Existing message database keys remain stable. Building new envelopes cannot rewrite an admitted task's original digest.

Keep old signed records only as local history, omitting their `clarification` field from new envelopes so strict decoding does not reject the whole conversation. Stop admission and drain old-version in-flight work before paired cutover; unknown requests retain original-key lookup and are never imported for new execution. Old-format queued items proven unsent retain text, attachments and unsubmitted state until an explicit owner resubmission creates a new-format request. Never silently rewrite an operation ID or digest. Release acceptance covers this existing data.

Host Store field/interface changes land with memory/file/PostgreSQL, file Snapshot and SQL migration. New durable control records contain only the minimal fields allowed in 5.6. Failed migrations roll back transactionally and preserve old data; old binaries must refuse writes to new schemas. Rollback first stops admission and handles in-flight/unknown work, then uses a proven compatible pair. Do not downgrade databases, delete unknown records or restore stale authorization. This revision verifies migrations in isolated fixtures; it neither migrates production data nor performs release actions.

### 7.2 Completion criteria

P1–P3 are integrated and verified as recorded above. P4 production paired release, installed-data cutover, hosted Relay joint fault qualification and complete per-stage performance sampling remain incomplete; 0051 review is recorded separately. Isolated results do not substitute for counterpart acceptance or production deployment evidence.

## 8 Cross-project boundary

- No ISCP operation is added, and the 118-operation inventory is unchanged. The only ISCP-related change is when `execution.input.put` is used; see 8.1.
- Envelope, execution status and clarification record payloads are owned by SparkClaw. Gateway, desktop and host Store changes do not automatically change frozen ISCP wire, but post-admission upload ordering still needs joint review.
- The `/v1/executions:*` path used by JingSi is a separate contract and is unaffected.
- Host WebChat persists files and history on the deployment host, so historical attachments are already resolvable there. P4 confirms that path under the convergence design's "equivalent conversation" acceptance.

### 8.1 What ISCP needs to do

The InfiniCenter `contracts/ISCP--SparkClaw/contract.md` (frozen version 2026-10-19.2) and `contracts/SparkClaw--JingSi/contract.md` have been checked. Bytes still travel through SparkClaw's own ISCP object store on the gateway (purpose `execution_input`, 8 MiB per file), relayed in `task.invoke` frames. The proposal retains operation name, scope `objects.write`, forward direction, `original_request` recovery, frame types, purpose and size limit; that does not establish acceptance of post-admission ordering.

There is one change: an `execution.input.put` for a given `request_id` may now arrive after `execution.submit` was admitted, interleaved with that request's `execution.lookup`. ISCP needs to:

1. **Check deduplication and ordering.** Relay, E2E and control recovery must not reject a later `input.put` because the request is already admitted, and must not deduplicate by `request_id` alone; the deduplication key must include the file ID and digest. Multi-file uploads before admission already depend on this; this item confirms it also holds after admission.
2. **Check recovery semantics.** A post-admission upload recovers as `original_request`: retries reuse the same key and object and are idempotent on the gateway; an unknown outcome stays unknown, with no new ID and no blind resend. If the gateway receives nothing before its deadline it treats the file as unavailable, so business work is never repeated because of this.
3. **Check the immutable-input common gate.** Post-admission bytes are bound in advance by the handle's sha256 in the admitted `context_json`; the admitted request's input digest does not change, so this is not new business input after admission.
4. **Check authorization.** The upload uses the session's existing `objects.write`; no permission is added. If authorization is revoked or expires during execution, the upload fails and the gateway treats the file as unavailable.
5. **Take part in P4 acceptance.** On work2, help complete fault injection for post-admission upload: disconnect during upload, retry after reconnect, unknown outcome, and authorization revoked mid-execution. The inventory's common gate requires fault-injection acceptance before the behavior is enabled externally.

These requirements are already filed in **0051**, not a possible future decision. The review request exists in ISCP inbox as `016-来自SparkClaw-受理后会话文件上传0051待评审.md`; this revision neither resends it nor signs for ISCP. Decision 0051 remains proposed, with ISCP review and joint fault qualification open. Unchanged frozen fields mean only that this proposal requests no new fields, not exemption from cross-project decisions.

Local source and independent preparation may continue, preserving this revision’s unreleased implementation and isolated acceptance record. If review identifies changes to deduplication keys, recovery semantics, contracts or counterpart implementation, document the change and compatibility in 0051 and obtain acceptance first; this design does not modify the counterpart. JingSi and IMMS need no new implementation because their contract entry points, authorization and data flows are unchanged. External enablement still requires paired versions, the 0051 outcome and section 6 evidence.

## 9 Out of scope

- Request completion or coreference rewriting (rejected by the owner).
- Whole-document rewriting for text formats: `text.replace_text` performs bounded replacements, so how well "improve" works on a whole md file needs a separate design. This design only guarantees that such requests are grounded correctly or reach clarification.
- Clarification buttons or other new UI elements.
- Sending externally without owner approval of the exact result and endpoint (4.6).
