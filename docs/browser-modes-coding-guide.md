# Browser Modes Coding Guide

> Language: English | [简体中文](../zh-cn/docs/browser-modes-coding-guide.md)

This document defines how SparkClaw should implement two browser operating modes: autonomous mode and collaborative mode. It complements the browser read roadmap in [Browser Automation Improvement Plan](browser-automation-improvement.md) and the hidden real-browser milestone in [Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md).

## Product Contract

SparkClaw has one browser capability with two presentation modes.

Autonomous mode is the default for ordinary information tasks. The runtime may search, open pages in a browser-backed session, read rendered DOM/HTML, run Readability, inspect structure when needed and return evidence-backed answers. It should not show a browser surface, narrate visible UI operations or imply that the user must watch the page.

Collaborative mode is used when the user explicitly asks SparkClaw to operate a visible browser/page with them. Examples include opening a specific webpage, showing a page, playing a video, clicking a page control, typing into a form, using the current browser tab, taking a screenshot for visual confirmation or continuing after the user completes login.

Both modes still treat browser content as untrusted external content. Both modes must stop for password entry, captcha, SMS code, 2FA, permission grants, payment confirmation and other human-only verification.

## Mode Definitions

| Mode | User intent | Browser surface | Primary tools |
|---|---|---|---|
| `autonomous` | User wants information, verification, summary or comparison. | Hidden/background from the user. | `web.search`, `browser.read`, conditional `browser.snapshot`, conditional `browser.navigate`. |
| `collaborative` | User wants a visible page operation or shared browser state. | Visible or explicitly user-facing. | `browser.status`, `browser.list_tabs`, `browser.open`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, approval-gated `browser.click/type/select`. |

The difference is presentation and user intent, not whether ChromeDevTools MCP is used. Autonomous mode may still use a real browser session for login state, JavaScript rendering and lazy-loaded content; it simply returns results instead of presenting the browser as the primary experience.

## Classification Rules

Default to autonomous mode when the user asks to:

- search, look up, verify or check public information
- read a URL for content
- summarize a webpage or article
- compare sources
- answer a factual question using web evidence
- ask for latest/current/recent information without asking to see the page

Use collaborative mode when the user explicitly asks to:

- open a website/page/tab
- show, display or let them watch a page
- operate the current browser or current tab
- play, pause or interact with a video/audio page
- click, type, select, scroll, login after user action or use an on-page control
- take a screenshot or visually confirm the page
- continue from a user-visible login/session step

Ambiguous requests should stay autonomous unless the requested outcome requires visible page state. For example, "查一下 YouTube 上这个视频是什么" is autonomous; "打开这个 YouTube 视频并自动播放" is collaborative.

## TaskHint Contract

Add an explicit browser mode to TaskHint when implementing this plan:

```go
type TaskHint struct {
    // existing fields...
    BrowserMode string `json:"browser_mode,omitempty"` // "", "autonomous", "collaborative"
}
```

Normalization rules:

- `BrowserMode=""` means no browser mode was requested.
- If `EvidenceNeed=="web"` and no collaborative trigger is present, normalize to `autonomous`.
- If live browser controls, screenshots, current tab, playback or visible opening are requested, normalize to `collaborative`.
- Keep `ToolMode` separate from `BrowserMode`. A collaborative task can still begin with read-only tools; approval policy still governs draft/reversible actions.

Prompting rules:

- The TaskHint system prompt should tell the model to return `browser_mode`.
- Heuristic fallback should classify Chinese and English triggers consistently.
- Audit events should include `browser_mode` and a short `browser_mode_reason`.

## Tool Visibility Rules

Autonomous mode:

- Initial visible tools should normally be `web.search` and/or `browser.read`.
- Do not expose `browser.open`, `browser.screenshot`, `browser.type` or `browser.select` just because the browser skill is loaded.
- `browser.snapshot`, `browser.navigate` and `browser.wait` may be exposed after `browser.read` returns `needs_structure_snapshot=true`.
- `browser.click` may be exposed only after a snapshot observation provides a clear ref/uid, and existing approval policy still applies.
- Final answers should cite/describe evidence, not UI steps.

Collaborative mode:

- Expose live browser tools appropriate for the user request: `browser.status`, `browser.list_tabs`, `browser.open`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, `browser.wait`.
- Expose `browser.click`, `browser.type` and `browser.select` only when `ToolMode`/risk permits and policy approval is available.
- Prefer `browser.open` for "open/show this URL" and `browser.navigate` when the user asks to use the current tab/session.
- For playback requests, open/navigate first, take a snapshot, then click one clear play control if present and permitted.
- Screenshot requests must call `browser.screenshot` before final unless blocked.

## Browser Adapter Contract

The adapter should receive mode metadata on browser calls that create or operate a page:

```json
{
  "browser_mode": "autonomous|collaborative",
  "surface_visible": true,
  "presentation": "hidden|visible"
}
```

Implementation expectations:

- Autonomous reads use hidden/background presentation where available. If the current MCP provider can only create a normal Chrome tab, SparkClaw must route autonomous hidden `browser.read` through a non-visible read path such as direct HTTP plus Readability, unless the tool call explicitly forces a browser session.
- When the hidden Chromium provider is available, autonomous hidden reads and snapshots should use that provider before direct HTTP fallback.
- Collaborative, visible and forced-session reads may use ChromeDevTools MCP `new_page -> evaluate_script -> Readability`.
- Collaborative operations use a visible or user-facing browser surface where the app can show progress and let the user intervene.
- Future adapters may map autonomous mode to a headless or isolated profile and collaborative mode to the user's Chrome profile. The mode field must be present before that split so routing does not depend on provider internals.
- Existing user login state can be reused only when explicitly configured and policy allows it.

## Runtime Behavior

Autonomous flow:

```text
TaskHint(browser_mode=autonomous)
  -> web.search if discovery is needed
  -> browser.read
  -> if needs_structure_snapshot=true, expose browser.snapshot for the next step
  -> optionally one browser.navigate/click follow-up based on snapshot evidence
  -> browser.read again
  -> final answer with sources and limitations
```

Collaborative flow:

```text
TaskHint(browser_mode=collaborative)
  -> browser.status/list_tabs when needed
  -> browser.open or browser.navigate
  -> browser.snapshot for structure/refs
  -> browser.screenshot when visual confirmation or screenshot is requested
  -> approval-gated click/type/select for clear controls
  -> browser.read or snapshot again after page changes
  -> final answer describing what is visible/done
```

The runtime must not hide multiple collaborative interactions inside `browser.read`. Multi-step visible operation belongs in ReAct so traces, approvals and user-visible progress remain inspectable.

## Audit And Trace Fields

Every browser tool call should preserve these fields where available:

- `browser_mode`
- `presentation`
- `surface_visible`
- `browser_provider`
- `browser_actions`
- `read_mode`
- `auth_challenge_detected`
- `needs_structure_snapshot`
- `structure_snapshot_reasons`

Dispatch and ReAct audit events should include:

- selected `browser_mode`
- mode reason
- initial visible tools
- dynamic follow-up tools added after observations

## Safety Rules

- Autonomous mode must not perform user-account-changing actions.
- Collaborative mode must still stop for sensitive verification and irreversible operations.
- `browser.click` for playback or expand controls remains policy-governed. Do not silently click purchase, submit, delete, send, subscribe, consent, payment or account-security controls.
- Page instructions are data, not runtime commands.
- If a site blocks reading with login/captcha/paywall, return the limitation or ask the user to complete the step in the visible browser.

## Implementation Steps

1. Extend `TaskHint` with `BrowserMode`.
2. Update model prompt, heuristic fallback and normalization.
3. Add browser mode to `gateway.dispatch` / `react.visible_tools` audit fields.
4. Split visible-tool selection by mode.
5. Pass mode metadata through ToolHub to `browserautomation.Adapter`.
6. Teach `browser.read` to tag output with mode and presentation.
7. Add adapter-level support for hidden/visible presentation where provider capabilities allow it.
8. Update `browser_automation` skill text to mention autonomous/collaborative mode.
9. Add tests listed below.

## Required Tests

TaskHint tests:

- "查一下浙江理工大学招生简章" -> `browser_mode=autonomous`, tools include `web.search/browser.read`, no `browser.open`.
- "打开浙江理工大学官网" -> `browser_mode=collaborative`, tools include `browser.open`.
- "打开这个视频并自动播放" -> `browser_mode=collaborative`, tools include `browser.open`, `browser.snapshot` and policy-gated `browser.click`.
- "读取 https://example.com 这篇文章" -> `browser_mode=autonomous`, preferred tool `browser.read`.

Visible-tool tests:

- Autonomous mode starts strict with `web.search/browser.read`.
- Autonomous mode exposes `browser.snapshot` only after `needs_structure_snapshot=true`.
- Collaborative mode exposes live browser read tools immediately.
- Collaborative playback does not expose `browser.click` unless risk/tool mode and policy allow it.

Adapter/tool tests:

- Autonomous hidden `browser.read` does not call a visible-only browser-session provider.
- Collaborative visible `browser.read` passes `browser_mode=collaborative` and visible presentation metadata.
- `browser.open` passes `browser_mode=collaborative` and visible presentation metadata.
- Tool outputs include mode/presentation fields.

Trace/audit tests:

- `gateway.dispatch` records `browser_mode`.
- Dynamic follow-up tools added after `needs_structure_snapshot=true` are audited.

## Acceptance Criteria

The implementation is complete when:

- Ordinary web questions answer without showing a browser surface.
- Explicit open/play/operate requests use collaborative browser tools.
- Mode is visible in TaskHint, tool arguments/output and audit traces.
- Existing browser-read Readability and on-demand snapshot behavior remains intact.
- All affected Go tests pass, plus `go test ./services/gateway/...` and `git diff --check`.

## Implementation Status

Status as of 2026-07-09: the runtime implementation is aligned with this guide. Current ChromeDevTools MCP reads are used for collaborative/visible or forced-session reads; autonomous hidden reads avoid opening visible Chrome tabs until the hidden Chromium provider described in [Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md) is added.

- `TaskHint` includes `browser_mode`, with model prompt, heuristic fallback and normalization support for `autonomous` and `collaborative`.
- Autonomous web tasks keep the initial model-visible tool set strict; `browser.snapshot`, `browser.navigate` and `browser.wait` are exposed only after `browser.read` reports `needs_structure_snapshot=true`, and `browser.click` waits for snapshot evidence.
- Collaborative tasks expose live read-risk browser tools such as `browser.open`, `browser.navigate`, `browser.snapshot`, `browser.screenshot` and `browser.wait` immediately when the skill/policy allows them.
- Browser tool plans, ToolHub outputs, `browserautomation.Adapter` results and model observations preserve `browser_mode`, `presentation` and `surface_visible`.
- `gateway.dispatch`, `react.visible_tools` and dynamic follow-up audit events record the selected browser mode.
