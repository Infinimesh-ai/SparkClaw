# macOS desktop design R3: LAN client and embedded browser control

> Language: English | [简体中文](../zh-cn/docs/macos-lan-desktop-design.md)

Date: 2026-09-30. Revision: R3. Status: product boundary confirmed; local-data separation, LAN control and physical-Mac qualification are pending. The authoritative cross-platform target is [Client and backend architecture R3](client-backend-architecture-design.md). This revision supersedes both the original R2.1 and the intervening R2.2; neither a code implementation nor a deployment is claimed.

**Mac presents business results, persists its own non-mail user data, and executes authorized browser commands only in its embedded browser. The Linux backend performs business processing and stores authoritative mail.** The same rule applies to a Linux client, including when it shares a machine with the backend.

## 1. Reassessment of R2.1 and R2.2

| Earlier assumption | R3 decision | Consequence |
|---|---|---|
| R2.1 displays Linux screenshots over WSS | Superseded | Present Mac's actual embedded page; no frame publisher or live remote viewport |
| R2.2 shares backend conversations/messages/tasks | Superseded by explicit user clarification | Each client has its own local database/history/files; only mail synchronizes |
| No database or browser execution code on Mac | Superseded | ClientStore, Browser Host Agent, Bridge and managed page assets belong in the client |
| Business core stays on Linux | Retained | Model/routing/workflow/approval verification remain backend responsibilities |
| One adapter controls real Linux and Mac browsers | Retained with explicit roles | Backend acquisition and client embedded hosts share control semantics, not profiles/data |
| An interactive task may select any device or standalone browser surface | Replaced | Client browser activity is bound to that client's embedded browser |
| Mac downloads become shared backend artifacts | Replaced | Downloads and generated files persist locally; mail attachments remain backend mail records |
| Desktop inherently means loopback-only | Replaced | LAN is supported by the target; loopback is a colocated transport optimization |
| Same URL/metadata makes two pages fully synchronous | Not a guarantee | No cross-client pixel, DOM, login or conversation synchronization |

The project is not ready for a “Mac build only” release. Backend history/context, file delivery, client persistence, browser routing and connection identity all need changes before hardware qualification.

## 2. Runtime and package boundaries

| Component | Mac client | Linux backend |
|---|---|---|
| Workbench and local state | React UI; main-process SQLite/files, outbox and mail cache | Business API and transient execution context |
| Business processing | Submit intent and display results; no duplicated policies | Models, routing, workflows, validation and admitted scheduling |
| Browser commands | Host Agent operates actual embedded WebContents | Controller/Playwright orchestration through BrowserHostAdapter |
| Browser storage | Client-local profile, downloads and task artifacts | Dedicated acquisition profile; backend mail storage |
| Connectivity | HTTPS business/mail client and outbound WSS host channel | Authenticated entrance and browser-host Broker |
| App-CLI | Required verified host/page assets only; no second Registry/Executor | Existing public Registry/Executor boundaries and grants |

The package allowlist includes Electron, UI, ClientStore/migrations, constrained IPC/Host Agent, Bridge and verified managed page assets. Exclude Linux Gateway binaries, models, service managers, server database dumps, server credentials and App-CLI Registry/Executor. “No backend on Mac” does not prohibit a local user database.

Separate client startup from backend-service startup in the current main process. Linux UDS paths, systemd, /run/user assumptions and private backend secrets cannot enter Mac composition. Keep the shared React UI and existing embedded-page execution core where portable.

## 3. LAN connection and authorization

Use the same logical API for colocated Linux and remote Mac. Mac connects to the backend's reachable LAN HTTPS origin, validates certificate/deployment identity, and receives an installation-specific client credential. Its own 127.0.0.1 is not the Linux host. SSH is only a development tool, not product connectivity.

The existing strict v1 loopback descriptor remains unchanged until a separately versioned loader is implemented. The new loader needs schema version, server origin, deployment identity, trust reference, client identity/credential reference and browser-host permission reference. These are design requirements, not a configuration file accepted by current code.

A workbench credential permits scoped business requests; a separate host grant permits browser commands. Mac main opens the outbound WSS control channel and registers host identity, runtime generation and capabilities. No inbound Mac listener or raw remote CDP endpoint is exposed. Bind the host to the authenticated Owner/client installation; reconnect creates a new connection epoch and revokes old leases.

Application API names and the host envelope are pending schema freeze. Do not present proposed endpoints as working commands or send the current local relay protocol unchanged across the LAN.

## 4. Unified browser adapter on Mac

The backend owns a BrowserHostAdapter with capability discovery, resource acquisition, lease renewal/release, command dispatch, results/events, cancellation and reconciliation. Its local transport reaches a backend browser; its remote transport reaches a client Host Agent. Both use the same validated command model and preserve Controller/Bridge and App-CLI public boundaries.

For a Mac client task, the path is:

```text
Mac local conversation -> backend workflow / authorization
  -> BrowserHostAdapter -> authenticated outbound channel
  -> Mac main Host Agent -> owned embedded WebContents
  -> command result / event -> backend business processing
  -> durable result delivery to Mac ClientStore
```

Navigate, read, click, type and task-scoped browser operations act on the page visible in the Mac workbench. Website requests originate from Mac and use its own session. Screenshots, when explicitly requested as tool evidence, are bounded local task artifacts/transient backend inputs, never a continuous presentation stream.

The remote envelope binds request_id, command sequence, deadline, parameter digest, client/task/conversation identity, host/runtime/connection generation, lease and page generation. ACK means receipt, not completion. A host-side command journal distinguishes not-started, completed and unknown outcomes; the client stores non-mail journal payloads locally. Never replay a write merely because the channel disconnected.

Host-side checks restrict execution to granted task pages and approved operations. Do not expose arbitrary shell, filesystem, unmanaged script injection, debugger targets or a personal browser. Preserve the validated managed-script pipeline. Bind popup/new-target creation to the same task and render permitted children in embedded views; an unsupported site flow reports a capability failure instead of escaping to an automated external window.

Cancellation has a separate control budget. Expiry, revocation and generation changes fence commands at the resource before acknowledging cleanup. In-flight website writes may still finish; retain their uncertain outcome and reconcile. Mac loss does not silently migrate an interactive browser task to Linux.

## 5. Embedded presentation by local conversation

Maintain a mapping from local conversation/task to host/page and generation. Switching A/B selects each conversation's actual embedded page; background events remain bound to their task. Empty conversations show an empty state. Returning to A can reveal its existing page, without recreating or rerunning it.

All client automation remains embedded. Do not introduce a standalone task browser window, mirror Linux frames, or open a second page to imitate the original. If multiple workbench windows refer to one task, move/focus the actual view or show its location; never attach competing controllers. Hiding the panel is distinct from closing its page or canceling a task.

Backend command results and local display refer to the same Mac page. Another client has its own conversations and page mappings; even the same URL is an independent page. The backend dedicated browser is an acquisition/operations resource, not the page the Mac displays.

## 6. Mail, files and task delivery

Mail caches synchronize from the backend using revisions/cursors/tombstones. Mac-created mail changes go through backend policy and conflict checks. The resident backend collector continues when Mac quits. Mac does not start a second collector or copy backend mailbox credentials.

Local conversations can reference authorized mail records. Conversation history and reports derived from those records remain local, while original mail attachments remain in the backend mail store. A downloaded attachment copy belongs to Mac. General downloads and generated files do not enter a shared backend artifact archive.

Persist request IDs/context before submitting, and commit received events/results/files before acknowledging delivery. Backend transient input/output storage is bounded and cleaned after acknowledgement/expiry. A full disk, missing client or expired output must be visible as a delivery problem. Business completion and successful local saving are separate states. See R3's [execution and delivery rules](client-backend-architecture-design.md#5-execution-and-reliable-delivery).

Selecting another client's conversation is not a recovery mechanism, because it is not automatically replicated there. Client backup/import and old server-history assignment are separate migration work, with execution IDs/fences preserved and no automatic outbox replay.

## 7. Current implementation and work order

| Phase | Deliverable | Gate |
|---|---|---|
| P0 | Freeze data/retention, local schema, transport scopes and migration manifest | Cross-project changes require accepted decisions; no implicit ledger deletion |
| P1 | ClientStore and bounded client-context submission | Local conversation/task/file persistence; no backend history fallback |
| P2 | LAN identity, event delivery/ACK and mailbox synchronization | Certificate/Owner/client isolation; lost responses reconcile |
| P3 | Broker and unified local/remote browser adapters | Mac and Linux embedded commands, resource-side leases and no external automation |
| P4 | Files/history migration and platform composition | Verified local saves; old data assigned before cleanup; package allowlist |
| P5 | Mac hardware/build/signing/upgrade qualification | GUI/browser failure matrix and scoped cutover evidence |

Current code entry points are [desktop composition](../apps/desktop/src/main/main.mjs), [loopback loader](../apps/desktop/src/main/local-backend.mjs), [local adapter](../apps/desktop/src/browser/adapter-server.mjs), [browser protocol](../apps/desktop/src/browser/protocol.mjs), [page presentation](../apps/desktop/src/main/presentation.mjs), [BrowserPanel](../apps/webchat/src/desktop/BrowserPanel.tsx), and [backend Store contracts](../services/gateway/internal/store/store.go). Their current local/shared-state behavior is not evidence that R3 already works.

The accepted App-CLI durable ledger and JingSi Runtime v1 recovery guarantees still apply to existing integrations. The [cross-project gate](client-backend-architecture-design.md#9-cross-project-boundaries-and-unresolved-gates) must be resolved before changing their retention/protocol semantics. Internal adapter naming cannot bypass those contracts.

## 8. Mac-specific acceptance and remaining questions

| ID | Required Mac evidence |
|---|---|
| M01 | LAN HTTPS works independently of SSH; wrong certificate/deployment/client is rejected |
| M02 | Mac persists conversations/messages/history/files across restart; Linux does not receive a copy |
| M03 | Both clients synchronize backend mail, including deletions and cursor recovery, while non-mail history remains separate |
| M04 | Same backend adapter navigates/types/reads real Mac and Linux embedded pages; backend acquisition is a separate granted role |
| M05 | Browser commands cannot open standalone/system-browser automation, copy profiles or substitute a backend page |
| M06 | A/B conversation switching, popups and page restart maintain exact page/task ownership and reject late events |
| M07 | Disconnect, lid close, sleep, quit, revocation and lost results fence commands and reconcile unknown writes |
| M08 | Disk full, dropped delivery ACK, backend restart and expired spool do not report unsaved files as delivered or duplicate work |
| M09 | GUI launch, Chinese input, Retina/multiple displays, permissions, microphone if enabled, and supported sites are tested on Mac |
| M10 | Package audit, selected CPU architectures, signing/notarization, upgrade/local-schema rollback and data preservation pass |

All M cases remain pending. Product ownership no longer needs clarification. Engineering still needs frozen retention/control schemas, legacy-data destinations, external-contract compatibility and physical Mac evidence. These gates make this a client/backend architecture change, not a frame-viewer upgrade.

Practical access and qualification: [Mac connection guide](macos-connection-guide.md).
