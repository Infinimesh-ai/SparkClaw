# SparkClaw Protocol

Shared API contract notes for SparkClaw services.

The current MVP implements these contracts directly in the Go Gateway. This package records stable message shapes so `webchat`, future split services and SDKs can converge on the same vocabulary.

Primary entities:

- `Session`
- `Message`
- `AgentRun`
- `ToolCall`
- `Approval`
- `Memory`
- `MemoryCandidate`
- `AuditEvent`
- `Event`

Risk levels are `read`, `draft`, `reversible` and `dangerous`.

Schemas:

- `schemas/message.schema.json`
- `schemas/approval.schema.json`
- `schemas/tool-call.schema.json`
