# Browser Runtime

> Language: English | [简体中文](../zh-cn/docs/browser-runtime.md)

This document describes the current production browser implementation. SparkClaw
uses one persistent owner-session Chromium profile, the checksum-pinned
SparkClaw Browser Bridge, and an owner-scoped Playwright Controller. The old
browserd, Host-CDP, and `agent-browser` paths were removed in the Phase 6
cutover.

## Runtime Topology

```text
Browser or email Workflow
  -> Gateway browserautomation / emailautomation
  -> owner-only Controller Unix socket
  -> fixed Playwright MCP or CLI client
  -> SparkClaw Browser Bridge native connection
  -> task-owned tab in persistent SparkClaw Chromium
```

Chromium and the Controller are systemd user services. Gateway mounts only the
Controller runtime directory at `/run/sparkclaw/browser-controller` as read-only.
It does not receive the browser profile, native-host manifest, display socket,
or browser executable. The Gateway image contains no Chromium, Xvfb, or browser
automation engine.

| Component | Location |
|---|---|
| Browser service | `sparkclaw-browser.service` |
| Controller service | `sparkclaw-browser-controller.service` |
| Browser config | `~/.config/sparkclaw/browser.json` |
| Persistent profile | `~/.local/share/sparkclaw/browser/default/user-data` |
| Controller runtime | `${XDG_RUNTIME_DIR}/sparkclaw/browser-controller` |
| Controller socket | `${XDG_RUNTIME_DIR}/sparkclaw/browser-controller/controller.sock` |
| Desktop launcher | `~/.local/share/applications/sparkclaw-browser.desktop` |

The pinned compatibility set is Browser Bridge `1.0.17`, Playwright MCP
`0.0.80`, Playwright CLI `0.1.19`, Playwright Library
`1.63.0-alpha-2026-08-31`, and Chromium `148.0.7778.0`. The Bridge source
closure is recorded in `configs/browser-bridge-artifacts.json`; installation
rejects changed or extra files.

## Browser Process

`sparkclaw-browser.service` is the sole long-lived owner of the default profile.
It starts a normal headed Chromium process with the fixed user-data directory
and unpacked Bridge. Its command line intentionally contains no remote-debugging,
automation, or headless flag.

Use **SparkClaw Browser** from the desktop launcher, or run:

```bash
npm run open:browser
```

The explicit open command brings the browser forward for owner work such as
login or human verification. Background acquisition and task actions do not
focus the browser or replace the active owner tab. An explicit owner handoff is
the only automation operation allowed to focus a task tab.

Authentication remains inside the persistent profile. SparkClaw never copies
cookies, exports storage state, mounts the profile into a container, or attaches
to another browser profile.

## Bridge And Controller

The Browser Bridge is independently packaged from a qualified upstream
Playwright Extension source. SparkClaw adds attachment-time task-tab
allowlisting, native Controller version handshake, stale-session cleanup, and
background-without-focus behavior. The extension ID and complete file hashes
are pinned.

The Controller owns the private Unix socket and supervises bounded MCP and CLI
processes. Each acquisition creates one task page and binds it to controller,
session, page, and credential generations. Every observation and action is
checked against that ownership before execution. Owner tabs are never selected,
read, changed, or closed.

MCP serves the generic browser adapter. CLI runs only six registered provider
handlers: probe and send revision 1 for QQ Mail, Outlook, and Gmail. Callers
cannot supply Playwright code, selectors, JavaScript, commands, storage access,
network interception, or arbitrary file paths.

MCP and CLI sessions detach without closing Chromium. Cancellation, replacement,
credential removal, browser restart, Controller restart, Gateway shutdown, and
normal completion invalidate their bounded identities and reap subprocesses and
private output. A stale identity is never rebound silently.

## Credential Boundary

Browser control uses the `playwright-extension-token-v1` credential. The owner
enters it under `Settings > Connections > Browser control`; Gateway validates a
fresh Bridge handshake before persisting encrypted Vault ciphertext. The form
never returns or prefills the token and clears the input after every save
attempt.

The raw token is not stored in Compose files, repository configuration, logs,
traces, artifacts, command arguments, or model context. The Controller service
does not retain it. Replacing or deleting the credential invalidates sessions
from the previous credential generation without changing browser authentication
state.

## Configuration

The sole production provider is `playwright-extension`:

```json
{
  "tools": {
    "browserAutomation": {
      "enabled": true,
      "provider": "playwright-extension",
      "profile": "default"
    }
  },
  "adapters": {
    "browserAutomation": {
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "settleTimeoutMs": 15000,
      "settleQuietPeriodMs": 500,
      "settlePollIntervalMs": 100,
      "routeRebindLimit": 2,
      "playwrightExtension": {
        "controllerSocket": "/run/sparkclaw/browser-controller/controller.sock",
        "profileID": "default",
        "connectTimeoutMs": 20000
      }
    }
  }
}
```

Deployment may set these machine-specific values in the selected mode-`0600`
environment file:

| Variable | Purpose |
|---|---|
| `SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST` | Host Controller runtime directory mounted read-only into Gateway |
| `SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET` | Controller socket path inside Gateway |
| `SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST` | Direct host socket used by setup, doctor, and qualification |
| `SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID` | Fixed profile identity; must be `default` |
| `SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS` | Bounded acquisition/handshake timeout |

Retired browser automation command, transport selector, and CDP variables are
rejected by configuration loading. There is no runtime fallback.

## Installation And Operations

Local and Remote deployment both call the same browser setup before Compose:

```bash
npm run setup:browser
npm run check:browser-controller
systemctl --user status sparkclaw-browser.service
systemctl --user status sparkclaw-browser-controller.service
```

`setup:browser` verifies or installs the pinned Chromium and Bridge, writes the
browser service and desktop launcher, installs Controller dependencies with
browser downloads disabled, writes the native-host manifest, starts both user
services, verifies the loaded Bridge version, and checks the private socket.
The Local and Remote startup paths repeat the check and run
`browser_controller_smoke.mjs` from Gateway after readiness.

To complete or refresh browser logins, open the persistent browser and use the
provider login action in WebChat. Login state persists across Gateway,
Controller, and Chromium restarts because the same owner-only profile remains
in place.

## Verification

```bash
python3 -m unittest scripts/test_browser_bridge.py
npm test --prefix tools/browser-bridge
npm test --prefix tools/browser-controller
npm run test:email-scripts
cd services/gateway && go test ./internal/browserautomation ./internal/browsercontrol ./internal/emailautomation ./internal/gateway ./internal/toolhub
```

Live acceptance additionally checks startup and restart, Bridge pairing and
detach, profile persistence, no-handoff focus isolation, explicit handoff,
generic adapter interaction, all three signed-in provider probes, process
cleanup, and the absence of forbidden browser flags. Provider qualification is
probe-only:

```bash
npm run qualify:playwright-email -- --profile remote
```

Never use qualification to send a real message. Email send retains exact-content
approval, one-attempt execution, and terminal unknown-outcome handling.

## Security Invariants

- Treat the Bridge as browser-wide privileged code even though SparkClaw limits
  every client to task-owned tabs.
- Keep the browser profile, native host, runtime directories, socket, and Vault
  credential owner-only.
- Keep provider origins and Controller operations allowlisted.
- Reject arbitrary code, selectors, commands, storage export, file URLs, and
  network interception.
- Redact page evidence and diagnostics before they reach traces or model input.
- Do not introduce container Chromium, profile copying, permanent CDP, or a
  compatibility backend.

See [Playwright Extension browser design](playwright-extension-browser-design.md)
for the migration decisions and [Browser email Workflow](browser-email-workflow-design.md)
for provider and approval semantics.
