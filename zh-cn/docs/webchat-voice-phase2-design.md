# WebChat 语音 Phase 2：原生 Realtime ASR 与静音结束

> 语言：[English](../../docs/webchat-voice-phase2-design.md) | 简体中文

> 状态：已在 commit `849f927` 完成实现、通过 candidate qualification，并于 2026-08-19
> 切换到 production WebChat 入口；desktop 与 mobile 的实体麦克风验收仍待完成。本文展开
> [WebChat 语音输入闭环设计](webchat-voice-input-design.md)的 Phase 2。Phase 1 继续作为
> batch fallback，LLM 润色继续属于 Phase 3。

本设计要求真正的边录边转写。完整 WAV upload 结束后才 stream decoder output 的方式明确
不在范围内。

## 已确认决策

- 首轮 qualification 固定使用当前 `Qwen/Qwen3-ASR-0.6B` 权重。Serving runtime 可以为暴露
  官方 native streaming API 而改变，但在取得该模型的实测证据前不切换其他模型。
- Silence auto-stop 只保存在 browser local，且**默认关闭**。Manual stop 仍是默认交互。
- Realtime capture 一旦开始，任何 transport、protocol、backpressure 或 backend failure 都立即
  停止 capture。WebChat flush failure boundary 前已经接受的 PCM，自动以一份完整 WAV 执行 batch
  transcription，然后结束本次录音 operation；中途失败后绝不继续录音。
- LLM 润色继续不属于 Phase 2。

## 实现与 Qualification 状态

Production path 已完成 ASR runtime、Gateway、Nginx proxy 与 WebChat 的完整 Phase 2 链路。
ASR image 以一个 `Qwen3ASRModel.LLM` instance 同时处理 batch 与 native realtime
调用，把全部 model operation 固定在同一个 owner thread，并在打开 HTTP listener 前执行一次
300 ms 静音 inference。因此，`ready=true` 后的第一个 owner request 不再承担 backend 的首次
inference 冷启动。

本机 DGX Spark qualification 使用 image
`sha256:3f20b317af0332062923bd6fb8176cee95ac0ac1e52221a53927c761de7c08f3`，
取得以下证据：

- 冷启动 121 秒后 ready，其中首次 inference warm-up 为 48.1 秒。Ready 后第一笔真实 batch
  request 用时 183 ms，并与 production transcript 完全一致：`Front, left, front, center,
  front, right.`
- 一段 4.439 秒、按录音速度发送的 stream 在约 1.1 秒后产生第一个 model partial，之后按每个
  completed model chunk 修订，最终返回相同 authoritative final；累计 model inference 为 470 ms。
- 一段 60 秒、按录音速度发送的重复语音 fixture 在 60.7 秒完成，产生 43 个 revision，累计
  model inference 为 17.8 秒，unacknowledged audio 峰值为 3.3 秒，低于 5 秒 hard bound。
- Realtime session 持有容量时，并发 batch request 返回 `429`；关闭 session 后，下一次 batch
  inference 在 161 ms 内完成，证明 capacity 已先释放。
- Desktop 与 390 px mobile Chromium fake-microphone pass 穿过真实 AudioWorklet、resampler、
  Vite proxy、authenticated Gateway ticket 与 ASR session。Stop 前可以看到 model partial；
  final 只进入草稿而未发送，且健康路径没有发起 batch transcription request。
- ASR runtime protocol 与 owner-thread warm-up suite 的 6 条测试通过；focused Gateway race
  suite 和 WebChat unit/build gate 也已通过。
- Production cutover 已替换 port `18790` 后的 ASR、Gateway 与 WebChat container。Readiness
  已声明 native streaming；production path WebSocket smoke 收到 `ready`、10 个有序 audio
  acknowledgement 和同一 session 的 `final`，随后关闭隔离的 `18792`、`18794` 与 `18006`
  listener。

Candidate qualification 与 production transport smoke 已形成 deployment cutover 证据。实体
麦克风交互、silence stop 的 device/noise corpus，以及 live failure 到 complete-WAV recovery
drill 仍是 release check。

## 不可妥协的 Realtime 定义

Phase 2 指的是**边录边转写**，不是完整 upload 结束后再流式返回 response。只有同时满足
以下条件，才能称为 realtime：

1. WebChat 在停止录音前持续发送 active microphone 的 PCM。
2. 一个 stateful ASR session 在 capture 仍然 active 时消费 PCM 并执行 inference。
3. 对于长度足以填满一个 inference chunk 的正常口述，WebChat 必须在发送 `finish` 前收到
   至少一个由模型产生的 partial snapshot。
4. `finish` flush 同一个 ASR state，并返回一个 authoritative final snapshot。

vLLM OpenAI-compatible `POST /v1/audio/transcriptions` 的 `stream=true` 不符合此定义。
它仍要求先收到完整 audio file，只是在之后以 stream 方式返回 decoder output。Phase 2 绝不
通过拆分或反复上传 WAV 来模拟 realtime。健康 realtime operation 在录音期间不得发起任何
batch transcription request。

## 已验证的本地 Engine 边界

`Qwen/Qwen3-ASR-0.6B` 通过 vLLM backend 支持官方 Qwen streaming API。其 native API
拥有 `ASRStreamingState`，接收任意长度的 16 kHz PCM increment，在累计到 configured model
chunk 时执行 decode，通过 prefix rollback 修订整个 transcript snapshot，并在 finish 时显式
flush tail。当前官方实现会重新送入累计 audio，并修订不稳定的 suffix token；它仍然是真正的
record-time inference，但不是带 timestamp 的 delta protocol，也不是 cached recurrent encoder。

当前部署的 SparkClaw ASR service 还不能暴露该 API：

- OpenAPI surface 没有 realtime/session endpoint；
- `stream=true` 属于 completed-file endpoint；
- 正在运行的 image 不包含拥有 `init_streaming_state`、`streaming_transcribe` 和
  `finish_streaming_transcribe` 的 `qwen_asr` inference package。

官方 `qwen-asr` package 当前 pin 的 vLLM release 与已部署 image 不同。因此 Phase 2 必须先做
compatibility qualification，不能原地执行 `pip install`。在替换当前 ASR image 前，candidate
image 必须在已部署 DGX Spark 上证明 model startup、streaming state correctness、batch quality
parity、memory bound 和 cold-boot recovery。

## 目标拓扑

```text
WebChat AudioWorklet
  -> continuous native mono PCM capture
  -> stateful resample to 16 kHz PCM16
  -> authenticated Gateway WebSocket
      -> sequence、size、duration、owner/session 与 backpressure 校验
      -> 一个 upstream realtime ASR session
          -> Qwen3ASRModel.LLM singleton
          -> native ASRStreamingState
          -> 录音期间产生 revisioned partial snapshot
      <- revisioned partial/final snapshot
  -> 与 editable draft 分离的 partial preview
  -> 对 Phase 1 draft anchor 只进行一次 final reconciliation

capture 开始后的 realtime failure
  -> 原子冻结 partial state 并关闭 capture boundary
  -> 立即停止 microphone/worklet 并 flush resampler
  -> 把该 boundary 前的全部 PCM 编码为 canonical WAV
  -> 自动调用一次 complete-WAV batch transcription
  -> 替换 partial，并通过 Phase 1 draft anchor reconcile
```

WebChat 绝不直连 model service。Gateway 拥有 public authentication、session authorization、
admission、cancellation、bound、protocol normalization 与 audit。ASR runtime 只拥有 model state，
不拥有任何 SparkClaw user/session authority。

## 一个 ASR Process，而不是两份 Model

`sparkclaw-asr` container 改为一个围绕单个 `Qwen3ASRModel.LLM` singleton 的轻量
SparkClaw-owned runtime。该 process 提供：

- 用于现有运维的 `/health`、`/version` 和 `/v1/models`；
- 用于 Phase 1 batch contract 和 Telegram voice note 的
  `/v1/audio/transcriptions`；
- 一个用于 stateful PCM/partial/final exchange 的 internal realtime WebSocket endpoint。

Batch 与 realtime admission 共享同一个 model worker 和 configured capacity。初版保留一个
active inference operation，因为官方 streaming API 是 single-stream 且不能 batch。不得在 stock
vLLM OpenAI server 旁再运行第二个 embedded Qwen engine；这会重复加载模型、分裂 readiness，
并使 memory 行为不可预测。

ASR runtime 只在 Gateway admission 后创建 state，把 model singleton 调用串行化，为 pending PCM
设置独立于 network buffer 的上限，清理 abandoned session，并在每个 terminal path 销毁
audio/state。它绝不把 PCM、WAV、partial text 或 final text 写入 disk 或 log。
Realtime-to-batch recovery 会先幂等销毁 realtime state 并释放其 admission slot，再让 fallback
request 进入 batch admission；两个 inference operation 绝不重叠，也不会等待仍被同一 voice
operation 持有的 slot。

## Browser 到 Gateway 的 Session Bootstrap

Browser WebSocket API 无法附加 SparkClaw 的普通 bearer header。因此 WebChat 先发起一个
authenticated request：

```text
POST /api/speech/realtime-sessions
```

Body 携带 `session_id`、`request_id`、language 与固定 audio format。Gateway 校验 authenticated
owner/session relationship，并预留 realtime capacity。Response 返回 opaque、random、single-use、
30 秒过期的 WebSocket ticket 和确切 same-origin WebSocket URL。Gateway 只在内存中保留 ticket
hash 及其绑定 principal。Upgrade 会原子消费 ticket；replay、expiry、wrong route 或第二条 connection
都 fail closed。Access log 必须 redact ticket query value。

即使 authentication 被关闭，ticket 仍然必需，并绑定 default local owner。这样两种部署 posture
使用同一协议。Nginx 增加一个精确 WebSocket location，并设置 `Upgrade` 与 `Connection` header；
generic `/api/` proxy 不得意外开放 upgrade route。

## Wire Contract

协议版本为 `sparkclaw.speech.realtime.v1`。

Client binary audio frame 由固定 header 和 PCM 构成：

```text
u32 sequence, big endian
u32 sample_count, big endian
sample_count * i16 little-endian mono PCM samples
```

WebChat 发送 100 ms frame：16 kHz 下 1,600 个 sample、3,200 个 PCM byte。Sequence 从零开始且
必须连续。Gateway 拒绝 duplicate、gap、malformed length、非 canonical format、超过 configured
upload bound 的 byte 或超过 `max_audio_seconds` 的 audio；绝不以 silence 填补 gap。ASR runtime
把 transport frame 组合成 server-owned、初始 1.0 秒的 model chunk。`chunk_size_sec`、prefix
rollback 和 generation-token limit 属于 runtime policy，不是 browser 提供的 knob。

Client control frame 使用 JSON：

| Event | 必填字段 | 含义 |
|---|---|---|
| `finish` | `last_sequence`、`captured_ms`、`reason` | Flush 全部已接受 PCM 并请求 final output |
| `cancel` | `last_sequence` | Abort 并丢弃 operation，不修改 draft |

`reason` 只能是 `manual_stop`、`silence_stop` 或 `max_duration`。Server event 是完整 JSON snapshot：

| Event | 重要字段 | 语义 |
|---|---|---|
| `ready` | protocol、format、limit | Model state 已存在，可以发送 audio |
| `ack` | accepted sequence、received audio ms | 已被 in-memory session 连续接受的最高 audio 位置 |
| `partial` | revision、text、language、audio end ms | 替换 prior preview；绝不作为 delta append |
| `final` | revision、text、language、duration/inference ms、stop reason | 来自同一 streaming state 的 authoritative result |
| `fallback` | code、retryable | Realtime 无法继续；停止 active capture，并自动 batch 本地保留的 PCM |
| `error` | code、retryable | Operation 无法安全继续 |

Revision 从一开始，只在 normalized text 或 language 改变时增加。Partial 与 final payload 均有界。
即使 final text 与上一版 byte-identical，final revision 也必须比所有 partial 更新。任何 event 都不包含
draft、bearer credential、device ID、raw audio 或 internal exception。

## Browser Audio Ownership 与 Backpressure

`PCMInputCapture` 继续在内存中保留完整 operation，供 Phase 1 fallback 使用。Stateful resampler
跨 AudioWorklet callback 保持 fractional position，只生成一次 canonical 16 kHz PCM16，并同时送入
WebSocket frame buffer 与最终 WAV wrapper。禁止逐 callback 独立 resample，因为这会产生 boundary
sample gap 或 duplicate。

Browser 保留小型 bounded unsent queue，并监控 `WebSocket.bufferedAmount`。Gateway 与 ASR runtime
分别保留一个以 audio millisecond 计量的独立 bounded queue。每一跳的初始目标都不超过五秒。
系统绝不为了追赶进度而静默 drop audio。如果任一 queue 达到上限，realtime session 发出
`speech_stream_overrun` 后关闭，WebChat 立即停止 capture，并自动 batch-transcribe failure boundary
前保留的 PCM。

WebChat 在预留 realtime capacity 前先取得 microphone permission 和 selected device track，因此
pending permission prompt 不会占住唯一 ASR slot。在 WebSocket 发出 `ready` 前，它不启动
AudioWorklet，也不接受 PCM。若 ticket admission、connection 或 readiness 在 capture 开始前失败，
且 batch endpoint 仍 ready，WebChat 启动一个明确标为 Phase 1 batch-only 的 recording，不声称提供
live transcription。若两种 speech mode 都不可用，WebChat 释放 track，并在不启动 capture 的情况下
报告错误。

`ready` 之后的每种 realtime failure 都 dispatch 同一个 idempotent recovery action。该 action 关闭
operation input boundary，使之后的 worklet callback 被忽略；flush capture/resampler 已接受的 sample；
停止 track 和 audio context；把 partial 冻结为 non-authoritative；关闭 realtime session；只把完整
local PCM wrap 一次；等待其 bounded、idempotent capacity release；然后自动调用 Phase 1 batch
endpoint。任何 recovery path 都不会回到 recording state。

## Partial 与 Final Draft Reconciliation

Qwen streaming output 是对整个 recognized prefix 的 revision，不是 append-only token stream。因此
WebChat 在 composer 附近显示一个视觉次要的 partial preview，并在每个 revision 到达时原子替换。
Partial text 绝不进入 textarea、不改变 selection、不成为 message，也不持久化。

收到 `final` 时，WebChat 构造现有 `SpeechTranscriptionResult`，并且只通过 Phase 1 anchor rule
应用一次：

- session 与 draft snapshot 未改变：替换 captured selection；
- draft 已改变：保留一个显式 pending-insert candidate；
- operation 已取消/替换或 session 已改变：忽略 late result。

健康 stream 的 final event 是 authoritative。SparkClaw 不再运行第二次 batch transcription 只为比较
wording。若 stream 失败，或 bounded finalization deadline 内没有收到 final，capture 已经停止或会
被立即停止，并把完整 in-memory WAV 自动通过 batch path 发送一次。成功的 batch result 替换冻结的
partial preview，并使用同一 anchor rule。若 batch 也失败，继续提供 Phase 1 已有的五分钟 same-WAV
显式 retry；绝不把不稳定 partial 当作 final 插入。

## Silence Auto-Stop

Silence auto-stop 是 browser-local recording controller，与 ASR partial text 无关。它观察与 capture
完全相同的 PCM sample clock，绝不 gate、trim 或 withheld 送往 realtime model 的 audio。因此触发
stop 的 trailing silence 在发送 `finish` 之前已经进入 ASR session。

Microphone menu 使用 option set，而不是在 toolbar 再增加一个 button：

| Mode | Trailing silence | 初始默认 |
|---|---:|---|
| Off | 无 | Phase 2 首版默认选择 |
| Standard | 1.2 秒 | Owner 可选 |
| Patient | 2.0 秒 | 适合刻意慢速口述 |

该 preference 只保存在 browser local，不进入 owner memory 或 Gateway configuration。

初始 detector 保留 OpenLess 有价值的 one-shot contract，同时为每个 device 自适应 threshold：

1. 在 confirmed speech 之前跟踪 rolling low-percentile noise floor。
2. 只有至少 160 ms sustained activity 高于由 noise floor 推导的 start threshold，才进入
   `speech_active`。
3. 使用更低的 end threshold 形成 hysteresis；短暂停顿不会 reset speech state。
4. Confirmed speech 之后，只有连续 below-threshold audio 达到 selected trailing-silence duration，
   才发出且只发出一次 `auto_stop`。
5. 十秒内始终未确认 speech 时，发出 `no_speech_cancel` 并丢弃 empty operation。

全部 duration 依据累计 audio sample，而不是可能被 throttle 的 page timer。单个 loud transient 不能
arm detector。Deadline 前恢复 speech 会取消 trailing-silence countdown。持续噪声环境下的安全失败
方式是继续录音，直到 manual stop 或 maximum duration；detector 必须宁可漏掉 auto-stop，也不能
截断 speech。Manual stop、cancel、device loss 和 maximum duration 始终优先于稍后到达的 VAD decision。

Threshold algorithm 隔离在 pure `SilenceDetector` contract 后，并以 recorded fixture 测试。只有
device/noise corpus 无法达到 false-stop gate 时才考虑 model-based VAD；它必须消费现有 PCM stream，
不得打开第二个 microphone，也不得依赖 remote service。

## State Ownership

Main voice reducer 增加以下 phase：

```text
acquiring_microphone
  -> connecting_realtime
      -> starting_capture -> recording_realtime -> finalizing_realtime
      -> starting_batch_capture -> recording_batch_only

recording_realtime | finalizing_realtime
  -> recovering_batch -> encoding -> transcribing

recording_batch_only
  -> encoding -> transcribing

finalizing_realtime | transcribing
  -> idle | pending_insert | retryable_error
```

Silence detection 使用独立 deterministic sub-state：

```text
disabled | waiting_for_speech -> speech_active -> trailing_silence -> decided
```

它只能向 main reducer 发出 `auto_stop` 或 `no_speech_cancel`，避免 transport 与 VAD state 形成
Cartesian product。一个 operation generation 统一拥有 capture、resampler、VAD、WebSocket、sequence
counter、queue、完整 PCM/WAV、draft anchor、timer 与 abort handle。每个 callback 都在 mutation 前
检查 generation 和 session。

## Failure 与 Fallback Matrix

| Failure point | 必须行为 |
|---|---|
| Capture 前 microphone permission/device acquisition 失败 | 不启动 recorder，释放已取得的 resource，并报告 Phase 1 device error |
| Capture 前 ticket/admission 或 connect/ready 失败 | Batch ASR ready 时启动有明确标记的 Phase 1 batch-only recording；否则释放 track 并报告 busy/unavailable |
| Capture 开始后的 sequence gap 或 malformed frame | Realtime fail closed，立即 stop/flush capture，并自动 batch 精确的 local PCM；绝不在 fabricated silence 上 inference |
| 录音期间 network/backend loss | 冻结 partial，立即 stop/flush capture，并自动 batch 精确的 local PCM |
| Backpressure bound exceeded | 不 drop audio；立即 stop/flush capture，并自动 batch 精确的 local PCM |
| First-sample timeout 或 device loss | 在最后一个本地已接受 sample 处停止，有 usable PCM 时自动 batch；录音中绝不切换 device |
| Manual cancel | Abort socket/model state，丢弃 PCM/WAV/partial，不改变 draft |
| Silence auto-stop | Flush capture/resampler，发送全部 frame，再以 `silence_stop` 执行 `finish` |
| Final timeout 或 invalid final | Capture 已停止；关闭 realtime，并自动调用一次 complete-WAV batch transcription |
| Batch fallback retryable failure | 继续使用现有五分钟 retry contract 保留同一 WAV/request ID |
| Session switch/unmount | Cancel 全部 resource 并忽略所有 late partial/final |

UI 必须区分 live transcription、batch-only capture 与 automatic recovery transcription。无法再
finalization 的 partial 会被冻结并标记为 recovering，不能继续显示得像 authoritative result。
Recovery 成功后通过正常 draft-anchor result 结束 operation，绝不恢复录音。

## Bound、Privacy 与 Audit

- 保持现有 60 秒和 upload-size bound；Phase 2 暂不引入 long-dictation segmentation。
- 初始 transport frame 为 100 ms；model chunk 从 1.0 秒开始，只有 measured accuracy/latency evidence
  才能改变。
- Connect/ready 上限五秒；final flush 初始上限十二秒，并包含在 Gateway operation deadline 内；
  idle/abandoned session 会过期。
- Realtime 与 batch 共享 configured concurrency 和 pending budget。
- Raw PCM/WAV 与 transcript text 不得进入 Store、artifact、audit、trace、access log 或 server log。
- Audit 只记录 request/session identity、transport outcome、stop reason、duration、frame/revision count、
  first-partial/final latency、fallback code、model 与 bounded error code。

`GET /api/speech/status` 保留 backward-compatible `supports_streaming` flag，但只有 native realtime
runtime 通过 readiness 才设为 true。Structured realtime projection 还可暴露 protocol version、固定
format、frame duration 与 limit；不得暴露 internal endpoint。WebChat 绝不根据 model name 猜测 capability。

## Phase 2 Qualification 与 Acceptance

实现拆成带证据的 gate：

1. **Runtime qualification**：构建 pinned ASR runtime image；证明一个
   `Qwen/Qwen3-ASR-0.6B` instance 同时服务 native streaming 与 batch；与当前 service 对比 batch
   output；测量 partial quality/latency、GPU memory、cold start 和 60 秒增长。只有记录这些证据后，
   才考虑其他模型。
2. **Transport**：增加 ticket bootstrap、WebSocket proxy、sequence/queue bound、partial/final
   normalization、cancellation 和 batch fallback。
3. **WebChat reconciliation**：增加 stateful resampling、partial preview、final insertion、stale-anchor
   behavior，以及 loss/reconnect test。
4. **Silence stop**：只有 recorded quiet、noisy、soft-speech、long-pause、keyboard 和 mixed-language
   fixture 通过后，才增加 browser-local setting 与 pure detector。

Release evidence 必须证明：

- Desktop 与 mobile fake-microphone E2E 中，带 timestamp 的 partial 在 manual/automatic stop 前可见；
- 健康 realtime recording 对 `/v1/audio/transcriptions` 的调用数为零，且不上传 WAV slice；
- partial revision 可以替换文本，而 final 对 draft 恰好 apply 一次；
- Capture 开始后的每种 transport failure 都会关闭 capture boundary，不接受之后的 microphone sample，
  并自动且只提交一份包含该 boundary 前全部 PCM 的完整 WAV；
- Capture 前 realtime failure 会启动一个明确标记的 batch-only recording；若 batch ASR 不可用则不
  启动任何 recording；
- 没有 frame 被静默 drop、reorder、duplicate 或接受到 configured bound 之外；
- silence mode 不会在 confirmed speech 前 stop，能跨 short pause 恢复，并在 unsupported/noisy case
  中保持 manual；
- 首次使用时 silence auto-stop 为 Off，只有 owner 显式选择 Standard 或 Patient 后才生效；
- cancellation、session switch、unmount、device loss 与 Gateway shutdown 会释放 browser、Gateway 和
  ASR runtime state；
- persistence、artifact、audit 或 log 中都没有 audio 或 transcript。

在部署的 0.6B model 上，初始 latency target 是 first partial 在首个 1.0 秒 model chunk 加 1.0 秒
inference 内到达，后续 visible revision gap 的 p95 小于 1.5 秒，final 的 p95 小于 2.0 秒。这些是
release target，不是当前能力声明；runtime qualification 会记录 baseline，满足目标，或以证据更新
product decision。
