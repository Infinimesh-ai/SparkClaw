# Store Credential Repository Design

> Language: English | [简体中文](../zh-cn/docs/store-credential-repository-design.md)

> Status: S3 design revision 8 received independent `GO` at `b0884f6` on
> 2026-08-20 after reviews 1-7 returned `REVISE`. This authorizes only the live Credential
> foundation checkpoint below, followed by ConnectorRepository lifecycle
> migration and then the final Credential integration gate.

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

This contract fixes the complete credential boundary. Production cutover also
needs a durable NotificationBinding identity before any credential can be
sealed. That lifecycle is owned by ConnectorRepository rather than smuggled
into CredentialRepository as a second binding log. The repository roadmap
therefore freezes this contract first, implements the Credential
repository/Vault primitives already consumed by production, then migrates
ConnectorRepository, and finally reviews their integrated caller cutover. It
does not claim a new atomic transaction across Store and an external provider.

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
}

type CredentialLifecycleRecovery interface { // introduced with Connector proof paths
    Delete(context.Context, string) error // ref
    AbortSeal(context.Context, string, string) error // binding ID, kind; added with Connector proof path
}
```

Connector recovery receives an explicit consumer-owned composite of these two
interfaces. It never discovers recovery through a type assertion or optional
capability. The foundation defines and uses `CredentialVault` only;
`CredentialLifecycleRecovery`, both concrete methods, and their first durable
proof-bearing callers land together in the Connector wave. The foundation
repository still implements `DeleteCredentialSecret`: Vault's private pending
create/orphan reconciliation consumes that method without exposing a public
delete capability.

The seal identity is an immutable binding ID durably created before an adapter
starts or Vault receives plaintext. It is trimmed, required, and at most 256
bytes. The accepted ConnectorRepository contract must make that ordering true;
the current best-effort `SaveNotificationBinding` does not. The binding record
remains the authority for identity persistence and terminal reuse prevention;
Vault does not create an additional identity log, receipt, audit, or public
field.
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
may clear volatile state: a caller that recovers the same durably nonterminal
binding reconstructs the exact ref after process restart, while equal
kind/plaintext under another binding ID has an independent ref and cannot share
or clean up the first credential. No replay guarantee exists for an ID that was
only process-local. The foundation checkpoint may retain that pre-existing
failure window under an explicit non-final gate; the final accepted production
path must not call Seal in that state.

This is deliberately a live-binding replay contract, not a durable generic
operation ledger. A confirmed public Delete or AbortSeal is paired with a durable
terminal binding transition. ConnectorRepository retains that record, rejects
reuse of its ID, and prevents a terminal ID from returning to Start, Poll, or
Seal. Delete has no caller-supplied operation ID, so it cannot be replayed
against a different ref under a stale identity. Audit is not a lifecycle
receipt or an atomicity substitute.

### Durable Binding Prerequisite

The final restart-safe Credential lifecycle is blocked until the separately
reviewed ConnectorRepository contract and implementation provide all of these
rules:

1. Gateway creates a fresh immutable binding ID and durably commits a `starting`
   binding before calling an adapter. A definite or unresolved binding create
   never reaches provider verification or Seal.
2. Telegram may Seal only from that exact `starting` version. A successful Seal
   is followed by a conditional transition to `active` with the returned ref.
3. Weixin first conditionally advances `waiting_scan`/`waiting_confirm` to a
   non-pollable `credential_pending` version carrying non-secret provider
   metadata, and confirms that transition before passing the returned token to
   Seal. A compensated or restarted record therefore cannot Poll and Seal the
   same retired ID again.
4. An unknown transition to `active` is reconciled behind the binding's Store
   barrier. Exact `active` state proves success. Only exact pre-active state
   proves that AbortSeal is permitted; an unresolved or different state is
   never cleaned up.
5. Before connector workers start or Gateway listens, recovery scans
   `starting`, `credential_pending`, and `revoking` records. It uses
   `AbortSeal(bindingID, kind)` or `Delete(ref)` as appropriate, then
   conditionally commits `failed` or `revoked`. Recovery failure keeps the
   non-pollable state and fails connector readiness instead of activating a
   binding or losing cleanup ownership.
6. Revocation first commits a non-active `revoking` state retaining the ref,
   then deletes the exact credential, then commits `revoked`. A restart repeats
   only the pending delete. Terminal binding records are retained, and binding
   IDs are never recycled.

`AbortSeal` derives the deterministic ref from binding ID, requires the exact
expected kind, authenticates a present Vault envelope, and conditionally deletes
that exact candidate. Absence is success. A different kind, invalid envelope,
replacement, or unresolved Store result is stable unavailable and is never
deleted. This reconstructible cleanup is independent of Vault's volatile
pending coordinator, but its authorization comes only from a proven Connector
pre-active state.

The dependency is implemented without dead code or a circular gate:

1. **Credential foundation checkpoint.** After this design GO, migrate the
   three repository methods, all three backends, File codec/fail-closed loading,
   deterministic Vault Seal, private conditional repository delete/rewrap,
   lifecycle binding, and
   every current credential caller. Telegram and Weixin use Seal; Notification
   and Syncer use Open. `CredentialLifecycleRecovery` is deliberately not defined
   or implemented in this checkpoint. A mismatch or failure from the legacy,
   unconditional binding save is ambiguous and returns stable unavailable
   without deleting the sealed credential. A ref-bearing legacy revoke likewise
   cannot prove that its binding transition is durable: foundation cancels local
   work, retains the credential, and returns stable unavailable instead of
   reporting final revocation or calling a public Delete. This checkpoint fixes
   Store durability and plaintext handling, but it is not Credential implementation
   GO: the current Connector methods cannot prove pre-Seal identity or safe
   compensation across restart.
2. **ConnectorRepository migration.** A focused foundation review must show the
   live primitives and backend contract match this design. It may authorize the
   Connector wave without declaring Credential complete. Connector then
   migrates its own interface/backends/callers and adds the durable states,
   barriers, startup recovery, and terminal ID retention above. That repository
   wave adds public Delete and AbortSeal with their first production callers.
   The revoking barrier authorizes Delete; the exact pre-active barrier authorizes
   AbortSeal. No Credential repository method changes during that wave.
3. **Final Credential integration gate.** After Connector implementation GO,
   rerun the complete Credential/Connector failure-window and restart matrix,
   remove every temporary allowance for process-local Seal identity, and review
   the exact integrated candidate. Only that decision is Credential
   implementation GO and unblocks SessionRepository.

Only one Store repository implementation is active in each code step. The
foundation checkpoint is not a partial repository scaffold: all three
Credential backends and all safely callable credential consumers are live before
Connector code starts. Public Delete and AbortSeal are the intentionally deferred
Vault lifecycle capabilities; Connector adds their interface and methods together
with durable proof-bearing callers, so neither is dead code or callable without
the required authorization.

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
credential. After restart, the Connector recovery path reconstructs cleanup
from the durable binding ID instead of relying on this volatile state. Immediate
success and final resolution clear matching state by generation; a late cleanup
cannot clear a replacement generation. Repository candidates and errors never
cross public APIs.

The composition root constructs the only production Vault. Before starting
connector workers, `gatewayServices.Start` calls `BindLifecycle(ctx)`. Binding
increments a generation and starts one cancellation watcher; cancellation
clears pending material only when its generation still matches. Rebinding first
invalidates the old generation. The Server does not construct a fallback Vault.

The foundation moves all new Weixin QR credentials to `CredentialVault.Seal`
and moves Notification and Syncer to `CredentialVault.Open` with their owned
request/work context instead of reading `CredentialSecret.Value`. After the
Connector wave, Seal receives the durably persisted immutable binding ID:
Gateway first persists `credential_pending`, then assigns the returned durable
ref only through the conditional `active` transition. Static
operator-configured Weixin tokens remain configuration inputs and do not enter
Store.

Gateway must check credential cleanup errors once Connector owns their durable
authorization. Within one running process, the
Vault coordinator retains conditional command material until resolution and the
next mutation resolves it before proceeding. Across cancellation or restart,
the durable Connector `starting`, `credential_pending`, or `revoking` record is
the cleanup owner; startup recovery must resolve it before workers run. Binding
start compensation and revocation may return stable unavailable while that
durable record remains non-active and retryable. During foundation, ambiguous
legacy binding saves and every ref-bearing legacy revoke retain the credential
and return stable unavailable; no public cleanup method exists yet. No raw Store,
Vault, envelope,
ref, token, or backend error is returned, and neither Audit nor volatile Vault
state is treated as the cross-repository commit record.

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

Once introduced with Connector, public `Delete` treats only typed Store
`not_found` as idempotent success. Notification
and Syncer distinguish unavailable from normal credential absence, expose only
the stable Vault message, and never copy it from raw Store diagnostics into
binding `last_error`.

## Gate And Commit Boundary

Implementation follows this accepted design in separate reviewable checkpoints:

1. Credential foundation commits: repository behavior across interface,
   operations, three backends, private File wire format and compatibility
   validation; then live Vault seal identity, coordinators, lifecycle
   binding, legacy Weixin rewrap, consumer/context migration, and safe Gateway
   projection. Public Delete and AbortSeal are excluded. The encrypted-envelope
   fail-closed defect may remain a separate
   commit for bisect clarity.
2. A foundation checkpoint review authorizes the separately documented complete
   ConnectorRepository design and implementation. That wave adds public Delete
   and AbortSeal with their exact barrier-proven recovery callers, but does not mark
   Credential GO.
3. The exact integrated Credential/Connector candidate receives the final
   Credential implementation review after Connector GO.

The implementation gate requires:

- shared Memory/File create/conditional-replace/delete, conflict, absence,
  validation precedence, timestamps/high-water, cancellation/timeout, audit
  atomicity, digest time extremes, and redaction;
- File rollback, fence, final reconciliation, encrypted/plain restart, corrupt
  state, encrypted-without-key rejection, failure injection, and race evidence;
- PostgreSQL acquire/begin/statement/commit/rollback classification,
  terminate-not-release, context-aware admission, barrier isolation, scan
  propagation, atomic audit, startup validation, and real-DSN evidence;
- the foundation checkpoint proves every method it implements has a production
  caller and all three Credential backends are migrated together; it proves an
  ambiguous legacy binding-save result never invokes Delete or AbortSeal, and
  a ref-bearing legacy revoke never deletes its credential or reports final
  success without a durable proof; the repository Delete is consumed only by
  private Vault reconciliation while no public recovery interface exists; it
  records the not-yet-migrated Connector restart identity instead of claiming a
  false final GO;
- the Connector gate proves public Delete and AbortSeal are introduced with
  their first production callers, only after exact revoking or pre-active barrier
  results respectively, and that concurrent or unresolved binding state never
  authorizes credential deletion;
- the final gate proves Vault deterministic replay from a durably recovered
  nonterminal binding, process-local ID rejection at the caller boundary, live-binding
  different-input conflict, independent identical plaintext bindings,
  create/delete immediate and next-call reconciliation, conditional orphan
  cleanup, restart-reconstructed AbortSeal, all legacy rewrap state transitions
  without orphan deletion, generation-safe lifecycle cleanup, safe typed
  errors, wrong-key failure, and non-Weixin plaintext rejection;
- failure-injection and restart tests for crash after durable binding create,
  after Seal but before active transition, after unknown active transition, and
  after revoke but before Delete/final revoke; Weixin proves the durable
  `credential_pending` record is non-pollable and terminal compensation cannot
  re-enter Poll or Seal with the same ID;
- Gateway/Notification/Syncer tests proving adapters never replace binding IDs,
  no active binding on failed Seal, no raw Store access, owned context
  propagation, cleanup error handling, startup recovery before workers, and no
  token/ref/error disclosure;
- source guards for one embedded repository, exact signatures, opaque command
  factories, and three implementations, no legacy methods, no ignored result, no migrated
  `context.Background()`, `CredentialSecret.Value` JSON redaction, and Vault's
  minimum repository dependency; Connector recovery uses an explicit composite
  containing `CredentialLifecycleRecovery`, with no type assertion; and
- full Go test/build/vet, focused Store/credential/Gateway/Weixin race, default
  File production entry, WebChat tests/build, 44 Python script tests, default
  Compose, bilingual docs CI, and disposable configured real-PostgreSQL full
  plus race runs. Existing PostgreSQL CI topology and DSN skip behavior remain
  unchanged.

After this design receives GO, only the Credential foundation checkpoint is
authorized. Its focused review may authorize ConnectorRepository design and
implementation without calling Credential complete. No SessionRepository
design starts until Connector receives GO and the resumed exact integrated
Credential candidate receives its own context-isolated implementation GO.

## Review Record

| Review | Revision | Decision | Evidence | Reviewer/date |
|---|---|---|---|---|
| Credential contract review 1 | `de4cd93` | `REVISE` | Seal lacked logical operation identity; ref-only Delete could remove a replacement and had no pending cleanup owner; File high-water rollback contradicted non-rollback timestamps; lifecycle and public error mappings were incomplete | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 2 | `1d646f0` | `REVISE` | Immediate success cleared all replay identity; legacy rewrap lacked a distinct non-orphan state machine; delete digest used overflow-prone UnixNano encoding | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 3 | `b6def5d` | `REVISE` | Deterministic refs preserved identity only while a row existed, but the document still promised generic post-delete operation-ID conflict; Delete also accepted a reusable caller operation ID without a durable binding | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 4 | `30cbf24` | `REVISE` | Gateway kept the binding ID only in memory until after adapter Start/Seal, so a crash could lose replay identity and orphan the credential; a compensated Weixin waiting record had no durable terminal transition preventing Poll/Seal reuse | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 5 | `4d54acf` | `REVISE` | Credential code was blocked until Connector GO while Connector recovery already required the not-yet-authorized AbortSeal, creating a sequencing cycle; stale text also left cleanup solely with volatile Vault state | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 6 | `3c86739` | `REVISE` | Foundation used AbortSeal without Connector's required exact pre-active proof, so concurrent Weixin activation could make stale compensation delete an active credential; the roadmap also scheduled Connector twice | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 7 | `8ef063f` | `REVISE` | The roadmap still assigned AbortSeal to foundation, and legacy revoke could delete a credential without durable binding-transition proof; deferring that call also exposed public Delete as dead code | Context-isolated gatekeepers / 2026-08-20 |
| Credential contract review 8 | `b0884f6` | `GO` | Foundation exposes no public cleanup; private repository delete has a live reconciliation caller; ambiguous start/revoke retains credentials; Connector introduces Delete/AbortSeal only with exact durable barriers and live callers; bilingual roadmaps agree | Context-isolated gatekeeper / 2026-08-20 |
