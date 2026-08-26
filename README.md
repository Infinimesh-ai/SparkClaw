# SparkClaw

> Language: English | [简体中文](zh-cn/README.md)

**Reliable local agent runtime for DGX Spark.**

SparkClaw turns local models into a bounded, auditable personal workflow system. It is designed for a single owner on a local AI workstation, with local-first data handling, explicit tool contracts, approval-gated risky actions, traces, artifacts and repeatable evals. The current local-model shape uses one responsive `fast` MoE chat model, plus resident embedding and guard endpoints. The `deep` model is temporarily excluded from the default product runtime.

The project is past the initial planning stage. This README is the entry point;
the [documentation index](docs/index.md) lists the complete current set. Start
with:

- [Architecture](docs/architecture.md): product boundary, runtime loop, service boundaries, tools, data and safety model.
- [Deployment](docs/deployment.md): local, Docker Compose and DGX Spark model-serving instructions.
- [Development](docs/development.md): repository layout, verification matrix, extension workflow and current completion status.
- [Workflow capability matrix](docs/workflow-capabilities.md): the exact leaf Workflows that can currently execute.
- [Intent routing](docs/intent-routing.md), [messaging and scheduling](docs/messaging-and-scheduling.md), [browser runtime](docs/browser-runtime.md), [document workflows](docs/document-workflows.md), [external integrations](docs/integrations.md), and [WebChat](docs/webchat.md): current component contracts.
- [Model loading plan](docs/model-loading.md): single-machine, light dual-residency and two-DGX-Spark loading strategy.
- [Model baseline](benchmarks/model_baseline.md): DGX Spark endpoint evidence, latency numbers and operating limits.
- [Contributing](CONTRIBUTING.md), [Security](SECURITY.md), [Support](SUPPORT.md), [Changelog](CHANGELOG.md) and [License](LICENSE): open-source project process and terms.

## Current Status

Implemented and validated:

- Go Gateway API with health, readiness, direct chat, sessions, messages, events, tools, approvals, memories, traces, artifacts, eval reports, feedback, client pairing, token auth and rate limiting.
- Agent Runtime with a Catalog-derived semantic graph, full-candidate embedding and Fast/Tree score fusion, deterministic Top-2 and one-leaf Workflow dispatch, grounded execution, repair, and trace snapshots.
- Single-machine `single-fast-v1` product profile for NVIDIA GB10: one `fast` chat model plus embedding, guard, and the OvisOCR2 document adapter, with the logical deep Workflow profile routed to the same Fast endpoint.
- ToolHub with JSON-schema-validated tools for files, memory, browser access, sandbox shell, code patching, notification and approvals.
- Approval-first policy for reversible and dangerous actions such as file deletion, shell execution, patch application and sensitive memory writes.
- File, browser and external adapter observations are treated as untrusted data and are summarized before being used for answers.
- Email, calendar and workspace knowledge/RAG are deliberately deferred until they have complete product designs; see [Deferred Capabilities](docs/deferred-email-calendar-knowledge.md).
- File-backed state for local runs, PostgreSQL 18/pgvector for durable runtime records, and filesystem or S3-compatible artifact storage.
- React/Vite WebChat workbench with chat, tool timeline, approval inbox, memory editor, trace viewer, eval/status/settings panels and model telemetry.
- Docker Compose profiles for mock local operation, development, evaluation, external model compatibility and DGX Spark local-model serving.
- DGX Spark validation on NVIDIA GB10 with PostgreSQL 18/pgvector, MinIO, sandbox-runner and vLLM fast/deep/embedding endpoints. The current Fast + Embedding calibration passes 15/15 labeled intents. On 2026-08-24, the restored vLLM-managed NVFP4 path passed all 47 real-model golden cases with no mock calls or model errors.

Known operating boundary:

- On the validated GB10 machine, full 128K-context fast and deep chat lanes with MTP enabled should be treated as mutually exclusive unless context, MTP or GPU memory utilization is reduced and re-measured.
- The current single-machine product profile is `single-fast-v1`: fast runs at 32K context with an 8G KV cache and MTP off; embedding, guard, and OvisOCR2 retain bounded auxiliary profiles. The historical `dual-light-v1` measurements remain as evidence, not as the startup default.
- Gateway still records its logical fast/deep Workflow choice, but both chat profiles resolve to `sparkclaw-fast` in the current deployment configuration. No `sparkclaw-deep` model process is started.
- Workflow capabilities are the only execution path; see the [Workflow capability matrix](docs/workflow-capabilities.md) for the current capability surface.

## Quick Start

For an Ubuntu test VM that uses externally managed model endpoints, run the
cloud installer as a normal sudo-capable user. It installs Docker when needed,
keeps all endpoint and optional API-key values in a local mode-0600 file, and
verifies the containerized Chromium automation runtime:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 300 \
  https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/codex/server-deployment/install-cloud.sh | bash
```

The interactive setup accepts an empty model API key when the endpoints do not
require authentication. Open `http://<vm-ip>:18790` after it completes.

On an NVIDIA GB10 DGX Spark with Docker, Compose, and the NVIDIA container
toolkit already installed, run the streamed installer:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Chiiz0/SparkClaw/main/install.sh | bash
```

The website may mirror the repository's top-level `install.sh` and replace the
URL above. The bootstrap installs or safely fast-forwards the repository under
`$HOME/SparkClaw`, then reconnects stdin to the terminal so the Hugging Face
token can be entered without echo. The deployment requires Linux/ARM64 and
NVIDIA GB10, downloads Fast, embedding, guard, and OCR, then builds and starts
Gateway, Sandbox Runner, and WebChat. Later runs reuse the model cache.

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

Install host development dependencies:

```bash
npm run setup:host
```

This installs root-workspace Node packages, user-site Python document
libraries, verifies the pinned agent-browser runtime, and resolves a system
Chromium installation. It does not download Chrome for Testing. Set
`adapters.browserAutomation.chromiumExecutable` when Chromium is installed in a
non-standard location.

Rebuild and restart the external-model/OCR/PostgreSQL development runtime used on
this machine:

```bash
npm run dev
```

Use `npm run dev:gateway` or `npm run dev:webchat` to rebuild only one
application container. Direct host mock/file and Vite debugging remain
available as `npm run dev:gateway:host` and `npm run dev:webchat:host`.

For a direct model-router smoke test without starting an Agent session or executing tools:

```bash
docker compose -f docker/compose.yaml exec -T gateway node -e \
  "fetch('http://127.0.0.1:18789/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({profile:'deep',message:'Say hello from the selected SparkClaw lane.'})}).then(async response=>{console.log(await response.text());process.exit(response.ok?0:1)})"
```

The command runs inside the Gateway container because product Compose does not
publish `18789` on the host. `profile` may be `fast`, `deep`, or the configured
chat profile/model name. When Gateway auth is enabled, `/chat` requires the same
bearer token as `/api/*`.

## Verification

Recommended checks before handing off changes:

```bash
npm --workspace @sparkclaw/webchat run build
go test ./services/gateway/...
bash scripts/doctor.sh
bash scripts/run-eval.sh
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

Current golden eval coverage is 47 cases. It verifies direct chat, config/tool visibility, auth and rate-limit surfaces, grounded file/browser answers, approval lifecycles, memory review, sensitive-memory handling, prompt-injection chaos, trace refresh, artifact catalog entries, model-call telemetry and eval history.

## Open Source

SparkClaw is licensed under the [Apache License 2.0](LICENSE). Contributions are welcome through issues and pull requests.

Before contributing, read [CONTRIBUTING.md](CONTRIBUTING.md). For vulnerabilities, follow [SECURITY.md](SECURITY.md) and avoid publishing exploit details in a public issue.

The npm workspace root is intentionally marked `private` to prevent accidental package publishing. The repository itself is open-source; runtime images and future release artifacts should be published deliberately.

## DGX Spark Models

For the current local-model path, start the single-Fast product profile first:

```bash
scripts/serve_models_compose.sh single-fast
scripts/restart_runtime_compose.sh
```

`single-fast` stops any old `sparkclaw-deep` container and starts Fast, embedding, guard, and OCR together. `scripts/restart_runtime_compose.sh` then reloads Gateway/WebChat with the single-Fast and OCR environments, maps both logical chat profiles to `sparkclaw-fast`, enables the document OCR adapter, and fails if Gateway is not ready.

Other serving entrypoints are available for targeted tests and controls:

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding
```

Default served lanes:

| Lane | Served name | Port | Default checkpoint |
|---|---|---:|---|
| fast | `sparkclaw-fast` | 8001 | `nvidia/Qwen3.6-35B-A3B-NVFP4` |
| deep | `sparkclaw-deep` | 8002 | `nvidia/Qwen3.6-35B-A3B-NVFP4` |
| embedding | `sparkclaw-embedding` | 8003 | `Qwen/Qwen3-Embedding-0.6B` |

The current single-machine product profile is intentionally conservative: only `fast` serves chat, both logical chat profiles use that NVFP4 endpoint, MTP is off, and embedding and guard use small explicit KV budgets. The dedicated vLLM 0.24.0 chat image interprets the checkpoint's quantization metadata and owns activation and kernel selection; SparkClaw supplies no quantization override. Deep and dual-light commands remain available for targeted controls but no longer load an FP8 chat checkpoint.

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
zh-cn/                     Chinese documentation mirror
```

## Design Boundary

SparkClaw is not an unrestricted autonomous agent. Read-only and draft tools can execute inside configured boundaries. Reversible and dangerous actions must go through approval. Tool observations are untrusted data. Every meaningful run should leave audit events, trace metadata and artifact references that can be inspected later.
