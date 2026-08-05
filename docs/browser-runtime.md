# Browser Runtime

> Language: English | [简体中文](../zh-cn/docs/browser-runtime.md)

This document is the current browser implementation and operating guide. It
replaces the Playwright migration, agent-browser migration, browser-mode,
profile, login, perception, interaction, and weather migration records.

## Current Architecture

SparkClaw uses pinned `agent-browser` with a resolved system Chromium as its
only browser execution backend. ToolHub and Workflow contracts remain
provider-neutral; `internal/browserautomation` owns process transport, protocol
validation, profile locking, deadlines, and conversion to typed observations.

```text
Workflow leaf
  -> stage-scoped browser ToolHub capability
  -> browserautomation adapter
  -> private agent-browser MCP process
  -> SparkClaw-owned Chromium profile
```

There is no Playwright fallback, Chrome DevTools MCP fallback, personal Chrome
attachment, cookie export, or second DOM perception engine.

## User-Visible Capabilities

| Capability | Current revision boundary |
|---|---|
| `browser.internet_search` r1 | Search public current information through `web.search`; it does not open source pages |
| `browser.weather` r1 | Query typed metric data through Infinimesh Info `POST /v1/info/weather` and render one card for one explicit location |
| `browser.automation` r2 | Acquire an explicit URL, registered destination, or Info-identified named public target, validate it in hidden Chromium, then present and independently verify it in visible Chromium |
| `browser.page_read` r1 | Run a fixed hidden health -> open -> session-required read chain and return bounded content from one explicit or Info-identified URL |
| `browser.interaction` r2 | Use the managed acquisition and presentation chain around at most three bounded, ref-bound clicks with independent transition and goal validation |
| `browser.form_draft` r1 | Discover and assess ordinary reversible fields in hidden Chromium, then type or select at most five independently approved, exact owner-supplied values in one visible session and verify the uncommitted draft in place |

`browser.interaction` r2 remains click-only. `browser.form_draft` r1 exposes only
type and select during its action stage and never exposes click, submit, send,
publish, upload, download, credential, captcha/2FA, payment/purchase, or page
script actions. Login and human verification are explicit owner handoffs.
Low-level browser tools do not expand the supported Workflow surface;
[Workflow capabilities](workflow-capabilities.md) is authoritative. Browser r1
profiles and their post-completion presentation compatibility path are retired.

## Page, Draft, And Visual Evidence

Browser evidence uses separate contracts:

- `browser.read` extracts bounded rendered text and typed page metadata. The
  `browser.page_read` Profile calls it only after hidden health and open stages,
  with `require_browser_session=true` and `reuse_active_page=true`; a managed
  session failure is explicit and never falls back to direct HTTP.
- `browser.wait` settles navigation or interaction against bounded observable
  readiness signals. A timeout, renderer failure, or caller cancellation fails
  the current stage explicitly.
- `browser.snapshot` creates a structured accessibility projection with
  executable wrapped refs, page identity, presentation mode, session generation,
  and page generation for the selected page state. Snapshot IDs include the
  session generation, while navigation and successful interaction advance the
  page generation and invalidate older action and visual evidence.
- `browser.click` accepts only a persisted ref from that snapshot.
- `browser.type` and `browser.select` are usable as page mutations only inside
  `browser.form_draft`. Runtime checks the active Profile, latest snapshot,
  page/ref identity, session and page generations, ordinary-field allowlist,
  forbidden control metadata, and exact owner-supplied value both before
  approval and again when the approved call executes. Each action gets its own
  approval; public summaries and persisted browser projections redact values.
- Workflow-only `browser.visual_inspect` validates the latest structured
  snapshot, captures a screenshot, reuses Fast image inspection, then captures
  another structured snapshot. Any change in session/page generation, page ID,
  URL, or snapshot digest returns `visual_evidence_stale`. Its bounded untrusted
  output contains no coordinates or executable refs. Current Profiles expose
  this stage only when the owner explicitly asks for a screenshot or visual
  confirmation.
- `browser.validate_transition` compares the persisted before/after snapshots;
  `browser.assess_goal` independently evaluates the frozen goal against one
  exact snapshot.
- Every navigation and click is followed by settle and a fresh snapshot. A
  stale generation, stale ref, repeated state, route divergence, or missing
  semantic evidence fails closed.

Settle and snapshots share one `content_digest` over the rendered title and
body. The URL is validated separately and is intentionally excluded from this
digest, so a hash-route or address-bar-only change cannot prove a new page
state. Goal assessment likewise treats a matching clickable control as an
available next action, not completion; before any validated click, evidence
made only of actionable refs is returned as `progress/action_required`.

Agent-browser's accessibility snapshot and native refs are the provider-owned
interaction truth. SparkClaw adds bounded model projection, relevance checks,
page identity, semantic fingerprints, repeated-state detection, and explicit
failure codes. Page text remains untrusted evidence and never becomes an
instruction source.

## Registered And Dynamic Public Targets

The existing destination registry and its matching behavior are unchanged and
remain the first named-target lookup. A registry hit keeps its existing
descriptor, host scope, route hints, and authentication handling. Adding a site
to this registry is data maintenance; it adds neither a Catalog leaf nor a
semantic candidate, so it does not increase Top-2 intent-selection cost.

After a managed-browser leaf is selected, an unregistered named public target
runs one bounded Info-backed `web.search`. Runtime consumes only the persisted
ordered `results[].url` fields and selects the first URL that passes mandatory
safety checks. It does not parse answer prose or snippets, call Fast or
embeddings, rescore relevance, reorder results, or write the result into the
registry. Identification latency is one Info round trip plus bounded DNS and
redirect checks; explicit URLs, current tabs, and registry hits bypass it.

Every dynamic target must use HTTPS without userinfo. Its hostname, every
resolved address, each redirect, and the final URL must remain on public
networks; loopback, private, link-local, multicast, and unspecified addresses
are rejected. Provider absence, no usable ordered result, and unsafe results
produce distinct typed failures instead of a guessed URL.

## Managed Chromium Profile

Normal execution is headless. Human-only verification may temporarily open a
visible Chromium process using the same SparkClaw-owned profile, but hidden and
visible processes must never own that profile concurrently. Authentication
state remains inside Chromium. SparkClaw does not copy credentials into another
process or attach to the owner's daily browser profile.

On the Linux ARM64 Compose runtime, a visible session uses the owner's real
X11/XWayland desktop. The desktop bridge is an opt-in Compose overlay,
`docker/compose.visible-browser.yaml`; the base `docker/compose.yaml` mounts no
X socket and passes no display environment into Gateway. `npm run dev` discovers
one unambiguous local display and its Xauthority file, then applies the overlay
so the X socket and authority are mounted into Gateway. On a headless host it
starts the same stack without the overlay and only hidden automation is
available. The
adapter disables agent-browser's Xvfb fallback for visible sessions: a missing
or inaccessible desktop now fails explicitly instead of reporting success for
a browser that can only be seen inside a virtual display. Headless automation
does not require this desktop bridge. The Gateway image provides a UTF-8 locale
and Noto CJK/emoji fonts so Chromium can render Chinese applications such as QQ
Mail without missing-glyph boxes.

The default profile root is `./data/browser-profiles`. Each session holds an
exclusive OS file lock for the full process lifetime; contention fails instead
of launching a second owner. Profile access also requires bounded startup,
bounded command execution, and cleanup of owned child processes. The Compose
Gateway uses an init process to reap Chromium descendants after a browser
session exits. A visible handoff stops hidden ownership before acquiring the
same profile; transfer back to hidden follows the same ordering.

Hidden Chromium uses a 20-minute daemon idle window by default. Configuration
loading requires that window to cover the two consecutive model-owned stages
that can occur between a snapshot and its bound click, including the configured
model request and Workflow step limits. This prevents slow model reasoning from
closing and relaunching Chromium underneath a still-current snapshot. Visible
sessions do not use the daemon idle timeout and remain open for the owner.

After acquiring the exclusive profile lock, session startup validates Chromium's
native `SingletonLock`, `SingletonSocket`, and `SingletonCookie`. A live
same-host PID or reachable Unix socket remains busy. Only stale symbolic links
with no live owner are removed; malformed entries, regular files, and
indeterminate ownership fail closed. This lets a recreated Gateway reclaim a
profile left by a terminated container without stealing it from a live browser.

Browser automation and interaction acquire, navigate, settle, snapshot, and
act in hidden Chromium before their required visible result presentation. Form
draft uses hidden Chromium only to acquire the target, capture the initial
structured controls, and assess what remains. Runtime then opens the target in
visible Chromium before any approved `browser.type` or `browser.select`; every
approved mutation, settle, higher-generation snapshot, and subsequent goal
assessment stays in that same visible session. It verifies the final unsubmitted
state in place instead of reopening the target and losing the draft.

Visible presentation is a required node in each applicable frozen Workflow:
Runtime settles the result, captures a visible snapshot, revalidates the frozen
route, and independently reassesses interaction or form-draft goals. The run
cannot succeed without that visible evidence. `browser.page_read` is
intentionally different: its entire health/open/read path is hidden and
successful reads do not create a visible result window. A fresh visible session
navigates directly to the target instead of first exposing its startup
`about:blank` tab; an already initialized reusable profile is never replaced
with a blank login prompt. Verified visible results remain open without the
headless daemon idle timeout, and production completion does not call
`browser.close`.

Safe result descriptors persist origin, path, route-shaped fragments
(`#/...` in-page routes; value-carrying fragments such as OAuth
`#access_token=...` are dropped), and query provenance
rather than provider session tokens. For applications such as QQ Mail, a new
process may replace a volatile `sid`; Runtime preserves the new session query,
reapplies only the verified same-origin hash route, and removes provider-injected
tokens from artifacts, audit records, episodes, and API responses. Owner-supplied
query parameters remain part of the frozen target. When a fresh visible process
must reapply such a route, Runtime performs one native reload and requires the
rendered content digest to change and settle before presentation can continue.

When a browser tool detects a login or human-verification gate, the Runtime
persists a handoff and asks the owner to complete it in visible Chromium.
Ambiguous replies cause zero browser calls. Explicit cancellation leaves the
visible page open; an explicit wrong-page reply reopens the frozen target. Only
an explicit completion confirmation enters validation.

Validation lists visible tabs, selects the handoff page, settles it, captures a
fresh visible snapshot, and independently checks both authentication evidence
and whether the current page still satisfies the frozen task. An explicit URL
must still match exactly; a registered destination may use only its bounded
host/subdomain rule. A missing, unauthenticated, or unrelated page keeps the
Workflow paused and reports the mismatch without starting hidden automation.
After visible validation succeeds, Runtime transfers the profile to hidden,
reacquires the selected page, settles it, and captures another fresh snapshot.
Loss of profile continuity returns to `waiting_owner` instead of guessing.
Pre-login refs are discarded while the completed-click budget is preserved.

Handoff transitions are persisted as `waiting_owner`, `reopening_visible`,
`validating_visible`, `transferring_profile`, `validating_hidden`, and
`resuming_workflow`, then `resolved`, `canceled`, or `failed`. Store
compare-and-swap plus a transition owner and bounded lease make retries and
Gateway restart recovery single-owner and idempotent across memory, file, and
PostgreSQL backends. Login completion is a Runtime-owned user-confirmation gate,
not a model-visible tool.

## Network And Safety Boundary

- Explicit targets must use normalized HTTP(S) URLs. Registered destinations
  resolve to frozen runtime URLs and bounded host/subdomain rules. Dynamic Info
  targets require public HTTPS and preserve their result-order provenance.
- URL fetch paths reject loopback, private, link-local, and otherwise forbidden
  literal hosts by default. Local fixtures require an explicit allowlist.
- Redirects and final page identity are revalidated.
- Existing unrelated tabs are not reused. The final result page remains open
  after successful open or interaction; tab closing is limited to test cleanup.
- `browser.status` is passive: it validates the pinned provider, system Chromium
  version and AArch64 ELF, profile lock availability, UTF-8/CJK support, and,
  when required, the DISPLAY socket and Xauthority file without starting
  Chromium or creating `about:blank`.
- Unsafe or consequential controls are blocked even if they appear in a
  snapshot. Model output cannot bypass ref ownership or Policy.
- Screenshots, raw responses, and rendered text are artifacts/evidence, never
  trusted instructions.
- The base Compose file exposes no X11 socket or Xauthority to Gateway. The
  `docker/compose.visible-browser.yaml` overlay mounts both read-only, which
  still grants Gateway access to the owner's desktop display. Apply the overlay
  only on the trusted, single-owner local runtime.

## Configuration And Setup

Install and verify the pinned runtime:

```bash
npm install
npm run setup:browser
```

The Linux setup check also requires fontconfig and an installed Chinese font.
Debian and Ubuntu hosts can provide them with the `fontconfig` and
`fonts-noto-cjk` packages.

Important settings are defined in `configs/sparkclaw.default.json` and mirrored
by `docker/env/sparkclaw.example.env`:

| Setting | Purpose |
|---|---|
| `adapters.browserAutomation.command` | Pinned `agent-browser` executable |
| `adapters.browserAutomation.chromiumExecutable` | Optional explicit system Chromium path |
| `adapters.browserAutomation.profileDir` | SparkClaw-owned persistent profile root |
| `timeoutMs` / `startupTimeoutMs` / `daemonIdleTimeoutMs` | Bounded lifecycle; hidden idle covers the configured model/Workflow reasoning gap |
| `security.browser_read_allow_hosts` | Explicit private-host exceptions, primarily test fixtures |
| `SPARKCLAW_BROWSER_DISPLAY` | Visible-browser overlay only: Linux host display, such as `:1` |
| `SPARKCLAW_BROWSER_XAUTHORITY` | Visible-browser overlay only: readable host Xauthority file (no default) |

Environment overrides use `SPARKCLAW_BROWSER_AUTOMATION_*`,
`SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`,
`SPARKCLAW_BROWSER_PROFILE_DIR`, and
`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS`. See [Deployment](deployment.md) for the
normal host and Compose commands.

`npm run dev` resolves the two desktop values and applies the
`docker/compose.visible-browser.yaml` overlay automatically. For a direct
Compose invocation, export them and stack the overlay explicitly:

```bash
mapfile -t browser_display < <(scripts/resolve-browser-display.sh)
export SPARKCLAW_BROWSER_DISPLAY="${browser_display[0]}"
export SPARKCLAW_BROWSER_XAUTHORITY="${browser_display[1]}"
docker compose --env-file .env -f docker/compose.yaml \
  -f docker/compose.visible-browser.yaml --profile models-local up -d gateway
```

Without the overlay the same command starts a fully headless Gateway with no
access to the host desktop.

## Verification

Browser changes should cover:

- adapter protocol, timeout, process ownership, and profile locking tests;
- passive Linux ARM64 environment preflight and reason-code tests;
- settle timeout/cancellation, snapshot normalization, and untrusted-evidence tests;
- explicit URL, registered destination, tab focus, and redirect cases;
- stale generations/refs, repeated state, unsafe controls, and attempt limits;
- fixed hidden page-read ordering, active-page reuse, no required-session HTTP
  fallback, final-URL validation, bounded raw-content fallback, and login resume;
- ordered Info-result consumption, unsafe-result skipping, structured-URL-only
  binding, provider failures, DNS/IP/redirect safety, and registry fast paths;
- form-draft hidden discovery followed by same-session visible mutations, exact
  values, separate approvals, public redaction, forbidden fields, five-action
  bound, no click exposure, pre/post-approval freshness, and in-place final
  verification without reopening the form;
- fresh and stale visual inspection with generation/digest binding and no
  coordinate or executable-ref projection;
- visible/hidden transfer, owner reply classification, restart recovery, CAS
  conflicts, and matching/non-matching post-login pages;
- UTF-8/CJK and QQ Mail Chinese snapshot/ref/auth-evidence round trips;
- private-host rejection and explicit fixture allowlisting;
- Workflow routing and stage-scoped tool exposure;
- `npm run setup:browser`, Gateway tests, WebChat tests/build, and the golden
  browser eval when the local fixture is available.
