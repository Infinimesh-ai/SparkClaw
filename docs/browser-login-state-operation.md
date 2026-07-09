# Browser Login State Operation Guide

> Language: English | [简体中文](../zh-cn/docs/browser-login-state-operation.md)

This document defines how SparkClaw should handle authenticated browser pages
across autonomous and collaborative browser modes. It complements
[Browser Automation Improvement Plan](browser-automation-improvement.md),
[Browser Modes Coding Guide](browser-modes-coding-guide.md) and
[Hidden Chromium Browser Access Plan](browser-hidden-chromium-access.md).

## Status

Status as of 2026-07-09: this is the target operating contract for the next
login-state milestone. The browser mode split and hidden provider groundwork
exist, but persistent browser auth records and first-time login handoff are not
yet implemented.

## Product Contract

All browser tools must treat login state consistently. The same rules apply to
`browser.read`, `browser.snapshot`, `browser.navigate`, `browser.open`,
`browser.click`, `browser.wait` and any future browser tool that reaches a page
behind an authenticated session.

Autonomous mode should keep the browser surface hidden for ordinary information
tasks. When it detects that a page needs login:

1. If this is the first login for the current owner, browser profile and site,
   SparkClaw opens a visible login handoff page, lets the user complete login,
   persists the resulting auth state when policy allows it, then closes or hides
   the handoff surface and continues the original task in the hidden browser
   session.
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

The store should add one typed record and implement it in memory, file and
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
       wait for user-completed login signal
       verify authenticated DOM state
       persist BrowserAuthRecord and secret refs
       close or hide visible handoff surface
       continue original operation in hidden provider
```

The visible handoff is only for the login step. Once authentication is verified,
the final page read, snapshot or navigation continues with
`browser_mode=autonomous`, `presentation=hidden` and `surface_visible=false`.

If login succeeds but the hidden provider cannot import or reuse the session,
return an explicit `hidden_auth_restore_failed` error and keep the user-visible
handoff page open rather than pretending the hidden task succeeded.

## Collaborative Flow

```text
browser tool detects auth challenge
  -> keep current visible surface
  -> ask the user to complete login in the browser
  -> verify authenticated DOM state
  -> optionally persist/update BrowserAuthRecord
  -> continue with collaborative tools on the same visible page/session
```

Collaborative mode does not open a separate login pop-up unless the user asks
for a new window. It also does not hide or close the browser after login unless
the user asks or the page was created as a disposable task tab.

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

Autonomous mode should repair `missing`, `expired` and `restore_failed` by
starting visible handoff when policy allows. Collaborative mode should report
the page state and wait for the user to complete login in the visible browser.

## Acceptance Tests

Minimum tests for implementation:

- Autonomous first-time login detects a login wall, opens a visible handoff,
  saves a record after verified login, closes or hides the handoff surface, then
  continues the original read hidden.
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

