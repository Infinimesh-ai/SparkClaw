# 本地语音输入设计

> Language: 简体中文 | [English](../../docs/local-speech-input-design.md)

> 状态：`codex/voice-complete` 的权威实现契约；已于 2026-07-14 使用真实 ASR 服务验证。

## 1. 决策摘要

SparkClaw WebChat 在现有输入区增加一个麦克风控件。浏览器录制有界的单声道 PCM，编码为 16 kHz PCM16 WAV，并发送给 Gateway。Gateway 校验请求后调用项目专属 ASR 服务 `https://sparkclaw.infinimesh.cloud/asr`。返回的转写文本只插入当前草稿，绝不自动发送。

生产链路如下：

```text
WebChat 麦克风
  -> 浏览器内存中的 PCM 采集
  -> 16 kHz 单声道 PCM16 WAV
  -> 需要认证的 Gateway speech API
  -> 有界的 OpenAI-compatible ASR 适配器
  -> sparkclaw.infinimesh.cloud/asr
  -> 以 sparkclaw-asr 提供服务的 Qwen/Qwen3-ASR-1.7B
  -> 转写文本插入现有草稿
```

语音转写在创建消息之前结束。它不得创建 `Message`、`AgentRun`、`ToolCall`、Approval、Trace Observation 或 Artifact。只有用户检查草稿并显式点击现有发送动作后，正常 Agent 生命周期才开始。

## 2. 范围

包含：

- WebChat 输入区中的单个有状态麦克风按钮。
- 浏览器权限、录音、取消、音量反馈、计时、重采样和 WAV 编码。
- Gateway 状态接口和批量转写接口。
- 面向已配置 SparkClaw ASR 端点的真实 OpenAI-compatible HTTP 适配器。
- 严格校验音频、时长、请求体大小、会话、语言和 request ID。
- 有界的 HTTP 执行、并发、排队、取消和关闭生命周期。
- 只记录元数据的 speech 审计事件。
- 聚焦的前后端测试、桌面/窄屏浏览器检查和真实模型冒烟证据。

不包含：

- 自动发送消息。
- 流式增量转写、VAD、说话人分离、字幕或上传音频文件转写。
- 操作系统全局快捷键或向其他应用注入文字。
- 持久化原始音频、转写历史或可复用语音 Artifact。
- WebChat 直接调用 ASR，或向浏览器暴露上游 ASR URL。
- 云厂商回退，或由请求数据选择目标 URL。
- Telegram、notification binding、credential store、Infinimesh Info 和 `web.search` 改动。

## 3. ASR 服务与模型选择

本次实现使用操作者提供的端点，不在此分支另行打包模型运行时：

| 属性 | 已验证值 |
|---|---|
| Base URL | `https://sparkclaw.infinimesh.cloud/asr` |
| 健康检查 | `GET /health` -> HTTP 200 |
| 运行时 | `GET /version` -> vLLM `0.24.0` |
| 模型发现 | `GET /v1/models` |
| served model | `sparkclaw-asr` |
| 根模型 | `Qwen/Qwen3-ASR-1.7B` |
| 模型上下文 | `4096` |
| 转写接口 | `POST /v1/audio/transcriptions` |
| 请求格式 | OpenAI-compatible multipart 上传 |

选择依据：

- 服务已经部署并可从当前开发机访问，因此功能能以真实推理闭环验证，而不是停留在 fake transcriber。
- 服务接受 WebChat 产生的规范 WAV，并提供标准 OpenAI-compatible 转写路径。
- 模型支持多语言，中英文冒烟样本均成功转写。
- `/version` 和 `/v1/models` 提供稳定运行时与模型身份，`doctor.sh` 可以检测漂移。
- Gateway 可以把协议、主机、超时和隐私边界封装在单一适配器中，而不改变 WebChat 公共 API。

此分支不增加 ASR Docker 镜像或模型权重下载。受管端点就是声明的生产依赖。这里的“可复现”是指 URL、served model、协议、健康检查、请求限制和预期运行时身份进入版本控制并接受检查；模型部署本身由服务操作者负责。

## 4. 真实模型证据

2026-07-14 在 Apple M5 Mac 上对已配置端点执行以下冒烟。音频生成在 `/tmp`，转换为 16 kHz 单声道 PCM16 WAV，上传一次并在验证后删除；仓库不提交音频 fixture。

| 样本 | 音频时长 | HTTP 总时长 | 实时系数 | 结果 |
|---|---:|---:|---:|---|
| 英文 | 4.936 秒 | 0.903 秒 | 0.183 | 句子结构和含义正确；`SparkClaw` 被识别为 `Sparklaw`。 |
| 中文 | 6.586 秒 | 0.921 秒 | 0.140 | 句子和含义正确，出现一处单字替换（`只` -> `之`）。 |

两次请求后服务报告：

- `request_success_total{finished_reason="stop"}=2`
- 运行中和排队请求均为零
- KV cache 使用率为零
- `server_load=0`
- 进程常驻内存约 1.72 GB

常驻内存指标是进程 RSS，不是完整的加速器分配量。它只能证明冒烟时真实服务健康且没有排队，不能替代 DGX Spark GPU 驻留测量。

## 5. 用户体验契约

麦克风按钮位于附件控件之后、文本框之前，保持稳定的 42 px 占位，并使用项目已有图标库。

| 状态 | 点击行为 | 可见结果 |
|---|---|---|
| `disabled` | 无动作 | 本地化的不可用原因 |
| `idle` | 请求权限并开始采集 | 麦克风图标 |
| `requesting_permission` | 忽略重复点击 | 忙碌图标 |
| `recording` | 停止并开始转写 | 停止图标、计时和音量条 |
| `encoding` | 取消准备 | 忙碌图标和准备提示 |
| `transcribing` | 中止 HTTP 请求 | 忙碌图标和本地转写提示 |
| `error` | 开始新一次尝试 | 可恢复的本地化错误 |

`Escape` 取消所有活动状态。取消必须停止全部媒体轨道、关闭 `AudioContext`、清空采样缓冲并中止 Gateway 请求。

草稿插入规则：

- 录音开始时保存 textarea 选择范围。
- 若原草稿未改变，在保存位置插入；否则追加到最新草稿末尾。
- 保留附件以及录音前后用户输入的文字。
- 仅在两侧字符都需要时添加 ASCII 空格。
- 聚焦文本框，并把光标放到插入文本之后。
- 空或纯空白转写不修改草稿，并显示“未检测到语音”。
- 语音路径绝不调用现有 send 函数。

切换会话、删除会话、认证失效、组件卸载或开始发送消息都会取消语音输入。只有当前活动会话仍与录音开始时一致，转写结果才能应用。

## 6. 浏览器音频契约

规范上传格式：

| 字段 | 限制 |
|---|---|
| 容器 | RIFF/WAVE |
| 编码 | 有符号 PCM16 little-endian |
| 采样率 | 16,000 Hz |
| 声道 | 1 |
| 最短时长 | 300 ms |
| 最长时长 | 60 秒 |
| Gateway 最大请求体 | 3 MiB |
| MIME 类型 | `audio/wav` |

WebChat 把单声道、回声消除、降噪和自动增益作为采集偏好。实现使用 `AudioWorklet` 帧，计算平滑 RMS 音量，只缓存当前录音，停止时完成一次降采样，并在编码后立即释放源缓冲。

麦克风需要安全上下文中的 `navigator.mediaDevices.getUserMedia`、`AudioContext` 和 `AudioWorklet`。支持的来源是 `localhost`、`127.0.0.1` 或 HTTPS；不支持局域网主机名上的普通 HTTP。

## 7. Gateway API

### 状态

`GET /api/speech/status` 返回：

```json
{
  "enabled": true,
  "ready": true,
  "state": "ready",
  "backend": "openai-http",
  "model": "sparkclaw-asr",
  "supports_streaming": false,
  "accepted_content_types": ["audio/wav"],
  "max_audio_seconds": 60,
  "max_upload_bytes": 3145728
}
```

`GET /readyz` 包含同样的精简 speech 状态。speech 失败不会让整个 Gateway unready，但 UI 在 speech ready 前保持禁用。

### 转写

`POST /api/speech/transcriptions` 是需要认证的 `multipart/form-data` 请求，只允许一个 `file`，并要求 `session_id` 和 `request_id`；`language` 可选，默认 `auto`。

响应包含请求/会话关联、文本、音频时长、推理时延、模型以及 `audio_retained: false`，不暴露上游 URL。

稳定错误码覆盖请求非法、太短、太大、格式不支持、繁忙、取消、禁用、不可用、超时和推理失败。Gateway 把上游 4xx/5xx 映射为这些错误码，不原样返回上游响应体。

## 8. 上游适配器契约

适配器名为 `openai-http`，目标只来自配置，不能由请求指定。

- Readiness：`GET {base_url}/health`，使用 2 秒子超时。
- 模型身份：配置的 served name 为 `sparkclaw-asr`。
- 推理：`POST {base_url}/v1/audio/transcriptions`，字段为 `file`、`model`、可选 `language` 和 `response_format=json`。
- 禁止重定向。
- 响应体最大 1 MiB。
- 每个请求同时使用调用方 context 和已配置 HTTP timeout。
- 适配器独占一个 `http.Client`，通过 `Close()` 关闭 idle connection，并在 Gateway 关闭时执行。
- 主机名必须与 `allowed_hosts` 精确匹配；默认列表只有 `sparkclaw.infinimesh.cloud`。
- 禁止回退主机、代理选择目标、浏览器传入 URL 或记录 transcript。

当前公开端点不需要应用凭据。本工作不增加 credential store 或 token 配置。若服务未来要求认证，必须通过独立评审变更，以环境变量注入 secret。

## 9. 隐私边界

所选端点使用 HTTPS 且为项目专属，但不是 loopback。原始音频会从 Gateway 所在工作站越过边界传输到 `sparkclaw.infinimesh.cloud`。这是本次实现明确授权的隐私边界。

- WebChat 不直接调用 ASR。
- 音频只在浏览器内存、Gateway 请求内存和推理期间的上游请求体中存在。
- Gateway 不把音频写入 workspace、artifact、trace、audit payload 或 state backend。
- Gateway 不把 transcript 文本写入日志、trace 或 audit event。
- Audit 元数据可包含 request ID、session ID、字节数、时长、模型、延迟、结果和稳定错误码。
- 只有用户随后通过正常消息 API 发送编辑后的草稿，transcript 才会持久化。
- 本实现不对受管 ASR 服务内部的数据保留方式作保证；服务端保留策略由操作者负责，是明确的遗留隐私风险。

## 10. 生命周期与资源限制

单用户运行时默认值：

| 资源 | 默认值 |
|---|---:|
| 活动转写 | 1 |
| 排队转写 | 1 |
| Gateway/上游超时 | 120 秒 |
| Readiness 超时 | 2 秒 |
| 音频时长 | 60 秒 |
| 上传请求体 | 3 MiB |
| 上游 JSON 响应 | 1 MiB |

适配器使用一个 admission channel 限制活动加排队工作，用一个 worker channel 限制活动推理。admission 满时立即返回 `429 speech_busy`。排队等待和 HTTP 推理都必须响应请求取消。

Gateway 启动时只构造一个 transcriber。启用状态下配置非法会导致启动失败。关闭时先取消 server context，通过现有 HTTP server shutdown 收敛请求，再调用 `Transcriber.Close()`。

## 11. 配置

生产默认值：

```json
{
  "speech": {
    "enabled": true,
    "backend": "openai-http",
    "base_url": "https://sparkclaw.infinimesh.cloud/asr",
    "allowed_hosts": ["sparkclaw.infinimesh.cloud"],
    "model": "sparkclaw-asr",
    "default_language": "auto",
    "timeout_seconds": 120,
    "max_audio_seconds": 60,
    "max_upload_bytes": 3145728,
    "max_concurrency": 1,
    "max_pending": 1,
    "retain_audio": false
  }
}
```

环境变量覆盖统一使用 `SPARKCLAW_SPEECH_*`。`retain_audio=true` 必须被拒绝。启用时要求 HTTPS、精确允许的主机名、非空模型以及经过规范化的正数限制。`enabled=false` 时后端规范化为 `disabled`。

## 12. 文件所有权与共享文件修改

speech 独占文件：

- `services/gateway/internal/speech/*`：类型、disabled 适配器、OpenAI HTTP 适配器、WAV 校验和聚焦测试。
- `services/gateway/internal/gateway/speech.go`：speech 专属 HTTP 解码、校验、审计和错误映射。
- `apps/webchat/src/audio/*`、`public/pcm-worklet.js`：浏览器采集与编码。
- `apps/webchat/src/hooks/useVoiceInput.ts`：语音状态机和取消。
- `apps/webchat/src/components/VoiceInputButton.tsx`：语音专属 UI。
- `docs/local-speech-input-design.md` 及中文镜像：权威设计。

必要的共享文件修改严格限制在 speech：

- `services/gateway/internal/config/config.go`：只增加 `SpeechConfig`、默认值、校验和环境变量覆盖。
- `services/gateway/cmd/sparkclaw/main.go`：只构造一个 transcriber、注入并 defer `Close()`。
- `services/gateway/internal/gateway/server.go`：只保存注入的 transcriber、注册两个 speech route 并暴露精简 readiness。
- `apps/webchat/src/App.tsx`：只增加 textarea ref、voice hook/button/status、光标感知草稿插入以及 send/voice 互斥。
- `apps/webchat/src/api/client.ts` 和 `api/types.ts`：只增加 speech 状态/转写契约。
- `apps/webchat/src/i18n.ts` 和 `styles/app.css`：只增加本地化 speech 文案和固定输入区状态样式。
- `configs/sparkclaw.default.json`、`docker/env/sparkclaw.example.env`、`docker/compose.yaml`：只声明/传递 speech 配置，不增加 ASR 容器。
- `scripts/doctor.sh`：只验证健康、运行时版本和 served model，不上传音频。

任何 store interface、Telegram、binding、notification、reminder、Weixin、ToolHub 或 Agent Runtime 文件都不属于本次改动。

## 13. 验收与验证

完成必须满足：

- 真实浏览器录音能够经过 Gateway 到达真实 Qwen3-ASR 端点。
- 成功转写插入保存的草稿位置。
- 仅转写不会创建消息，也不会触发发送。
- 音频和 transcript 内容不出现在 state、artifact、trace、audit payload 或应用日志中。
- 非法 WAV、超大、超时长、繁忙、取消、超时和上游失败都有聚焦测试。
- HTTP 适配器拒绝重定向和非 allowlist 主机，并提供 `Close()`。
- `doctor.sh` 验证 `/health`、vLLM 版本和 `sparkclaw-asr` 模型身份。
- 桌面和窄屏视口没有输入区重叠，按钮各状态可用。
- 中英文真实模型冒烟成功；报告延迟和识别观察，但不夸大质量。
- Gateway build/vet/test、WebChat build、文档镜像/链接检查和 worktree clean 全部通过。

未来的 streaming、VAD、服务认证和服务端 retention 保证必须另行设计评审，并且不能改变“只写草稿、检查后发送”的产品契约。
