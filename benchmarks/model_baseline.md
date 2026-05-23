# SparkClaw Model Baseline

This file records the Phase 0 baseline for the local fast/deep model services. It is intentionally a lightweight runbook until real DGX Spark numbers are collected.

## Serving Commands

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Defaults:

| Lane | Model | Served name | Port | Context | MTP |
|---|---|---|---:|---:|---:|
| fast | Qwen/Qwen3.6-35B-A3B-FP8 | sparkclaw-fast | 8001 | 131072 | 2 speculative tokens |
| deep | Qwen/Qwen3.6-27B-FP8 | sparkclaw-deep | 8002 | 131072 | 2 speculative tokens |

Set `SPARKCLAW_FAST_*` or `SPARKCLAW_DEEP_*` environment variables to adjust model id, host, port, tensor parallel size, context length, speculative token count, or extra vLLM args.

## Checks

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8002/v1/models
```

Then run the Gateway in external model mode:

```bash
SPARKCLAW_MODEL_MODE=external-model go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

## Results Log

| Date | Hardware | Lane | Context | MTP | TTFT ms | Tokens/s | Total latency ms | Notes |
|---|---|---|---:|---|---:|---:|---:|---|
| TBD | DGX Spark | fast | 131072 | on:2 | TBD | TBD | TBD | Fill after local benchmark |
| TBD | DGX Spark | deep | 131072 | on:2 | TBD | TBD | TBD | Fill after local benchmark |
