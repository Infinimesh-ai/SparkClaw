# Issue #15 Deployment Startup Reliability Design

> Language: English | [简体中文](../zh-cn/docs/issue-15-deployment-reliability-design.md)

> Status: implemented and locally validated for
> [issue #15](https://github.com/Infinimesh-ai/SparkClaw/issues/15). The owner
> accepted all remaining recommendations on 2026-08-17. Live DGX restart
> acceptance remains a deployment-window check.

## Decision Summary

SparkClaw's product Compose runtime uses PostgreSQL. The repository will not
provide a file-to-PostgreSQL migration tool or an automatic startup guard for
old file snapshots because the product is still in development and has no
installed-user migration obligation. The example environment and deployment
guide must state the effective product default without implying that the file
backend remains the product path.

Model startup becomes a three-way decision instead of an unconditional cold
recreate:

- keep an already running, healthy, configuration-current group in place;
- recover the complete selected residency group when one member is absent,
  stopped, unhealthy, or configuration-drifted; and
- let an explicit `SPARKCLAW_FORCE_MODEL_RECREATE=true` request rebuild the
  group even when it is healthy.

WebChat's host port becomes one validated `SPARKCLAW_WEBCHAT_PORT` setting used
by Compose, deployment probes, runtime readiness, and printed URLs. The Nginx
container continues listening on internal port `18790`.

Fast and Guard readiness code is copied into a SparkClaw-built derivative vLLM
image. Running model containers therefore do not depend on the repository's
host path. Warmup success remains readiness success even when its optimization
marker cannot be persisted.

The boot unit uses `Type=oneshot`, retains the successful active state with
`RemainAfterExit=yes`, and has a finite start timeout covering the Docker/NVIDIA
wait plus the normal model startup budget.

Commit `bbacb81` already made deployment honor
`SPARKCLAW_AUTOSTART_ENABLED=false`; that behavior is accepted and is not
reimplemented by this issue.

## Scope And Non-Goals

In scope:

- product state-backend defaults and upgrade documentation;
- model group inspection, recovery, and explicit forced recreation;
- WebChat host-port configuration across Compose and shell entrypoints;
- self-contained model readiness and best-effort marker persistence;
- finite, observable systemd startup; and
- focused script, Compose, documentation, and DGX startup validation.

Out of scope:

- importing `data/memory/gateway-state.json` into PostgreSQL;
- deleting, renaming, or automatically interpreting an old file snapshot;
- changing PostgreSQL schemas or Store contracts;
- changing model checkpoints, warmup payloads, residency budgets, or logical
  model lanes;
- changing WebChat's internal listener port or Gateway's internal port; and
- replacing systemd with Docker restart policies.

## Current Failure Model

The current paths disagree in ways that are individually valid but unsafe in
combination:

1. `docker/env/sparkclaw.example.env` says `SPARKCLAW_STATE_BACKEND=file`, while
   `scripts/restart_runtime_compose.sh` loads
   `docker/env/sparkclaw.single-fast.env` later and therefore selects
   PostgreSQL.
2. `scripts/serve_models_compose.sh` stops and force-recreates every requested
   model on every invocation. `npm start` and boot consequently pay a full
   model load even when all containers are already healthy and current.
3. Compose exposes a configurable WebChat bind address but fixes the host port
   at `18790`. `scripts/deploy.sh` and the runtime readiness default repeat that
   port independently.
4. Fast and Guard health checks execute a Python file bind-mounted from the
   checkout. The model process may remain valid while repository movement or
   deletion makes its health check impossible to start.
5. A successful warmup is changed into a failed health check if the marker
   write raises an `OSError`.
6. The generated systemd unit has an unlimited start timeout, so a stuck
   startup can remain activating indefinitely.

## Configuration Contract

| Setting | Default | Contract |
|---|---|---|
| `SPARKCLAW_STATE_BACKEND` | `postgres` in the example and product profile | Product startup selects PostgreSQL. `file` remains supported only for explicit isolated host/mock use. |
| `SPARKCLAW_WEBCHAT_PORT` | `18790` | Decimal host TCP port in `1..65535`; invalid or empty explicit values fail before Compose mutation. |
| `SPARKCLAW_WEBCHAT_BIND` | `0.0.0.0` | Host address only. It does not contain a port. |
| `SPARKCLAW_FORCE_MODEL_RECREATE` | `false` | Boolean. Exported process environment wins over `.env`; otherwise `.env` is read without shell evaluation. Invalid values fail before model mutation. |
| `SPARKCLAW_VLLM_IMAGE` | current pinned/default upstream image | Remains the upstream base of the local readiness-enabled derivative, preserving the existing operator override. |

Shell entrypoints must not `source .env`. They should share one non-evaluating
dotenv reader for the small set of script-owned values. Extracting the two
existing readers from `deploy.sh` and `autostart_compose.sh` is a mechanical
commit separate from behavior changes.

## State Backend Alignment

Change the example environment to PostgreSQL and keep the single-Fast product
profile explicitly PostgreSQL-backed. The deployment guide must say:

- `npm start`, the one-command deployer, and boot autostart use PostgreSQL;
- an old file snapshot is neither migrated nor deleted;
- direct upgrade may therefore start with an empty PostgreSQL database; and
- the file backend is still available for an explicitly selected isolated
  host/mock run, not as the product default.

The product Compose CI assertion for `state_backend=postgres` remains the
executable guard. No Store code or data migration is added.

## Model Startup State Machine

### Inspection

For every selected service, inspect all of the following before changing the
group:

1. a Compose container ID exists;
2. `.State.Status` is `running`;
3. `.State.Health.Status` is `healthy`; and
4. the container's `com.docker.compose.config-hash` equals `docker compose
   config --hash` for that service.

Checking process state separately matters after a host reboot: Docker may
retain an old health value on a stopped container. A stopped container is not a
healthy resident service.

For `single-fast`, Fast, embedding, Guard, and OCR remain one atomic residency
group. If one member requires recovery, all four are recovered together. The
legacy Deep container is still stopped before the product group is evaluated.
Explicit standalone and benchmark commands apply the same decision rules only
to their selected services.

### Actions

| Condition | Group action | Expected result |
|---|---|---|
| All members running, healthy, current; force flag false | `compose up --wait --build --no-recreate` without stopping or recreating | Cached build check and readiness confirmation; model processes keep their identity. |
| Any member absent, stopped, unhealthy, or drifted; force flag false | Stop the complete selected group, then `compose up --wait --build --force-recreate` | Automatic whole-group recovery refreshes stale GPU runtime state. |
| Force flag true | Stop the complete selected group, then `compose up --wait --build --force-recreate` | Fresh container runtime, NVIDIA attachment, and process-local caches. |

`--build` keeps the small readiness derivative aligned with its Dockerfile and
helper while Docker layer caching avoids rebuilding the upstream vLLM image on
an unchanged start. The startup timeout continues to bound `compose up --wait`.
Any inspection failure is an explicit script failure; it must not silently be
treated as a healthy group.

The script tests use a stateful fake Docker command and cover healthy/current,
missing, stopped-with-stale-health, unhealthy, drifted, forced, invalid-flag,
inspection failure, single-Fast atomicity, no-recreate retention, and
standalone-lane cases.

## WebChat Port Ownership

Compose publishes:

```text
${SPARKCLAW_WEBCHAT_BIND:-0.0.0.0}:${SPARKCLAW_WEBCHAT_PORT:-18790}:18790
```

Apply the same mapping to `docker/compose.yaml` and
`docker/compose.dev.yaml`. The internal WebChat Dockerfile, Nginx listener, and
service-to-service routing remain on `18790`.

The shared dotenv reader resolves and validates the host port once. That value
then owns:

- the WebChat and `/readyz` probe URLs in `deploy.sh`;
- local and LAN URLs printed by the deployer;
- the default readiness URL in `restart_runtime_compose.sh`; and
- the Compose interpolation used by direct `npm start` and boot startup.

An explicit `SPARKCLAW_GATEWAY_READY_URL` remains the highest-precedence escape
hatch for unusual proxy layouts. Tests must use a non-default port so a hidden
`18790` literal on an operational path cannot pass.

## Self-Contained Model Readiness

Add a small derivative Dockerfile based on `SPARKCLAW_VLLM_IMAGE` and copy
`scripts/model_readiness.py` to `/opt/sparkclaw/model_readiness.py` during the
image build. The common vLLM services use that image and remove the source-file
bind mount. The model cache remains the only host mount needed by these
services. OCR keeps its separately pinned image and lightweight health check.

Fast and Guard markers move from the general `/tmp` directory to a dedicated,
small container-local tmpfs such as `/run/sparkclaw-readiness`. Marker contents
stay bound to the model, warmup shape, and process start identity, so a new
model process still requires a warmup.

After a completion has passed `require_completion`, marker persistence is an
optimization:

1. attempt the atomic marker write;
2. on `OSError`, emit one bounded warning without secrets; and
3. return readiness success.

A failed model listing, failed completion, malformed response, or timeout still
fails readiness. Only persistence after a proven successful warmup is
best-effort.

The normal read-only/full-`/tmp` case no longer applies because the marker has
its own tmpfs. If that dedicated location is also unwritable, a stateless Docker
health-check process has no durable way to remember the successful warmup and
may repeat it on a later probe. The owner accepts this residual behavior; this
issue does not add a long-lived supervisor or sidecar.

## Systemd Startup

The generated service keeps its current dependencies, user/group, repository
mount requirement, restrictive umask, and non-starting installation behavior.
Change the service contract to:

```ini
[Service]
Type=oneshot
ExecStart=/absolute/path/to/bash /absolute/path/to/scripts/autostart_compose.sh
RemainAfterExit=yes
TimeoutStartSec=4h
```

Four hours is the accepted fixed limit: it exceeds the current 10-minute
Docker/NVIDIA readiness wait and three-hour Compose model startup budget while
remaining finite. A timeout makes the unit failed, leaves logs in the journal,
and lets the operator retry explicitly. This issue does not add another timeout
configuration setting.

Descriptions and logs must stop calling every boot a cold recreate. They should
describe reconciliation and report whether the model group was retained,
recovered, or explicitly force-recreated.

## Failure Semantics

- Invalid script-owned configuration fails before stopping containers.
- Model inspection errors fail closed and preserve the current group.
- A healthy/current group is not stopped merely because `npm start` or boot ran.
- Group recovery never starts only one member of the single-Fast residency set.
- Model readiness remains failed until model evidence succeeds; only marker
  persistence after that evidence is non-fatal.
- A custom WebChat port is used consistently for publication and local probes.
- systemd timeout produces a failed unit, not an indefinitely activating unit.
- Existing file state is retained on disk but ignored by the PostgreSQL product
  runtime; this is documented behavior, not an automatic error.

## Implementation Slices

1. Extract and test the shared non-evaluating dotenv reader with no behavior
   change.
2. Align the state-backend template and bilingual upgrade documentation.
3. Add WebChat port validation and propagate the value through Compose,
   deployment, runtime readiness, and tests.
4. Add the derivative vLLM image, remove the bind mount, move markers to the
   dedicated tmpfs, and make marker writes best-effort.
5. Implement model-group inspection and the accepted recovery/force state
   machine with focused fake-Docker tests.
6. Change the generated systemd unit and its tests, then update bilingual model
   loading and deployment guidance.

Mechanical extraction and behavior changes should remain separate commits.

## Verification Matrix

Focused deterministic checks:

```bash
python3 -m unittest scripts/test_model_readiness.py
python3 -m unittest scripts/test_dotenv.py
python3 -m unittest scripts/test_serve_models_compose.py
python3 -m unittest scripts/test_runtime_compose.py
python3 -m unittest scripts/test_autostart_compose.py
bash -n scripts/deploy.sh scripts/start_compose.sh \
  scripts/restart_runtime_compose.sh scripts/serve_models_compose.sh \
  scripts/autostart_compose.sh scripts/install_autostart_systemd.sh
```

Compose checks must render both the default and a non-default WebChat port,
assert PostgreSQL for the product environment, assert that the vLLM services no
longer bind-mount `scripts/model_readiness.py`, and verify all relevant overlay
combinations.

Repository gates:

```bash
cd services/gateway && go build ./... && go vet ./... && go test ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
```

The bilingual Markdown mirror and local-link check also gate the change.

DGX acceptance evidence must cover:

1. a healthy/current `npm start` preserves all four model container IDs and
   returns without another heavy warmup;
2. each degraded predicate triggers the accepted whole-group recovery;
3. the explicit force flag replaces all selected container IDs;
4. a non-default WebChat port serves both `/` and `/readyz` and is printed by
   deployment;
5. after the readiness image is built, the host helper is renamed temporarily
   and the running model health check still succeeds;
6. a simulated marker-write failure leaves a successful warmup healthy; and
7. the generated unit verifies with `systemd-analyze`, succeeds as a oneshot,
   and fails within its finite bound when startup is deliberately blocked.

## Resolved Decisions

1. The product state backend is PostgreSQL.
2. There is no file-to-PostgreSQL migration tool or legacy-snapshot startup
   guard for this issue.
3. File state remains an explicit isolated development option.
4. Healthy/current model groups do not cold-recreate on ordinary startup.
5. The operator has one explicit force-recreate flag.
6. WebChat host-port ownership is environment-driven and end-to-end.
7. Model health checks must be self-contained in the running container.
8. Marker persistence cannot override successful warmup evidence.
9. A degraded selected model group is automatically force-recreated as one
   group. The force flag applies the same action to an otherwise healthy group.
10. The systemd service is a oneshot with fixed `TimeoutStartSec=4h`.
11. Dedicated tmpfs marker persistence is best-effort. If that tmpfs is also
    unwritable, readiness still succeeds and a later probe may repeat warmup;
    no supervisor or sidecar is added.
12. The autostart enable/disable install behavior fixed by `bbacb81` is complete.

These decisions remove the remaining recovery and failure-semantics ambiguity.
The design now exceeds the requested 90 percent implementation-confidence
threshold.
