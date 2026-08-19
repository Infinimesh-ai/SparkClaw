# Generic External MCP Safeguards Design

> Language: English | [简体中文](../zh-cn/docs/generic-mcp-safeguards-design.md)

> Status: implemented for
> [issue #10](https://github.com/Infinimesh-ai/SparkClaw/issues/10) with the
> secure defaults recorded under Resolved Decisions.
>
> LocalMind contract update (2026-08-19): LocalMind no longer discovers or
> translates remote catalogs and no longer uses `internal/mcptools`. Its fixed
> three-tool task adapter retains the shared `internal/mcpsafety` projection and
> approval-persistence guard. LocalMind discovery/filter/translation details
> below are retained only as issue #10 history and are superseded by
> [External integrations](integrations.md).

## Decision Summary

The generic external MCP client owns remote catalog filtering and translation.
Generic and LocalMind clients share result redaction, bounded state/archive
projection, and approval-argument persistence. Provider adapters may add
narrower behavior but may not weaken those shared safeguards.

Two dependency-safe owner packages enforce that contract:

| Package | Ownership |
|---|---|
| `internal/mcptools` | Normalize remote tool metadata, apply allow/deny and mutation policy, classify risk/effects, and translate one discovered MCP tool into an `app.ToolDefinition`. |
| `internal/mcpsafety` | Detect sensitive keys, bearer values, signed URLs, and large base64 payloads; build redacted, byte-bounded state and archive projections; reject unsafe approval arguments. |

`mcpintegration` consumes both packages; `localmind` consumes only
`mcpsafety`; `agent` consumes the approval guard. Neither shared package imports
an adapter, Gateway handler, Store implementation, or Agent runtime, so the
existing dependency direction remains intact.

## Problem And Threat Model

The configured MCP endpoint, its credentials, and the selected namespace are
operator-controlled. Everything returned by that endpoint remains untrusted,
including tool names, descriptions, schemas, annotations, result content, and
error text.

Before this change, the generic path violated that boundary in four ways:

1. A `list_` or `get_` name prefix can make an unannotated tool `RiskRead` and
   remove approval. A remote server can therefore advertise a destructive tool
   such as `get_wipe_workspace` as an unapproved read.
2. Generic server configuration rejects `tool_allow`, `tool_deny`, and
   `allow_mutations`, so an operator cannot reduce the discovered catalog.
3. Generic results enter persisted state and observations without the
   redaction and state/archive size separation already used by LocalMind.
4. Approval persistence rejects unsafe arguments only for LocalMind tool
   definitions, even though generic external MCP mutations persist the same
   approval and tool-call records.

The design assumes an authenticated MCP server can still be compromised or
misconfigured. Remote content must not be able to grant itself authority, leak
credentials into durable state, or create unbounded persisted observations.

## Security Invariants

- Tool names and descriptions never lower risk or remove approval.
- `tool_allow` and `tool_deny` are exact remote-name filters. An empty allow
  list means "no additional allow restriction"; deny is applied after allow,
  and overlap is a configuration error.
- `allow_mutations=false` prevents mutation-classified tools from being
  registered in ToolHub. It is an exposure gate, not only an execution check.
- Every registered mutation requires owner approval. Dangerous or open-world
  tools retain deep verification through the existing Policy path.
- Only an explicit `readOnlyHint=true` may classify a tool as a read and remove
  approval. Names and descriptions never imply read-only behavior. A
  destructive or open-world annotation overrides a contradictory read-only
  annotation.
- Both successful and `isError` MCP results use the same redaction and size
  projection before they reach tool-call state, summaries, or artifacts.
- The bounded state projection is used for workflow reasoning. The separately
  bounded archive projection preserves a sanitized MCP envelope for inspection.
- Secret-like keys, bearer values, signed URLs, and large base64 payloads are
  rejected before approval arguments or pending tool calls are persisted for
  every external MCP capability.
- Refresh replaces a provider's visible ToolHub set atomically. Status reports
  the visible tool count after filtering, not the unfiltered remote count.
- Adapter-owned direct calls must pass the same latest discovery policy as
  ToolHub registrations. A typed service cannot bypass allow/deny or mutation
  policy by calling the MCP client directly.

## Configuration Contract

Generic `mcp_servers.<name>` entries accept the existing fields
`allow_mutations`, `tool_allow`, and `tool_deny`. Normalization and conflict
validation apply only to generic servers. Filters refer to the remote MCP tool
name, before namespace translation. LocalMind rejects these fields because its
three-tool contract is fixed.

Example generic shape:

```json
{
  "mcp_servers": {
    "happy-tasks": {
      "url": "https://happy.example.com/v1/team/mcp",
      "token_env": "HAPPY_TEAM_MCP_TOKEN",
      "expected_server_name": "happy-team-tasks",
      "allow_mutations": true,
      "tool_allow": ["list_tasks", "get_task", "create_task", "cancel_task"],
      "tool_deny": []
    }
  }
}
```

The configuration loader continues rejecting LocalMind-only endpoint,
identity, refresh, and projection-tuning fields on generic entries. Generic
state/archive projections use fixed 16 KiB/16 MiB safeguards.

## Shared Tool Translation

`internal/mcptools` takes a discovered tool plus adapter-owned translation
options and returns two typed values: a visibility/classification decision and
an `app.ToolDefinition`. One classification result drives visibility,
`Risk`, `RequiresApproval`, `Idempotent`, directory effects, and capability mode
so those fields cannot disagree.

The shared translator owns:

- bounded remote title and description metadata;
- input/output schema copying and nil input-schema normalization;
- annotation parsing;
- risk, approval, idempotency, and effect mapping;
- common external MCP origin metadata.

Adapters still own:

- local name and dynamic registration source;
- capability name and provider/snapshot qualifiers;
- provider-specific directory wording;
- timeout exceptions such as Happy's `wait_for_idle`;
- the execution closure, refresh/retry policy, and coded transport errors.

LocalMind instead validates the exact `localmind-ai` delegate/get/cancel schemas
before atomically registering its three fixed wrappers. The generic path does
not synthesize a LocalMind task contract.

## Shared Result Projection

`internal/mcpsafety` provides one canonical MCP result projection used by
both adapters:

1. Prefer `structuredContent.result` when present.
2. Otherwise preserve non-empty `structuredContent`.
3. Otherwise combine text content; a single JSON text block may be decoded into
   its JSON value.
4. Sanitize recursively before measuring or persisting either projection.
5. Emit a compact truncation record containing byte count and SHA-256 when a
   sanitized projection exceeds its bound.

The state projection contains only the canonical result. The archive projection
contains provider/source identity, remote tool name, content,
`structuredContent`, `isError`, metadata, and `untrusted: true`. Raw remote
payloads are never used as the fallback after projection fails.

Error messages expose only a short sanitized excerpt. The full sanitized,
bounded error observation remains available through the normal artifact path.

## Approval Persistence

The Agent guard identifies external MCP definitions by typed capability,
covering both `ToolCapabilityExternalMCPWorkspace` and
`ToolCapabilityMCPExternal` instead of checking only the LocalMind provider
qualifier. Sensitive-value detection lives in `internal/mcpsafety`, removing
the duplicate key, signed-URL, and base64 logic.

On rejection, the ToolCall retains only `{ "persistence_rejected": true }`, no
Approval is created, and the existing typed
`mcp_persistence_unsafe` failure is returned with provider-neutral copy.

## Request Flow

```text
configured MCP server
  -> bounded discovery
  -> exact allow/deny filter
  -> mutation exposure gate
  -> shared classification and ToolDefinition translation
  -> atomic ToolHub replacement
  -> Workflow -> Policy -> Approval
  -> approval argument persistence guard
  -> remote call
  -> shared redacted state/archive projection
  -> ToolCall state + bounded observation artifact
```

Direct Happy plan synchronization continues to use its dedicated typed
`happyapproval` service. Its fixed `list_tasks`, `get_task_plan`, `get_task`,
`approve_plan`, and `reject_plan` calls are not model-selected ToolHub calls and
must remain covered by their existing explicit workflow and tests.
`mcpintegration.Manager.CallTool` authorizes each direct call
against the latest successfully discovered remote name and its shared policy
decision. A filtered, undiscovered, or mutation-disabled tool fails
closed before transport. Enabling Happy plan decisions therefore requires the
operator to expose the fixed read tools and explicitly allow its mutations.

## Compatibility And Rollout

This is intentionally a security behavior change. Removing name-prefix trust
can add approval or hide a tool depending on the confirmed policy. Making
generic mutations opt-in can hide Happy create/message/stop/cancel tools from
existing configurations that omit `allow_mutations`.

The English and Chinese integration examples and tuning-key documentation carry
the same contract. The implementation does not silently infer
policy from known server names or add a per-provider hard-coded tool list.
Operators must express exceptions in configuration.

No Store schema or backend interface change is required. Existing persisted
approvals are not rewritten; the new guard applies before new records are
created. Dynamic discovery refresh atomically applies the new policy.

## Implementation

1. `internal/mcpsafety` owns the table-tested sensitive-value primitives,
   LocalMind/generic result projection, and Agent approval persistence guard.
2. `internal/mcptools` owns classification, filtering, effects, and definition
   translation; both LocalMind and generic registration consume it.
3. `internal/config` normalizes generic allow/deny/mutation settings, while
   `internal/mcpintegration` applies them to discovery, status, dynamic
   registration, and direct calls.
4. Agent workflow/manual tests and generic MCP tests cover persistence,
   projection, classification, refresh, and direct-call boundaries.
5. `docs/integrations.md` and its Chinese mirror document defaults, migration,
   projection bounds, and direct-call behavior.

Production ownership is limited to:

| Area | Change |
|---|---|
| `internal/mcpsafety` | New shared sanitizer, projection, and persistence guard. |
| `internal/mcptools` | New shared visibility, classification, and ToolDefinition translator. |
| `internal/config` | Admit and normalize generic safeguard settings. |
| `internal/localmind` | Replace duplicated translation/projection helpers with shared calls. |
| `internal/mcpintegration` | Enforce policy during registration and direct calls; project all dynamic tool results. |
| `internal/agent` | Apply approval persistence protection to every typed external MCP tool. |
| `docs/integrations.md` and mirror | Document operator configuration and security behavior. |

No WebChat, Store, MCP protocol, or Workflow Profile change is required.

## Validation Plan

Focused tests cover:

- generic and LocalMind config normalization, duplicate removal, exact filters,
  allow/deny conflicts, and mutation defaults;
- spoofed `get_wipe_workspace`, absent annotations, contradictory annotations,
  dangerous/open-world annotations, and idempotency mapping;
- identical translation invariants across generic and LocalMind adapters;
- secret-key, inline secret, bearer, signed-URL, base64, malformed/non-JSON,
  structured, text fallback, `isError`, state truncation, and archive truncation
  results;
- approval persistence rejection for generic and LocalMind external tools on
  both workflow and manual invocation paths;
- atomic refresh, filtered status count, and independent server degradation;
- direct-call denial for filtered, missing, or mutation-disabled Happy
  tools, without weakening the typed Happy approval lifecycle;
- existing Happy read/mutation Workflow selection and Happy plan approval
  synchronization.

The final gate is:

```bash
cd services/gateway && go build ./...
cd services/gateway && go test ./...
cd services/gateway && go vet ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/run-eval.sh
bash scripts/doctor.sh
```

The docs mirror/link CI check also gates the English and Chinese updates.

## Resolved Decisions

1. Trust the MCP protocol's explicit `readOnlyHint=true`, but remove all
   name-prefix inference. Unannotated tools default to mutation, require
   approval when enabled, and remain hidden while mutations are disabled.
2. Omitted generic `allow_mutations` defaults immediately to `false`. Trusted
   Happy configurations must opt in explicitly.
3. Generic results use fixed 16 KiB state and 16 MiB archive bounds. LocalMind
   retains its existing configurable bounds while sharing the same projection
   implementation.
