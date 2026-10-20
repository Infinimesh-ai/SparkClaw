# ISCP business receipt retention

[简体中文](../zh-cn/docs/iscp-receipt-retention.md)

SparkClaw automatically removes completed **business result bodies** after seven
days. It retains an encrypted, compact record of the original scoped operation
key, input digest and operation type. An expired result never makes an old
operation eligible for execution again. This policy belongs to the Gateway;
Relay admission and issuer authentication receipts retain their separate rules.

## Collection and recovery

New completed mutation receipts contain an authenticated completion timestamp.
The existing Gateway retention coordinator performs an initial sweep and then
examines up to 256 entries every minute, with a 250 ms processing budget between
entries. The directory cursor advances across ticks; collection can lag the
seven-day threshold while a large directory is scanned. The coordinator stops
and releases its directory handle during Gateway shutdown.

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
