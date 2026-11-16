# McDonald's ordering Workflow refactor design

> Language: English | [简体中文](../zh-cn/docs/mcd-ordering-workflow-refactor-design.md)

Date: 2026-11-16. Revision: R4 (all section 10 decisions confirmed: cart approval, discount comparison, amount limit and pickup methods corrected after checking the code, and the store confirmation flow decided). Status: implemented in this worktree's source; not repackaged, deployed or qualified by a real order/payment. This document replaces the earlier five-leaf orchestration in section 8 of the [EzMcDonalds split integration design](ezmcd-split-integration-design.md). The Mac/work2 split, sessions, quote handoff, wallet payment, ledgers and authority protocol stay unchanged. The [implementation checkpoint](ezmcd-adapter-implementation.md#november-16-ordering-workflow-checkpoint) separates confirmed checks from remaining validation and release gates. Review fixes on the same date add request-budget planning (section 4.5), stale pickup-slot handling, a separate refresh allowance, settlement replies for undispatched payments and coupon verification by quote line.

**There is one user-facing capability: ordering. One Workflow runs the complete flow, from parsing the request to confirming this order's status. Questions are asked through approval dialogs. A successful order returns the pickup code.**

## 1. Problems in the pre-refactor baseline

The pre-refactor code (`d3c619e1`) registered five routing leaves: `dining.mcd_read`, `dining.mcd_cart`, `dining.mcd_quote`, `dining.mcd_purchase` and `dining.mcd_status`. The current [catalog.go](../services/gateway/internal/capability/catalog.go) and [workflow_registry.go](../services/gateway/internal/agent/workflow_registry.go) register only `dining.mcd_order`. The following describes the old behavior that motivated R4:

1. **The flow became a routing guess.** For "order a McSpicy chicken burger meal for me", routing has to guess the stage: read, cart, quote or purchase. Ordering is one continuous task, but it is split into five independent capabilities, and the only way to move between stages is another user turn.
2. **The Workflow is a tool set, not a flow.** Each old profile has a single node (`mcd_read` or `mcd_selection`). The cart and purchase nodes let the model pick tools freely for up to eight rounds in the now-removed `mcd_cart_workflow.go`; its replacement is [mcd_order_workflow.go](../services/gateway/internal/agent/mcd_order_workflow.go). Stage order, preconditions, when to ask and when to stop all depend on the model following English hints in `StageContext`. There is no deterministic progression and no dependency structure, so this is a generic agent behind a tool allowlist.
3. **There is no channel for questions.** The prompts forbid inferring the store, time or pickup method, but when information is missing the model can only ask back in its final answer. When the execution ends, its execution-scoped resources (temporary cart directory, quote handoff, `review`) are cleaned up ([execution_workflow.go](../services/gateway/internal/gateway/execution_workflow.go)). After the user answers, routing and session setup start over, and all progress is lost.
4. **Order status is not tied to the ordering flow.** Checking the result after payment is a separate `dining.mcd_status` capability that waits for the user to ask. It also needs `mcd.purchase.status`, which only exists while `purchase_enabled` is on ([mcd_purchase.go](../services/gateway/internal/toolhub/mcd_purchase.go)). After a rollback that disables payment, an original purchase with an unknown result cannot be reconciled.
5. **The model writes the output freely.** All five profiles use `workflowFinalizationModel`, so the final reply may restate menus or quotes, or invent them. The user only needs the pickup code.
6. **Routing still happens while the flags are off.** The leaves are not gated by the `mcd_selection` flags. With no tools present, the "must call the required tool" check does not apply ([workflow_step_loop.go](../services/gateway/internal/agent/workflow_step_loop.go)), so the model may answer menu or price questions on its own.

## 2. Goals and non-goals

Goals:

- Routing keeps a single leaf, `dining.mcd_order` (ordering).
- One Workflow expresses the complete flow as a multi-node DAG, advanced deterministically by the runtime. The model does only two things: it parses the user's words into a structured order intent, and it ranks official menu candidates.
- Missing or ambiguous information is asked through a `notify.ask_approval` input form. After the answer, the **same execution** continues without rerouting, so the cart and quote state are kept.
- Unstated details use fixed defaults instead of questions: quantity 1, the earliest available pickup time, the best discount chosen automatically, and the account's single-order limit as the amount limit.
- Cart writes inside the flow are not approved one by one; the payment approval is the only release gate. Clearing an existing cart still needs confirmation.
- The first version offers only pickup methods that passed live qualification (currently `locker-in`, the pickup locker).
- Payment confirmation reuses the existing immutable approval (`mcd.purchase.commit`).
- At the end of every order, the Workflow confirms this order's status and handles it. A successful order returns the pickup code.

Non-goals:

- No separate "check my last order" capability.
- No separate "browse menu" or "view offers" capability.
- No new payment channels (still `ARCHCARD` only).
- No changes to Mac ledgers, authority digests, quote handoff, ISCP operations or credential flow.
- No replacement of existing approval components or connection settings pages, only the copy changes listed below.

## 3. Routing

| Item | Pre-refactor baseline | R4 source implementation |
|---|---|---|
| Leaves | Five `dining.mcd_*` leaves | One `dining.mcd_order`, `Operations: [draft]`, `RequireQuery: true` |
| Routing examples | One set per leaf | "Order a McSpicy chicken burger meal for me", "Order two Big Macs at the McDonald's downstairs from the office, pick up at 12", "Buy breakfast with my McDonald's wallet, under 30 yuan" |
| Hard negatives | Leaves exclude each other | "History of McDonald's", "How many calories in McDonald's food", "What fast food is nearby", "Did my last McDonald's order go through?" (left to general answers or search, never ordering) |
| When unavailable | Routed normally; the model answers freely | Still routed to this leaf; the entry node returns a fixed message without calling the model |

Routing is kept while the capability is unavailable so that "order McDonald's for me" gets a clear message instead of being caught by another capability or answered with invented content. This matches the project UI rule: the entry stays and the capability is disabled.

Flags: the `enabled`, `cart_enabled` and `purchase_enabled` settings stay for rollback compatibility and all default to false. Ordering requires all three, plus negotiated and qualified ISCP operations (the existing logic in [execution_authorization.go](../services/gateway/internal/gateway/execution_authorization.go)). Status reconciliation needs only `enabled` plus `mcd.operation.status` / `mcd.orders.query`, through the independent `AllowMCDStatus`, without depending on `cart_enabled` or `purchase_enabled`. After a rollback that disables payment, the entry node can still reconcile an unresolved original order before replying that ordering is unavailable. The full existing integration capabilities remain available behind their original qualification gates; the single ordering leaf narrows user-facing orchestration, not the adapter's capability inventory.

Pickup methods: a new setting, `mcd_selection.qualified_eat_types`, defaults to `["locker-in"]`. Only listed methods appear in candidates and dialogs. Ordinary in-store pickup (`take-in-store`) is added to the list once its quote handoff passes live qualification, with no code change.

## 4. Workflow: `dining.mcd_order`

The profile implements `workflowRuntimeStageProfile`. Each node runs directly in `RunStage`, the same way the medicine-box edit Workflow does ([medicine_profiles.go](../services/gateway/internal/agent/medicine_profiles.go), [medicine_edit.go](../services/gateway/internal/agent/medicine_edit.go)). McDonald's tools are no longer in the model's visible tool list; stage code calls them directly. Finalization uses deterministic templates.

```text
guard ──► intent ──► context ──► menu ──► offer ──► cart ──► quote ──► pay ──► settle
  │                     │          │                   │         │         │        │
  └─ unresolved: stop   └─ form ───┘     (clear prompt)┘  one requote  payment     └─ confirm and handle this order
                                                          without coupons  approval
```

| Node | Execution | Calls | Output / exit condition |
|---|---|---|---|
| `guard` | Deterministic | `mcd.purchase.status` (recovery mode) | A safety gate, not a user-facing capability. If an earlier original operation has an unknown result, it is reconciled once. If it is now verified `PAID` or `CANCELLED`, the freeze is released, this order continues, and the final reply carries one line about the earlier result. If it is still unresolved, the node stops with a fixed message and **does not start a new order** |
| `intent` | Model, structured output, no tools | — | `OrderIntent` (section 4.1). Records only what the user said and never fills in official identifiers |
| `context` | Deterministic reads plus a form | `stores_favorites`, `cities_list`, `stores_search`, `pickup_methods`, `pickup_slots` | One exact store, pickup method and pickup time. The user always confirms the store in a dialog; see section 4.4. The pickup method and time are taken automatically when the user's words match exactly one candidate; otherwise they are added to the question form. The pickup method is chosen only from the intersection of `qualified_eat_types` and the store's methods; section 4.4 guarantees the confirmed store supports at least one of them. An unstated pickup time takes the earliest `pickup_slots` slot at least three minutes away, without a question. Before the first cart write, such a defaulted slot that has come within three minutes is re-read and replaced; an owner-chosen time that has passed stops the order |
| `menu` | Deterministic reads, model ranking, form | `menu_list`, `product_detail` | Binds each intent item to one `product_code` and fills required configuration groups. The model can only rank given candidates, and the runtime verifies its choices belong to the candidate set. Ambiguity becomes a question. An unstated quantity is 1 |
| `offer` | Deterministic | `coupons_list`, `coupon_products`, `product_detail` with `coupon_ref` | Decides coupon or no coupon for each line before any cart write, under section 4.2, without asking |
| `cart` | Deterministic writes, not approved one by one | `cart_snapshot`, `cart_clear`, `cart_add_simple` / `cart_add_configured`, `checkout_quote_pickup` | Writes the `offer` result so the cart matches the plan line by line. If the existing cart is non-empty and differs from the plan, a form asks "clear and continue / cancel"; this is the only cart-related confirmation in the flow |
| `quote` | Deterministic | `mcd.quote.prepare` | Hands the complete quote to the originating Mac and produces `review`. Checks discounts under section 4.2; if the total exceeds the amount limit, stops before the payment approval |
| `pay` | Existing immutable approval | `mcd.purchase.commit` (arguments bound by the runtime) | The approval dialog shows store, time, method, items (including default quantities), applied discounts, amount and `ARCHCARD`. Rejection aborts the preparation without ordering |
| `settle` | Deterministic | `mcd.purchase.status`, `mcd.orders.query` when needed | Runs at the end of every order; see section 4.3 |

All questions come before `cart`, except the clear-cart prompt inside `cart`. Rejecting a question form cancels the order. Nothing has been written at that point, so the reply is simply "Cancelled, no order placed".

Cart writes are not approved one by one. The existing `mcd.cart.write` is defined as requiring approval ([mcd_cart.go](../services/gateway/internal/toolhub/mcd_cart.go)). After the refactor, the `cart` node calls it directly without per-call approval: the cart is reversible, creates no order and charges nothing, and the payment approval confirms the complete contents before ordering. Writes still follow the existing operation-key, snapshot and reconcile-only-on-unknown rules.

Execution limits reuse existing mechanisms. The whole flow runs in one execution, and the execution stays alive while waiting for approvals (`continueExecutionApprovals` in [execution_workflow.go](../services/gateway/internal/gateway/execution_workflow.go): at most 32 rounds within a 15-minute budget). The quote's 120-second expiry and the limit of two approved-quote refreshes stay unchanged; changed content requires a new approval. A planned no-coupon requote is a new plan quote and does not use a refresh. Provider requests follow section 4.5.

### 4.1 `OrderIntent` structured output

```json
{
  "items": [{"text": "McSpicy chicken burger meal", "quantity": null, "notes": "no ice in the cola"}],
  "store_text": "downstairs from the office",
  "pickup_method_text": "pick up in store",
  "pickup_time_text": "",
  "max_amount_text": "under 30 yuan"
}
```

The JSON Schema is strict. It accepts no store code, product code, coupon code or numeric amount. The runtime, not the model, fills in defaults:

- A `null` `quantity` means 1.
- An empty `pickup_time_text` means the earliest available slot.
- The runtime parses `max_amount_text` into integer cents as this order's amount limit. When it is empty, the account's local Policy single-order limit `max_order_cents` is used, because upstream quotes require an amount limit (`max_amount_cny`). A user limit above the single-order limit is capped at the single-order limit. If the user stated a limit that cannot be parsed into an amount, the workflow asks.
- Daily budgets and daily order counts (upstream defaults: 20 yuan per order, 20 yuan per day, one order per day) stay unchanged. This design neither relaxes the defaults nor adds a configuration UI. Exceeding them is reported as needing user action.

If the model call fails, the reply is "模型暂时不可用" (model temporarily unavailable), following the project convention.

### 4.2 Discount selection

The best discount is chosen automatically, without asking the user.

Constraints: a McDonald's coupon is a coupon-priced product, added to the cart as one line with a `coupon_ref`. The cart can only be cleared as a whole; single lines cannot be removed. Also, once a quote has been handed to the Mac within an execution, no new quote is allowed until that handoff is aborted (`quoteLocked` in [cart.go](../services/gateway/internal/mcddining/cart.go)). So there is no multi-quote comparison. The plan is fixed before any cart write, quoted once, and requoted once without coupons when needed.

1. **Candidates**: for each intent item, coupon-priced products that `coupons_list` reports as currently usable, that `coupon_products` maps to the same product, and that upstream marks as directly addable (`cart_configuration_supported`). Coupons that require extra items or configuration in the official App are not used; the user's order is never changed to qualify for a coupon.
2. **Per-line choice**: compare the coupon product's display price with the `menu_list` price, including the selected component and modifier surcharges on both sides, and use the coupon only when it is cheaper. Component quantities multiply their extra prices and nested modifier charges; line quantities then scale the complete per-meal price. A coupon must preserve the selected configuration. When one coupon fits several lines, assign it by savings from largest to smallest, within its remaining uses. Display prices only pick the option; they are not final prices.
3. **Quote check**: after the `quote` node gets the complete quote, it checks that:
   - every planned coupon line appears as a quote line bound to a coupon (`coupon_fingerprint`) with the same product and quantity, no unplanned coupon line appears, and `discount_cents` is positive. `discount_names` come from a different upstream field than coupon list titles, so they are shown but not compared;
   - the total is lower than the all-menu-price total;
   - the total does not exceed the amount limit.
4. **Requote without coupons**: if the check fails, the Mac aborts this preparation; the cart is cleared and rewritten without any coupons, and quoted once more. If the new quote still exceeds the limit, the workflow stops. Each order makes at most two quotes. Coupons are planned only while this requote still fits the request budget (section 4.5).
5. **Over the limit**: if the no-coupon option also exceeds the limit, the workflow stops before the payment approval with a fixed message and never removes items automatically.

The payment approval lists the applied discount names and amounts. Coupons never change after approval.

### 4.3 End-of-order confirmation (`settle`)

`settle` confirms the status of every order when it ends, so the user never has to ask separately.

| This order's status | Handling |
|---|---|
| Verified `PAID`, receipt includes the pickup code | Reply with the pickup code |
| Verified `PAID`, pickup code not generated yet | Query at most three times (immediately, then after 3 and 8 seconds) within the remaining execution deadline. If there is still no pickup code, reply with the order number when available and say the pickup code will appear in the McDonald's App. A verified `PAID` receipt is never reported as unconfirmed |
| Verified `CANCELLED` | Reply "order cancelled, not charged" |
| Unknown result (lost response, timeout, helper interruption) | Reconcile only the original operation, at most three queries within the remaining budget, with no write retries. If still unknown, reply with a fixed message; `guard` reconciles it again automatically at the next order |
| Payment failed before dispatch (expired quote that could not refresh, changed content after the last allowed approval, authority mismatch) | Abort the preparation. Only when both ledgers prove nothing was dispatched, reply that no order was placed and why; otherwise reconcile like an unknown result |
| Aborted or rejected (payment approval rejected, over the limit, etc.) | Confirm the preparation was aborted and no write claim remains, then reply with the matching message |
| The official service needs user action (insufficient balance, verification in the App, etc.) | Confirm no payment was dispatched, then reply with the reason the user can act on |

### 4.4 Store confirmation

The user confirms the store on every order; it is never taken automatically. Store confirmation is the first question round of `context`, because pickup slots and the menu depend on it.

1. **Check favorite stores**: read `stores_favorites`; favorites the user's words mention come first. In that order, at most five favorites are checked with `pickup_slots` and `pickup_methods`, and only while a later coupon probe and the required ordering path still fit the request budget (section 4.5). Checked favorites that support none of `qualified_eat_types` are dropped; the rest stay listed unchecked and are checked after selection (step 5).
2. **Favorites exist: confirm a favorite**. The `store` dialog lists the remaining favorites; if the user's words mention a store, the matching one comes first. Two fixed options are always offered: "not these, choose another store" and "cancel the order". A single favorite still needs confirmation.
3. **No favorites, or the user chose "choose another store": open store selection**, following the official store selection interface in two rounds:
   - Round one: `city` (select, from `cities_list`) and `store_keyword` (text, optional, at most 40 characters, such as a district or road name). The keyword is used only as a `stores_search` filter, like the search box in the official App's store picker, and is never treated as a store identifier.
   - Round two: `store` (select), listing `stores_search` results in official order, at most three pages or 90 stores, shown as "store name · address · opening hours". It also offers "search again" (back to round one) and "cancel the order".
4. **Selecting is confirming**: choosing a store from the favorites or the search results is the user's confirmation; no extra confirmation round follows. The payment approval shows the store again.
5. **Check after selection**: for a store chosen from search results or an unchecked favorite, check `pickup_methods` once more. If it supports no enabled pickup method, say so and return to the same list without it.
6. **Limit**: store selection allows at most three new searches; beyond that the workflow replies with a fixed message and places no order. These reads count toward the existing request budget.

### 4.5 Request budget

An execution may reserve at most 128 provider requests, the cart session's unchanged original aggregate. Every call reserves its per-call maximum before dispatch, and nothing is refunded. With these maxima, a plain one-line order with one pre-checked favorite reserves 57 requests; one coupon adds 14, its possible no-coupon requote 41, and an approved-quote refresh 19.

- The Workflow reads this execution's remaining reservation from the gateway's connection record without reserving anything, and computes the required remainder from the same per-call maxima: the selected store's check, unresolved menu reads, a defaulted slot's re-read, the cart path including a possible clear, and the no-coupon requote whenever coupons are planned.
- Before the first store question, an order whose required path cannot fit stops with a fixed reply and makes no provider read.
- Optional reads run only while the required remainder still fits after them: favorite pre-checks (which also keep room for one coupon probe), additional search pages, the full-menu fallback and every coupon read. When a coupon does not fit, the order continues at menu prices.
- The required remainder is checked again before the first cart write, so an order does not stop for budget after changing the cart.
- Approved-quote refreshes are best effort. When none fits, the expired quote is aborted and the reply says that no order was placed.

## 5. Question dialogs

Questions reuse `notify.ask_approval` and `ApprovalInputForm` ([approval_inputs.go](../services/gateway/internal/app/approval_inputs.go); on the WebChat side, [approvalInputForm.tsx](../apps/webchat/src/components/approvalInputForm.tsx)). No new components are added.

Only the cases below open a question dialog; everything else uses defaults:

| Question | Field | Option source | When asked |
|---|---|---|---|
| Store (favorites) | `store` (select, with "choose another store" and "cancel the order") | Favorite stores that support an enabled pickup method | Every order that has favorite stores |
| Store selection, round one | `city` (select), `store_keyword` (text, optional) | `cities_list` | No favorites, or the user chose another store |
| Store selection, round two | `store` (select, with "search again" and "cancel the order") | `stores_search`, at most 90 | After round one is submitted |
| Pickup method | `pickup_method` (select) | Intersection of `pickup_methods` and `qualified_eat_types` | More than one method in the intersection, and unstated or not uniquely matched (never shown in the first version, which has only `locker-in`) |
| Pickup time | `pickup_time` (select, earliest first) | `pickup_slots` | A time was stated but not uniquely matched |
| Product | `item_N.product` (select) | Menu candidates, top 20, with prices | Not uniquely matched |
| Meal or configuration | `item_N.<group>` (select) | Required configuration groups from `product_detail` | The request did not specify that group |
| Amount limit | `max_amount` (number, in yuan) | — | A limit was mentioned but could not be parsed |
| Clear cart | `clear_cart` (select: clear and continue / cancel) | `cart_snapshot` | The existing cart is non-empty and differs from the plan |

Rules:

- All questions of one round go into one form, one entry per question, so the user is asked once where possible. When options depend on an earlier answer (for example, pickup slots depend on the store), questions are asked in dependency order across rounds.
- Option values are opaque identifiers from official evidence. They are frozen in `input_context` together with the candidate snapshot. On submission, the existing `ValidateApprovalInputs` checks that each answer is one of the options. The model cannot generate option values.
- Configuration notes are matched automatically only when the complete note uniquely names an available official choice. Negated, multi-part or unmatched notes are shown with the official configuration form for explicit confirmation; if no supported form can resolve them, ordering stops. Notes are never silently discarded or treated as substring matches.
- When no candidate fits, the form offers "none of these, cancel the order". There is no free-text fallback. The only text input is the optional `store_keyword` in store selection round one, used only as an official store search filter.
- `ApprovalInputForm` now has an optional `submit_label`, included in the form digest and preferred by WebChat. It is limited to 80 Unicode characters, with blank and control-character labels rejected. Ordering forms use "Confirm order details" / "确认点餐信息". When omitted, the medicine-box copy "Complete and write {count} entries" is unchanged, as are the component and interactions.
- Channels without form support (WeChat, text-only executions) reply "Please order in the SparkX client" instead of falling back to text questions.

## 6. Reply templates

| Case | Reply |
|---|---|
| Ordered, pickup code available | `已下单，取餐码：{pickup_code}` (Ordered, pickup code: {pickup_code}) |
| Ordered, pickup code not yet available | `已下单，订单号：{order_no}，取餐码生成后可在麦当劳 App 查看` (Ordered, order number {order_no}; the pickup code will appear in the McDonald's App) |
| User cancelled during a question or the payment approval | `已取消，未下单` (Cancelled, no order placed) |
| Over the amount limit | `按当前餐单最低需 {amount} 元，超出上限 {limit} 元，未下单` (This order needs at least {amount} yuan, over the {limit} yuan limit; not ordered) |
| Store selection searched again more than three times | `未选定门店，未下单` (No store chosen; not ordered) |
| Not connected, not enabled, or Mac offline | `麦当劳点餐暂不可用，请在 SparkX 连接设置中连接麦当劳账号` (McDonald's ordering is unavailable; connect your McDonald's account in SparkX connection settings) |
| `guard` finds the earlier order still unresolved | `上一单付款结果尚未确认，暂不能再次下单` (The last payment is not confirmed yet; a new order cannot be placed) |
| This payment's result still unknown after reconciliation | `付款结果未确认，未重复下单；下次点餐前会自动核对` (Payment result not confirmed, no duplicate order; it will be checked automatically before your next order) |
| Order cancelled | `订单已取消，未扣款` (Order cancelled, not charged) |
| The official service needs user action (insufficient balance, verification in the App, etc.) | `未下单：{reason the user can act on}` (Not ordered: {reason}) |
| Required reads exceed the per-order request budget | `未下单：本单所需查询超出单次点餐额度，请减少餐品后重试` (Not ordered: this order needs more lookups than one order allows; order fewer items) |
| Verified paid, no order number or pickup code yet | `已下单，取餐码生成后可在麦当劳 App 查看` (Ordered; the pickup code will appear in the McDonald's App) |
| Expired quote could not refresh, nothing dispatched | `未下单：报价已过期，请重新点餐` (Not ordered: the quote expired; please order again) |
| Quote changed after the last allowed approval, nothing dispatched | `未下单：报价已变化，请重新点餐` (Not ordered: the quote changed; please order again) |
| Other payment failure before dispatch | `未下单：付款未发起，请重新点餐` (Not ordered: payment was not started; please order again) |
| Owner-chosen pickup time has passed before the cart write | `未下单：所选取餐时间已过，请重新点餐` (Not ordered: the chosen pickup time has passed) |
| No future pickup slot | `未下单：门店暂无可选取餐时间` (Not ordered: the store has no pickup slot available) |
| Model unavailable | `模型暂时不可用` (Model temporarily unavailable) |

When `guard` releases an earlier order's freeze, the reply is prefixed with one line: `上一单已确认：{已付款，取餐码 X / 已取消，未扣款}` (Earlier order confirmed: {paid, pickup code X / cancelled, not charged}).

Pickup code and order number source: only a verified terminal `PAID` receipt or an original order query result. The Python adapter normalizes `pickup_code` and `order_no` in receipts ([receipt.py](../tools/ezmcd-adapter/src/sparkclaw_mcd/receipt.py), [purchase.py](../tools/ezmcd-adapter/src/sparkclaw_mcd/purchase.py)) and order query results, using the pinned upstream fields. Go consumes these fields rather than parsing the opaque upstream `result_json`. The pickup code appears only in the reply, never in audit summaries or generic logs.

## 7. Safety invariants kept

- Mac is the only writer of sessions and payments. Credentials never enter model context, logs or replies.
- The runtime binds payment arguments to the complete `review`, and the approval content is immutable. Any change to amount, items, discounts, store or time requires a new approval. Discount selection and the requote without coupons both finish before the payment approval; coupons never change after approval.
- Cart writes are not approved one by one, but only the `cart` node can write, following the frozen plan; the model cannot call write tools directly.
- Only `ARCHCARD`; no payment channel substitution.
- Writes with unknown results are only reconciled: no retries and no new operation keys. Only verified `PAID` or `CANCELLED` releases the freeze.
- Original ledgers, daily budgets, member locks and revocation records stay unchanged.

The preflight fix is implemented: [purchase.py](../tools/ezmcd-adapter/src/sparkclaw_mcd/purchase.py) records `NOT_DISPATCHED` only when the original attempt and write records prove non-dispatch; ambiguous failures remain `UNKNOWN`. Through the existing status operation, [recovery.py](../tools/ezmcd-adapter/src/sparkclaw_mcd/recovery.py) can durably retire an exact, provably undispatched preparation and revoke its unused authority without calling the provider. The backend releases that preparation only after the bound `aborted_before_write` receipt is persisted. A missing operation or a bare `NOT_DISPATCHED` result is insufficient. Original attempts, receipts and budgets remain intact; this retirement does not claim an official cancellation or refund, and never retries a write.

## 8. Code changes

| Area | Change |
|---|---|
| Gateway routing | Merge the five leaves in [catalog.go](../services/gateway/internal/capability/catalog.go) into `dining.mcd_order`; add routing calibration and evaluation samples |
| Gateway Workflow | Replaced `mcd_workflow.go`, `mcd_cart_workflow.go` and `mcd_status_workflow.go` with [mcd_order_workflow.go](../services/gateway/internal/agent/mcd_order_workflow.go) (profile and nine-node DAG), `mcd_order_stages.go` (dispatch), and dedicated intent, context, menu, offer, cart, form and settle files. [mcd_order_payment_summary.go](../services/gateway/internal/agent/mcd_order_payment_summary.go) formats the bound payment review. Generic stage outcomes preserve progress and form answers in the same execution; `pay` binds commit arguments, replacing the special branch in [workflow_runtime.go](../services/gateway/internal/agent/workflow_runtime.go) |
| Gateway tools | `mcd.*` tools become Workflow-internal and are no longer exposed to the model; `mcd.cart.write` inside the `cart` node skips per-call approval; decouple the `mcd.purchase.status` registration from `purchase_enabled`. The `cart` node may use `mcd.selection.read` only for `pickup_slots`. Status without a pending purchase returns only the Policy ceiling, never earlier journal rows or pickup codes. [mcd_order_budget.go](../services/gateway/internal/agent/mcd_order_budget.go) plans reads against the remaining request reservation |
| Configuration | `mcd_selection.qualified_eat_types`, default `["locker-in"]` |
| Authorization | Add `AllowMCDStatus` to [execution_authorization.go](../services/gateway/internal/gateway/execution_authorization.go); the ordering entry requires `AllowMCDPurchase` |
| Approval forms | `ApprovalInputForm.submit_label` (Go and WebChat together, included in the form digest), with Chinese and English copy |
| Python adapter | Add `pickup_code` and `order_no` to receipts and order queries; record preflight failures as a provable not-dispatched state; confirm the abort before a requote without coupons completes reliably and leaves no payable preparation |
| Docs | Update links in section 8 of the split integration design, the implementation checkpoint and the docs index |

The original Mac UI, ISCP operation list and runtime packaging architecture stay unchanged. Native recovery is extended within the existing status path; no ISCP operation is added. Generated source contracts are synchronized, but this round has not rebuilt runtime packages or deployed them.

## 9. Migration and acceptance

Migration: the three flags were never enabled in production, so no persisted runs reference the old Workflow IDs and the old profiles can be removed directly. The old IDs remain only as rejection cases in the registry legacy tests. Ledgers, receipts and revocation records need no migration.

Acceptance:

- Unit tests:
  - success, question, cancel and failure paths for every node;
  - defaults: an unstated quantity is 1 and an unstated time takes the earliest slot;
  - rejection of out-of-range or tampered form answers; `OrderIntent` rejects official identifiers; rejection of model rankings outside the candidate set.
- Discount tests:
  - each line uses the cheaper coupon product, and coupon remaining uses are assigned correctly;
  - coupons that require extra items or official App configuration are not used;
  - a failed quote check aborts and requotes once without coupons, with at most two quotes per order;
  - the workflow stops when the no-coupon option still exceeds the limit; an unstated limit uses the single-order limit.
- Cart tests: writes inside the flow open no per-call approval; clearing an existing cart requires confirmation; the model cannot call write tools directly.
- Pickup method tests: only methods in `qualified_eat_types` appear.
- Store tests:
  - with favorites, every order asks for confirmation, even with a single favorite; the store matching the user's words comes first; favorites that support no enabled pickup method are filtered out;
  - with no favorites, or after choosing another store, the two-round city-plus-keyword and store selection runs;
  - choosing an unsupported store returns to selection; more than three new searches stops the order;
  - `store_keyword` reaches only `stores_search` and never becomes a store identifier.
- End-of-order tests: cover `PAID` with and without a pickup code, `CANCELLED`, unknown, rejected and user-action-needed states. An unknown result gets only bounded queries and no writes, and `guard` releases or blocks it at the next order.
- Fixture end-to-end: "order a McSpicy chicken burger meal for me" goes through favorite store confirmation, a configuration question, automatic coupon selection and the payment approval, and returns a pickup code. Also cover quote-expiry refresh, re-approval after changed content, Mac offline and flags off.
- Routing evaluation: at least 30 positive and negative samples, confirming that ordering requests reach `dining.mcd_order` and that order checks and non-ordering requests do not.
- UI: the existing approval components render ordering forms and the payment approval correctly, and the medicine-box form copy and behavior are unchanged. Passing functional tests does not replace interaction acceptance of the original interface.
- Real payment: still requires the user's separate explicit authorization of a concrete order. This design is not a payment authorization.

Implementation checkpoint (2026-11-16): Python adapter checks passed 63 tests; WebChat passed 620 tests across 94 files and its build; desktop passed 683 tests with one skip. Source contract checks, Go build/vet, MCD/routing race checks in agent/toolhub, and mcddining/mcdintegration race checks passed. Full Go runs and the corrected physical temporary-directory reruns leave only the three known gateway `/dev/shm` memory-workspace failure groups; a long workspace temporary path exceeded the Unix socket limit, and `cmd/sparkclaw` passed separately with a short physical `/private/tmp` path. The bilingual routing corpus contains 44 positive/negative samples and deterministic contract checks; live model calibration has not run, so these samples do not establish live routing accuracy. See the [implementation checkpoint](ezmcd-adapter-implementation.md#november-16-ordering-workflow-checkpoint) for evidence boundaries. No new runtime package, installation, deployment, real order or payment is part of this checkpoint.

## 10. Confirmed decisions (2026-11-16)

1. There is no "check my last order" capability. `settle` confirms each order's status when it ends and handles it; an earlier unresolved order is reconciled automatically by the next order's `guard`.
2. An unstated quantity defaults to 1.
3. An unstated pickup time defaults to the earliest available slot.
4. The best discount is applied automatically; a coupon that gives no real discount, or that would exceed the user's amount limit, is not used.
5. A successful order returns the pickup code.
6. Cart writes inside the flow are not approved one by one; the payment approval is the only release gate, and clearing an existing cart still needs confirmation.
7. Discounts are fixed per line before any cart write and quoted once; if the quote check fails, the preparation is aborted and requoted once without coupons.
8. When the user states no amount limit, the account's single-order limit is used; daily budgets and order counts keep the upstream defaults.
9. The first version offers only `locker-in` pickup; other methods are added to `qualified_eat_types` after qualification.

10. The user confirms the store on every order: with favorites, a favorite is confirmed; with no favorites, or when the user declines them, the official store selection opens (city plus an optional keyword, then a store), and selecting is confirming.
