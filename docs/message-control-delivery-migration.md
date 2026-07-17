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
Binding/session ownership is derived before delivery. Missing historical
dedupe keys receive a stable `reminder:<id>` projection, while legacy direct
recipient/context/credential fields are resolved inside the matching Provider.
Memory and file state need no collection migration; Postgres adds nullable
`schedule_spec JSONB` in both the runtime schema and `migrations/0001_core.sql`.

The `reminders.create` tool accepts optional `payload_mode` and
`expected_capability`. Existing callers default to `literal`. Existing list,
update, cancel, recurrence, retry, and delivery history behavior remains.

## Workflow Integration

Integration is complete for the router-first vertical slice. Web, Telegram,
Weixin, and Timer ingress carry a `MessageIngressContext`; its owner,
authorization, source Endpoint, route decision, and ReturnRoute persist on the
Run. Matched Workflows return their original `WorkflowResult` unchanged.
Unmatched ReAct uses one bounded compatibility adapter, and a matched result
without its WorkflowResult fails explicitly.

Ordinary connector replies and scheduled request results both follow
`WorkflowResult -> DeliveryRequest -> Delivery Gateway -> Provider`. Business
failure/blocked results are still deliverable output and do not become a Timer
transport failure after successful delivery. Approval or browser-login waiting
states remain explicit and are never presented as completed scheduled output.
Document-processing outputs and browser screenshots become file/image message
parts only when ToolHub metadata declares that output kind and the path stays
inside the linked workspace.

The parallel `notification.Router`, connector notification registration, and
Reminder notification bridge have been removed. Provider-specific direct sends
remain only for connector control traffic such as typing, commands, and
approval prompts.

Web delivery currently preserves the prior local receipt behavior. Persisted
Web events/streaming can replace `delivery.LocalWebDelivery` through its port
without changing Workflow or provider code.
