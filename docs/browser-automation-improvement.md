# Browser Automation Improvement Plan

> Language: English | [简体中文](../zh-cn/docs/browser-automation-improvement.md)

This document is the focused plan for improving SparkClaw's browser-backed web access. The architecture overview remains in [Architecture](architecture.md); this file owns the browser read and interaction roadmap. Browser presentation modes are specified in [Browser Modes Coding Guide](browser-modes-coding-guide.md), and the hidden real-browser milestone is specified in [Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md).

## Goal

Unify public web search, URL reading and live page interaction under one browser capability. The browser layer should behave as a real browser-backed access system, not as a separate static fetch branch.

The target flow is:

```text
web.search discovers candidate URLs when needed
  -> browser.read chooses the mode-safe read path
      - collaborative/visible/forced session reads use ChromeDevTools MCP
      - autonomous/hidden reads avoid visible Chrome tabs until a hidden provider exists
  -> evaluate_script reads rendered DOM/HTML after page rendering and lazy loading when a browser session is selected
  -> ToolHub runs @mozilla/readability outside the browser to extract article text
  -> if the article text is incomplete, browser.snapshot inspects page structure
  -> browser.click or browser.navigate follows expand controls, pagination or internal links
  -> browser.read runs again on the changed page state
```

## Design Rules

- `browser.read` should use the real browser session first for collaborative/visible reads, explicit forced-session reads, or autonomous reads backed by a hidden/headless provider.
- Autonomous hidden reads must not open a user-visible Chrome tab through a provider that cannot hide it; use direct HTTP plus Readability until a hidden provider is available.
- The next autonomous browser milestone is a hidden Chromium/Chrome provider: real rendering, JavaScript execution and DOM extraction without showing a window.
- `browser.read` should not force `take_snapshot` for every normal page read.
- Readability is the default article extractor for rendered HTML.
- Structure snapshots are diagnostic and interactive aids, used only when body extraction is insufficient or page structure matters.
- Search remains a discovery tool, but search/read/interaction all belong to the browser access domain.
- Existing browser login state may be used, but SparkClaw must stop for passwords, captcha, 2FA, payment confirmation and other human-only steps.
- All browser observations remain untrusted external content.

## Standard Read Path

For a direct URL or a search result source page, the first read should do only the work needed to get readable page content. When the selected read path is a browser session, use:

```text
ChromeDevTools MCP new_page
  -> wait for page load/render state
  -> evaluate_script
       - scroll enough to trigger normal lazy loading
       - collect document.documentElement.outerHTML
       - collect document.body.innerText
       - collect title, lang, readyState, contentType and auth hints
  -> @mozilla/readability with jsdom in ToolHub
  -> return title, article text, metadata and read diagnostics
```

Expected first-read output:

- `read_mode=browser_session` when the browser path succeeds.
- `read_mode=direct_http` for autonomous hidden reads handled without a visible browser session.
- `rendered=true` when DOM/HTML came from the browser.
- `extractor=readability` and `readability_status=applied` when Readability succeeds.
- `browser_actions` should normally contain `new_page` and `evaluate_script`, not `take_snapshot`.
- `browser_snapshot_text` may be absent on a complete first read.

If the browser session is unavailable, `browser.read` may fall back to direct HTTP and must mark that path with `read_mode=direct_http_fallback`.

## Snapshot Trigger Rules

Call `browser.snapshot` after the first read only when one of these signals appears:

- Readability returns no text or very short text for a page that should contain content.
- The page looks like an index, directory, search result, login wall, captcha or paywall.
- `auth_challenge_detected=true`.
- The rendered HTML or text was truncated.
- The user asked about controls, comments, tables, downloads, menus, tabs, pagination or page structure.
- The answer likely depends on non-article content such as sidebars, accordions, attachments, related links or comment areas.

Snapshot output should be used to identify stable refs/uids, internal links and visible labels. It should not replace Readability as the normal article extraction path.

## Interaction Loop

When snapshot shows relevant page controls, the runtime can continue with a bounded loop:

```text
browser.snapshot
  -> choose one clear control or internal link
  -> browser.click or browser.navigate
  -> wait for state change when needed
  -> browser.read
  -> re-evaluate Readability output
```

Examples:

- Click `展开`, `更多`, `阅读全文` or `显示全部` before re-reading.
- Navigate to `下一页`, `招生章程`, `下载`, `通知正文` or a source document link when the snapshot exposes it.
- Stop and ask the user when the next step is login, captcha, SMS code, 2FA or payment confirmation.

The loop must stay bounded. If one retry does not reveal useful content, return the best evidence and explain the limitation.

## Tool Responsibilities

| Tool | Responsibility |
|---|---|
| `web.search` | Discover candidate public URLs and source pages. |
| `browser.read` | Mode-safe page read, rendered HTML capture when a browser session is selected, and Readability extraction. |
| `browser.snapshot` | Inspect page structure, controls, refs/uids, internal links and non-body affordances. |
| `browser.click` | Activate one clear ref/uid from the latest snapshot. |
| `browser.navigate` | Move the current browser session to a known URL while preserving context. |
| `browser.screenshot` | Visual verification when requested or when snapshot text is insufficient. |

## Implementation Status

The browser-read implementation uses ChromeDevTools MCP for collaborative/visible or forced-session reads, then uses `evaluate_script` to capture rendered DOM/HTML and runs `@mozilla/readability` outside the browser. Autonomous hidden reads currently use direct HTTP plus Readability when the available ChromeDevTools provider would otherwise open a visible tab.

The default `browser.read` path no longer takes a structure snapshot as part of every browser-session read. Explicit page-structure inspection still uses `browser.snapshot`, which maps to ChromeDevTools MCP `take_snapshot`.

Remaining work:

- Implement the hidden Chromium provider described in [Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md), so autonomous reads can be both real-browser-backed and non-visible.
- Replace autonomous `browser.snapshot` over the visible current tab with hidden-page or archived-HTML structure extraction.
- Improve follow-up selection quality after `browser.snapshot`, especially choosing the safest single internal link or expand control.
- Keep login and anti-bot handling as explicit future extensions.

## Implementation Phases

1. Documentation and contract update.
   - Keep this document as the focused browser improvement plan.
   - Update tool descriptions and skill guidance to say snapshot is on-demand.

2. Remove mandatory snapshot from `browser.read`. Done.
   - Keep `new_page -> evaluate_script -> Readability` as the default path when a browser session is selected.
   - Preserve direct HTTP fallback.
   - Return snapshot fields only when a snapshot was explicitly collected.

3. Add sufficiency diagnostics. Done.
   - Add or reuse fields such as `readability_status`, `readability_length`, `browser_html_truncated`, `auth_challenge_detected` and `needs_structure_snapshot`.
   - Teach the runtime observation adapter to expose these signals clearly.

4. Add bounded follow-up behavior. Partly done.
   - Let ReAct decide `browser.snapshot -> browser.click/navigate -> browser.read` when diagnostics say the first read is incomplete.
   - Avoid hidden multi-click browsing inside a single tool call until the behavior is easy to trace.
   - Current runtime behavior: when a `browser.read` observation includes `needs_structure_snapshot=true`, the next ReAct step can see `browser.snapshot`, `browser.navigate` and `browser.wait`; after a snapshot observation, it can also see approval-gated `browser.click`.

5. Prepare login and anti-bot extensions.
   - Keep browser profile/session configuration explicit.
   - Reuse existing login state when present.
   - Stop for human-only verification.
   - Leave hooks for future anti-bot handling without adding silent credential or captcha automation.

6. Add hidden Chromium access.
   - Follow [Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md).
   - Autonomous hidden reads should use real browser rendering when the hidden provider is available.
   - Autonomous snapshots should inspect the hidden page or archived HTML, never an unrelated visible `about:blank` tab.

## Validation

Minimum validation for this change set:

- Unit test that `browser.read` uses browser session HTML and Readability when automation is enabled.
- Unit test that autonomous hidden `browser.read` does not call a visible browser-session provider.
- Unit test that the default browser read does not call `take_snapshot`.
- Unit test or adapter test that explicit snapshot operations still map to ChromeDevTools MCP `take_snapshot`.
- `go test ./services/gateway/internal/browserautomation -count=1`
- `go test ./services/gateway/internal/toolhub -count=1`
- `go test ./services/gateway/internal/agent -count=1`
- `go test ./services/gateway/...`
- `git diff --check`
