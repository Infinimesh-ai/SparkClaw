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
owner and carrying `message_send_self`:

```json
{
  "endpoints": [
    {
      "id": "binding:bind_123",
      "binding_id": "bind_123",
      "channel": "telegram",
      "display_name": "Personal bot",
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

The response excludes `external_chat_id`, context tokens, credential
references, base URLs, provider state, and cursors.

### 7.2 Create a delivery

`POST /api/deliveries` accepts an explicitly confirmed request:

```json
{
  "target": "binding:bind_123",
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
- a multi-part attachment tray supporting images, audio, and arbitrary files;
- an audio disposition control (`Audio file` or `Voice note`) for audio parts;
- per-part filename, type, size, caption, remove, and reorder controls;
- capability and size validation before the review step;
- a review dialog with any `file_fallback` called out next to the affected
  part;
- pending, sent, partially sent, failed, retryable, and outcome-unknown states;
- retry only for failed parts when the receipt says doing so is safe.

The existing microphone transcription flow remains an Agent-chat drafting
feature. It must not silently become a voice-note attachment. Owners can upload
an audio file and mark it as a voice note; a future recording mode must retain
and review audio explicitly before using this delivery API.

Switching sessions or composer modes must not leak draft parts into another
session or destination. A revoked or expired binding invalidates its endpoint
immediately and prevents send even if WebChat has stale endpoint data.

## 10. Security, Policy, And Audit

- Only authenticated owner clients can list endpoints or create deliveries.
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
