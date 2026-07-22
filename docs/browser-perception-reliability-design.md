# Browser Perception Reliability Optimization Design

> Language: English | [简体中文](../zh-cn/docs/browser-perception-reliability-design.md)
>
> Status: proposed implementation design. This document improves the internals
> of the existing `browser.interaction` revision 1 contract; it does not add a
> capability leaf, Workflow, tool, browser provider, or policy path.

## Decision

Improve SparkClaw's existing Playwright snapshot implementation with a bounded,
multi-source perception pipeline inside the current Node driver. The enhanced
pipeline combines DOM semantics, accessibility evidence, layout state, frame
context, and generic clickability signals before producing the same bounded
`browser_interaction_snapshot_v1` projection.

Keep every architectural boundary already in production:

- Fast still selects only the registered `browser.interaction` leaf.
- The existing `browser.interaction` revision 1 profile still owns stage order.
- ToolHub remains the model-visibility, risk, ref-binding, and verification
  authority.
- Go still owns one bounded, cancellable Node driver process.
- The Node driver still owns one Playwright persistent Chromium context.
- `browser.snapshot`, `browser.click`, and `browser.verify` keep their public
  names and required arguments.
- A click still requires a current page, current snapshot, exact element ref,
  matching fingerprint, and Playwright actionability.
- Every successful click still invalidates its source snapshot and requires a
  new snapshot plus verification before another click.

The upstream `browser-use` project is an implementation reference, not a new
runtime dependency or an alternative agent architecture.

## Goals

1. Discover interactive controls implemented as custom SPA components rather
   than only native elements or `cursor: pointer` nodes.
2. Observe controls in bounded same-origin and cross-origin frames and open
   shadow roots.
3. Reduce false candidates caused by hidden, disabled, pointer-inert, or
   visually covered elements.
4. Preserve semantic continuity across normal SPA rerenders without allowing
   an old snapshot ref to authorize a new element.
5. Improve domain-neutral authentication assessment using the same structured
   page evidence used by interaction snapshots.
6. Give `browser.verify` stable, observable state deltas instead of treating
   any transient DOM text change as progress.
7. Preserve deterministic behavior, bounded latency, explicit degradation,
   and the existing untrusted-evidence boundary.

## Non-Goals

This design does not:

- replace the capability tree with a general-purpose browser Agent;
- add another browser provider, MCP process, remote-debugging endpoint, or
  `connectOverCDP` path;
- expose CDP, CSS selectors, XPath, JavaScript, backend node IDs, or coordinates
  to the model;
- add typing, selecting, form submission, uploads, downloads, screenshots,
  login completion, credential entry, captcha solving, payment, or account
  mutation to `browser.interaction` revision 1;
- introduce site-, mailbox-, or host-specific production rules;
- make coordinate clicking or visual clicking a fallback;
- batch multiple clicks into one tool call;
- use an LLM judge as completion authority;
- silently repair or reuse a stale ref;
- change the current 24-control model projection or add candidate paging in the
  first implementation slice. Paging remains a separate item in the browser
  improvement roadmap.

## Current Implementation And Failure Modes

The current snapshot implementation in
`services/gateway/internal/browserautomation/scripts/playwright_driver.cjs`
does the following:

1. Queries the selected page's main `body` for native controls and a fixed set
   of ARIA roles.
2. Adds visible elements whose computed cursor is `pointer`, preferring the
   deepest same-label pointer node.
3. Assigns `data-sparkclaw-ref` attributes in DOM order.
4. Builds accessible names from labels, `aria-label`, text, placeholder, title,
   or name.
5. Applies a deterministic lexical goal score and returns the top 24 controls.
6. Resolves a click through the injected attribute, recomputes a semantic
   fingerprint, and delegates actionability to `locator.click()`.

This is a sound bounded baseline, but it leaves reusable gaps:

- JavaScript-listener controls can be missed when they have no recognized role
  and no pointer cursor.
- Main-document queries do not provide complete frame or shadow-root coverage.
- Geometry and CSS visibility do not prove that a control is hit-testable or
  not covered by another layer.
- Injected attributes disappear when a framework replaces the node, even when
  the new node is semantically equivalent.
- The current digest includes volatile page evidence, so clocks, counters,
  rotating content, or unrelated live regions can look like useful progress.
- Authentication assessment and interaction candidate discovery perform
  separate DOM scans and can disagree about the visible application surface.
- The lexical score improves ordering but cannot recover a control that was
  never collected.

The primary problem is therefore candidate recall and evidence quality, not a
missing learned reranker.

## Preserved Runtime Shape

```text
browser.interaction r1
  -> existing Workflow stage gate
  -> existing browser.snapshot registration
  -> PlaywrightAdapter.Call
  -> same newline-delimited JSON driver protocol
  -> same Node driver and persistent Chromium context
       -> enhanced bounded perception collector
       -> same snapshot state/ref table
  -> same browserautomation.Result
  -> existing ToolOutcome adapter
  -> existing Workflow assessment
  -> browser.click through Playwright Locator
  -> post-click browser.snapshot
  -> existing browser.verify boundary
```

Using a Playwright-created, page-scoped CDP session inside the local Node driver
is permitted only as an observation implementation detail. It must not create a
remote-debugging port, a public CDP route, a second browser connection, or a
model-visible tool.

## Enhanced Perception Pipeline

### 1. Bounded Frame Inventory

Start from the selected Playwright `Page` and enumerate its frame tree in a
stable parent-before-child order. Record an internal opaque frame reference,
depth, URL origin, viewport intersection, and owner frame element bounds.

Initial hard limits:

| Budget | Initial limit | Behavior when exceeded |
|---|---:|---|
| Frame documents | 20 | Process visible shallow frames first; mark the snapshot truncated. |
| Frame depth | 4 | Skip deeper descendants; mark the snapshot truncated. |
| DOM nodes inspected | 20,000 | Stop collection deterministically; mark degradation. |
| Internal candidates | 1,000 | Stop adding candidates; preserve collected order and mark truncated. |
| Model-visible controls | 24 | Keep the current contract and report omitted counts. |
| Nearby text per control | 320 characters | Keep the current bound. |

Invisible or tiny third-party frames are not processed before a large visible
application frame. Frame ordering must not depend on network completion order.

### 2. Multi-Source Capture

Collect bounded evidence from the existing Playwright session:

- DOM structure, including open shadow roots;
- accessibility role, name, ignored state, focusability, editability, and
  interactive state where Chromium exposes it;
- layout bounds, computed `display`, `visibility`, `opacity`, `pointer-events`,
  and cursor;
- native element semantics and ARIA attributes;
- the presence, but never the source code, of click or pointer listeners;
- frame ownership and viewport position;
- current boolean state such as checked, selected, expanded, pressed, and
  disabled.

The collector may use Chromium's local `DOMSnapshot`, DOM, Accessibility, and
DOMDebugger domains through a Playwright CDP session. Calls should be issued in
parallel under one sub-deadline. Failure of an enhancement source must not
crash or reset an otherwise healthy Playwright session.

No page-supplied script is generated from model text. All evaluation snippets
are static, repository-owned collector code.

### 3. Internal Node Merge

Merge sources by an internal backend-node identity plus frame identity. The
merge produces one internal descriptor with:

- frame reference and depth;
- backend node identity, retained only inside the driver;
- tag, type, semantic role, accessible name, and role/name source;
- container landmark and bounded nearby text;
- layout bounds and viewport relation;
- enabled and interactive state;
- generic clickability signals;
- hit-test and occlusion state;
- stable DOM order;
- current fingerprint and cross-snapshot continuity key;
- an exact Playwright resolver owned by the current snapshot state.

Missing evidence stays missing. The collector must not invent an accessible
name, role, login state, or click listener based on a host name or product name.

### 4. Generic Candidate Eligibility

A node becomes an interaction candidate when at least one positive signal is
present:

- native interactive element;
- interactive ARIA or accessibility role;
- focusable, editable, or settable accessibility state;
- explicit click, mouse, pointer, or keyboard activation listener;
- enabled form-control wrapper such as a label around an input;
- positive `tabindex` or content-editable state;
- pointer cursor as the lowest-confidence fallback.

The following conditions exclude or downgrade the node:

- `display: none`, hidden visibility, zero opacity, `aria-hidden=true`, or no
  usable layout bounds;
- disabled native or ARIA state;
- `pointer-events: none` without an actionable descendant;
- complete coverage by an opaque higher paint-order element;
- an interactive ancestor or descendant already representing the same name,
  role, bounds, and action surface;
- decorative SVG or icon content without another interaction signal.

This intentionally excludes upstream heuristics based on product words, search
class names, or guessed icon dimensions. Candidate discovery must remain
domain-neutral.

### 5. Visibility And Hit Testing

Snapshot visibility and click-time actionability are separate guarantees:

- Snapshot collection uses computed style, bounds, parent-frame visibility,
  paint order, and bounded hit testing to rank or exclude obviously unusable
  controls.
- Immediately before a click, the driver still resolves exactly one element,
  checks the current fingerprint, and relies on Playwright actionability and
  auto-waiting.

An element that is partially visible remains eligible when at least one tested
point is hit-testable. An element with uncertain occlusion may be returned with
`hit_testable=false` only when another strong semantic signal exists; it ranks
below actionable candidates and cannot bypass click-time actionability.

## Backward-Compatible Snapshot Contract

Keep `schema_version=browser_interaction_snapshot_v1` and every required field
already consumed by ToolHub and the Workflow. Add optional evidence fields:

```json
{
  "schema_version": "browser_interaction_snapshot_v1",
  "snapshot_id": "snapshot_1_8",
  "page_id": "page_1",
  "digest": "legacy-compatible-digest",
  "verification_digest": "stable-semantic-state-digest",
  "perception": {
    "version": "hybrid_dom_ax_v1",
    "status": "complete",
    "degradation_reasons": [],
    "frames_seen": 3,
    "frames_processed": 3,
    "nodes_inspected": 8120,
    "candidates_total": 42
  },
  "controls": [
    {
      "ref": "snapshot_1_8:e7:0123456789abcdef",
      "short_ref": "e7",
      "role": "menuitem",
      "accessible_name": "Drafts",
      "frame_ref": "f0",
      "click_signals": ["ax_role", "js_listener"],
      "hit_testable": true,
      "occluded": false,
      "continuity_key": "bounded-semantic-value",
      "fingerprint": "bounded-current-snapshot-value"
    }
  ]
}
```

Rules:

- `fingerprint` remains the click-binding value for the current snapshot.
- `continuity_key` is comparison evidence only. It never authorizes a click.
- `frame_ref` is opaque and scoped to the current snapshot. It is not a CDP
  target ID or origin credential.
- `click_signals` contains a small registered vocabulary, not arbitrary page
  strings.
- `perception.status` is `complete` or `degraded`. Degradation is explicit and
  bounded.
- `truncated=true` when any frame, node, candidate, or model-projection budget
  prevents complete candidate coverage.
- Existing consumers may ignore every new field.

## Deterministic Candidate Ranking

Keep ranking deterministic and local. Do not call the embedding, reranker,
Fast, or Deep lanes from the browser adapter.

Rank only eligible candidates, in this priority order:

1. normalized accessible name is contained in the frozen goal, or the goal is
   contained in the name;
2. accessible-name phrase and token overlap, with Unicode normalization and a
   bounded character fallback for languages without whitespace segmentation;
3. matching container or nearby-text evidence;
4. strong interaction signals: native semantics, accessibility role, or event
   listener;
5. hit-testable and in-viewport state;
6. role suitability as a tie-breaker;
7. stable frame order and DOM order.

Negative weight applies to disabled, uncertainly occluded, unnamed, generic,
or weak pointer-only candidates. Diversity selection prevents all 24 returned
controls from being consumed by duplicate same-name nodes in one container when
other relevant containers exist.

Ranking affects only which current-snapshot candidates are shown to the model.
It cannot make an ineligible node clickable, bypass ToolHub risk checks, or
resolve a stale ref.

## Ref Validity And Rerender Continuity

The existing strict ref tuple remains authoritative:

```text
(managed_profile_id, page_id, snapshot_id, element_ref, fingerprint)
```

If the injected attribute, frame, backend node, resolver, or fingerprint is
missing or changed at click time, return `snapshot_stale`. Do not search the
current DOM and click a semantic replacement under the old ref.

Stable identity is used only after a new snapshot exists:

- correlate before/after controls for verification;
- recognize that a selected menu item became active or disappeared;
- improve the ranking of a semantically equivalent target after a stale-ref
  transition;
- detect repeated semantic actions from equivalent page states.

A continuity key should be derived from normalized role, accessible name,
stable attributes, container path, frame path, target URL class, and duplicate
ordinal. Dynamic classes, focus/hover/loading state, generated snapshot attrs,
screen coordinates, and volatile text are excluded.

An ambiguous continuity match remains ambiguous. It must not select a click
target automatically.

## Authentication Assessment Reuse

Replace the separate broad main-document scan with a pure classifier over the
enhanced perception result. Keep the existing `unknown`, `challenged`, and
`authenticated` states and the evidence priority defined in the Browser Login
State Operation Guide.

Enhancements:

- inspect visible controls inside processed frames and open shadow roots;
- distinguish main application surfaces from small embedded widgets;
- report the source frame and evidence class internally;
- treat a visible challenge in the main frame or a large active modal/frame as
  stronger than login text in unrelated navigation;
- treat sign-out, identity, account, and usable application-shell evidence as
  positive only when it is visible and structurally contextual;
- preserve conflicting strong signals as `unknown`;
- never use hostnames, mailbox names, cookie names, or hidden text as rules.

Authentication state is still evidence, not permission. It does not widen the
Workflow or permit credential entry.

## Stable Verification Evidence

Preserve the existing `browser.verify` tool and ordered before-click-after
binding. Add a normalized `verification_digest` and structured deltas to the
archived snapshot output.

The verification digest includes:

- normalized URL and title;
- processed frame topology;
- control continuity keys and relevant boolean states;
- visible dialog/menu/tab/expanded-region structure;
- stable goal-relevant text evidence when present.

It excludes:

- timestamps, counters, animation state, rotating banners, and live-region
  noise unrelated to the goal;
- dynamic CSS classes and generated refs;
- absolute coordinates unless layout movement is the expected effect;
- hidden script, style, template, or metadata text.

ToolHub should prefer `verification_digest` when both snapshots provide it and
fall back to the existing `digest` for old or degraded snapshots. State change
alone remains insufficient for success. The bound selected element, expected
effect, normalized delta, risk rules, click count, and model verdict still pass
through the existing deterministic ToolHub checks.

Useful structured deltas include:

- URL or title change;
- selected control disappeared or changed checked/selected/expanded/pressed
  state;
- a goal-relevant control or region appeared;
- dialog, menu, or tab-panel state changed;
- explicit authentication challenge appeared;
- no stable observable change occurred.

## Degradation And Error Semantics

The enhanced collector is additive. It must degrade to the current DOM-based
collector when CDP accessibility, snapshot, listener, or frame evidence is
unavailable, while reporting the reason in `perception`.

Degradation rules:

- Never reset a healthy browser session solely because one observation source
  failed.
- Never report complete coverage after a frame, node, candidate, or time budget
  was exceeded.
- Never turn a failed enhancement into an empty successful snapshot when the
  legacy collector still has valid candidates.
- Never allow degraded evidence to weaken click-time fingerprint and
  actionability checks.
- Return an explicit snapshot failure when both enhanced and legacy collection
  fail or the driver cannot produce a bounded result.

The collector receives a sub-deadline inside the existing adapter request
deadline. It does not add retries for clicks or another long-lived worker.

## Ownership By Existing Module

| Existing module | Responsibility after optimization |
|---|---|
| `browserautomation/scripts/playwright_driver.cjs` | Frame inventory, multi-source collection, candidate merge/ranking, snapshot-local resolver state, click-time validation, and page auth evidence. |
| `browserautomation/playwright_stdio.go` | Unchanged process ownership, embedded driver startup, deadlines, protocol framing, and cleanup. |
| `browserautomation/adapter.go` | Unchanged public tool mapping and provider-neutral result envelope; no site rules. |
| `toolhub/browser_interaction.go` | Existing unsafe-target guard, current-run ref binding, ordered snapshot/click verification, and stable-digest preference. |
| `agent/workflow_outcome.go` | Additive projection of registered snapshot attributes into typed refs. |
| `agent/browser_interaction_workflow.go` | Existing stages, three-click limit, loop handling, and completion boundary. No new stage topology. |
| Artifact and trace path | Archive complete bounded observation and expose only the model projection, under existing redaction and owner scope. |

If the driver would exceed the engineering baseline's soft file-size limit,
collector helpers should be split mechanically while remaining embedded into
the same Node process. That split is code organization, not a service or
runtime boundary.

## Security And Privacy

- All DOM, accessibility, frame, listener, and layout content is untrusted
  external evidence.
- Never collect password values, hidden input values, cookies, storage,
  authorization headers, listener source code, or page JavaScript bodies.
- Text input values are excluded from model-visible controls; boolean state is
  allowed where required for verification.
- Password and verification fields may contribute only their presence, role,
  visibility, and structural context to authentication assessment.
- Raw backend node and frame target IDs remain driver-internal.
- Cross-origin content receives the same bounds and untrusted treatment as the
  main page.
- Page content cannot change launch options, allowed domains, Workflow scope,
  Policy, approval, or tool arguments.
- Unsafe labels are still rejected in ToolHub before reaching the adapter.

## Implementation Sequence

### Phase 0: Baseline And Fixtures

1. Record current snapshot latency, candidate counts, target-unavailable rate,
   stale-ref rate, and auth-assessment outcomes on deterministic fixtures.
2. Extract pure current collector, naming, fingerprint, and ranking helpers
   without changing output.
3. Keep the real Chromium smoke test green before behavior changes.

### Phase 1: Candidate Recall And Actionability

1. Add bounded frame and open-shadow-root inventory.
2. Add accessibility and listener-backed interaction signals.
3. Add pointer-events, parent-frame visibility, occlusion, and hit-test
   evidence.
4. Preserve the current schema and 24-control projection with optional
   perception fields.
5. Fall back explicitly to the legacy collector when enhancement sources fail.

### Phase 2: Continuity, Ranking, And Authentication

1. Add continuity keys that cannot authorize clicks.
2. Replace numeric ad hoc ordering with the documented deterministic priority
   and duplicate diversity rule.
3. Reuse structured perception evidence in the existing page-auth classifier.
4. Keep conflict handling and login handoff unchanged.

### Phase 3: Verification Hardening

1. Add `verification_digest` and structured state deltas.
2. Make ToolHub prefer the stable digest with legacy fallback.
3. Prove that unrelated live-page churn does not count as progress while
   selected/expanded/navigation changes do.
4. Keep the existing click count, loop, and completion semantics.

Candidate paging, screenshots, coordinate actions, typing, selection, and
download handling require separate reviewed changes and are not pulled into
these phases.

## Validation Plan

### Collector Fixtures

- native button, link, input, select, and ARIA controls;
- event-listener `div` without a pointer cursor;
- nested wrapper and leaf controls without duplicate action surfaces;
- open shadow-root control;
- same-origin and cross-origin frame controls;
- invisible, disabled, `pointer-events:none`, and overlay-covered controls;
- duplicate labels in different landmarks or dialogs;
- Chinese and English interaction goals;
- more than 24 relevant and irrelevant candidates with deterministic ordering;
- frame, node, candidate, and time-budget degradation.

### Ref And Verification Fixtures

- unchanged node accepts its current ref;
- rerendered node rejects the old ref with `snapshot_stale`;
- a new snapshot may correlate the replacement but produces a new ref;
- ambiguous continuity never selects an element;
- selected, checked, expanded, dialog, URL, and title changes produce stable
  deltas;
- clock/live-region changes alone do not produce progress;
- repeated stable state still returns `interaction_loop_detected`;
- the third unfinished click still returns `interaction_attempt_limit`.

### Authentication Fixtures

- authenticated application shell with identity/account controls;
- hidden or unrelated login text inside an authenticated SPA;
- visible folder-unlock or account-settings password input without a login
  form;
- real visible login form and explicit action;
- challenge or account control inside a visible frame or open shadow root;
- small unrelated embedded sign-in widget;
- conflicting challenge and authenticated evidence returns `unknown`.

### Required Commands

```bash
go test ./services/gateway/internal/browserautomation -count=1
go test ./services/gateway/internal/toolhub -count=1
go test ./services/gateway/internal/agent -count=1
SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 \
  go test ./services/gateway/internal/browserautomation \
  -run TestRealChromiumSnapshotAndLocatorInteractions -count=1
go test ./services/gateway/...
git diff --check
```

The real-browser fixture must exercise a listener-only control, an open shadow
root, a frame, an overlay, a rerendered stale ref, and the existing generic
authenticated-application case. A manual QQ Mail run may be used as additional
evidence, but no credentialed third-party site is an acceptance dependency.

## Observability

Add bounded diagnostic fields to the archived snapshot and existing browser
tool observation; do not create another telemetry system:

- perception version and status;
- capture duration by DOM, accessibility, layout, and merge stage;
- frames seen, processed, and skipped by reason;
- nodes inspected;
- candidates by positive signal;
- candidates excluded by visibility, disabled state, deduplication, or
  occlusion;
- model-visible and omitted candidate counts;
- legacy fallback reason;
- click resolution outcome: exact, stale, ambiguous, or actionability failure;
- authentication state, confidence, and registered evidence signals.

Do not log raw page text, form values, full URLs containing secrets, cookies,
storage, or listener bodies.

## Acceptance Criteria

1. Capability Catalog revision, registered leaf set, Workflow ID/revision,
   fixed tool scope, public tool names, risk levels, and approval behavior are
   unchanged.
2. The same Go adapter and Node driver process own the browser; no browser-use,
   MCP, remote CDP, cloud-browser, or second-process dependency is introduced.
3. Existing snapshot consumers continue to work when optional fields are
   absent or present.
4. Listener-only, framed, and open-shadow-root controls are discovered in the
   deterministic fixture suite.
5. Covered, hidden, disabled, and pointer-inert controls cannot bypass
   click-time Playwright actionability.
6. Old refs remain invalid after action or rerender; continuity metadata never
   authorizes a click.
7. Authentication decisions remain domain-neutral and preserve conflict as
   `unknown`.
8. Stable verification ignores unrelated volatile churn and still detects
   meaningful control, region, and navigation changes.
9. All snapshot work remains within the existing request timeout, budgets are
   explicit, and partial coverage is never reported as complete.
10. Focused tests, the real Chromium smoke test, full Gateway tests, and docs
    mirror validation pass.

## Upstream References

The following `browser-use` mechanisms informed the perception design but are
not copied as an Agent runtime:

- [DOM, accessibility, snapshot, and device-ratio collection](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/dom/service.py#L560-L668)
- [Layout, clickability, pointer-events, bounds, and paint-order extraction](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/dom/enhanced_snapshot.py#L46-L176)
- [Generic interactive-element signal combination](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/dom/serializer/clickable_elements.py#L39-L244)
- [Stable hashes and cascading history matching](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/agent/service.py#L3529-L3668)
- [Page-change guards for multi-action execution](https://github.com/browser-use/browser-use/blob/2be09b6c5eb702a9287684b42b27e7042a1aba29/browser_use/agent/service.py#L2719-L2818)

SparkClaw adopts the observation lessons while retaining stricter ref expiry,
one-click stages, deterministic verification, local process ownership, and
registration-driven capability boundaries.
