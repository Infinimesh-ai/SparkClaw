# JingSi LAN Web Client Connection Design

> Language: English | [简体中文](../zh-cn/docs/jingsi-lan-connection-design.md)

| Field | Value |
|---|---|
| Status | SparkClaw side implemented and tested; JingSi implementation and physical LAN proof pending |
| Decision date | 2026-08-14 |
| SparkClaw baseline | `76a72aa` |
| JingSi Android baseline | `ZZZZJJJ0928/JingSi-Windows` at `1708fd9` |
| Phase-one result | One server-bound text conversation with idle realtime updates |
| LAN presentation port | Experimental, disabled and unpublished by default; `18793` unless overridden via `SPARKCLAW_JINGSI_LAN_PORT` |
| Realtime transport | SSE with cursor catch-up over the existing session event log |
| Authentication | Deferred |
| ISCP | Out of scope |

## Decision

JingSi is a mobile presentation client for SparkClaw WebChat, not a third-party
connector such as Telegram or Weixin. Phase one proves one text conversation in
both directions over a trusted LAN. JingSi must also receive new messages in
the configured conversation while it is idle; receiving cannot depend on
JingSi sending first.

JingSi currently has no conversation-management page. It therefore does not
list, create, select, rename, delete, or receive identifiers for sessions. The
server binds the LAN presentation surface to one existing visible WebChat
session. The phone sees only a text send operation and new message events.

There is also no message-history endpoint. On first connection JingSi starts at
the current event head and receives no earlier transcript. A saved event cursor
is used only to recover messages that arrived after a previous connection.

The dedicated host port `18793` is an exact allowlist in front of the existing
Gateway. It is not a second application service, message ingress, delivery
provider, or message store. The message and result paths remain the WebChat
paths that SparkClaw already uses.

```text
WebChat browser -> :18790 -> full WebChat surface ------------+
                                                             |
JingSi -> :18793 -> fixed-session presentation allowlist ----+
                                                             v
                                                  Gateway :18789
                                                             |
  server-configured WebChat session --------------------------+
  -> webMessageIngress
  -> MessageEnvelope
  -> semantic routing + Workflow Runtime
  -> WorkflowResult
  -> DeliveryRequest
  -> Delivery Gateway
  -> LocalWebDelivery
  -> existing app.Message + existing session message.created event
  -> WebChat and JingSi presentation clients
```

The port, route names, and projections remain experimental. Passing the LAN
proof does not make them a stable public SparkClaw API.

## Current LAN Baseline

The default Compose topology publishes WebChat on host port `18790`. WebChat's
Nginx listener serves the SPA and proxies its broad `/api/` surface to Gateway
port `18789` on the Docker-internal network. Gateway is not published directly.
The implemented optional overlay adds `18793` to that Nginx container only
after an operator supplies one RFC1918 host address and one existing visible
WebChat session. The base stack does not publish the port.

The current WebChat API includes session management, message history, message
send, and session events:

```text
GET    /api/sessions
POST   /api/sessions
GET    /api/sessions/{id}
GET    /api/sessions/{id}/messages
POST   /api/sessions/{id}/messages/stream
GET    /api/sessions/{id}/events
GET    /api/sessions/{id}/events/stream
```

JingSi must not connect to this full surface. Its Android cleartext policy also
currently permits only loopback and emulator addresses, so a physical phone
cannot yet use an RFC1918 SparkClaw HTTP address.

SparkClaw already provides the required internal primitives:

- `webMessageIngress` constructs the Web source endpoint and
  `ReturnToSource` route;
- Agent Runtime and `PersistentWebDelivery` persist or idempotently reuse the
  normal `app.Message` result;
- `Store.AddMessage` appends a session-scoped `message.created` event carrying
  the persisted message;
- `/api/sessions/{id}/events/stream` can replay after a cursor and then continue
  as SSE.

These primitives are reused. The raw session event API is not exposed because
it accepts a session ID, returns internal event shapes, is unbounded, and has
inconsistent unknown-cursor behavior between memory and PostgreSQL.

Port `18791` is already used by the browser evaluation fixture and `18792` by
the ISCP Bridge. `18793` is the first non-conflicting provisional port.

## Implemented SparkClaw Surface

### Server-Side Binding

Enabling the presentation listener requires a typed server configuration value
that names one existing WebChat session. The selected session must be visible
and belong to `DefaultOwnerID`. The Gateway resolves and revalidates it before
send and event projection.

JingSi never receives, stores, submits, creates, or selects the session ID.
There is no automatic session creation and no fallback to a latest session. If
the configured session is missing, hidden, or changes owner, `18793` remains
unavailable until the operator corrects the configuration.

### Exact Route Allowlist

Every JingSi route lives under the single `/api/jingsi/` prefix, versioned as
`/api/jingsi/v0/`. The wide WebChat proxy on `18790` therefore blocks the whole
prefix with one rule instead of tracking individual paths, and the Gateway can
guard the same prefix without consulting a route list.

The provisional `18793` listener exposes only:

| Method and path | Purpose |
|---|---|
| `GET /api/jingsi/v0/readyz` | Confirm that the selected SparkClaw process and binding are ready |
| `POST /api/jingsi/v0/messages/stream` | Send plain text to the server-bound session through Web ingress |
| `GET /api/jingsi/v0/client-events/head` | Obtain the current visible event cursor for first connection |
| `GET /api/jingsi/v0/client-events?after={cursor}&limit=100` | Recover new message events after a saved cursor |
| `GET /api/jingsi/v0/client-events/stream?after={cursor}` | Replay after a cursor and receive later messages while idle |

There is deliberately no session route and no message-history route. In
particular, `/api/sessions`, `/api/sessions/*`, `/api/messages`, `/mcp`, the SPA,
configuration, connectors, tools, traces, schedules, files, approvals, and
delivery administration return `404`; unsupported methods on allowlisted paths
return `405`. The listener has no catch-all `/api/` proxy.

The implemented presentation listener maps exact methods and paths to Gateway-owned
handlers over the internal network. It must not interpolate a caller-provided
session ID into an upstream path.

### Gateway-Layer Isolation

The Nginx allowlist is packaging, not the security boundary. The Gateway
enforces the same isolation itself on every `/api/jingsi/` route: while the
surface is disabled each route stays an indistinguishable `404`; when enabled,
a request whose direct TCP peer is not a loopback or private address is
rejected with `403`, and a browser `Origin` header must itself name a loopback
or private-address origin. A misconfigured or directly exposed Gateway port
therefore still refuses public callers, and a public web page cannot read the
fixed-session feed through a browser that sits on the LAN.

### Text Send

JingSi sends:

```http
POST /api/jingsi/v0/messages/stream
Content-Type: application/json

{"content":"Reply with exactly: SparkClaw LAN connected"}
```

`Content-Type` must be `application/json`. The body accepts exactly one
non-empty, bounded `content` string. It rejects
attachments, caller-supplied session or owner IDs, target endpoints, schedule
actions, unknown fields, and oversized text before creating a message or run.
Body validation belongs in the Gateway handler; an Nginx path allowlist cannot
validate JSON fields.

The handler resolves the configured session and calls the same
`webMessageIngress` and streamed runtime path as WebChat. It does not call Agent
directly and does not create a JingSi-specific ingress or callback.

The response is a small SSE stream. `message.stream.started` means that Gateway
has accepted ownership of the detached run. `message.stream.final` reports
completion and the final message ID; `error` reports execution failure. This
surface intentionally does not forward model token deltas. The authoritative
conversation rows are always the persisted `message.created` projections from
the independent client-event stream.

There is no phase-one idempotency key. If the connection is lost before the
client observes `message.stream.started`, the outcome is unknown: the draft is
retained, but the client must not retry automatically because the acceptance
frame may have been lost after the server started the run. A validation error
returned before `201` is safely retryable after correction.

### Realtime Message Projection

The Gateway reads the configured session's existing Store event log. It does
not add an owner-wide journal, mobile mailbox, or duplicate message table. A
bounded Store query must provide consistent cursor validation and pagination
for memory, file, and PostgreSQL while reusing the existing events written by
`Store.AddMessage`.

Only `message.created` is public on `18793`. Each event is projected to:

```json
{
  "cursor": "evt_01...",
  "type": "message.created",
  "message": {
    "id": "m_01...",
    "role": "assistant",
    "text": "SparkClaw LAN connected",
    "created_at": "2026-08-14T08:00:00Z"
  }
}
```

The response does not contain session ID, owner ID, run ID, attachments,
resource metadata, tool arguments, audit payloads, workspace paths, or
credentials. Phase one displays text only. Unsupported non-text content may be
represented by one bounded unavailable-content marker so the cursor can still
advance.

The event cursor is an opaque position in the configured session's existing
event log, not a timestamp or message-history offset. The presentation query
must reject malformed cursors and cursors that do not belong to the configured
session. It returns no events at or before the cursor and at most 100 projected
events per page. If the session has no message event, `head` returns an opaque
session-bound empty cursor rather than a global genesis value; changing the
server binding therefore invalidates even an empty-session cursor.

Persisting an `app.Message` and its existing `message.created` event must remain
one observable mutation. Memory and file must retain both under their current
critical-section/snapshot boundary; PostgreSQL implementation must use one
transaction for the message and event writes. This closes a current durability
gap without creating a second message system.

### First Connection And Reconnect

First connection deliberately does not restore a transcript:

1. JingSi reads `/api/jingsi/v0/client-events/head` and persists cursor `C0`.
2. JingSi opens SSE after `C0`.
3. Only messages created after `C0` are displayed.

Reconnect is queue-like recovery, not history browsing:

1. JingSi reads bounded event pages after its saved cursor.
2. It applies each included message projection idempotently by message ID.
3. After every projected event has been applied, it persists the page's
   `next_cursor`. That cursor may also advance over server-filtered message
   roles, so persisting only the last visible event cursor is incorrect.
4. After catch-up is empty, it opens SSE after the last applied cursor.
5. SSE first replays any race-window events and then waits for new ones.

Each client keeps its own cursor. Reading an event does not acknowledge or
delete it on the server and one client cannot consume another client's update.

If the cursor is unknown or belongs to another session, the server returns
`cursor_reset_required`. JingSi retains its current local display, moves to the
current head, and reports that the missing interval cannot be reconstructed.
There is no fallback to a history endpoint.

## JingSi Implementation Guide

No JingSi source is changed by the SparkClaw implementation. The Android work
should add one direct-LAN transport beside the current Happy/ISCP transport;
it must not adapt `HappyWireClient`, request `sessions.list`, or expose the
existing Happy session chips for this profile.

### Integration Points In The Current Android Project

- Add a `SparkClawLanClient` that uses the project's existing JDK/Android HTTP
  facilities and strict `JsonCompat` decoding. No Gradle dependency is needed.
- Add a lifecycle-owning `SparkClawLanManager` with callbacks for connection
  state, accepted/unknown send state, and projected messages. It owns the
  catch-up worker, one long-lived SSE worker, cancellation, and capped reconnect
  scheduling.
- `MainActivity` starts synchronization when the conversation UI becomes
  active, stops it on lifecycle exit, and sends through this manager. It must
  not call `HappyManager.refreshSessions` or `selectSession` in LAN mode.
- `AppState.Message` already has `id`, `user`, and `text`; use the server message
  ID for deduplication. Keep one fixed message list for LAN mode and remove the
  sample rows when the profile first becomes active.
- `JingSiView.drawChat` renders that one list and a connection state. It omits
  the Happy session selector and history/refresh controls. The screen never
  displays or stores a SparkClaw session ID.
- Change `MainActivity.sendChatInput` so the draft is not cleared merely because
  the send button was pressed. Clear it after `message.stream.started`; retain
  it on validation failure or unknown outcome.

Do not insert an optimistic chat row. The send surface does not return a client
correlation ID for the persisted user message, so content matching would be
ambiguous. Show a separate sending indicator and render both the user row and
assistant row only when their authoritative `message.created` events arrive.

### Profile And URL Validation

Persist only a normalized base URL and a cursor keyed to that URL, for example
in a dedicated `SharedPreferences` file. The profile contains no session ID,
owner ID, bearer token, ISCP material, or cached server transcript. Changing
the base URL discards the old cursor and local rows.

The phase-one validator accepts only `http://A.B.C.D:18793` (the default
presentation port; a deployment that overrides `SPARKCLAW_JINGSI_LAN_PORT`
must configure the phone with the same port), where the decimal
IPv4 literal is loopback, `10.0.0.0/8`, `172.16.0.0/12`, or
`192.168.0.0/16`. Reject hostnames, IPv6, user info, fragments, query strings,
non-root paths, missing/other ports, ambiguous octets, and public addresses
before any request. Normalize an optional trailing `/` away.

Android network-security XML cannot express RFC1918 CIDR ranges or a runtime IP
selection. For this development-only phase,
`res/xml/network_security_config.xml` must permit cleartext at the base level;
the strict literal-IP validator is therefore the application boundary. Do not
ship that policy as the later authenticated production design.

### Wire Models

Decode only these shapes and reject missing, unknown, or wrongly typed fields:

```text
Ready       { ok: true, event_version: "v0", session_ready: true }
Head        { version: "v0", cursor: string }
Catch-up    { version: "v0", events: ClientEvent[],
              next_cursor: string, has_more: boolean }
ClientEvent { cursor: string, type: "message.created", message: Message }
Message     { id: string, role: "user" | "assistant", text: string,
              created_at: RFC3339 timestamp }
```

For SSE, require `event: message.created`, a non-empty `id`, and one `data`
object whose `cursor` exactly equals that `id`. Ignore comment heartbeats. Cap
ordinary JSON bodies and individual SSE frames; close and reconnect on malformed
or oversized input rather than displaying partial data.

The send connection recognizes only `message.stream.started`,
`message.stream.final`, `error`, and comment heartbeats. A `201` without the
started event is not accepted. The client sends UTF-8 JSON with the sole field
`content` and never sends session, history, attachment, target, or schedule
fields.

### Synchronization State Machine

Use `DISCONNECTED -> PROBING -> CATCHING_UP -> STREAMING` for receive state;
sending is an independent operation and never gates receiving.

1. Validate the saved profile and call `GET /api/jingsi/v0/readyz`; require event version
   `v0` and `session_ready=true`.
2. If no cursor exists, call `GET /api/jingsi/v0/client-events/head`, persist that
   cursor, keep the local list empty, and continue directly to streaming.
3. If a cursor exists, call bounded catch-up pages after it. Apply every event
   by message ID, then atomically persist `next_cursor`; repeat while
   `has_more=true`.
4. Open `/api/jingsi/v0/client-events/stream?after={cursor}`. Apply and persist each
   valid event before reading the next frame. The stream replays the catch-up to
   SSE race window before waiting for idle updates.
5. On EOF, heartbeat timeout, or network change, return to catch-up after the
   last persisted cursor and reconnect with jittered exponential backoff capped
   at 15 seconds. Keep only one catch-up and one SSE worker per profile.
6. On HTTP `409` with `code=cursor_reset_required`, retain currently displayed
   rows, fetch the current head, persist it, and show a non-blocking data-gap
   state. Never call a session or history endpoint.

Use a bounded connect timeout (for example 10 seconds), bounded ordinary read
timeout (30 seconds), SSE idle timeout greater than the server's 15-second
heartbeat (for example 45 seconds), and explicit response/frame byte limits.
Android can keep SSE only while its process and connection are active. Phase
one guarantees live updates in that state and catch-up after resume; suspended
or force-stopped delivery remains deferred.

## LAN Publication

The base Compose product does not publish the presentation port. A dedicated
JingSi-LAN override explicitly binds it to one selected RFC1918 host address;
the port defaults to `18793` and follows `SPARKCLAW_JINGSI_LAN_PORT` in the
Nginx listener, the Compose port mapping, and the restart helper alike. It must not
default to `0.0.0.0`. The phone opens no listener, WebChat remains on `18790`,
and Gateway `18789` remains Docker-internal.

Port separation and private-address validation reduce accidental exposure but
are not authentication. Until authentication and TLS are designed, this mode
is development-only on a trusted LAN and is visibly labeled as such.

### Operator Runbook

List existing WebChat sessions on the SparkClaw host and choose one visible
`source=webchat` ID. This is an operator-only step; the value is never sent to
JingSi:

```bash
curl -fsS http://127.0.0.1:18790/api/sessions | jq -r \
  '.sessions[] | select(.hidden != true and .source == "webchat") | [.id, .title] | @tsv'
```

Select one RFC1918 address actually assigned to the SparkClaw host, then start
the optional overlay:

```bash
ip -4 -o addr show scope global
export SPARKCLAW_JINGSI_LAN_BIND=192.168.1.20
export SPARKCLAW_JINGSI_SESSION_ID=sess_replace_with_selected_id
bash scripts/restart_jingsi_lan_compose.sh
```

The script rejects wildcard, public, hostname, and malformed binds; rebuilds
Gateway and WebChat with `docker/compose.jingsi-lan.yaml`; and waits for
`http://$SPARKCLAW_JINGSI_LAN_BIND:18793/api/jingsi/v0/readyz`. Confirm the allowlist before
configuring the phone:

```bash
curl -fsS http://192.168.1.20:18793/api/jingsi/v0/readyz
curl -i http://192.168.1.20:18793/api/sessions  # must be 404
```

## Failure Semantics

| Failure | Required behavior |
|---|---|
| Wrong host/port, failed binding, or incompatible event version | Stay disconnected and preserve the previous profile |
| SSE disconnect | Catch up after the persisted cursor, then reconnect with capped backoff |
| Duplicate cursor or message ID | Apply idempotently; never add a duplicate row |
| Unknown or wrong-session cursor | Return `cursor_reset_required`; retain local display, move to current head, and report an unrecoverable gap |
| Applying an event fails | Do not advance the cursor; retry safely |
| Send validation fails before `201` | Keep the draft; correct the request and retry |
| Send connection drops before observed `message.stream.started` | Keep the draft, mark outcome unknown, and never retry automatically |
| Send drops after `message.stream.started` | Do not replay; detached execution continues and event sync recovers persisted messages |
| Gateway or Android restart | Resume from the saved cursor and existing durable session events |
| Configured session becomes invalid | Keep `18793` unavailable until configuration is corrected |

Ordinary logs must not contain message text, event payloads, or LAN addresses.
Bounded event type, cursor, message ID, and run ID diagnostics are sufficient.

## Implementation Slices

1. **Complete in SparkClaw:** typed configuration for the existing visible
   WebChat session and default-disabled `18793` presentation.
2. **Complete in SparkClaw:** bounded session event reads across memory, file,
   and PostgreSQL, including atomic PostgreSQL message/event writes.
3. **Complete in SparkClaw:** Gateway head, catch-up, SSE, and content-only send
   handlers on the existing Web ingress/runtime/delivery path.
4. **Complete in SparkClaw:** exact Nginx allowlist, private-address Compose
   overlay, startup validation, and deterministic tests.
5. **Pending in JingSi:** the profile, strict HTTP/SSE client, state machine,
   one-view reconciliation, lifecycle wiring, and temporary cleartext policy
   described above. This repository does not modify JingSi source.
6. **Pending integration evidence:** build/install the separately changed
   Android app and run the physical LAN proof below. Authentication, ISCP,
   session management, history, attachments, and a stable public API remain out
   of scope.

## Acceptance Criteria

### SparkClaw Evidence

- A JingSi text send creates ordinary user and assistant `app.Message` records
  in the configured WebChat session and traverses the current Web ingress,
  Workflow Runtime, Delivery Gateway, and `LocalWebDelivery` path.
- No `jingsi` connector, provider, binding, external chat, receive record,
  mailbox, message table, or owner-wide client journal is created.
- A message created in the configured session appears on `18793` while JingSi
  is idle and without a preceding JingSi send.
- Messages from every other session and every non-message internal event are
  absent from the presentation feed.
- First connection starts at current head and returns no earlier messages.
  Reconnect returns only message events after the saved cursor, in order and
  without duplicate display rows.
- Catch-up to SSE has no race gap. Pagination, heartbeat, disconnect, slow
  client, malformed cursor, and wrong-session cursor behavior have focused
  tests.
- Memory, file restart, and PostgreSQL integration prove equivalent message
  event ordering, filtering, cursor validation, and atomic persistence.
- Every route and method outside the allowlist is unavailable on `18793`.
  Explicit negative tests cover `/`, `/api/sessions`, session message/history
  routes, `/api/config`, `/api/connectors`, `/api/schedules`, `/api/deliveries`,
  and `/mcp`. WebChat behavior on `18790` is unchanged.
- The send route rejects attachments, target endpoints, schedule actions,
  caller-supplied session/owner IDs, unknown fields, empty content, and
  oversized text before creating a message or run.

### Physical Android Proof

1. Start the default file-backed SparkClaw stack, choose one existing visible
   WebChat session in server configuration, and bind `18793` to one private LAN
   interface. Keep Gateway internal and WebChat on `18790`.
2. Configure a physical Android device with
   `http://<sparkclaw-private-ip>:18793`. Verify that it requests no session or
   message-history route.
3. Create a text message in the configured WebChat session while JingSi is open
   and idle. Verify that JingSi displays it without initiating a request that
   produced the message.
4. From JingSi send `Reply with exactly: SparkClaw LAN connected`. Verify that
   both WebChat and JingSi display the same persisted user message and exact
   assistant reply.
5. Disable phone Wi-Fi, create two more text messages in the configured session
   from WebChat or an existing SparkClaw Web delivery, restore Wi-Fi, and verify
   ordered catch-up without duplicates.
6. Restart Gateway and JingSi, create one more message in the configured
   session, and verify cursor persistence and reconnection.

Interoperability is confirmed only when send, idle receive, and reconnect
catch-up all pass. A JingSi request followed only by its own response is not
sufficient evidence.

## Deferred Decisions

After the proof succeeds, separate designs may decide:

- authentication, client identity, enrollment, revocation, and TLS;
- whether the provisional surface becomes a supported mobile API;
- whether `18793` remains separate and how routes are versioned;
- session discovery, creation, selection, and message history if JingSi later
  adds a conversation-management page;
- Android foreground or OS push delivery;
- attachments, approvals, schedules, activity, and richer projections;
- discovery and onboarding; and
- retirement of JingSi's previous Bridge/ISCP path.

None of those decisions may introduce a second Agent ingress, result path, or
message store. The shared Web message and delivery chain remains the invariant.
