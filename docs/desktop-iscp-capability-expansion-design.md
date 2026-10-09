# SparkX–SparkClaw ISCP capability expansion design

> Language: English | [简体中文](../zh-cn/docs/desktop-iscp-capability-expansion-design.md)
>
> Date: 2026-10-09. Status: P1–P5 adapters and gated desktop surfaces implemented; qualification is recorded in section 9.2. Installed deployments are unchanged.
> Scope: SparkX connects to SparkClaw through the local Docker ISCP Relay. Local Relay changes are permitted but must preserve protocol compatibility with the online Relay; this work does not switch the deployment online.

## 1. Objective, baseline and implementation order

The existing text transport and renewal evidence are in the [local ISCP integration design](desktop-iscp-connection-design.md). The current application profile, `sparkclaw.workbench.transport.v1`, contains nine operations: identity, installation bind, three presentation reads, and execution submit/lookup/cancel/ack. Files, settings, notifications, approvals, mail, browser and voice do not become available merely because ISCP is connected. The previously audited text concurrency, authorization-expiry recovery and oversized-result issues are addressed by the first implementation slice recorded in section 9.1.

Keep the same Gateway domain services, execution service, ToolHub and Policy. ISCP adds transport and explicit business adapters. Desktop non-mail history, drafts, files and schedules remain desktop-local; mail remains backend-authoritative. Do not replicate WebChat history into the desktop or create another runtime.

Review decisions, 2026-10-09: standing connection/device authorization is permanent until the user manually deletes it; local Relay changes must not create a protocol fork incompatible with the online Relay; Gateway restart terminates tasks awaiting approval, without restoring their wait or automatically continuing execution. These guide the implementation. Existing evidence for twenty-four-hour authorization and the original reference Relay describes the implemented baseline, not a rollout of these decisions.

| Order | Deliverable | Prerequisites | UI eligible for release |
|---|---|---|---|
| P1 | Capability negotiation, standing authorization/deletion, configuration, settings and basic notifications | Existing identity, binding, renewal and text transport | Qualified authorization management, settings, notification list and read actions |
| P2 | Files, images, attachments, generated artifacts and large-message chunking; Relay capacity/compatibility qualification | P1 capability, permission and request recovery contracts | Separately qualified uploads, attachments, previews and downloads |
| P3 | Approvals, execution progress and reliable event synchronization | P1 resource revisions; P2 large snapshots/artifacts | Approval cards, progress and live state |
| P4 | Mail, browser and other tools | P1–P3 and each tool's declared dependencies | Per-provider, per-browser-host and per-tool surfaces |
| P5 | Recorded transcription, realtime ASR and separately qualified realtime audio | P1–P4 plus measured ISCP streaming support | Recorded transcription, then live transcription, then playback/duplex audio |

Implement and release in order: `P1 → P2 → P3 → P4 → P5`. A predecessor's common foundation must pass before enabling its dependent phase; subcapabilities also qualify independently. An unavailable provider, mocks alone, unit tests alone or only rejection evidence cannot qualify a UI capability.

## 2. Shared contracts for all five phases

### 2.1 Application profile, operation registry and capability gates

Operation names here are a **draft SparkClaw application protocol**, not upstream ISCP standards. P1 freezes the message schemas, error codes and negotiation test vectors for `sparkclaw.workbench.transport.v2`. Current v1 validates an exact operation list; do not insert new fields or operations into its manifest. Old versions retain text mode; new versions explicitly negotiate a mutually supported profile and report incompatible versions.

Use one typed v2 operation registry for direction, version, permissions, input/output schemas, limits, idempotency, recovery, dependencies and event categories. Derive Gateway dispatch, helper validation, client mappings and contract tests from it. Do not offer an arbitrary `method + URL + headers` tunnel or trust a caller's claimed Owner/admin identity.

Obtain the capability manifest within the authenticated session. It includes profile/schema versions, deployment identity, capability revision, supported operations, authorization projection, effective limits, dependency readiness and expiry, bound to the current session and principal. Only Electron main/helper hold credentials; the renderer receives a redacted capability projection.

```text
UI enabled = client implementation ∩ server implementation ∩ current permission
             ∩ all dependencies ready ∩ qualified release ∩ rollout enabled
```

Qualification is controlled release configuration; server-advertised `supported=true` cannot substitute for it. Missing/expired manifests, changed scopes or failed dependencies disable affected actions with an explanation. Gate settings, uploads, downloads, mail reads, mail sends, browser reads, browser writes and realtime voice separately. Never remove every `iscp` guard or disable `TextOnly` wholesale.

### 2.2 Permissions and authorization renewal

For every request, the server checks Grant/peer, deployment, Owner, Client, installation, operation and target resource. Execution, mailbox and browser-host operations also verify ownership and the current authorization revision. The existing `sparkclaw.workbench.v1` permission is transport admission, not authority over settings, all files or all tools. New permissions require explicit authorization; auto-renew preserves the granted scope without expanding it.

Standing connection/device authorization has no absolute deadline and remains durable until the user manually deletes it. Short-lived Grants, Relay access/refresh credentials and capability manifests retain protocol-required finite validity and automatic renewal; credential expiry does not expire user consent or require daily approval. Task deadlines, individual approval expiry and browser task leases remain resource/action boundaries. Use explicitly versioned authorization records and renewal capability descriptions, not a distant date pretending to mean permanent or removal of upstream Grant expiry fields. Specify migration of existing authorization and revocation records; permanent authorization is effective only after both peers and the issuer pass migration acceptance.

Manual deletion takes effect when the authoritative server authorization record commits revocation, advancing its revision and retaining minimal deduplication/revocation records. Gateway blocks new requests, subsequent tool effects, approval execution, subscriptions and object delivery, and cancels cancellable work; the issuer also stops renewing the old authorization. Merely stopping renewal while existing Grants remain usable for their remaining TTL is insufficient. Reconcile a lost deletion response by the original operation; offline deletion remains pending until the server confirms it. Old Grants, cached manifests, delayed renewal responses and restarts cannot restore deleted authority. Reauthorization requires an explicit user action and a new authorization revision. Retain evidence to reconcile existing external effects rather than claiming they were rolled back.

Receipt reconciliation after deleting one's own authorization cannot require that authorization to remain valid. P1 defines a restricted control-plane entry that verifies the original device identity, deletion operation ID and digest and returns only that operation's revocation status. It cannot return business data, issue Grants or restore authority.

Subscriptions, chunks, resume and downloads remain permissioned: check on creation, recovery and before delivery/mutation; long sessions use revocation fences to block subsequent messages. Changing Owner, Client or deployment invalidates old cached actions, cursors, approvals and transfers. Separate Owner settings from deployment administration; keep administrative operations closed when the principal model does not provide that authority.

Transient authorization-control failures enter bounded backoff; valid Grants remain usable within unrevoked authority. If a Grant expires before a valid replacement is available, pause access/new effects governed by it and report temporary renewal unavailability without deleting standing authorization. After service recovery, renew automatically, revalidate and reconcile the original task without replaying unknown effects. Manual deletion closes affected capabilities without automatic restoration. Track renewal, Relay access/refresh and business retries independently rather than treating one as proof of another.

P1 freezes the authoritative authorization records, per-operation permissions and revision ownership. Gateway derives an execution allowlist from current authorization and qualified capabilities and passes it into shared runtime/Policy. Tool discovery, invocation, scheduled execution and approval continuation use the same restrictions and recheck revocation before effects. Client-declared capabilities or hidden buttons cannot replace this check; do not simply remove the `TextOnly` gate.

### 2.3 Idempotency, durability and recovery

Generate a stable `operation_id` before every mutation, with resource revision and original-input digest. The same ID/input returns the same durable result; the same ID with different input conflicts. Retain receipts for at least the advertised retry/recovery window; expiry returns an explicit terminal outcome, never a fresh replay. Transport message IDs are distinct from business operation IDs; reconnect preserves the latter.

| Confirmation layer | Meaning | What it does not prove |
|---|---|---|
| ISCP receive acknowledgment | An encrypted message was received | The business transaction committed |
| Chunk durable receipt | A verified chunk was persisted | The whole object is ready or the task completed |
| Event applied cursor | The event and its local projection committed | The user read it or decided an approval |
| Execution delivery ACK | The final result and required files are durable on the desktop | An external mail/browser write succeeded; that requires its own business receipt |

Commit business state and idempotency receipts atomically. File operations spanning storage systems use staging, a journal and recovery reconciliation. P3 adds a durable outbox. Commit desktop SQLite projections and cursors together; verify and atomically move files before acknowledging delivery. Extend memory, file and PostgreSQL implementations for every new repository contract; memory provides process-local semantics only, so restart acceptance requires a persistent backend.

Typed errors distinguish permission denial/deleted authorization, Grant expiry/temporary renewal unavailability, revision conflict, unavailable capabilities, throttling, resource limits, expired objects, cursor gaps and unknown business outcomes. Only explicitly retryable failures enter bounded backoff; unknown external writes require reconciliation. Reserve capacity for cancellation, renewal and control. Files, polling and recovery share bounded scheduling rather than each independently consuming the current four RPC slots.

### 2.4 Definition and proof of no hidden HTTP fallback

The prohibited path is **SparkX bypassing ISCP for Gateway business traffic**. In ISCP mode, configuration, execution, files, mail, browser control, events and voice cannot connect directly to Gateway HTTP/HTTPS/WS/SSE or use presigned/object-storage URLs to bypass chunking. Include main, renderer, helper, previews, downloaders and child windows; unknown operations fail closed.

Relay HTTP/WebSocket carriage, restricted enrollment/issuer renewal control calls, and authorized backend model/mail/ASR requests or browser visits to user-selected sites are separate legitimate network purposes. Classify tests by process, destination, route and purpose. Do not classify all TCP/HTTP as fallback, or let a broad domain allowlist hide business traffic. Same-origin control-plane exceptions must identify exact routes.

Run two topologies in every phase: normal ISCP, and desktop-side blocking of all Gateway business/download addresses while preserving Relay, necessary control traffic and controlled tool access. Both must produce the same business result. Then interrupt Relay and verify no HTTP rescue attempts. Collect outbound attempts across processes plus ingress observations: `direct_gateway_business_attempts=0`, including blocked attempts. An injected HTTP-adapter counter is useful evidence but does not replace native-app network-boundary observations.

Manually selecting HTTPS is a separate visible connection mode with renewed identity/data-ownership checks; failure does not automatically switch modes. Every new operation uses the ISCP transport. Reusing a Gateway handler/domain service in process does not mean issuing a hidden HTTP request.

### 2.5 Local Relay changes and online compatibility

Local Docker Relay changes may cover polling/continuous delivery, throttling, queues, backpressure, fair scheduling and persistence. Preserve the online Relay's ISCP enrollment, PoP, encrypted envelopes, routing and credential semantics. The online Relay must not need to interpret SparkClaw business operations, and clients must not require private local routes or fields. Enable optimizations only through compatible capability negotiation; otherwise retain standard ISCP carriage and its effective limits. Keep any capability that cannot meet acceptance disabled, without falling back to Gateway HTTP.

Before P2, record client/helper/SDK/local Relay versions, modifications and a verifiable online Relay version/protocol contract. Use the same client and application profile against the modified local Relay and an online-compatible baseline in phase order: P1 covers enrollment, handshake, renewal-related access, reconnect and error semantics; P2 adds chunks, P3 events and P5 audio. Earlier phases must not depend on unimplemented later operations. A matching implementation and contract vectors may provide isolated online-compatibility evidence; only actual online tests qualify an online deployment. Do not claim compatibility without verifiable evidence. Keep the local integration deployment; implementation changes affecting other projects or shared upstream contracts still follow repository coordination rules.

## 3. P1: Configuration, settings and basic notifications

### 3.1 Scope and operations

Inventory every actual settings component's reads, mutations and status dependencies. Record UI item → operation → permission → evidence of effective behavior; keep unmapped items individually disabled.

| Draft operation family | Business behavior | Permission/result contract |
|---|---|---|
| `capabilities.get`, existing `presentation.*` | Configuration, Owner, service readiness and capabilities | Current-principal projection without secret values |
| `settings.get/update`, `owner.update` | Language, Owner preferences, supported runtime parameters and connector settings | Resource-specific authority; `expected_revision` writes return stored/effective revisions |
| `credentials.status/set/delete` | Credential status, replacement and removal | Secrets are write-only; deployment credentials require explicit administrative permission |
| `authorizations.list/delete` | Inspect standing authorizations and manually delete them | Within the current principal's authority; deletion carries authorization revision/idempotency ID and returns authoritative revocation, not merely local credential removal |
| `notifications.list/read/read_all` | Paged notifications, read markers and unread count | Owner isolation; `read_all` uses the submission watermark and cannot consume later notifications |

Local themes, drafts and desktop settings still use local storage. Connection-affecting settings first persist recoverable state and then rebuild the connection without losing original execution tracking.

P1 notifications use bounded pagination and low-frequency polling, with explicit resource revisions/snapshot tokens. They do not depend on event streaming. P3 later wakes the same reader through events. Receiving a notification, obtaining OS permission to display it and marking it read are separate states. Approval-notification actions stay disabled until P3.

After a settings mutation, verify the actual runtime uses the new value. If restart is necessary, return `restart_required` and the pending effective revision rather than claiming immediate application. Verify credential usability with a controlled real-service request; log only credential identifiers and outcomes. Updating tool policy does not enable an unqualified tool.

### 3.2 Acceptance before enabling UI

- **Permissions:** cover Owner/admin separation, cross-Owner reads, forged Clients, secret readback and post-revocation writes. Legitimate principals must successfully perform the corresponding settings operation.
- **Standing authorization:** retain consent across the previous twenty-four-hour boundary, multiple Grant cycles and peer/issuer restarts; label shortened-cycle and controlled-clock evidence separately. Old Grants, delayed renewal responses, caches and restarts cannot restore deleted access. Reconcile concurrent/lost deletion responses, offline pending deletion and explicit reauthorization; migration cannot resurrect revoked records.
- **Business:** saved settings, post-restart reads and runtime behavior agree. Display at least one notification generated by real business activity; mark it read and reconcile the backend unread count. An old `read_all` must not consume newer notifications.
- **Recovery:** lose responses before/after commit, repeat operations, conflict revisions and restart client/Gateway. There is one final mutation; interrupted notification pagination neither loses entries nor duplicates read effects.
- **Transport:** settings and notifications still work with direct Gateway access blocked. Relay loss reports unavailability. Enable individual settings/notification UI only after zero bypass attempts.

## 4. P2: Files, images, attachments, artifacts and chunking

### 4.1 Shared object and chunk protocol

Add `transfer.open/status/chunk/commit/abort` and `object.describe/read/release`. Large contexts, final execution JSON, ordinary files, images, mail attachments and later event snapshots share a bounded object transport. Small control messages remain RPC; large bodies reference committed objects. Declare object purpose explicitly; never expose arbitrary server paths.

| Record | Minimum fields and invariants |
|---|---|
| Object manifest | `object_id`, resource ownership, purpose, name, media type, total bytes, full SHA-256, version and retention deadline |
| Transfer | Stable `transfer_id`, direction, object version, authorization binding, chunk size, credit window and expiry; independent of session ID |
| Chunk | `transfer_id`, index, offset, length, SHA-256 and bytes; no overlapping/out-of-range regions or conflicting bytes at one position |
| Checkpoint | Durably acknowledged ranges/chunks, acknowledged bytes and state; bounded pagination, never an unlimited bitmap |
| Commit receipt | Full-object hash, size, version and persistence result; repeated commits return the same result |

Receiver-issued credit bounds in-flight data; senders cannot exceed it. Replenish credit only after verification and persistence. Bound object size, per-Owner/global disk usage, concurrent transfers and idle lifetime. After reconnect, reauthorize and query the checkpoint, sending only missing chunks. Repair failed chunks; a mismatched whole-object hash cannot commit. Cancellation/expiry cleans temporary files, quotas and references; recovery journals reclaim crash orphans.

Candidate defaults are 8 KiB raw chunks and two in-flight chunks per transfer. These are proposed transport defaults, not business limits. Before release, calculate the fully encoded size through SDK encryption, JSON/base64, helper IPC and actual Relay envelopes, test the boundary and reduce chunks if necessary. The existing 64 KiB plaintext cap is not a Relay net-payload guarantee. Never put a whole base64 file into one RPC.

Qualify Relay capacity in P2. The current reference's shared 120 requests/IP/minute allowance and roughly one-second polling consume both file and control budgets; small-file success cannot freeze chunk parameters. On target networks, measure throughput and completion time for each purpose's maximum permitted file, plus concurrent chat, cancellation, renewal and notification latency. Freeze pass thresholds beforehand and record throttling/retry overhead. Adjust the local Relay under section 2.5 or negotiate lower effective limits; do not defer carriage improvements until P5.

Preserve [workbench-limits.json](../configs/workbench-limits.json) and domain-service budgets: generic `fileBytes` is currently 64 MiB, but each execution input file and the total execution result are bounded by the 8 MiB `ResultBytes` contract, with at most 32 result files. The generic file cap is not the execution attachment cap. Context is currently bounded at 1 MiB. Advertise the effective minimum per purpose; chunking does not increase these budgets. Oversize results must produce a visible terminal outcome rather than endless lookup.

### 4.2 Business and UI integration

Finish verifying/committing uploads before submitting an execution that references their versions and digests. Preserve the original request ID. Transfer original serialized large inputs and validate their byte digest; re-encoding JSON must not change request identity. In v2, execution lookup returns state plus a result manifest. Download large results/files in chunks and send the original execution ACK only after every required result is durable in SQLite/files.

Persist distinct desktop states for uploading, objects committed/execution not submitted, unknown submission outcome and accepted execution. Upload recovery is not execution retry; do not consume the execution submission marker before upload. Perform permission/capacity admission before persisting the boundary at which sending becomes possible. When the journal proves no execution submission was attempted, resume uploads and continue the original user intent/request ID; scheduled tasks still obey the rule against catching up offline occurrences. After the possible-send boundary, only reconcile the original request: lookup 404 cannot prove no execution or authorize automatic resubmission. Inject crashes after object commit/before submit and around the sending boundary.

Use staged files, full hashes, atomic rename and a local commit journal to handle cross-storage transactions. A crash after rename but before ACK reconciles the files and resends ACK, never reruns generation. Pin objects while execution delivery is outstanding; after the recovery window, apply an explicit result-expiry policy rather than silently deleting data still promised to be recoverable.

Preview through a controlled local scheme/cache. Validate media type, image dimensions and decoder budgets; reject traversal, arbitrary HTML scripts and remote resource loads. A local download destination does not change remote-object permissions. File delivery, image preview, model image understanding and generation tools are separate capabilities; successful image transfer does not prove model image support.

P2 qualifies actual execution artifact commit, persistence, download and ACK paths using reproducible generated examples through the same artifact-commit entry. It does not enable all generation tools. Approval-requiring file actions wait for P3; general document/image-generation tools and their real invocation surfaces wait for P4. Delivery of existing artifacts can qualify independently, avoiding a reverse dependency from the transfer foundation onto every tool.

### 4.3 Acceptance before enabling UI

- **Permissions:** reject cross-Owner/installation/execution references, unauthorized downloads, forged paths, expired transfers and post-revocation resume/commit. Legitimate uploads/downloads must succeed.
- **Business:** byte/hash equality for empty and multichunk files, images, multiple attachments and persisted artifacts. Cover exact limits, oversize objects, corrupt chunks, forged media types and full disks. Compare execution manifests to actual local files, not filenames alone.
- **Recovery:** missing, reordered and repeated chunks; lost commit responses; peer restarts; Grant renewal and replacement sessions transfer only missing data. Commit/generation/delivery occur once. Cancellation releases space/quota; result expiry is visible.
- **Transport:** block Gateway file/artifact routes and remote-image URLs while uploading, rendering and downloading successfully. Enable file selection, image previews, attachments and artifact downloads independently after zero bypass evidence.
- **Capacity and compatibility:** each purpose's maximum file meets the frozen completion-time and control-response thresholds; the same client passes the online-compatible baseline in section 2.5. Success only on the modified local Relay cannot enable a capability.

## 5. P3: Approvals, execution progress and event synchronization

### 5.1 Unified event model

Add `events.subscribe/resume/ack/unsubscribe`, `state.snapshot` and `approvals.list/get/decide`. Preserve execution lookup/cancel/ack semantics. Events include `event_id`, authorization scope, stream generation, per-stream increasing sequence, resource ID/revision, type and bounded content/object references. Promise ordering within a scope, not an invented global order across Owners.

Persist business state and outbox records atomically; deliver events at least once. Deduplicate on event ID/resource revision. Commit projections and applied cursors together before acknowledging. Slow consumers use credit, pagination and limits without blocking execution. Publish log retention and maximum supported offline duration as configuration; expired cursors return `cursor_gap`.

Subscriptions must not permanently occupy ordinary RPC slots; use bounded logical event channels and a separate control budget. The reference Relay may continue carrying these reliable events through queue polling. Record actual notification latency rather than claiming audio-level responsiveness. Business state must still commit without a subscriber and remain recoverable through lookup/snapshots.

Recovery reauthorizes and resumes from the last durable cursor. A gap obtains a snapshot with a consistent watermark, atomically replaces the corresponding remote projection, and consumes subsequent events. Avoid a read-then-subscribe race. Large snapshots use P2. Reset only relevant remote state, preserving local drafts/history. Replace P1 notification polling with event wakeups plus periodic reconciliation in the same state machine, not a second independent synchronizer.

### 5.2 Approval and execution semantics

Bind approval decisions to original execution, approval ID, action/argument digest, revision, expiry and actor. A visible button is not authorization: the server rechecks pending state, unchanged content and current permission, then atomically commits one decision. The same decision replays its existing result; conflicting decisions or stale revisions conflict. Expiry, revocation and execution cancellation cannot permit execution.

Gateway restart terminates tasks awaiting approval, without restoring their wait/continuation or automatically resubmitting them. Persist a minimal waiting phase/approval identity/digest before displaying approval. Before accepting new decisions after restart, durably terminate leftover waiting tasks with a queryable restart reason and invalidate their old approvals. Desktop lookup/events update the card to explain termination by Gateway restart. Desktop-only disconnect/restart while Gateway remains running may revalidate and display the original valid wait.

Resolve approval-decision versus restart/termination races by durable revision, rejecting stale buttons and delayed decisions. If a decision committed but its execution outcome is unknown, preserve the decision receipt and external-effect reconciliation records. Termination does not mean no earlier effects occurred and never authorizes rerunning from the beginning. Restart continuation is no longer a deliverable; do not persist full execution context to restore approval waits. A separately initiated user task needs a new request ID and approval; changed arguments still need a new digest. Effects continue through shared Policy/execution services.

Progress may include stages, tool state, partial text and file preparation, but the last progress event is not completion. Durable execution results define terminal state; P2 persistence precedes delivery ACK. Cancellation/completion races converge by server revision; old events cannot revert a terminal state to running. Lookup must recover authoritative state when progress is lost.

### 5.3 Acceptance before enabling UI

- **Permissions:** reject cross-Owner subscriptions/decisions, cursors from previous principals, expired or modified approvals and continued subscriptions after revocation.
- **Business:** a controlled real approval-requiring operation executes once after approval; rejection/expiry has no effect. Progress/final results agree with backend state; cancellation actually stops cancellable work.
- **Recovery:** reconcile lost decision responses, Gateway restart while awaiting approval and races with decision commit. Restart termination remains queryable; old approvals cannot execute, and no continuation or replacement task starts automatically. Desktop-only restart does not terminate a valid wait. Reordered/duplicated/missing events, crashes around ACK, cursor gaps and new events during snapshots cannot repeat decisions or execution.
- **Transport:** approvals, progress and replay work with Gateway SSE/WebSocket blocked. Enable approval actions and live-state UI only after zero bypass evidence.

## 6. P4: Mail, browser and other tools

### 6.1 Mail

Use `mail.mailboxes/sync/message/attachment` and separately controlled `mail.send`. Reuse mail sync projections, cursors and binding generations. The backend remains authoritative; desktop caches belong to the current principal. Page lists, fetch bodies on demand, transfer originals/attachments through P2, and synchronize changes/deletions/binding revocation through P3. A synchronization gap cannot appear as an empty inbox.

Preserve provider evidence, retries and deduplication from the [incremental mail design](email-timeline-incremental-sync-design.md) and [original-storage design](email-local-download-storage-design.md); changing transport does not rewrite collection. Rich previews still suppress remote images, trackers and scripts.

Inventory network entry points in mail collectors, browser-extension background processes, host brokers and download callbacks, not just workbench fetch. Desktop-side collection results, attachments and receipts also reach Gateway through the same ISCP adapter. Register provider access and controlled local IPC separately by purpose.

Authorize reading mail, writing local drafts and sending separately. Bind recipients, body and attachment manifests to send approval; any later change invalidates it. Persist provider-verifiable receipt identifiers. After losing a response to a submitted send, reconcile first. If non-delivery cannot be established, show an unknown outcome and never automatically resend. Qualify each provider's reads, attachments and sending independently rather than using one mail-support flag.

### 6.2 Browser

Use `browser.host.grant/revoke`, `browser.command/receipt/reconcile` and browser-state events. Gateway sends restricted reverse commands to the desktop host through the same authenticated ISCP session. This direction has its own schema and permission allowlist; the responder does not gain arbitrary computer control.

Reuse the browser-host broker, Owner Controller, task-tab ownership and write fences. Bind grants to host, installation, conversation/task, permitted actions, tabs and expiry. Screenshots/downloads/exports use P2; progress, authorization and command state use P3. Gate browser reads, login authorization and external writes separately; revocation stops subsequent commands. Existing desktop URLs, cookies and sessions are not default authority for arbitrary tasks.

Reconcile lost command results using the original command ID/fence, never replaying unknown sends/orders. Reloading a page alone is not write reconciliation. Disconnect releases control leases; reconnection cannot automatically control tabs without confirming ownership again. Authorized page traffic is tool activity, while desktop–Gateway control and screenshot uploads remain ISCP-only.

### 6.3 Other tools

Derive tool capability declarations from the existing typed ToolHub registry, including settings/credentials, objects, approvals, events and host dependencies. Adapters call the shared runtime/Policy; do not create another executor or expose arbitrary shell/URL/filesystem RPC. Text results, large artifacts and external effects retain their respective recovery rules.

Qualify at least read-only, file/document, external-write and host-tool categories. Passing one tool never enables its entire category: a query tool does not qualify mail sending or document generation. Unconfigured providers and tools without verifiable business output remain disabled.

### 6.4 Acceptance before enabling UI

- **Permissions:** reject cross-mailbox access, stale bindings, unauthorized browser hosts/tabs, denied tools and approvals reused after content changes. Read-only principals cannot acquire writes through tool invocation.
- **Business:** each advertised provider synchronizes a real mail and attachment; send to a controlled test mailbox and verify actual receipt. On the actual browser host, read a page, capture a screenshot and perform one approved reversible write with verified page state. Verify each tool's real files/data/provider receipts; blocked/mock outcomes do not count as positive evidence.
- **Recovery:** reconcile interrupted mail pagination, attachment downloads, browser-command receipts and external-write responses across peer restarts/revocation using original business IDs. Surface unknown external results without repeating sends/actions.
- **Transport:** preserve provider/controlled-site access while blocking Gateway business ingress. Mail, browser-host connections, screenshots and tool results have no direct HTTP/WS path. Enable per provider/host/tool.

## 7. P5: Voice and realtime audio

### 7.1 Recorded transcription before realtime streaming

P5a adds `speech.status/transcribe/cancel`. User-started recording uploads through P2 into an authorized transcription task, follows P3 state and receives actual ASR text. Microphone authorization requires OS consent, the trusted workbench window and the current connection capability; background pages/ordinary browser tabs do not inherit it. Transcripts enter the draft without sending chat. Configure recording duration, format, size and deletion explicitly.

P5b adds `audio.session.open/control/close`, sequenced/timestamped audio frames and `speech.partial/final` events. Negotiate codec, sample rate, channels, frame duration, window, buffer limits, idle timeout and deadline. Give control, audio and bulk files separate capacity budgets. Partial revisions replace earlier snapshots; final is authoritative for the same ASR session, not appended duplicate text.

The pinned reference Relay currently drains its queue, closes, and is polled again roughly one second later. Neither P2 capacity improvements nor existing text acceptance automatically establishes continuous low latency, duplex streaming or fair scheduling. Before P5b, verify or implement continuous send/receive, backpressure and priorities in local Docker under section 2.5's online-compatibility rules, recording SDK/Relay versions and changes. If qualification fails, realtime UI stays disabled; no private Gateway WebSocket bypass. P5a can qualify independently of streaming.

### 7.2 Audio interruption and recovery

Files require reliable completion; realtime playback frames expire, so discard late frames. If an ASR input gap cannot be repaired, explicitly stop/degrade that recording rather than treating missing audio as a complete utterance. Disconnect immediately stops capture and releases the device. Do not automatically reopen the microphone; reconcile only the ended session's final. New recording requires a fresh user action; old session IDs/frames cannot enter a new session.

Do not reuse the existing WebChat voice HTTP batch fallback in ISCP mode. Only when P5a is qualified, a complete recording is saved and the user understands the recovery behavior may the same recording receive one file transcription over ISCP. Label it recorded transcription, not realtime recovery. Incomplete recordings require a clear explanation, not a purported complete final. Transcription operations and draft insertion are idempotent; recovery never submits chat again.

P5c gates TTS playback/duplex audio separately. Establish actual provider support, then verify first audio, playback, interruption and stale-frame cleanup. ASR acceptance does not establish voice conversation support. Business permission, OS audio permission and visible capture/playback state must agree. Audio content stays out of Relay logs; temporary recordings expire under the disclosed retention policy.

### 7.3 Acceptance before enabling UI

- **Permissions:** reject OS denial, untrusted windows, absent speech scope, capture during revocation and cross-session frame injection. The physical microphone indicator turns off after stop/disconnect.
- **Business:** real microphone and real ASR emit partial text before stop and a same-session final afterward. Record accuracy, long-form, noise and language coverage against fixed corpora. Qualify complete-file transcription separately for P5a, and actual speaker playback/interruption for P5c; received packets do not prove audible output.
- **Recovery:** cover loss around recording/finish/final, Grant renewal, jitter, slow consumers, network outages and system sleep. Never restart recording automatically, play stale frames, insert duplicate finals or send chat twice.
- **Transport:** disable Gateway speech HTTP/WS and remote-audio URLs, verify the entire path and account for audio bytes in both directions. Gate recording, realtime ASR and playback/duplex independently after zero bypass evidence.

Initial realtime targets, to freeze against target hardware before implementation: p95 first partial during recording ≤ 2 seconds; p95 finish-to-final ≤ 3 seconds; unacknowledged audio target ≤ 2 seconds with a hard 5-second cap; local capture/playback stops within 300 milliseconds of stop/cancel. Record corpus, devices, model, network and at least 30 normal samples. A breach fails or explicitly degrades; never grow buffers without bounds. Report recovery timing separately from normal latency. TTS/duplex additionally requires frozen end-to-end playback/interruption metrics.

## 8. Code ownership and deliverables

| Location | Planned work |
|---|---|
| `services/gateway/internal/iscpworkbench` | v2 operation registry, negotiation, typed errors, chunks/windows, stream multiplexing and session recovery, separated from business logic |
| `services/gateway/internal/iscpbridge`, `internal/iscplocalissuer`, `cmd/iscp-workbench` | Reuse SDK identity; implement standing authorization, short-lived Grant renewal and revocation consistency after deletion; extend bounded helper IPC and redacted diagnostics |
| `services/gateway/internal/gateway/workbench_iscp.go` | Explicit operation-to-domain adapters and per-request authorization; never a generic HTTP proxy |
| `services/gateway/internal/execution` and domain repositories | Object references, business idempotency, minimal approval-wait records/restart termination, outbox/snapshots and external reconciliation; all three stores |
| `docker/images/iscp-relay-local.Dockerfile`, `scripts/lib/iscp-docker-lab.mjs` and compatibility fixtures | Pin local Relay changes/versions, qualify throughput and online protocol compatibility, and distinguish isolated compatibility evidence from online deployment acceptance |
| `apps/desktop/src/main/iscp-transport.mjs`, `desktop-auth.mjs` | Capability projections, scheduling, recovery state and strict transport selection |
| `apps/desktop/src/main/client-store.mjs`, `execution-client.mjs` | Transfer checkpoints, file commit journals, event projections/cursors and original-request recovery |
| `apps/desktop/src/main/mail-sync-*.mjs`, browser-host/permission modules | Reuse mail caches and host authorization; replace Gateway network paths and add audio permission checks |
| `apps/webchat/src/desktop/LocalWorkbench.tsx` and shared components | Enable individual surfaces from one capability projection; show failure/recovery/unknown outcomes and remove superseded hardcoded guards |

Each phase delivers operation/scope/error inventories, version/storage migration plans, both peer implementations, normal/fault replay cases and native UI acceptance. Migrations preserve local history and original-request tracking. Rollback first disables new capabilities; old code must not read incompatible state, and rollback must never switch the connection to HTTP.

## 9. Shared acceptance record and release rules

For each capability record client source/package hashes; helper/SDK/Relay/Gateway versions; profile; redacted principal/permissions; business operation/request IDs; expected/actual results; durable state/file hashes/provider receipts; injected fault point; recovered state; business execution counts; per-process bypass attempts; and UI gate outcome. Public evidence must exclude credentials and content bodies.

| Gate | Required evidence | If missing or failed |
|---|---|---|
| G1 Permissions | Real successful authorized requests; standing consent does not expire automatically; reject absent valid Grants, deleted authorization and unauthorized recovery | Capability remains closed |
| G2 Actual business | Native SparkX through local Docker Relay to real Gateway with verifiable durable/external results | Mocks/isolation remain prerequisite evidence only |
| G3 Disconnect recovery | Convergence across commit/persistence boundaries, restarts, renewal and revocation, without duplicate effects | UI stays closed with diagnostic state |
| G4 No HTTP fallback | Positive results with business addresses blocked; no bypass on Relay failure; zero attempts across processes | Entire capability fails qualification |

Run contracts/fault injection, then isolated Docker Relay integration, then positive business acceptance in installed native SparkX. Always test the default file backend; cover every backend for new store contracts. Controlled test configuration may expose candidate capabilities, but user-facing release flags require all four gates and must not ship enabled merely because a test flag existed.

All P1–P5 additions begin as `planned`. Record progression per capability: `implemented → isolated_verified → native_verified → enabled`; regressions immediately disable affected capabilities while preserving data. Reconcile suspended/revoked tasks through existing business state, never hidden retries or transport switching. If the deferred text defects recur in a release gate, they still fail that gate; this roadmap does not waive them.

## 9.1 Initial implementation record, 2026-10-09

This slice implements prerequisites and part of P1; it does not complete P1 or qualify new UI capabilities.

| Area | Implemented and isolated evidence | Remaining release work |
|---|---|---|
| Standing authorization | Issuer state v2, explicit permanent policy, pinned helper profiles, signed proof-bound status, bounded Grants and revocation fence; controlled clock crosses two years, restarts and shortened-Grant migration tested | Product authorization management, deletion operation receipts/offline reconciliation, explicit reauthorization and installed peer migration |
| Text recovery | Provably unsent admission retains the original draft; upload staging precedes submission; short Grant expiry retries; lookup 413 becomes durable `delivery_too_large` with a visible explanation | Native regression acceptance; no chunking or new file operation is enabled |
| Approval shutdown | Minimal durable approval records, persist-before-continuation decisions, terminal `gateway_restarted_awaiting_approval`; previous approved effects stay `unknown` with receipts | P3 transport operations, events and approval UI remain closed |
| Revocation | Fresh issuer status at dispatch/delivery, in-flight cancellation and same-lock Client admission fence, persisted original-request terminal state | General per-tool side-effect fencing, subscriptions and object delivery qualify with their later phases |

Fresh labs use `-authorize-renewal -authorization-hours 0` and both helper profiles pin `authorization_lifetime: until_revoked`. Existing v1 bounded authorization remains readable and is never silently migrated on service startup. Explicit migration preserves the current signed Grant TTL, including a shortened final Grant, and refuses revoked records. Renewal receipts remain recoverable until seven days after their Grant expires; stale proofs cannot issue a second Grant after receipt cleanup. The issuer CLI `-revoke-renewal` keeps a durable tombstone and advances its revision once.

The private issuer route `/v1/authorization-status` is bound to a fresh device proof, request digest and pinned issuer signature. It returns authorization state only, including after revocation; it is not the planned deletion-operation receipt API. Unsigned errors cannot permanently revoke consent. Renewal and status pacing are separate. New business dispatch/delivery pauses if a current status check fails; background standing-authorization checks run at most ten seconds apart, apart from request duration or status-endpoint backoff. This is a conservative local text implementation, not a claim of atomic revocation across all future tools.

Execution control storage migrates v2 to v3. **Old binaries cannot read v3.** Do not roll back by discarding the ledger or restoring older request fences; use a compatible reader/migration. Issuer state v2 likewise requires the updated issuer. Neither storage change modifies the upstream Grant, Relay envelopes or exact v1 operation manifest. No local Relay source or SDK dependency changed in this slice.

Validation: focused authorization/execution/Gateway race tests; 158 desktop tests; 220 WebChat tests and production build; six local-lab cases including real pinned Docker Relay, actual Gateway execution, helper reconnect and zero direct Gateway HTTP calls. The Docker model is explicitly mocked. Controlled-clock permanence and injected-network revocation tests are not a real two-year soak, native-app acceptance or online Relay acceptance. Isolated Linux full Go tests and Go build/vet passed; Linux validation covers the macOS baseline failures caused by missing `/dev/shm` and symbolic-link paths.

This was the initial checkpoint; section 9.2 supersedes its pending implementation list. Existing installed SparkX and running deployments were not upgraded. InfiniCenter was unavailable at the configured ancestor anchors, so no cross-project contract or central status was modified.

## 9.2 Full implementation and qualification, 2026-10-09

This is the initial full implementation checkpoint. Section 9.3 supersedes its
pending browser, authorization-deletion and attachment-send qualifications.

P1–P5 use the same existing domain services; there is no second execution engine or arbitrary HTTP tunnel. The canonical inventory is `services/gateway/internal/iscpworkbench/operations.json`; `node scripts/sync-iscp-operations.mjs --check` verifies the desktop projection. Both private helper profiles must explicitly select `["sparkclaw.workbench.transport.v2", "sparkclaw.workbench.transport.v1"]`. `qualified_capabilities` contains individual operation names and defaults to empty. Signed authorization scopes are independent of that deployment qualification. v1 remains exactly nine operations, including when an old configuration and original Grant connect to a new responder.

| Phase | Implemented | Evidence and remaining qualification |
|---|---|---|
| P1 | Session-bound expiring manifests, signed exact scopes, permanent own-device authorization, proof-bound deletion/reconciliation, explicit reauthorization, Owner/connector/credential CAS, encrypted mutation receipts, notification watermarks | Real issuer/crypto/default file Gateway tests; native settings UI save/read and Keychain; no provider credential usability is claimed without a configured provider |
| P2 | Shared private object store, 8 KiB chunks/two credits, hashes, resumable durable checkpoints, bounded quotas/expiry, execution input/output and large typed JSON | Real local Docker Relay 8/64 MiB capacity test; signed Grant expiry/renewal during partial upload; native local file upload/execution result/ACK; modified and unmodified reference Relay compatibility |
| P3 | Durable approval revisions/digests/decision receipts, restart termination, bounded persistent event window, encrypted cursor/outbox, persist-before-ACK SQLite projection and cursor-gap reset | Approval/reject/replay/restart and response-loss tests; actual governed document read/edit/artifact tests; event epoch, lost ACK and retention-gap tests on repository backends |
| P4 | Backend-authoritative mail cache/attachment reads/versioned drafts and explicit send confirmation/reconciliation; existing Browser Broker over ISCP polling; exact per-tool allowlist into shared ToolHub/Policy | Real document tools; controlled mail sink with durable receipts; native Chromium read/fill/click/screenshot and lost-write fencing. Actual mail providers and browser presentation configurations qualify separately. The existing mail provider cannot send attachment manifests, so attachment sending remains explicitly unsupported |
| P5 | Recorded WAV objects with durable request reconciliation/cancel; bounded PCM streaming frames, provider ACKs, partial/final/cancel events and revocation cancellation | Controlled provider protocol tests; live microphone/corpus/provider latency gates still require that deployment's configured ASR. Playback/duplex has no existing provider and explicitly reports `provider_unsupported`; it remains disabled |

Standing authorization deletion uses helper `authorization_delete` / `authorization_delete_receipt` control IPC and subject-device PoP against the pinned issuer. It continues to reconcile after the Relay/Grant is unavailable. `authorizations.list` reports only the current device's fresh signed policy; the Gateway cannot impersonate the subject device to delete authorization. Generic `authorizations.delete` returns `device_authorization_control_required`. The desktop persists the original deletion ID and expected revision before sending. A new authorization requires the issuer operator's explicit `-reauthorize-permanent -expected-revision … -operation-id … -authorization-scopes … -grant-file …`, both peers importing its new Grant, and the user's **Check newly installed authorization** action. Neither renewal nor restart expands scopes or restores deleted consent.

Recorded capacity on local Docker Relay (one upload/download per size; control p95 is sampled throughout):

| Payload | Upload | Download | Control p95 |
|---|---:|---:|---:|
| 8 MiB | 38.63 s | 44.57 s | 41.49 ms across both sizes |
| 64 MiB | 353.40 s | 403.58 s | same control sample series |

Both downloaded SHA-256 values match their inputs. The predeclared limits were 90 seconds per 8 MiB direction, 600 seconds per 64 MiB direction and 2,000 ms control p95. The first 64 MiB attempt exposed a missing capability-refresh worker after five minutes; an elapsed-time regression and a full rerun now pass. No capacity threshold was relaxed. Chromium NetLog and native HTTP/WS blocking observed zero direct business attempts during the native settings/file/event/reconnect fixture.

Validation: 65 Linux Go packages passed (six additional packages have no tests), Go build/vet, focused race checks, real PostgreSQL 18 CAS/event-window/reopen tests, 186 desktop tests, 227 WebChat tests/build, both generated contract checks and 110 bilingual documentation mirrors. The native application test uses Electron 44.4.3 / Chromium 152.0.7977.130. The unsigned Mac arm64 candidate ZIP was built without replacing the installed application. Its SHA-256 is `f9de06ab7f5b8bfa4d272b27feb8ba795d407f61127dfdbdb046f337b974aff1`. Native business tests retain their explicitly controlled model/provider scope.

The separate native Browser Host fixture uses real `WebContentsView`, the production Broker and adapters, and encrypted host polling through the pinned issuer/local Relay. It read and filled the controlled page, returned an 18,609-byte PNG, then lost a click reply after the actual effect. Both journals retained one `unknown`; repeating that command was rejected and the click count remained one. Direct Gateway HTTP and Host WebSocket attempts were zero. On macOS the fixture explicitly activates the app/dock and disables occluded-window and renderer backgrounding so its compositor produces frames. This qualifies that foreground fixture configuration; it does not establish screenshot behavior under the production app's default background settings. The machine-readable [qualification summary](evidence/iscp-expansion-2026-10-09.json) records these limits with source/artifact hashes.

Storage and recovery: execution control v3, issuer state v2, desktop SQLite v8 (migration from v6/v7 preserves local history). Old binaries must not open newer ledgers. Objects and execution results retain a 24-hour recovery window; in-progress transfers expire after one hour. Domain receipts use an encrypted bounded journal (65,536 records / 512 MiB), reserving result space before effects. Generic files/mail attachments allow 64 MiB, execution inputs/results 8 MiB, execution context 1 MiB and task staging 32 MiB. Recorded audio is bounded by the configured ASR upload limit and 25 MiB. Native screenshots retain their existing 64 KiB capture limit and 96 KiB reply-JSON limit. Manifests expose effective limits, rather than treating generic file capacity as every domain's capacity. Revocation rejects subsequent delivery immediately at the authorization fence; disk objects then expire under the bounded janitor.

Local Relay optimization is opt-in with `prepare-expansion --capacity-relay`. The builder copies the checksum-locked ISCP `v0.2.0-rc.1` source into the private lab, records before/after source and binary hashes, and changes only local-lab scheduling/rate limits. It does not edit the module cache, SDK, PoP, enrollment, envelopes, routes or message/drained frames. The stream detects closed peers so an abandoned connection cannot consume later messages. The unmodified reference Relay passes v2 settings/chunks/reconnect and exact-v1 checks. This is isolated protocol compatibility evidence; an online deployment has not been tested or changed.

Reproducible checks (all paths below are disposable private lab directories, never an existing deployment):

```sh
node scripts/build-iscp-helper.mjs
node scripts/iscp-local-lab.mjs prepare-expansion --input /private/path/input.json --directory /private/path/lab --capacity-relay
node scripts/iscp-local-lab.mjs up --directory /private/path/lab
node scripts/iscp-expansion-capacity.mjs /private/path/lab
node scripts/iscp-expansion-compatibility.mjs /private/path/reference-lab
SPARKCLAW_ISCP_NATIVE_LAB=/private/tmp/native-lab node_modules/.bin/electron scripts/iscp-native-expansion-fixture.mjs
node apps/desktop/test/run-host-iscp-native-qualification.mjs /private/tmp/browser-host-lab
```

Prepare a separate unmodified `prepare-expansion` lab for compatibility, and a fresh capacity lab for native UI. The Browser Host runner requires its own prepared capacity lab with the lab Gateway stopped; it starts a real Gateway test process, while its pinned HTTPS listener serves only the controlled page and test-driver routes. One Client is bound to one persisted installation; do not reuse one lab concurrently across unrelated desktop stores. The native workbench fixture imports the real application entry point, operates the settings UI and sandboxed IPC, saves a screenshot/Chromium NetLog and checks execution delivery with Gateway business ports unpublished. It also blocks native direct HTTP/WS, interrupts Relay and verifies reconnect without resubmission. Its model is explicitly mocked; it does not qualify semantic model output, mail-provider behavior or microphone recognition. Candidate lab flags are not production release flags.

Release state: implementation is available for final review in source and isolated candidates. Provider-dependent mail/ASR, the deployment's actual browser presentation configuration and each individually governed tool require their recorded positive business evidence before production enablement. Unsupported mail attachment sending and TTS/duplex stay closed; audio is not silently replaced by ASR or HTTP. Existing installations, remote deployments and online Relay remain untouched. InfiniCenter is still absent at the configured anchors; no central acceptance or shared upstream protocol change is claimed.

## 9.3 Non-audio follow-up and workspace-only attachments, 2026-10-09

The user deferred ASR and TTS acceptance. Their existing provider gates remain
closed; passing general regression tests does not qualify microphone recognition,
playback or duplex behavior. The remaining non-audio implementation and local
acceptance follow-up is recorded in
[the qualification evidence](evidence/iscp-workspace-mail-2026-10-09.json).

**Boundary correction:** workspace means the originating SparkX installation's
desktop-local workbench data, as already defined in the [convergence
design](workbench-runtime-convergence-design.md). It does not mean the Gateway
Owner workspace. The earlier path-based implementation and its attachment
acceptance evidence are superseded; this was an implementation error, not a new
product boundary requested by the user.

The mail editor selects existing, scoped local file IDs from
`<userData>/workbench/files`. The main process resolves those IDs through
ClientStore, verifies the recorded name, size and hash and uploads the bytes using
the existing encrypted ISCP object transfer. Arbitrary paths, unowned files,
symlinks, hard links and nonregular files are rejected. At most five files,
totaling 10 MiB, may be attached. The saved draft includes the local file ID,
verified object reference, name, size and SHA-256. Confirmation binds that saved
version and manifest; main verifies the local file again before sending. A
changed, removed or unavailable file requires explicit selection, saving and
review. A pending unknown send is reconciled before any new local read or upload.

Gateway accepts only completed `mail_send_attachment` objects for the current
deployment, Owner, Client, installation and authorization revision, with explicit
`files.read` permission. It reads object metadata from its object store, never
from a source pathname. Objects retain their absolute expiry of at most 24 hours;
expiry, release or a different binding cannot fall back to a Gateway file. The
permanent authorization decision does not extend temporary file retention.

The Gateway freezes the verified transferred bytes in a private per-send
directory solely for the provider runtime. This temporary copy does not confer
authority to read Gateway workspace source files. The application
runtime validates that snapshot and uploads its in-memory contents through
bounded, redacted chunks and the browser File/DataTransfer input path. It never
hands the browser a pathname to reopen. Ownership and hashes are checked before
dispatch and the attachment inventory is checked again before sending. Missing,
additional, pending or failed attachment rows prevent sending. Temporary files
are bounded and cleaned after completion or restart. Unknown effects retain their
original invocation and durable fence; reconciliation does not upload or resend.
The receipt identity excludes the disposable staging path while retaining names,
sizes and hashes. Legacy drafts without attachments remain compatible.

This application change is an explicit local App-CLI `.12` delta from the pinned
`.11` bundle. `vendor/app-cli/workspace-mail-attachments.patch` is reviewable;
`python3 scripts/build-workspace-mail-release.py --check` reproduces the runtime,
Python wheel and paired release metadata. The metadata records the original
upstream commit and a separate local patch digest. Runtime protocol 2.0 and Host
protocol 1.0 are unchanged. The installer continues to reject mixed versions and
tampered files and to preserve the durable ledger through whole-set rollback.
This is not an upstream App-CLI release or a cross-project contract change.

The final 10 MiB upload used the installed binding, actual Controller/CLI and an
isolated Electron adapter: 228 chunks in 123.24 seconds against the unchanged
180-second limit, with matching sink SHA-256 and 12 normal lease renewals. Each
code envelope remains below 64 KiB; the 45 KiB data chunks are registered for
diagnostic redaction. This capacity fixture exercises the existing application
upload channel, not production ISCP routing. macOS's legacy process reaper remains
Linux-only; the fixture separately proves CLI exit and cleans its own processes.
Linux Controller tests cover that platform's ownership and process-cleanup path.

The earlier real SparkX renderer/main/preload → encrypted ISCP → Linux Gateway → controlled
mail sink path passed changed-file rejection, saved-manifest review, exact bytes,
lost-response reconciliation and one effect per invocation. Native direct business
HTTP/WS attempts and Chromium business URL events were zero. Real PostgreSQL
passed snapshot persistence, version conflicts and unknown-send fencing. Separate
real Chromium tests exercise the provider-shaped upload controls, path replacement,
input movement during hashing, and final attachment verification. These controlled
pages and sinks do **not** establish Gmail, Outlook or QQ Mail production DOM or
delivery qualification; that last check needs a logged-in test account, a recipient
and explicit approval to send the reviewable test message. The original source
fixture incorrectly placed files on Gateway; it cannot qualify the desktop-local
boundary. Provider-dependent
surfaces remain independently closed until that check succeeds.

The production Browser Host entry point now passes seven capture states without
extra compositor flags: never-selected page, foreground, occluded window, hidden
window, restored window, another selected conversation and restored conversation.
A fixed, bounded Chromium capture handles a never-presented view without changing
focus or selection; revocation/deadline checks and debugger cleanup remain active.
Native read/fill/click and lost-click-reply fencing passed with one actual click.

Permanent authorization deletion was also exercised through the real Settings UI.
After Relay shutdown and both issuer and desktop restart, the same signed deletion
receipt was recovered and business calls remained blocked. Approval-wait restart
termination retains the earlier real-process SIGKILL and durable-reopen evidence.
Installed applications, remote services and online Relay have not been upgraded.
InfiniCenter remains unavailable at the configured anchors; no central acceptance
is claimed. Candidate packages and the exact validation counts/hashes are recorded
in the evidence file above for the user's final acceptance.

Historical pre-correction checks: 65 Go test packages, build/vet and focused race; 193 Desktop tests;
237 WebChat tests and production build; 142 Linux Controller tests (one unrelated
opt-in download fixture skipped); 23 focused attachment checks, including seven
real Chromium cases; paired-release install/reproduction/rollback and 110 bilingual
documentation mirrors. Use the standalone capacity fixture with
`SPARKCLAW_MAIL_CAPACITY_TEST=1 node tools/browser-controller/test/qualify-workspace-mail-capacity.mjs`.

## 9.4 Desktop-local attachment correction, 2026-10-09

The native acceptance fixture now creates desktop user data outside every Gateway
Docker bind mount. A same-name Gateway file contains different decoy bytes.
The fixture imports the production SparkX main/preload/renderer, selects owned
local file IDs and verifies encrypted transfer, exact sink bytes, rejection after
desktop-file corruption, and receipt reconciliation after local-file removal.
It separately exercises the 10 MiB object-transfer ceiling. The mail save/send
deadline is bounded at 180 seconds across desktop and ISCP layers; unrelated RPC
deadlines remain unchanged. Real-provider delivery and ASR/TTS are outside this
controlled qualification. Results are recorded in
[the corrected evidence](evidence/iscp-desktop-mail-boundary-2026-10-09.json).

The integrated source passed 65 Linux Go test packages, build/vet, focused race,
real PostgreSQL manifest/CAS/restart checks, 216 Desktop tests, 239 WebChat tests,
production UI build, 27 Linux local-file checks and 110 bilingual mirrors. Native
mail qualification passed all six boundary/recovery checks with three controlled
sink effects and one receipt reconciliation. Transferring and saving the 10 MiB
desktop file for review took 43.727 seconds; its sink hash and bytes matched.
Gateway had no mount of the desktop data, and native direct business HTTP/WS
attempts and Chromium business URL events were zero. The first native run exposed
a local error-code mapping defect; its correction passed the complete rerun.

## 10. Related designs

- [Local ISCP integration and renewal acceptance](desktop-iscp-connection-design.md): implemented baseline and actual evidence scope.
- [Architecture](architecture.md), [workbench release](workbench-release.md): data ownership and durable delivery.
- [Browser runtime](browser-runtime.md): Controller, host, tab and external-write constraints.
- [Incremental mail synchronization](email-timeline-incremental-sync-design.md), [original storage](email-local-download-storage-design.md): reused mail semantics.
- [WebChat voice Phase 2](webchat-voice-phase2-design.md): current ASR partial/final semantics; its HTTP fallback does not apply to ISCP mode.
