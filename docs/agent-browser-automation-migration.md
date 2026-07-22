# Agent-Browser Browser Automation Migration

> Language: English | [简体中文](../zh-cn/docs/agent-browser-automation-migration.md)

Status: proposed implementation contract. This document does not describe an
already completed runtime change.

This document defines how SparkClaw will replace the Playwright browser
execution backend with
[vercel-labs/agent-browser](https://github.com/vercel-labs/agent-browser)
without changing the router-first capability architecture, public ToolHub
contracts, managed-profile ownership, approval policy, or browser Workflow
semantics.

The design was assessed against agent-browser `v0.32.3` at commit
`81c336c1c20b80ac648e0416a7b6e0c0ae7878bb`. Implementation must pin an exact
validated release; it must not install or execute an unbounded `latest` build.

Related contracts:

- [Architecture](architecture.md)
- [Browser Automation Improvement Plan](browser-automation-improvement.md)
- [Browser Interaction Workflow](browser-interaction-workflow-proposal.md)
- [Browser Perception Reliability Optimization](browser-perception-reliability-design.md)
- [Managed Shared Chromium Profile](managed-persistent-browser-profile.md)
- [Playwright Browser Automation Migration](playwright-browser-automation-migration.md)

The Playwright migration document remains historical evidence for the current
implementation. This document supersedes its execution-backend decision only
after the migration acceptance criteria below pass.

## Decision

SparkClaw will use agent-browser as the only production browser execution
backend. Agent-browser launches and controls local Chrome/Chromium through its
Rust CDP daemon. SparkClaw communicates with one agent-browser MCP stdio server
started as an internal subprocess with the `core` tool profile.

```text
Fast semantic router
  -> versioned browser Workflow
      -> ToolHub, Policy, Approval, Audit and Trace
          -> browserautomation.Adapter
              -> AgentBrowserAdapter
                  -> agent-browser mcp --tools core
                      -> isolated agent-browser daemon/session
                          -> SparkClaw-managed Chrome profile
```

MCP is an adapter transport in this design. Agent-browser tools are never
registered directly in ToolHub, never exposed to Fast or Deep, and never become
a second model-visible capability registry.

The final implementation removes the Playwright runtime dependency, embedded
Node driver, Playwright adapter, and Playwright-specific setup. It does not keep
two production browser backends or silently fall back from agent-browser to
Playwright.

## Preserved Architecture Boundaries

The migration changes execution mechanics, not product architecture. The
following remain authoritative and provider-neutral:

- Fast selects only registered semantic capability leaves.
- A versioned Workflow owns stage order and model-visible tool exposure.
- ToolHub is the only browser tool registration and execution boundary.
- Policy and Approval remain authoritative for consequential actions.
- Every browser operation produces the existing ToolCall, audit, trace, result,
  and artifact records.
- Browser observations remain untrusted external evidence.
- `browserautomation.Adapter`, `Result`, and `PageReadResult` remain the Go
  ownership boundary unless a provider-neutral contract defect is found.
- Public `browser.*` names, input/output schemas, risk levels, and approval
  semantics remain unchanged.
- `browser.automation r1` and `browser.interaction r1` keep their current
  routing and stage contracts.
- Human-only login, captcha, SMS, 2FA, payment, and security steps keep the
  visible handoff contract.

No Workflow may call arbitrary agent-browser commands. Only explicit mappings
inside `AgentBrowserAdapter` are allowed.

## Why The MCP Transport

Agent-browser offers a CLI, an MCP stdio server, and an internal client-daemon
protocol. SparkClaw will use the MCP stdio interface for these reasons:

- it is a documented, typed interface with explicit initialization and errors;
- one long-lived subprocess avoids a new shell process for every browser call;
- the `core` profile contains the required navigation, snapshot, interaction,
  wait, screenshot, tab, evaluation, and close operations;
- SparkClaw does not depend on the private daemon socket protocol;
- request IDs allow strict matching and rejection of late responses.

The Adapter must not parse human CLI output or invoke a shell command string.
It starts the configured executable directly with an argument vector:

```text
agent-browser mcp --tools core
```

The Go side implements only the MCP lifecycle needed by the adapter:

1. start process with bounded stdout, stderr, and a cancellable context;
2. send `initialize` and validate the negotiated protocol and server identity;
3. obtain and validate the expected core tool schemas;
4. issue `tools/call` requests with monotonically increasing IDs;
5. distinguish JSON-RPC, MCP tool, agent-browser business, and process errors;
6. call the owned session's close tool before subprocess termination.

The MCP client is not a generic project-wide MCP framework. It stays private to
`internal/browserautomation` until another real production consumer exists.

## Dependency And Configuration

The root package manifest will replace the exact Playwright dependency with an
exact agent-browser dependency. `npm run setup:browser` will invoke the pinned
local agent-browser binary to install its compatible Chrome for Testing build.
`scripts/doctor.sh` must verify all of the following:

- the resolved agent-browser version equals the pinned version;
- the native binary is executable on the host architecture;
- the configured Chrome executable is usable, or the managed Chrome for
  Testing installation is present;
- a bounded headless launch, snapshot, and close smoke succeeds;
- no stale SparkClaw-owned browser session remains after the smoke.

The target adapter configuration is provider-specific only below the existing
adapter boundary:

```json
{
  "tools": {
    "browserAutomation": {
      "enabled": true,
      "provider": "agent-browser",
      "profile": "default"
    }
  },
  "adapters": {
    "browserAutomation": {
      "command": "agent-browser",
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "daemonIdleTimeoutMs": 60000,
      "chromiumExecutable": "",
      "profileDir": "./data/browser-profiles"
    }
  }
}
```

The default command resolver prefers the pinned workspace binary. An explicit
`command` override is allowed for packaged deployments, but it is resolved and
version-checked at startup. Model/tool arguments cannot override the command,
MCP profiles, browser executable, profile root, namespace, provider, CDP URL,
extensions, restore state, or launch arguments.

The implementation removes `nodeCommand`, `SPARKCLAW_BROWSER_RUNTIME_DIR`, the
Playwright dependency, `PLAYWRIGHT_BROWSERS_PATH`, and Playwright setup checks.
Existing operator-facing profile and Chromium override variables may retain
their provider-neutral names:

- `SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`
- `SPARKCLAW_BROWSER_PROFILE_DIR`
- `SPARKCLAW_BROWSER_AUTOMATION_TIMEOUT_MS`

A new command override, if needed, is
`SPARKCLAW_BROWSER_AUTOMATION_COMMAND`. There is no environment option for raw
agent-browser arguments.

## Process, Session, And Profile Ownership

`AgentBrowserAdapter` serializes browser operations as the current adapter does.
It owns at most one active tuple:

```text
(owner_id, browser_profile_id, presentation)
```

For that tuple, it derives:

- an agent-browser namespace isolated to this SparkClaw gateway instance;
- an opaque session name derived from owner, logical profile, and
  presentation hashes;
- the absolute SparkClaw-managed profile directory
  `<profile-root>/<owner-hash>/<profile-hash>/user-data/`.

The namespace and session name must not contain raw owner identifiers.

The MCP process receives only adapter-owned environment and arguments. The
session launches with the managed profile path, optional validated Chromium
executable, bounded output, and one fixed presentation:

| Presentation | Agent-browser launch behavior |
|---|---|
| `hidden` | default headless Chrome, stable `1365x768` viewport |
| `visible` | `headed=true`, native visible window |

Changing owner, logical profile, or presentation performs an exclusive handoff:

1. invalidate every page and snapshot handle;
2. call `agent_browser_close` for the current owned session;
3. terminate and reap the MCP subprocess;
4. wait for the managed profile lock to clear;
5. start and initialize a replacement MCP subprocess;
6. launch the replacement session with the same or new managed profile.

Hidden and visible sessions never own the same profile concurrently. SparkClaw
never uses agent-browser `--auto-connect`, `--cdp`, named daily-Chrome profiles,
state import, `--restore`, auth vault, or user-supplied browser arguments. The
persistent profile itself remains the login-state source of truth.

Gateway shutdown closes only its derived session, never `close --all`. A
bounded agent-browser idle timeout is defense in depth for an ungraceful gateway
exit; it does not replace explicit close and process reaping.

## Public Tool Mapping

The adapter validates the MCP tool list once and calls only this fixed mapping:

| SparkClaw tool | Agent-browser core operation | Adapter responsibility |
|---|---|---|
| `browser.status` | initialize, `agent_browser_open`, `agent_browser_get_url` | lazily launch `about:blank`, report version and session health |
| `browser.list_tabs` | `agent_browser_tab_list` | normalize stable `tN` IDs to `page_N` |
| `browser.open` | `agent_browser_open` or `agent_browser_tab_new` | reuse one blank tab or create a tab, then validate final URL |
| `browser.focus` | `agent_browser_tab_switch` | resolve only a current adapter page ID |
| `browser.close` | `agent_browser_tab_close` | close one tab and select a valid remainder |
| `browser.navigate` | open/back/forward/reload core tools | map the declared navigation mode only |
| `browser.snapshot` | `agent_browser_snapshot` | build the SparkClaw snapshot and private ref map |
| `browser.screenshot` | `agent_browser_screenshot` | normalize image evidence for ArtifactStore |
| `browser.wait` | wait-for-text/selector/load or bounded milliseconds | reject unsupported wait combinations |
| `browser.click` | `agent_browser_click` | translate one current SparkClaw ref to a private `@eN` ref |
| `browser.type` | `agent_browser_fill` or `agent_browser_type` | preserve fill-versus-focused-type semantics |
| `browser.select` | `agent_browser_select` | translate one current SparkClaw ref and value |
| `browser.read` | open plus fixed `agent_browser_eval` | preserve rendered HTML and Readability evidence flow |

Page IDs remain provider-neutral. Agent-browser tab IDs are never accepted from
model/tool input. Opening, switching, closing, navigation, and unexpected tab
creation reconcile the page map and invalidate affected snapshots.

The adapter must fail startup if a required MCP tool is missing or its required
input fields are incompatible with the pinned contract. It must not discover a
similar tool by name or send raw `extraArgs` as a compatibility fallback.

## Browser Read And Evidence

`browser.read` will not delegate product semantics to `agent_browser_read`.
SparkClaw must retain its existing rendered-page and evidence contract:

```text
agent_browser_open / agent_browser_tab_new
  -> bounded render settle
  -> agent_browser_eval with SparkClaw-owned fixed read function
      - rendered outerHTML and visible text
      - title, URL, language, content type and ready state
      - length and truncation diagnostics
      - authentication signals
  -> ToolHub Readability extraction
  -> untrusted observation and optional artifact archive
```

Only the embedded, fixed read function may use `agent_browser_eval`. Model text,
page text, and ordinary tool arguments can never become JavaScript source.

Direct HTTP fallback, URL safety validation, local-host allowlisting,
Readability, truncation, authentication assessment, and artifact behavior stay
outside agent-browser and retain their current contracts.

## Snapshot And Ref Safety

Agent-browser snapshots expose a text accessibility tree and a structured map
of short refs such as `e1`. Those refs are session-local execution handles, not
SparkClaw identities. Agent-browser rebuilds its ref map on every snapshot, so
the same `@eN` can identify a different element after a later snapshot.

The adapter must preserve SparkClaw's stricter snapshot contract:

1. request an interactive, URL-aware agent-browser snapshot;
2. validate the structured `refs` object and bounded tree payload;
3. assign a new SparkClaw `snapshot_id` tied to the active page ID and URL;
4. build bounded control descriptors and semantic fingerprints from role,
   accessible name, tree context, URL when present, and duplicate ordinal;
5. store a private mapping from each SparkClaw ref to the current raw `@eN`;
6. return only the fingerprinted SparkClaw refs to Workflow/model consumers;
7. reject a click/type/select unless snapshot ID, page ID, ref, fingerprint,
   active tab, and unused-action state all match;
8. invalidate the mapping after one successful mutating action or any later
   snapshot/navigation/tab/profile/presentation change.

Example boundary:

```text
model-visible:
  snapshot_42:e5:8bc8f32d...

adapter-private until invalidation:
  snapshot_42:e5:8bc8f32d... -> @e5
```

The adapter never invokes agent-browser's selector fallback for a SparkClaw ref.
Unknown, stale, detached, renumbered, ambiguous, hidden, covered, or disabled
targets fail explicitly and require a new `browser.snapshot`.

This rule specifically prevents a stale-ref failure in which `@e5` meant
"Drafts" in snapshot A, a new snapshot renumbered `@e5` to "Inbox", and a late
click silently activated Inbox. SparkClaw rejects the snapshot-A ref before it
reaches agent-browser.

The existing relevance ranking and bounded control projection remain
SparkClaw-owned. Raw full trees may be archived as untrusted evidence, while the
model-visible projection stays within current size limits. If the pinned
agent-browser structured snapshot schema is missing required semantics, the
adapter fails closed; it does not guess from arbitrary prose output.

## Login Handoff

The current collaborative login sequence remains unchanged at the product
level:

```text
hidden session detects a human-only challenge
  -> invalidate snapshots and close hidden session
  -> open the same managed profile in a visible session
  -> owner completes password/captcha/2FA outside chat
  -> capture the selected post-login URL
  -> close visible session and release the profile
  -> reopen the same profile headlessly
  -> navigate to the selected URL, reassess authentication, and resume
```

SparkClaw does not copy cookies, ask for credentials in chat, or accept a text
claim as login evidence. Profile-lock failure is explicit and never handled by
deleting Chromium lock files.

## Security And Policy

The following restrictions are mandatory:

- only the `core` MCP profile is enabled;
- only the fixed mapping above is callable by Adapter code;
- arbitrary CLI `extraArgs`, CSS/XPath selectors, CDP endpoints, auto-connect,
  provider plugins, extensions, state replay, auth vault, and agent-browser chat
  are rejected;
- public URL validation runs before navigation;
- profile roots and Chromium executable paths are config-owned and normalized;
- stderr and MCP payloads are bounded and redacted before traces or errors;
- screenshots and raw snapshots flow through ArtifactStore and existing
  redaction rules;
- mutating operations are never retried automatically;
- agent-browser confirmation prompts do not replace SparkClaw Approval.

Agent-browser's `allowed-domains` launch option cannot be the universal policy
layer for this migration because its containment mode intentionally rejects
Chrome profile reuse and restore/state replay. SparkClaw needs a persistent
managed profile for login continuity. Therefore SparkClaw's existing URL and
network trust checks remain authoritative. A future stateless browser profile
may add agent-browser domain containment as defense in depth under a separate
versioned contract.

## Errors, Timeouts, And Recovery

The adapter distinguishes at least these failure classes:

- executable/version/setup unavailable;
- MCP initialization or schema mismatch;
- daemon/session launch failure;
- managed profile locked;
- request timeout or canceled context;
- malformed or oversized MCP response;
- agent-browser process crash or EOF;
- navigation/business failure;
- stale or invalid SparkClaw snapshot ref;
- human verification required.

Every MCP request uses the caller deadline or configured adapter timeout. A
timeout, malformed response, ID mismatch, unexpected EOF, or MCP process crash
invalidates all session/page/snapshot state. Recovery closes the owned session
through a separate bounded command when possible, terminates the MCP process
group, and permits a later call to start a clean session.

Read-only startup is recoverable by a later caller. The adapter never retries a
click, fill, type, select, submit, or other mutating interaction automatically.
A late response from an abandoned process can never satisfy a request in a new
process generation.

## Expected Behavior Changes

The public tool and Workflow contracts do not change, but these observable
implementation details do:

- `provider` changes from `microsoft-playwright-*` to `agent-browser-*`;
- setup and doctor output report agent-browser and Chrome for Testing rather
  than Playwright and Playwright Chromium;
- low-level action error wording changes and is normalized into stable
  SparkClaw error codes;
- snapshot tree formatting may differ, while the structured SparkClaw snapshot
  schema and stale-ref guarantees remain stable;
- action readiness comes from agent-browser CDP behavior rather than
  Playwright Locator actionability.

No documentation or test should promise byte-identical provider text.
Behavioral tests should assert stable SparkClaw contracts and typed outcomes.

## Migration Sequence

### Phase 0: Freeze The Contract

1. Land this document and its Chinese mirror.
2. Record the existing browser unit, fixture, golden, visible-login, and real
   Chromium baseline.
3. Add a stale-ref renumbering regression fixture before changing providers.

### Phase 1: Dependency And Transport

1. Pin the validated agent-browser release in the root manifest and lockfile.
2. Update `setup:browser`, doctor, Docker image, Compose, and deployment docs.
3. Add the private bounded MCP stdio client and protocol fixture tests.
4. Add explicit version and required-tool schema validation.

### Phase 2: Adapter Parity

1. Implement `AgentBrowserAdapter` behind the existing `Adapter` interface.
2. Implement session, page, profile, presentation, read, snapshot, screenshot,
   interaction, timeout, and close normalization.
3. Preserve SparkClaw snapshot IDs and private raw-ref mapping.
4. Run provider-neutral ToolHub and Workflow suites against the new adapter.

### Phase 3: Cutover

1. Switch the default provider to `agent-browser`.
2. Run the full browser fixture and real managed-Chrome matrix.
3. Verify hidden/visible profile handoff and process cleanup on host and Docker.
4. Update architecture, development, browser roadmap, deployment, and operator
   documentation to describe the new backend.

### Phase 4: Remove Playwright

1. Delete `PlaywrightAdapter`, Playwright stdio code, and the embedded
   `playwright_driver.cjs`.
2. Remove the Playwright package, browser install, environment variables,
   Docker layers, provider strings, and Playwright-only tests.
3. Keep only provider-neutral tests plus agent-browser protocol and real-browser
   tests.
4. Confirm no production reference to Playwright remains.

The temporary implementation branch may support a test-only provider switch
during parity work. The merged final state has one production provider. There
is no runtime fallback that could hide an agent-browser failure.

## Verification Matrix

Required automated coverage:

| Area | Required evidence |
|---|---|
| MCP lifecycle | initialize, tool validation, request IDs, tool error, timeout, EOF, stderr bound, close |
| Mapping | every public browser tool maps to exactly one declared adapter path |
| Tabs | open/list/focus/close, popup reconciliation, stable page IDs |
| Snapshots | bounded controls, ranking, fingerprint, duplicate names, iframe behavior |
| Ref safety | resnapshot renumbering, stale ID, wrong page, reused action, navigation invalidation |
| Read | rendered HTML/text, Readability, lazy content, truncation, auth signals |
| Interaction | click/fill/type/select, covered target, detached target, post-action invalidation |
| Profiles | owner isolation, logical profile isolation, lock handling, persistence |
| Presentation | hidden/visible mutual exclusion and login handoff |
| Recovery | hung call, daemon crash, gateway shutdown, no owned Chrome left running |
| Security | URL rejection, no raw args/selectors/CDP/state import, redaction |
| Product | existing ToolHub, Workflow, gateway golden, and WebChat behavior |

Real-browser acceptance includes the existing managed Chromium scenario plus a
fixture that deliberately renumbers the same raw `@eN` between two snapshots.
The old SparkClaw ref must fail before agent-browser receives the click.

## Acceptance Criteria

The migration is complete only when all of the following are true:

- agent-browser is pinned and reproducibly installed on supported host and
  container architectures;
- Playwright has no production dependency, adapter, driver, setup, environment,
  provider string, or browser binary role;
- all public `browser.*` ToolHub schemas, risk levels, and Workflow exposure
  remain unchanged;
- Fast routes the same registered browser capability leaves as before;
- browser read, tab, snapshot, screenshot, wait, click, type, and select paths
  pass provider-neutral tests;
- raw agent-browser refs never cross the Adapter boundary;
- a successful action invalidates the snapshot and stale/renumbered refs fail
  closed;
- hidden and visible sessions reuse the same managed profile only serially;
- login state persists without exporting cookies or credentials;
- timeouts and shutdown leave no SparkClaw-owned MCP or Chrome process active;
- full Gateway tests, WebChat tests/build, doctor, mock golden eval, docs mirror
  checks, Docker config, and real-browser smoke pass.

## Rollback

Rollback is release-level, not an automatic runtime fallback. Keep the last
validated Playwright-based release artifact and state schema compatible during
the first agent-browser release. If a blocking regression appears, stop the
gateway, ensure the managed profile is not owned by an agent-browser session,
and deploy the prior release against the same profile root.

Do not run Playwright and agent-browser against the same managed profile at the
same time. Do not delete profile locks to force rollback.

## Upstream References

- [agent-browser repository](https://github.com/vercel-labs/agent-browser)
- [agent-browser MCP server](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#mcp-server)
- [agent-browser sessions and profiles](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#authentication)
- [agent-browser snapshot options](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#snapshot-options)
- [agent-browser architecture](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#architecture)
