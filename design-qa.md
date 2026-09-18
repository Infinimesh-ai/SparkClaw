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
