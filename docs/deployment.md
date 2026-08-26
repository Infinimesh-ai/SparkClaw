# SparkClaw Deployment

> Language: English | [简体中文](../zh-cn/docs/deployment.md)

This document is the current deployment guide for local development, Docker Compose and DGX Spark model serving. It replaces the older Docker implementation plan and DGX handoff notes.

## Prerequisites

- NVIDIA DGX Spark with its GB10 GPU, Linux/ARM64, and at least 100 GiB of
  system/unified memory. Ubuntu 24.04 is the validated OS.
- Docker Engine, the Docker Compose plugin, the NVIDIA driver/container toolkit,
  `curl`, systemd, `sudo` access for the boot service, and outbound access to
  container registries and Hugging Face.
- At least 125 GiB of free space for a cold model/image cache. The deployment
  script computes the remaining requirement when part of the cache exists.
- A Hugging Face token for model downloads. Do not commit the generated `.env`.

Node.js 26/npm 11 and Go 1.25 are required for host-side development, but not
for the containerized deployment path.

## One-Command DGX Spark Deployment

Starting from a prepared DGX Spark host:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | bash
```

The project website can serve the repository's top-level `install.sh` unchanged
and use its own HTTPS URL in this command. Do not publish the installer over
plain HTTP.

The streamed bootstrap and deployment entrypoints:

1. Clone the configured branch/tag into `$HOME/SparkClaw`, or fast-forward an
   existing clean checkout. A checkout with local changes or a divergent history
   is left untouched and reported as an error. A clean checkout created with the
   former official repository URL is migrated to the current origin after a
   successful fast-forward; every other origin mismatch is rejected.
2. Reattach stdin from the curl pipe to `/dev/tty`, allowing the Hugging Face
   token prompt to remain hidden and interactive.
3. Require Linux/ARM64, NVIDIA GB10, at least 100 GiB of memory, Docker
   Compose, `nvidia-smi`, and sufficient free space.
4. Create or preserve a mode-`0600` `.env`, accept a Hugging Face token
   without echoing it, and align bind-mounted data with the current user.
5. Use vLLM's Hugging Face integration to download Fast, embedding, guard,
   Qwen3-ASR, and OvisOCR2 into the shared `data/models` cache.
6. Wait for model readiness and Fast/Guard warmup, build Gateway, Sandbox
   Runner, and WebChat, then verify both Gateway and WebChat.
7. Install and enable the system-level `sparkclaw-autostart.service` for the
   deploying user. Installation does not restart the running deployment.

The first run downloads roughly 70-85 GiB of model data plus container images
and can take hours. Model health and joint startup share a three-hour default
window. Set `SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS` to a larger positive
number when the download link is slower. Later runs reuse downloaded checkpoints
and container images. A running, healthy, configuration-current model group is
retained; only a degraded or drifted group is recreated with fresh GPU runtime
state and process-local caches.

For a non-interactive install, export the token before starting the pipeline;
the deployment persists it only in the ignored, mode-`0600` local environment
file:

```bash
export HF_TOKEN=hf_example
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | bash
```

Install/update the repository and run only deployment preflight with:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 300 https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh | \
  bash -s -- --check
```

The bootstrap defaults to `main` and `$HOME/SparkClaw`. Pin a release or use a
different installation directory by setting `SPARKCLAW_GIT_REF` or
`SPARKCLAW_INSTALL_DIR` on the `bash` process. To deploy directly from an
already-cloned repository, run:

```bash
bash scripts/deploy.sh
```

## Compose Profiles

| Profile | Purpose |
|---|---|
| `dev` | Development-oriented runtime. |
| `eval` | Gateway plus evaluator and data services. |
| `compat` | Gateway connected to externally managed OpenAI-compatible endpoints. |
| `models-local` | PostgreSQL 18/pgvector, MinIO, sandbox-runner, Gateway, WebChat and optional vLLM lanes. |

WebChat is the only application ingress and binds host port `18790` to
`0.0.0.0` by default. Set `SPARKCLAW_WEBCHAT_PORT` to publish another host port;
the container and Nginx listener remain on internal port `18790`. Gateway is not
published on the host; WebChat proxies its selected routes to `gateway:18789`
over the private `sparkclaw_internal` network. Set
`SPARKCLAW_WEBCHAT_BIND=127.0.0.1` when WebChat must remain local. Both values
are read from `.env`, and an invalid port fails before containers are changed.
Models, state services, and the sandbox runner remain bound to localhost or the
private Docker network.

## Cloud-Model Server Runtime

Use the cloud runtime on a Linux server or VM that owns the SparkClaw
application and durable state but not the model processes. It starts exactly
PostgreSQL, Sandbox Runner, Gateway, and WebChat; the model services in the
`models-local` profile are not selected.

On an Ubuntu VM, run the streamed installer as a normal sudo-capable user:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 300 \
  https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/codex/server-deployment/install-cloud.sh | bash
```

The bootstrap installs Git when necessary, safely clones or fast-forwards the
repository under `$HOME/SparkClaw`, reconnects stdin to the terminal, and runs
`scripts/deploy_cloud_vm.sh`. The VM deployment installs Docker Engine and the
Compose plugin when necessary. Existing checkouts with tracked or untracked
local changes are never overwritten.

On the first run, the deployment prompts for the private Fast, embedding, and
guard endpoints. Standard SparkClaw model names are filled automatically, while
existing configured names are preserved. The logical Deep lane can reuse Fast
or use a separate endpoint. The shared model API key is optional: submit an
empty value when the endpoints do not require Bearer auth.
Speech/ASR and OCR are optional and remain disabled unless their endpoints are
provided. Endpoint and credential values are never included in the repository;
they are written only to the VM's ignored, mode-0600 `.env` file.

The current cloud overlay is a trusted-LAN profile and explicitly disables the
optional Gateway owner-token authentication boundary. WebChat therefore opens
without a token prompt. A normal deployment also clears a legacy
`SPARKCLAW_API_TOKEN` value, and readiness rejects a cloud runtime that still
reports `auth_required: true`.

The command builds and starts PostgreSQL, Sandbox Runner, Gateway, and WebChat.
The Gateway image installs Chromium, `agent-browser`, Xvfb, Chinese/emoji fonts,
and ffmpeg. Deployment succeeds only after both Gateway readiness and a
container-local Chromium open/snapshot smoke test pass. Browser state persists
under `data/browser-profiles`. No Ubuntu desktop or host Chromium package is
required.

Hidden Chromium works on a headless VM. Weixin QR login is different: it opens
a visible Chromium window on the VM owner's desktop so the owner can scan it.
When the cloud start script resolves a local X11/XWayland display, it
automatically stacks `docker/compose.visible-browser.yaml` and prints `Visible
Chromium display: ...`. If no display is available, deployment remains healthy
with hidden Chromium only and prints an explicit warning; opening the Weixin
login window will then fail by design. The window appears on the VM desktop,
not on a different computer that merely opened WebChat.

Run the deployment from the VM desktop session, or use the PVE console to log
in to that desktop, then reconcile the stack:

```bash
cd "$HOME/SparkClaw"
bash scripts/resolve-browser-display.sh
bash scripts/start_cloud_compose.sh
```

If the VM has multiple displays, select the active display and authority file
explicitly before the second command:

```bash
export SPARKCLAW_BROWSER_DISPLAY=:1
export SPARKCLAW_BROWSER_XAUTHORITY=/run/user/$(id -u)/gdm/Xauthority
bash scripts/start_cloud_compose.sh
```

Do not use a synthetic Xvfb display for QR login: it is suitable for hidden
automation but is not an owner-visible scan surface.

Re-run the repository deployment entrypoint to reconcile the runtime without
updating the checkout:

```bash
bash "$HOME/SparkClaw/scripts/deploy_cloud_vm.sh"
```

Re-enter the private configuration or perform a read-only deployment check:

```bash
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 300 \
  https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/codex/server-deployment/install-cloud.sh | \
  bash -s -- --configure

curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 15 --max-time 300 \
  https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/refs/heads/codex/server-deployment/install-cloud.sh | \
  bash -s -- --check
```

SparkClaw appends `/chat/completions` or `/embeddings` to the model base URLs.
The current model router uses one optional `OPENAI_API_KEY` for Fast, Deep,
embedding, and guard; use a trusted compatibility proxy when providers require
different credentials or headers. The speech adapter expects its service root
because it appends `/v1/audio/*`; OCR expects an OpenAI-compatible base URL.

The cloud overlay gives the four application services `restart: unless-stopped`,
and the installer enables Docker so they recover after a host
reboot. Gateway remains Docker-internal; WebChat publishes `18790` by default
and is reachable at `http://<vm-ip>:18790`. This test topology does not install
TLS or firewall rules and must remain on a trusted LAN: every device that can
reach the WebChat port can operate SparkClaw. Do not install the DGX Spark
autostart unit because that unit owns local NVIDIA model reconciliation.

## Product Runtime

The deployment entrypoint ultimately delegates to the same product startup
command exposed at the repository root. Operators with an existing `.env` can
invoke it directly to load the resident `single-fast-v1` model group and the
PostgreSQL-backed control plane:

```bash
npm start
```

The entrypoint delegates model ownership to `serve_models_compose.sh
single-fast`, which treats Fast, embedding, guard, ASR, and OCR as one resident
group. Startup retains the complete group when every container is running,
healthy, and on the current Compose configuration hash. If one member is
absent, stopped, unhealthy, or drifted, all five are stopped and force-recreated
together. Set `SPARKCLAW_FORCE_MODEL_RECREATE=true` to perform that same refresh
on an otherwise healthy group. The command waits for every model health check,
including the configured Fast and Guard completion warmups, before it starts
PostgreSQL, Sandbox Runner, Gateway, and WebChat. PostgreSQL must become healthy
before Gateway is recreated. Gateway then verifies
`model_mode=external` with the PostgreSQL state backend; the logical Deep
profile aliases the Fast endpoint. Set `SPARKCLAW_MODEL_MODE=mock` explicitly
only for isolated deterministic debugging or evaluation.

### Boot Autostart

Deployment enables host-boot startup by default. The setting lives in the local
`.env` file:

```dotenv
SPARKCLAW_AUTOSTART_ENABLED=true
SPARKCLAW_AUTOSTART_READY_TIMEOUT_SECONDS=600
SPARKCLAW_AUTOSTART_PROBE_TIMEOUT_SECONDS=10
```

At boot, `sparkclaw-autostart.service` runs as the deploying user, waits up to
the configured total timeout for Docker and the NVIDIA runtime, and bounds each
individual readiness command by the probe timeout. It then calls the same product
entry used by `npm start`. It retains a healthy model group or automatically
force-recreates a degraded group before application services start. The unit is
a `Type=oneshot` service with `RemainAfterExit=yes`; it stays activating while
reconciliation runs and fails after the fixed `TimeoutStartSec=4h` bound rather
than waiting forever. It does not use Docker's container restart policy.

Set `SPARKCLAW_AUTOSTART_ENABLED=false` to skip startup at the next boot. The
unit remains enabled so changing the setting back to `true` is sufficient; it
will read the file again on the following boot. To install or refresh the unit
after moving the repository, run:

```bash
npm run autostart:install
systemctl status sparkclaw-autostart.service
journalctl -u sparkclaw-autostart.service -b
```

Installing the unit does not restart the current deployment. To apply a changed
setting without rebooting, run `sudo systemctl restart
sparkclaw-autostart.service`. A healthy group is retained. To request a full
refresh, set `SPARKCLAW_FORCE_MODEL_RECREATE=true` in `.env`, restart the unit,
then restore the setting to `false` for later boots.

Check status:

```bash
docker ps --filter name=sparkclaw
curl -fsS http://127.0.0.1:18790/readyz
bash scripts/doctor.sh
```

Open WebChat locally at [http://127.0.0.1:18790](http://127.0.0.1:18790), or
from another LAN device at `http://<host-lan-ip>:18790`.

### JingSi LAN Presentation (Experimental)

The base stack does not publish the JingSi presentation port. To enable the
implemented SparkClaw side for one existing visible WebChat session, select the
session ID as an operator, choose one RFC1918 address assigned to this host, and
run:

```bash
curl -fsS http://127.0.0.1:18790/api/sessions | jq -r \
  '.sessions[] | select(.hidden != true and .source == "webchat") | [.id, .title] | @tsv'
ip -4 -o addr show scope global

export SPARKCLAW_JINGSI_LAN_BIND=192.168.1.20
export SPARKCLAW_JINGSI_SESSION_ID=sess_replace_with_selected_id
bash scripts/restart_jingsi_lan_compose.sh
```

This adds only the exact presentation allowlist on port `18793` (override
with `SPARKCLAW_JINGSI_LAN_PORT`); WebChat remains on
`18790` and Gateway remains Docker-internal. The helper rejects wildcard,
public, hostname, and malformed bind values. It applies to the current runtime
restart, so rerun it after a later ordinary product restart while this
experimental mode is needed. Authentication and TLS are intentionally absent
in this phase; use only a trusted LAN. The route contract, Android work, and
physical proof are documented in the
[JingSi LAN Web client design](jingsi-lan-connection-design.md).

The golden eval script exercises internal-only `/chat` and `/metrics` routes, so
it intentionally targets an isolated host-development Gateway rather than the
product WebChat ingress. Start that Gateway first, then run:

```bash
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
GATEWAY_URL=http://127.0.0.1:18789 \
bash scripts/run-eval.sh
```

The `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS` setting is intentionally explicit. It lets eval fixtures work while keeping `browser.read` closed to private hosts by default.

## Host Development Runtime

The standard development runtime on the validated DGX Spark host is the
containerized external-model/OCR/PostgreSQL topology:

```bash
npm run dev
```

Use `npm run dev:gateway` or `npm run dev:webchat` to rebuild a single
application container without switching the runtime back to mock/file mode.

For isolated host-process debugging only, run the mock/file Gateway and Vite
server in separate terminals:

```bash
npm run dev:gateway:host
npm run dev:webchat:host
```

The host WebChat dev server listens on `0.0.0.0:18790` and proxies API requests
to the loopback-only Gateway. Set `SPARKCLAW_API_TOKEN` and
`VITE_SPARKCLAW_API_TOKEN` to the same value for protected host-process runs.

## External MCP Connections

The owner-facing External MCP surface is installed but disabled by default. It
becomes ready only when SparkClaw can ask the actual ISCP Domain authority for
a standard `iscp.pairing_ticket.v2` object:

```dotenv
SPARKCLAW_ISCP_PAIRING_ENABLED=true
SPARKCLAW_ISCP_DOMAIN_ID=<sparkclaw-domain-id>
SPARKCLAW_ISCP_AUTHORITY_URL=https://authority.example/v1/pairing-tickets
SPARKCLAW_ISCP_AUTHORITY_TOKEN_ENV=SPARKCLAW_ISCP_AUTHORITY_TOKEN
SPARKCLAW_ISCP_AUTHORITY_TOKEN=<authority-client-token>
```

Use `SPARKCLAW_ISCP_AUTHORITY_TOKEN_FILE` instead of `TOKEN_ENV` when the secret
is mounted as a mode-`0600` file. Configure exactly one token source. The
authority URL must be HTTPS except for loopback/private development services,
and requests are bounded to 15 seconds and 64 KiB by default.

This is a SparkClaw authority-adapter contract, not an HTTP endpoint currently
defined by ISCP v0.1.0. SparkClaw sends an authenticated `POST` with type
`sparkclaw.iscp_pairing.request.v1`, a stable `request_ref`, the configured
`domain_id`, `max_uses: 1`, and `ttl_seconds`. The authority response contains
only `authority_ref` and a signed standard `ticket` object. The authority still
owns signing, consumption, Device Proof, Provisioning, Trust Grants, and Relay
credentials; SparkClaw neither stores the signed ticket nor exposes a claim
endpoint.

After configuration, enable the generic MCP connector and **Connect through
ISCP** in WebChat Settings. Issue and transfer the copy-once ISCP Pairing Ticket,
complete enrollment in the external Access Gateway, then issue the separate MCP
Access Ticket with the fixed conversation scope. Disabling the ISCP switch
immediately rejects MCP Bridge ingress without deleting existing onboarding,
ticket, or binding records. A real authority implementation, external Access
Gateway, and live Relay path are still required for production end-to-end
access.

### Direct LAN MCP

Direct MCP uses the existing WebChat ingress instead of publishing Gateway or
adding a separate port. Enable **Allow LAN access** in WebChat Settings, then
connect to:

```text
URL: http://<sparkclaw-lan-ip>:18790/mcp
Initial Authorization: Bearer <SPARKCLAW_MCP_ACCESS_TICKET>
MCP-Protocol-Version: 2025-06-18
```

WebChat always listens on `18790`, but Nginx forwards only the exact `/mcp`
route to the internal Gateway. The switch is an application authorization gate:
when it is off, `/mcp` returns 404. Gateway `18789` has no host port publication.
When no ISCP Domain is configured, direct access uses
`SPARKCLAW_MCP_LOCAL_DOMAIN_ID` (`sparkclaw-local` by default); a configured
ISCP Domain takes precedence so both transports share one access-ticket domain.

MCP Access Tickets are valid for 24 hours and remain single-use. The first
`initialize` consumes the ticket and returns `Mcp-Session-Id`; the client keeps
that header for `notifications/initialized`, `tools/list`, and `tools/call`.
The session ID is a bearer credential and must not be logged or persisted in
source code; SparkClaw stores only its SHA-256-derived identity. Direct LAN MCP
uses plain HTTP and provides no ISCP encryption, Device Proof, Relay, or Trust
Grant. Limit it to a trusted LAN and revoke unused MCP Bindings. MCP calls use
the independent MCP Access Ticket and do not require `SPARKCLAW_API_TOKEN`;
that setting, when used, protects the separate owner WebChat/Gateway API.

`/mcp` validates the browser `Origin` header as a DNS-rebinding defense.
Requests without an `Origin` header (curl, native MCP clients such as
LocalMind) are unaffected. When the header is present it must name a loopback
origin, the gateway's own bind-address origin, or an entry in
`mcp_access.allowed_origins` (also settable as a comma-separated
`SPARKCLAW_MCP_ALLOWED_ORIGINS`); anything else receives 403. The list is
empty by default — add exact origins such as `https://panel.example.com` only
for a trusted browser-based MCP client served from another origin.

## LocalMind MCP

LocalMind access is opt-in. Add an `mcp_servers.localmind` block to the active
SparkClaw JSON configuration as documented in
[External integrations](integrations.md), then provide the referenced values in
the deployment environment:

```bash
LOCALMIND_MCP_URL=https://localmind.example/api/workspaces/<workspace-id>/mcp
LOCALMIND_MCP_TOKEN=<workspace-bound-token>
```

`docker/compose.yaml` forwards both variables to Gateway. The shipped LocalMind
entry is inert while either value is empty. Keep the token out of committed
configuration. Restart Gateway after changing the JSON entry; it validates the
fixed server identity and exact three-tool task contract, then repeats that
refresh at the configured interval.

For a host LocalMind reached from containerized Gateway, replace `localhost`
with `host.docker.internal`. A LocalMind service attached to
`sparkclaw_internal` can use its Compose service name. Public endpoints must use
HTTPS; private or container HTTP additionally requires
`allow_private_http: true`. The endpoint path must remain exactly
`/api/workspaces/<workspace-id>/mcp`.

## ISCP Bridge Process

The JingSi App integration runs as a separate host process so it can use the GB10
operating-system keyring and reach only the loopback Gateway. Enable Gateway token
auth, provision the identity and Cloud-issued enrollment bundle, then run:

```bash
cd services/gateway
mkdir -p ../../bin
go build -o ../../bin/sparkclaw-iscp-bridge ./cmd/iscp-bridge
../../bin/sparkclaw-iscp-bridge run -config ../../configs/iscp-bridge.example.json
```

The same Gateway bearer value, or a dedicated paired-client token, must be stored
in the Bridge `gateway.token` file with mode `0600`. The enrollment bundle is also
`0600`; the production Ed25519 identity key stays in the system keyring. Install
the binary under a service manager with restart-on-failure, but do not restart on
an explicit device-revocation error until a new enrollment bundle is installed.

See [ISCP Bridge](iscp-bridge.md) for enrollment, schema, credential rotation,
mock mode, and the exact security boundary.

## State Backends

The product `.env` template, `npm start`, one-command deployment, and boot
service select PostgreSQL. An older `data/memory/gateway-state.json` file is not
migrated, imported, or deleted during an upgrade; the PostgreSQL product runtime
starts from the records already in PostgreSQL. The project is pre-release and
does not provide a file-to-PostgreSQL migration tool.

File state used by isolated host/mock runs:

```text
data/memory/gateway-state.json
```

Useful options:

```bash
SPARKCLAW_STATE_BACKEND=memory
SPARKCLAW_STATE_PATH=/path/to/state.json
SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS=180
SPARKCLAW_STATE_READ_TIMEOUT_SECONDS=10
SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS=30
SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS=60
SPARKCLAW_STATE_ENCRYPT_AT_REST=true
SPARKCLAW_STATE_ENCRYPTION_KEY_FILE=/path/to/key
```

Postgres-backed state:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d postgres

SPARKCLAW_STATE_BACKEND=postgres \
SPARKCLAW_STATE_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw?sslmode=disable' \
SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS=180 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

`state.backend` is case-insensitive after trimming and accepts only `memory`,
`file`, or `postgres`. File state requires an absolute normalized path after
load. When file encryption is enabled, configure exactly one of the direct key
or a readable, non-empty key file. PostgreSQL requires a non-empty DSN;
`SPARKCLAW_POSTGRES_DSN` remains a legacy override that wins over
`SPARKCLAW_STATE_DSN` when both are set. Store startup defaults to 180 seconds
and accepts values from 1 through 900. Read, write, and transaction operation
budgets default to 10, 30, and 60 seconds; each accepts values from 1 through
900 and preserves a shorter caller deadline.

Gateway starts listening only after the selected backend probe succeeds.
`/readyz` returns `503` with a bounded Store status when runtime supervision
marks the backend unready. Recovery probes retry periodically. `/metrics`
exports `sparkclaw_store_ready`, active operations, operation totals, and total
duration with bounded backend/repository/operation/mode/outcome labels; it does
not expose state paths, DSNs, owner IDs, record IDs, or raw errors. Shutdown
rejects new Store operations, drains admitted work within its close deadline,
and closes the backend. See [Store](store.md) for the complete state machine and
failure contract.

Gateway applies the embedded ordered schema before readiness. It serializes new
runners with an advisory lock and records immutable filename/checksum rows in
`sparkclaw_schema_migrations`. A database without a ledger is treated as an
unversioned adoption candidate: all migrations, compatibility copies and
normalizations, exact catalog validation, and ledger rows commit atomically.
Checksum drift, unknown or gapped versions, incompatible legacy natural keys,
or catalog drift fail startup without accepting a partial migration. The
PostgreSQL image no longer copies schema SQL into `docker-entrypoint-initdb.d`.

S1 is a non-rolling database upgrade. Stop every old Gateway process before
starting a binary that owns these migrations. The migration also locks the four
Weixin/external compatibility tables against old writes until commit, but that
lock is a backstop rather than a rolling-upgrade protocol.

The project-standard data service image remains PostgreSQL 18 with pgvector
available, but Gateway does not create or query a document-chunk/vector schema
while workspace knowledge/RAG is deferred.

PostgreSQL 18 stores clusters under a major-version-specific subdirectory, so
Compose mounts the versioned `sparkclaw_pg18` volume at
`/var/lib/postgresql`. An existing PostgreSQL 17 `sparkclaw_pg` volume created
with the old `/var/lib/postgresql/data` mount must be backed up and migrated
with `pg_dump`/`pg_restore`. Do not attach the old data directory directly to
PostgreSQL 18 or delete it to force a clean start.

## Artifact Storage

The default artifact backend is filesystem object storage under `data/artifacts/{bucket}/...`. Use S3-compatible storage by setting:

```bash
SPARKCLAW_ARTIFACT_BACKEND=s3
SPARKCLAW_S3_ENDPOINT=http://127.0.0.1:9000
SPARKCLAW_S3_ACCESS_KEY=sparkclaw
SPARKCLAW_S3_SECRET_KEY=sparkclaw-local
```

Compose provides MinIO:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d minio minio-init
```

Artifacts include tool observations, browser snapshots, generated documents and
media, memory exports, patch rollback files and eval failure archives.

## Sandbox Runner

For host binary runs, Gateway can use `SPARKCLAW_SANDBOX_BACKEND=local-docker`.

Compose uses a standalone sandbox runner:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d sandbox-runner
```

Standalone runner boundary outside Compose:

```bash
sparkclaw-sandbox-runner
SPARKCLAW_SANDBOX_BACKEND=http \
SPARKCLAW_SANDBOX_RUNNER_URL=http://127.0.0.1:18889 \
go run ./services/gateway/cmd/sparkclaw -config configs/sparkclaw.default.json
```

When the runner talks to a host Docker socket, set `SPARKCLAW_SANDBOX_HOST_WORKSPACE_ROOT` and `SPARKCLAW_SANDBOX_CONTAINER_WORKSPACE_ROOT` if paths differ between host and container.


## DGX Spark Data Services

Start durable state, artifacts, sandbox, Gateway and WebChat:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d \
  postgres minio minio-init sandbox-runner gateway webchat
```

For model-backed operation, recreate Gateway in external mode after the selected endpoints are healthy:

```bash
scripts/restart_runtime_compose.sh
```

Use this script instead of a plain `docker compose up --force-recreate gateway webchat` for the durable product runtime. It loads `docker/env/sparkclaw.single-fast.env`, `docker/env/sparkclaw.asr.env`, and `docker/env/sparkclaw.ocr.env` after `.env`, then stacks the ASR and OCR overlays. This selects PostgreSQL, keeps both logical chat profiles mapped to Fast, and enables speech transcription and document OCR against the co-resident services. The script starts and waits for PostgreSQL when Gateway is requested, then checks `/readyz` with a bounded request and exits non-zero unless Gateway reports `model_mode=external` and `state_backend=postgres`. Set `SPARKCLAW_RUNTIME_ENV` explicitly to use another chat/runtime profile; the ASR and OCR environments remain part of this product runtime.

When the host has a resolvable X11/XWayland display, the script additionally stacks the `docker/compose.visible-browser.yaml` overlay so login handoffs can open a visible Chromium on the owner's desktop. On a headless host it starts the same stack without the overlay; hidden browser automation remains available and the base compose file grants Gateway no access to any host display.

## DGX Spark Model Services

Host-side vLLM scripts:

```bash
scripts/serve_fast.sh
scripts/serve_deep.sh
```

Compose vLLM services:

```bash
scripts/serve_models_compose.sh single-fast
scripts/serve_models_compose.sh fast
scripts/serve_models_compose.sh deep
scripts/serve_models_compose.sh dual-light
scripts/serve_models_compose.sh dual-light-asr
scripts/serve_models_compose.sh dual-light-chat
scripts/serve_models_compose.sh embedding
scripts/serve_models_compose.sh guard
scripts/serve_models_compose.sh asr
scripts/serve_models_compose.sh all
scripts/serve_models_compose.sh all-with-asr
```

With no argument, `serve_models_compose.sh` also selects `single-fast`. This is
the current product startup path: it stops a previously running Deep container
and starts Fast, embedding, guard, ASR, and OCR together with the single-Fast,
ASR, and OCR environments. The older `single-fast-with-ocr` name is an alias for this
same startup. Deep and dual-light commands are explicit test/benchmark
entrypoints. The command waits for every selected service to become healthy.
Fast is not healthy until a bounded production-shaped `/chat/completions`
request succeeds. On the current Qwen3.6 tokenizer it carries about 3.4K input
tokens and forces a 480-token decode, so startup absorbs the long-prompt and
generation cold path seen by Tree routing. Guard separately requires its small
bounded completion. The readiness helper is copied into a local derivative of
`SPARKCLAW_VLLM_IMAGE`; health checks have no source-file bind mount back to the
checkout. Each marker lives on a dedicated container-local tmpfs and includes
the current model-process start time, so a new process cannot reuse readiness
from its predecessor. A successful warmup remains healthy if its marker cannot
be written; if even the dedicated tmpfs is unavailable, a later probe may repeat
the warmup. Periodic checks use the lightweight model listing endpoint after a
marker is stored. If any member of the five-service product group is absent,
stopped, unhealthy, or configuration-drifted, the shortcut force-recreates all
five together. `SPARKCLAW_FORCE_MODEL_RECREATE=true` requests the same action
for a healthy group. It never adds or recreates one model alone inside the
resident product group. The single-Fast embedding
endpoint admits 128 short sequences under its fixed 2 GiB KV budget so the
110-entry startup index completes within its 20-second bound.

Default endpoints:

| Endpoint role | Served name | Endpoint |
|---|---|---|
| fast | `sparkclaw-fast` | `http://127.0.0.1:8001/v1` |
| deep | `sparkclaw-deep` | `http://127.0.0.1:8002/v1` |
| embedding | `sparkclaw-embedding` | `http://127.0.0.1:8003/v1` |
| guard | `Qwen/Qwen3Guard-Gen-0.6B` | `http://127.0.0.1:8005/v1` |
| asr | `sparkclaw-asr` | `http://127.0.0.1:8006` |
| OCR adapter | `sparkclaw-ocr` | `http://127.0.0.1:8007/v1` |

Check endpoints:

```bash
curl -fsS http://127.0.0.1:8001/v1/models
curl -fsS http://127.0.0.1:8003/v1/models
curl -fsS http://127.0.0.1:8005/v1/models
curl -fsS http://127.0.0.1:8006/v1/models
curl -fsS http://127.0.0.1:8007/v1/models
```

Port `8002` is available only after an explicit `deep`, `dual-light`, or `all`
startup and is not part of the current single-Fast readiness check.

Important environment variables:

- `SPARKCLAW_VLLM_IMAGE` (embedding, guard, and ASR base image)
- `SPARKCLAW_CHAT_VLLM_IMAGE` (Fast/Deep chat image; defaults to vLLM 0.24.0 for NVFP4)
- `SPARKCLAW_FORCE_MODEL_RECREATE` (`false` by default; set `true` for one explicit full model-group refresh)
- `SPARKCLAW_FAST_MODEL_ID`, `SPARKCLAW_FAST_MODEL`, `SPARKCLAW_FAST_CONTEXT_TOKENS`, `SPARKCLAW_FAST_MAX_MODEL_LEN`, `SPARKCLAW_FAST_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_FAST_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_FAST_MAX_NUM_SEQS`, `SPARKCLAW_FAST_SPECULATIVE_CONFIG`
- `SPARKCLAW_DEEP_MODEL_ID`, `SPARKCLAW_DEEP_MODEL`, `SPARKCLAW_DEEP_CONTEXT_TOKENS`, `SPARKCLAW_DEEP_MAX_MODEL_LEN`, `SPARKCLAW_DEEP_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_DEEP_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_DEEP_MAX_NUM_SEQS`, `SPARKCLAW_DEEP_SPECULATIVE_CONFIG`
- `SPARKCLAW_EMBEDDING_MODEL_ID`, `SPARKCLAW_EMBEDDING_MODEL`, `SPARKCLAW_EMBEDDING_MAX_MODEL_LEN`, `SPARKCLAW_EMBEDDING_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_EMBEDDING_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_EMBEDDING_MAX_NUM_SEQS`
- `SPARKCLAW_GUARD_MODEL_ID`, `SPARKCLAW_GUARD_MODEL`, `SPARKCLAW_GUARD_SERVED_NAME`, `SPARKCLAW_GUARD_MAX_TOKENS`, `SPARKCLAW_GUARD_CONTEXT_TOKENS`, `SPARKCLAW_GUARD_MAX_MODEL_LEN`, `SPARKCLAW_GUARD_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_GUARD_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_GUARD_MAX_NUM_SEQS`
- `SPARKCLAW_ASR_MODEL_ID`, `SPARKCLAW_ASR_SERVED_NAME`, `SPARKCLAW_ASR_MAX_MODEL_LEN`, `SPARKCLAW_ASR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_ASR_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_ASR_MAX_NUM_SEQS`, `SPARKCLAW_ASR_DTYPE`
- `SPARKCLAW_SPEECH_ENABLED`, `SPARKCLAW_SPEECH_BASE_URL`, `SPARKCLAW_SPEECH_ALLOWED_HOSTS`, `SPARKCLAW_SPEECH_MODEL`, `SPARKCLAW_SPEECH_TIMEOUT_SECONDS`, `SPARKCLAW_SPEECH_MAX_AUDIO_SECONDS`, `SPARKCLAW_SPEECH_MAX_UPLOAD_BYTES`
- `SPARKCLAW_OCR_ENABLED`, `SPARKCLAW_OCR_PROVIDER` (`openai-http`, the default and only adapter today; `disabled` turns the adapter off explicitly), `SPARKCLAW_OCR_BASE_URL`, `SPARKCLAW_OCR_ALLOWED_HOSTS`, `SPARKCLAW_OCR_MODEL`, `SPARKCLAW_OCR_TIMEOUT_SECONDS`, `SPARKCLAW_OCR_MAX_UPLOAD_BYTES`, `SPARKCLAW_OCR_MAX_OUTPUT_BYTES`, `SPARKCLAW_OCR_MAX_TOKENS`, `SPARKCLAW_OCR_MAX_CONCURRENCY`, `SPARKCLAW_OCR_MAX_PENDING`
- `SPARKCLAW_OCR_IMAGE`, `SPARKCLAW_OCR_MODEL_ID`, `SPARKCLAW_OCR_SERVED_NAME`, `SPARKCLAW_OCR_MAX_MODEL_LEN`, `SPARKCLAW_OCR_GPU_MEMORY_UTILIZATION`, `SPARKCLAW_OCR_KV_CACHE_MEMORY_BYTES`, `SPARKCLAW_OCR_MAX_NUM_SEQS`
- `SPARKCLAW_MODEL_DISABLE_THINKING`
- `SPARKCLAW_MODEL_HTTP_TIMEOUT_SECONDS`
- `HF_TOKEN` or `HUGGING_FACE_HUB_TOKEN`

Use `*_MODEL_ID` for the Hugging Face checkpoint loaded by the serving container and `*_MODEL` for the OpenAI-compatible served name sent by Gateway.

### Dedicated Qwen3Guard

The guard lane uses the public generative checkpoint
`Qwen/Qwen3Guard-Gen-0.6B`; `Qwen/Qwen3Guard-0.6B` is not a valid public
checkpoint ID. Start only this endpoint with:

```bash
SPARKCLAW_MODEL_LOADING_PROFILE=single-fast scripts/serve_models_compose.sh guard
curl -fsS http://127.0.0.1:8005/v1/models
```

The single-GB10 `single-fast` profile limits guard to 16K context, 2 GiB KV
cache, one sequence and eager execution. Qwen3Guard returns its native
`Safety: Safe|Unsafe|Controversial` and `Categories:` format; Gateway maps
those severities to `allow`, `block` and `review`. Because SparkClaw has no
human safety-review queue, both `review` and `block` stop the run before routing
or tool execution. If the external endpoint is unavailable, Gateway records
`mock=true` and uses the local heuristic fallback. Compose allows the initial
real-inference readiness probe up to 110 seconds and does not report the Guard
container healthy until that probe has produced a non-empty completion.

### OvisOCR2 Document OCR

The document OCR adapter uses
[`ATH-MaaS/OvisOCR2`](https://huggingface.co/ATH-MaaS/OvisOCR2) through an
OpenAI-compatible vLLM chat-completions endpoint. It parses page images into
Markdown while preserving readable order, formulas, and tables. Fast remains
the visual-semantics and Workflow-reasoning model; OCR output is untrusted
document evidence and never selects a model lane or authorizes an edit.

The overlay pins vLLM `0.22.1`, exposes port `8007` only on loopback, uses an
explicit 2 GiB KV cache budget, and shares the Hugging Face cache. The default
`single-fast` command starts OCR in the same Compose operation as Fast,
embedding, guard, and ASR:

```bash
scripts/serve_models_compose.sh single-fast
curl -fsS http://127.0.0.1:8007/v1/models
```

Run Gateway and WebChat with the matching OCR adapter configuration:

```bash
scripts/restart_runtime_compose.sh
```

For host-side doctor checks, keep the Compose service URL for Gateway and
override only the check destination:

```bash
set -a
. docker/env/sparkclaw.ocr.env
set +a
SPARKCLAW_OCR_BASE_URL=http://127.0.0.1:8007/v1 scripts/doctor.sh
```

OCR is enabled in the current single-Fast product runtime. Selected Office/PDF
images receive bounded OCR Markdown; scanned PDF pages invoke it automatically.
Page rendering is limited to eight pages, 4 MiB per rendered page, and 16 MiB
total per PDF read. A disabled,
busy, timed-out, malformed, or incomplete OCR response is reported as partial
evidence. Combined startup on the GB10 has been validated after stopping all
resident model services and invoking the joint startup; adding OCR alone to the
already-resident stack failed during CUDA initialization. Keep the explicit 2 GiB KV cache: utilization-based
allocation alone produced a negative available-cache calculation. One
concurrent image and scanned-PDF smoke call completed successfully, but it is
not an OCR quality baseline; broader document measurements are still required.

### Qwen3-ASR Speech

SparkClaw speech uses the OpenAI-compatible transcription endpoint. Qwen3-ASR supports vLLM serving and the OpenAI transcription API. The default product group uses `Qwen/Qwen3-ASR-0.6B` and downloads it through the same Hugging Face cache used by the other resident models. Switch to `Qwen/Qwen3-ASR-1.7B` only after measuring memory and latency with the complete resident group.

The ASR service uses an explicit 2 GiB KV cache budget. During a five-service
cold start, utilization-based allocation alone accounted for the audio encoder
profiling peak and calculated `-10.24 GiB` of available KV cache after loading
the 1.53 GiB model, so the fixed cache is required rather than optional tuning.

The [official Qwen3-ASR README](https://github.com/QwenLM/Qwen3-ASR) recommends ModelScope for users in Mainland China. To use a preloaded ModelScope copy instead, download it into the shared cache and set `SPARKCLAW_ASR_MODEL_ID=/models/modelscope/Qwen3-ASR-0.6B` in the process environment that invokes startup:

```bash
python3 -m pip install -U modelscope
mkdir -p data/models/modelscope/Qwen3-ASR-0.6B
modelscope download --model Qwen/Qwen3-ASR-0.6B --local_dir data/models/modelscope/Qwen3-ASR-0.6B
```

The ASR compose override builds a small derivative of the local vLLM image that adds audio dependencies without changing the main text-model image:

- Compose: `docker/compose.asr.yaml`
- Environment: `docker/env/sparkclaw.asr.env`
- Image recipe: `docker/images/asr-vllm.Dockerfile`
- Default served model: `sparkclaw-asr`
- Default model ID: `Qwen/Qwen3-ASR-0.6B`

Start ASR by itself:

```bash
scripts/serve_models_compose.sh asr
```

The default product startup already includes ASR. The historical dual-light
experiment can also be started with ASR explicitly:

```bash
scripts/serve_models_compose.sh dual-light-asr
```

Run Gateway and WebChat with speech enabled:

```bash
docker compose \
  --env-file docker/env/sparkclaw.dual-light.env \
  --env-file docker/env/sparkclaw.asr.env \
  -f docker/compose.yaml \
  -f docker/compose.dual-light.yaml \
  -f docker/compose.asr.yaml \
  --profile models-local up -d gateway webchat
```

Check the ASR endpoint from the host:

```bash
curl -fsS http://127.0.0.1:8006/health
curl -fsS http://127.0.0.1:8006/v1/models
curl -fsS http://127.0.0.1:8006/v1/audio/transcriptions \
  -F model=sparkclaw-asr \
  -F response_format=json \
  -F file=@/path/to/sample.wav
```

For host-side doctor checks, keep the container URL in `docker/env/sparkclaw.asr.env` for Gateway but override the base URL to loopback:

```bash
set -a
. docker/env/sparkclaw.asr.env
set +a
SPARKCLAW_SPEECH_BASE_URL=http://127.0.0.1:8006 scripts/doctor.sh
```

Validated DGX Spark notes from 2026-05-24:

- NVIDIA GB10 and driver `580.159.03` were visible on host and from CUDA containers.
- `vllm/vllm-openai:cu130-nightly` worked on arm64.
- `Qwen/Qwen3.6-27B-FP8`, `Qwen/Qwen3.6-35B-A3B-FP8`, `Qwen/Qwen3-Embedding-0.6B`, and `Qwen/Qwen3Guard-Gen-0.6B` were validated.
- Full-context fast+deep dual residency did not fit with both chat lanes at 128K context and MTP enabled. Operate one 128K/MTP chat lane at a time, route both Gateway profiles to the loaded lane for evals, or reduce context/MTP and re-measure.

Current single-Fast product startup:

```bash
scripts/serve_models_compose.sh single-fast
scripts/restart_runtime_compose.sh
```

This applies the single-Fast, ASR, and OCR environments plus the bounded service
settings from `docker/compose.dual-light.yaml`, `docker/compose.asr.yaml`, and
`docker/compose.ocr.yaml`. Fast, embedding, guard, ASR, and OCR start together.
Gateway sends both logical chat profiles to `sparkclaw-fast`, uses
`sparkclaw-asr` for speech transcription, and uses `sparkclaw-ocr` for document
OCR. The chat endpoint loads `nvidia/Qwen3.6-35B-A3B-NVFP4` through the
dedicated vLLM 0.24.0 image. SparkClaw supplies the checkpoint ID and capacity
budgets only; vLLM reads the ModelOpt metadata and owns activation precision,
quantization dispatch, and kernel/backend selection. No product or targeted
chat-loading default retains an FP8 chat checkpoint.

Historical light dual-residency experiment:

```bash
scripts/serve_models_compose.sh dual-light
python3 scripts/record_model_loading.py --profile dual-light-v1
```

The `dual-light` shortcut applies `docker/env/sparkclaw.dual-light.env` and `docker/compose.dual-light.yaml`: fast 32K with 8G KV cache, deep 64K with 12G KV cache, embedding 8K with 2G KV cache, and guard 16K with 2G KV cache. MTP is off and sequence concurrency is low. Start this full profile before running Gateway in external mode.

Use `dual-light-chat` only when intentionally measuring chat lanes without auxiliary endpoints.

Run the repeatable endpoint benchmark:

```bash
SPARKCLAW_FAST_BASE_URL=http://127.0.0.1:8001/v1 \
SPARKCLAW_FAST_MODEL=sparkclaw-fast \
SPARKCLAW_DEEP_BASE_URL=http://127.0.0.1:8002/v1 \
SPARKCLAW_DEEP_MODEL=sparkclaw-deep \
SPARKCLAW_MODEL_DISABLE_THINKING=true \
python3 scripts/benchmark_models.py --append-markdown benchmarks/model_baseline.md
```

Run real-model golden eval:

```bash
SPARKCLAW_EXPECT_REAL_MODELS=1 \
SPARKCLAW_MODEL_MODE=external \
BROWSER_FIXTURE_URL=http://host.docker.internal:18791 \
BROWSER_FIXTURE_BIND=0.0.0.0 \
bash scripts/run-eval.sh
```

The historical validated real-model run completed 58 golden cases. On 2026-08-24, the restored vLLM-managed path passed the current 47-case matrix with 15 tool calls, 2 approvals and 1 memory candidate; all Fast, Deep, Embedding and Guard calls were real (`mock=0`) with no model errors. The forced-W4A4 result remains historical because that experiment was rolled back. See [model_baseline.md](../benchmarks/model_baseline.md) for benchmark rows and operating notes.

## Backup And Restore

Back up these paths or volumes:

- `.env` secret template values, stored outside git
- `data/memory`
- `data/traces`
- `data/artifacts`
- `data/workspaces`
- Postgres volume `sparkclaw_pg18`
- MinIO volume `sparkclaw_minio`
- `data/models` if model cache reuse matters

For Postgres:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml exec postgres \
  pg_dump -U sparkclaw sparkclaw > sparkclaw.sql
```

For filesystem state, stop Gateway before copying state files if possible.

## Upgrade Flow

1. Save or export important state.
2. Pull or apply code changes.
3. Rebuild images:

```bash
sudo -n docker compose --env-file .env -f docker/compose.yaml --profile models-local build
```

4. Start the target profile.
5. Run `bash scripts/doctor.sh`.
6. Run mock golden eval.
7. For DGX Spark model changes, run endpoint checks and append a new benchmark section.

### Behavior changes to check when upgrading past 2026-07-30

- Visible-browser login handoffs now require stacking the
  `docker/compose.visible-browser.yaml` overlay; the base compose file no
  longer exposes the host X11 socket. `scripts/restart_runtime_compose.sh`
  applies the overlay automatically when a display resolves.
- Telegram and Weixin now both ship disabled in typed config, Compose, and the
  example environment. Enable a channel from WebChat before account setup.
  `SPARKCLAW_TELEGRAM_ENABLED` and
  `SPARKCLAW_WEIXIN_NOTIFICATION_ENABLED` only provide the initial value when
  no persisted owner choice exists; bindings and credentials never auto-enable
  a channel.
- The transitional skills registry was removed, including `GET /api/skills`
  and the `skills` config section; workflows are the only execution path.
- Guard replies that parse to no recognizable verdict resolve to a
  non-blocking `unknown` verdict recorded as a `guard.verdict_unknown`
  audit event; explicit `review`/`block` verdicts still stop the run.
  Config-less boots now run with model thinking disabled, matching the
  shipped default config.
- Runtime budget keys split into `workflow_stage_max_*` and
  `workflow_run_max_*` (legacy `workflow_step_max_*`/`react_max_*` keys
  still map; see [Workflow execution](workflow-execution.md)).
- Observation pressure now has two boundaries:
  `workflow_run_observation_compaction_bytes=36000` starts rolling compaction,
  while `workflow_run_max_observation_bytes=48000` is a hard stop checked first.
  Legacy config derives the lower value as 75% of the resolved maximum; two
  explicit values must satisfy `0 < compaction < maximum`.
- `workflow_stage_max_observation_reads=2` independently limits executed
  `observation.read` support calls. Environment overrides are
  `SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES` and
  `SPARKCLAW_WORKFLOW_STAGE_MAX_OBSERVATION_READS`.

## Secure Defaults

- Keep **Allow LAN access** off unless a direct MCP client needs it. Gateway is
  private to the Docker network in the shipped Compose topology.
- Restrict WebChat `18790` to an owner-trusted LAN. MCP Access Tickets protect
  `/mcp`; they do not authenticate the other WebChat API routes.
- Keep the unauthenticated experimental JingSi listener unpublished unless it
  is actively being tested; when enabled, bind `18793` to one RFC1918 address,
  never a wildcard or public interface.
- Keep dangerous and reversible tools approval-gated.
- Keep shell execution sandboxed and network-disabled.
- Treat browser/email/file observations as untrusted.
- Keep the host desktop closed to containers: the base compose file mounts no
  X11 socket, and the `docker/compose.visible-browser.yaml` overlay belongs
  only on the trusted single-owner desktop runtime that needs visible login
  handoffs.
- Keep `.env`, model weights, state encryption keys and downloaded data out of git.
- Scan diffs for tokens before handoff.

## Troubleshooting

| Symptom | Check |
|---|---|
| Docker permission denied | Use `sudo -n docker ...` or add the user to the Docker group. |
| Golden eval browser step fails | Start Gateway with `SPARKCLAW_BROWSER_READ_ALLOW_HOSTS=host.docker.internal` for Docker eval or `127.0.0.1` for host eval. |
| CUDA or Triton reports `operation not permitted` after a host restart | Run `scripts/serve_models_compose.sh single-fast`. A stopped or unhealthy member triggers automatic whole-group recreation with fresh runtime caches while retaining `data/models`; set `SPARKCLAW_FORCE_MODEL_RECREATE=true` to force the same recovery manually. |
| Model returns reasoning but no answer | Set `SPARKCLAW_MODEL_DISABLE_THINKING=true`. |
| Postgres vector extension unavailable | SparkClaw falls back to JSON vectors and Gateway-side hybrid scoring. |
| 128K fast+deep does not fit | Run one chat lane at a time or lower context/MTP and re-benchmark. |
