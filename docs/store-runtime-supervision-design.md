# Store Runtime And Supervision Design

> Language: English | [简体中文](../zh-cn/docs/store-runtime-supervision-design.md)

> Status: draft for S5 design review, 2026-08-19. No Runtime or health
> abstraction may merge before S4 proves that `store.Store` has been deleted.

## Objective

Add one assembly-owned Store Runtime and one finite Supervisor after consumers
already depend on small repositories. This stage centralizes operational policy
without recreating a business mega-interface or service locator.

## Runtime Boundary

`store.Runtime` is retained only by `cmd/sparkclaw` assembly. It owns:

- the selected Memory, File, or PostgreSQL backend and lifecycle;
- typed repository accessors used during assembly only;
- one Supervisor and the finite operation registry created by the S2 pilot and
  completed through S3;
- startup probe, readiness projection, recovery probe, and bounded close.

Production handlers, Agent, schedulers, connectors, adapters, and registries
receive repository interfaces directly. They never accept `*store.Runtime`.

## Supervisor Boundary

Supervisor wraps the backend boundary already used by each migrated method. It
owns:

- caller/fallback deadline composition;
- backend error classification while preserving domain errors;
- bounded duration and result metrics;
- safe structured diagnostics with no record content;
- backend health state and readiness projection;
- recovery probe coordination.

Supervisor does not own validation, CAS rules, transaction contents, record
schemas, routing, Policy, approval, delivery, audit persistence, or retry
decisions. It cannot accept arbitrary operation names.

Telemetry never writes through the Store being supervised.

## Finite Operation Registry

Each `OperationID` has one static spec:

```go
type OperationSpec struct {
    ID               OperationID
    Repository       RepositoryID
    Mode             OperationMode
    TimeoutClass     TimeoutClass
    AffectsReadiness bool
}
```

Registration rejects duplicate, missing, or unreferenced IDs. Metrics use only
bounded spec fields, backend kind, and classified outcome. IDs, owners, paths,
queries, DSNs, and content never become labels.

The S2-S3 operation boundary is upgraded in place; repository method signatures
and call sites do not change again.

## Health State Machine

- `not_found`, `conflict`, `invalid`, and `canceled` do not affect health.
- `durability_failed`, `unknown_outcome`, and `corrupt` immediately make a
  durable backend unready.
- readiness-affecting `timeout` or `unavailable` results make the backend
  unready after three consecutive failures.
- unrelated successful operations do not clear degradation.
- only an explicit successful bounded recovery probe returns the backend to
  ready.
- Memory reports `durable=false` but remains ready when its own invariant probes
  pass; intentional non-durability is not an incident.

Thresholds and transitions are deterministic and race-tested. Public readiness
contains safe state and timestamps, never infrastructure secrets.

## Probes

File recovery uses an isolated same-directory temporary write, file sync,
rename, directory sync, verify, and cleanup cycle without changing the state
snapshot.

PostgreSQL recovery acquires the pool, runs `SELECT 1`, reads the migration
ledger, and verifies the expected latest version/checksums.

Startup uses the same bounded primitives but does not report ready until backend
construction, migration/load, and probe all pass.

## Lifecycle

Runtime construction returns an error and leaves no partially published
repositories. `Close(ctx)` is idempotent and bounded, rejects new operations,
waits for admitted operations within the deadline, closes backend resources,
and joins relevant errors. Gateway shutdown owns the close context.

## Verification

Required evidence:

- source guard restricting Runtime use to assembly;
- finite registry completeness and no arbitrary labels;
- effective earlier caller deadline and all fallback classes;
- exactly-once operation finish accounting;
- immediate and thresholded readiness degradation;
- successful probe recovery and failed-probe retention;
- File and PostgreSQL probe isolation;
- no recursive Store telemetry or sensitive logs;
- bounded, idempotent close with in-flight operations;
- Memory/File/PostgreSQL repository parity after wrapping;
- focused and full race tests.

## S5 Review Gate

Design `GO` requires accepted ownership, registry, health transitions, probes,
lifecycle, and public projection. Implementation `GO` requires all verification
above and proof that no consumer regained broad Store access.

## Review Record

| Review | Revision/commit | Decision | Evidence and unresolved risks | Reviewer/date |
|---|---|---|---|---|
| Design | pending | pending | pending | pending |
| Implementation | pending | pending | pending | pending |
