# Web-to-Connector Outbound Messaging Design

> Language: English | [简体中文](../zh-cn/docs/web-outbound-messaging-design.md)

## 1. Status And Goal

This document defines the implementation contract for sending owner-composed
messages from WebChat to an active third-party messaging binding. It is a
design specification, not a claim that the feature is already implemented.

The first release must accept every message part already represented by
SparkClaw's canonical message model:

- text;
- image;
- audio, including ordinary audio attachments and `voice_note` disposition;
- file.

These four kinds are the complete first-release scope. Video and other binary
formats are delivered as `file` parts because they are not currently
first-class `MessagePartKind` values. Stickers, locations, contacts, polls, and
other provider-native objects are out of scope until the canonical model
represents them.

The payload must remain structured from WebChat through Gateway and the
connector adapter. No delivery path may infer a file from assistant Markdown,
silently discard a part, or expose provider recipient IDs and credentials to
the browser.

## 2. Current Baseline And Gaps

The repository already has most of the lower-level primitives, but they are
not connected into a Web-to-connector sending path:

- `app.MessageContent` and `app.MessagePart` define `text`, `image`, `audio`,
  and `file`, with `inline`, `attachment`, and `voice_note` dispositions.
- `app.DeliveryRequest` and `app.DeliveryReceipt` define provider-neutral
  delivery identities and results.
- Telegram's client can send text, photos, documents, and voice payloads.
- Weixin notification code can send text, images, and files. Audio can retain
  its bytes through file delivery when no native audio/voice operation exists.
- WebChat can upload one artifact and attach it to an Agent message, but it
  cannot choose an external target or invoke connector delivery directly.
- `connector.Registration` exposes only the reminder-oriented text
  `notification.Adapter`; image/file helpers remain provider-specific.

The feature must close these gaps through one connector-neutral outbound
contract rather than adding channel switches to Gateway or WebChat.

## 3. Product Semantics

WebChat has two explicit composer modes:

1. **Agent chat** sends the message to the current SparkClaw session and keeps
   the existing streaming Agent behavior.
2. **External send** sends owner-authored content directly to one selected
   active connector endpoint. It does not invoke the Agent Runtime.

Changing to external-send mode requires selecting a destination. The final
send action opens a review surface containing the destination, channel, text,
attachments, total bytes, and any announced provider fallback. Confirming that
review is the owner's approval for this direct action; the Gateway must not
create a second approval record for the same click. Agent-generated, scheduled,
or tool-generated sends continue to use their existing policy and approval
boundaries.

Sending to more than one destination is out of scope for the first release.
Forwarding an existing third-party message is also out of scope; the owner may
compose equivalent content and select existing artifacts.

### 3.1 Multi-user terminology

This feature assumes that a SparkClaw project may contain multiple authorized
actors and that one third-party software channel may contain multiple reachable
people or conversations. Every lookup is scoped to the authenticated actor and
owner/project authorization boundary. New receive/send code must not fall back
to `DefaultOwnerID` when that identity is missing or ambiguous.

A connector **binding** identifies a software account and its credential. It
is not necessarily a recipient. A deliverable **endpoint** identifies one exact
destination:

```text
software/channel + binding + external user + chat + optional thread
```

Display names are presentation data, not authorization or uniqueness keys. Two
people with the same name, or one person reachable through two accounts/chats,
remain distinct endpoint candidates.

### 3.2 Explicit receive state

Every third-party inbound message persists a receive state before routing:

- direction is `receive`;
- owner/project and authenticated actor are known;
- source endpoint is exact and includes channel, binding, external-user,
  conversation, and optional thread identity;
- provider-native message ID and the exact source endpoint form the
  idempotency boundary;
- authorization is checked before attachment download or Agent execution;
- return route is frozen to that exact source endpoint for an ordinary reply.

The receive lifecycle is `received -> authorized -> normalized -> routed ->
processed`, with explicit `rejected`, `failed`, and `duplicate` terminal
states. A provider event without an exact authorized source endpoint is never
treated as belonging to the current Web user based on a display-name match.

An automatic reply to an authorized third-party inbound message returns to its
frozen source endpoint. This is a reply, not a newly inferred external target.

### 3.3 Explicit send state

Every outbound action persists a send state independently from receive state:

```text
draft
  -> needs_channel_confirmation
  -> needs_recipient_confirmation
  -> target_resolved
  -> awaiting_send_approval
  -> approved
  -> sending
  -> sent | partially_sent | failed | outcome_unknown
```

States that do not apply may be skipped, but `sending` is unreachable until an
exact endpoint is resolved and the external action is approved. A sole
recipient may skip recipient clarification; it does not skip send approval.

Send state records the origin (`web_direct`, `agent_workflow`,
`source_reply`, or `schedule`), requested software, requested recipient,
candidate endpoint IDs, resolution rule, exact target endpoint, actor,
authorization, approval source, and delivery receipt. A scheduled send freezes
the exact endpoint when the schedule is approved and never silently reroutes
to a different person if bindings later change.

### 3.4 Deterministic target resolution

The deterministic resolver applies these rules after filtering endpoints by
the current actor's authorization:

| Origin and request | Eligible candidates | Result |
|---|---:|---|
| Web/Agent request has no explicit external-send intent | any | Use the current Web session endpoint; do not inspect third-party history. |
| Authorized third-party inbound ordinary reply | 1 frozen source | Return to the exact source endpoint. |
| External-send intent names no software | any | Ask which software; never infer one from prior sends. |
| Software and recipient are both named | 1 exact match | Resolve that endpoint, then require send approval. |
| Software and recipient are both named | 0 or more than 1 | Report not found or ask the user to choose the exact account/chat. |
| Software is named but recipient is omitted | exactly 1 endpoint in that software | Resolve the sole endpoint, show its identity, then require send approval. |
| Software is named but recipient is omitted | more than 1 endpoint | Enter `needs_recipient_confirmation` and ask who should receive it. |
| Software is named but recipient is omitted | 0 | Block as unavailable; never fall back to Web or another software. |

"Only one user" therefore means exactly one active, authorized, deliverable
endpoint for the current actor within the explicitly named software. It does
not mean one binding, one display name, or one user globally.

External delivery is orthogonal to the business capability tree. The current
tree therefore does not add a `message.send` leaf. A pure direct send is a
Message Control command and bypasses business Workflow routing. A request that
also needs a browser or document Workflow carries a separate delivery
directive; the deterministic endpoint resolver binds its opaque endpoint ID
before approval, and the Workflow result later uses that frozen return target.
Missing or ambiguous targets clarify before any approval or provider call.
When no external-send intent is present, the result remains on the Web return
route.

## 4. Canonical Delivery Contract

The `app` package remains the source of truth for message and delivery types.
Extend the existing delivery contract only where receipts and capability
discovery require additional typed data.

```go
type DeliveryCapabilities struct {
    Kinds                 []MessagePartKind        `json:"kinds"`
    Dispositions          []MessagePartDisposition `json:"dispositions"`
    MaxParts              int                      `json:"max_parts"`
    MaxTotalBytes         int64                    `json:"max_total_bytes"`
    MaxBytesByKind        map[MessagePartKind]int64 `json:"max_bytes_by_kind,omitempty"`
    SupportsCaption       bool                     `json:"supports_caption"`
    SupportsNativeVoice   bool                     `json:"supports_native_voice"`
    SupportsFileFallback  bool                     `json:"supports_file_fallback"`
}

type PartDeliveryReceipt struct {
    PartID         string `json:"part_id"`
    Status         string `json:"status"`
    Representation string `json:"representation"` // native or file_fallback
    ProviderRef    string `json:"provider_ref,omitempty"`
    ErrorCode      string `json:"error_code,omitempty"`
}
```

The contract also carries explicit direction and target resolution:

```go
type MessageDirection string

const (
    MessageDirectionReceive MessageDirection = "receive"
    MessageDirectionSend    MessageDirection = "send"
)

type TargetResolutionStatus string

const (
    TargetDefaultWeb             TargetResolutionStatus = "default_web"
    TargetSourceReply            TargetResolutionStatus = "source_reply"
    TargetNeedsChannel           TargetResolutionStatus = "needs_channel"
    TargetNeedsRecipient         TargetResolutionStatus = "needs_recipient"
    TargetAmbiguous              TargetResolutionStatus = "ambiguous"
    TargetResolved               TargetResolutionStatus = "resolved"
    TargetUnavailable            TargetResolutionStatus = "unavailable"
)

type DeliveryTargetSelection struct {
    Status                 TargetResolutionStatus `json:"status"`
    RequestedProviderKey   string                 `json:"requested_provider_key,omitempty"`
    RequestedRecipientText string                 `json:"requested_recipient_text,omitempty"`
    CandidateEndpointIDs   []EndpointID           `json:"candidate_endpoint_ids,omitempty"`
    ResolvedEndpointID     EndpointID             `json:"resolved_endpoint_id,omitempty"`
    ResolutionRule         string                 `json:"resolution_rule"`
}
```

Receive and send state may use separate persisted records, but both reference
the same exact `MessageEndpoint` identity. A third-party endpoint ID represents
one recipient conversation. A binding ID alone is not a valid target when the
binding can reach multiple users or chats.

`DeliveryRequest` keeps `Target`, `Content`, and `IdempotencyKey`. Gateway sets
the request ID and creation time; browser-supplied values for those server
fields are ignored. A delivery receipt includes ordered per-part receipts so a
multi-operation provider send cannot report success while losing one part.

Binary browser requests reference an `artifact_id`; they never submit an
absolute path, an arbitrary URL, or a provider-native media identifier. Gateway
resolves each artifact into a governed `ResourceRef` after checking owner,
session, workspace containment, file type, actual size, and regular-file state.

## 5. Connector Boundary

Add a connector-neutral outbound package with this minimum contract:

```go
type Adapter interface {
    Capabilities(context.Context, app.NotificationBinding) app.DeliveryCapabilities
    Deliver(context.Context, app.NotificationBinding, app.DeliveryRequest) (app.DeliveryReceipt, error)
}
```

`connector.Registration` gains one optional `Outbound Adapter`. The Registry
builds the outbound router exactly as it currently builds binding,
notification, and runtime routers. Gateway depends on that router and never
switches on `telegram`, `weixin`, provider names, MIME extensions, or protocol
constants.

Each adapter must:

- validate the active binding, its owner, and the `message_send_self` scope;
- resolve credentials only inside the provider package;
- preflight every part before performing the first provider call;
- preserve part order;
- use bounded HTTP clients and request contexts;
- return typed blocked/retryable errors without tokens, raw recipient IDs, or
  message bodies;
- return one receipt for every input part.

Reminder notification can remain on its current text adapter initially. A
later cleanup may wrap reminder text in `DeliveryRequest`, but this feature must
not make reminder delivery depend on the Web API.

## 6. Message-Type Mapping

All canonical kinds are valid Web inputs. Provider limitations affect the
representation, not whether Gateway understands the part.

| Canonical part | Telegram | Weixin | Required behavior |
|---|---|---|---|
| `text/inline` | `sendMessage` | text item | Split only at provider limits; preserve order. |
| `image/attachment` | `sendPhoto`; use `sendDocument` when photo encoding/size is not compatible | encrypted CDN image item | Preserve caption when supported; announce file fallback before confirmation. |
| `audio/attachment` | `sendDocument` | encrypted CDN file item | Preserve original bytes, name, and content type. |
| `audio/voice_note` | `sendVoice` for a compatible native voice format; otherwise `sendDocument` | encrypted CDN file item | Use `file_fallback` explicitly when native voice is unavailable; never substitute a transcript. |
| `file/attachment` | `sendDocument` | encrypted CDN file item | Preserve the safe display filename and bytes. |

Text plus media may require multiple provider calls. The adapter must finish
them in canonical part order and record partial failure. Automatic fallback is
allowed only when it preserves the complete bytes and the review surface
announced it. Conversion, recompression, or transcript substitution requires a
future explicit transform workflow and is not part of direct delivery.

## 7. Gateway API

### 7.1 Discover endpoints

`GET /api/delivery-endpoints` returns only active bindings owned by the current
actor's authorized owner/project scope and carrying `message_send_self`. It
returns exact recipient endpoints rather than credential bindings:

```json
{
  "endpoints": [
    {
      "id": "endpoint:chat_123",
      "channel": "telegram",
      "software_display_name": "Telegram",
      "account_display_name": "Personal bot",
      "recipient": {
        "id": "recipient:r_7f3",
        "display_name": "Alex"
      },
      "capabilities": {
        "kinds": ["text", "image", "audio", "file"],
        "dispositions": ["inline", "attachment", "voice_note"],
        "max_parts": 8,
        "max_total_bytes": 26214400,
        "supports_native_voice": true,
        "supports_file_fallback": true
      }
    }
  ]
}
```

The response excludes binding IDs, raw external user/chat IDs, context tokens,
credential references, base URLs, provider state, and cursors. The endpoint
and recipient IDs are opaque, stable IDs scoped to the authorized actor.

### 7.2 Create a delivery

`POST /api/deliveries` accepts an explicitly confirmed request:

```json
{
  "target": "endpoint:chat_123",
  "idempotency_key": "web-019f...",
  "confirmed": true,
  "content": {
    "parts": [
      {
        "id": "part-text",
        "kind": "text",
        "disposition": "inline",
        "text": "Please review this image."
      },
      {
        "id": "part-image",
        "kind": "image",
        "disposition": "attachment",
        "artifact_id": "obj_123",
        "caption": "Latest render"
      }
    ]
  }
}
```

The success response is `201 Created` with the persisted `DeliveryReceipt`.
Replaying the same owner, idempotency key, target, and content digest returns
the original result with `200 OK`. Reusing the key for different content
returns `409 Conflict`. `confirmed != true` returns `400 Bad Request`.

`GET /api/deliveries/{id}` returns status for retryable or partially completed
requests. The first release performs the send during the bounded POST request;
it does not introduce an unbounded background queue. Provider timeouts produce
a persisted failed/retryable receipt, not an unknown HTTP disconnect.

A natural-language external-delivery directive does not accept model-selected
endpoint IDs. Its resolver receives typed requested software and recipient
slots, filters the endpoint catalog by actor, and freezes the exact resolved
endpoint into Message Control/ReturnRoute state before approval. It never
widens or adds a business capability-tree branch.

### 7.3 Uploads

WebChat may reuse the existing governed artifact upload while the common
browser ingress limit remains 25 MiB. The upload response's `artifact.id` is
the only binary identifier accepted by `/api/deliveries`. Multiple files are
uploaded one at a time and assembled into ordered parts client-side.

Preflight uses the selected endpoint's advertised limits. A target whose
native media limit is lower may advertise and use an exact-byte file fallback;
otherwise WebChat blocks confirmation and shows the typed limit error.

## 8. Persistence And Idempotency

Persist delivery requests, content metadata, status, attempts, part receipts,
provider references, error codes, timestamps, and a SHA-256 content digest.
Do not persist connector credentials or duplicate artifact bytes in delivery
state.

Any Store interface addition must be implemented in memory, file, and
PostgreSQL backends, included in the file `Snapshot`, and covered by the core
migration. The default file backend is a release gate.

A crash or client retry must not create a second external message after a
successful persisted receipt. If the provider times out after accepting a
request and offers no idempotency facility, the receipt remains `failed` with
an `outcome_unknown` code; automatic retry is disabled and the UI asks for an
explicit retry. Provider-native idempotency keys are used when available.

## 9. WebChat Experience

External-send mode provides:

- a destination menu populated from `/api/delivery-endpoints`;
- separate software and recipient selectors; changing software clears the
  previous recipient selection;
- a multi-part attachment tray supporting images, audio, and arbitrary files;
- an audio disposition control (`Audio file` or `Voice note`) for audio parts;
- per-part filename, type, size, caption, remove, and reorder controls;
- capability and size validation before the review step;
- a review dialog with any `file_fallback` called out next to the affected
  part;
- pending, sent, partially sent, failed, retryable, and outcome-unknown states;
- retry only for failed parts when the receipt says doing so is safe.

Message history and delivery history expose direction and exact endpoint
identity. Third-party rows show `Received from` or `Sent to`, software,
recipient display name, and disambiguating account/chat label. Web rows show
the current Web session. Raw provider IDs remain hidden. A channel-only label
such as `Telegram` is insufficient in a multi-user history.

When the selected software has one eligible recipient, WebChat may preselect
it but must display that person's identity in the review. With multiple
recipients, the send button remains disabled until one exact endpoint is
selected. The UI never treats a software account/binding label as a recipient.

The existing microphone transcription flow remains an Agent-chat drafting
feature. It must not silently become a voice-note attachment. Owners can upload
an audio file and mark it as a voice note; a future recording mode must retain
and review audio explicitly before using this delivery API.

Switching sessions or composer modes must not leak draft parts into another
session or destination. A revoked or expired binding invalidates its endpoint
immediately and prevents send even if WebChat has stale endpoint data.

## 10. Security, Policy, And Audit

- Only authenticated owner clients can list endpoints or create deliveries.
- Endpoint listing and target resolution are filtered by both owner/project
  scope and the authenticated actor; another actor's candidates are invisible.
- Gateway derives the owner and resolves the binding; the browser cannot
  override recipient, thread, credential, base URL, or provider.
- Binding scope must contain `message_send_self`. Existing
  `reminder_send_self` alone is insufficient.
- Existing reminder-only bindings are not silently upgraded. WebChat must ask
  the owner to enable ordinary messaging before Gateway adds
  `message_send_self`; newly created bindings request reminder and ordinary
  send scopes separately in the binding review.
- Every artifact must belong to the same owner and remain inside an allowed
  workspace root. Symlinks, missing files, directories, and changed size/hash
  are rejected before provider calls.
- Direct Web confirmation is recorded as the approval source. API-originated
  Agent or scheduled sends do not inherit that approval.
- Recipient clarification and send approval are separate gates. Resolving a
  unique recipient never grants permission to perform the external send.
- Audit events record delivery ID, target endpoint ID, kinds, dispositions,
  byte counts, status, fallback representation, and redacted error code. They
  do not record message bodies, raw recipients, context tokens, credentials, or
  provider response bodies.
- External sends never appear successful until every required part has a
  receipt. Partial success is visible and immutable in history.

## 11. Typed Failures

Gateway and adapters use stable error codes:

| Code | Meaning | HTTP behavior |
|---|---|---|
| `delivery_binding_unavailable` | Binding is missing, stale, revoked, or expired. | `409` |
| `delivery_scope_denied` | Binding lacks owner-send scope. | `403` |
| `delivery_channel_required` | External-send intent omitted the third-party software. | clarification / `422` |
| `delivery_recipient_required` | Named software has multiple eligible recipients. | clarification / `422` |
| `delivery_recipient_ambiguous` | Recipient text matches more than one exact endpoint. | clarification / `409` |
| `delivery_recipient_not_found` | No authorized endpoint matches the requested software/user. | `404` |
| `delivery_cross_user_denied` | Endpoint belongs to another actor or authorization scope. | `403` |
| `delivery_part_unsupported` | No native or byte-preserving fallback exists. | `422` |
| `delivery_payload_too_large` | Part or total exceeds the effective limit. | `413` |
| `delivery_artifact_invalid` | Artifact ownership, path, hash, or file state failed. | `422` |
| `delivery_idempotency_conflict` | Key was reused with another digest or target. | `409` |
| `delivery_provider_retryable` | Bounded provider failure is safe to retry. | `502` or `503` |
| `delivery_outcome_unknown` | Provider may have accepted the message; automatic retry is unsafe. | `502` |

Error messages exposed to WebChat remain actionable but secret-free.

## 12. Implementation Sequence

1. Finalize `app` capability and per-part receipt types plus validation tests.
2. Add the outbound adapter/router contract and register Telegram and Weixin
   implementations without channel switches in Gateway.
3. Add store records and all three backend implementations.
4. Add endpoint discovery and delivery APIs with ownership, scope, artifact,
   preflight, audit, and idempotency enforcement.
5. Add WebChat external-send mode, multi-part composition, review, and receipt
   states.
6. Add focused adapter/API/UI tests, then run the full project validation.

## 13. Acceptance Criteria

- WebChat can send text, image, ordinary audio, voice-note audio, and file
  parts to every active endpoint returned by Gateway.
- Receive records identify one exact source actor/software/binding/chat/thread,
  and ordinary replies return only to that frozen source endpoint.
- WebChat history visibly distinguishes receive/send direction and the exact
  software/recipient/account endpoint for each third-party message.
- Web/Agent responses default to the current Web endpoint unless external-send
  intent is explicit.
- Explicit software plus user resolves one exact authorized endpoint; software
  without a user auto-resolves only when exactly one eligible endpoint exists.
- Missing and ambiguous software/user cases clarify before approval and make
  zero provider calls.
- Telegram and Weixin tests prove the mapping table, including byte-preserving
  fallback and part ordering.
- Unsupported, oversized, stale-binding, wrong-owner, wrong-scope, invalid
  artifact, provider timeout, partial failure, and idempotent replay paths are
  covered.
- No provider switch exists in Gateway or WebChat.
- No message part is inferred from Markdown or silently dropped.
- Memory, default file, and PostgreSQL stores pass the same delivery contract
  tests.
- WebChat build and focused UI tests pass on desktop and mobile layouts.
- `go build ./...`, `go vet ./...`, `go test ./...`, WebChat build, doctor, and
  mock golden eval are green with no new failures.

## 14. Parallel Execution

Implementation is coordinated by the
[Web Outbound Messaging Worktree Plan](web-outbound-messaging-worktree-plan.md).
That plan defines three user-visible Codex tasks and worktrees for message I/O,
routing/workflow work, and final integration. No worktree may begin feature
implementation until the plan and this design are approved.
