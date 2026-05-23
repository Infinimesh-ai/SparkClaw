# SparkClaw

**Reliable Local Agent Runtime for DGX Spark**

SparkClaw is a local-first agent runtime for turning local models into a bounded, auditable personal workflow system. The MVP implements the first control-plane loop from the architecture docs: WebChat -> Gateway -> Agent Runtime -> Model Router -> ToolHub -> approval and trace records.

## What Works Now

- Go Gateway API with `/healthz`, `/readyz`, direct `/chat`, sessions, messages, tools, approvals, memories, run feedback, audit and trace endpoints.
- Gateway control-plane protection includes bearer/client authentication, paired-client listing and revocation, and a configurable per-client rate limiter with Prometheus rejection metrics.
- File-backed Gateway state at `data/memory/gateway-state.json`, plus a PostgreSQL fact-source backend for sessions, tool calls, approvals, eval runs and document chunks.
- Model router with fast/deep/embedding/reranker/guard profiles, deterministic mock mode, OpenAI-compatible chat-completions and embeddings calls, manual direct-chat profile selection, guard classification telemetry for agent runs, pre-tool blocking for guard `block` verdicts, and fast-to-deep fallback for agent runs.
- MVP tools: `files.search`, `files.read`, `files.write_draft`, `file.delete`, `memory.search`, `memory.write_candidate`, `memory.propose`, `memory.write_sensitive`, `knowledge.index_workspace`, `knowledge.search`, `browser.read`, `email.search`, `email.read_thread`, `email.draft_reply`, `email.send`, `calendar.read`, `calendar.propose_event`, `calendar.create`, `shell.exec_sandboxed`, `code.apply_patch`, `notify.ask_approval`. `GET /api/tools` exposes input and output JSON schemas for these tools, and ToolHub validates successful tool outputs against the declared contracts.
- Local file search and reads now produce grounded assistant answers from ToolHub observations, including matched paths, match reasons, content previews, byte/truncation metadata, multi-file comparison evidence and untrusted-data framing for read content.
- Policy engine that blocks denied tools and holds dangerous/reversible actions for approval.
- Optional local API token enforcement for Gateway routes via `SPARKCLAW_API_TOKEN` or `gateway.pairing_required`; `/healthz`, `/readyz` and `/metrics` remain available for local diagnostics.
- Approval execution path: approving `code.apply_patch` applies a unified diff inside the allowed workspace and stores the original patch, file backups, `manifest.json`, and an inverse `rollback.patch` under `.sparkclaw/`; approving `file.delete` moves a single workspace file into `.sparkclaw/trash` with a recovery manifest instead of permanently deleting it; approving `shell.exec_sandboxed` runs through a sandbox runner with Docker `--network none`. Pending and approved shell/patch runs surface grounded assistant summaries with command/status/output or rollback metadata.
- `browser.read` fetches only HTTP(S) pages as read-only untrusted external content, archives the raw response as a `browser_snapshot` artifact, refuses loopback/private literal hosts unless a host is explicitly allowlisted for fixtures or controlled internal use, and grounds assistant answers in the extracted title/text with an untrusted-data warning. Multi-URL browser research prompts read each source, compare observed notes, and list source URLs/snapshots.
- Email and calendar tools use adapter boundaries. The default `file` adapters read fixtures under `.sparkclaw/mock/` and write approval-gated mock outbox/event logs; `http` adapters can connect to a local or account-bridge service. Email search/thread reads and calendar reads now produce grounded assistant answers from observed adapter data, including simple calendar free-slot and conflict checks. Draft/propose tools never send, while `email.send` and `calendar.create` are dangerous tools that execute only after approval.
- `memory.write_candidate` follows candidate-then-confirm, rejects content matching configured sensitive patterns such as `api_key`, `password`, `token` or `ssh_key` unless `memory.allow_sensitive_memory` is explicitly enabled, and applies `memory.retention_days` before memory search/export surfaces. `memory.propose` is a compatibility alias for the same candidate-review path.
- Workspace knowledge tools build a local keyword chunk index under `.sparkclaw/index/knowledge.json`, archive indexed source snapshots and the generated index in the artifact store, and catalog them as `knowledge_document` / `knowledge_index`. `knowledge.search` exposes the RAG chain as structured metadata: original and rewritten query, top-50 candidate counts, reranked results, citations, and a byte-bounded cited `evidence_context` for answer grounding. When the Gateway runs with PostgreSQL, knowledge indexing also persists `documents.object_key` plus `document_chunks`; pgvector is used when available, with JSONB vector fallback otherwise.
- Local file reads include an explicit untrusted-data note so instructions embedded inside files are not treated as runtime commands.
- Declarative skill registry reads local `skills/*/SKILL.md` packages and exposes them through `GET /api/skills`; skills document workflows, input schemas, dependencies, eval cases and allowed tools but cannot bypass runtime policy.
- Agent Runtime records a visible `repairing` state for bounded recovery. For example, a missing workspace knowledge index first escalates to a deep-lane `repair_verifier` model call, then triggers `knowledge.index_workspace` and retries `knowledge.search` once, preserving the failed call, repair call, retry call and repair audit in the trace. Narrow schema repairs are also recorded: a calendar create/proposal with a start but missing end gets a derived 30-minute end before continuing to draft or approval. Guard `block` verdicts stop before tool planning, creating no tool calls or approvals. Sensitive memory writes are blocked on the normal candidate path and must use approval-gated `memory.write_sensitive`. Tool observations keep their full archived artifact, while each `tool_call` also carries a bounded `observation_summary` for trace/UI display and context compression.
- User feedback and corrections are persisted per run through `POST /api/runs/{id}/feedback`, audited, streamed as events, and included in refreshed run traces for later evaluation or training-data review.
- Vite 8 + React WebChat workbench with chat, tool timeline, approval inbox, memory review, trace viewer, smoke eval, runtime status, session model-call telemetry and audit event diagnostics.
- Docker Compose minimal profile plus config templates, doctor script and golden eval.

## Local Development

```bash
npm install
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

In another terminal:

```bash
npm --workspace @sparkclaw/webchat run dev
```

Open [http://127.0.0.1:18790](http://127.0.0.1:18790).

For a direct model-router smoke test without starting an Agent session or executing tools:

```bash
curl -fsS -X POST http://127.0.0.1:18789/chat \
  -H 'Content-Type: application/json' \
  -d '{"profile":"deep","message":"Say hello from the selected SparkClaw lane."}'
```

`profile` may be `fast`, `deep`, or the configured chat profile/model name. This endpoint is intentionally plain chat: it does not call ToolHub, create approvals, or mutate session state.
When Gateway auth is enabled, `/chat` requires the same `Authorization: Bearer ...` token as the `/api/*` routes.

If you are running inside Codex desktop on macOS, use a system or Homebrew Node for Vite 8 builds:

```bash
PATH="/opt/homebrew/opt/node/bin:$PATH" npm --workspace @sparkclaw/webchat run build
```

The Codex-bundled Node binary uses hardened runtime signing and may refuse to load Vite 8 / Rolldown native bindings.

For token-protected local runs, set the same token for Gateway and WebChat:

```bash
SPARKCLAW_API_TOKEN=change-me go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
VITE_SPARKCLAW_API_TOKEN=change-me npm --workspace @sparkclaw/webchat run dev
```

If `VITE_SPARKCLAW_API_TOKEN` is not set, WebChat shows a token prompt after the first unauthorized response. Paste the Gateway token there to store it in browser local storage and retry the session bootstrap.

Gateway state defaults to `data/memory/gateway-state.json`. Set `SPARKCLAW_STATE_BACKEND=memory` for throwaway runs, or `SPARKCLAW_STATE_PATH=/path/to/state.json` for an alternate local state file. The file backend can be encrypted at rest with `SPARKCLAW_STATE_ENCRYPT_AT_REST=true` plus either `SPARKCLAW_STATE_ENCRYPTION_KEY` or `SPARKCLAW_STATE_ENCRYPTION_KEY_FILE`; old plaintext state can still be read and is rewritten encrypted on the next save. PostgreSQL is also available as the production-oriented fact source:

```bash
SPARKCLAW_STATE_BACKEND=postgres \
SPARKCLAW_STATE_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

The Gateway auto-applies the core schema used by `migrations/0001_core.sql` at startup. For integration tests, set `SPARKCLAW_TEST_POSTGRES_DSN` and run `go test ./services/gateway/internal/store -run TestPostgresStoreRoundTrip -count=1`.

In `SPARKCLAW_MODEL_MODE=mock`, document embeddings are deterministic local vectors so RAG can be tested offline. For real semantic search, point the embedding lane at an OpenAI-compatible embeddings endpoint:

```bash
SPARKCLAW_MODEL_MODE=external \
SPARKCLAW_EMBEDDING_BASE_URL=http://127.0.0.1:8003/v1 \
SPARKCLAW_EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B \
SPARKCLAW_STATE_BACKEND=postgres \
SPARKCLAW_STATE_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

The Compose Postgres service uses the `pgvector/pgvector` image. If a plain Postgres instance is used, SparkClaw still stores embeddings in `embedding_json` and performs hybrid scoring in the Gateway.

Trace artifacts are written to `storage.trace_dir` for direct UI lookup and also through an artifact store boundary. Trace JSON is redacted with the configured logging and memory redact patterns before it is written, so diagnostic views do not expose common secrets such as tokens, passwords, API keys or authorization headers. The default backend is filesystem-compatible object storage under `data/artifacts/{bucket}/...`, using `artifact://bucket/key` URIs in trace metadata. Tool observations are archived as `observations/{run_id}/{tool_call_id}.json` and linked from `tool_call.observation_ref`. Browser reads archive raw page snapshots as `browser/snapshots/...` objects. Memory exports are archived as `memory-exports/{timestamp}-{id}.json` through `POST /api/memories/export`. Artifact metadata is cataloged in the configured state backend and exposed via `GET /api/artifacts` for trace, observation, browser snapshot, memory export and eval archive browsing. Set `SPARKCLAW_ARTIFACT_BACKEND=s3` or `minio` plus `SPARKCLAW_S3_ENDPOINT`, `SPARKCLAW_S3_ACCESS_KEY`, and `SPARKCLAW_S3_SECRET_KEY` to write artifacts with S3-compatible `PutObject`.

For local binary runs, sandbox execution defaults to `SPARKCLAW_SANDBOX_BACKEND=local-docker`, which keeps the MVP single-process friendly. Compose profiles use the standalone HTTP sandbox-runner by default so the Gateway container never needs to execute host Docker commands directly. To use the standalone runner service boundary outside Compose:

```bash
sparkclaw-sandbox-runner
SPARKCLAW_SANDBOX_BACKEND=http \
SPARKCLAW_SANDBOX_RUNNER_URL=http://127.0.0.1:18889 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

The runner exposes `GET /healthz` and `POST /run`; the Gateway still enforces tool policy and approval before it dispatches shell execution.
When the runner itself talks to a host Docker socket, set `SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT` and `SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT` if the workspace path differs between the runner container and the host.

Email and calendar adapters default to local fixtures. To connect a bridge service while preserving SparkClaw's approval-first boundary:

```bash
SPARKCLAW_EMAIL_ADAPTER_BACKEND=http \
SPARKCLAW_EMAIL_ADAPTER_URL=http://127.0.0.1:18910 \
SPARKCLAW_CALENDAR_ADAPTER_BACKEND=http \
SPARKCLAW_CALENDAR_ADAPTER_URL=http://127.0.0.1:18911 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

The expected HTTP adapter endpoints are `GET /email/search`, `GET /email/threads/{id}`, `POST /email/send`, `GET /calendar/events`, and `POST /calendar/events`. Drafting and event proposals still write local drafts only; sending and event creation remain approval-gated.

## Local Model Baseline

`scripts/serve_fast.sh` and `scripts/serve_deep.sh` provide the Phase 0 vLLM entrypoints for `sparkclaw-fast` on port `8001` and `sparkclaw-deep` on port `8002`. Defaults match `configs/model.profiles.json` and can be overridden with `SPARKCLAW_FAST_*` or `SPARKCLAW_DEEP_*` environment variables. Record DGX Spark latency/context/MTP measurements in `benchmarks/model_baseline.md`.

## Verification

```bash
go test ./services/gateway/...
PATH="/opt/homebrew/opt/node/bin:$PATH" npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
bash scripts/run-eval.sh
```

When evaluating against the default local Gateway config, set `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=127.0.0.1` before starting the Gateway so the deterministic local browser fixture can be read. Without that explicit allowlist, `browser.read` correctly rejects loopback hosts.

The golden eval requires at least 20 cases in `eval/golden/files.yaml` and currently runs 58 checks. It verifies direct `/chat` profile selection, config/tool/skill visibility, runtime secret redaction including state-encryption key status, tool input/output contract metadata, skill contract metadata, Gateway rate-limit configuration, owner profile persistence, paired-client auth and revocation boundaries, read-only file search/read, multi-file grounded answers and browser tools with observation summaries, grounded final answers, multi-source browser comparison and raw snapshot artifacts, local drafts, approval-gated file deletion to SparkClaw trash, memory candidate review, the `memory.propose` compatibility alias, approval-gated sensitive memory writes, retention pruning, editing and export archiving, sensitive-memory rejection, grounded email/calendar read answers, unread inbox triage, calendar free-slot/conflict detection and draft/send/create approval boundaries, schema validation and bounded schema repair, approval modification, approval-pending run lifecycle completion after approval, session event log cursoring, trace refresh, run-feedback capture, trace metadata and artifact catalog entries after approved patch execution, grounded pending shell/test approvals, combined code diagnostics for repo inspection plus failing-test approval, code repo inspection routing, model-call telemetry, model routing evals for fast/deep/repair lane selection, the MTP A/B eval profile contract, notify approval confirmation, knowledge index/search with query rewrite metadata, compressed cited evidence contexts and source-snapshot archives, prompt-injection chaos protection, a missing-knowledge-index repair path, failed-eval artifact archiving, and eval report history.

The Gateway also exposes run traces and a built-in smoke evaluator:

```bash
curl -fsS http://127.0.0.1:18789/api/traces/{run_id}
curl -fsS http://127.0.0.1:18789/api/traces
```

Approving or rejecting a pending action refreshes the trace snapshot so the diagnostic view reflects the latest tool-call status. `GET /api/sessions/{id}/events?after={event_id}` returns the session event log for UI catch-up, and `GET /api/traces` returns recent trace metadata with tool/model/approval counts and artifact references for browsing trace history in WebChat.

```bash
curl -fsS -X POST http://127.0.0.1:18789/api/evals/run \
  -H 'Content-Type: application/json' \
  -d '{"profile":"smoke"}'
```

Use `{"profile":"chaos"}` to run the prompt-injection and tool-repair chaos cases. The response is persisted in the configured state backend, can be listed with `GET /api/evals`, and can be fetched with `GET /api/evals/{id}`. Failed eval cases are archived under the configured artifact backend as `eval-failures/...` JSON records and returned in `failure_archives`. The WebChat status panel can run the smoke profile, browse recent eval reports, and show any failure archive references.

An independent evaluator process is also available. It talks to Gateway over HTTP, verifies health/readiness/tool and skill registries, checks the MTP A/B eval profile contract in `configs/eval.profiles.json`, triggers the persisted smoke eval, and writes a regression report:

```bash
go run ./services/gateway/cmd/evaluator -gateway-url http://127.0.0.1:18789
```

Compose `--profile eval` uses the same `sparkclaw-evaluator` binary and writes `data/eval/evaluator-report.json`. The `mtp-ab` profile declares the offline benchmark matrix required before real-model latency comparisons: MTP off, MTP on with two speculative tokens, and a coding/long-answer-only MTP on with three speculative tokens, with TTFT, tokens/s, total latency, tool JSON validity, task completion, hallucinated tool calls, repair rate and verifier disagreement metrics.

## Docker

```bash
cp docker/env/sparkclaw.example.env .env
docker compose -f docker/compose.yaml --profile minimal up --build
```

Then open [http://127.0.0.1:18790](http://127.0.0.1:18790).

For golden eval against a Dockerized Gateway, allow the deterministic host-side browser fixture explicitly:

```bash
SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal \
  docker compose -f docker/compose.yaml --profile minimal up -d --build
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 bash scripts/run-eval.sh
```

To run the Gateway against the Compose Postgres service, start an `eval` or `models-local` profile and set `SPARKCLAW_STATE_BACKEND=postgres`.

Profiles scaffolded in `docker/compose.yaml`:

- `minimal`: Gateway + WebChat with mock model routing.
- `dev`: development-oriented services.
- `eval`: independent evaluator plus trace/data services and sandbox-runner scaffold.
- `compat`: external OpenAI-compatible model endpoints.
- `models-local`: Postgres/pgvector, MinIO and DGX Spark local-model endpoint wiring; vLLM/SGLang model serving is started with `scripts/serve_fast.sh` / `scripts/serve_deep.sh` or an external OpenAI-compatible service. Remaining real-hardware deployment, serving, benchmark and training work is tracked in `docs/dgx-spark-finalization-handoff.md`.

## Repository Layout

```text
apps/webchat/              Vite 8 React workbench
services/gateway/          Go Gateway, runtime, tools, policy and trace MVP
skills/                    Built-in declarative workflow skills
configs/                   Default model, tool, sandbox, logging and eval config
docker/                    Compose files and image definitions
eval/golden/               Golden task definitions
scripts/                   doctor and eval scripts
docs/                      Product and architecture planning docs
```

## Design Boundary

SparkClaw is not an unrestricted autonomous agent. Read-only and draft tools can execute inside the configured workspace boundary. Reversible and dangerous actions must go through approval. Tool observations are treated as untrusted data, and each run produces audit events and trace artifacts.
