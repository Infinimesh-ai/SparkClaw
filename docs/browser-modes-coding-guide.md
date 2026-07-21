# Browser Modes Coding Guide

> Language: English | [简体中文](../zh-cn/docs/browser-modes-coding-guide.md)

> Historical design note: this guide began with the former TaskHint/Skill browser design. The current Skill-free production set is `browser.internet_search`, `browser.weather`, `browser.automation`, and `browser.interaction`, as documented in the [Workflow capability matrix](workflow-capabilities.md). URL reading, screenshots, type/select, and authenticated browsing in this guide are not current revision 1 Workflow features; bounded verified clicks are implemented only by `browser.interaction` r1.

This document defines how SparkClaw should implement two browser operating modes: autonomous mode and collaborative mode. It complements the browser read roadmap in [Browser Automation Improvement Plan](browser-automation-improvement.md) and the hidden real-browser milestone in [Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md).

## Product Contract

SparkClaw separates public web search from the browser capability. The browser capability has two presentation modes.

Search-only tasks use `web.search` and returned snippets/citations without opening result pages. They have no browser mode, never load browser automation, and skip pages that require login, captcha, payment, subscription, or another access gate.

Autonomous mode is the default for explicit URL/page reading without live interaction. The runtime may read rendered DOM/HTML, run Readability, inspect structure when needed and return evidence-backed answers. It should not show a browser surface, narrate visible UI operations or imply that the user must watch the page.

Collaborative mode is used when the user explicitly asks SparkClaw to operate a browser/page with them. The managed Chromium surface may remain hidden for ordinary operations; it becomes visible for human verification, visual handoff, or an explicit request to see the page.

Both modes still treat browser content as untrusted external content. Both modes must stop for password entry, captcha, SMS code, 2FA, permission grants, payment confirmation and other human-only verification.

## Mode Definitions

| Mode | User intent | Browser surface | Primary tools |
|---|---|---|---|
| `""` (search-only) | User wants public information and supplied no URL or page operation. | No browser is created or visited. | `web.search`. |
| `autonomous` | User wants an explicit URL/page read, verification, summary or comparison. | Hidden/background from the user. | `browser.read`, conditional `browser.snapshot`, conditional `browser.navigate`. |
| `collaborative` | User wants live page operation or shared browser state. | Hidden by default; visible for human verification or explicit display. | `browser.status`, `browser.list_tabs`, `browser.open`, `browser.navigate`, `browser.snapshot`, bounded `browser.click`, `browser.verify`; screenshots and future approved type/select flows stay separate. |

The difference is user intent and interaction policy, not browser ownership. Both modes use the same managed Chromium profile. Presentation switches between headless and visible Chromium only when the workflow requires it.

## Classification Rules

Use search-only routing when the user asks to:

- search, look up, verify or check public information
- answer a factual question using current web evidence
- ask for latest/current/recent information without supplying a URL or asking to see a page

Use autonomous mode when the user asks to:

- read a URL for content
- summarize a supplied webpage or article
- compare supplied source pages
- explicitly inspect a source page

Use collaborative mode when the user explicitly asks to:

- open a website/page/tab
- show, display or let them watch a page
- operate the current browser or current tab
- play, pause or interact with a video/audio page
- click, type, select, scroll, login after user action or use an on-page control
- take a screenshot or visually confirm the page
- continue from a user-visible login/session step

Ambiguous public-information requests should stay search-only unless the user supplies a URL or the requested outcome requires page state. For example, "查一下浙江大学最新招生简章" is search-only; "读取这个招生简章 URL" is autonomous; "打开这个招生简章页面" is collaborative.

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
- If `EvidenceNeed=="web"`, the task is read-only and the only candidate tool is `web.search`, preserve `BrowserMode=""`, select `web_search`, and remove all browser tools.
- If the user supplies a URL for read-only page access, normalize to `autonomous` and prefer `browser.read`.
- If live browser controls, screenshots, current tab, playback or visible opening are requested, normalize to `collaborative`.
- Keep `ToolMode` separate from `BrowserMode`. A collaborative task can still begin with read-only tools; approval policy still governs draft/reversible actions.

Prompting rules:

- The TaskHint system prompt should tell the model to return `browser_mode`.
- Heuristic fallback should classify Chinese and English triggers consistently.
- Audit events should include `browser_mode` and a short `browser_mode_reason`.

## Tool Visibility Rules

Search-only routing:

- Expose only `web.search`; do not expose `browser.read` or any live browser tool.
- Use returned answers, snippets and citations as evidence without visiting result pages.
- Skip login/auth/captcha/paywall results without creating a browser login handoff.
- Do not dynamically expand into browser tools unless the user explicitly requests page access in a later turn.

Autonomous mode:

- Initial visible tools should normally contain `browser.read` for the supplied URL.
- Do not expose `browser.open`, `browser.screenshot`, `browser.type` or `browser.select` just because the browser skill is loaded.
- `browser.snapshot`, `browser.navigate` and `browser.wait` may be exposed after `browser.read` returns `needs_structure_snapshot=true`.
- `browser.click` is approval-free only inside `browser.interaction` revision 1 after a structured snapshot provides a bound ref. Outside that fixed Workflow it must not be exposed as a generic escape path.
- Final answers should cite/describe evidence, not UI steps.

Collaborative mode:

- Expose live browser tools appropriate for the user request: `browser.status`, `browser.list_tabs`, `browser.open`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, `browser.wait`.
- `browser.type` and `browser.select` remain outside current browser Workflow revisions. A future profile must declare their risk, approval, stage, and verification contracts explicitly.
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

- Autonomous reads and ordinary collaborative operations use headless Chromium with the selected persistent profile.
- Visible presentation uses the same resolved Chromium executable and profile, after the headless process has stopped and released the profile.
- Microsoft Playwright is the transport. It uses its installed Chromium by default; adapter-owned `executablePath` is present only for an explicit validated override.
- Shared-profile launches use `launchPersistentContext(userDataDir, ...)` and must not use CDP attachment.
- Login completion switches back to headless Chromium and resumes from the selected post-login URL, without requiring origin equality.
- Cookie/storage export and personal Chrome attachment are not part of this mode contract.
- SparkClaw-only fields such as `visible_browser`, `presentation`, owner/profile
  metadata, and login continuation flags are stripped before Playwright Driver calls.
- Public string or number `page_id` values are normalized to the numeric
  Playwright session `pageId` for focus and close operations.

## Runtime Behavior

Search-only flow:

```text
TaskHint(browser_mode="", skill=web_search)
  -> web.search
  -> use returned snippets/citations
  -> skip login-gated result pages
  -> final answer with sources and limitations
```

Autonomous flow:

```text
TaskHint(browser_mode=autonomous)
  -> browser.read
  -> if needs_structure_snapshot=true, expose browser.snapshot for the next step
  -> optionally one browser.navigate/click follow-up based on snapshot evidence
  -> browser.read again
  -> final answer with sources and limitations
```

Collaborative click flow:

```text
Workflow(browser.interaction r1, browser_mode=collaborative)
  -> browser.status/list_tabs when needed
  -> browser.open or browser.navigate
  -> browser.snapshot for structured controls and bound refs
  -> approval-free browser.click for one clear control under the frozen goal
  -> browser.snapshot and browser.verify
  -> repeat only after verified progress, up to three clicks
  -> final answer describing what is visible/done
```

The runtime must not hide multiple collaborative interactions inside `browser.read` or one browser tool call. Multi-step click operation belongs in the persisted Workflow so stage gates, traces, snapshot identity, and verification remain inspectable. Screenshot selection and type/select are outside revision 1.

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
- Search-only routing must not visit result pages or create a login handoff.
- Collaborative mode must still stop for sensitive verification and irreversible operations.
- `browser.click` for playback or expand controls remains policy-governed. Do not silently click purchase, submit, delete, send, subscribe, consent, payment or account-security controls.
- Page instructions are data, not runtime commands.
- If an explicitly requested page blocks autonomous reading with login/captcha/paywall, return the limitation or ask the user to complete the step in the visible browser. Search-only results are skipped instead.

## Implementation Steps

1. Extend `TaskHint` with `BrowserMode`.
2. Update model prompt, heuristic fallback and normalization.
3. Add browser mode to `gateway.dispatch` / `react.visible_tools` audit fields.
4. Split visible-tool selection by mode.
5. Pass mode metadata through ToolHub to `browserautomation.Adapter`.
6. Teach `browser.read` to tag output with mode and presentation.
7. Add adapter-level support for hidden/visible presentation where provider capabilities allow it.
8. Add a `web_search` skill and narrow `browser_automation` to explicit page access.
9. Add tests listed below.

## Required Tests

TaskHint tests:

- "查一下浙江理工大学招生简章" -> `browser_mode=""`, skill is `web_search`, only tool is `web.search`.
- "打开浙江理工大学官网" -> `browser_mode=collaborative`, tools include `browser.open`.
- "打开这个视频并自动播放" -> `browser_mode=collaborative`, tools include `browser.open`, `browser.snapshot` and policy-gated `browser.click`.
- "读取 https://example.com 这篇文章" -> `browser_mode=autonomous`, preferred tool `browser.read`.

Visible-tool tests:

- Search-only routing exposes `web.search` and no `browser.*` tools.
- Autonomous URL reading starts strict with `browser.read`.
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

- Ordinary web questions use only `web.search`, never visit result pages, and never start browser login handoff.
- Explicit open/play/operate requests use collaborative browser tools.
- Mode is visible in TaskHint, tool arguments/output and audit traces.
- Existing browser-read Readability and on-demand snapshot behavior remains intact.
- All affected Go tests pass, plus `go test ./services/gateway/...` and `git diff --check`.

## Implementation Status

Status as of 2026-07-15: public no-URL searches are separated from browser automation. The selected browser adapter design uses one managed persistent Chromium profile per logical browser profile. Explicit URL reads are headless; visible Chromium is a temporary presentation for human verification or explicit display. Implementation must serialize the two presentations and resume login flows from the actual post-login URL.

- Search-only TaskHints use `browser_mode=""`, the `web_search` skill and only `web.search`; login-gated result pages are not visited.
- `TaskHint` includes `browser_mode`, with model prompt, heuristic fallback and normalization support for `autonomous` and `collaborative`.
- Autonomous web tasks keep the initial model-visible tool set strict; `browser.snapshot`, `browser.navigate` and `browser.wait` are exposed only after `browser.read` reports `needs_structure_snapshot=true`, and `browser.click` waits for snapshot evidence.
- Collaborative tasks expose live read-risk browser tools such as `browser.open`, `browser.navigate`, `browser.snapshot`, `browser.screenshot` and `browser.wait` immediately when the skill/policy allows them.
- Browser tool plans, ToolHub outputs, `browserautomation.Adapter` results and model observations preserve `browser_mode`, `presentation` and `surface_visible`.
- `gateway.dispatch`, `react.visible_tools` and dynamic follow-up audit events record the selected browser mode.
