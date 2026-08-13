# ISCP Bridge

> Language: English | [简体中文](../zh-cn/docs/iscp-bridge.md)

The SparkClaw ISCP Bridge is a separate transport process that turns authenticated
ISCP v2 sessions into the provider-neutral `agent.*.v1` API owned by the local
Gateway. It does not call Agent Runtime or ToolHub directly, and it never receives
or stores an ITES access or refresh token.

This document describes the current shared Bridge. LocalMind's use of its
enrollment direction is legacy: SparkClaw sends an enrollment request and the
external LocalMind controller returns the bundle and grants used by the Bridge.
The target does not reverse that legacy flow by adding a SparkClaw-specific ISCP
credential service. Instead, LocalMind's Access Gateway enrolls as a new device
in SparkClaw's ISCP Domain with a one-time Pairing Ticket presented locally by
SparkClaw through the standard ISCP pairing capability. The external gateway
connects to ISCP and redeems it through Provisioning. ISCP defines, signs,
verifies, and consumes the ticket and owns device admission, Trust Grants, Relay
credentials, session security, rotation, and transport revocation. Once the
authenticated ISCP session is ready, SparkClaw separately issues and consumes a
single-use MCP Access Ticket over that session to activate the durable local
Route MCP Binding. That application ticket does not admit a device to ISCP and
is not a public claim service.

This document makes no decision about JingSi's future credential flow.

The replacement architecture uses one Route MCP Service carried through a generic
ISCP MCP Access Gateway. Its SparkClaw-owned local runtime and encrypted Bridge
dispatch are implemented; production PairingTicket/Provisioning integration,
the deployable external gateway, and live Relay validation remain pending. See
[Unified third-party ISCP MCP access](unified-third-party-access-design.md).
Do not add LocalMind callers, capabilities, or compatibility features to the
legacy Bridge. After LocalMind passes the new path, delete its Bridge
registrations, grants, branches, fallbacks, configuration, tests, and guidance.
Keep only the minimum frozen shared surface still required by JingSi; JingSi does
not join MCP and its later binding project owns final Bridge retirement. The
outbound SparkClaw-to-LocalMind workspace MCP client is outside that deletion
scope.

## Supported Surface

The Bridge advertises only capabilities implemented by the current Gateway:

- `agent.sessions` v1;
- `agent.conversation` v1;
- `agent.streaming` v1;
- `agent.activities` v1;
- `agent.approvals` v1;
- `agent.notifications` v1.

`agent.notification.deliver.v1` accepts only a structured LocalMind
`document_mention` or `comment_mention`, a bounded deep link, and its occurrence
time. Gateway durably writes the owner-scoped inbox record before returning
`status: "ok"`. This path does not create an Agent session, message, run, model
call, tool call, or approval. The existing session-create and message-send path
remains available to LocalMind only within the legacy chain before target
cutover. Cutover removes LocalMind authorization and dispatch through both paths;
it does not remove JingSi's current use of shared request types.

Workspace and file capabilities are intentionally absent until their remote
authorization and upload/download contracts are implemented end to end. The
versioned JSON Schema is
[`packages/protocol/iscp-bridge.v1.schema.json`](../packages/protocol/iscp-bridge.v1.schema.json).

## Security Boundaries

- The production identity key backend is the operating-system keyring. The file
  backend is rejected in the `production` profile and exists only for local labs.
- Enrollment files and configured Gateway token files must be regular files with
  mode `0600` on Unix systems. Long-term device keys are not written into config
  or enrollment bundles when the keyring backend is used.
- Production Relay endpoints must use HTTPS and WSS. Every envelope submission
  carries an ISCP proof of possession in addition to the short-lived Relay access
  credential.
- The Gateway dispatch endpoint is loopback-only. When Gateway authentication is
  disabled, the Bridge can dispatch without a placeholder token; when it is
  enabled, a paired client token or `SPARKCLAW_API_TOKEN` authenticates the
  Bridge process.
- Session Hello and Ready objects are signed and routed in the public
  `sparkclaw.iscp.relay_frame.v1` wrapper. Business requests, responses, and
  events are sent only after Ready key confirmation and always use ISCP
  `SecureEnvelope` with `task.invoke` or `task.result` payload types.
- The Bridge checks peer identity, Domain, Trust Grant audience, confirmation
  thumbprint, permission, Relay constraint, revocation epoch, expiry, Hello time
  window, endpoint binding, and envelope sequence.

## Enrollment

For LocalMind, the following procedure documents legacy operation only. Its
generated request and externally returned bundle must not be accepted by the
target MCP gateway or converted into a target peer binding. Existing LocalMind
devices must enroll through fresh ISCP PairingTicket/Provisioning into
SparkClaw's Domain during cutover, then redeem a fresh SparkClaw MCP Access
Ticket over the authenticated session.
JingSi continues its current required procedure until a separate design replaces
it.

Create the device identity and a public enrollment request on the GB10:

```bash
cd services/gateway
go run ./cmd/iscp-bridge enroll \
  -identity-dir ../../data/iscp-bridge/identity \
  -domain DOMAIN_ID \
  -device DEVICE_ID \
  -hardware gb10 \
  -proof-audience ENROLLMENT_AUDIENCE \
  -proof-challenge SHORT_LIVED_CHALLENGE \
  -output ../../data/iscp-bridge/enrollment-request.json
```

The default stores the Ed25519 private key in the system keyring. For a disposable
local test, add `-key-backend file` and use a `local-lab` Bridge config. Audience
and challenge must be supplied together; `-proof-nonce` is optional and otherwise
generated locally. The resulting `iscp.device.proof.v2` proves possession of the
device key and can be verified by the enrollment controller with
`identity.VerifyProof`.

The current LocalMind enrollment bootstrap does not issue an enrollment proof
audience/challenge and its strict request decoder does not yet accept
`device_proof`. Omit the proof flags only for that compatibility path. Device
proof enforcement requires a coordinated LocalMind controller update; SparkClaw
does not claim that legacy enrollment proves private-key possession.

The JingSi Cloud enrollment endpoint must accept the generated request after App
approval and return a `sparkclaw.bridge.enrollment.v1` bundle containing:

- Domain, device, Relay ID, HTTPS/WSS Relay endpoints;
- short-lived access and rotatable refresh credentials bound to this device;
- the Trust Root public identity;
- each allowed App peer identity plus inbound and outbound Trust Grants.

The Cloud enrollment URL and its authenticated request/response transport are not
defined by ISCP v0.1.0 or the Bridge requirements. They are deliberately left as
the Cloud integration interface instead of inventing an ITES-dependent endpoint.
An enrollment grant must never be placed in the long-lived bundle.

Write the returned bundle to `data/iscp-bridge/enrollment.json` with mode `0600`.
The Bridge rotates access and refresh credentials through the ISCP Relay refresh
endpoint and atomically replaces this file. Replacing the bundle and restarting
the service performs Domain change or re-enrollment.

## Gateway And Bridge Setup

The example config omits `gateway.token_file` for SparkClaw's default loopback,
no-auth Gateway. If Gateway authentication is enabled, place the bearer value or
a dedicated paired-client token in a private file and add its path to the Bridge
config:

```bash
install -d -m 700 data/iscp-bridge
install -m 600 /dev/null data/iscp-bridge/gateway.token
```

```json
"gateway": {
  "base_url": "http://127.0.0.1:18789",
  "token_file": "../data/iscp-bridge/gateway.token",
  "timeout_seconds": 30
}
```

Use [`configs/iscp-bridge.example.json`](../configs/iscp-bridge.example.json) as
the non-secret configuration. Start Gateway and Bridge separately:

```bash
cd services/gateway
go run ./cmd/sparkclaw -config ../../configs/sparkclaw.default.json
go run ./cmd/iscp-bridge run -config ../../configs/iscp-bridge.example.json
```

The Gateway container image includes `/usr/local/bin/iscp-bridge`, so a deployed
image does not need Go or a source checkout to provide the executable. The
default Compose service still runs Gateway as its entrypoint; run and supervise
the Bridge as a separate process or service with its enrollment volume and
keyring access.

The Bridge reconnects with bounded exponential backoff. Relay/device revocation
closes the process instead of retrying cached credentials. Operations continue in
Gateway if the App disconnects; `agent.event.resume.v1` resumes from a stored
cursor and `agent.operation.status.v1` resolves unknown mutation outcomes.

## Operations

Run Gateway and Bridge as separate services under the host service manager. Give
the Bridge read access only to its `0600` enrollment and Gateway token files, and
restart it after installing a new binary or replacing the enrollment bundle. A
rolling Gateway restart is safe: the Bridge reconnects locally and remote callers
can recover an uncertain mutation with `agent.operation.status.v1`.

Forward stdout and stderr to the host journal. Logs contain endpoint, request,
session, and operation identifiers, but credentials and decrypted message payloads
must not be added to log fields. Alert on repeated Relay authentication failures,
refresh failures, rejected Trust Grants, sequence violations, and reconnect loops.

Relay access and refresh credential rotation is automatic and atomically updates
the enrollment file. The current LocalMind controller refresh endpoint returns
only those two credentials; it does not renew Trust Grants or a complete
enrollment bundle. Trust Grant or bundle expiry therefore still requires a new
Cloud bundle, an atomic `0600` replacement, and a Bridge restart. Do not treat
Relay credential refresh as full long-lived enrollment renewal. For key or Domain
rotation, use the same re-enrollment procedure. For device revocation, revoke it
in Cloud first and stop the service; Relay will reject subsequent connections.
Delete the operating-system keyring entry only when permanently decommissioning
the GB10.

## Simulated Bridge

App CI can run the explicit local-lab mock without ISCP or Cloud credentials. It
accepts the same schema over an authenticated loopback endpoint and forwards it
to the protected Gateway adapter:

```bash
cp configs/iscp-bridge.example.json configs/iscp-bridge.local-lab.json
# In the local copy, set profile to local-lab and identity_key_backend to file.
cd services/gateway
go run ./cmd/iscp-bridge mock \
  -config ../../configs/iscp-bridge.local-lab.json \
  -listen 127.0.0.1:18792 \
  -client-token-file ../../data/iscp-bridge/mock-client.token
```

Send requests to `POST /v1/requests` with the mock client bearer token. The mock
cannot bind a non-loopback address and cannot run with the production profile.

## Approval And Idempotency Rules

Session create, message send, cancel, and approval resolution require an
`idempotency_key`. Message IDs and run IDs are derived from the authenticated
endpoint plus that key. Reuse with different input returns `conflict`.

Passive delivery also requires an idempotency key. Gateway deduplicates it
durably by `(endpoint_id, idempotency_key)` across restarts; replaying the same
payload returns the existing notification, while changing the payload returns
`conflict`. Deep links must be absolute HTTPS URLs without credentials; loopback
HTTP is accepted only for local development. WebChat never opens them without an
explicit owner click.

The authenticated owner-facing notification surface is:

```text
GET  /api/notifications
POST /api/notifications/{id}/read
POST /api/notifications/read-all
GET  /api/notifications/events/stream
```

The global SSE stream is owner-scoped. WebChat initializes from the durable list,
shows new records without switching the active session, and persists read state
through Gateway restarts.

Approval list responses include `preview_hash`. Resolution requires the approval
ID, `expected_state: "pending"`, decision, and current hash. Resolved or changed
approvals return `stale_state`; an identical replay returns the existing result
without executing the tool again.

## Verification

```bash
cd services/gateway
go test ./internal/iscpbridge ./internal/gateway ./cmd/iscp-bridge
go test -race ./internal/iscpbridge
go vet ./...
```

The service test performs an ISCP SDK Hello/Ready exchange, verifies both Trust
Grants, decrypts the encrypted capability manifest, sends an encrypted
`agent.session.list.v1` request, and decrypts the Gateway response.
