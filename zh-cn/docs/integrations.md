# 外部集成

> 语言： [English](../../docs/integrations.md) | 简体中文

本文档总结当前有效的可选集成边界。环境默认值见
`docker/env/sparkclaw.example.env`，启动命令见[部署](deployment.md)。

## 共同规则

- credential、readiness 或 capability 检查失败时，集成保持 disabled 或关闭失败。
- secret 从环境变量或文件注入，不出现在公开 Gateway 配置中。
- 外部内容是不可信证据，绝不成为 system instruction。
- outbound call 有明确 host allowlist、deadline、body limit、retry limit 和 audit record。
- messaging provider 进入 Connector/Delivery Registry；data provider 进入 typed adapter contract。

## Telegram

Telegram 是可选 private-chat connector，可同时存在多个 Bot binding。每个 Bot token 激活前
先验证，再通过 credential vault 独立加密；持久化状态只保存 ciphertext envelope。

已验证 Bot 初始没有 recipient。第一条 fresh authorized private message 原子 claim user/chat；
历史 update 和 group 不能 claim。每个 binding 独立拥有 cursor、inbox identity、ordering 和
recipient authorization。long polling、global concurrency、pending work、attachment size/count
和 voice duration 都有边界。

Inbound text/media 进入共享 Message Runtime，voice note 委托共享 speech transcriber。
Outbound text/media、定时结果和 approval prompt 使用[消息与定时任务](messaging-and-scheduling.md)
中的同一个 Delivery Gateway。

主要配置使用 `SPARKCLAW_TELEGRAM_*`。Bot API 默认官方 endpoint，polling 有界，并只允许 private chat。

## 微信

微信是通过同一 provider-neutral 接口注册的可选 connector。QR/binding lifecycle、polling/media、
address 和 acknowledgement 留在微信 package 内。Agent Runtime、Timer 和 Delivery Gateway
不按微信名称分支。

配置使用 notification channel block 和对应环境变量。被撤销或不可用 binding 仍可见，
但不能选作 delivery target。

## 语音转写

Speech 是 WebChat microphone 和 Telegram voice note 共享的可选 OpenAI-compatible
transcription adapter。WebChat 录制有界 mono 16 kHz PCM16 WAV，并调用：

```text
GET  /api/speech/status
POST /api/speech/transcriptions
```

Gateway 在调用配置的 allowlisted endpoint 前校验 media type、WAV structure、duration、
upload size、request ID、session 和 language。adapter 默认关闭，endpoint 和 allowlist
默认为空；只有显式配置 service URL、allowed host 和 served model 后才能启用。

WebChat transcript 只插入当前 draft，绝不自动发送。转写不创建 chat message、Agent run、
Tool Call、approval 或 artifact。audio byte 不保留，audit 只记录有界 metadata 和 outcome。
queue/concurrency 超限返回明确 busy 或 unavailable 状态。

配置使用 `SPARKCLAW_SPEECH_*`，包括 endpoint、allowlist、model、language、timeout、duration、
upload、concurrency、pending 和 expected runtime version。

## Infinimesh Info

Infinimesh Info 是 `web.search`、直接 `info.query` 和天气 Workflow evidence query 的可选
生产 provider。adapter 保留冻结 query 和 request ID，把 one-shot query token 放在内存
wallet 中，并限制 retry 和 response size。

SparkClaw 把 summary、非空 key fact、公开 source metadata、snippet 和 citation 映射为稳定
evidence ref，在模型调用前选择与 query 相关的有界 projection。summary 缺失不会隐藏可用
结构化 fact，provider status 文本也不会伪装成答案。

配置使用 `SPARKCLAW_INFINIMESH_INFO_*`。entitlement、device attestation 和 license proof
可以直接或通过文件提供，但绝不能进入 public config、log、trace 或 artifact。

## ISCP Bridge

可选 ISCP Bridge 是位于 JingSi App 与 loopback Gateway 之间的独立进程。它使用 ISCP v0.1.0
Core SDK 处理设备身份、Trust Grant、Session Hello/Ready、proof-of-possession 和
SecureEnvelope。Bridge 把加密的 `agent.*.v1` 请求映射到一个带认证的本地 Gateway 端点；
session、run、policy、approval、event 和 audit 仍由 Gateway 统一负责。

Bridge 不接收 ITES token，也不暴露无认证的局域网 listener。生产设备身份密钥保存在操作系统
keyring，Relay credential 独立轮换，Gateway 不支持的能力不会进入 manifest。注册、版本化
schema、App CI mock 和 GB10 运维见 [ISCP Bridge](iscp-bridge.md)。

## Binding 与状态 API

WebChat 通过 Gateway API 管理当前 connector binding：

```text
GET    /api/notification-bindings
POST   /api/notification-bindings/{channel}/start
GET    /api/notification-bindings/{id}
DELETE /api/notification-bindings/{id}
GET    /api/delivery-endpoints
```

UI 展示 Endpoint Registry 提供的软件、账号、接收人、会话、capability 和 status，不从
channel name 推断 destination，也不暴露 native recipient ID。

## 验证

集成改动需要覆盖 disabled/unavailable、secret redaction、host/timeout enforcement、Store
backend parity、binding lifecycle、authorization isolation、payload limit、retry、connector
shutdown 和端到端 Message Runtime/Delivery Gateway。credential-gated live check 只能补充，
不能替代确定性本地测试。
