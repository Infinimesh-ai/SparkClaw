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
| Router-first capability workflows | Browser Internet search/weather/automation and document read/edit migrated | Catalog, exact Registry/Dispatcher, fixed tool exposure, end-to-end tests and semantic boundary regressions |
| ToolHub contracts and MVP tools | Complete | ToolHub tests, `/api/tools`, golden checks |
| Approval-first reversible/dangerous actions | Complete | Approval tests, patch/delete/shell/memory golden cases |
| Audit log, traces, observation summaries and artifact catalog | Complete | Trace/artifact tests and golden checks |
| File, browser, memory, code and notify workflows | Complete | Unit tests plus 43-case eval |
| Email, calendar and workspace knowledge/RAG | Deferred; prototypes removed | [Deferred capability record](deferred-email-calendar-knowledge.md) |
| Skills registry boundary | Complete | Registry tests and `/api/skills`; skills do not bypass policy |
| WebChat workbench | Complete | TypeScript/Vite build |
| Runtime config, model profiles, tool policy editor, secret redaction and metrics | Complete | Gateway tests and golden checks |
| Docker profiles and local deployment | Complete | Compose config, image builds, doctor script |
| DGX Spark fast/deep/embedding/reranker serving | Complete | `benchmarks/model_baseline.md` |
| Infinimesh Info `web.search` provider | Complete, opt-in | Contract/fault tests, redacted public config, credential-gated live smoke |
| WebChat and Gateway speech transcription | Complete, opt-in | Speech/Gateway tests, voice frontend tests, live ASR smoke evidence |
| Messaging connector Registry and Telegram multi-Bot binding | Complete, opt-in | Provider-neutral registry, credential isolation, binding, worker, media, reminder and WebChat tests |
| Message Control, scheduled messages, and result delivery | Complete for the router-first vertical slice | Persisted ingress/return context, Endpoint/Schedule registries, bounded Timer workers, one WorkflowResult delivery path, Provider capability preflight, [migration guide](message-control-delivery-migration.md) |

## Standard Verification

Run the smallest relevant tests while iterating, then the full matrix before handoff.

Host checks:

```bash
npm --workspace @sparkclaw/webchat run build
npm --workspace @sparkclaw/webchat run test:voice
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

`scripts/run-eval.sh` currently expects 43 golden cases. It covers:

- direct `/chat` profile selection
- config, tool, skill, owner, client, auth and rate-limit surfaces
- file search/read/write/delete and multi-file grounded answers
- browser read, multi-source comparison, raw snapshots and prompt-injection handling
- memory candidates, sensitive-memory rejection, approval-gated sensitive writes, editing and export
- approval modification, approval-pending run lifecycle and post-approval trace refresh
- shell and code patch approval, rollback artifacts and sandbox command queueing
- model-call telemetry and fast/deep/repair lane selection
- smoke/chaos eval persistence, failure archives and eval history

When adding user-visible behavior, prefer adding a focused unit test plus a golden case that exercises the real API path.

## Working With Tools

For a new tool:

1. Define input and output structures.
2. Register execution, risk, semantic capabilities, trusted directory metadata,
   effects, and the outcome adapter together in ToolHub.
3. Validate successful outputs and typed `ToolOutcome` adaptation.
4. Add policy defaults if it can mutate, send, delete, execute or expose sensitive data.
5. Add audit events and trace observation summaries.
6. Archive full observations if they can be large or useful later.
7. Add unit tests and at least one golden/smoke eval path.
8. Update [Architecture](architecture.md) if the tool changes the product boundary.

A registered capability does not become model-visible by itself. A migrated
Workflow Profile must include that capability in a frozen active scope, and
`ToolExposure.Search/Materialize` must admit and materialize its registration.
Do not add a parallel tool-name list to TaskHint, Skill metadata, or Agent
Runtime.

Current risk expectations:

| Risk | Behavior |
|---|---|
| `read` | May run when policy allows; output is untrusted evidence. |
| `draft` | May produce local drafts/candidates without external side effects. |
| `reversible` | Requires approval; must store recovery metadata. |
| `dangerous` | Requires approval; must record reason, resources and execution result. |

## Working With Capability Routing And Workflows

Follow the [routing refactor plan](intent-routing-workflow-refactor-plan.md) for
the full contract. For each migrated capability leaf:

1. Add the leaf and allowed typed operations to the versioned Capability
   Catalog, with one exact Workflow contract reference.
2. Keep Fast output tool-neutral, reject unknown JSON fields, and freeze
   deterministic URL/path facts in the normalizer.
3. Register one exact versioned Workflow Profile. Registry resolution must use
   the validated leaf identity and must not reinterpret the message.
4. Add fixed capability metadata and outcome adapters to every permitted
   ToolHub registration. Tool Exposure must materialize only that scope.
5. Declare allowed transitions, risks, and governed argument bindings in the
   profile, then persist the route for approval/login resume.
6. Remove the same feature's TaskHint candidates and legacy Workflow branch.
7. Add a production-entry end-to-end test that executes a real tool adapter,
   asserts the `WorkflowResult`, and proves no legacy routing audit occurred.

Current-state facts use one typed semantic boundary. Set
`fact_scope=current_internet_state` for read-only facts whose correct answer
depends on the live Internet, including prices, exchange rates, stock/index
quotes, immediate news, match results, and schedules. Keep stable common
knowledge unmatched. Do not add one leaf, keyword switch, or tool-name list per
fact category. The only narrow specialization is `browser.weather` for one
grounded location's current conditions or short forecast card; weather alerts,
news, history, and comparisons remain `browser.internet_search`.

Core runtime code must remain profile-neutral. If a change requires a switch on
Workflow ID or tool name to select a scope, resource, assessment, or next step,
move that behavior into a Profile, plan binding, ToolHub registration, or
outcome adapter. Only `RouteDecision.Status == unmatched` may enter ReAct.

### Extending Document Processing

The document Workflow order is owned by `internal/document.Pipeline`, not by a
format adapter or model prompt. New format support registers one canonical
parser and, where applicable, operation-qualified editors in ToolHub. Do not
add extension switches outside the signature-aware detector and registration
composition.

High-level routing classifies only whether an existing document is being read
or changed. Direct image analysis is a read format under the same
`document.read` Workflow. It must not map natural-language insert, delete, append, row, cell,
paragraph, slide, or page phrases to concrete editor operations. Every content
change enters `document.edit` r2 with the detected format; after the structured
read, bounded directory selection chooses one compatible operation-qualified
registration. An unsupported change blocks there instead of being coerced into
another editor.

The only implemented strategy is `small_file_v1`: 8 MiB maximum source size
and 200,000 bytes maximum complete extracted content. A larger resource must
return typed `strategy_deferred` until another `document.Strategy` implements
chunked, streaming, indexed, or lazy access. Truncated content is never a
successful small-document result.

Every structured-document reader must produce stable location IDs in
`structured_document_v1`. The direct image reader instead returns a bounded
semantic result with dimensions and Fast-model provenance through
`images.inspect`; its source limit is 12 MiB.
High-level parsers may add the optional `document_enrichment_v1` envelope for
assets, annotations, layout, extensions, coverage, and category policy. DOCX,
XLSX, PPTX, and text-PDF readers register parser-visible embedded images with
source relationships and SHA-256 identities. Image bytes go to ArtifactStore,
never the ToolCall JSON or prompt. `files.read` defaults to targeted image
analysis, accepts stable `image_target_paths`, and uses only the Fast model;
`image_analysis=all` is reserved for explicit full-document visual
understanding. The implemented limits are 4 targeted or 8 full-document unique
images, two concurrent calls, 30 seconds per image, a 120-second enrichment
stage, 512 output tokens, and 4,000 characters of combined image context.

Every editor must consume located targets, reject missing, ambiguous, or
count-mismatched targets, write non-existing output copies, and return every
produced path through the typed result rather than adapter-specific details.
An XLSX append derives its row anchor from the highest populated row in that
structured representation. It must not use a library's physical used range or
`rowCount`, because formatting-only blank cells can extend both and create
visible gaps before the appended content.
The Pipeline completely re-reads every output through the same parser, verifies
the expected after-value and unchanged non-target content, compares known
asset/annotation/layout fingerprints, re-hashes the input, and only then
returns `change_summary`. A mismatch removes generated copies and returns
`preservation_mismatch`. The summary reports
`high_level_preservation=verified` and `package_preservation=unknown`; the
latter remains unknown until OOXML/PDF package-level checks exist. Invalid or
zero-change results also clean up generated copies.
Keep subprocesses behind the bounded document adapters and treat all parsed
content as untrusted.

Paths remain internal governed references, not user-facing success text. A
successful document mutation projects each output as a downloadable file
attachment on the assistant message. Image outputs use `kind=image` with
`disposition=inline` and WebChat renders the complete image at its natural
aspect ratio instead of an attachment thumbnail. The same projection applies
after approval resume and to persisted message history.

Run the document setup before tests:

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/document ./internal/toolhub ./internal/agent ./cmd/sparkclaw
```

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
- Preserve the review-before-send microphone flow and the Telegram multi-binding lifecycle.
- Check both desktop and mobile layouts after changing composer or settings controls.
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
- `SPARKCLAW_WEB_SEARCH_ENABLED`, `SPARKCLAW_WEB_SEARCH_PROVIDER`
- `SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE`
- `SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE`
- `SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE`
- `SPARKCLAW_SPEECH_ENABLED`, `SPARKCLAW_SPEECH_BACKEND`
- `SPARKCLAW_SPEECH_BASE_URL`, `SPARKCLAW_SPEECH_ALLOWED_HOSTS`, `SPARKCLAW_SPEECH_MODEL`
- `SPARKCLAW_TELEGRAM_ENABLED`, `SPARKCLAW_TELEGRAM_BASE_URL`
- `SPARKCLAW_CREDENTIAL_KEY`, `SPARKCLAW_CREDENTIAL_KEY_FILE`
- `HF_TOKEN`, `HUGGING_FACE_HUB_TOKEN`

Never commit `.env`, state encryption keys or downloaded model weights.

Infinimesh search, speech and Telegram are independently disabled by default. The minimal profile remains on the `file` state backend and requires no cloud or connector credential. Enable each feature explicitly; enabling Telegram without speech keeps text and attachments available while voice returns a clear unavailable response.

## Data And Trace Hygiene

Traces and artifacts are development assets, but they can contain sensitive operational context. Before sharing:

- confirm redaction settings are active
- avoid committing `data/`
- scan diffs for tokens such as `hf_`, `sk-` and `Authorization`
- keep Infinimesh queries and speech transcripts out of logs, traces, status payloads and committed fixtures
- confirm Telegram file/PostgreSQL state contains credential envelopes rather than bot tokens
- keep raw external observations out of training data unless deliberately cleaned

## Post-MVP Work

Useful next work that is not required for the current MVP:

- longer DGX Spark soak loops
- smaller-context and no-MTP residency matrix for simultaneous fast/deep serving
- design-first reintroduction of deferred capabilities after the gates in [Deferred Capabilities](deferred-email-calendar-knowledge.md) are met
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
