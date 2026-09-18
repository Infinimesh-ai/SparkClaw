# SparkClaw workbench alignment

final result: passed

## Scope and visual evidence

- Source: `/home/infinimesh/Documents/xwechat_files/wxid_e7wrktvq5xkj22_a22d/msg/file/2026-09/SparkClaw 工作台（独立版）.html`.
- Preview: `http://127.0.0.1:18890/`, connected to the isolated worktree's default mock-model Gateway. This is not a production deployment.
- Evidence directory: `/home/infinimesh/.codex/visualizations/2026/09/17/01a0ad7d-598c-70a0-a16a-ad98b8c6e379/`.
- Source screenshot: `reference-workbench.png` in the evidence directory.
- Final implementation screenshot: `implemented-workbench-final.png` in the evidence directory.
- Mobile captures: `mobile-workbench.png`, `mobile-conversation.png` in the evidence directory.
- Desktop comparison: both images 1280 × 900 pixels, 1280 × 900 CSS viewport, 1× density, Chinese, light theme, empty-task home. Images were emitted together in the same browser-tool result and compared directly. Earlier unequal viewport captures were excluded from the comparison.
- Mobile verification: 390 × 844 CSS pixels, 1× density. Native viewport override was reset afterward.

## Required fidelity surfaces

- Typography: source system-font stack, 40px desktop / 32px mobile home heading, 550 weight, 1.42 line height, two-line headline, 12–14px supporting copy. Matches source hierarchy and wrapping. Existing technical panels retain monospace for identifiers and structured diagnostics.
- Layout: 244px sidebar, 74px desktop header, centered 870px home content, 16px composer corners, four-column workflow strip, page navigation, collapsible task inspector. Mobile navigation overlays and closes after selection; workflow becomes two columns. Source and implementation hero/composer/guide positions align within a few pixels.
- Colors: white / #f6f7f9 surfaces, #4285f4 actions, #1f5fbf emphasis, #e8e9ec borders. Former green accents were migrated throughout existing panels. Error and notice text were corrected for light backgrounds.
- Assets: original embedded PNG wordmark reused without redrawing or recoloring. Existing Lucide line icons retained, matching source stroke-icon style. No generated replacement branding.
- Content: source headline, introduction, suggestions, workflow steps, and page titles preserved with English equivalents. Task lists, approval counts, connector availability, and model mode come from the existing API rather than the sample data.
- Focused comparison: the desktop images are readable at 1×; logo, sidebar, heading, composer, and flow-strip regions were inspected directly in the same images, so separate enlarged crops were unnecessary.

## Findings and iterations

1. Initial offline capture exposed low-contrast legacy error text on the light theme (P2). Replaced pale red/blue banner text with dark semantic colors; retained the real offline/error state instead of hiding it.
2. First 390px capture exposed inherited `width: 100%` on topbar actions, hiding the title and inspector control (P1). Set actions to intrinsic width, reduced destination-picker detail on narrow screens, and removed the legacy message-list height cap. Post-fix `mobile-conversation.png` shows the title, mail, notifications, destination, and inspector controls within the viewport.
3. Search dialog initially focused its close button (P2). Explicitly focus its search input after `showModal()`. Rechecked Ctrl+K: accessibility focus is on the search field; filtering and selecting a result opens the matching task. Native dialog supplies focus containment and Escape handling.
4. Selecting an empty task could restore a previously open inspector (P2). Task selection now closes the inspector; final home capture has the intended full-width composer and guide.
5. Final same-size desktop comparison has no remaining actionable P0/P1/P2 visual findings within this production-frontend adaptation.

## Intentional production adaptations

- Existing upload, workspace-document picker, voice, email, notifications, and delivery-target controls remain usable. The prototype's simulated thinking/search switches are not exposed as fake settings; model settings open the real settings page.
- Supported messaging connections are Telegram, WeChat, and the existing browser-mail flow. The prototype's simulated Feishu connection is not advertised as available.
- New schedules and memory entries start a task draft through the existing agent workflow. Existing schedule edit/delete and memory review/edit/delete/export APIs remain unchanged. No browser-only fake records or new backend contracts were introduced.
- Settings retain the real account, connection, agent, and system configuration surfaces. The inspector retains timeline, approvals, memory, trace, status, and settings rather than discarding existing diagnostics.
- Existing session selection on startup remains intact; empty/new sessions show the reference home design. Real history and empty states replace sample investment/research tasks.

## Verification

- Browser: task suggestions fill drafts; message submission reaches the isolated mock Gateway and renders a response; task inspector opens/closes; search filters and opens a real task; desktop settings/connections/memory/approvals render; mobile navigation opens/closes; schedule creation entry opens a new draft with the scheduling prompt.
- Console: no error-level entries in the final implementation tab.
- Frontend production build, 708-key bilingual usage/parity check, 146 existing tests across 41 files, and `git diff --check` passed.
- Real external-model inference, microphone access, account binding, production scheduling execution, and remote deployment were not exercised by this visual acceptance run.

## Follow-up polish

- P3: source versus implementation scrollbar metrics shift the centered home content by approximately 4px. Control roles and production runtime content account for the remaining toolbar/history differences.
- Build reports a >500kB chunk advisory; the original supplied embedded wordmark contributes to the bundle. This is not a build failure.
