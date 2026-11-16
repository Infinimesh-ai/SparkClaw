# EzMcDonalds split integration implementation

> Language: English | [简体中文](../zh-cn/docs/ezmcd-adapter-implementation.md)

Date: 2026-11-16. **P0–P4 and the confirmed R4 single ordering Workflow are implemented in this worktree's source; live product release qualification remains incomplete and all three rollout flags default to false.** No new real order or payment was made. The November 16 source has not been repackaged, installed or deployed. See the [split design](ezmcd-split-integration-design.md), [ordering R4](mcd-ordering-workflow-refactor-design.md) and historical [November 13 implementation evidence](evidence/ezmcd-product-implementation-2026-11-13.json).

## Handoff and live evidence

The referenced upstream handoff completed one explicitly authorized ¥14.90 `ARCHCARD` purchase. Its authority has been consumed. Its original account, operation keys and purchase ledger remain authoritative; that purchase does not qualify SparkClaw's new Workflow or authorize another meal. The public handoff fixes are pinned as a checksum-verified patch over upstream `2497334e4ff1b2331773a36890db74136b288537` (0.1.0a50, MIT), with adapter revision `sparkclaw-mcd-handoff-v3`. The patched upstream suite passed 787 tests with two skips.

The [November 13 G1 check](evidence/ezmcd-handoff-g1-2026-11-13.json) verifies the installed 7.0.451 binary, live v4 member and v5 order-list reads, unchanged account file, unchanged original ledger and unchanged observation timestamps. It resolves the earlier current-build v5 blocker without changing the historical failed reports. No sampler was restarted and no observation lifetime was extended.

The [November 12 selected G2](evidence/ezmcd-g2-selected-handoff-2026-11-12.json) remains historical real work2 → fresh Mac Store evidence for store `3330613`, `locker-in`, 19:30. Complete quote and pickup receipt survived transfer; purchase digests matched, original 120-second expiry was preserved, repeated import and five rejection cases passed, and Mac persisted an abort before dispatch. The ordinary `eat-in` path was not qualified. The earlier 18:30 UNKNOWN switch remains in its original ledger, with a separate expired-intent retirement receipt; it was never retried or rewritten as successful.

The [session foundation check](evidence/ezmcd-session-foundation-2026-11-12.json) proved encrypted work2 persistence and new-process reads, renewal and revocation over authenticated SSH. It is distinct from product ISCP qualification. The [initial failures](evidence/ezmcd-p0-qualification-2026-11-12.json) and [credential portability evidence](evidence/ezmcd-credential-portability-2026-11-12.json) are retained.

## Implemented responsibilities

| Area | Implementation and boundary |
|---|---|
| P0 helpers | [Private Python adapter](../tools/ezmcd-adapter/src/sparkclaw_mcd), fixed official endpoints, explicit build table, shared canonical digest vectors, pinned upstream/dependencies and MIT attribution. No public MCP/HTTP listener. |
| P1 native session | [Desktop session](../apps/desktop/src/main/mcd/session.mjs) imports an already verified original account. OS-backed encrypted native storage retains the full host account; only minimal v4 material enters the authenticated connection. Pending provision/revoke requests persist before transmission. Renewal does not refresh the upstream account or its observation timestamps. |
| P1 Gateway | [mcdintegration](../services/gateway/internal/mcdintegration) uses Vault CAS, separate encrypted material/control records and permanent anchors, revocation tombstones, expiry sweeps, scope checks and aggregate execution budgets. Delayed responses cannot revive revoked access. |
| P1/P2 product routing | Three forward session operations and six reverse host operations are registered, independently negotiated and explicitly qualified through ISCP. Sensitive messages bypass generic receipt journals and body spooling. No HTTP/MCP fallback. |
| P2 cart/quotes | [mcddining](../services/gateway/internal/mcddining) owns a bounded temporary Store, model-safe projections, cart serialization, durable pre-dispatch fences, full quote transfer and Mac prepare/abort receipts. Missing temporary content never authorizes replay. |
| P3 wallet host | [purchase.py](../tools/ezmcd-adapter/src/sparkclaw_mcd/purchase.py) runs the original upstream service and budget/reservation/write logic against the original Mac ledger. [Native host](../apps/desktop/src/main/mcd/host.mjs) independently checks the locally saved immutable approval before invoking it. Only `ARCHCARD` is supported. |
| Workflows | One [dining.mcd_order](../services/gateway/internal/agent/mcd_order_workflow.go) profile advances nine stages deterministically in the same execution, with model calls confined to intent parsing and official candidate ranking. Existing forms resume that execution; the `pay` stage binds the complete immutable prepared review to `ARCHCARD` approval. Status recovery is independent of cart/payment flags. The five old leaf IDs are rejected; menu/status questions cannot initiate ordering. Model text cannot select an account, provide credentials, replace the ledger or grant spending authority. |
| P4 UI/runtime | A native adapter is added under the existing Connections directory. Original navigation and providers are retained; unavailable actions stay disabled. Self-contained Python runtime builders, source contracts, native manifest pins, package audit and an operator configuration are implemented. |

The [tool inventory](../tools/ezmcd-adapter/src/sparkclaw_mcd/tool-inventory.json) reviews all 38 pinned upstream tools. Implemented helper operations do not imply product qualification. The selection role has no purchase, refresh or wallet-token operation. A native host process can purchase only through its scoped preparation and original persisted approval.

## November 16 ordering Workflow checkpoint

The confirmed [R4 decisions](mcd-ordering-workflow-refactor-design.md#10-confirmed-decisions-2026-11-16) are implemented in source:

- A single `dining.mcd_order` (`draft`, `RequireQuery: true`) advances `guard → intent → context → menu → offer → cart → quote → pay → settle`. Generic stage outcomes preserve progress, candidate snapshots and form answers within the same execution. Existing approval and connection components, layouts and interactions remain.
- Optional `ApprovalInputForm.submit_label` is validated and included in the form digest. Ordering uses Chinese/English confirmation labels; omitted labels retain the exact medicine-box behavior. Unresolved configuration notes require explicit confirmation of official choices or stop ordering.
- Coupon and regular prices both include the selected component/modifier surcharges. Coupon allocation is bounded by remaining uses, preserves the chosen meal and finishes before cart writes; failed verification permits one abort and requote without coupons, at most two quotes. The payment review shows the fixed items, discounts and amount and remains immutable, with `ARCHCARD` as the only channel.
- `AllowMCDStatus` requires enabled, negotiated and qualified status operations independently of `cart_enabled` and `purchase_enabled`. Native `NOT_DISPATCHED` requires original-ledger evidence; the existing status path durably retires the exact undispatched preparation and unused authority before the backend releases it. It constructs no provider, creates no order, and adds no ISCP operation. Missing or ambiguous evidence remains unresolved, with no write retry.
- Review fixes plan provider requests against the execution's remaining 128-request reservation: an order whose required path cannot fit stops before the store question; favorite pre-checks, extra search pages, the full-menu fallback and coupon reads run only while the required remainder still fits; the remainder is checked again before the first cart write. A defaulted pickup slot is re-read before the cart write when it has come within three minutes, and the `cart` node may read only `pickup_slots`. Approved-quote refreshes have their own allowance of two, so a planned no-coupon requote no longer consumes it. Coupons are verified by the quote's coupon-bound lines (`coupon_fingerprint`), not by comparing coupon titles with quote names. `settle` queries at most three times over about 11 seconds, never reports a verified `PAID` as unconfirmed, and reports a proven undispatched payment as not ordered. Status without a pending purchase returns only the Policy ceiling, not journal rows or earlier pickup codes.
- The full existing helper/integration capability inventory is retained, and all three rollout flags remain false by default. `qualified_eat_types` defaults to `["locker-in"]`. Source contract generation is synchronized; runtime and package rebuild, installation and deployment remain outstanding.

Confirmed checks at this checkpoint: 63 Python adapter tests; 620 WebChat tests across 94 files plus its build; and 683 desktop tests with one skip, including three native `_verifier` regression tests. Generated source contract checks and Go build/vet passed. Agent/toolhub MCD and routing race checks, and mcddining/mcdintegration race checks passed. Two full Go runs plus physical temporary-directory reruns leave only the three known gateway `/dev/shm` memory-workspace failure groups. The long workspace temporary directory exceeded the Unix socket path limit; `cmd/sparkclaw` passed when rerun separately under a short physical `/private/tmp` path. That environment failure remains recorded rather than counted as a product regression. The bilingual routing corpus has 44 positive/negative samples with deterministic contract checks; live model calibration has not run, so corpus coverage is not a live accuracy claim. These checks used fixtures; no real order or payment was made, and installed product/UI and live fault-matrix qualification remain separate.

## Transaction and recovery rules

The private protocol accepts one bounded JSON request on stdin; credentials never travel in argv, environment, model results or generic logs. A helper invocation is limited to 90 seconds. An execution has an original maximum 15-minute deadline, within the session lease (maximum 24 hours), with a durable aggregate budget of 64 read requests or 128 when cart access is issued. Each helper request also reserves a bounded sub-budget. Renewal cannot extend the original execution or reset its counters.

`mcd-json-v1` uses sorted ASCII keys, UTF-8, safe integers, explicit nulls and ordered arrays; duplicate keys, floats, unsupported encodings and excessive size/depth reject. Official responses with fractional metadata remain complete in bounded opaque JSON strings covered byte-for-byte by digests. Money uses integer cents. A quote binds deployment, owner, client, installation, account/member, credential revision, execution, purchase ID, epoch, review and full pickup receipt. Transfers are at most 256 KiB in 8 KiB chunks and preserve the original 120-second expiry. Upstream quote fingerprint and pickup revalidation remain in force.

A member-wide control record is persisted before cart dispatch. Unknown writes are query-only; a retry cannot invent a new operation key. An expired pickup intent may be explicitly retired only after its original time, preserving its UNKNOWN outcome and blocking replay of the old key. If temporary response content is lost, official-App review or explicit expired-intent retirement can still be necessary; automatic recovery of every cart outcome is not claimed.

Preparation initializes adapter control tables only in an explicitly empty qualification Store, or adopts an existing original Mac ledger after committing its native anchor. Established missing/corrupt/non-private ledgers, inconsistent anchors and incomplete older schemas fail closed. There is no automatic empty-ledger fallback, destructive repair or prototype migration. Original attempts, writes and daily budgets survive adoption and reconnect.

Purchase approval binds purchase digest, amount, currency, wallet channel, store code/name, pickup time/method, complete items and discount names, and credential revision. It must exist both in Gateway Policy and the originating Mac's ClientStore. The host reserves command and authority before calling upstream. Authority consumption and the first original write claim are atomic. The original member lock and spending constraints remain active. Duplicate keys return the original attempt; they never call submit or `pay_existing` again. Lost responses, helper termination and ambiguous outcomes require status reconciliation. Only verified terminal `PAID` or `CANCELLED` evidence releases the backend purchase freeze.

Expired quotes can refresh at most twice within the original execution and request budget; this allowance is separate from the ordering Workflow's planned no-coupon requote. Mac first records `quote_refresh` abort for the undispatched epoch. A selected pickup needs a fresh snapshot and new switch receipt. Reuse of approval requires every bound review field to remain identical, including the purchase digest; even a lower price requires a new approval. A changed or expired authority never pays.

Disconnect stops an active native helper before removing local secrets and recording a revocation request. Offline revocation remains pending and is sent before reconnection. Reconnecting the same account retains account identity and the established ledger anchor while issuing a new connection ID. Logout, lock and authorization loss stop native execution. Status/history can read retained original receipts after revocation; this never restores selection or purchase access and local history is not fresh official verification.

## Build and operator configuration

Use Node 26 and Python **3.11.15**. Development setup is explicit and never performed at runtime:

```bash
npm run setup:ezmcd-adapter -- --python python3.11
npm run test:ezmcd-adapter
node --test apps/desktop/test/mcd-python-host.test.mjs
npm run check:ezmcd-contract
```

Setup verifies [upstream.lock.json](../tools/ezmcd-adapter/upstream.lock.json), the archive, public patch and exact dependencies. `--archive` accepts an already downloaded matching archive. Existing build directories are never overwritten: retain them and use a new output, or move the old development build before rebuilding. The development venv is not a release runtime.

On macOS arm64, build with a matching standalone Python prefix:

```bash
SPARKCLAW_MCD_BUILD_PYTHON=/path/to/python3.11 npm run build:ezmcd-runtime
npm run check:ezmcd-runtime
```

The builder records all file hashes, source/dependency contract and Python base metadata, relocates the output, then smoke-tests imports. It updates the Mac manifest pin shipped inside the client. The runtime is packaged outside ASAR; audit verifies the pin and full file set. The historical November 13 unsigned macOS arm64 DMG and ZIP artifacts under `apps/desktop/dist/` have hashes and package audit in that day's evidence; they were not installed and do not contain the November 16 refactor. Generated source contracts are now synchronized, but this round has not rebuilt the runtime or packages. Generated runtimes stay out of Git. Unknown architecture/build, altered files, missing runtime, or contract mismatch disable the capability or fail package validation.

Build Linux x86_64 natively with `scripts/package-ezmcd-runtime.py --build … --runtime-prefix … --output …`. Deploy the complete bundle in a stable directory and pin its manifest in the Gateway configuration:

```json
{
  "mcd_selection": {
    "enabled": false,
    "cart_enabled": false,
    "purchase_enabled": false,
    "helper_python": "/absolute/installed/ezmcd/bin/python3",
    "runtime_manifest_sha256": "<SHA-256 of this installed manifest.json>"
  }
}
```

Enable flags only for separately qualified capabilities and a matched authenticated Mac/work2 pair. Session access requires `mcd.session.provision`, `mcd.session.revoke`, `mcd.session.status`. Host access additionally negotiates `mcd.account.ensure`, `mcd.checkout.prepare`, `mcd.checkout.commit`, `mcd.checkout.abort`, `mcd.operation.status`, `mcd.orders.query`; purchase also requires the existing approval decision capability. Flags alone do not bypass qualification or user approval.

Use the original connection page to select the verified native account file; do not paste credentials into chat or renderer settings. The original source and original purchase ledger stay on Mac. The backend receives neither full account/v5 runtime nor a copied purchase database. For an expired/changed official session, complete authorized official-App verification first and import fresh verified material; there is no background login sampler or refresh-token flow.

Rollback disables new operations but retains encrypted control anchors, unresolved write claims, original ledgers, receipts and revocation tombstones. Do not delete them to clear an unavailable state. work2 root disk was below the 4 GiB reserve during acceptance, so builds stayed under `/tmp`; these temporary verification artifacts are not a production installation.

## Validation and outstanding release gates

The [machine-readable evidence](evidence/ezmcd-product-implementation-2026-11-13.json) records commands, counts, manifests and limits. Coverage includes Python ledger/authority/crash/replay contracts, cross-language canonicalization, native-to-real-Python preparation and synthetic purchase/restart, encrypted ISCP fixture negotiation/recovery, Go race checks, desktop and webchat regressions, source/runtime contracts and visual inspection of existing settings navigation. The payment fixture substitutes the external provider and cannot contact official services.

The current full macOS Go checks retain the same three `/dev/shm`-dependent gateway failure groups as the historical baseline, with the temporary-path rerun limits recorded above. Historical Linux evidence is separate and does not imply a current paired product release. The Node 26 webchat suite uses `NODE_OPTIONS=--no-experimental-webstorage` so jsdom owns browser storage. Historical initial frontend failures and the desktop timing failure remain recorded with their corrected successful reruns; no test result is rewritten.

Still unqualified: matched installed product ISCP under real disconnect/restart/lost-response faults; ordinary quote and broader menu/configured-combo/benefit methods; fresh account acquisition/cross-day refresh; the full installed desktop release; and a separately approved real purchase through this new Workflow. These are rollout gates, not additional permission to reuse the handoff's paid order. Payment remains disabled until a concrete new acceptance order is explicitly authorized. No further user action or verification code was needed for this worktree implementation.
