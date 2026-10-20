# ISCP authentication recovery and duplex implementation

[简体中文](../zh-cn/docs/iscp-parallel-implementation.md)

Date: 2026-10-19. This record covers the two local implementation worktrees. It is not a production deployment or a claim that the baseline binaries can read the new state. Local implementation and integration qualification are complete. The final implementation candidates are SparkClaw `62358707d7f11202df4df44ab9420a77f305ee15` and ISCP `22da89f329a88bde8e12219c359e82063064798b`.

## Scope and interface freeze

The user selected a joint interface review and freeze before parallel implementation of the [authentication and operation recovery design](iscp-authentication-operation-recovery-design.md) and [duplex transport design](iscp-duplex-websocket-design.md). Both worktrees start from SparkClaw `cf9b81dec39b8872efc43650080bb8facaa58511`, with the same design snapshot `76ed11bd94ab4bf4b7f63697a96dbf36d4fce053`. The independent ISCP service source starts from `10fe37b3bd02a56b4f9d6cb8dc0333d7eec5356a`.

The branches are `codex/iscp-auth-operation-recovery` and `codex/iscp-duplex-websocket`. The former is also the integration candidate. The original checkout and its design drafts are retained.

InfiniCenter decisions 0038 and 0041 accept the shared interfaces for the self-managed fork. Contract revision `2026-10-19.2` incorporates the accepted 0042 correction to the Base64URL key-ID pattern; the original snapshot remains archived. This does not change JingSi's permanent `not_started` fence or the IMMS contract. Withdrawn decision 0040 remains withdrawn. ISCP services and SparkClaw consumers build independently and interoperate through the frozen wire protocol.

## Implemented behavior

Credential recovery uses the original durable operation intent and fresh, correlated HTTPS authentication attempts. The issuer and Relay keep separate credential generations and authorization revisions. A parent operation retains its stable child key; response loss, process replacement and lost installation ACK reconcile those original records. A result that expires before installation is explicitly closed as expired before a linked successor is created.

Credential consumers install the Grant and credential lease durably before acknowledging installation. Automatic and manual callers share one recovery flight, including across process ownership. Both cached and recovered leases are rechecked after the installation ACK callback, so a concurrent invalidation cannot return an obsolete credential. A verified issuer terminal is recorded in its own authority domain and cannot be compared with, or overwrite, the Relay revision. Revocation and actual HTTP Write/Flush disclosure share the authoritative database fence.

For the explicitly pinned local Relay profile, an authenticated rejection of the current Access token triggers one compare-and-force refresh through the original valid Refresh token. The server rotates both tokens and the credential generation in one guarded transaction. Initial rejection reconnects; a live socket reauthenticates without replacing its owner. A delayed failure hint retains the credential observed at its first wire write and cannot invalidate a newer lease. Missing generation or authority history is rejected.

The duplex client separates its reader, writer and application callback worker. Sending obtains a receipt future instead of waiting for a round trip while holding the encryption lock. Relay readiness precedes end-to-end Hello/Ready. Authentication renewal on the existing socket preserves the end-to-end session; actual socket replacement invalidates the old session before a new handshake.

Before a potentially uncertain write, the client durably records the original message identity, ciphertext, digest, expiry, history and enqueue order. Recovery queries that original tuple. A still-unknown earlier message blocks later resubmissions in the same session. Replaying identical ciphertext requires the original active session and deadline. An accepted transport receipt is not a business result, and Relay `absent` never authorizes a business retry. Cancelled writes are `NotSent` only while the writer can still atomically skip them; otherwise the durable tuple is retained as `Unknown`.

The Relay stores admission and queued message data in one PostgreSQL transaction, retains immutable receipts and enforces owner, authority, credential and writer generations. Per-session admission order prevents later replies from passing their predecessors. The retired envelope POST and receive-only connect paths reject old clients. Database guards reject the actual baseline writer after activation; old snapshots and incomplete history do not become new admission authority.

Desktop recovery journals retain original business operation IDs and encrypted results. Browser control actions, mutations and speech recovery use their original identities after reconnection. Renderer action tokens contain opaque IDs and digests, not credentials or operation payloads. The existing task authority and business deadlines remain in force.

Recording insertion and its application marker commit in one local SQLite transaction. Schema 12 adds an opaque recording ID, scope, conversation and draft revision; it stores no audio or extra transcript copy. A crash after saving the draft but before acknowledging the recording can only repeat the acknowledgement. Clearing or sending the draft does not erase the marker. Same-process disk failures retry saving the original draft without inserting the transcript again.

The UI keeps the original components, menus, layout and styles. Unavailable operations retain their entries and are disabled. No persistent transport diagnostics are added to product screens.

The October 19 implementation coalesced manual and automatic browser setup into one live owner. The October 20 follow-up removes the manual browser-authorization button and renderer grant API: verified ISCP and HTTPS LAN desktop connections both establish and restore the host automatically. Repeated setup notifications preserve a valid host. A late result from a stopped, suspended or replaced scope cannot register over the current host; unknown writes still require reconciliation.

## Qualification evidence

All qualification uses synthetic identities, private profiles, temporary PostgreSQL databases and controlled mail sinks. The daily desktop installation, accounts and data are not used.

| Fixed candidate | Evidence |
| --- | --- |
| SparkClaw `41b24a38`; ISCP `22da89f` | Full independent Go test, build and vet pass; later local-refresh and delayed-hint changes also pass focused race and real PostgreSQL checks |
| SparkClaw `62358707`; WebChat `3b4a0be1` | 465 desktop tests; 360 WebChat tests in 70 files; strict frontend build pass |
| SparkClaw `a438c44b` | Independent SQLite/renderer restart proves recording save-before-lost-ACK inserts once, with rollback and historical schema checks |
| Final ISCP `22da89f` / SparkClaw `41b24a38` transport | Independent review closes original-tuple cancellation, ordering, durable first-write, issuer/Relay revision, actual HTTP disclosure, missing-domain and delayed-hint races |
| Native stream, SparkClaw `3b4a0be1` / independent ISCP `058e81b` | 20 runs, 2,000 batches: observed p95 151.09 ms / p99 168.16 ms; no missing/duplicate output, blank or remount. Visible 6,000-character auto-follow and joint selection/scroll also pass |
| Same native stream candidate | Helper interruption recovers in 2,233.8 ms under the unchanged 3-second gate, one execution and no missing/duplicate output; one blank observation occurs during injected interruption |
| Native mail, SparkClaw `00b48196` / ISCP `30707ae` | Original mail UI passes file-change invalidation, desktop-owned attachment bytes, lost-receipt reconciliation without resend, and 10 MiB in 103,655 ms under the unchanged 180-second gate |
| Native authorization, SparkClaw `77fbfb21` | Original settings deletion, retained signed result, issuer/native restart and Relay-offline no-fallback checks pass |
| Native browser, SparkClaw `62358707` / ISCP `30707ae` | All seven capture modes pass, including first use of a never-presented task page; original manual/automatic grant coalesces, read/fill/click run through ISCP, and lost click receipt produces exactly one effect |
| Original desktop components | Visual matrix: 432 cases, 88 screenshots, no recorded layout errors |
| ISCP latency fixture | 100 samples: send-to-receipt p95/p99 14.20/17.32 ms; full admission transaction 8.21/14.33 ms; database COMMIT 0.71/1.36 ms |

The native mail run explicitly invalidates only the two original valid Access tokens. Both original Refresh tokens recover automatically from credential generation 1 to 2 while preserving identity files and authorization revision 1. Real HTTP/client tests also cover the initial closing response, same-socket reauthentication, concurrent refresh, revocation and missing authority. Moving the actual private-key file away makes all enrollment consumers fail without modifying identity or pending evidence; restoring that key restores the original identity.

The first native screenshot exposed a transient `UnknownVizError` while Chromium created a display surface. The final implementation retries only that bounded screenshot read within the same five-second capture budget and checks the lease before every read. The actual cold capture recovers in about 411 ms; all seven modes preserve view/window visibility and focus, release the debugger and return the current draft. Browser writes are never retried by this path.

The [sanitized machine-readable receipt](evidence/iscp-parallel-2026-10-19.json) maps all 16 authentication and 18 transport design acceptance items to tests and limitations. It records exact source revisions and evidence hashes. Stream timings use the original DOM plus the next animation frame as a compositor proxy; intentionally offscreen reading is not reported as visible latency. These are controlled local measurements, not production network/model benchmarks.

Earlier failures remain recorded: about eight-second recovery before the FIFO fix, excessive journal checkpoint cost, an obsolete mail postcheck, and a candidate Refresh path that minted missing-generation credentials. The latter lab remains rejected and preserved; no missing-generation compatibility exception was introduced. Synthetic fixture timing and old-schema setup were corrected without changing production deadlines or weakening business assertions.

## Maintenance and release boundary

Activation requires an explicit maintenance window and preserved identity, pins, pending operations and data. Existing unknown legacy operations block activation; they are not imported as new control history. The helper's explicit `-upgrade-node-control-v2` command requires `-config` and `-activation-evidence-sha256`, and rejects enrollment, normal check and control-only combinations.

The release inventory must cover the shared Relay, all five Gateway/issuer nodes, control peers, forwarders and companion clients. Production cutover is a later operation. After new-format writes, the baseline HTTP/upsert binary is not a rollback candidate. Without a verified compatible package, remain in maintenance and preserve the current database and recovery evidence.

The existing-domain Relay command has two explicit stages: prepare an incomplete history, then activate against an audited, complete legacy-key manifest and its hash. The old database role must have `NOLOGIN`, no write privileges and no active sessions. Activation preserves the `(domain_id, message_id)` key; it does not introduce a namespace prefix. Old queued data and receipts remain preserved outside the new delivery/cleanup plane, and previously deleted legacy keys remain permanent unknown tombstones. Missing historical evidence prevents activation. A fresh, never-served test domain is not evidence that an arbitrary existing production database is safe to activate.

The recorded [deployment inventory](public-iscp-node-deployment.md) supplies the following baseline. It must be reconciled with live services and companion installations before a later deployment; this local implementation does not assert that the recorded installation versions remain current.

| Component | Baseline identity and required coverage |
| --- | --- |
| Shared Relay | TEST1 / VM100; public ingress, persistent signer, PostgreSQL, all five allowlisted authority routes and independent control forwarders |
| work1 / VM103 | `sparkclaw-vm103-aee06778-2010-470c-83e9-f83572831c1a`; Gateway, node issuer, issuer service peer, responder and its TEST1 control peer |
| work2 / VM104 | `sparkclaw-work2-431c00ce-b780-42e3-9d25-c25c87c8c011`; the same complete component set, retaining its existing authority directory |
| work3 / VM105 | `sparkclaw-vm105-219ec0dc-4add-41b0-ab1e-7dd02997de59`; the same complete component set |
| work4 / VM106 | `sparkclaw-vm106-6c3485b3-81ed-4414-a3c2-b6ce879e9561`; the same complete component set |
| work5 / VM107 | `sparkclaw-vm107-c840a3e8-86d2-4fc4-8cb2-75341cd1e333`; the same complete component set |
| Companion clients | Every SparkX/helper profile and other admitted client for these routes, including the existing Mac installation; retain each installation, device key, pins, original operations and data. Discovery of an unaccounted client blocks cutover. |

The executable entry points are:

1. Independent ISCP `iscp-node-issuer`: enable `control_v2`, set `control_writer_epoch`, `control_bind`, `control_tls_certificate_file` and `control_tls_key_file`. Its existing `control_config_file` identifies the registered issuer service actor. The HTTPS control handler does not wait for WebSocket READY.
2. Independent ISCP `iscp-public-relay`: enable `ISCP_CONTROL_V2` and set `ISCP_CONTROL_ROUTES_FILE`. Each fixed route specifies the issuer `audience`, HTTPS `origin` and `tls_certificate_file`; the local Relay audience remains local. A router restart invalidates unfinished challenge routing rather than trusting a caller-supplied destination.
3. Node authority: `control_issuer_actor` must be an existing member of `ServicePeers`. Issuer agents, forwarders and responders recover through their own operator authority and durable ownership; recovery grants `relay.transport`, not a workbench business Grant.
4. During maintenance, explicitly activate the issuer writer with `-activate-control-writer`, then the original invitation histories with `-activate-control-history`. Nonterminal legacy issuance blocks activation. Upgrade each old initiating helper with the command above. For service/responder enrollment, explicitly change only the same-origin WebSocket path from `/v2/relay/connect` to `/v2/relay/duplex`; preserve identity, pins and binding.
5. Enrollment and existing administrative revocation retain their roles. Activated renewal, recovery, lookup and ACK use independent HTTPS. The new Relay server artifact comes from the ISCP repository; SparkClaw's consumer build does not import a new fork module or rely on a sibling source checkout.

No source push, package installation or service deployment is included in this implementation record.
