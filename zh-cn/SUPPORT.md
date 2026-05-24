# 支持

> 语言： [English](../SUPPORT.md) | 简体中文

SparkClaw 是开源项目。除非维护者另行说明，支持以社区 best-effort 方式提供。

## 去哪里提问

- 可复现 bugs、documentation gaps 和 feature proposals 请使用 GitHub issues。
- 如果启用 discussions，可用于 setup help 和开放问题。
- 不要发布 secrets、tokens、traces、`.env` files 或 private documents。

## 有用的调试信息

部署问题请包含：

- host OS and architecture
- Docker and Docker Compose versions
- 如果使用 DGX Spark model serving，包含 GPU model and driver
- 运行的 command
- `bash scripts/doctor.sh` 的 sanitized output
- 相关 Compose profile
- sanitized Gateway logs

Runtime behavior 问题请包含：

- 已移除 private data 的 prompt 或 workflow
- model mode（`mock` 或 `external`）
- 相关 tool names
- sanitized trace metadata
- 是否能在 `bash scripts/run-eval.sh` 中复现
