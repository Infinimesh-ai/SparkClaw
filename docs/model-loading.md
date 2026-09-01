# SparkClaw Model Loading Plan

> Language: English | [简体中文](../zh-cn/docs/model-loading.md)

This document records the current model-loading strategy for SparkClaw on DGX Spark-class hardware. It complements the measured endpoint evidence in [Model baseline](../benchmarks/model_baseline.md) and the operational steps in [Deployment](deployment.md).

The short version: the current single-machine product runtime loads the responsive `fast` MoE chat model together with embedding, guard, Qwen3-ASR speech, and the OvisOCR2 document adapter. The logical Deep Workflow profile is temporarily aliased to the Fast endpoint, so no Deep model process starts. Historical Deep and dual-residency measurements remain below for future evaluation; they are not the current startup policy.

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
- Keep Qwen3-ASR resident as the bounded speech-transcription adapter. It is not
  a Model Router lane and accepts only the Gateway's validated audio requests.
- Keep OvisOCR2 resident as a document adapter. It is not a Model Router lane;
  it supplies bounded, untrusted text evidence for selected document images.

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

The shortcut first stops a previously running Deep container, then starts Fast,
embedding, guard, ASR, and OCR in one Compose operation. Run
`scripts/restart_runtime_compose.sh` afterward; it uses the single-Fast, ASR,
and OCR environments by default. Before changing the selected group, startup verifies
that every container exists, is running and healthy, and carries the current
Compose configuration hash. A healthy/current group is retained. If any member
is absent, stopped, unhealthy, or drifted, the complete selected group is
stopped and force-recreated. Set `SPARKCLAW_FORCE_MODEL_RECREATE=true` for the
same full refresh on a healthy group; do not use `docker start` or `docker
restart` as the product startup or recovery path.

Model checkpoints and Hugging Face metadata remain durable under `data/models`.
vLLM/TorchInductor AOT artifacts, Triton kernels, FlashInfer caches, and NVIDIA
runtime injection stay in the disposable container instance. Recreating the
group discards those process-local caches and refreshes GPU device injection
without redownloading the checkpoint. The current Fast capacity
remains at the previously exercised 32K context and 8 GiB KV cache rather than
claiming an unmeasured capacity increase from the memory freed by Deep. Model
startup waits for Docker health. Fast health includes one production-shaped
chat completion per model process: the current synthetic input is about 3.4K
tokens on Qwen3.6 and forces a 480-token decode, covering the Tree-routing cold
path before user traffic is admitted. Guard health retains its smaller bounded
chat completion. The readiness helper is copied into a local derivative of the
configured vLLM image, so health checks do not depend on a source-file bind
mount from the checkout. Both probes keep their completion marker on a dedicated
container-local tmpfs and bind it to the model, warmup shape, and current
server-process start time. Marker persistence is best-effort after successful
warmup; if the tmpfs is unwritable, readiness succeeds but a later probe may
repeat the warmup. Periodic checks use the lightweight model listing only after
the exact process is warm and its marker is stored. Embedding keeps the fixed
2 GiB KV budget but admits up to 128 short sequences so the 110-entry semantic
corpus can be embedded as one startup request within the 20-second index bound.

Qwen3-ASR is a speech adapter, not a Model Router lane. The `single-fast`
product profile loads `Qwen/Qwen3-ASR-0.6B` through `docker/compose.asr.yaml` on
port `8006`; the derivative vLLM image adds bounded audio dependencies, while
the model itself uses the shared Hugging Face cache. The service assigns a
fixed 2 GiB KV cache: utilization-only allocation calculated `-10.24 GiB`
available after encoder profiling during a five-service cold start. Gateway
enables the OpenAI-compatible transcription adapter from the matching ASR
environment. With the fixed cache, vLLM reported 44.55 GiB initially free,
18,720 cached tokens, and 2.29x estimated concurrency at 8K; ASR became healthy
in 92 seconds during the complete five-service force-recreate, and a one-second
WAV transcription smoke request completed successfully.

OvisOCR2 is likewise a document adapter rather than a Model Router lane. The `single-fast`
product profile loads `ATH-MaaS/OvisOCR2` with Fast, embedding, guard, and ASR through
`docker/compose.ocr.yaml` on port `8007`. The older `single-fast-with-ocr`
command remains an alias for the same five-service startup. That
overlay pins the model's documented vLLM `0.22.1` runtime, disables thinking,
uses deterministic generation, assigns a fixed 2 GiB KV cache, and keeps
response, concurrency, and queue limits in Gateway. On the GB10, combined
startup was validated only after stopping the already-resident model services
and reloading Fast, embedding, guard, and OCR together. The current product
startup extends that atomic group with ASR. Adding OCR to the
already-resident stack failed during CUDA initialization. OvisOCR2 then loaded
1.72 GiB of weights, but `gpu-memory-utilization=0.12` alone calculated
`-1.96 GiB` of available KV cache; the explicit 2 GiB KV cache is therefore a
required part of this profile, not optional tuning. With that cap, vLLM
reported 53.26 GiB initially free, a 2 GiB cache with 164,352-token capacity,
and 5.02x estimated 32K concurrency. This validates combined startup, not a
steady-state residency budget or OCR quality baseline. A concurrent image and
scanned-PDF smoke call succeeded, but broader quality measurements are still
required before claiming model quality.

## IMMS E2 Evidence Replica Boundary

ProjectGroup-2 decision 0026 was accepted by InfiniCenter commit `42bc8a4`.
SparkClaw also accepts IMMS ADR 0019 proposal
`ac9a33d3c55f3c9d55af21e91586902f530aa39f`, exact document
`c073202cff039dee23211ec6785464ef093d13992cfeee16840547cfa7001165/10006`,
with these exact limits:

- E2 is an evidence-only replica profile, not part of `single-fast-v1` and not
  a production user-data processor. Only synthetic evaluation inputs may use
  its public route. Real Source or Memory content must remain on the future
  GB10-local loopback service.
- The accepted target is only
  `Qwen/Qwen3-Reranker-4B@22e683669bc0f0bd69640a1354a6d0aebcfeede5`,
  served as `imms-qwen3-reranker-4b-e2` with BF16, no quantization, tensor
  parallel 1, maximum model length 8192, maximum sequences 1, seed 0, eager
  execution, and no prefix caching, speculative decoding, or LoRA. Deployment
  still must not start until ADR 0019 is canonically accepted.
- The vLLM completion protocol can represent ADR 0019 without a scoring
  adapter: `/v1/completions` accepts a direct integer token-ID array and the
  requested `allowed_token_ids`, `logprob_token_ids`, `add_special_tokens`,
  `truncate_prompt_tokens`, and token-ID response controls. The future pinned
  image/version must prove those exact fields in a content-free deployment
  smoke transcript before any calibration request; text-only labels, server
  re-tokenization, fallback, or retry are not admissible substitutes.
- An E2 run is admissible only when it binds an immutable Hugging Face model
  revision, an immutable vLLM image digest and reported version, the complete
  serving configuration, and a deployment revision. Matching mutable model
  names in `/models` before and after a run detect mid-run drift but are not an
  immutable pin by themselves.
- Repository-managed model changes are operator-driven. For an endpoint that
  has entered an accepted IMMS evidence scope, SparkClaw will notify IMMS by
  ProjectGroup-2 status or inbox before intentionally changing the model,
  quantization, serving image, or scoring behavior. A separately hosted route
  with an automatic or unknown update mechanism is not E2-admissible until it
  exposes a resolvable immutable deployment revision.
- The reranker lane was removed from the current product profile on 2026-07-24.
  A hosted reranker route or the proposed 4B evidence profile therefore must
  not be represented as a current `single-fast-v1` dependency. Its differences
  from the eventual E3 GB10 profile are currently unknown and must be measured
  or explicitly retained as unknown before E3 admission.

This review creates no deployment by itself and no SLA, availability,
dedicated-capacity, Gateway wire, or runtime integration obligation. Outages
only delay new evidence runs and do not invalidate already sealed evidence.

### 2026-09-01 live deployment discovery (not an E2 pin)

The operator subsequently deployed public `reranker` and `embedding` routes. A
content-free discovery at `2026-09-01T05:09:24Z` used only `GET /v1/models`,
`GET /version`, `GET /health`, and `GET /metrics`; it sent no completion,
embedding, calibration, held-out, Source, or Memory request.

- The reranker route reported `id=sparkclaw-reranker`,
  `root=Qwen/Qwen3-Reranker-4B`, `max_model_len=8192`, and vLLM `0.23.0`.
  The normalized selected `/models` projection is
  `90a38557bf407359a8f6c32d24828ed379b4536c74daa2fa7cd63c76705d5e8b/114`;
  the normalized version response is
  `075e267960c71451371b4267a2b98efd65f045237544f950a51ceef72ad63700/20`.
- The embedding route reported `id=sparkclaw-embedding`,
  `root=Qwen/Qwen3-Embedding-8B`, `max_model_len=8192`, and the same vLLM
  version. Its normalized selected `/models` projection is
  `545cb9fb49dc73b784d2bfbbb0a5303c24c312f3108bfed23956cf16bd9902cb/116`.
  This identity response does not prove the operator-described FP8 weight
  representation, and the 8B embedding route is not the ADR 0019 scorer
  critical path.
- The reranker route is not E2-admissible in this state. Its served name differs
  from the accepted `imms-qwen3-reranker-4b-e2`; the public identity surface
  does not resolve the exact Hugging Face revision, complete artifact catalog,
  container image digest, deployment revision, CUDA/driver identity, dtype,
  quantization, tensor parallelism, maximum sequences, seed, eager mode, or
  disabled feature flags. At the same observation point, public metrics
  reported `prefix_cache_queries_total=41961` and
  `prefix_cache_hits_total=576`, while ADR 0019 requires prefix caching to be
  disabled.

This discovery is a fail-closed external-boundary result, not the canonical
immutable deployment manifest/profile or its acceptance. IMMS calibration
authority remains unissued and no calibration request may be sent until the
operator supplies the missing immutable pins, the profile matches ADR 0019,
and the reviewed content-free smoke contract is satisfied.
IMMS `d16eb56` independently accepted this deployment-admission STOP without
changing the calibration counter, and InfiniCenter `7eab880` recorded the
cross-repository result.

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
- ASR startup and transcription latency, plus OCR startup, page latency,
  scanned-PDF recovery, and malformed or incomplete Markdown behavior.
- Golden eval result and any regression notes.

Append durable measurements to [Model baseline](../benchmarks/model_baseline.md). Keep this document focused on strategy and accepted loading profiles.
