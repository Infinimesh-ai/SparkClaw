# SparkClaw Model Baseline

> Language: English | [简体中文](../zh-cn/benchmarks/model_baseline.md)

This file records DGX Spark hardware validation and repeatable endpoint
benchmarks for the current fast, deep, embedding, and guard services. Reranker
entries below are retained only as historical measurements from the model stack
that preceded its 2026-07-24 removal.

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
scripts/serve_models_compose.sh embedding
scripts/serve_models_compose.sh guard
```

Defaults:

| Lane | Model | Served name | Port | Context | MTP |
|---|---|---|---:|---:|---:|
| fast | Qwen/Qwen3.6-35B-A3B-FP8 | sparkclaw-fast | 8001 | 131072 | 2 speculative tokens |
| deep | Qwen/Qwen3.6-27B-FP8 | sparkclaw-deep | 8002 | 131072 | 2 speculative tokens |
| embedding | Qwen/Qwen3-Embedding-0.6B | sparkclaw-embedding | 8003 | 32768 | off |
| guard | Qwen/Qwen3Guard-Gen-0.6B | Qwen/Qwen3Guard-Gen-0.6B | 8005 | 32768 | off |

Set `SPARKCLAW_FAST_*`, `SPARKCLAW_DEEP_*`, `SPARKCLAW_EMBEDDING_*`, or
`SPARKCLAW_GUARD_*` environment variables to adjust checkpoint, served name,
context length, generation limits, GPU memory utilization, or vLLM image. Use
`*_MODEL_ID` for the Hugging Face checkpoint and `*_MODEL` for the
OpenAI-compatible served model name that Gateway sends.

Experimental single-machine dual residency:

| Profile | Fast | Deep | MTP | Notes |
|---|---|---|---|---|
| `dual-light` | 32K context, 8G KV, 4 seqs, 768 max tokens | 64K context, 12G KV, 2 seqs, 1536 max tokens | off | Single-user product profile. Embedding uses 8K/2G KV/1 seq; guard uses 16K/2G KV/1 seq. |

## Checks

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8005/v1/models
```

Then run the Gateway in external model mode:

```bash
SPARKCLAW_MODEL_MODE=external \
SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS=300 \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
SPARKCLAW_GUARD_BASE_URL=http://127.0.0.1:8005/v1 \
SPARKCLAW_GUARD_MODEL=Qwen/Qwen3Guard-Gen-0.6B \
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

## Prompt Token Estimator Calibration

The ReAct prompt estimator was calibrated on 2026-07-27 against the local
Qwen Fast `/tokenize` endpoint:

```bash
python3 scripts/calibrate_prompt_tokens.py
```

| Sample | UTF-8 bytes | Tokens | Bytes/token |
|---|---:|---:|---:|
| English | 115 | 20 | 5.750 |
| Chinese | 160 | 38 | 4.211 |
| ReAct JSON | 145 | 36 | 4.028 |
| Mixed Chinese/English/JSON | 220 | 49 | 4.490 |

Runtime admission therefore estimates one token per four UTF-8 bytes plus a
12-token chat-envelope allowance. This remains conservative for all measured
samples and requires no online tokenizer dependency.

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
| 2026-05-24 | NVIDIA GB10 | Reranker endpoint (historical) | Passed | `Qwen/Qwen3-Reranker-0.6B` returned relevance scores. This endpoint and its Gateway integration were removed on 2026-07-24. |
| 2026-05-24 | NVIDIA GB10 | Real-model golden eval | Passed | Dockerized Gateway ran in `SPARKCLAW_MODEL_MODE=external` with `SPARKCLAW_EXPECT_REAL_MODELS=1`; output included `ok golden tasks passed tool_calls=38 approvals=8 memory_candidates=1` and `ok extended golden checks passed golden_cases=58`. |
| 2026-05-24 | NVIDIA GB10 | Full-context fast+deep residency | Limited | Deep 128K/MTP residency left 12.08 GiB free; fast requested 12.16 GiB even at reduced settings. Operate one chat lane at 128K/MTP at a time, route both profiles to the loaded lane, or reduce context/MTP and re-measure. |
| 2026-05-25 | NVIDIA GB10 | Light dual-residency v1 | Passed | `dual-light-v1` kept fast and deep healthy together with embedding and reranker resident. Fast used 32K context, 8G KV and 4 seqs; deep used 64K context, 12G KV and 2 seqs; MTP off. |
| 2026-05-25 | NVIDIA GB10 | `dual-light-v1` golden eval | Passed | External Gateway with fast/deep/embedding/reranker resident passed real-model golden eval: `ok golden tasks passed tool_calls=38 approvals=8 memory_candidates=1`; `ok extended golden checks passed golden_cases=58`. |
| 2026-07-24 | NVIDIA GB10 | Fast + Embedding intent calibration | Passed | The current full-candidate pipeline reached exact Top-1 15/15 at `alpha = 0.50`; pure Embedding reached 13/15 and the Fast channel reached 15/15. No reranker service or call participated. |
| 2026-07-24 | NVIDIA GB10 | Qwen3Guard endpoint | Passed | `Qwen/Qwen3Guard-Gen-0.6B` ran at 16K context with 2 GiB KV and one sequence. vLLM reported 1.12 GiB model memory; the process used 3,659 MiB and warmed Safe/Controversial checks completed in 79-107 ms. The rejected 32K setting required 3.5 GiB KV. |
| 2026-07-24 | NVIDIA GB10 | Current 43-case runner | Needs alignment | The runner stopped at its expected `code.apply_patch` approval because the current Catalog has no natural-language code/shell Workflow, while the unchanged matrix still asserts retired `code.apply_patch`, `shell.exec_sandboxed`, and `files.search` paths. This is not a current routing-quality acceptance result. |

Historical evidence files in this working tree:

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
| 2026-05-24 | DGX Spark | deep | summary | 1 | 351.0 | 351.0 | 3351.7 | 3351.7 | 17.66 | real endpoint benchmark |

## DGX Spark Run 2026-05-24T14:30:46.126303+00:00

| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---|
| 2026-05-24 | DGX Spark | fast | chat | 1 | 29661.8 | 29661.8 | 29943.9 | 29943.9 | 85.07 | cold first request after model load |
| 2026-05-24 | DGX Spark | fast | coding | 1 | 145.1 | 145.1 | 4288.4 | 4288.4 | 61.54 | real endpoint benchmark |
| 2026-05-24 | DGX Spark | fast | summary | 1 | 154.3 | 154.3 | 929.0 | 929.0 | 69.71 | real endpoint benchmark |

## Endpoint Check 2026-05-24T14:50:08.058881+00:00

| Lane | Model | Status | Latency ms | Result |
|---|---|---|---:|---|
| embedding | sparkclaw-embedding | passed | 26.5 | 1024-dimensional embedding vector |
| reranker | sparkclaw-reranker | passed | 14.6 | Generative scoring returned 0.3923 for the relevant document and 0.2121 for the distractor |

## Operating Notes

- The Hugging Face token belongs in local `.env` as `HF_TOKEN` and/or `HUGGING_FACE_HUB_TOKEN`; `.env` and downloaded model weights are ignored by git.
- Gateway embedding requests must use the served name
  `SPARKCLAW_EMBEDDING_MODEL=sparkclaw-embedding`; Compose loads the checkpoint
  through `SPARKCLAW_EMBEDDING_MODEL_ID`.
- `SPARKCLAW_MODEL_DISABLE_THINKING=true` is required for Qwen3 chat-completions runs that need concise assistant content rather than reasoning-only output.
- For the real golden run, both fast and deep profiles were routed to the loaded live chat endpoint with lower generation caps (`SPARKCLAW_FAST_MAX_TOKENS=256`, `SPARKCLAW_DEEP_MAX_TOKENS=384`) to keep the 58-case eval stable.
- At 128K context with MTP enabled, fast and deep chat services should be treated as mutually exclusive on this GB10 configuration unless context, MTP or GPU memory utilization is reduced and validated again.
- `fast` is the responsive MoE lane; `deep` is the dense stability/quality lane. The measured `deep` throughput around 7.3 tok/s is expected for this compromise and should not be optimized in isolation from task quality, eval pass rate and overall product feel.
- The historical chat-only control did not materially change `deep` throughput.
- When both chat lanes are resident, embedding must not use broad default
  context or memory settings. Embedding 32K failed after chat residency;
  embedding 8K with a 2G explicit KV cap and one sequence restored the stack.
- The historical 58-case result predates removal of the reranking lane. Align
  the 43-case runner with the current capability matrix, then run it before
  claiming equivalent current product quality.
- Qwen3Guard emits native `Safety: Safe|Unsafe|Controversial` labels. Gateway
  maps them to `allow`, `block`, and `review`. With no human review queue,
  `review` and `block` both stop the run; an unavailable endpoint remains
  visible in model-call telemetry as `mock=true`.
- `scripts/serve_models_compose.sh dual-light` starts fast, deep, embedding, and
  guard. Use `dual-light-chat` only for chat-only controls.

## DGX Spark Run 2026-05-25T07:15:25.627592+00:00

Profile: `dual-light-v1`

| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---|
| 2026-05-25 | DGX Spark | deep | chat | 2 | 9770.6 | 19240.7 | 16268.1 | 25489.8 | 7.38 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | coding | 2 | 302.0 | 303.4 | 36045.6 | 41436.7 | 7.28 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | summary | 2 | 309.5 | 309.7 | 7547.1 | 7621.3 | 7.39 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | chat | 2 | 9747.9 | 19391.2 | 10227.8 | 19871.5 | 50.01 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | coding | 2 | 317.6 | 514.6 | 6867.4 | 7079.1 | 48.09 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | summary | 2 | 482.7 | 835.4 | 1587.3 | 1938.3 | 48.89 | real endpoint benchmark |

## DGX Spark Run 2026-05-25T07:22:00.080057+00:00

Profile: `dual-light-v1-chat-only`

| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---|
| 2026-05-25 | DGX Spark | deep | chat | 2 | 377.1 | 454.6 | 4038.3 | 4603.7 | 7.51 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | coding | 2 | 304.7 | 306.6 | 59829.2 | 63060.4 | 7.27 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | deep | summary | 2 | 308.1 | 309.5 | 7481.5 | 7488.7 | 7.39 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | chat | 2 | 292.8 | 467.5 | 771.2 | 944.7 | 50.16 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | coding | 2 | 120.6 | 120.8 | 6679.2 | 7367.4 | 48.12 | real endpoint benchmark |
| 2026-05-25 | DGX Spark | fast | summary | 2 | 130.8 | 132.0 | 1255.5 | 1278.5 | 48.9 | real endpoint benchmark |
