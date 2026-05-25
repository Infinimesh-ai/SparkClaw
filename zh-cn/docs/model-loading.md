# SparkClaw 模型加载方案

> 语言： [English](../../docs/model-loading.md) | 简体中文

本文记录 SparkClaw 在 DGX Spark 级硬件上的模型加载策略。它补充 [模型基线](../benchmarks/model_baseline.md) 中的实测证据，以及 [部署文档](deployment.md) 中的运行步骤。

简短结论：单机 SparkClaw 优先追求稳定常驻、可预测的 agent 行为和产品综合体验，不优先追求最大解码速度。`fast` 是响应快的 MoE lane；`deep` 是稠密模型 lane，解码更慢属于预期内。Deep 的判断标准是任务稳定性、推理质量和 eval 表现，而不是追平 fast lane 吞吐。MTP 和 DFlash 这类加速项先留到双 DGX Spark 方案里再打开。

## 当前基线

已验证的 GB10 运行结果是：一次只加载一个 chat lane，128K context，vLLM，FP8 Qwen 模型，并且 MTP 设为 2 个 speculative tokens。

| Lane | Model | Context | Model memory | KV cache | Model + KV | 实测表现 |
|---|---|---:|---:|---:|---:|---|
| `fast` | `Qwen/Qwen3.6-35B-A3B-FP8` | 131072 | 34.18 GiB | 59.55 GiB | 93.73 GiB | warm request 约 59-70 tok/s |
| `deep` | `Qwen/Qwen3.6-27B-FP8` | 131072 | 28.08 GiB | 63.98 GiB | 92.06 GiB | 约 13-18 tok/s |

重点是：full profile 的常驻消耗主要来自 KV cache reservation，而不只是模型权重。按当前实测 full settings，`fast + deep` 合计约 185.79 GiB，还没有算其他运行时开销，所以不能把单台 128 GB unified-memory 机器上的 full-context dual residency 当成可行默认方案。

之前 dual-residency 尝试看起来只差一点，是因为失败发生在一次增量分配上：`deep` 已加载后机器只剩 12.08 GiB，`fast` 启动路径又请求 12.16 GiB。这个 12.16 GiB 不是 `fast` 的完整成本，只是当时失败的那一段分配。

## 单机策略

单机默认应一次运行一个 full chat lane：

- 日常交互、草稿、搜索归纳、轻规划，运行 `fast` full profile。
- 代码、高风险审查、repair verification、terminal 相关任务、显式 deep 请求，运行 `deep` full profile。
- 把 `deep` 的较低吞吐视为稠密模型的预期成本。`deep` 优化方向是稳定性和答案质量；`fast` 负责交互响应和短轮次体验。
- eval 或受限运行时，必要情况下可以把 Gateway 的两个 profiles 都临时路由到已加载 lane。
- embedding 和 reranker 保持小而可选；只有 retrieval workflow 需要 live endpoints 时才加载。

单机阶段的性能项保持保守：

- 轻量双常驻实验先关闭 MTP。
- 单机方案不依赖 DFlash 或类似 attention/runtime 加速。
- 加速项放在 residency 稳定之后再评估。
- 每次修改 context、KV budget、MTP、serving image 或 model checkpoint 后，都重新跑 endpoint benchmark 和 golden eval。

## 轻量双常驻实验

如果单台 DGX Spark 需要两个 chat lanes 同时常驻，实验应从 reduced residency profiles 开始，而不是从 full 128K/MTP profiles 开始。

建议第一轮目标：

| Setting | `fast` | `deep` | embedding | reranker | 取舍原因 |
|---|---:|---:|---:|---:|---|
| Context | 32768 | 65536 | 8192 | 2048 | 优先保 deep context；辅助模型保留单用户 retrieval 够用的 context。 |
| MTP | off | off | off | off | 先节省内存和复杂度，验证常驻可行性。 |
| KV cache budget | 8 GiB | 12 GiB | 2 GiB | 1 GiB | 直接限制真正的压力点，而不是让每个 server 按 full lane 预留。 |
| Max response tokens | 768 | 1536 | n/a | n/a | 保持 agent loop 响应速度和 eval 稳定。 |
| Max concurrent sequences | 4 | 2 | 1 | 1 | 产品是单用户使用，优先 fit 和 latency，不追求并发。 |
| GPU memory utilization | 0.42 | 0.44 | 0.06 | 0.06 | 真正的约束来自 `--kv-cache-memory-bytes`；utilization 只保持保守的启动检查余量。 |

这个 profile 已实现为 `dgx-spark-dual-light-v1`：

- Compose：`docker/compose.dual-light.yaml`
- 环境变量：`docker/env/sparkclaw.dual-light.env`
- Profile 元数据：`configs/model.profiles.json`
- 启动快捷方式：`scripts/serve_models_compose.sh dual-light`

更推荐的取舍是 `deep` priority：

- `deep` 保留能稳定运行的最大 context。
- `fast` 降到 32K 或 64K。
- MTP 和 DFlash 类优化都先关闭。
- 升级为推荐 profile 前，必须测 startup、idle residency、warm request、long-context request 和 58-case golden eval。

这样 SparkClaw 仍然有一个响应快的本地 `fast` lane，同时保住 `deep` 最重要的价值：在更大的证据窗口里处理复杂推理。

第一轮接受结论：在辅助模型显式 KV cap 后，`dual-light-v1` 可以作为单机完整产品常驻 profile。Warm `fast` 约 48-50 tok/s，warm `deep` 约 7.3 tok/s。停掉 embedding/reranker 后的 chat-only 对照中，`deep` 吞吐基本不变，因此 `deep` 慢应视为稠密模型的质量/稳定性取舍，而不是常驻方案失败。辅助模型如果在两个 chat lanes 常驻后启动，需要小的显式 KV budget：embedding 32K 启动失败，embedding 8K + 2G KV 可启动，warm request 约 28.5 ms。

这个单用户 profile 的验收标准是综合任务表现，而不是并发。2026-05-25，`dual-light-v1` 在 fast、deep、embedding、reranker 全部常驻，并由 external Gateway 调用的情况下，通过了 58-case real-model golden eval。因此它是当前接受的单机双模型 profile；后续调优重点应放在质量回退、启动体验和 first-request warmup 上。

### 轻量双常驻测试循环

每轮调参使用这个循环：

```bash
scripts/serve_models_compose.sh dual-light

curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models

set -a
. docker/env/sparkclaw.dual-light.env
set +a
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_EMBEDDING_BASE_URL=http://127.0.0.1:8003/v1 \
SPARKCLAW_RERANKER_BASE_URL=http://127.0.0.1:8004/v1 \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md

SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_EMBEDDING_BASE_URL=http://127.0.0.1:8003/v1 \
SPARKCLAW_RERANKER_BASE_URL=http://127.0.0.1:8004/v1 \
python3 scripts/record_model_loading.py --profile dual-light-v1
```

只在 chat-only 对照实验中使用 `scripts/serve_models_compose.sh dual-light-chat`。被接受的产品 profile 是 `dual-light`，包含 fast、deep、embedding 和 reranker。

失败的 profile 也要记录。启动失败加上 logs 和 GPU process state，本身就是有价值的证据。

## 取舍矩阵

| 选择 | 收益 | 代价 | 适用场景 |
|---|---|---|---|
| 一次一个 full lane | 最稳定，保留完整 128K context | 需要切 lane 或 profile aliasing | 单机默认模式 |
| 轻量双常驻 | 两个 lanes 都是 warm | context、并发降低，且先不开 MTP/DFlash | 单机演示或必须快速切 lane 的工作流 |
| `deep` priority | 高风险/代码审查质量更稳 | `fast` 变成短上下文助手 | 单机上的最佳 SparkClaw 取舍 |
| `fast` priority | 日常 chat 响应更强 | `deep` 失去长上下文优势 | UI-heavy 或短轮次工作流 |
| 两台 DGX Spark | full `fast` 和 full `deep` 都可常驻 | 需要更多硬件和 endpoint 管理 | 长期推荐部署 |

## 双机方案

当有两台 DGX Spark 时，优先按 lane 拆服务，而不是一开始就做跨机器单模型分布式：

| 机器 | 服务 | 备注 |
|---|---|---|
| DGX Spark A | `fast`、embedding、reranker、可选 guard | 优先优化交互延迟和 retrieval support。 |
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
- 如果 profile 包含 embedding/reranker，记录它们的可用性。
- Golden eval 结果和 regression notes。

长期测量结果追加到 [模型基线](../benchmarks/model_baseline.md)。本文档只维护策略和被接受的加载方案。
