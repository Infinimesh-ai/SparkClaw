# Unified Third-Party ISCP MCP Access Design

> Language: English | [简体中文](../zh-cn/docs/unified-third-party-access-design.md)

> Status: the SparkClaw-owned local Runtime, owner management surface, binding
> schema, conversation tool, and MCP result provider are implemented in the
> active worktree. Production ISCP authority integration, a deployable external
> Access Gateway, live Relay validation, LocalMind cutover, and legacy removal
> remain rollout work.

## Decision

Inbound external MCP is one provider-neutral ordinary-conversation channel. It
does not project SparkClaw Catalog leaves, Workflow Profiles, ToolHub tools, or
approval grants to the remote client.

An active binding exposes exactly one business tool:
`sparkclaw.conversation.send`. The tool submits owner-authored text and optional
locators for existing media under the linked SparkClaw workspace. Message
Runtime then performs ordinary semantic routing. MCP initialization, ticket
redemption, operation status/result, cancellation, and binding revocation are
protocol or lifecycle controls, not additional business abilities.

This design applies only to third-party inbound access to SparkClaw. The
workspace-scoped SparkClaw-to-LocalMind MCP client is the opposite direction and
is unchanged. JingSi does not join this MCP contract; its current shared Bridge
surface remains frozen until a separate binding design replaces it.

## Why MCP And ISCP Are Both Required

ISCP owns network membership and authenticated transport. Its authorities
define, sign, verify, and consume Pairing Tickets; verify Device Proof; issue
Trust Grants and Relay credentials; establish encrypted sessions; rotate
credentials; and enforce transport revocation.

SparkClaw owns application authorization. After an authenticated ISCP session
exists, SparkClaw issues a separate short-lived, single-use MCP Access Ticket.
The external device redeems it through that authenticated session to activate a
durable conversation-scoped MCP Binding.

The two tickets are never interchangeable:

| Credential | Authority | Purpose | Durable result |
|---|---|---|---|
| ISCP Pairing Ticket | ISCP authority | Admit a device to the SparkClaw ISCP Domain | ISCP device/session credentials |
| MCP Access Ticket | Local SparkClaw | Authorize inbound ordinary conversation | SparkClaw MCP Binding |

Ordinary MCP calls reuse neither ticket. They rely on current authenticated
peer identity plus the active binding.

## Target Topology

```text
External MCP client
  -> external ISCP MCP Access Gateway
  -> authenticated ISCP session / SecureEnvelope / Relay
  -> SparkClaw-side ISCP MCP Access Gateway
  -> local MCP protocol adapter
  -> MessageEnvelope (third_party_device)
  -> ordinary routing and Workflow Runtime
  -> WorkflowResult -> Delivery Gateway -> MCP Provider
  -> correlated durable MCP operation result
```

Both gateway roles connect outbound to Relay. The local SparkClaw MCP service
opens no public inbound application port for ISCP. As an explicit alternative,
the owner can enable direct LAN MCP at `/mcp` on the existing WebChat ingress
port `18790`; Nginx forwards that exact route to the Docker-internal Gateway.
That transport-only option does not weaken the Binding, Runtime, Store, or
Delivery contract and is absent while its owner switch is off.

## Component Ownership

| Component | Responsibility |
|---|---|
| ISCP authority and Relay | Device admission, authenticated encrypted sessions, credential rotation, and transport revocation |
| External Access Gateway | Hold the external device identity, redeem pairing, speak MCP `2025-06-18`, and carry frames over ISCP |
| SparkClaw MCP adapter | Validate MCP envelopes, redeem MCP Access Tickets, authenticate bindings, and manage durable operations |
| Message Runtime | Normalize the call as ordinary third-party-device input and run normal semantic routing |
| Workflow Runtime | Execute the selected registered Workflow; ordinary conversation uses `conversation.answer` r3 |
| Store | Persist hash-only tickets, schema-v2 bindings, invocation provenance, operation state, and redacted audit across memory/file/PostgreSQL |
| Delivery Gateway and MCP Provider | Validate complete multipart results and encode correlated MCP content atomically |

The external requester remains source provenance. The local owner-authorized
SparkClaw principal remains the Workflow actor and executor.

## Pairing And Binding

1. The owner enables the default-off generic `mcp` connector.
2. SparkClaw requests an `iscp.pairing_ticket.v2` from the configured authority
   adapter and shows the signed ticket once. SparkClaw retains only a non-secret
   onboarding receipt.
3. The owner transfers the ticket once to the external Access Gateway.
4. The gateway redeems it through standard ISCP PairingTicket/Provisioning and
   establishes an authenticated session in SparkClaw's Domain.
5. SparkClaw issues a separate copy-once MCP Access Ticket with fixed
   `scope: conversation`.
6. The authenticated external peer redeems that ticket. SparkClaw atomically
   consumes the secret hash and creates an MCP Binding with schema version 2,
   the authenticated domain/device/key thumbprint, linked local session, owner
   and actor, conversation scope, and authorization revision.
7. Reconnects must present the same authenticated peer identity. Rotation to a
   new device key requires fresh pairing and MCP authorization.

Pre-schema-v2 or non-conversation bindings fail closed. They are never silently
widened into conversation access. Disabling the connector, suspending or
revoking the binding, owner mismatch, peer mismatch, or linked-session mismatch
blocks ingress and delivery.

## MCP Channel Contract

The local service speaks strict MCP `2025-06-18`. Its first release keeps
durable SparkClaw operation controls rather than advertising standard MCP Tasks.

Business surface:

- `sparkclaw.conversation.send`

Binding-scoped lifecycle controls:

- `sparkclaw.operation.get`
- `sparkclaw.operation.result`
- `sparkclaw.operation.cancel`

No `sparkclaw.route.*`, `files.search`, directory listing, resource browser,
workspace reader, Catalog projection, or remote approval tool is listed or
callable. The client cannot select a route, operation, effect, Workflow revision,
model lane, owner, session, endpoint, MIME type, artifact identity, hash, or
workspace root.

Every `tools/call` requires a bounded deadline and an idempotency key. The
binding and key derive stable invocation, message, run, and operation identities.
A repeated equivalent request observes the same durable operation; a different
fingerprint under the same key fails. Cancellation is terminal and does not
promise rollback. Restart recovery preserves operation/result correlation.

## Conversation And Workspace Media

`sparkclaw.conversation.send` accepts non-empty text, one to eight ordered media
locators, or both. A locator supplies exactly one of:

- `path`: an exact workspace-relative path;
- `name`: a complete case-sensitive basename;
- `query`: an incomplete filename or short owner-authored description.

The adapter validates syntax but does not resolve files. The message enters
ordinary routing. When `conversation.answer` r3 is selected, its fixed workflow
is:

```text
detect_response_media -> answer
```

The detection node reuses a governed direct attachment/path, then tries exact
basename lookup, then uses the shared bounded filename-only search after an
exact miss or for an incomplete description. Search never reads file content or
returns previews. It ranks positive filename matches and uses workspace-relative
lexical path order as the final stable tie-break. Each locator selects only
Top-1; explicit multiple locators retain their input order.

A complete zero-result lookup returns `file_not_found` and asks the owner for a
better name or direct attachment. Failed, timed-out, truncated, permission-
incomplete, unsafe, or partially traversed lookup returns a blocked typed reason
and sends no provisional candidate. Absolute paths, path escape, symlinks,
directories, special files, duplicates, cross-workspace objects, and changed
objects fail atomically.

Detection freezes workspace-relative resource references, actual byte counts,
content types, artifact identities, and SHA-256 hashes. The answer node cannot
search, add, remove, refresh, or substitute a resource. It revalidates frozen
objects before completion. A normal answer may return text plus the frozen
media; a pure publish may return media only.

See [External MCP conversation convergence](external-mcp-conversation-design.md)
for the complete schema and decision contract.

## Result Delivery

Every terminal route produces the shared `WorkflowResult` and
`DeliveryRequest`. Endpoint Registry resolves the binding-scoped MCP source;
Delivery Gateway invokes one generic MCP Provider.

The Provider maps ordered result parts as follows:

| SparkClaw part | MCP content |
|---|---|
| text | `text` |
| image | native `image` with base64 data and MIME type |
| audio | native `audio` with base64 data and MIME type |
| other file | embedded `resource` blob with an operation/part URI |

Local paths and `workspace://` identities never cross the protocol boundary.
Every binary object must belong to the binding's linked session and still match
its frozen byte count and hash. All parts are prepared before Store mutation;
one invalid part leaves the operation without a partial result.

Raw binary media is capped below 3 MiB so base64 expansion plus bounded text,
content metadata, and structured operation projection fit the 4 MiB encoded
MCP result envelope. The final encoded envelope remains authoritative and is
checked before persistence.

## Policy, Audit, And Management

The `mcp` connector is default-off. Owner settings gate pairing-ticket issue,
MCP ingress, endpoint visibility, and Provider availability. A retained ticket
or binding never implies connector enablement.

WebChat exposes connector state, copy-once ISCP and MCP ticket flows, fixed
conversation scope, linked binding state, and ticket/binding revocation. It has
no Catalog grant picker.

Audit covers ticket issue/redemption/replay/revocation, binding activation and
revocation, peer denials, tool listing/invocation, response-media decision and
bounded search outcome, operation create/replay/conflict/cancel/terminal state,
and result delivery. Records use stable reason codes and omit secrets, absolute
paths, file content, and raw result blobs.

## LocalMind Cutover And JingSi Boundary

LocalMind's legacy external-controller enrollment, `agent.*.v1` conversation
fallback, and passive Bridge path remain temporary until its Access Gateway is
newly paired into SparkClaw's Domain, redeems a fresh conversation-scoped MCP
Access Ticket, and passes live Relay E2E. Then delete the LocalMind Bridge
manifest entries, grants, dispatch branches, fallbacks, configuration, tests,
and guidance. Do not add compatibility features to that legacy path.

Bridge components still required by JingSi remain unchanged until JingSi's
separate binding project owns their replacement. This does not preserve a hidden
LocalMind fallback and does not bring JingSi into MCP.

## Acceptance Criteria

- An active binding lists exactly one business tool and three operation controls;
  no route leaf or workspace search tool is remotely callable.
- Text-only calls use ordinary semantic routing and the shared message/runtime
  path.
- Direct path, exact basename, and incomplete filename queries select governed
  workspace media using one shared filename-only implementation and stable Top-1.
- Zero results clarify; incomplete lookup blocks; neither emits candidates or
  partial media.
- `conversation.answer` r3 runs detection before answer and detects object
  changes before result construction.
- MCP results preserve ordered text/image/audio/file content without local path
  disclosure and remain atomic under part or envelope failure.
- Ticket, binding, invocation, and operation behavior is identical across
  memory, file, and PostgreSQL backends.
- Connector disablement, revocation, idempotency, deadline, cancellation,
  recovery, endpoint isolation, and redacted audit stay fail closed.
- Gateway build/test/vet, WebChat test/build, bilingual docs checks, Compose,
  doctor, and focused live ISCP validation are green before production rollout.
