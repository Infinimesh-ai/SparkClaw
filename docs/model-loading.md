# SparkClaw Model Loading Plan

> Language: English | [简体中文](../zh-cn/docs/model-loading.md)

This document records the current model-loading strategy for SparkClaw on DGX Spark-class hardware. It complements the measured endpoint evidence in [Model baseline](../benchmarks/model_baseline.md) and the operational steps in [Deployment](deployment.md).

The short version: the current single-machine product runtime loads only the responsive `fast` MoE chat model, plus embedding and guard. The logical Deep Workflow profile is temporarily aliased to the Fast endpoint, so no Deep model process starts. Historical Deep and dual-residency measurements remain below for future evaluation; they are not the current startup policy.

## Current Baseline

The validated GB10 run used one chat lane at a time with 128K context, vLLM, FP8 Qwen models and MTP set to 2 speculative tokens.

| Lane | Model | Context | Model memory | KV cache | Model + KV | Observed behavior |
|---|---|---:|---:|---:|---:|---|
| `fast` | `Qwen/Qwen3.6-35B-A3B-FP8` | 131072 | 34.18 GiB | 59.55 GiB | 93.73 GiB | Warm requests around 59-70 tok/s |
| `deep` | `Qwen/Qwen3.6-27B-FP8` | 131072 | 28.08 GiB | 63.98 GiB | 92.06 GiB | Around 13-18 tok/s |

The important point is that the full profiles are dominated by KV cache reservation, not only model weights. `fast + deep` at the measured full settings would require roughly 185.79 GiB before runtime overhead, so full-context dual residency should not be treated as viable on one 128 GB unified-memory machine.

The failed dual-residency attempt was close only because it failed during an incremental allocation: after `deep` was already loaded, the machine had 12.08 GiB free and the `fast` startup path requested 12.16 GiB. That 12.16 GiB was not the full cost of `fast`; it was only the allocation that failed at that moment.

## Single-Machine Policy

Default single-machine operation uses the `single-fast-v1` profile:

- Run one Fast chat endpoint for all Workflow model calls.
- Preserve the logical fast/deep profile choice in traces, but configure both profiles with `SPARKCLAW_DEEP_BASE_URL=http://sparkclaw-fast:8001/v1` and `SPARKCLAW_DEEP_MODEL=sparkclaw-fast`.
- Do not start the `sparkclaw-deep` container in the product startup path.
- Keep embedding and the dedicated guard small but resident in the product
  profile. Embedding builds the semantic routing index and scores each request;
  guard moderates the owner prompt before routing or tool execution.

Single-machine performance features are intentionally conservative:

- Keep MTP off for light dual-residency experiments.
- Do not rely on DFlash or similar attention/runtime acceleration for the single-machine plan.
- Treat acceleration features as second-order tuning after the residency plan is stable.
- Re-run endpoint benchmarks and the golden eval after any change to context, KV budget, MTP, serving image or model checkpoint.

## Active Single-Fast Profile

The current profile is implemented as `dgx-spark-single-fast-v1`:

- Environment: `docker/env/sparkclaw.single-fast.env`
- Compose resource override: `docker/compose.dual-light.yaml`
- Profile metadata: `configs/model.profiles.json`
- Startup shortcut: `scripts/serve_models_compose.sh single-fast`

The shortcut first stops a previously running Deep container, then starts only
Fast, embedding, and guard. Run `scripts/restart_runtime_compose.sh` afterward;
it uses the same single-Fast environment by default. The current Fast capacity
remains at the previously exercised 32K context and 8 GiB KV cache rather than
claiming an unmeasured capacity increase from the memory freed by Deep. Model
startup waits for Docker health. Guard health includes one real bounded chat
completion per container start, so its first user moderation request does not
pay the serving runtime's lazy-initialization cost.

## Historical Light Dual-Residency Experiment

If a single DGX Spark needs both chat lanes resident, the experiment should start from reduced residency profiles rather than from the full 128K/MTP profiles.

Recommended first target:

| Setting | `fast` | `deep` | embedding | guard | Rationale |
|---|---:|---:|---:|---:|---|
| Context | 32768 | 65536 | 8192 | 16384 | Preserve deep context; keep auxiliary contexts sufficient for their bounded inputs. |
| MTP | off | off | off | off | Save memory and reduce moving parts while validating residency. |
| KV cache budget | 8 GiB | 12 GiB | 2 GiB | 2 GiB | Cap the real pressure point instead of letting each server reserve a full lane. |
| Max response tokens | 768 | 1536 | n/a | 128 | Keep agent loops and moderation responsive. |
| Max concurrent sequences | 4 | 2 | 1 | 1 | The product is single-user; optimize for fit and latency, not concurrency. |
| GPU memory utilization | 0.42 | 0.36 | 0.06 | 0.04 | Explicit KV budgets bind capacity; utilization stays conservative for startup checks. |

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

The historical `dual-light-v1` experiment showed that both chat lanes plus small
auxiliary endpoints fit on one machine. Warmed `fast` ran around 48-50 tok/s and
warmed `deep` around 7.3 tok/s. A chat-only control left `deep` throughput
essentially unchanged. The current product profile keeps embedding and guard:
embedding at 8K with a 2G KV cap returned warmed requests around 28.5 ms;
Qwen3Guard at 16K with a 2G KV cap used 1.12 GiB for model weights and returned
warmed moderation requests in about 80-110 ms. A 32K guard context was rejected
because it needed 3.5 GiB KV.

The acceptance standard for this single-user profile is integrated task
performance, not concurrency. On 2026-05-25, the historical stack passed the
then-current 58-case real-model golden eval. Removing the reranking lane changes
the routing model stack, so the current Fast + Embedding + Guard profile
requires a fresh active 43-case real-model run before its quality can be
compared with that historical result.

### Dual-Light Test Loop

Use this loop for each tuning step:

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

Use `scripts/serve_models_compose.sh dual-light-chat` only for chat-only controls.
The historical `dual-light` profile includes fast, deep, embedding, and the
dedicated guard; it is no longer the product startup default.

Record rejected profiles too. A failed startup with logs and GPU process state is still useful evidence.

## Trade-Off Matrix

| Choice | Gains | Costs | When to choose |
|---|---|---|---|
| Single Fast with profile aliasing | One chat model process and predictable residency | Deep-specific quality is unavailable | Current single-machine product mode |
| One full lane at a time | Maximum stability and full 128K context | Lane switching is required | Targeted model evaluation |
| Light dual residency | Both lanes stay warm | Lower context, lower concurrency and no MTP/DFlash | Single-machine demos or workflows that need instant lane switching |
| `deep` priority | Keeps high-risk/code review quality higher | `fast` becomes a short-context helper | Best SparkClaw compromise on one machine |
| `fast` priority | Better daily chat responsiveness | `deep` loses long-context advantage | UI-heavy or short-turn workflows |
| Two DGX Spark machines | Full `fast` and full `deep` can stay resident | More hardware and endpoint management | Preferred long-term deployment |

## Two-Machine Plan

When two DGX Spark systems are available, split model serving by lane rather than distributing one model across both machines first:

| Machine | Services | Notes |
|---|---|---|
| DGX Spark A | `fast`, embedding, guard | Optimized for interactive latency, semantic routing and prompt moderation. |
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
- Embedding and guard availability plus Gateway semantic-router readiness.
- Golden eval result and any regression notes.

Append durable measurements to [Model baseline](../benchmarks/model_baseline.md). Keep this document focused on strategy and accepted loading profiles.
