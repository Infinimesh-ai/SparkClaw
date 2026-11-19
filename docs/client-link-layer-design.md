# Client Link Layer Design

> Language: English | [简体中文](../zh-cn/docs/client-link-layer-design.md)

Date: 2026-11-19. Status: implemented (section 9). The code survey is based on `dfc49969`.

This document covers only how the desktop, phone and watch clients communicate with the backend. Domains, device binding and access-credential issuance are in [Self-hosted console design](self-hosted-console-design.md). The two documents are maintained separately.

This document supersedes the boundary in sections 1 and 2 of [Client and backend architecture R3](client-backend-architecture-design.md) that conversations belong to each client locally.

## 0. Premises

The user confirmed these premises on 2026-11-19.

1. **Production has one connection method.** The official console has domains. A domain is the isolation boundary for devices and trust. The three clients bind to a domain with a SparkClaw access credential issued by the console (scanned or pasted), the backend joins the same domain, and the clients then talk to the backend through the link layer. This is the only way to join.
2. **The console only binds devices, pairs them and issues credentials.** Messaging, conversation sync, approvals and all other business traffic use the link layer (LAN or ISCP Relay) and never pass through the console.
3. **The self-hosted environment is the development environment of that same method**, not a second method. The self-hosted console and Relay only place the console's roles on our own hosts. Any behavior that holds only in the self-hosted environment is a defect.
4. **The self-hosted console must match the official console in form, not interface by interface.** Adapting to the real console's interfaces is deferred until the official chain is connected. Client and backend code that talks to the console must therefore sit in one adapter (3.11). The three clients do not use ITES sign-in for now.
5. **Every feature is open by default and limited only by what the device itself can do.** For example, a phone has no built-in browser, so it gets no browser-automation entry (3.9).
6. **The backend stores all conversation records, including attachments.**
7. **Old conversation records are not migrated and may be deleted.** Existing local conversations on desktop and phone are cleared at cutover; existing host WebChat conversations in the backend database may be deleted as well.
8. **No client works while the backend is offline.** When the backend is unreachable, any operation from any client fails at once with a clear error. Nothing is queued locally and no client executes on the backend's behalf (3.12).
9. **One backend serves one person.** Multi-user use is out of scope for now.
10. **Control of the SparkX built-in browser stays as it is.** It is a capability of the desktop client itself and is outside this refactor (3.12).

## 1. Conclusions

1. What was dropped was not the ISCP Bridge architecture but its credential source at the time. The Bridge code is still in the repository and maintained. Phones left it because the reference Trust Root could only issue one-hour `text` Grants, could not renew them, and needed each phone written into `peers[]` by hand. All three are credential problems.
2. The Bridge's process boundary is what is missing today: ISCP terminates in a separate process, and the Gateway knows only a narrow, transport-neutral interface. The node-issuer path moved ISCP into the Gateway process, and the coupling follows from that.
3. The design is: **the Bridge's process boundary with the console's domain-model credentials.** Devices and the backend join the same domain, and the console issues and renews their Grants. A backend-authoritative conversation sync layer sits on top.
4. The console's Grants last days and support automatic renewal. "One hour, no renewal" was a limit of the reference implementation, not of the console model. The node issuer is a separate issuance scheme built to work around that limit. It differs from the console model and is retired at the end.

## 2. Problems today

| Problem | Location | Consequence |
|---|---|---|
| ISCP terminates inside the Gateway process | `cmd/sparkclaw/workbench_iscp.go`, `internal/gateway/workbench_iscp_lifecycle.go` | A Gateway upgrade interrupts every remote connection; ISCP faults and business faults affect each other |
| The operation table is HTTP-shaped | `iscpworkbench/operations.json`: 159 operations, 92 with an `http` field | The Gateway receives an ISCP request and then builds an `http.Request` to call the original handler, so two interfaces are stacked |
| Business adapters are written against transport types | `internal/gateway/workbench_iscp_*.go`, about 11,000 lines | Each new feature changes the operation table, the negotiation and the adapter |
| Transport negotiation is used as business authorization | `execution_authorization.go` decides tool and file permissions from `NegotiatedOperations` | Permissions depend on what the client version negotiated |
| The operation table is compared exactly | `compatibleV2Operations` in `negotiation.go` | A new operation stops older clients from connecting unless it is added as a separate extension flag |
| Clients branch on transport everywhere | Desktop main process `main.mjs` and `desktop-auth.mjs`; about 15 `iscp` branches in webchat `LocalWorkbench.tsx` | LAN and remote are two code paths with different behavior |
| Two conversation models coexist | Host WebChat conversations live in the Gateway `sessions`/`messages` tables; desktop and phone conversations live on the device and send history with each `execution.submit` | The clients cannot share conversations; the remote interface has no operation to list conversations or read messages |
| The phone client library depends on backend business code | `mobile/sparkclawmobile` compiles `internal/toolhub` | The library cannot build for 32-bit, so the watch cannot use it (3.8) |

## 3. Target structure

```
 Desktop          Phone            Watch
 LinkClient      LinkClient      LinkClient        ← one interface; path selection is internal
    │  │            │  │            │  │
    │  └── LAN ─────┼──┼────────────┼──┼──→ LAN connector (thin listener inside the Gateway)
    │               │               │
    └── ISCP ───────┴───────────────┴─→ Relay ─→ ISCP connector (separate process)
                                                        │ Unix socket, duplex frames
                                                        ▼
                                              ┌───────────────────────┐
                                              │ Gateway link entry    │  sees only Principal + frames
                                              ├───────────────────────┤
                                              │ sync │ runs │ approvals │ domains │
                                              └───────────────────────┘
```

Three terms:

- **Link contract** (working name `sparkclaw.link.v1`): a transport-neutral message definition. Clients, connectors and the Gateway depend only on it.
- **Connector**: terminates one transport, verifies the device identity and passes frames to the Gateway. There are two: the LAN connector and the ISCP connector.
- **Principal**: what a connector hands to the Gateway after verifying identity. It holds the owner, the device ID and the device class. Business code sees only this and never sees Grants, sessions or the Relay.

### 3.1 Link contract

The starting point is the existing Bridge `agent.*.v1` contract (`iscpbridge/contract.go`). It already has conversation listing, message send, cancel, cursor-based events with resume, approvals, operation status, idempotency keys and a capability manifest. It still needs paged history reads, a change cursor, device projections and duplex frames.

There are five frame kinds: `hello`, `request`, `response`, `event` and `cancel`. Each frame carries an `id` and a `type`.

At handshake both sides exchange protocol versions and capability sets and take the intersection. An unknown `type` returns `unsupported_capability` and the connection stays open. The operation table is no longer compared exactly.

Operations fall into three groups:

| Group | Content | Notes |
|---|---|---|
| Core | Sync, conversations, messages, run status, approvals | Used by all three clients; see section 4 |
| Business domains | Mail, medicine, settings, speech and so on | Unified as `domain.call` with a domain name and a method name; the Gateway calls the business function directly and no longer builds an `http.Request` |
| Device capabilities | The 11 current reverse operations, such as embedded browser control and McDonald's checkout | Behavior unchanged: a desktop with a built-in browser declares it at handshake and the backend calls back over the same connection; see 3.12 |

Large objects (attachments and generated files) use separate chunked operations and are not mixed into signal frames.

Clients exist in at least three languages (Go, Java/Kotlin and Swift). The contract must be written as a specification first and ship with a set of conformance test vectors.

### 3.2 Gateway side

A new package (working name `internal/link`) exposes two entry points:

- `Dispatch(ctx, principal, request) → response`
- `Open(ctx, principal) → duplex frame stream` (for event push and reverse calls)

Rules:

- Business code in `internal/gateway` no longer imports any `iscp*` package.
- Whether a feature is available is decided at the entry point by the rule in 3.9, not by transport negotiation.
- When a device is revoked, the Gateway tells the connector to drop that device's connections.

### 3.3 ISCP connector

A separate process, the successor of the Bridge:

- It reuses the existing multi-peer responder and Grant renewal code (`iscpworkbench/node_*.go`, `grant_lifecycle.go`) and the Bridge's Relay send and receive code.
- Credentials follow the console's domain model (3.11): the backend is a device in the domain, holds a Grant with each client, and renews it before expiry. The Bridge already has the beginnings of this code (`enroll_ticket.go`, `managed_lifecycle.go`, `grant_lifecycle_client.go`).
- The existing node-issuer path stays unchanged for installed clients during the migration and is retired in P4.
- It talks to the Gateway over a duplex frame stream on a Unix socket. The old Bridge used one HTTP request per call (`/api/bridge/v1/dispatch`), which cannot carry event push or reverse calls.
- The connector may only assert devices that exist and are valid in the Gateway's device directory, and the Gateway checks every time.

The connector does not restart when the Gateway is upgraded, so remote connections see only a short burst of request retries. A connector or Relay fault does not take the Gateway down.

### 3.4 LAN connector

It stays inside the Gateway process as a thin listener over `Dispatch`/`Open`: pinned TLS (as `pinned-https.mjs` does today) plus WebSocket.

The device identity is the same device key and the same device record as on the ISCP path. The Gateway verifies the LAN handshake against its locally stored device directory without contacting the console, so the LAN still works without internet access.

### 3.5 Trust configuration

A client stores only a console address and its trust anchor. In development this is the self-hosted console; in production it is the official console. The domain, the bound backend and its public key, the Grant and the Relay node list all come from the console when the device joins the domain.

Switching environments changes only this address and the console adapter (3.11). The link contract and the rest of the client code stay the same.

### 3.6 Clients

Each client has a `LinkClient` with the same interface: connect, request, subscribe and fetch large objects. Path selection happens inside it: LAN and ISCP are probed together, LAN is preferred, ISCP is the fallback, and after a switch the client resumes from its cursor.

- **Desktop**: `LinkClient` replaces the branching in `DesktopAuth` and `ISCPTransport` in the main process. The interface stays the same; only the data source under existing components changes.
- **Phone**: the gomobile library moves from passing raw operation names through to a typed sync interface.
- **Watch**: also uses the link layer (LAN and ISCP Relay), in the pull profile; see 3.7 and 3.8.

**The host WebChat stays.** The web client on the backend host keeps using the Gateway's local interface directly, with an unchanged interface. It reads and writes the same conversation tables, so it follows the data model changes in section 4 and sees the same conversations as the three clients.

The Go implementation of `LinkClient` is a standalone module that depends only on the ISCP packages and the link contract. It does not import Gateway internal packages.

### 3.7 Two access profiles

The clients run under very different conditions, so the contract defines two access profiles. Core operations must work in both.

| Profile | Connection | Timeliness | Used by |
|---|---|---|---|
| Duplex | One long-lived connection with event push and reverse calls | Streaming output arrives as it is produced | Desktop; phone in the foreground |
| Pull | Request and response only, no long-lived connection | Polls `run.status` for the current snapshot | Watch; phone during a short background wake |

Constraint: **events are only an accelerator; the cursor is the truth.** Any state that events can deliver must also be retrievable with `sync.changes`, `session.messages` and `run.status`.

### 3.8 Current state of the clients

Findings from the local repositories and a real device on 2026-11-19.

| Client | Platform | How it reaches SparkClaw today | Local storage | Effect on this design |
|---|---|---|---|---|
| Desktop SparkX | Electron plus a Go helper process | LAN HTTPS, or ISCP through the helper | SQLite; the device is the source of truth for conversations | Becomes a cache; the largest piece of work |
| Phone JingSi Android | Java 11, minimum Android 9 | Two paths: the gomobile library over the node issuer; a LAN mode that is cleartext HTTP, unauthenticated, with a hand-typed IP and one implicit conversation | The remote path keeps `chat.json` and sends up to 31 history messages per turn; the LAN path keeps only a cursor | The LAN mode is replaced by the authenticated LAN connector; local conversations become a cache |
| Watch JingSi Watch | OPPO Watch, Kotlin, minimum Android 8.1, pure JVM | Only demo mode works. The console interfaces defined in the code have no callers and real pairing is not implemented | Room: offline event queue, approvals, status. No conversation cache | New pairing and a new link module, in the pull profile |
| iOS JingSi | Swift | Has ISCP connection code and a console API client; not examined in depth | Not examined | A fourth client implementation |

The watch is a 32-bit device. The real device (OPPO OWW261) reports `armeabi-v7a`, has only a 32-bit runtime, runs Android 11 and has about 1.8 GB of memory.

The current gomobile library has only 64-bit builds. A 32-bit compile fails in exactly one place: a constant overflow in `internal/toolhub/mcd_purchase.go`. The ISCP repository's `pkg/...` compiles for 32-bit ARM. So the standalone `LinkClient` module (3.6) can produce both 64-bit and 32-bit builds, and the watch and the phone can share one implementation.

Not verified: only a `linux/arm` compile check was done. No gomobile `android/arm` build was made and nothing was run on the watch.

### 3.9 Feature availability rule

Every feature is open to all three clients by default. A feature is closed only where the device itself cannot do it. There is no per-device permission table, and the console does not manage feature availability.

- **A device declares its own conditions.** At handshake the client reports what it has, for example a built-in browser surface, the ability to show and pick attachments, a microphone, and its screen class.
- **A feature declares what it needs.** Each operation in the contract states the device conditions it requires. Most require none.
- **The backend decides at the entry point.** When a condition is not met it returns `unsupported_capability`. The LAN and ISCP paths give the same result.
- **Interface.** The handshake reply carries the set of features available to this device. A client that has the relevant screen keeps the existing entry and disables the unavailable action; a client that never had that screen gains no new entry.

| Feature | Desktop | Phone | Watch | Reason for the limit |
|---|---|---|---|---|
| Conversations, messages, approvals | Yes | Yes | Yes | None |
| Attachment upload and download | Yes | Yes | No, file names only | Screen and storage |
| Business domains such as mail and medicine | Yes | Yes | Yes, trimmed to the screen | None |
| Settings | Yes | Yes | Yes | None |
| Browser-automation surface | Yes | No | No | No built-in browser |

### 3.10 Attachments

Attachments and generated files are stored on the backend with the conversation. They are part of a message, and the message holds only a reference.

- Clients download on demand and cache locally. Lists and message pages carry only the file name, size and type.
- Uploads are chunked, carry a content digest and can resume. A message refers to attachments that have finished uploading.
- Quotas and retention are required: a per-file limit, a total limit, and deletion together with the conversation. The values are set during implementation.

### 3.11 Interface to the console

The console side is described in [Self-hosted console design](self-hosted-console-design.md). The link layer depends only on the following.

| What the console provides | Who uses it | Purpose |
|---|---|---|
| Identity in the domain: domain ID and this device's ID | Devices, backend | State who it is at handshake |
| The backend's device ID and public key | Devices | Confirm the peer is the bound backend, on both LAN and ISCP |
| A short-lived Grant between the device and the backend | Devices, backend | Establish sessions on the ISCP path; renewed before expiry |
| The Relay node list | Devices, backend | Where the ISCP path goes |
| The domain's device directory and revocation state | Backend | The backend keeps a copy and accepts or rejects devices from it |

**Console adapter.** The client and the backend each have one module that talks to the console. It exposes four operations: join the domain, obtain and renew a Grant, sync the device directory (backend only), and leave the domain. The rest of the link layer does not know the console's address or interfaces. In development the adapter targets the self-hosted console; connecting the real console replaces only this module.

**The link layer does not depend on the console being online.** After a device has joined the domain, daily communication needs only an unexpired Grant. A short console outage affects only new devices joining and Grant renewal, not established connections. The LAN path does not need the console at all.

### 3.12 Backend offline and device capabilities

**No client works while the backend is offline.**

- When the backend is unreachable, sending a message, deciding an approval and business-domain operations all return a clear error at once, and the interface says the backend is offline.
- There is no offline send queue. Only a request that was sent but received no reply is retried with its idempotency key, to learn its outcome.
- Cached conversations can still be opened and read.
- A client never executes business logic in place of the backend.

**Control of the SparkX built-in browser stays as it is.** The 11 reverse operations the desktop provides today (`browser.controller.*`, McDonald's checkout and so on) remain: the desktop declares at handshake that it has a built-in browser, and the backend calls back through the link layer. The refactor changes only how they are carried, from the ISCP operation table to link-layer reverse-call frames. Behavior and interface do not change. The phone and the watch do not declare this capability.

## 4. Conversation sync

The backend is the single source of truth and all three clients are caches. The approach is to promote the conversation interface the host WebChat already has into the only interface, and add paging and a change cursor. The current desktop and phone model, in which history travels with every request, is retired.

### 4.1 Data model changes

| Table | Added | Purpose |
|---|---|---|
| `sessions` | `revision` (monotonic), `last_seq`, `preview`, `archived`, `deleted_at` | Change cursor, list display, archive synced across clients, deletion tombstone |
| `messages` | `seq` (monotonic within a conversation), `client_message_id` | Paging by sequence number, idempotent send |

There is no separate change-log table. "Changes since a cursor" are the conversations whose `revision` is greater than the cursor, ordered by `revision`. State size depends only on the number of conversations and does not grow with time.

### 4.2 Core operations

| Operation | Purpose |
|---|---|
| `sync.changes(cursor, limit)` | Returns the headers of changed conversations: title, `last_seq`, preview, archive and deletion flags. The first call after reconnecting |
| `session.messages(session, before_seq, limit)` | Pages backward through history. Opening a conversation fetches the latest page; scrolling up fetches more |
| `message.send(session, client_message_id, content)` | Sends a message. The same idempotency key returns the original result |
| `run.subscribe(session)` | Subscribes to output in progress: the current snapshot first, then increments |
| `run.status` / `run.cancel` | Query and cancel |
| `approval.list` / `approval.resolve` | Approvals |

### 4.3 Behavior

- **Cache.** A client stores conversation headers and the message pages it has read. It shows the cache at start and then aligns with `sync.changes`.
- **Lazy loading.** Messages are fetched only when a conversation is opened, and only the latest page.
- **Streaming output.** The preview of a run in progress exists only in memory and has no `seq`. On completion it becomes one final message. After a disconnect, subscribing again returns the current snapshot.
- **Several clients online.** All subscribers of a conversation receive the same events. Only one message runs in a conversation at a time; a send from a second device gets a conflict.
- **Backend unreachable.** A send fails at once and is not queued (3.12).
- **Device projection.** The device record carries the device class. For the watch the backend returns trimmed plain text and fewer items, and gives only file names for attachments.

## 5. Where the 159 existing operations go

| Existing operations | Destination |
|---|---|
| `execution.*`, `events.*`, `approvals.*`, `phone.*` | Folded into the core group. `phone.snapshot.get` and `phone.activity.list` remain as projections |
| Mail, medicine, settings, speech and so on | Moved to `domain.call` one domain at a time; each move deletes one `workbench_iscp_<domain>.go` |
| `clients.*` device management | Joining and revoking devices moves to the console; this group is retired |
| Reverse operations such as `browser.controller.*` and `mcd.checkout` | Behavior unchanged; carried by link-layer reverse-call frames |
| The v1 text operation table | Deleted last |

## 6. Phases

| Phase | Content | Effect on installed clients |
|---|---|---|
| P0 | Contract finalized; Gateway `link` package; conversation table migration, paging and cursor; LAN connector; console adapter | None. The old paths stay as they are |
| P1 | Standalone `LinkClient` Go module (64-bit and 32-bit builds); ISCP connector process, through which the backend joins the domain, running beside the in-Gateway responder | None |
| P2a | Phone moves to the new link layer. The phone validates the sync model first | Phone needs a new version; its old local conversations are cleared |
| P2b | Watch gains real pairing and the link module (pull profile). LAN first, then remote | Watch needs a new version |
| P3 | Desktop: `LinkClient`, cache and sync engine, unchanged interface | Desktop needs a new version; its old local conversations are cleared |
| P4 | Business domains move one by one; the in-Gateway ISCP responder, the operation table and `workbench_iscp_*` are deleted; the node issuer is retired | Old client versions stop working |

The self-hosted console's schedule is in its own document. P1 integration can start once its phase C1 is complete.

The old Bridge `agent.*.v1` entry is still used by JingSi and by LocalMind's MCP access. This design leaves them alone; the new connector is a different binary that shares only the Relay send and receive code.

## 7. Risks and costs

- **One more process** to supervise and deploy.
- **The connector is a trust point.** Compromising it allows impersonating any valid device. It runs on the same host and in the same trust domain as the Gateway, and socket permissions plus the device-directory check narrow the exposure.
- **Most of the work is in P3 and P4.** The desktop's local store becomes a cache, and about 11,000 lines of adapter code move.
- **Cutover deletes old local conversations on each client.** This cannot be undone and must be stated in the release notes.
- **Backend storage grows.** Conversation text and attachments are kept on the backend, so quotas and retention are needed.

## 8. Out of scope

- The design and build of the console side.
- Push wake-up. In development, clients sync only while the app is open.
- Several people using one backend.
- How JingSi and LocalMind connect.

## 9. Implementation (2026-11-19)

| Phase | State |
|---|---|
| P0 | Done. Contract in [Link contract](link-contract.md) with test vectors; Gateway `internal/link`; migration `0023_conversation_sync`; LAN connector; console adapter `services/link/consoleapi` |
| P1 | Done. `services/link` Go module: `LinkClient`, the ISCP connector `link-connector`, the desktop helper `link-helper` and the gomobile binding `linkmobile` (arm64, armeabi-v7a, x86_64) |
| P2a | Done. JingSi-Andriod on the link layer, verified on the emulator |
| P2b | Done. JingSi-Watch pairs with a console code and uses the pull profile, verified on a real 32-bit watch. Its commit stays local because that repository is read-only for this account |
| P3 | Done. SparkX main process on `LinkClient` with a ClientStore cache (schema 17); the windows and interactions are unchanged |
| P4 | Done. Business domains go through `domain.call`; the in-Gateway ISCP responder, its lifecycle, the v2 transport, `clients.*`, `authorizations.*` and the plaintext JingSi LAN entry are deleted; the node issuer and the control forwarder are stopped and their code deleted |

**Deployment**

| Host | Item |
|---|---|
| TEST1 | Relay ISCP `59057fe`; self-hosted console (see the console document) |
| work2 | Gateway image `sparkclaw-gateway:link-<commit>` with schema 23; the `sparkclaw-link-connector` container next to it; state in `/home/ubuntu/.local/share/sparkclaw/link`; LAN connector on port 18792 |
| Mac | SparkX built from the same commit |

**Differences from the design**
- Deletions are kept as rows in `session_tombstones`. In PostgreSQL triggers maintain `revision`, `seq` and `preview`; the memory and file stores maintain the same fields in Go.
- The Relay registers console devices through an operator bind (ISCP `59057fe`). The node-authority file is unchanged.
- The device presents its Grant in a plaintext envelope `sparkclaw.link.grant.v1` before the session hello, so the connector needs no Grant sync from the console.
- The pull profile has no subscriptions or pushes. The watch keeps the connection only while the app is in the foreground.
- `domain.call` runs the existing domain adapters for the link principal: the method is the operation name and the arguments are the former v2 envelope. The operation table remains as the registry of those methods. The 92 HTTP-shaped methods were not rewritten as direct functions.
- Device keys are kept in private state files (`link-device.json`), not in the OS keyring.
- Device management (`clients.*`) answers 410 `devices_are_managed_on_the_console`; the console is the only place to join and revoke devices.
- The desktop's isolated qualification launcher still uses the former ISCP helper `iscp-workbench` with its local fixtures. Ordinary profiles use only the link layer.
