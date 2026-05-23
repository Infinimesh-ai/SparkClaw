# DGX Spark Finalization Handoff

This handoff lists the remaining work that must be completed on a real NVIDIA DGX Spark or an equivalent local model host. The current repository implements and verifies the local-first agent control plane, tools, policy, WebChat, traces, approvals, golden evals, Docker wiring, and mock/external adapter boundaries. The items below need hardware, model weights, GPU runtime validation, or training infrastructure.

## Current Boundary

Completed in this repository:

- Gateway, Agent Runtime, Model Router, ToolHub, policy engine, approvals, audit/events, trace and artifact catalog.
- WebChat workbench for chat, tool timeline, approval inbox, memory editor, trace viewer, eval/status/settings panels, model profiles and tool policy editing.
- File, memory, knowledge/RAG, browser, email/calendar, shell sandbox, patch, notify and approval flows.
- Golden/chaos eval harness with 58 executable cases covering grounded answers, prompt-injection protection, approval boundaries, trace artifacts, code diagnostics, email triage and calendar-aware drafts.
- Docker profiles for minimal/dev/models-local control-plane deployment, plus Postgres/pgvector, MinIO and sandbox runner wiring.
- vLLM entrypoint scripts for `sparkclaw-fast` and `sparkclaw-deep`.

Not completed locally:

- Real DGX Spark container installation and first boot validation.
- Actual vLLM/SGLang/Ollama model-serving containers inside Compose.
- DGX Spark latency, context, memory, MTP and concurrency benchmarks.
- LoRA/QLoRA, DPO/GRPO, distillation and model release packaging.
- llama.cpp / Unsloth GGUF compatibility profile validation.

## DGX Spark Bring-Up

1. Install NVIDIA drivers, container runtime, Docker and Docker Compose on the DGX Spark host.
2. Verify GPU visibility from containers:

```bash
docker run --rm --gpus all nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi
```

3. Start the control-plane profile:

```bash
cp docker/env/sparkclaw.example.env .env
docker compose -f docker/compose.yaml --profile minimal up -d
docker compose -f docker/compose.yaml ps
bash scripts/doctor.sh
```

4. Start the local data/model-wiring profile:

```bash
docker compose -f docker/compose.yaml --profile models-local up -d
docker compose -f docker/compose.yaml ps
```

5. Confirm the Gateway and WebChat can reach the configured fast/deep OpenAI-compatible endpoints.

## Model Serving

The repo currently provides host-side vLLM launch scripts:

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Validate or replace these with DGX Spark model-serving containers. The first hardware pass should test:

- `Qwen/Qwen3.6-35B-A3B-FP8` as `sparkclaw-fast`.
- `Qwen/Qwen3.6-27B-FP8` as `sparkclaw-deep`.
- `tensor-parallel-size=1` unless real hardware validation proves another setting is better.
- `64K`, `128K` and reduced fallback context windows.
- MTP off/on with two speculative tokens.
- Tool calling, JSON mode, thinking mode and reasoning parser compatibility.
- Embedding and reranker endpoints for knowledge search.

Record all results in `benchmarks/model_baseline.md`.

## Benchmark Matrix

Run at least this matrix on the target host:

| Area | Required Evidence |
|---|---|
| Fast lane latency | p50/p95 first-token and total latency for chat, summary and email triage |
| Deep lane latency | p50/p95 for code diagnostics, repair verifier and risky-action verifier |
| Context length | successful 64K and 128K prompts, plus failure mode when memory is exceeded |
| MTP | A/B results with MTP disabled vs enabled |
| Tool calling | golden eval pass with local model endpoints, no mock-model fallback |
| RAG | embedding/reranker quality check with citations |
| Sandbox | approved shell execution through sandbox-runner with network disabled |
| Stability | at least one overnight loop of smoke/golden eval subsets |

## Training And Distillation

Do not start fine-tuning until the hardware eval loop is stable. The first training cycle should use only validated traces:

1. Export successful tool-selection, schema, repair, approval, injection-defense, coding and memory traces.
2. Clean and deduplicate trace data; remove secrets and external untrusted payloads that should not become instructions.
3. Fine-tune the 27B authority/deep model first for tool correctness and verifier behavior.
4. Distill successful 27B trajectories into the 35B-A3B fast lane only after the 27B evals improve.
5. Use DPO/GRPO only for recurring over-calling, loops, unsafe action selection or refusal mistakes.
6. Merge a model only when the same golden/chaos suite improves or stays neutral and prompt-injection critical failures remain zero.

Required artifacts:

- Dataset manifest with trace source IDs and redaction notes.
- Training config and exact base checkpoint hash.
- Eval before/after report.
- Rollback path to the previous model profile.

## GGUF Compatibility Profile

The Unsloth GGUF line is a compatibility distribution, not the DGX Spark default. Validate separately:

- llama.cpp or Ollama startup.
- OpenAI-compatible endpoint mapping.
- Context window and tool-call behavior.
- Known limitations compared with vLLM/SGLang.
- A reduced golden profile that excludes unsupported model features only when documented.

## Final Acceptance Gates

The DGX Spark finalization is complete when all of these are true:

- `docker compose --profile minimal` and `--profile models-local` boot cleanly on the host.
- Fast/deep/embedding/reranker model endpoints are live and configured in `configs/model.profiles.json` or `.env`.
- Full golden eval passes against real model endpoints.
- Chaos eval profile reports zero prompt-injection critical failures.
- `benchmarks/model_baseline.md` contains DGX Spark measurements for latency, context, MTP and memory behavior.
- Approval-gated shell, patch, email send and calendar create flows remain approval-first with trace artifacts.
- Any trained or distilled model has before/after eval evidence and rollback instructions.

## Handoff Status

As of this repository state, all remaining items in this document require DGX Spark hardware, model weights, model-serving containers or training infrastructure. The local control plane can continue to evolve, but the final development closure depends on the acceptance gates above.
