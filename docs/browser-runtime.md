# Browser Runtime

> Language: English | [简体中文](../zh-cn/docs/browser-runtime.md)

This document is the current browser implementation and operating guide.
SparkClaw uses one host-owned Chromium process, a dedicated persistent profile,
and pinned `agent-browser` `0.32.3` attached through a protected Host-CDP
endpoint. The previous container Chromium, Xvfb, profile mount, X11 overlay,
profile lease, and launch fallback have been removed.

The detailed migration rationale remains in
[Host-CDP browser design](host-cdp-browser-design.md).

## Runtime Topology

```text
Browser Workflow
  -> ToolHub browser capability
  -> internal/browserautomation
  -> private agent-browser MCP process
       AGENT_BROWSER_CDP=<capability WebSocket URL>
  -> browserd CDP proxy on a Docker bridge
  -> host sparkclaw-browserd
  -> one SparkClaw Chromium process
  -> dedicated owner-only persistent profile
```

There is no Playwright fallback, direct CDP execution backend, daily-browser
attachment, cookie export, or container-side Chromium launcher.

## Process And Profile Ownership

`sparkclaw-browserd` is a systemd user service and the sole owner of Chromium.
It installs the approved architecture-specific artifact under
`/opt/sparkclaw`, launches it with a dedicated profile under the owner's local
data directory, and publishes browser health through an owner-only runtime
directory.

The default paths are:

| Resource | Host path |
|---|---|
| Browser daemon | `/opt/sparkclaw/browserd/sparkclaw-browserd` |
| Browser config | `~/.config/sparkclaw/browserd.json` |
| Persistent profile | `~/.local/share/sparkclaw/browser/default/user-data` |
| Runtime directory | `${XDG_RUNTIME_DIR}/sparkclaw/browserd` |
| Capability endpoint | `${XDG_RUNTIME_DIR}/sparkclaw/browserd/cdp-endpoint` |
| Desktop launcher | `~/.local/share/applications/sparkclaw-browser.desktop` |

The runtime directory and profile are mode `0700`. The endpoint is an ordinary,
non-symlink file owned by the deployment user with permissions no wider than
`0600`. Gateway runs with the same numeric UID/GID and receives only a read-only
mount of the browserd runtime directory. It never receives the profile or raw
Chromium debugging port.

`browserd` starts headed when it can validate the owner's X11/XWayland display;
otherwise it starts host-side headless Chromium. Opening **SparkClaw Browser**
from the desktop launcher can restart the same browserd-owned process against a
valid desktop display and the same profile. It never launches a second process
against that profile.

## Capability Endpoint

Browserd binds its capability proxy only to loopback and discovered `docker0`
or `br-*` Docker bridge addresses. The endpoint file contains browser identity,
generation, presentation, qualified version, and capability-bearing WebSocket
URLs for container and direct-host use.

The capability value is not returned by public config/status APIs and must not
appear in logs, traces, artifacts, or model context. Browserd rotates it on
every Chromium generation. Gateway re-reads the endpoint when it creates or
replaces its private MCP connection. A stale endpoint, wrong owner, broad file
mode, profile mismatch, invalid URL boundary, or lost process fails closed.

## Tab Ownership

Connecting to the same Chromium process does not authorize SparkClaw to use
tabs that the owner already opened. Gateway maintains an in-memory allowlist of
target IDs created by each logical browser scope.

- `browser.open` lists tabs before and after `agent_browser_tab_new` and registers
  ownership only when exactly one new target exists.
- Existing owner tabs and tabs owned by another logical scope are filtered from
  list results and cannot be focused, read, clicked, typed into, selected, or
  closed.
- Implicit active-tab operations verify that the active target belongs to the
  current scope before execution.
- An ambiguous multi-target diff fails closed and records no ownership instead
  of guessing which target SparkClaw created.
- MCP transport loss, browser generation change, or reconnect clears all
  in-memory target grants. No target ownership is persisted across reconnects.

Owner inactivity never transfers tab ownership to SparkClaw.

## Login Handoff

Authentication remains inside the dedicated Chromium profile. When a Workflow
detects a login or human-verification gate, it persists a handoff and asks the
owner to open **SparkClaw Browser**. The owner completes login in the dedicated
profile; SparkClaw resumes only the frozen task-owned target after fresh target,
URL, and page evidence validation.

SparkClaw does not attach to the owner's ordinary browser profile, copy cookies,
or select an arbitrary already-open authenticated tab. Login state survives
Gateway and MCP restarts because browserd and the host profile remain running.

## Agent-Browser Lifecycle

Gateway validates the exact `agent-browser 0.32.3` CLI and MCP server version.
It starts one private MCP subprocess with the browserd WebSocket URL in
`AGENT_BROWSER_CDP`. The subprocess owns protocol transport only; browserd owns
Chromium.

During normal shutdown, Gateway first calls `agent_browser_close` for its unique
session so the invocation-owned agent-browser daemon and socket are reclaimed,
then stops the private MCP subprocess. This close detaches agent-browser from the
externally owned browser; it does not terminate browserd or Chromium. An unhealthy
MCP transport is aborted directly and relies on the bounded daemon idle timeout.
In every case the host Chromium PID must remain alive. Browserd or Chromium loss
produces a typed unavailable/reconnect failure and never falls back to a container
browser.

## Configuration

The active adapter configuration is:

```json
{
  "adapters": {
    "browserAutomation": {
      "command": "agent-browser",
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "hostCDP": {
        "endpointFile": "/run/sparkclaw/browserd/cdp-endpoint",
        "profileID": "default",
        "connectTimeoutMs": 10000
      }
    }
  }
}
```

Deployment environment values:

| Variable | Purpose |
|---|---|
| `SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST` | Host browserd runtime directory mounted read-only into Gateway |
| `SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE` | Endpoint path inside Gateway; default `/run/sparkclaw/browserd/cdp-endpoint` |
| `SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST` | Direct-host endpoint path used by setup, doctor, and PID checks |
| `SPARKCLAW_BROWSER_CDP_PROFILE_ID` | Expected browserd profile identity; default `default` |
| `SPARKCLAW_BROWSER_CDP_CONNECT_TIMEOUT_MS` | Bounded Host-CDP attachment timeout |

Legacy Chromium executable, profile directory, daemon idle, display, and
Xauthority environment fields are rejected. Legacy JSON launch/profile fields
are also rejected instead of silently selecting a compatibility path.

## Installation And Operation

The two official deployment entrypoints call the same installer:

```bash
npm run deploy:local
npm run deploy:remote
```

`scripts/install-host-browser.sh` resolves the approved artifact from
`configs/host-browser-artifacts.json`, verifies its checksum and architecture,
installs browserd, creates the systemd user service and desktop launcher, and
writes the Host-CDP paths to the selected mode-`0600` env file.

Read-only verification is available through:

```bash
bash scripts/deploy_local.sh --check
bash scripts/deploy_remote.sh --check
bash scripts/install-host-browser.sh --check --env-file .env.local
bash scripts/install-host-browser.sh --check --env-file .env.remote
```

For local setup and diagnostics:

```bash
npm run setup:browser
bash scripts/doctor.sh
systemctl --user status sparkclaw-browserd.service
/opt/sparkclaw/browserd/sparkclaw-browserd \
  --config "$HOME/.config/sparkclaw/browserd.json" status
```

Compose contains `agent-browser` but no Chromium or Xvfb. Startup checks
browserd before Gateway, runs an MCP open/snapshot/close smoke through
`AGENT_BROWSER_CDP`, stops the MCP process, and verifies that the recorded host
Chromium PID remains alive.

## Verification

Browser runtime changes require:

```bash
cd services/gateway && go test ./internal/browserautomation ./internal/config ./internal/gateway
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest scripts.test_host_browser \
  scripts.test_local_compose scripts.test_remote_compose scripts.test_deploy_remote
docker compose --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.local.env \
  -f docker/compose.yaml -f docker/compose.models.local.yaml \
  --profile product --profile models-local config --quiet
docker compose --env-file docker/env/sparkclaw.product.env \
  --env-file docker/env/sparkclaw.remote.env \
  -f docker/compose.yaml --profile product config --quiet
```

Final live acceptance also runs browserd plus the container-side MCP smoke and
confirms that owner tabs remain untouched and Chromium survives Gateway/MCP
shutdown.
