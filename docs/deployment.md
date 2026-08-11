# SparkClaw Deployment

> Language: English | [简体中文](../zh-cn/docs/deployment.md)

This document is the current deployment guide for local development, Docker Compose and DGX Spark model serving. It replaces the older Docker implementation plan and DGX handoff notes.

## Prerequisites

- NVIDIA DGX Spark with its GB10 GPU, Linux/ARM64, and at least 100 GiB of
  system/unified memory. Ubuntu 24.04 is the validated OS.
- Docker Engine, the Docker Compose plugin, the NVIDIA driver/container toolkit,
  `curl`, and outbound access to container registries and Hugging Face.
- At least 125 GiB of free space for a cold model/image cache. The deployment
  script computes the remaining requirement when part of the cache exists.
- A Hugging Face token for model downloads. Do not commit the generated `.env`.

Node.js 26/npm 11 and Go 1.25 are required for host-side development, but not
for the containerized deployment path.

## One-Command DGX Spark Deployment

Starting from a prepared DGX Spark host:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Chiiz0/SparkClaw/main/install.sh | bash
```

The project website can serve the repository's top-level `install.sh` unchanged
and use its own HTTPS URL in this command. Do not publish the installer over
plain HTTP.

The streamed bootstrap and deployment entrypoints:

1. Clone the configured branch/tag into `$HOME/SparkClaw`, or fast-forward an
   existing clean checkout. A checkout with local changes or a divergent history
   is left untouched and reported as an error.
2. Reattach stdin from the curl pipe to `/dev/tty`, allowing the Hugging Face
   token prompt to remain hidden and interactive.
3. Require Linux/ARM64, NVIDIA GB10, at least 100 GiB of memory, Docker
   Compose, `nvidia-smi`, and sufficient free space.
4. Create or preserve a mode-`0600` `.env`, accept a Hugging Face token
   without echoing it, and align bind-mounted data with the current user.
5. Use vLLM's Hugging Face integration to download Fast, embedding, guard, and
   OvisOCR2 into the shared `data/models` cache.
6. Wait for model readiness and Fast/Guard warmup, build Gateway, Sandbox
   Runner, and WebChat, then verify both Gateway and WebChat.

The first run downloads roughly 70-85 GiB of model data plus container images
and can take hours. Model health and joint startup share a three-hour default
window. Set `SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS` to a larger positive
number when the download link is slower. Later runs reuse the cache and healthy
containers.

For a non-interactive install, export the token before starting the pipeline;
the deployment persists it only in the ignored, mode-`0600` local environment
file:

```bash
export HF_TOKEN=hf_example
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Chiiz0/SparkClaw/main/install.sh | bash
```

Install/update the repository and run only deployment preflight with:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Chiiz0/SparkClaw/main/install.sh | \
  bash -s -- --check
```

The bootstrap defaults to `main` and `$HOME/SparkClaw`. Pin a release or use a
different installation directory by setting `SPARKCLAW_GIT_REF` or
`SPARKCLAW_INSTALL_DIR` on the `bash` process. To deploy directly from an
already-cloned repository, run:

```bash
bash scripts/deploy.sh
```

## Compose Profiles

| Profile | Purpose |
|---|---|
| `dev` | Development-oriented runtime. |
| `eval` | Gateway plus evaluator and data services. |
| `compat` | Gateway connected to externally managed OpenAI-compatible endpoints. |
| `models-local` | PostgreSQL 18/pgvector, MinIO, sandbox-runner, Gateway, WebChat and optional vLLM lanes. |

WebChat binds host port `18790` to `0.0.0.0` by default for LAN access. Gateway,
models, state services, and the sandbox runner remain bound to localhost.
Containers communicate over the private `sparkclaw_internal` network.

## Product Runtime

The deployment entrypoint ultimately delegates to the same product startup
command exposed at the repository root. Operators with an existing `.env` can
invoke it directly to load the resident `single-fast-v1` model group and the
PostgreSQL-backed control plane:

```bash
npm start
```

The entrypoint delegates model ownership to `serve_models_compose.sh
single-fast`, which treats Fast, embedding, guard, and OCR as one resident
group. If one member is missing, unhealthy, or has a stale Compose identity,
all four are stopped and loaded together. The command waits for every model
health check, including the configured Fast and Guard completion warmups,
before it starts PostgreSQL, Sandbox Runner, Gateway, and WebChat. PostgreSQL
must become healthy before Gateway is recreated. Gateway then verifies
`model_mode=external` with the PostgreSQL state backend; the logical Deep
profile aliases the Fast endpoint. Set `SPARKCLAW_MODEL_MODE=mock` explicitly
only for isolated deterministic debugging or evaluation.

Check status:

```bash
docker ps --filter name=sparkclaw
curl -fsS http://127.0.0.1:18789/readyz
bash scripts/doctor.sh
```

Open WebChat locally at [http://127.0.0.1:18790](http://127.0.0.1:18790), or
from another LAN device at `http://<host-lan-ip>:18790`.

Run golden eval against the Dockerized Gateway:

```bash
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

The `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS` setting is intentionally explicit. It lets eval fixtures work while keeping `browser.read` closed to private hosts by default.

## Host Development Runtime

The standard development runtime on the validated DGX Spark host is the
containerized external-model/OCR/PostgreSQL topology:

```bash
npm run dev
```

Use `npm run dev:gateway` or `npm run dev:webchat` to rebuild a single
application container without switching the runtime back to mock/file mode.

For isolated host-process debugging only, run the mock/file Gateway and Vite
server in separate terminals:

```bash
npm run dev:gateway:host
npm run dev:webchat:host
```

The host WebChat dev server listens on `0.0.0.0:18790` and proxies API requests
to the loopback-only Gateway. Set `SPARKCLAW_API_TOKEN` and
`VITE_SPARKCLAW_API_TOKEN` to the same value for protected host-process runs.

## LocalMind MCP

LocalMind access is opt-in. Add an `mcp_servers.localmind` block to the active
SparkClaw JSON configuration as documented in
[External integrations](integrations.md), then provide the referenced values in
the deployment environment:

```bash
LOCALMIND_MCP_URL=https://localmind.example/api/workspaces/<workspace-id>/mcp
LOCALMIND_MCP_TOKEN=<workspace-bound-token>
```

`docker/compose.yaml` forwards both variables to Gateway, but the empty
`mcp_servers` default means environment values alone do not enable the
integration. Keep the token out of committed configuration and use a read-only
LocalMind credential first. Restart Gateway after changing the JSON entry; it
performs an initial scope discovery and then refreshes it at the configured
interval.

For a host LocalMind reached from containerized Gateway, replace `localhost`
with `host.docker.internal`. A LocalMind service attached to
`sparkclaw_internal` can use its Compose service name. Public endpoints must use
HTTPS; private or container HTTP additionally requires
`allow_private_http: true`. The endpoint path must remain exactly
`/api/workspaces/<workspace-id>/mcp`.

## ISCP Bridge Process

The JingSi App integration runs as a separate host process so it can use the GB10
operating-system keyring and reach only the loopback Gateway. Enable Gateway token
auth, provision the identity and Cloud-issued enrollment bundle, then run:

```bash
cd services/gateway
mkdir -p ../../bin
go build -o ../../bin/sparkclaw-iscp-bridge ./cmd/iscp-bridge
../../bin/sparkclaw-iscp-bridge run -config ../../configs/iscp-bridge.example.json
```

The same Gateway bearer value, or a dedicated paired-client token, must be stored
in the Bridge `gateway.token` file with mode `0600`. The enrollment bundle is also
`0600`; the production Ed25519 identity key stays in the system keyring. Install
the binary under a service manager with restart-on-failure, but do not restart on
an explicit device-revocation error until a new enrollment bundle is installed.

See [ISCP Bridge](iscp-bridge.md) for enrollment, schema, credential rotation,
mock mode, and the exact security boundary.

## State Backends

File state used by isolated host/mock runs:

```text
data/memory/gateway-state.json
```

Useful options:

```bash
SPARKCLAW_STATE_BACKEND=memory
SPARKCLAW_STATE_PATH=/path/to/state.json
SPARKCLAW_STATE_ENCRYPT_AT_REST=true
SPARKCLAW_STATE_ENCRYPTION_KEY_FILE=/path/to/key
```

Postgres-backed state:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d postgres

SPARKCLAW_STATE_BACKEND=postgres \
SPARKCLAW_STATE_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

Gateway applies the active core schema at startup. The project-standard data service image remains PostgreSQL 18 with pgvector available, but Gateway no longer creates or queries a document-chunk/vector schema while workspace knowledge/RAG is deferred.

PostgreSQL 18 stores clusters under a major-version-specific subdirectory, so
Compose mounts the versioned `sparkclaw_pg18` volume at
`/var/lib/postgresql`. An existing PostgreSQL 17 `sparkclaw_pg` volume created
with the old `/var/lib/postgresql/data` mount must be backed up and migrated
with `pg_dump`/`pg_restore`. Do not attach the old data directory directly to
PostgreSQL 18 or delete it to force a clean start.

## Artifact Storage

The default artifact backend is filesystem object storage under `data/artifacts/{bucket}/...`. Use S3-compatible storage by setting:

```bash
SPARKCLAW_ARTIFACT_BACKEND=s3
SPARKCLAW_S3_ENDPOINT=http://127.0.0.1:9000
SPARKCLAW_S3_ACCESS_KEY=sparkclaw
SPARKCLAW_S3_SECRET_KEY=sparkclaw-local
```

Compose provides MinIO:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d minio minio-init
```

Artifacts include tool observations, browser snapshots, generated documents and
media, memory exports, patch rollback files and eval failure archives.

## Sandbox Runner

For host binary runs, Gateway can use `SPARKCLAW_SANDBOX_BACKEND=local-docker`.

Compose uses a standalone sandbox runner:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d sandbox-runner
```

Standalone runner boundary outside Compose:

```bash
sparkclaw-sandbox-runner
SPARKCLAW_SANDBOX_BACKEND=http \
SPARKCLAW_SANDBOX_RUNNER_URL=http://127.0.0.1:18889 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

When the runner talks to a host Docker socket, set `SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT` and `SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT` if paths differ between host and container.


## DGX Spark Data Services

Start durable state, artifacts, sandbox, Gateway and WebChat:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d \
  postgres minio minio-init sandbox-runner gateway webchat
```

For model-backed operation, recreate Gateway in external mode after the selected endpoints are healthy:

```bash
scripts/restart_runtime_compose.sh
```

Use this script instead of a plain `docker compose up --force-recreate gateway webchat` for the durable product runtime. It loads `docker/env/sparkclaw.single-fast.env` and `docker/env/sparkclaw.ocr.env` after `.env`, then stacks `docker/compose.ocr.yaml`. This selects PostgreSQL, keeps both logical chat profiles mapped to Fast, and enables the document OCR adapter against the co-resident OCR service. The script starts and waits for PostgreSQL when Gateway is requested, then checks `/readyz` and exits non-zero unless Gateway reports `model_mode=external` and `state_backend=postgres`. Set `SPARKCLAW_RUNTIME_ENV` explicitly to use another chat/runtime profile; the OCR environment remains part of this product runtime.

When the host has a resolvable X11/XWayland display, the script additionally stacks the `docker/compose.visible-browser.yaml` overlay so login handoffs can open a visible Chromium on the owner's desktop. On a headless host it starts the same stack without the overlay; hidden browser automation remains available and the base compose file grants Gateway no access to any host display.

## DGX Spark Model Services

Host-side vLLM scripts:

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Compose vLLM services:

```bash
scripts/serve_models_compose.sh single-fast
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light
scripts/serve_models_compose.sh dual-light-asr
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding
scripts/serve_models_compose.sh guard
scripts/serve_models_compose.sh asr
scripts/serve_models_compose.sh all
scripts/serve_models_compose.sh all-with-asr
```

With no argument, `serve_models_compose.sh` also selects `single-fast`. This is
the current product startup path: it stops a previously running Deep container
and starts Fast, embedding, guard, and OCR together with the single-Fast and
OCR environments. The older `single-fast-with-ocr` name is an alias for this
same startup. Deep and dual-light commands are explicit test/benchmark
entrypoints. The command waits for every selected service to become healthy.
Fast is not healthy until a bounded production-shaped `/chat/completions`
request succeeds. On the current Qwen3.6 tokenizer it carries about 3.4K input
tokens and forces a 480-token decode, so startup absorbs the long-prompt and
generation cold path seen by Tree routing. Guard separately requires its small
bounded completion. Each marker includes the current model-process start time,
so stopping and starting an existing container cannot reuse readiness from the
previous process; after one-time warmup, periodic checks use the lightweight
model listing endpoint. If any member of the four-service product group is
absent or unhealthy, the shortcut stops all four before reloading them together;
a Compose configuration-hash change does the same. It never adds or recreates
one model alone inside the resident product group. The single-Fast embedding
endpoint admits 128 short sequences under its fixed 2 GiB KV budget so the
110-entry startup index completes within its 20-second bound.

Default endpoints:

| Endpoint role | Served name | Endpoint |
|---|---|---|
| fast | `sparkclaw-fast` | `http://127.0.0.1:8001/v1` |
| deep | `sparkclaw-deep` | `http://127.0.0.1:8002/v1` |
| embedding | `sparkclaw-embedding` | `http://127.0.0.1:8003/v1` |
| guard | `Qwen/Qwen3Guard-Gen-0.6B` | `http://127.0.0.1:8005/v1` |
| asr | `sparkclaw-asr` | `http://127.0.0.1:8006` |
| OCR adapter | `sparkclaw-ocr` | `http://127.0.0.1:8007/v1` |

Check endpoints:

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8005/v1/models
curl -fsS http://127.0.0.1:8006/v1/models
curl -fsS http://127.0.0.1:8007/v1/models
```

Port `8002` is available only after an explicit `deep`, `dual-light`, or `all`
startup and is not part of the current single-Fast readiness check.

Important environment variables:

- `SPARKCLAW_VLLM_IMAGE`
- `SPARKCLAW_FAST_MODEL_ID`, `SPARKCLAW_FAST_MODEL`, `SPARKCLAW_FAST_MAX_MODEL_LEN`, `SPARKCLAW_FAST_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_FAST_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_FAST_MAX_NUM_SEQS`, `SPARKCLAW_FAST_SPECULATIVE_CONFIG`
- `SPARKCLAW_DEEP_MODEL_ID`, `SPARKCLAW_DEEP_MODEL`, `SPARKCLAW_DEEP_MAX_MODEL_LEN`, `SPARKCLAW_DEEP_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_DEEP_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_DEEP_MAX_NUM_SEQS`, `SPARKCLAW_DEEP_SPECULATIVE_CONFIG`
- `SPARKCLAW_EMBEDDING_MODEL_ID`, `SPARKCLAW_EMBEDDING_MODEL`, `SPARKCLAW_EMBEDDING_MAX_MODEL_LEN`, `SPARKCLAW_EMBEDDING_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_EMBEDDING_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_EMBEDDING_MAX_NUM_SEQS`
- `SPARKCLAW_GUARD_MODEL_ID`, `SPARKCLAW_GUARD_MODEL`, `SPARKCLAW_GUARD_SERVED_NAME`, `SPARKCLAW_GUARD_MAX_TOKENS`, `SPARKCLAW_GUARD_CONTEXT_TOKENS`, `SPARKCLAW_GUARD_MAX_MODEL_LEN`, `SPARKCLAW_GUARD_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_GUARD_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_GUARD_MAX_NUM_SEQS`
- `SPARKCLAW_ASR_MODEL_ID`, `SPARKCLAW_ASR_SERVED_NAME`, `SPARKCLAW_ASR_MAX_MODEL_LEN`, `SPARKCLAW_ASR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_ASR_MAX_NUM_SEQS`, `SPARKCLAW_ASR_DTYPE`
- `SPARKCLAW_SPEECH_ENABLED`, `SPARKCLAW_SPEECH_BASE_URL`, `SPARKCLAW_SPEECH_ALLOWED_HOSTS`, `SPARKCLAW_SPEECH_MODEL`, `SPARKCLAW_SPEECH_TIMEOUT_SECONDS`, `SPARKCLAW_SPEECH_MAX_AUDIO_SECONDS`, `SPARKCLAW_SPEECH_MAX_UPLOAD_BYTES`
- `SPARKCLAW_OCR_ENABLED`, `SPARKCLAW_OCR_BASE_URL`, `SPARKCLAW_OCR_ALLOWED_HOSTS`, `SPARKCLAW_OCR_MODEL`, `SPARKCLAW_OCR_TIMEOUT_SECONDS`, `SPARKCLAW_OCR_MAX_UPLOAD_BYTES`, `SPARKCLAW_OCR_MAX_OUTPUT_BYTES`, `SPARKCLAW_OCR_MAX_TOKENS`, `SPARKCLAW_OCR_MAX_CONCURRENCY`, `SPARKCLAW_OCR_MAX_PENDING`
- `SPARKCLAW_OCR_IMAGE`, `SPARKCLAW_OCR_MODEL_ID`, `SPARKCLAW_OCR_SERVED_NAME`, `SPARKCLAW_OCR_MAX_MODEL_LEN`, `SPARKCLAW_OCR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_OCR_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_OCR_MAX_NUM_SEQS`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN`

Use `*_MODEL_ID` for the Hugging Face checkpoint loaded by the serving container and `*_MODEL` for the OpenAI-compatible served name sent by Gateway.

### Dedicated Qwen3Guard

The guard lane uses the public generative checkpoint
`Qwen/Qwen3Guard-Gen-0.6B`; `Qwen/Qwen3Guard-0.6B` is not a valid public
checkpoint ID. Start only this endpoint with:

```bash
SPARKCLAW_MODEL_LOADING_PROFILE=single-fast scripts/serve_models_compose.sh guard
curl -fsS http://127.0.0.1:8005/v1/models
```

The single-GB10 `single-fast` profile limits guard to 16K context, 2 GiB KV
cache, one sequence and eager execution. Qwen3Guard returns its native
`Safety: Safe|Unsafe|Controversial` and `Categories:` format; Gateway maps
those severities to `allow`, `block` and `review`. Because SparkClaw has no
human safety-review queue, both `review` and `block` stop the run before routing
or tool execution. If the external endpoint is unavailable, Gateway records
`mock=true` and uses the local heuristic fallback. Compose allows the initial
real-inference readiness probe up to 110 seconds and does not report the Guard
container healthy until that probe has produced a non-empty completion.

### OvisOCR2 Document OCR

The document OCR adapter uses
[`ATH-MaaS/OvisOCR2`](https://huggingface.co/ATH-MaaS/OvisOCR2) through an
OpenAI-compatible vLLM chat-completions endpoint. It parses page images into
Markdown while preserving readable order, formulas, and tables. Fast remains
the visual-semantics and Workflow-reasoning model; OCR output is untrusted
document evidence and never selects a model lane or authorizes an edit.

The overlay pins vLLM `0.22.1`, exposes port `8007` only on loopback, uses an
explicit 2 GiB KV cache budget, and shares the Hugging Face cache. The default
`single-fast` command starts OCR in the same Compose operation as Fast,
embedding, and guard:

```bash
scripts/serve_models_compose.sh single-fast
curl -fsS http://127.0.0.1:8007/v1/models
```

Run Gateway and WebChat with the matching OCR adapter configuration:

```bash
scripts/restart_runtime_compose.sh
```

For host-side doctor checks, keep the Compose service URL for Gateway and
override only the check destination:

```bash
set -a
. docker/env/sparkclaw.ocr.env
set +a
SPARKCLAW_OCR_BASE_URL=http://127.0.0.1:8007/v1 scripts/doctor.sh
```

OCR is enabled in the current single-Fast product runtime. Selected Office/PDF
images receive bounded OCR Markdown; scanned PDF pages invoke it automatically.
Page rendering is limited to eight pages, 4 MiB per rendered page, and 16 MiB
total per PDF read. A disabled,
busy, timed-out, malformed, or incomplete OCR response is reported as partial
evidence. Combined startup on the GB10 has been validated after stopping all
resident model services and invoking the joint startup; adding OCR alone to the
already-resident stack failed during CUDA initialization. Keep the explicit 2 GiB KV cache: utilization-based
allocation alone produced a negative available-cache calculation. One
concurrent image and scanned-PDF smoke call completed successfully, but it is
not an OCR quality baseline; broader document measurements are still required.

### Qwen3-ASR From ModelScope

SparkClaw speech uses the OpenAI-compatible transcription endpoint. Qwen3-ASR supports vLLM serving and the OpenAI transcription API, and the [official Qwen3-ASR README](https://github.com/QwenLM/Qwen3-ASR) recommends ModelScope downloads for users in Mainland China. On one GB10 with the validated `dual-light` residency profile, start with `Qwen/Qwen3-ASR-0.6B`; switch to `Qwen/Qwen3-ASR-1.7B` only after measuring memory and latency with the local fast, deep, and embedding services resident.

Download the ASR checkpoint into the shared model cache:

```bash
python3 -m pip install -U modelscope
mkdir -p data/models/modelscope/Qwen3-ASR-0.6B
modelscope download --model Qwen/Qwen3-ASR-0.6B --local_dir data/models/modelscope/Qwen3-ASR-0.6B
```

The ASR compose override builds a small derivative of the local vLLM image that adds audio dependencies without changing the main text-model image:

- Compose: `docker/compose.asr.yaml`
- Environment: `docker/env/sparkclaw.asr.env`
- Image recipe: `docker/images/asr-vllm.Dockerfile`
- Default served model: `sparkclaw-asr`
- Default model path in container: `/models/modelscope/Qwen3-ASR-0.6B`

Start ASR by itself:

```bash
scripts/serve_models_compose.sh asr
```

Start the validated residency profile with ASR:

```bash
scripts/serve_models_compose.sh dual-light-asr
```

Run Gateway and WebChat with speech enabled:

```bash
docker compose \
  --env-file docker/env/sparkclaw.dual-light.env \
  --env-file docker/env/sparkclaw.asr.env \
  -f docker/compose.yaml \
  -f docker/compose.dual-light.yaml \
  -f docker/compose.asr.yaml \
  --profile models-local up -d gateway webchat
```

Check the ASR endpoint from the host:

```bash
curl -fsS http://127.0.0.1:8006/health
curl -fsS http://127.0.0.1:8006/v1/models
curl -fsS http://127.0.0.1:8006/v1/audio/transcriptions \
  -F model=sparkclaw-asr \
  -F response_format=json \
  -F file=@/path/to/sample.wav
```

For host-side doctor checks, keep the container URL in `docker/env/sparkclaw.asr.env` for Gateway but override the base URL to loopback:

```bash
set -a
. docker/env/sparkclaw.asr.env
set +a
SPARKCLAW_SPEECH_BASE_URL=http://127.0.0.1:8006 scripts/doctor.sh
```

Validated DGX Spark notes from 2026-05-24:

- NVIDIA GB10 and driver `580.159.03` were visible on host and from CUDA containers.
- `vllm/vllm-openai:cu130-nightly` worked on arm64.
- `Qwen/Qwen3.6-27B-FP8`, `Qwen/Qwen3.6-35B-A3B-FP8`, `Qwen/Qwen3-Embedding-0.6B`, and `Qwen/Qwen3Guard-Gen-0.6B` were validated.
- Full-context fast+deep dual residency did not fit with both chat lanes at 128K context and MTP enabled. Operate one 128K/MTP chat lane at a time, route both Gateway profiles to the loaded lane for evals, or reduce context/MTP and re-measure.

Current single-Fast product startup:

```bash
scripts/serve_models_compose.sh single-fast
scripts/restart_runtime_compose.sh
```

This applies the single-Fast and OCR environments plus the bounded service
settings from `docker/compose.dual-light.yaml` and `docker/compose.ocr.yaml`.
Fast, embedding, guard, and OCR start together. Gateway sends both logical chat
profiles to `sparkclaw-fast` and uses `sparkclaw-ocr` for document OCR.

Historical light dual-residency experiment:

```bash
scripts/serve_models_compose.sh dual-light
python3 scripts/record_model_loading.py --profile dual-light-v1
```

The `dual-light` shortcut applies `docker/env/sparkclaw.dual-light.env` and `docker/compose.dual-light.yaml`: fast 32K with 8G KV cache, deep 64K with 12G KV cache, embedding 8K with 2G KV cache, and guard 16K with 2G KV cache. MTP is off and sequence concurrency is low. Start this full profile before running Gateway in external mode.

Use `dual-light-chat` only when intentionally measuring chat lanes without auxiliary endpoints.

Run the repeatable endpoint benchmark:

```bash
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md
```

Run real-model golden eval:

```bash
SPARKCLAW_EXPECT_REAL_MODELS=1 \
SPARKCLAW_MODEL_MODE=external \
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

The historical validated real-model run completed 58 golden cases. The active matrix now contains 43 cases and should be rerun after model-stack changes. See [model_baseline.md](../benchmarks/model_baseline.md) for benchmark rows and operating notes.

## Backup And Restore

Back up these paths or volumes:

- `.env` secret template values, stored outside git
- `data/memory`
- `data/traces`
- `data/artifacts`
- `data/workspaces`
- Postgres volume `sparkclaw_pg18`
- MinIO volume `sparkclaw_minio`
- `data/models` if model cache reuse matters

For Postgres:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml exec postgres \
  pg_dump -U sparkclaw sparkclaw > sparkclaw.sql
```

For filesystem state, stop Gateway before copying state files if possible.

## Upgrade Flow

1. Save or export important state.
2. Pull or apply code changes.
3. Rebuild images:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local build
```

4. Start the target profile.
5. Run `bash scripts/doctor.sh`.
6. Run mock golden eval.
7. For DGX Spark model changes, run endpoint checks and append a new benchmark section.

### Behavior changes to check when upgrading past 2026-07-30

- Visible-browser login handoffs now require stacking the
  `docker/compose.visible-browser.yaml` overlay; the base compose file no
  longer exposes the host X11 socket. `scripts/restart_runtime_compose.sh`
  applies the overlay automatically when a display resolves.
- Telegram and Weixin now both ship disabled in typed config, Compose, and the
  example environment. Enable a channel from WebChat before account setup.
  `SPARKCLAW_TELEGRAM_ENABLED` and
  `SPARKCLAW_WEIXIN_NOTIFICATION_ENABLED` only provide the initial value when
  no persisted owner choice exists; bindings and credentials never auto-enable
  a channel.
- The transitional skills registry was removed, including `GET /api/skills`
  and the `skills` config section; workflows are the only execution path.
- Guard replies that parse to no recognizable verdict resolve to a
  non-blocking `unknown` verdict recorded as a `guard.verdict_unknown`
  audit event; explicit `review`/`block` verdicts still stop the run.
  Config-less boots now run with model thinking disabled, matching the
  shipped default config.
- Runtime budget keys split into `workflow_stage_max_*` and
  `workflow_run_max_*` (legacy `workflow_step_max_*`/`react_max_*` keys
  still map; see [Workflow execution](workflow-execution.md)).

## Secure Defaults

- Expose only WebChat on `0.0.0.0:18790`; keep Gateway and other service ports
  bound to `127.0.0.1`.
- Set `SPARKCLAW_API_TOKEN` before sharing WebChat on a LAN or shared machine.
- Keep Gateway unauthenticated only for local development.
- Keep dangerous and reversible tools approval-gated.
- Keep shell execution sandboxed and network-disabled.
- Treat browser/email/file observations as untrusted.
- Keep the host desktop closed to containers: the base compose file mounts no
  X11 socket, and the `docker/compose.visible-browser.yaml` overlay belongs
  only on the trusted single-owner desktop runtime that needs visible login
  handoffs.
- Keep `.env`, model weights, state encryption keys and downloaded data out of git.
- Scan diffs for tokens before handoff.

## Troubleshooting

| Symptom | Check |
|---|---|
| Docker permission denied | Use `sudo -n docker ...` or add the user to the Docker group. |
| Golden eval browser step fails | Start Gateway with `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal` for Docker eval or `127.0.0.1` for host eval. |
| Model returns reasoning but no answer | Set `SPARKCLAW_MODEL_DISABLE_THINKING=true`. |
| Postgres vector extension unavailable | SparkClaw falls back to JSON vectors and Gateway-side hybrid scoring. |
| 128K fast+deep does not fit | Run one chat lane at a time or lower context/MTP and re-benchmark. |
