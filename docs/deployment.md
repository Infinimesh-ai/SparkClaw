# SparkClaw Deployment

> Language: English | [简体中文](../zh-cn/docs/deployment.md)

This document is the current deployment guide for local development, Docker Compose and DGX Spark model serving. It replaces the older Docker implementation plan and DGX handoff notes.

## Prerequisites

- Ubuntu 24.04 or another Linux host with Docker and Docker Compose.
- NVIDIA container runtime for DGX Spark model serving.
- Node.js 24+ and npm 11+ for host-side WebChat builds.
- Go 1.25 for host-side Gateway work, or Docker access for the Go builder fallback.
- A Hugging Face token in local `.env` for model downloads. Do not commit `.env`.

Create the local environment file:

```bash
cp docker/env/sparkclaw.example.env .env
```

Set `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN` inside `.env`. The token is passed only to model-serving containers.

## Compose Profiles

| Profile | Purpose |
|---|---|
| `minimal` | Gateway + WebChat with mock model routing. Best first run. |
| `dev` | Development-oriented runtime. |
| `eval` | Gateway plus evaluator and data services. |
| `compat` | Gateway connected to externally managed OpenAI-compatible endpoints. |
| `models-local` | PostgreSQL 18/pgvector, MinIO, sandbox-runner, Gateway, WebChat and optional vLLM lanes. |

WebChat binds host port `18790` to `0.0.0.0` by default for LAN access. Gateway,
models, state services, and the sandbox runner remain bound to localhost.
Containers communicate over the private `sparkclaw_internal` network.

## Minimal Local Runtime

Start the mock-mode control plane:

```bash
sudo -n env SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d --build
```

Check status:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal ps
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

Run Gateway and WebChat directly:

```bash
npm install
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
npm --workspace @sparkclaw/webchat run dev
```

The host WebChat dev server also listens on `0.0.0.0:18790` and proxies API
requests to the loopback-only Gateway.

Use token auth for local protected runs:

```bash
SPARKCLAW_API_TOKEN=change-me go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
VITE_SPARKCLAW_API_TOKEN=change-me npm --workspace @sparkclaw/webchat run dev
```

If `VITE_SPARKCLAW_API_TOKEN` is not set, WebChat prompts after the first unauthorized response.

## State Backends

Default file state:

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

Artifacts include tool observations, browser snapshots, knowledge indexes, memory exports, patch rollback files and eval failure archives.

## Sandbox Runner

For host binary runs, Gateway can use `SPARKCLAW_SANDBOX_BACKEND=local-docker`.

Compose uses a standalone sandbox runner:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d sandbox-runner
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

Use this script instead of a plain `docker compose up --force-recreate gateway webchat` for model-backed runs. It loads `docker/env/sparkclaw.external-postgres.env` after `.env`, so Compose cannot fall back to the `mock/file` defaults from `docker/env/sparkclaw.example.env`. It also checks `/readyz` after restart and exits non-zero unless Gateway reports `model_mode=external` and `state_backend=postgres`.

## DGX Spark Model Services

Host-side vLLM scripts:

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
scripts/serve_models_compose.sh all
```

Default endpoints:

| Lane | Served name | Endpoint |
|---|---|---|
| fast | `sparkclaw-fast` | `http://127.0.0.1:8001/v1` |
| deep | `sparkclaw-deep` | `http://127.0.0.1:8002/v1` |
| embedding | `sparkclaw-embedding` | `http://127.0.0.1:8003/v1` |
| reranker | `sparkclaw-reranker` | `http://127.0.0.1:8004/v1` |

Check endpoints:

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8004/v1/models
```

Important environment variables:

- `SPARKCLAW_VLLM_IMAGE`
- `SPARKCLAW_FAST_MODEL_ID`, `SPARKCLAW_FAST_MODEL`, `SPARKCLAW_FAST_MAX_MODEL_LEN`, `SPARKCLAW_FAST_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_FAST_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_FAST_MAX_NUM_SEQS`, `SPARKCLAW_FAST_SPECULATIVE_CONFIG`
- `SPARKCLAW_DEEP_MODEL_ID`, `SPARKCLAW_DEEP_MODEL`, `SPARKCLAW_DEEP_MAX_MODEL_LEN`, `SPARKCLAW_DEEP_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_DEEP_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_DEEP_MAX_NUM_SEQS`, `SPARKCLAW_DEEP_SPECULATIVE_CONFIG`
- `SPARKCLAW_EMBEDDING_MODEL_ID`, `SPARKCLAW_EMBEDDING_MODEL`, `SPARKCLAW_EMBEDDING_MAX_MODEL_LEN`, `SPARKCLAW_EMBEDDING_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_EMBEDDING_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_EMBEDDING_MAX_NUM_SEQS`
- `SPARKCLAW_RERANKER_MODEL_ID`, `SPARKCLAW_RERANKER_MODEL`, `SPARKCLAW_RERANKER_MAX_MODEL_LEN`, `SPARKCLAW_RERANKER_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_RERANKER_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_RERANKER_MAX_NUM_SEQS`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN`

Use `*_MODEL_ID` for the Hugging Face checkpoint loaded by the serving container and `*_MODEL` for the OpenAI-compatible served name sent by Gateway.

Validated DGX Spark notes from 2026-05-24:

- NVIDIA GB10 and driver `580.159.03` were visible on host and from CUDA containers.
- `vllm/vllm-openai:cu130-nightly` worked on arm64.
- `Qwen/Qwen3.6-27B-FP8`, `Qwen/Qwen3.6-35B-A3B-FP8`, `Qwen/Qwen3-Embedding-0.6B` and `Qwen/Qwen3-Reranker-0.6B` were validated.
- The reranker uses vLLM generative scoring when `/rerank` is unavailable.
- Full-context fast+deep dual residency did not fit with both chat lanes at 128K context and MTP enabled. Operate one 128K/MTP chat lane at a time, route both Gateway profiles to the loaded lane for evals, or reduce context/MTP and re-measure.

Light dual-residency experiment:

```bash
scripts/serve_models_compose.sh dual-light
python3 scripts/record_model_loading.py --profile dual-light-v1
```

The `dual-light` shortcut applies `docker/env/sparkclaw.dual-light.env` and `docker/compose.dual-light.yaml`: fast 32K with 8G KV cache, deep 64K with 12G KV cache, embedding 8K with 2G KV cache, reranker 2K with 1G KV cache, no MTP, and low sequence concurrency. Start this full profile before running Gateway in external mode. This is the current accepted single-user full product profile after the 2026-05-25 real-model golden eval passed.

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
- Postgres volume `sparkclaw_pg`
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
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal build
```

4. Start the target profile.
5. Run `bash scripts/doctor.sh`.
6. Run mock golden eval.
7. For DGX Spark model changes, run endpoint checks and append a new benchmark section.

## Secure Defaults

- Expose only WebChat on `0.0.0.0:18790`; keep Gateway and other service ports
  bound to `127.0.0.1`.
- Set `SPARKCLAW_API_TOKEN` before sharing WebChat on a LAN or shared machine.
- Keep Gateway unauthenticated only for local development.
- Keep dangerous and reversible tools approval-gated.
- Keep shell execution sandboxed and network-disabled.
- Treat browser/email/file observations as untrusted.
- Keep `.env`, model weights, state encryption keys and downloaded data out of git.
- Scan diffs for tokens before handoff.

## Troubleshooting

| Symptom | Check |
|---|---|
| Docker permission denied | Use `sudo -n docker ...` or add the user to the Docker group. |
| Golden eval browser step fails | Start Gateway with `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal` for Docker eval or `127.0.0.1` for host eval. |
| Model returns reasoning but no answer | Set `SPARKCLAW_MODEL_DISABLE_THINKING=true`. |
| Reranker `/rerank` returns 404 | Use the existing generative-scoring fallback and served name `sparkclaw-reranker`. |
| Postgres vector extension unavailable | SparkClaw falls back to JSON vectors and Gateway-side hybrid scoring. |
| 128K fast+deep does not fit | Run one chat lane at a time or lower context/MTP and re-benchmark. |
