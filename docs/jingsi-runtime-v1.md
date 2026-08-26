# JingSi Runtime v1 Provider

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
1–64 executions (default 4). The five POST actions use media type
`application/vnd.infinimesh.sparkclaw-runtime.v1+json`:

- `/v1/executions:submit`
- `/v1/executions:lookup`
- `/v1/executions:status`
- `/v1/executions:cancel`
- `/v1/execution-events:list`

## Durable reconciliation

Before any Agent Runtime work starts, Submit atomically persists the authenticated
caller/space/request-key binding, canonical semantic digest, stable execution ID,
authorization, bounded input, and initial accepted/queued events. Exact replay
returns that execution; drift conflicts without creating work. Lookup of a key
that has never been bound commits an irreversible `not_started` negative fence,
so a later Submit cannot revive the key.

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

Memory is included only when JingSi supplied the bounded v1 `memory_context`.
It is clearly marked as task context, never authorization. Results expose only
coarse state, bounded summary, and opaque versioned trace/artifact references;
internal paths and store identifiers do not cross this surface.

## Evidence and remaining boundary

Provider tests cover exact replay/drift, durable negative fences across restart,
lost-response lookup, monotonic event pages, uniform authorization, idempotent
cancel, dedicated bearer routing, and dispatch into the existing Agent Runtime.
This is development-host evidence. It does not prove the JingSi consumer/worker,
two-process integration, production credential provisioning, or GB10 physical
acceptance; those remain cross-repository exit gates.

