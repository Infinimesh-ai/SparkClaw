# Store Credential Repository Design

> Language: English | [简体中文](../zh-cn/docs/store-credential-repository-design.md)

> Status: S3 design revision 4, 2026-08-20. Reviews 1-3 returned `REVISE`; no
> CredentialRepository code is authorized before an independent
> context-isolated design GO.

## Boundary And Existing Defects

S0 assigns three methods to CredentialRepository. They persist opaque secret
values and their kind/ref metadata; cryptographic sealing belongs to the
`credential` package, not Store. The current boundary is unsafe in several
independent ways:

- File mutates Memory before `persist()` and discards every persistence error;
- PostgreSQL discards save and audit failures, maps every get failure to
  absence, and does not commit the secret plus audit atomically;
- Memory replaces `CreatedAt` on overwrite while PostgreSQL preserves the
  stored value but returns the caller candidate;
- Store methods discard caller context;
- Telegram writes AES-GCM Vault envelopes, but Weixin QR writes and reads a raw
  bot token directly through Store; and
- `CredentialSecret.Value` is publicly JSON-serializable even though no public
  response is authorized to expose it.

This wave fixes that complete credential boundary. It does not migrate
NotificationBinding persistence or make credential-plus-binding a new
cross-repository transaction; ConnectorRepository owns that later command.

## Interface

```go
type CredentialRepository interface {
    SaveCredentialSecret(context.Context, CredentialSaveCommand) (app.CredentialSecret, error)
    GetCredentialSecret(context.Context, string) (app.CredentialSecret, bool, error)
    DeleteCredentialSecret(context.Context, CredentialDeleteCondition) (app.CredentialSecret, error)
}
```

`Store` embeds the interface once and removes the three legacy signatures.
MemoryStore, FileStore, and PostgresStore each assert it directly. The
`credential.Vault` depends only on this repository. A consumer that also needs
other state uses its own explicit composite; no type assertion, dynamic
repository lookup, or optional capability is introduced.

## Stable Semantics

- Every method first composes caller context with the accepted read or
  transaction timeout. Cancellation and timeout remain typed Store errors.
- Standalone refs and candidate `Ref`/`Kind` fields are trimmed. `Value` is an
  opaque byte-preserving string: it is never trimmed, logged, included in an
  error, audit field, event, or public JSON value.
- Save accepts only a `CredentialSaveCommand` built by the Store package. A
  create command requires absence; a replace command carries an opaque expected
  condition and requires an exact current version. Both require non-empty ref,
  kind, and value. Store never generates a credential ref and ignores caller
  `CreatedAt`/`UpdatedAt`.
- A new ref receives one UTC PostgreSQL-microsecond command timestamp for both
  `CreatedAt` and `UpdatedAt`. A conditional replace preserves the existing
  `CreatedAt` exactly and assigns a strictly newer `UpdatedAt` from a
  per-ref non-rollback high-water mark.
- Save is explicit create-or-conditional-replace, not an unlocked upsert.
  Existing-row create or a stale replace condition is `conflict` without
  mutation. A successful replace preserves the current `CreatedAt`. Success
  atomically persists the complete secret and exactly one
  `credential_secret.saved` audit containing only ref and kind. It emits no
  general event.
- Get is side-effect free. Empty or missing refs are ordinary
  `(zero, false, nil)` absence. Backend, scan, decode, validation, or context
  failures are never absence.
- Delete is an exact conditional command. Vault derives the opaque
  `CredentialDeleteCondition` from a complete candidate previously returned by
  Get or Save. It contains a normalized ref and a domain-separated SHA-256
  version over every candidate field, but no value or reversible plaintext.
  Empty/invalid conditions are `invalid`, missing is typed `not_found`, and any
  persisted-field digest mismatch is `conflict` without mutation. Success
  returns the exact deleted candidate, atomically removes the secret, and
  appends exactly one
  `credential_secret.deleted` audit containing only the ref. It emits no
  general event.
- `app.CredentialSecret.Value` changes to `json:"-"`. The File persistence
  codec uses a private wire type to retain `value`; ordinary JSON encoding,
  logs, audit payloads, and HTTP responses remain redacted.

### Deterministic Outcomes

Validation order is: effective context, standalone/candidate normalization,
candidate structural validation, target lookup, persisted-row validation, then
the business mutation. A corrupt stored row cannot be hidden by a later
not-found, overwrite, or delete result.

| Method or condition | Exact repository outcome |
|---|---|
| Get with empty/missing ref | `(zero, false, nil)` |
| Save with an invalid command or empty normalized ref/kind/value | `invalid` |
| Create with an existing ref | `conflict` and no mutation |
| Replace whose current row is absent or differs from the expected version | `conflict` and no mutation |
| Delete with an invalid expected condition | `invalid` |
| Delete with a valid condition whose ref is missing | `not_found` |
| Delete whose current row differs from the expected version | `conflict` and no mutation |
| Create absent ref or conditionally replace an exact current row | persisted normalized candidate |
| Delete an exactly matching current row | exact deleted candidate |
| Backend/scan/decode failure | typed non-absence error |
| Persisted invariant violation outside compatibility rules | `corrupt` |
| Unsafe submission/commit after candidate formation | `unknown_outcome` with the exact save/deleted candidate |

`NewCredentialCreate`, `NewCredentialReplace`, and
`NewCredentialDeleteCondition` are the only command/condition constructors.
Create retains the proposed secret. Replace retains the proposed ref/kind/value
plus an opaque condition derived from the prior row; no constructor permits a
caller-supplied timestamp or digest. The condition digest uses length-delimited
normalized Ref/Kind, byte-exact Value, and UTC civil components for both times:
signed year, month, day, hour, minute, second, and nanosecond. It does not use
`UnixNano`, so every Go `time.Time` accepted by File or PostgreSQL has a
non-overflowing representation. Conditions stay in process memory and are
never serialized, logged, persisted, audited, traced, or projected.

Definite save/delete failures return no candidate or secret value. An unsafe
failure before Delete has read, validated, and matched the current row also
returns no candidate. Store validation errors never interpolate untrusted refs,
kinds, opaque values, SQL, or snapshot bytes. Raw backend causes remain only in
the internal error chain; Vault public projections never expose them.

## Compatibility And Secret Format

Repository validation treats `Value` as opaque and never parses a Vault
envelope. File startup and PostgreSQL readiness enforce:

- a non-empty trimmed ref, and for File a map key exactly equal to embedded
  `Ref`;
- non-zero `CreatedAt` and `UpdatedAt`; clock rollback is compatible, so their
  chronological order is not invented;
- legacy blank kind/value rows remain loadable and readable but cannot be
  created by a new save; and
- all scalar values are copied exactly after the explicit ref/kind projection
  above.

Vault owns format compatibility. AES-256-GCM envelope version 1 remains the
only normal format. The exact legacy kind `openclaw-weixin-bot-token` may carry
the pre-migration raw token. On first `Open`, Vault seals that value at the same
ref, durably verifies/reconciles the replacement, zeroes temporary byte copies,
and only then returns plaintext. Raw values of every other kind still fail
closed as invalid/unauthenticated envelopes. New Weixin credentials never use
the legacy representation.

An encrypted File state envelope opened without File encryption configured is
rejected before Snapshot decoding. It must not be accepted as an empty snapshot
through unknown JSON fields or overwritten by the next command. Configured
encryption and plaintext snapshot compatibility otherwise remain unchanged.

## Operation And Durability Rules

| Operation ID | Mode | Timeout | Reconciliation |
|---|---|---|---|
| `credential_secret.save` | write | transaction | exact candidate behind ref barrier |
| `credential_secret.get` | read | read | ref barrier when resolving a command |
| `credential_secret.delete` | write | transaction | absence, exact prior candidate, or conflicting replacement behind ref barrier |

Memory applies context before and under one lock. Secret mutation and audit
append happen under that lock.

File uses accepted migrated admission and `runFileCommand`; no credential path
calls legacy `persist()`. A command snapshots the complete pre-state, commits
one encoded replacement, restores secret and audit state on definite failure,
but deliberately retains the failed candidate's non-rollback high-water mark.
It installs the accepted fence on an unknown rename/directory-sync outcome.
Reads cannot cross that fence. Startup validates the private secret wire
representation before loading Memory.

PostgreSQL save/delete acquire a context-aware capacity-one process admission
for high-water ownership, then an owned connection and explicit transaction.
They take a ref-derived advisory transaction lock, read the current row, form a
candidate, validate create absence or the replace/delete condition under that
lock, and write secret plus audit in that transaction. Resolution reads use
explicit `READ COMMITTED`, take the same advisory lock in one statement, and
query in a later statement. Server rejections are definite `internal` except
applicable uniqueness/business codes; safe-to-retry transport failures are
definite. Unsafe statement, context, or commit outcomes after connection
acquisition terminate without release and return `unknown_outcome`. Rollback
failure also terminates without release while retaining every cause.

Save reconciliation succeeds only when every persisted field equals the exact
candidate. Delete reconciliation returns three distinct proofs: final absence
means the target command completed; the exact prior candidate means it did not
commit and may be conditionally retried; a different row is `conflict` and must
never be deleted by that pending command. A failed barrier remains unresolved.
No reconciliation result is inferred from an unlocked read.

## Vault And Consumer Migration

Vault propagates each method context to every repository call and maps typed
Store failures without string matching. `CredentialVault.Seal` carries an
explicit bounded, non-secret one-shot binding identity:

```go
type CredentialVault interface {
    Ready() error
    BindLifecycle(context.Context)
    Seal(context.Context, string, string, []byte) (string, error) // binding ID, kind, plaintext
    Open(context.Context, string) ([]byte, error)
    Delete(context.Context, string) error // ref
}
```

The seal identity is the immutable binding ID already created before an
adapter starts. It is trimmed, required, and at most 256 bytes. The binding
record remains the authority for its normal ID persistence/projection; Vault
does not create an additional identity log, receipt, audit, or public field.
Vault derives a separate ref key as
HMAC-SHA-256(master key, `sparkclaw-credential-ref-key-v1`), then
derives each new ref as `cred_` plus the unpadded base64url encoding of the full
HMAC-SHA-256 over domain `sparkclaw-credential-ref-v1` and the length-delimited
seal identity. The kind is not part of this derivation, so binding-ID reuse
with another kind addresses the same row and is detected rather than creating
another ref.

Seal first resolves pending work, derives the ref, and reads it. Absence uses a
create command. A present authenticated envelope with the same kind and
constant-time-equal plaintext is a completed replay and returns the same ref
without another save/audit. Any other present row is stable
`credential_invalid` and is never overwritten. Consequently immediate success
may clear volatile state: replay for the same live binding reconstructs the
exact ref even after process restart, while equal kind/plaintext under another
binding ID has an independent ref and cannot share or clean up the first
credential.

This is deliberately a live-binding replay contract, not a durable generic
operation ledger. A confirmed Delete retires that binding's seal identity; a
later Seal with the retired binding ID is outside the contract and may create a
new row at the deterministic ref. Production makes the precondition structural:
Gateway creates a fresh immutable binding ID before every Start, adapters never
generate or replace it, and no revoked/compensated binding ID returns to Seal.
Delete has no caller-supplied operation ID, so it cannot be replayed against a
different ref under a stale identity. ConnectorRepository may later add a
durable cross-repository lifecycle receipt; this wave does not pretend Audit is
such a ledger.

Vault has one capacity-one in-memory command coordinator with three explicit,
disjoint pending modes:

1. **Create** retains binding ID, kind, a plaintext fingerprint, ref, sealed
   payload, and any normalized candidate returned by Store, never plaintext.
   Exact committed candidate completes the matching binding Seal. Before a
   different Vault mutation proceeds, it conditionally deletes an exact
   undisclosed orphan.
   Absence proves rollback and retries the same create payload. A different row
   at the derived ref is identity conflict and is never deleted or overwritten.
2. **Delete/cleanup** is identified by ref and retains the opaque delete
   condition, never candidate value. Absence proves completion. The exact prior
   version proves rollback and permits one new conditional delete without an
   unlocked get/delete gap. A replacement version is conflict and is never
   deleted by the pending command.
3. **Legacy rewrap** is identified by ref plus the opaque raw-prior condition;
   it is never an orphan and never enters cleanup deletion. It retains only the
   sealed replacement payload and any normalized encrypted candidate. An exact
   encrypted candidate proves completion. The exact raw prior proves rollback
   and permits another conditional replace using the same sealed payload. An
   absent row clears pending and is unseal failure. Another authenticated
   envelope for the same ref/kind/plaintext also proves that rewrap completed;
   any other replacement is conflict and is neither overwritten nor deleted.
   A read/barrier failure retains pending. Temporary plaintext used to compare
   an authenticated replacement is zeroed before releasing the coordinator.

Every later Vault mutation resolves the applicable pending mode before it
proceeds. An unresolved create cleanup or delete prevents generation of another
ref; an unresolved rewrap blocks mutation but never deletes the active
credential. Thus a later binding start can finish prior compensation without
receiving or disclosing the old ref. Immediate success and final resolution
clear matching state by generation; a late cleanup cannot clear a replacement
generation. Repository candidates and errors never cross public APIs.

The composition root constructs the only production Vault. Before starting
connector workers, `gatewayServices.Start` calls `BindLifecycle(ctx)`. Binding
increments a generation and starts one cancellation watcher; cancellation
clears pending material only when its generation still matches. Rebinding first
invalidates the old generation. The Server does not construct a fallback Vault.

All new Weixin QR credentials use `CredentialVault.Seal` with the immutable
binding ID; Gateway assigns the returned durable ref to the binding only
after Seal succeeds. Notification and Syncer use `CredentialVault.Open` with
their owned request/work context instead of reading `CredentialSecret.Value`.
Static operator-configured Weixin tokens remain configuration inputs and do not
enter Store.

Gateway must check credential cleanup errors. Binding-start compensation and
binding revocation may return a stable unavailable response after the binding
operation has completed; the Vault coordinator owns conditional deletion until
resolution, and the next mutation resolves it before proceeding. No raw Store,
Vault, envelope, ref, token, or backend error is returned. ConnectorRepository
will later decide whether binding plus credential lifecycle becomes one
cross-repository command.

### Stable Error Projection

Vault never returns a raw Store error. It preserves the cause only behind
`Unwrap` and maps outcomes without string matching:

| Source | Vault code | Public/worker behavior |
|---|---|---|
| invalid binding ID/kind/value or live binding-ID input conflict | `credential_invalid` | HTTP 400; stable validation copy |
| caller cancellation | `credential_canceled` | HTTP 408 when a response is still writable; worker stops the owned item |
| Store timeout/unavailable/durability/unknown/internal/corrupt, unresolved conditional conflict, or local random/encryption failure | `credential_unavailable` | HTTP 503; worker returns retryable unavailable without treating it as absence |
| key missing or unusable | `credential_key_unavailable` | HTTP 503; connector unavailable |
| absent ref, invalid envelope, wrong key/AAD, or authentication failure on Open | `credential_unseal_failed` | stable credential failure; never includes ref, kind, value, or cause |

`Delete` treats only typed Store `not_found` as idempotent success. Notification
and Syncer distinguish unavailable from normal credential absence, expose only
the stable Vault message, and never copy it from raw Store diagnostics into
binding `last_error`.

## Gate And Commit Boundary

Implementation follows this accepted design in separate reviewable commits:

1. repository behavior across interface, operations, three backends, private
   File wire format, and compatibility validation;
2. Vault seal identity, save/delete coordinator, lifecycle binding,
   legacy Weixin rewrap, consumer/context migration, and safe Gateway
   projection; and
3. any independent File encrypted-envelope fail-closed defect fix, if kept
   separate from repository behavior for bisect clarity.

The implementation gate requires:

- shared Memory/File create/conditional-replace/delete, conflict, absence,
  validation precedence, timestamps/high-water, cancellation/timeout, audit
  atomicity, digest time extremes, and redaction;
- File rollback, fence, final reconciliation, encrypted/plain restart, corrupt
  state, encrypted-without-key rejection, failure injection, and race evidence;
- PostgreSQL acquire/begin/statement/commit/rollback classification,
  terminate-not-release, context-aware admission, barrier isolation, scan
  propagation, atomic audit, startup validation, and real-DSN evidence;
- Vault deterministic live-binding replay before/after process restart,
  live-binding different-input conflict, independent identical plaintext bindings,
  create/delete immediate and next-call reconciliation, conditional orphan
  cleanup, all legacy rewrap state transitions without orphan deletion,
  generation-safe lifecycle cleanup, safe typed errors, wrong-key failure, and
  non-Weixin plaintext rejection;
- Gateway/Notification/Syncer tests proving no active binding on failed Seal,
  no raw Store access, owned context propagation, cleanup error handling, and
  no token/ref/error disclosure;
- source guards for one embedded repository, exact signatures, opaque command
  factories, and three implementations, no legacy methods, no ignored result, no migrated
  `context.Background()`, `CredentialSecret.Value` JSON redaction, and Vault's
  minimum repository dependency; and
- full Go test/build/vet, focused Store/credential/Gateway/Weixin race, default
  File production entry, WebChat tests/build, 44 Python script tests, default
  Compose, bilingual docs CI, and disposable configured real-PostgreSQL full
  plus race runs. Existing PostgreSQL CI topology and DSN skip behavior remain
  unchanged.

No SessionRepository design starts until the exact Credential implementation
receives an independent context-isolated GO.

## Review Record

| Review | Revision | Decision | Evidence | Reviewer/date |
|---|---|---|---|---|
| Credential contract review 1 | `de4cd93` | `REVISE` | Seal lacked logical operation identity; ref-only Delete could remove a replacement and had no pending cleanup owner; File high-water rollback contradicted non-rollback timestamps; lifecycle and public error mappings were incomplete | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 2 | `1d646f0` | `REVISE` | Immediate success cleared all replay identity; legacy rewrap lacked a distinct non-orphan state machine; delete digest used overflow-prone UnixNano encoding | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 3 | `b6def5d` | `REVISE` | Deterministic refs preserved identity only while a row existed, but the document still promised generic post-delete operation-ID conflict; Delete also accepted a reusable caller operation ID without a durable binding | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 4 | pending | pending | pending | pending |
