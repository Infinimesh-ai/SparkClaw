# Hidden Chromium Browser Access Plan

> Language: English | [简体中文](../zh-cn/docs/browser-hidden-chromium-access.md)

This document defines hidden browser execution. Profile ownership and visible
login transitions are specified in
[Managed Shared Chromium Profile](managed-persistent-browser-profile.md).

## Decision

SparkClaw uses Chromium for every managed browser surface:

- ordinary browsing runs in headless Chromium;
- login and human verification temporarily run in visible Chromium;
- both presentations use the same persistent profile for the selected logical
  browser profile;
- personal Chrome attachment is not supported;
- Cookie/storage export is not used.

## Goals

- Render JavaScript pages without showing a browser window.
- Preserve Chromium-managed login state across visible and hidden transitions.
- Keep normal search, reading, snapshots, navigation, and safe interaction
  hidden.
- Show Chromium only when human intervention is required or explicitly
  requested.
- Keep browser lifecycle and profile state owned by Gateway.

## Provider Contract

The existing Chrome DevTools MCP package remains the automation transport, but
the launched browser executable is configured Chromium. Provider names and
diagnostics should describe Chromium, not imply that SparkClaw attached to the
user's Chrome profile.

Hidden launch requirements:

```text
--executablePath=<Chromium executable>
--userDataDir=<SparkClaw shared profile>
--headless
--viewport=1365x768
--no-usage-statistics
```

The hidden shared-profile provider must reject `--isolated`, user-supplied
profile paths, browser endpoints, and automatic Chrome attachment flags.

## Read Path

```text
browser.read
  -> start or reuse headless Chromium for the selected profile
  -> new_page target URL
  -> wait for render state
  -> evaluate_script for DOM/HTML/text diagnostics
  -> run Readability outside Chromium
  -> return content and structure diagnostics
```

`browser.snapshot` and safe follow-up tools use the same active headless
session. They do not launch a second isolated browser.

## Authentication Transition

When hidden Chromium detects a login or verification wall:

1. Create a `BrowserLoginBlock`.
2. Stop headless Chromium and release the profile.
3. Start visible Chromium with the same executable and profile.
4. Reuse or open the handoff page.
5. Wait for the user to complete the human-only step.
6. Capture the selected post-login URL.
7. Stop visible Chromium.
8. Restart headless Chromium with the same profile.
9. Read and verify the post-login URL.

The runtime does not compare the post-login origin with the original origin.

## Visibility Rules

Hidden is the default for:

- public and authenticated reads;
- search result inspection;
- snapshots and DOM structure inspection;
- navigation and safe read-only interactions;
- continuing a task after login verification.

Visible Chromium is reserved for:

- password entry;
- captcha, SMS, 2FA, passkey, permission, consent, and payment confirmation;
- sites that reject headless execution after authentication;
- an explicit user request to see the browser surface.

## Lifecycle

- One profile has one active Chromium/MCP process.
- Switching visible/hidden closes the active process before starting another.
- `Close()` shuts down the current process on Gateway shutdown.
- Process startup and shutdown are bounded by timeouts.
- Chromium profile locks are respected and never force-deleted.
- The profile directory is never copied while live.

## Output Fields

Browser output should preserve:

- `browser_mode`
- `presentation`
- `surface_visible`
- `browser_provider`
- `browser_profile_id`
- `browser_actions`
- `read_mode`
- `auth_challenge_detected`
- `needs_structure_snapshot`
- `final_url`

Profile filesystem paths and credential material are never model-visible.

## Failure Semantics

- Chromium executable missing;
- shared profile path invalid or busy;
- hidden Chromium start failed;
- visible-to-hidden switch failed;
- page read failed;
- authentication wall remains after the user reports completion;
- site rejects headless execution.

These conditions must remain distinguishable in tool output and audit events.

## Acceptance Tests

- Hidden and visible sessions use the same Chromium executable and profile.
- Hidden launch is headless and not isolated.
- Hidden and visible sessions never overlap for one profile.
- Hidden reads can reuse login state created in visible Chromium.
- Cross-origin SSO resumes from the post-login URL.
- No Cookie/storage export or injection occurs.
- Ordinary reads do not show a browser window.
- Human-only verification reliably switches to visible Chromium.
