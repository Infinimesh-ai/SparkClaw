# Playwright Extension Migration Handoff

> Language: English | [简体中文](../zh-cn/docs/playwright-extension-migration-handoff.md)

## Snapshot

The Playwright Extension migration is complete as of 2026-09-05. The
SparkClaw Browser Bridge is the sole production browser transport. Browserd,
Host-CDP, `agent-browser`, the migration selector, their deployment wiring,
and their executable tests have been removed.

Authority order:

1. [Playwright Extension browser design](playwright-extension-browser-design.md)
   records the accepted architecture and migration gates.
2. [Browser runtime](browser-runtime.md) records the current production
   implementation.
3. Current source code defines exact protocol and behavior.
4. [Host-CDP browser design](host-cdp-browser-design.md) is historical only.

## Final Architecture

- SparkClaw installs one fixed Chromium artifact on the desktop host.
- `sparkclaw-browser.service` runs that browser without remote-debugging,
  automation, or headless startup flags.
- The owner uses one persistent profile at
  `~/.local/share/sparkclaw/browser/default/user-data`.
- The checksum-pinned SparkClaw Browser Bridge connects only task-owned tabs.
- `sparkclaw-browser-controller.service` owns bounded Playwright MCP and CLI
  processes behind owner-only Unix sockets.
- Generic browser work uses the provider-neutral Gateway adapter and
  Playwright MCP.
- QQ Mail, Outlook, and Gmail probes and sends use fixed repository-owned
  Playwright CLI handlers.
- The encrypted Gateway Vault is the only SparkClaw store for the Bridge
  credential. Browser authentication remains in the browser profile.
- Background attachment and actions do not focus the browser or select an
  owner tab. Only the explicit `tabs.handoff` contract may focus the task tab.

The production browser and controller versions qualified in this cutover are:

| Component | Version |
|---|---|
| SparkClaw Chromium | `148.0.7778.0` |
| SparkClaw Browser Bridge | `1.0.18` |
| Playwright MCP | `0.0.80` |
| Playwright CLI | `0.1.19` |
| Playwright Library/Core | `1.63.0-alpha-2026-08-31` |

Bridge releases use a versioned Service Worker entry path. This prevents an
unpacked extension upgrade from retaining an older worker script cache while
the installed extension directory and native host have advanced.

## Phase Status

| Phase | Final state |
|---|---|
| 0. Compatibility PoC | Complete: MCP and CLI attachment, login retention, credential lifecycle, and cleanup contracts passed |
| 1. Host Controller | Complete: private protocol, generations, health, deadlines, supervision, and cleanup are production-installed |
| 2. Generic MCP Adapter | Complete: provider-neutral behavior and real form interaction passed through the production Bridge |
| 3. Deterministic provider scripts | Complete: six fixed probe/send handlers and the three-account read-only qualification passed |
| 4. SparkClaw Browser Bridge | Complete: independently packaged, attributed, checksum-pinned, background-safe, and explicit-handoff qualified |
| 5. Deployment qualification | Complete: shared Local/Remote setup, checks, Compose contracts, restart, pairing, detach, and profile persistence passed |
| 6. Atomic cutover and deletion | Complete: Bridge-only configuration is active and all executable legacy paths are removed |

There is no remaining migration selector or fallback. A stale
`SPARKCLAW_BROWSER_AUTOMATION_*` or `SPARKCLAW_BROWSER_CDP_*` value is
rejected with a migration error.

## Host Cutover

The signed-in qualification profile became the production default profile by
an in-place directory rename. No account data, cookies, browser database,
credential, or storage state was read, copied, or exported.

The former default Host-CDP profile is retained only as an owner-only archive:

```text
~/.local/share/sparkclaw/browser/default-host-cdp-retired-20260905
```

Both profile roots are mode `0700`. The controller and native Bridge sockets
are mode `0600`. The installed browser command line names only the fixed
browser, persistent profile, and installed Bridge, plus ordinary display
flags.

The obsolete installed `sparkclaw-browserd.service`, browserd configuration,
browserd runtime directory, and SparkClaw `agent-browser` namespaces were
removed after source and deployment dependency scans found no active consumer.
The archived profile is not an executable fallback.

## Live Qualification

The final `1.0.18` Bridge completed all live checks through the saved
PostgreSQL credential record, encrypted Vault, private controller socket, and
the persistent default profile:

- `npm run setup:browser` completed installation, service restart, exact
  loaded-version readiness, and profile reuse.
- Both Local and Remote private deployment files pass Browser setup
  verification.
- A generic no-handoff scenario opened, read, filled, selected, clicked,
  settled, captured a screenshot, closed, and detached in about 23 seconds.
  X11 monitoring proved Codex retained focus and no task page became the
  visible active browser tab.
- The same scenario with explicit handoff completed in about 30 seconds. X11
  monitoring observed the requested task page become the active focused
  browser window only after the exact handoff marker.
- The fixed read-only email qualification passed QQ Mail, Outlook, and Gmail
  sequentially against the three signed-in accounts in about 112 seconds.
  Focus monitoring found no browser activation, and no send handler ran.
- Browser and controller remained alive after each detach. MCP/CLI runtime
  directories were empty and no Playwright subprocess remained.
- Restarting both user services retained browser authentication and the saved
  Bridge pairing while invalidating task/session generations.

The Bridge credential used by production was generated by the Bridge profile
and validated before persistence. Save, failed replacement, successful
replacement, deletion, stale-generation rejection, non-prefill, and
clear-after-save behavior are covered by Gateway and WebChat tests. Raw
credential material is never returned by status APIs or written to Compose,
repository files, command arguments, logs, traces, or artifacts.

## Verification Baseline

The completed cutover passed:

- Browser Controller Node tests: 45;
- Browser Bridge Node tests: 25;
- deterministic provider runtime tests: 3;
- browser/deployment Python tests: 48, plus the complete Compose Python gate:
  101;
- WebChat: 519 translation keys, 28 test files, 88 tests, and production build;
- Gateway build and vet;
- all Gateway packages, including the external central-contract package;
- the concurrency-heavy Gateway race suite;
- ASR fake-runtime tests: 11;
- Local and Remote Compose expansion and shell syntax;
- Remote Doctor plus Local and Remote deployment preflights;
- Browser setup checks for both Local and Remote private environments;
- live generic background, explicit handoff, and three-account email probes;
- residual process, session-directory, legacy-runtime, and forbidden-flag
  checks.

The Local preflight validated Linux/ARM64, the NVIDIA GB10, 121 GiB of memory,
model-cache capacity, Docker, permissions, Compose, and Browser Bridge without
changing containers. The Remote preflight validated its five public model
endpoints, five application services, Compose, and Browser Bridge.

After restoring the expected sibling InfiniCenter checkout, `go test ./...`
passes including `internal/contracttest`. The ProjectGroup-2 inbox and proposed
decisions contain no pending SparkClaw work. This migration changes no
cross-project contract, and the SparkClaw status broadcast records the completed
cutover and that no other member project must follow up.

## Maintenance Rules

- Do not add Host-CDP, browserd, `agent-browser`, container Chromium, profile
  copying, storage export, or an automatic fallback.
- Do not weaken task-tab ownership or permit arbitrary selectors, JavaScript,
  Playwright code, CLI commands, extension endpoints, or executable paths.
- Bump the Bridge package version and versioned Service Worker entry together,
  then regenerate the checksum closure for every Bridge release.
- Keep the loaded Bridge version gate before Controller health checks.
- Run email probes only for qualification. A real send still requires the
  existing exact-content confirmation and preserves one-attempt and
  unknown-outcome semantics.
- Never read, print, export, or duplicate browser authentication or the Bridge
  credential.
