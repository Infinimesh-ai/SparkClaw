# SparkClaw 部署

> 语言： [English](../../docs/deployment.md) | 简体中文

本文档是当前的本地开发、Docker Compose 和 DGX Spark 模型服务部署指南，替代旧的 Docker implementation plan 和 DGX handoff notes。

## 前置条件

- Ubuntu 24.04 或其他带 Docker / Docker Compose 的 Linux host。
- DGX Spark 模型服务需要 NVIDIA container runtime。
- host-side WebChat build 需要 Node.js 24+ 和 npm 11+。
- host-side Gateway 开发需要 Go 1.25；如果 host 没有 Go，可使用 Docker Go builder fallback。
- 模型下载需要把 Hugging Face token 放在本地 `.env` 中。不要提交 `.env`。

创建本地环境文件：

```bash
cp docker/env/sparkclaw.example.env .env
```

在 `.env` 中设置 `HF_TOKEN` 或 `HUGGING_FACE_HUB_TOKEN`。该 token 只传给 model-serving containers。

## Compose Profiles

| Profile | 用途 |
|---|---|
| `minimal` | Gateway + WebChat，mock model routing。推荐首次运行。 |
| `dev` | 开发运行形态。 |
| `eval` | Gateway 加 evaluator 和 data services。 |
| `compat` | Gateway 连接外部 OpenAI-compatible endpoints。 |
| `models-local` | Postgres/pgvector、MinIO、sandbox-runner、Gateway、WebChat 和可选 vLLM lanes。 |

所有 host ports 默认绑定 localhost。Containers 通过私有 `sparkclaw_internal` network 通信。

## Minimal Local Runtime

启动 mock-mode control plane：

```bash
sudo -n env SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d --build
```

检查状态：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal ps
curl -fsS http://127.0.0.1:18789/readyz
bash scripts/doctor.sh
```

打开 WebChat：[http://127.0.0.1:18790](http://127.0.0.1:18790)。

对 Dockerized Gateway 运行 golden eval：

```bash
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS` 是刻意显式的设置。它允许 eval fixtures 工作，同时保持 `browser.read` 默认拒绝 private hosts。

## Host Development Runtime

直接运行 Gateway 和 WebChat：

```bash
npm install
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
npm --workspace @sparkclaw/webchat run dev
```

本地 token auth：

```bash
SPARKCLAW_API_TOKEN=change-me go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
VITE_SPARKCLAW_API_TOKEN=change-me npm --workspace @sparkclaw/webchat run dev
```

如果未设置 `VITE_SPARKCLAW_API_TOKEN`，WebChat 会在第一次 unauthorized response 后提示输入。

## State Backends

默认 file state：

```text
data/memory/gateway-state.json
```

常用选项：

```bash
SPARKCLAW_STATE_BACKEND=memory
SPARKCLAW_STATE_PATH=/path/to/state.json
SPARKCLAW_STATE_ENCRYPT_AT_REST=true
SPARKCLAW_STATE_ENCRYPTION_KEY_FILE=/path/to/key
```

Postgres-backed state：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d postgres

SPARKCLAW_STATE_BACKEND=postgres \
SPARKCLAW_STATE_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

Gateway 启动时会应用核心 schema。如果使用没有 pgvector 的普通 Postgres，embedding 也会以 JSON 形式存储，hybrid scoring 在 Gateway 内执行。

## Artifact Storage

默认 artifact backend 是 `data/artifacts/{bucket}/...` 下的 filesystem object storage。S3-compatible storage 设置：

```bash
SPARKCLAW_ARTIFACT_BACKEND=s3
SPARKCLAW_S3_ENDPOINT=http://127.0.0.1:9000
SPARKCLAW_S3_ACCESS_KEY=sparkclaw
SPARKCLAW_S3_SECRET_KEY=sparkclaw-local
```

Compose 提供 MinIO：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d minio minio-init
```

Artifacts 包括 tool observations、browser snapshots、knowledge indexes、memory exports、patch rollback files 和 eval failure archives。

## Sandbox Runner

Host binary 运行时，Gateway 可使用 `SPARKCLAW_SANDBOX_BACKEND=local-docker`。

Compose 使用独立 sandbox runner：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d sandbox-runner
```

Compose 外的 standalone runner：

```bash
sparkclaw-sandbox-runner
SPARKCLAW_SANDBOX_BACKEND=http \
SPARKCLAW_SANDBOX_RUNNER_URL=http://127.0.0.1:18889 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

如果 runner 访问 host Docker socket，且 host 与 container 看到的 workspace path 不同，需要设置 `SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT` 和 `SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT`。

## Email And Calendar Adapters

默认 adapters 是本地 file fixtures。连接 bridge services：

```bash
SPARKCLAW_EMAIL_ADAPTER_BACKEND=http \
SPARKCLAW_EMAIL_ADAPTER_URL=http://127.0.0.1:18910 \
SPARKCLAW_CALENDAR_ADAPTER_BACKEND=http \
SPARKCLAW_CALENDAR_ADAPTER_URL=http://127.0.0.1:18911 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

期望 HTTP endpoints：

- `GET /email/search`
- `GET /email/threads/{id}`
- `POST /email/send`
- `GET /calendar/events`
- `POST /calendar/events`

Drafting 和 event proposals 在 owner approve send/create action 前仍然保持本地。

## DGX Spark Data Services

启动 durable state、artifacts、sandbox、Gateway 和 WebChat：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d \
  postgres minio minio-init sandbox-runner gateway webchat
```

模型端点 healthy 后，用 external mode 重建 Gateway：

```bash
sudo -n env \
  SPARKCLAW_MODEL_MODE=external \
  SPARKCLAW_STATE_BACKEND=postgres \
  SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS=300 \
  SPARKCLAW_MODEL_DISABLE_THINKING=true \
  SPARKCLAW_FAST_MODEL=sparkclaw-fast \
  SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
  SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d --build --force-recreate gateway webchat
```

## DGX Spark Model Services

Host-side vLLM scripts：

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Compose vLLM services：

```bash
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh embedding,reranker
scripts/serve_models_compose.sh all
```

默认 endpoints：

| Lane | Served name | Endpoint |
|---|---|---|
| fast | `sparkclaw-fast` | `http://127.0.0.1:8001/v1` |
| deep | `sparkclaw-deep` | `http://127.0.0.1:8002/v1` |
| embedding | `sparkclaw-embedding` | `http://127.0.0.1:8003/v1` |
| reranker | `sparkclaw-reranker` | `http://127.0.0.1:8004/v1` |

检查 endpoints：

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8004/v1/models
```

重要环境变量：

- `SPARKCLAW_VLLM_IMAGE`
- `SPARKCLAW_FAST_MODEL_ID`, `SPARKCLAW_FAST_MODEL`, `SPARKCLAW_FAST_MAX_MODEL_LEN`, `SPARKCLAW_FAST_SPECULATIVE_CONFIG`
- `SPARKCLAW_DEEP_MODEL_ID`, `SPARKCLAW_DEEP_MODEL`, `SPARKCLAW_DEEP_MAX_MODEL_LEN`, `SPARKCLAW_DEEP_SPECULATIVE_CONFIG`
- `SPARKCLAW_EMBEDDING_MODEL_ID`, `SPARKCLAW_EMBEDDING_MODEL`
- `SPARKCLAW_RERANKER_MODEL_ID`, `SPARKCLAW_RERANKER_MODEL`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `HF_TOKEN` 或 `HUGGING_FACE_HUB_TOKEN`

`*_MODEL_ID` 是 serving container 加载的 Hugging Face checkpoint；`*_MODEL` 是 Gateway 发送的 OpenAI-compatible served name。

2026-05-24 DGX Spark 验证说明：

- NVIDIA GB10 和 driver `580.159.03` 在 host 和 CUDA containers 中可见。
- `vllm/vllm-openai:cu130-nightly` 可在 arm64 上运行。
- `Qwen/Qwen3.6-27B-FP8`、`Qwen/Qwen3.6-35B-A3B-FP8`、`Qwen/Qwen3-Embedding-0.6B` 和 `Qwen/Qwen3-Reranker-0.6B` 已验证。
- reranker 在 `/rerank` 不可用时使用 vLLM generative scoring。
- full-context fast+deep dual residency 在两个 chat lanes 都为 128K context 且启用 MTP 时未能同时容纳。可一次运行一个 128K/MTP chat lane，把两个 Gateway profiles 都路由到已加载 lane，或降低 context/MTP 后重新测量。

运行 repeatable endpoint benchmark：

```bash
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md
```

运行 real-model golden eval：

```bash
SPARKCLAW_EXPECT_REAL_MODELS=1 \
SPARKCLAW_MODEL_MODE=external \
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

已验证 real-model run 完成 58 个 golden cases。benchmark rows 和运行说明见 [model_baseline.md](../benchmarks/model_baseline.md)。

## Backup And Restore

需要备份的路径或 volumes：

- `.env` secret template values，存储在 git 外
- `data/memory`
- `data/traces`
- `data/artifacts`
- `data/workspaces`
- Postgres volume `sparkclaw_pg`
- MinIO volume `sparkclaw_minio`
- 如果需要复用模型缓存，则备份 `data/models`

Postgres：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml exec postgres \
  pg_dump -U sparkclaw sparkclaw > sparkclaw.sql
```

filesystem state 最好在 Gateway 停止后复制。

## Upgrade Flow

1. 保存或导出重要 state。
2. 拉取或应用代码变更。
3. rebuild images：

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile minimal build
```

4. 启动目标 profile。
5. 运行 `bash scripts/doctor.sh`。
6. 运行 mock golden eval。
7. DGX Spark 模型变更需要运行 endpoint checks，并追加新的 benchmark section。

## Secure Defaults

- host ports 绑定到 `127.0.0.1`。
- Gateway 仅在本地开发时允许无认证。
- 共享机器上设置 `SPARKCLAW_API_TOKEN`。
- dangerous 和 reversible tools 保持 approval-gated。
- shell execution 保持 sandboxed 且 network-disabled。
- browser/email/file observations 视为 untrusted。
- `.env`、model weights、state encryption keys 和下载数据不进入 git。
- 交付前扫描 diff 中的 token。

## Troubleshooting

| Symptom | Check |
|---|---|
| Docker permission denied | 使用 `sudo -n docker ...` 或将用户加入 Docker group。 |
| Golden eval browser step fails | Docker eval 启动 Gateway 时设置 `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal`；host eval 使用 `127.0.0.1`。 |
| Model returns reasoning but no answer | 设置 `SPARKCLAW_MODEL_DISABLE_THINKING=true`。 |
| Reranker `/rerank` returns 404 | 使用已有 generative-scoring fallback 和 served name `sparkclaw-reranker`。 |
| Postgres vector extension unavailable | SparkClaw fallback 到 JSON vectors 和 hybrid scoring。 |
| 128K fast+deep does not fit | 一次运行一个 chat lane，或降低 context/MTP 后重新 benchmark。 |
