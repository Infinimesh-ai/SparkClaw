# SparkClaw Protocol

> 语言： [English](../../../packages/protocol/README.md) | 简体中文

SparkClaw services 的共享 API contract notes。

当前 MVP 直接在 Go Gateway 中实现这些 contracts。本 package 记录稳定的 message shapes，使 `webchat`、未来拆分服务和 SDKs 可以收敛到同一套 vocabulary。

主要实体：

- `Session`
- `Message`
- `AgentRun`
- `ToolCall`
- `Approval`
- `Memory`
- `MemoryCandidate`
- `AuditEvent`
- `Event`

Risk levels 为 `read`、`draft`、`reversible` 和 `dangerous`。

Schemas：

- `schemas/message.schema.json`
- `schemas/approval.schema.json`
- `schemas/tool-call.schema.json`
