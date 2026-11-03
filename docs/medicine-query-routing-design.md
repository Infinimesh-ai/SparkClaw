# Medicine Box Query And Edit Routing Design

> Language: English | [简体中文](../zh-cn/docs/medicine-query-routing-design.md)
>
> Date: 2026-11-02. Status: fourth design draft implemented in local source; multi-medicine batches and grouped field-completion approvals are connected end to end. Qualification and release boundaries are recorded in section 13; no deployment or desktop installation has been performed. Scope: move medicine box data to Gateway storage with client synchronization; add a top-level route `medicine` to `capability.Catalog` with three leaves: medicine box query, medicine box edit, and medicine information. The medicine box module's pages, menus, layout, and interactions are unchanged.

## 1. Background And Goals

The medicine box module is live (Gateway `97613e2a`, Mac `e6392b45`). Records currently exist only in the desktop's local SQLite (`userData/workbench/medicine/medicine-box.sqlite`), and medicine questions in conversation fall to `conversation.answer` or `browser.internet_search`: the model cannot see the box and has no medicine-specific answer rules.

Owner requirements (2026-11-02):

1. Add medicine box query as a top-level route.
2. Medicine box query: questions about the box are answered by the model from the box records.
3. Medicine information: plain medicine questions decide between Info search and a direct model answer, then return the answer.
4. Like mail, box data is stored in the backend and synchronized to clients; third-party channels and timers are supported.
5. Editing the box from conversation is supported through a new leaf, "medicine box edit".
6. Symptom questions that do not mention the box, such as "我发烧了吃什么药", also give general advice based on the box: whether a stocked medicine can be used or a new one should be bought.
7. When the Info-or-model decision fails, default to Info.
8. Keep the fixed closing sentence in answers.
9. Box edits with clear fields and successful validation need no approval.
10. Support batch writes for multiple medicines; missing or ambiguous fields open an approval window for the owner to complete or confirm before proceeding.

Design principles:

- Routing adds only Catalog nodes and Workflow Profiles and keeps the existing split, fused scoring, Top-2, clarification, and single-leaf dispatch, with no keyword routing.
- The authoritative box data lives in the Gateway; the desktop keeps a synchronized cache, the UI reads the cache, and changes are submitted through the Gateway.
- Single and batch conversation edits write directly when fields are clear and validation passes. Missing or ambiguous fields pause the whole batch for completion in the existing approval window, followed by revalidation. Deterministic checks, version checks, and per-entry before/after replies guard against mistaken edits.
- "Info or model" is Workflow execution policy and does not affect route scores.

## 2. Current State

| Item | Current state | Use in this design |
|---|---|---|
| Top-level routes | Branches under root in `internal/capability/catalog.go`: `conversation`, `browser`, `ai_chat`, `document`, `localmind`, `schedule`, `coding`; Catalog revision `2026-10-30.v28` | Add branch `medicine` with three leaves; bump revision to v29 |
| Mail storage and sync | The Gateway keeps authoritative mail (PostgreSQL typed repository); `internal/mailsync` keeps a bounded event journal with epoch/sequence, and `mail.sync` returns a snapshot or delta by cursor; desktop `MailSyncStore` caches it in tables `boxes`, `mail`, `snapshots`, `staged`; ISCP event category `email` prompts the client to sync | The medicine box follows the same pattern |
| Desktop medicine store | `MedicineStore`: partitioned by deployment+owner, at most 2000 rows; fields name (≤80), category (8), quantity (0–100000, two decimals) + unit (9), expiry/no expiry, location (≤80), usage (≤80), notes (≤500, multiline); the renderer calls `list / save / remove` over IPC `sparkclaw-medicine-box:invoke` | Becomes a synchronized cache with an unchanged IPC interface |
| Client mutations | Forward operations with receipts (`recovery: operation_receipt`) plus desktop `ISCPMutationJournal`, for example `mail.drafts.save` | Medicine create, update, and delete use the same approach |
| Approval-free write tools | The schedule tools `reminders.update` / `reminders.cancel`: `RiskReversible`, `RequiresApproval: false`, `Sandbox: forbidden` | Medicine write tools register the same way |
| Info search | `browser.internet_search` uses `web.discovery` (provider `info`), executed by ToolHub `web.search`, grounded finalization | Reused by the Info path of medicine information |
| Time zone | Message envelopes carry `ClientTimezone`; schedules carry their own time zone; `currentSearchDateForTimezone` computes today's date | Used for expiry status |

## 3. Overall Design

```text
Data
  Gateway medicine repository (PostgreSQL, per owner) ── event journal ── medicine.sync ──> desktop cache ──> box UI
       ^                                                                                       |
       └──────────── medicine.items.create / update / delete (with receipts) <────────────────┘

Routing
  owner text (desktop, WebChat, third parties such as WeChat, timers)
    -> existing split / embedding + Tree scoring / Top-2 / clarification
        -> medicine.box_query   #read   what is stocked, how much, where, expired or not
                                #advise symptom-based use: stocked medicine or buy a new one
            -> read_box (Gateway repository) -> answer from the snapshot
        -> medicine.box_edit    #create / #edit / #delete
            -> read_box -> plan_change (single / batch) -> resolve targets and validate
                -> clear fields: commit batch -> per-entry reply
                -> missing / unclear: approval form -> revalidate -> commit batch -> per-entry reply
        -> medicine.drug_info   #answer medicine knowledge, box not involved
            -> choose_source -> Info search | direct model -> answer
```

## 4. Medicine Box Data: Backend Storage And Client Sync

### 4.1 Ownership

- Authoritative data lives in the Gateway, partitioned by `owner_id`. All desktop installations of the same owner see the same box.
- The desktop keeps only a synchronized cache for display and offline viewing.
- Photo recognition (`medicine.recognize`) and text parsing (`medicine.parse`) are unchanged; their results are still saved only after user confirmation, but the save target becomes the Gateway.

### 4.2 Gateway Storage

Add package `internal/medicine` (domain types, validation, service). Storage follows the existing `*_contract.go` pattern with memory, file, and postgres implementations, plus migration `0021_medicine_box.sql`:

```sql
CREATE TABLE IF NOT EXISTS medicine_items (
    owner_id    text NOT NULL,
    id          text NOT NULL,              -- med_<32 hex>, the desktop format
    name        text NOT NULL,
    category    text NOT NULL,
    quantity    numeric(8,2) NOT NULL,
    unit        text NOT NULL,
    expires_on  date,                       -- empty for no expiry or old rows
    no_expiry   boolean NOT NULL,
    location    text NOT NULL DEFAULT '',
    usage       text NOT NULL DEFAULT '',
    notes       text NOT NULL DEFAULT '',
    version     bigint NOT NULL CHECK (version > 0),
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (owner_id, id)
);
CREATE TABLE IF NOT EXISTS medicine_events (
    owner_id  text NOT NULL,
    sequence  bigint NOT NULL,
    item_id   text NOT NULL,
    deleted   boolean NOT NULL,
    item      jsonb,                         -- delete events keep the whole deleted row
    PRIMARY KEY (owner_id, sequence)
);
CREATE TABLE IF NOT EXISTS medicine_box_state (
    owner_id  text PRIMARY KEY,
    epoch     text NOT NULL,
    sequence  bigint NOT NULL,
    floor     bigint NOT NULL                -- events before it were pruned
);
```

- Each create, update, or delete updates `medicine_items`, appends to `medicine_events`, and advances `medicine_box_state.sequence` in one transaction; `version` increments by 1.
- Batch creates or updates reuse the same domain service with the whole batch as the transaction boundary. All rows, per-entry events, sequence advancement, and durable batch receipt commit together or remain unchanged. Memory, file, and PostgreSQL must all guarantee this; looping over single-row write APIs would permit partial success and is not allowed.
- The most recent 4096 events are kept (same as `mailsync.MaxEvents`); older ones advance `floor`. A cursor before `floor` returns a reset, and the client fetches a new snapshot.
- At most 2000 rows per owner (same as the desktop limit).

### 4.3 Fields And Validation

The Go implementation enforces the same rules as `medicine-store.mjs` and shares one set of test cases so both sides agree: name required ≤80; category from 8 fixed values; unit from 9 fixed values; quantity 0–100000 with at most two decimals; expiry a valid `YYYY-MM-DD`, empty when `no_expiry` is true; location and usage ≤80; notes ≤500, multiline allowed; no control characters.

### 4.4 Interfaces

New ISCP forward operations (added to `operations.json`, then run `node scripts/sync-iscp-operations.mjs`). The HTTP paths are also added to the desktop `workbench-proxy-policy` allowlist (the previous release missed `/api/medicine` and returned 501):

| Operation | HTTP | scope | Mutation | Recovery | Purpose |
|---|---|---|---|---|---|
| `medicine.sync` | `POST /api/medicine/sync` | `medicine.read` | No | `read` | Cursor-based incremental sync |
| `medicine.items.create` | `POST /api/medicine/items` | `medicine.write` | Yes | `operation_receipt` | Create one row |
| `medicine.items.update` | `PUT /api/medicine/items/{item}` | `medicine.write` | Yes | `operation_receipt` | Update one row with `base_version` |
| `medicine.items.delete` | `DELETE /api/medicine/items/{item}` | `medicine.write` | Yes | `operation_receipt` | Delete one row with `base_version` |
| `medicine.items.batch` | `POST /api/medicine/items/batch` | `medicine.write` | Yes | `operation_receipt` | Atomic batch create or update using one operation (section 7.6) |
| `medicine.import` | `POST /api/medicine/import` | `medicine.write` | Yes | `operation_receipt` | One-time upload of old local data (section 4.7) |

A new ISCP event category `medicine` is pushed when the box changes; the desktop runs one `medicine.sync` on receipt.

`medicine.items.batch` accepts only complete, validated final changes with one batch operation ID and payload digest; each update includes `base_version`. The conversation tool and this interface share the domain service. Field completion uses a versioned approval input contract; incomplete drafts cannot enter the batch write API, and `medicine.import` is not a shortcut for conversation batch writes.

### 4.5 Incremental Sync

Request and response follow the shape of `mail.sync`:

```json
{"cursor": "<cursor from the previous response, empty the first time>", "limit": 100}
```

```json
{
  "schema_version": 1, "epoch": "…", "mode": "snapshot | delta",
  "base_sequence": 120, "sequence": 126,
  "events": [{"sequence": 121, "id": "med_…", "deleted": false, "item": {"id": "med_…", "version": 3, "name": "…"}}],
  "cursor": "…", "more": false
}
```

- The first sync or a reset uses `snapshot`, paging through all rows; the client writes them to `staged` and swaps them in after the last page, so a half box is never shown.
- Later syncs use `delta` and return only events after the cursor.
- At most 100 rows per page (same as `MAIL_PAGE_LIMIT`), and never above the ISCP response size limit.

### 4.6 Desktop Cache And UI Integration

- `medicine-box.sqlite` moves to schema v3: `items` gains `version`, plus new `sync_state` (scope, epoch, sequence, cursor, synced_at) and `staged`, as in `MailSyncStore`.
- The renderer's `window.sparkclawMedicineBox.{list, save, remove}` and their response formats are unchanged, so box UI code does not change:
  - `list`: reads the cache and triggers one background sync.
  - `save` / `remove`: the main process turns them into `medicine.items.*` forward operations, records them in `ISCPMutationJournal` first, and updates the cache from the returned row on success.
- Offline or disconnected from the Gateway, the box still shows the cache; add, edit, delete, and quantity buttons stay but are disabled with "连接后可修改". An error is shown only when a change the user started cannot complete.
- An unknown outcome (request sent, no receipt) is reconciled through the existing receipt mechanism without resubmitting.

### 4.7 Local Data Migration

- On the first Gateway connection after upgrade, the desktop uploads existing local rows in batches through `medicine.import` (each batch within the ISCP request limit), keeping the original `med_…` IDs and creation times.
- The Gateway is idempotent by ID: an existing ID is skipped, never overwriting a newer Gateway version. Multiple computers of the same owner each upload; their IDs differ, so rows merge directly. Rows with identical content but different IDs are not deduplicated automatically.
- After all batches succeed, the desktop records local migration as complete, fetches a snapshot, and uses only the cache afterwards. The old table is renamed `items_v2_backup` and kept for one release cycle, then removed in the next release.
- Migration failure does not affect viewing; remaining batches continue on the next connection.

### 4.8 Concurrency And Conflicts

- Updates and deletes must carry `base_version`; a mismatch returns 409.
- On 409 the UI refreshes the row and shows "这条药品已在别处修改，已更新为最新内容", asking the user to retry.
- Conversation edits record the version when reading the box and check it at write time (section 7.6).

## 5. Top-Level Route And Semantic Registration

### 5.1 Catalog Nodes

```go
branch("medicine", string(RootID), "Read, advise from, or edit the owner's medicine box, or answer general medicine questions."),
leaf("medicine.box_query", "medicine", "Answer from the owner's medicine box records: whether a medicine is stocked, remaining quantity, location, expiry status, and symptom-based advice on whether a stocked medicine fits or a new one is needed.", RouteContract{
    Operations: []app.RouteOperation{app.RouteOperationRead, app.RouteOperationAnswer}, FactScopes: []app.RouteFactScope{app.RouteFactScopeMedicineBox}, RequireQuery: true,
}),
leaf("medicine.box_edit", "medicine", "Add or change one or several medicines in the owner's medicine box, or remove one medicine; collect missing or ambiguous fields before writing.", RouteContract{
    Operations: []app.RouteOperation{app.RouteOperationCreate, app.RouteOperationEdit, app.RouteOperationDelete}, RequireQuery: true,
}),
leaf("medicine.drug_info", "medicine", "Answer general questions about a named medicine: ingredients, indications, usage, side effects, interactions, precautions, price, availability, or recalls. Does not read the owner's medicine box.", RouteContract{
    Operations: []app.RouteOperation{app.RouteOperationAnswer}, RequireQuery: true,
}),
```

- Add `app.CapabilityMedicineBoxQuery`, `CapabilityMedicineBoxEdit`, `CapabilityMedicineDrugInfo`, the matching `WorkflowID`s, and fact scope `medicine_box`.
- Bump `DefaultCatalogRevision` to `2026-11-xx.v29`; the graph revision changes with it, and signed clarification choices from the old revision are rejected under existing rules.
- All three leaves accept every source (desktop, WebChat, third parties, timers). Timers cannot interact, so an edit that needs clarification or lacks fields ends immediately and sends the reason to the timer's delivery target (sections 7.4 and 7.5).

### 5.2 Semantic Variants

| Candidate | Clarify label | EmbedTexts (independent utterances) | HardNegatives |
|---|---|---|---|
| `medicine.box_query#read` | 查看我的药箱记录 / Check my medicine box | 药箱里还有退烧药吗; 我的布洛芬还剩多少; 有哪些药快过期了; 感冒药放在哪里; 家里还有没有创可贴; 药箱里过期的药有哪些; What do I have for allergies in my medicine box? | 布洛芬有什么副作用; 把布洛芬减一片; 帮我添加一盒感冒灵; 打开药箱; 一小时后提醒我吃药 |
| `medicine.box_query#advise` | 根据药箱给用药建议 / Advise from my medicine box | 我发烧了吃什么药; 头疼得厉害有什么药能吃; 孩子咳嗽家里的药能用吗; 拉肚子了要不要去买药; 被划了个口子怎么处理; I have a sore throat, what should I take? | 布洛芬和对乙酰氨基酚有什么区别; 发烧是怎么引起的; 明天早上提醒我吃退烧药; 添加一盒退烧药 |
| `medicine.box_edit#create` | 往药箱添加药品 / Add medicines to my box | 药箱里加一盒布洛芬，24 粒，2027 年 5 月过期; 新买了一瓶碘伏放在卫生间; 把这盒感冒灵记到药箱里; 把布洛芬、感冒灵和碘伏一起记到药箱; Add loratadine and bandages to my medicine box | 药箱里有布洛芬吗; 识别这张药盒照片; 布洛芬多少钱 |
| `medicine.box_edit#edit` | 修改药箱里的药品 / Change medicines in my box | 布洛芬吃了两粒; 把感冒灵的数量改成 5 袋; 创可贴挪到客厅抽屉了; 阿莫西林的有效期改成 2026 年 12 月; 给碘伏加个备注开封于 11 月; 布洛芬减两粒，感冒灵减一袋，帮我一起更新 | 布洛芬还剩多少; 布洛芬一次吃几粒; 提醒我每天吃两粒布洛芬 |
| `medicine.box_edit#delete` | 从药箱删除药品 / Remove a medicine from my box | 把过期的阿莫西林删掉; 药箱里的感冒灵扔了，删除记录; Remove the expired ibuprofen from my box | 有哪些药过期了; 删除这个定时任务; 删除这个文件 |
| `medicine.drug_info#answer` | 查询药品信息 / Look up medicine information | 布洛芬和对乙酰氨基酚有什么区别; 阿莫西林饭前还是饭后吃; 连花清瘟的成分是什么; 孕妇能吃氯雷他定吗; 头孢和酒精一起会怎样; 感康现在多少钱; 这个药最近有没有召回; What are the side effects of ibuprofen? | 我药箱里有布洛芬吗; 我发烧了吃什么药; 解释一下免疫系统; 今天有什么医疗新闻; 提醒我八点吃药 |

### 5.3 Boundary Rules

TreeDescriptions state the distinctions as follows:

- Questions about the owner's own box (stocked or not, how much is left, where, expired or not) → `box_query#read`.
- Describing a symptom or situation and asking what to take or how to handle it, without naming a specific medicine → `box_query#advise`, whether or not the box is mentioned.
- General information or comparison about a named medicine, or its price, availability, or recalls → `drug_info`. A named medicine asked as "can the one in my box be taken" (for example "我药箱里的布洛芬能和感冒灵一起吃吗") → `box_query#advise`.
- Requests to add, record usage, change fields, or delete → the matching `box_edit` variant. A completed-action statement ("我刚吃了布洛芬") is not itself an edit request; an explicit request to update the quantity ("布洛芬吃了两粒，帮我减掉") is. These boundaries are covered by hard negatives and test cases.
- Medication reminders belong to `schedule.manage`; a timer-triggered "检查药箱有没有快过期的药" enters `box_query#read` when due.

Several medicines listed under one create or update intent remain one business span, one `box_edit` Workflow, and one batch; the splitter must not dispatch per medicine name. Mixed create/update/delete requests retain existing compound-request splitting, with independently executed spans and no cross-span atomicity.

### 5.4 Sibling Leaves And Calibration

- Add "布洛芬有什么副作用" and "我发烧了吃什么药" to the `conversation.answer#answer` hard negatives.
- Add "感康现在多少钱" and "这个药最近有没有召回" to the `browser.internet_search#search` hard negatives; its TreeDescription notes that price, availability, and recalls of a specific medicine belong to medicine information.
- When changing RoutingSemantics of a released Profile, follow existing rules on whether to bump the Profile revision and keep a legacy registration.
- `box_edit` is a mutation route and uses the stricter score and margin thresholds per [Intent Routing](intent-routing.md#decision-states); with only one semantic channel available, edits are `blocked` while queries may still run under degraded thresholds.
- Add cases and rebuild calibration evidence per steps 5 and 6 of [Intent Routing](intent-routing.md#extending-routing); keep fusion weight `alpha = 0.50` unless calibration evidence requires a change.

## 6. Leaf 1: Medicine Box Query `medicine.box_query`

### 6.1 Workflow Plan

```text
read_box  (deterministic, Runtime calls medicine.box.read, MaxAttempts 1)
  -> answer (grounded finalization; evidence is the box snapshot; #advise adds the advice rules)
```

- ToolHub adds read-only tool `medicine.box.read` (capability `medicine.box.read`, effect `local.read`), which reads the Gateway repository directly without the desktop.
- Risk `RiskRead`; no write tool is exposed.

### 6.2 Snapshot Projection

- Today's date: message sources use `ClientTimezone`, timers use the schedule's own time zone, and otherwise the Gateway default time zone; the near-expiry threshold is 30 days, as in the box UI.
- Fields per row for the model: name, Chinese category name, quantity+unit, expiry, status (expired / expires within 30 days / not near expiry / no expiry) and remaining days, location, usage, notes (cut to 120 characters). Runtime computes dates; the model does no date arithmetic.
- Rows are sorted by expiry ascending with same-name medicines adjacent. Above 48 KiB of evidence, drop notes, then usage, then truncate rows and state the total.

### 6.3 `#read` Answer Rules

- Answer only from the snapshot. When nothing matches, say "药箱里没有 X 的记录"; never guess.
- Quote quantity, unit, and location as recorded, without conversion or completion.
- Mark expired items as expired; give the expiry date of items expiring within 30 days.
- Empty box: "药箱里还没有药品记录，可以在药箱中添加，也可以直接告诉我。"
- On truncation, say only part of the records was read.

### 6.4 `#advise` Answer Rules

The answer always has three parts:

1. **General advice**: the usual over-the-counter direction and care points for the stated symptom, without diagnosis.
2. **Usable from the box**: list only stocked medicines whose use matches, are not expired, and have quantity above 0, with name, remaining quantity, and location, and a reminder to follow the label. Matching medicines that are expired or used up are listed separately as unusable.
3. **Whether to buy**: when the box has nothing suitable, name the class of over-the-counter medicine to buy (generic name or category, no brands, no purchase links); when something suitable is stocked, do not suggest buying.

In addition:

- With emergency signs (breathing difficulty, chest pain, severe allergy, altered consciousness, large accidental intake, persistent high fever, and similar), the first sentence advises immediate medical care before anything else.
- For children, pregnancy and lactation, the elderly, chronic illness, and hepatic or renal impairment, advise asking a doctor or pharmacist first; adult medicines in the box are not directly recommended for children.
- Prescription medicines (such as antibiotics) are not recommended for self-use even when stocked.
- Keep the fixed closing sentence (section 8.4).

### 6.5 Sources

Desktop, WebChat, third-party channels such as WeChat, and timers can all query; results return to the source or the timer's target under existing Delivery rules. For example, the timer "每天早上 9 点检查药箱有没有快过期的药" is created by `schedule.manage#create`, and its payload enters `box_query#read` when due.

## 7. Leaf 2: Medicine Box Edit `medicine.box_edit`

### 7.1 Scope

- One request can create or update one or several medicines; a batch uses one operation, and a single row is a batch of length 1. The initial limit is 20 entries. Above it, ask the owner to split the request; never silently truncate or automatically commit separate batches.
- Create: an independent record per entry, including same-name medicines with different expiry dates or locations.
- Update: quantity (set or delta), unit, expiry/no expiry, location, usage, notes (replace or append), category, and name per entry.
- Delete still handles one record; bulk deletion remains out of scope. The box page's existing photo and text entry interactions stay unchanged; batching is added to conversation routing.

### 7.2 Workflow Plan

```text
read_box      (deterministic, reads the Gateway repository)
  -> plan_change (Fast structured call producing medicine_edit_v2)
  -> resolve     (resolve each target, merge fields, collect missing/ambiguous inputs, validate the batch)
      -> all clear: apply
      -> input needed: collect_fields (approval window, waits for user input or selection)
          -> revalidate (deterministic: inputs, target versions, final batch values) -> apply
  -> apply       (medicine.box.write, one atomic batch commit and one batch receipt)
  -> answer      (deterministic per-entry template, no further model call)
```

Nothing in the batch is written while awaiting input, including already complete entries. Completion is structured input to the original batch, bound to its request and draft; it is not reparsed as an unrelated new conversation request.

### 7.3 Normalized Change Structure `medicine_edit_v2`

Add modelcapacity operation `medicine.edit_plan` (fast structured, thinking off, strict JSON Schema, at most one repair). Input is the verbatim business span, today's date, and a compact list of box rows (name, expiry, location, quantity+unit, no notes). The strict model wire format uses `medicine_edit_v4`: each entry carries its own `action` (`create`, `update`, or `delete`), `changes` is an array of `{field, value, evidence}` (values retain their string, number or boolean types), and optional target fields carry their own source evidence. Runtime derives the batch `operation` from the entries and rejects the whole batch when actions differ. The earlier `medicine_edit_v3` asked the model for one batch `operation` that included `mixed`; the real Fast lane labeled every plain update `mixed`, so chat updates were never written. Runtime immediately normalizes this into the map below and freezes the draft. This avoids an observed constrained-decoding failure where optional map keys such as expiry were skipped after later keys. It does not change batch, approval, or storage semantics. Example normalized draft:

```json
{
  "operation": "create",
  "items": [
    {
      "changes": {"name": "布洛芬", "category": "fever", "quantity": 24, "unit": "capsule", "expires_on": "2027-05-31", "no_expiry": false},
      "unresolved": []
    },
    {
      "changes": {"name": "感冒灵", "category": "cold", "quantity": 6, "unit": "sachet"},
      "unresolved": [{"field": "expiry", "reason": "missing"}]
    }
  ]
}
```

- `operation` is `create`, `update`, or `delete` and must match the route variant. `items` contains 1–20 entries; delete requires exactly one. Runtime counts the array instead of using a model-supplied `medicine_count`.
- Each update or delete entry has a `target` containing the owner-supplied name and optional expiry and location. Runtime resolves the record ID and `base_version`. The model neither selects record IDs nor decides to write.
- A create draft's `changes` may omit fields until completion makes it a valid record. An update includes only requested changes and preserves unmentioned fields. `quantity` / `quantity_delta` and `notes` / `notes_append` are mutually exclusive pairs.
- `unresolved` marks missing or ambiguous inputs (`missing` / `ambiguous`). `field` is restricted to declared fields and the compound inputs `target` and `expiry`. Runtime independently checks required fields, unique targets, and source evidence; an empty model-produced array does not establish completeness.
- Unclear values stay unset: never guess quantity, unit, date, or target, or substitute 0, an empty string, or "no expiry" for completion. Runtime assigns stable draft `entry_id` values to correlate window rows, inputs, and receipts; these are not medicine record IDs.

### 7.4 Target Resolution And Clarification

For each entry, Runtime matches the exact name, then the normalized name (whitespace removed, full-width and half-width unified), and only when both find nothing, names that contain the owner's words or are contained in them ("布洛芬" finds "布洛芬缓释胶囊"). It filters by expiry and by a location the owner explicitly supplied, matching the location by containment ("卧室" in "卧室柜子"). The model reports the owner's own words as the target and never picks a record. An unresolved name on an update or delete that does not rename the record means the target is unclear, so the window asks the owner to pick a record instead of typing a name:

1. One row: bind its record ID and the version read.
2. Several rows: list signed candidates under that medicine in the completion approval window, including name, expiry, location, and quantity. The owner must select one; never pick the first automatically.
3. None: write nothing in the batch and identify the entry with "药箱里没有 X 的记录"; never silently turn an update into a create.
4. Create needs no existing target; an existing same-name row does not prevent creation, and the result notes that the medicines are recorded separately.
5. Multiple updates resolving to the same record are rejected with a request to combine its changes; never apply repeated decrements or implicit overwrites in order.
6. Timers cannot interact. If any target or field needs input, end the whole batch and deliver the affected medicines and missing information to the schedule's target instead of waiting for approval.

### 7.5 Field Validation And Completion Approval Window

- Create requires an explicit name, quantity, unit, and either expiry or the owner's explicit "no expiry" choice. Month-only expiry uses the last day of that month under the existing rule, with the actual date shown in the window or write result.
- Category uses the existing 8 values and defaults to "其他" when unsure. Unmentioned location, usage, and notes may stay empty without a prompt. A field the owner requested but described ambiguously still needs input; an update does not require completing unrelated old fields.
- Missing required fields, ambiguous quantity/unit/date wording, or a non-unique target opens the existing approval window container with a typed field form. Group by medicine, show known values and the full batch summary, and request only missing or unresolved inputs: numeric quantity, fixed unit options, expiry date or "no expiry", and target candidates.
- Collect all affected medicines in one window. Show errors at the corresponding row and disable submission until valid. The submit action explicitly says "补全并写入 N 条": one submission provides the missing values and confirms the whole batch, with no second generic write approval.
- The backend revalidates every final row under section 4.3. Reject negative resulting quantity and show the current amount; allow zero and note it in the reply. If the batch would exceed 2000 rows, write none of it.
- Rejection, cancellation, approval expiry, or task termination writes nothing. Closing follows existing cancellation semantics and never implies consent. Gateway restart retains the existing termination rule for approval-waiting tasks; it never resumes an old draft to write.
- Desktop and WebChat use the same form contract. Sources unable to display it, such as WeChat, receive the exact missing inputs and a request to send a complete instruction; the current batch writes nothing. Arbitrary follow-up messages are not approvals, and the request is not automatically forwarded to another channel.

**The approval input contract is new phase-3 work.** Desktop execution approvals currently accept only `digest` and `decision`, and `ComposerApproval` only offers approve/reject; the existing interface cannot already submit fields. Extend the original approval component and adapters with the form, together with version-negotiated Gateway, LAN/ISCP, desktop journal, and WebChat input contracts:

- A pending completion binds deployment/owner/client, original request, draft revision and digest, each `entry_id`, candidate records and versions, allowed input schema, and deadline. Submitted inputs must match these bindings and contain only allowed fields.
- The server validates inputs, freezes the submitted values together with the complete final batch, and computes its final digest. Consent covers only that batch. An old draft decision cannot authorize different values, and target selection cannot silently refresh bound versions.
- Completion submission has a durable idempotency key. Identical resubmission returns the same result; changed content or a stale revision is rejected. Preserve the original request body and digest, linking supplementary input separately. An unknown submission reconciles the original approval/batch receipt without creating another batch.
- Old clients without the form contract retain the entry point but disable submission and prompt use of a supported client; never degrade to an approve-only button that writes missing values.

For example, "把布洛芬 24 粒、2027 年 5 月到期，感冒灵 6 袋一起记到药箱" shows two drafts and asks only for the second medicine's expiry or an explicit no-expiry choice. Submission and validation write both together; cancellation writes neither.

### 7.6 Direct Writes And Batch Commit

The owner's latest 2026-11-02 decision refines the earlier approval-free rule: clear fields, unique targets, and valid values write directly; missing or ambiguous information first requires the completion approval in section 7.5. Batching alone does not trigger approval.

- `medicine.box.write` stays `RiskReversible`, `RequiresApproval: false`, `Sandbox: forbidden`. A typed Workflow node owns the completion wait. The write node accepts only a fully validated batch and cannot bypass completion to write missing values.
- Routing retains stricter mutation score/margin thresholds and is `blocked` under a degraded semantic channel. Operation mismatch, unresolved ambiguity, missing required fields, or invalid values cannot enter the write node.
- Inside the write transaction, recheck every target's ownership, existence, and `base_version`, plus box capacity. Any conflict writes none of the batch, identifies the medicines, and asks for a new request; never replay old consent or quantity deltas against refreshed versions.
- Runtime assigns a batch operation ID bound to owner, original request, and final payload digest. Records, per-entry events, sequence advancement, and receipt commit together; each updated row advances its version. The file backend also needs one durable commit so crash recovery yields all or none.
- A retry with the same batch ID and digest returns the original result; the same ID with a different digest is rejected. Recover a committed batch with a lost response through its receipt, never by filling in individual writes or retrying under a new ID.

### 7.7 After Writing

- Emit a `medicine` event after the whole batch commits, prompting desktops to sync. Per-entry events may span pages, but completion can only be claimed after the full batch commits; acceptance checks every entry after sync finishes.
- Template replies give the count, then each created record or before/after update in input order, for example "已更新 2 条：布洛芬 6 粒 → 4 粒；感冒灵 5 袋 → 4 袋", with relevant expiry, location, and same-name hints. Delete still lists the entire removed row, and its event preserves the prior value.
- Validation failure or cancellation explicitly reports that the whole batch was not written. An unknown outcome reports reconciliation and looks up one batch receipt; it must not claim success or repeat the write.

## 8. Leaf 3: Medicine Information `medicine.drug_info`

### 8.1 Source Decision `choose_source`

The first node is deterministic: Runtime makes one Fast structured call (modelcapacity operation `medicine.info_source`, thinking off, strict JSON Schema, at most one repair):

```json
{"source": "info", "reason_code": "price_or_purchase"}
```

| source | reason_code | Meaning |
|---|---|---|
| `info` | `price_or_purchase` | Price, where to buy, insurance coverage |
| `info` | `availability_or_approval` | Marketed, on sale, approved, imported |
| `info` | `safety_notice` | Recall, discontinuation, warnings, label revisions |
| `info` | `recent_evidence` | Latest research or guidelines |
| `info` | `unfamiliar_product` | Unfamiliar name, brand, or combination product |
| `model` | `stable_knowledge` | Ingredients, mechanism, indications, common adverse effects, general usage, common interactions, storage |

- Input is only the verbatim business span and today's date, without candidates or route scores.
- The decision is written to the node's `OutcomeRefs` (kind `medicine_info_source`) and audit event `workflow.medicine_source_selected`, like `detect_response_media`.
- **When the decision fails (initial and repair both invalid), use Info** (owner decision, 2026-11-02). Use the model only when Info is not configured.

### 8.2 Info Path

- The node scope is `web.discovery` (provider `info`) bound to the route query; the query passes through `materializeRoutedQuery`, as for `browser.internet_search`.
- Grounded finalization: answer from the Info sources and list them.
- Info failure or no results: fall back to the model path, opening with one sentence: "未能联网核实，以下为一般知识".

### 8.3 Model Path

- Same as `conversation.answer`: no tools, `workflowFinalizationModel`, but with the rules in section 8.4.
- Never invent sources, prices, approval numbers, or dates; for time-varying facts, point to the label or current information.

### 8.4 Shared Medicine Answer Rules

Apply to both `drug_info` paths and to `box_query#advise`:

- Give only the label's standard adult dose and defer to the label; for children, pregnancy and lactation, the elderly, and hepatic or renal impairment, advise consulting a doctor or pharmacist.
- With emergency signs, advise immediate medical care first.
- No diagnosis and no self-treatment plans for prescription drugs.
- **Fixed closing sentence** (owner decided to keep it, 2026-11-02): "以上仅供参考，用药请以说明书或医生、药师意见为准。" `box_query#read` and `box_edit` report records only and omit it.
- Existing input guard and content governance are unchanged; overdose and self-harm questions are handled by the existing guard and included as acceptance cases.

### 8.5 Call Counts

| Leaf | Calls |
|---|---|
| `box_query` | 1 repository read + 1 answer |
| `box_edit` | 1 repository read + 1 Fast batch parse + 1 batch write; completion adds a user-input wait and deterministic revalidation, with no model call for the reply |
| `drug_info` model path | 1 Fast decision + 1 answer |
| `drug_info` Info path | 1 Fast decision + 1 Info query + 1 answer |

## 9. Owner Decisions (2026-11-02)

| # | Question | Decision |
|---|---|---|
| 1 | Where box data lives and which sources can use it | Like mail: backend storage with client sync; third-party channels and timers supported (sections 4 and 6.5) |
| 2 | Editing the box from conversation | Supported through new leaf `medicine.box_edit` (section 7) |
| 3 | Symptom questions that do not mention the box | General advice based on the box: use a stocked medicine or buy a new one (`box_query#advise`, section 6.4) |
| 4 | Default when the source decision fails | Info (section 8.1) |
| 5 | Fixed closing sentence | Keep (section 8.4) |
| 6 | Whether box edits need approval | Clear, validated fields write directly; missing or ambiguous fields require completion and batch confirmation in the approval window (sections 7.5–7.6, refining the earlier approval-free decision) |
| 7 | Writing several medicines at once | Support batch create and update with per-entry parsing and validation (sections 7.1–7.3) |

This design also makes the following choices, to be adjusted if the owner disagrees:

- Since edits with clear fields need no approval, timers can also edit the box, for example "每天晚上 9 点把维生素 C 减一片"; when clarification is needed or fields are missing, the run ends and states the reason (section 5.1).
- Initially at most 20 entries per batch, using only create or update, with an atomic batch commit; no bulk delete (sections 7.1 and 7.6).
- The old local table is kept for one release cycle as a backup (section 4.7).

## 10. UI Impact

- The medicine box module's pages, menus, layout, styles, and interactions are unchanged; the `window.sparkclawMedicineBox` interface is unchanged.
- Visible changes within the box page: offline, edit buttons are disabled with "连接后可修改"; on an edit conflict, a refreshed-row notice (sections 4.6 and 4.8). No persistent sync status is added.
- Conversation keeps existing answers, route clarification, and approval containers. Only when completion is needed, extend the original approval component with grouped medicine fields and target selection. Preserve entered values, keyboard/focus behavior, cancellation, and offline disabling; add no replacement box page or permanent panel.

## 11. Implementation And Release

Three phases, each requiring a paired Gateway and desktop release (new ISCP operations or a Catalog revision). Push, deploy, and install need separate owner approval.

### 11.1 Phase 1: Backend Storage And Sync

- Gateway: `internal/medicine`; store contract and three implementations; migration `0021_medicine_box.sql`; `medicine.sync / items.* / import` interfaces; event category `medicine`; validation test cases shared with the desktop.
- Desktop: schema v3 cache; sync client (modeled on `mail-sync-client.mjs`); `MedicineBoxCapability` `save / remove` through forward operations; migration upload; offline disabling; `workbench-proxy-policy` allowlist; `iscp-capabilities.mjs` and timeouts.
- Acceptance: multi-computer sync, offline viewing, conflict notice, migration (empty box, 2000 rows, resume after interruption, repeated upload), and every existing box UI interaction (photo entry, text entry, detail quantity adjustment, filtering and sorting).

### 11.2 Phase 2: Query Routing

- `box_query` (`#read`, `#advise`) and `drug_info` Profiles, `medicine.box.read` tool, `medicine.info_source` operation, sibling hard negatives, Catalog v29, rebuilt calibration.
- Acceptance: for each variant in section 5.2, direct, paraphrased, cross-variant confusion, confusion with `conversation.answer` / `browser.internet_search`, negation, quotation, troubleshooting, compound, English, and non-medicine cases; desktop, WebChat, WeChat, and timer sources; empty box, expired, near expiry, no expiry, multiple same-name rows, evidence truncation; all three `#advise` outcomes (usable medicine stocked, only expired stock, buy needed) plus emergency, child, and prescription cases; at least two cases per source-decision reason_code, decision failure going to Info, and Info failure falling back to the model.

### 11.3 Phase 3: Conversation Edits

- `box_edit` Profile, `medicine.edit_plan` and `medicine_edit_v2`, batch write tool and transaction service, target selection, conditional completion approval, and per-entry reply templates.
- Extend the approval form and versioned input contract across Gateway, LAN/ISCP, desktop approval journal, and WebChat, including draft/final-batch binding, idempotent completion, and receipt reconciliation; a display-only form is insufficient.
- Acceptance: single/batch create and update (1, 2, and 20 entries; reject 21 without truncation); preserve a single intent across medicine names; keep each medicine's fields and order aligned; complete fields write directly without approval; some/all entries missing quantity, unit, or expiry, or containing ambiguous fields, open just one grouped completion window; empty optional fields need no prompt; explicit same-name target selection; incomplete fields disable submission; month-end dates, explicit no-expiry choice, same-name creates, and crossing capacity.
- Retain set, delta, zero, negative-result, location, notes-append, and single-delete cases. Add duplicate target updates, version changes during the wait, and rejection/cancellation/expiry/restart yielding zero writes for the whole batch; atomicity, crash recovery, and per-entry events across all three stores; double submission and lost receipts causing no duplicate creates or decrements; rejection of wrong owner/client, tampered fields, stale drafts, and changed-payload replay.
- Cover LAN/ISCP and WebChat forms, disabled submission on old clients, WeChat success with complete requests and zero-write missing-field replies, timer batch success or termination when interaction is needed, and edits blocked under degraded semantic routing.

### 11.4 Real-Model And Native Acceptance

- Spot-check the source decision, change parsing, and all three answer kinds on the fast lane (`sparkclaw-fast`), following `TestMedicineRealModelSpotCheck`.
- Accept in the packaged app: preserve original conversation and box entry points, photo/text entry, detail quantity adjustment, and filtering/sorting; verify grouped-form keyboard/focus behavior, validation, cancellation, reconnection, and duplicate submission; all batch changes synchronize into the box UI.

## 12. Out Of Scope

- Bulk deletion by condition, and atomic commits across business spans with mixed operations.
- An AI search box inside the medicine box module (the open-medkit adaptation design's section 9 "not for now" decision stands).
- Medication reminders (they belong to `schedule.manage`).
- Recommending specific brands or giving purchase links.
- Keyword fallback routing or hard-coded branches by medicine name.


## 13. Local Implementation Checkpoint (2026-11-02)

- Catalog `2026-11-02.v29` adds all three medicine leaves and the existing semantic split/fusion path. A multi-medicine edit remains one batch. Clear fields write directly; missing/ambiguous fields produce one grouped form in the existing approval container. Noninteractive channels return the missing fields with zero writes.
- The persistent owner-scoped medicine repository survives temporary conversation execution. Memory, file and PostgreSQL support atomic 1–20-entry create/update, single delete, per-record versions, bounded sync and immutable receipts. Unknown commit outcomes are checked against the original owner/payload-bound receipt, never resubmitted as a new mutation.
- Approval form schema 1 and input digests bind the original frozen draft, decision and completion values. LAN/ISCP, host WebChat and desktop use the same contract. Desktop workbench schema 16, execution control version 5, medicine cache schema 3, and PostgreSQL migrations `0021_medicine_box.sql` / `0022_approval_inputs.sql` must be released together. Old medicine rows are retained in `legacy_items` during bounded migration uploads.
- The original medicine rail, category sidebar, cards/table, search, expiry filters, detail quantity controls, photo/text entry and review dialogs are retained. Offline writes are disabled in place; conflicts use existing error locations. No permanent sync-status panel was added.
- Qualification: full Linux Gateway suite (5994 passed / 214 skipped), build/vet and affected race checks passed. The production execution/file-inventory test covers form completion, rejection, durable receipts, owner isolation and duplicate requests. Full store tests against isolated PostgreSQL, concurrent version conflicts, SQL receipt-insert rollback and file persistence fault cases passed. Desktop 627 passed / 1 skipped; WebChat 592 passed; desktop UI build passed. Seven isolated Chrome interaction groups and screenshots cover the original medicine components and grouped completion form. Real Fast checks passed for 16 medicine routes, exact complete/missing fields, the 20/21-entry boundary, three source decisions and read/advice/information answers. The broader 63-case search run exposed two private-state routing errors, one outdated medicine expectation and four failed Tree channels; after correction, all 16 affected, held-out and control cases passed with two healthy channels. This is not a claim that all 63 cases were rerun successfully; earlier failed logs are retained.
- This is source qualification, not a packaged release. The isolated Electron harness stalled before renderer startup, so native packaged-app acceptance is still required. Live Info, deployment, installation and multi-device production migration are not claimed; section 11 remains the release acceptance checklist.
- 2026-11-03 edit parsing fix: real-model WebChat checks showed every chat update was rejected as a mixed batch and deletes failed parsing, because the model set the batch `operation` to `mixed` and gave deletes stray changes. The wire format is now `medicine_edit_v4` with per-entry actions; deletes drop changes and field gaps; target matching adds the containment tier above. The real qualification test now also covers single and batch updates, moves, restocking, deletes and two mixed requests. It needs `SPARKCLAW_MODEL_CAPACITY_PROFILE=sparkclaw-product-v1`: under the default mock profile the Tree channel rejects the routing prompt as oversized.
