# SparkClaw Model Loading Plan

> Language: English | [简体中文](../zh-cn/docs/model-loading.md)

This document records the current model-loading strategy for SparkClaw on DGX Spark-class hardware. It complements the measured endpoint evidence in [Model baseline](../benchmarks/model_baseline.md) and the operational steps in [Deployment](deployment.md).

The short version: single-machine SparkClaw should prioritize stable residency, predictable agent behavior and the combined product experience over maximum decoding speed. `fast` is the responsive MoE lane; `deep` is a dense model lane whose slower decoding is expected. Deep is judged by task stability, reasoning quality and eval behavior, not by matching fast-lane throughput. MTP and DFlash-style acceleration are deferred until a two-DGX-Spark layout has enough memory headroom to make performance tuning useful.

## Current Baseline

The validated GB10 run used one chat lane at a time with 128K context, vLLM, FP8 Qwen models and MTP set to 2 speculative tokens.

| Lane | Model | Context | Model memory | KV cache | Model + KV | Observed behavior |
|---|---|---:|---:|---:|---:|---|
| `fast` | `Qwen/Qwen3.6-35B-A3B-FP8` | 131072 | 34.18 GiB | 59.55 GiB | 93.73 GiB | Warm requests around 59-70 tok/s |
| `deep` | `Qwen/Qwen3.6-27B-FP8` | 131072 | 28.08 GiB | 63.98 GiB | 92.06 GiB | Around 13-18 tok/s |

The important point is that the full profiles are dominated by KV cache reservation, not only model weights. `fast + deep` at the measured full settings would require roughly 185.79 GiB before runtime overhead, so full-context dual residency should not be treated as viable on one 128 GB unified-memory machine.

The failed dual-residency attempt was close only because it failed during an incremental allocation: after `deep` was already loaded, the machine had 12.08 GiB free and the `fast` startup path requested 12.16 GiB. That 12.16 GiB was not the full cost of `fast`; it was only the allocation that failed at that moment.

## Single-Machine Policy

Default single-machine operation should use one full chat lane at a time:

- Run `fast` full profile for normal interactive work, drafting, search-grounded answers and light planning.
- Run `deep` full profile for code work, high-risk review, repair verification, terminal-related tasks and explicit deep requests.
- Treat lower `deep` throughput as an expected dense-model cost. Optimize `deep` for reliability and answer quality, while using `fast` for responsive interaction and short-turn flow.
- During evals or constrained runs, route both Gateway profiles to the loaded lane when necessary.
- Keep embedding and reranker small but resident in the accepted product
  profile. Embedding is required to build the semantic routing index at Gateway
  startup; reranker supplies the bounded routing reorder channel.

Single-machine performance features are intentionally conservative:

- Keep MTP off for light dual-residency experiments.
- Do not rely on DFlash or similar attention/runtime acceleration for the single-machine plan.
- Treat acceleration features as second-order tuning after the residency plan is stable.
- Re-run endpoint benchmarks and the golden eval after any change to context, KV budget, MTP, serving image or model checkpoint.

## Light Dual-Residency Experiment

If a single DGX Spark needs both chat lanes resident, the experiment should start from reduced residency profiles rather than from the full 128K/MTP profiles.

Recommended first target:

| Setting | `fast` | `deep` | embedding | reranker | Rationale |
|---|---:|---:|---:|---:|---|
| Context | 32768 | 65536 | 8192 | 2048 | Preserve deep context first; keep auxiliary context sufficient for single-user semantic routing and bounded ranking. |
| MTP | off | off | off | off | Save memory and reduce moving parts while validating residency. |
| KV cache budget | 8 GiB | 12 GiB | 2 GiB | 1 GiB | Cap the real pressure point instead of letting each server reserve a full lane. |
| Max response tokens | 768 | 1536 | n/a | n/a | Keep agent loops responsive and evals stable. |
| Max concurrent sequences | 4 | 2 | 1 | 1 | The product is single-user; optimize for fit and latency, not concurrency. |
| GPU memory utilization | 0.42 | 0.44 | 0.06 | 0.06 | `--kv-cache-memory-bytes` is the binding cap; utilization stays conservative for startup checks. |

This profile is implemented as `dgx-spark-dual-light-v1`:

- Compose: `docker/compose.dual-light.yaml`
- Environment: `docker/env/sparkclaw.dual-light.env`
- Profile metadata: `configs/model.profiles.json`
- Startup shortcut: `scripts/serve_models_compose.sh dual-light`

The preferred compromise is `deep` priority:

- Keep `deep` at the largest context that fits reliably.
- Reduce `fast` to 32K or 64K.
- Keep both MTP and DFlash-style optimizations off.
- Measure startup, idle residency, a warmed request, a long-context request and the active 43-case golden eval before promoting the profile.

This gives SparkClaw a responsive local `fast` lane while preserving the main value of `deep`: harder reasoning over larger evidence windows.

First accepted finding: `dual-light-v1` fits as a single-machine full product residency profile after auxiliary KV caps are made explicit. Warmed `fast` runs around 48-50 tok/s and warmed `deep` runs around 7.3 tok/s. A chat-only control, with embedding and reranker stopped, left `deep` throughput essentially unchanged, so the slower `deep` lane is treated as the dense-model quality/stability trade-off rather than a residency failure. The auxiliary models need small explicit KV budgets when they are started after both chat lanes: embedding at 32K failed, embedding at 8K with 2G KV started and returned warm requests around 28.5 ms.

The acceptance standard for this single-user profile is integrated task performance, not concurrency. On 2026-05-25, `dual-light-v1` passed the historical 58-case real-model golden eval with fast, deep, embedding and reranker resident through an external Gateway. That makes it the current accepted single-machine dual-model profile while further tuning focuses on quality regressions, startup ergonomics and first-request warmup. The active 43-case matrix still requires a fresh real-model run when model-stack behavior changes.

### Dual-Light Test Loop

Use this loop for each tuning step:

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

Use `scripts/serve_models_compose.sh dual-light-chat` only for chat-only controls. The accepted product profile is `dual-light`, which includes fast, deep, embedding and reranker.

Record rejected profiles too. A failed startup with logs and GPU process state is still useful evidence.

## Trade-Off Matrix

| Choice | Gains | Costs | When to choose |
|---|---|---|---|
| One full lane at a time | Maximum stability and full 128K context | Lane switching or profile aliasing is required | Default single-machine mode |
| Light dual residency | Both lanes stay warm | Lower context, lower concurrency and no MTP/DFlash | Single-machine demos or workflows that need instant lane switching |
| `deep` priority | Keeps high-risk/code review quality higher | `fast` becomes a short-context helper | Best SparkClaw compromise on one machine |
| `fast` priority | Better daily chat responsiveness | `deep` loses long-context advantage | UI-heavy or short-turn workflows |
| Two DGX Spark machines | Full `fast` and full `deep` can stay resident | More hardware and endpoint management | Preferred long-term deployment |

## Two-Machine Plan

When two DGX Spark systems are available, split model serving by lane rather than distributing one model across both machines first:

| Machine | Services | Notes |
|---|---|---|
| DGX Spark A | `fast`, embedding, reranker, optional guard | Optimized for interactive latency, semantic routing, and bounded ranking. |
| DGX Spark B | `deep` | Keep memory headroom for large context and repair/review tasks. |

Only after this split is stable should performance features be enabled:

1. Enable MTP on `fast`, then benchmark.
2. Enable MTP on `deep` only if memory headroom and output quality remain acceptable.
3. Evaluate DFlash or similar runtime acceleration after both lanes are independently stable.
4. Raise context or response caps only when evals show no regression.
5. Consider cross-machine tensor parallelism only for a future model that does not fit on one DGX Spark.

## Validation Checklist

Every new loading profile should record:

- Hardware, driver, serving image and model checkpoint IDs.
- Context length, KV cache budget, GPU memory utilization and MTP/DFlash status.
- Startup success, idle memory, first-request latency and warmed-request latency.
- Throughput for chat, summary, email triage and coding scenarios.
- Embedding/reranker availability and Gateway semantic-router readiness.
- Golden eval result and any regression notes.

Append durable measurements to [Model baseline](../benchmarks/model_baseline.md). Keep this document focused on strategy and accepted loading profiles.
