# SparkClaw 模型加载方案

> 语言： [English](../../docs/model-loading.md) | 简体中文

本文记录 SparkClaw 在 DGX Spark 级硬件上的模型加载策略。它补充 [模型基线](../benchmarks/model_baseline.md) 中的实测证据，以及 [部署文档](deployment.md) 中的运行步骤。

简短结论：当前单机产品 runtime 会同时加载响应快的 `fast` MoE chat 模型、embedding、guard、Qwen3-ASR 语音转写与 OvisOCR2 文档适配器。逻辑 Deep Workflow profile 暂时别名到 Fast endpoint，因此不会启动 Deep 模型进程。历史 Deep 与双常驻实测继续保留给后续评估，但不再是当前启动策略。

## 当前基线

已验证的 GB10 运行结果是：一次只加载一个 chat lane，128K context，vLLM，FP8 Qwen 模型，并且 MTP 设为 2 个 speculative tokens。

| Lane | Model | Context | Model memory | KV cache | Model + KV | 实测表现 |
|---|---|---:|---:|---:|---:|---|
| `fast` | `Qwen/Qwen3.6-35B-A3B-FP8` | 131072 | 34.18 GiB | 59.55 GiB | 93.73 GiB | warm request 约 59-70 tok/s |
| `deep` | `Qwen/Qwen3.6-27B-FP8` | 131072 | 28.08 GiB | 63.98 GiB | 92.06 GiB | 约 13-18 tok/s |

重点是：full profile 的常驻消耗主要来自 KV cache reservation，而不只是模型权重。按当前实测 full settings，`fast + deep` 合计约 185.79 GiB，还没有算其他运行时开销，所以不能把单台 128 GB unified-memory 机器上的 full-context dual residency 当成可行默认方案。

之前 dual-residency 尝试看起来只差一点，是因为失败发生在一次增量分配上：`deep` 已加载后机器只剩 12.08 GiB，`fast` 启动路径又请求 12.16 GiB。这个 12.16 GiB 不是 `fast` 的完整成本，只是当时失败的那一段分配。

## 单机策略

单机默认使用 `single-fast-v1` profile：

- 所有 Workflow 模型调用只运行一个 Fast chat endpoint。
- trace 保留逻辑 fast/deep profile 选择，但两个 profiles 都通过 `SPARKCLAW_DEEP_BASE_URL=http://sparkclaw-fast:8001/v1` 与 `SPARKCLAW_DEEP_MODEL=sparkclaw-fast` 指向 Fast。
- 产品启动路径不启动 `sparkclaw-deep` 容器。
- embedding 和专用 guard 保持小型并在产品 profile 中常驻。Embedding 构建 semantic
  routing index 并为请求评分；guard 在 routing 或 tool execution 前审核 owner prompt。
- Qwen3-ASR 作为有界语音转写 adapter 常驻。它不是 Model Router lane，只接受 Gateway
  已校验的音频请求。
- OvisOCR2 作为文档适配器保持常驻。它不是 Model Router lane，只为选中的文档图片提供
  有界且不可信的文本证据。

单机阶段的性能项保持保守：

- 轻量双常驻实验先关闭 MTP。
- 单机方案不依赖 DFlash 或类似 attention/runtime 加速。
- 加速项放在 residency 稳定之后再评估。
- 每次修改 context、KV budget、MTP、serving image 或 model checkpoint 后，都重新跑 endpoint benchmark 和 golden eval。

## 当前单 Fast Profile

当前 profile 实现为 `dgx-spark-single-fast-v1`：

- 环境变量：`docker/env/sparkclaw.single-fast.env`
- Compose 资源 override：`docker/compose.dual-light.yaml`
- Profile 元数据：`configs/model.profiles.json`
- 启动快捷方式：`scripts/serve_models_compose.sh single-fast`

快捷方式会先停止此前运行的 Deep 容器，再通过一次 Compose 操作同时启动 Fast、embedding、
guard、ASR 和 OCR。随后运行 `scripts/restart_runtime_compose.sh`；该脚本默认使用单 Fast、ASR
与 OCR 环境。修改目标组之前，启动脚本会验证每个容器存在、running、healthy，并使用当前 Compose
configuration hash。healthy/current 模型组会被保留；任一成员缺失、停止、不健康或配置漂移
时，完整目标组会先停止再 force-recreate。设置 `SPARKCLAW_FORCE_MODEL_RECREATE=true` 可对
健康模型组执行相同的完整刷新。产品启动或故障恢复不要直接使用 `docker start` 或
`docker restart`。

模型 checkpoint 与 Hugging Face 元数据继续持久化在 `data/models`。GPU 进程缓存与其分离：
vLLM/TorchInductor AOT 产物、Triton kernel、FlashInfer cache 与 NVIDIA runtime 注入都
留在可丢弃的容器实例中。整体重建会清除这些进程内缓存并刷新 GPU device 注入，但不会重新
下载 checkpoint。当前 Fast
容量仍保持在已实际运行过的 32K context 与 8 GiB KV cache，不把 Deep 释放的内存
直接当作未经测量的容量提升。模型启动会等待 Docker health。Fast health 会为每个模型进程
执行一次贴近生产负载的 chat completion：当前合成输入在 Qwen3.6 上约为 3.4K token，并强制
解码 480 token，在接收用户流量前覆盖 Tree routing 的冷路径。Guard health 保留较小的有界
chat completion。readiness helper 被复制进配置的 vLLM 镜像派生出的本地镜像，因此
healthcheck 不依赖 checkout source file 的 bind mount。两个 probe 都把完成 marker 存放在
专用的容器本地 tmpfs，并绑定到模型、预热形状和当前服务进程启动时刻。成功 warmup 后 marker
持久化是 best-effort；tmpfs 不可写时 readiness 仍成功，但后续 probe 可能再次 warmup。只有
准确进程完成预热且 marker 成功保存后，周期检查才改用轻量模型列表。Embedding 保持固定
2 GiB KV budget，但允许最多 128 条短 sequence，使 110 项语义语料能够通过一次启动请求在
20 秒索引时限内完成 embedding。

Qwen3-ASR 是 speech adapter，不是 Model Router lane。`single-fast` 产品 profile 通过
`docker/compose.asr.yaml` 在端口 `8006` 加载 `Qwen/Qwen3-ASR-0.6B`；派生 vLLM 镜像补充
有界音频依赖，模型使用共享 Hugging Face cache。服务分配显式 2 GiB KV cache：
五服务冷启动的 encoder profiling 后，仅依赖 utilization 的分配算出
`-10.24 GiB` 可用 cache。Gateway 通过匹配的 ASR 环境启用 OpenAI-compatible
transcription adapter。使用固定 cache 后，vLLM 报告初始空闲 44.55 GiB、
18,720 cached tokens，8K 下预计并发 2.29x；整个五服务 force-recreate 中
ASR 在 92 秒后 healthy，1 秒 WAV 转写冒烟请求也成功完成。

OvisOCR2 同样是 document adapter，而不是 Model Router lane。`single-fast` 产品 profile
会通过 `docker/compose.ocr.yaml` 在端口 `8007` 将 `ATH-MaaS/OvisOCR2` 与 Fast、embedding、
guard、ASR 一起加载；旧的 `single-fast-with-ocr` 命令保留为同一五服务启动的兼容别名。该 overlay 固定使用
模型文档要求的 vLLM `0.22.1`，关闭 thinking、使用确定性生成，并由 Gateway 限制响应、并发
和队列，同时给 OCR 分配固定 2 GiB KV cache。在 GB10 上，只有先停止已常驻的模型服务，
再一起加载 Fast、embedding、guard 和 OCR，组合启动才验证成功；当前产品启动在该原子组中
继续加入 ASR。直接向已常驻栈增加 OCR
会在 CUDA 初始化阶段失败。OvisOCR2 随后成功加载 1.72 GiB 权重，但仅设置
`gpu-memory-utilization=0.12` 会算出 -1.96 GiB 可用 KV cache，因此显式 2 GiB KV cache
是该 profile 的必需配置，不是可选调优。设置后 vLLM 报告初始空闲 53.26 GiB、2 GiB cache
可容纳 164,352 tokens，预计 32K 并发为 5.02x。这验证了组合启动，不代表已经建立稳定常驻
预算或 OCR 质量基线。一次并发图片与扫描 PDF 冒烟调用已成功，但宣称模型质量前仍需更广泛
的质量测量。

## IMMS E2 证据复刻面边界

ProjectGroup-2 决策 0026 已由 InfiniCenter commit `42bc8a4` 接受。SparkClaw 同时接受
IMMS ADR 0019 proposal `ac9a33d3c55f3c9d55af21e91586902f530aa39f`，exact document 为
`c073202cff039dee23211ec6785464ef093d13992cfeee16840547cfa7001165/10006`，但只限于
以下精确范围：

- E2 是 evidence-only 复刻 profile，不属于 `single-fast-v1`，也不是生产个人数据处理方。
  公网路由只允许合成评测输入；真实 Source/Memory 内容必须保留在未来 GB10 本地
  loopback 服务内。
- 唯一接受目标是
  `Qwen/Qwen3-Reranker-4B@22e683669bc0f0bd69640a1354a6d0aebcfeede5`，served name
  为 `imms-qwen3-reranker-4b-e2`，使用 BF16、无量化、tensor parallel 1、
  max model length 8192、max sequences 1、seed 0、eager execution，并关闭 prefix
  caching、speculative decoding 与 LoRA。ADR 0019 获 central accepted 前仍不得部署。
- vLLM completion protocol 无需 scoring adapter 即能表示 ADR 0019：`/v1/completions`
  接受 direct integer token-ID array，并支持要求的 `allowed_token_ids`、
  `logprob_token_ids`、`add_special_tokens`、`truncate_prompt_tokens` 和 token-ID response
  controls。未来 pinned image/version 必须在任何 calibration request 前以 content-free
  deployment smoke transcript 证明这些字段；只返文本 label、服务端重新分词、fallback 或 retry
  都不是可接受替代。
- E2 run 只有在绑定 immutable Hugging Face model revision、immutable vLLM image digest
  及 reported version、完整 serving config 和 deployment revision 时才可 admission。run 前后
  `/models` 的可变名称相同只能发现 run 中漂移，不能单独成为 immutable pin。
- 本仓管理的模型变更由 operator 发起。对已进入 accepted IMMS evidence scope 的端点，
  SparkClaw 会在主动修改模型、量化、serving image 或评分语义前，通过 ProjectGroup-2
  status/inbox 通知 IMMS。若独立托管路由是自动或未知更新机制，在它提供可解析
  immutable deployment revision 之前不具备 E2 admission 资格。
- reranker lane 已于 2026-07-24 从当前 product profile 移除。因此，托管 reranker 路由或
  proposed 4B evidence profile 都不得被表述为当前 `single-fast-v1` 依赖。它与未来
  E3 GB10 profile 的差异当前未知，E3 admission 前必须实测，或明确保留为未知。

本评审本身不产生部署，也不产生 SLA、可用性、专用配额、Gateway wire 或 runtime integration
义务。中断只会推迟新 evidence run，不会使已封存证据失效。

## 历史轻量双常驻实验

如果单台 DGX Spark 需要两个 chat lanes 同时常驻，实验应从 reduced residency profiles 开始，而不是从 full 128K/MTP profiles 开始。

建议第一轮目标：

| Setting | `fast` | `deep` | embedding | guard | 取舍原因 |
|---|---:|---:|---:|---:|---|
| Context | 32768 | 65536 | 8192 | 16384 | 优先保 deep context；辅助模型保留适合有界输入的 context。 |
| MTP | off | off | off | off | 先节省内存和复杂度，验证常驻可行性。 |
| KV cache budget | 8 GiB | 12 GiB | 2 GiB | 2 GiB | 直接限制真正的压力点，而不是让每个 server 按 full lane 预留。 |
| Max response tokens | 768 | 1536 | n/a | 128 | 保持 agent loop 和审核响应速度。 |
| Max concurrent sequences | 4 | 2 | 1 | 1 | 产品是单用户使用，优先 fit 和 latency，不追求并发。 |
| GPU memory utilization | 0.42 | 0.36 | 0.06 | 0.04 | 显式 KV budget 约束容量；utilization 保持保守的启动检查余量。 |

这个 profile 已实现为 `dgx-spark-dual-light-v1`：

- Compose：`docker/compose.dual-light.yaml`
- 环境变量：`docker/env/sparkclaw.dual-light.env`
- Profile 元数据：`configs/model.profiles.json`
- 启动快捷方式：`scripts/serve_models_compose.sh dual-light`

更推荐的取舍是 `deep` priority：

- `deep` 保留能稳定运行的最大 context。
- `fast` 降到 32K 或 64K。
- MTP 和 DFlash 类优化都先关闭。
- 升级为推荐 profile 前，必须测 startup、idle residency、warm request、long-context request 和当前 43-case golden eval。

这样 SparkClaw 仍然有一个响应快的本地 `fast` lane，同时保住 `deep` 最重要的价值：在更大的证据窗口里处理复杂推理。

历史 `dual-light-v1` 实验说明两个 chat lanes 加小型辅助端点可以在单机常驻。
Warm `fast` 约 48-50 tok/s，warm `deep` 约 7.3 tok/s；chat-only 对照中 `deep`
吞吐基本不变。当前产品 profile 常驻 embedding 和 guard：embedding 8K + 2G KV
的 warm request 约 28.5 ms；Qwen3Guard 16K + 2G KV 的模型权重占用 1.12 GiB，
warm moderation request 约 80-110 ms。Guard 32K 因需要 3.5 GiB KV 被 vLLM 拒绝。

这个单用户 profile 的验收标准是综合任务表现，而不是并发。2026-05-25 的历史模型栈
通过了当时的 58-case real-model golden eval。移除 reranking lane 改变了路由模型栈，
因此当前 Fast + Embedding + Guard profile 仍需重新运行活动的 43-case real-model
matrix，才能与历史结果进行质量比较。

### 轻量双常驻测试循环

每轮调参使用这个循环：

```bash
scripts/serve_models_compose.sh dual-light

curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8005/v1/models

set -a
. docker/env/sparkclaw.dual-light.env
set +a
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_EMBEDDING_BASE_URL=http://127.0.0.1:8003/v1 \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md

SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_EMBEDDING_BASE_URL=http://127.0.0.1:8003/v1 \
python3 scripts/record_model_loading.py --profile dual-light-v1
```

只在 chat-only 对照实验中使用 `scripts/serve_models_compose.sh dual-light-chat`。
历史 `dual-light` profile 包含 fast、deep、embedding 和专用 guard；它不再是产品
默认启动项。

失败的 profile 也要记录。启动失败加上 logs 和 GPU process state，本身就是有价值的证据。

## 取舍矩阵

| 选择 | 收益 | 代价 | 适用场景 |
|---|---|---|---|
| 单 Fast + profile aliasing | 只需一个 chat 模型进程，常驻可预测 | 暂无 Deep 专项质量 | 当前单机产品模式 |
| 一次一个 full lane | 最稳定，保留完整 128K context | 需要切 lane | 定向模型评估 |
| 轻量双常驻 | 两个 lanes 都是 warm | context、并发降低，且先不开 MTP/DFlash | 单机演示或必须快速切 lane 的工作流 |
| `deep` priority | 高风险/代码审查质量更稳 | `fast` 变成短上下文助手 | 单机上的最佳 SparkClaw 取舍 |
| `fast` priority | 日常 chat 响应更强 | `deep` 失去长上下文优势 | UI-heavy 或短轮次工作流 |
| 两台 DGX Spark | full `fast` 和 full `deep` 都可常驻 | 需要更多硬件和 endpoint 管理 | 长期推荐部署 |

## 双机方案

当有两台 DGX Spark 时，优先按 lane 拆服务，而不是一开始就做跨机器单模型分布式：

| 机器 | 服务 | 备注 |
|---|---|---|
| DGX Spark A | `fast`、embedding、guard | 优先优化交互延迟、semantic routing 和 prompt moderation。 |
| DGX Spark B | `deep` | 给长上下文和 repair/review 任务保留内存余量。 |

只有在这个拆分稳定后，再逐步打开性能项：

1. 先给 `fast` 开 MTP，然后 benchmark。
2. `deep` 只有在内存余量和输出质量都稳定时再开 MTP。
3. 两个 lanes 独立稳定后，再评估 DFlash 或类似 runtime acceleration。
4. 只有 eval 无回退时，才提高 context 或 response caps。
5. 只有未来模型单台 DGX Spark 放不下时，才考虑跨机器 tensor parallelism。

## 验证清单

每个新的 loading profile 都应记录：

- 硬件、driver、serving image 和 model checkpoint IDs。
- Context length、KV cache budget、GPU memory utilization、MTP/DFlash 状态。
- Startup success、idle memory、first-request latency、warmed-request latency。
- Chat、summary、email triage、coding 场景吞吐。
- 记录 embedding、guard 可用性和 Gateway semantic-router readiness。
- 记录 ASR 启动与转写延迟，以及 OCR 启动、单页延迟、扫描 PDF 恢复和
  malformed/incomplete Markdown 行为。
- Golden eval 结果和 regression notes。

长期测量结果追加到 [模型基线](../benchmarks/model_baseline.md)。本文档只维护策略和被接受的加载方案。
