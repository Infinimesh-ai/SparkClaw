# SparkClaw client R3 implementation and acceptance

> Language: English | [简体中文](../zh-cn/docs/client-r3-implementation.md)

Date: 2026-09-30. Delivery branch: `codex/sparkclaw-r3`. This records staged implementation, not a production cutover. Mac compilation, hardware, signing and notarization are performed by the user on the delivered commit. No old test history is migrated.

Design inputs: [client/backend R3](client-backend-architecture-design.md), [Mac LAN design](macos-lan-desktop-design.md), [Mac connection guide](macos-connection-guide.md).

## Current continuation scope (2026-10-03)

The user clarified that InfiniCenter is not connected and its coordination process is out of scope for this round. Continue the remaining R3 implementation from the SparkClaw repository without waiting for InfiniCenter integration or decision 0031 review. The September 30 center checks, proposal and requests below remain historical records; this does not mark 0031 accepted, and it is no longer a prerequisite for this round.

The user will build on Mac after the remaining Linux/shared implementation and applicable verification are complete. Do not compile, cross-build or sign Mac here. Mac hardware/Keychain/signing/upgrade checks follow source completion and are performed by the user. Existing interface compatibility, identity isolation, deduplication and unknown-effect reconciliation remain technical verification requirements in this repository. Do not migrate legacy test data or equate implementation with production cutover.

## Reconciled baseline

Before editing, document-tool dependencies were prepared. Gateway build/vet/full tests, Desktop 17 tests, WebChat 169 tests and WebChat build passed on Linux ARM64, Node 26.2.0, npm 11.17.0, Go 1.25.5. The incumbent desktop auto-loaded a private local token, consumed backend sessions/history and ran a loopback adapter. Client issuance/revocation existed, but the settings navigation did not expose the paired-client component. None of this qualified R3.

InfiniCenter confirmed ProjectGroup-2; no unread SparkClaw inbox letters or proposed decisions affecting SparkClaw existed at entry. Accepted JingSi Runtime v1 and App-CLI Lifecycle/Host guarantees were reviewed. Proposed decision 0031 and review requests to JingSi and InfiniCenter record the retention/receiver issue. Existing contracts and ledgers remain intact. This proposal requires counterpart review; it is not accepted by this implementation.

## Phase ledger

| Phase | Delivered scope | Gate and remaining work |
|---|---|---|
| P0 | Data/identity separation, fresh local schema, local context limits, and historical external retention proposal | Partial: backend temporary-content/control whitelist and remote execution envelopes still require implementation and verification; 0031 is not a prerequisite for this round |
| P1 | Main-owned SQLite ClientStore, local conversations/files, atomic input + immutable bounded context + request-ID transaction; scoped IPC and local desktop workbench | Partial: network ExecutionClient/context submission and Workflow isolation are not activated; saved input is explicitly not submitted |
| P2 | Independent initial/recovery credential tooling, reachable device settings, desktop user-input unlock, secure credential reuse and secret-free versioned LAN descriptor | Partial: Linux/shared verification recorded below; install-identity server binding, execution/event ACK and mailbox revision/cursor/tombstone synchronization remain pending |
| P3 | R3 production disables the old local adapter instead of dispatching to an unauthorized substitute | Not delivered: authenticated WSS Broker, common BrowserHostAdapter, remote leases, per-conversation pages and write reconciliation |
| P4 | Local selected-file save/export with manifest hash and atomic writes | Partial: authenticated backend output/file delivery, expiry, disk/ACK/network fault matrix and backend cleanup audit |
| P5 | Mac client-only packaging source/configuration and exact user build commands | Source only; Mac build, GUI, hardware, signing/notarization, upgrades and production cutover remain pending |

Legacy ordinary Web continues on its existing path and does not claim R3 local persistence. The desktop R3 surface never mounts legacy shared-history hooks. It offers local saving and device management; runtime-dependent tasks, mailbox synchronization and browser automation remain unavailable. A locally saved request has `awaiting_runtime`, not server accepted/completed. Future execution must require an explicit submission; upgrading must not automatically replay this queue.

## Frozen local boundaries and candidate backend budgets

ClientStore schema 1 uses SQLite WAL/FULL with a new installation UUID; deployment/Owner/issued client ID partitions local records. It lives in `userData/client-r3` and never imports backend data. Logout preserves the database and files. Newer local schemas are rejected without deleting data.

Local input is at most 16 KiB UTF-8, context at most 32 messages and 96 KiB, each context message at most 16 KiB. Context roles are user/assistant; they do not carry trusted authorization. Selected local files are at most 64 MiB, written through private staging with fsync/atomic rename and SHA-256 manifest verification. Renderer cannot choose backend or arbitrary local file paths, scope identities or deliver trusted execution events.

Candidate backend values in 0031: 15-minute execution budget, 32 MiB content/task, 256 MiB/Owner, 8 MiB/result, absolute result expiry at generation + 24 hours, 30-second Host lease/10-second heartbeat. These are not active configuration. Durable content-free fences retain request/client IDs, canonical digest, state, authorization/lease generations and times for deployment lifetime; capacity exhaustion must reject new admission rather than delete fences. No prompt/title/DOM/body/output belongs in control records. External contracts are not shortened by these candidates.

## Acceptance record

`PARTIAL` means scoped Linux/shared evidence, not completion of the full acceptance ID. `NOT_RUN` means required evidence is absent. Mac M01–M12 all remain `NOT_RUN` until user evidence for the same SHA.

| Cases | Result | Evidence and limit |
|---|---|---|
| A01/A02/A14 | PARTIAL | Fresh ClientStore and device/Owner/backend partitions; restart persists local data and desktop avoids legacy session routes. Shared mailbox and live Mac/Linux pair not verified |
| A03 | NOT_RUN | New backend context/Workflow/temporary store and residual-content audit not implemented |
| A04/A13 | PARTIAL | Local transaction and file failure/hash/path tests. Real disk-full and backend output/attachment delivery not verified |
| A05/A06 | NOT_RUN | Storage receipt helper replay/gap tests do not qualify live submit/ACK/restart or 24-hour backend cleanup |
| A07–A10 | NOT_RUN | Broker/Host/browser command chain not enabled; old adapter disabled for R3 production |
| A11/A12 | NOT_RUN | No new mailbox sync/online task lease verification; incumbent resident mailbox service preserved |
| A15/A17 | PARTIAL | Versioned connection, main-owned login and scoped secure vault tested on Linux/shared code; Mac Keychain/certificate/live LAN not verified |
| A16 | NOT_RUN | Mac build/hardware/signing/upgrades and ordinary Web local persistence not qualified |
| A18–A21 | PARTIAL | Local retrieval/recovery and device navigation/error/retry/revocation fixtures; no live production credentials or Mac secure-store evidence |

## Integrated Linux/shared verification

Integration was verified on Linux ARM64 with Node 26.2.0/npm 11.17.0/Go 1.25.5. The installed Electron 44.4.3 uses Chromium 152.0.7977.130 and Node 24.21.0 internally; ClientStore was also exercised with that exact Electron binary in Node mode.

| Check | Result |
|---|---|
| `go build ./...`, `go vet ./...`, `go test ./...` from `services/gateway` | PASS, full suite after integration |
| `go test -race ./cmd/sparkclaw ./internal/gateway ./internal/config` | PASS |
| `npm run test:desktop` | PASS, 36 tests |
| Electron Node-mode ClientStore/capability tests | PASS, 7 tests; fresh DB/restart, scope, disk-failure rollback, paths/hash and trusted IPC |
| `npm run test:webchat`, `npm run build:desktop-ui` | PASS, 185 tests / 46 files; i18n 757 keys and production build |
| Synthetic token/origin injected into desktop UI build then scanned | PASS, neither canary in built assets |
| `npm run test:credentials` | PASS, 14 tests including actual private UDS, PTY one-time display and absolute slow-response deadline |
| Provisioning/deploy/Compose/autostart/dotenv/install Python suites | PASS, 2 + 5 + 11 + 10 + 7 + 6 + 2 tests |
| Local and remote Compose config expansion | PASS with synthetic private environment; no live deployment |
| `npm run check:desktop-managed-scripts` | PASS |
| `npm run qualify:desktop` | PASS on isolated Xvfb/disposable profile; regression of the older adapter, not R3 Broker evidence |
| Synthetic ASAR/Mac Resources package hook | PASS; a private connection file is rejected, no Mac build |
| Dependency audit, exact bilingual CI/link check, `git diff --check` | PASS; zero npm audit vulnerabilities; 100 mirrored project Markdown files |

Pinned HTTPS tests cover actual TLS hostname/chain/pin validation, wrong Owner, accepted identity and main-owned credential persistence. Production Mac requires v2; v1 strict loopback remains Linux/qualification only. Secure vault tests use fixtures; Mac Keychain itself is NOT_RUN.

Device/login synthetic UI fixtures were inspected at 1440 and 390 pixels. The new local workbench has React integration checks and layout/source review; its separate live browser preview was NOT_RUN because the available in-app browser blocked the local fixture. This does not count as Mac GUI acceptance. The Vite bundle-size warning remains non-fatal.

## Source delivery and next gate

Delivery remote: `origin` (`https://github.com/Infinimesh-ai/SparkClaw.git`), branch `codex/sparkclaw-r3`. The handoff reports the final pushed HEAD SHA; record that SHA with Mac results. [Mac commands and connection prerequisites](macos-connection-guide.md#3-synchronize-and-build-on-mac) include Node/npm, architecture selection, actual initial/recovery tools and required pre-existing LAN HTTPS. No production deployment, real credential use, mail operation, legacy-data migration, Mac compile/cross-build/signing or cleanup of active ledgers was performed.

Under the user's October 3 scope, this round does not wait for the center or 0031 review. The next gate is implementing and verifying P0's backend temporary/control field allowlist, frozen budgets and execution envelopes in this repository; validate existing public-interface compatibility in code and tests. The new local receipt helpers remain on the delivery branch until a production ExecutionClient calls them; do not merge unused helpers to main. Future submission must be explicit and must not replay this tranche's local `awaiting_runtime` queue automatically.

Finish P0, then P1 context execution, P2 delivery/mail sync, P3 Broker/host grants, P4 output/cleanup fault matrix and P5 client source/build preparation in order. After the remaining Linux/shared implementation and applicable verification pass, hand off the exact SHA for user-run Mac builds and dedicated qualification. Failed or absent evidence keeps its acceptance gate open.
