# ISCP Bridge

> Language: English | [简体中文](../zh-cn/docs/iscp-bridge.md)

The SparkClaw ISCP Bridge is a separate transport process that turns authenticated
ISCP v2 sessions into the provider-neutral `agent.*.v1` API owned by the local
Gateway. It does not call Agent Runtime or ToolHub directly, and it never receives
or stores an ITES access or refresh token.

## Supported Surface

The Bridge advertises only capabilities implemented by the current Gateway:

- `agent.sessions` v1;
- `agent.conversation` v1;
- `agent.streaming` v1;
- `agent.activities` v1;
- `agent.approvals` v1.

Workspace and file capabilities are intentionally absent until their remote
authorization and upload/download contracts are implemented end to end. The
versioned JSON Schema is
[`packages/protocol/iscp-bridge.v1.schema.json`](../packages/protocol/iscp-bridge.v1.schema.json).

## Security Boundaries

- The production identity key backend is the operating-system keyring. The file
  backend is rejected in the `production` profile and exists only for local labs.
- Enrollment and Gateway token files must be regular files with mode `0600` on
  Unix systems. Long-term device keys are not written into config or enrollment
  bundles when the keyring backend is used.
- Production Relay endpoints must use HTTPS and WSS. Every envelope submission
  carries an ISCP proof of possession in addition to the short-lived Relay access
  credential.
- The Gateway dispatch endpoint is loopback-only and refuses requests unless
  Gateway authentication is enabled. A paired client token or
  `SPARKCLAW_API_TOKEN` can authenticate the Bridge process.
- Session Hello and Ready objects are signed and routed in the public
  `sparkclaw.iscp.relay_frame.v1` wrapper. Business requests, responses, and
  events are sent only after Ready key confirmation and always use ISCP
  `SecureEnvelope` with `task.invoke` or `task.result` payload types.
- The Bridge checks peer identity, Domain, Trust Grant audience, confirmation
  thumbprint, permission, Relay constraint, revocation epoch, expiry, Hello time
  window, endpoint binding, and envelope sequence.

## Enrollment

Create the device identity and a public enrollment request on the GB10:

```bash
cd services/gateway
go run ./cmd/iscp-bridge enroll \
  -identity-dir ../../data/iscp-bridge/identity \
  -domain DOMAIN_ID \
  -device DEVICE_ID \
  -hardware gb10 \
  -output ../../data/iscp-bridge/enrollment-request.json
```

The default stores the Ed25519 private key in the system keyring. For a disposable
local test, add `-key-backend file` and use a `local-lab` Bridge config.

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

Enable Gateway authentication and place the same bearer value, or a dedicated
paired-client token, in a private file:

```bash
install -d -m 700 data/iscp-bridge
install -m 600 /dev/null data/iscp-bridge/gateway.token
```

Use [`configs/iscp-bridge.example.json`](../configs/iscp-bridge.example.json) as
the non-secret configuration. Start Gateway and Bridge separately:

```bash
cd services/gateway
go run ./cmd/sparkclaw -config ../../configs/sparkclaw.default.json
go run ./cmd/iscp-bridge run -config ../../configs/iscp-bridge.example.json
```

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

Credential refresh is automatic. For key or Domain rotation, obtain a new Cloud
bundle, replace the enrollment file atomically with mode `0600`, then restart the
Bridge. For device revocation, revoke it in Cloud first and stop the service; Relay
will reject subsequent connections. Delete the operating-system keyring entry only
when permanently decommissioning the GB10.

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
