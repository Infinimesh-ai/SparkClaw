# Playwright Extension Migration Handoff

> Language: English | [简体中文](../zh-cn/docs/playwright-extension-migration-handoff.md)

## Snapshot

This handoff records the accepted decisions and verified implementation state
as of 2026-09-04. It exists so a new development session does not need to infer
the current plan from earlier browser, Host-CDP, or email discussions.

Authority order:

1. [Playwright Extension browser migration design](playwright-extension-browser-design.md)
   defines the target architecture and cutover gates.
2. Current source code defines what is implemented.
3. [Browser runtime](browser-runtime.md) describes the still-active Host-CDP
   production implementation.
4. Earlier chat proposals and the Host-CDP email design are historical context,
   not authority for the Playwright target.

## Accepted Product Decisions

- The browser artifact is the fixed host-installed **SparkClaw Chromium**. Do
  not substitute Chrome or Edge, download Chromium in Gateway, or expose a
  browser choice in product settings.
- Browser channel identity must be `chromium`. It is an internal implementation
  fact, not an owner-selectable setting. The executable path, channel identity,
  and profile identity must agree.
- The final product uses one fixed persistent SparkClaw browser profile. The
  owner may use that browser normally; SparkClaw attaches only for a bounded
  task, creates task-owned tabs, and detaches afterward.
- Background work must not activate the browser window, steal focus, replace the
  current WebChat tab, or use an owner-opened tab. Only an explicit owner
  handoff may bring a requested task tab to the foreground.
- Generic model-guided browser work uses Playwright MCP behind the existing
  provider-neutral browser interface. Deterministic provider login probes and
  effects use fixed repository-owned Playwright CLI scripts. The model selects
  a function and supplies semantic values; it never authors browser actions,
  selectors, JavaScript, or CLI commands.
- Browser authentication stays in the browser profile. SparkClaw does not copy
  cookies, storage state, passwords, passkeys, browser databases, or profiles.
- The Playwright Extension credential is configured once under Browser control,
  stored encrypted in the Gateway credential Vault, and never duplicated by an
  email provider. Provider configuration stores only provider/account state.
- The official Playwright Extension is a qualification baseline only. Because
  the pinned upstream extension can attach to all known pages and can cause
  foreground focus, it must remain on the disposable qualification profile.
  Production requires the independently packaged SparkClaw Browser Bridge with
  attachment-time task-tab allowlisting and background-without-focus behavior.
- Host-CDP remains the production path until every migration gate passes. The
  final cutover is atomic: Playwright Extension becomes the sole transport and
  browserd, Host-CDP, `agent-browser`, their configuration, tests, packaging,
  and fallback text are deleted. There is no permanent compatibility fallback.

## Implemented And Verified

The production execution path has **not** migrated. It is still:

```text
Browser and email Workflow
  -> agent-browser 0.32.3
  -> protected Host-CDP endpoint
  -> sparkclaw-browserd
  -> host SparkClaw Chromium
```

The Playwright preview control plane is implemented:

- owner-scoped `sparkclaw-browser-controller.service`;
- owner-only Unix socket and bounded controller protocol;
- encrypted `playwright-extension-token-v1` Vault credential;
- authenticated, redacted Gateway status/save/check/remove APIs;
- `Settings > Connections > Browser control` WebChat surface;
- disposable `extension-qualification` Chromium profile;
- bounded task-page creation, close, detach, subprocess termination, and reap;
- pinned official Extension `0.4.0`, Playwright MCP `0.0.80`, and Playwright
  `1.63.0-alpha-2026-08-31` with Chromium `148.0.7778.0`.

A real saved credential completed a fresh handshake through the installed
systemd user service. The Gateway and controller expose only generation,
version, timestamp, and typed status metadata; the raw credential is not stored
in Compose or repository files and must not be copied from chat into a new
development session.

The qualification browser profile is:

```text
~/.local/share/sparkclaw/browser/extension-qualification/user-data
```

The controller must invoke Playwright MCP with `--browser chromium`. Declaring
the pinned Chromium artifact as `chrome` selects the wrong Linux launch path;
the short-lived connection page then fails under Ubuntu AppArmor with `No
usable sandbox`. Defaults, the generated systemd unit, installation checks, and
tests now guard the `chromium` identity.

Both `sparkclaw-browserd.service` and
`sparkclaw-browser-controller.service` currently run by design. Browserd owns
production Host-CDP execution; the controller owns only the preview. Neither
path falls back to the other.

## Remaining Migration Work

Phase status:

| Phase | State on 2026-09-04 |
|---|---|
| 0. Compatibility PoC | MCP handshake is proven; separate CLI attachment, real Gmail manual-login retention, token rotation, and one-client-per-tab behavior still need live qualification |
| 1. Host Controller | Preview control plane is substantially complete and live-validated |
| 2. Generic MCP Adapter | Not implemented; production browser tools still instantiate the Host-CDP `agent-browser` adapter |
| 3. Deterministic provider scripts | Not migrated; QQ Mail, Outlook, and Gmail scripts still require `AGENT_BROWSER_CDP` and execute `agent-browser` |
| 4. SparkClaw Browser Bridge | Not started; the official extension remains qualification-only |
| 5. Deployment qualification | Not started for the Playwright production path; Local and Remote startup still require Host-CDP |
| 6. Atomic cutover and deletion | Not started; browserd, Host-CDP, and `agent-browser` remain required production components |

The next implementation task is Phase 2, not email work and not deletion of the
legacy runtime:

1. Implement a provider-neutral Playwright Extension adapter behind the current
   `browserautomation.Adapter` boundary.
2. Keep Host-CDP as the explicit default while qualification is in progress;
   do not add automatic fallback.
3. Map the existing browser tool surface without changing public ToolHub,
   Workflow, Policy, Approval, evidence, snapshot, reference, settle, or typed
   failure contracts.
4. Preserve task-tab ownership before every list, read, snapshot, focus, click,
   close, and cleanup operation.
5. Run the existing adapter characterization suite against both implementations
   and add controller-backed fake and live scenarios.
6. Only after the generic adapter passes should Phase 3 replace the three email
   providers' Host-CDP scripts with fixed Playwright CLI scripts.
7. Complete the Browser Bridge and deployment qualification before any
   production selector change or deletion.

## Guardrails For A New Development Session

- Start by reading this handoff, the target design, the current browser runtime,
  and the actual configuration and assembly code.
- The working tree contains a large uncommitted browser, email, and deployment
  integration. Do not reset, discard, or overwrite unrelated changes.
- Do not interpret a `Ready` preview credential as a production cutover.
- Do not use the official extension with an everyday or production-account
  profile; it is not a per-tab privacy boundary.
- Do not add container Chromium, Xvfb, profile copying, cookie export, permanent
  CDP, arbitrary Playwright code, or model-authored selectors.
- Do not expose browser channel, executable path, profile path, extension token,
  controller socket, or transport internals as model-controlled arguments.
- Do not remove Host-CDP or `agent-browser` before Phase 6 gates pass.
- Never place the extension credential in a shell argument, environment file,
  repository file, log, trace, artifact, model context, or test fixture.

## Verification Baseline

The current preview baseline has passed:

- browser-controller Node tests;
- Host-CDP/controller/deployment Python tests;
- Gateway browser-control and browser-automation tests plus focused race tests;
- all three email provider script suites;
- WebChat tests and production build;
- Local and Remote Compose expansion, Remote Doctor, shell syntax, bilingual
  Markdown mirror/link checks, `git diff --check`, and a changed-file secret
  scan;
- live systemd extension handshake, Gateway restart persistence, controller and
  Chromium liveness, and subprocess-orphan checks.

On this workstation, `go test ./...` has one environment-only failure in
`internal/contracttest` because the required sibling InfiniCenter repository and
`SparkClaw--JingSi` central conformance manifest are absent. All other Gateway
packages pass. A new session must distinguish that missing coordination
repository from a Playwright regression and must process InfiniCenter inbox,
decisions, contracts, and status when the repository becomes available.
