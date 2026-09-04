# Playwright Extension Browser Migration Design

> Language: English | [简体中文](../zh-cn/docs/playwright-extension-browser-design.md)

## Status

Proposed on 2026-09-04. Phase 1 preview scaffolding is implemented: the pinned
host controller, encrypted token APIs, WebChat settings, disposable
qualification profile, and private Gateway socket mount are available. This
does not change the current browser runtime.

For the concise current-state baseline and the exact starting point for a new
development session, read the dated
[Playwright Extension migration handoff](playwright-extension-migration-handoff.md)
before making changes.

This document is the normative authority for the target Playwright
implementation. The existing
[Browser email Workflow design](browser-email-workflow-design.md) records the
currently implemented Host-CDP path only. When that document mentions
browserd, Host-CDP, `agent-browser`, headless provider tabs, or prohibition of
Playwright, those statements describe the legacy implementation and do not
constrain the target defined here.

The implemented runtime remains the host-owned SparkClaw Chromium controlled by
`agent-browser` through Host-CDP until every cutover gate in this document
passes. There is no automatic fallback between the two transports. Failed
qualification leaves Host-CDP unchanged.

The extension implementation sequence is fixed. SparkClaw first integrates the
unmodified official Playwright Extension to prove the browser, controller, MCP,
CLI, settings, credential, and task-tab contracts. After those product contracts
are complete and frozen, SparkClaw derives an independently packaged extension
from the upstream source to implement the final background-without-focus and
explicit-handoff behavior. The official extension is an integration baseline,
not the final production extension.

## Decision Summary

SparkClaw will qualify a Playwright Extension architecture in which the owner
uses an ordinarily launched browser and SparkClaw attaches only while it owns a
task tab. Browser startup must not expose a remote-debugging port or add
automation startup flags.

The target divides browser control into two lanes:

| Lane | Backend | Purpose |
|---|---|---|
| Governed generic browser | Playwright MCP plus Playwright Extension | Model-guided observation and bounded interaction through the existing provider-neutral browser tools |
| Deterministic provider scripts | Playwright CLI plus Playwright Extension | Fixed login probes and effect scripts in which the model selects a function and supplies semantic values but never authors browser actions |

Both lanes attach to the same running browser and therefore use its current
profile, cookies, local storage, passkeys, and authenticated sessions without
copying them into SparkClaw. They must use separate task tabs and must never
control the same tab concurrently.

Extension adoption has two deliberate stages:

1. The official Web Store extension is used unchanged while SparkClaw designs
   and implements the surrounding product boundary. Its known foreground-focus
   behavior is accepted only as a development and qualification limitation.
2. After the controller and browser contracts are stable, SparkClaw builds and
   qualifies the independently distributed **SparkClaw Browser Bridge**. It
   preserves upstream Playwright compatibility while removing unconditional
   focus, allowing background task tabs, and reserving foreground activation for
   an explicit owner handoff.

The API, credential abstraction, profile identity, task ownership, provider
registry, and MCP/CLI request contracts must not depend on which of these two
extension packages is installed. Moving to the Browser Bridge is an extension
implementation replacement, not a Gateway or Workflow redesign.

Playwright Library remains the common implementation engine underneath the MCP
and CLI packages. SparkClaw does not initially build a third direct-Library
backend. A later replacement of CLI with a typed long-lived Library worker is
allowed only if qualification shows that CLI process or result contracts are
insufficient.

## Official Extension Isolation Limitation

Source inspection of the pinned official extension path found that it enables
automatic debugger attachment for every known browser tab, initializes MCP tab
wrappers for all pages, and may read existing console and request metadata while
building those wrappers. Tab-list operations can also render headers for all
attached pages. Creating a fresh SparkClaw task tab therefore does not make the
current official extension a per-tab privacy boundary.

This changes the qualification posture, not the target contract:

- the official extension may be used only with a disposable qualification
  profile that contains no ordinary owner tabs or production credentials;
- the Phase 1 settings and controller integration remain preview-only and must
  not be qualified against the owner's everyday profile;
- Gateway still requests only a neutral task tab and never intentionally selects
  an owner tab, but that is not sufficient to prevent extension-level
  attachment or observation;
- the independent SparkClaw Browser Bridge is mandatory for production because
  it must implement and prove an explicit task-tab allowlist before attachment,
  not merely remove foreground focus.

## Context

Host-CDP solved container browser ownership and profile persistence, but it
requires Chromium to start with a browser-wide remote-debugging endpoint.
SparkClaw needs a different owner experience:

- the browser starts and behaves as an ordinary daily browser;
- the owner may sign in to Gmail and other sites normally before SparkClaw is
  connected;
- SparkClaw creates isolated task tabs instead of taking over owner tabs;
- automation attaches for the duration of a task and detaches afterward;
- login state remains in the browser profile;
- provider operations remain deterministic and approval-governed.

The Playwright Extension is the attachment boundary. MCP and CLI are clients of
that boundary, not alternative browser profiles. MCP provides a structured tool
protocol suitable for iterative model-guided browser work. CLI provides named
sessions and fixed script execution suitable for deterministic provider code.

## Goals

1. Allow the owner to use the browser normally before, during, and after a
   SparkClaw task.
2. Reuse current browser authentication without cookie, password, token, or
   profile export.
3. Remove permanent browser-wide CDP exposure and automation startup flags.
4. Preserve the existing `browserautomation.Adapter`, ToolHub, Workflow,
   Policy, Approval, evidence, and public failure boundaries.
5. Keep model authority at the semantic-function level; do not allow the model
   to emit Playwright code or arbitrary CLI commands.
6. Give every task an explicit tab owner, bounded lifetime, and deterministic
   cleanup path.
7. Keep generic browser exploration separate from deterministic provider
   probes and effects.
8. Retire Host-CDP, browserd, and `agent-browser` only after live browser,
   deployment, email, and regression gates pass.

## Non-Goals

- Copying cookies, storage state, browser databases, passwords, or passkeys.
- Circumventing provider security checks, CAPTCHA, 2FA, or browser-integrity
  controls.
- Running model-generated JavaScript or Playwright code.
- Automatically taking over a tab merely because the owner is inactive.
- Giving the Gateway unrestricted access to every tab in the owner profile.
- Running MCP and CLI against the same page at the same time.
- Keeping Host-CDP as a permanent fallback after cutover.
- Redesigning provider selectors, message composition, or email Workflow
  semantics in this document.

## Terminology And Responsibility

| Component | Responsibility | Must not own |
|---|---|---|
| Ordinary browser | User-facing browsing, profile, authentication state, extensions, browser process | Workflow, approval, provider selection |
| Extension bridge | Official Playwright Extension during integration, then SparkClaw Browser Bridge for production; temporary connection to the running browser and client-specific task tabs | SparkClaw authorization or business policy |
| Playwright MCP | Structured browser observation and action tools for the generic browser adapter | Provider effects or model-generated unrestricted code |
| Playwright CLI | Named attachment sessions and execution of repository-owned scripts | Semantic routing or arbitrary model commands |
| Browser host controller | Extension pairing, subprocess lifecycle, private transport, health, and cleanup | Browser content decisions |
| Gateway adapter | Tab ownership, snapshot generations, normalization, timeouts, and typed failures | Browser profile or credentials |
| Provider registry | Fixed scripts, origins, revisions, result schemas, and deadlines | Model-selected executable paths or URLs |
| Workflow and Policy | Capability selection, frozen arguments, approval, effects, and completion evidence | Low-level selector improvisation |

The browser host controller is a product boundary, not a second browser
automation engine. It runs under the desktop owner and supervises the official
Playwright clients because extension attachment belongs to the host browser
session. The Gateway container communicates with it through a private,
authenticated, versioned local transport.

In this document, **official extension** means the unmodified upstream package
used for initial integration. **Browser Bridge** means the later independently
packaged SparkClaw derivative. The generic term **extension bridge** applies to
their shared protocol boundary.

## Target Topology

```text
Owner desktop session
  -> ordinarily launched fixed SparkClaw Chromium artifact
       -> one persistent fixed SparkClaw profile
       -> Playwright Extension
       -> ordinary owner tabs
       -> SparkClaw task-tab groups

Host user service: sparkclaw-browser-controller
  -> extension pairing and token custody
  -> generic lane: pinned Playwright MCP server
  -> script lane: pinned Playwright CLI sessions
  -> private versioned capability endpoint
  -> bounded process, session, and tab cleanup

Gateway container
  -> browserautomation.Adapter
       -> allowlisted MCP operations through the host controller
  -> deterministic provider runner
       -> registered script request through the host controller
  -> Workflow -> ToolHub -> Policy -> Approval
```

The first proof of concept must run the MCP and CLI clients as the same host
desktop owner as the browser. Container-to-extension discovery is not assumed.
Only after local attachment is proven may the private Gateway-to-controller
transport be implemented and qualified.

## Browser And Profile Ownership

The browser is launched as an ordinary user application. The launch contract
for extension mode forbids:

- `--remote-debugging-port` and `--remote-debugging-pipe`;
- `--enable-automation`;
- headless mode for the shared daily profile;
- a second browser process against the same profile;
- an automation-owned temporary profile for configured account operations.

The transport does not require a SparkClaw-specific browser brand. Initial
qualification starts with the pinned SparkClaw Chromium artifact because the
deployment already owns and verifies it. Cutover is blocked until the
Playwright Extension and both Playwright clients work with that Chromium build.
The official extension path currently documents Chrome and Edge; compatibility
with the SparkClaw Chromium artifact is therefore an explicit PoC result, not an
assumption.

The selected profile remains browser-owned. SparkClaw stores only a bounded
profile identity, browser generation, extension pairing state, and readiness
metadata. It never stores browser authentication material.

## Extension Pairing And Control Boundary

Extension pairing is a local owner action performed once per browser profile.
The extension connection token is credential-like and must be:

- generated with high entropy;
- entered only through the authenticated household settings surface or an
  equivalent owner-only bootstrap path;
- stored at rest only as an authenticated encrypted value in the existing
  SparkClaw credential Vault;
- decrypted only inside the Gateway browser-credential manager and the host
  controller memory for a bounded connection attempt;
- absent from controller files and duplicate plaintext configuration;
- omitted from Gateway public APIs, logs, traces, artifacts, model context,
  subprocess arguments visible to other users, and support bundles;
- rotatable without deleting the browser profile.

The host controller exposes a separate short-lived capability to the Gateway.
Possession of the extension token alone is not treated as tab authorization.
The Gateway adapter continues to enforce task ownership, Workflow scope, and
Policy independently.

The controller rejects unpinned client versions, an unpaired extension, a wrong
desktop owner, multiple controllers for one profile, and stale browser or
extension generations. It reports typed health without returning secret values.

## Settings Surface And Secret Persistence

WebChat adds one connection entry separate from browser email:

```text
Settings
`- Connections
   |- Browser control
   |  `- Playwright Extension
   `- Browser email
      |- QQ Mail
      |- Outlook
      `- Gmail
```

`Browser control` owns the shared browser attachment. The three email entries
continue to own only provider enablement, login-page opening, and provider
readiness. They do not store separate extension tokens.

During official-extension integration, the detail view may display
`Playwright Extension (preview)`. The production cutover changes the display
name to `SparkClaw Browser Bridge` without changing the API paths or provider
settings. Because the independently packaged extension has its own identity and
storage, its token is enrolled as a new credential; SparkClaw never copies or
silently reuses the official extension token.

The Playwright Extension detail view contains:

- browser/profile identity and current non-secret status;
- one password-style token input that is never prefilled;
- Save or Replace, Check connection, and Remove actions;
- last successful validation time, credential generation, bounded browser
  version, and a stable error code;
- an owner-visible link to the approved extension listing.

Saving the token is a validate-before-persist operation:

1. WebChat submits the token once through the authenticated control API. It
   does not place it in a URL, browser storage, analytics, or client logs.
2. Gateway rejects unknown JSON fields, trailing values, control characters,
   surrounding whitespace, and an opaque token outside the bounded length.
3. Gateway passes the candidate only in memory to the host controller.
4. The controller performs one bounded extension handshake, requests creation
   and closure of one neutral task tab, then detaches. With the official
   extension this is permitted only in a disposable qualification profile
   because upstream may still attach to and observe every known page. The final
   Browser Bridge must prove that owner tabs are never attached or inspected.
5. Only a successful handshake is sealed into the existing credential Vault as
   `playwright-extension-token-v1`. A failed candidate does not replace the
   current credential.
6. WebChat clears the input after every terminal outcome. No response returns
   the submitted token.

The authenticated API is:

```text
GET    /api/browser/extension
PUT    /api/browser/extension/token
POST   /api/browser/extension/check
DELETE /api/browser/extension/token
```

All four routes fail closed when Gateway authentication is not configured.
Local no-auth development mode cannot enroll, check, replace, or delete this
credential.

Public state contains only `configured`, connection state, profile identity,
credential generation, bounded browser/extension versions, validation time,
and a stable error code. It never contains the token, ciphertext, Vault ref,
controller capability, endpoint path, process identifiers, or extension debug
details.

Replacing or deleting the token increments the credential generation, blocks
new browser admissions, cancels and detaches sessions using the old generation,
and publishes the new state atomically. A paused or resumed task cannot cross a
credential generation. Removing the token disables new browser and email
automation but does not delete the browser profile, cookies, or login state.

Suggested public states are `not_configured`, `checking`, `ready`,
`needs_attention`, `temporarily_unavailable`, and `vault_unavailable`. A prior
`ready` state is historical evidence only; every task still performs its
required fresh attachment or provider probe.

## Generic Browser Lane: MCP

The generic `browser.*` surface continues to enter through the existing
provider-neutral adapter. The model does not receive the complete upstream
Playwright MCP catalog. The adapter exposes only the operations required to
implement the current SparkClaw contracts:

- create, enumerate, select, and close task-owned tabs;
- navigate, refresh, and wait;
- obtain accessibility snapshots and bounded readable page content;
- click, fill, type, and select against fresh opaque references;
- obtain current URL, title, and bounded screenshot evidence.

The adapter translates those calls to an allowlisted Playwright MCP profile and
normalizes results into existing SparkClaw types. Upstream tool names, raw
locator syntax, arbitrary evaluation, tracing, recording, file access, network
interception, storage mutation, and unrestricted Playwright code are not exposed
to the model.

MCP state is transport state only. SparkClaw remains authoritative for task
identity, page identity, snapshot generation, content fingerprints, reference
expiry, approval, and completion evidence.

## Deterministic Script Lane: CLI

Provider login probes and effects use repository-owned scripts through pinned
Playwright CLI. The model may select only a registered function and provide the
semantic fields declared by its Workflow. It cannot choose:

- the CLI command;
- the session name;
- a script filename;
- a browser tab;
- a URL, selector, locator, or timeout;
- retries or an alternative provider implementation.

The provider registry binds the script revision, allowed origins, deadline,
input schema, output schema, risk, and result verifier. The host controller
creates a unique bounded CLI attachment session, creates one task tab, invokes
only the registered script, validates the result envelope, detaches the CLI
session, and closes or releases the task tab according to the operation
contract.

Scripts receive structured input on stdin or an equivalent private pipe.
Message content and credentials never appear in argv. `run-code --filename`
may reference only repository-owned, checksum-qualified files. Inline
model-generated code and arbitrary `run-code` content are forbidden.

Email send keeps its current one-attempt effect rule. Once the script may have
clicked Send, timeout, transport loss, or missing positive confirmation is an
unknown terminal outcome and is never retried automatically.

## Task Tabs, Groups, And Concurrency

Every MCP or CLI connection receives a client-specific task-tab group. A tab has
exactly one automation owner at a time:

| Class | Rule |
|---|---|
| `owner` | Never read, mutate, focus, or close automatically |
| `sparkclaw_mcp` | Controlled only by the generic MCP session that created it |
| `sparkclaw_cli` | Controlled only by the deterministic CLI invocation that created it |
| `handoff` | Automation paused while the owner performs an explicit action |
| `released` | No longer controlled and treated as an owner tab |

Normal tasks create a background tab and do not select an existing owner tab.
The browser remains headed and owner-visible; "background" describes tab focus,
not a hidden or headless browser. A task may request foreground focus only for
an explicit owner handoff.

MCP and CLI must not attach to the same tab concurrently. Per-profile
admission serializes conflicting provider effects, while independent generic
tasks may run only after the PoC proves that separate extension tab groups are
isolated. Initial implementation uses one active automation client per profile.

Owner interaction with a task tab pauses automation, invalidates its current
snapshot, and requires fresh state before resume. Owner inactivity never grants
control of an owner tab.

## Authentication And Login Validation

Account login happens in the ordinary browser without an active automation
connection whenever possible:

1. The settings surface opens the registered provider URL in the configured
   browser profile.
2. The owner completes login, CAPTCHA, 2FA, consent, or recovery manually.
3. SparkClaw does not observe keystrokes or receive credentials.
4. A later explicit check creates a new background CLI-owned tab in the same
   profile.
5. The fixed provider probe distinguishes signed-in, signed-out, ambiguous, and
   changed-page states using deterministic evidence.
6. The probe closes only its own tab and persists bounded readiness metadata.

Login validation remains configuration and pre-Workflow admission. It is not a
model-visible tool and does not become a Workflow node. Every external provider
effect still requires a fresh probe according to the provider contract.

## Target Email Workflow Binding

The target retains these email business invariants from the legacy Workflow:

- send only; reading, search, reply, forward, attachments, and draft management
  remain unavailable;
- provider and account selection remain deterministic and Runtime-owned;
- login validation remains outside the Workflow and model context;
- the model supplies only recipient, optional subject, and plain-text body;
- the exact provider, account hint, recipient, subject, and full body require
  approval immediately before the external effect;
- Send may be attempted once, and an unknown result is terminal and is never
  retried automatically;
- scripts remain first-party, revisioned, bounded, and strict-schema.

The following transport-specific legacy requirements are superseded:

| Legacy Host-CDP requirement | Playwright Extension target |
|---|---|
| browserd switches one profile between headed and headless Chromium | One ordinarily launched headed browser remains available to the owner |
| Probe and send require headless presentation | Probe and send use a background CLI-owned task tab |
| Runner receives a Host-CDP endpoint and `agent-browser` session | Host controller creates a bounded Playwright CLI extension session |
| Admission freezes a headless browser generation | Admission freezes provider-setting and browser-credential generations; connection and tab generations are invocation-local |
| Post-approval execution requires the earlier browser generation | Post-approval execution creates a fresh CLI session and reruns deterministic login validation immediately before composition |
| Email configuration may imply browser process/profile configuration | Email configuration references the one shared Browser control credential and stores no extension token |
| Playwright is forbidden | Playwright CLI plus Extension is the sole target provider-script backend |

The target send sequence is:

1. Resolve one configured provider and account deterministically.
2. Require a ready shared Browser control credential.
3. Ask the host controller to attach a bounded CLI session, create a background
   provider tab, run the fixed login probe, close the tab, and detach.
4. Freeze provider, account, provider-setting generation, browser-credential
   generation, probe and send script revisions, validation time, and invocation
   ID before creating the Workflow.
5. Let the one-node Workflow collect message values and obtain exact-content
   approval without holding a browser session or task tab open.
6. Recheck provider-setting and browser-credential generations after approval.
7. Create a new bounded CLI session and task tab, rerun the deterministic login
   probe in that same session, then compose, verify, and attempt Send once.
8. Verify the provider result, close only the task tab, detach, and return a
   bounded receipt. Any uncertainty after the possible Send click becomes the
   existing non-retryable unknown outcome.

Browser, controller, connection, and tab generations remain useful for
within-invocation stale-state checks, but they do not cross the human approval
wait. Credential or provider-setting changes invalidate the approval binding;
ordinary detach and reattach do not by themselves force the owner to approve
unchanged message content again.

## Adapter Compatibility Contract

Replacing the backend must not change public browser tools or silently weaken
the current safety invariants. The Playwright adapter must preserve:

- stable SparkClaw page IDs independent of visible tab order;
- generation-scoped snapshot IDs;
- opaque element references bound to one page and one fresh snapshot;
- semantic fingerprints across harmless upstream reference renumbering;
- stale-reference rejection after navigation, mutation, handoff, reconnect, or
  browser generation change;
- rendered content settling and bounded retries;
- task-owned tab filtering before list limits or active-tab selection;
- typed unavailable, timeout, stale, ambiguous, and unknown-outcome failures;
- bounded screenshots and page text with untrusted-content treatment.

The migration is not a command-name substitution. Existing adapter
characterization tests define the compatibility target. Any intentional public
behavior change requires a separate accepted design and contract update.

## Lifecycle And Cleanup

The host controller owns all Playwright subprocesses and sessions. Every
session has a profile identity, lane, task ID, browser generation, start time,
deadline, and cleanup state.

Normal completion performs, in order:

1. stop accepting actions for the task;
2. close the task tab when the operation contract permits it;
3. detach the MCP or CLI client without closing the ordinary browser;
4. terminate and reap invocation-owned subprocesses;
5. remove temporary pipes, output files, and capabilities;
6. record bounded non-secret cleanup evidence.

Gateway cancellation and shutdown call the same cleanup path. A controller
reconciler reaps expired sessions and orphaned subprocesses but never closes an
unclassified tab or the owner browser. Browser restart rotates its generation,
invalidates every task grant, and requires fresh attachment and page evidence.

## Configuration Shape

The proposed logical configuration is:

```json
{
  "adapters": {
    "browserAutomation": {
      "transport": "playwright_extension",
      "controllerEndpointFile": "/run/sparkclaw/browser-controller/endpoint",
      "profileID": "default",
      "connectTimeoutMs": 10000,
      "actionTimeoutMs": 30000
    }
  }
}
```

Exact Playwright, MCP, CLI, and extension versions are pinned together after
qualification. `latest`, floating browser extensions, mixed Playwright versions,
raw extension tokens, arbitrary endpoints, executable overrides, profile paths,
and model-controlled options are rejected.

The official extension version is pinned as the integration baseline. The
Browser Bridge is separately versioned and checksum-pinned after it is derived;
its compatibility suite must prove the same MCP and CLI protocol behavior before
it replaces the baseline.

The extension token is deliberately absent from this JSON configuration. A
token entered in WebChat lives in the encrypted credential Vault and is
projected through APIs only as redacted status. The controller receives the
decrypted value over its authenticated private transport only when validating
or opening an on-demand Playwright connection.

The migration selector exists only during qualification. After cutover,
`playwright_extension` becomes the sole accepted transport and stale Host-CDP
settings fail with a documented migration error.

During Phase 1 preview, the current `transport` remains `host-cdp`. The nested
`playwrightExtension` block registers only the private controller socket,
fixed `default` profile identity, and connection timeout. It does not activate
the Playwright adapter or create a runtime fallback.

## Deployment And Packaging

The local and remote deployment entrypoints must use one shared browser setup
path. The target setup:

- installs or verifies the qualified ordinary browser on the host, never in the
  Gateway image;
- installs the official extension through an owner-visible process during the
  integration stages;
- installs the checksum-qualified SparkClaw Browser Bridge through an
  owner-visible and auditable process for production qualification and cutover;
- installs pinned Playwright MCP, CLI, and compatible Library dependencies on
  the host controller runtime;
- creates the owner-scoped controller service, private runtime directory, and
  capability endpoint;
- never writes the extension token into Compose environment or repository
  files;
- verifies ordinary browser startup without CDP or automation flags;
- verifies Gateway-to-controller reachability before enabling browser
  capabilities.

Extension mode requires a persistent owner browser session. A displayless
remote host is not silently converted to headless Playwright or Host-CDP. The
remote cutover is blocked until its supported owner-session mechanism and
restart behavior pass qualification.

The Gateway image contains neither Chromium nor Playwright browser binaries.
Whether it retains a small MCP client library is an implementation detail; all
browser and extension process ownership remains on the host.

The Phase 1 preview is installed explicitly after the current Host-CDP browser
setup. It is not part of either production startup gate yet:

```bash
npm run setup:browser-controller
npm run check:browser-controller
npm run open:browser-extension-preview
```

For Remote deployments, pass the matching private environment file directly:

```bash
bash scripts/setup-browser-controller.sh --env-file .env.remote
bash scripts/setup-browser-controller.sh --check --env-file .env.remote
```

The open command uses the separate `extension-qualification` profile. Install
the official extension and perform all preview sign-ins only in that profile.
The controller identifies the pinned host artifact as `chromium`, not `chrome`.
This selects the official Playwright MCP Linux Chromium handoff path required
when the controller runs as a user service; declaring the artifact as Chrome
causes the short-lived connect-page launcher to fail under Ubuntu's AppArmor
user-namespace restrictions. Start the qualification browser with the open
command before validating or checking the extension connection.
The controller service stores no extension token; the token remains in the
Gateway credential Vault and crosses the owner-only Unix socket only for a
bounded validation or acquisition attempt.

## Security Boundaries

- Treat extension control as browser-wide privileged access even when the
  extension presents client-specific tab groups.
- Keep extension and controller capabilities local, owner-scoped, rotatable,
  and absent from public APIs.
- Allowlist MCP operations and provider origins at the SparkClaw boundary.
- Enforce task-tab ownership before every observation or action.
- Reject arbitrary JavaScript, arbitrary CLI commands, raw locator injection,
  file URL access, storage export, and network interception.
- Keep credentials in the browser; redact page content and diagnostics under
  the existing evidence policy.
- Preserve exact-content approval for external sends and all existing
  unknown-outcome rules.
- Do not claim that Playwright allowed-origin options are a security boundary;
  SparkClaw authorization and network containment remain mandatory.

## Migration Plan

### Phase 0: Compatibility PoC

- Install the official extension in a disposable qualified browser profile.
- Keep that profile free of owner browsing tabs and production account state;
  the pinned official extension currently auto-attaches all known pages.
- Launch the fixed SparkClaw Chromium without remote-debugging, automation, or
  headless flags.
- Complete Gmail login manually before attachment.
- Attach Playwright MCP, create a task tab, read a page, interact, close the tab,
  and detach while the browser remains alive.
- Attach Playwright CLI in a separate session and execute a fixed local script.
- Verify that Gmail remains signed in and permits ordinary manual login.
- Verify extension token rotation and one-client-per-tab behavior.
- Determine whether the SparkClaw Chromium artifact is supported; record a
  clear GO or NO-GO.

No production code or deployment default changes in this phase.

### Phase 1: Host Controller

- Add the owner-scoped controller, endpoint schema, authentication, health,
  process supervision, deadlines, and cleanup reconciliation.
- Add the encrypted `playwright-extension-token-v1` credential, authenticated
  redacted APIs, and the `Settings > Connections > Browser control` detail
  view.
- Pin a mutually compatible Playwright Library, MCP, CLI, and extension set.
- Add fake-client and live host qualification tests.
- Use the official extension unchanged so failures in the product boundary are
  not confused with changes to extension internals.

### Phase 2: Generic MCP Adapter

- Implement the provider-neutral Playwright adapter behind the existing
  interface.
- Map the current tool surface and preserve snapshot, ref, generation, settle,
  and tab-ownership tests.
- Keep Host-CDP as the explicit qualification default until live acceptance.

### Phase 3: Deterministic CLI Scripts

- Migrate QQ Mail, Outlook, and Gmail probes and sends to the fixed CLI script
  contract and the target email binding above without changing their retained
  Workflow or approval semantics.
- Run provider-specific offline fixtures and live login probes.
- Do not send a real message without the existing final content confirmation.

### Phase 4: SparkClaw Browser Bridge

- Freeze the extension handshake, controller endpoint, credential, generation,
  task-tab ownership, background-operation, and explicit-handoff contracts.
- Derive the independently packaged SparkClaw Browser Bridge from the qualified
  upstream source and retain the applicable license and attribution files.
- Remove unconditional tab and window focus during attachment and normal task
  actions. Permit foreground activation only for an explicit owner handoff.
- Keep WebChat owner tabs outside the automation-owned tab set and verify that
  background work does not select, read, mutate, or close them.
- Preserve the official-baseline MCP and CLI behavior through compatibility
  tests, then enroll a newly generated Browser Bridge token. Do not migrate or
  reuse the official-extension token.

### Phase 5: Deployment Qualification

- Update both deployment entrypoints and doctor checks behind the temporary
  selector.
- Validate local and remote owner-session startup, restart, pairing, detach,
  and profile persistence.
- Validate that the Browser Bridge performs background work without changing
  the active WebChat tab or focusing its window, and that explicit handoff does
  focus the requested task tab.
- Run full Gateway, WebChat, Compose, docs, browser, and email validation.

### Phase 6: Atomic Cutover And Removal

Only after all acceptance gates pass:

- make the SparkClaw Browser Bridge the sole production extension transport;
- remove browserd, Host-CDP endpoint/proxy/configuration, and their deployment
  wiring;
- remove `agent-browser`, its MCP adapter, daemon cleanup, tests, and package
  pin;
- remove migration selectors and fallback language;
- update current architecture, runtime, deployment, development, email, README,
  and capability documentation in both languages;
- verify no Chromium or browser automation engine is downloaded in the Gateway
  container.

## Acceptance Gates

Cutover requires all of the following:

- ordinary manual Gmail login succeeds before any automation attachment;
- the browser command line contains no remote-debugging, automation, or
  headless flags for the shared profile;
- MCP and CLI can independently attach through the extension and detach without
  closing the browser;
- the official-extension integration baseline passes before extension changes
  begin, and the Browser Bridge subsequently passes the same compatibility
  suite;
- Browser Bridge attachment and background actions do not focus the browser or
  replace the active WebChat tab; explicit owner handoff does;
- authentication is reused in newly created task tabs without state export;
- the settings form never prefills or returns the token, clears it after every
  save attempt, and persists only Vault ciphertext after a real handshake;
- replacing or deleting the token invalidates old-generation sessions without
  deleting browser authentication state;
- owner tabs are never selected, read, mutated, or closed;
- MCP and CLI cannot concurrently control one tab;
- browser restart invalidates stale task and snapshot identities;
- subprocess and session cleanup leaves no persistent daemon accumulation;
- generic browser adapter characterization and live scenarios pass;
- all three provider probes pass against real signed-in accounts;
- send scripts preserve exact approval, one-attempt, and unknown-outcome rules;
- local and remote deployment checks pass from fresh supported hosts;
- Gateway, WebChat, Compose, docs, and security validation pass;
- removal verification finds no active `agent-browser`, browserd, Host-CDP,
  container Chromium, or legacy compatibility path.

## Qualification Risks And Required Answers

These are implementation gates, not reasons to weaken the target contract:

1. Resolved on 2026-09-04: the pinned official extension and MCP stack work with
   the fixed SparkClaw Chromium artifact when the channel is declared as
   `chromium`. Chrome and Edge are not product alternatives.
2. What owner-visible pairing or permission confirmation occurs on first MCP
   and CLI attachment, and can it be completed once without silent privilege
   escalation?
3. Can separate MCP and CLI clients keep distinct tab groups without cross-tab
   visibility under the chosen extension version?
4. How does the owner-session browser recover after host reboot on local and
   remote deployments without adding automation startup flags?
5. Can the Browser Bridge remove foreground focus without changing the
   qualified MCP/CLI behavior or weakening tab ownership?
6. Which extension, MCP, CLI, and Library versions form one tested compatibility
   set?

A NO-GO on any mandatory gate keeps Host-CDP as the current runtime and returns
the design for revision. It does not trigger cookie copying, hidden automation
flags, or a container-browser fallback.

## Version Evidence At Proposal Time

On 2026-09-04, discovery found `playwright` `1.62.1`, `@playwright/mcp`
`0.0.80`, and `@playwright/cli` `0.1.19` as the npm `latest` releases. These are
PoC candidates only. Implementation must record and pin the exact mutually
qualified set rather than installing `latest`.

## References

- [Playwright Library](https://playwright.dev/docs/library)
- [Playwright MCP](https://github.com/microsoft/playwright-mcp)
- [Playwright CLI](https://github.com/microsoft/playwright-cli)
- [Playwright Extension](https://github.com/microsoft/playwright/tree/main/packages/extension)
- [Current browser runtime](browser-runtime.md)
- [Implemented Host-CDP design](host-cdp-browser-design.md)
- [Current browser email Workflow](browser-email-workflow-design.md)
