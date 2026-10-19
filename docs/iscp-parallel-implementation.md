# ISCP authentication recovery and duplex implementation

[简体中文](../zh-cn/docs/iscp-parallel-implementation.md)

Date: 2026-10-19. This record covers the two local implementation worktrees. It is not a production deployment or a claim that the baseline binaries can read the new state. Final integration qualification is in progress.

## Scope and interface freeze

The user selected a joint interface review and freeze before parallel implementation of the [authentication and operation recovery design](iscp-authentication-operation-recovery-design.md) and [duplex transport design](iscp-duplex-websocket-design.md). Both worktrees start from SparkClaw `cf9b81dec39b8872efc43650080bb8facaa58511`, with the same design snapshot `76ed11bd94ab4bf4b7f63697a96dbf36d4fce053`. The independent ISCP service source starts from `10fe37b3bd02a56b4f9d6cb8dc0333d7eec5356a`.

The branches are `codex/iscp-auth-operation-recovery` and `codex/iscp-duplex-websocket`. The former is also the integration candidate. The original checkout and its design drafts are retained.

InfiniCenter decisions 0038 and 0041 accept the shared interfaces for the self-managed fork. Contract revision `2026-10-19.2` incorporates the accepted 0042 correction to the Base64URL key-ID pattern; the original snapshot remains archived. This does not change JingSi's permanent `not_started` fence or the IMMS contract. Withdrawn decision 0040 remains withdrawn. ISCP services and SparkClaw consumers build independently and interoperate through the frozen wire protocol.

## Implemented behavior

The duplex client separates its reader, writer and application callback worker. Sending obtains a receipt future instead of waiting for a round trip while holding the encryption lock. Relay readiness precedes end-to-end Hello/Ready. Authentication renewal on the existing socket preserves the end-to-end session; actual socket replacement invalidates the old session before a new handshake.

Before a potentially uncertain write, the client durably records the original message identity, ciphertext, digest, expiry, history and enqueue order. Recovery queries that original tuple. A still-unknown earlier message blocks later resubmissions in the same session. Replaying identical ciphertext requires the original active session and deadline. An accepted transport receipt is not a business result, and Relay `absent` never authorizes a business retry. Cancelled writes are `NotSent` only while the writer can still atomically skip them; otherwise the durable tuple is retained as `Unknown`.

The Relay stores admission and queued message data in one PostgreSQL transaction, retains immutable receipts and enforces owner, authority, credential and writer generations. Per-session admission order prevents later replies from passing their predecessors. The retired envelope POST and receive-only connect paths reject old clients. Database guards reject the actual baseline writer after activation; old snapshots and incomplete history do not become new admission authority.

Desktop recovery journals retain original business operation IDs and encrypted results. Browser control actions, mutations and speech recovery use their original identities after reconnection. Renderer action tokens contain opaque IDs and digests, not credentials or operation payloads. The existing task authority and business deadlines remain in force.

Recording insertion and its application marker commit in one local SQLite transaction. Schema 12 adds an opaque recording ID, scope, conversation and draft revision; it stores no audio or extra transcript copy. A crash after saving the draft but before acknowledging the recording can only repeat the acknowledgement. Clearing or sending the draft does not erase the marker. Same-process disk failures retry saving the original draft without inserting the transcript again.

The UI keeps the original components, menus, layout and styles. Unavailable operations retain their entries and are disabled. No persistent transport diagnostics are added to product screens.

## Qualification evidence

All qualification uses synthetic identities, private profiles, temporary PostgreSQL databases and controlled mail sinks. The daily desktop installation, accounts and data are not used.

| Fixed candidate | Evidence |
| --- | --- |
| SparkClaw `4e25b1ac` | 456 desktop tests pass; Linux full Go suite passes in 75 tested packages |
| SparkClaw `dfa0b651` | 360 WebChat tests in 70 files pass |
| SparkClaw `a438c44b` | Independent SQLite store/renderer restart verifies save-before-lost-ACK inserts the recording once; scope, rollback and historical schema tests pass |
| ISCP `3cd6ea9`, `36a3117`, `058e81b` | Independent race checks cover original-tuple cancellation, unknown predecessor ordering, bounded consumed-terminal retention and first-write persistence |
| ISCP `c0bb3fa` with independent SparkClaw client | Native helper interruption recovers in 1,928.8 ms under the unchanged 3-second gate, with one execution and no missing or duplicate authoritative output |
| Original desktop components | Native interaction checks pass; visual matrix covers 432 cases and 88 screenshots with no recorded layout errors |
| Shared design snapshot | Exact CI Markdown link and language-mirror check passes for 121 project documents before this report |

The earlier native recovery failure of about eight seconds is retained as failure evidence. The per-session FIFO fix passes the original threshold. Test-harness failures are also retained: an omitted mail provider controller, obsolete mail-dialog selectors and incomplete synthetic schema downgrades were corrected without changing the product UI or weakening the business assertions.

The visual matrix alone does not establish live mail, settings or authorization behavior. Final native business qualification, actual HTTPS service credential recovery, database restart/history boundaries and the full performance matrix are tracked separately until they pass.

## Maintenance and release boundary

Activation requires an explicit maintenance window and preserved identity, pins, pending operations and data. Existing unknown legacy operations block activation; they are not imported as new control history. The helper's explicit `-upgrade-node-control-v2` command requires `-config` and `-activation-evidence-sha256`, and rejects enrollment, normal check and control-only combinations.

The release inventory must cover the shared Relay, all five Gateway/issuer nodes, control peers, forwarders and companion clients. Production cutover is a later operation. After new-format writes, the baseline HTTP/upsert binary is not a rollback candidate. Without a verified compatible package, remain in maintenance and preserve the current database and recovery evidence.

No source push, package installation or service deployment is included in this implementation record.
