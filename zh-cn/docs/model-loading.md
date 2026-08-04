# SparkClaw 模型加载方案

> 语言： [English](../../docs/model-loading.md) | 简体中文

本文记录 SparkClaw 在 DGX Spark 级硬件上的模型加载策略。它补充 [模型基线](../benchmarks/model_baseline.md) 中的实测证据，以及 [部署文档](deployment.md) 中的运行步骤。

简短结论：当前单机产品 runtime 只加载响应快的 `fast` MoE chat 模型，并常驻 embedding 与 guard。逻辑 Deep Workflow profile 暂时别名到 Fast endpoint，因此不会启动 Deep 模型进程。历史 Deep 与双常驻实测继续保留给后续评估，但不再是当前启动策略。

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

快捷方式会先停止此前运行的 Deep 容器，再只启动 Fast、embedding 和 guard。随后运行
`scripts/restart_runtime_compose.sh`；该脚本默认使用同一个单 Fast 环境。当前 Fast
容量仍保持在已实际运行过的 32K context 与 8 GiB KV cache，不把 Deep 释放的内存
直接当作未经测量的容量提升。模型启动会等待 Docker health；Guard health 在每次
容器启动时包含一次有界的真实 chat completion，因此第一条用户审核请求不会再承担
serving runtime 的懒初始化开销。

OvisOCR2 是可选 document adapter，不是第五个 Model Router lane，因此 `single-fast` 产品
profile 默认不加载它。需要 OCR 时，`scripts/serve_models_compose.sh single-fast-with-ocr`
通过 `docker/compose.ocr.yaml` 在端口 `8007` 增加 `ATH-MaaS/OvisOCR2`。该 overlay 固定使用
模型文档要求的 vLLM `0.22.1`，关闭 thinking、使用确定性生成，并由 Gateway 限制响应、并发
和队列，同时给 OCR 分配固定 2 GiB KV cache。在 GB10 上，只有先停止已常驻的模型服务，
再一起加载 Fast、embedding、guard 和 OCR，组合启动才验证成功；直接向已常驻栈增加 OCR
会在 CUDA 初始化阶段失败。OvisOCR2 随后成功加载 1.72 GiB 权重，但仅设置
`gpu-memory-utilization=0.12` 会算出 -1.96 GiB 可用 KV cache，因此显式 2 GiB KV cache
是该 profile 的必需配置，不是可选调优。设置后 vLLM 报告初始空闲 53.26 GiB、2 GiB cache
可容纳 164,352 tokens，预计 32K 并发为 5.02x。这验证了组合启动，不代表已经建立稳定常驻
预算或 OCR 质量基线。一次并发图片与扫描 PDF 冒烟调用已成功，但宣称模型质量前仍需更广泛
的质量测量。

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
- 评估 OCR overlay 时记录 OCR 启动、单页延迟、扫描 PDF 恢复，以及 malformed/incomplete
  Markdown 行为。
- Golden eval 结果和 regression notes。

长期测量结果追加到 [模型基线](../benchmarks/model_baseline.md)。本文档只维护策略和被接受的加载方案。
