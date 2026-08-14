# External MCP Conversation Convergence Design

> Language: English | [简体中文](../zh-cn/docs/external-mcp-conversation-design.md)

| Field | Value |
|---|---|
| Status | Implemented in the active worktree; production ISCP rollout pending |
| Decision date | 2026-08-13 |
| Scope | Inbound external MCP business capability |
| Business tool | `sparkclaw.conversation.send` |
| Content boundary | Ordinary text and governed media files under the linked SparkClaw workspace |
| Protocol | MCP `2025-06-18` over the existing authenticated transport |
| Supersedes | Route-leaf projection and per-leaf grants in the inbound MCP business surface |

## Decision

SparkClaw will converge the external MCP business surface to ordinary
conversation. It will stop projecting numerous Catalog route leaves as MCP
tools. An active external MCP binding exposes exactly one business tool:
`sparkclaw.conversation.send`.

The tool submits one ordinary message to the binding's linked SparkClaw
conversation. The message may contain owner-authored text and zero or more
workspace-media locators. SparkClaw runs the same natural-language routing used
by other message sources. When that routing selects ordinary conversation,
`conversation.answer` revision 3 first decides whether the response needs media
and resolves any required workspace file, then answers from that frozen
decision. The result returns to the same MCP source through the shared Delivery
path.

This is not a reduced Catalog projection. The external client does not select,
name, or receive authorization for a route leaf, operation, Workflow Profile,
or tool effect. Natural-language routing belongs to SparkClaw after message
ingress, exactly as it does for an ordinary Web, Weixin, Telegram, or Timer
message.

MCP initialization, access-ticket redemption, binding revocation, and durable
operation status/cancellation remain protocol and lifecycle controls. They are
not additional business capabilities.

The SparkClaw-owned contract, Runtime, Store, Delivery, and owner UI changes are
implemented. Production enablement still requires a deployable external Access
Gateway and live ISCP Relay validation.

## Product Meaning

"External MCP can send workspace media" means all of the following:

1. The external client can submit a normal conversational request.
2. That request can identify files already present under the linked SparkClaw
   workspace by relative path, exact file name, or a bounded owner-authored
   search phrase when the complete name is unknown.
3. SparkClaw, not the client, resolves and validates those paths.
4. Valid files enter Message Runtime as ordinary image, audio, or file parts.
5. A result containing media is delivered in the same MCP operation as actual
   MCP content, not merely as a local path or opaque artifact identifier.
6. The client gains no independently callable workspace listing, search, read,
   or mutation API. When one response-media locator matches multiple eligible
   files, SparkClaw selects and sends only the server-ranked Top-1 file.
7. A file selected in WebChat enters Workflow Runtime as a governed
   workspace-relative resource reference, never as a client-supplied absolute
   host path.

This design uses "media" in the existing SparkClaw message sense: image, audio,
and file parts. Video and other binary formats are file parts unless and until
the shared message contract introduces another typed kind. The MCP adapter does
not create its own media taxonomy.

## Goals

- Present one stable, provider-neutral MCP business ability: ordinary
  conversation with workspace media.
- Reuse the existing `MessageEnvelope`, natural-language router, Workflow,
  Policy, approval, Store, and Delivery contracts.
- Constrain ordinary conversation to two ordered Workflow nodes: detect and
  freeze response media first, then answer from that decision.
- Preserve text-only, media-only, and text-plus-media messages without a
  separate MCP media routing lane.
- Keep workspace ownership and path validation entirely inside SparkClaw.
- Resolve files progressively: reuse a direct attachment/path, try the exact
  basename index, then use bounded `files.search` when the full name is unknown
  or the exact lookup has no match.
- Return governed media bytes through standard MCP tool-result content types
  when the protocol has a native representation.
- Preserve durable idempotency, bounded execution, cancellation, recovery,
  connector enablement, binding revocation, and redacted audit.
- Remove Catalog revision, route leaf, operation, effect, and approval-grant
  selection from external MCP onboarding and ordinary invocation.

## Non-Goals

- Exposing SparkClaw's Catalog, Workflow Profiles, ToolHub tools, or semantic
  routing graph to the external MCP client.
- Adding one MCP tool per route, provider, media type, file extension, or
  destination.
- Allowing arbitrary absolute paths, `file://` URIs, artifact paths, remote
  URLs, or paths from another session's workspace.
- Providing `resources/list`, workspace directory browsing, wildcard expansion,
  an independently callable filename-search tool, content search, or a generic
  file-download API. The detection node may invoke bounded, filename-only
  `files.search` as an internal fallback for the current response-media request
  and select one server-ranked result for each locator.
- Letting the external client prescribe a capability ID, route operation,
  target leaf, Workflow revision, risk, effect, approval decision, delivery
  endpoint, or model lane.
- Uploading bytes from the external device into the SparkClaw workspace. This
  first contract only selects files that already exist under the linked
  workspace.
- Changing the outbound SparkClaw-to-LocalMind workspace MCP client.
- Changing JingSi's separate bridge and binding design.
- Replacing product capability routing. Browser actions, current-fact search,
  document reading/editing, scheduling, and media generation remain separate
  registered Workflows selected before the ordinary-conversation Workflow.

## One Business Tool

After binding authentication, `tools/list` returns
`sparkclaw.conversation.send` plus the existing binding-scoped operation control
tools required for deferred execution. The operation controls are infrastructure
tools and never create a new conversation message.

The business tool has a fixed server-owned schema revision. A proposed first
schema is:

```json
{
  "name": "sparkclaw.conversation.send",
  "title": "Send a SparkClaw conversation message",
  "description": "Send ordinary text and existing workspace media to the linked SparkClaw conversation.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "text": {
        "type": "string",
        "maxLength": 65536
      },
      "media": {
        "type": "array",
        "minItems": 1,
        "maxItems": 8,
        "items": {
          "type": "object",
          "properties": {
            "path": {
              "type": "string",
              "maxLength": 4096
            },
            "name": {
              "type": "string",
              "maxLength": 255
            },
            "query": {
              "type": "string",
              "maxLength": 255
            },
            "caption": {
              "type": "string",
              "maxLength": 2000
            }
          },
          "oneOf": [
            { "required": ["path"] },
            { "required": ["name"] },
            { "required": ["query"] }
          ],
          "additionalProperties": false
        }
      }
    },
    "anyOf": [
      { "required": ["text"] },
      { "required": ["media"] }
    ],
    "additionalProperties": false
  },
  "annotations": {
    "readOnlyHint": false,
    "destructiveHint": false
  }
}
```

Empty or whitespace-only text does not satisfy the request by itself. An empty
`media` array does not satisfy the request by itself. At least one non-empty
text part or one valid media item is required.

`media` is intentionally an ordered array rather than separate image, audio,
video, and document arguments. Ordering is part of ordinary multipart message
semantics, and the shared message contract determines each part kind from the
governed content type.

Each media item supplies exactly one locator:

- `path` is an exact workspace-relative path, such as
  `exports/annual-report.pdf`;
- `name` is a complete base file name, such as `annual-report.pdf`, used for the
  exact-index fast path when the caller does not know the containing directory;
- `query` is an incomplete name or short owner-authored description, such as
  `annual report`, used only by the bounded `files.search` fallback.

For example, both of these requests can select the same file:

```json
{
  "text": "Send this file.",
  "media": [{ "path": "exports/annual-report.pdf" }]
}
```

```json
{
  "text": "Send annual-report.pdf.",
  "media": [{ "name": "annual-report.pdf" }]
}
```

The caller is not expected to know the complete filename. The equivalent
fallback form is:

```json
{
  "text": "Send the annual report.",
  "media": [{ "query": "annual report" }]
}
```

### Directly Selected Attachments

Selecting a file in WebChat does not add its absolute path to the conversation
contract. The upload implementation may use an absolute host path internally,
but it also writes the object beneath the session workspace and returns a safe
`rel_path`. Message ingress sanitizes that value, and Workflow Runtime consumes
a governed resource such as:

```json
{
  "kind": "workspace_file",
  "ref": "uploads/20260813/report.pdf",
  "provenance": "current_turn_attachment"
}
```

The public message contract may additionally carry a server-issued artifact
identity. It must not accept or persist an absolute path. A direct attachment
therefore means "the current turn already has a bounded workspace resource,"
not "the client may read an arbitrary host path." The detection node can reuse
that resource without a directory scan, but must still govern and freeze its
identity before it can become response media.

## Ordinary Conversation Workflow Revision 3

The two-node rule applies after normal intent routing has selected
`conversation.answer`; it is not a replacement for capability routing. The
`answer` and `publish` semantic variants both resolve to revision 3, and a
media-only current-turn attachment may select the same Workflow deterministically
from typed content.

```text
ordinary message ingress
  -> semantic or typed capability selection
  -> conversation.answer r3
      -> detect_response_media
      -> answer
  -> WorkflowResult
```

### Node 1: `detect_response_media`

This node determines whether the requested response should include media. It
consumes only the frozen owner-authored question, the selected route variant,
already governed current-turn resources, and explicit `media[].path`,
`media[].name`, or `media[].query` locators carried by ingress.

Attachment presence alone is not a send instruction. A file can be input
evidence for reading, summarizing, inspecting, or editing, while the response
remains text or contains a newly generated artifact. The node therefore assigns
each current-turn resource a typed role such as `input_evidence` or
`response_candidate`; only an explicit request to return/publish a resource can
select it as response media.

All file resolution and media governance for ordinary conversation belong to
this node. A deterministic resolver may bind an owner-supplied locator, but a
model may not invent, rank, or substitute a path. Resolution uses one ordered
ladder: a governed current-turn resource or exact relative path, the exact
basename index, then a single bounded `files.search` fallback. The node invokes
the fallback itself with Runtime-fixed workspace scope and budgets; it is not a
model-selected tool. The node verifies the workspace boundary, object kind,
content type, byte limits, stable identity, and hash, then freezes ordered
`workspace_file` resource references for the next node.

The node persists one typed decision:

| Decision | Meaning |
|---|---|
| `none` | The response is text-only; no response-media resource is selected |
| `selected` | One governed reference per requested locator is frozen; explicit multiple locators retain their input order |
| `clarify` | A complete lookup produced zero eligible files; no resource is selected |
| `blocked` | Search or traversal was incomplete, the object is unsafe/unsupported/changed, or a limit was exceeded |

For `selected`, the decision includes only server-derived resource identities,
workspace-relative refs, display metadata, hashes, and ordering. It never
contains an absolute path. For all other decisions, its selected-resource list
is empty.

### Node 2: `answer`

`answer` has a hard dependency on `detect_response_media` and consumes only its
frozen decision and resources. It cannot list a directory, resolve another
name, add or remove a file, reopen a free-form locator, or substitute a resource.

- `none` produces the ordinary no-tool text answer from the owner question and
  allowed conversation context.
- `selected` produces the ordinary ordered multipart result from exactly the
  frozen resources. The pure publish case remains deterministic and does not
  need a model to choose content.
- `clarify` is reserved for a complete zero-result lookup and asks the owner to
  refine the name/description or attach the file.
- `blocked` produces a bounded failure projection and sends no media.

The full multipart result is still governed atomically before Delivery. A
resource that changes between detection and result construction fails closed;
the answer node cannot silently refresh its binding.

### Decision Examples

| Owner request | Routing and detection | Result |
|---|---|---|
| `What does idempotency mean?` with no attachment | `conversation.answer`; `none` | Normal text answer |
| `Send report.pdf` with no attachment | Try the exact basename index in `detect_response_media` | Freeze the exact match; on an exact miss, run the bounded fallback |
| Explicit `media[].name = report.pdf` | Resolve the exact basename in `detect_response_media` | Same frozen result as an equivalent exact relative locator |
| `Send the annual report` or explicit `media[].query = annual report` | Run filename-only `files.search` after the exact stage cannot bind a file | Freeze and return the server-ranked Top-1 result |
| Directly selected attachment plus `send it` | Reuse the current-turn `workspace_file`; no directory scan | `selected` with that frozen resource |
| Directly selected attachment plus `summarize it` | Route to `document.read`; the attachment is input evidence | Text summary from the document Workflow, not republished input |
| Search returns multiple eligible files | Rank by filename relevance and use relative-path lexical order only as the final tie-break | Freeze and return only Top-1 |
| Search returns no eligible files | `clarify` with `file_not_found` | No media; ask the owner to refine the query or attach the file |
| Attached input whose requested operation generates different media | Route to the registered generator/editor Workflow | Only that Workflow's governed output artifact may become result media |

## Progressive File Resolution

`media[].name`, `media[].query`, and a filename/description token in an ordinary
send request invoke the server-owned resolver inside `detect_response_media`.
The MCP adapter performs only schema and locator syntax validation before
Workflow execution. It does not resolve the file and does not expose
`files.search` to the MCP client.

Resolution is ordered and stops only after a safe selection or a typed terminal
decision:

1. Reuse an explicitly selected, already governed current-turn resource.
2. Validate an exact `media[].path` when present.
3. Query the case-sensitive exact basename index with `media[].name` or a
   complete filename parsed from text.
4. If the exact stage returns zero candidates, or the owner supplied only an
   incomplete name/description, invoke `files.search` once with the frozen
   owner query.

An exact-index result with multiple identical basenames does not fall through
to fuzzy search. Because their filename relevance is equal, the resolver uses
workspace-relative lexical path order as the final deterministic tie-break and
freezes only the first eligible file.

### Exact Basename Index

The resolver recursively compares the requested value with base file names
under the linked session's effective workspace. The first implementation uses
exact, case-sensitive file-name equality. It does not use substring, glob,
regular-expression, edit-distance, embedding, file-content, or model matching.
The name must be a single base name and must not contain `/`, `\\`, a URI
scheme, `.`/`..` path components, or a NUL byte.

Resolution has only these outcomes:

| Matches | Outcome |
|---|---|
| One | Freeze the unique workspace-relative path and continue through normal media governance |
| Zero | Continue to the bounded `files.search` fallback |
| More than one | Select one by workspace-relative lexical path order, govern it, and freeze it as `selected` |
| Traversal budget exhausted | Return typed `file_lookup_incomplete`; never return a partial match set |

The traversal reuses one shared workspace traversal policy and skips internal or
dependency directories that are not eligible for ordinary workspace lookup.
It is context-cancellable and bounded by entry count, depth, elapsed time, and
candidate count. Finding one match does not stop the scan: the complete eligible
traversal is required to apply the stable tie-break. Candidate order is stable
and lexicographic.

The exact candidate set must not expose the absolute workspace root, host paths,
content previews, or metadata from unrelated files. The winning
workspace-relative ref remains internal governed evidence; MCP result
construction returns the actual bytes only for the frozen Top-1 file.

As an ordinary-conversation convenience, a text-only request with a send/publish
verb and a file phrase, for example `Send annual-report.pdf`, `send the annual
report`, or `把年度报告发给我`, may be parsed into `name` when the token is a
complete filename, otherwise into `query`. Parsing is deterministic and
Unicode-aware. It does not ask a model to invent a path or rank results.

### Bounded `files.search` Fallback

`files.search` is an internal ToolHub capability exposed only to
`detect_response_media` at this stage. Runtime, not a model or external client,
binds its workspace root, query, timeout, traversal budgets, and maximum
candidates. This mode compares the frozen query only with file basenames. It
must not inspect file contents, content previews, extracted text, or directory
path components when computing relevance. Search observations are untrusted
evidence and cannot authorize a file send.

The current ToolHub implementation returns absolute paths and optional content
previews and may stop at `max_results` without reporting completeness. That raw
result is not suitable for this Workflow contract. Implementation must either
revise the shared tool output or add a Workflow-owned adapter that:

- converts every path to a validated workspace-relative candidate and rejects
  any candidate outside the linked session workspace;
- returns only a validated `rel_path`, basename, server-owned filename relevance
  score, and typed match reason for each candidate before persistence;
- removes the workspace root, host path, content preview, extracted content,
  and unrelated file metadata before persistence or user projection;
- records whether traversal completed and whether the candidate list was
  truncated;
- deduplicates candidates and ranks them by the server-owned filename relevance
  score, using workspace-relative lexical path order only as a final tie-break;
- proves that candidate enumeration is complete within the declared traversal
  budget so the selected Top-1 cannot be displaced by an unseen file.

Fallback outcomes are:

| Search result | Decision |
|---|---|
| One eligible candidate and complete traversal | Govern and freeze it as `selected` |
| Zero eligible candidates and complete traversal | `clarify` with `file_not_found`; ask for a refined query or attachment |
| Multiple eligible candidates and complete traversal | Govern and freeze only the filename-ranked Top-1 as `selected` |
| Truncated or incomplete traversal, including an observed provisional Top-1 | `blocked` with `file_lookup_incomplete`; send no file |
| Tool failure or timeout | `blocked` with `file_lookup_failed`; send no media |

The ranker is server-owned and deterministic; the model cannot select a
candidate or invent a path. The contract does not add a confidence threshold
after matching: any complete search with at least one eligible positive
filename match selects its Top-1. A low-scoring match is therefore returned as
the likely file rather than converted into a multi-candidate clarification.
Zero positive matches still produce `file_not_found`.

### Top-1 Selection Semantics

One locator produces at most one frozen response file. Multiple matches are not
a clarification state and are never returned as a candidate list. Explicitly
selected multiple attachments or multiple `media` locators may still produce an
ordered multipart response, with one Top-1 result per locator in input order.
If a selected object changes, becomes unreadable, or fails governance before
result construction, the entire response fails atomically.

## Workspace Media Boundary

Each `media[].path`, and every candidate produced from `media[].name` or
`media[].query`, is interpreted relative to the linked session's effective
workspace root. The request does not carry a workspace root. The binding does
not select or override one.

Ingress rejects malformed locators such as absolute paths, URI schemes, NUL
bytes, and traversal before message persistence. Filesystem resolution and
governance then run atomically across all items in `detect_response_media`. A
node failure freezes `clarify` or `blocked` with no selected resource and no
partial delivery. Together these checks include:

- reject empty locators, absolute paths, URI schemes, NUL bytes, and path
  traversal;
- clean and join the path beneath the effective workspace root;
- resolve the workspace root and candidate through the existing symlink-aware
  workspace guard;
- reject the workspace root itself, directories, sockets, devices, named pipes,
  and symlinks;
- require a regular file whose resolved path remains beneath the same root;
- inspect the file after resolution and derive the content type from governed
  evidence rather than trusting client-supplied MIME or size metadata;
- apply the MCP provider's shared part-count, per-part byte, total-byte, and
  qualified transport-envelope limits before execution;
- reject duplicate resolved files in one request;
- bind the validated file identity to the operation so a later replacement,
  symlink swap, or size change cannot silently alter the delivered object.

The external client supplies exactly one of `path`, `name`, or `query`, plus
optional owner-authored `caption`. It cannot supply `artifact_id`, selection
identity, absolute URI, content type, size, dimensions, hash, disposition,
source, or understanding summary. SparkClaw derives those fields.

Caption is owner-authored conversation text associated with the selected part.
File metadata and extracted content remain untrusted resource evidence and
must not be promoted to owner instructions.

## Ordinary Message Mapping

One schema-valid call creates exactly one durable MCP operation, one user
message, one normalized `MessageEnvelope`, and one Agent run. A valid
workspace-relative locator remains request data until
`detect_response_media` resolves and freezes it; it is not yet authority to
read or return the object.

The mapping is:

| MCP value | SparkClaw value |
|---|---|
| Active binding owner | `MessageEnvelope.OwnerID` |
| Active binding local actor | `MessageAuthorization.PrincipalID` |
| Authenticated external device | typed MCP requester provenance |
| MCP request and idempotency key | stable invocation, operation, message, and run IDs |
| `text` | ordinary text part |
| `media[].path`, `media[].name`, or `media[].query` | typed unresolved current-turn media locator for `detect_response_media` |
| Directly selected Web attachment | sanitized `MessageAttachment.RelPath` plus server-issued artifact identity |
| MCP source endpoint | `ReturnToSource` route |
| Binding-linked session | session and effective workspace root |

The adapter must call the ordinary message entry point with text, typed media
locators, and `MessageIngressContext`. The shared ingress contract must preserve
the distinction between an unresolved MCP locator and an already governed Web
attachment. It must not call the existing bound-leaf entry point, synthesize a
`RouteDecision`, or turn a basename into a `MessageAttachment` before Workflow
execution.

After normalization:

- text-only input participates in normal semantic routing;
- media-only input follows the existing deterministic ordinary media
  publication behavior;
- text-plus-media input uses the same routing projection and resource boundary
  as the equivalent message from another provider;
- supported workflows may produce text or governed media results;
- Policy and approval are derived from the selected local Workflow and effects,
  never from an MCP grant bit.

MCP source provenance does not make a request safer or more privileged. It also
does not force every request into `conversation.answer`; "ordinary
conversation" describes the ingress contract, while SparkClaw remains free to
route an owner request to a supported local Workflow.

## Result Mapping

The shared Delivery Gateway first validates the complete `MessageContent`
against MCP provider limits. The MCP sender then maps the ordered content into
an MCP `CallToolResult`.

| SparkClaw part | MCP `content` block |
|---|---|
| Text | `TextContent` |
| Image | `ImageContent` with base64 `data` and governed `mimeType` |
| Audio | `AudioContent` with base64 `data` and governed `mimeType` |
| Other file | Embedded resource with base64 `blob`, governed `mimeType`, and a non-local synthetic URI |

The synthetic URI identifies the returned operation object. It must not expose
an absolute workspace path, a `file://` URI, host layout, workspace root, or
bearer credential. The file bytes are embedded in the tool result; the client
does not need `resources/read`, and SparkClaw does not advertise the MCP
`resources` capability for this design.

The result also carries a bounded `structuredContent` projection containing
operation ID, terminal/waiting state, result status, part kind, display name,
content type, byte count, and SHA-256. It omits local paths and raw text already
present in content blocks. The serialized structured projection remains in a
text block only where MCP compatibility requires it; it must not duplicate
binary payloads.

Result construction must read bytes from the artifact or workspace object that
was governed for that exact Workflow result. It must not reopen a free-form
path supplied by the client. A missing, changed, oversized, or unreadable
object fails delivery atomically; partial multipart delivery is forbidden.

### Transport Size Qualification

The current MCP Delivery Provider advertises a logical maximum of eight parts
and 4 MiB total content. That value is not by itself an end-to-end binary
guarantee. Base64 expands image, audio, and embedded-file content, and the
Gateway response reader, SecureEnvelope, Relay, and external Access Gateway may
each impose a smaller encoded-message limit.

Implementation must define one tested MCP result-envelope budget. The effective
media limit is the minimum of the shared Delivery limit and every active
transport's limit after JSON, base64, and encryption overhead. It must be
checked against the fully encoded result before the first byte is sent. Until a
production ISCP path is qualified at a larger bound, any result exceeding the
proven bound fails closed with a typed payload-too-large outcome. Raising the
logical Provider limit alone cannot widen this boundary.

## Deferred Operations

The existing durable operation behavior remains:

- `tools/call` requires a binding-scoped idempotency key;
- immediate completion returns the finished result;
- execution that exceeds the immediate wait returns a durable operation;
- `sparkclaw.operation.get`, `sparkclaw.operation.result`, and
  `sparkclaw.operation.cancel` remain binding-scoped;
- replay with the same key and same request fingerprint returns the same
  operation; a different request with the same key is rejected;
- binding revocation rejects new calls and terminates or blocks recoverable
  work according to the existing operation contract.

Standard MCP Tasks remain unadvertised in this version. Control tools must not
be described in the owner UI as additional SparkClaw capabilities.

## Authorization And Binding Simplification

An MCP Access Ticket authorizes one thing: activate the external device as an
ordinary conversation source for the selected owner. It no longer carries a
list of Catalog leaves, route operations, effects, Workflow revisions, Catalog
revision, or `allow_approval` values.

The durable binding retains device and owner identity, status, linked session,
authorization revision, timestamps, and transport-session evidence. It does
not retain leaf grants.

Approval cannot be pre-granted by MCP onboarding. When a locally selected
Workflow requires approval, the normal owner approval record and UI decide the
outcome. The external requester may observe a waiting operation, but cannot
approve it through `sparkclaw.conversation.send` or an operation control tool.

The owner management UI therefore changes from a Catalog grant picker to a
plain statement of scope: the device can send ordinary conversation messages
and receive text/media results associated with that binding. Ticket and binding
revocation remain.

## Security Invariants

- The generic `mcp` connector remains default-off and gates ticket issue,
  redemption, ingress, endpoint visibility, execution, and delivery.
- ISCP or the explicitly enabled direct LAN transport authenticates the external
  device; SparkClaw does not accept requester identity from tool arguments.
- External requester identity and local executor/owner identity remain
  separate in persisted invocation, audit, endpoint, and delivery records.
- The binding fixes the owner and linked session. A call cannot select another
  owner, session, workspace, source endpoint, or return endpoint.
- Exact-index and bounded fallback search are scoped to that one workspace and
  current invocation. They do not create a remotely queryable file index or
  widen the binding.
- Message text, captions, and external metadata are untrusted input. Workspace
  file contents are untrusted evidence.
- Workspace selection never implies arbitrary read access. Only explicitly
  selected, validated files that become result message parts may leave through
  MCP.
- No local path, workspace root, credential, raw access ticket, raw pairing
  ticket, or key material appears in tool definitions, results, logs, audit, or
  stored operations.
- Deadlines, request-size limits, concurrency bounds, operation CAS, and
  delivery size limits remain mandatory.
- Multipart validation and result encoding are atomic. SparkClaw sends all
  governed parts once or none.

## Compatibility And Migration

This is an intentional breaking change to the unshipped external MCP business
surface. It should not keep a compatibility registry for projected leaf tools.

Implementation landed in this order:

1. Add contract tests for the single conversation tool, direct path/attachment
   binding, exact-index then `files.search` fallback, complete/incomplete
   search outcomes, deterministic filename-ranked Top-1 selection, stable
   tie-breaking, frozen resource handoff, ordinary routing, and workspace media
   result encoding.
2. Add one versioned conversation-tool schema and remove remote MCP metadata
   from Catalog eligibility decisions.
3. Replace `MCPBoundRouteRequest` execution with the ordinary message runtime
   entry point carrying typed media locators and governed direct attachments.
4. Replace ticket and binding leaf grants with a single conversation-access
   scope; migrate persisted pre-release records fail closed or revoke them with
   an explicit audit event.
5. Remove leaf tool generation, grant filtering, stale-leaf logic, Catalog
   revision coupling, and bound-leaf audit events.
6. Replace the WebChat Catalog grant picker and leaf-count display with the
   fixed conversation scope.
7. Encode text, image, audio, and other file results as MCP content blocks and
   verify that no local path leaks.
8. Update the unified third-party access design, architecture, integrations,
   messaging, deployment, workflow matrix, protocol schemas, evals, and both
   language mirrors.

Because the previous implementation was not released as a stable external API,
the migration uses a schema-version bump plus fail-closed rejection of old
leaf-grant tickets and bindings. Silent widening of an old leaf-specific
binding into general conversation access is forbidden.

## Deleted Concepts

The implementation has no production dependency on:

- `sparkclaw.route.<leaf>` tool names;
- remote Catalog leaf eligibility or per-leaf projection revision;
- owner-selected MCP route operations or effects;
- MCP `allow_approval` grants;
- deterministic bound-leaf Top-1 routing;
- Catalog revision snapshots in MCP access tickets and bindings;
- stale-leaf filtering and leaf-specific reauthorization;
- an MCP-only `RouteDecision` reason or Agent entry point.
- a remotely callable `files.search`, filename-search, or content-search MCP
  tool.

Catalog and Workflow concepts remain part of SparkClaw's internal routing and
execution architecture. They are deleted only from the external MCP contract.

## Failure Semantics

The adapter fails before creating a message or run for malformed schemas,
missing idempotency, inactive bindings, disabled connectors, absolute/escaping
locator syntax, URI schemes, NUL bytes, or request-envelope limit violations.

After operation creation:

- routing or Workflow setup failure becomes a typed operation failure;
- `detect_response_media` freezes a complete zero-result lookup as `clarify`,
  freezes the filename-ranked Top-1 from a complete non-empty lookup as
  `selected`, and freezes incomplete/failed lookup, unsafe or unsupported
  filesystem objects, duplicates, changed objects, and media-limit violations
  as `blocked`;
- `answer` sends exactly the resources frozen for `selected`, or projects the frozen
  reason for `clarify`/`blocked`, without performing a second lookup;
- owner approval or browser-login waits remain durable waiting states;
- cancellation is terminal and does not promise rollback;
- result-object mutation or disappearance becomes delivery failure;
- unsupported MCP media encoding or provider-limit failure blocks the whole
  result;
- a retry with the same idempotency key observes the same durable outcome.

User-visible errors are bounded and do not reveal filesystem layout, route
candidates, tool catalogs, or credentials. Audit records use typed reason codes.

## Audit Contract

At minimum, audit records cover:

- access ticket issue, redemption, expiration, and revocation;
- binding activation, reconnect, suspension, and revocation;
- business tool listing and invocation;
- workspace media validation outcome with item count, governed content kinds,
  total bytes, and hashes, but no local paths or file content;
- every exact-index and fallback-search attempt with query digest, stage,
  match count, traversal completion/truncation, ranker revision, selected score
  and match reason, tie-break use, and reason code;
- `detect_response_media` decision, current-turn resource roles, and frozen
  selected-resource hashes without absolute paths;
- ordinary route selection and Workflow execution through existing audits;
- operation creation, replay, conflict, wait, cancellation, and terminal state;
- result delivery with content kinds and byte counts;
- every denial using a stable reason code.

The audit vocabulary should use `mcp.conversation.*` and shared message events,
not `mcp.bound_leaf.*`.

## Acceptance Criteria

- An active binding lists exactly one business tool,
  `sparkclaw.conversation.send`; no `sparkclaw.route.*` tool is listed or
  callable.
- Text-only MCP input creates one ordinary message and uses normal semantic
  routing without a server-synthesized route leaf.
- When ordinary conversation is selected, `conversation.answer` revision 3
  executes `detect_response_media` before `answer`; the second node cannot run
  before the first reaches a frozen typed decision.
- Media-only MCP input selects the ordinary conversation Workflow from typed
  content without model routing or synthetic command text, then freezes and
  publishes the governed media through the two-node plan.
- Text-plus-media MCP input has the same normalized owner-text and resource
  projections as an equivalent Web message.
- A direct Web attachment reaches Workflow Runtime as a sanitized
  workspace-relative `workspace_file`/artifact reference, never as an input or
  persisted absolute path.
- Attachment presence alone does not select response media: `send it` selects
  the governed attachment, while `summarize it` treats the same attachment as
  input evidence and routes to the document Workflow.
- Valid relative files under the linked session workspace become governed
  ordered message parts; path traversal, absolute paths, symlinks, directories,
  special files, duplicates, and cross-workspace files fail atomically.
- A direct attachment/path binds first, then the exact basename index runs, and
  only an exact miss or incomplete owner filename invokes one bounded
  `files.search` fallback. Duplicate exact basenames select one file by stable
  workspace-relative lexical tie-break instead of triggering broader search or
  owner choice.
- Fallback compares only filenames, never file content or extracted previews.
  A complete non-empty fallback freezes only the deterministic Top-1 positive
  match. Zero candidates return `file_not_found`; incomplete/truncated lookup
  returns `file_lookup_incomplete`; neither sends a provisional result.
- One locator never expands into a multi-file result. Explicit multiple
  attachments or locators may still produce multipart output with one governed
  file per locator in input order.
- All lookup occurs only in `detect_response_media`. Its `selected` outcome
  freezes ordered resource refs and hashes; `answer` cannot search, add, remove,
  refresh, or substitute them.
- Text such as `把年度报告发给我` may trigger the bounded fallback without a full
  filename; owner-authored descriptions are never converted into invented paths.
- The external client cannot set MIME type, byte count, hash, artifact ID,
  owner, session, workspace root, endpoint, route, operation, effect, Workflow,
  approval, or model lane.
- `tools/list` exposes no `files.search`, resource browser, or other document
  lookup tool. The Workflow may invoke `files.search` internally only after the
  exact stage cannot bind a file, under the frozen workspace/query/budgets.
- Image and audio results use native MCP image/audio content. Other files are
  embedded as resource content without a local path or follow-up read API.
- Binary result bytes match the governed object hash and stay within provider
  and encoded transport-envelope limits; a changed object produces no partial
  result.
- Boundary tests cover the exact largest accepted encoded result and the first
  rejected size for LAN-direct, Bridge/Gateway, SecureEnvelope, Relay, and the
  external Access Gateway; production enablement requires live ISCP evidence.
- Access-ticket and binding projections contain no Catalog revision or leaf
  grants, and the owner UI contains no Catalog grant picker.
- Existing idempotency, deadline, cancellation, restart recovery, connector
  gate, binding revocation, endpoint isolation, Delivery, and audit tests remain
  green.
- Memory, file, and PostgreSQL persistence behave identically for the revised
  ticket, binding, invocation, and operation contracts.
- English and Simplified Chinese documentation, protocol schemas, WebChat
  tests/build, Gateway build/test/vet, Compose validation, doctor, and focused
  external MCP live E2E are green.

## Relationship To Existing Documents

This design replaces the route-leaf projection, leaf-grant, bound Top-1, and
Catalog-coupled business sections of
[Unified third-party ISCP MCP access](unified-third-party-access-design.md).
That document remains authoritative for ISCP pairing, authenticated transport,
MCP Access Ticket redemption, connector management, operation durability,
external requester provenance, and LocalMind legacy removal.

The ordinary multipart behavior and workspace governance defined in
[Messaging and scheduling](messaging-and-scheduling.md) remain authoritative.
The external MCP adapter must consume those contracts rather than restating or
forking them.
