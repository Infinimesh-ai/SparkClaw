# SparkClaw 模型基准

> 语言： [English](../../benchmarks/model_baseline.md) | 简体中文

本文记录 DGX Spark 上本地 fast、deep、embedding 和 reranker 模型服务的硬件验证与可复现实测基准。

## 启动命令

Host-side vLLM：

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Compose vLLM services：

```bash
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding,reranker
```

默认值：

| Lane | Model | Served name | Port | Context | MTP |
|---|---|---|---:|---:|---:|
| fast | Qwen/Qwen3.6-35B-A3B-FP8 | sparkclaw-fast | 8001 | 131072 | 2 speculative tokens |
| deep | Qwen/Qwen3.6-27B-FP8 | sparkclaw-deep | 8002 | 131072 | 2 speculative tokens |
| embedding | Qwen/Qwen3-Embedding-0.6B | sparkclaw-embedding | 8003 | 32768 | off |
| reranker | Qwen/Qwen3-Reranker-0.6B | sparkclaw-reranker | 8004 | 2048 | off |

通过 `SPARKCLAW_FAST_*`、`SPARKCLAW_DEEP_*`、`SPARKCLAW_EMBEDDING_*`、`SPARKCLAW_RERANKER_*` 可以调整 checkpoint、served name、端口、tensor parallel size、context length、speculative decoding、GPU memory utilization 或 vLLM image。`*_MODEL_ID` 用于 Hugging Face checkpoint，`*_MODEL` 用于 Gateway 请求 OpenAI-compatible endpoint 时发送的 served model name。

实验性单机双常驻：

| Profile | Fast | Deep | MTP | Notes |
|---|---|---|---|---|
| `dual-light-v1` | 32K context、8G KV、4 seqs、768 max tokens | 64K context、12G KV、2 seqs、1536 max tokens | off | 单用户完整产品 profile。Embedding 使用 8K/2G KV/1 seq；reranker 使用 2K/1G KV/1 seq。 |

## 检查

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8004/v1/models
```

然后以 external model mode 运行 Gateway：

```bash
SPARKCLAW_MODEL_MODE=external \
SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS=300 \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

OpenAI-compatible 服务就绪后运行可复现 benchmark：

```bash
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md
```

## DGX Spark Bring-Up 结果

| Date | Hardware | Area | Result | Evidence |
|---|---|---|---|---|
| 2026-05-24 | NVIDIA GB10, driver 580.159.03 | GPU visibility | Passed | `nvidia-smi` 检测到 NVIDIA GB10 与 CUDA 13.0 driver stack。 |
| 2026-05-24 | NVIDIA GB10, driver 580.159.03 | GPU container runtime | Passed | `docker run --rm --gpus all nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi` 在容器内检测到 NVIDIA GB10。 |
| 2026-05-24 | NVIDIA GB10 | Control plane | Passed | `doctor.sh`、WebChat build、Gateway Go tests、Docker `minimal` stack、`/healthz`、`/readyz` 与 58-case golden eval 均通过。 |
| 2026-05-24 | NVIDIA GB10 | Data services | Passed | Docker `models-local` 启动 Postgres/pgvector 与 MinIO；Gateway 在 `SPARKCLAW_STATE_BACKEND=postgres` 下通过 smoke/golden eval。 |
| 2026-05-24 | NVIDIA GB10 | vLLM container | Passed | `vllm/vllm-openai:cu130-nightly` arm64 image 返回 `0.19.2rc1.dev134+gfe9c3d6c5.cu130`。 |
| 2026-05-24 | NVIDIA GB10 | Deep chat endpoint | Passed | `Qwen/Qwen3.6-27B-FP8` 加载 66 shards、28.75 GiB checkpoint、28.08 GiB model memory、63.98 GiB KV cache、7.13x 131K concurrency；benchmark rows 如下。 |
| 2026-05-24 | NVIDIA GB10 | Fast chat endpoint | Passed | `Qwen/Qwen3.6-35B-A3B-FP8` 加载 42 shards、34.89 GiB checkpoint、34.18 GiB model memory、59.55 GiB KV cache、20.05x 131K concurrency；benchmark rows 如下。 |
| 2026-05-24 | NVIDIA GB10 | Embedding endpoint | Passed | `Qwen/Qwen3-Embedding-0.6B` 返回 1024 dimensions，latency 26.5 ms。 |
| 2026-05-24 | NVIDIA GB10 | Reranker endpoint | Passed | `Qwen/Qwen3-Reranker-0.6B` 使用 `--runner auto --convert auto --max-model-len 2048`；`/generative_scoring` 返回 relevance scores。Gateway 和 benchmark code 会从 `/rerank` 404 fallback 到 `/generative_scoring`。 |
| 2026-05-24 | NVIDIA GB10 | Real-model golden eval | Passed | Dockerized Gateway 以 `SPARKCLAW_MODEL_MODE=external` 和 `SPARKCLAW_EXPECT_REAL_MODELS=1` 运行；输出包含 `ok golden tasks passed tool_calls=38 approvals=8 memory_candidates=1` 和 `ok extended golden checks passed golden_cases=58`。 |
| 2026-05-24 | NVIDIA GB10 | Full-context fast+deep residency | Limited | Deep 128K/MTP 常驻后剩余 12.08 GiB；fast 即使降配仍请求 12.16 GiB。应一次运行一个 128K/MTP chat lane，或降低 context/MTP 后重新测量。 |
| 2026-05-25 | NVIDIA GB10 | Light dual-residency v1 | Passed | `dual-light-v1` 让 fast/deep 与 embedding/reranker 同时 healthy。Fast 使用 32K context、8G KV、4 seqs；deep 使用 64K context、12G KV、2 seqs；MTP 关闭。 |
| 2026-05-25 | NVIDIA GB10 | `dual-light-v1` golden eval | Passed | External Gateway 在 fast/deep/embedding/reranker 常驻时通过 real-model golden eval：`ok golden tasks passed tool_calls=38 approvals=8 memory_candidates=1`；`ok extended golden checks passed golden_cases=58`。 |

工作树中的证据文件：

- `data/eval/model-benchmark-report.json`：最新 fast-lane chat benchmark JSON。
- `data/eval/embedding-reranker-check.json`：embedding 与 reranker endpoint check JSON。
- 本 markdown 文件：deep-lane benchmark rows 与合并硬件说明。
- `data/eval/model-benchmark-dual-light-v1.json`：第一轮 light dual-residency chat benchmark。
- `data/eval/aux-check-dual-light-v1.json`：light dual-residency run 的 embedding/reranker 辅助检查。
- `data/eval/model-loading-dual-light-v1.json`：`dual-light-v1` 的 endpoint、GPU process 和 benchmark 快照。
- `data/eval/model-benchmark-dual-light-v1-chat-only.json`：停掉 embedding/reranker 后的 chat-only 对照。
- `data/eval/aux-check-dual-light-v1-small-aux-warm.json`：辅助模型显式小 KV cap 后的 warm endpoint check。
- `data/eval/model-loading-dual-light-v1-small-aux.json`：辅助 cap 后的完整产品常驻快照。
- `data/eval/model-loading-dual-light-v1-golden-passed.json`：real-model golden eval 通过后的完整产品常驻快照。
- `data/eval/model-loading-dual-light-v1-full-entry.json`：修正 `dual-light` 启动快捷方式使其包含四个模型服务后的快照。
- `data/eval/aux-check-dual-light-v1-full-entry-warm2.json`：修正完整产品启动路径后的 warm 辅助 endpoint check。

## DGX Spark Run 2026-05-24T14:21:59.638694+00:00

| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---|
| 2026-05-24 | DGX Spark | deep | chat | 1 | 209.7 | 209.7 | 2105.0 | 2105.0 | 16.36 | real endpoint benchmark |
| 2026-05-24 | DGX Spark | deep | coding | 1 | 348.7 | 348.7 | 23917.3 | 23917.3 | 16.29 | real endpoint benchmark |
| 2026-05-24 | DGX Spark | deep | email_triage | 1 | 350.6 | 350.6 | 7932.8 | 7932.8 | 12.93 | real endpoint benchmark |
| 2026-05-24 | DGX Spark | deep | summary | 1 | 351.0 | 351.0 | 3351.7 | 3351.7 | 17.66 | real endpoint benchmark |

## DGX Spark Run 2026-05-24T14:30:46.126303+00:00

| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---|
| 2026-05-24 | DGX Spark | fast | chat | 1 | 29661.8 | 29661.8 | 29943.9 | 29943.9 | 85.07 | cold first request after model load |
| 2026-05-24 | DGX Spark | fast | coding | 1 | 145.1 | 145.1 | 4288.4 | 4288.4 | 61.54 | real endpoint benchmark |
| 2026-05-24 | DGX Spark | fast | email_triage | 1 | 151.4 | 151.4 | 3306.4 | 3306.4 | 58.95 | real endpoint benchmark |
| 2026-05-24 | DGX Spark | fast | summary | 1 | 154.3 | 154.3 | 929.0 | 929.0 | 69.71 | real endpoint benchmark |

## Endpoint Check 2026-05-24T14:50:08.058881+00:00

| Lane | Model | Status | Latency ms | Result |
|---|---|---|---:|---|
| embedding | sparkclaw-embedding | passed | 26.5 | 1024-dimensional embedding vector |
| reranker | sparkclaw-reranker | passed | 14.6 | Generative scoring 对相关文档返回 0.3923，对 distractor 返回 0.2121 |

## DGX Spark Run 2026-05-25T07:15:25.627592+00:00

Profile: `dual-light-v1`

| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---|
| 2026-05-25 | DGX Spark | deep | chat | 2 | 9770.6 | 19240.7 | 16268.1 | 25489.8 | 7.38 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | coding | 2 | 302.0 | 303.4 | 36045.6 | 41436.7 | 7.28 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | email_triage | 2 | 310.8 | 312.5 | 13068.7 | 14112.4 | 7.33 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | summary | 2 | 309.5 | 309.7 | 7547.1 | 7621.3 | 7.39 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | chat | 2 | 9747.9 | 19391.2 | 10227.8 | 19871.5 | 50.01 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | coding | 2 | 317.6 | 514.6 | 6867.4 | 7079.1 | 48.09 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | email_triage | 2 | 125.2 | 125.4 | 4767.1 | 5012.3 | 48.15 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | summary | 2 | 482.7 | 835.4 | 1587.3 | 1938.3 | 48.89 | real endpoint benchmark |

## 运行说明

- Hugging Face token 应只放在本地 `.env` 中，作为 `HF_TOKEN` 和/或 `HUGGING_FACE_HUB_TOKEN`；`.env` 与下载的模型权重均被 git ignore。
- Gateway 请求辅助模型时必须使用 served names：`SPARKCLAW_EMBEDDING_MODEL=sparkclaw-embedding` 和 `SPARKCLAW_RERANKER_MODEL=sparkclaw-reranker`。Compose 通过 `SPARKCLAW_EMBEDDING_MODEL_ID` 与 `SPARKCLAW_RERANKER_MODEL_ID` 加载 checkpoint IDs。
- 对 Qwen3 chat-completions，`SPARKCLAW_MODEL_DISABLE_THINKING=true` 是必要的；否则容易得到 reasoning-only output，而不是简洁 assistant content。
- 真实 golden run 中，fast 与 deep profiles 都路由到已加载的 live chat endpoint，并使用较低 generation caps：`SPARKCLAW_FAST_MAX_TOKENS=256`、`SPARKCLAW_DEEP_MAX_TOKENS=384`，以保证 58-case eval 稳定。
- 在 128K context 且 MTP enabled 时，本 GB10 配置上 fast 与 deep chat services 应视为互斥常驻，除非降低 context、MTP 或 GPU memory utilization 后重新验证。
- `fast` 是响应快的 MoE lane；`deep` 是稠密的稳定性/质量 lane。`deep` 实测约 7.3 tok/s 属于这个取舍的预期表现，不应脱离任务质量、eval 通过率和整体产品体验单独优化。
- 停掉 embedding 和 reranker 后，`dual-light-v1` 的 chat 吞吐没有明显变化；需要 retrieval workflow 时，辅助模型常驻是可以接受的。
- 两个 chat lanes 已常驻时，辅助模型不能使用宽松的默认 context/memory 设置。Embedding 32K 在 chat 常驻后因可用 KV cache 不足启动失败；embedding 8K + 2G 显式 KV + 1 seq 恢复了完整栈。重启后的首次辅助请求可能慢，但 warm embedding/reranker check 分别为 28.5 ms 和 24.7 ms。
- 当前单用户验收标准是综合任务表现：`dual-light-v1` 已被接受，因为 external Gateway 在两个 chat lanes 和辅助模型都常驻时通过了 58-case real-model golden eval。
- `scripts/serve_models_compose.sh dual-light` 现在启动完整产品 profile：fast、deep、embedding、reranker。`dual-light-chat` 只用于 chat-only 对照。
