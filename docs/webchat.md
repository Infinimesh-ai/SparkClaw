# WebChat

> Language: English | [简体中文](../zh-cn/docs/webchat.md)

> R3 scope note (2026-09-30): this guide describes the current server-backed UI.
> [Client/backend R3](client-backend-architecture-design.md) instead requires
> ClientStore/ExecutionClient/MailSyncClient: non-mail data stays local, mail
> synchronizes, and the backend retains business decisions. The Web storage adapter
> and desktop embedded-host path each need implementation and separate qualification.

WebChat is the owner-facing control surface for SparkClaw. This guide replaces
the original frontend handoff requirements with the current implemented
responsibilities and extension rules.

## Product Boundary

The React/Vite application presents Gateway state and sends typed owner actions.
Gateway remains authoritative for execution, routing, policy, approval, traces,
persistence, delivery, schedules, and connector bindings. WebChat must not
reimplement those decisions or execute tools directly.

The workbench includes:

- session navigation, chat, streaming responses, uploads, and assistant
  attachments;
- a schedule page whose create button opens a focused requirement dialog,
  alongside the current schedule list and typed edit/delete actions;
- per-session third-party result destination selection on the ordinary message
  composer, including text, uploads, workspace files, and voice drafts;
- microphone selection, live input preview, native record-time transcription,
  optional silence stop, complete-WAV recovery, and explicit insertion into the
  draft;
- tool timeline, approval inbox, memory review, traces, model calls, audits,
  episode summaries, artifacts, evals, status, owner/client settings, connector
  activation and bindings, and policy settings;
- Simplified Chinese and English UI.

## State And Refresh

Startup verifies workbench identity before private reads and subscriptions. The
controlled host-loopback entrance returns `access_mode=local` and requires no
manual token; LAN and independent clients retain bearer login. The UI shows
“Local access” and permits Owner device management without a fake Client ID.
A rejected stored token remains rejected until the user explicitly clears the
current credential and selects local access; this never deletes conversation
history or bypasses a fixed build-time token. Connection and authentication
failures remain visible. Reconnection rereads state without replaying mutations;
language updates occur only after explicit selection. See
[Local WebChat access](local-webchat-access-design.md).

State is separated into global data and active-session data. Authenticated
fetch-based workbench invalidations drive prompt refreshes, with foreground
five-second reconciliation as fallback. Initial connection, overflow and
Gateway restart cause a full resync; stale responses are generation-guarded.

Mutations refresh their affected state immediately: chat send, schedule change,
delivery, approval resolution, memory action, feedback, owner/client/policy
change, connector activation or binding, and eval run.

The Status view consumes Gateway's ordered `resident_services` readiness
projection for Fast, embedding, guard, ASR, and OCR. Each row shows backend,
model, readiness, and the latest persisted call status without reconstructing
service identity or health rules in the browser.

## Typed Control Surfaces

Structured owner actions are not converted back into ambiguous prose:

- the schedule page keeps its compact create button; clicking it opens a modal
  requirement editor, and submission posts directly to `/api/schedules`;
  Agent Runtime selects `schedule.manage#create` without generic intent routing
  and uses a hidden schedule context, while preserving guards, Workflow
  execution, audit, in-place results, and projection refresh without creating or
  navigating to a chat; successful creation closes the modal, while
  clarification keeps the requirement editable;
- schedule toolbar actions submit `schedule_action` with selected ID and
  observed `updated_at`; Agent Runtime validates and executes the registered
  Workflow;
- the delivery target picker submits one optional opaque `target_endpoint_id`
  with the ordinary session message; it does not change or duplicate the
  composer, attachments, streaming, routing, or Workflow path; an attachment-only
  send submits empty text and lets Message Runtime route the typed media parts;
- ordinary tool approval modifications validate JSON and keep verifier-owned
  fields read-only; Happy Team plan approvals render typed task/goal/plan data
  and submit only edited plan text, never raw remote tool arguments;
- workspace files are uploaded and fetched through authenticated document APIs;
- speech capture acquires the microphone before reserving realtime ASR, then
  starts one AudioWorklet only after the realtime session is ready. Its stateful
  resampler feeds the same 16 kHz PCM16 to revisioned live preview, the optional
  Off-by-default silence detector, and the retained complete-WAV fallback. A
  setup failure starts a visibly batch-only recording. Any failure after live
  capture starts immediately stops and flushes capture, releases the realtime
  session, and automatically batch-transcribes the retained WAV without
  resuming recording. Device loss follows the same retained-audio path. A
  retryable batch failure keeps the byte-identical WAV for up to five minutes;
  a changed draft produces an explicit insert-at-cursor candidate. Partial text
  never mutates the draft, and transcription never calls message send.
- connector settings render the registered channel list from `/api/connectors`;
  a versioned toggle changes activation, while credential or QR binding remains
  a separate action that is unavailable until the channel is enabled.
- External MCP access records keep revocation separate from permanent record
  deletion. Owners can delete any ticket or binding, including expired,
  consumed, or revoked records, or delete all owner-scoped access records at
  once; deleting an active binding invalidates that access immediately. Inbound
  MCP request sessions are never listed in WebChat and are deleted with their
  session-scoped execution records as soon as the operation reaches a terminal
  state.

The UI shows reminder and delivery destinations as concrete software, account,
recipient, conversation, and status values supplied by Gateway. It never
defaults an unavailable third-party destination to WebChat.

## API Ownership

The typed client and response contracts live in:

```text
apps/webchat/src/api/client.ts
apps/webchat/src/api/types.ts
```

Gateway routes and public projections live in
`services/gateway/internal/gateway`. The frontend should consume those typed
projections instead of reading Store records or reconstructing backend rules.

Important API groups include sessions/messages/events, schedules, delivery
endpoints, connector settings, notification bindings, speech,
documents, approvals, memory, traces, artifacts, evals, owner/client settings,
config, and policy.

## UX And Safety Rules

- Present a quiet operational workbench, not a marketing page.
- Keep risky, pending, failed, unavailable, and unknown-outcome states visible.
- Disable Happy plan approval and editing while the live plan is unavailable;
  keep rejection available and show the retry state without treating plan text
  as UI instructions.
- Suppress the source-WebChat assistant result for ordinary pure-media
  publications sent to a selected third-party endpoint; those explicit sends do
  not require approval. Keep text-only and other third-party Workflow results
  behind Gateway-owned send approval. Explicit direct-send API clients still
  require confirmation for send and retry.
- Preserve API IDs, paths, tool names, user text, and model output exactly; do
  not translate or normalize them for display.
- Localize static UI copy only. Persist the selected language.
- All icon-only controls need accessible labels, tooltips, and visible focus.
- Long paths, IDs, model names, endpoint labels, and JSON must wrap or truncate
  without horizontal page overflow.
- Mobile layout must keep navigation, conversation, composer, task controls,
  and inspector panels reachable without overlap.
- Microphone device IDs remain browser-local. Raw audio is held only by the
  active recording or retry operation and is never written to browser storage.

## Development

Install dependencies and start the development server:

```bash
npm install
npm --workspace @sparkclaw/webchat run dev
```

The dev server listens on port `18790` and proxies Gateway requests to the local
Gateway. Keep API requests in the shared client, shared domain types in
`api/types.ts`, and focused behavior in feature/component modules. Avoid adding
state or localization dependencies unless current complexity justifies them.

## Verification

```bash
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
```

For user-visible changes, also run Gateway contract tests for the touched API
and inspect desktop/mobile layouts against a running local Gateway. Verify
loading, empty, offline, unauthorized, disabled, pending, successful, failed,
and approval states, plus long Chinese/English labels and multipart attachments
with both Web and third-party targets.
