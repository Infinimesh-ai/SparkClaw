# Browser Login State Operation Guide

> Language: English | [简体中文](../zh-cn/docs/browser-login-state-operation.md)

This document defines how SparkClaw pauses browser tasks for human login and
resumes them with the same managed Chromium profile. The profile lifecycle is
specified in [Managed Shared Chromium Profile](managed-persistent-browser-profile.md).

## Status

Status as of 2026-07-10: managed shared Chromium profiles are the selected
login-state strategy. Cookie/storage export, personal Chrome attachment, and
pre-login/post-login origin equality are legacy behavior and must not be used
for new login continuations.

## Product Contract

- Browser tasks run in headless Chromium by default.
- A login, captcha, SMS, 2FA, permission, payment, or other human-only step
  creates a `BrowserLoginBlock` and switches the same profile to visible
  Chromium.
- The user's next message in the same session resumes the blocked run; it is not
  treated as a new goal.
- After login, SparkClaw uses the selected visible page's current URL as the
  resume target.
- The authenticated page may be on a different origin from the original page.
- SparkClaw switches the same profile back to headless Chromium before ordinary
  automated work continues.
- Browser credentials remain inside the Chromium profile. No cookie or storage
  secret is exported, injected, or stored by Gateway.

## Browser Login Block

The unfinished task remains durable runtime state:

```go
type BrowserLoginBlock struct {
    ID                  string         `json:"id"`
    SessionID           string         `json:"session_id"`
    RunID               string         `json:"run_id"`
    Status              string         `json:"status"`
    OriginalGoal        string         `json:"original_goal"`
    ResumeTool          string         `json:"resume_tool"`
    ResumeArgs          map[string]any `json:"resume_args"`
    LoginHandoffURL     string         `json:"login_handoff_url,omitempty"`
    LoginHandoffPageID  string         `json:"login_handoff_page_id,omitempty"`
    LastVisiblePageID   string         `json:"last_visible_page_id,omitempty"`
    OwnerID             string         `json:"owner_id"`
    BrowserProfileID    string         `json:"browser_profile_id"`
    SiteOrigin          string         `json:"site_origin"`
    LastUserReply       string         `json:"last_user_reply,omitempty"`
    LastError           string         `json:"last_error,omitempty"`
    CreatedAt           time.Time      `json:"created_at"`
    UpdatedAt           time.Time      `json:"updated_at"`
    ResolvedAt          *time.Time     `json:"resolved_at,omitempty"`
}
```

`OriginalGoal` and the original URL remain unchanged for task context.
`LoginHandoffURL`, `ResumeArgs.url`, and `SiteOrigin` may be updated to the
actual authenticated page after login.

## First Login Or Expired Login

```text
headless Chromium reaches an auth/verification wall
  -> create BrowserLoginBlock
  -> keep the original run in browser_login_blocked
  -> stop headless Chromium
  -> start visible Chromium with the same profile
  -> open or reuse the handoff page
  -> ask the user to complete the human-only step
```

The runtime must not mark the original run complete and must not ask the user to
repeat the goal.

## Resume After User Confirmation

When the user replies with text such as `login completed`, `logged in`,
`登录完成`, `登录成功`, or `已经登录成功`:

1. Keep the original run and block.
2. List visible tabs from the currently active visible Chromium session.
3. Prefer the page recorded by this login block's handoff/last-visible page ID;
   if it is no longer present, use the current tab marked by the provider as
   selected.
4. Read its current URL and ignore `about:blank` or internal browser pages.
5. Update the block and resume arguments to the selected post-login URL.
6. Stop visible Chromium and wait for the shared profile to be released.
7. Start headless Chromium with the same profile.
8. Read the post-login URL.
9. If the page is authenticated, resolve the block and continue the original
   ReAct run.
10. If a login or verification wall remains, return the profile to visible mode
    and keep the block waiting.

The runtime must reuse the existing selected handoff tab. It must not open a
second visible page merely because the pre-login and post-login origins differ.
Another task's selected tab must not replace a login block's still-open handoff
page when several conversations share the same browser profile.

Authentication verification must prefer page state over keyword matching. A
visible password field, captcha/2FA control, or explicit `please sign in`
prompt is challenge evidence. `sign out`, `log out`, `退出登录`, or a usable
authenticated application body is authenticated evidence. A navigation item
such as `activation requires VPN login` must not reopen the login block.

### Layered Authentication Assessment

Login state is a progressive assessment, not a one-shot string decision. The
runtime records `unknown`, `challenged`, or `authenticated` with confidence and
evidence signals. It follows the same operating principles as OpenClaw's
browser automation loop: keep a stable profile/tab, inspect the visible UI
before acting, snapshot again after page changes, avoid blind waits, and report
only real human blockers.

Evidence priority is:

1. Structured provider state, including `profile_verified` or an explicit auth
   challenge.
2. Visible controls and explicit instructions, including password/captcha/2FA
   controls or sign-out/account controls.
3. Compound page evidence, such as a restricted-resource message plus a clear
   VPN/login instruction.
4. Generic page text and navigation labels, which remain insufficient alone.

The browser provider must expose its page-level assessment as structured
`state`, `confidence`, and `signals`; `auth_challenge_detected` remains only a
compatibility projection of `state=challenged`. Provider assessment is layered:

1. **Explicit challenge controls**: a visible captcha/OTP control, or a visible
   credential control inside a login-context form/dialog with a matching login
   action. A password input by itself is not a login wall because authenticated
   applications may contain folder unlock, payment confirmation, account
   settings, or credential-management controls.
2. **Explicit authenticated controls**: a visible sign-out control, account
   identity control, or another authenticated-only application control.
3. **Authenticated application continuity**: after a human login confirmation,
   the same managed profile reaches a usable non-login application route with a
   substantial visible application shell and no explicit challenge. This is
   positive evidence, not merely the absence of login keywords. The provider
   reports the application-shell signal; ToolHub combines it with the confirmed
   shared-profile transition before upgrading the result to `authenticated`.
4. **Text fallback**: explicit page-level login instructions may support a
   challenge only when they come from visible page text. Text from `script`,
   `style`, `template`, hidden nodes, metadata, or unrelated navigation is not
   authentication evidence.

The provider must evaluate contextual combinations instead of independent
keywords. A login-looking route or title strengthens visible login controls but
does not decide the state alone. Likewise, a generic application shell without
profile continuity or authenticated controls is not enough to claim a logged-in
account. Strong challenge and authenticated signals on the same page produce
`unknown` with conflict signals and trigger a fresh snapshot/read; they must not
be collapsed into a Boolean result.

These rules are domain-neutral. Site hostnames, mailbox brands, school names,
and account-specific cookie names must not be used as production auth rules.
Site-shaped fixtures are appropriate for tests, but the implementation must
derive the result from reusable DOM, route, profile-continuity, and control
evidence.

Conflicting evidence produces `unknown`; it does not silently choose login or
authenticated. Resume confirmation verifies the selected handoff tab and then
performs a profile-backed page read. Only an `authenticated` assessment resolves
the block. `unknown` remains waiting with an inconclusive-evidence reason, while
`challenged` remains waiting with the observed challenge evidence. Lower-
priority text must never override structured provider verification.

## Post-Login Domain Semantics

Origin equality is not an authentication requirement.

For example:

```text
original request: https://s.example.edu
visible login:    https://vpn.example.edu
authenticated:    https://sso-app.vpn.example.edu/home
resume target:    https://sso-app.vpn.example.edu/home
```

The final selected URL becomes the operational target. The original URL remains
available in `OriginalGoal` and historical tool calls.

The runtime should reject only unsafe or unusable targets, such as empty URLs,
`about:blank`, browser-internal schemes, or URLs blocked by browser host policy.

## Profile State Instead Of Cookie Export

Managed shared profiles do not use browser auth payloads:

- do not call `document.cookie` to export authentication;
- do not call `ExportAuthState` or `ImportAuthState`;
- do not create a browser-auth `CredentialSecret`;
- do not compare exported origin with the original site origin;
- do not recreate cookies with JavaScript.

This preserves HttpOnly cookies, cookie attributes, IndexedDB, service workers,
and cross-origin SSO state because Chromium itself owns the complete profile.

## Existing Login State

For later tasks, headless Chromium opens the requested page with the persistent
profile:

- authenticated page: continue hidden;
- expired login or new verification wall: create/reopen a login block and
  switch the profile to visible Chromium;
- site rejects headless execution after successful login: keep the task in
  visible collaborative mode and report that limitation explicitly.

## Wrong Page And Missing Tab

- `wrong page` or `页面不对`: keep the block waiting and navigate the existing
  visible tab to the corrected URL when possible.
- no selected usable tab: keep the block waiting with
  `browser_login_block_missing_tab`.
- selected page is still an auth wall: keep the block waiting with
  `browser_login_block_still_unauthenticated`.
- selected page has conflicting or insufficient evidence: keep the block
  waiting with `browser_login_auth_evidence_inconclusive` and capture the
  provider signals instead of asking the user to repeat login immediately.
- profile transition fails: keep the block waiting and report the profile
  lifecycle error separately from website authentication.

## Audit Events

Audit login lifecycle without secret values:

- `browser_login_block.created`
- `browser_login_block.resume_requested`
- `browser_login_block.current_tabs_checked`
- `browser_login_block.post_login_target_selected`
- `browser_profile.switch_visible`
- `browser_profile.switch_hidden`
- `browser_login_block.resolved`
- `browser_login_block.reopened`
- `browser_login_block.still_waiting`

Include owner/profile identifiers, presentation, selected page id when
available, original site origin, post-login site origin, and failure reason.
Never include cookies, tokens, storage values, or the raw profile path.

## Failure Semantics

- `browser_login_block_missing_tab`
- `browser_login_block_still_unauthenticated`
- `browser_login_post_login_url_missing`
- `browser_shared_profile_busy`
- `browser_shared_profile_start_failed`
- `browser_shared_profile_stop_timeout`
- `browser_shared_profile_visible_verification_failed`
- `browser_shared_profile_hidden_verification_failed`

## Acceptance Tests

- A login wall creates a durable block and opens visible Chromium with the same
  profile previously used headlessly.
- `登录成功` is recognized as a completion signal.
- Login completion reuses the selected visible tab.
- A cross-origin WebVPN/SSO redirect updates the resume URL and does not fail on
  origin mismatch.
- Visible Chromium stops before headless Chromium starts.
- Headless Chromium reuses the profile login state and continues the original
  run.
- A QQ-Mail-shaped SPA fixture with persisted profile state, a usable mailbox
  application shell, and unrelated password/login strings in hidden or style
  content remains authenticated.
- A visible folder-unlock/account-settings password field without a login form
  does not create a login block.
- A real login form with a visible password field, explicit login action, and
  login-context text remains challenged.
- Conflicting strong challenge and authenticated controls return `unknown` and
  preserve both signal sets for audit.
- Duplicate visible handoff tabs are not created.
- No browser auth `CredentialSecret` is created.
- Captcha, SMS, 2FA, permission, and payment steps remain user-controlled.
- The original goal survives all profile transitions.
