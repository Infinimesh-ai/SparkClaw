# Support

> Language: English | [简体中文](zh-cn/SUPPORT.md)

SparkClaw is an open-source project. Support is community best-effort unless a maintainer states otherwise.

## Where To Ask

- Use GitHub issues for reproducible bugs, documentation gaps and feature proposals.
- Use discussions, if enabled, for setup help and open-ended questions.
- Do not post secrets, tokens, traces, `.env` files or private documents.

## Useful Debug Information

For deployment issues, include:

- host OS and architecture
- Docker and Docker Compose versions
- GPU model and driver, if using DGX Spark model serving
- command run
- sanitized output from `bash scripts/doctor.sh`
- relevant Compose profile
- sanitized Gateway logs

For runtime behavior issues, include:

- prompt or workflow, with private data removed
- model mode (`mock` or `external`)
- relevant tool names
- sanitized trace metadata
- whether the issue reproduces in `bash scripts/run-eval.sh`
