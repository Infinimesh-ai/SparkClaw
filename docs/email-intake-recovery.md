# Receiving configuration recovery

Language: English | [简体中文](../zh-cn/docs/email-intake-recovery.md)

An interrupted receiving toggle retains its original ISCP operation. A healthy
provider check verifies sign-in; it does not create a mailbox or resolve that
operation. Sending and draft operations retain their existing uncertainty rules.

`mail.intake.update` binds deployment, owner, client, installation, operation ID
and the existing request digest to its mailbox transaction. The transaction
stores the original mailbox result together with the provider binding version.
An incomplete domain receipt therefore has a durable business result to recover
when the commit succeeded but its response or final receipt was lost.

`mail.intake.reconcile` is an explicit typed mutation with `settings.write`, a
provider parameter, and `{operation_id, original}`. The original object contains
operation, params, body and any original expected_revision, request_id and
input_digest. The Gateway requires the existing receipt in the current identity
scope and verifies its exact request digest. It never repeats the browser probe,
mail discovery, configuration request, send, or any other original effect.
`operations.receipt` and all HTTP GET endpoints remain read-only.

If a matching business result exists, reconciliation completes the original
receipt with that result. Otherwise it may establish `not_applied` only while
the provider binding version still equals the original expected version. In one
transaction it advances that version and stores the negative result. A delayed
writer retaining the old version can no longer commit, including an older writer
that does not know the new recovery record. A changed version without a matching
positive result remains unknown. This also permits safe negative reconciliation
of older intake receipts whose mailbox transactions lacked an operation link.

Reconciliation uses the original operation as its idempotency key and has no
second domain intent. Cancellation, a crash, or response loss during recovery
can be retried against the same durable result. Terminal domain receipts cannot
be overwritten by a delayed original response. Native recovery only selects
this path for qualified intake operations with their complete protected intent;
unknown sends and fingerprint-only legacy records are unchanged.

The status projection includes `provider_versions`, including providers without
a mailbox. The existing receiving toggle uses this version. A recovered negative
result explains that the earlier change was not applied; a subsequent explicit
foreground action may use the current version. Background recovery never creates
a replacement action. Existing pages, controls, drafts and mailbox identities
remain intact.

Memory, file and PostgreSQL stores share the same aggregate transition. Recovery
records use the existing durable email record storage; they are not expired with
completed domain-response bodies. Software rollback must preserve all records.
Recovery of an expired response returns that business proof without recreating
the expired body or removing its original replay fence.
An older client or Gateway cannot enable an empty provider binding after its
version was advanced by recovery: it safely fails the old CAS until upgraded.
No rollback may erase the fence to make an old version usable.
