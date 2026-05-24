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

工作树中的证据文件：

- `data/eval/model-benchmark-report.json`：最新 fast-lane chat benchmark JSON。
- `data/eval/embedding-reranker-check.json`：embedding 与 reranker endpoint check JSON。
- 本 markdown 文件：deep-lane benchmark rows 与合并硬件说明。

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

## 运行说明

- Hugging Face token 应只放在本地 `.env` 中，作为 `HF_TOKEN` 和/或 `HUGGING_FACE_HUB_TOKEN`；`.env` 与下载的模型权重均被 git ignore。
- Gateway 请求辅助模型时必须使用 served names：`SPARKCLAW_EMBEDDING_MODEL=sparkclaw-embedding` 和 `SPARKCLAW_RERANKER_MODEL=sparkclaw-reranker`。Compose 通过 `SPARKCLAW_EMBEDDING_MODEL_ID` 与 `SPARKCLAW_RERANKER_MODEL_ID` 加载 checkpoint IDs。
- 对 Qwen3 chat-completions，`SPARKCLAW_MODEL_DISABLE_THINKING=true` 是必要的；否则容易得到 reasoning-only output，而不是简洁 assistant content。
- 真实 golden run 中，fast 与 deep profiles 都路由到已加载的 live chat endpoint，并使用较低 generation caps：`SPARKCLAW_FAST_MAX_TOKENS=256`、`SPARKCLAW_DEEP_MAX_TOKENS=384`，以保证 58-case eval 稳定。
- 在 128K context 且 MTP enabled 时，本 GB10 配置上 fast 与 deep chat services 应视为互斥常驻，除非降低 context、MTP 或 GPU memory utilization 后重新验证。
