# SparkX Desktop Typography Layout and Interaction Design

> Language: English | [简体中文](../zh-cn/docs/desktop-experience-design.md)
>
> Status: original design dated 2026-10-18; local P0–P2 implementation is now recorded in [implementation and validation](desktop-experience-validation.md). No packaging or deployment has been performed.

Retain the existing SparkX workbench and all feature entry points, improving readability, layout stability, and predictable operation. Phase one unifies typography, spacing, colors, and content alignment. Phase two improves input, keyboard navigation, scrolling, and panels. Phase three brings mail, files, schedules, and settings into the same visual system.

## 1 Scope and principles

Target the Electron desktop application, starting with Chinese on macOS and covering font fallbacks and keyboard differences on Windows and Linux. Keep the default 1440 × 900 window and 1180 × 720 minimum. Shared WebChat components require browser regression checks; mobile layout is not redesigned here.

- Preserve conversations, schedules, notifications, the account menu, mail, files, voice, the browser panel, and all nine settings destinations.
- As explicitly requested in this review, remove the intermediate tool chooser containing only Browser. Replace the header toggle with a browser icon that opens a new tab directly. This is the specified exception to retaining menu structure.
- Improve existing components and state presentation. Do not create a simplified replacement workbench or migrate conversations, drafts, credentials, or task data.
- Retain the SparkX identity, blue accent, and pale sidebar. Use system sans-serif fonts, avoiding a serif conversation face that produces inconsistent Chinese typography merely to resemble another product.
- Keep unavailable features discoverable, disabling affected actions with an explanation. Transport, queue, and connection recovery details do not become permanent interface content.
- Dimensions, colors, and behaviors below are SparkX proposals, not claims about Codex or Claude's internal design specifications.

## 2 Current interface and evidence

The source baseline is `19408777`. The installed SparkX Chinese long-answer view, account menu, and general settings page were also inspected without changing preferences. Source measurements describe the repository; installed-package byte parity with that revision was not verified. Visual observations and source findings are distinguished from untested interaction defects.

| Current evidence | User impact | Design response |
|---|---|---|
| Sidebar tasks and schedules are mostly 12px, account descriptions 10px; many settings descriptions and selectors are 11px in `workbench.css` | Small Chinese navigation and descriptions; a large size gap between headings and content | 14px navigation, 13px descriptions, at least 12px metadata |
| Paragraph line height is 1.55; message metadata uses 10px bold monospace with expanded tracking in `conversation.css` | Dense long Chinese answers; roles and timestamps resemble logs | 16px/28px body and ordinary 12px/18px UI metadata |
| Message width, assistant max-width, and an 820px composer dock with 40px side padding live in separate rules | No single rule guarantees reading/input alignment | A shared 760px column with consistent padding and narrowing |
| A 70px header and 62px empty textarea combine with padding and a toolbar | Excess space above content and around empty input reduces reading height | 52px header and content-driven composer, approximately 112px when empty |
| Large text uses `zoom: 1.1` in `appearance.css` | Increasing text also changes sidebar, panel, and control geometry | Keep default/large choices and storage keys, using type and line-height tokens |
| Desktop panel defaults to 640px; chat yields down to 520px then is overlaid; the divider removes its focus-visible outline | Occlusion risk in narrow windows and weak keyboard resize feedback | Explicit split/focus modes with visible divider focus and size feedback |
| `BrowserPanel.tsx` retains launcher/browser surfaces and Back to tools; both workbench entries use `PanelRight`, with different initial panel states | A generic tool chooser remains despite having only one tool, and the entry does not clearly identify its purpose | Use a browser icon opening a new tab, align both desktop entries, and remove the chooser and Back to tools actions |
| Escape in `App.tsx` closes search and inspector together | Layered interfaces may dismiss too much; requires focused reproduction | Let the top layer handle Escape and restore trigger focus |
| Search, new chat, and IME protection already exist | These behaviors should be inherited | Preserve semantics while extending shortcuts and coverage |

Source entry points: [style imports](../apps/webchat/src/styles/app.css), [workbench styles](../apps/webchat/src/styles/workbench.css), [conversation styles](../apps/webchat/src/styles/conversation.css), [appearance styles](../apps/webchat/src/styles/appearance.css), [composer](../apps/webchat/src/components/composer.tsx), [divider](../apps/webchat/src/components/inspectorResize.tsx), [settings navigation](../apps/webchat/src/components/settingsSidebar.tsx), and [desktop window](../apps/desktop/src/main/main.mjs).

## 3 Codex and Claude references

| Reference | Pattern confirmed in official documentation | Application to SparkX |
|---|---|---|
| Desktop application containing Codex | New-chat, sidebar, font-size, and shortcut-discovery controls | Keep search/new-chat behavior; add sidebar/settings shortcuts and shortcut hints |
| Claude's conversation-adjacent content panel | Content opens beside the conversation; Tab enters and Escape leaves layers with focus restoration | Improve focus movement in the existing browser panel without automatic opening or ownership changes |
| Claude Desktop quick entry | Text, context, and dictation share an input entry point | Keep attachments, mail, and voice in the existing composer with consistent sizes and feedback |

Consulted on 2026-10-18: [OpenAI official commands and shortcuts](https://learn.chatgpt.com/docs/reference/commands), [Claude artifact keyboard interaction](https://support.claude.com/en/articles/17457950-use-artifacts-with-a-keyboard-or-screen-reader), and [Claude Desktop quick entry](https://support.claude.com/en/articles/12626668-use-quick-entry-with-claude-desktop-on-mac). These inform interaction organization. This scope adds no global quick-entry window, new system permission, or copied product menu.

## 4 Typography and visual specification

### 4.1 Fonts and type scale

Use system fonts for UI and Chinese body text: `-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif`. Code uses `ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace`. Do not download fonts in the first release. Missing Linux CJK fonts belong in installation dependencies, not rasterized text.

| Semantic token | Default size/line height | Large size/line height | Weight and use |
|---|---|---|---|
| `--font-body` | 16/28px | 18/32px | 400; conversations, mail reading, composer text |
| `--font-ui` | 14/20px | 16/24px | 400; navigation, buttons, settings fields |
| `--font-label` | 13/20px | 15/22px | 500; groups and field descriptions |
| `--font-meta` | 12/18px | 14/20px | 400; times, sources, file sizes |
| `--font-title` | 22/30px | 24/34px | 600; page titles |
| `--font-section` | 16/24px | 18/28px | 600; settings and content sections |
| `--font-code` | 13/21px | 15/24px | 400; code and exact identifiers |

All measurements are CSS pixels at 100% page zoom. Standardize weights to 400/500/600, without body tracking or uppercase metadata styling for Chinese. Numeric columns may use tabular numerals. Full paths, URLs, and identifiers remain copyable.

Retain `default`/`large` in `sparkclaw.appearance.text-size`, changing rendering only. The sidebar stays 244px and saved panel widths remain usable; line heights and minimum control heights expand to fit text. Do not apply both CSS zoom and type tokens. Preserve independent OS/Electron accessibility zoom and validate 100%, 125%, 150%, and 200%.

### 4.2 Color and spacing

| Semantic role | Proposed light | Proposed dark |
|---|---|---|
| Main workspace | `#FFFFFF` | `#171A1F` |
| Sidebar/secondary surface | `#F5F5F5` | `#20252C` |
| Primary text | `#202226` | `#EDF0F4` |
| Secondary text | `#626873` | `#B9C1CB` |
| Ordinary separator | `#E3E5E8` | `#414954` |
| Blue button/link/focus | `#2563EB` | `#93B8FF` |
| Accent-button text | `#FFFFFF` | `#171A1F` |

Keep the blue identity, using deeper blue for readable white button labels in light mode. Separators organize content; meaningful input boundaries, selection, and focus indicators require separate checks. Target at least 4.5:1 for ordinary text and 3:1 for essential non-text controls. A palette alone does not prove application-wide compliance. Errors, warnings, and completion use labels/icons as well as color.

Use 4/8/12/16/24/32px spacing, 8px control radii, 12px content-card radii, and a 16px composer radius. Keep existing lucide icons, normally 18px in at least 32 × 32px hit areas, with 36px primary actions. Reserve prominent shadows for overlays; organize body content with space and headings. Hover/focus transitions last 120–160ms; reduced-motion preferences remove movement and unnecessary animation.

## 5 Layout

### 5.1 Core structure

This is a structural wireframe; dimensions are specified in the table. The native window title bar sits outside the workbench.

```text
┌────────────────────┬─────────────────────────────────────────────┐
│ SparkX    Collapse │ Workspace               Notifications Browser│
│                    ├─────────────────────────────────────────────┤
│ Schedules          │             Conversation column             │
│ Recent Search New  │        Shared maximum width: 760px          │
│ Current chat       │                              User message   │
│ Other chats        │ SparkX                                      │
│                    │ Answer, lists, citations, and file results  │
│                    │                                             │
│                    │ ┌─────────────────────────────────────────┐ │
│                    │ │ Input text and staged attachments       │ │
│                    │ │ Upload Files Mail Voice             Send│ │
│ Owner Account menu │ └─────────────────────────────────────────┘ │
└────────────────────┴─────────────────────────────────────────────┘
```

| Region | Proposed specification | Preserved behavior |
|---|---|---|
| Sidebar | 244px; 12px horizontal padding; minimum 40px task rows | Collapse, create, search, rename, delete, selection, completion indicators |
| Header | 52px inside workspace; 24px horizontal padding | Workspace label, notifications, direct browser icon, sidebar reopening |
| Conversation/input column | Shared maximum outer width 760px; at least 24px outer gutters; 16px internal padding | Independent conversation scrolling, bottom composer; right-aligned user messages at most 80% of the column |
| Message rhythm | 24px between messages; 12px between paragraphs; 6–8px between list items; headings 24px above/8px below | Complete content, sources, attachments; no collapsing body text to save space |
| Composer | Approximately 112px empty; 20px bottom inset; textarea grows through 1–6 lines | All existing input tools; internal text scrolling beyond the limit |
| Settings | Maximum 800px; 32px page padding; minimum 64px field rows | Nine destinations and return location; descriptions wrap without being squeezed by controls |

The 760px measurement is a shared outer frame, not independently declared bubble widths. Message and input text start 16px inside it. Wide tables/code scroll locally rather than widening the window. Lists, mail, and timelines use page-level width instead of being forced into the conversation column.

### 5.2 Direct browser entry

The right panel contains only a browser, so remove the Panel tools → Browser chooser. Replace the header's `PanelRight` toggle with the existing icon library's `Globe2`: an 18px glyph in at least a 32 × 32px hit area, retaining its position beside notifications. Tooltip and accessible name reflect Open browser or Collapse browser, with `aria-expanded` retained.

```text
Collapsed: [Notifications] [Browser icon]
                                │ Click
                                ▼
Right panel: [New tab ×] [+]                 [Widen] [Collapse]
             [Back] [Forward] [Reload] [Search or enter address…]
             New-tab content
```

| Action or state | Required behavior |
|---|---|
| Click browser icon while collapsed | Open the browser in one step, select a new tab, and focus the address bar without a tool chooser |
| Existing personal pages or task tabs | Retain all tabs; select a blank personal tab for this opening without replacing a page or taking over a task tab |
| An unused blank personal tab exists | Reuse it; a tab with unsubmitted address text or other user input is not an unused blank |
| No reusable blank exists | Create one for the explicit opening action; rerenders, state pushes, or rapid repeated clicks must not create duplicates |
| Click the header icon while open, or the panel Collapse action | Hide panel and native page view, preserve tab state, and return focus to the header browser icon |
| Click the tab-strip plus button | Keep explicit new-tab creation and select the added tab, without a secondary chooser |
| Close the last closable personal tab | Keep the browser panel with an address-ready new-tab empty state; do not return to a chooser or automatically close task tabs |
| Browser unavailable or opening fails | Keep the icon; disable with a reason when unavailable and show opening errors in the browser region, without falling back to a tool menu |

Remove the launcher, Back to tools button, and the matching browser overflow action. Where the existing position provides collapse access, make it Collapse browser directly. Browser tabs, address controls, back/forward/reload, downloads/permissions, and browser-specific overflow actions remain; they are not the intermediate tool chooser being removed.

Cover both the desktop branch of `App.tsx` and `LocalWorkbench.tsx`, removing launcher state inside the existing `BrowserPanel.tsx`. The non-browser inspector in plain WebChat is outside this desktop change. Focus the address bar after expansion and blank-tab readiness; subsequent background task updates must not steal typing focus.

### 5.3 Right panel and narrow windows

Keep the 640px browser default and saved user width, with pointer and keyboard resizing. Split mode requires `available main width ≥ 520 + 6 + panel width`, measured in actual CSS content width after subtracting the sidebar.

| Situation | Expected layout |
|---|---|
| 1440px content width, 244px sidebar, 640px panel | 1196px main area fits 550px conversation + 6px divider + 640px panel |
| 1180px content width, expanded sidebar, 640px panel | 936px main area uses panel focus mode and a return-to-conversation control, without a hidden composer underneath |
| Collapsed sidebar or resized panel makes split fit | Allow split mode, preserving tabs, scroll position, and unsent text |
| Explicitly maximized panel | Stay focused until the user exits; window growth must not undo the choice |

Focus mode belongs to the existing panel, not a second browser implementation. Automatic focus caused by narrowing may restore the prior split when space returns, with 24px hysteresis to avoid oscillation. Automatic layout changes do not steal focus: when the conversation has keyboard focus, retain the conversation and use the existing panel toggle to reach the browser; when the browser has focus, retain the browser with a return-to-conversation control. The temporarily hidden region stays mounted, with its native view hidden where applicable. Show a 2px divider focus indicator and accessible size feedback. Preserve drag-to-hide.

Electron `WebContentsView` is not governed by ordinary DOM z-index. Panel geometry, zoom, dialogs, and focus changes must synchronize native view bounds/visibility. A native page must not cover application approvals or settings dialogs. Layout changes must not reload pages.

## 6 Core interaction

### 6.1 Conversations and search

Use a subtle selected background and weight, reducing border decoration. Reveal full titles on hover or keyboard focus. Show row actions on hover/focus with reserved space to prevent title movement. Keep the current search entry and Cmd/Ctrl+K, with arrow navigation, Enter, Escape, and focus restoration. Preserve existing viewed-completion clearing; do not mark every historical completion with a permanent blue dot.

### 6.2 Input and drafts

Keep Enter to send and Shift+Enter for a newline. Confirming a Chinese IME candidate must never send; preserve composition and post-composition guards. Existing execution, voice, and upload state remains authoritative for sending availability.

- Grow text through six lines, then scroll internally. Attachments occupy their own row with name, state, and removal.
- Show upload failure and retry beside the affected attachment, retaining text and other successful attachments.
- Keep input focus after successful submission and the draft after failure. Preserve existing per-conversation draft isolation and persistence.
- Show recording/transcribing/failure and existing stop/retry controls in the voice region, retaining conversation-switch restrictions.
- Preserve generation-time queuing/disable behavior. Enable a stop action only where reliable cancellation already exists; this design creates no cancellation API.

### 6.3 Long answers and streaming

Follow new content while the user is at the bottom. Stop following when they deliberately scroll up more than 80px or select text, showing a return-to-latest control. Clicking it resumes following. Restore reading positions per conversation. Preserve the message anchor when final content replaces a streaming preview instead of resetting the list.

Show ongoing activity as a short line within the current reply, with existing steps available when needed. Approval clearly identifies action, target, scope, and confirmation controls. Collapse process information after completion, emphasizing the result and files. Do not remove facts, risks, citations, or limitations from model-authored body text for visual polish; source/status presentation must keep the original content accessible.

### 6.4 State and recovery

| State | Presentation and action |
|---|---|
| Initial loading | Lightweight placeholders matching final structure; avoid flashing for loads under 300ms |
| Executing | Compact task/reply progress, without duplicated global-header status |
| Waiting for approval | Existing approval entry and clear conversation indication; separate preview from authorization |
| Automatically recoverable reconnection | Keep draft and displayed results stable; no toast when no user action is needed |
| Connection or authorization action needed | Explain locally and link to connection settings |
| Unknown submission outcome | Say that the previous submission is being checked; reconcile its existing identity, without a generic retry-send action that could duplicate effects |
| Definite safely retryable failure | Short reason and an action matching existing semantics; expandable technical details |
| Unavailable capability | Keep the entry, disable affected actions, and expose the reason to keyboard users |
| Background completion/new notification | Preserve notification-center and unread semantics; do not interrupt again for foreground content already viewed |

### 6.5 Keyboard and focus

| Action | Proposed shortcut | Scope |
|---|---|---|
| New conversation | Cmd/Ctrl+N | Preserve existing behavior and busy/recording guards |
| Search tasks | Cmd/Ctrl+K | Preserve search semantics rather than changing it to a command palette |
| Toggle sidebar | Cmd/Ctrl+B | New; do not intercept editable content or pages where it has another meaning |
| Workspace settings | Cmd/Ctrl+, | New; restore prior page on return |
| Dismiss current layer | Escape | Menu, dialog, then panel, one layer per press |
| Resize divider | Arrows; Shift for larger steps; Home/End | Keep bindings, constrained by layout mode |
| Traverse controls | Tab/Shift+Tab | Visual reading order with visible focus |

Focus a dialog heading or first action on open, contain focus in modal dialogs, and restore the trigger on close. Non-modal panels allow movement between conversation and panel. Native pages/editors handle their own Escape, copy, undo, and formatting operations first. Main-process and React handlers must not execute the same shortcut twice.

## 7 Secondary-page consistency

| Page | Improvement | Must preserve |
|---|---|---|
| Mail | 14px lists/16px reading text, clear account/folder/message hierarchy, primary/secondary toolbar actions, retained lists while loading | Sync, classification, compose, attachments, reading, send/confirmation, and safe HTML isolation |
| Files | Prioritize names; 12px type/size metadata; clear preview/download actions and local errors | Both existing upload and file-selection entries, preview/export paths and permissions |
| Schedules | Scannable title/next occurrence; aligned state/actions; narrow rows may wrap | Create, edit, pause/resume, delete, timezone and recurrence semantics |
| Notifications | Clear unread hierarchy and task positioning after navigation; simple empty state | Read handling, existing navigation and background-completion rules |
| Settings | Consistent field rows/type, locatable search results, local field errors | General, appearance, devices/credentials, models/tools, permissions, connections, memory, approvals, timeline |
| Browser | Header browser icon opens a new tab without a tool chooser; consistent tabs/address/actions and clear personal/task identity | Read-only task pages, personal pages, navigation, downloads, permissions, tab lifecycle; collapse does not close pages |

## 8 Implementation sequence and boundaries

| Phase | Work | Existing files | Exit |
|---|---|---|---|
| P0 Visual foundation | Type tokens, themes, text sizes, spacing, shared message/input column | `foundation.css`, `appearance.css`, `workbench.css`, `conversation.css`, `composer.css` | Conversation/settings screenshots approved at both text sizes |
| P1 Interaction continuity | Direct browser icon/new tab and removal of tool chooser; growing input, scroll anchors, focus return, Escape routing, shortcuts, panel layout | Existing `composer.tsx`, `messages.tsx`, `App.tsx`, `LocalWorkbench.tsx`, `BrowserPanel.tsx`, `inspectorResize.tsx`, desktop bounds adapter | One-step browser opening, IME, keyboard, streaming completion, narrow native-panel cases pass |
| P2 Page consistency | Mail, files, schedules, notifications, nine settings pages, dark-mode details | Existing page components and styles | Preservation and accessibility matrix passes |

First map CSS imports and overrides; avoid accumulating increasingly specific repair selectors. Scope desktop layout selectors to the desktop host. Shared typography tokens require WebChat regression coverage. Each phase must be reversible; retain old preference values without business-data migration.

This design changes no Gateway, ISCP, JingSi, or IMMS external contract and creates no execution states, automatic retries, or permissions. Any backend capability discovered during implementation requires a separate design and InfiniCenter decision rather than being included silently in UI work.

## 9 Acceptance criteria

### 9.1 Visual matrix

Use sanitized fixed fixtures for empty conversations, short answers, 2000-character Chinese answers, mixed languages, nested lists, tables, code, long URLs, attachments, approvals, errors, mail, schedules, and all settings.

- macOS: 1180 × 720, 1440 × 900, and 1920 × 1080; light/dark/system themes; default/large text. Record content size, OS scaling, page zoom, and font. Retina pixels are not CSS pixels.
- Windows/Linux: at least default/minimum windows for Chinese fallback, truncation, and shortcuts; mark unavailable environments as pending acceptance.
- At 100%, message/input text left edges differ by no more than 2 CSS pixels. No unintended page-wide horizontal scroll or overlap between main actions, the final message, and composer.
- At 125%, 150%, and 200% zoom, content and actions remain reachable through scrolling or focus layout, never through unrecoverable clipping.
- Check contrast from computed styles for normal/hover/selected/focus/disabled states. Inspect every overlay and native-view boundary in dark mode.

### 9.2 Behavior matrix

| Scenario | Required result |
|---|---|
| Chinese candidate confirmation plus Enter/Shift+Enter | No accidental submit; true submission occurs once |
| 1–30 lines and an attachment failure | Bounded height, retained text/successful attachments, continuous focus |
| Scroll up, select text, switch conversation, receive final output while streaming | No forced scroll or disrupted selection; isolated positions and complete final output |
| Search, menu/dialog, and right panel | One Escape closes only the highest layer and returns focus |
| First browser-icon opening, reopening, existing pages, closing the last personal tab | One click reaches an address-ready new tab; old pages/task tabs remain; no chooser or Back to tools action |
| Rapid toggles, rerenders, background browser-state pushes | No duplicate blank tabs or stolen typing focus; native view is hidden after collapse |
| Drag/hide/maximize panel, resize window, change text size | No inaccessible occlusion, page reload, or mismatch with native bounds |
| Recovery, unknown submission, expired authorization | Draft retained, original request reconciled, no duplicate execution or false success |
| Original functionality | Rename/delete chats, mail, files, voice, schedules, notifications, all settings and actions remain usable |
| Keyboard/screen reader only | Accessible names, visible focus, escapable modal focus, no repeated character-by-character status announcements |

Target local typing/menu feedback within 100ms, excluding network, model, and task-execution latency. Long lists and streaming must not cause whole-page layout per token or lose scroll anchors. Record actual performance during implementation.

Implementation runs affected WebChat tests and strict build; changes to shortcuts, native bounds, or windows also run relevant Desktop checks. Functional tests cannot substitute for screenshot comparisons and native-window walkthroughs. This documentation delivery checks mirrors, links, and design consistency, not implementation acceptance.

## 10 Review recommendation

Adopt system sans-serif, 16px body/14px navigation, a shared 760px content column, the existing 244px sidebar, and a 52px header. Preserve the current color direction and every functional entry. This review explicitly specifies Browser icon → New tab and removes the intermediate tool chooser. Review P0 long-answer, empty-conversation, and settings samples before P1 panel and interaction work.

Larger body text reduces characters per screen. Recover some reading height through a shorter header, smaller empty composer, and consistent paragraph spacing. Font pickers, theme marketplaces, global quick-entry windows, and a second navigation system are outside the first release.
