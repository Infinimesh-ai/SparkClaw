# Store Client Repository Design

> Language: English | [简体中文](../zh-cn/docs/store-client-repository-design.md)

> Status: S3 Client design accepted at `fba19a6` on 2026-08-20 after four
> repaired candidates. Client implementation is authorized against this exact
> contract and still requires an independent context-isolated implementation
> GO before the next repository starts.

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
- Client `LastSeenAt`/`RevokedAt` and pairing `ClaimedAt` are cloned on input,
  output, event payload, snapshot capture, and snapshot load. No backend shares
  a mutable time pointer with a caller or another stored record.
- Newly assigned times use UTC PostgreSQL microsecond precision. Existing
  persisted `CreatedAt` values are preserved exactly, including legacy File
  precision. Repository clocks use a per-record non-rollback high-water mark
  for revoke, touch, pairing creation, and claim candidates. Each backend
  initializes those marks from client created/last-seen/revoked and pairing
  created/claimed timestamps, never from the future expiry deadline.
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
  trims its ID and code hash, accepts an empty or pending input status and
  normalizes it to pending, rejects claim fields or another status, and requires
  a non-zero expiry and code hash; an empty ID is generated. `ExpiresAt` is
  normalized to UTC PostgreSQL microsecond precision, while the repository
  replaces caller `CreatedAt` with its own high-water time. It atomically
  appends `pairing_code.created` audit and event records. A duplicate ID or hash
  is `conflict`; a consumed code cannot be reopened by another save.
- `GetPairingCode` is side-effect free. A pending record whose expiry is in the
  past remains persisted as pending; callers evaluate both status and expiry.
  Claiming an expired code returns `conflict` without a hidden state change.
- `ClaimPairingCode` requires a present, pending, unexpired code and a new
  active client ID/token hash. It rejects a client carrying `LastSeenAt` or
  `RevokedAt`, ignores caller `CreatedAt`, and assigns the client creation time
  and pairing claim time as one command timestamp strictly above both record
  high-water marks. It then atomically creates the client, marks the code
  claimed, and appends
  one `client.saved` and one `pairing_code.claimed` audit/event pair. Event
  sequence is strictly client-saved then pairing-claimed. Audit rows form an
  unordered atomic set because the Audit contract has no sequence column; tests
  compare their exact types and shared command timestamp, not equal-time row order. No
  backend may expose or durably retain only part of that command.

### Deterministic Method Outcomes

Every backend applies the following precedence. It first applies and checks the
effective operation context. It then trims every standalone string argument and
normalizes candidate fields exactly as specified above. Candidate structural
validation precedes target lookup; target lookup and persisted-row validation
precede state and uniqueness checks. A persisted invariant violation outside an
explicit compatibility exception is always `corrupt` and cannot be hidden by a
later business result. Within the final business step, state preconditions
precede uniqueness checks. These rules make a request with multiple defects
produce the same result in Memory, File, and PostgreSQL.

| Method or condition | Exact repository outcome |
|---|---|
| `GetClient` / `GetPairingCode` with an empty ID, or no matching row | `(zero, false, nil)` |
| `FindClientByTokenHash` with an empty hash, no match, or only a revoked match | `(zero, false, nil)` |
| `ListClients` success with no rows | non-nil empty slice and `nil` error |
| `RevokeClient` with an empty or missing ID | `not_found` |
| `TouchClient` with an empty or missing ID, or a client revoked before the command lock | `(zero, false, nil)` |
| `SavePairingCode` with invalid status, claim fields, zero expiry, or empty normalized hash | `invalid` |
| `ClaimPairingCode` with an invalid normalized client shape, including empty name/hash or supplied last-seen/revoked fields | `invalid` |
| `ClaimPairingCode` with an empty or missing pairing ID after valid client normalization | `not_found` |
| Pairing save with a duplicate non-empty pairing ID/hash, or claim with a duplicate non-empty client ID/hash | `conflict` |
| Claim of a non-pending code, a code whose expiry is at or before the command clock, or a compatibility-valid pending row with an empty legacy code hash | `conflict` |
| Any scanned row that violates the compatibility matrix outside its explicit blank-field exceptions | `corrupt` |

Cancellation, timeout, transport, durability, unknown-outcome, and internal
failures retain their operation-level typed results and never collapse into a
business outcome above. Definite failures return the zero candidates already
specified by the durability rules.

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
uses this explicit compatibility matrix:

- client and pairing map keys must equal non-empty embedded IDs;
- a legacy blank client owner becomes `app.DefaultOwnerID`, and a blank actor
  becomes that owner, matching the already-shipped schema backfill;
- a legacy blank client name/token hash or pairing code hash remains loadable,
  but an empty hash never authenticates or claims and duplicate empty hashes are
  permitted. PostgreSQL's existing unique constraints mean it can contain at
  most one blank of each hash, while File may preserve more than one;
- non-empty client token hashes and pairing code hashes must be unique;
- client creation time is non-zero, and every present client last-seen/revoked
  or pairing claimed pointer contains a non-zero time. Legacy clock rollback is
  legitimate, so startup does not invent chronological ordering constraints
  among those non-zero values;
- pairing status is exactly pending, claimed, or expired. Pending/expired rows
  have no claim time/client; claimed rows require both and reference a present
  client; and pairing creation/expiry times must be non-zero.

All accepted pointer values are cloned. No other identity, status, relation, or
time is normalized; a violation is corrupt startup state.

PostgreSQL commands acquire an owned connection and explicit transaction.
Client commands use a client-ID advisory lock; pairing commands use a
pairing-ID advisory lock, and claim also locks the generated client ID. Reads
used as resolution barriers explicitly use `READ COMMITTED`, take the matching
advisory lock in one statement, and query in a later statement. Unique
violations map to `conflict`; other server rejections map to `internal`.

Before readiness, PostgreSQL scans every client and pairing row through the same
compatibility validator, including claimed-client existence. Legacy blank
owner/actor fields receive only the projection backfill above; the GET remains
read-only. Every later row scan validates through that same function, including
get/list/find results and revoke/touch/claim command preconditions. An invariant
violation maps to `corrupt`, never absence, conflict, or a normalized write.
This runtime validation is the PostgreSQL equivalent of File startup
validation; no schema migration or CI service-topology change is required.

An unsafe statement, transport, context, or commit failure after connection
acquisition returns `unknown_outcome`, terminates the owned session, and never
releases it. Before candidate formation it returns zero candidates. Safe-to-
retry and server-rejected failures roll back and return definite typed errors;
a rollback failure terminates without release and retains every cause.

After candidate formation, unknown pairing save returns its normalized pairing
candidate, unknown claim returns both normalized candidates, and unknown
revoke/touch returns its client candidate. Definite failures return zero
candidates except for typed business results explicitly stated above.

Reconciliation succeeds only on exact persisted-field equality. Pair creation
is create-only and claim is single-use, so exact candidates behind the matching
barrier prove the complete atomic transaction. Revoke and touch candidates use
non-rollback high-water times. A different record, absence, or read failure
remains unresolved and is never automatically retried or reported as success.

## Pending Secret Coordinator

Gateway owns a capacity-one pairing coordinator covering both start and claim.
It is process-local and never persists, logs, audits, traces, or projects a
plaintext code/token.

The coordinator serializes each complete transition: inspect or install
pending intent, invoke the repository, attach every returned candidate,
reconcile, and either retain, disclose, or clear the secret. Each installed
intent has a monotonically increasing process-local generation. Its timer may
clear only that generation, so a late callback can never clear a replacement.
The timer also selects on the lifecycle context bound by
`gatewayServices.Start`; lifecycle cancellation clears the matching generation
and exits without another Store call.

- Before `SavePairingCode`, Gateway generates a non-empty pairing ID and code
  hash, then start stores the plaintext code, owner, canonical request
  fingerprint (exactly the normalized owner ID), expiry, and complete attempted
  pairing command identity while holding the coordinator gate. Repository ID
  generation remains supported for other callers but is never used by this
  flow. The repository-returned
  normalized candidate is attached before success or unknown reconciliation.
  Unknown save immediately runs the get barrier with the retained attempted ID.
  Unresolved reconciliation retains pending; the same later start reconciles
  and can receive the original code, while a different request gets conflict.
  No second code/save is generated while pending exists. A zero-candidate
  unknown can prove only rollback through barrier absence; any present record
  is non-matching and therefore conflicts rather than disclosing the code.
- After validating the submitted pairing code but before repository claim,
  Gateway generates a non-empty client ID and token hash. Claim stores the
  plaintext bearer token, complete attempted client command identity, canonical
  fingerprint of owner, pairing ID, submitted code hash, and normalized client
  name, plus the pre-command pairing record and expiry. Repository client-ID
  generation remains supported for other callers but is never used by this
  flow. The repository-returned normalized pairing/client candidates are
  attached before success or unknown reconciliation. A later request for the
  same pairing first repeats the constant-time comparison against the retained
  pre-command code hash; an invalid code remains unauthorized. Only then may an
  exact fingerprint reconcile and recover the original token. A different valid
  request conflicts, and no second token/client/claim is generated while
  pending. A zero-candidate unknown uses the retained attempted client ID for
  the absence barrier and can never infer it from the repository result.
- A definite failure clears the matching pending secret. For start, barrier
  absence proves rollback. For claim, the exact pre-command pending pairing
  together with client absence proves rollback. Either result clears pending
  and reports persistence failure without an automatic retry. Exact command
  candidates complete; a different state clears pending and reports conflict;
  an unresolved backend error retains pending.
- Completion reads the live Gateway clock immediately before disclosure. At or
  after expiry it clears the plaintext and returns an expired result. A pending
  claim that committed but expires before recovery can leave a visible active
  client whose random token was never disclosed; the owner remediates it through
  the existing client list/revoke surface. It is not silently retried or hidden.
- An owned expiry timer clears the pending plaintext at the pairing expiry even
  without another request. Replacement/completion stops the prior timer, and
  Gateway lifecycle cancellation clears pending and stops it. A process crash
  or response loss can still leave an undisclosed client because plaintext is
  intentionally not durable. Startup cannot distinguish that client from one
  whose response was delivered, so it does not guess and revoke; the same
  visible remediation applies.

## Gateway Migration

- Every call uses `r.Context()` or the owned authentication request context.
- Client list timeout is 504 `client list request timed out`; every other list
  error is 503 `clients are temporarily unavailable`.
- Revoke `not_found` is 404 `client not found`, timeout is 504
  `client revoke request timed out`, and every other typed or untyped error is
  503 `clients are temporarily unavailable`. In particular, canceled,
  conflict, invalid, unavailable, durability, unknown-outcome, corrupt, and
  internal failures never inherit 404 and never expose their cause.
- Pairing start never discloses the plaintext code until the exact saved
  pairing candidate is durable or reconciled and a live completion clock still
  precedes `ExpiresAt`.
- Pairing claim performs one repository command and never discloses the bearer
  token until the pairing and client candidates are durable or reconciled and
  the live completion clock still precedes the pairing expiry.
- Bearer authentication distinguishes invalid/revoked credentials from Store
  failure. Invalid, missing, or concurrently revoked credentials stay 401
  `valid bearer token required`. Lookup/touch timeout is 504
  `authentication request timed out`; every other lookup/touch error is 503
  `authentication is temporarily unavailable`, including canceled, not-found,
  conflict, invalid, unavailable, durability, unknown-outcome, corrupt,
  internal, and untyped errors. Touch must confirm the client is still active.
- Existing bootstrap validation remains stable: disabled pairing is 400
  `pairing is not required`, non-local start is 403
  `pairing can only be started locally`, malformed claim JSON is 400
  `invalid pairing request`, absent claim is 400 `pairing code not found`,
  non-pending/expired claim is 400
  `pairing code is not active`, and a code mismatch is 401
  `invalid pairing code`. A different pending fingerprint is 409
  `another pairing request is pending`; a different reconciled state is 409
  `pairing state changed`. Start expiry immediately before disclosure is 503
  `pairing is temporarily unavailable`; claim expiry at that point is the same
  stable 400 inactive result. Any Store timeout in start, claim, or
  reconciliation is 504 `pairing request timed out`; definite persistence
  failure, unresolved unknown outcome, canceled operation whose response remains
  writable, corruption, internal failure, or other Store failure is 503
  `pairing is temporarily unavailable`. No response contains raw Store errors,
  candidates, code/token hashes, or plaintext secrets.
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
- pending start/claim immediate and next-request reconciliation, same-request
  secret recovery, different-request conflict, no second generation/command,
  live-clock expiry, cleanup, and process-loss orphan/remediation evidence;
- File/PostgreSQL compatibility-matrix and corrupt-row tests, pairing
  `ClaimedAt` alias tests, event sequence tests, and unordered audit-set tests;
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
| Client contract review 1 | `bae3623` | `REVISE` | Unknown claim could lose the bearer token; legacy/corrupt backend parity, pairing pointer isolation, audit ordering, and live expiry disclosure were incomplete | Context-isolated gatekeeper / 2026-08-20 |
| Client contract review 2 | `9ff7c14` | `REVISE` | Zero-candidate recovery lacked attempted command identities; pairing public error projection was not frozen; roadmap status was stale | Context-isolated gatekeeper / 2026-08-20 |
| Client contract review 3 | `1ccd5db` | `REVISE` | Client list/revoke/auth and malformed-claim public copy remained incomplete; repository roadmap still labeled Owner active | Context-isolated gatekeeper / 2026-08-20 |
| Client contract review 4 | `b33343a` | `REVISE` | Deterministic typed outcomes and validation precedence remained incomplete across repository methods and legacy blank-hash pairing rows | Context-isolated gatekeeper / 2026-08-20 |
| Client contract review 5 | `fba19a6` | `GO` | Exact outcomes and precedence are frozen; atomicity, recovery, backend compatibility, public mappings, and implementation evidence are complete and coherent | Context-isolated gatekeeper / 2026-08-20 |
