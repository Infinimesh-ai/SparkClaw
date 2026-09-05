# Host-CDP Browser Design

> Language: English | [简体中文](../zh-cn/docs/host-cdp-browser-design.md)

## Status

**Superseded.** Host-CDP was implemented on 2026-09-02 and retired by the
Playwright Browser Bridge cutover on 2026-09-05; see
[Browser runtime](browser-runtime.md) for the current design. This document
is kept as the historical record of that transport, host process lifecycle,
tab ownership model, and authentication handoff.

Host-CDP was, at the time, the only supported browser runtime. The
two official deployment entrypoints,
`scripts/deploy_local.sh` and `scripts/deploy_remote.sh`, configure the sole
Host-CDP transport and install or verify Chromium on the host. The Gateway image
no longer contains Chromium. The container-managed implementation,
configuration, packaging, Compose wiring, startup logic, profile ownership
code, and dedicated tests are deleted; no compatibility image or overlay
remains.

## Context

Before this cutover, SparkClaw launched pinned `agent-browser` and Chromium
inside the Gateway container. The persistent profile was stored on the host and
mounted into the container. Hidden and visible Chromium processes took
exclusive, ordered ownership of that profile and could not run concurrently.

That design is suitable for isolated automation and remote deployment, but it
does not provide one ordinary desktop browser that the owner can use while
SparkClaw opens separate tabs with the same authenticated profile. The desired
desktop behavior is:

- the owner signs in through a normal SparkClaw-specific Chromium window;
- one host Chromium process owns one dedicated profile;
- `agent-browser` attaches to that process through CDP;
- owner tabs and SparkClaw tabs share authentication but have explicit
  ownership boundaries;
- existing governed browser operations continue through `agent-browser` and
  the provider-neutral browser adapter.

## Goals

1. Reuse login state without copying cookies, passwords, or storage records.
2. Keep exactly one Chromium process per SparkClaw profile.
3. Preserve `agent-browser` as the only browser automation backend.
4. Provide a normal host desktop browser with address bar, tabs, downloads,
   bookmarks, and ordinary owner navigation when a desktop display is present,
   while retaining host-owned headless operation on displayless servers.
5. Prevent automatic takeover of unrelated owner tabs.
6. Keep current Workflow, ToolHub, Policy, approval, evidence, and typed
   failure boundaries.
7. Support both direct-host development and the Compose Gateway without
   exposing an unauthenticated CDP endpoint to the LAN.
8. Retire the container-managed browser completely after Host-CDP passes the
   qualification gates.

## Non-Goals

- Attaching to the owner's normal Chrome or Chromium default profile.
- Importing or exporting cookies, passwords, passkeys, or browser storage.
- Running two Chromium processes against the same profile.
- Treating a period without mouse or keyboard input as permission to take over
  an owner tab.
- Adding Playwright, a second DOM collector, or model-authored browser code.
- Defining application-specific providers, scripts, settings, tools, or
  Workflows on top of the browser transport.

## Decision

Browser automation migrates to one supported runtime:

| Runtime | Chromium owner | Profile owner | agent-browser transport |
|---|---|---|---|
| `host-cdp` | Host `sparkclaw-browserd` | Host browser daemon | CDP attachment to existing Chromium |

During qualification, the existing implementation may remain available behind
an explicit migration-only selector so the current release remains operable.
That selector is not part of the final product configuration. Host-CDP fails
closed when browserd or its endpoint is unavailable and must never start a
replacement Chromium in Gateway.

Passing the rollout gates triggers removal, not indefinite coexistence. The
official DGX Spark and Ubuntu VM deployments then use Host-CDP only, and the
repository no longer ships or tests a container-managed runtime.

## Target Topology

```text
Host desktop
  -> SparkClaw Browser launcher
  -> sparkclaw-browserd
       -> one normal Chromium process
            -> dedicated SparkClaw profile
            -> owner tabs
            -> SparkClaw-owned tabs
       -> private Chromium CDP endpoint
       -> capability-gated CDP proxy

Gateway container
  -> browserautomation adapter
  -> private agent-browser MCP process
       AGENT_BROWSER_CDP=<browserd capability WebSocket URL>
  -> browserd CDP proxy
  -> existing host Chromium process
```

The Chromium process and profile outlive individual Gateway and
`agent-browser` MCP processes. Restarting or stopping Gateway disconnects
automation but does not close the owner's browser.

## Host Browser Daemon

`sparkclaw-browserd` is a user-scoped host service and the sole process owner
for the dedicated browser profile. `SparkClaw Browser` means a qualified host
Chromium binary plus the SparkClaw launcher, daemon, and dedicated profile. It
is not a downstream Chromium fork. The daemon must:

1. Resolve and validate the supported Chromium executable.
2. launch Chromium with a dedicated non-default user data directory and an
   ephemeral loopback remote-debugging port;
3. retain one process identity and reject a second owner for the same profile;
4. discover the browser-level CDP WebSocket endpoint from Chromium runtime
   state rather than assuming a fixed port;
5. expose bounded `status`, `open-or-focus`, and `shutdown` operations through
   a local control socket;
6. provide a capability-gated CDP proxy for Gateway without publishing the raw
   Chromium debugging port;
7. choose a fresh opaque browser-generation epoch whenever browserd starts,
   increment it and rotate the proxy capability whenever the browser process
   is replaced;
8. keep browser logs, profile paths, and CDP URLs free of credential-bearing
   query values.

The daemon runs as the desktop owner, not as root. A desktop launcher named
`SparkClaw Browser` calls `open-or-focus`; repeated launches focus the existing
window instead of starting another process. Kiosk and app mode are not the
default because the browser remains a normal owner-facing work browser.

Browserd presentation is `auto`: it starts ordinary headed Chromium when it
can validate the owner's desktop display, and host-owned `--headless=new`
Chromium otherwise. Moving between headless and headed presentation is an
ordered browserd restart against the same profile, never a second concurrent
process. A displayless server retains hidden browser automation but cannot
perform a visible owner handoff until a usable desktop is available.

## CDP Transport Boundary

Chromium's raw debugging listener binds only to host loopback. Browserd connects
to it locally and exposes a separate WebSocket proxy on the minimum interface
needed by Gateway:

- direct-host Gateway: loopback only;
- Compose Gateway: the dedicated SparkClaw Docker bridge address only.

The proxy URL contains a high-entropy, short-lived capability path. The current
capability is written to a mode-`0600` runtime file that is bind-mounted read
only into Gateway. The endpoint and capability are redacted from API responses,
logs, traces, artifacts, and health details. Other containers are not attached
to the browser control network, and host firewall rules reject LAN access.

CDP grants browser-wide authority; the proxy is an authentication and network
containment boundary, not a fine-grained authorization layer. Product-level
authorization remains in SparkClaw's tab ownership, Workflow, ToolHub, Policy,
and approval layers.

## agent-browser Integration

The pinned `agent-browser` remains the sole execution and perception backend.
In `host-cdp` mode Gateway starts its private MCP subprocess with:

```text
AGENT_BROWSER_CDP=<browserd browser-level WebSocket URL>
AGENT_BROWSER_NAMESPACE=<SparkClaw namespace>
AGENT_BROWSER_SESSION=<single host-CDP session>
```

It must not set `AGENT_BROWSER_PROFILE`, `AGENT_BROWSER_EXECUTABLE_PATH`,
`AGENT_BROWSER_HEADED`, or Chromium launch arguments in this mode. Browserd,
not the adapter, owns those decisions.

Normal adapter shutdown calls `agent_browser_close` for the unique SparkClaw
session before stopping the MCP subprocess. In Host-CDP mode that command closes
the session daemon and its socket but only detaches from externally owned
Chromium; browserd and Chromium remain alive. If the MCP transport is already
unhealthy, shutdown aborts it directly and relies on the bounded daemon idle
timeout. `browser.close` may close only a tab recorded as SparkClaw-owned;
closing the last such tab must not close Chromium or an owner tab.

The adapter no longer acquires the existing profile lease or runs Chromium
singleton cleanup. Browserd exposes profile/process health instead. The old
lease, singleton cleanup, and local Chromium-launch paths are deleted at
cutover.

## Tab Ownership

An open Chromium process is not an authorization boundary. Every page target
must be classified before automation:

| Class | Meaning | Automation rule |
|---|---|---|
| `owner` | Existing or owner-created ordinary tab | Never select or mutate automatically |
| `sparkclaw` | Created by SparkClaw for a frozen task | May be operated under its active lease |
| `handoff` | SparkClaw tab temporarily controlled by the owner | Automation paused until explicit completion |
| `released` | Former SparkClaw tab returned to ordinary owner use | Treated as `owner` unless explicitly handed back |

The registry stores the stable CDP target ID, profile ID, creator, class,
Workflow/run owner, generation, lease deadline, last validated URL, and content
identity. Numeric tab position is presentation data only and must not be used as
a durable identifier. A missing or replaced target fails explicitly; it never
falls back to the active or first tab.

The system does not infer owner consent from inactivity. Initial implementation
does not take over arbitrary owner tabs. A later explicit handoff may be added,
but it must name one current target and create a bounded lease. Owner
interaction detection is defense in depth, not the source of authorization.

For ordinary automation, SparkClaw creates a separate tab in the shared profile.
This preserves cookies and origin storage while avoiding interference with the
owner's current page. A provider script may request same-tab continuation only
when it can prove that required authentication state is tab-local and the tab
is already SparkClaw-owned.

## Authentication Handoff

Host-CDP keeps the existing principle that authentication is an explicit owner
action and browser state remains inside Chromium:

1. SparkClaw creates a task-owned tab for the frozen destination and brings it
   to the foreground.
2. The tab enters `handoff`; automation on that target pauses.
3. The owner completes sign-in, captcha, 2FA, consent, or other required human
   interaction in SparkClaw Browser.
4. The owner explicitly confirms completion through the existing handoff
   surface.
5. Runtime reselects the same stable target ID, settles it, captures fresh
   evidence, and validates both authentication and the frozen destination.
6. Successful validation returns the tab to `sparkclaw` ownership for the
   waiting Workflow. Cancellation or explicit release returns it to `owner`.
7. Cookies, passwords, passkeys, local storage, and CDP credentials remain in
   Chromium or browserd runtime state and are never copied into SparkClaw Store.

Pre-handoff refs and page generations are invalid after owner interaction.
Resume always uses a fresh snapshot. An unrelated, missing, or unauthenticated
page remains paused and cannot be substituted with another open tab.

## Configuration Shape

The intended configuration shape is:

```json
{
  "adapters": {
    "browserAutomation": {
      "hostCDP": {
        "endpointFile": "/run/sparkclaw/browserd/cdp-endpoint",
        "profileID": "default",
        "connectTimeoutMs": 10000
      }
    }
  }
}
```

There is no permanent browser mode enum. `hostCDP` is the sole browser transport
configuration. Static CDP WebSocket URLs are not accepted because they are
credential-like, rotation-sensitive runtime values. Configuration validation
rejects missing endpoint files, world-readable endpoint files, attempts to use
a normal browser default profile, and legacy fields such as
`mode=container-managed`, executable paths, profile launch settings, headed
flags, or Chromium arguments. Deployment migration removes those fields;
remaining stale settings fail startup with a clear Host-CDP migration error.

A Host-CDP connection failure is reported as unavailable and never triggers a
container Chromium fallback.

## Deployment And Packaging Migration

Qualification may keep the old release operable while Host-CDP is incomplete,
but the production cutover changes the two official deployment entrypoints and
their shared startup path atomically. Removing Chromium from the Gateway image
before host browserd and CDP startup are ready would disable every current
browser Workflow and is therefore forbidden. Once all qualification gates pass,
retaining the old runtime is also forbidden; final acceptance includes proving
that its removal is complete.

### Common Host Installer

Both deployment scripts call one shared, idempotent host installer rather than
duplicating package logic. The intended helper is
`scripts/install-host-browser.sh`. It must:

1. resolve the host architecture and the approved pinned SparkClaw Chromium
   artifact from a version-controlled manifest;
2. download it on the host, verify its checksum, and install it under a
   versioned SparkClaw-owned path such as
   `/opt/sparkclaw/chromium-<version>/chrome`;
3. never use Ubuntu's `/usr/bin/chromium-browser` Snap launcher or the owner's
   ordinary system browser as the product runtime;
4. validate executable type, architecture, version, sandbox startup, UTF-8,
   CJK/emoji fonts, and a fresh dedicated-profile launch;
5. install `sparkclaw-browserd`, its user service, desktop launcher, runtime
   directory, and profile directory with owner-only permissions;
6. record the qualified executable path and version for browserd health without
   writing them into the Gateway container configuration.

The installer uses the pinned artifact qualified for each supported
architecture. It reuses an already verified matching installation and never
downloads an arbitrary latest binary. Version changes require the same browser
qualification tests as an agent-browser version change.

### Official Deployment Entrypoints

| Script | Required host-CDP behavior |
|---|---|
| `scripts/deploy_local.sh` | Download or verify the pinned SparkClaw Chromium on the DGX Spark host, install and start browserd for the deploying user, write the Host-CDP endpoint-file configuration, and wait for a protected endpoint before starting product containers |
| `scripts/deploy_remote.sh` | Download or verify the pinned SparkClaw Chromium on the supported Ubuntu host, install and start browserd for the owner, preserve headless host operation when no desktop exists, write the Host-CDP endpoint-file configuration, and wait for a protected endpoint before starting product containers |

Their `--check` paths remain read-only. They verify the host executable,
browserd installation, profile permissions, service definition, endpoint-file
configuration, Docker bridge/proxy reachability prerequisites, and absence of
legacy container-browser settings without installing packages or starting
processes.

The start/reconcile scripts called by those entrypoints must also move their
browser smoke test:

- `scripts/start_local_compose.sh` and `scripts/start_remote_compose.sh` verify
  browserd before Gateway startup and run the existing agent-browser smoke
  through `AGENT_BROWSER_CDP` after Gateway is ready;
- neither start path executes Chromium inside Gateway or applies a Gateway X11
  overlay; both verify host browserd in headed or headless presentation and
  run the same CDP smoke from the Gateway container;
- deployment success requires both browserd health and an agent-browser
  open/snapshot round trip against the host Chromium process.

### Gateway Image And Compose

`docker/images/gateway.Dockerfile` removes the `chromium` and `xvfb`
packages, the container Chromium executable environment variable, and the
container-owned browser profile directory. It keeps fonts or media tools only
when another Gateway capability still requires them.

Product Compose no longer mounts the browser profile or X11/Xauthority into
Gateway. It provides:

- a read-only browserd endpoint capability file;
- access only to the dedicated browser control bridge/proxy;
- endpoint-file configuration for the sole Host-CDP transport;
- no raw Chromium remote-debugging port.

The cutover deletes every Chromium-bearing Gateway image target and browser
compatibility overlay. It also removes container Chromium launch environment
variables, the browser profile bind mount, profile lease and singleton cleanup
wiring, the Gateway X11/Xauthority overlay, and any deployment branch that can
select the old runtime.

### Deployment Verification

Deployment tests must prove that:

- Chromium is installed and launched on the host, under the deploying owner;
- the profile path belongs to SparkClaw and is not a normal browser default;
- Gateway contains `agent-browser` but no Chromium executable or Xvfb package;
- Gateway shutdown leaves browserd and Chromium running;
- browserd shutdown makes Gateway browser health unavailable without launching
  a replacement container browser;
- direct-host, desktop Compose, and headless VM deployments all execute their
  intended browserd presentation path;
- a container-side agent-browser MCP process can reach only the capability-
  gated host CDP proxy and complete open/snapshot/tab smoke tests;
- repository and expanded Compose checks find no container Chromium launcher,
  Chromium-bearing Gateway target, browser profile mount, Gateway X11 overlay,
  legacy mode fallback, or container-browser-only test.

## Failure Semantics

The transport exposes stable typed failures, including:

- `browser_host_unavailable`;
- `browser_cdp_unauthorized`;
- `browser_cdp_version_unsupported`;
- `browser_profile_busy`;
- `browser_target_missing`;
- `browser_tab_not_owned`;
- `browser_handoff_required`;
- `browser_session_replaced`;
- `browser_connection_not_authenticated`.

Gateway restart, browserd restart, Chromium replacement, and target loss each
invalidate prior session and page generations. Workflow resume must reacquire a
SparkClaw-owned target and fresh evidence; it cannot reuse pre-restart refs.

## Rollout Plan

### Phase 0: Qualification

- Verify pinned `agent-browser` MCP operation through `AGENT_BROWSER_CDP`.
- Prove snapshot, read, open, tab creation/switch/close, type, select, and
  screenshot behavior against one existing Chromium process.
- Prove stopping MCP does not close Chromium after the adapter shutdown change.
- Measure stable target identity across navigation, SPA route changes, login,
  Gateway restart, and browserd reconnect.
- Validate supported host Chromium versions and Compose bridge connectivity.

### Phase 1: Browser Foundation

- Add Host-CDP configuration and fail-closed validation. A temporary explicit
  qualification selector may exist only until cutover and is not retained in
  the final schema.
- Implement browserd, desktop launcher, systemd user service, capability proxy,
  endpoint rotation, and health reporting.
- Add Host-CDP adapter lifecycle while leaving the released container runtime
  untouched during qualification.
- Add stable target-ID tab ownership and no-owner-tab-touch tests.
- Add the shared host-browser installer and Host-CDP deployment smoke path
  without selecting it as the official runtime yet.

### Phase 2: Tab Ownership And Handoff

- Persist stable target identity and ownership only for active browser
  handoffs that require restart recovery.
- Implement task-tab creation, explicit owner handoff, completion validation,
  cancellation, release, and lease expiry.
- Prove that owner tabs cannot be selected through active-tab or numeric-index
  fallback.
- Validate authentication continuity across Gateway restart, Chromium restart,
  and browserd reconnect without credential export.

### Phase 3: Cutover And Removal

- Add host service installation, desktop launcher, diagnostics, and operator
  documentation.
- Qualify existing browser Workflow Profiles in both direct-host and Compose
  host-CDP environments.
- Keep host-CDP opt-in until the existing browser validation matrix is green and
  operational recovery has been exercised.
- Then atomically switch both official deployment scripts and all startup paths
  to Host-CDP, remove Chromium and Xvfb from Gateway, and remove the old profile
  and X11 mounts.
- Delete the container-managed adapter lifecycle, launch logic, profile lease,
  singleton cleanup, legacy configuration fields, fallback branches,
  compatibility image/Compose artifacts, and container-browser-only tests.
- Finish with Host-CDP as the only supported runtime; stale legacy
  configuration must fail with the documented migration error.

## Acceptance Criteria

- The owner can keep SparkClaw Browser open while SparkClaw operates a separate
  tab in the same Chromium process.
- Login survives Gateway restart and Chromium restart through the dedicated
  persistent profile.
- No second Chromium process can acquire the profile.
- Gateway shutdown leaves the host browser and owner tabs open.
- Browserd or CDP loss produces a typed failure with no fallback launch.
- Existing owner tabs are never selected, mutated, or closed without explicit
  handoff.
- Tab identity remains bound to stable target IDs across ordinary navigation.
- CDP endpoints and capabilities never appear in public state, logs, traces, or
  artifacts.
- The source tree, configuration schema, Gateway image, Compose expansion, and
  deployment scripts contain no container-managed browser runtime or fallback.
- Legacy container-browser tests are removed or rewritten as Host-CDP tests;
  only explicit stale-configuration rejection coverage remains.
- `scripts/deploy_local.sh` and `scripts/deploy_remote.sh` install or verify host
  Chromium and configure only the Host-CDP endpoint after cutover.
- The Gateway image contains agent-browser but no Chromium or Xvfb, and
  deployment smoke proves agent-browser is controlling the host process.
- Headless VMs run one host-owned headless Chromium process; desktop hosts run
  one host-owned headed Chromium process using the same dedicated profile rule.
- Generic owner login handoff resumes only the same frozen task-owned target
  with fresh post-handoff evidence.
- Existing browser Workflow Profiles preserve their Policy, approval, evidence,
  and typed failure behavior in host-CDP mode.

## Rejected Alternatives

### Attach To The Owner's Daily Browser Profile

Rejected because it exposes unrelated authenticated sites, tabs, history,
extensions, and credentials to SparkClaw. Modern Chromium also restricts remote
debugging of the default data directory. SparkClaw uses a dedicated work
profile instead.

### Two Chromium Processes Sharing One Profile

Rejected because Chromium profile singleton locking forbids concurrent owners
and concurrent writes can corrupt state. One browserd-owned process is the
invariant.

### Export Or Synchronize Cookies

Rejected because login state can include device-bound tokens, passkeys,
certificates, extension state, and provider-specific storage. Copying a subset
is unreliable and widens the credential boundary.

### Replace agent-browser With Direct CDP Code

Rejected because it creates a second execution and perception backend. The new
mode changes process transport and ownership only; agent-browser remains the
provider implementation behind `browserautomation.Adapter`.

### Infer Tab Handoff From Inactivity

Rejected because inactivity does not prove consent or abandonment. Explicit
target-bound handoff is required.

### Permanent Dual-Runtime Support

Rejected because maintaining container-managed and Host-CDP implementations
would duplicate process ownership, configuration, packaging, deployment, and
test contracts. The old runtime exists only as a temporary migration aid and
is deleted after Host-CDP qualification.

## Relationship To Existing Documents

- [Browser runtime](browser-runtime.md) remains authoritative for shipped
  browser behavior until this proposal is implemented.
- [Architecture](architecture.md) remains authoritative for cross-component
  ownership and the supported product surface.
- [Workflow capabilities](workflow-capabilities.md) remains authoritative for
  the executable browser behavior exposed to users.
- [Deployment](deployment.md) will own installation and operational commands
  after the host service is implemented.
