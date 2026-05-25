# SparkClaw Model Baseline

> Language: English | [简体中文](../zh-cn/benchmarks/model_baseline.md)

This file records the DGX Spark hardware validation and repeatable endpoint benchmarks for the local fast, deep, embedding and reranker model services.

## Serving Commands

Host-side vLLM:

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Compose vLLM services:

```bash
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding,reranker
```

Defaults:

| Lane | Model | Served name | Port | Context | MTP |
|---|---|---|---:|---:|---:|
| fast | Qwen/Qwen3.6-35B-A3B-FP8 | sparkclaw-fast | 8001 | 131072 | 2 speculative tokens |
| deep | Qwen/Qwen3.6-27B-FP8 | sparkclaw-deep | 8002 | 131072 | 2 speculative tokens |
| embedding | Qwen/Qwen3-Embedding-0.6B | sparkclaw-embedding | 8003 | 32768 | off |
| reranker | Qwen/Qwen3-Reranker-0.6B | sparkclaw-reranker | 8004 | 2048 | off |

Set `SPARKCLAW_FAST_*`, `SPARKCLAW_DEEP_*`, `SPARKCLAW_EMBEDDING_*` or `SPARKCLAW_RERANKER_*` environment variables to adjust checkpoint, served name, port, tensor parallel size, context length, speculative decoding, GPU memory utilization or vLLM image. Use `*_MODEL_ID` for the Hugging Face checkpoint and `*_MODEL` for the OpenAI-compatible served model name that Gateway sends.

Experimental single-machine dual residency:

| Profile | Fast | Deep | MTP | Notes |
|---|---|---|---|---|
| `dual-light-v1` | 32K context, 8G KV, 4 seqs, 768 max tokens | 64K context, 12G KV, 2 seqs, 1536 max tokens | off | Single-user full product profile. Embedding uses 8K/2G KV/1 seq; reranker uses 2K/1G KV/1 seq. |

## Checks

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8004/v1/models
```

Then run the Gateway in external model mode:

```bash
SPARKCLAW_MODEL_MODE=external \
SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS=300 \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

Run the repeatable endpoint benchmark after the OpenAI-compatible services are live:

```bash
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md
```

## DGX Spark Bring-Up Results

| Date | Hardware | Area | Result | Evidence |
|---|---|---|---|---|
| 2026-05-24 | NVIDIA GB10, driver 580.159.03 | GPU visibility | Passed | `nvidia-smi` detected NVIDIA GB10 with CUDA 13.0 driver stack. |
| 2026-05-24 | NVIDIA GB10, driver 580.159.03 | GPU container runtime | Passed | `docker run --rm --gpus all nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi` detected NVIDIA GB10 from inside the container. |
| 2026-05-24 | NVIDIA GB10 | Control plane | Passed | `doctor.sh`, WebChat build, Gateway Go tests, Docker `minimal` stack, `/healthz`, `/readyz`, and 58-case golden eval passed. |
| 2026-05-24 | NVIDIA GB10 | Data services | Passed | Docker `models-local` started Postgres/pgvector and MinIO; Gateway passed smoke and golden evals with `SPARKCLAW_STATE_BACKEND=postgres`. |
| 2026-05-24 | NVIDIA GB10 | vLLM container | Passed | `vllm/vllm-openai:cu130-nightly` arm64 image returned `0.19.2rc1.dev134+gfe9c3d6c5.cu130`. |
| 2026-05-24 | NVIDIA GB10 | Deep chat endpoint | Passed | `Qwen/Qwen3.6-27B-FP8` loaded 66 shards, 28.75 GiB checkpoint, 28.08 GiB model memory, 63.98 GiB KV cache and 7.13x 131K concurrency; benchmark rows below. |
| 2026-05-24 | NVIDIA GB10 | Fast chat endpoint | Passed | `Qwen/Qwen3.6-35B-A3B-FP8` loaded 42 shards, 34.89 GiB checkpoint, 34.18 GiB model memory, 59.55 GiB KV cache and 20.05x 131K concurrency; benchmark rows below. |
| 2026-05-24 | NVIDIA GB10 | Embedding endpoint | Passed | `Qwen/Qwen3-Embedding-0.6B` returned 1024 dimensions in 26.5 ms. |
| 2026-05-24 | NVIDIA GB10 | Reranker endpoint | Passed | `Qwen/Qwen3-Reranker-0.6B` ran with `--runner auto --convert auto --max-model-len 2048`; `/generative_scoring` returned relevance scores. Gateway and benchmark code fall back from `/rerank` 404 to `/generative_scoring`. |
| 2026-05-24 | NVIDIA GB10 | Real-model golden eval | Passed | Dockerized Gateway ran in `SPARKCLAW_MODEL_MODE=external` with `SPARKCLAW_EXPECT_REAL_MODELS=1`; output included `ok golden tasks passed tool_calls=38 approvals=8 memory_candidates=1` and `ok extended golden checks passed golden_cases=58`. |
| 2026-05-24 | NVIDIA GB10 | Full-context fast+deep residency | Limited | Deep 128K/MTP residency left 12.08 GiB free; fast requested 12.16 GiB even at reduced settings. Operate one chat lane at 128K/MTP at a time, route both profiles to the loaded lane, or reduce context/MTP and re-measure. |
| 2026-05-25 | NVIDIA GB10 | Light dual-residency v1 | Passed | `dual-light-v1` kept fast and deep healthy together with embedding and reranker resident. Fast used 32K context, 8G KV and 4 seqs; deep used 64K context, 12G KV and 2 seqs; MTP off. |
| 2026-05-25 | NVIDIA GB10 | `dual-light-v1` golden eval | Passed | External Gateway with fast/deep/embedding/reranker resident passed real-model golden eval: `ok golden tasks passed tool_calls=38 approvals=8 memory_candidates=1`; `ok extended golden checks passed golden_cases=58`. |

Evidence files in this working tree:

- `data/eval/model-benchmark-report.json`: latest fast-lane chat benchmark JSON.
- `data/eval/embedding-reranker-check.json`: embedding and reranker endpoint check JSON.
- This markdown file: deep-lane benchmark rows and consolidated hardware notes.
- `data/eval/model-benchmark-dual-light-v1.json`: first light dual-residency chat benchmark.
- `data/eval/aux-check-dual-light-v1.json`: auxiliary embedding/reranker check for the light dual-residency run.
- `data/eval/model-loading-dual-light-v1.json`: endpoint, GPU process and benchmark snapshot for the accepted `dual-light-v1` test.
- `data/eval/model-benchmark-dual-light-v1-chat-only.json`: chat-only control with embedding and reranker stopped.
- `data/eval/aux-check-dual-light-v1-small-aux-warm.json`: warm auxiliary endpoint check after explicit small KV caps.
- `data/eval/model-loading-dual-light-v1-small-aux.json`: full product residency snapshot after auxiliary caps.
- `data/eval/model-loading-dual-light-v1-golden-passed.json`: full product residency snapshot after the real-model golden eval passed.
- `data/eval/model-loading-dual-light-v1-full-entry.json`: snapshot after the `dual-light` startup shortcut was corrected to include all four model services.
- `data/eval/aux-check-dual-light-v1-full-entry-warm2.json`: warm auxiliary endpoint check after the corrected full-product startup path.

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
| reranker | sparkclaw-reranker | passed | 14.6 | Generative scoring returned 0.3923 for the relevant document and 0.2121 for the distractor |

## Operating Notes

- The Hugging Face token belongs in local `.env` as `HF_TOKEN` and/or `HUGGING_FACE_HUB_TOKEN`; `.env` and downloaded model weights are ignored by git.
- Gateway requests must use served names for the small auxiliary models: `SPARKCLAW_EMBEDDING_MODEL=sparkclaw-embedding` and `SPARKCLAW_RERANKER_MODEL=sparkclaw-reranker`. Compose loads the checkpoint IDs through `SPARKCLAW_EMBEDDING_MODEL_ID` and `SPARKCLAW_RERANKER_MODEL_ID`.
- `SPARKCLAW_MODEL_DISABLE_THINKING=true` is required for Qwen3 chat-completions runs that need concise assistant content rather than reasoning-only output.
- For the real golden run, both fast and deep profiles were routed to the loaded live chat endpoint with lower generation caps (`SPARKCLAW_FAST_MAX_TOKENS=256`, `SPARKCLAW_DEEP_MAX_TOKENS=384`) to keep the 58-case eval stable.
- At 128K context with MTP enabled, fast and deep chat services should be treated as mutually exclusive on this GB10 configuration unless context, MTP or GPU memory utilization is reduced and validated again.
- `fast` is the responsive MoE lane; `deep` is the dense stability/quality lane. The measured `deep` throughput around 7.3 tok/s is expected for this compromise and should not be optimized in isolation from task quality, eval pass rate and overall product feel.
- Stopping embedding and reranker did not materially change `dual-light-v1` chat throughput, so auxiliary residency is acceptable when retrieval workflows need it.
- When both chat lanes are resident, the auxiliary models must not use broad default context/memory settings. Embedding 32K failed after chat residency because available KV cache was too small; embedding 8K with 2G explicit KV and one sequence restored the full stack. First auxiliary request after restart can be slow, but warmed embedding/reranker checks were 28.5 ms and 24.7 ms.
- The current single-user acceptance criterion is integrated task behavior: `dual-light-v1` is accepted because the external Gateway passed the 58-case real-model golden eval with both chat lanes and auxiliary models resident.
- `scripts/serve_models_compose.sh dual-light` now starts the full product profile: fast, deep, embedding and reranker. Use `dual-light-chat` only for chat-only controls.

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

## DGX Spark Run 2026-05-25T07:22:00.080057+00:00

Profile: `dual-light-v1-chat-only`

| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---|
| 2026-05-25 | DGX Spark | deep | chat | 2 | 377.1 | 454.6 | 4038.3 | 4603.7 | 7.51 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | coding | 2 | 304.7 | 306.6 | 59829.2 | 63060.4 | 7.27 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | email_triage | 2 | 360.6 | 409.8 | 11252.4 | 13097.0 | 7.35 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | summary | 2 | 308.1 | 309.5 | 7481.5 | 7488.7 | 7.39 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | chat | 2 | 292.8 | 467.5 | 771.2 | 944.7 | 50.16 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | coding | 2 | 120.6 | 120.8 | 6679.2 | 7367.4 | 48.12 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | email_triage | 2 | 128.9 | 129.2 | 4356.0 | 4590.6 | 48.27 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | summary | 2 | 130.8 | 132.0 | 1255.5 | 1278.5 | 48.9 | real endpoint benchmark |
