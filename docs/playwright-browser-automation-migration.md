# Playwright Browser Automation Migration

> Language: English | [简体中文](../zh-cn/docs/playwright-browser-automation-migration.md)

This document defines the migration of SparkClaw browser automation from the
Chrome DevTools MCP subprocess to the official Microsoft Playwright Library.
It is the implementation contract for the migration; the public ToolHub browser
API and the managed-profile product contract remain stable.

Related documents:

- [Managed Shared Chromium Profile](managed-persistent-browser-profile.md)
- [Browser Automation Improvement Plan](browser-automation-improvement.md)
- [Browser Modes Coding Guide](browser-modes-coding-guide.md)
- [Browser Login State Operation Guide](browser-login-state-operation.md)

## Decision

SparkClaw will use the official `playwright` Node.js library, pinned to a
validated version, to install, launch, and operate its matching local Chromium
binary. Operators may explicitly configure a custom Chromium executable, but
the version-matched Playwright browser is the stable default.
The Go gateway will communicate with one SparkClaw-owned Node driver over a
small newline-delimited JSON protocol. The driver is an implementation detail,
not a model-visible tool server.

The new implementation must not:

- start `chrome-devtools-mcp` or speak MCP/JSON-RPC;
- use `connectOverCDP`, a remote-debugging port, a browser WebSocket endpoint,
  or a user-supplied DevTools endpoint;
- attach to the owner's daily Chrome profile;
- change the public `browser.*` ToolHub names, schemas, risk levels, approval
  rules, or result envelope.

## Why Playwright

The previous adapter translated each SparkClaw operation into a
Chrome DevTools MCP tool name and then repaired provider-specific output,
selected-page text, and `about:blank` behavior in Go. A stalled MCP response
could block the serialized session, and page interactions depended on
provider-generated refs.

Playwright provides a direct browser lifecycle, page and context ownership,
locator actionability checks, automatic waiting for interaction readiness, and
bounded navigation APIs. SparkClaw still owns orchestration, authorization,
timeouts, evidence handling, and profile transitions.

## Dependency And Runtime Layout

- The repository root declares an exact `playwright` runtime dependency.
- The existing project `npm install` installs the Node driver dependency, and
  `npm run setup:browser` installs the matching local Chromium binary.
- SparkClaw uses Playwright Library, not Playwright Test, at runtime.
- The Node driver source is embedded in the Go binary and executed with the
  configured Node command. Module resolution is rooted at the repository
  workspace containing the installed `playwright` package.
- The default launch omits `executablePath`, allowing Playwright to resolve the
  browser revision installed for its pinned package version.
- `chromiumExecutable` is an explicit override for managed environments. When
  set, SparkClaw validates the path and passes it as `executablePath`.

Configuration keeps one browser adapter boundary:

```json
{
  "adapters": {
    "browserAutomation": {
      "nodeCommand": "node",
      "timeoutMs": 30000,
      "chromiumExecutable": "",
      "profileDir": "./data/browser-profiles"
    }
  }
}
```

`mcpCommand` and `mcpArgs` are removed. Model or tool arguments cannot override
the Node command, executable path, profile root, or Playwright launch options.

## Process And Profile Ownership

The Go adapter serializes browser operations and owns at most one Playwright
driver process. The driver owns exactly one persistent Chromium context for one
owner, logical profile, and presentation.

```text
Go PlaywrightAdapter
  -> embedded Node Playwright driver
      -> chromium.launchPersistentContext(userDataDir, options)
          -> BrowserContext
              -> Page[]
```

The existing profile layout remains unchanged:

```text
<profile-root>/<owner-hash>/<profile-hash>/user-data/
```

Hidden and visible presentations are mutually exclusive. A change of owner,
logical profile, or presentation closes the current context and driver, waits
for Chromium to release the profile, and then starts the replacement. Gateway
shutdown closes the driver and persistent context. SparkClaw never deletes
Chromium lock files.

Launch options are owned by the adapter:

| Presentation | Playwright options |
|---|---|
| `hidden` | `headless: true`, stable `viewport: 1365x768` |
| `visible` | `headless: false`, `viewport: null` |

Both use the same resolved browser revision, absolute `userDataDir`, bounded
default timeout, and HTTPS-error policy. By default `executablePath` is omitted
and Playwright uses its matching installed Chromium. If a custom local
`chromiumExecutable` is configured, both presentations use that same validated
path; compatibility is then the operator's responsibility.

## Driver Protocol

The driver reads one JSON object per stdin line and writes one JSON response per
stdout line. Every request and response carries a numeric `id`.

```json
{"id":1,"method":"list_pages","params":{}}
{"id":1,"result":{"pages":[]}}
```

Errors are explicit and separate from successful output:

```json
{"id":1,"error":{"code":"playwright_action_failed","message":"..."}}
```

Driver diagnostics go to stderr and are bounded by the Go adapter. The driver
must not write logs to stdout. A canceled or timed-out Go request terminates the
driver so a late response cannot corrupt the next request.

## Public Tool Mapping

The public ToolHub contract remains provider-neutral:

| SparkClaw tool | Playwright operation |
|---|---|
| `browser.status` | ensure context, report driver/browser health |
| `browser.list_tabs` | `context.pages()` plus stable session page IDs |
| `browser.open` | reuse the sole blank page or `context.newPage()`, then `page.goto()` |
| `browser.focus` | select page and `page.bringToFront()` |
| `browser.close` | `page.close()` and select a remaining page |
| `browser.navigate` | `page.goto()`, `goBack()`, `goForward()`, or `reload()` |
| `browser.snapshot` | accessibility snapshot plus a bounded interactive-element ref table |
| `browser.screenshot` | `page.screenshot()` returned as base64 evidence |
| `browser.wait` | Locator/text wait with the operation timeout |
| `browser.click` | Locator click with Playwright actionability and auto-waiting |
| `browser.type` | Locator `fill()` or focused-keyboard typing |
| `browser.select` | Locator `selectOption()` |
| `browser.read` | `page.goto()` followed by bounded DOM evaluation and optional snapshot |

Navigation waits for `domcontentloaded`, then performs a short bounded settle
for client rendering. It does not use `networkidle` as a universal completion
condition because long-lived network connections make it unreliable.

## Page And Element References

Page IDs are stable numeric IDs allocated by the driver for the life of its
context. They do not expose Playwright objects or DevTools target IDs.

Each `browser.snapshot` replaces the previous element-ref table for the selected
page. The driver enumerates visible interactive elements, assigns bounded refs
such as `e1`, and returns role, accessible name, element type, and selected
state. `browser.click`, `browser.type`, and `browser.select` resolve only refs
from the latest snapshot. A missing, stale, hidden, detached, or ambiguous ref
fails explicitly; the caller must take a new snapshot.

This keeps model-visible refs stable within one observation while allowing
Playwright Locator actionability checks to handle rendering races.

## Read And Evidence Contract

`browser.read` keeps the existing output schema. The driver returns rendered
URL, title, language, ready state, content type, bounded HTML, visible text,
scroll height, truncation signals, and authentication indicators. ToolHub still
runs Readability outside the browser and archives browser output as untrusted
evidence.

Snapshots remain on demand. A normal read is:

```text
page.goto -> bounded render settle -> page.evaluate -> Readability
```

An explicit or diagnostic snapshot adds accessibility and element-ref evidence.
No page content can alter driver commands, launch configuration, Policy, or
approval decisions.

## Timeouts And Recovery

- Every driver request uses the caller deadline or the configured adapter
  timeout. Browser launch reserves a short cleanup margin inside that bound.
- Playwright context and page default timeouts use the same configured bound.
- A timeout, malformed response, EOF, or driver crash resets the session and
  terminates the driver process group so Chromium children cannot be orphaned.
- The next call may start one clean driver for the same managed profile.
- Business failures such as stale refs, navigation errors, login walls, and
  profile lock errors remain distinguishable.
- No operation retries a mutating interaction automatically. Read-only session
  startup may be retried only by a later caller after reset.

## Security And Login Handoff

The managed-profile and human-verification rules do not change:

- Chromium remains the source of truth for cookies, storage, IndexedDB, service
  workers, and login state.
- SparkClaw does not export cookies or credentials.
- Passwords, captcha, SMS, 2FA, permission grants, payments, and account
  security actions remain visible and human-controlled.
- A hidden login challenge closes the hidden driver before opening the same
  profile visibly.
- Resume captures the selected visible page URL, closes the visible context,
  reopens the same profile headlessly, verifies the page, and resumes the run.

## Migration Sequence

1. Add this contract and its Chinese mirror.
2. Pin `playwright` and add browser setup/doctor checks.
3. Add the embedded Node driver and its protocol tests.
4. Replace `ChromeDevToolsAdapter` and MCP stdio code with `PlaywrightAdapter`.
5. Update provider names, tool descriptions, configuration, and browser docs.
6. Replace MCP-shape unit tests with provider-neutral and Playwright protocol
   tests while preserving ToolHub behavior tests.
7. Run fixture, hidden Chromium, visible lifecycle, screenshot, interaction,
   profile-switch, shutdown, and login-handoff verification.

## Acceptance Criteria

- No production reference to `chrome-devtools-mcp`, MCP browser tools,
  `connectOverCDP`, DevTools WebSockets, or remote-debugging ports remains.
- A clean setup installs an exact Playwright version and validates it in
  `scripts/doctor.sh`.
- Hidden and visible operations launch the version-matched local Playwright
  Chromium (or one explicit custom override) through `launchPersistentContext`
  and reuse the same managed profile serially.
- Open/list/focus/close/navigate/read/snapshot/screenshot/wait/click/type/select
  all retain their public ToolHub contracts.
- Locator interactions auto-wait and reject stale snapshot refs explicitly.
- Timeouts and shutdown leave no driver or Chromium child process behind.
- Existing direct-HTTP fallback, Readability, untrusted evidence, Policy,
  approval, and login-handoff behavior remain intact.
- Browser unit tests, ToolHub browser scenarios, gateway build/vet, and a real
  local Chromium smoke test pass.

## Official References

- [Microsoft Playwright repository](https://github.com/microsoft/playwright)
- [Playwright Library documentation](https://playwright.dev/docs/library)
- [BrowserType.launchPersistentContext](https://playwright.dev/docs/api/class-browsertype#browser-type-launch-persistent-context)
- [Playwright actionability and auto-waiting](https://playwright.dev/docs/actionability)
- [Locator API](https://playwright.dev/docs/api/class-locator)
