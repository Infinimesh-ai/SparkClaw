# SparkClaw client R3 implementation and acceptance

> Language: English | [简体中文](../zh-cn/docs/client-r3-implementation.md)

A subsequent local Mac session completed the ARM64 development build, launch and scoped isolated checks; see the [actual Mac acceptance record](macos-r3-acceptance.md). The text below preserves the historical Linux/shared delivery scope and does not claim full M01–M12 acceptance.

Date: 2026-10-03. Delivery branch: `codex/sparkclaw-r3`. P0–P5 Linux/shared source and applicable isolated checks are delivered. This is source delivery, not production cutover or Mac acceptance. The user builds on Mac after code completion. No Mac compile/cross-build/signing, legacy test-data migration or production-data operation occurred.

Design inputs: [client/backend R3](client-backend-architecture-design.md), [Mac LAN design](macos-lan-desktop-design.md), [Mac connection guide](macos-connection-guide.md).

## Scope and historical baseline

The user's October 3 instruction excludes InfiniCenter coordination and waiting for decision 0031. September 30 center checks and the proposal are historical records, not this round's prerequisite or an acceptance claim. Existing public integration contracts and compatibility checks remain intact; this work adds installed-client R3 paths without changing their retention or wire protocols.

At September 30, local SQLite/files, secure login, credential retrieval/device management and Mac packaging source were implemented; execution, mailbox sync and Host Broker were still pending. The continuation baseline passed full Go build/vet/tests, Desktop 36, WebChat 185 and credential 14 tests. The former desktop consumed backend history and used a local relay; that evidence does not qualify R3.

## Phase ledger

| Phase | Delivered Linux/shared scope | Remaining gate |
|---|---|---|
| P0 | Frozen byte/deadline/control allowlists; installed-client identity; durable admission/deduplication fences; memory-only Workflow repository, owned tmpfs workspace and encrypted delivery spool | Physical power-loss/full-disk and Mac hardware evidence; production cutover excluded |
| P1 | Main-owned ClientStore schema 4; explicit immutable context submission, input files, ExecutionClient, scoped local task/message/file/approval persistence, explicit approval continuation and cancellation/status reconciliation | Real Mac GUI and model/provider use on the delivered SHA |
| P2 | One-step initial/recovery connection credentials, reachable device settings, pinned LAN login and installation binding; durable result receipt/ACK; revisioned mailbox catalog/snapshot/delta/tombstones and offline cache | Real LAN/Mac Keychain and paired physical clients |
| P3 | Authenticated outbound WSS Broker/Host, common acquisition/embedded adapter roles, resource-side leases, actual per-conversation Electron pages, closed native operations and explicit unknown-write reconciliation | Mac embedded pages, supported real sites/popups and physical sleep/quit matrix |
| P4 | Verified input/output and mail-attachment copies, private atomic local saves, durable ACK only after save; expiry/restart/failure/cleanup and hash/path checks | Physical disk-full/power-loss and Mac download/permission checks |
| P5 | Integrated desktop main/preload/UI, isolated Linux/native verification, client-only Mac packaging source and exact user build commands; delivery through the R3 branch | User-run Mac compilation, M01–M12, architecture/signing/notarization/upgrades; production cutover |

Ordinary Web remains on its existing path and does not claim R3 local persistence. R3 desktop mounts local workbench, mail cache, device settings and embedded BrowserPanel. Saved requests remain `awaiting_runtime` until explicit submission; neither upgrade nor background polling submits that queue. Delivery polling queries the original request ID and retries only an already durable ACK. Unknown execution/write outcomes never cause automatic resend.

## Frozen storage, identity and capacity

ClientStore is main-owned SQLite WAL/FULL at `userData/client-r3`, schema 4, with a durable installation UUID and deployment/Owner/issued-client partitions. Schema 1/2/3 upgrades preserve local records and installation without replay; newer schemas fail closed. Logout preserves local database/files. The backend binds each issued client to one installation; another installation cannot take it over. Renderer receives typed, bounded IPC and opaque file/page IDs, never credentials, host grants or arbitrary paths.

| Parameter | Implemented bound |
|---|---|
| Local input/context | 16 KiB UTF-8/input; 32 user/assistant messages and 96 KiB/context; no trusted roles |
| Temporary execution content | 32 MiB/task, 256 MiB/Owner; conservative JSON/artifact admission accounting before retaining records; at most 8 active executions |
| Execution/input staging | 15-minute execution and upload deadline; at most 32 staged uploads; no deadline extension on retry |
| Pending approvals | At most 32 immutable rows per execution, 64 KiB/row; explicit decision bound to original request/approval/digest and existing 15-minute deadline |
| Result | 8 MiB total payload/files; absolute generation + 24-hour expiry; ACK deletes earlier |
| Local selected/saved attachment | 64 MiB/file; execution uploads stay within task limits; generated files stay within result limits |
| Durable execution fences | 100,000 deployment-lifetime records; capacity rejects admission instead of deleting fences |
| Browser grant/lease | 15-minute explicit host grant; 30-second lease and 10-second heartbeat; exact identity/runtime/epoch/page/generation binding |
| Mail sync | Entire encoded page at most 1 MiB; bounded pagination, revisions, epoch/cursor recovery and tombstones |
| Single-run schedule | Due within 24 hours, no recurrence/files; 30-second online lease renewed every 10 seconds; 8 registrations/Owner, 32 globally |

These are logical content-admission limits, not a claim that total process RSS is bounded to those values. Unsupported operations or unavailable required limits fail closed.

Each R3 execution receives only the explicit immutable client context. A fresh MemoryStore/ToolHub/policy/artifact composition shares the compiled Workflow and model router but excludes legacy history, traces, resident reminder/message routes, MCP and persisted approvals/memory. Tool-required files live in a deployment-scoped owned tmpfs directory, not the ordinary workspace; the Linux implementation requires tmpfs. Completion/cancel/expiry removes it; startup removes only owned stale directories for that deployment. Temporary input uploads remain in memory. Temporary input paths preserve unique displayed filenames so explicit filename references work; main queueing and backend admission reject duplicate names rather than overwriting or guessing a file. Generated delivery bundles use AES-256-GCM, private atomic files and an owned 0600 key; no plaintext output archive is written to the ordinary artifact store.

Durable execution control contains only owner/client/installation/request IDs, input digest, enumerated state, creation/deadline/generation/expiry times and result digest. No prompt, title, context, local conversation ID, DOM, document body or generated output is retained there. Browser fences/journal retain command digest, scoped identity/page/lease generations and enumerated outcome; arguments, DOM and outputs are excluded. Mail revisions are typed backend mail records, separate from non-mail control.

Admission persists the request fence before execution. Same ID/digest queries return the original state; drift rejects. Interrupted accepted/running work recovers as `unknown`, never re-executes. Result lookup checks absolute expiry before serving; GET is read-only. ACK requires matching sequence/digest and a durable client receipt, then persists `delivered` before removing spool content. Restart, lost ACK and cleanup do not delete deduplication fences. A private process lock excludes two execution spool writers.

## Protocol and client composition

All `/api/r3/*` requests require an issued authenticated device. Except installation registration, they also require the server-bound `X-SparkClaw-Installation`. HTTPS/WSS validate certificate chain, hostname and pin before credentials; no redirects, raw CDP or inbound client listener.

Desktop enrollment uses one opaque `sparkclaw-connect-v1...` value containing the public v2 pinned backend descriptor and one issued device token. The main process parses it, persists the descriptor separately from the encrypted token, and performs the same TLS checks before any bearer transmission. This local enrollment envelope does not change the Gateway bearer or R3 wire protocols.

| Transport | Implemented operation |
|---|---|
| `POST /api/r3/installations` | Closed schema/version/installation registration; confirms deployment/Owner/client |
| `PUT /api/r3/inputs/{request}/files/{file}` | Explicit bounded immutable upload with digest, size and hash verification |
| `POST /api/r3/executions` | Closed schema/context envelope plus `X-R3-Digest`; original durable request ID |
| `GET /api/r3/executions/{request}` and `/files/{file}` | Scoped status/result/file retrieval; expired content unavailable |
| `POST /api/r3/executions/{request}/approvals/{approval}` | Closed digest/approve-or-reject decision; same running request, immutable arguments, no content-derived authority |
| `POST /api/r3/executions/{request}/ack` and `/cancel` | Durable receipt or conservative cancellation; no fabricated completion |
| `GET /api/r3/mail/mailboxes` / `POST /api/r3/mail/{mailbox}/sync` | Authorized catalog and bounded cursor/limit sync; mutating journal work is POST |
| `GET /api/r3/mail/{mailbox}/messages/{mail}/attachments/{part}` | Backend-authoritative attachment with verified size/hash; explicit local copy |
| `POST /api/r3/hosts/grants`, `GET /api/r3/hosts/connect` | Separate explicit host grant and outbound authenticated WSS |
| `GET /api/r3/hosts/fences`, `POST /reconcile`, `POST /{host}/revoke` | Content-free unknown outcomes, explicit observed outcome and independent host revocation |
| `POST /api/r3/schedules/lease`, `POST /{request}/renew` or `/cancel` | In-memory future context tied to a renewable installed-client lease |

Optional native Gateway TLS uses paired absolute `gateway.tls_cert_file` / `gateway.tls_key_file` paths or `SPARKCLAW_GATEWAY_TLS_CERT_FILE` / `SPARKCLAW_GATEWAY_TLS_KEY_FILE`. Invalid pairs fail startup; the private key must be a regular owner-only file owned by the Gateway user, with no symlink. TLS is at least 1.2. Host admission requires real TLS reaching Gateway; a proxy must use HTTPS upstream. Forwarded headers cannot grant access. The unconfigured listener retains its existing HTTP behavior; TLS certificates and production ingress are not provisioned by source delivery.

The renderer cannot proxy these R3 transports. Desktop main owns ExecutionClient, ScheduleClient, MailSyncClient and BrowserHostAgent, aborts them on logout/suspend/unavailability, and resumes status reconciliation after connection recovery. Browser grants require explicit activation again after a lost channel. Authentication generations fence late same-identity responses before persistence/ACK.

Policy-required approvals stay inside the original temporary workflow. Status exposes bounded immutable public arguments and its execution deadline. Explicit approval executes the persisted tool call through the normal Runtime validation and resumes that run; rejection terminates the temporary workflow. An approved tool failure stops conservatively as `unknown`, without claiming completion or replaying a potentially partial external write. No approval is imported from client context or retained in the durable backend ledger. Main requires a fresh same-scope/generation running-status check before enabling an action; restored/offline approval snapshots are read-only. A lost decision response reconciles the original request and preserves an uncertain decision; it never submits another task. Duplicate matching decisions while active are idempotent, conflicting decisions reject, and restart/cancel/expiry never replay approvals.

ExecutionClient verifies raw payload and each file, durably stores result text/files/receipt together, then ACKs. Database failure, atomic-write failure, corruption or hash mismatch leaves the task unacknowledged. A lost ACK after restart retries the original receipt without creating another execution; receipt files are reverified first.

MailSyncStore has a separate main-owned scoped SQLite cache. It keeps the last complete cache during resnapshot staging; revision gaps/epoch changes reset staging, not backend mail. Backend collection remains authoritative and continues with no clients. Sync/cache actions do not start another collector. Suspend aborts in-flight work; late pages/cursor-reset replies after restart cannot advance or clear the committed cache, and late attachments cannot be saved. Attachment copies are verified local conversation files, while the originals remain backend mail records. No real mail was read/sent/deleted in this round.

A single-run scheduled definition persists locally before registration. Its future context is held only under an online lease; expiration/revocation discards it, and a missed due time is not caught up. Reconnect first queries the original request; a future definition may re-register the same ID only if unadmitted. Explicit “Run now” creates one new durable request. Resident backend mail collection is independent of client leases.

Client embedded browser commands operate the PageRegistry's actual conversation-owned WebContents. Selection changes presentation, not controller ownership; release/reacquire reuses the same view while generations fence stale commands. Acquisition uses a separate dedicated backend role through the common adapter, preserving existing Controller arguments/results; it is never an interactive client fallback. Native operations are a closed navigate/read/snapshot/click/fill/select/screenshot/wait set with snapshot refs and exact fences. Unknown writes from either backend fences or local journal require explicit observed-completed/observed-not-applied reconciliation; no automatic retry or inferred success.

## Acceptance record

`PARTIAL` below means the Linux/shared part passed, while a complete cross-platform/live/production case still lacks evidence. `NOT_RUN` means required evidence is absent. All Mac M01–M12 remain `NOT_RUN` until user evidence on the delivered SHA.

| Cases | Result | Evidence and remaining limit |
|---|---|---|
| A01/A02/A14 | PARTIAL | Fresh stores, scoped installation/device partitions, schema upgrades without replay and no legacy-history fallback pass; physical pair/cutover not performed |
| A03 | PASS (Linux/shared) | Real HTTP + standard mock Workflow uses supplied context only; a synthetic model endpoint drives the real document Workflow through immutable approval, actual editing/file delivery or explicit rejection; transient budgets, tmpfs cleanup and plaintext/control audits pass; no external model/site claim |
| A04/A13 | PARTIAL | Input/output/attachment hashing, atomic local save, database/disk-failure injection and actual installed-client HTTP attachment path pass; physical full-disk/power-loss pending |
| A05/A06 | PASS (Linux/shared) | Durable admission, dropped admission/ACK, restart, deduplication, sequence/digest drift, absolute 24-hour clock fixture, expired lookup/cleanup and capacity refusal pass; no 24-hour real-time soak |
| A07/A08 | PARTIAL | Actual Linux embedded DOM click/fill/read plus acquisition adapter semantics and TLS/WSS pass; Mac/supported external sites pending |
| A09/A10 | PARTIAL | Two actual views, 20 switches, same-view reacquire, late A reply during B, real write-response loss, unknown journal/fence, lease expiry/revocation/suspend tests pass; physical sleep/kill matrix pending |
| A11 | PASS (Linux/shared) | Durable snapshots/deltas/tombstones, gap/epoch recovery, offline cache, malformed/oversized/conflict/disk failure and 10 × 220 KiB pagination pass on synthetic mail |
| A12 | PASS (Linux/shared) | File-backend collector remains active without a client; online schedule expiry/admission/revocation, restart and no catch-up pass on synthetic work |
| A15/A17 | PARTIAL | Actual pinned TLS/WSS, wrong identity/certificate, trusted IPC, first unlock/install binding, revocation and credential-generation fencing pass; Mac Keychain/live LAN pending |
| A16 | NOT_RUN (Mac) | Client packaging source/audit and Linux negative build gate pass; Mac build/hardware/signing/upgrades and Web local persistence unqualified |
| A18–A21 | PARTIAL | Private UDS/PTY retrieval/recovery, reachable device route, issuance retries and revocation fixtures pass; no live production credentials or Mac evidence |

## Integrated Linux/shared verification

Environment: Linux ARM64, Node 26.2.0, npm 11.17.0, Go 1.25.5. Installed Electron 44.4.3 embeds Chromium 152.0.7977.130 and Node 24.21.0. All new data is synthetic and isolated; temporary qualification profiles/displays/TLS services are disposed after checks. The new native Host qualification is wired into desktop CI; this record claims local execution, not an unrun remote CI result.

| Check | Result |
|---|---|
| `go build ./...`, `go vet ./...`, `go test ./...` in `services/gateway` | PASS, final integrated suite |
| `go test -race ./cmd/sparkclaw ./internal/gateway ./internal/r3execution ./internal/r3browser ./internal/r3mail ./internal/emailmanagement` | PASS |
| `npm run test:desktop` | PASS, 87 tests |
| Installed Electron Node-mode store/capability/execution/approval/schedule/mail tests | PASS, 49 tests on Electron's actual Node/SQLite runtime |
| `npm run test:webchat`, `npm run build:desktop-ui` | PASS, 193 tests / 47 files; i18n 757 keys and production build |
| `npm run qualify:desktop-r3` | PASS: real Go TLS/WSS Broker and actual Electron embedded pages; click/fill, two conversations, 20 switches, reacquire and response-loss fence |
| `npm run qualify:desktop` | PASS, isolated legacy adapter regression; distinct from R3 Host evidence |
| `npm run test:credentials`, `npm run check:desktop-managed-scripts` | PASS, 14 credential tests and generated-asset check |
| Synthetic ASAR/Resources package audit and Linux Mac-build refusal | PASS inside Desktop tests; no Mac compilation |
| `npm audit --omit=dev` | PASS, zero runtime dependency vulnerabilities |
| `npm audit` including development dependencies | OPEN, 8 high findings in electron-builder development tooling; no forced downgrade |
| Exact bilingual CI/link check, `git diff --check` | PASS, 100 mirrored project Markdown files |

The local workbench's production bundle was inspected in disposable headed Chromium at 1440×1000 and 390×844: zero page errors/horizontal overflow. The first narrow-screen pass found an inherited full-height sidebar; the corrected rerun passed. Device/login fixtures retain September 30 evidence. A separate approval UI pass at the same sizes showed immutable arguments and explicit controls, with cached actions disabled and zero errors/overflow. Neither screenshot run qualifies Mac GUI. Vite's existing >500 KiB chunk warning remains non-fatal.

The first legacy adapter regression timed out after the popup; a fresh isolated rerun passed without changing deadlines. That first failure remains recorded. Development audit follows `electron-builder → app-builder-lib → @electron/get@3 → got → cacheable-request → http-cache-semantics@4.2.0`; [GHSA-ch52-4w7c-c8xp](https://github.com/advisories/GHSA-ch52-4w7c-c8xp) was updated October 2 and lists no patched version. This tooling audit remains open; runtime dependency audit passes. Keep the pinned packaging versions rather than claiming a forced downgrade is a fix.

Failure injection is bounded synthetic evidence, not a physical full-disk, abrupt power-loss, all-site compatibility or real-time expiry soak claim. The Host native harness exercises Broker and actual WebContents; installed-client HTTP route tests verify Gateway installation/grant/reconcile/approval shapes, and a production-listener test qualifies actual registered-client HTTPS/WSS, revocation, TLS1.1/untrusted-cert rejection and spoofed-forwarding-header denial. An integration mismatch between those request shapes was found and fixed before delivery. The first post-approval full Go check exposed a Store source-guard name collision; the ephemeral decision method was renamed without relaxing the guard, and the full rerun passed. No running production Gateway, collector, browser profile or client database was touched.

## Source delivery and next gate

Remote: `origin` (`https://github.com/Infinimesh-ai/SparkClaw.git`), branch `codex/sparkclaw-r3`. The final handoff supplies the exact pushed HEAD SHA; use that SHA for Mac build/results. [Mac commands and connection prerequisites](macos-connection-guide.md#3-synchronize-and-build-on-mac) describe native ARM64/x64 packaging, separate device credentials and a configured verified LAN HTTPS entry through native Gateway TLS or a proxy with HTTPS upstream.

P0–P5 source work and applicable Linux/shared checks are complete. Next is user-run Mac compilation and dedicated M01–M12 qualification on the delivered SHA. Signing/notarization, selected CPU architectures, Keychain, upgrades/rollback, physical sleep/quit/disk faults, supported-site compatibility, paired physical clients and production cutover remain open. No old test history is imported, no queued local request is replayed automatically, and no production deployment is implied.
