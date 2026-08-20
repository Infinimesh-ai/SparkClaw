# Store Client Repository Design

> Language: English | [简体中文](../zh-cn/docs/store-client-repository-design.md)

> Status: S3 design candidate based on accepted Owner implementation
> `0b85cc4` and gate record `fc5acba`. Client code is not authorized until this
> exact contract receives a context-isolated design GO.

## Boundary Correction

S0 assigned nine legacy methods to `ClientRepository`. Production has only one
`SaveClient` caller: Gateway pairing saves a token-bearing client and then calls
`ClaimPairingCode`. Those two calls cannot make the client, pairing state, audit,
and events atomic. A failed claim can leave an orphan client, and PostgreSQL
currently discards either write failure.

This wave therefore merges legacy `SaveClient` and `ClaimPairingCode(id,
clientID)` into one `ClaimPairingCode(ctx, pairingID, client)` command. The old
standalone save signature is deleted rather than retained as a test-only API.
Client normalization remains a private backend helper. This reduces the current
Store method catalog from 141 to 140 without changing repository ownership or
migrating another repository.

## Interface

```go
type ClientRepository interface {
    GetClient(context.Context, string) (app.Client, bool, error)
    ListClients(context.Context) ([]app.Client, error)
    RevokeClient(context.Context, string) (app.Client, error)
    FindClientByTokenHash(context.Context, string) (app.Client, bool, error)
    TouchClient(context.Context, string) (app.Client, bool, error)
    SavePairingCode(context.Context, app.PairingCode) (app.PairingCode, error)
    GetPairingCode(context.Context, string) (app.PairingCode, bool, error)
    ClaimPairingCode(context.Context, string, app.Client) (app.PairingCode, app.Client, error)
}
```

`Store` embeds this interface once and removes all nine legacy signatures. Each
backend asserts `ClientRepository` independently. No dynamic repository lookup
or compatibility interface is introduced.

## Stable Semantics

- Every method composes the caller context with the accepted read, write, or
  transaction timeout. Cancellation and timeout remain typed errors.
- Ordinary absence is `(zero, false, nil)`. Empty IDs and an empty token hash
  are ordinary absence on reads. Backend, scan, decode, and row-iteration
  failures are never absence or an empty successful list.
- Lists order by `CreatedAt DESC`, then `ID ASC`.
- `LastSeenAt` and `RevokedAt` are cloned on input, output, snapshot capture,
  and snapshot load. No backend shares a mutable time pointer with a caller.
- Newly assigned times use UTC PostgreSQL microsecond precision. Existing
  persisted `CreatedAt` values are preserved exactly, including legacy File
  precision. Repository clocks use a per-record non-rollback high-water mark
  for revoke, touch, pairing creation, and claim candidates.
- Client IDs, owner IDs, actor IDs, names, and token hashes are trimmed. An
  empty client ID is generated; an empty owner defaults to
  `app.DefaultOwnerID`; an empty actor defaults to the owner. A new client
  requires a non-empty name and token hash. Client ID and non-empty token hash
  are unique.
- `FindClientByTokenHash` returns only a non-revoked client. `TouchClient`
  updates and returns only a still-active client; missing or concurrently
  revoked clients are ordinary absence. It emits no audit or event.
- `RevokeClient` requires an existing client. It assigns a fresh `RevokedAt`
  and atomically appends one `client.revoked` audit and event. A repeated revoke
  is a new explicit command with a later candidate, preserving existing
  behavior without reusing an uncertain candidate.
- Pairing IDs and code hashes are unique. `SavePairingCode` is create-only,
  accepts only a normalized pending record with no claim fields, and requires
  a non-zero expiry. The repository replaces caller `CreatedAt` with its own
  high-water time. It atomically appends `pairing_code.created` audit and event
  records. A duplicate ID or hash is `conflict`; a consumed code cannot be
  reopened by another save.
- `GetPairingCode` is side-effect free. A pending record whose expiry is in the
  past remains persisted as pending; callers evaluate both status and expiry.
  Claiming an expired code returns `conflict` without a hidden state change.
- `ClaimPairingCode` requires a present, pending, unexpired code and a new
  client ID/token hash. It assigns the client creation time and pairing claim
  time, then atomically creates the client, marks the code claimed, and appends
  `client.saved` followed by `pairing_code.claimed` audit/event pairs. No
  backend may expose or durably retain only part of that command.

## Operation Registry

| Operation ID | Mode | Timeout | Reconciliation |
|---|---|---|---|
| `client.get` | read | read | self/barrier |
| `client.list` | read | read | none |
| `client.revoke` | write | transaction | exact client candidate |
| `client.find_token_hash` | read | read | none |
| `client.touch` | write | transaction | exact active client candidate |
| `pairing_code.save` | write | transaction | exact pairing candidate |
| `pairing_code.get` | read | read | self/barrier |
| `pairing_code.claim` | write | transaction | exact pairing and client candidates |

The existing timeout configuration and PostgreSQL CI topology do not change.

## Durability And Outcome Rules

Memory applies the effective context before and under one lock. Each command
mutates all owned records and lifecycle entries under that lock.

File uses the accepted admission, full-snapshot rollback, atomic replacement,
unknown-outcome fence, and read reconciliation. Client and pairing commands use
`runFileCommand`; no legacy `persist()` path remains for these fields. Startup
validates map-key/embedded-ID equality, non-empty hash uniqueness, pairing
status/claim-field consistency, and claimed-code client references. Legacy
clients with an empty token hash remain loadable but can never authenticate.
File never normalizes corrupt persisted identity.

PostgreSQL commands acquire an owned connection and explicit transaction.
Client commands use a client-ID advisory lock; pairing commands use a
pairing-ID advisory lock, and claim also locks the generated client ID. Reads
used as resolution barriers explicitly use `READ COMMITTED`, take the matching
advisory lock in one statement, and query in a later statement. Unique
violations map to `conflict`; other server rejections map to `internal`.

An unsafe statement, transport, context, or commit failure after connection
acquisition returns `unknown_outcome`, terminates the owned session, and never
releases it. Before candidate formation it returns zero candidates. Safe-to-
retry and server-rejected failures roll back and return definite typed errors;
a rollback failure terminates without release and retains every cause.

Reconciliation succeeds only on exact persisted-field equality. Pair creation
is create-only and claim is single-use, so exact candidates behind the matching
barrier prove the complete atomic transaction. Revoke and touch candidates use
non-rollback high-water times. A different record, absence, or read failure
remains unresolved and is never automatically retried or reported as success.

## Gateway Migration

- Every call uses `r.Context()` or the owned authentication request context.
- Client list failures return stable 504 timeout or 503 unavailable copy;
  revoke preserves 404 only for typed `not_found`.
- Pairing start never discloses the plaintext code until the exact saved
  pairing candidate is durable or reconciled.
- Pairing claim performs one repository command and never discloses the bearer
  token until the pairing and client candidates are durable or reconciled.
- Bearer authentication distinguishes invalid/revoked credentials from Store
  failure. Invalid or revoked stays 401; lookup/touch timeout is 504 and other
  Store failure is 503. Touch must confirm the client is still active.
- No migrated Client path uses `context.Background()` or ignores a repository
  error.

## Gate And Commit Boundary

The implementation is one behavior commit after this bilingual design commit.
Its gate requires:

- shared Memory/File success, absence, order/tie-break, pointer clone,
  cancellation, timeout, uniqueness, revoke/touch, expiry, and atomic claim
  tests;
- File rollback/fence/reconciliation/restart/encryption/failure-injection and
  race evidence covering clients, pairing codes, audit, and events;
- PostgreSQL statement/commit classification, terminate-not-release, explicit
  barrier, query/scan/rows propagation, unique conflicts, and atomic rollback;
- Gateway no-code/no-token-disclosure tests, atomic claim tests, safe error
  projection, authentication Store-failure tests, and owned-context evidence;
- source guards for one embedded repository, removal of `SaveClient` and all
  legacy signatures, no ignored Client errors, and no migrated background
  contexts;
- full Go test/build/vet, focused Store race, default File entry tests, WebChat
  test/build, 44 Python script tests, default Compose, bilingual docs CI, and a
  disposable configured real-PostgreSQL full plus race run.

No Credential or Session design starts until the exact Client implementation
receives an independent context-isolated GO.

## Review Record

| Review | Revision | Decision | Evidence | Reviewer/date |
|---|---|---|---|---|
| Client contract | pending | pending | boundary, atomicity, outcomes, callers, and gate | pending |
