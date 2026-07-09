# Hidden Chromium Browser Access Plan

> Language: English | [简体中文](../zh-cn/docs/browser-hidden-chromium-access.md)

This document defines the next browser-access milestone for SparkClaw:
autonomous mode should be able to visit webpages as a real Chromium/Chrome
browser without showing a window to the user. It complements the broader
[Browser Automation Improvement Plan](browser-automation-improvement.md) and
the presentation contract in [Browser Modes Coding Guide](browser-modes-coding-guide.md).

## Source Relationship

The implementation should be compatible with the Chromium browser model. The
public reference point is the official GitHub mirror of Chromium source:
[chromium/chromium](https://github.com/chromium/chromium).

SparkClaw should **not** vendor or compile Chromium source in the gateway.
Instead, it should use an installed Chrome/Chromium/Chrome for Testing binary
or an existing DevTools-capable provider, and drive it through the current
browser automation adapter boundary.

## Goal

Autonomous web reads should have a real rendered browser path:

```text
web.search
  -> browser.read(browser_mode=autonomous, presentation=hidden)
  -> hidden Chromium/Chrome page loads URL
  -> JavaScript, redirects, cookies, normal rendering and lazy loading complete
  -> evaluate_script extracts DOM/HTML/text/page metadata
  -> ToolHub runs @mozilla/readability
  -> autonomous structure snapshot inspects links, buttons and page affordances
  -> optional bounded hidden navigate/read follow-up
  -> final answer with source evidence
```

The user should not see a browser window, tab, screenshot, or UI narration for
ordinary information tasks.

## Non-Goals

- Do not bypass login, captcha, 2FA, paywalls, bot checks or payment flows.
- Do not silently use the user's visible Chrome profile for autonomous reads.
- Do not make `browser.snapshot` depend on whatever visible browser tab happens
  to be focused.
- Do not hide multi-step account-changing browser operations inside autonomous
  tools.
- Do not add a hard dependency on a developer-machine-specific Chromium path.

## Provider Contract

SparkClaw should keep one browser capability with two provider surfaces:

| Surface | Mode | Window | Profile | Primary use |
|---|---|---|---|---|
| hidden Chromium provider | `autonomous` | none/headless | isolated runtime profile by default | search/read/render/structure extraction |
| visible DevTools provider | `collaborative` | visible | configured user profile only when allowed | open/show/click/type/screenshot/login handoff |

The hidden provider may be implemented by either:

- configuring the existing ChromeDevTools MCP subprocess to launch/use a
  headless Chrome/Chromium instance, if that provider supports the needed launch
  flags and lifecycle control; or
- adding a new adapter implementation behind the existing
  `browserautomation.Adapter` boundary that starts Chrome/Chromium headless and
  speaks the Chrome DevTools Protocol directly.

Both choices must reuse the existing ToolHub and ReAct routing contracts. The
model should still call `web.search`, `browser.read`, `browser.snapshot`,
`browser.navigate` and related tools; provider selection belongs below ToolHub.

## Current Implementation Notes

The first implementation uses the existing ChromeDevTools MCP adapter and starts
a second MCP stdio session for autonomous hidden access. The visible
collaborative session keeps the configured MCP arguments unchanged; the hidden
session appends provider launch flags for `--headless`, `--isolated`,
`--viewport=1365x768` and `--no-usage-statistics` when they are not already
configured.

`browser.read(browser_mode=autonomous, presentation=hidden)` now tries this
hidden browser session first and reports `read_mode=hidden_browser_session`.
If the hidden provider cannot start or navigate, the existing direct HTTP path
remains the fallback and reports `read_mode=direct_http_fallback`.

Autonomous hidden `browser.snapshot` with a URL reads that URL through the
hidden browser session before calling `take_snapshot`. If no URL or future page
reference is provided, it fails explicitly instead of snapshotting whatever
visible tab happens to be focused.

After a hidden snapshot, `browser.open`, `browser.navigate`, `browser.click`
and `browser.wait` also stay on the hidden session when the call carries
`browser_mode=autonomous`, `presentation=hidden` and `surface_visible=false`.
Those follow-up calls add a lightweight current-page state (`current_url`,
`current_title`, `current_ready_state`) so the model can decide whether to call
`browser.read` again or stop.

## Launch Requirements

A hidden Chromium process should be launched with an isolated, owned lifecycle:

- root it in a cancellable context and close it from `Adapter.Close()`
- apply gateway timeouts to every page operation
- use a runtime-owned user data directory, not the user's normal Chrome profile
- allow an explicit configured Chrome/Chromium executable, with cross-platform
  discovery fallback
- avoid focusing or showing a window
- keep stderr/stdout captured for diagnostics without leaking page content into
  logs
- delete or expire temporary profiles according to storage policy

Typical launch intent:

```text
chrome-or-chromium
  --headless=new
  --remote-debugging-port=0 or --remote-debugging-pipe
  --user-data-dir=<runtime-owned-profile>
  --no-first-run
  --no-default-browser-check
```

Exact flags should be provider-specific and tested on macOS/Linux before they
become defaults.

## Read Path

For autonomous reads, `browser.read` should choose the hidden browser session
when available:

```text
browser.read
  -> select hidden provider for autonomous/hidden
  -> create or reuse hidden page context
  -> navigate to URL
  -> wait for load/domcontentloaded/network-idle bounded by timeout
  -> perform small scroll loop for lazy-loaded body content
  -> evaluate DOM extraction script
  -> return rendered HTML/text and page metadata
  -> run Readability in ToolHub
```

Expected output additions:

- `read_mode=hidden_browser_session`
- `rendered=true`
- `browser_mode=autonomous`
- `presentation=hidden`
- `surface_visible=false`
- `browser_provider=chromium-headless` or a specific provider name
- `browser_actions` such as `new_hidden_page`, `navigate`, `evaluate_script`
- `browser_page_ref` or equivalent stable hidden session reference
- `auth_challenge_detected` when login/captcha/password indicators appear

If the hidden provider is unavailable, the current direct HTTP path remains the
fallback. It must continue to report `read_mode=direct_http` or
`direct_http_fallback`.

## Autonomous Snapshot

Autonomous `browser.snapshot` must not call visible-browser `take_snapshot` on
the currently focused tab. It should use one of these sources, in order:

1. the hidden Chromium page referenced by the latest `browser.read`
2. a `browser_page_ref` supplied by the model/runtime
3. the archived raw HTML from `snapshot_ref`
4. a fresh hidden/direct read of the requested URL

The snapshot should describe page structure, not only article text:

- document title and final URL
- canonical URL and meta description
- headings
- links with label, absolute URL, internal/external classification
- buttons and button-like elements
- forms and inputs without sensitive values
- tables with captions/header summaries and row/column counts
- attachments and download links
- pagination and next/previous links
- expand/read-more affordances such as `展开`, `更多`, `阅读全文`, `显示全部`
- login/captcha/paywall/auth hints

Autonomous snapshot output should use stable structural identifiers for the
snapshot result, but these identifiers are not the same as visible-browser
accessibility refs. They may guide `browser.navigate` or another `browser.read`;
they should not be treated as permission to click sensitive controls.

## Hidden Interaction Loop

The first hidden interaction loop should be conservative:

```text
browser.read
  -> needs_structure_snapshot=true
  -> browser.snapshot using hidden page or archived HTML
  -> choose one safe internal link or one safe expand/read-more control
  -> browser.navigate or approval-gated browser.click
  -> browser.read again
  -> final
```

Allowed autonomous actions:

- follow a public internal link
- expand non-sensitive article text
- move to next page in a public article/list
- download/read public attachments through existing document tools

Stop instead of acting when the next step is login, password, captcha, SMS code,
2FA, consent, purchase, submit, delete, send, subscribe or account security.

## Mode Routing

ToolHub should route by metadata:

```text
browser_mode=autonomous + presentation=hidden
  -> hidden Chromium provider if available
  -> direct HTTP fallback if unavailable

browser_mode=collaborative or presentation=visible or surface_visible=true
  -> visible DevTools provider
```

Forced-session arguments can still exist, but they must be explicit and audited.
If a forced autonomous browser session would create a visible window, ToolHub
should reject it or downgrade to direct HTTP instead of surprising the user.

## Failure Semantics

The hidden browser path should return explicit failures:

- `hidden_browser_unavailable`
- `navigation_timeout`
- `render_timeout`
- `auth_challenge_detected`
- `captcha_detected`
- `snapshot_source_missing`
- `provider_opened_visible_surface`

An `about:blank` snapshot after a URL-based read is not useful evidence. It
should be treated as a failed snapshot and trigger a repair path, not a normal
completed observation.

## Observability

Every hidden browser call should preserve:

- selected provider
- launch mode
- profile mode
- page ref
- final URL
- readiness state
- DOM/text lengths and truncation
- whether JavaScript execution completed
- whether auth/captcha/paywall was detected
- whether any fallback occurred

Audit events should make it clear whether a read used hidden browser rendering,
direct HTTP, or visible collaborative browser automation.

## Implementation Phases

1. Documentation and contracts. Done when this document and its Chinese mirror
   are linked from the browser roadmap.
2. Provider capability detection.
   - Detect whether current ChromeDevTools MCP can launch/use headless Chrome.
   - If not, introduce a separate hidden Chromium adapter behind
     `browserautomation.Adapter`.
3. Hidden read path.
   - Implement autonomous hidden `browser.read`.
   - Return rendered HTML/text with `read_mode=hidden_browser_session`.
4. Autonomous structure snapshot.
   - Parse hidden DOM or archived HTML into structured links/buttons/forms.
   - Stop using visible `take_snapshot` for autonomous hidden snapshot.
5. Hidden follow-up loop.
   - Support one safe navigate/expand/read retry.
   - Keep approvals and sensitive-action stops intact.
6. Login and anti-bot extension points.
   - Preserve explicit handoff to collaborative mode for user-completed login.
   - Keep anti-bot handling as future policy-governed work.

## Required Tests

- Autonomous hidden `browser.read` returns rendered JS content from a fixture
  without opening a visible provider.
- Autonomous hidden `browser.read` reports `read_mode=hidden_browser_session`,
  `rendered=true`, `presentation=hidden` and `surface_visible=false`.
- Autonomous `browser.snapshot` after hidden/direct `browser.read` uses the
  target page or archived HTML, never an unrelated `about:blank` tab.
- Structure snapshot extracts links, buttons, headings, forms, tables,
  attachments and pagination from fixture HTML.
- If hidden browser launch fails, `browser.read` falls back to direct HTTP with
  explicit fallback metadata.
- Collaborative visible tools still use the visible provider and screenshots
  still render as before.
- `Adapter.Close()` terminates hidden browser subprocesses.
- `go test ./services/gateway/internal/browserautomation -count=1`
- `go test ./services/gateway/internal/toolhub -count=1`
- `go test ./services/gateway/internal/agent -count=1`
- `go test ./services/gateway/...`
- `git diff --check`

## Acceptance Criteria

The milestone is complete when ordinary Weixin/web information questions can
load JavaScript-rendered pages through a real browser engine, extract article
content and inspect page structure without showing a browser window. Explicit
open/show/play/click/screenshot requests must still use collaborative visible
browser tools.
