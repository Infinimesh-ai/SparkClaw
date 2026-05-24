# SparkClaw Tool Schema

> 语言： [English](../../../packages/tool-schema/README.md) | 简体中文

Tool definitions 必须声明：

- name 与 description
- JSON Schema input contract
- risk level
- approval requirement
- idempotency
- timeout
- sandbox policy
- audit policy

当前 Go ToolHub 注册 MVP tools。这些 schemas 是未来 service split 与 external tool packs 的可移植 contract。
