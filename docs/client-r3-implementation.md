# SparkClaw client R3 implementation and acceptance

> Language: English | [简体中文](../zh-cn/docs/client-r3-implementation.md)

Date: 2026-09-30. Delivery branch: `codex/sparkclaw-r3`. This records staged implementation, not a production cutover. Mac compilation, hardware, signing and notarization are performed by the user on the delivered commit. No old test history is migrated.

Design inputs: [client/backend R3](client-backend-architecture-design.md), [Mac LAN design](macos-lan-desktop-design.md), [Mac connection guide](macos-connection-guide.md).

## Reconciled baseline

Before editing, document-tool dependencies were prepared. Gateway build/vet/full tests, Desktop 17 tests, WebChat 169 tests and WebChat build passed on Linux ARM64, Node 26.2.0, npm 11.17.0, Go 1.25.5. The incumbent desktop auto-loaded a private local token, consumed backend sessions/history and ran a loopback adapter. Client issuance/revocation existed, but the settings navigation did not expose the paired-client component. None of this qualified R3.

InfiniCenter confirmed ProjectGroup-2; no unread SparkClaw inbox letters or proposed decisions affecting SparkClaw existed at entry. Accepted JingSi Runtime v1 and App-CLI Lifecycle/Host guarantees were reviewed. Proposed decision 0031 and review requests to JingSi and InfiniCenter record the retention/receiver issue. Existing contracts and ledgers remain intact. This proposal requires counterpart review; it is not accepted by this implementation.

## Phase ledger

| Phase | Delivered scope | Gate and remaining work |
|---|---|---|
| P0 | Data/identity separation, fresh local schema, local context limits, and external retention proposal | Partial: 0031 is proposed; backend temporary-content/control whitelist and remote execution envelopes still require implementation and acceptance |
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

Final test counts, Git SHA and runnable handoff commands are appended when integration is validated. Source delivery, Mac build, hardware acceptance and production switching remain separate states.
