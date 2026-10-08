# Web And Desktop Shared Local Backend Design

> Language: English | [简体中文](../zh-cn/docs/local-shared-backend-design.md)
>
> Date: 2026-09-22 (Asia/Shanghai). Status: implementation complete and isolated qualification passed. Production deployment, real-device microphone/HTTPS acceptance, and release cutover remain separately authorized activities; this document is not a production deployment claim.

> Historical design: current workbench behavior and clean-start paths are defined by the [architecture](architecture.md), [implementation ledger](workbench-convergence-implementation.md) and [release guide](workbench-release.md). The dated rationale and qualification evidence below remain historical.

> R3 target update (2026-09-30): this document is the implemented shared-state baseline,
> not the future data-ownership rule. R3 also requires SparkClaw credential unlock on
> each client's first login; preprovisioned automatic login below is not the new rule.
> [Client/backend R3](client-backend-architecture-design.md)
> replaces shared conversations/history/files with client-local storage, retains backend
> mail synchronization, and supports LAN desktop connections. Cutover is pending, with no legacy test-data migration;
> the behavior and evidence below apply to the current implementation only.

## 1. Goal And Scope

One host runs one SparkClaw Gateway, PostgreSQL database, and business file store. The ordinary browser workbench and Electron workbench access that backend and share the same Owner's conversations, messages, tasks, email, memory, approvals, and configuration. Desktop startup connects to the host service automatically, without server selection or pairing.

This phase qualifies both local desktop and LAN Web access. Desktop uses `http://127.0.0.1:18790`; LAN browsers use `http://<backend-host-LAN-IP>:18790`, with the configured port when different. Both reach one listener. Desktop accepts loopback addresses only. Remote desktop server lists, discovery, QR pairing, offline write queues, and multi-host synchronization are outside this phase. Local/remote model deployment choices are independent of client placement; both support this design.

Desktop authenticates automatically using an installation-provisioned dedicated local credential. Web retains independent client-token login. All business requests to the shared entrance remain authenticated. This revision supersedes the earlier local-only Web assumption and anonymous `/api/local/session` enrollment proposal; network location does not grant Owner authority. Other integrations retain their credential, approval, and authorization semantics.

## 2. Implemented Result

| Area | Implemented result |
|---|---|
| Shared UI/API | `apps/webchat` remains the browser and desktop UI; environment-specific transport stays below one typed API |
| Single entrance | Web and desktop HTTP/SSE/files plus speech use the configured workbench origin; host `18789` stays private and `18795` is absent |
| Persistence | Both clients use the same Gateway repositories, PostgreSQL, workspace, artifact, and trace stores; desktop creates no business database |
| Credentials | Deployment provisions a restricted desktop Client; authenticated Owner settings issue independent one-time Web Client tokens |
| Identity/boundaries | Both clients verify deployment/Owner/client identity; Gateway derives Owner from the bearer and filters sessions, traces, memories, approvals, feedback, tools, files, and clients server-side |
| Refresh | Authenticated bounded workbench SSE plus five-second/focus polling refreshes lists and visible state; initial/reconnect/overflow paths resync |
| Concurrency/lifecycle | Gateway centrally rejects concurrent same-conversation submissions, accepted work detaches from the request, and desktop revalidates on resume/retry |
| Startup | Empty state remains empty until explicit creation; stale responses are generation-guarded and deleted active sessions select a valid/empty view |

The implementation is backed by repository tests, isolated PostgreSQL/LAN dual-client qualification, and rebuilt packaged desktop artifacts. Target-host release acceptance remains distinct from implementation evidence.

## 3. Single Entrance And Deployment Ownership

| Caller / protocol | Entrance | Destination |
|---|---|---|
| Local browser page | `http://127.0.0.1:<port>/` | WebChat assets |
| LAN browser page | `http://<backend-host-LAN-IP>:<port>/` | The same WebChat assets |
| Web JSON / SSE / files | Same-origin `/api/...` | Nginx → the same Gateway |
| Electron JSON / SSE / files | `sparkclaw-app://workbench/desktop-gateway/...` | Main process → `http://127.0.0.1:<port>/...` → the same Gateway |
| Speech WebSocket in either client | Page origin for Web, loopback origin for desktop; both use `/api/speech/realtime` | The same Nginx WebSocket proxy → Gateway |
| Gateway business state | Existing Store repositories | Existing PostgreSQL, workspace, artifact, and trace storage |

`18790` is the default workbench port; `18789` remains internal. The local workbench deployment removes the `18795` listener, publication, and probes without introducing another pairing port. Desktop does not expose a business HTTP server.

Preserve the LAN-accessible entrance, using the existing default IPv4 publication on `0.0.0.0:<port>` so loopback and LAN IPs reach one service. Do not narrow Web to loopback for desktop convenience. Validate existing custom bindings for both desktop loopback and LAN accessibility; report incompatible configurations. IPv6 follows existing deployment configuration and is not required for this phase.

The shared entrance forwards credentials already supplied by the caller; it neither injects a general Owner token nor offers anonymous enrollment. Desktop uses a local credential file. Browser requests use the same token validation whether local or over LAN. This document changes no live deployment.

The deployment/service manager owns Gateway, database, and background-service lifecycle. Desktop only checks and connects to that service. Closing a window, quitting desktop, or closing a browser tab must not stop Gateway, create a database, or select another Store backend.

### 3.1 Local Connection Description

Deployment should generate `data/runtime/local-workbench.json`; the installed launcher passes its absolute path to desktop main. This is deployment output, not another manually maintained configuration. It contains only its version, origin, and deployment identity:

```json
{
  "schema_version": 1,
  "origin": "http://127.0.0.1:18790",
  "deployment_id": "<persistent-installation-id>"
}
```

No token, database password, or credential key belongs here. The port comes from existing `SPARKCLAW_WEBCHAT_PORT`. The deployment ID persists across ordinary restarts; importing another installation or restoring a full database requires identity reconciliation. A port number alone does not identify a backend. Store the dedicated credential separately in `data/runtime/desktop-client.json`; the launcher supplies its controlled absolute path to main, as described below. Neither file belongs in Nginx assets or download directories.

Main accepts only a canonical HTTP loopback origin. Reject domain names, non-loopback IPs, URL credentials, paths, queries, fragments, and redirects elsewhere. A missing or mismatched description produces a connection configuration error, without scanning ports, falling back to `18789`, or initializing fresh data.

Development and isolated qualification may explicitly inject a test description. That override must not introduce remote connection support into the product.

## 4. Authentication Without 18795

Desktop reads its preprovisioned dedicated Client credential; Web uses independent Client credentials. Gateway validates bearer tokens on the same entrance. Do not introduce anonymous `/api/local/session` enrollment or issue credentials based on loopback IP, Docker subnet, Host, forwarding headers, or an Electron marker.

### 4.1 Desktop Provisioning And Automatic Connection

1. The local deployment/installation flow prepares a dedicated random token, stable client ID, deployment ID, and Owner binding for the selected desktop OS user. Use a `0600` credential file and `0700` parent directory, outside application bundles and shared directories. Containers receive required provisioning inputs through restricted read-only mounts.
2. Gateway's local startup assembly registers the Client with a conditional repository command, storing only the token hash. HTTP cannot invoke this initialization path, and no anonymous client-creation route is enabled. Deployment scripts do not write directly to the database or run another persistent Gateway.
3. After confirmed persistence and identity agreement, the launcher passes absolute description/credential paths to desktop main. Main checks file type, ownership, and permissions, reads the credential, and calls the authenticated identity route over loopback.
4. The authenticated `GET /api/workbench/identity` returns `deployment_id`, `owner_id`, and `client_id` without a token. Both clients verify identity before loading data and subscribing.
5. Main injects the bearer only into authorized workbench requests; preload returns connection status only. Restart reuses the Client without issuance or pairing calls.

The first implementation slice adds a separate internal Client registration command instead of reusing pairing claims or assuming a SaveClient API exists. Replaying the same ID/hash/Owner is idempotent. Revoked clients and identity conflicts are rejected, and startup reconciles according to existing durability/unknown-outcome rules. The database record and credential file are separate persistence steps: recover using stable candidate identity, never replace an already registered candidate with a silently generated token after interruption.

Existing installations missing the file use the local configuration flow. Desktop displays incomplete setup without network self-enrollment. Reinstallation, ordinary service restart, and retry must not reactivate revoked credentials. Report desktop registration failures separately and prevent desktop connection; do not stop Gateway from serving otherwise healthy clients.

### 4.2 Web Login And New Credential Issuance

Retain existing token entry and browser persistence for both local and LAN browsers. Existing valid tokens continue working. A first LAN visit may load the page, but protected data requires login. Namespace storage by service origin/deployment; never copy the dedicated desktop token into browser storage.

For new Web clients, authenticated Owner-management `POST /api/clients` is invoked from desktop settings or another authorized management entrance. It derives Owner/actor from authentication, requires an idempotency key, reconciles unknown outcomes against a stable candidate, and displays the credential once for the target browser's token form. No pairing code or port 18795 is involved.

Issuance responses use `Cache-Control: no-store`; never log plaintext tokens, put them in URLs, automatically copy them to the clipboard, or transmit them to other devices. Reconcile unknown issuance outcomes by candidate identity rather than blindly creating more credentials. Each Client remains independently revocable; revoking desktop does not revoke Web.

Verify matching deployment/Owner in both clients, preserving the current default Owner where applicable. Explain an old token's Owner mismatch without switching, merging, or showing a fresh empty workspace. Align session endpoints' current default/query/body Owner selection with authenticated principal ownership; frontend checks alone are insufficient.

### 4.3 Request Boundary And Failures

- Nginx preserves LAN and loopback access and forwards caller Authorization. It injects neither general Owner credentials nor location proofs. Spoofed forwarding headers, Host, or local markers cannot bypass authentication; Docker address translation does not affect this rule.
- Main injects credentials only toward the descriptor's loopback origin, rejects cross-origin redirects, and validates the trusted workbench caller. Never expose credential files through HTTP/workspace downloads; personal browsing pages cannot invoke the privileged proxy or read main credentials.
- Web uses same-origin business requests. Allowed Host/Origin values cover configured LAN and local addresses. Origin/CORS checks constrain browser sources, not Owner authorization. Other integrations retain independent source/authentication rules.
- Files, SSE, and speech tickets share the authentication boundary. Web derives its WS URL from the page origin, never the visiting device's `127.0.0.1`; only desktop uses the backend host's loopback address.
- Missing/unsafe desktop credential files or revoked tokens stop automatic connection and prompt local configuration repair. Invalid Web tokens show login. Repair/reissuance requires existing Owner authority or local deployment administration; ordinary network calls/retries cannot recreate authority.
- Timeouts/`503` preserve identity. Recovery retries reads/subscriptions, not message submissions, email sends, or approvals.

This phase trusts the selected desktop OS user and main process; permission-restricted files do not isolate malware controlling that user. Decouple business authentication from the old `pairing_required` switch rather than disabling it and clearing the API token. Update browser-control dependencies and product configuration validation consistently.

## 5. Shared Data Boundary

| Data | Authority | Client behavior |
|---|---|---|
| Conversations, messages, runs, tasks, approvals, memory, schedules, notifications | Gateway / PostgreSQL | Same records for the same Owner and query filters |
| Email, saved email drafts, classification, viewing receipts | Gateway / PostgreSQL | Share persisted state and preserve existing version/send semantics |
| Integrations, model configuration, service credentials | Backend configuration / credential repositories | Share public projections; secrets remain on the backend |
| Documents, uploads, artifacts, traces | Backend files and metadata | Use the same resource APIs; clients do not mount backend data directories |
| Unsent chat input and temporary attachment selections | Current frontend | Preserve local input during refresh; unsaved drafts do not synchronize in this phase |
| Selected conversation, language, microphone, sidebar, window size | Each client | Independent device/view preferences |
| Browser login storage, Electron website cookies, personal downloads/permissions | Each browser / Electron Session | No copying into ordinary browsers or business-database synchronization |

Do not migrate PostgreSQL, import old `gateway-state.json`, or add a desktop business database. Both clients must show existing product data. Diagnose deployment, Owner, filters, and entrance first when views disagree; do not fix a routing error by importing empty data.

Ordinary browsers retain existing Web capabilities without Electron capability. Browser execution remains the same-host Gateway → Controller → Electron Adapter chain over the existing Unix Socket. No network device channel is needed. Fully exiting Electron makes its browser capability unavailable while backend data and work that does not depend on the browser remain available.

## 6. Cross-Client Refresh

A confirmed persistent API response defines the write outcome. SSE asks clients to refresh. Add authenticated `GET /api/workbench/events/stream`, publishing invalidation notifications for the backend-selected Owner across conversation lists/content, tasks, approvals, email, schedules, notifications, and shared settings.

For one Gateway, use bounded in-process notifications without adding a durable UI event table or distributed message bus. Events contain `epoch`, a process-local increasing `sequence`, resource category, and necessary resource ID. These are notification markers, not business row versions or transaction receipts.

- Publish only after confirmed repository persistence. Include background email, schedule, approval, and model-execution paths rather than HTTP handlers alone.
- Send `resync` on every initial connection/reconnection. Clients reload conversation lists and visible data. Gateway restart changes epoch; gaps, overflow, or slow consumers also trigger rereads. Offline event-by-event replay is not promised.
- Subscribe before loading snapshots. Mark invalidations received during a snapshot as dirty and reread after the outstanding request, preventing a startup race.
- Share a `fetch`-based authenticated SSE client. Web supplies its bearer; desktop main injects it. Reuse existing stream parsing and remove the current token-dependent absence of active-session EventSource connections.
- Coalesce and debounce by resource. Avoid full `refreshGlobal()` per token delta. Refresh conversation lists independently, and invalidate the relevant email, notification-read, or settings queries.
- Reconcile bounded visible-view queries every five seconds in the foreground and immediately on focus, visibility recovery, and network recovery. Consolidate existing timers rather than stacking full polling loops.
- Guard responses by frontend generation and selected-resource identity. Ignore stale responses, preserve unsent local input, and select an empty/valid view when another client deletes the active conversation.

Acceptance target: a persisted change triggers refresh in the other visible client within two seconds on a healthy host. With events disabled, views converge within one five-second polling interval plus query latency. Other clients receive persisted state/messages; mirroring another client's token-by-token output is not required in this phase.

## 7. Concurrency, Retries, And Startup

Both clients may remain online and select different conversations. Preserve existing conditional writes, version checks, and approval state machines. Audit affected interfaces before implementation; do not assume every write already supports an idempotency key. Existing expected-version conflicts trigger rereads instead of overwriting newer values with stale caches.

Gateway centrally admits/queues or explicitly rejects concurrent submissions to one conversation. Independent frontend idle flags are insufficient. After a disconnected message, task, or external send, reconcile using existing operation/run IDs. If no reconciliation identity exists, show an unconfirmed outcome and do not automatically resubmit. Verify HTTP-stream and admitted-task lifetime independently; shared data alone does not prove that closing a page leaves execution unaffected.

Startup reads the conversation list without creating a default conversation. An empty store shows a create action; only an explicit user action creates one. Opening both clients at once therefore does not create two unintended empty conversations.

## 8. Connection State And Lifecycle

| State / event | Behavior |
|---|---|
| Desktop first launch | Read local descriptor/credential → verify identity → load existing data |
| Local / LAN Web first visit | Load data with a valid token, otherwise show existing login; no anonymous enrollment |
| Gateway stopped | Desktop reports local service unavailable; Web reports connection failure; offer retry without a temporary Gateway |
| PostgreSQL / credential vault unready | Display dependency failure, retain cache/identity, no memory/file fallback |
| Description / deployment / Owner mismatch | Stop loading and explain identity conflict; no automatic rebinding |
| Gateway restart / resume from sleep | Revalidate identity, subscribe, and reread; do not resubmit writes |
| Desktop window closed | Preserve existing hide behavior; Gateway remains independent |
| Electron fully exits | Gateway remains; embedded-browser work follows existing interruption/recovery semantics |
| Desktop uninstalled | Preserve backend PostgreSQL, credential keys, workspace, artifacts, and traces |

Desktop states include local service connected, reconnecting, incomplete local setup, and invalid authentication. Web retains login and service-connection status. Ordinary flows do not expose pairing codes, server lists, database parameters, or `18795`.

## 9. Implementation Locations And Stages

| Location | Implemented work |
|---|---|
| Compose, Nginx template, deployment/autostart scripts | Preserve LAN access, remove 18795, generate descriptor/restricted credential, reconcile file/config persistence |
| Gateway config, middleware, Client/credential assembly | Internal issuance, installation registration, authenticated identity/Owner issuance routes, authentication separation and Owner alignment |
| Gateway services / workbench event handler | Post-persistence invalidations, bounded subscriptions, resync |
| Desktop main, preload, connection module | Local descriptor, credential custody, unified HTTP/SSE/file target, speech WS, remove pairing proxy |
| WebChat API, App, shared hooks | Retain Web token login, Owner issuance UI, authenticated subscriptions, list/view refresh, read-only startup and connection state |
| Deployment docs and desktop/artifact qualification | Update the model and requalify all four transports in actual packages |

The completed implementation followed these stages in order:

1. **Same instance:** descriptor, local desktop credential, independent Web token login, Owner binding, and both clients reading existing PostgreSQL. Qualification includes LAN Web and explicitly has no `18795` listener.
2. **Shared experience:** events, polling reconciliation, concurrency/disconnection behavior, list updates, and empty-store startup.
3. **Product integration:** deployment/docs updates, rebuilt desktop artifacts, isolated dual-client acceptance. Previous pairing tests do not qualify the new design.

All three implementation stages are complete in the working tree. Production rollout remains excluded: the rebuilt candidate still requires target-host GPU/DPI/IME/audio, HTTPS LAN microphone, provider-login, install/update/uninstall, icon, and explicit cutover acceptance.

### 9.1 Qualification Evidence

- `npm run qualify:local-shared-backend` starts disposable PostgreSQL, binds the test Gateway to all IPv4 interfaces, uses the desktop Client over loopback and an independent Web Client from a Docker network through the host's non-loopback address, and verifies identity, empty startup, bidirectional CRUD, sub-two-second SSE, identical file bytes, and forged-source rejection.
- `npm run qualify:desktop-artifacts` independently extracts and launches the rebuilt ARM64 AppImage and DEB with disposable user data and a restricted descriptor/credential, proving packaged workbench loading, authenticated HTTP/SSE proxying, speech WebSocket routing, and bounded desktop IPC.
- Gateway, Store, WebChat, Desktop, deployment-profile, launcher, and Compose suites cover provisioning replay/revocation/unknown outcomes, Owner isolation, queue overflow/resync, request detachment, custom ports, no `18795` Compose listener, and credential non-disclosure.

## 10. Acceptance Matrix

Use isolated PostgreSQL, disposable Electron user data, test pages, and two independent clients. Web must also connect from a second test network environment to the host's non-loopback entrance, not just localhost. Do not manipulate the user's active desktop display, real mail accounts, or production data. Web speech capture retains browser secure-context requirements: qualify LAN microphone use through an HTTPS entrance satisfying them, and record HTTP LAN data access separately from microphone permission.

| Scenario | Required evidence |
|---|---|
| No listener on 18795 and no host 18789 | Desktop reads its local credential automatically; LAN Web logs in with a separate token; both use 18790 without pairing requests |
| Unauthenticated LAN / forged local source | Login page loads; protected/issuance APIs reject access; Host/forwarding-header spoofing grants no authority |
| Interrupted/replayed/revoked desktop provisioning | Reconcile the same candidate without duplicate clients, credential replacement, or restart undoing revocation |
| New Web credential issuance | Authenticated Owner can issue; anonymous callers cannot; same Owner binding without desktop token reuse |
| Custom workbench port | Deployment, Web, desktop, and speech use one configuration without hardcoded fallback |
| Initial launch/restart | Same deployment/Owner, separately recorded client IDs, session reuse, no startup-created conversations |
| Bidirectional CRUD | Conversation create/rename/delete and supported shared resources update the other client within targets |
| Files/speech | Authorized downloads have identical bytes; HTTP, SSE, blob, and WS reach one backend |
| Background email/schedule/notification writes | Events arrive without frontend write requests; notification read state converges |
| Concurrent edits/out-of-order responses | Existing version checks hold; stale responses do not replace newer views; no duplicate sends |
| SSE loss, overflow, Gateway restart | Resync/polling recovers database truth; epoch sequence is not treated as a durable cursor |
| Unknown write result during disconnect | Reconcile or report uncertainty; no automatic resend/approval/backend fallback |
| Desktop non-loopback target, cross-origin redirects, HTTP credential-file access | Reject without credential leakage while legitimate LAN Web targets continue working |
| Revocation, Owner/deployment mismatch | Stop the old connection; no background identity replacement |
| Client close/uninstall | Data remains; ordinary backend services continue; browser dependency state is explicit |

## 11. Compatibility And Transition

Reuse the current PostgreSQL volume, credential key, Owner, file directories, and LAN Web accessibility. Verify identities and existing data before provisioning desktop credentials. There is no file-to-PostgreSQL migration or database merge. Persist client issuance through repository durability and unknown-outcome reconciliation rules.

Existing tokens may be validated and reused. Do not replace a session automatically when it belongs to a different local Owner. Remove/update `18795`, desktop pairing proxy, workbench pairing fields, and associated product probes with the connection change. Preserve pairing business APIs still needed by other integrations; this design does not redistribute their authority.

InfiniCenter's SparkClaw–JingSi Runtime v1 and SparkClaw–IMMS evidence v1/v2 contracts were checked. This local workbench and UI-notification design does not change those contracts, central schemas, or other projects' release schedules; no other project follow-up is required. Changes crossing those boundaries require the cluster decision process first.
