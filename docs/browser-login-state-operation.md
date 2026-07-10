# Browser Login State Operation Guide

> Language: English | [简体中文](../zh-cn/docs/browser-login-state-operation.md)

This document defines how SparkClaw should handle authenticated browser pages
across autonomous and collaborative browser modes. It complements
[Browser Automation Improvement Plan](browser-automation-improvement.md),
[Browser Modes Coding Guide](browser-modes-coding-guide.md) and
[Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md).

## Status

Status as of 2026-07-10: SparkClaw persists scoped browser auth records and can
open a visible handoff page when autonomous reads hit a login wall. The runtime
must also record the unfinished task as a dedicated browser-login block; a login
wall is not a completed answer and must not depend on ordinary prompt context to
resume. Provider support is limited to browser-visible cookie/storage state;
password, captcha, SMS, 2FA and payment confirmation still stop for the user.
The target behavior should match the OpenClaw-style handoff contract: after the
user says login succeeded, the agent resumes the interrupted browser task from
the authenticated browser state instead of asking for the goal again.

## Product Contract

All browser tools must treat login state consistently. The same rules apply to
`browser.read`, `browser.snapshot`, `browser.navigate`, `browser.open`,
`browser.click`, `browser.wait` and any future browser tool that reaches a page
behind an authenticated session.

Autonomous mode should keep the browser surface hidden for ordinary information
tasks. When it detects that a page needs login:

1. If this is the first login for the current owner, browser profile and site,
   SparkClaw opens a visible login handoff page, records a `BrowserLoginBlock`
   for the unfinished run, and asks the user to finish login in the browser.
   After the user's next reply, SparkClaw verifies the current visible browser
   state, exports the resulting cookie/storage state, persists it when policy
   allows it, then closes or hides the handoff surface and continues the
   original task in the hidden browser session.
2. If a valid auth record already exists in the database for the same owner,
   browser profile, site and account, SparkClaw restores or refreshes that state
   without showing a window, verifies that the page is authenticated, then
   continues hidden.
3. If the stored auth record is missing, expired, rejected, or requires
   password, captcha, SMS code, 2FA, permission grant, payment confirmation or
   another human-only step, SparkClaw falls back to the first-time visible
   handoff flow.

Collaborative mode uses the already visible browser surface. It does not need
the autonomous pop-up/hide cycle. When login is required, the user completes it
in the visible browser, then SparkClaw continues from that page state. It may
still save or reuse the same database auth records when policy allows, but it
must not surprise the user by switching surfaces.

## Identity And Profile Keys

SparkClaw is multi-user and multi-profile. Browser auth data must be scoped by
all of these keys:

| Key | Meaning |
|---|---|
| `owner_id` | SparkClaw owner profile ID, from `OwnerProfile`. |
| `browser_profile_id` | Logical browser profile such as `default`, `work`, `school` or `personal`. |
| `site_origin` | Normalized origin, for example `https://example.com`. |
| `site_realm` | Optional finer-grained login realm when one origin hosts multiple apps. |
| `account_hint` | Optional non-secret account label, such as email domain or masked username. |
| `auth_strategy` | `session_restore`, `credential_login`, `sso_handoff`, or provider-specific strategy. |

Records must never be shared across owners or logical browser profiles unless
the user explicitly links those profiles. A failed lookup for `owner_a/work`
must not fall back to `owner_a/personal` or `owner_b/work`.

The runtime should derive `owner_id` from the current session/client binding and
derive `browser_profile_id` from explicit tool arguments, owner preferences, or
`tools.browserAutomation.profile`, falling back to `default`.

## Persistent Auth Record

The store should add one typed auth record and implement it in memory, file and
Postgres backends at the same time:

```go
type BrowserAuthRecord struct {
    ID               string     `json:"id"`
    OwnerID          string     `json:"owner_id"`
    BrowserProfileID string     `json:"browser_profile_id"`
    SiteOrigin       string     `json:"site_origin"`
    SiteRealm        string     `json:"site_realm,omitempty"`
    AccountHint      string     `json:"account_hint,omitempty"`
    AuthStrategy     string     `json:"auth_strategy"`
    Status           string     `json:"status"` // active, expired, revoked, failed
    SessionRef       string     `json:"session_ref,omitempty"`
    CredentialRef    string     `json:"credential_ref,omitempty"`
    CookieJarRef     string     `json:"cookie_jar_ref,omitempty"`
    LastVerifiedAt   time.Time  `json:"last_verified_at,omitempty"`
    ExpiresAt        *time.Time `json:"expires_at,omitempty"`
    LastError        string     `json:"last_error,omitempty"`
    CreatedAt        time.Time  `json:"created_at"`
    UpdatedAt        time.Time  `json:"updated_at"`
    RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}
```

Secret values such as passwords, cookies, refresh tokens and exported storage
state must live behind encrypted `CredentialSecret` or artifact references, not
in tool arguments, traces, audit metadata or plain JSON summaries. The file
backend must respect the existing state encryption configuration.

When a user completes a visible login handoff, the browser provider should
export an origin-scoped state bundle containing only reusable browser state:

- cookies visible to the provider for the target origin and compatible domain
  scope;
- `localStorage` and `sessionStorage` for the target origin when available;
- provider/session metadata needed to re-import the state into the configured
  hidden or collaborative browser profile.

The exported bundle must be saved through `CredentialSecret` or the artifact
store and referenced by `CredentialRef`/`CookieJarRef`; the plaintext cookie
string must never appear in traces, model-visible observations, audit fields or
chat content. A record becomes `active` only after SparkClaw reuses the exported
state and verifies that the destination page is no longer on the login wall.
Future visits to the same `owner_id` + `browser_profile_id` + `site_origin` +
`site_realm` + `account_hint` must try this stored state before asking the user
to log in again.

## Browser Login Block Record

`BrowserAuthRecord` only describes reusable authenticated state. It must not be
used as the source of truth for an unfinished task that is waiting for the user
to complete login. Autonomous login handoff requires a separate typed block:

```go
type BrowserLoginBlock struct {
    ID                string         `json:"id"`
    SessionID         string         `json:"session_id"`
    RunID             string         `json:"run_id"`
    Status            string         `json:"status"` // waiting, resuming, resolved, canceled, failed
    OriginalGoal      string         `json:"original_goal"`
    ResumeTool        string         `json:"resume_tool"` // usually browser.read
    ResumeArgs        map[string]any `json:"resume_args"`
    LastToolCallID    string         `json:"last_tool_call_id,omitempty"`
    LoginHandoffURL   string         `json:"login_handoff_url,omitempty"`
    LoginHandoffPageID string         `json:"login_handoff_page_id,omitempty"`
    LastVisiblePageID  string         `json:"last_visible_page_id,omitempty"`
    OwnerID           string         `json:"owner_id"`
    BrowserProfileID  string         `json:"browser_profile_id"`
    SiteOrigin        string         `json:"site_origin"`
    SiteRealm         string         `json:"site_realm,omitempty"`
    AccountHint       string         `json:"account_hint,omitempty"`
    BrowserAuthStatus string         `json:"browser_auth_status,omitempty"`
    ResolvedAuthRecordID string       `json:"resolved_auth_record_id,omitempty"`
    LastUserReply     string         `json:"last_user_reply,omitempty"`
    LastError         string         `json:"last_error,omitempty"`
    CreatedAt         time.Time      `json:"created_at"`
    UpdatedAt         time.Time      `json:"updated_at"`
    ResolvedAt        *time.Time     `json:"resolved_at,omitempty"`
}
```

An active block is part of runtime state, not extra prompt context. The owning
run should move to `browser_login_blocked` with no `completed_at` timestamp,
and the block should keep the original goal, tool name, tool arguments and all
owner/profile/site keys required to resume. When the browser provider exposes a
stable page identifier, the block should also store the visible login handoff
page id so the resume path can inspect and continue from the same authenticated
tab. The next user message in the same session is routed to this block before
normal TaskHint generation.

OpenClaw-style continuation is a hard requirement: while the block is active,
the user's next reply is a signal or correction for the blocked run, not a new
goal. A reply such as "login completed" or "登录成功" must never produce an
assistant answer that asks what the user wants to do next if `OriginalGoal` is
available.

## Autonomous Flow

```text
browser tool detects auth challenge
  -> normalize owner_id + browser_profile_id + site_origin + optional realm
  -> lookup BrowserAuthRecord
  -> if active record exists:
       restore session/credential in hidden provider
       verify authenticated DOM state
       continue hidden browser operation
  -> if no usable record exists:
       open visible login handoff window/page
       save BrowserLoginBlock with original run, goal, tool and args
       set original run state to browser_login_blocked
       wait for the user's next message
```

`BrowserLoginBlock` creation is keyed by the auth handoff fields in the tool
result, not by a single tool name. `browser.read` is the common path, but a
visible browser tool such as `browser.open`, `browser.navigate` or
`browser.snapshot` must also create the block if its result reports
`browser_auth_status=handoff_waiting|handoff_required`,
`login_handoff_opened=true`, or
`auth_challenge_detected=true` with `login_handoff_required=true`.
Visible tool observations must also detect common human login blockers such as
password forms, captcha/verification-code pages, SSO/VPN/login prompts and
provider-specific "login required" pages, then normalize them into the same
auth handoff fields so `BrowserLoginBlock` is created.

The visible handoff is only for the login step. Once authentication is verified,
the final page read, snapshot or navigation continues with
`browser_mode=autonomous`, `presentation=hidden` and `surface_visible=false`.

If login succeeds but the hidden provider cannot import or reuse the session,
return an explicit `hidden_auth_restore_failed` error and keep the user-visible
handoff page open rather than pretending the hidden task succeeded.

## Resume After User Reply

When a session has an active `BrowserLoginBlock`, the next user message is a
block response, not a new unrelated task. The runtime should classify it with
simple intent rules before normal planning:

- Login completed: phrases such as "done", "logged in", "I signed in",
  "登录完成", "已登录", "登好了" or "好了".
- Wrong page: phrases such as "wrong page", "not this page", "页面错了",
  "不是这个页面", "你打开错了" or a corrected URL.
- Ambiguous: any other reply while a block is active.

For all three cases, first inspect the current visible browser state with
`browser.list_tabs` or an equivalent provider call. Then:

1. If the user says the page is wrong and provides a URL, update the block's
   resume URL and open that URL visibly, then keep the block waiting.
2. If the user says the page is wrong without a URL, reopen the stored
   `login_handoff_url` or original resume URL visibly, then keep the block
   waiting and explain what page was reopened.
3. If the user says login completed or the reply is ambiguous, try the stored
   resume operation. First bind to the visible handoff tab using
   `LoginHandoffPageID` or the best matching current tab, verify that the page is
   past the login wall, and export cookie/storage state from that visible
   browser context.
4. Save/update `BrowserAuthRecord` from the exported cookie/storage state, then
   verify reuse by importing it into the intended hidden or profile-scoped
   browser context. The retry should include `login_handoff_completed=true`,
   `persist_browser_auth=true`, `browser_mode=autonomous`,
   `presentation=hidden` and `surface_visible=false` when hidden reuse is
   supported.
5. If authenticated state is verified, mark the block `resolved`, store
   `resolved_auth_record_id`, close or hide the disposable handoff surface when
   supported, and continue the original ReAct run using the original goal,
   original tool history and the new browser observation. If hidden import fails
   but the visible tab is authenticated, continue in the visible tab or keep the
   block waiting with an explicit `hidden_auth_restore_failed`; do not reopen the
   site in a fresh tab and do not ask the user to restate the goal.
6. If there are no visible tabs, the provider cannot export auth state, or the
   resumed read still lands on a login wall, keep the block `waiting`, update
   `last_error`, and use the user's reply plus the original goal/resume URL to
   either reopen the handoff page or ask for the missing human step.

The previous task and progress must not be dropped. The resumed run uses the
original `run_id`, `original_goal`, previous tool observations, and stored
`resume_args`; the user's reply is only the signal or correction that unblocks
the task.

## Collaborative Flow

```text
browser tool detects auth challenge
  -> keep current visible surface
  -> ask the user to complete login in the browser
  -> when the user reports success, inspect the same visible tab first
  -> export and persist origin-scoped cookie/storage state when policy allows
  -> verify authenticated DOM state
  -> optionally persist/update BrowserAuthRecord
  -> continue with collaborative tools on the same visible page/session
```

Collaborative mode does not open a separate login pop-up unless the user asks
for a new window. It also does not hide or close the browser after login unless
the user asks or the page was created as a disposable task tab.
When the user restates the original goal after login, TaskHint/ReAct should
prefer `browser.list_tabs`, `browser.focus` and `browser.snapshot` on the
authenticated visible tab. It should not call `browser.open` for the same origin
unless the user explicitly requests a new page or the existing tab is gone.

## Detection And Output Fields

Every browser observation that may involve auth should include these fields when
available:

- `auth_challenge_detected`
- `auth_challenge_kind`
- `auth_site_origin`
- `auth_site_realm`
- `browser_auth_status`: `none`, `challenge`, `restored`, `handoff_required`,
  `handoff_waiting`, `verified`, `expired`, `failed`
- `browser_auth_record_id`
- `browser_profile_id`
- `owner_id`
- `login_surface`: `hidden`, `visible_handoff`, `collaborative_visible`
- `login_handoff_required`

Auth detection should use page evidence such as password fields, login labels,
HTTP redirects, blocked article text, account menu presence and provider-specific
signals. It must not infer that private content exists when the page only shows
a login wall.

## Security Rules

- Do not ask the user to paste passwords, SMS codes or 2FA codes into chat.
- Do not automate captcha, SMS code, 2FA, payment confirmation, consent,
  purchase, account security or password-change steps.
- Captcha, SMS, 2FA and similar human-verification pages should remain open in
  the visible browser. SparkClaw may wait for the user's "login completed"
  signal and then verify the resulting state, but it must not solve, bypass or
  request the verification secret in chat.
- Do not store raw passwords or cookies in traces, audit events or model-visible
  observations.
- Do not reuse an auth record across owners, profiles, origins or account hints
  without explicit user action.
- Mark records expired or failed when a restore lands on a login wall.
- Provide a way to revoke a browser auth record and delete its secret refs.

## Audit Events

The gateway should audit these transitions without secret values:

- `browser_auth.challenge_detected`
- `browser_auth.record_lookup`
- `browser_auth.restore_attempted`
- `browser_auth.restore_succeeded`
- `browser_auth.restore_failed`
- `browser_auth.handoff_started`
- `browser_auth.handoff_verified`
- `browser_auth.record_saved`
- `browser_auth.record_revoked`
- `browser_login_block.created`
- `browser_login_block.resume_requested`
- `browser_login_block.current_tabs_checked`
- `browser_login_block.reopened`
- `browser_login_block.resolved`
- `browser_login_block.still_waiting`

Each event should include `owner_id`, `browser_profile_id`, `site_origin`,
`site_realm`, `account_hint` when known, selected `browser_mode`, selected
provider and failure reason when applicable.

## Failure Semantics

Use explicit tool errors or output status values:

- `browser_auth_record_missing`
- `browser_auth_record_expired`
- `browser_auth_restore_failed`
- `browser_auth_handoff_required`
- `browser_auth_handoff_timeout`
- `browser_auth_handoff_canceled`
- `browser_auth_verification_failed`
- `browser_auth_profile_mismatch`
- `browser_login_block_missing_tab`
- `browser_login_block_still_unauthenticated`

Autonomous mode should repair `missing`, `expired` and `restore_failed` by
starting visible handoff when policy allows. Collaborative mode should report
the page state and wait for the user to complete login in the visible browser.

## Acceptance Tests

Minimum tests for implementation:

- Autonomous first-time login detects a login wall, opens a visible handoff,
  creates a `BrowserLoginBlock`, marks the original run
  `browser_login_blocked`, and does not claim the task is complete.
- A follow-up "login completed" message first checks current tabs, saves a
  record after verified login, closes or hides the handoff surface when
  supported, then resumes the original run and continues the original read
  hidden.
- A follow-up "login completed" message preserves `OriginalGoal`; it must not
  ask the user what to do next when the blocked goal is known.
- After a successful human login, the exported cookie/storage state is persisted
  as an encrypted secret/artifact reference and subsequent same-scope visits
  reuse it without asking the user to log in again.
- If a captcha/SMS/2FA page appears, SparkClaw leaves it to the user in the
  visible browser and resumes only after the user reports completion.
- If the user restates the goal after login and an authenticated tab is still
  open, SparkClaw focuses/snapshots that tab instead of opening a new tab for
  the same origin.
- A follow-up "wrong page" message reopens or corrects the handoff URL while
  preserving the original blocked task and progress.
- If no visible tab exists or the page is still unauthenticated, the active
  block remains waiting and the next answer explains the exact missing step.
- Autonomous repeated login for the same owner/profile/site restores from the
  stored record and never opens a visible surface.
- Expired or rejected stored state is marked failed/expired and falls back to
  visible handoff.
- Collaborative login stays on the visible page and does not perform the
  autonomous pop-up/hide cycle.
- Records for different owners and browser profiles cannot be cross-used.
- Store interface changes are implemented in memory, file and Postgres backends,
  with snapshot/file encryption coverage.
- Audit events contain auth transition metadata but no secret values.
