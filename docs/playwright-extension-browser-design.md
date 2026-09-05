# Playwright Extension Browser Migration Design

> Language: English | [简体中文](../zh-cn/docs/playwright-extension-browser-design.md)

## Status

Proposed on 2026-09-04 and completed on 2026-09-05. All phases and cutover gates
have been implemented. The checksum-pinned SparkClaw Browser Bridge `1.0.18`,
fixed Chromium `148.0.7778.0`, owner-scoped Controller, Playwright MCP, and
Playwright CLI now form the only production browser runtime. Browserd,
Host-CDP, `agent-browser`, and the migration selector have been removed.

Final live qualification covered background-without-focus behavior, explicit
task-tab handoff, generic form interaction, restart and detach cleanup, and
read-only probes against signed-in QQ Mail, Outlook, and Gmail accounts. No
browser authentication or Bridge credential was copied or exported, and no
email send was invoked.

For the concise current-state baseline and the exact starting point for a new
development session, read the dated
[Playwright Extension migration handoff](playwright-extension-migration-handoff.md)
before making changes.

This document is the normative record of the Playwright implementation and its
migration gates. [Browser email Workflow design](browser-email-workflow-design.md)
and [Browser runtime](browser-runtime.md) describe the resulting current
implementation. Host-CDP references in the historical design are retained only
to explain the retired source architecture and the reasons for the cutover.

The migration followed a fixed extension sequence. SparkClaw first integrated
the unmodified official Playwright Extension to prove the browser, controller,
MCP, CLI, settings, credential, and task-tab contracts. After those product
contracts were complete and frozen, SparkClaw derived an independently packaged
extension from the upstream source to implement background-without-focus and
explicit-handoff behavior. The official extension remains an integration
baseline, not the production extension.

## Decision Summary

SparkClaw qualified a Playwright Extension architecture in which the owner
uses an ordinarily launched browser and SparkClaw attaches only while it owns a
task tab. Browser startup must not expose a remote-debugging port or add
automation startup flags.

The implementation divides browser control into two lanes:

| Lane | Backend | Purpose |
|---|---|---|
| Governed generic browser | Playwright MCP plus Playwright Extension | Model-guided observation and bounded interaction through the existing provider-neutral browser tools |
| Deterministic provider scripts | Playwright CLI plus Playwright Extension | Fixed login probes and effect scripts in which the model selects a function and supplies semantic values but never authors browser actions |

Both lanes attach to the same running browser and therefore use its current
profile, cookies, local storage, passkeys, and authenticated sessions without
copying them into SparkClaw. They must use separate task tabs and must never
control the same tab concurrently.

Extension adoption used two deliberate stages:

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
and CLI packages. SparkClaw added no third direct-Library backend. A later
replacement of CLI with a typed long-lived Library worker remains allowed only
if new evidence shows that CLI process or result contracts are insufficient.

## Official Extension Isolation Limitation

Source inspection of the pinned official extension path found that it enables
automatic debugger attachment for every known browser tab, initializes MCP tab
wrappers for all pages, and may read existing console and request metadata while
building those wrappers. Tab-list operations can also render headers for all
attached pages. Creating a fresh SparkClaw task tab therefore did not make the
official baseline extension a per-tab privacy boundary.

This changed the qualification posture, not the production contract:

- the official extension was used only with a disposable qualification profile
  that contained no ordinary owner tabs or production credentials;
- the Phase 1 settings and controller integration were preview-only and were
  not qualified against the owner's everyday profile;
- during official qualification, Gateway requested only a neutral task tab and
  never intentionally selected an owner tab, but that was not sufficient to
  prevent extension-level attachment or observation;
- the independent SparkClaw Browser Bridge was mandatory for production and now
  enforces the qualified task-tab allowlist before attachment rather than merely
  removing foreground focus.

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

## Current Production Topology

```text
Owner desktop session
  -> ordinarily launched fixed SparkClaw Chromium artifact
       -> one persistent fixed SparkClaw profile
       -> SparkClaw Browser Bridge
       -> ordinary owner tabs
       -> SparkClaw task-tab groups

Host user service: sparkclaw-browser-controller
  -> extension pairing and bounded credential relay
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

The completed proof of concept ran MCP and CLI as the same host desktop owner
as the browser and did not assume container-to-extension discovery. After local
attachment passed, the private Gateway-to-controller transport was implemented
and qualified.

## Browser And Profile Ownership

The browser is launched as an ordinary user application. The launch contract
for extension mode forbids:

- `--remote-debugging-port` and `--remote-debugging-pipe`;
- `--enable-automation`;
- headless mode for the shared daily profile;
- a second browser process against the same profile;
- an automation-owned temporary profile for configured account operations.

The transport does not require a SparkClaw-specific browser brand. Qualification
used the pinned SparkClaw Chromium artifact because the deployment already owns
and verifies it. The official extension documentation named Chrome and Edge,
so compatibility with SparkClaw Chromium was proved explicitly before cutover.

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

WebChat provides one connection entry separate from browser email:

```text
Settings
`- Connections
   |- Browser control
   |  `- SparkClaw Browser Bridge
   `- Browser email
      |- QQ Mail
      |- Outlook
      `- Gmail
```

`Browser control` owns the shared browser attachment. The three email entries
continue to own only provider enablement, login-page opening, and provider
readiness. They do not store separate extension tokens.

During official-extension integration, the detail view displayed
`Playwright Extension (preview)`. Production now displays `SparkClaw Browser
Bridge` without changing the API paths or provider settings. Because the
independently packaged extension has its own identity and storage, its token was
enrolled as a new credential; SparkClaw never copied or silently reused the
official extension token.

The Browser Bridge detail view contains:

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
   and closure of one neutral task tab, then detaches. The official baseline ran
   only in a disposable qualification profile because upstream could attach to
   every known page. Production Browser Bridge tests prove that owner tabs are
   never attached or inspected.
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

The private controller API exposes `POST /v1/run-script` for exact registered
script IDs and revisions and `POST /v1/open-provider-login` for fixed provider
identities. Neither route accepts a script path, URL, selector, executable, or
browser option from Gateway. The current registry contains only QQ Mail,
Outlook, and Gmail probe/send revision 1 entries and computes a SHA-256 over
each entry's repository source closure at controller startup.

CLI subprocess failures are projected only as a fixed command category, typed
failure class, output stream, secret-match count, and residual byte count.
Provider-owned targetless URL reads and evaluations may retry a destroyed
execution context up to four times at 250 ms intervals, revalidating the current
origin before each evaluation attempt. Element-targeted click, fill, press, and
other effect operations are never retried by this mechanism.

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

The implementation retains these email business invariants from the legacy
Workflow:

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

The current send sequence is:

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

The implemented logical configuration is:

```json
{
  "adapters": {
    "browserAutomation": {
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "settleTimeoutMs": 15000,
      "settleQuietPeriodMs": 500,
      "settlePollIntervalMs": 100,
      "routeRebindLimit": 2,
      "playwrightExtension": {
        "controllerSocket": "/run/sparkclaw/browser-controller/controller.sock",
        "profileID": "default",
        "connectTimeoutMs": 20000
      }
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

The migration selector was removed during cutover. The nested
`playwrightExtension` block registers only the private controller socket, fixed
`default` profile identity, and connection timeout. Stale Host-CDP environment
settings fail with a documented migration error and cannot create a fallback.

## Deployment And Packaging

The local and remote deployment entrypoints use one shared browser setup path.
The production setup:

- installs or verifies the qualified ordinary browser on the host, never in the
  Gateway image;
- installs the checksum-qualified SparkClaw Browser Bridge through an
  owner-visible and auditable process;
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
remote host is not silently converted to headless Playwright or Host-CDP. A
missing supported owner session is a deployment error.

The Gateway image contains neither Chromium nor Playwright browser binaries.
Whether it retains a small MCP client library is an implementation detail; all
browser and extension process ownership remains on the host.

Install or verify the production browser, Bridge, Controller, and persistent
profile with the shared entrypoint:

```bash
npm run setup:browser
bash scripts/setup-browser.sh --check
npm run open:browser
```

For Remote deployments, bind the same setup to the matching private environment
file:

```bash
SPARKCLAW_BROWSER_ENV_FILE=.env.remote bash scripts/setup-browser.sh
SPARKCLAW_BROWSER_ENV_FILE=.env.remote bash scripts/setup-browser.sh --check
```

The open command uses the persistent `default` profile. The controller
identifies the pinned host artifact as `chromium`, not `chrome`, and invokes the
Bridge's native launcher rather than starting another browser. The Controller
service stores no extension token; the token remains in the Gateway credential
Vault and crosses the owner-only Unix socket only for a bounded validation or
acquisition attempt.

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

## Completed Migration Plan

### Phase 0: Compatibility PoC (Completed)

- The unmodified official extension was qualified in a disposable profile that
  contained no ordinary owner tabs or production account state.
- Fixed SparkClaw Chromium launched without remote-debugging, automation, or
  headless flags; ordinary manual Gmail login completed before attachment.
- MCP and CLI independently attached, operated a task tab, detached, and left
  the browser and login state intact.
- Credential rotation and one-client-per-tab ownership passed. Chromium
  `148.0.7778.0` received a GO as the fixed product artifact.

This phase changed no production default.

### Phase 1: Host Controller (Completed)

- The owner-scoped controller, versioned private protocol, authentication,
  health, process supervision, deadlines, generation handling, and cleanup
  reconciliation were implemented and installed.
- The encrypted `playwright-extension-token-v1` credential, authenticated
  redacted APIs, and `Settings > Connections > Browser control` view were added.
- Fake-client, protocol, lifecycle, WebChat, Gateway, and live host coverage
  qualified the pinned Playwright set against the official baseline.

### Phase 2: Generic MCP Adapter (Completed)

- The provider-neutral Playwright adapter replaced the former implementation
  behind the existing interface without changing ToolHub contracts.
- Snapshot, ref, generation, settle, task-tab ownership, action, screenshot,
  close, detach, and failure characterization passed offline and live.
- The temporary Host-CDP qualification default was removed in Phase 6.

### Phase 3: Deterministic CLI Scripts (Completed)

- QQ Mail, Outlook, and Gmail probes and sends moved to six fixed, checksummed
  CLI handlers without changing Workflow, approval, one-attempt, or
  unknown-outcome semantics.
- Provider fixtures retained signed-out classification, bounded
  context-destroyed recovery, origin validation, and fail-closed redirects.
- On 2026-09-05, `npm run qualify:playwright-email -- --profile remote` passed
  all three signed-in accounts sequentially in a read-only run through Browser
  Bridge `1.0.18`. The browser remained running, cleanup left no CLI daemon or
  session directory, focus did not move to the browser, and no send handler ran.

### Phase 4: SparkClaw Browser Bridge (Completed)

- The extension handshake, credential, generation, task-tab ownership,
  background-operation, and explicit-handoff contracts were frozen.
- SparkClaw Browser Bridge `1.0.18` was independently packaged from the
  qualified upstream source with license, attribution, versioned Service Worker
  entry, and checksum closure.
- Compatibility tests retained the official-baseline MCP and CLI behavior.
  Background attachment and actions do not focus the browser or expose owner
  tabs; only an explicit owner handoff focuses the requested task tab.
- Production enrolled a newly generated Bridge credential. The official
  extension credential was neither reused nor migrated.

### Phase 5: Deployment Qualification (Completed)

- Local and Remote deployment entrypoints and doctor checks now use the shared
  Bridge-only Browser setup with no migration selector.
- Owner-session startup, service restart, pairing, detach, generation
  invalidation, profile persistence, and Local/Remote Browser checks passed.
- X11-monitored no-handoff and explicit-handoff scenarios proved the focus
  contract. Gateway, WebChat, Compose, docs, browser, provider, shell, and
  security suites passed within the environment limits recorded below.

### Phase 6: Atomic Cutover And Removal (Completed)

- SparkClaw Browser Bridge became the sole production extension transport.
- Browserd, Host-CDP endpoint/proxy/configuration and deployment wiring,
  `agent-browser`, its MCP adapter and daemon cleanup, obsolete tests, package
  pins, migration selectors, and fallback paths were removed.
- Current architecture, runtime, deployment, development, email, README, and
  capability documentation were updated in both languages.
- Gateway image and dependency checks confirm that it downloads no Chromium or
  Playwright browser binary.

## Completed Acceptance Gates

The cutover passed every browser-migration gate:

- manual login and authentication reuse worked without state export;
- the shared-profile browser command line contained no remote-debugging,
  automation, or headless flags;
- MCP and CLI attached independently and detached without closing the browser;
- the official integration baseline and Browser Bridge compatibility suites
  passed;
- background work did not focus the browser or select an owner tab, while
  explicit handoff focused only the requested task tab;
- owner tabs remained outside task ownership and MCP and CLI could not control
  one task tab concurrently;
- settings and Gateway tests proved token non-prefill, clear-after-save,
  handshake-before-persist, ciphertext-only storage, rotation, deletion, and
  stale-generation rejection without deleting browser authentication;
- browser restart invalidated stale task and snapshot identities while
  preserving the profile and pairing;
- subprocess and session cleanup left no persistent Playwright process or
  runtime directory;
- generic adapter characterization, live form interaction, and all three
  signed-in provider probes passed without invoking a send;
- send handlers retained exact approval, one-attempt, and unknown-outcome rules;
- shared Local/Remote Browser setup, Compose, and deployment contracts passed,
  as did both complete Local and Remote deployment preflights;
- Gateway, WebChat, Compose, docs, and security validation passed;
- removal verification found no active `agent-browser`, browserd, Host-CDP,
  container Chromium, or executable compatibility path.

## Resolved Qualification Answers

1. Fixed Chromium `148.0.7778.0` works with the Playwright extension channel
   declared as `chromium`; Chrome and Edge are not product alternatives.
2. Pairing is an owner-visible credential enrollment through WebChat followed
   by a real bounded handshake. The token is not exposed again, and attachment
   grants no tab outside the Controller's task ownership.
3. MCP and CLI receive distinct controller sessions and task grants. Ownership
   checks reject cross-task and concurrent same-tab control.
4. User services restart the fixed browser and Controller against the
   persistent `default` profile without automation flags. Readiness restores
   pairing and authentication while rotating task/session generations.
5. Browser Bridge compatibility and X11-monitored live scenarios proved that
   attachment and normal actions remain in the background; only
   `tabs.handoff` activates the task tab.
6. The final production set is SparkClaw Browser Bridge `1.0.18`, Playwright MCP
   `0.0.80`, Playwright CLI `0.1.19`, Playwright Library/Core
   `1.63.0-alpha-2026-08-31`, and Chromium `148.0.7778.0`. Official Extension
   `0.4.0` remains only the completed compatibility baseline.

## Version Evidence At Proposal Time

On 2026-09-04, discovery found `playwright` `1.62.1`, `@playwright/mcp`
`0.0.80`, and `@playwright/cli` `0.1.19` as the npm `latest` releases. These
were PoC candidates only. The completed implementation pins the mutually
qualified production set listed above and never installs a floating `latest`.

## References

- [Playwright Library](https://playwright.dev/docs/library)
- [Playwright MCP](https://github.com/microsoft/playwright-mcp)
- [Playwright CLI](https://github.com/microsoft/playwright-cli)
- [Playwright Extension](https://github.com/microsoft/playwright/tree/main/packages/extension)
- [Current browser runtime](browser-runtime.md)
- [Implemented Host-CDP design](host-cdp-browser-design.md)
- [Current browser email Workflow](browser-email-workflow-design.md)
