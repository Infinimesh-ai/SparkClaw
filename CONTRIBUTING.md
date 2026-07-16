# Contributing to SparkClaw

> Language: English | [简体中文](zh-cn/CONTRIBUTING.md)

Thanks for helping improve SparkClaw. This project is a local-first agent runtime, so contributions should preserve the core boundary: visible tools, explicit approvals, auditable traces and safe local defaults.

## Before You Start

- Read [README.md](README.md), [docs/architecture.md](docs/architecture.md) and [docs/development.md](docs/development.md).
- Open an issue for large changes before implementation.
- Keep changes narrowly scoped. Avoid drive-by refactors in unrelated files.
- Do not commit `.env`, model weights, traces, local state, keys or downloaded data.

## Development Setup

```bash
npm install
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
npm --workspace @sparkclaw/webchat run dev
```

If host Go is unavailable, use the Docker builder fallback documented in [docs/development.md](docs/development.md).

## Verification

Run the smallest relevant tests while developing. Before opening a pull request, run:

```bash
npm --workspace @sparkclaw/webchat run build
go test ./services/gateway/...
bash scripts/doctor.sh
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

For runtime, tool, policy, trace, model routing or approval changes, also run:

```bash
bash scripts/run-eval.sh
```

When evaluating against a Dockerized Gateway, start it with `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal` and set `BROWSER_FIXTURE_URL=http://host.docker.internal:18791`.

## Tool And Runtime Changes

New or changed tools must include:

- typed input and output contracts
- a risk level
- policy defaults
- audit events
- observation summaries
- artifact archiving when observations are large or useful later
- unit tests
- at least one golden or smoke eval path for user-visible behavior

Treat file, browser and external adapter content as untrusted data. Reversible and dangerous actions must remain approval-gated.

## Documentation

Update docs when a change affects commands, configuration, environment variables, deployment, safety boundaries, APIs or user workflows.

Default documentation is English. Chinese documentation lives under `zh-cn/`. Runtime skill packages under `skills/` are intentionally evolving and do not need a Chinese mirror.

## Pull Request Checklist

- Tests relevant to the change pass.
- WebChat build passes for UI/API type changes.
- Gateway tests pass for Go changes.
- Compose config validates for Docker/config changes.
- Golden eval passes for runtime behavior changes.
- Docs are updated when operator or contributor behavior changes.
- No secrets, traces, model weights or local state are included.
- The PR explains risk, verification and any known limitations.
