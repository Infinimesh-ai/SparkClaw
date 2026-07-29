# Browser Runtime

> Language: English | [简体中文](../zh-cn/docs/browser-runtime.md)

This document is the current browser implementation and operating guide. It
replaces the Playwright migration, agent-browser migration, browser-mode,
profile, login, perception, interaction, and weather migration records.

## Current Architecture

SparkClaw uses pinned `agent-browser` with a resolved system Chromium as its
only browser execution backend. ToolHub and Workflow contracts remain
provider-neutral; `internal/browserautomation` owns process transport, protocol
validation, profile locking, deadlines, and conversion to typed observations.

```text
Workflow leaf
  -> stage-scoped browser ToolHub capability
  -> browserautomation adapter
  -> private agent-browser MCP process
  -> SparkClaw-owned Chromium profile
```

There is no Playwright fallback, Chrome DevTools MCP fallback, personal Chrome
attachment, cookie export, or second DOM perception engine.

## User-Visible Capabilities

| Capability | Current revision boundary |
|---|---|
| `browser.internet_search` r1 | Search public current information through `web.search`; it does not open source pages |
| `browser.weather` r1 | Query typed metric data through Infinimesh Info `POST /v1/info/weather` and render one card for one explicit location |
| `browser.automation` r1 | Open one explicit HTTP(S) URL or registered destination, or focus a matching existing tab |
| `browser.interaction` r1 | Inspect one target and perform at most three bounded, ref-bound, post-click-verified clicks |

`browser.interaction` r1 does not type, select, upload, download, submit forms,
enter credentials, solve captcha/2FA, perform payment/purchase, run page scripts,
or complete login. Low-level browser tools do not expand the supported Workflow
surface; [Workflow capabilities](workflow-capabilities.md) is authoritative.

## Read And Interaction Evidence

Browser evidence uses separate contracts:

- `browser.read` extracts bounded rendered text and typed page metadata from the
  active page. It never evaluates page scripts.
- `browser.snapshot` creates a structured accessibility projection with
  executable wrapped refs for the selected page state.
- `browser.click` accepts only a persisted ref from that snapshot.
- A fresh snapshot and `browser.verify` are required after every click before
  completion or another click.

Agent-browser's accessibility snapshot and native refs are the provider-owned
interaction truth. SparkClaw adds bounded model projection, relevance checks,
page identity, semantic fingerprints, repeated-state detection, and explicit
failure codes. Page text remains untrusted evidence and never becomes an
instruction source.

## Managed Chromium Profile

Normal execution is headless. Human-only verification may temporarily open a
visible Chromium process using the same SparkClaw-owned profile, but hidden and
visible processes must never own that profile concurrently. Authentication
state remains inside Chromium. SparkClaw does not copy credentials into another
process or attach to the owner's daily browser profile.

The default profile root is `./data/browser-profiles`. Profile access requires
an exclusive lock, bounded startup, bounded command execution, and cleanup of
owned child processes. A visible handoff must stop hidden ownership first; a
resume must use the actual selected post-login URL and fresh evidence.

The current browser Workflow does not expose login completion, so authentication
or human-verification requests fail closed instead of pretending they were
completed. The managed-profile lifecycle remains the foundation for a future
explicit login Workflow.

## Network And Safety Boundary

- Explicit targets must use normalized HTTP(S) URLs. Registered destinations
  resolve to frozen runtime URLs and bounded host/subdomain rules.
- URL fetch paths reject loopback, private, link-local, and otherwise forbidden
  literal hosts by default. Local fixtures require an explicit allowlist.
- Redirects and final page identity are revalidated.
- Existing unrelated tabs are not reused. Explicitly opened pages remain open
  after successful open or interaction.
- Unsafe or consequential controls are blocked even if they appear in a
  snapshot. Model output cannot bypass ref ownership or Policy.
- Screenshots, raw responses, and rendered text are artifacts/evidence, never
  trusted instructions.

## Configuration And Setup

Install and verify the pinned runtime:

```bash
npm install
npm run setup:browser
```

Important settings are defined in `configs/sparkclaw.default.json` and mirrored
by `docker/env/sparkclaw.example.env`:

| Setting | Purpose |
|---|---|
| `adapters.browserAutomation.command` | Pinned `agent-browser` executable |
| `adapters.browserAutomation.chromiumExecutable` | Optional explicit system Chromium path |
| `adapters.browserAutomation.profileDir` | SparkClaw-owned persistent profile root |
| `timeoutMs` / `startupTimeoutMs` / `daemonIdleTimeoutMs` | Bounded process lifecycle |
| `security.browser_read_allow_hosts` | Explicit private-host exceptions, primarily test fixtures |

Environment overrides use `SPARKCLAW_BROWSER_AUTOMATION_*`,
`SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`,
`SPARKCLAW_BROWSER_PROFILE_DIR`, and
`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS`. See [Deployment](deployment.md) for the
normal host and Compose commands.

## Verification

Browser changes should cover:

- adapter protocol, timeout, process ownership, and profile locking tests;
- read/snapshot normalization and untrusted-evidence tests;
- explicit URL, registered destination, tab focus, and redirect cases;
- stale/foreign refs, repeated state, unsafe controls, and attempt limits;
- private-host rejection and explicit fixture allowlisting;
- Workflow routing and stage-scoped tool exposure;
- `npm run setup:browser`, Gateway tests, WebChat tests/build, and the golden
  browser eval when the local fixture is available.
