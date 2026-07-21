# Managed Shared Chromium Profile

> Language: English | [简体中文](../zh-cn/docs/managed-persistent-browser-profile.md)

This document is the source of truth for SparkClaw browser profile ownership,
browser visibility, and authenticated-session reuse. SparkClaw uses Chromium
for both hidden automation and visible human verification. It does not attach
to the owner's daily Chrome profile and it does not copy browser credentials
between browser processes.

Related documents:

- [Browser Login State Operation Guide](browser-login-state-operation.md)
- [Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md)
- [Browser Modes Coding Guide](browser-modes-coding-guide.md)

## Decision

Status as of 2026-07-10: this design supersedes personal Chrome attachment and
JavaScript cookie/storage export.

For each owner and logical `browser_profile_id`, SparkClaw owns one persistent
Chromium user data directory. The same directory is opened in one of two
mutually exclusive presentations:

| Presentation | Chromium process | Window | Use |
|---|---|---|---|
| `hidden` | headless Chromium | none | normal search, reading, navigation, and authenticated automation |
| `visible` | headed Chromium | visible | password, captcha, SMS, 2FA, permission, payment, or other human-only verification |

The visible and hidden processes never own the profile concurrently. Switching
presentation means stopping the current Playwright driver and Chromium context,
waiting for them to release the profile, and starting Chromium again with the
same profile.

## Product Contract

1. Both presentations launch the same Playwright-managed Chromium revision, or
   the same explicitly configured custom Chromium executable.
2. Both presentations use the same SparkClaw-owned persistent profile for the
   selected owner and logical browser profile.
3. Normal browser work stays hidden. SparkClaw shows Chromium only when a human
   must complete a verification step or when the user explicitly asks to see
   the browser surface.
4. Chromium remains the source of truth for cookies, including HttpOnly
   cookies, storage, IndexedDB, service workers, and browser-managed session
   state.
5. SparkClaw does not export `document.cookie`, inject cookies, or save browser
   authentication in `CredentialSecret`.
6. A profile switch is serialized. Starting a visible process first stops the
   hidden process; returning to hidden first stops the visible process.
7. Authentication is verified by reading the resulting page, not by comparing
   its origin with the pre-login origin.

This contract supports SSO, WebVPN, and other flows where the authenticated
page is on a different origin from the page that initiated login.

## Profile Layout

Profiles are runtime-owned and derived from trusted identifiers:

```text
<profile-root>/
  <owner-id-hash>/
    <browser-profile-id-hash>/
      user-data/
```

Requirements:

- Never point the profile root at Chrome's or Chromium's daily user profile.
- Resolve the path through typed configuration and reject path traversal.
- Create profile directories with owner-only permissions where supported.
- Do not expose the real path in model-visible observations or chat.
- Do not archive, copy, or commit a live profile.
- Keep the resolved Chromium executable identical across visible and hidden launches.

The complete profile directory is sensitive local state even though SparkClaw
does not extract individual secrets from it.

## Launch Contract

The adapter owns browser resolution and Playwright's `userDataDir`; model/tool
arguments cannot override them. The default omits `executablePath` so
Playwright uses its installed, version-matched Chromium. An explicit
`chromiumExecutable` config sets one validated override for both presentations.

```text
hidden:
  chromium.launchPersistentContext(<resolved shared profile>, {
    headless: true,
    viewport: { width: 1365, height: 768 }
  })

visible verification:
  chromium.launchPersistentContext(<same resolved shared profile>, {
    headless: false,
    viewport: null
  })
```

When `chromiumExecutable` is explicitly configured, both option objects above
also contain the same validated `executablePath`.

Shared-profile launches must not use `connectOverCDP`, a remote-debugging port,
a WebSocket endpoint, or a user-supplied data directory. When a fresh context
contains only Playwright's initial `about:blank` page, the adapter reuses that
page for the first requested URL. An already-running context creates a new page
for an explicit new-tab request.

## Runtime Flow

### Normal Work

```text
select owner + browser_profile_id
  -> start headless Chromium with the persistent profile
  -> open/read/interact with pages
  -> keep the browser surface hidden
```

### Login Or Human Verification

```text
hidden Chromium detects a login or verification wall
  -> save BrowserLoginBlock and original run state
  -> stop hidden Chromium and release the profile
  -> start visible Chromium with the same profile
  -> open the handoff URL
  -> user completes the human-only step
  -> user replies that login is complete
  -> inspect the selected visible page and capture its actual URL
  -> use that post-login URL as the resume target
  -> stop visible Chromium and release the profile
  -> start headless Chromium with the same profile
  -> open and verify the post-login URL
  -> resume the original run
```

The resume target is the selected authenticated page after login. The runtime
must not require that this URL share an origin with the original URL.

## Post-Login URL Rules

- Prefer the selected visible tab's current URL after the user completes login.
- Ignore `about:blank` and internal browser pages.
- Update the login block's handoff URL, resume URL, and current site origin from
  the post-login page.
- Preserve the original goal and original URL separately for task context.
- If the selected page still shows a login/verification wall, keep the block
  waiting.
- Do not open another visible tab when the existing selected handoff tab is
  usable.
- Authentication detection uses visible password/verification controls and
  explicit login prompts. Generic words such as `login`, `需登录`, or
  `退出登录` inside authenticated content are not sufficient by themselves.
- Authenticated evidence such as `sign out`, `log out`, `退出登录`, or a usable
  authenticated application body prevents a text-only login false positive.

## Lifecycle Rules

- At most one Playwright driver and Chromium context may own a shared profile.
- Mode transitions run under the adapter lock and have bounded timeouts.
- Stopping a session waits for the Playwright driver and Chromium child to exit
  before starting the next presentation.
- Gateway shutdown closes whichever presentation is active.
- A locked profile returns an explicit busy/start error; SparkClaw never deletes
  Chromium lock files to force recovery.
- Separate logical browser profiles use separate directories and coordinators.

## Authentication State

The shared profile replaces browser auth export/import for this strategy.

- `ExportAuthState` and `ImportAuthState` are not part of the managed shared
  profile flow.
- No cookie/storage `CredentialSecret` is created.
- Existing exported auth records are legacy data and are not imported into the
  shared Chromium profile.
- Non-secret verification metadata may be retained for audit, but the profile
  directory remains the only source of reusable authentication state.

## Security Rules

- Passwords, captcha answers, SMS codes, 2FA, permission grants, payment, and
  account-security actions remain visible and human-controlled.
- Browser mutations retain existing policy and approval requirements.
- Profile reset or deletion requires approval and can run only while no
  Chromium process owns the profile.
- Profile files are excluded from ToolHub file access, artifacts, traces,
  static serving, and training-data collection.
- Full-disk encryption is the recommended protection for persistent browser
  state.

## Failure Semantics

- `browser_shared_profile_busy`
- `browser_shared_profile_start_failed`
- `browser_shared_profile_stop_timeout`
- `browser_shared_profile_path_invalid`
- `browser_shared_profile_chromium_missing`
- `browser_shared_profile_login_required`
- `browser_shared_profile_visible_verification_failed`
- `browser_shared_profile_hidden_verification_failed`

Profile lifecycle failures and website authentication failures must remain
distinct so the user receives the correct next step.

## Acceptance Tests

- Visible and hidden launches use the same Chromium executable and profile.
- Hidden launch includes `--headless` and excludes `--isolated`.
- Visible launch excludes `--headless` and does not start until hidden Chromium
  has stopped.
- Login completion uses the selected post-login URL even when its origin differs
  from the original URL.
- A WebVPN/SSO redirect does not produce an origin-mismatch error.
- The visible handoff tab is reused instead of opening duplicate tabs.
- Returning to hidden mode preserves Chromium-managed login state.
- No browser cookie/storage secret is created.
- Ordinary browser tasks remain hidden.
- Captcha, SMS, 2FA, payment, and similar steps cause a visible handoff.
