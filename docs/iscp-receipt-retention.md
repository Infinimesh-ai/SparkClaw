# ISCP business receipt retention

[简体中文](../zh-cn/docs/iscp-receipt-retention.md)

SparkClaw automatically removes completed **business result bodies** after seven
days. It retains an encrypted, compact record of the original scoped operation
key, input digest and operation type. An expired result never makes an old
operation eligible for execution again. This policy belongs to the Gateway;
Relay admission and issuer authentication receipts retain their separate rules.

## Collection and recovery

New completed mutation receipts contain an authenticated completion timestamp.
A lifecycle-owned Gateway worker keeps an in-memory expiry index and wakes at
the earliest indexed deadline. Successful receipt commits update the index and
reset the timer when needed; an empty index disables the expiry timer. Startup
rebuilds this derived index from authenticated files and collects expired results.
An hourly directory pass reconciles missed updates. Admission checks also request
reconciliation when the entry count or byte budget (including active reservations)
reaches 80%, coalescing sustained pressure requests to once per minute.

All triggers use the same bounded collector: at most 256 entries or a 250 ms
budget checked between entries, followed by a 10 ms pause if work remains. Index
rebuilding uses a directory cursor across batches. Each entry releases the
journal lock; a collector encountering an active request yields. Capacity checks
only signal the worker and may still reject admission until space is reclaimed.
Deletion rechecks the durable timestamp, so a stale index cannot expire a newer
result. Failed expiry attempts back off for a minute without starving other keys.
A large backlog or slow individual filesystem operation can delay collection.
Gateway shutdown cancels the worker and closes its directory cursor.

Before expiry, replay and lookup return the original result. After collection,
the existing `operation_outcome_unknown` / `state: unknown` recovery responses
keep clients from automatically resending; a changed input under the same key
still returns `operation_input_conflict`. The result is no longer available;
these responses do not assert that the original operation failed. New explicit
business work must use its normal authority and reconciliation rules.

Incomplete intent fences, legacy ciphertext without authenticated timestamps,
event subscription state and terminal speech checkpoints are not deleted by
this collector. File modification times are never treated as proof of expiry.
In particular, the historical ACK files from the October 2026 incident remain
preserved; stopping redundant ACK creation and collecting new completed records
prevent that same growth pattern from recurring.

## Persistence and limits

The new encrypted envelope binds its ciphertext to the scoped key's SHA-256
filename. Legacy receipts remain readable using their original authenticated
scope and operation ID. All peer adapters share one journal, mutex and capacity
reservations for the Gateway's execution root.

Expired records move into at most 256 encrypted index shards. Each shard is
bounded to 4 MiB. A separately encrypted catalog records which shards must
exist: missing or corrupt catalog/shard history fails closed, including after
restart. Compaction commits and fsyncs the shard and catalog before unlinking
the full result. A crash can leave both copies; lookup prefers the compact fence
and a later sweep finishes the removal. A failed index write preserves the
original result.

Compaction releases full-result directory slots. The 262,144-entry admission
bound still includes journal metadata, and the shared 512 MiB byte budget counts
compact indexes too. Reclamation must not increase the combined durable size;
atomic writes require temporary disk headroom. If storage or a shard is full,
the Gateway preserves evidence and refuses new admission rather than dropping
deduplication records. This is bounded storage, not an unlimited history claim.
Capability limits advertise `operation_receipt_result_retention_seconds: 604800`.

## Upgrade and rollback

Opening the new journal creates its `retired` directory and encrypted catalog.
The previous implementation rejects directories during its capacity check, so
an older binary refuses new mutations instead of re-executing expired IDs.
Do not remove the directory/catalog to bypass that fence. A usable rollback
must understand this format and preserve its indexes; deleting indexes or
restoring an older partial snapshot is not a supported rollback.

This source change does not automatically upgrade a running service or client.
No UI layout, menus, conversation data or task results outside this receipt
journal change. Tests cover real owner mutation/replay, restart, exact expiry,
scope isolation, unknown/legacy preservation, missing and corrupt history,
interrupted collection, bounded progress, byte accounting and lifecycle cleanup.
Scheduler tests cover earlier deadline updates, empty-index idling, startup and
late journal creation, hourly reconciliation, capacity pressure, contention and
failed-entry backoff.

On 2026-10-20, product `094deb8f` was deployed to the current work2 Gateway, with
a compatible rollback image and all original receipts preserved. See the
[scoped rollout record](public-iscp-node-deployment.md#17-receipt-retention-and-automatic-browser-rollout-2026-10-20).
