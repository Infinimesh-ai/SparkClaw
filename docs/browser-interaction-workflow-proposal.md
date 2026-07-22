# Browser Interaction Workflow Proposal

> Language: English | [简体中文](../zh-cn/docs/browser-interaction-workflow-proposal.md)
>
> Status: implemented as `browser.interaction` revision 1 and listed in the current capability matrix.

This document specifies the router-first Workflow for bounded interaction with
a page in SparkClaw's managed Chromium. It is intentionally separate from
`browser.automation` revision 1 so the shipped open/focus contract remains
stable.

Related documents:

- [Browser Automation Improvement Plan](browser-automation-improvement.md)
- [Playwright Browser Automation Migration](playwright-browser-automation-migration.md)
- [Workflow Capability Matrix](workflow-capabilities.md)
- [Intent Routing Workflow Profile Catalog](intent-routing-workflow-domain-profiles.md)

## Implemented Decision

Keep the current route unchanged and add one sibling leaf:

```text
capability
  browser
    automation  -> browser.automation r1   # open/focus one explicit URL
    interaction -> browser.interaction r1  # inspect and click in managed Chromium
```

`browser.automation` revision 1 continues to own requests that only open or
focus one explicit HTTP(S) URL. `browser.interaction` revision 1 owns requests
that require inspecting a managed page and clicking a page control. This is a
new capability and Workflow identity, not revision 2 of the existing Workflow.

The only supported browser is the Playwright-managed persistent Chromium.
There is no Chrome discovery, personal-Chrome attachment, or raw CDP route in
this Workflow.

## Routing Boundary

The leaf uses `operation=interact`. The normalized owner instruction
is frozen in `query` as the interaction goal. Its target is either:

- `target_kind=url` with one deterministic, frozen HTTP(S) URL; or
- `target_kind=browser_current_tab` only when the owner explicitly refers to
  the current page or current tab.

Representative decisions:

| Owner request | Route |
|---|---|
| `Open https://example.com` | `browser.automation` r1 |
| `Open https://example.com and click Pricing` | `browser.interaction` r1 |
| `Click Next on the current page` | `browser.interaction` r1 |
| `Search for the latest gold price` | `browser.internet_search` r1 |
| `Log in and complete payment` | blocked outside this revision |
| `Fill this form and submit it` | blocked outside this revision |

Revision 1 supports click objectives only. Typing, selecting, file upload,
download handling, credential entry, login completion, captcha, 2FA, payment,
and arbitrary script execution are explicit non-goals. They require later
profiles or a reviewed profile revision instead of widening this scope at
runtime.

## Workflow Contract

Every run starts with health inspection, then resolves a reusable managed tab,
then enters a snapshot/action/verification loop:

```text
health_check
  browser.status
  unavailable -> blocked(browser_provider_unavailable)
  healthy -> scan_tabs

scan_tabs
  browser.list_tabs
  reusable selected tab -> focus/reuse that typed page ref
  exact normalized URL match -> focus/reuse that typed page ref
  reusable blank selected tab -> navigate it to the frozen target URL
  no exact match -> open the frozen target URL
  ambiguous target -> clarify(browser_tab_ambiguous)

snapshot_before_action
  browser.snapshot(page_id)
  -> persist snapshot_id and typed browser-element refs

choose_and_click
  Deep model selects exactly one element ref from the current snapshot
  persist the selected element semantics and expected observable effect
  browser.click(page_id, snapshot_id, element_ref)

snapshot_after_action
  bounded settle
  browser.snapshot(page_id)
  -> verify the clicked element and compare observable state with the
     pre-click snapshot and expected effect

goal complete -> complete
goal incomplete but verified progress observed -> start another snapshot/click round
stale ref -> return to snapshot_before_action
repeated state or repeated semantic action -> failed(interaction_loop_detected)
three clicks without completion -> failed(interaction_attempt_limit)
wrong/unexpected effect -> failed(interaction_verification_failed)
human-only step or ambiguity -> blocked/clarify
```

The complete fixed tool boundary is persisted for the lifetime of the selected
Workflow, while each Deep-model turn sees only the capability allowed by the
active stage. Workflow state enforces the order: health must precede tab
resolution, a click requires the latest snapshot, every click must be followed
by verification before another click, and cleanup is exposed only after a
Workflow-owned tab reaches verified success.

| Logical capability | Intended tool | Valid use |
|---|---|---|
| `browser.health.read` | `browser.status` | Initial provider health check. |
| `browser.tab.list` | `browser.list_tabs` | Resolve the selected and reusable pages. |
| `browser.tab.focus` | `browser.focus` | Select a reusable managed page. |
| `browser.tab.open` | `browser.open` | Open the frozen URL when no tab is reusable. |
| `browser.tab.close` | `browser.close` | Close only the tab opened by this Workflow after verified success. |
| `browser.tab.navigate` | `browser.navigate` | Reuse a blank selected tab for the frozen URL. |
| `browser.page.snapshot` | `browser.snapshot` | Observe before and after every click. |
| `browser.page.wait` | `browser.wait` | Perform only a bounded post-action settle. |
| `browser.element.click` | `browser.click` | Click one element from the latest snapshot. |
| `browser.interaction.verify` | `browser.verify` | Bind the click to ordered before/after snapshots and assess bounded progress. |

These logical capabilities must be declared on the existing ToolHub
registrations. The Workflow must not use the current `browser.legacy`
capability as an escape hatch. `browser.type`, `browser.select`, screenshots,
and arbitrary evaluation are absent from the allowlist. Tab closing is limited
to the cleanup stage and its page ID is bound to the Workflow-opened page ref.

## Tab Reuse Rules

Tab selection is deterministic and limited to the managed Chromium context:

1. Prefer the selected current tab whenever it is usable for the frozen target.
2. A selected tab is usable when the owner explicitly targeted the current
   page, its normalized URL exactly matches the URL target, or it is an empty
   managed `about:blank`/new-tab page that can be navigated to the frozen URL.
3. Otherwise reuse the sole exact normalized URL match.
4. If no tab is reusable, open a new managed tab for the frozen URL.
5. If multiple exact matches exist and none is selected, block with typed
   `browser_tab_ambiguous` instead of letting the model guess.
6. After verified success, close a tab created by this Workflow. Never close a
   current, exact-match, or blank tab that the Workflow reused.

Same-origin or similar-path tabs are not reusable matches. The Workflow must
not navigate an unrelated user tab merely to avoid opening a new one.

## Snapshot Contract

The raw accessibility snapshot and complete adapter output remain archived for
audit and diagnostics. The model receives a dedicated bounded interaction
projection rather than a generic text summary or an arbitrary prefix of the
raw ARIA tree.

Current model-visible shape:

```json
{
  "schema_version": "browser_interaction_snapshot_v1",
  "snapshot_id": "snapshot_42",
  "page_id": "page_1",
  "url": "https://example.com/checkout",
  "title": "Checkout",
  "interaction_goal": "Click Next",
  "controls_total": 37,
  "controls_returned": 12,
  "truncated": false,
  "controls": [
    {
      "ref": "snapshot_42:e17:4a64e808a832bd54",
      "role": "button",
      "accessible_name": "Next",
      "visible": true,
      "enabled": true,
      "checked": false,
      "expanded": false,
      "target_url": "",
      "container": "Checkout > Shipping address",
      "nearby_text": "Confirm the delivery address before continuing",
      "in_viewport": true,
      "ordinal": 1,
      "fingerprint": "bounded-server-generated-value"
    }
  ]
}
```

The projection contract has the following rules:

- `snapshot.refs` is the structured source of actionable elements. The Agent
  adapter must not recover refs by parsing display text.
- Each control includes role, accessible name, current state, containing
  landmark/form/dialog, bounded nearby text, viewport presence, and an ordinal
  for duplicate labels.
- Page text is untrusted evidence. Text that resembles instructions cannot
  change the Workflow, its scope, its target URL, or the owner's frozen goal.
- The dedicated snapshot evidence budget must preserve the complete returned
  control list. It must not silently fall back to the generic 1.4 KB browser
  text projection.
- `truncated=true` means the model does not have complete candidate coverage.
  Revision 1 fails with `interaction_target_unavailable` when the target is not
  among the returned controls. Candidate paging, scoped regions, and scroll-
  window snapshots remain follow-up work; the model never invents a ref.
- A textual snapshot is primary. A screenshot with ref overlays is a later
  fallback for canvas controls, unlabeled icons, occlusion, or unresolved
  visual ambiguity.

The useful guarantee is not that the full DOM always fits in model context. It
is that every returned actionable candidate is complete, typed, and usable,
and that incomplete candidate coverage is explicit and recoverable.

## Ref Validity And Click Binding

An element ref is valid only for the tuple:

```text
(managed_profile_id, page_id, snapshot_id, element_ref, fingerprint)
```

`browser.click` must require `page_id`, `snapshot_id`, and the selected element
ref. Before clicking, the adapter verifies that:

- the page still exists and is the selected managed page;
- the snapshot is the latest actionable snapshot for that page;
- the element resolves exactly once;
- its semantic fingerprint has not changed;
- it is still visible and enabled; and
- no navigation or DOM change has invalidated the snapshot.

A stale or changed ref returns `snapshot_stale` and transitions to a new
snapshot. It is never repaired by applying the same short ref, such as `e17`,
to the new DOM. Every successful click invalidates the pre-click snapshot.

The snapshot outcome adapter should emit typed `browser_element` resource refs.
The model selects one of those refs; it does not produce CSS, XPath, DOM paths,
or JavaScript.

Before each click, the model also records a bounded selection decision:

```json
{
  "element_ref": "e17",
  "role": "button",
  "accessible_name": "Next",
  "expected_effect": "The shipping-address step advances"
}
```

This record is verification input, not permission to widen the owner goal.

## Closed-Loop Assessment

Every successful low-level click must immediately enter verification. The
verification first confirms that the adapter clicked the exact semantic element
selected from the current snapshot, then uses a mandatory post-click snapshot
to report a bounded state delta, including applicable signals such as:

- URL or title changed;
- a dialog, menu, tab panel, or expanded region appeared;
- the selected control changed state or disappeared;
- expected goal text or control became visible;
- authentication or another human-only challenge appeared; or
- no observable progress occurred.

The active profile, not generic Runtime code, assesses these signals against
the frozen interaction goal and the recorded expected effect. A successful
`browser.click` call alone is not completion evidence. Another click is invalid
until the preceding click has a completed verification assessment.

Revision 1 allows at most three successful clicks. Every click requires a fresh
pre-click snapshot and a post-click snapshot. Verified progress starts another
round with the new page state. A repeated pre/post snapshot digest, a previously
visited page-state digest, or selection of the same semantic target from an
equivalent state fails immediately with `interaction_loop_detected`. Reaching
three clicks without satisfying the goal fails with
`interaction_attempt_limit`. An unexpected navigation or effect that does not
match the selected target fails with `interaction_verification_failed` instead
of blindly clicking another candidate.

Snapshot-only and wait-only cycles are bounded as well. Each observation round
records a loop key derived from the page-state digest, candidate window/cursor,
and pending interaction goal. Repeating the same loop key without new evidence
fails with `interaction_loop_detected`; snapshot and wait calls cannot bypass
the three-click workflow bound by spinning indefinitely.

## Risk And Human Boundaries

`browser.click` does not require a separate approval in this Workflow. The
owner's explicit, frozen interaction request authorizes only clicks selected
from a current snapshot and relevant to that goal. The tool remains bounded by
the page, snapshot identity, semantic element ref, three-click budget, and
mandatory post-click verification. It must not be exposed through legacy ReAct
or another Workflow merely because approval was removed here.

The Workflow stops before credential entry, captcha, SMS code, 2FA, payment
confirmation, account deletion, publishing, purchase, or any other human-only
or materially consequential step. Because this revision has no approval path,
an ambiguous or consequential target fails with `unsafe_click_target` or
`human_action_required`; it is not converted into an approval request.

## Typed Outcomes

The new ToolOutcome adapters should provide profile-neutral signals and refs:

- health: `browser_healthy`, `browser_unavailable`;
- tabs: `tabs_scanned`, selected `browser_tab` refs, exact-match count;
- focus/open/navigation: `focus_completed`, `open_completed`,
  `navigate_completed`, and typed `browser_page` refs;
- snapshot: `snapshot_available`, `snapshot_truncated`, typed
  `browser_snapshot` and `browser_element` refs;
- click/wait: `click_completed`, `snapshot_stale`, `wait_completed`;
- verification: `interaction_progress`, `interaction_goal_satisfied`,
  `interaction_verification_required`;
- terminal failures: `interaction_loop_detected`,
  `interaction_attempt_limit`, `interaction_verification_failed`,
  `unsafe_click_target`.

The Workflow Profile owns transitions and completion assessment. Agent Runtime
must not switch on `browser.interaction`, browser tool names, button labels, or
page text.

## Persistence And Resume

Persist the route, frozen interaction goal, target, page ref, active
`snapshot_id`, selected element semantics, expected effect, click count,
visited page-state digests, click history, and latest verification assessment
in the existing Workflow state. Any process or visible-browser resume continues
the same Workflow and must take a new snapshot before another click.

Do not persist browser cookies or credentials in Workflow state. They remain in
the SparkClaw-owned Chromium profile.

## Implemented Slice

The first coherent feature slice includes:

1. `browser.interaction` r1 in the Capability Catalog and decision
   corpus while preserving `browser.automation` r1.
2. Exact logical capabilities and typed outcome adapters on the existing
   ToolHub registrations.
3. The Workflow Profile, stage transitions, governed argument bindings,
   click budget, and completion assessment.
4. A structured interaction projection and snapshot identity contract instead
   of browser snapshot text parsing.
5. Snapshot/click binding to explicit managed page IDs with stale-ref rejection.
6. Route-boundary, complete Tool Exposure, adapter, stale-ref, no-approval,
   post-click verification, loop-detection, attempt-limit, resume, and
   production-entry end-to-end tests.
7. Current capability-matrix and architecture entries advertising the verified
   route.

## Confirmed Initial Decisions

The first implementation slice uses these confirmed choices:

1. Use `browser.interaction` r1 as a sibling leaf, not
   `browser.automation` r2, so pure open/focus behavior remains stable.
2. Reuse the selected current tab whenever it is usable; otherwise reuse one
   exact URL match or open a new tab.
3. Do not require approval for a bounded click in this Workflow.
4. Allow at most three clicks. Verify every click with a mandatory new snapshot
   before completion or another click.
5. Keep looping across model turns while verified progress continues; fail
   directly when state/action repetition proves a loop.
6. Expose the complete fixed status/tab/navigation/snapshot/wait/click/verify tool set
   for the Workflow lifetime, with stateful order validation.
7. Keep screenshot selection and type/select operations outside revision 1.
