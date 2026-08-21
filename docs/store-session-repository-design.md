# SessionRepository Migration Design

> Language: English | [简体中文](../zh-cn/docs/store-session-repository-design.md)

> Status: candidate for S3 SessionRepository design review, 2026-08-21.
> Implementation starts only after this exact bilingual design receives a
> recorded `GO`. Connector and the integrated Credential lifecycle received
> implementation `GO` at `a9a5ab9`.

## Scope

This wave migrates the six Session-owned Store methods frozen by S0, all three
backends, and every caller. It closes the current silent File/PostgreSQL
failure paths and makes session row plus lifecycle records atomic. S0 keeps
`DeleteSession` in SessionRepository even though that command removes records
owned by several later repositories; this design therefore treats deletion as
one explicit cross-record transaction rather than CRUD.

The wave does not migrate Conversation, Run, Document, Approval, Schedule,
External Chat, Browser State, Memory, Audit, or Artifact Metadata interfaces.
It does not split large Store files, change the File snapshot shape, add a
PostgreSQL migration, alter PostgreSQL CI/DSN behavior, add Runtime supervision,
or include ASR work.

## Repository Contract

```go
type SessionRepository interface {
    CreateSession(context.Context, string) (app.Session, error)
    CreateSessionWithScope(context.Context, string, string, string, string, bool) (app.Session, error)
    ListSessions(context.Context) ([]app.Session, error)
    GetSession(context.Context, string) (app.Session, bool, error)
    UpdateSessionTitle(context.Context, string, string) (app.Session, error)
    DeleteSession(context.Context, string) (app.Session, error)
}
```

`Store` embeds the interface exactly once. MemoryStore, FileStore, and
PostgresStore assert it directly. The six context-free signatures are removed
together; there is no compatibility adapter, optional type assertion, dynamic
repository lookup, or ignored error wrapper.

`CreateSession` remains the default-owner/default-webchat convenience method
because S0 froze it as a separately owned method. It and
`CreateSessionWithScope` share a private implementation but retain distinct
finite operation IDs and error attribution.

## Record Contract

Create generates a fresh `s_` ID before backend submission. Callers do not
supply IDs or lifecycle times. A blank title becomes `New SparkClaw Session`.
Owner and source are trimmed; blank owner becomes `app.DefaultOwnerID`, blank
source becomes `webchat`, and workspace root is trimmed. Nonblank title content
is otherwise preserved for compatibility. CreatedAt and UpdatedAt are equal,
UTC, and PostgreSQL-microsecond compatible.

Persisted sessions require a nonblank ID, owner, title, and source; an ID at
most 256 bytes; normalized owner/source/workspace fields; and nonzero UTC
creation/update times. UpdatedAt may not precede CreatedAt. Update changes only
the trimmed nonblank title and a repository-assigned UpdatedAt. The timestamp
uses a per-store high-water rule, so a clock rollback cannot move a session
behind its prior version.

List returns only `Hidden == false` sessions, ordered by UpdatedAt descending
and then ID ascending. Get remains ID-exact and may return hidden sessions.
Normal absence is `(zero, false, nil)` only for Get. Backend, scan, validation,
context, and row-iteration failures are never absence or an empty list.

File startup first applies the already shipped linked-MCP and linked-external-
chat compatibility normalization. It may normalize a blank legacy owner to the
default owner and a blank legacy source to `webchat`; no other malformed
session is repaired. Snapshot keys must equal embedded IDs. PostgreSQL runs its
existing adoption normalizers before validating every session row for
readiness.

## Protected Sessions

MCP bindings own the title and retained history of their linked visible
conversation. UpdateSessionTitle and DeleteSession return typed `conflict` for
a session whose normalized source is `mcp`. Gateway keeps its early public
check for clear UX, but repository enforcement prevents another caller from
bypassing the invariant. MCPRepository remains the only owner of MCP binding
cleanup and never calls ordinary Session deletion.

Hidden Telegram/Weixin sessions retain current behavior: ordinary public
listing excludes them, exact internal Get remains available, and an explicitly
authorized internal delete uses the same complete transaction.

## Lifecycle Atomicity

Create commits the session, one `session.created` audit, and one
`session.created` event atomically. Update commits the title/time, one
`session.updated` audit, and one `session.updated` event atomically. A lifecycle
write failure is a command failure, never a successful session mutation.

Delete returns the exact removed session and atomically removes all state S0
assigned to the command:

- the session and its messages;
- Agent runs, run feedback, model calls, tool calls, and episode summaries;
- document records and approvals scoped to the session;
- reminders plus their delivery rows;
- memory candidates and memories whose source run belongs to the session;
- browser login blocks;
- artifact metadata plus the in-memory URI index entries;
- linked external-chat sessions and their messages;
- PostgreSQL compatibility-source `weixin_chat_sessions` linked to the session
  and their `weixin_chat_messages`, before their canonical external-chat
  targets;
- the deleted session's old audit and event rows.

The same transaction appends one replacement `session.deleted` audit and event
outside the deleted session scope, carrying only the deleted session projection
and non-secret ID/title evidence. It does not delete delivery/receive records,
connector bindings, passive notifications, evaluations, browser auth records,
or unrelated records. Tests prove both complete target deletion and
cross-session isolation.

The PostgreSQL command explicitly deletes reminder deliveries before reminders,
legacy Weixin chat messages before legacy sessions and canonical external-chat
targets, and every foreign-key child before runs/session. It checks every
statement error and requires exactly one deleted session row. Removing both
legacy source and canonical target rows preserves the existing PostgreSQL
compatibility postcondition on the next restart. This also closes the current
backend divergence where Memory/File delete reminders but PostgreSQL may retain
them.

## Operation And Failure Contract

| Operation ID | Mode | Timeout | Reconciliation proof |
|---|---|---|---|
| `session.create` | write transaction | transaction | exact Get equals candidate |
| `session.create_with_scope` | write transaction | transaction | exact Get equals candidate |
| `session.list` | read | read | none |
| `session.get` | read/barrier | read | self |
| `session.update_title` | write transaction | transaction | exact Get equals candidate |
| `session.delete` | write transaction | transaction | exact Get absence plus complete delete-closure proof |

Caller deadlines always win; fallback bounds come from the existing Store
operation settings. Cancellation before admission/backend acquisition performs
no work. Memory checks context before and under its lock. File uses migrated
admission. PostgreSQL uses owned connections, explicit transactions, and a
domain-separated session-ID advisory barrier for Get and every mutation; List
uses one read transaction and checks `rows.Err()`.

Input/shape failures are `invalid`; missing update/delete targets are
`not_found`; protected MCP mutation is `conflict`; canceled/deadline work is
`canceled`/`timeout`; invalid persisted state is `corrupt`. File pre-submit
durability failure is `durability_failed`; an uncertain submitted replacement
is `unknown_outcome` behind the accepted fence.

PostgreSQL safe business/server rejections roll back and release. An unsafe
transport, statement, context, or commit failure after submission is
`unknown_outcome`; the session is terminated and not released. A rollback
failure terminates the session and preserves the primary, rollback, and
termination causes. `pgx.ErrNoRows` is normal only for Get and maps to typed
`not_found` for update/delete.

On unknown create/update, a nonzero returned candidate is evidence only; exact
Get equality proves commit. On unknown delete, the returned removed candidate
is evidence only. Reconciliation requires Get absence plus a complete backend-
private delete-closure proof behind the same barrier/fence. File reconciliation
matches the digest of the complete candidate snapshot, not only its session
map. PostgreSQL's absent Get path uses one internal closure query under the
session advisory barrier to prove that all canonical children and compatibility-
source Weixin rows assigned to delete are absent. Memory cannot produce an
unknown submitted outcome. This proof does not call still-unmigrated public
child list methods that may suppress backend errors. Any different, present,
incomplete, or unresolved read retains the original unknown outcome. No caller
reports a candidate as success or automatically retries before this proof.
Shared store helpers centralize these three reconciliation shapes rather than
duplicating ad hoc comparisons.

## Backend Implementation

Memory applies create/update/delete plus lifecycle/index changes under its one
lock and returns errors instead of silently succeeding. Session values contain
no mutable reference fields, but all outputs are still value copies.

File replaces all six `admitLegacy*`/`persist()` paths with migrated admission
and `runFileCommand`. Definite failure restores the complete pre-snapshot,
including event sequence, audit rows, artifact URI indexes, and deleted child
maps; timestamp high-water state does not roll back. Unknown rename/directory
sync installs the standard fence, and later session or other migrated reads
reconcile before observing state. Snapshot JSON remains byte-compatible in
shape.

PostgreSQL creates/updates/deletes the session and required lifecycle rows in
one transaction. It never uses `context.Background()`, ignored Exec results,
post-commit best-effort lifecycle appends, or a read-before-transaction delete.
Create and update use `RETURNING`; delete selects the protected target `FOR
UPDATE`, performs the dependency-ordered delete set, inserts replacement
lifecycle rows, and commits once.

No mechanical movement of unrelated methods is authorized. Narrow Session
contract/helpers/tests may be new files; existing large Store files are not
split as part of this wave.

## Caller Migration

Every production caller passes its request, run, worker, startup, or shutdown
context and handles typed errors. HTTP session handlers stop matching error
strings: invalid is 400, not_found is 404, conflict is 409, canceled is 408,
timeout is 504, and unavailable, durability, unresolved, corrupt, or internal
outcomes are 503. Every error response uses stable redacted copy and never
serializes an uncertain candidate or raw Store diagnostic. List, get, create,
update, and delete handlers cover this mapping matrix.

Gateway, Agent, ToolHub, ISCP Bridge, message control, delivery, Telegram, and
Weixin may temporarily keep broader consumer-owned composites until S4, but
their Session surface is the exact repository above. Creation failure prevents
later message/external-chat linking work in that call. Reads do not turn Store
failure into missing session, default owner/workspace, an empty catalog, or
continued execution.

Tests use `storetest` must-helpers where session setup is incidental. Production
does not receive a no-error convenience wrapper.

## Verification Gate

Implementation `GO` requires:

- exact interface/signature/operation/source guards, one Store embedding, and
  direct assertions for all three backends;
- Memory/File parity for normalization, hidden filtering, deterministic order,
  exact absence, protected MCP records, timestamp high-water, and atomic
  created/updated/deleted lifecycle rows;
- a complete delete fixture covering every S0-owned record/index plus a second
  session proving isolation, including PostgreSQL legacy Weixin source rows and
  a restart/readiness proof after deletion;
- deterministic File injection at encode, write, file sync, close, rename,
  directory open/sync/close, rollback, unknown fence, restart, and cancellation,
  with no Session `persist()` path;
- PostgreSQL fault coverage for acquisition, begin, barrier, select/scan,
  create/update, each delete statement, lifecycle inserts, rows affected,
  list iteration, commit, rollback, release, and termination;
- DSN-skipping real PostgreSQL round-trip/concurrent update-delete tests without
  CI service or `SPARKCLAW_TEST_POSTGRES_DSN` changes;
- migrated production caller error/context tests, complete HTTP mapping tests,
  and zero context-free Session calls outside compile-fail/source fixtures;
- affected package tests, full Go build/test/vet, focused Store/Gateway/Agent/
  connector race tests, unchanged WebChat tests/build, and bilingual docs CI.

## Commit Boundaries

1. This bilingual design and independent design gate.
2. Session contract, operations, three backend implementations, and repository
   contract/failure tests.
3. Production/test caller migration and public typed error mapping.
4. Independent implementation gate record.

No other repository migration, PostgreSQL schema change, Runtime supervision,
ASR CI work, or large-file split is mixed into these commits.

## Review Record

| Review | Revision | Decision | Evidence | Reviewer/date |
|---|---|---|---|---|
| SessionRepository design | pending | pending | pending | pending |
