# SparkClaw Development

> Language: English | [简体中文](../zh-cn/docs/development.md)

This document is for contributors continuing the project. It replaces the old initial roadmap and local implementation audit by recording the current implementation state, verification commands and extension rules.

## Repository Map

```text
apps/webchat/              React/Vite workbench
services/gateway/          Go API, Agent Runtime, Model Router, ToolHub, policy, state and traces
configs/                   Runtime, model, tool, sandbox, logging and eval configuration
docker/                    Compose file and service images
scripts/                   Doctor, eval, model serving and benchmark scripts
eval/golden/               Golden task definitions and fixtures
benchmarks/                DGX Spark model evidence
packages/                  Portable protocol, policy and tool-schema notes
skills/                    Runtime skill packages, intentionally evolving
docs/                      Current project documentation
zh-cn/                     Chinese documentation mirror
```

## Implementation State

The MVP control plane and DGX Spark real-model closure are complete. Future work should be scheduled as model optimization, product expansion or connector hardening rather than as MVP blockers.

| Area | Status | Main Evidence |
|---|---|---|
| Gateway control plane, sessions, messages, events, owner profile, client pairing and rate limits | Complete | Gateway tests, golden API checks |
| Agent Runtime, guard review, model routing, planning, repair and grounded answers | Complete | Agent tests, golden eval |
| ToolHub contracts and MVP tools | Complete | ToolHub tests, `/api/tools`, golden checks |
| Approval-first reversible/dangerous actions | Complete | Approval tests, patch/delete/shell/email/calendar golden cases |
| Audit log, traces, observation summaries and artifact catalog | Complete | Trace/artifact tests and golden checks |
| File, browser, email, calendar, memory, knowledge, code and notify workflows | Complete | Unit tests plus 58-case eval |
| Skills registry boundary | Complete | Registry tests and `/api/skills`; skills do not bypass policy |
| WebChat workbench | Complete | TypeScript/Vite build |
| Runtime config, model profiles, tool policy editor, secret redaction and metrics | Complete | Gateway tests and golden checks |
| Docker profiles and local deployment | Complete | Compose config, image builds, doctor script |
| DGX Spark fast/deep/embedding/reranker serving | Complete | `benchmarks/model_baseline.md` |

## Standard Verification

Run the smallest relevant tests while iterating, then the full matrix before handoff.

Host checks:

```bash
npm --workspace @sparkclaw/webchat run build
go test ./services/gateway/...
bash scripts/doctor.sh
bash scripts/run-eval.sh
```

If host Go is unavailable:

```bash
sudo -n docker run --rm -u "$(id -u):$(id -g)" \
  -v "$PWD":/workspace -w /workspace/services/gateway \
  -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomodcache \
  golang:1.25-alpine /usr/local/go/bin/go test ./...
```

Compose checks:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml config --quiet
sudo -n env SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose --env-file .env -f docker/compose.yaml --profile minimal up -d --build
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

Postgres integration check:

```bash
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
go test ./services/gateway/internal/store -run TestPostgresStoreRoundTrip -count=1
```

## Golden Eval Coverage

`scripts/run-eval.sh` currently expects 58 golden cases. It covers:

- direct `/chat` profile selection
- config, tool, skill, owner, client, auth and rate-limit surfaces
- file search/read/write/delete and multi-file grounded answers
- browser read, multi-source comparison, raw snapshots and prompt-injection handling
- memory candidates, sensitive-memory rejection, approval-gated sensitive writes, editing and export
- email search/thread/draft/send and calendar read/propose/create
- approval modification, approval-pending run lifecycle and post-approval trace refresh
- shell and code patch approval, rollback artifacts and sandbox command queueing
- model-call telemetry and fast/deep/repair lane selection
- knowledge indexing/search, citations, query rewrite metadata and missing-index repair
- smoke/chaos eval persistence, failure archives and eval history

When adding user-visible behavior, prefer adding a focused unit test plus a golden case that exercises the real API path.

## Working With Tools

For a new tool:

1. Define input and output structures.
2. Register the tool in ToolHub with a risk level.
3. Validate successful outputs.
4. Add policy defaults if it can mutate, send, delete, execute or expose sensitive data.
5. Add audit events and trace observation summaries.
6. Archive full observations if they can be large or useful later.
7. Add unit tests and at least one golden/smoke eval path.
8. Update [Architecture](architecture.md) if the tool changes the product boundary.

Current risk expectations:

| Risk | Behavior |
|---|---|
| `read` | May run when policy allows; output is untrusted evidence. |
| `draft` | May produce local drafts/candidates without external side effects. |
| `reversible` | Requires approval; must store recovery metadata. |
| `dangerous` | Requires approval; must record reason, resources and execution result. |

## Working With Models

Use mock mode for deterministic development and evals. Use external mode for DGX Spark or compatible OpenAI-style endpoints.

Important rules:

- Send served names such as `sparkclaw-fast`, not necessarily Hugging Face checkpoint IDs.
- Set `SPARKCLAW_MODEL_DISABLE_THINKING=true` for Qwen3 chat-completions paths that should return concise assistant content.
- Keep generation caps practical for long golden eval runs.
- Re-run the model benchmark when changing model, context, MTP, GPU memory utilization or serving image.
- Treat LoRA, distillation and GGUF compatibility as post-MVP model-ops work until backed by before/after eval evidence.

## Frontend Development

WebChat lives in `apps/webchat/src/App.tsx` with shared API types in `apps/webchat/src/api/types.ts`.

When changing UI behavior:

- Keep Gateway as the source of truth for policy and execution.
- Show approval, trace and tool-call state rather than hiding it.
- Build with `npm --workspace @sparkclaw/webchat run build`.
- Keep runtime status and error states visible enough for local operators.

## Config And Environment

Primary config files:

- `configs/sparkclaw.default.json`
- `configs/model.profiles.json`
- `configs/tools.policy.json`
- `configs/sandbox.policy.json`
- `configs/eval.profiles.json`
- `docker/env/sparkclaw.example.env`

Common environment variables:

- `SPARKCLAW_API_TOKEN`
- `SPARKCLAW_MODEL_MODE`
- `SPARKCLAW_FAST_BASE_URL`, `SPARKCLAW_FAST_MODEL`
- `SPARKCLAW_DEEP_BASE_URL`, `SPARKCLAW_DEEP_MODEL`
- `SPARKCLAW_EMBEDDING_BASE_URL`, `SPARKCLAW_EMBEDDING_MODEL`
- `SPARKCLAW_RERANKER_BASE_URL`, `SPARKCLAW_RERANKER_MODEL`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_STATE_BACKEND`, `SPARKCLAW_STATE_DSN`
- `SPARKCLAW_ARTIFACT_BACKEND`
- `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS`
- `SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`
- `SPARKCLAW_BROWSER_PROFILE_DIR`
- `HF_TOKEN`, `HUGGING_FACE_HUB_TOKEN`

Never commit `.env`, state encryption keys or downloaded model weights.

## Data And Trace Hygiene

Traces and artifacts are development assets, but they can contain sensitive operational context. Before sharing:

- confirm redaction settings are active
- avoid committing `data/`
- scan diffs for tokens such as `hf_`, `sk-` and `Authorization`
- keep raw external observations out of training data unless deliberately cleaned

## Post-MVP Work

Useful next work that is not required for the current MVP:

- longer DGX Spark soak loops
- smaller-context and no-MTP residency matrix for simultaneous fast/deep serving
- external email/calendar account bridges
- connector hardening and user-facing account setup
- LoRA/QLoRA or distillation after trace cleaning
- GGUF/Ollama/llama.cpp compatibility profile validation
- SDK extraction from `packages/protocol`
- packaging and rollback documentation for custom model profiles

Model training should start only after a stable eval loop exists. Required artifacts for any custom model release: dataset manifest, redaction notes, exact base checkpoint hash, training config, before/after eval report and rollback path.

## Handoff Checklist

Before declaring a project-level change complete:

1. Update docs if commands, environment variables, boundaries or user workflows changed.
2. Run the targeted tests for the changed area.
3. Run WebChat build if UI or API types changed.
4. Run Gateway tests if Go code changed.
5. Run `scripts/doctor.sh` and mock golden eval for runtime changes.
6. Validate Compose config for Docker changes.
7. Scan tracked diffs for secrets.
8. Record any new DGX Spark benchmark evidence in `benchmarks/model_baseline.md`.
