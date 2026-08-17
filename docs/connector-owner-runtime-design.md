# Per-Owner Connector Activation And Runtime Reconciliation

> Language: English | [简体中文](../zh-cn/docs/connector-owner-runtime-design.md)

> Status: accepted and implemented for
> [issue #13](https://github.com/Infinimesh-ai/SparkClaw/issues/13) on
> 2026-08-17. The six product decisions are recorded in
> [Resolved decisions](#resolved-decisions).

## Decision Summary

SparkClaw keeps `ConnectorSetting` as an owner-scoped durable opt-in. A setting
for one owner must control that owner's inbound polling, endpoint visibility,
binding setup, and outbound delivery without changing another owner's access.

The physical Telegram and Weixin workers remain one shared process per channel.
Both current implementations scan all active bindings and own channel-wide
polling, concurrency, inbox, and cursor coordination. Starting the same worker
once per owner would duplicate polling and dispatch. Instead, the Connector
Registry reconciles a channel worker from the aggregate desired state and gives
the worker an owner-aware gate which it must apply before acquiring or
dispatching work.

The process-local Connector Registry becomes the read authority for effective
owner activation after startup. It loads every persisted `ConnectorSetting`
before starting workers and refreshes the cache after each successful
compare-and-swap update. Delivery and endpoint resolution must not perform a
synchronous Store read for each message.

`/api/config` continues to return the owner-effective `enabled` value and must
return the static channel configuration value in `operator_enabled`; it must no
longer replace that value with `true` merely because a Connector Controller is
installed. Despite the legacy public field name, this static value is a default
for owners without a persisted setting, not a non-overridable operator gate.

The supported deployment is one household Gateway with logical owner
isolation. Owner settings, bindings, endpoints, and delivery authorization must
not cross owner boundaries, but the process is not a hostile-tenant security
boundary: household owners share one Gateway process, Store, operator, and
channel worker pool.

The credential-vault warning is out of scope because commit `e752c55` already
made it independent of the static Telegram flag.

## Problem

The current Registry persists `(owner_id, channel)` settings but manages
runtime workers only by `channel`:

- `Start()` checks only `app.DefaultOwnerID`, so another owner's persisted
  enablement is not replayed after restart.
- disabling one owner cancels the channel worker used by every owner;
- Telegram and Weixin workers enumerate all active bindings without checking
  the owning user's current ConnectorSetting;
- `Enabled()` calls `GetConnectorSetting` synchronously on endpoint and
  delivery hot paths; and
- the public config projection overwrites `operator_enabled` with `true` when
  managed Connector status is available.

This combines owner-scoped desired state with channel-scoped process state and
therefore produces both cross-owner interference and silent restart failures.

## Invariants

1. A ConnectorSetting belongs to exactly one normalized owner and one
   normalized registered channel.
2. Owner A disabling a channel immediately blocks new endpoint resolution,
   binding setup, and outbound delivery for A, without stopping those paths for
   owner B.
3. A shared channel worker runs at most once in the process.
4. A shared worker may acquire or dispatch work for an owner only when the
   Registry's current owner gate permits it.
5. Restart loads all persisted owner settings before the first runtime
   reconciliation.
6. Store updates remain compare-and-swap writes. A conflict cannot change the
   cache or runtime state.
7. Bindings and encrypted credentials are retained when an owner disables a
   connector.
8. Delivery and endpoint hot paths read process memory only after Registry
   startup.
9. A disabled owner does not learn that a channel worker is running for another
   household owner through `ConnectorStatus.Running` or `LastError`.
10. The default file backend, memory backend, and PostgreSQL backend implement
    the same ConnectorSetting enumeration contract.
11. Household owners are logically isolated inside one trusted Gateway; this
    design does not claim process or infrastructure isolation between hostile
    tenants.

## State Model

For owner `o` and channel `c`, define:

- `configured(c)`: the static `NotificationChannelConfig.Enabled` value;
- `persisted(o,c)`: the optional durable ConnectorSetting; and
- `effective(o,c)`: the owner-specific value used by binding, endpoint,
  delivery, and runtime gates.

The selected compatibility model is:

```text
effective(o,c) = persisted(o,c).enabled, when a record exists
                 configured(c), otherwise

runtime_wanted(c) = configured(c)
                    OR any persisted owner has enabled=true
```

This lets static configuration remain the default for an owner without a
persisted choice, while any explicit owner opt-in can start a default-off
channel. An explicit opt-out still blocks that owner even when the shared
worker remains alive for a configured default or another owner.

This formula is authoritative for the current single-Gateway runtime.

## Ownership And API Changes

### Store

Add an explicit all-owner enumeration operation rather than assigning magic
meaning to an empty owner ID. Existing code normalizes an empty owner to
`DefaultOwnerID`, so overloading `ListConnectorSettings("")` would be ambiguous.

The new operation returns all ConnectorSettings in stable `(owner_id, channel)`
order and reports query failure distinctly from an empty result. It must be
implemented by:

- `MemoryStore`, from the existing ConnectorSettings map;
- `FileStore`, as a read-through to its MemoryStore snapshot; and
- `PostgresStore`, with one ordered query over `connector_settings`.

No new Snapshot field or SQL schema migration is required because the records
already exist. Store contract and backend parity tests are required.

### Connector Registry Cache

The Registry owns a dedicated settings lock and a map keyed by normalized
`ownerID + channel`. A cached record and a missing record are distinct because
an explicit `enabled=false` must override the configured default.

Startup performs one all-owner load, atomically installs the snapshot, and only
then reconciles registered channel workers. `Enabled(ownerID, channel)` reads
the cache and falls back to static configuration for a known channel with no
record. Tests which use a Registry without calling `Start` may use a serialized
lazy fill, but the production hot path is fully seeded before the HTTP server
begins listening.

`SetEnabled` and `SetMCPTransports` serialize the record read and CAS update
against cache fills. After a successful Store update they install the returned
record in the cache before reconciliation or status projection. Failed writes
leave the old cache untouched. The Registry is the only production writer for
ConnectorSetting; direct Store writes after startup are unsupported because
they cannot invalidate process state.

No TTL is proposed. A TTL would restore periodic synchronous PostgreSQL reads
without solving cross-process writer consistency. If multiple Gateway writers
are later supported, ConnectorSetting needs an explicit invalidation stream or
revision protocol rather than polling on message paths.

### Shared Runtime Scope

The runtime contract carries both the owner gate and the Gateway lifecycle:

```go
type RuntimeScope struct {
    Channel          string
    OwnerEnabled     func(ownerID string) bool
    LifecycleContext context.Context
}

type Runtime interface {
    Run(acquisitionContext context.Context, scope RuntimeScope) error
}
```

The concrete scope is created by the Registry for one fixed channel. It exposes
no Store and cannot inspect or mutate another connector's state.

Telegram applies `OwnerEnabled` when building its polling binding list and
again before dispatching a persisted inbox item. Weixin applies it when
building each Tick's binding list and again before dispatching an acquired
batch. The second check closes the race in which an owner disables a connector
after polling but before Agent dispatch.

MCP has no registered polling Runtime and continues to use the same owner gate
in its endpoint and delivery paths.

### Runtime Reconciliation

`runtimeRuns` and `runtimeErrors` remain keyed by channel. This matches the
physical worker ownership and prevents duplicate provider polling.

After startup or a successful setting change, `reconcileChannel(channel)`
recomputes `runtime_wanted(channel)` from the in-memory snapshot:

- false to true starts the registered worker once;
- true to true keeps the current worker and lets its owner gate observe the new
  cache value;
- true to false cancels the worker; and
- false to false does nothing.

Cancellation stops acquisition, but the run remains registered while its
already-admitted work drains. If an owner re-enables the channel during that
interval, reconciliation does not start a parallel worker. When the old run
returns, the Registry re-evaluates the latest aggregate desired state and
starts one replacement if still wanted. Gateway shutdown cancels both the
acquisition and lifecycle contexts, so process shutdown remains the hard stop.

The settings lock is not held while starting, stopping, or waiting on a worker.
The reconciliation method re-reads the latest aggregate state, so concurrent
updates for distinct owners converge on the newest snapshot instead of applying
a stale reference count.

Unexpected worker exit remains a channel-level failure shared by enabled
owners. Automatic restart policy is outside issue #13; the existing error
behavior remains unless separately designed.

### Status And Public Configuration

`ConnectorStatus.Enabled` is the calling owner's effective state.
`ConnectorStatus.Running` is true only when that owner is enabled and the
shared channel worker is running. `LastError` is projected only to enabled
owners. This prevents an opted-out owner from observing another owner's worker
activity while preserving useful channel-level failure information for owners
who depend on it.

For `/api/config`:

- `enabled` is the calling owner's effective state;
- `operator_enabled` is copied from `NotificationChannelConfig.Enabled` and is
  never overwritten by controller presence; and
- `available`, `binding_status`, `startable`, and `disabled_reason` continue to
  come from the owner-scoped Connector status.

No frontend behavior change is required by the issue because WebChat currently
types but does not consume `operator_enabled`. A focused API projection test is
still required.

## Disable Boundary

All newly resolved outbound work is blocked immediately by the existing owner
gate. Inbound runtimes stop acquiring new provider updates for the disabled
owner on their next binding selection and re-check before Agent dispatch.

Work already dispatched before the setting update continues through its Agent
turn and exact source-reply delivery. The inbound adapter freezes a
`SourceAdmitted` marker on that return route; endpoint and Provider layers still
recheck binding identity and revocation, but do not retroactively apply the
later owner opt-out to that one admitted reply. Only Telegram, Weixin, and MCP
ingress create this marker; a newly constructed send without source admission
remains blocked.

Provider work persisted but not yet dispatched stays pending. Telegram skips
the disabled owner's inbox records without changing their state. Weixin leaves
the provider cursor before the skipped batch. Re-enabling the owner resumes
those records. This is suspension by eligibility, not deletion or a terminal
cancellation state, and the acquisition loops remain bounded rather than busy
polling the suspended owner.

## Startup Failure

An all-owner PostgreSQL load must not be mistaken for an empty Store. The new
enumeration operation therefore returns an error, and Gateway startup fails
with a non-zero process status before the HTTP listener opens. Falling back to
static defaults could enable or disable inbound channels contrary to durable
owner choices.

## Compatibility And Migration

- Existing ConnectorSetting records and versions are reused unchanged.
- Existing file snapshots require no rewrite.
- Existing PostgreSQL tables require no migration.
- An owner without a record uses the static configuration as its default.
- The runtime remains one process per channel, so provider cursors and bounded
  worker pools are not duplicated.
- Binding, credential, and inbox records are not deleted or reassigned.
- The public `operator_enabled` value changes from a controller-presence
  constant to the configured value; this behavior change is recorded in both
  Changelogs.

## Verification Matrix

### Store and cache

- Memory, file reload, and PostgreSQL integration list every owner in stable
  order and distinguish query failure from an empty set.
- Explicit false is cached as a record and overrides the configured default.
- Repeated `Enabled`, endpoint lookup, and delivery checks do not call
  `GetConnectorSetting` after startup.
- Successful CAS refreshes the cache; a stale CAS conflict changes neither
  cache nor worker state.
- `SetMCPTransports` preserves `Enabled` and refreshes the complete cached
  record.

### Lifecycle and isolation

- With static default false, a persisted non-default owner enablement starts
  the channel once after restart.
- Two enabled owners still start exactly one worker.
- Disabling owner A keeps the worker alive and owner B eligible.
- Disabling the last enabled owner stops a default-off channel.
- A newly enabled owner becomes eligible without restarting an already-running
  worker.
- Telegram and Weixin skip disabled-owner bindings at acquisition and re-check
  before dispatch.
- Race-focused tests cover simultaneous owner updates, `Enabled` reads, worker
  exit, and Gateway cancellation.

### API and behavior

- Connector list/status remains owner-scoped.
- Disabled owner A cannot resolve an endpoint or deliver through owner B's
  active runtime.
- `/api/config.operator_enabled` equals the static channel value for both true
  and false configurations while `enabled` reflects the calling owner.
- Changelog, architecture, messaging/integration guide, and Chinese mirrors are
  updated with the final semantics.

### Commands

After document-tool setup, implementation validation includes:

```bash
cd services/gateway && go test ./internal/store ./internal/connector ./internal/telegram ./internal/weixin ./internal/messagecontrol ./internal/gateway ./cmd/sparkclaw
cd services/gateway && go test -race ./internal/connector ./internal/telegram ./internal/weixin ./internal/messagecontrol
cd services/gateway && go test ./...
cd services/gateway && go vet ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
```

The repository's bilingual Markdown mirror and local-link checks also gate the
change. The default file backend and PostgreSQL product configuration both need
coverage; a memory-only proof is insufficient.

## Resolved Decisions

1. `NotificationChannelConfig.Enabled` is the default for an owner without a
   persisted setting. An owner setting overrides it.
2. Work already dispatched when an owner disables the connector finishes and
   delivers its exact admitted source reply. Gateway shutdown still cancels it.
3. Persisted but not-yet-dispatched provider work is retained and resumes after
   re-enable; disable does not terminally cancel it.
4. Failure to preload all owner settings fails Gateway startup before listen
   and produces a non-zero process exit.
5. Connector Registry is the only supported setting writer. Direct SQL updates
   during runtime are unsupported and are not observed until restart.
6. Multi-tenancy means logical owner isolation for a household inside one
   Gateway. It is not a hostile-tenant process, Store, or infrastructure
   boundary.

These decisions remove the remaining product ambiguity and put the design
confidence above the requested 90 percent threshold. The implementation and
verification matrix in this record encode the resulting contract.
