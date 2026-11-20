# Link Contract `sparkclaw.link.v1`

> Language: English | [简体中文](../zh-cn/docs/link-contract.md)

Date: 2026-11-19. Status: implemented. This is the specification of the transport-neutral contract in [Client link layer design](client-link-layer-design.md) section 3.1. The Go definitions are in `services/link/contract`; conformance vectors are in `services/link/contract/testdata/vectors.json`, and every client implementation (Go, Java/Kotlin, Swift) checks itself against them.

## 1. Frames

Every message is one JSON object, one frame:

| Field | Kinds | Meaning |
|---|---|---|
| `type` | all | `hello`, `request`, `response`, `event` or `cancel` |
| `id` | all | 1–128 bytes, no NUL or newline. A response carries its request's id |
| `hello` | hello | See section 2 |
| `op`, `params` | request | Operation name (≤ 96 bytes) and its parameters |
| `ok`, `result`, `error` | response | `ok` is required; a failure carries `error` |
| `topic`, `subscription`, `data` | event | `topic` is required |

An encoded frame is at most 600 KiB. Large objects use the chunked object operations (≤ 256 KiB raw per chunk).

A frame with an unknown `type`, or without its required fields, is invalid. A request whose `op` is unknown, or not available to this device, is answered with `unsupported_capability`, and **the connection stays open**. Unknown fields in `params` are ignored, so a newer client keeps working with an older backend.

Request ids sent by the backend (reverse calls) start with `s`; clients must not use that prefix.

## 2. Hello

The client sends `hello` first. It carries:

- `versions`: the versions it speaks;
- `profile`: `duplex` or `pull`;
- `device`: the device's conditions, as `{"class","name","app","conditions":{"browser_surface","attachments","microphone","screen"}}`;
- `reverse`: the reverse operations it serves.

The backend answers with a `hello` frame that carries:

- `version`;
- `profile`;
- `features`: the operations available to this device, computed by rule 3.9 from the conditions and profile;
- `principal`: `owner_id`, `device_id`, `class`, `name`;
- `server_time`;
- `reverse`: the accepted reverse operations.

Device class comes from the console record. A hello whose class differs from that record is refused. Class also bounds what a device may claim:

- only a desktop keeps the browser surface;
- a watch gets no attachment transfer.

A repeated hello gets the same reply.

## 3. Profiles

| Profile | Connection | Live output |
|---|---|---|
| `duplex` | Long-lived. Events are pushed and reverse calls are possible | `run.subscribe` |
| `pull` | Request and response only. No subscriptions and no pushed events | Poll `run.status` |

Events only accelerate. Everything an event delivers can also be read with `sync.changes`, `session.messages` and `run.status`.

In the current clients the pull profile still opens one connection while the app is in the foreground. The backend neither pushes nor accepts subscriptions on it.

## 4. Operations

### Core

| Operation | Params → result | Notes |
|---|---|---|
| `ping` | — → `{time}` | Liveness |
| `device.describe` | — → `{principal, features, domains, backend}` | |
| `sync.changes` | `{cursor, limit}` → `{sessions[], cursor, more}` | Headers with `revision > cursor`, ordered by revision. A tombstone has `deleted: true` |
| `session.create` | `{title, client_request_id}` → header | Idempotent by `client_request_id` |
| `session.rename` | `{session_id, title}` → header | |
| `session.archive` | `{session_id, archived}` → header | |
| `session.delete` | `{session_id}` → `{session_id, deleted}` | Deleting twice succeeds. Uploaded attachments are deleted with the conversation |
| `session.messages` | `{session_id, before_seq?, after_seq?, limit}` → `{session, messages[], more}` | Oldest first. `before_seq: 0` returns the latest page |
| `message.send` | `{session_id, client_message_id, content, attachments[], client_timezone, client_presentation?, replay_only?}` → `{message, run_id, replayed}` | The same `client_message_id` replays. With `replay_only` the backend only answers whether the message arrived: the original reply, or `not_found` with `not_sent` when it never did; it never starts a late run. A second send while a run is in progress returns `busy`. A desktop may declare `client_presentation {schema_version, schedule_show, timezone, requested_at, local_conversation_id}`; the backend may then answer with `extra.client_action` (`schedule.show`) in the final message instead of reading local schedules |
| `run.subscribe` | `{session_id}` → `{subscription, status}` | duplex only. The current snapshot first, then `run` events |
| `run.unsubscribe` | `{session_id}` → `{ok}` | duplex only |
| `run.status` | `{session_id}` → RunStatus | `state`: `idle`, `running`, `approval_pending`, `completed`, `failed`, `cancelled` or `blocked`. Also carries `preview`, `progress`, `final_message`, `pending_approvals` and `seq`, the last run event the snapshot includes. A finished run is answered from the stored conversation |
| `run.cancel` | `{session_id, run_id?}` → RunStatus | |
| `approval.list` | `{session_id?, pending?}` → `{approvals[]}` | |
| `approval.resolve` | `{approval_id, decision: approve\|reject, inputs?, idempotency_key}` → `{approval}` | |

### Objects (device condition `attachments`)

| Operation | Notes |
|---|---|
| `object.upload.begin` | Params: `{upload_key, name, size, content_type, sha256, session_id}` or, for a business domain's object, `{…, purpose}` without `session_id`. Returns `{upload_id, received, chunk_size}`. The same `upload_key` resumes the upload |
| `object.upload.chunk` | Params: `{upload_id, offset, data}`, where `data` is base64 |
| `object.upload.commit` | Verifies the SHA-256 and returns `object_id` |
| `object.describe` | Does not require the `attachments` condition |
| `object.read` | Params: `{object_id, offset, length}`. Returns `{object, offset, data, eof}` |

A domain object (uploaded with a `purpose`, or returned by a domain method) belongs to the device that uploaded or received it, is read only by that device, never enters a workspace and expires after 24 hours. Its id is a UUID. Purposes in use: `mail_send_attachment`, `speech_recording`, `domain_args`, `domain_result`, `mail_attachment`.

Quotas:
- at most 25 MiB per file;
- at most 8 unfinished uploads per device;
- at most 512 MiB staged in total;
- staged uploads expire after 24 hours.

### Business domains

`domain.call` takes `{domain, method, args, args_object?, idempotency_key?, replay_only?}` and returns `{result}`.

- `method` is a typed domain method, for example `mail.conversations.list`; `domain` is its first segment. `device.describe` lists the domains the backend serves.
- `args` are the method's own typed fields. The records a desktop route names (a mail, a conversation, a binding) are fields too, never a path. Unknown fields are ignored.
- Arguments larger than one frame are uploaded first as a domain object of purpose `domain_args`; `args_object` names it instead of `args`. The call and its receipt are those of the arguments.
- Mutations need `idempotency_key`, a UUID. The backend records the attempt before it runs, and a retry with the same key gets the original result, `conflict` (the same key with other content) or `outcome_unknown` (the attempt started and never finished). It never runs twice.
- `replay_only: true` answers only the original outcome of a mutation and never starts it: `not_found` with `not_sent` means it never ran.

Two result shapes:

- **Typed results** (`medicine`, `notifications`, `speech`, `browser`, `mcd`): the method's value. A failure is a link error whose `reason` is the domain's own (for example `medicine_sync_reset_required`); a model outage is `backend_unavailable` with the message 模型暂时不可用.
- **Domain results** (`settings`, `mail`, `presentation`): `{status, body, body_object?}`, the status and JSON body the host WebChat answers the same business call with, which the desktop's original screens read unchanged. A file or a body larger than one frame comes back as `body_object`, a domain object read with `object.read`. A business failure is a result with its status (for example `409 revision_conflict`); link errors stay for the call itself.

| Domain | Methods |
|---|---|
| `medicine` | `medicine.sync`, `.operations.get`, `.items.create`, `.items.update`, `.items.delete`, `.items.batch`, `.import`, `.recognize`, `.parse` |
| `notifications` | `notifications.list` `{limit?, after?}` (with a sealed `watermark`), `.read` `{id}`, `.read_all` `{watermark}` |
| `speech` (condition `microphone`) | `speech.status`, `.transcribe` `{session_id, request_id, language, audio_object}`, `.cancel`, `.session.open`, `.session.frame`, `.session.events`, `.session.finish`, `.session.cancel` |
| `browser`, `mcd` (condition `browser_surface`) | `browser.receipt`, `browser.reconcile`; `mcd.session.provision`, `.revoke`, `.status` |
| `settings` | `settings.owner.get`/`.patch`, `.connectors.list`/`.patch`, `.integrations.list`, `.credentials.add`/`.activate`/`.check`/`.delete`, `.bindings.list`/`.start`/`.get`/`.poll`/`.revoke`/`.login`, `.browser.get`/`.save`/`.check`/`.delete`, `.mcp.catalog`/`.transports`/`.tickets.*`/`.bindings.*`/`.records.delete`/`.pairing.*` |
| `mail` | `mail.providers.*`, `.intake.update`, `.intake.reconcile`, `.mailboxes`, `.sync`, `.attachment`, `.message`, `.conversations.*`, `.pending`, `.notifications`, `.interaction`, `.sent_sources`, `.sender_rules.*`, `.verification`, `.render_preview`, `.file`, `.presentations.*`, `.compose.capabilities`, `.sync.*`, `.drafts.*`, `.send`, `.replies.polish`, `.source.cleanup`, `.classification`, `.assignment`, `.viewed`, `.reanalyze` |
| `presentation` | `presentation.config`, `.owner`, `.ready` |

The settings owner, connector and integration values come as `{revision, value}`; a change carries the `expected_revision` it was made against and a stale one answers `409 revision_conflict`.

A receiving change (`mail.intake.update`) whose reply was lost and whose attempt is unknown is settled with `mail.intake.reconcile` `{idempotency_key, original}`, where `original` is the change's own arguments: the mailbox tells whether it was applied, and that answer becomes the change's receipt (`{state: "completed", idempotency_key, response}`). A login attempt whose provider failed (`mail.providers.login`) stays `outcome_unknown`, since the login window may have opened.

### Projections

`projection.snapshot` and `projection.activity` replace `phone.snapshot.get` and `phone.activity.list`.

### Device projection

For a watch the backend trims the data it returns:
- message text, trimmed to plain text of at most 2000 characters;
- list previews, at most 120 characters;
- attachments, file names only;
- history pages, at most 20 messages.

## 5. Events

| Topic | Data |
|---|---|
| `run` | `{session_id, run_id, seq, kind: state\|delta\|progress\|final, text?, status?}`. `seq` increases by one per event of a conversation, across runs; one subscription receives them in order. A gap means events were lost: read `run.status` again |
| `sync` | `{cursor}`: a conversation changed; call `sync.changes` |
| `approval` | `{pending}`: the set of pending approvals changed |
| `domain` | `{domains[]}`: a business domain's state changed (for example `settings`, `mail`, `medicine`); read it again |
| `device` | `{state: "revoked"}`: sent right before the backend closes the connection |

## 6. Errors

`error` is `{code, message, retryable, not_sent?, reason?}`. `not_sent` means the request never reached a handler (no path was up, the frame could not be written, or the backend refused it before running it): it had no effect and may be sent again. `reason` is a business domain's own reason. The codes are:

- `unsupported_capability`
- `invalid_request`
- `not_found`
- `conflict`
- `unauthorized`
- `revoked`
- `backend_unavailable`
- `too_large`
- `busy`
- `cancelled`
- `internal`
- `quota_exceeded`
- `timeout`
- `outcome_unknown`: a retried mutation whose original attempt started and never finished. It is never run again; the client reconciles the business state

`backend_unavailable` means the backend is unreachable. Clients fail the operation at once and queue nothing (design 3.12). A request that was sent but got no reply is retried with the same idempotency key, only to learn its outcome.

## 7. Reverse calls

A desktop with a built-in browser declares these reverse operations at hello:

- `browser.controller.status`, `.acquire`, `.execute`, `.release` and `.chunk`;
- `mcd.account.ensure`;
- `mcd.checkout.prepare`, `.commit` and `.abort`;
- `mcd.operation.status`;
- `mcd.orders.query`.

The backend sends `request` frames with `s`-prefixed ids, and the client answers with `response` frames. The bodies are unchanged from the previous transport.

## 8. Transports

### 8.1 LAN connector

The LAN connector speaks WebSocket over TLS at `wss://<host>:<port>/link/v1`, with subprotocol `sparkclaw.link.lan.v1`. The client pins the backend's certificate with `tls_pin`, the base64url SHA-256 of the leaf certificate's DER, published by the console as part of the host record.

Before any link frame, the two sides exchange:

1. The server sends `{"type":"challenge","version","host":<backend device id>,"nonce"}`.
2. The client checks `host` and answers `{"type":"auth","device_id","proof"}`. The proof is an ISCP device proof with audience = the backend's device id and challenge = `sparkclaw.link.lan:` + nonce.
3. The server checks the proof against its local copy of the device directory. It answers `{"type":"authenticated","principal"}` or `{"type":"error","code","message"}`.

After that, each WebSocket text message is one link frame.

### 8.2 ISCP Relay

Each device holds one end-to-end ISCP session with the backend. All payloads are ISCP secure envelopes:

| Payload type | Encryption | Content |
|---|---|---|
| `sparkclaw.link.grant.v1` | Plaintext | The device's current Grant. Sent before Hello on every handshake |
| `iscp.session.hello.v2` / `iscp.session.ready.v2` | Plaintext | Standard ISCP handshake; the device's Hello names the Grant |
| `sparkclaw.link.frame.v1` | Encrypted | One link frame |
| `sparkclaw.link.reset.v1` | Plaintext | `{reason}`; the device must handshake again. Reasons: `unknown_session`, `grant_invalid`, `revoked`, `superseded` |

The backend checks the following on every frame:
- the Grant against the console signer: subject, audience, permission `sparkclaw.link`, the Relay, expiry and revocation epoch;
- the device against the directory.

A device handshakes again after a reset, after its Grant is renewed, and at most every 6 hours.

### 8.3 Connector socket

The ISCP connector opens one Unix-socket connection to the Gateway per device session. Each message on the socket is a length-prefixed frame: a 4-byte big-endian length followed by the payload.

The first exchange is an attach:
1. The connector sends `{"device_id","transport","session_id"}`.
2. The Gateway answers `{"ok","code","message","principal"}`.

Link frames follow. If the Gateway restarts, the connector attaches again and replays the device's hello.
