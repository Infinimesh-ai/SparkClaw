# Issue #16 Tool-Policy Approval Design

> Language: English | [简体中文](../zh-cn/docs/issue-16-external-media-approval-design.md)

> Status: implemented design for
> [issue #16](https://github.com/Infinimesh-ai/SparkClaw/issues/16). On
> 2026-08-17 the owner established ToolDefinition plus Policy as the approval
> authority. "External MCP" means the MCP surface exposed by SparkClaw to an
> external AI, not an MCP server consumed by SparkClaw and not SparkClaw's local
> model. All product boundaries required for implementation are resolved.

## Decision Summary

Approval is an execution property, not a delivery-destination property. Every
Workflow uses the registered tool's `Risk` and `RequiresApproval` values plus
the shared Policy decision. Requester, ingress adapter, return route, and result
endpoint never override that baseline merely by being different.

A human owner's explicit request for their own data is consent for the bounded
data operation they requested. An inbound call through SparkClaw's exposed MCP
surface is different: its immediate caller is an external AI principal, not the
human owner. That AI cannot manufacture human consent in prompt text. When it
requests SparkClaw workspace data, Policy must pause before protected access and
obtain owner approval for that security-boundary crossing.

A human owner's explicit send/publish instruction is authorization for that
exact send and does not require approval. SparkClaw's local model participating
in routing or tool selection does not change that authority and is not the
"external AI" in this design. If an external AI calls SparkClaw's exposed MCP
surface for workspace data, the one workspace-data approval binds the complete
operation, including output class and any requested return/send target. Delivery
does not ask a second time after that approval. Text, image, audio, file, mixed,
and multipart content follow the same rule.

Examples:

- `weather.lookup` remains a safe read without approval regardless of who asks
  for weather or where its ordinary result returns;
- a tool already marked approval-required remains approval-required regardless
  of who invokes it;
- an external AI calling SparkClaw's inbound MCP to access owner workspace data
  is the first accepted contextual escalation and must require approval; and
- a human-explicit send requires no approval merely because a local model routes
  or executes it;
- an external-AI MCP workspace-data operation produces one approval covering its
  frozen output/return/send, not stacked prompts; and
- every other tool retains its registered approval behavior.

A principal or transport alone is not an escalation: an external MCP weather
lookup and its ordinary protocol response remain unapproved. The accepted
contextual escalation is an external AI, authenticated through SparkClaw's
exposed MCP surface, accessing governed SparkClaw workspace data. A contextual
rule may only strengthen a ToolDefinition decision. It must never make an
approval-required tool safe, infer risk or consent from display text, or create
a second per-channel tool catalog.

## Implemented Boundary

Runtime now persists a typed `PolicyExecutionContext` on context-bound tool
calls and approvals in memory, file, and PostgreSQL stores. The hidden
`workspace.data.access` ToolHub capability enters the ordinary Policy/Approval
path without becoming model-visible. External-MCP response-media lookup and
exact document paths use that gate before filesystem or index access; approved
document reads reuse the same frozen contract rather than creating a second
data approval. Ordinary workspace tools still receive the same contextual
escalation when no covering contract exists.

Inbound MCP runs do not inherit prior session messages, tool summaries,
memories, images, or episode summaries as implicit model context. Those stores
may contain workspace derivatives whose presence and lineage are outside the
current frozen request. Approved current-run evidence remains available, and a
caller that needs workspace data must provide the current locator governed by
the approval contract. The contract also binds the local owner and authorized
principal in addition to the MCP device/binding identity. A cross-channel
return still updates the original durable MCP operation after its frozen target
delivery, while a suppressed waiting result records `approval_required` without
sending content to that target. Cross-channel operation polling records status
without copying the delivered workspace payload into the MCP result.

Destination-owned `message_control.external_send` approval creation is removed.
Human-explicit text and media delivery proceeds through the shared Workflow and
Delivery path, while persisted legacy approvals fail closed. Context-bound
approval modification returns a conflict, and approval resume revalidates the
arguments, MCP identity, Workflow plan, output class, and return route.

### Owner Review Surface And Resume Lifecycle

Each MCP Binding owns one visible conversation titled `AI · <short device ID>`.
The title is derived from the authenticated device identity, and ordinary
session rename, delete, and message APIs reject writes to MCP conversations;
requirements enter only through the authenticated Binding. Existing hidden
`External MCP` conversations are normalized on file-store load and PostgreSQL
migration. WebChat therefore treats the conversation as a read-only review
surface, identifies the external AI once in its session title, and labels each
inbound request `Requirement` with only its requested text and media conditions.
Revoking or deleting the access Binding stops access but retains this read-only
conversation history; deleting that history would require a separate explicit
data-purge action.

MCP `media` locators are persisted on the user message as `requested_media`.
They render as non-clickable, not-yet-verified request conditions and are never
projected as downloadable attachments. A media-only call consequently has a
visible message without implying that SparkClaw found, opened, or authorized
the requested file.

The approval API derives a read-only presentation from the frozen approval
arguments and authenticated `PolicyExecutionContext`. It shows the managed AI
conversation title, unverified locators, access class, output class, return
route, and single-operation scope; raw arguments remain collapsed under
technical details. This presentation is not persisted authorization input and
is never consulted when resuming execution.

After the owner decision is durable, an MCP approval returns `202 Accepted`
with separate `approval_status=approved` and `execution_status=running` fields.
Tool execution, Workflow resume, and delivery continue under the Gateway
lifecycle context rather than the approval HTTP request. Resumed work is also
registered under the original MCP operation, so operation cancellation, Binding
revocation, the original invocation deadline, or Gateway shutdown cancels the
background context before later Workflow or delivery steps can continue. The
durable operation therefore follows:

```text
running -> approval_required -> running -> succeeded | failed
```

Rejection, resume failure, and delivery failure have distinct operation error
codes. A successful approval is never relabeled as failed merely because later
execution or delivery failed, and a late waiting result cannot move an already
approved execution back to `approval_required`.

## Why Destination-Based Approval Is Rejected

Receive and send adapters are deliberately independent. A request may enter
through Web, Weixin, Telegram, or inbound MCP; invoke the same Workflow and
tools; and return through the same or another transport. Endpoint kind therefore
describes transport, not execution risk. Human consent and external-AI
delegation are authenticated principal facts, not aliases for channel names or
delivery destinations.

Using `EndpointKindThirdPartyDevice` as the approval switch would incorrectly:

- require approval for a safe weather lookup merely because its result returns
  through MCP or a connector;
- make the same tool alternate between safe and dangerous based on transport;
- treat local model participation as a new external principal; and
- spread security policy across Message Control, Agent, Timer, and Delivery.

The previous draft's endpoint-kind proposal is superseded and must not be
implemented.

## Current Architecture Gap

ToolHub definitions already carry `Risk` and `RequiresApproval`. Policy applies
those facts consistently to model-selected, direct-once, and manual tool calls.
For example, `weather.lookup` and `files.read` are currently read/no-approval,
while document mutations and dangerous tools require approval.

Issue #16 sits outside that normal path:

1. `message_control.external_send` is an Agent-created confirmation action, not
   a registered business ToolDefinition evaluated by normal Policy. Under the
   final model, destination alone is not an approval reason.
2. `conversation.answer#publish` can complete deterministically without a
   ToolHub call. That remains valid for an authenticated human-explicit publish
   instruction even when the local model recognized or routed it.
3. Pure media then has two explicit early returns that bypass the extra
   post-result approval used by text external sends. The inconsistency is real,
   but content type and destination are both the wrong policy inputs.
4. Inbound MCP response-media resolution can read workspace filenames,
   metadata, hashes, and bytes on behalf of an external AI inside a deterministic
   Workflow node, also outside normal tool Policy.
5. `policy.Engine.Decide` currently receives only a ToolDefinition and
   arguments; it has no typed invocation/resource context for the accepted
   external-MCP workspace escalation.

Deleting only the two media early returns retains the wrong destination policy.
The correct issue #16 fix removes the destination-owned post-result approval and
adds the inbound-MCP workspace-data escalation before protected discovery/read,
treating every content type identically at that data boundary.

## Target Policy Model

### Baseline Tool Authority

The registered ToolDefinition remains the baseline and single source of truth:

- `RequiresApproval=true` always requires approval;
- dangerous risk keeps the configured dangerous-tool approval and deep
  verification behavior;
- configured `approval_required_tools` still escalates the named tool; and
- safe tools remain unapproved unless a typed contextual rule escalates them.

Tool definitions are not cloned or mutated per requester. Model-visible risk
metadata remains stable for the Workflow stage.

### Typed Execution Context

Policy execution receives a small typed context in addition to the definition
and exact arguments. It contains security facts already frozen by Runtime, such
as:

- authenticated principal class, distinguishing the owner human from an
  external AI invoking SparkClaw's exposed MCP surface;
- the persisted inbound MCP invocation and requester identity;
- Workflow/run identity;
- governed resource class, including `sparkclaw_workspace_data`; and
- requested access/effect class, including source read, transformation, and
  disclosure of an existing derivative.

Runtime derives the external-AI fact only from the authenticated and persisted
MCP invocation (`MessageRunContext.MCP` and its requester identity), never from
provider names, prompt text, endpoint IDs, or model output. The MCP request
continues to execute under the bound local owner's authorization, but that
authorization is not evidence that the owner personally approved this specific
workspace disclosure. Before approval, Runtime may only validate the syntax and
lexical workspace scope of the external AI's locator or query without touching
the filesystem. Policy consumes that typed request classification; it does not
parse host paths or query Store directly.

Context decisions are monotonic:

```text
effective_requires_approval =
    tool_definition_requires_approval
    OR configured_tool_escalation
    OR dangerous_tool_policy
    OR (external_mcp_ai_principal AND sparkclaw_workspace_data_access)
```

There is no contextual downgrade.

### Workspace Data Is Content-Equivalent

The governed boundary covers both original workspace content and every derived
representation of that content. It includes raw files and bytes, extracted text,
summaries, excerpts, answers grounded in the content, OCR, image previews and
thumbnails, audio transcription, transcoding, embeddings, and other transformed
or cached representations. Changing the output format does not declassify the
data.

For a new transformation, approval occurs before the first source-content read.
If a derivative already exists in a cache, artifact, index, or prior Workflow
state, approval occurs before that derivative is disclosed to the external AI.
An earlier human-authorized read does not grant a later external-AI disclosure.
The approval binds the exact frozen MCP invocation, requested locator or query,
deterministic resolution contract, operation, and output class. After approval,
the resource owner resolves exactly once, validates the authoritative workspace
root and symlink boundary, and freezes the selected resource identity/version
before reading. Ambiguity, replanning, or a changed resource fails closed or
requires a new approval. This is not a blanket workspace grant.

### No Workspace Discovery Before Approval

Pre-approval processing performs no filesystem or index access. It must not
test existence, enumerate filenames or directories, follow symlinks, call
`stat`, determine MIME type, read timestamps or size, compute hashes, search an
index, inspect cached derivatives, or read content. These facts are themselves
workspace data.

The approval display may show only the external AI's unverified locator/query,
the authenticated MCP requester, intended operation, bounded resolution rules,
and output class. After approval, deterministic bounded discovery selects at
most the contractually allowed resource set and freezes it before the first
content access. No discovery result is disclosed to the external AI unless it
is within the approved operation.

### Deterministic Workflow Effects

Any deterministic Workflow step that accesses governed workspace data on behalf
of an external AI must enter the same Policy/approval machinery. It may use a
Runtime-invoked registered capability rather than exposing tool choice to the
model. This applies to response-media lookup, read, transformation, and export.

`conversation.answer#publish` remains a deterministic consent-bearing Workflow
operation for an authenticated human-explicit instruction. Semantic model
routing may recognize that instruction, but local model participation does not
create an external-AI principal or a second approval. The normalized source
instruction, content/output class, and requested target are frozen before
untrusted data can influence execution.

For an external AI invoking SparkClaw's exposed MCP, the persisted MCP invocation
is the principal boundary. If that operation accesses workspace data, its one
approval includes all frozen data and output/return/send facts. A change is a
new operation and cannot reuse it. There is no additional send approval after
the data-boundary approval. Issue #16 must keep neither a media-specific bypass
at that boundary nor a destination-specific gate. Text, image, audio, file,
mixed, and multipart data follow the same rule. Audio is media.

## Preliminary Applicability Matrix

| Invocation | Tool/effect | Approval |
|---|---|---|
| Any requester/channel | Safe weather lookup | No |
| Human owner request | Own workspace data through current safe read tool | No additional approval; the bounded request is consent |
| Human owner instruction | Explicit send/publish of text or any media | No; authenticated instruction is consent |
| Human owner Workflow | Local model routes/selects tools for that request | No origin-based escalation; use each tool's existing policy |
| Inbound SparkClaw MCP, external AI | Safe non-workspace lookup such as weather | No |
| Inbound SparkClaw MCP, external AI | Original or derived SparkClaw workspace data | Yes, contextual escalation |
| Inbound SparkClaw MCP, external AI | Approved workspace data followed by its frozen return/send | One workspace-data approval total; no second send approval |
| Any invocation | Ordinary response to the same invocation | No send approval; underlying tool/data policy still applies |
| Any requester/channel | Tool already marked approval-required | Yes |
| Any requester/channel | Other safe tool | No |
| Any destination alone | No tool/effect change | No approval change |

## Security Invariants

- ToolDefinition plus Policy is the sole approval authority.
- Requester, source channel, return route, and endpoint never lower approval;
  transport alone never raises it.
- Context rules are typed, centralized, auditable, and escalation-only.
- External-AI identity comes from the authenticated persisted inbound MCP
  invocation, not user text, an asserted human instruction, or transport labels.
- Human consent is scoped to the exact owner-requested operation; local model
  participation does not invalidate or broaden it.
- An authenticated human-explicit send/publish instruction authorizes that exact
  frozen send.
- Workspace classification is resolved against the authoritative governed root;
  path strings alone cannot claim or avoid the rule.
- Approval is evaluated after exact request/locator and resolution-contract
  binding, but before any filesystem/index lookup, content access, disclosure,
  or effect.
- Original content and every derived representation retain the same governed
  classification; a cached derivative cannot bypass approval.
- Prior session messages, tool results, memories, images, and episode summaries
  are not implicit context for an inbound MCP run; current-run evidence enters
  only after its bound approval when workspace-derived.
- Workspace existence, names, metadata, hashes, index matches, and cached-
  derivative presence are not inspected before approval.
- Approval resume revalidates the same tool, arguments, context, locator/query,
  and any bound governed-resource identity; mismatch blocks.
- No media kind, including audio, receives a special bypass.
- External-MCP-AI workspace access and its frozen requested output/return/send
  produce one approval record, not stacked data and delivery approvals.
- Provider and Delivery adapters never make Policy decisions.
- Destination-owned `message_control.external_send` is retired as an approval
  source; the typed inbound-MCP workspace-data rule replaces its security role.

## Expected Implementation Areas

- `internal/policy`: add typed execution context and monotonic contextual
  escalation while preserving the existing definition/config rules;
- `internal/agent`: pass persisted principal/run/MCP context through every
  Workflow execution path, govern deterministic workspace access, and remove
  destination-owned `message_control.external_send` plus its media exception;
- `internal/toolhub` plus the owning Workflow: register or directly invoke the
  protected workspace-data capability needed for shared Policy; do not create a
  local-model or external-send approval tool merely for routing;
- `internal/app`: add only the minimal typed context/resource contracts needed
  across those owner boundaries; and
- tests/docs: cover local, connector, Timer, inbound MCP, text, image, audio,
  file, mixed, and multipart cases without provider-name branches.

If an interface or durable context changes, all memory/file/PostgreSQL paths
must remain complete. No new external-MCP tool is exposed merely to implement an
internal approval step. Existing unresolved legacy external-send approvals must
not auto-deliver after rollout; they become stale/blocked and require a fresh
instruction.

## Verification Direction

- the same weather Workflow has no approval from Web, connector, Timer, and
  inbound SparkClaw MCP;
- every existing approval-required tool remains approval-required from all
  origins;
- an explicit human-owner request for the bounded safe workspace read retains
  the tool's existing behavior;
- accepted external-MCP workspace access cases pause before protected access,
  persist one approval, and resume the exact frozen operation;
- pre-approval exact-path and fuzzy-query cases perform zero filesystem/index
  reads; approval displays only the unverified request and bounded operation;
- post-approval resolution freezes a deterministic resource identity/version;
  ambiguity, resource change, or replan cannot reuse the approval;
- raw file, summary, excerpt, OCR, thumbnail, audio transcript, transcode,
  embedding, cached derivative, and answer-from-content cases all require the
  same contextual escalation;
- rejected or mismatched approval exposes no workspace content or media;
- authenticated human-explicit text/image/audio/file/mixed/multipart sends create
  no additional approval on Web or connectors, and local model execution does
  not change that result;
- an external-MCP-AI workspace operation that includes a frozen return/send
  creates exactly one data-boundary approval and performs no delivery after
  rejection or mismatch;
- a changed locator, query, output class, or target cannot reuse that approval;
- no new destination-owned `message_control.external_send` approval is created,
  and unresolved legacy ones cannot auto-deliver; and
- direct-once, model-selected, manual, and deterministic Runtime invocation use
  the same Policy decision API.

Focused and full Gateway build/test/vet, the golden eval, and bilingual docs
checks gate implementation. Provider-neutral tests are authoritative; live MCP,
Weixin, or Telegram credentials are additional evidence only.

## Resolved Decisions

Accepted by the owner on 2026-08-17:

- approval is based on tool/effect risk, not final destination;
- safe tools such as weather lookup do not require approval based on caller;
- dangerous or data-sensitive execution must not bypass approval;
- "external MCP" means SparkClaw's MCP surface called by an external AI, not
  generic/LocalMind MCP servers called by SparkClaw;
- a human owner's request for their own data constitutes consent for that
  bounded request;
- external-AI access through inbound MCP to SparkClaw workspace data is the
  first contextual approval escalation;
- original workspace content and all derived representations require that
  escalation before source read or derivative disclosure;
- pre-approval handling is limited to pure locator/query syntax and lexical
  scope validation; all discovery and metadata access occurs after approval;
- an authenticated human-explicit send/publish instruction authorizes that exact
  send for any text or media type, even when the local model routes/executes it;
- "AI" in this contextual rule means only an external AI calling SparkClaw's
  exposed MCP, never SparkClaw's local model;
- an external-MCP-AI workspace-data operation has one approval containing its
  frozen output and requested return/send target, with no second send approval;
- the destination-based `message_control.external_send` gate is removed;
  unresolved legacy approvals fail closed instead of delivering;
- all other tools keep their registered approval setting; and
- audio belongs to the media set.

## Decision History And Readiness

The issue's first owner comment asked to restore the text-style external-send
approval for image and file publications, with only same-session Web exempt.
The later owner clarifications supersede the destination-wide gate: human-
requested sends are authorized, and SparkClaw's local model is merely the local
executor, not the external AI. The protected AI boundary is specifically an
external AI calling SparkClaw's exposed MCP for workspace data. Every content
type remains covered at that boundary, while destination and media kind are not
approval inputs.

No product decision remains open. The approval boundary and owner review
surface are implemented under this contract.
