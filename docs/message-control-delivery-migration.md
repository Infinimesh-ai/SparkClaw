# Message Control and Delivery Migration

> Language: English | [简体中文](../zh-cn/docs/message-control-delivery-migration.md)

This guide records the implemented Message Control and Delivery boundaries,
the compatibility behavior for existing Reminder and connector data, and the
single registration path for future messaging providers.

## Runtime Boundaries

- `internal/app/message_control.go` defines provider-neutral Endpoint and
  Schedule contracts. Existing `MessageContent`, `WorkflowResult`, and
  `DeliveryRequest` remain the only result/delivery contracts.
- `internal/messagecontrol` implements Endpoint Registry, Schedule Registry,
  and Return Route Resolver over current Store methods.
- `internal/delivery` implements Delivery Gateway, Provider Registry,
  full-payload capability negotiation, and governed Artifact resolution.
- `internal/reminder` is now the Timer Runtime compatibility package. Its
  production polling loop only claims and enqueues work into a bounded queue;
  four workers own provider calls and scheduled Agent execution.

Delivery requests carry owner authorization. Schedule creation and final
delivery both reject an Endpoint owned by another principal. This check is in
the control/gateway layers and does not depend on provider behavior.

## Provider Registration

A new third-party messaging system is added at the connector composition
point, not in the Delivery Gateway:

1. Implement the existing binding and inbound Runtime ports where applicable.
2. Implement `delivery.Provider`: stable key, declared content capabilities,
   and ordered delivery of the complete request.
3. Resolve every binary Part before the first external send. Unsupported or
   unresolved Parts must fail the whole request explicitly.
4. Register the adapter once through `connector.Registration.Provider`.

The Endpoint Registry stores the registration key and binding reference.
Provider credentials and native clients remain in the adapter. Current output
capabilities are:

| Adapter | Text | Image | Audio/voice | File |
|---|---:|---:|---:|---:|
| Web local compatibility | yes | yes | yes | yes |
| Weixin | yes | yes | no, explicit failure | yes |
| Telegram | yes | yes | yes | yes |

## Schedule Compatibility

`Reminder` remains the persisted compatibility record and public API shape.
New records embed a versioned `ScheduleSpec` with:

- payload mode: `literal` or `request`;
- ordered multimodal content;
- owner/actor authorization;
- ReturnRoute and optional expected capability family.

Old records without `ScheduleSpec` are projected as literal text schedules.
Binding/session ownership is derived before delivery. Memory and file state
need no collection migration; Postgres adds nullable `schedule_spec JSONB` in
both the runtime schema and `migrations/0001_core.sql`.

The `reminders.create` tool accepts optional `payload_mode` and
`expected_capability`. Existing callers default to `literal`. Existing list,
update, cancel, recurrence, retry, and delivery history behavior remains.

## Workflow Integration

The current composition layer adapts scheduled request envelopes to the legacy
Agent entry point and converts a completed Agent result back to the shared
`WorkflowResult -> DeliveryRequest -> Gateway` path. It refuses to present
`approval_pending` or `browser_login_blocked` runs as completed output.

When the Workflow branch is integrated, replace only
`cmd/sparkclaw/scheduled_messages.go` with the common message/work queue
publisher. Schedule Registry, Timer workers, Return Route Resolver, Delivery
Gateway, and Provider adapters remain unchanged.

Web delivery currently preserves the prior local receipt behavior. Persisted
Web events/streaming can replace `delivery.LocalWebDelivery` through its port
without changing Workflow or provider code.
