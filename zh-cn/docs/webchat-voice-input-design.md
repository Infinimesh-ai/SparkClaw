# WebChat 语音输入闭环设计

> 语言：[English](../../docs/webchat-voice-input-design.md) | 简体中文

> 状态：Phase 1 已在当前 worktree 实现；Phase 2 为详细设计，更新于 2026-08-19。
> LLM 润色继续推迟，直到原生 realtime 语音闭环满足下述验收门槛。

## 决策摘要

第一目标是实现可靠、仅限 WebChat 的听写闭环：

```text
点击麦克风
  -> 获取选定的浏览器麦克风
  -> 显示音量和时长并录音
  -> 显式停止或取消
  -> 编码为有界 16 kHz mono PCM16 WAV
  -> 通过现有 speech adapter 转写
  -> 在不覆盖较新编辑的前提下插入当前 draft
  -> owner 检查/编辑并显式发送
```

Phase 2 在此闭环上增加 native record-time inference：WebChat 通过一个 authenticated stateful
session 持续发送 PCM，在 microphone 仍 active 时显示 revisioned partial snapshot，并在 manual
或 optional silence stop 后 reconcile 一个 final snapshot。完整 in-memory WAV 只作为 failure
fallback 保留，绝不拆成 pseudo-streaming request。

Phase 2 首轮 qualification 保持使用当前 `Qwen/Qwen3-ASR-0.6B` model，只改变暴露其 native
streaming state 所需的 serving runtime。Silence auto-stop 默认关闭。若 realtime 在 capture 开始后
失败，WebChat 会立即 stop/flush capture，自动 batch-transcribe failure boundary 前录到的全部 PCM，
然后结束 operation；中途失败后绝不继续录音。

LLM 润色不进入这条关键路径。只有录音、转写、重试、取消、资源释放和 draft 插入稳定后，
它才作为可选的转写后 draft 增强功能加入。

交互只发生在当前 WebChat composer 内。Phase 1 不增加 global hotkey、wake word、tray process、
background listener、system-wide push-to-talk mode 或跨应用 cursor insertion。Owner 通过可见的
麦克风控件开始和停止录音，SparkClaw 绝不自动发送结果文本。

现有实现已经覆盖大部分 happy path。主要稳定性缺口包括 microphone selection 与 fallback、
运行期 device-loss detection、无需重录的 transcription retry、过期 draft anchor 的显式处理、
聚焦的 state-machine test，以及覆盖 queue 与 ASR inference 的端到端 deadline。

## 参考范围

[OpenLess](https://github.com/Open-Less/openless/tree/9f360e20) 只作为行为参考，不作为依赖。
它对语音输入最有价值的经验是：

- 使用一个明确的 dictation state machine 和唯一 generation/session ID 拒绝迟到 callback；
- 区分 startup、recording、processing、cancellation 和 terminal state；
- 录音时显示 input level 与 elapsed time；
- 支持 preferred microphone，并在失效时 fallback 到 system default；
- 区分启动与运行期 capture error，包括 recorder liveness watchdog；
- cancellation 统一拥有 recorder 与 downstream request 的资源释放；
- silence auto-stop 只作为用户选择的可选录音模式；
- 通过 recovery path 避免丢失已经说出的内容。

相关 OpenLess 实现包括
[dictation state machine](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src-tauri/src/coordinator_state.rs)、
[recorder and device handling](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src-tauri/src/recorder.rs)、
[silence auto-stop](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src-tauri/src/coordinator/silence_auto_stop.rs)
和
[microphone selection UI](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src/pages/settings/MicrophoneSelect.tsx)。

SparkClaw 不应复制 OpenLess 的 Tauri/Rust runtime、global hotkey、accessibility permission、
clipboard fallback、任意应用 insertion、tray/capsule UI、audio archive、provider matrix 或持久化
dictation history。这些用于解决 system-wide input method 问题；SparkClaw 当前只需要一个位于
authenticated WebChat composer 内的有界 voice control。

## 范围决定

| 能力 | Phase 1 决定 | 原因 |
|---|---|---|
| Composer 麦克风按钮 | 保留并强化 | 唯一支持的 activation surface |
| 手工点击开始/点击停止 | 保留 | Desktop 与 mobile browser 中行为可预测 |
| 任一 active phase 的 Escape/cancel | 保留并测试 | owned cleanup 的必要条件 |
| 实时 input level 与 elapsed time | 保留并强化 | 确认 capture 正常且使用了预期设备 |
| Microphone selection 与 default fallback | 新增 | 多设备 desktop 和设备变化后的必要能力 |
| 同一录音的 transcription retry | 使用内存 buffer 新增 | 避免 transient ASR failure 导致整段口述丢失 |
| 最大时长自动停止 | 保留 | 维持现有 upload 与 inference bound |
| Silence/VAD 自动停止 | 暂缓；未来增加时默认关闭 | Threshold error 可能截断语音，属于易用性而非基础可靠性 |
| Streaming/partial ASR | 暂缓 | Batch transcription 更容易校验和恢复 |
| 持久化 voice/audio history | 不增加 | 最终发送消息已提供有效历史，audio privacy 成本过高 |
| LLM 润色与 style mode | 暂缓 | 是可选的转写后增强，不是 Phase 1 依赖 |
| Global hotkey、wake word、push-to-talk | 不在范围 | 用户只在 WebChat 内显式激活语音 |
| 跨应用 insertion 或 clipboard fallback | 不在范围 | WebChat 直接拥有 composer 与 draft state |

## 目标

1. 让一次录音 attempt 具有确定性 lifecycle，不出现卡住的 UI、泄漏的 microphone track、
   泄漏的 `AudioContext`，也不允许迟到 callback 影响新 attempt。
2. 在不持久化 audio 的前提下，跨 retryable network、capacity、timeout 和 ASR failure 保留
   已说出的内容。
3. 当捕获的 composer selection 仍有效时插入成功 transcript；当 transcription 期间 draft
   已被编辑时，绝不覆盖新文本。
4. 对 permission、device、recording、encoding、service readiness、busy、timeout 和
   empty-transcript failure 提供清晰状态与恢复操作。
5. 保持当前 privacy boundary：transcription 不创建 message、Agent run、Tool Call、approval
   或 artifact，Gateway 不保留 raw audio。
6. 保持 final send 显式，并原样保留发送的 owner text。

## 非目标

- Always-listening 或 wake-word 行为。
- Desktop-global shortcut，或在 WebChat 未激活时录音。
- Push-to-talk keyboard binding、earbud media-button trigger 或 OS-level microphone control。
- 替换当前 OpenAI-compatible speech provider contract，或增加大范围 ASR provider marketplace。
- 自动把 transcript 发送给 Agent。
- 自动重写、总结或解释 transcript。
- 把 audio 存入 local storage、IndexedDB、Gateway Store、artifact、trace 或 audit。
- 在 Phase 1 中把该 WebChat flow 应用于 Telegram 或微信 voice note。

## 当前基线与缺口

SparkClaw 已经提供：

- 通过 `AudioWorklet` 使用浏览器 `getUserMedia` capture；
- mono capture、level reporting、resampling 和 canonical 16 kHz PCM16 WAV；
- minimum duration、maximum duration 和 maximum upload check；
- 可见的 permission、recording、encoding、transcription、disabled 和 error state；
- 通过 operation generation 实现 Escape 与 session-switch cancellation；
- 有界 Gateway upload 与 OpenAI-compatible transcription adapter；
- 明确的 busy、timeout、unavailable、cancelled 和 inference error code；
- 有界 ASR concurrency 和 pending admission；
- 插入未发送的 per-session draft；
- `audio_retained: false` 的 metadata-only speech audit。

本设计处理以下缺口：

1. Capture 总是使用 browser default device；没有 preferred-device picker、live preview 或保存的
   client-local device choice。
2. Capture 没有把 `MediaStreamTrack.onended`、audio-context failure 或 first/last-sample
   liveness deadline 投影到 voice state。
3. Transcription 失败后 encoded WAV 会被丢弃，因此 retry 必须重新录音。
4. ASR 运行期间 composer draft 变化时，当前 insertion 会 fallback 为追加到末尾。它避免了
   overwrite，却可能在无提示的情况下把文字放错位置。
5. WAV 与 insertion helper 有 unit test，但完整 voice operation state 和 browser-resource cleanup
   path 缺少 focused test。
6. ASR HTTP timeout 本身没有定义从 Gateway admission 到 response 的完整 queue-plus-inference
   deadline。
7. Gateway 当前在每次 transcription 前执行 remote speech health check，并且只检查
   `session_id` 存在。最终契约应避免冗余 health prerequisite，并强制 authenticated
   owner/session scope。

## 产品体验

### 正常流程

1. Owner 把 caret 放在当前 composer 中，或选择一段文本，然后点击麦克风按钮。
2. WebChat 在需要时请求 permission，解析所选 device，并在 audio graph active 且 PCM callback
   已开始后才进入 recording。
3. Composer 显示 recording state、elapsed time、input-level meter 和 stop control。现有最大时长
   保持可见；达到上限时执行正常 stop，而不是报错。
4. 点击 stop 后 flush 最后的 worklet sample、关闭 microphone resource、编码 WAV 并开始转写。
5. 成功后，如果 draft snapshot 未变化，WebChat 在捕获的 selection 位置插入 transcript，并把
   caret 恢复到插入文本之后。
6. Transcript 保持为普通可编辑 draft。只有正常 send action 才创建 message 并启动 routing。

成功的 raw transcript 不需要额外 review modal。Owner 可以直接在 composer 中查看和编辑。

### 设备选择

取得 microphone permission 后，WebChat 可以调用 `enumerateDevices()`，并在麦克风旁显示紧凑的
device menu。所选 `deviceId` 与 origin 和 browser profile 绑定，因此应存入 client-local
preference，而不是 owner profile 或 Gateway Store。

打开 device menu 时，可以显示所选设备的 live level preview。该 preview 拥有单独的短生命周期
stream；menu 关闭时必须停止所有 track 并关闭 audio context。它不能与 active recording 并行。

开始录音时：

1. 已保存设备仍在枚举列表中时，优先尝试它。
2. 设备消失，或 `getUserMedia` 因 exact device unavailable 而失败时，只 fallback 一次到
   system default。
3. 显示 fallback 已发生；只有 capture 成功后才更新 effective selection。
4. Permission denied 或一般 security error 不允许自动 fallback。

### 无需重录的 Retry

录音停止后，encoded WAV 只保留在当前 browser operation 中。Retryable `speech_busy`、
`speech_timeout`、`speech_model_unavailable`、transient network failure 或 retryable upstream
failure 会显示**重试转写**。Retry 发送同一 WAV，不重新打开麦克风。

成功、显式 cancel/dismiss、session change、新 recording、component unmount 或短期过期时丢弃
retry buffer。初步建议过期时间为五分钟。它绝不写入 browser persistence。Non-retryable
invalid-audio 或 no-speech outcome 需要重新录音。

Phase 1 建议显式 retry，不自动 retry。这样 latency 和 ASR load 保持可见，服务故障期间不会
重复消耗资源。

### Draft 冲突恢复

每次 operation 捕获：

```text
operation_generation
session_id
draft_snapshot
selection_start
selection_end
```

Transcription 完成时：

- 同一 generation 与 session 仍 active，且 draft 与 snapshot 相同时，替换捕获的 selection；
- draft 已变化时，把 transcript 保存为 pending in-memory candidate，并提供**在光标处插入**和
  **关闭**操作；
- 绝不静默 append、replace 或 merge 到已经变化的 draft；
- session 已变化或 operation 已取消时，丢弃迟到 result。

这是 WebChat 对 OpenLess insertion fallback 的对应实现：已说出的文字不会丢失，但 SparkClaw
不需要 system clipboard，因为它直接拥有 destination composer。

### 错误恢复

| Failure | 用户可见恢复 |
|---|---|
| Permission denied | Draft 不变；browser permission 改变后允许 retry |
| 无设备 / 已保存设备移除 | 刷新 device list，并提供 system default |
| PCM 开始前 capture failed | 关闭全部 resource，回到可重新录音的 error state |
| 录音中设备断开 | 停止 capture，报告所选设备断开，并提供重新录音 |
| Recording too short | 丢弃 audio，并提供重新录音 |
| 达到 maximum duration | 正常 stop 并转写 |
| ASR busy、timeout、unavailable 或 transient network error | 在内存中保留 WAV，并提供 retry |
| Empty transcript / no speech | 丢弃 retry buffer，并提供重新录音 |
| Result 返回前 draft 已变化 | 把 transcript 保存为 pending insert candidate |
| Session switch 或 operation cancelled | Abort request、释放 resource、丢弃 operation-local data |

Error 应显示在 composer 附近，并直接提供操作；不使用 blocking modal，也不替换 global
application error surface。

## 状态与所有权模型

WebChat hook 应暴露显式 state machine，而不是从多个 boolean 推断行为：

```text
disabled
idle
  -> requesting_permission
  -> starting_capture
  -> recording
  -> encoding
  -> transcribing
  -> applied

transcribing -> retryable_error -> transcribing
active state -> cancelled -> idle
active state -> error -> idle or new recording
transcribing -> pending_insert when the draft anchor is stale
```

同一时刻只有一个 operation 可以拥有 microphone、timer、WAV、abort controller 和 pending
transcript state。开始新 generation 时，必须先 cancel 并清理旧 operation，再分配新 resource。
每个 asynchronous continuation 在改变 UI 或 draft state 前，都要检查 generation 与 `session_id`。

Stop 与 cancel 含义不同：

- **stop** 会 flush 已捕获 sample，并继续 transcription；
- **cancel** 会停止 track、丢弃 sample/WAV/pending transcript、中止 HTTP request，并在不改变
  draft 的情况下回到 idle。

Permission、capture startup、recording、encoding、transcription、retryable error 和 pending
insertion 期间均允许 cancel。如果不可逆的 draft insertion 已完成，cancel 不尝试撤销普通的
owner-editable text。

## Browser Capture 可靠性

`PCMInputCapture` 应统一拥有并报告完整 capture lifecycle：

- selected 与 effective device identity；
- first PCM callback deadline；
- 最近一次 PCM callback timestamp；
- `MediaStreamTrack` ended event；
- 导致 capture 不可用的 audio context state change；
- idempotent stop/cancel cleanup；
- 最终 flush 后的 sample set。

Liveness watchdog 检查 worklet 在 startup 后产生 callback，并在 track live 时持续产生 callback。
它检测 capture plumbing failure，不判断 owner 是否正在讲话。Phase 1 不应使用 RMS/VAD threshold
拒绝安静语音；在独立评测可选 VAD mode 之前，no-speech 继续由 ASR 判定。

所有 terminal path 必须停止每条 media track、disconnect audio node、清除 timer/listener、移除
worklet handler，并关闭 `AudioContext`。Cleanup 在重复调用或 startup 仍在进行时也必须安全。

## Gateway 与 Speech 契约

现有 public operation 已足够：

```text
GET  /api/speech/status
POST /api/speech/transcriptions
```

基础闭环不需要为 multipart request 和成功 response 增加新版本。`request_id` 继续关联一段
captured-audio operation；transcription 在 SparkClaw 侧没有 effect，因此 retry 可以复用它。

Gateway 强化包括：

1. 验证 referenced session 属于 authenticated owner，而不只是检查它存在。
2. 在 admission 前应用一个 end-to-end deadline，确保 pending wait 与 inference 总和不超过
   configured speech budget。
3. Pending capacity 已满时，继续立即返回有界、retryable `speech_busy`。
4. 把实际 transcription request 作为 readiness authority。不要在每个合法 transcription 前
   强制第二次 remote `/health` request；health 结果只用于缓存/status/readiness projection。
5. 保持 response body、upload、duration、content type、language、redirect、allowlist、
   concurrency 与 pending bound 不变。
6. Audit 继续只记录 metadata，并保持 transcription 不创建 message、run、tool call、approval
   或 artifact 的不变量。

Gateway 仍是唯一 browser-facing ASR boundary。WebChat 不直接调用 model endpoint。

## 隐私与持久化

- Gateway 与 ASR adapter 在 request 结束后不保留 audio。
- WebChat 只可在 active retry window 的内存中保留 encoded WAV。
- Audio 绝不存入 `localStorage`、IndexedDB、Service Worker cache、Store、audit、trace、artifact
  或 message attachment。
- Raw transcript 只插入 client draft；只有 owner 发送普通消息时才被持久化。
- Speech audit 包含 request ID、duration、bytes、language、model、queue/inference timing、outcome
  和有界 error code，但不含 audio 或 transcript。
- Device preference 只保存当前 client 所需的 browser device ID/label，不同步为 owner memory。

## 可观测性

可靠性应按 stage 度量，而不是只看一个 aggregate success flag：

- permission 与 capture-start outcome；
- time to first PCM callback；
- recording duration 与 stop/cancel cause；
- encode duration 与 WAV bytes；
- Gateway admission wait、ASR inference 和完整 stop-to-draft latency；
- retry count 与 final result；
- stale-anchor pending insertion；
- device fallback 或 runtime device loss；
- 测试中的 resource-cleanup completion。

不需要外部 telemetry service。Server audit 继续只记录 metadata；client-only capture diagnostic
可出现在 development log 中，但不得包含 audio sample、device ID 或 transcript content。

## 验证矩阵

### Browser 与 State Test

- First-use permission grant、denial、revocation，以及 permission prompt pending 时 cancel。
- Default microphone、saved microphone、saved device removed、fallback success/failure 和
  录音中 device disconnect。
- First PCM timeout、callback liveness failure、worklet load failure、audio context failure，
  以及每种 failure 后的 idempotent cleanup。
- Very quick stop、normal stop、maximum-duration stop、重复 stop/cancel 和 final worklet flush。
- Startup、recording、encoding、transcription、retryable error、pending insertion、session switch
  与 unmount 期间 cancel。
- Busy、timeout、unavailable、network failure、invalid JSON、empty transcript 和成功的 same-WAV retry。
- Unchanged draft insertion、selected-text replacement、changed-draft pending candidate、dismiss
  与显式 insert-at-cursor。
- 迟到 operation 不得改变新的 session、state 或 draft。

State transition logic 应提取成 pure reducer 或等价的确定性 owner，避免这些 case 只依赖
component timing test。

### Gateway 与 Adapter Test

- Owner/session authorization，且不能跨 owner 使用 session。
- Canonical WAV、duration、upload、language、request ID 和 unexpected-file rejection。
- End-to-end deadline 包含 admission wait。
- Full queue 返回 retryable busy，且不启动 inference。
- Cancellation 能到达 pending wait 或 outbound request。
- Redirect 继续被拒绝，upstream body 继续有界。
- Audit 与 error 不含 transcript 或 audio。
- Success 与所有 failure 均不创建 message、Agent run、Tool Call、approval 或 artifact。

### Live Acceptance

- Desktop Chromium 与 mobile viewport，使用真实或确定性 fake microphone。
- 至少两个 physical input device，包括拔掉所选设备。
- 使用 deployed ASR 验证中文、英文和中英混合 technical dictation。
- 重复 start、stop、cancel、retry 和 session-switch，不出现长期 browser microphone indicator
  或卡住的 WebChat state。
- Stop-to-draft latency 与 recording duration 分开度量；只有记录 deployed ASR baseline 后才设置
  初始 SLO。

Phase 1 验收要求：

- 不自动发送；
- 完整 test matrix 中不覆盖 changed draft；
- transcription retry 复用 byte-identical WAV；
- audio 不在 in-memory retry window 之外持久化；
- 每个 terminal path 都关闭全部 media track 与 audio context；
- 重复 lifecycle test 中没有 stuck state 或 late-generation mutation。

## Phase 2 详细设计

规范性的 runtime、transport、partial/final reconciliation、silence-stop、fallback 与
acceptance contract 详见
[WebChat 语音 Phase 2：原生 Realtime ASR 与静音结束](webchat-voice-phase2-design.md)。
它要求 microphone 仍 active 时产生 model partial text，
并明确拒绝把 completed-file response streaming 称为 realtime。

## 交付阶段

### Phase 0：Baseline 与状态契约

- 记录当前 desktop/mobile 与 deployed-ASR 行为。
- 提取并测试显式 voice-operation state transition contract。
- 增加 deterministic browser-media fake，覆盖 capture、device、failure 与 cleanup test。
- 测量 stop-to-draft latency 和当前 failure category。

### Phase 1：稳定的 WebChat 语音输入

- 增加 microphone selection、preview 与 system-default fallback。
- 增加 first-sample 与 runtime capture failure reporting。
- 增加 in-memory WAV retry window 和 retry UI。
- 用 pending insert candidate 替代 stale draft 上的 silent append。
- 在 Gateway 强制 owner/session scope 与统一 queue-plus-inference deadline。
- 在保留 status readiness 的同时，删除每次 transcription 前的冗余 health prerequisite。
- 完成 focused state、resource、API、desktop 与 mobile verification。

### Phase 2：原生 Realtime 语音输入

- Qualification 并替换当前 ASR serving process，使用一个 pinned runtime 暴露 Qwen native
  stateful streaming API，同时保留 batch。
- 通过 authenticated、bounded WebSocket stream continuous canonical PCM，并在录音期间显示
  revisioned partial snapshot。
- 把一个 final snapshot 与现有 draft anchor reconcile；完整 in-memory WAV 只作为明确 failure
  fallback。中途失败会立即停止 capture，并自动 batch-transcribe 该 boundary 前的 audio；不会把
  active operation 降级为继续录音。
- 增加 browser-local、default-off silence auto-stop，使用 deterministic one-shot semantics，
  且不 gate audio。
- 保持 60 秒 bound；longer dictation segmentation 继续暂缓。

### Phase 3：丰富语音功能

- 把可选 LLM 润色、固定 style、原文/润色稿对照和 raw fallback 作为独立的转写后层加入。
- 在不保留完整 dictation history 的前提下，评估 owner-confirmed terminology correction 与
  provider-supported ASR hotword。
- Enhancement 被禁用或失败时，raw transcription 始终可用。

每个 implementation phase 稳定后，把长期行为合并到[架构](architecture.md)、
[外部集成](integrations.md)和 [WebChat](webchat.md)。只有全部后续 phase 都已完成，或其剩余
decision 与 acceptance criteria 已显式迁移后，才能删除本设计稿；完成单个 phase 不构成删除理由。

## 待讨论问题

| 问题 | 初步建议 | 影响 |
|---|---|---|
| Device selector 位置 | Composer microphone 旁的紧凑 menu | 无需离开当前任务即可确认或切换输入 |
| Device preference scope | 仅 browser-local | 避免通过 Gateway 同步 origin-specific device ID |
| Device loss fallback | 下一次 recording fallback，不在录音中切换 | 避免合并时间语义不明确的 audio stream |
| Retry audio expiry | 五分钟，仅内存 | 足以恢复 transient failure，又不会变成 history |
| Retry policy | Owner 显式 retry | ASR load 可预测，failure 可见 |
| Changed draft result | Pending insert candidate | 口述内容可恢复，同时不 overwrite 或 silent append |
| Silence auto-stop | Browser-local Off / Standard / Patient；初始为 Off | False-stop risk 由 owner 控制，且不能阻塞 manual recording |
| Streaming ASR | 通过 bounded WebSocket 使用 Qwen native state | Partial 在 microphone 仍 active 时产生；batch 只用于 failure fallback |
| 录音中 realtime failure | 停止 capture，并自动 batch 累计的完整 WAV | 保留 failure boundary 前的语音，且不静默继续录音 |
| LLM polishing | Phase 3 可选增强 | 没有 chat model 时语音输入仍可靠可用 |
| Activation scope | 仅可见 WebChat microphone | 不增加 global listener、wake word 或 background capture |
