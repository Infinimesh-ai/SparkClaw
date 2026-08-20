# Store Credential Repository Design

> Language: English | [简体中文](../zh-cn/docs/store-credential-repository-design.md)

> Status: S3 design candidate, 2026-08-20. No CredentialRepository code is
> authorized before an independent context-isolated design GO.

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
    SaveCredentialSecret(context.Context, app.CredentialSecret) (app.CredentialSecret, error)
    GetCredentialSecret(context.Context, string) (app.CredentialSecret, bool, error)
    DeleteCredentialSecret(context.Context, string) error
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
- New saves require non-empty ref, kind, and value. Store never generates a
  credential ref and ignores caller `CreatedAt`/`UpdatedAt`.
- A new ref receives one UTC PostgreSQL-microsecond command timestamp for both
  `CreatedAt` and `UpdatedAt`. An overwrite preserves the existing
  `CreatedAt` exactly and assigns a strictly newer `UpdatedAt` from a
  per-ref non-rollback high-water mark.
- Save is an explicit upsert. It atomically persists the complete secret and
  exactly one `credential_secret.saved` audit containing only ref and kind.
  It emits no general event.
- Get is side-effect free. Empty or missing refs are ordinary
  `(zero, false, nil)` absence. Backend, scan, decode, validation, or context
  failures are never absence.
- Delete requires a present ref. Missing is typed `not_found`. Success
  atomically removes the secret and appends exactly one
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
| Save with empty normalized ref/kind or empty value | `invalid` |
| Delete with empty/missing ref | `not_found` |
| Save new ref or overwrite valid existing ref | persisted normalized candidate |
| Backend/scan/decode failure | typed non-absence error |
| Persisted invariant violation outside compatibility rules | `corrupt` |
| Unsafe submission/commit result | `unknown_outcome` with the save candidate only for Save |

Definite save/delete failures return no candidate or secret value. Store error
strings and public projections never include ref values supplied by an
untrusted caller, kinds, opaque values, SQL, snapshot bytes, or backend causes.

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
| `credential_secret.delete` | write | transaction | final absence behind ref barrier |

Memory applies context before and under one lock. Secret mutation and audit
append happen under that lock.

File uses accepted migrated admission and `runFileCommand`; no credential path
calls legacy `persist()`. A command snapshots the complete pre-state, commits
one encoded replacement, restores all secret/audit/high-water state on definite
failure, and installs the accepted fence on an unknown rename/directory-sync
outcome. Reads cannot cross that fence. Startup validates the private secret
wire representation before loading Memory.

PostgreSQL save/delete acquire a context-aware capacity-one process admission
for high-water ownership, then an owned connection and explicit transaction.
They take a ref-derived advisory transaction lock, read the current row, form a
candidate, and write secret plus audit in that transaction. Resolution reads
use explicit `READ COMMITTED`, take the same advisory lock in one statement,
and query in a later statement. Server rejections are definite `internal`
except applicable uniqueness/business codes; safe-to-retry transport failures
are definite. Unsafe statement, context, or commit outcomes after connection
acquisition terminate without release and return `unknown_outcome`. Rollback
failure also terminates without release while retaining every cause.

Save reconciliation succeeds only when every persisted field equals the exact
candidate. Delete reconciliation succeeds only on final absence. A different
row or failed barrier remains unresolved and is never reported as success.

## Vault And Consumer Migration

`credential.Vault` propagates its method context into every repository call and
maps typed Store failures to its stable seal/unseal errors without string
matching. `Delete` treats only typed `not_found` as idempotent success.

Seal keeps a capacity-one in-memory pending coordinator for an unresolved save.
It retains only a plaintext fingerprint plus the sealed candidate, never the
plaintext. The same logical request reconciles and recovers the original ref;
a different request must first resolve and, when necessary, delete an orphaned
committed candidate before generating another ref. Immediate success or final
absence clears pending state. Gateway lifecycle cancellation clears volatile
pending material. Repository candidates and errors never cross public APIs.

All new Weixin QR credentials use `CredentialVault.Seal`; Gateway assigns the
returned durable ref to the binding only after Seal succeeds. Notification and
Syncer use `CredentialVault.Open` with their owned request/work context instead
of reading `CredentialSecret.Value`. Static operator-configured Weixin tokens
remain configuration inputs and do not enter Store.

Gateway must check credential cleanup errors. Binding-start compensation and
binding revocation may return a stable unavailable response after the binding
operation has completed; retry repeats idempotent Vault deletion. No raw Store,
Vault, envelope, ref, token, or backend error is returned. ConnectorRepository
will later decide whether binding plus credential lifecycle becomes one
cross-repository command.

## Gate And Commit Boundary

Implementation follows this accepted design in separate reviewable commits:

1. repository behavior across interface, operations, three backends, private
   File wire format, and compatibility validation;
2. Vault reconciliation, legacy Weixin rewrap, consumer/context migration, and
   safe Gateway projection; and
3. any independent File encrypted-envelope fail-closed defect fix, if kept
   separate from repository behavior for bisect clarity.

The implementation gate requires:

- shared Memory/File success, overwrite, absence, validation precedence,
  timestamps/high-water, cancellation/timeout, audit atomicity, and redaction;
- File rollback, fence, final reconciliation, encrypted/plain restart, corrupt
  state, encrypted-without-key rejection, failure injection, and race evidence;
- PostgreSQL acquire/begin/statement/commit/rollback classification,
  terminate-not-release, context-aware admission, barrier isolation, scan
  propagation, atomic audit, startup validation, and real-DSN evidence;
- Vault save/delete immediate and next-call reconciliation, no second ref/value
  generation, pending cleanup, safe typed errors, wrong-key failure, legacy
  Weixin rewrap, and non-Weixin plaintext rejection;
- Gateway/Notification/Syncer tests proving no active binding on failed Seal,
  no raw Store access, owned context propagation, cleanup error handling, and
  no token/ref/error disclosure;
- source guards for one embedded repository, exact signatures and three
  implementations, no legacy methods, no ignored result, no migrated
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
| Credential contract review 1 | pending | pending | pending | pending |
