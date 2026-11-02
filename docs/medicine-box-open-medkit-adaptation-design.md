# Medicine Box UI Adapted from open-medkit Design

> Language: English | [简体中文](../zh-cn/docs/medicine-box-open-medkit-adaptation-design.md)
>
> Date: 2026-11-02. Status: design settled; the owner confirmed the items in section 9 on 2026-11-02. Both phases were implemented on 2026-11-02 (section 10); the real-model spot check passed the same day, and the paired release went live that day (section 10.1). Reference: [MonoYan/open-medkit](https://github.com/MonoYan/open-medkit) commit `8c2217ba707cf239ec3c8285c3ff3aa3b357a1c4` (MIT licence). Scope: the desktop medicine box workspace, the detail dialog, the add/edit dialog, the medicine fields and their local data migration, and text entry. The module rail, the medicine sidebar and the photo recognition path stay.

## 1. Background and goals

The medicine box was committed in `add4e7dc` (Gateway `medicine.recognize`) and `21a455cb` (desktop/WebChat). It has not been pushed or installed. The workspace is currently a single row list. Once the box holds more than a few medicines, finding one gets slow, and the list does not show which ones expire soon.

The owner asked for the medicine box UI to follow open-medkit directly, adapted to SparkClaw. This design will:

1. Adopt open-medkit's information structure and interactions: a stats line, search, expiry filters, a card/list toggle, sorting, a detail dialog and a two-step add flow.
2. Keep the look of the SparkClaw client: the glass theme tokens, light and dark themes, fonts, and the Codex-style module rail and sidebar. open-medkit's warm paper palette, serif fonts and dark header bar are not adopted.
3. Keep the owner's earlier decisions: eight fixed functional categories, remaining quantity as a number plus a unit, recognition from a photo of the package front, and no camera capture.
4. Apply the owner's 2026-11-02 decisions: the medicine fields become name, expiry, quantity left, location, usage and notes (the functional category stays); text entry alongside photo entry, one medicine at a time; both save the same format, and nothing is added until the owner confirms every field; after photo recognition the owner enters the quantity left and the expiry date; the detail dialog gets quick quantity adjustment; AI search is not implemented for now.
5. Proceed in two phases. Phase 1 changes the UI and local data and ships with the desktop app alone. Phase 2 adds text entry and adjusts the recognition output, and needs a paired Gateway and desktop release.

## 2. Reference UI breakdown

Based on that commit's `frontend/src` sources, `backend/src/ai/prompts.ts` and the README screenshots.

| Surface | open-medkit |
|---|---|
| Header | Dark bar with the logo, a centred "AI search / Medicine list" segmented switch, and on the right an "Add medicine" primary button, a theme toggle and settings |
| Page header | Large "Medicine list" title, then a line of small monospaced stats: `Total 12 · Expiring in 30 days N · Expired 1`; zero counts are hidden |
| Category panel | Separate left panel: "All medicines" plus each category, each row with a count; the selected row is filled dark |
| Toolbar | Sticky. Search (name, brand, usage, location); status segments "All / Expiring in 30 days / Expired"; refresh, a card/table view toggle, and sort segments "By expiry / Recently added". When filters are active, filter chips and "Clear filters" appear below |
| Card | Auto-fill grid `minmax(260px, 1fr)`. Name with a status badge at the top right (Expired / Expires in N days / In date); English name and pack size in monospace; usage clamped to two lines; below a divider: expiry, quantity left, location. Expired cards get a light red background, expiring cards a light orange one |
| Table | Name (with pack size), category, expiry, status, quantity, location, actions (edit/delete) |
| Detail | Dialog: title with category and status badges; a one-line banner by status; two-column info cards (brand, pack size, expiry, quantity left, location); "Usage / indications" and "Notes" sections; "Edit" and "Delete" at the bottom; delete asks for confirmation |
| Add | Two steps. Step 1, "Describe": one sentence, or several lines for a batch, or a camera/album photo (with expiry and location filled in next to it). Step 2, "Review and submit": a "Key details" group and a "More details" group; fields filled by AI flash once |
| AI search | A home page with a large "What do you want to look up today?" title and a centred search box. The whole box and the question go to the model, which streams a short Markdown answer; matched medicines are listed from IDs the model returns. Suggested questions are generated from the current state of the box |
| Other | Expiry push (Telegram / Discord / Lark / email), an MCP server, login, AI provider settings, Docker self-hosting |

Data model (`medicines` table): `name`, `brand`, `name_en`, `spec`, `quantity` (free text), `expires_at`, `category` (free text, seven defaults), `usage_desc`, `location`, `notes`. Expiry rule: `expires_at < today` is expired; `today ≤ expires_at ≤ today + 30 days` is expiring; no expiry date is unknown.

## 3. Current SparkClaw state

| Layer | Current state |
|---|---|
| Entry | "Home / Medicine box" in `ClientModuleRail`; the `module` state in `LocalWorkbench` switches the sidebar and the workspace |
| Sidebar | `MedicineSidebar`: title, an "Add medicine" button, and a "By function" list (All plus eight categories, each with a count; empty categories are dimmed) |
| Workspace | `MedicineWorkspace`: a title (the current category or "Medicine box"), a description and an "Add medicine" button; a row list sorted by name, each row showing name, pack size · category, quantity left, expiry text and manufacturer, with edit/delete on hover |
| Dialog | `MedicineDialog`: a single form. When adding, a photo area sits on top (choose, drop or paste). Recognition starts as soon as a photo is chosen and fills name, pack size, manufacturer, category and unit; the owner enters the quantity left and the expiry date |
| Data | `MedicineItem`: `id`, `name`, `spec`, `manufacturer`, `category` (eight fixed values), `quantity` (number), `unit` (nine fixed values), `expires_on`, `created_at`, `updated_at`. Main-process SQLite at `user_version = 1`, scoped to deployment and account, at most 2000 rows |
| Recognition | `POST /api/medicine/recognize`, ISCP operation `medicine.recognize`, strict JSON `medicine_recognition_v1`; the photo is never stored |
| Unavailable | Without the bridge, the add buttons are disabled. Without the recognition surface, choosing a photo is disabled, and the form can still be filled in by hand |

## 4. Adaptation principles: element by element

| open-medkit element | Decision | Notes |
|---|---|---|
| Dark header, logo, theme toggle, settings | Not adopted | SparkClaw already has a window title bar, a global theme and settings; the medicine box gets no header of its own |
| "AI search / Medicine list" segments and AI search | Not for now | The owner will discuss it later |
| Page stats line | Adopted | Placed under the workspace title |
| Left category panel | Mapped to the existing sidebar | No second category panel; `MedicineSidebar` keeps its style and counts |
| Search, status segments, filter chips, clear filters | Adopted | Computed on the client |
| Refresh button | Not adopted | The data is local and is already re-read on activation and after saves |
| Card/list toggle | Adopted, cards by default | The list view keeps the existing inline edit/delete |
| Sort (by expiry / recently added) | Adopted | Defaults to by expiry |
| Card style and status backgrounds | Adopted with SparkClaw tokens | See 5.10 |
| Detail dialog | Adopted | New; opened by clicking a card; adds quick quantity adjustment |
| Two-step add | Adopted | Step 1 takes a photo or text, step 2 is one shared review; no camera capture; see 5.8 |
| One-sentence text entry | Adopted (phase 2) | One medicine per entry; text naming several asks for separate entries; see 6.2 |
| Multi-line batch entry | Not adopted | The owner decided not to support several medicines in one entry |
| Location, usage, notes | Adopted (phase 1) | All three are optional; see 6.1 |
| Free-form categories | Not adopted | The eight fixed functional categories stay, and the sidebar groups by them |
| Free-text quantity | Not adopted | The number plus unit stays |
| Brand, English name, pack size | Not adopted | Not among the fields the owner chose |
| Expiry push, MCP, login, provider settings, Docker | Not adopted | See section 7 |
| Warm paper palette, Noto Serif SC, Fraunces, DM Mono | Not adopted | Stays consistent with the other client modules |

SparkClaw's existing pack size (`spec`) and manufacturer fields are removed as well; 6.1 covers the existing data.

The sources are a design reference only and are not copied wholesale. If the implementation ports a piece of code (for example the status logic), that file keeps the MIT copyright notice in its header.

## 5. Target UI design

### 5.1 Overall layout

```text
+------+----------------+------------------------------------------------------------+
| Home | Medicine box   | Medicine box                         [+ Add medicine]      |
|      | [+ Add]        | Total 12 · Expiring in 30 days 2 · Expired 1               |
| Med  |                | ----------------------- Results panel ------------------   |
|      | By function    | [Search] (All|Expiring in 30 days|Expired)                 |
|      | All        12  |                    [Cards|List] (By expiry|Recently added) |
|      | Cold        3  | Category · Cold  Status · Expired  [Clear filters]         |
|      | Fever       2  | ------------------------------------------------------     |
|      | Pain        1  | [card] [card] [card]    auto-fill, minmax(260px, 1fr)      |
|      | Cough       0  | [card] [card] [card]                                       |
|      | Digestive   2  | [card] [card] ...                                          |
|      | Allergy ...    |                                                            |
+------+----------------+------------------------------------------------------------+

+------------------------------------+
| Name                [status badge] |
| Functional category                |
| Usage (two-line clamp, optional)   |
| --------------------------------   |
| Expiry  27 Dec 2027                |
| Left  14 capsules  |  Living room  |
+------------------------------------+
```

The module rail, the sidebar and the window title bar keep their structure and behaviour. The workspace title and the primary button stay where they are; below them, the body becomes a panel with a toolbar and a results area.

### 5.2 Page header and stats line

- The title is unchanged: "Medicine box" when "All" is selected, otherwise the category name.
- The description "Keep track of the medicines you have at home, sorted by what they treat." moves into the empty state, and the stats line takes its place in the header.
- The stats line uses small text and tabular figures (`font-variant-numeric: tabular-nums`, no font change): `Total N`, then `· Expiring in 30 days N` (expiring colour) and `· Expired N` (danger colour); a zero count is not shown.
- Stats cover the whole box and ignore the category, search and status filters, the same basis as the sidebar counts.
- The "Add medicine" primary button keeps its position and disabled rules.

### 5.3 Toolbar

Sticky at the top of the results panel. The panel uses a `--ui-glass-strong` background, `--radius-panel` corners and a `--ui-divider` separator.

- Search: placeholder "Search name, usage, location, notes". Matching ignores case and surrounding spaces, and also matches category labels.
- Status segments: "All / Expiring in 30 days / Expired". "Expiring in 30 days" fills with the expiring colour, "Expired" with the danger colour, and "All" with inverted `--ui-ink`.
- View toggle: two icon buttons for cards and list (lucide `LayoutGrid`, `List`), with `aria-pressed` and tooltips; cards by default.
- Sort segments: "By expiry" (ascending; medicines with no expiry come after dated ones, and earlier rows with no date filled in come last; ties broken by name) and "Recently added" (newest first). Medicines with the same name always sit together; see 5.13.
- Filter chips: when the category is not "All", the status is not "All" or the search is not empty, chips such as `Category · Cold`, `Status · Expired` and `Search · ibuprofen` appear under the toolbar, followed by "Clear filters". Clear filters resets all three, and the sidebar selection returns to "All medicines".
- The three filters combine: sidebar category, status and search.
- At narrow widths the toolbar wraps into two rows: search and status on one, view and sort on the other.

### 5.4 Card view

- Grid: `grid-template-columns: repeat(auto-fill, minmax(260px, 1fr))`, 16px gaps, 20px panel padding.
- Card: 18px radius, 1px `--ui-divider` border, `--ui-glass-strong` background and a light shadow. On hover it rises 2px and the shadow deepens. The whole card is one `<button>` that opens the detail dialog.
- First row: the name (17px, weight 600, single-line ellipsis) with the status badge on the right. Badge text: `Expired`, `Expires today`, `Expires in N days`, `In date`, `No expiry`, `No expiry date`. `No expiry` is an explicit choice by the owner and uses a neutral colour. New entries need an expiry date or the no-expiry tick, so `No expiry date` only appears on medicines added earlier. Medicines sharing a name also carry an "N entries" tag next to the name; see 5.13.
- Second row: the functional category label, 12px secondary colour.
- Third row: the usage, clamped to two lines. If any card in the current results has a usage, cards without one keep a two-line gap so they stay aligned; if none has one, the row is left out entirely.
- Below the divider: `Expiry  27 Dec 2027` (the date is coloured when expired or expiring, and reads "No expiry" when there is none), then `Left  14 capsules │ Location  Living room`; an empty location shows "Not set" in the secondary colour. At 0 the quantity reads "Used up".
- Status backgrounds: expired cards use the danger background at 60% with a 15% danger border; expiring cards use the expiring background at 50%; all others use the normal card style.
- Cards carry no edit/delete buttons; those actions are in the detail dialog and the list view.
- Entry animation: `fadeUp` (16px offset, 0.45s), staggered by 50ms for at most 12 cards; turned off under `prefers-reduced-motion`.

### 5.5 List view

The existing `medicineRow` becomes a table (`<table>`), keeping the existing hover edit/delete:

| Column | Content |
|---|---|
| Name | Medicine name |
| Category | Functional category label |
| Expiry | The date, "No expiry" or "Not set" |
| Status | The same badge as on cards |
| Left | Quantity and unit, tabular figures; "Used up" at 0 |
| Location | Storage location, "—" when empty; hidden below 1024px |
| Actions | Edit and delete icon buttons, shown on hover or focus |

Clicking a row opens the detail dialog; edit opens the edit dialog directly; delete keeps the existing danger confirmation. Below 600px each row folds into two lines (name and badge / expiry · quantity · location and actions). Medicines sharing a name get a group header row; see 5.13.

### 5.6 Detail dialog

Reuses the existing dialog shell (`documentPickerOverlay` and `useDismissibleLayer`, with Esc, click-outside and focus trapping), at most 720px wide.

- Header: the name (22px), followed by the category tag and the status badge; a close button at the top right.
- A banner by status:
  - Expired: "This medicine has expired. Do not use it; clear it out soon."
  - Expiring: "This medicine expires within N days. Use or replace it soon."
  - In date: "In good order."
  - No expiry (ticked): "This medicine has no expiry date."
  - No expiry date filled in: "No expiry date yet. Consider adding one."
- Two-column info cards: expiry, quantity left, location, functional category, date added. The quantity card has quick adjustment; see 5.7.
- Sections: "Usage" and "Notes", showing "Not set" when empty.
- Same name: when other medicines share this name, their expiry, quantity and location are listed below; clicking one switches to its details; see 5.13.
- Footer: "Edit" (secondary, switches to the edit dialog) and "Delete" (danger secondary, through the `useConfirm` danger confirmation). If delete fails, the error shows in the footer and the dialog stays open.

### 5.7 Quick quantity adjustment

- Placement: the "Quantity left" card in the detail dialog, shown as `[−] 14 capsules [+]`. It is not on cards or list rows, to avoid accidental taps. For millilitres and grams there are no buttons; the quantity is changed through edit.
- Each click adds or removes one unit, with a floor of 0 and a ceiling of 100000. Fractional quantities (such as 2.5 ml) also move by 1, and drop to 0 when less than 1 would remain. "−" is disabled at 0.
- Each click saves at once: `save()` is called with the medicine's full current fields, changing only `quantity`, so the saved format is exactly that of an edit.
- Both buttons are disabled while saving, and the number updates on screen first. A local SQLite write takes milliseconds, so clicks are not merged or debounced.
- If saving fails, the number returns to its previous value and the footer shows "The quantity was not saved. Try again."
- After a successful save the list updates. At 0, the quantity on cards and list rows reads "Used up" in the secondary colour. The medicine is not deleted automatically, and its expiry status is unaffected.
- Accessibility: the buttons' `aria-label` reads "One capsule fewer" / "One capsule more" (with the medicine's unit), and the number has `aria-live="polite"`.

### 5.8 Add/edit dialog

Adding takes two steps, with step tabs "① Add" and "② Review" at the top. Photo and text are only two ways in; step 2 is identical.

Step 1: add

Step 1 opens with a "Photo / Text" segmented switch, defaulting to Photo; when photo recognition is unavailable and text reading is available, it defaults to Text.

Photo:
- Keeps the existing photo area: a "Choose photo" button, drop, paste, the thumbnail and "Choose another". No "Take photo" button and no `capture` attribute.
- The photo only identifies the medicine: its name is read from the package front. The model then gives the functional category, the unit and, from phase 2, the usage (the medicine's main efficacy) from its own knowledge of that medicine, whether or not the package prints it (owner decision, 2026-11-02). Section 10.2 covers how the exact product is identified. The quantity left and the expiry date are never read from the photo, even if a date is printed on it; the owner enters them in step 2, as well as the location and notes.
- One fixed line under the photo area: "The photo is only used for recognition and is not kept. Enter the quantity left and the expiry date in the next step."
- Recognition starts as soon as a photo is chosen, with the existing "Recognizing the medicine…" text; on success the dialog moves to step 2 automatically.
- On failure (not a medicine, unreadable photo, service unavailable), the dialog stays on step 1, shows the existing problem message and offers "Fill in".

Text (phase 2):
- A multi-line box with the placeholder "e.g. Ibuprofen SR capsules, 14 left, expires December 2027, living room drawer, for fever and pain", at most 500 characters.
- One medicine per entry. "Read text" or ⌘/Ctrl+Enter sends it to the backend; while it runs, "Reading…" shows and it cannot be submitted again.
- The name, quantity left, unit, expiry, location and usage written in the text are filled in; anything not written is not guessed. The functional category is judged from the name and usage. An expiry given only as a month becomes the last day of that month. Notes are never taken from the text. The result is checked in step 2.
- One fixed line under the box: "The text is only used to fill in the form and is not kept. Add one medicine at a time, and check the result in the next step."
- On success the dialog moves to step 2 automatically.
- If the text names more than one medicine, the dialog stays on step 1, fills nothing and says "Add one medicine at a time."
- If no medicine is found, it stays on step 1 with "No medicine was found in the text. Include its name, or fill it in."; if the service is unavailable, "Text reading is unavailable right now. You can fill it in."
- Without a connection, or when the peer lacks the `medicine.parse` operation, the "Text" segment stays but is disabled, with a tooltip giving the reason.

Shared:
- "Skip and fill in" is always at the bottom. When neither photo nor text is available, the dialog starts at step 2.

Step 2: review

- Photo entry, text entry and manual entry share one form, one set of validation rules and one saved `MedicineFields`; the entry method is not recorded. It only decides which fields arrive pre-filled.
- "Key details" (required): name, functional category, quantity left with unit, expiry (or the no-expiry tick).
- "More details" (optional): location, usage, notes.
- Where each field comes from:

| Field | Photo entry | Text entry | Manual |
|---|---|---|---|
| Name, functional category, unit | Filled by recognition, to check | Filled from the text, to check | Typed by the owner; the name suggests a category |
| Quantity left, expiry | Empty; the owner must enter them | Filled and marked to check if the text has them, otherwise empty | Typed by the owner |
| Location | Empty; optional | Filled and marked to check if the text has it, otherwise empty | Typed by the owner; optional |
| Usage | From phase 2, the model's efficacy for the recognized medicine, marked to check | Filled and marked to check if the text has it, otherwise empty | Typed by the owner; optional |
| Notes | Empty; optional | Empty; optional | Typed by the owner; optional |
| No expiry | Ticked by the owner | Ticked by the owner; never ticked automatically, even if the text says so | Ticked by the owner |

- Expiry defaults to a full date. A "Month only" switch next to it changes it to a month picker and saves the last day of that month; a "No expiry" tick disables and clears the date. One of the three is required: a date, a month, or no expiry. The saved format does not change: `expires_on` is a date or an empty string, plus `no_expiry`. When edited later, a month-only expiry shows as the last day of that month.
- For photo entry the top of the form shows the photo as a thumbnail (72px high) that enlarges on click, so the fields can be checked against it; for text entry the same place shows the original text, collapsible.
- If medicines with the same name are already in the box, a line above the form says "N entries with this name are already in the box. This one will sit next to them as a separate entry."; if one of them has the same expiry date, it adds "One of them has the same expiry date; check that this is a different one." This is only a notice and does not block saving.
- Pre-filled fields show "Check" next to their label until the owner edits them. A line above the form reads: "These fields were filled from the photo or text. Check each one before adding."
- Pre-filled fields flash once (`flashField`, 0.5s), not under reduced motion.
- Focus lands on the first empty required field. For photo entry this is always the quantity left; when all required fields are filled, focus lands on the name.
- Typing a name by hand still suggests a category through `suggestMedicineCategory`, without overriding a recognized or chosen category.
- Buttons: "Back" (returns to step 1, keeping the entered fields and the original photo or text), "Cancel" and "Confirm and add". "Confirm and add" is disabled until the required fields are filled.
- Recognition and text results are never written to the box directly; saving happens only when the owner clicks "Confirm and add".

Edit: no step tabs, only the step 2 form with the same required fields, "Cancel" and "Save"; focus starts on the quantity left. A medicine saved earlier without an expiry date must get one, or the no-expiry tick, when it is next edited.

Behaviour after a successful save is unchanged: the dialog closes, the list is re-read, and if a category is selected, the view moves to the new medicine's category.

### 5.9 Empty, loading and error states

- Empty box: icon, "No medicines yet", the original description and an "Add medicine" button, disabled without the bridge.
- No filter matches: "No medicines match these filters" plus "Clear filters".
- First load: six skeleton blocks the size of cards, replaced as soon as the read finishes, with no text.
- Read failure: keeps the existing `localFeedback` message with "Retry".

### 5.10 Visual tokens

Status colours are defined only in the medicine box CSS, on `.medicinePage` and the dialogs, using open-medkit's hues. So that 11–12px badge text reaches WCAG AA 4.5:1, the light-theme expiring and danger colours are darkened:

| Token | Light (text / background) | Contrast | Dark (text / background) | Contrast |
|---|---|---|---|---|
| `--medicine-ok` | `#3a6b4a` / `#eaf3ed` | 5.48 | `#88c699` / `#1f3528` | 6.62 |
| `--medicine-warn` | `#9a5410` / `#fdf0e0` | 5.13 | `#eeb85d` / `#402c16` | 7.33 |
| `--medicine-danger` | `#b2412a` / `#fcecea` | 4.98 | `#ee876c` / `#441f18` | 5.72 |

Dark values switch on `html[data-resolved-theme="dark"]`, as in `glass.css`. All other colours, fonts, control heights and radii come from the existing `--ui-*`, `--radius-*` and `--control-*` tokens. The primary button stays the blue `--ui-primary` gradient; open-medkit's orange-red accent is not introduced. The current hard-coded `#b26a00` and `#c62828` move to these tokens.

### 5.11 Responsive layout and accessibility

- Breakpoints: at 1024px and above, the full toolbar and all table columns; from 600 to 1023px, a two-row toolbar and no location column; below 600px, folded list rows and a single card column.
- Status never relies on colour alone; every badge has text.
- Segmented controls use `role="group"` and `aria-pressed`. The card grid is a `<ul>` with one button per item, whose `aria-label` includes the name and status.
- Keyboard: Tab moves through the toolbar, cards and row actions; Enter/Space opens the detail dialog; Esc closes dialogs and returns focus to the element that opened them.

### 5.12 Copy

`medicineBoxCopy` changes in both Chinese and English. The main entries are below; the existing `spec`, `specPlaceholder` and `manufacturer` entries are removed.

| Key | Chinese | English |
|---|---|---|
| `total` | 药品总数 | Total |
| `expiringWithin` | {days}天内过期 | Expiring in {days} days |
| `searchPlaceholder` | 搜索名称、用途、位置、备注 | Search name, usage, location, notes |
| `statusAll` | 全部 | All |
| `cardView` / `listView` | 卡片视图／列表视图 | Cards / List |
| `sortExpiry` / `sortCreated` | 按过期时间／按添加时间 | By expiry / Recently added |
| `clearFilters` | 清除筛选 | Clear filters |
| `noMatches` | 没有符合当前筛选条件的药品 | No medicines match these filters |
| `expiresToday` | 今天到期 | Expires today |
| `notExpiring` | 未临期 | In date |
| `noExpiry` | 未填有效期 | No expiry date |
| `withoutExpiry` | 无有效期 | No expiry |
| `monthOnly` | 只知道年月 | Month only |
| `sameNameCount` | 共 {count} 条 | {count} entries |
| `sameNameHint` | 药箱里已有 {count} 条同名药品，保存后会和它们放在一起，分开记录。 | {count} entries with this name are already in the box. This one will sit next to them as a separate entry. |
| `sameExpiryHint` | 其中一条有效期相同，请确认是另外一份。 | One of them has the same expiry date; check that this is a different one. |
| `sameNameRecords` | 同名记录 | Same name |
| `location` / `locationPlaceholder` | 存放位置／如 客厅抽屉 | Location / e.g. living room drawer |
| `usage` / `usagePlaceholder` | 用途／如 退烧、止痛 | Usage / e.g. fever, pain |
| `notes` | 备注 | Notes |
| `notSet` | 未填写 | Not set |
| `stepEntry` / `stepReview` | 录入／核对并入库 | Add / Review |
| `entryPhoto` / `entryText` | 照片／文字 | Photo / Text |
| `skipPhoto` | 跳过，直接填写 | Skip and fill in |
| `photoPrivacy` | 照片只用于识别，不会保存。剩余数量和有效期请在下一步填写。 | The photo is only used for recognition and is not kept. Enter the quantity left and the expiry date in the next step. |
| `textPlaceholder` | 例如：布洛芬缓释胶囊，还剩 14 粒，2027 年 12 月到期，放在客厅抽屉，退烧止痛 | e.g. Ibuprofen SR capsules, 14 left, expires December 2027, living room drawer, for fever and pain |
| `parse` / `parsing` | 解析／正在解析… | Read text / Reading… |
| `textPrivacy` | 文字只用于解析，不会保存。一次录入一种药品，解析结果请在下一步核对。 | The text is only used to fill in the form and is not kept. Add one medicine at a time, and check the result in the next step. |
| `textMultiple` | 一次只能录入一种药品，请分开录入。 | Add one medicine at a time. |
| `textNotRecognized` | 没能从文字里认出药品，请写出药品名称，或直接填写。 | No medicine was found in the text. Include its name, or fill it in. |
| `parseUnavailable` | 解析服务暂不可用，可以直接填写。 | Text reading is unavailable right now. You can fill it in. |
| `needsReview` | 待核对 | Check |
| `reviewHint` | 以下内容由照片识别／文字解析填入，请逐项核对后确认入库。 | These fields were filled from the photo or text. Check each one before adding. |
| `confirmSave` | 确认入库 | Confirm and add |
| `usedUp` | 已用完 | Used up |
| `decrease` / `increase` | 减少 1 {unit}／增加 1 {unit} | One {unit} fewer / One {unit} more |
| `quantitySaveFailed` | 剩余数量没有保存，请重试。 | The quantity was not saved. Try again. |
| `keyInfo` / `moreInfo` | 关键信息／补充信息 | Key details / More details |
| `back` | 上一步 | Back |

### 5.13 Medicines with the same name

The same medicine can come in several lots, such as two boxes of ibuprofen with different expiry dates. They are not merged: each is its own entry with its own expiry, quantity and location, but the UI keeps them together.

- Matching: names that are equal after NFKC normalisation, trimming, collapsing repeated spaces and ignoring case count as the same name. Different categories, units or expiry dates still form one group.
- Card view: cards in a group sit next to each other, ordered by expiry within the group. The group's place in the results follows its first entry: the earliest expiry when sorting by expiry, the newest entry when sorting by recently added. Each card in the group shows "N entries" next to the name (when N ≥ 2).
- List view: rows in a group sit together under a group header row, for example "Ibuprofen SR capsules · 2 entries · 34 capsules in total". Quantities are added up when the units match, and listed separately otherwise, such as "14 capsules + 1 box". The header is not clickable; it only labels the group.
- Detail dialog: the other entries in the group are listed (expiry, quantity, location); clicking one switches to its details.
- Filters and search apply first, and grouping runs on what remains, so a group may show only some of its entries, and "N entries" counts the ones shown.
- The stats line and sidebar counts count entries, not groups.
- Adding a medicine whose name already exists shows a notice without blocking; see 5.8.

## 6. Data and interface impact

### 6.1 Phase 1: UI and local data

Medicine fields:

| Field | Type | Required | Rule |
|---|---|---|---|
| `id` | text | generated | `med_` plus 32 hex digits, as today |
| `name` | text | yes | 1–80 characters |
| `category` | enum | yes | Eight functional categories, as today |
| `quantity` | number | yes | 0–100000, at most two decimals, as today |
| `unit` | enum | yes | Nine units, as today |
| `expires_on` | date | required in the UI, unless no expiry is ticked | `YYYY-MM-DD`; the main process still accepts empty so existing rows stay readable |
| `no_expiry` | boolean | no | When true, `expires_on` must be an empty string; defaults to false |
| `location` | text | no | 0–80 characters |
| `usage` | text | no | 0–80 characters |
| `notes` | text | no | 0–500 characters, line breaks allowed |
| `created_at` / `updated_at` | time | generated | As today |

- Text fields keep the existing `text()` validation (trimmed, length-limited, no control characters), except that `notes` allows `\n`.
- Main-process validation: `no_expiry` must be a boolean, and when it is true `expires_on` must be empty. An empty `expires_on` with `no_expiry` false is kept only for earlier rows; the UI never saves it.
- Main-process schema `user_version` 1 → 2, in one `BEGIN IMMEDIATE` transaction: create a table with the new fields, copy every old row into it with empty `location` and `usage` and `no_expiry` false, and fold any non-empty `spec` and `manufacturer` into `notes` as two lines ("规格：…" and "生产企业：…") so nothing is dropped silently; drop the old table, rename, and set `user_version` to 2. A failed transaction rolls back as a whole and is retried on the next start.
- A database with a newer version is still refused. An older client seeing `user_version = 2` refuses to open it and leaves it untouched.
- `MedicineItem`, `MedicineFields`, `MEDICINE_FIELDS` in `medicine-capability.mjs`, the main-process `MedicineStore` and the preview mock switch to the new fields together. The renderer and the main process are in the same package and ship together; the IPC `schema_version` stays 1, and its field set changes with the package.
- `medicine.recognize` is unchanged in phase 1. The renderer ignores the `spec` and `manufacturer` it returns and takes only the name, category and unit.
- Status, search, filtering and sorting run in the renderer over the full `list()` result. The box is capped at 2000 rows, so there is no paging.
- New pure functions with unit tests: `medicineStatus(item, now)` returning `expired | expiring | ok | none | unknown` (`none` meaning no expiry) with a fixed 30-day threshold, built on `medicineDaysLeft`; `filterMedicines`; `sortMedicines`; `medicineGroupKey` and `groupMedicines`; `monthEnd(yearMonth)`; `medicineStats`; `stepQuantity(quantity, delta)`.
- Cards are the default view. View and sort preferences are kept in renderer `localStorage` (keys `sparkclaw.medicine.view` and `sparkclaw.medicine.sort`); they hold UI preferences only, no medicine data.
- Quick quantity adjustment goes through `save()`; no new IPC operation.
- Ships with the desktop app alone. The WebChat build is bundled into the desktop app, so no paired Gateway release is needed.

### 6.2 Phase 2: text entry and recognition changes

Text entry:
- A new ISCP operation `medicine.parse` (`POST /api/medicine/parse`, scope `workbench.text`, no mutation, recovery `read`). It is added to `operations.json` and synced to the desktop app with `node scripts/sync-iscp-operations.mjs`; modelcapacity gains `OperationMedicineParse` on the fast model; the timeout suits plain text and is shorter than recognition's 120 seconds.
- Request: `{ text, today }`. `text` is 1–500 characters after trimming, with no control characters; `today` is the desktop's local date `YYYY-MM-DD`, used for phrases such as "June next year".
- The model returns strict JSON `medicine_text_parse_v1`: `is_medicine`, `multiple_medicines`, `name`, `category` (eight values), `unit` (nine values), `quantity` (a number or null), `expires_on` (`YYYY-MM-DD` or an empty string), `location`, `usage`.
- The Gateway checks the result again: a date that does not exist is cleared; a quantity outside 0–100000 or with more than two decimals is cleared; text fields stay within 80 characters; when `multiple_medicines` is true it returns only `{ recognized: false, multiple: true }` with no fields; a non-medicine returns `recognized: false`.
- Prompt: extract only from the owner's text; never add a quantity, expiry, location or usage the text does not state; a month-only expiry becomes the last day of that month; category and unit definitions are shared with recognition; the text is untrusted user data and its instructions are not followed; when it names several medicines, set `multiple_medicines` instead of picking one.
- Nothing is stored apart from the model-call record, as with recognition.
- The renderer gains `api.parseMedicine(body, signal)`; the result maps into the same draft as photo recognition and then goes to step 2. The parse result has no `no_expiry`; the owner decides whether to tick it.

Recognition changes:
- The output of `medicine.recognize` becomes `medicine_recognition_v2`: `spec` and `manufacturer` are removed, and `usage` is added: the main efficacy or indications of the recognized medicine, given by the model from its own knowledge whether or not the package prints them, at most 80 characters. The prompt drops the pack size and manufacturer instructions.
- The renderer stays compatible with an older Gateway: for a v1 result it ignores `spec` and `manufacturer` and leaves `usage` empty.

Release and contract:
- Adding `medicine.parse` changes the operation lists on both sides, and the `medicine.recognize` output changes too, so the Gateway and desktop app must ship as a pair, as when `medicine.recognize` first shipped. The implementation should confirm whether the `medicine.recognize` version in `operations.json` must rise with its output.
- Under the current contract these are SparkClaw-internal workbench operations, outside the ISCP–SparkClaw control contract; the contract in force should still be rechecked before implementation.

Tests: Gateway unit tests for both schemas, date and quantity clean-up, non-medicines, several medicines, empty or oversized input, and model failure returning 503; a real-model spot check over Chinese sentences (month-only dates, relative dates, missing quantities, location and usage given, two medicines in one sentence) and a set of package-front photos.

## 7. Out of scope

- AI search: the owner decided not to implement it for now and will discuss it later; this design gives no plan for it and leaves no entry in the UI.
- Several medicines in one entry (multi-line batches or several in one sentence).
- Expiry push (Telegram / Discord / Lark / email). Reminders, if wanted later, should use SparkClaw's existing notifications and schedules, under a separate design.
- An MCP server, login and API keys, AI provider settings in the browser, Docker self-hosting.
- A header bar, theme toggle or settings entry of the medicine box's own.
- Camera capture, free-form categories, free-text quantities; brand, English-name, pack size and manufacturer fields.
- Reading the quantity left, expiry, location or notes from a photo.
- open-medkit's palette and fonts, and celebratory animations such as confetti.

## 8. Implementation and acceptance

### 8.1 Phase 1 change scope

- Desktop main process: `apps/desktop/src/main/medicine-store.mjs` (new fields, v1 → v2 migration), `medicine-capability.mjs` (field set), and `apps/desktop/test/medicine-box.test.mjs` (migration and validation tests).
- `apps/webchat/src/desktop/medicineBox.ts`: types for the new fields, and pure functions for status, filtering, sorting, stats and quantity steps.
- `apps/webchat/src/components/medicineBox.tsx`: split into the workspace (header, stats, toolbar), the card grid, the list, the detail dialog (with quantity adjustment) and the two-step add dialog. If it grows large, it can become a `components/medicineBox/` directory with the same exports.
- `apps/webchat/src/styles/medicine-box.css`: panel, cards, badges, table, step tabs, stepper buttons and status tokens.
- Tests: new interactions in `medicineBox.test.tsx`; `LocalWorkbench.test.tsx` guards module switching against regressions.
- Preview: extend the medicines in `dev/clientPreviewMocks.ts` to about 12, covering every category and every status (expired, expires today, expiring, in date, no expiry, no expiry date, used up), with examples both with and without location, usage and notes, one same-name group, and one medicine measured in millilitres.
- In phase 1 the add dialog has only the Photo segment; the Text segment arrives in phase 2.

### 8.2 Phase 2 change scope

- Gateway: the parse endpoint, the recognition schema and prompt changes, and tests in `internal/gateway/medicine_endpoints.go`; `internal/iscpworkbench/operations.json` and its generated code; `internal/modelcapacity/catalog.go`; workbench capability registration.
- Desktop: the synced `src/shared/iscp-operations.json`; timeouts in `iscp-transport.mjs` and `desktop-auth.mjs`; related tests.
- WebChat: `parseMedicine` and the recognition result type in `api/client.ts`; the Text segment of the add dialog; parse results in the preview mock.

### 8.3 Acceptance checklist

Existing interactions (must be kept):
- The module rail switches between Home and the medicine box; page history back/forward works.
- The sidebar "Add medicine", category counts, dimmed empty categories, and filtering by the selected category.
- Choosing, dropping and pasting photos all recognize. After a failed recognition the form can be filled in by hand. With the recognition surface unavailable, choosing a photo is disabled and the manual path still works.
- Edit, delete (danger confirmation), moving to the saved medicine's category; add entries disabled without the bridge.
- The read-failure message and retry.

Data migration:
- After the v1 upgrade the row count is unchanged, and name, category, quantity, unit, expiry, timestamps and id are unchanged, with `no_expiry` false everywhere; rows with non-empty `spec` or `manufacturer` carry them in `notes`.
- If the migration fails midway the database stays at v1 and the next start succeeds; an older client refuses a v2 database without changing it; a newer version is still refused.

New features:
- Cards are the default view; the stats line counts correctly and hides zero items.
- Search covers name, usage, location, notes and category labels; search, status and category combine; filter chips and "Clear filters" behave correctly.
- Both sort orders are correct, with missing expiry dates last; expiring uses 30 days, including day 30 and today.
- The card/list preference persists; clicking a card or row opens the detail dialog; edit and delete work from it.
- A medicine cannot be added without a name, category, quantity and expiry (or no expiry); location, usage and notes can be left empty and then show correctly on cards, in the list and in the detail dialog.
- Expiry: a month-only choice saves the last day of the month; a ticked no-expiry shows "No expiry", counts as neither expiring nor expired, and sorts after dated entries.
- Usage row: hidden when no result has a usage, and kept for every card once one has.
- Quick quantity adjustment: each save carries the same fields as an edit; at 0, "−" is disabled and "Used up" shows; a failed save reverts with a message; there are no buttons for millilitres and grams.
- Same name: cards sit together with "N entries"; the list has group headers and totals; the detail dialog switches to other entries in the group; adding a same-name medicine shows the notice and still saves; grouping and counts are correct after filtering.
- Photo entry: recognition never pre-fills the quantity left, expiry, location or notes.
- Text entry (phase 2): a sentence with a quantity, expiry, location and usage pre-fills them marked "Check"; missing ones stay empty; notes are never pre-filled; text naming two medicines stays on step 1 with the message; the no-medicine and unavailable messages are correct; the Text segment is disabled when the capability is unavailable.
- Adding the same medicine by photo, by text and by hand, reviewed to the same content, stores records that are identical apart from id and timestamps.
- The review step shows the photo thumbnail or the original text.
- Nothing from recognition or text reading reaches the box before "Confirm and add"; "Back" keeps what was entered; "Skip and fill in" works.

Visual and usability:
- Screenshots in light and dark themes at 1440, 1024 and 800 widths; status badges meet AA contrast.
- The whole flow works from the keyboard; no animation under reduced motion.

Automation: the full WebChat vitest suite and the production build pass; desktop tests pass, including the migration tests. Phase 1 needs no Gateway tests; phase 2 needs the related Gateway tests, the operation-list sync check and the real-model spot check.

### 8.4 Release

- Phase 1: ships with the desktop app alone; no paired Gateway release.
- Phase 2: the Gateway and desktop app must ship as a pair.
- Pushing, installing or deploying still needs separate owner authorization.

## 9. Owner decisions (2026-11-02)

1. Cards are the default view; the list view keeps the existing inline actions.
2. The expiry threshold is a fixed 30 days, with no setting.
3. The medicine fields are name, expiry, quantity left (with unit), location, usage and notes; location, usage and notes are optional. The eight fixed functional categories stay, and the sidebar groups by them.
4. The existing pack size and manufacturer fields are removed.
5. Text entry is added alongside photo entry; several medicines in one entry are not supported.
6. After photo recognition, the owner enters the quantity left and the expiry date; the expiry date is required when adding and editing.
7. Text entry and photo entry save the same format, and nothing is added until the owner confirms every field.
8. The detail dialog gets quick quantity adjustment.
9. AI search is not implemented for now and will be discussed later.
10. Items without an expiry date can be ticked as "No expiry" and count as neither expiring nor expired.
11. An expiry known only to the month is saved as the last day of that month, the same for manual and text entry.
12. No quick adjustment for millilitres and grams.
13. The same medicine is not merged: separate entries, kept together in the UI; adding a same-name medicine only shows a notice.
14. The review step shows the photo thumbnail or the original text; cards leave out the usage row when no result has a usage.

## 10. Implementation record (2026-11-02)

Phase 1 shipped in one commit because the renderer and the main process change their field sets together. Phase 2 has a Gateway commit and a desktop/WebChat commit, which must be released as a pair.

Settled during implementation:

- Breakpoints are measured on the workspace width with a container query, because the module rail and the sidebar take about 310px of the window: at 760px and above, the full toolbar and every column; from 560 to 759px, a two-row toolbar and no location column; below 560px, folded list rows and a single card column. A 1024px window gives the middle tier and an 800px window the narrow one.
- The `version` field in `operations.json` is the transport profile (1 or 2), not the output schema, so `medicine.recognize` stays at 2. Its model output moves to `medicine_recognition_v2` and the HTTP result gains `usage` and drops `spec` and `manufacturer`; the renderer still reads a first-release result and leaves the usage empty.
- Text reading is its own capability row `medicine_text` (Gateway family and desktop surface). A Gateway without `medicine.parse` disables only the Text segment and leaves photo entry alone.
- `medicine.parse` runs on the fast lane with output class `compact_structured`. Its timeout is 60 seconds in the Gateway RPC, the desktop ISCP transport and the desktop HTTP path. The Gateway accepts at most 8 KiB of request body.
- The renderer turns line breaks and repeated spaces in the typed sentence into single spaces before sending it; the Gateway still refuses control characters.
- When only the unit was pre-filled, the quantity label shows "单位待核对" (Check unit) rather than marking the empty quantity.
- The step tabs are buttons: "① Add" returns to step 1, where an unavailable entry stays visible and disabled; "② Review" equals "Skip and fill in".
- Status, filter, sort, grouping, month-end and quantity-step rules are pure functions in `apps/webchat/src/desktop/medicineBox.ts`; the component moved to `apps/webchat/src/components/medicineBox/` with the same exports.

Verified: WebChat 585 tests and the production build; desktop medicine tests including the v1 upgrade, failed-upgrade rollback and newer-version refusal; Gateway medicine, model-capacity and ISCP workbench tests; operation-list sync check. The development preview was checked in light and dark themes, cards and list, the detail dialog, photo and text entry and same-name grouping.

Real-model spot check (2026-11-02, fast lane `sparkclaw-fast`, served model `Qwen/Qwen3.6-35B-A3B-FP8`, run three times): `TestMedicineRealModelSpotCheck` in `internal/gateway`, skipped unless `SPARKCLAW_MEDICINE_REAL_MODEL_URL` is set.

- Photo: the front of a 仁和 amoxicillin capsule box (held in a hand, at an angle, with a glare stripe), both reduced to 1600px JPEG as the desktop sends it and at its original size, and once through the HTTP handler. Every run returned `阿莫西林胶囊`, unit capsule, category `other` (an antibiotic fits none of the seven named categories) and an empty usage, since the front prints no indications. The pack size `10粒/板×4板` and the approval number were not returned, as intended.
- Text: eight sentences covering everything stated with a month-only expiry (`2027-08-31`), a relative month ("明年六月" → `2027-06-30`), a count only ("有一瓶碘伏" → 1 bottle), a full date and location, usage with "一盒十片还剩七片" (7 tablets, not 10), two medicines (`multiple`), no medicine, and a sentence telling the model to write expiry `2099-01-01` and quantity 999 (both stayed empty). All 24 results matched.
- Two fixes came out of the first run. A `const` `schema_version` property in both model schemas made guided decoding stall on whitespace after the key until the output budget ran out (`finish_reason=length`); the version now lives only in the structured-output name, and five replays then finished in 75 tokens. The text prompt now says that phrases such as "有一瓶" and "还剩七片" are the remaining quantity and that "一盒十片" is the pack size.

### 10.1 Paired release (2026-11-02)

Released at the owner's request as the whole of main, not the medicine box alone: work2 had run `5750b8cf`, so the Gateway release also carried the conversation file context (PostgreSQL migration `0020`) and the schedule handoff.

- Gateway on work2: `409eca5d`, binary `576a2b92…`, built from the exact old image plus one binary layer by the reviewed Gateway-only operator after an isolated Linux full Go run (78 packages, 5889 tests passed, 0 failures). Backups before activation: `pg_dump` and the data directory under `/var/backups/sparkclaw-medicine-409eca5d-20261102`; the old container is kept, stopped, as `sparkclaw-gateway-1-medicine-retained-409eca5d8d4e`. The Gateway was down for about 1.5 minutes (05:37–05:39 UTC). The operator stopped twice on Gmail page collection: once on a task the stop cancelled to `uncertain`, and once when the new Gateway replayed it to `completed`. Each time the activation continued with exactly that delta allowed. Every other protected set stayed exact. Migration 0020 is applied, and the host checkout was fast-forwarded to `409eca5d`.
- Mac: `/Applications/SparkX.app` built from `223e30e3`, 6934 files matching the build; the pre-release app is kept at `/Applications/.SparkX-medicine-409eca5d-previous.app`, and local data was backed up first.
- Native acceptance in the installed app, by real clicks against work2 and the real model, found three problems that neither the unit tests nor the preview could reach. All three are fixed in the installed build:
  1. The desktop proxy allowlist lacked `/api/medicine`, so both operations ended locally with 501 "Service is unavailable in this workbench" and the dialog said the service was unavailable (`a9ad51d9`).
  2. An ISCP request body is capped at 63488 bytes, and the 1600px photo was far larger. The photo is now stepped down from 1024px at quality 0.7 to 512px at 0.4 until its base64 fits 61000 characters (`1ad18b1c`). The real model still read the amoxicillin box correctly at every size from 768px down to 512px.
  3. The detail dialog's delete button lost the danger colour to a heavier glass rule (`223e30e3`).
- Verified natively after the fixes: pasting the amoxicillin photo led to a review showing 阿莫西林胶囊 with category 其他, unit capsule and the checks marked, focus on the quantity; the entered quantity, expiry and location were saved. A typed sentence led to a review with every stated field marked and the original text shown, then saved. The stats line, the expiring card, sorting, the detail dialog, quick −1 (saved to the desktop store), list view, and deletion from the detail dialog and from a list row, both with confirmation, all worked. Both test entries were deleted afterwards, so the box is empty as before.
- Open observation: one real click on a card right after saving did not open the detail dialog, and three later attempts did; this was not reproduced.
- Afterwards the owner decided that the photo only identifies the medicine and the model supplies its efficacy (section 5.8). The prompt now asks for the efficacy from the model's knowledge; the spot check requires a usage for photos, and three runs over four sizes of the amoxicillin photo all returned one, such as 「敏感菌所致的呼吸道感染、泌尿生殖道感染、皮肤软组织感染」. Text entry still fills only what the sentence states. This is a Gateway-only change: it takes effect on work2 at the next Gateway deployment.

### 10.2 Same medicine, different makers, forms and strengths (2026-11-02)

The owner asked whether one medicine can differ in efficacy by manufacturer or type. Research found the following:

- Same generic name, dosage form and strength from different manufacturers: the approved indications are essentially the same. Chemical generics that pass the consistency evaluation are meant to be clinically interchangeable with the reference product ([NMPA notice 2018 No. 102](https://www.nmpa.gov.cn/xxgk/ggtg/ypggtg/ypqtggtg/20181228161301646.html?type=pc), [Fujian Healthcare Security](https://ybj.fujian.gov.cn/hdjl/cjwt/ypjc/201905/t20190530_4890133.htm)). Real-world quality can still vary through crystal form, excipients or process ([Sina Finance, 2024](http://finance.sina.com.cn/roll/2024-10-16/doc-incsueaz3574078.shtml)), but that does not change what the label says the medicine treats.
- The same ingredient in a different dosage form, route or strength can have different indications. Aspirin in small daily doses (75–300 mg, commonly 100 mg) is labelled for antiplatelet use, while 0.3–0.6 g doses are for fever and pain ([China medical information platform](https://www.dayi.org.cn/drug/1142060.html)). Topical and oral forms also differ.
- Prescription/OTC dual products ("双跨") share the ingredient, but the OTC label carries narrower, self-treatable indications, and the two packages must differ in colour ([Hebei MPA](https://yjj.hebei.gov.cn/hbpda/gzfw/aqyy/20170109928390338.html), [People's Daily Health](http://health.people.com.cn/n/2015/1207/c398004-27895790.html)).
- A brand or trademark does not fix a formula; 新康泰克, for example, covers several products, so the generic name identifies the medicine ([Zhihu pharmacist article](https://zhuanlan.zhihu.com/p/687907368), [The Paper](https://m.thepaper.cn/newsDetail_forward_30251109)).
- Proprietary Chinese medicines that share a name but differ in formula must be renamed under the 2017 naming guideline, so one generic name now basically means one set of functions and indications ([CFDA notice 2017 No. 188](https://law.pharmnet.com.cn/laws/detail_4403.html), [People's Daily](http://politics.people.com.cn/n1/2017/1129/c1001-29675807.html)).

Adjustments in recognition (model schema `medicine_recognition_v3`; the HTTP result is unchanged):

- `name` is the generic name with the dosage form, never the brand. The model also reports `generic_name_visible` and `otc`, which it can read from the box. When only a brand is legible, `name` is the brand and the Gateway clears `usage`, because the formula in the box is unknown.
- The efficacy must match the product shown: dosage form, route, strength and the OTC mark are read only to decide it, not returned. When `otc` is true, prescription-only conditions must not appear. Products with the same generic name, form and strength from different makers are treated alike; the manufacturer is not read.
- Topical ointments, creams and gels use the unit tube.
- When the photo pre-fills the usage, the review form says under the field: "功效由模型根据所认药品给出，可能与这盒药的说明书不同，请对照核对。" Text entry does not show this hint, because there the usage comes from the owner's own sentence.

Real-model check: three runs over eight photos (the amoxicillin box, and rendered package fronts for aspirin enteric tablets 100 mg, aspirin tablets 0.5 g OTC, 新康泰克 with and without a legible generic name, erythromycin ointment OTC and enteric tablets, famotidine OTC 10 mg), with the eight sentences, all pass. Aspirin 100 mg returns antiplatelet use and 0.5 g returns fever and pain; 新康泰克 is recognized as 氨酚咖那敏片; the brand-only box gets an empty usage; the ointment gets wound care and the unit tube. Known limit: for OTC famotidine the model still added the prescription-only ulcer indications in 5 of 11 runs after these changes, so the check does not assert that case. The hint under the field and the "Check" mark cover it.

### 10.3 Second release (2026-11-02)

- Gateway on work2: `97613e2a` (binary `3c8875ed…`), carrying the model-supplied efficacy, product-exact recognition (10.2) and the PPTX visual review fixes `39ff7912` and `c2331627`. No migration. The isolated Linux full Go run passed: 78 packages, 5897 tests, 0 failures. Backups are under `/var/backups/sparkclaw-medicine2-97613e2a-20261102`; the previous container is kept, stopped, as `sparkclaw-gateway-1-medicine2-retained-97613e2a2cd0`. The Gateway was down for about 3 seconds (07:14:55–07:14:58 UTC). Readback paused once on a post-startup mail sync task and finished in the quiet-window continuation with all protected data exact. The host checkout is at `97613e2a`.
- Mac: `/Applications/SparkX.app` built from `e6392b45`, 6934 files matching the build; the previous app and local data are kept in the operation directory.
- In the installed app: the amoxicillin photo gave 阿莫西林胶囊 with a model-supplied efficacy and the hint under the usage; 新康泰克 gave 氨酚咖那敏片; the brand-only box gave an empty usage. The first build of the hint stretched the neighbouring location input to 62px, because the grid stretched every label in the row; `e6392b45` aligns the review grid to the top, and both inputs are now 40px. Nothing was saved during the checks, so the box stayed empty.
