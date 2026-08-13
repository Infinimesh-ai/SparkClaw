# Unified Third-Party ISCP MCP Access Design

> Language: English | [简体中文](../zh-cn/docs/unified-third-party-access-design.md)
>
> Status: the SparkClaw-owned local runtime and owner management surface are
> implemented. Production ISCP authority integration, external Access Gateway
> deployment, Relay validation, LocalMind cutover, and legacy removal remain
> external rollout work. The status section below is authoritative for this
> boundary.

## Decision

SparkClaw will provide one provider-neutral MCP access path for LocalMind and
future third-party systems that choose this MCP contract:

1. SparkClaw owns a Route MCP Service whose tools are projections of eligible,
   registered Workflow route leaves.
2. A generic ISCP MCP Access Gateway carries MCP requests and results through
   authenticated ISCP sessions and `SecureEnvelope` messages.
3. LocalMind's Access Gateway first joins the same ISCP Domain as the SparkClaw
   device with a one-time ISCP Pairing Ticket. ISCP defines, signs, verifies,
   and consumes that ticket and owns device admission, Trust Grants, Relay
   credentials, sessions, encryption, rotation, and transport revocation;
   SparkClaw does not duplicate that control plane.
4. After that end-to-end channel exists, SparkClaw issues a separate one-time
   MCP Access Ticket. The enrolled external device redeems it only through its
   authenticated ISCP session to activate a durable, owner-approved MCP Binding.
   The ISCP ticket grants network membership; the SparkClaw ticket grants MCP
   application use. Neither ticket is used for ordinary MCP calls.
5. Every participating provider uses the same MCP, ISCP, Policy,
   approval, event, and audit contracts. SparkClaw does not add a
   LocalMind-specific or provider-name dispatch path.
6. MCP has its own protocol adapter but shares the managed business chain used
   by Web, Weixin, Telegram, and Timer. A `tools/call` enters the common receive
   layer, runs through the common Workflow/Policy core, and returns through the
   common result-sending layer with a registered MCP sender/provider.

This target applies to inbound access from a third party to SparkClaw. The
existing outbound SparkClaw-to-LocalMind workspace integration remains an MCP
client integration and is not routed through this gateway.

JingSi is explicitly outside this design. It does not implement this MCP client,
does not receive an ISCP Pairing Ticket for this MCP path, and does not
participate in MCP tool projection, invocation, result delivery, migration, or
acceptance. Its future SparkClaw binding will use a separately designed
mechanism. Until that design is
approved and implemented, this work leaves JingSi's current required path
unchanged and adds no new JingSi behavior.

## Implementation Status

The SparkClaw-owned local runtime and owner management surface are implemented:

- the generic `mcp` connector is disabled by default and gates ticket issue,
  authenticated ingress, endpoint visibility, and result delivery fail closed;
- SparkClaw issues a short-lived, single-use MCP Access Ticket, returns its
  secret only in the creation response, stores only its SHA-256 digest, and
  atomically redeems it through an authenticated ISCP peer identity into a
  durable device-bound MCP Binding;
- MCP `2025-06-18` initialization is strict, `tools/call` returns the standard
  `CallToolResult` shape, no standard Tasks capability is advertised, and
  deferred state uses binding-scoped `sparkclaw.operation.*` tools;
- projected tools resolve one exact Catalog leaf at confidence 1 without
  embedding or Tree scoring, then enter the existing Message, Workflow,
  Policy, Store, and Delivery chain with external requester and local executor
  identities kept separate;
- operation idempotency, bounded deadlines, cancellation, approval recovery,
  binding-revocation termination, terminal-state CAS, stale leaf filtering,
  restart recovery, and redacted MCP lifecycle audit are implemented for
  memory, file, and PostgreSQL stores;
- the ISCP Bridge recognizes `sparkclaw.mcp.iscp.v1`, injects the authenticated
  Domain/device/key/session identity, and carries the request and response in
  the established encrypted ISCP session through a loopback-only Gateway API.
- a default-off ISCP authority adapter requests only the standard
  `iscp.pairing_ticket.v2` object through an authenticated, time- and
  size-bounded outbound call; WebChat exposes authority readiness, copy-once
  Pairing Ticket presentation, Catalog-derived grant selection, separate MCP
  Access Ticket issuance, and ticket/Binding revocation;
- memory, file, and PostgreSQL persist only the non-secret ISCP onboarding
  receipt. The signed Pairing Ticket remains only in the creation response and
  is absent from list, audit, restart, and public-config surfaces.

The implementation is not yet a production external onboarding path. The
configured ISCP authority still needs to expose and integrate the exact
PairingTicket/Provisioning bootstrap used to enroll a new external gateway into
SparkClaw's Domain. A deployable external Access Gateway, live Relay validation,
LocalMind cutover, and deletion of LocalMind's legacy `agent.*.v1`
authorization remain work to complete. The current ISCP repository defines the
Pairing Ticket and cryptographic primitives but no production authority HTTP
endpoint, so the configured adapter contract must be implemented by the actual
Domain authority before readiness can be claimed. SparkClaw does not sign,
verify, consume, or persist that protocol ticket and must not replace missing
authority functions with a private credential protocol. JingSi and the outbound
SparkClaw-to-LocalMind MCP client remain unchanged.

For pre-ISCP integration testing only, an explicit default-off LAN transport
may replace ISCP reachability between SparkClaw and an external MCP client on
the same trusted network. It does not simulate or claim ISCP security. The
SparkClaw MCP Access Ticket, atomic redemption, durable Binding, granted route
leaves, Workflow execution, approval, operation, Message, and Delivery
contracts remain unchanged. The LAN endpoint must be path-isolated, disabled
after the test, and never treated as the production topology below.

## Why MCP And ISCP Are Both Required

MCP defines the capability surface: discovery, input/output schemas, tool calls,
resources, and progress. ISCP defines the trusted device network: same-Domain
device provisioning, peer identity, proof of possession, Trust Grants, Relay
reachability, end-to-end encryption, replay protection, credential rotation,
and transport revocation. SparkClaw owns the additional application-admission
step that turns one authenticated ISCP device into an owner-approved MCP client.
Reconnect of an ISCP session is transport behavior; recovery of an MCP business
operation remains part of the MCP-over-ISCP application contract.

Neither layer replaces the other:

- publishing only a public SparkClaw MCP endpoint would require direct network
  exposure and would reduce long-lived authorization to a bearer secret;
- using only the current `agent.*.v1` ISCP API would retain a second capability
  vocabulary and would not expose the registered Workflow leaf catalog through
  a standard tool protocol;
- MCP over an authenticated ISCP session gives external products one standard
  tool interface without making the local Gateway public.

## ISCP Domain And Authorization Direction

The external Access Gateway is provisioned as a device in the SparkClaw ISCP
Domain. The target does not rely on cross-Domain Trust Root federation, which is
outside the currently implemented ISCP protocol. Both the external gateway and
the local SparkClaw gateway use identities, Trust Grants, Relay credentials,
Hello/Ready, and `SecureEnvelope` from that same Domain.

ISCP and SparkClaw own different authorization layers. ISCP owns device admission
and the coarse permission to establish the encrypted peer session. SparkClaw
owns MCP application admission: it issues and consumes its own one-time MCP
Access Ticket, binds the authenticated ISCP device to a local owner, and records
the allowed route leaves, operations, effects, and approval rights. The
direction is therefore:

```text
SparkClaw owner
  -> starts ISCP pairing locally in SparkClaw
      -> SparkClaw invokes the configured ISCP pairing capability
          -> SparkClaw locally displays the one-time ISCP Pairing Ticket
              -> owner transfers it once to LocalMind / another external gateway
                  -> external gateway redeems it through ISCP Provisioning
                      -> gateway joins the SparkClaw ISCP Domain
                          -> ISCP issues the ongoing protocol credentials
                              -> authenticated end-to-end ISCP session becomes ready
                                  -> owner selects MCP capability limits
                                      -> SparkClaw issues a one-time MCP Access Ticket
                                          -> enrolled device redeems it over ISCP
                                              -> SparkClaw activates the durable MCP Binding
```

LocalMind or another participating external controller must not issue an
enrollment bundle, bearer token, Trust Grant, or equivalent object that admits
itself into the SparkClaw Domain or authorizes SparkClaw capabilities. The ISCP
Trust Root signs Trust Grants and the Relay issues access/refresh credentials;
SparkClaw neither signs those protocol objects nor implements a parallel ISCP
claim or refresh endpoint. A coarse ISCP permission such as
`sparkclaw.peer.connect` permits the encrypted session but never grants MCP
discovery, invocation, or a Workflow leaf by itself. Those rights begin only
after the separate SparkClaw MCP Access Ticket has been consumed and its MCP
Binding is active.

The current LocalMind Bridge path does the opposite at bootstrap: SparkClaw
generates an enrollment request and expects a LocalMind controller to return
`sparkclaw.bridge.enrollment.v1` with Relay credentials and peer grants. That is
a legacy trust direction and must not be carried into the target implementation.
Existing LocalMind Bridge bundles, grants, refresh credentials, and provider
enrollment records cannot be imported or converted into a new peer binding.
Every existing LocalMind Access Gateway must enroll again in the SparkClaw
Domain using a fresh ISCP Pairing Ticket and must then redeem a fresh SparkClaw
MCP Access Ticket. This rule makes no decision about JingSi's future binding.

This does not change the outbound SparkClaw-to-LocalMind workspace MCP client:
when SparkClaw accesses LocalMind resources, LocalMind remains the resource
authority and may issue the credential consumed by SparkClaw. The two directions
must use separate bindings and credentials.

## Target Topology

```text
LocalMind / another MCP-compatible third party
  -> standard MCP client
  -> generic ISCP MCP Access Gateway (external role)
      -> ISCP Relay (routing only; no plaintext business payload)
          -> generic ISCP MCP Access Gateway (SparkClaw role)
              -> loopback or Unix-socket Route MCP Service
                  -> MCP receive adapter -> MessageEnvelope
                      -> authenticated tool-to-leaf binding
                          -> deterministic Top-1 Catalog leaf
                              -> exact Workflow Profile
                                  -> Workflow Runtime -> ToolHub -> Policy/Approval
                                      -> WorkflowResult -> DeliveryRequest
                                          -> MCP sender/provider
                                              -> encrypted MCP result/progress/operation status
```

The Access Gateway is one product-neutral component with external and SparkClaw
roles. It may be distributed as a service or SDK-sidecar, but the wire contract
and identity rules are identical for every provider. The end-to-end ISCP
security boundary is the enrolled external gateway device to the enrolled
SparkClaw device in one ISCP Domain. The Relay forwards ciphertext and does not
terminate the MCP business session. Neither Route MCP role opens a public inbound
port; both reach the Relay with outbound connections.

The Route MCP Service is local-only. It must not bind a public or LAN listener.
The existing Gateway, Catalog, Workflow Runtime, Policy, Store, artifacts, and
owner approval UI remain authoritative.

MCP control traffic remains in its protocol adapter, while MCP business traffic
uses the shared managed chain:

```text
Web / Weixin / Telegram / Timer
  -> Message Plane -> intent routing -> Workflow Core -> Delivery Plane

External MCP client
  -> MCP protocol adapter -> Message Plane -> bound-leaf routing
      -> Workflow Core -> Delivery Plane -> MCP sender/provider
```

`initialize`, `ping`, MCP Access Ticket redemption, capability listing, and
SparkClaw operation status/control do not become messages. A valid
`tools/call` does create a `MessageEnvelope`, source endpoint, `ReturnRoute`,
Workflow run, `WorkflowResult`, `DeliveryRequest`, and delivery receipt. MCP
specific request/operation correlation is retained as typed metadata beside those
shared records; it does not create a parallel business lifecycle.

## Component Responsibilities

### Route MCP Service

The Route MCP Service is the single inbound capability source of truth. It:

- derives MCP tool definitions from registered, executable Catalog leaves;
- exposes only leaves explicitly marked eligible for remote projection;
- binds each MCP tool to one leaf ID and one Workflow Profile revision;
- validates a typed ingress schema before creating a run;
- maps `tools/call` into the common receive contract and one provider-neutral
  `MessageEnvelope`;
- starts the exact Workflow through Message Runtime and never calls ToolHub
  directly;
- returns a bounded, provider-neutral result, activity, approval, or operation
  status through Delivery Gateway and the registered MCP sender/provider;
- never exposes raw ToolHub registrations, internal paths, secrets, model
  prompts, unprojected observations, or unregistered runtime functions.

An MCP tool call binds the leaf by tool identity. The local server derives a
typed binding from the current Catalog entry after authenticating the peer and
validating its grant; the caller cannot provide the leaf marker as free text or
an arbitrary argument. Routing restricts eligibility to that one leaf and
records it as deterministic Top-1 evidence. SparkClaw does not run embedding or
Fast/Tree scoring over other leaves and cannot silently change the requested
capability. Deterministic resource grounding, Workflow decisions, Policy, and
finalization inside the bound leaf continue normally.

### ISCP MCP Access Gateway

The Access Gateway:

- presents standard Streamable HTTP MCP to the external product;
- maps MCP JSON-RPC requests, responses, progress, and cancellation to a
  versioned MCP-over-ISCP envelope contract;
- establishes ISCP Hello/Ready only after peer identity and Trust Grant checks;
- admits MCP discovery and calls only after that authenticated device has
  redeemed a valid SparkClaw MCP Access Ticket and has an active MCP Binding;
- preserves MCP request IDs, ISCP sequence, deadline, idempotency key, binding
  ID, and Catalog revision across the translation;
- resumes read/status traffic after reconnect and never blindly replays a
  mutation with an unknown outcome;
- applies request, response, concurrency, and session bounds before forwarding;
- contains no provider-name branches or provider-specific capability map.

The external role may run beside LocalMind or another participating MCP client. The
SparkClaw role runs beside the local Gateway and reaches the Route MCP Service
only over loopback or a Unix socket.

### ISCP Infrastructure

The existing ISCP protocol and its conformant Domain services own:

- Pairing Ticket issuance, verification, expiry, use limits, and consumption;
- device identity submission, proof of possession, and same-Domain enrollment;
- Trust Root authorization, coarse Trust Grant issuance, expiry, and revocation;
- Relay access/refresh credential issuance and rotation;
- Hello/Ready session establishment, `SecureEnvelope`, replay protection,
  ciphertext routing, and transport revocation.

SparkClaw consumes those guarantees through the ISCP SDK and service APIs. It
must not add a SparkClaw ISCP invitation format, ISCP claim endpoint, Trust Grant
signer, Relay credential issuer, or cross-Domain compatibility path. This does
not prohibit the separate SparkClaw MCP Access Ticket: that ticket is consumed
by SparkClaw only over an already authenticated ISCP session and never admits a
device to the Domain. If a required ISCP capability is absent or incomplete in
the selected ISCP deployment, that deployment or the upstream ISCP project must
be fixed; SparkClaw fails closed.

ISCP does not authorize a Workflow leaf and the Relay does not see decrypted MCP
arguments or results. Leaf authorization remains bound to the local SparkClaw
external-peer binding.

### Local Authorization Controller

SparkClaw owns MCP application admission and authorization, not ISCP protocol
provisioning. Its owner-facing control surface invokes the configured ISCP
pairing capability and locally presents the ISCP authority's Pairing Ticket.
After authoritative ISCP enrollment and Hello/Ready, it independently creates a
short-lived, single-use MCP Access Ticket for owner-selected capability limits.
SparkClaw stores only a hash of that secret, verifies and atomically consumes it
over the authenticated ISCP session, binds it to that session's device identity
and public-key thumbprint, and activates the durable MCP Binding. It also lists,
narrows, suspends, and revokes those bindings. SparkClaw does not define, sign,
or verify the ISCP ticket and does not provide a public network claim service. A
third-party service cannot grant itself another leaf, change an effect boundary,
or approve its own pending action.

## Route Leaf Projection

The Catalog remains the only registry of user-facing capabilities. MCP exposure
is a projection, not a parallel hand-maintained tool list.

A leaf is listed by `tools/list` only when all of these conditions hold:

1. the leaf and exact Workflow Profile revision are registered and executable;
2. the leaf has a versioned remote ingress and result schema;
3. its metadata explicitly allows remote MCP exposure;
4. the local owner granted that leaf and its permitted operations to the peer;
5. required runtime providers are currently ready;
6. its resource contract can be represented without arbitrary host paths,
   credentials, or ungoverned binary transfer.

The visible catalog is therefore the intersection of current runtime ability,
remote-exposure policy, and the peer grant. A Catalog or grant revision change
invalidates the prior listing; the caller must list again before invoking a
changed tool.

Catalog change alone does not automatically widen or invalidate every grant.
Upgrade behavior is evaluated per granted leaf:

- a new unrelated leaf is not auto-granted and does not disturb existing grants;
- display-only metadata changes may follow the current Catalog after relisting;
- temporary provider unavailability returns `unavailable` without making the
  grant stale;
- a removed leaf or a leaf no longer eligible for remote exposure is hidden and
  rejected immediately;
- an input/output schema, operation, effect, approval rule, governed-resource
  contract, or safety-relevant Workflow Profile change marks only that leaf's
  grant stale;
- a stale leaf remains hidden and rejected until the local owner reviews the new
  contract and creates a new authorization revision.

No safety-relevant capability expansion follows a new Catalog or Profile
revision automatically. Unrelated leaves in the same MCP Binding remain usable.

Tool names use a stable provider-neutral namespace derived from the leaf ID, for
example `sparkclaw.route.browser.internet_search`. Provider names never appear
in tool names or dispatch logic. Tool definitions include the exact leaf/profile
revision, read/write effects, approval behavior, and bounded schemas.

Not every current leaf is automatically suitable for remote use. A leaf that
accepts a governed local resource must use an opaque owner-authorized resource
reference; it must not accept a host path supplied by the external caller.
Leaves without such a contract remain absent until the contract is implemented
end to end.

## Two One-Time Tickets, Then Durable Bindings

The target deliberately uses two different one-time credentials. They have
different issuers, consumers, scopes, and results and must never be represented
as one generic token:

| Credential | Issuer and consumer | Purpose | Durable result |
|---|---|---|---|
| ISCP `iscp.pairing_ticket.v2` | Issued and consumed by conformant ISCP Domain services | Admit a device to the ISCP Domain and bootstrap its authenticated end-to-end channel | ISCP device membership, rotating protocol credentials, and Trust Grants |
| SparkClaw MCP Access Ticket | Issued and consumed by local SparkClaw | Let one already enrolled and authenticated ISCP device activate owner-approved MCP use | Durable SparkClaw MCP Binding |

Both are short-lived and single-use. Neither is a permanent bearer credential,
and neither is sent on ordinary MCP discovery or invocation. "Persistent"
describes the resulting device membership and MCP Binding, not either secret.

### ISCP Pairing Ticket And Provisioning

The local owner starts onboarding from SparkClaw. SparkClaw invokes the pairing
capability of its configured ISCP Domain and displays the resulting signed
Pairing Ticket locally, for example as a copy-once value or QR representation.
The owner transfers that credential once to the external device. This local
presentation is the SparkClaw product experience; protocol issuance remains an
ISCP operation.

Standard ISCP Pairing and Provisioning own the ticket format, signature,
Domain/Relay/Trust Root binding, expiry, use limit, proof-of-possession check,
consumption, and delivery of device-bound provisioning material. SparkClaw
stores only a non-secret ISCP onboarding transaction reference; it does not
store a reusable ISCP ticket, verify the ISCP ticket itself, or implement the
ISCP claim endpoint.

The external Access Gateway generates its long-term device key locally and never
exports the private key. It connects to ISCP and presents the Pairing Ticket with
an ISCP Device Proof through Provisioning to join the SparkClaw Domain. It does
not connect directly to a public SparkClaw enrollment endpoint. A non-expiring
reusable bearer token is explicitly rejected: normal connectivity uses the
persistent device binding, rotating ISCP credentials, and authenticated ISCP
sessions.

### SparkClaw MCP Access Ticket

Once the enrolled external gateway and SparkClaw have an authenticated ISCP
session, the local owner selects the MCP leaf/effect limits. SparkClaw creates a
cryptographically random opaque MCP Access Ticket, presents it locally as a
copy-once value or QR representation, and persists only its hash plus:

- ticket ID, owner ID, pending authorization revision, and expected ISCP Domain;
- granted leaf IDs, operations, effects, and approval permissions;
- issue time, short expiry, `max_uses = 1`, and pending/consumed/expired/revoked
  state.

The external gateway submits the secret only through its authenticated and
encrypted ISCP session. SparkClaw derives the requester device ID, public-key
thumbprint, Domain ID, and session identity from ISCP rather than accepting them
as caller-controlled fields. It verifies the stored hash, expiry, state, Domain,
and pending owner authorization and atomically consumes the ticket. Concurrent
or replayed redemption fails. SparkClaw exposes no public MCP ticket-claim port.

The MCP Access Ticket authorizes only creation of the bounded MCP Binding. It
cannot be exchanged for ISCP credentials, cannot admit a device to the Domain,
cannot be reused as MCP bearer authentication, and is never retained in logs,
traces, artifacts, or the resulting Binding.

### Durable SparkClaw MCP Binding

After successful MCP Access Ticket redemption, SparkClaw activates a durable
MCP Binding containing:

- owner ID and the SparkClaw ISCP Domain ID;
- external device ID and public-key thumbprint;
- granted leaf IDs, operations, effects, and approval permissions;
- binding and Catalog revisions;
- status, creation/use timestamps, and revocation state.

This Binding is not an ISCP credential and does not replace the Trust Grant. An
MCP call is accepted only when the current ISCP device/session authorization and
the current SparkClaw MCP Binding both pass. Relay credentials and Trust Grants are
issued, rotated, renewed, and revoked by their ISCP authorities. SparkClaw may
request those lifecycle actions through ISCP APIs but does not implement them.

The ISCP device membership and SparkClaw MCP Binding survive restarts and
ordinary ISCP credential rotation until explicit revocation or a safety-relevant
leaf revision becomes stale. A new external device or a lost device key requires
a new ISCP Pairing Ticket and a new SparkClaw MCP Access Ticket. Narrowing or
re-authorizing the same authenticated device creates a new MCP authorization
revision; it never re-enables the consumed secret.

Local owner revocation atomically marks every nonterminal operation for that
Binding as `revoked`. SparkClaw cancels any live local execution context and
rejects its pending approvals; an already terminal operation remains immutable,
and a late Workflow or delivery result cannot overwrite the revoked state.

## Pairing Flow

```text
1. Owner selects "Add external MCP client" and starts ISCP device pairing.
2. SparkClaw records a non-secret ISCP onboarding reference and invokes the
   configured ISCP pairing capability.
3. SparkClaw locally displays the resulting short-lived, single-use ISCP Pairing
   Ticket to the owner.
4. The owner transfers that ticket once to the external generic Access Gateway;
   the gateway creates its device key locally.
5. The gateway connects to ISCP and uses standard Provisioning plus Device Proof
   to redeem the ticket and join the SparkClaw Domain.
6. ISCP verifies and consumes the ticket, admits the device, and has the Trust
   Root and Relay issue their respective protocol credentials.
7. Both devices connect outbound through the ISCP Relay and complete
   Hello/Ready; subsequent onboarding and MCP traffic is encrypted end to end.
8. Owner selects the MCP leaf/effect limits. SparkClaw creates a short-lived,
   single-use MCP Access Ticket and displays it locally.
9. Owner transfers that separate ticket once to the enrolled external gateway.
10. The gateway redeems it through its authenticated ISCP session. SparkClaw
    atomically consumes it and binds the session's proven device identity and
    public-key thumbprint to the pending owner authorization.
11. SparkClaw activates the durable MCP Binding. Ordinary `tools/list` and
    `tools/call` use the ISCP session identity plus that Binding, never either
    one-time ticket.
12. ISCP rotates its protocol credentials while the MCP Binding remains active.
```

ISCP enrollment failure must not permit MCP Access Ticket redemption. If ISCP
enrollment succeeds but MCP redemption or Binding activation fails, the device
has no SparkClaw leaf authorization and every MCP discovery or invocation fails
closed. ISCP rejects replay or races for its Pairing Ticket; SparkClaw separately
rejects replay, expiry, revocation, identity substitution, or races for its MCP
Access Ticket.

## MCP Channel Contract

The MCP adapter separates protocol control from business traffic before using
the common receive and send layers:

| MCP operation | SparkClaw behavior |
|---|---|
| `initialize`, `ping`, session negotiation | Transport control only; no invocation or Workflow run |
| `tools/list` | Return the current peer-filtered remote Catalog projection |
| `tools/call` for a projected Route tool | Create one message ingress and execute its bound Workflow leaf |
| progress and MCP request cancellation | Observe or cancel an in-flight request while its current MCP exchange remains active |
| `sparkclaw.operation.get` | Return durable state for one operation owned by the current MCP Binding |
| `sparkclaw.operation.result` | Return the bounded terminal result for that operation when available |
| `sparkclaw.operation.cancel` | Request cancellation without representing deletion or guaranteed rollback |
| `resources/list` | Return bounded metadata for explicitly projected resources |
| protected `resources/read` | Execute only through an authorized remote read leaf; never read local storage directly |

The three reserved `sparkclaw.operation.*` tools are application-level control
tools for the initial MCP version. They are not Route Catalog leaves, do not run
semantic or bound-leaf routing, and do not create a new business message or
Workflow. The adapter authorizes them against the current MCP Binding and an
existing operation record. Their schemas are one versioned adapter-owned
contract, not a second user-capability registry.

The MCP adapter owns protocol session negotiation, request IDs, progress,
current-request cancellation, and operation-control translation. The shared
chain owns local execution authorization, requester provenance, message/run
persistence, idempotency, Workflow state, Policy, approval, result state,
endpoint resolution, send attempts, and audit. MCP is a registered third-party
channel type, not a product-name branch.

For consistent administration, MCP registers one generic channel definition in
the existing third-party control plane. `ConnectorSetting`, or its successor
unified setting, controls enable/suspend state, Endpoint Registry visibility,
inbound access, and outbound Provider availability; a durable peer binding means
paired and authorized, not enabled. MCP does not need to reuse connector
internals that do not apply, such as polling or account login. It reuses the
management contract and business lifecycle.

Every `tools/call` creates a source endpoint plus a normalized message ingress:

| Shared field | MCP mapping |
|---|---|
| source kind | `third_party_device` |
| adapter/provider key | stable value `mcp`, never a participating product name such as `localmind` |
| source endpoint | owner-scoped endpoint derived from the authenticated peer binding |
| native message/thread | MCP request ID and authenticated MCP session/operation correlation |
| owner | local owner resolved from the durable MCP Binding |
| actor/authorization principal | local SparkClaw execution principal; never the external ISCP device |
| requester | authenticated external ISCP device retained separately in `MCPInvocationContext` |
| content | bounded typed projection of validated MCP arguments and governed resource references |
| return route | frozen to the same source MCP endpoint |
| idempotency | peer binding plus caller idempotency key |

A versioned `MCPInvocationContext` is persisted beside the common message/run
records and contains at least:

```text
MCP request/session ID + SparkClaw operation ID when deferred
requester device ID/public-key thumbprint + durable MCP Binding/revision
local owner ID + local SparkClaw execution principal
tool name + leaf ID + Workflow/Profile revision
Catalog revision + allowed operation/effect
validated argument digest + bounded arguments/resource refs
idempotency key + deadline
message/run/delivery IDs
```

Structured MCP arguments remain structured. They are validated against the
published schema, preserved in typed MCP context, and bound to Workflow inputs
without being flattened into a synthetic natural-language owner message.
Arbitrary remote host paths and unresolved local resource names are rejected.

The external gateway is the authenticated requester and source, not the actor
that executes SparkClaw capabilities. It submits a bounded request; SparkClaw's
local execution principal creates and executes the Workflow under the owner and
MCP Binding limits. Requester device identity remains mandatory provenance for
authorization, idempotency, revocation, source reply, and audit, but it is never
promoted to the local owner or SparkClaw execution principal.

MCP source-reply authorization therefore uses a dedicated Binding-aware rule.
The frozen `ReturnRoute` must resolve to the original MCP source endpoint; that
endpoint's `OwnerID` and `BindingRef` must match the result owner and current MCP
Binding; and its requester device identity must match both the invocation
context and the device authenticated by the current ISCP session. The local
execution principal must remain authorized for that owner. MCP does not reuse a
same-principal rule requiring requester device, owner, and actor to be equal, and
implementation must not globally relax the existing source-reply rules for
other third-party channels.

## Deterministic Top-1 Leaf Selection

An MCP `tools/call` already names a function published by SparkClaw. Open-ended
intent recognition would add ambiguity and could route away from the advertised
contract. The common router therefore accepts a server-owned binding signal from
the authenticated MCP receive adapter and selects the leaf as follows:

1. Resolve the tool name in the current grant-filtered remote Catalog.
2. Revalidate peer binding, Catalog/Profile revision, schema, operation, effect,
   and runtime readiness.
3. Intersect the routing graph with the bound leaf; exactly one candidate must
   remain.
4. Record `mcp_tool_binding=1.0` as a typed deterministic selection signal, not
   as a fabricated embedding or Tree semantic score.
5. Persist the candidate as Top-1 and instantiate its exact Workflow Profile.

No embedding or Fast/Tree route-scoring call is made. The route trace still
records the binding source, Catalog and binding revisions, candidate, reason
code, and the fact that semantic channels were intentionally bypassed. A stale,
revoked, unauthorized, unavailable, or non-unique binding fails explicitly. It
never falls back to natural-language routing or another leaf.

## Invocation Flow

```text
1. External MCP client calls tools/list through its generic Access Gateway.
2. SparkClaw returns only the current grant-filtered leaf projection.
3. Client calls one exact leaf tool with an idempotency key and deadline.
4. The gateways carry the MCP request inside an ISCP SecureEnvelope.
5. SparkClaw authenticates the peer and revalidates binding, grant, Catalog
   revision, schema, effect, and runtime readiness.
6. The MCP receive adapter creates one `third_party_device` `MessageEnvelope`,
   source MCP endpoint, source `ReturnRoute`, and server-owned tool-to-leaf
   binding.
7. Bound-leaf routing selects that candidate as deterministic Top-1 without
   embedding or Tree scoring.
8. Message Runtime starts the exact Workflow Profile. Local Policy executes
   reads or parks effects for local owner approval.
9. The waiting or terminal `WorkflowResult` becomes a `DeliveryRequest` to the
   frozen source MCP endpoint.
10. Delivery Gateway invokes the MCP sender/provider, which projects the result
    into an immediate MCP result, progress notification, or SparkClaw operation
    handle/status and returns it through the encrypted ISCP session.
```

External content remains untrusted input. A result from one call cannot expand
the grant, select another leaf, authorize a mutation, or resolve its own
approval.

## MCP Result Sender

The common send layer owns result lifecycle and invokes a registered,
provider-neutral MCP sender/provider for the source endpoint. That adapter maps
the governed `WorkflowResult` and delivery state to one of:

- immediate MCP `tools/call` result for a bounded completed operation;
- progress notification for a running operation;
- an application-level operation handle for a deferred running or
  `approval_pending` operation;
- completed, blocked, failed, canceled, revoked, or unknown-outcome operation
  state through `sparkclaw.operation.get`;
- bounded content, structured data, governed resource handles, and references.

The MCP sender/provider is part of Delivery Gateway; Workflow Runtime never
writes directly to an MCP stream. It correlates the delivery with the original
MCP request and SparkClaw operation IDs and records a typed delivery receipt.
Disconnect does not redirect an MCP result to Web, Weixin, or another connector.
The result remains
bound to the source MCP endpoint and can be resumed through the binding-scoped
SparkClaw operation contract. Only a known non-delivery may be retried.

The invoking peer cannot approve its own pending effect unless it separately
holds an explicit owner-device approval grant. Approval continues in the local
durable owner inbox. When approval resolves, the same external invocation
continues and the common send layer persists its new operation state for the
same endpoint and Binding.

## MCP-Over-ISCP Contract

Implementation must define one versioned schema for MCP traffic inside ISCP,
covering at least:

- initialization and negotiated MCP/protocol versions;
- `tools/list` and `tools/call`;
- cancellation and progress notifications;
- the reserved `sparkclaw.operation.get`, `sparkclaw.operation.result`, and
  `sparkclaw.operation.cancel` control tools;
- bounded resource list/read only for explicitly projected resources;
- request ID, session ID, peer binding ID, idempotency key, deadline, Catalog
  revision, and operation ID;
- terminal result, approval-pending, retryable failure, revoked, stale-catalog,
  and unknown-outcome states;
- binding-scoped operation-status/result recovery after reconnect.

The first implementation retains MCP `2025-06-18`, matching SparkClaw's current
client implementation. That protocol version has no standard MCP Tasks API, so
the first implementation must not advertise or emit `tasks/get`, `tasks/result`,
`tasks/cancel`, task-augmented `tools/call`, or a proprietary frame presented as
standard MCP Tasks. Durable approval, reconnect, and restart recovery use the
SparkClaw operation tools above. A future implementation may negotiate a newer
common MCP version with Tasks support and map the same internal operation record
to standard Tasks; protocol negotiation must fail explicitly rather than
silently downgrade.

Read-only calls may retry only when the operation is known not to have executed.
Mutations are never automatically replayed after timeout or authentication
refresh. An uncertain mutation is resolved by operation ID before any new call.

## Policy, Approval, And Data Boundaries

- The authenticated ISCP peer is the external requester/source. SparkClaw is the
  executor, and the MCP Binding determines the local owner, local execution
  authorization, and allowed leaves.
- Requester device identity stays in the source endpoint and
  `MCPInvocationContext`; it must not be copied into local `ActorID` or used to
  impersonate the owner.
- Listing permission and invocation permission are separate from approval
  resolution permission.
- Reversible and dangerous effects continue through SparkClaw Policy and the
  local durable approval inbox.
- An invoking third party cannot approve its own action by default. A future
  owner-device approval capability requires a separate explicit grant and must
  not be inferred from MCP access.
- Results expose only consumer-sized projections. Local paths, secrets, raw
  credentials, hidden prompts, private traces, and unbounded artifacts remain
  local.
- Every list, invocation, denial, approval, result, reconnect, grant change, and
  revocation records the requester device, local execution principal, Binding
  revision, leaf/profile revision, operation ID, and outcome without recording
  secret material.
- Revocation immediately removes the peer's effective catalog, rejects new
  calls, closes active sessions, and prevents credential renewal.

## Current And Target Boundaries

| Area | Current implementation | Target design |
|---|---|---|
| Third-party inbound API | Legacy `agent.*.v1` plus implemented local Route MCP dispatch | Provider-neutral Route MCP Service through a deployed external gateway |
| Runtime plane | MCP receive adapter already joins the shared Message/Workflow chain | Live external Relay path validated with the same chain |
| Capability source | Implemented filtered projection of registered Catalog leaves | Owner-managed remote Catalog expansion without provider branches |
| Participating integration | LocalMind remains on its legacy path | One generic Access Gateway and no provider branches |
| Channel management | Generic `mcp` setting and External MCP owner UI gate pairing, ticket issue, ingress, endpoint visibility, sender availability, and revocation | Live operations against the deployed authority and external gateway |
| Credential authority | LocalMind controller returns the bundle used by its legacy Bridge path | ISCP authorities issue their Pairing Ticket, Trust Grants, and Relay credentials; SparkClaw separately issues and consumes the MCP Access Ticket |
| Enrollment | Externally supplied enrollment bundle | External device first redeems the ISCP Pairing Ticket into the Domain, then redeems the SparkClaw MCP Access Ticket over that authenticated session |
| Long-term authorization | Expiring bundle and Trust Grants | ISCP credential lifecycle plus a separate durable, owner-approved SparkClaw MCP Binding; neither one-time ticket is reused |
| Network exposure | Separate Bridge connects to Relay | Same outbound-only ISCP reachability; local MCP remains private |
| Result return | Generic MCP sender/provider and durable operation tools are implemented locally | Live external reconnect and Relay result recovery validation |
| SparkClaw -> LocalMind | Workspace-scoped MCP client | Unchanged |
| JingSi | Current path pending separate replacement design | Out of scope; no MCP pairing, tools, sender, migration, or acceptance dependency |

The current LocalMind inbound use of ISCP Bridge and `agent.*.v1` is the legacy
chain replaced by this design. It remains executable only until LocalMind is
validated and cut over to the generic MCP path. No new LocalMind caller or
capability may be added to that chain. Shared Bridge code still required by
JingSi is not migrated by this design and must be kept only at the minimum
frozen surface needed for current JingSi operation. Documentation must
distinguish the implemented local-runtime phase from the still unavailable
production external onboarding path during the transition.

## Mandatory LocalMind Legacy Removal

For LocalMind, the target is a replacement, not a permanent dual stack. Once
LocalMind passes the new end-to-end path, implementation is not complete until
its old inbound capability path is deleted. If a listed component is shared with
JingSi, remove the LocalMind registration, authorization, manifest entries, and
dispatch branches while retaining only the minimum frozen JingSi-owned surface.
The LocalMind removal scope includes:

- LocalMind peer identities, provider enrollment, externally issued Trust Grants,
  Relay refresh material, and LocalMind-specific Bridge configuration;
- LocalMind capability-manifest entries and `agent.*.v1` notification,
  conversation, approval, event, or status paths superseded by the MCP channel;
- Gateway dispatch and `internal/iscpbridge` branches used only by LocalMind,
  plus their schemas, mocks, tests, deployment instructions, and health checks;
- LocalMind fallback and provider-name branches not used by the generic MCP
  channel;
- LocalMind legacy guidance after current-state documentation has been merged
  into the generic MCP/ISCP guide.

Do not delete the whole `cmd/iscp-bridge`, shared Gateway handler, schema, or
configuration merely because LocalMind has migrated if JingSi still requires
it. Conversely, JingSi's temporary dependency is not permission to retain a
hidden LocalMind fallback. Complete removal of the remaining Bridge and
`agent.*.v1` surface belongs to JingSi's future binding design, not this project.

LocalMind cutover must disable new legacy LocalMind ingress first, let already
acknowledged LocalMind operations finish or move to an explicit terminal state,
revoke its old externally issued bundles and Relay refresh material, and then
remove the LocalMind-only code and registrations.
Historical messages, notifications, runs, receipts, and audit records may remain
readable under their existing IDs; they do not keep the old transport or API
alive for LocalMind. There is no automatic LocalMind credential migration,
protocol fallback, or cross-protocol resume.

## Implementation Sequence

1. Define Catalog remote-exposure metadata and versioned ingress/result schemas
   without adding a second capability registry.
2. Define MCP receive mapping, typed invocation context, tool-to-leaf binding,
   requester provenance distinct from the local execution principal,
   deterministic Top-1 evidence, source endpoint, and return route on the shared
   message/run contracts.
3. Implement and test the local-only Route MCP Service against exact Workflow
   leaves, Policy, approvals, all Store backends, and shared result projection.
4. Register the generic MCP channel definition and sender/provider, enforce its
   enable/suspend gates independently of peer binding, and cover
   `WorkflowResult` -> `DeliveryRequest` -> MCP result/progress/SparkClaw
   operation plus receipt semantics.
5. Integrate standard ISCP PairingTicket/Provisioning confirmation. Do not
   implement ISCP ticket signing, verification, consumption, or protocol
   credential issuance in SparkClaw.
6. Implement the separate opaque SparkClaw MCP Access Ticket, hash-only durable
   storage, authenticated-ISCP-only atomic redemption, durable MCP Binding,
   revocation, and owner controls across all Store backends.
7. Define and test the MCP-over-ISCP schema, generic Access Gateway, Relay
   reconnect, idempotency, and unknown-outcome behavior.
8. Run an end-to-end external gateway -> Relay -> MCP receive -> Message Plane
   -> Workflow Core -> Delivery Gateway -> MCP sender test with read,
   approval-pending mutation, reconnect, and revocation cases.
9. Present a fresh ISCP Pairing Ticket locally in SparkClaw, transfer it once to
   LocalMind's Access Gateway, enroll that gateway as a new device in
   SparkClaw's ISCP Domain, then separately present and redeem a fresh SparkClaw
   MCP Access Ticket over that authenticated ISCP session. Activate the
   owner-approved MCP Binding and move it to the generic gateway without
   provider-specific server code. Do not import its old enrollment bundle or
   grants.
10. Disable legacy LocalMind ingress, drain or explicitly terminalize its
   acknowledged work, and validate discovery, read, approval-pending mutation,
   reconnect, and revocation exclusively through the new path.
11. Delete the LocalMind legacy surface listed above and prove no LocalMind
    fallback remains. Leave JingSi unchanged and defer removal of any remaining
    JingSi-required Bridge surface to its separate binding project.

## Acceptance Criteria

- A newly integrated third party connects without a SparkClaw code change or a
  provider-name configuration branch.
- Only the configured ISCP authorities issue protocol credentials and Trust
  Grants. An external controller cannot self-admit to SparkClaw's ISCP Domain,
  and only the local SparkClaw owner can activate leaf/effect authorization.
- SparkClaw locally presents the ISCP authority's short-lived Pairing Ticket and
  the owner transfers it once. After the authenticated ISCP session is ready,
  SparkClaw presents its separate short-lived MCP Access Ticket and the owner
  transfers that once. The resulting device membership and MCP Binding remain
  active until revoked or stale; ordinary use reuses neither secret.
- Relay operators cannot decrypt MCP arguments or results.
- `tools/list` exactly matches the current Catalog, remote-exposure policy, peer
  grant, and runtime readiness intersection.
- Every `tools/call` creates exactly one message ingress, selects exactly one
  server-bound Top-1 leaf without semantic scoring, and returns through exactly
  one Delivery Gateway attempt to the source MCP endpoint.
- MCP control operations create no business message; MCP business calls and
  results reuse common message/run/delivery ownership and audit records.
- The first implementation negotiates MCP `2025-06-18`, advertises no standard
  MCP Tasks, and recovers deferred approval, reconnect, and restart state only
  through binding-scoped `sparkclaw.operation.*` tools.
- The external ISCP device is retained as requester/source provenance while the
  local SparkClaw principal remains the Workflow executor; neither identity may
  impersonate the local owner.
- One generic `mcp` channel setting governs ingress, endpoint visibility, and
  sender availability; pairing or retaining a peer binding never enables it.
- An external caller cannot invoke an unlisted leaf, widen scope through input
  or output, use an arbitrary local path, or approve its own effect.
- A revoked peer loses discovery, invocation, event resume, and renewal access;
  every running or approval-required operation for that Binding becomes the
  immutable `revoked` terminal state.
- Mutation timeout/reconnect never causes an automatic duplicate execution.
- File and PostgreSQL state backends preserve identical non-secret onboarding
  reference, hash-only pending MCP ticket, Binding, idempotency, audit, atomic
  consumption, and revocation semantics; neither stores or treats either Ticket
  as a reusable credential.
- LocalMind passes the target path after SparkClaw locally presents a fresh ISCP
  Pairing Ticket and LocalMind redeems it through ISCP Provisioning into
  SparkClaw's Domain, then redeems a fresh SparkClaw MCP Access Ticket through
  that authenticated session, while its old externally issued enrollment
  material is rejected.
- A safety-relevant leaf/Profile contract change makes only that leaf grant
  stale and unavailable until owner reauthorization; it neither auto-expands
  authorization nor disables unrelated granted leaves.
- Production source, configuration, schemas, images, and active documentation
  contain no LocalMind inbound fallback after cutover, even when shared Bridge
  components remain temporarily for JingSi.
- JingSi has no MCP channel setting, Pairing Ticket for this path, peer binding,
  projected tool, endpoint, sender, migration step, or acceptance dependency in
  this design.

## Open Implementation Decisions

The design fixes the trust and ownership model but leaves these deployment
choices for implementation review:

- whether the external gateway ships as a standalone sidecar, SDK component, or
  both;
- the exact production authority and Provisioning/Relay bootstrap endpoints
  through which the configured adapter obtains a Pairing Ticket and an
  unenrolled external gateway redeems it;
- Trust Grant lifetime and automatic renewal cadence for an active durable
  binding;
- the first leaf subset whose resource contracts are safe for remote exposure.
