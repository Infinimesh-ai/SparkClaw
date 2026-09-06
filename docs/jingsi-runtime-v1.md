# JingSi Runtime v1 Provider

> Language: English | [简体中文](../zh-cn/docs/jingsi-runtime-v1.md)

SparkClaw implements the provider side of the accepted ProjectGroup-2
`JingSi → SparkClaw` Runtime v1 contract. The authority remains InfiniCenter
decision 0007 and its central JSON Schema, HTTP binding, and conformance fixtures.
This provider is separate from the historical JingSi-LAN Web presentation routes.

## Enablement

The surface is disabled by default and is served on the normal Gateway listener.
While enabled, `gateway.bind` must be a literal loopback IP. Supply exactly one
dedicated service credential through the environment or an owner-only regular
file:

```bash
export SPARKCLAW_JINGSI_RUNTIME_V1_ENABLED=true
export SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN='<random service credential>'
export SPARKCLAW_JINGSI_RUNTIME_V1_STATE_DIR='/var/lib/sparkclaw/jingsi-runtime-v1'
```

Alternatively set
`SPARKCLAW_JINGSI_RUNTIME_V1_BEARER_TOKEN_FILE`; the file must not be a symlink
and must have no group/other permissions. The token is secret-only: it is not
serialized into public configuration, responses, records, or errors.

`SPARKCLAW_JINGSI_RUNTIME_V1_MAX_CONCURRENT` bounds active Runtime v1 work to
1–64 executions (default 4). `SPARKCLAW_JINGSI_RUNTIME_V1_RETENTION_DAYS`
(`jingsi_runtime_v1.retention_days`, default 30, 0 keeps records forever, at most
3650) bounds how long terminal records and negative fences stay in the state
directory; see "Operational bounds" below. The five POST actions use media type
`application/vnd.infinimesh.sparkclaw-runtime.v1+json`:

- `/v1/executions:submit`
- `/v1/executions:lookup`
- `/v1/executions:status`
- `/v1/executions:cancel`
- `/v1/execution-events:list`

## Durable reconciliation

Before any Agent Runtime work starts, Submit atomically persists the authenticated
caller/request-key binding, canonical semantic digest, stable execution ID,
authorization, bounded input, and initial accepted/queued events. Exact replay
returns that execution; drift conflicts without creating work. Lookup of a key
that has never been bound commits an irreversible `not_started` negative fence,
so a later Submit cannot revive the key. The contract forbids reusing a request
key across callers or spaces, so the key is bound to the authenticated caller and
a replay of the same key from another space conflicts instead of creating work.

Records are owner-only JSON files written by file-sync, atomic rename, and
directory-sync. They contain the bounded goal and Memory Context needed to resume
an accepted execution after a process restart, so the state directory is personal
runtime data and belongs in the same encrypted backup and access-control boundary
as other SparkClaw state. The bearer is never stored there.

On startup, nonterminal accepted/queued/running records are re-entered with the
same execution/run ID. Existing Agent runs are read idempotently. Work that cannot
be resumed safely becomes an explicit failed outcome; it is not silently replayed
under a new identity. Approval-required work remains stopped for the existing
approval flow. Cancel intent is persisted before the active context is canceled,
and terminal cancel replay is stable.

## Authorization and output projection

The provider validates and persists the complete v1 authorization envelope on
every action. Status, events, and cancel return uniform `not_found` for an unknown
or differently authorized execution. Agent ingress receives the exact task
identity and sorted tool/data/network/approval/grant projection. Runtime tool
exposure requires an exact `tool_scope` match; `approval_policy=deny` removes
approval-requiring tools. Per-request deadline, maximum runtime, maximum tool-call
count, and maximum output bytes only narrow the existing global Runtime policy.
The contract accepts `budget.max_output_bytes` up to 1 MiB but caps every
response at 131072 bytes, so result summaries are held to 64 KiB regardless of
the requested budget.

Memory is included only when JingSi supplied the bounded v1 `memory_context`.
The goal remains the sole owner-intent input for risk, guard, semantic routing,
message control, and capability admission. Memory summary is persisted in a
separate task-context field for deterministic resume and is added to workflow
prompts only after the route and authority boundary are frozen, under an explicit
data-only marker. A malicious or merely off-topic Memory Context therefore cannot
select a capability, add a return endpoint, or widen tool authority. Results expose
only coarse state, bounded summary, and opaque versioned trace/artifact references;
internal paths and store identifiers do not cross this surface.

## Operational bounds and logging

The provider serves nothing until the Gateway has bound its lifecycle: every
authenticated action before `Start` receives the retryable `runtime_unavailable`
Problem with `side_effects=none`, so no execution can run under an uncancellable
context or escape shutdown.

The state directory is bounded by retention. An hourly sweep bound to the
provider lifecycle (the first run happens at `Start`) deletes bound records whose
terminal outcome completed, and negative fences that were committed, more than
`retention_days` ago; each sweep removes at most 5000 records so a backlog drains
over several sweeps. Nonterminal work (accepted, queued, running,
approval_required) is never swept. Deleting a record forgets its key: a later
Lookup of a swept key commits a fresh `not_started` fence with the same
deterministic fence ID, and a later Submit is treated as a first request. The
contract itself puts no time bound on reconciliation, but JingSi only resolves an
unknown Submit through Lookup, keeps it blocked until a `bound` or `not_started`
outcome arrives, and binds accepted executions atomically. The window therefore
only has to exceed the longest outage during which JingSi could still be holding
an unreconciled key; 30 days covers that with a wide margin, and operators who
must honor the fence indefinitely set the knob to 0 and accept unbounded growth.

Operational lines go to the process `slog` logger. Bearer rejections log a running
failure count and the remote address, never the presented credential; idempotency
conflicts log the request ID, request key and a reason code (`negative_fence`,
`authorization_drift`, `semantic_drift`); durable-record persist failures log the
operation and the opaque record ID; each terminal outcome logs the execution ID,
outcome and event count. No line carries the goal, the Memory Context, a result
summary or the bearer, as the contract requires of logs.

## Evidence and remaining boundary

`internal/contracttest` checks the central conformance manifest, HTTP binding,
and fixtures. It resolves the manifest from `SPARKCLAW_JINGSI_CONTRACT_MANIFEST`
first and otherwise from a sibling `InfiniCenter` checkout; when neither is
present (a fresh clone, CI) the gate skips instead of failing `go test ./...`.

Provider tests cover exact replay/drift, durable negative fences across restart,
lost-response lookup, monotonic event pages, uniform authorization, idempotent
cancel, dedicated bearer routing, `return_nowhere`, data-only Memory Context, and
dispatch into the existing Agent Runtime. JingSi additionally owns a development
gate that starts PostgreSQL 18, IMMS, SparkClaw, JingSi and a real JingSi-Node
process independently, then proves successful Task result reconciliation,
Observation writeback and origin notification/ACK. This evidence does not prove
production credential provisioning, power-loss recovery, real networking, or
GB10 physical acceptance; those remain cross-repository exit gates.
