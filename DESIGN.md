---
name: SparkX workbench
description: A quiet local-agent workspace in pale frosted glass.
colors:
  ui-ground: "#eceef1"
  ui-ink: "#252a32"
  ui-muted: "#626b77"
  ui-blue: "#086de1"
  ui-on-blue: "#fff"
  ui-glass: "rgb(255 255 255 / 48%)"
  ui-glass-strong: "rgb(255 255 255 / 78%)"
  ui-glass-edge: "rgb(255 255 255 / 88%)"
  ui-glass-highlight: "rgb(255 255 255 / 92%)"
  ui-solid: "#f7f8fa"
  ui-divider: "rgb(112 125 144 / 14%)"
  ui-input-edge: "rgb(112 125 144 / 24%)"
  ui-disabled: "#939ca9"
  danger: "#b42335"
  danger-surface: "#fff1f2"
  warning: "#925313"
  warning-surface: "#fff7e9"
  success: "#2b7d56"
typography:
  wordmark:
    fontFamily: '"League Spartan", -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif'
    fontSize: "22px"
    fontWeight: 600
    lineHeight: "30px"
    letterSpacing: "-0.4px"
  title:
    fontSize: "22px"
    fontWeight: 600
    lineHeight: "30px"
  section:
    fontSize: "16px"
    fontWeight: 600
    lineHeight: "24px"
  body:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif'
    fontSize: "16px"
    lineHeight: "28px"
  ui:
    fontSize: "14px"
    lineHeight: "20px"
  label:
    fontSize: "13px"
    lineHeight: "20px"
  metadata:
    fontSize: "12px"
    lineHeight: "18px"
rounded:
  small: "10px"
  control: "12px"
  row: "14px"
  card: "22px"
  dialog: "26px"
  pill: "999px"
spacing:
  one: "4px"
  two: "8px"
  three: "12px"
  four: "16px"
  six: "24px"
  eight: "32px"
components:
  button-primary-solid:
    backgroundColor: "{colors.ui-blue}"
    textColor: "{colors.ui-on-blue}"
    rounded: "{rounded.pill}"
    padding: "8px 18px"
    height: "40px"
  button-secondary:
    backgroundColor: "{colors.ui-glass}"
    textColor: "{colors.ui-ink}"
    rounded: "{rounded.control}"
    padding: "7px 12px"
    height: "40px"
  navigation-selected:
    backgroundColor: "{colors.ui-glass-strong}"
    textColor: "{colors.ui-ink}"
    rounded: "{rounded.row}"
    padding: "10px"
    height: "44px"
  glass-section:
    backgroundColor: "{colors.ui-glass}"
    textColor: "{colors.ui-ink}"
    rounded: "{rounded.card}"
    padding: "22px"
---

# Design System: SparkX workbench

> Language: English | [简体中文](zh-cn/DESIGN.md)

## Overview

**Creative North Star: "A quiet local-agent workspace in pale frosted glass."**

The user-supplied Iconly component reference accepted on 2026-10-19 establishes
the material, action shapes and paired icon states. That treatment now extends
from the homepage across the existing workbench: settings, schedules, mail,
notifications, task search, document selection, device menus, account menus and
confirmations. It is one visual system, with the existing routes, content,
capabilities, layouts and workflows retained.

This is an operator interface. Reading, composing and understanding actual state
take priority over material effects. The homepage keeps its accepted geometry;
secondary pages retain their established information structure and density.

**Key Characteristics:**

- Cool gray ground, milky translucent surfaces, bright inner edges and soft depth.
- Blue primary actions; authored line and solid glyphs tied to real UI state.
- Stable layouts and accessible interactions across themes and text preferences.

## Colors

The frontmatter records the shared light-theme primitives. Runtime theme and
preference mappings remain in the CSS; update this snapshot when those tokens
change. Shared material uses `--ui-*` in `apps/webchat/src/styles/glass.css`.
The accepted homepage keeps its `--home-*` material and geometry in `home.css`.

### Primary

Use `--ui-blue` for primary actions, active accents and focus. Shared primary
buttons use `--ui-primary` and `--ui-primary-hover` gradients; mail send/reply and
confirmation actions use the solid blue. The shared blue is deep enough to keep
white action labels legible. Use `--ui-on-blue` for the foreground; dark mode
remaps both together rather than retaining white text on the lighter dark-theme
accent.

### Neutral

`--ui-ground` is the workspace ground. `--ui-ink` carries primary text;
`--ui-muted` carries supporting text and resting icons. `--ui-glass` is the quiet
container material; `--ui-glass-strong` supports floating and selected surfaces.
`--ui-glass-edge` and `--ui-glass-highlight` define the bright edge. Dividers and
field edges use their own low-contrast tokens. Dense mail reading surfaces stay
nearly opaque through their derived reading token.

Danger, warning and success retain the semantic foundation tokens and their
theme mappings. Recording remains red and processing remains amber. Disabled
controls remain muted and actually disabled.

## Typography

Use the system UI stack and existing title, section, body, UI, label and metadata
roles. The homepage may use its incumbent SF Pro stack and welcome headline
scale. Small and Large text modes remap the shared type and line-height roles
through `appearance.css`; controls must accommodate those metrics. Small also
updates fixed homepage reading roles while keeping the wordmark, welcome display
heading and control geometry. Persist Small, Default and Large through the same
local preference so they restore on startup.

Chinese reading roles use a compact scale in `chinese-typography.css`: Default
body/UI are 16px/14px, Small is 14px/12px, and Large is 17px/15px. Line
heights follow each role. Conversation text, sidebar titles and the composer
read at 13px (Small 12px, Large 14px) with 1.75 leading. Apply these mappings to the existing homepage labels
and composer as well, while preserving the brand wordmark and welcome display
heading. Both web and desktop synchronize the document language on startup.
English retains its existing scale.

Appearance includes a sample conversation that inherits the live theme, type and
surface tokens. It follows the controls in a single column at every width. Standard conversation measure is 760px; Wide is 1040px and also
widens the welcome composer. Transparency and motion each offer System or
Reduced, respect system accessibility preferences, and persist locally. Reduced
transparency makes surfaces opaque and removes backdrop blur; Reduced motion
also shows incoming reply text immediately. Preference changes apply without a
reload to both the preview and actual workbench.

League Spartan is reserved for the **Spark X** wordmark. Preserve the literal
space, shared letter metrics, blue X and accepted optical offset
(`translateY(3px)`). Its metrics are recorded in `typography.wordmark` above.
The supplied graphic mark stays unchanged; component effects and SVG rules must
not alter it.

## Layout

Retain the desktop left sidebar, central work area, responsive drawer and every
existing entry point. Settings keep their grouped navigation and form flow;
schedules keep their table and create/delete dialogs; mail keeps its three-pane
workspace, mobile pane navigation and protected send/review flow.

The shared desktop shell uses an outer inset and gap (14px), with the existing
header height (68px). At the drawer breakpoint (840px), the shell loses that
outer inset and the sidebar becomes a viewport-height drawer. Smaller page
layouts compact at 600px; mail becomes a full-screen window at 720px. Schedule
columns progressively adapt at 900px and 480px. Preserve these existing
responsive behaviors rather than changing the information architecture.

The desktop client draws one 48px title bar above a full-height shell. It holds
space for the inset macOS window controls, Back and Forward page history, the
sidebar toggle and the workspace actions; the window is dragged from its empty
space. The sidebar and settings navigation sit flush beneath it with a divider
instead of a floating card. Linux and Windows keep their native frame above the
same title bar. The web keeps its in-page topbar without page history. The
desktop home uses the same light welcome presentation as the web home without
the suggestion row.

The desktop window follows Codex: the title bar and an 8px margin keep the frame
colour, and everything below sits in one rounded sheet whose sidebar,
conversation and browser columns are told apart by lines; the sidebar is a
slightly darker surface than the white conversation and browser. Short
separators in the title bar line up with those lines, and the workspace part
shows the open conversation's title in 12px medium type. With the sidebar
collapsed, or in windows of 840px or less, the leading part is only as wide as
its controls. Schedule sits under New conversation and matches it exactly, and
the search and new conversation buttons beside Recent tasks stay visible within
the label's height. The sidebar keeps an 8px side margin; the brand, row icons,
Recent tasks label and conversation titles share one left edge, and rows read at
14px on 32px lines.
Sidebar conversation rows follow Codex: a plain title that fades out at the row
edge instead of an ellipsis. Hover or keyboard focus shows the archive and
ellipsis actions on the right and scrolls a long title once to its end, both
edges fading while it moves, without looping (not under reduced motion); a right click opens the same menu at the
pointer. Conversation
history fades under the title bar and above the composer only while more
history lies in that direction. The schedule page sits lower and offers a row of
example requests that open the create dialog filled in. In the mail window the
toolbar rows, list rows, messages and the reply box stay inset from the window
and pane edges. The desktop window opens at 1250x800, shrinking only to fit a
smaller work area, and can shrink to 640x560.

The desktop browser opens at 440px beside the conversation and narrows to keep at
least 520px for it; it takes over the workspace only when even a 360px browser no
longer fits. A dragged width is kept for the next open in the same run but is not
stored, so a restart returns to the default; a drag cannot cover the
conversation, and a widened browser can still be dragged back from its edge.
Its tabs, new-tab, widen and collapse controls sit in the title bar above the
panel, and that part of the title bar follows the panel's width, while the
notification and browser buttons stay over the conversation. Like the sidebar,
the panel sits flush in the sheet with a single divider; the title bar keeps its
frame colour above it, and the selected tab is a chip in the pane's colour, as
in Codex. The address bar
continues the same surface, and its field is a quiet fill without an outline.

The six-column homepage composer must fit at 320px. Keep its overflow visible
so the voice-device menu can escape the card. Document overlays keep viewport
insets (16px), and dialogs constrain their width and height to the viewport.
Check existing and new interactions at 320px and 390px, including large text,
long labels, drawer navigation and dialog actions.

## Elevation & Depth

Depth comes from translucent planes, a thin bright border, an inset top highlight
and soft downward shadows. Sections use quiet depth; menus, notifications and
dialogs use a stronger floating shadow. Nested settings records remain quieter
than their containing section and do not become repeated elevated cards.

**The Surface Frost Rule.** Blur belongs on the visible sidebar, card or floating
surface itself. Keep workspace, header and overlay ancestors unfiltered so fixed
notifications and dialogs keep their viewport positioning. Native dialog
backdrops may use their existing light blur.

`body.glassWorkbench` supplies shared tokens to portals mounted under the body;
the web and local workbench also keep the class. Do not make a portal depend on
homepage ancestry. Dark mode remaps shared materials to dark translucent planes.
Reduced-transparency mode makes them opaque and removes backdrop blur. Exact
shadow and blur recipes are recorded in `.impeccable/design.json` and the CSS.

## Shapes

Use gently rounded rectangular glass controls, larger rounded section surfaces
and floating windows, and pill-shaped primary text actions. Icon-only send
controls retain their circle. The frontmatter records the recurring radius
scale; preserve component-specific geometry such as the homepage composer,
full-screen mobile mail window and asymmetric message corners.

Controls generally use the shared comfortable height (40px), with compact icon
actions (36px). Navigation rows retain their existing larger hit areas. Bright
borders describe glass edges; use muted dividers within dense lists and fields.

## Components

### Buttons and fields

Primary, secondary and danger roles remain distinct. Secondary controls use
quiet glass; hover increases surface opacity and contrast. Primary actions use
the shared blue treatment, and destructive confirmation uses a filled danger
action. Keep visible keyboard focus through the existing blue outline or
field-focus ring. Field backgrounds are more opaque than the surrounding plane.
Disabled controls preserve the real disabled attribute, suppress press movement
and lose their active shadow.

### Paired icons and navigation

`ActionIcon` contains authored line and solid drawings on one canvas (24px),
usually rendered at the shared icon size (20px) with rounded line strokes
(1.75 units). Small close and disclosure glyphs can remain smaller. Solid states
use dedicated silhouettes with transparent details; never fill arbitrary outline
paths or apply broad SVG resets.

**The Real State Rule.** Resting and hover states keep the line glyph. Real
open, selected, current or pressed state shows the solid glyph and the more
opaque selected surface. Closing a menu or panel restores its line trigger.
One-shot actions may show the solid glyph during an enabled press. Disabled
buttons do not gain a pressed state.

This pairing covers workbench actions, settings navigation and mail categories.
Use semantic state attributes such as `aria-expanded`, `aria-current`,
`aria-selected` and `aria-pressed` alongside existing selected classes. Icons are
decorative (`aria-hidden`); accessible names stay on their controls. Voice stop
and progress indicators retain their specific symbols. The brief opacity
transition (120ms) disappears under reduced motion; avoid decorative animation.

Connections directory identities use the user-requested frosted two-layer
artwork: an opaque rear shape in brand blue (`#086DE1`) and a pale blue glass
front with a soft overlap. The eight generated cutouts fill the existing 44px
desktop / 40px mobile icon footprint without an extra surrounding tile. Keep
their transparent backgrounds, decorative semantics and current entry actions.
Assets and generation prompts live in `apps/webchat/src/assets/connections/`.

Provider identities inside connection details use imagegen-produced raster
line icons referenced from the original product marks: QQ Mail, Outlook, Gmail,
ChatGPT, Claude, Gemini, Grok, Weixin and Telegram. Render their generated alpha
at 22px using the current theme's brand blue. Keep all provider silhouettes in
the generated files, without authored SVG paths. Assets and prompts are in
`apps/webchat/src/assets/connections/providers/`. Keep these marks decorative
and retain the existing login, refresh, default-provider, toggle and delete
controls.

### Pages, windows and menus

Settings sections use shared glass with quieter nested rows, segmented choices
and switches. Schedules use the same material on their existing list and dialogs.
Mail's category selection, conversation selection and reading pane retain their
distinct hierarchy. Notification inboxes and toasts apply frost to themselves,
preserving their fixed positioning. Search, document, device and account
surfaces share the floating material without removing their current controls.

Conversations keep open assistant responses and softly rounded user messages
with a pale brand-blue surface, including the Appearance sample. User messages
carry no header; replies show only their time, and managed MCP requirements keep
their label. Replies have no thumbs or correction footer.
Attachments and task statuses retain their shared glass material and semantic
colors. A labelled New conversation entry sits above Recent tasks. Sidebar rows reveal
an archive action and a single right-aligned ellipsis on hover or keyboard
focus; touch devices keep them visible. Archiving only hides a conversation from
Recent on this device (browser storage, per owner); the account menu opens the
Archived list, where the same action restores it. Rename and delete live in the
floating menu, with viewport clamping, keyboard navigation and the existing
delete confirmation. Current rows, settings navigation, mail categories and the
schedule entry use a quiet grey selection fill rather than raised glass. Chat composer controls use 36px on desktop and
the existing compact geometry on small screens. Homepage composer geometry
stays unchanged. `conversation-preview.html` is a development-only review entry
that mounts the real sidebar, message and composer components with in-memory
example data; it is excluded from the production build and never submits work
to a gateway.

Schedule navigation stays in the shared sidebar footer, above the account
menu, on both the homepage and the schedule page. Keep its geometry and spacing
consistent across those destinations. On the web, the schedule page has an
icon Back to home action in its topbar; the desktop uses title-bar page history.
Either changes the page without creating a conversation or clearing the current
conversation or input draft.

Runtime pairing expands inline using the same glass field and blue action.
Its password field and key action share a 40px height; narrow layouts place
the form beneath the connection copy on both home and settings surfaces.
The key's line and solid states share the same visible outer contour, shaft,
single short tooth and hole position, so selection only changes its weight.

Deep expansions follow the same material: credential and policy editors,
issuance and integration actions, mail sync and review forms, voice actions,
and browser overflow, transfer and permission rows. Shared residual controls
live in `glass-expanded.css`; preserve compact 36px actions where space is
limited and use 40px form fields, clear blue focus and semantic danger states.
Single-select menus across settings, mail and desktop login retain their native
selection and keyboard semantics with a shared picker in `glass-select.css` on
supported engines. Use the quiet solid glass material, 16px corners, 40px option
rows and a brand-blue selection check. Anchor below the control, align its end
edge and flip only when viewport space requires it. Mail's address trigger keeps
its separate truncated value and chevron; its menu stays wide enough to read an
address. Escape dismisses any open mail picker before the outer mail dialog.
Search and address controls show focus on the rounded outer field, without a
second rectangular outline around their inner control.

### Unified mail workspace

Mailbox, search, category and conversation form one view scope. Reopening mail
restores that scope. Any navigation first saves active reply work; failure keeps
the editor and its text in place. Incoming mail does not replace an active reply
target. Message details, reply identity and send confirmation show the actual
receiving/sending address.

Classification rules control future sender routing; category tabs only select
the current view. The category icons are distinct: chat/check, bell and inbox,
with matching line and solid contours.

Receiving accounts stay in the existing sync expansion, grouped by provider.
Each additional account has its own sign-in, identity check and receiving toggle.
Opening sign-in never starts receiving. Keep unavailable actions disabled using
the runtime and transport capabilities; an unavailable runtime still permits
stopping an existing account when its management API remains available.

Additional receiving accounts are a frontend surface until a backend provides
them: account rows and "Add account" stay visible, and their actions remain
disabled unless the account list reports `multi_account_available` and, on
desktop, the connection exposes the `mail_accounts` surface. Desktop keeps its
existing rule of no email sign-in button; identity checks and resume after
backend sign-in stay available.

Original-file cleanup confirms its exact mailbox or global/date scope before
submission. The confirmation states that local originals and attachments become
unavailable while message metadata/text and remote mailbox messages remain.
It stays inline inside the native mail dialog so it remains visible above that
dialog, focuses Cancel and consumes Escape as cancellation.

### Feedback surfaces

Errors and validation feedback use the same neutral glass surface, a 1px edge,
14px corners and normal readable body text. Do not use thick colored side rails
or large red fields. A compact status icon carries error, warning, success or
progress color; keep recovery actions beside the relevant message and preserve
all error text, alert/status semantics and disabled states.

### Composer approvals and binding windows

Composer approvals replace the composer input with a card (22px) on the same
strong glass. Answer rows use 14px corners and a 44px minimum height; the
selected row gains the blue edge. Submission is the blue pill primary action
and stays disabled until a choice exists. The WeChat binding window uses the
floating dialog material (26px), with the QR code on a white 16px tile.

### Confirmations

Application confirmations use the shared `ConfirmationProvider` and `useConfirm`
dialog. It is a labelled modal with focus containment, **Cancel** focused by
default, Escape and direct overlay click mapped to cancellation, and focus
returned to the originating control when it closes. Requests are queued; owners
cancel their pending requests on unmount, and provider teardown resolves pending
requests as cancellation. Retain the semantic danger option for destructive
operations. The native `window.confirm` fallback exists only for standalone
components mounted outside the provider.

## Do's and Don'ts

### Do

- **Do** extend the shared material through `--ui-*` while retaining homepage geometry and `--home-*` rules.
- **Do** preserve all existing routes, menus, controls, business behavior and API boundaries.
- **Do** verify original interactions alongside new styling in light, dark, large-text and reduced-preference modes.
- **Do** show failure or recovery guidance when the user needs to act, and keep temporarily unavailable entry points with disabled operations.

### Don't

- **Don't** replace existing screens with simplified pages or remove entry points to make the styling easier.
- **Don't** blur workspace or header ancestors, clip floating menus, or make body portals depend on homepage-only tokens.
- **Don't** change the Spark X wordmark, graphic mark, icon state semantics or confirmation focus behavior.
- **Don't** add artificial connection states, placeholder data, permanent technical status UI or decorative animation.
