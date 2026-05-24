# SparkClaw Policy Engine

> 语言： [English](../../../packages/policy-engine/README.md) | 简体中文

MVP policy engine 位于 `services/gateway/internal/policy`。

核心不变量：

- denied tools 永不运行
- dangerous tools 必须经过 approval
- MVP 中 reversible tools 也必须经过 approval
- mutating tools 必须具备 sandbox policy coverage
- approval records 包含 reason、resources 与 raw arguments

默认 policy surface 见 `configs/tools.policy.json` 与 `configs/sandbox.policy.json`。
