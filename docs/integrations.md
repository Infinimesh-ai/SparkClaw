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

## LocalMind Workspace MCP

LocalMind is an optional workspace-scoped MCP integration and ships disabled.
Enable it by adding the fixed `localmind` entry to the Gateway configuration;
the default configuration keeps `mcp_servers` empty:

```json
{
  "mcp_servers": {
    "localmind": {
      "transport": "streamable-http",
      "url_env": "LOCALMIND_MCP_URL",
      "bearer_token_env": "LOCALMIND_MCP_TOKEN",
      "namespace": "localmind",
      "expected_server_name": "localmind-workspace",
      "protocol_version": "2025-06-18",
      "allow_mutations": false,
      "allow_private_http": false,
      "tool_allow": [],
      "tool_deny": []
    }
  }
}
```

Set `LOCALMIND_MCP_URL` to the exact
`/api/workspaces/<workspace-id>/mcp` endpoint and set
`LOCALMIND_MCP_TOKEN` to a credential bound to that workspace. Start with a
read-only LocalMind credential and leave `allow_mutations` false. URL and token
values are resolved from the environment at every refresh, omitted from public
Gateway configuration, and never accepted from an owner utterance. `tool_allow`
and `tool_deny` can only reduce the credential-visible catalog.

Gateway initializes through the shared MCP 2025-06-18 Streamable HTTP client,
verifies `localmind-workspace`, discovers the credential scope, and atomically
refreshes namespaced `localmind.*` ToolHub entries. The model sees at most 16
matching directory entries and only the selected tool's full schema. Read-only
operations require no approval. Every mutation requires owner approval;
destructive or open-world operations additionally require deep verification.
Remote execution remains subject to LocalMind authorization, DLP, and audit and
is never represented as running in SparkClaw's local sandbox.

Canonical results come from `structuredContent.result`; MCP `isError` remains a
failed tool call. Text fallback, Resources, and archived large results are
bounded and treated as untrusted evidence. Authentication or scope changes
trigger a fresh discovery. Reads may retry once after a successful refresh,
while mutations are never replayed automatically.

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
window. When polling observes activation, expiry, or failure, or when the owner
revokes the binding, Gateway releases that managed browser session and closes
the Chromium window. This surface requires the trusted desktop runtime's
`docker/compose.visible-browser.yaml` overlay; it fails explicitly when no
visible display is available and never falls back to the default browser.

## Speech Transcription

Speech is an optional OpenAI-compatible transcription adapter shared by
WebChat microphone input and Telegram voice notes. WebChat records bounded mono
16 kHz PCM16 WAV and posts it to:

```text
GET  /api/speech/status
POST /api/speech/transcriptions
```

Gateway validates media type, WAV structure, duration, upload size, request ID,
session, and language before calling the configured allowlisted endpoint. The
adapter is disabled and its endpoint and allowlist are empty by default. Enable
it only after explicitly configuring the service URL, allowed host, and served
model name.

A WebChat transcript is inserted into the current draft and is never sent
automatically. Transcription does not create a chat message, Agent run, Tool
Call, approval, or artifact. Audio bytes are not retained; audit stores bounded
metadata and outcome only. Queue and concurrency limits return explicit busy or
unavailable states.

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

SparkClaw maps summary, non-empty key facts, public source metadata, snippets,
and citations into stable evidence refs. It chooses a query-relevant bounded
projection before model use. Missing summary text does not hide usable
structured facts, and provider status text is not presented as an answer.
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
    "expected_server_name": "happy-team-tasks"
  },
  "happy-bridge": {
    "url": "http://127.0.0.1:8790/",
    "token_file": "~/.happy/mcp.token",
    "expected_server_name": "happy-bridge"
  }
}
```

Discovered names are registered atomically as
`mcp.<server-name>.<remote-tool-name>` with their input/output schemas and MCP
annotations. Read tools can run in the `coding.agent_manage` Workflow; remote
mutations stop at the normal approval boundary. `approve_plan` and
`reject_plan` use a separate capability that is never exposed to chat model
tool selection.

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
Disabling a channel cancels its inbound runtime and blocks its outbound Provider
and Endpoint Registry entries. It retains encrypted credentials and bindings so
the owner can re-enable the channel without repeating setup. Existing bindings
never imply opt-in. A persisted enabled choice is restored on Gateway restart.

The UI displays software, account, recipient, conversation, capabilities, and
status from the Endpoint Registry. It does not infer a destination from channel
names or expose native recipient IDs.

## Verification

Integration changes require focused disabled/unavailable tests, secret-redaction
checks, host and timeout enforcement, Store backend parity, binding lifecycle,
authorization isolation, payload limits, retry semantics, connector shutdown,
and end-to-end Message Runtime/Delivery Gateway tests. Credential-gated live
checks supplement but do not replace deterministic local tests.
