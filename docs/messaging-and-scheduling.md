# Messaging And Scheduling

> Language: English | [简体中文](../zh-cn/docs/messaging-and-scheduling.md)

This document is the current contract for message ingress, result delivery,
third-party direct sends, and scheduled messages. It replaces the Message
Control migration, connector and Gateway assembly plans, Web outbound design,
worktree plan, and scheduled-task design record.

## One Message Architecture

Web, third-party connectors, and Timer are input sources for the same Message
Runtime. They do not own separate Agent loops.

The target [unified third-party ISCP MCP access](unified-third-party-access-design.md)
registers MCP as a managed third-party channel while retaining a dedicated MCP
protocol adapter. `initialize`, `ping`, and `tools/list` stay in that control
plane, as do MCP Access Ticket redemption and the first version's
`sparkclaw.operation.*` control tools. A Route `tools/call` enters the common
receive layer as a `third_party_device` `MessageEnvelope`; its server-owned tool
binding selects one deterministic Top-1 leaf. The result returns through
Delivery Gateway and a generic MCP sender/provider. Enable/suspend state,
endpoint visibility, and
provider availability reuse the unified third-party management contract, while
polling and other inapplicable connector internals remain adapter-specific. This
reuses lifecycle management without pretending that MCP control traffic is a
chat message.

```text
Web or provider input, or Timer claim
  -> MessageEnvelope + authorization + ReturnRoute
  -> normalization and intent routing
  -> one WorkflowResult
  -> DeliveryRequest
  -> Delivery Gateway
  -> resolved Endpoint and registered Provider
```

`MessageContent` preserves ordered text, image, audio, and file parts.
`MessageEnvelope` preserves source endpoint, native message/thread identity,
owner/actor authorization, and return routing. For target MCP ingress, the
external device remains requester/source provenance in typed MCP context while
the local SparkClaw principal remains the Workflow actor. These contracts live in
`internal/app` and are provider-neutral.

An ordinary request to publish the current message uses
`conversation.answer` revision 2's `publish` variant. It does not create a
second file-send path: the Workflow freezes the normalized request
`MessageContent` as its channel-neutral result without calling a model or a
tool. Text, image, audio, and file remain peer message parts throughout the
same Message Plane, routing, Workflow, Policy, and delivery chain. When the
request contains image, audio, or file parts, `publish` removes the owner command
text and returns only the governed media parts, preserving their order.
For a Web message containing only media parts, Message Runtime selects the same
registered `publish` variant directly from typed content; no synthetic owner
text or separate file-send request is created.

Before an attached workspace part can become that result, the Workflow
validates it against the source session workspace, rejects path escape and
symlinks, verifies its size and SHA-256, and registers or reuses a governed
`ArtifactObject`. This happens before external dispatch or any applicable
approval, so Delivery Gateway always resolves the exact source-session artifact
rather than interpreting the destination endpoint's workspace.

## Ownership

| Component | Responsibility |
|---|---|
| Message Plane | Normalize Web, connector, and Timer input into one envelope |
| Endpoint Registry | Resolve owner-scoped Web or third-party destinations and their status |
| Schedule Registry | Persist schema-v2 schedules and perform compare-and-swap changes |
| Timer Runtime | Claim due schedules and republish their payload through Message Runtime |
| Delivery Gateway | Resolve endpoint, preflight the full payload, and invoke one Provider |
| Provider Registry | Hold live delivery capabilities for Web, Telegram, Weixin, and future connectors |
| Connector Runtime | Own provider polling, inbound media, acknowledgements, and ordering |

`LocalWebDelivery` is the Web port adapter registered in the same Provider
Registry. It projects a channel-neutral result onto the persisted assistant
message. It is not a Web-specific message-sending interface and does not bypass
the Delivery Gateway. Third-party adapters implement the same `delivery.Provider`
boundary for their protocols.

## Delivery Rules

Every normal Agent result enters the Delivery Gateway. Before sending its first
part, the Gateway:

1. Resolves the target Endpoint for the persisted owner and actor.
2. Verifies endpoint state and authorization scope.
3. Resolves governed artifact references.
4. Validates the complete multipart payload against provider capabilities.
5. Dispatches once with an idempotency key and records a typed receipt.

Web delivery stores text as assistant content and governed image/audio/file
parts as message attachments. Connector control traffic such as typing states
or approval buttons remains local to the provider and is not another result
path.

WebChat's delivery target picker submits an optional opaque
`target_endpoint_id` with the ordinary session message. Message Plane keeps the
same text, attachments, source, authorization, normalization, routing, and
Workflow path, and freezes only the result's `ReturnRoute`. A pure image, audio,
or file publication to the selected third-party endpoint requires no send
approval, creates one `DeliveryRequest` for only that exact endpoint, and does
not persist or stream a corresponding assistant result in the source WebChat.
Text-only and other third-party Workflow results retain the existing
send-approval boundary. The picker never converts attachments into a direct-send
draft or calls `/api/deliveries`.

Gateway separately supports explicit direct sends for clients that already own
final message content. That API validates all parts, requires confirmation,
creates a durable delivery record, and then calls the same Delivery Gateway. A
retry is allowed only when the previous receipt proves which parts failed and
the outcome is known.

Primary APIs:

```text
POST /api/sessions/{id}/messages
POST /api/sessions/{id}/messages/stream
GET  /api/delivery-endpoints
GET  /api/deliveries
POST /api/deliveries
GET  /api/deliveries/{id}
POST /api/deliveries/{id}/retry
```

## Scheduled Message Contract

`schedule.manage` revision 2 is the only lifecycle Workflow. A schema-v2
`ScheduleSpec` stores:

- due-time `MessageContent` without `literal` or `ExpectedCapabilityPath`;
- owner, actor, and authorization context;
- session correlation and recurrence;
- a frozen `ReturnRoute` to Web, one resolved third-party endpoint, or nowhere.

At expiry, Timer only claims work and republishes the stored content as a new
`MessageEnvelope` with `source=timer`. Current routing then chooses the payload's
business Workflow. Timer never selects a capability and never calls Delivery
Gateway directly.

This allows a schedule to run an ordinary conversation reminder, weather query,
public search, or another currently registered Workflow without encoding that
Workflow into the schedule record.

## Create, List, Edit, And Delete

Natural-language create/read/edit/delete requests route to variants of the
same `schedule.manage` leaf. Edit and delete use a two-stage Workflow:

```text
reminders.list (fresh, pending, owner-scoped)
  -> resolve exactly one target
  -> freeze schedule ID + updated_at version
  -> reminders.update or reminders.cancel
  -> Schedule Registry compare-and-swap
```

The model does not mutate a guessed ID. No match or multiple matches returns a
clarification. A stale `updated_at` blocks the mutation so a concurrent change
is not overwritten.

The WebChat task toolbar uses typed `schedule_action` input for edit/delete.
It first loads `GET /api/schedules`, shows the current task and reminder endpoint
(WebChat or the concrete third-party software/account/recipient), and submits
the selected ID plus its observed version. The Workflow still performs a fresh
owner-scoped list and compare-and-swap. Editing preserves the existing reminder
endpoint; changing delivery target is a separate explicit operation.

The schedule list includes only current owner/actor pending or sending records,
with `editable`, `cancelable`, and endpoint status. An unavailable endpoint is
shown explicitly; it is never silently changed to Web.

## Connector Registration

Each optional connector registers only what it implements:

- binding lifecycle and credential ownership;
- one outbound `delivery.Provider` and capability declaration;
- an optional inbound `connectorruntime.Runtime`;
- shutdown and binding cancellation.

Gateway assembly iterates this registry. Message Control and Agent Runtime do
not branch on Telegram or Weixin names. A new provider must use current Endpoint,
MessageContent, DeliveryRequest, receipt, policy, audit, and store contracts.

The same registry owns channel lifecycle control. `ConnectorSetting` stores the
owner's versioned opt-in independently from account bindings. Static channel
configuration is only the initial fallback when no setting exists. Enabling a
channel starts its optional inbound Runtime; disabling cancels that Runtime and
gates endpoint resolution and outbound delivery while retaining bindings.
Creating or discovering a binding never changes the setting.

Provider-specific behavior is documented in [External integrations](integrations.md).

## Failure And Audit Semantics

- Missing, revoked, unauthorized, or unavailable endpoints block before send.
- Unsupported multipart payloads block atomically before the first part.
- Delivery receipts distinguish delivered, retryable known failure, unknown
  outcome, and non-retryable failure.
- Idempotency-key reuse with different target or content is a conflict.
- Timer claim, Workflow run, delivery attempt, endpoint resolution, and schedule
  mutations are persisted for audit and recovery.
- Scheduled sends use the frozen return route. They never fall back to whichever
  Web session happens to be active.

## Verification

Changes should cover Endpoint and Provider registry behavior, Web projection,
target-picker return routes with unchanged multipart ingress, multipart
preflight, direct-send confirmation and idempotency, retry semantics, schema-v2
persistence, Timer worker bounds, due-time routing, schedule target resolution,
optimistic concurrency, toolbar API types, and Web/third-party return routes.
