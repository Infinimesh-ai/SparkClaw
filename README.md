# SparkClaw

> Language: English | [简体中文](zh-cn/README.md)

**Reliable local agent runtime for DGX Spark.**

SparkClaw turns local models into a bounded, auditable personal workflow system. It is designed for a single owner on a local AI workstation, with local-first data handling, explicit tool contracts, approval-gated risky actions, traces, artifacts and repeatable evals. The current local-model shape is a full single-machine dual-lane stack: a responsive `fast` MoE lane, a dense `deep` lane for harder or higher-risk work, and resident embedding/reranker endpoints for retrieval workflows.

The project is past the initial planning stage. This README is the entry point; the detailed current-state docs are:

- [Architecture](docs/architecture.md): product boundary, runtime loop, service boundaries, tools, data and safety model.
- [Deployment](docs/deployment.md): local, Docker Compose and DGX Spark model-serving instructions.
- [Development](docs/development.md): repository layout, verification matrix, extension workflow and current completion status.
- [Model loading plan](docs/model-loading.md): single-machine, light dual-residency and two-DGX-Spark loading strategy.
- [Model baseline](benchmarks/model_baseline.md): DGX Spark endpoint evidence, latency numbers and operating limits.
- [Contributing](CONTRIBUTING.md), [Security](SECURITY.md), [Support](SUPPORT.md), [Changelog](CHANGELOG.md) and [License](LICENSE): open-source project process and terms.

## Current Status

Implemented and validated:

- Go Gateway API with health, readiness, direct chat, sessions, messages, events, tools, approvals, memories, traces, artifacts, eval reports, feedback, client pairing, token auth and rate limiting.
- Agent Runtime with guard review, Gateway-controlled fast/deep routing, bounded repair, schema repair, grounded final answers and trace snapshots.
- Single-machine `dual-light-v1` model profile for NVIDIA GB10: `fast` and `deep` chat lanes plus embedding and reranker resident together, with explicit context, KV cache and sequence caps.
- ToolHub with JSON-schema-validated tools for files, memory, browser access, sandbox shell, code patching, notification and approvals.
- Approval-first policy for reversible and dangerous actions such as file deletion, shell execution, patch application and sensitive memory writes.
- File, browser and external adapter observations are treated as untrusted data and are summarized before being used for answers.
- Email, calendar and workspace knowledge/RAG are deliberately deferred until they have complete product designs; see [Deferred Capabilities](docs/deferred-email-calendar-knowledge.md).
- File-backed state for local runs, PostgreSQL 18/pgvector for durable sessions, tool calls, approvals, evals and document chunks, and filesystem or S3-compatible artifact storage.
- React/Vite WebChat workbench with chat, tool timeline, approval inbox, memory editor, trace viewer, eval/status/settings panels and model telemetry.
- Docker Compose profiles for mock local operation, development, evaluation, external model compatibility and DGX Spark local-model serving.
- DGX Spark validation on NVIDIA GB10 with PostgreSQL 18/pgvector, MinIO, sandbox-runner and vLLM fast/deep/embedding/reranker endpoints. The historical run used 58 cases; the active matrix now has 43 after prototype-only capabilities were removed.

Known operating boundary:

- On the validated GB10 machine, full 128K-context fast and deep chat lanes with MTP enabled should be treated as mutually exclusive unless context, MTP or GPU memory utilization is reduced and re-measured.
- The accepted single-machine dual-lane profile is `dual-light-v1`: fast runs at 32K context with 8G KV cache, deep runs at 64K context with 12G KV cache, both with MTP off. Deep is intentionally slower because it is a dense model; the acceptance standard is task stability and overall product experience, not deep-lane throughput alone.
- Gateway, not the `fast` model, decides which chat lane to call. It routes code, terminal, dangerous, repair or explicitly deep/review requests to `deep`; routine bounded work goes to `fast`, with deep fallback only if a fast call fails.
- Skill packages under `skills/` are runtime workflow descriptions and will keep evolving; they are intentionally not the source of truth for product documentation.

## Quick Start

Docker is the recommended first run path:

```bash
cp docker/env/sparkclaw.example.env .env
sudo -n env SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d --build
```

Open [http://127.0.0.1:18790](http://127.0.0.1:18790) locally, or use
`http://<host-lan-ip>:18790` from another device on the same LAN.

Run the project health check and golden eval:

```bash
bash scripts/doctor.sh
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

The browser allowlist is required only for deterministic local fixtures. In normal operation, `browser.read` rejects loopback/private hosts unless they are explicitly allowlisted.

## Local Development

Install JavaScript dependencies:

```bash
npm install
```

Run the Gateway and WebChat in separate terminals:

```bash
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
npm --workspace @sparkclaw/webchat run dev
```

If Go is not installed on the host, use the Docker Go builder path used by `scripts/doctor.sh`:

```bash
sudo -n docker run --rm -u "$(id -u):$(id -g)" \
  -v "$PWD":/workspace -w /workspace/services/gateway \
  -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache \
  golang:1.25-alpine /usr/local/go/bin/go test ./...
```

For a direct model-router smoke test without starting an Agent session or executing tools:

```bash
curl -fsS -X POST http://127.0.0.1:18789/chat \
  -H 'Content-Type: application/json' \
  -d '{"profile":"deep","message":"Say hello from the selected SparkClaw lane."}'
```

`profile` may be `fast`, `deep`, or the configured chat profile/model name. When Gateway auth is enabled, `/chat` requires the same bearer token as `/api/*`.

## Verification

Recommended checks before handing off changes:

```bash
npm --workspace @sparkclaw/webchat run build
go test ./services/gateway/...
bash scripts/doctor.sh
bash scripts/run-eval.sh
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

Current golden eval coverage is 43 cases. It verifies direct chat, config/tool/skill visibility, auth and rate-limit surfaces, grounded file/browser answers, approval lifecycles, memory review, sensitive-memory handling, prompt-injection chaos, trace refresh, artifact catalog entries, model-call telemetry and eval history.

## Open Source

SparkClaw is licensed under the [Apache License 2.0](LICENSE). Contributions are welcome through issues and pull requests.

Before contributing, read [CONTRIBUTING.md](CONTRIBUTING.md). For vulnerabilities, follow [SECURITY.md](SECURITY.md) and avoid publishing exploit details in a public issue.

The npm workspace root is intentionally marked `private` to prevent accidental package publishing. The repository itself is open-source; runtime images and future release artifacts should be published deliberately.

## DGX Spark Models

For the current full local-model path, start the accepted single-machine profile first:

```bash
scripts/serve_models_compose.sh dual-light
scripts/restart_runtime_compose.sh
```

`dual-light` starts all resident product model services: `fast`, `deep`, embedding and reranker. `scripts/restart_runtime_compose.sh` then reloads Gateway/WebChat in `external/postgres` mode and fails if Gateway is not ready.

Other serving entrypoints are available for targeted tests and controls:

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding,reranker
```

Default served lanes:

| Lane | Served name | Port | Default checkpoint |
|---|---|---:|---|
| fast | `sparkclaw-fast` | 8001 | `Qwen/Qwen3.6-35B-A3B-FP8` |
| deep | `sparkclaw-deep` | 8002 | `Qwen/Qwen3.6-27B-FP8` |
| embedding | `sparkclaw-embedding` | 8003 | `Qwen/Qwen3-Embedding-0.6B` |
| reranker | `sparkclaw-reranker` | 8004 | `Qwen/Qwen3-Reranker-0.6B` |

The accepted single-machine profile is intentionally conservative: `fast` is the responsive MoE lane, `deep` is the dense stability/quality lane, MTP is off, and auxiliary models use small explicit KV budgets so the full product stack fits. `dual-light-chat` is only for chat-lane controls without auxiliary endpoints.

Loading strategy lives in [docs/model-loading.md](docs/model-loading.md). Benchmark evidence, endpoint snapshots and operating notes live in [benchmarks/model_baseline.md](benchmarks/model_baseline.md).

## Repository Layout

```text
apps/webchat/              Vite/React workbench
services/gateway/          Go Gateway, Agent Runtime, ToolHub, policy and traces
configs/                   Model, tool, sandbox, logging and eval configuration
docker/                    Compose file and image definitions
scripts/                   Doctor, eval, model serving and benchmark helpers
eval/golden/               Golden task definitions
benchmarks/                DGX Spark model baseline evidence
docs/                      Current project architecture, deployment and development docs
packages/                  Portable protocol, policy and tool-schema notes
skills/                    Runtime workflow skill packages
zh-cn/                     Chinese documentation mirror
```

## Design Boundary

SparkClaw is not an unrestricted autonomous agent. Read-only and draft tools can execute inside configured boundaries. Reversible and dangerous actions must go through approval. Tool observations are untrusted data. Every meaningful run should leave audit events, trace metadata and artifact references that can be inspected later.
