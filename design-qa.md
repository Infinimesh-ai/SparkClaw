# Design QA — Email conversation reply

[简体中文](zh-cn/design-qa.md)

- Source visual truth path: `http://localhost:3000/` (JingSi-mail, browser-native capture of the inbox conversation detail)
- Implementation screenshot path: `http://127.0.0.1:4173/email-design-preview.html` (browser-native screenshot evidence captured inline by CUA; the browser API did not expose a filesystem screenshot path)
- Normalized viewport: 1280 × 720 CSS px, source 1280 × 720 px, implementation 1280 × 720 px, device pixel ratio 1 for both
- State: selected conversation, chronological per-message summaries with source bodies in secondary details, empty reply-intent composer; focused checks also covered an editable polished draft with one direct “Send reply” action

## Findings

No actionable P0, P1, or P2 findings remain.

- Typography: the implementation retains SparkClaw's Inter/system stack, compact dark-theme hierarchy, and existing weights rather than copying JingSi-mail's light product skin. Title, sender metadata, message summaries, composer helper text, and buttons remain legible without broken wrapping. This is an intentional product-token difference, not design drift.
- Spacing and layout: the normalized desktop comparison preserves the reference hierarchy of list → selected conversation → chronological message summaries → bottom reply composer. The composer is a sibling of the scrollable history and no longer covers messages. At 390 × 844, detail-only mode removes list filters and category controls, leaving enough room for the editable draft and send action.
- Colors and tokens: the source green emphasis maps to SparkClaw's existing `--cyber-blue`/green success token on the dark surfaces. Contrast, selected state, disabled polish action, focus state, and direct send action are coherent with the existing product.
- Image and asset fidelity: neither target relies on product imagery. Visible actions use the established Lucide icon set; sender initials are textual avatars matching the source's information pattern. No raster placeholders, hand-drawn SVGs, CSS illustrations, gradients, or fake logos were introduced.
- Copy and content: the conversation-level AI Overview is absent. Each message uses the selected-language summary as its primary content; the original source remains behind a secondary disclosure. The composer makes the sequence explicit: describe intent, generate an editable reply, review or change it, then send the reply in one action.
- Accessibility and behavior: conversation messages retain semantic article/body content, the intent and edited reply textareas are labeled, disabled states are exposed, keyboard focus remains visible on interactive controls, and “Send reply” is the single final action after editing.

## Full-view comparison evidence

The final comparison placed both browser screenshots in the same CUA comparison input at 1280 × 720 and DPR 1. The composition, selected conversation treatment, chronological message hierarchy, and bottom reply affordance align. SparkClaw intentionally keeps its global mail controls and three-column information architecture.

## Focused region comparison evidence

The reply region was compared in two states: empty intent and edited polished draft. The implementation was exercised through intent entry, polish, direct draft editing, and the single “Send reply” action. A separate 390 × 844 pass verified the detail view and direct-send layout. The final clean desktop tab reported no console warnings or errors.

## Comparison history

1. Initial pass found a P1: the sticky reply composer overlaid the conversation history; and a P2: the programmatically focused non-interactive title showed a large browser outline. The composer was moved outside the history scroller and the title's non-interactive focus outline was removed. The next capture showed an independent bottom composer and readable scroll area.
2. Mobile pass found that detail mode retained filters/sync/category controls that consumed most of the viewport. Mobile detail mode now hides list-only controls. The 390 × 844 recapture showed no overlap, clipping, or broken button text.
3. Final pass opened fresh source and implementation tabs, normalized both to 1280 × 720 at DPR 1, compared them in one input, exercised the reply flow, and checked the implementation console. No actionable P0/P1/P2 issue remained.
4. Owner follow-up removed the second confirmation layer while retaining the editable polished body. The final action is now a single direct “Send reply” button; the underlying versioned native-reply save/send fence remains unchanged.
5. Owner follow-up removed the conversation AI Overview and changed the primary message content to selected-language summaries. Original bodies remain reachable in each message's secondary details; already-synchronized mail uses the same language projection path.
6. Deployed validation opened one historical verification mail. The localized summary exposed neither the credential nor the legacy CSS preamble, the complete original remained under the disclosure, and a fresh post-deployment tab had no console warnings or errors.

## Open Questions

- Production model tone still depends on the configured Fast model and real correspondence; this visual QA does not claim semantic-quality qualification.
- The preview uses synthetic local mail data and does not send a real email.

## Implementation Checklist

- [x] Continuous chronological message presentation
- [x] No conversation-level AI Overview
- [x] Selected-language summaries for loaded historical messages
- [x] No repeated subject/address grid in the primary conversation path
- [x] Intent-to-polished-draft flow
- [x] Editable polished body
- [x] One direct native “Send reply” action after editing
- [x] Desktop and mobile responsive checks
- [x] Clean browser console on the final implementation tab

## Follow-up Polish

- P3: a future compact-toolbar option could reduce SparkClaw's existing desktop mail controls when the popup is used primarily for reading, but it is not required for this interaction change.

final result: passed

---

# Design QA — WebChat full-page settings shell

- Source visual truth path: `/tmp/codex-clipboard-abd01b14-cf85-42b2-aa60-2e173929b493.png`
- Implementation capture: Codex in-app Browser tab 7, current QA turn
- Viewport and density: source 1259 × 927 px; implementation 1259 × 927 CSS px at device pixel ratio 1
- State: “常规” selected, settings search empty, local preview without loaded Gateway configuration
- Comparison scope: full-page settings shell, left navigation, right content placement, and navigation behavior. The source includes about 22 px of desktop application menu chrome that is not owned by this web app.

## Findings

No actionable P0, P1, or P2 findings remain.

- Fonts and typography: the implementation keeps SparkClaw's established system/Inter typography while matching the reference's compact 12–14 px navigation hierarchy and large right-side page title.
- Spacing and layout: settings replace the task workspace rather than opening in a modal or nesting inside it. At the reference viewport, the left rail is 232 px wide; the search field begins at x=9 and is 213 × 31 px, the selected navigation row begins at x=9 and is 205 × 31 px, and the right heading begins at approximately x=370 and y=135.
- Colors and visual tokens: the light settings surface, soft-gray search/selected states, hairline divider, and neutral icon treatment align with the supplied Codex reference while preserving SparkClaw's existing control tokens.
- Image and asset fidelity: the reference has no product imagery. The implementation uses the existing Lucide line-icon family; no raster placeholders, generated illustrations, gradients, or fabricated logos were introduced.
- Copy and content: the shell follows the reference structure, while navigation labels and detail panels remain SparkClaw-specific. Existing settings, connection, Agent, account, and system content is preserved in the right detail area.
- Accessibility and behavior: the back action, search field, and navigation buttons expose accessible names and selected state. Search filtering, navigation switching, and returning to the prior task page were exercised successfully.

## Comparison history

1. The first full-page pass placed the right heading about 30 px too high and made the selected navigation row about 8 px too wide.
2. The content top inset was adjusted from 105 px to 135 px, and the navigation row width was reduced to `calc(100% - 8px)`.
3. The final 1259 × 927 capture aligned the settings rail and right heading with the reference. A fresh preview tab reported no console warnings or errors.

## Implementation Checklist

- [x] Settings replace the main application surface
- [x] Persistent left settings navigation with back action and search
- [x] Right-side detail content for the selected settings section
- [x] No modal overlay or nested workbench split
- [x] Search, section switching, and back navigation verified
- [x] 43 test files / 161 tests and production build passed
- [x] Clean browser console on a fresh implementation tab

## Follow-up Polish

- None required for this scoped correction.

final result: passed

---

# Design QA — WebChat new conversation controls

- Source visual truth path: `/home/infinimesh/Documents/SparkClaw-workbench-page/design-qa-artifacts/sidebar-new-conversation-reference.png`
- Implementation screenshot path: `/home/infinimesh/Documents/SparkClaw-workbench-page/design-qa-artifacts/sidebar-new-conversation-implementation.jpg`
- Combined comparison path: `/home/infinimesh/Documents/SparkClaw-workbench-page/design-qa-artifacts/sidebar-new-conversation-comparison.png`
- Viewport and density: 1280 × 720 CSS px at device pixel ratio 1; source crop 28 × 27 px; implementation button 28 × 28 CSS px with a 16 × 16 SVG slot
- State: empty chat, left sidebar expanded, right inspector closed; the development-only authentication banner is outside the target sidebar and topbar regions

## Findings

No actionable P0, P1, or P2 findings remain.

- Fonts and typography: the new-chat action is intentionally icon-only and exposes its name through `aria-label`/title; “最近任务” retains the existing 12 px sidebar label treatment.
- Spacing and layout rhythm: the 28 px action sits on the same row as “最近任务”; the prior full-width action row is gone. The brand-row left-sidebar toggle is restored, the main breadcrumb row contains no workspace/title copy, and the right-panel toggle is aligned to the far right.
- Colors and visual tokens: the implementation uses the existing sidebar surface plus neutral `#777c82` icon color. The supplied reference uses a neutral gray icon on a light-gray surface; the comparison shows equivalent contrast without introducing a one-off image asset.
- Image quality and asset fidelity: the source’s 14 px visible edit-square silhouette maps to the existing Lucide `SquarePen` icon inside a 16 px SVG slot. The icon remains vector-sharp in production code; the enlarged QA crop uses nearest-neighbor scaling only to expose pixel differences.
- Copy and content: “个人工作区 › 工作台” and its fallback/connecting/model-mode translation logic were removed, not hidden. User-visible `SparkClaw Browser Bridge` wording was removed from the settings directory and browser-control detail while the underlying connection behavior remains unchanged.
- Accessibility and interaction: the icon-only new-chat action keeps an accessible name. The restored left sidebar toggle and the new right sidebar toggle were each exercised in the in-app browser; expanded/collapsed state changed correctly. The final preview tab reported no console warnings or errors.

## Full-view comparison evidence

The implementation screenshot confirms the requested global composition: original left-panel icon in the brand row, icon-only new conversation action beside “最近任务”, no workspace breadcrumb text, a mirrored right-panel control at the far right, the retained right-edge pull handle, centered “我们要做什么”, and the bottom composer.

## Focused region comparison evidence

The combined comparison places the exact 28 × 27 source crop and the 28 × 28 rendered button crop together at 6× scale. The edit-square proportions, diagonal pencil, visual footprint, neutral gray treatment, and surrounding spacing align. The one-pixel slot-height difference is the existing even-pixel control grid and does not create visible drift.

## Comparison history

1. Initial implementation used a full-width text button and a different `PanelLeftClose` control on the same row; the owner clarified that only the reference icon should remain beside “最近任务”.
2. The button was reduced to a 28 px icon-only action, the original `PanelLeft` control returned to the brand row, the breadcrumb render and translation logic were removed, and a symmetric `PanelRight` control was added to the main topbar.
3. Final measured comparison and interaction checks found no actionable P0/P1/P2 mismatch.

## Implementation Checklist

- [x] Icon-only new conversation action beside recent tasks
- [x] Original left sidebar toggle restored
- [x] Right sidebar toggle added and interactive
- [x] Workspace breadcrumb copy and dead translation logic removed
- [x] User-visible Browser Bridge wording removed
- [x] Reference/implementation comparison captured
- [x] Clean console and left/right toggle interaction checks

## Follow-up Polish

- None required for this scoped change.

final result: passed
