# External Integrations

> Language: English | [简体中文](../zh-cn/docs/integrations.md)

This document summarizes the active optional integration boundaries. Detailed
environment defaults live in `docker/env/sparkclaw.example.env`, and startup
commands live in [Deployment](deployment.md).

## Shared Rules

- Every third-party messaging connector ships disabled. The owner explicitly
  enables registered channels in WebChat before starting account setup.
- Integrations are disabled or fail closed when credentials, readiness, or
  capability checks fail.
- Secrets come from environment variables or files and are omitted from public
  Gateway configuration.
- External content is untrusted evidence. It never becomes system instruction.
- Outbound calls have explicit host allowlists, deadlines, body limits, retry
  limits, and audit records.
- Messaging providers enter through Connector and Delivery registries; data
  providers enter through typed adapter contracts.
- Owner isolation is logical inside one household Gateway. It protects settings,
  bindings, endpoints, and delivery authorization from cross-owner use, but is
  not a hostile-tenant process or Store boundary.

## LocalMind Workspace MCP

LocalMind is an optional workspace-scoped MCP integration. The shipped
configuration contains its fixed environment references, but the integration
remains disabled until both referenced values are present:

```json
{
  "mcp_servers": {
    "localmind": {
      "transport": "streamable-http",
      "url_env": "LOCALMIND_MCP_URL",
      "bearer_token_env": "LOCALMIND_MCP_TOKEN",
      "namespace": "localmind",
      "expected_server_name": "localmind-ai",
      "protocol_version": "2025-06-18",
      "allow_private_http": false
    }
  }
}
```

Set `LOCALMIND_MCP_URL` to the exact
`/api/workspaces/<workspace-id>/mcp` endpoint and set
`LOCALMIND_MCP_TOKEN` to a credential bound to that workspace. URL and token
values are resolved from the environment at every refresh, omitted from public
Gateway configuration, and never accepted from an owner utterance. The fixed
task contract has no `allow_mutations`, `tool_allow`, or `tool_deny` setting.

Gateway initializes through the shared MCP 2025-06-18 Streamable HTTP client,
verifies `localmind-ai`, rejects Resources and any catalog other than the exact
three-tool task contract, and atomically registers exactly three SparkClaw
tools: `localmind.task.delegate`, `localmind.task.get`, and
`localmind.task.cancel`. Delegate and cancel are conservatively dangerous,
approval-gated remote effects; get is read-only. Remote execution remains
subject to LocalMind authorization, DLP, and audit and is never represented as
running in SparkClaw's local sandbox.

Each wrapper requires a `localmind.task.v1` value in
`structuredContent.result`; MCP `isError` remains a failed tool call. Delegate
and cancel idempotency keys are generated inside SparkClaw, while get supports
the protocol's bounded `knownStateVersion`/`waitMs` long poll. Results and large
archives remain bounded untrusted evidence. Calls are not replayed after an
authentication failure.

These tools are not currently attached to a Catalog leaf or natural-language
Workflow. Their business orchestration is intentionally deferred; registration
alone does not advertise a user-visible LocalMind capability.

Use public HTTPS whenever possible. From the Gateway container, `localhost`
means that container, not the host. Use a LocalMind service name on the shared
Compose network, `host.docker.internal`, or the public HTTPS endpoint. Plain
HTTP requires `allow_private_http: true` and is accepted only for loopback,
private-network, or container-service hosts; redirects are rejected.

## Telegram

Telegram is an optional private-chat connector and ships disabled. Enable it
from the common connector controls in WebChat, then supply a Bot token to create
a separate account binding. Multiple Bot bindings may coexist. Each Bot token
is verified before binding and encrypted separately through the credential
vault; persisted state stores ciphertext envelopes, not plaintext tokens.

`tools.notifications.channels.telegram.enabled` and
`SPARKCLAW_TELEGRAM_ENABLED` remain bootstrap defaults for deployments without
a persisted owner choice. They do not override a later WebChat choice, and a
stored Bot binding never enables Telegram by itself.

A verified Bot starts without a recipient. The first fresh authorized private
message atomically claims its user/chat; historical updates and groups cannot
claim it. Each binding owns its cursor, inbox identity, ordering, and recipient
authorization. Long polling, global concurrency, pending work, attachment size,
attachment count, and voice duration are bounded.

Inbound text and supported media enter the shared Message Runtime. Voice notes
delegate to the shared speech transcriber. Outbound text/media, scheduled
results, and approval prompts use the same Delivery Gateway described in
[Messaging and scheduling](messaging-and-scheduling.md).

Main controls use `SPARKCLAW_TELEGRAM_*`. The Bot API base URL defaults to the
official endpoint, polling is bounded, and private chats are required.

## Weixin

Weixin is an optional connector registered through the same provider-neutral
interfaces. Its QR/binding lifecycle, polling/media behavior, addresses, and
acknowledgements stay in the Weixin packages. Agent Runtime, Timer, and Delivery
Gateway do not branch on Weixin names.

Weixin also ships disabled. Enable it through the same WebChat control before
starting QR setup. Its notification channel block and environment overrides are
only bootstrap defaults when no persisted owner choice exists. Revoked or
unavailable bindings remain visible but cannot be selected for delivery.

For the QR provider, WebChat opens the persisted provider login URL through a
dedicated visible Chromium profile instead of a link in the owner's default
browser. Gateway accepts that action only for the current owner's pending
Weixin binding and only for the provider's HTTPS `liteapp.weixin.qq.com` URL;
the client cannot supply a URL. A repeated action reuses the same binding-scoped
window and renews its fixed 10-minute lease, capped by any earlier binding
expiry. A ToolHub-owned janitor sweeps expired leases every 30 seconds without
polling browser tabs. Poll-observed activation, expiry, or failure and explicit
owner revocation still release immediately. Graceful Gateway shutdown drains
every tracked QR window before closing the browser adapter; after an ungraceful
exit, the existing deterministic profile recovery reclaims a leaked process on
the next acquisition. This surface requires the trusted desktop runtime's
`docker/compose.visible-browser.yaml` overlay; it fails explicitly when no
visible display is available and never falls back to the default browser.

## Speech Transcription

Speech is an optional adapter shared by WebChat microphone input and Telegram
voice notes. One SparkClaw ASR runtime wraps a single `Qwen/Qwen3-ASR-0.6B`
instance and exposes both the OpenAI-compatible complete-WAV endpoint and a
native stateful realtime endpoint. WebChat captures native mono PCM with one
AudioWorklet and statefully resamples it once to 16 kHz PCM16. The selected
browser-local microphone is tried first, with one fallback to the system
default when that device is missing. A device picker and short live-level
preview do not persist audio.

The public Gateway surface is:

```text
GET  /api/speech/status
POST /api/speech/transcriptions
POST /api/speech/realtime-sessions
DELETE /api/speech/realtime-sessions/{id}
GET  /api/speech/realtime?ticket=...  (WebSocket upgrade)
```

Gateway validates media type, WAV structure, duration, upload size, request ID,
language, and authenticated owner/session scope before calling the configured
allowlisted endpoint. One request deadline covers admission wait and inference.
The transcription call is the readiness authority; the health result remains a
status projection and is not a prerequisite request. The adapter is disabled
and its endpoint and allowlist are empty by default. Enable it only after
explicitly configuring the service URL, allowed host, and served model name.

A realtime session starts AudioWorklet capture only after the Gateway has
authenticated the owner/session, reserved the shared model slot, issued a
single-use 30-second ticket, upgraded the WebSocket, and emitted `ready` for
the fixed 16 kHz mono PCM16 format. WebChat sends contiguous 100 ms frames and
replaces one out-of-draft partial preview by revision. A healthy stream flushes
the same model state to one authoritative final and makes no batch request.
The browser-local silence controller is Off by default; Standard and Patient
stop after 1.2 or 2.0 seconds of trailing silence only after confirmed speech.

Ticket, connection, or readiness failure before capture visibly falls back to
batch-only recording. Transport, protocol, backpressure, device, or finalization
failure after capture begins closes and flushes the microphone boundary,
releases the realtime slot, and automatically submits exactly one complete WAV
containing the locally retained canonical PCM. Recording never continues after
that failure. Realtime and batch share one model admission limit.

A WebChat final transcript is inserted into the captured selection only while that
draft snapshot remains current and is never sent automatically. A changed draft
keeps the transcript as an in-memory candidate with explicit insert or dismiss
actions. Retryable busy, timeout, unavailable, and network failures keep the
same byte-identical WAV and request ID in memory for an explicit retry for up to
five minutes. Success, cancellation, expiry, session change, or a new recording
discards it. Transcription does not create a chat message, Agent run, Tool Call,
approval, or artifact. Audio bytes are not retained by Gateway; audit stores
bounded metadata and outcome only. Queue and concurrency limits return explicit
busy or unavailable states.

The shared transcriber records one `ModelCall` with lane `asr` for every batch
invocation and one for every realtime session, including Telegram voice notes.
The record contains backend profile, model, wall-clock latency, terminal status,
bounded error, and start/completion times; it never contains transcript or audio.

`GET /api/speech/status` reports `supports_streaming=true` and the structured
protocol/format projection only when the configured runtime advertises the
exact native contract. Otherwise WebChat retains the complete-WAV batch path
and does not claim live transcription. SparkClaw never splits or repeatedly
uploads WAV files to imitate streaming.

Configuration uses `SPARKCLAW_SPEECH_*`, including endpoint, allowlist, model,
language, timeout, duration, upload, concurrency, pending, and expected runtime
version.

## Infinimesh Info

Infinimesh Info is the optional production provider for `web.search` and the
existing `browser.weather` Workflow. Public search uses `POST /v1/info/query`.
Weather exclusively uses the structured `POST /v1/info/weather` contract; no
generic query fallback or free-text weather parser remains. Both paths preserve
request IDs, obtain one-shot `info.basic` tokens through the existing in-memory
wallet, send them as `PrivateToken`, and apply bounded retries, deadlines, and
response sizes.

Info query results are already aggregated upstream. SparkClaw persists them as
`info_search_result_v2`, preserving Info's summary, fact, conflict, freshness,
uncertainty, source-ID edges, usage metadata, and final `sources[]` order. It
does not rerank or resynthesize these units. Upstream
`recommended_next_actions` remains only in the raw untrusted result and never
enters model evidence, Workflow control, or the user answer.

The answer projection is `info_aggregate_projection_v4`. It validates unique
source IDs and citation edges, admits whole facts and conflict viewpoints in
Info order, excludes snippets, and reports capacity or invalid-reference
omissions as `partial`. Facts and viewpoints retain their own citation markers;
freshness, uncertainty, and non-linkable citation labels remain visible. A
deterministic renderer completes `browser.internet_search` without another
model finalizer. Browser target identification separately reads the raw ordered
source view, skips non-linkable entries, and retains the existing HTTPS,
DNS/IP, and redirect safety gates. A read-only decoder supports persisted
pre-v2 search results; new ToolHub calls write only v2.

The weather adapter instead validates fixed metric current/hourly/daily fields
and the normalized condition vocabulary, then exposes a typed card payload.
Provider coordinates are discarded before ToolHub output, traces, or card
rendering. Malformed or incomplete weather responses fail explicitly.

Configuration uses `SPARKCLAW_INFINIMESH_INFO_LICENSE_ID` together with
`SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY` (or
`SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE`). Token issuance authenticates
with `Authorization: Bearer <ilk_v1...>`; the retired entitlement proof, device
attestation, and license proof environment variables are not accepted. The key
must embed the configured license ID and must never appear in public config,
logs, traces, or artifacts.

## ISCP Bridge

The optional ISCP Bridge is the current legacy inbound process between JingSi App and the
loopback Gateway. It uses the ISCP v0.1.0 Core SDK for device identity, Trust
Grants, Session Hello/Ready, proof of possession, and SecureEnvelope. The Bridge
maps encrypted `agent.*.v1` requests to one loopback Gateway endpoint. Gateway
remains authoritative for sessions, runs, policy, approvals, events, the passive
notification inbox, and audit. The endpoint uses bearer authentication when
Gateway auth is enabled and explicitly supports token-free loopback dispatch
when it is disabled.

The Bridge never accepts an ITES token and never exposes an unauthenticated LAN
listener. Its production identity key lives in the operating-system keyring,
Relay credentials rotate independently, and unsupported Gateway capabilities
are absent from the manifest. `agent.notification.deliver.v1` stores LocalMind
document/comment mentions without starting Agent Runtime; the existing
conversation delivery remains only as a temporary legacy fallback before target
cutover. Gateway exposes the owner-scoped inbox through list/read APIs and a
global authenticated SSE stream. See [ISCP Bridge](iscp-bridge.md) for
enrollment, the versioned schema, current LocalMind DeviceProof/Trust Grant
renewal limits, App CI mock, and GB10 operation.

LocalMind's external-controller enrollment direction is not retained by the
target design. LocalMind's Access Gateway instead joins SparkClaw's ISCP Domain
using a one-time ISCP Pairing Ticket presented locally by SparkClaw. LocalMind
connects to ISCP and redeems it through standard Provisioning. ISCP owns the
ticket and protocol admission, Trust Grants, Relay credentials, secure sessions,
rotation, and transport revocation. Once that authenticated channel is ready,
SparkClaw issues a separate single-use MCP Access Ticket; LocalMind redeems it
through ISCP to activate the local owner-approved conversation-scoped MCP
Binding. Neither
ticket is reused during ordinary MCP calls. After LocalMind is newly enrolled and
validated through the generic ISCP MCP gateway, its Bridge manifest entries, grants,
dispatch branches, passive/conversation fallbacks, configuration, and tests are
deleted. Shared Bridge components still required by JingSi remain frozen; JingSi
does not join MCP and will receive a separate binding design later. The LocalMind
Workspace MCP section above is the opposite, outbound direction and remains
supported.

The SparkClaw-owned inbound MCP phase is now implemented behind the
default-off generic `mcp` connector: strict MCP `2025-06-18`, hash-only
single-use MCP Access Ticket redemption over authenticated ISCP identity,
durable schema-v2 conversation Binding and operation recovery, the single
`sparkclaw.conversation.send` business tool, ordinary message routing, bounded
workspace filename resolution, shared Delivery, and encrypted Bridge
request/response dispatch. This is not yet a
production LocalMind connection: standard ISCP PairingTicket/Provisioning
integration, a deployable external Access Gateway, and live Relay validation
remain required before cutover and legacy deletion.

## MCP And Happy

Gateway can discover and execute tools from configured stateless Streamable
HTTP MCP servers. Each server is initialized independently with MCP
`2025-06-18`; discovery and calls send one JSON-RPC message per POST with
`Accept: application/json, text/event-stream`. The client accepts both plain
JSON responses and a single response wrapped in SSE `data:` frames, preserves a
returned `Mcp-Session-Id`, and bounds deadlines and response sizes.

Happy uses two independent endpoints. Happy Team exposes Cloud Agent task tools,
while the personal bridge is reachable only while the member machine and
`happy-agent mcp` process are running:

```json
"mcp_servers": {
  "happy-tasks": {
    "url": "https://happy.example.com/v1/team/mcp",
    "token_env": "HAPPY_TEAM_MCP_TOKEN",
    "expected_server_name": "happy-team-tasks",
    "allow_mutations": true,
    "tool_allow": [],
    "tool_deny": []
  },
  "happy-bridge": {
    "url": "http://127.0.0.1:8790/",
    "token_file": "~/.happy/mcp.token",
    "expected_server_name": "happy-bridge",
    "allow_mutations": true,
    "tool_allow": [],
    "tool_deny": []
  }
}
```

Discovered names are registered atomically as
`mcp.<server-name>.<remote-tool-name>` with their input/output schemas and MCP
annotations. Only an explicit MCP `readOnlyHint: true` classifies a tool as an
unapproved read; `list_` and `get_` names carry no authority. Destructive or
open-world annotations override a contradictory read-only annotation. All
unannotated tools are mutations, remain hidden while `allow_mutations` is false,
and stop at the normal approval boundary when enabled. `approve_plan` and
`reject_plan` use a separate capability that is never exposed to chat model
tool selection.

Generic mutations default off. Existing Happy configurations must explicitly
set `allow_mutations: true` to retain create, message, stop, cancel, approve, and
reject operations. `tool_allow` and `tool_deny` match exact remote names and can
only reduce the discovered catalog; an empty allow list adds no restriction and
allow/deny overlap is rejected. The same policy gates the fixed direct calls
used by Happy plan synchronization, so production configurations should
populate `tool_allow` from the exact tools the deployment intends to use.

The endpoints degrade independently. An offline personal bridge does not remove
or fail the Happy Team task endpoint. A Team 401 asks the owner to mint a new
personal MCP token; a bridge 401 asks the owner to verify the local token file.
Current status is available through `GET /api/mcp-servers`, and one server can be
rediscovered with `POST /api/mcp-servers/{name}/refresh`.

All task details, transition history, plans, session metadata, and transcripts
remain untrusted observations. They are archived and summarized through the
normal observation path and never become instructions or authority for another
tool call. `wait_for_idle` receives a call deadline longer than its requested
wait; callers may use bounded `get_session` polling instead.

Every generic MCP result is recursively redacted before persistence. Workflow
state receives at most 16 KiB of canonical result data, while a separate
sanitized archive projection retains at most 16 MiB of the MCP envelope. Secret
keys, bearer values, signed URLs, and large base64 values cannot enter a pending
external MCP ToolCall or Approval.

When `happy-tasks` is configured, one bounded worker polls
`list_tasks {"status":"WAITING_APPROVAL"}` every 60 seconds and creates a typed
approval keyed by Happy task ID. It fetches `get_task_plan` for new items and
retries while the member machine is offline. The inbox shows the task title,
goal, and plan; an unavailable plan cannot be approved or edited. The owner may
edit only plan text. Approval calls fixed `approve_plan {taskId, editedPlan?}`;
rejection calls fixed `reject_plan {taskId}`. Gateway updates local state only
after the remote action succeeds. A business error is followed by authoritative
`get_task`; a task no longer in `WAITING_APPROVAL` closes as resolved elsewhere.
Items absent from a later waiting list receive the same reconciliation check.

### MCP Tuning Keys

Generic `mcp_servers.<name>` entries accept `request_timeout_seconds`
(default 30, max 3600), `discovery_refresh_seconds` (default 60, max 86400),
`response_body_max_bytes` (default 4 MiB, max 32 MiB), `allow_mutations`
(default false), and exact-name `tool_allow`/`tool_deny`. Generic state/archive
projection bounds are fixed at 16 KiB/16 MiB. The dedicated
`localmind` entry instead accepts `request_timeout_seconds` (default 30),
`long_call_grace_seconds` (default 10), `refresh_interval_seconds`
(default 300), `max_response_bytes`, `state_output_max_bytes`
(default 16 KiB), and `archive_output_max_bytes` (default 16 MiB). The two
endpoint and projection-tuning key families remain intentionally disjoint; a
key from the wrong family is rejected at configuration load.

Inbound MCP access is domain-scoped through `mcp_access.local_domain_id`
(default `sparkclaw-local`): issued access tickets are bound to this domain
and redemption from a peer in another ISCP domain is rejected. Change it
before issuing tickets when the host joins a multi-domain ISCP topology.

## Connector Control, Binding, And Status APIs

WebChat discovers registered channels and manages their explicit opt-in through
one versioned API. Account setup remains a separate lifecycle:

```text
GET    /api/connectors
PATCH  /api/connectors/{channel}
GET    /api/notification-bindings
POST   /api/notification-bindings/{channel}/start
GET    /api/notification-bindings/{id}
POST   /api/notification-bindings/{id}/browser
DELETE /api/notification-bindings/{id}
GET    /api/delivery-endpoints
```

The PATCH body contains `enabled` and the last observed `expected_version`.
The static channel `Enabled` value is a bootstrap default for an owner without a
persisted choice, not an operator gate; `/api/config.operator_enabled` reports
that static value while connector `enabled` reports the owner-effective state.

Gateway preloads every owner's settings before listening, restores every
persisted choice after restart, and fails startup if the all-owner read fails.
Connector Registry is the only supported setting writer; direct SQL changes at
runtime are not observed. Existing bindings never imply opt-in.

Disabling a channel blocks that owner's new polling, binding setup, outbound
Provider, and Endpoint Registry access while retaining encrypted credentials and
bindings. A shared per-channel worker continues for other enabled owners. Work
already dispatched from the disabled owner's source can finish and deliver its
exact admitted reply; persisted but undispatched input pauses and resumes after
re-enable. See
[Per-owner connector activation](connector-owner-runtime-design.md).

The UI displays software, account, recipient, conversation, capabilities, and
status from the Endpoint Registry. It does not infer a destination from channel
names or expose native recipient IDs.

## Verification

Integration changes require focused disabled/unavailable tests, secret-redaction
checks, host and timeout enforcement, Store backend parity, binding lifecycle,
authorization isolation, payload limits, retry semantics, connector shutdown,
and end-to-end Message Runtime/Delivery Gateway tests. Credential-gated live
checks supplement but do not replace deterministic local tests.
