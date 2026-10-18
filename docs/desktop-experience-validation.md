# Desktop experience implementation and validation

> Language: English | [简体中文](../zh-cn/docs/desktop-experience-validation.md)

Date: 2026-10-18. This is a local implementation of the [desktop experience design](desktop-experience-design.md), based on `cd7d5ccca5e522b7b140cc2ac14b1c592e591be9`, on `codex/desktop-experience`. It is not a release, package, installation, live-backend acceptance, or ISCP upstream acceptance.

## Implemented scope

P0 uses shared system-font, size/line-height, spacing, surface and contrast tokens. Existing `default`/`large` storage values are unchanged; large text no longer zooms the workbench. The reading and input outer frames share 760 CSS pixels, with 16-pixel text insets. Desktop navigation retains its 244-pixel width and the header uses 52 pixels. A real class collision between the message scroller and Markdown lists was corrected by selecting their element types explicitly.

P1 keeps the existing composer and upload/file/mail/voice actions. Text grows through six lines; failed uploads retain the draft and successful attachments, with an adjacent retry. IME composition and the post-composition guard prevent accidental submission. Search supports arrows/Enter, modal dialogs contain focus, Escape dismisses one layer, and focus returns to its trigger. Cmd/Ctrl+B ignores editable fields, Cmd/Ctrl+, opens settings, and existing new/search shortcuts remain.

The desktop header opens the existing browser directly to an unused blank personal tab, creating one only when needed. Unsubmitted address text makes a blank tab non-reusable. Collapse keeps tab/address state mounted; the last personal tab leaves an address-ready empty surface. Task tabs remain read-only. `BrowserPanel` retains existing calls through optional `open`, `visible`, `onCollapse`, `wide`, and `onWideChange` props.

The panel switches between split and focus layouts using measured CSS width and 24-pixel hysteresis. Narrowing preserves the region with keyboard focus. Explicit widening persists until reversed. Native bounds convert CSS coordinates to Electron DIPs once, independent of display pixel density. Hidden panels and application modals suspend native presentation; background host activity cannot reveal another tab. No webpage reload or business-data migration is introduced.

Scrolling observes the existing list, follows only while at the bottom, pauses for deliberate upward scrolling or text selection, and restores positions per conversation. The anchor order is `data-scroll-anchor`, `data-message-id`, then DOM `id`. `messages.tsx` only adds `data-message-id`; the ISCP branch owns stable execution identity, optional `scrollAnchorId`, stream projection, and preview/final reconciliation. No simulated typing is added.

P2 applies the same type/surface system to mail, files, schedules, notifications and all nine settings destinations. Existing actions, permission checks, safe mail HTML, task ownership, drafts, schedules and credentials remain in their original components.

## Baseline and checks

Node 26.2.0 and independent worktree dependencies were used. Before edits, WebChat passed 319 tests across 59 files; Desktop passed 374 tests; the strict frontend build and 24-case isolated browser-layout check passed.

The final record includes (117 project Markdown sources and their language mirrors/link checks also passed):

- WebChat: 333 tests across 64 files passed; Desktop: 377 tests passed; strict TypeScript/Vite build passed; original local workbench, mail recovery, schedules, voice, settings and messages remain covered.
- Isolated macOS Electron visual fixtures for 18 scenes, both text sizes, light/dark palettes, three content sizes (1180 × 720, 1440 × 900, 1920 × 1080), and 125/150/200% page zoom at the minimum size. The fixture uses real production components with sanitized data and blocked network access. All 432 combinations passed, retaining 88 screenshots. At 100% zoom, reading/input text alignment differs by 0 pixels; all zoom levels differ by at most 0.2 pixels. Default body/input metrics are 16/28 pixels and large metrics are 18/32 pixels, with CSS zoom 1. Representative text/icon contrast is at least 5.17:1; composer border contrast is 3.57:1 in light mode and 5.27:1 in dark mode.
- Actual Electron native-view layout/visibility checks across 48 size/text/sidebar/zoom combinations, plus address readiness, blank reuse, preserved native input, dialog occlusion, narrow focus layout, keyboard resizing and drag-to-hide.
- Scroll/input behavior tests covering anchor identity changes, async conversation restoration, selection, return-to-latest, IME guards, 30-line input and failed-upload retry. A real Electron layout run passed asynchronous conversation restoration, preserved selection, follow resumption and 30-line input; the latter remained 168 pixels high (six 28-pixel lines), with 15.9ms input-to-two-animation-frame feedback in this local run. This measurement is not a cross-platform performance guarantee.
- `go build` and `go vet` passed. No Gateway source changed. The macOS full Go suite is not green: default temporary paths hit existing symlink guards; using `TMPDIR=/private/tmp` clears those failures, while Linux memory-workspace tests require `/dev/shm`. A readiness test also rejects `/private/tmp` because its output guard rejects the literal text `private`. These are recorded separately from frontend validation, not silently treated as passes.

Run the focused native checks with Node 26:

```sh
npm run test:webchat
npm run build:webchat
npm run test:desktop
npm --workspace @sparkclaw/desktop run test:browser-layout
node apps/desktop/test/experience-visual-run.mjs
node apps/desktop/test/run-experience-interaction.mjs
```

The visual runner prints its temporary evidence directory and keeps screenshots plus `report.json`; it removes its disposable Electron profile. The native browser and interaction runners remove their disposable profiles on completion. No installed-client profile, live database, real mailbox or production service is used.

## Evidence limits and integration

Computed alignment, font metrics, overflow and representative contrast checks supplement screenshot inspection; they are not a claim of a full accessibility audit. System theme resolution retains the existing preference mechanism. Real OS Chinese IME, screen-reader behavior and Windows/Linux font/shortcut acceptance remain target-environment gates. Synthetic component fixtures do not prove live mail delivery, model generation, authorization recovery or backend performance. Existing transport/recovery tests remain distinct from real-service acceptance.

The coordinator must merge the App/LocalWorkbench layout hooks with the ISCP branch's data changes and repeat the scroll/final-handoff cases using the integrated stable execution anchors. Keep both `data-message-id` and `data-scroll-anchor`. The only native browser interface additions are internal presentation controls; no Gateway, ISCP, JingSi or IMMS public contract changes. Shared InfiniCenter status and coordination updates belong to the coordinating chat. No merge to main, push, packaging, installation or deployment is performed here.
