# WebChat

> Language: English | [简体中文](../zh-cn/docs/webchat.md)

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
- task toolbar with current schedules and typed edit/delete actions;
- explicit third-party destination selection, multipart review, delivery
  history, and safe retry;
- microphone transcription into the draft;
- tool timeline, approval inbox, memory review, traces, model calls, audits,
  episode summaries, artifacts, evals, status, owner/client settings, connector
  bindings, and policy settings;
- Simplified Chinese and English UI.

## State And Refresh

Startup loads readiness first, then authentication and private state. Bearer
tokens come from `VITE_SPARKCLAW_API_TOKEN` or the local token flow. Pairing and
token failures remain visible; language switching does not depend on Gateway
availability.

State is separated into global data and active-session data. Session events
drive prompt refreshes when possible, with bounded polling as fallback. Native
`EventSource` cannot attach bearer headers, so authenticated mode uses the
implemented compatible path rather than opening an unauthenticated stream.

Mutations refresh their affected state immediately: chat send, schedule change,
delivery, approval resolution, memory action, feedback, owner/client/policy
change, connector binding, and eval run.

## Typed Control Surfaces

Structured owner actions are not converted back into ambiguous prose:

- schedule toolbar actions submit `schedule_action` with selected ID and
  observed `updated_at`; Agent Runtime validates and executes the registered
  Workflow;
- direct sends submit one opaque endpoint, ordered message parts, confirmation,
  and idempotency key to `/api/deliveries`;
- approval modifications validate JSON and keep verifier-owned fields read-only;
- workspace files are uploaded and fetched through authenticated document APIs;
- speech transcription returns text to the draft and never calls message send.

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
endpoints/deliveries, notification bindings, speech, documents, approvals,
memory, traces, artifacts, evals, owner/client settings, config, and policy.

## UX And Safety Rules

- Present a quiet operational workbench, not a marketing page.
- Keep risky, pending, failed, unavailable, and unknown-outcome states visible.
- Require explicit review for third-party direct sends and explicit confirmation
  for retry.
- Preserve API IDs, paths, tool names, user text, and model output exactly; do
  not translate or normalize them for display.
- Localize static UI copy only. Persist the selected language.
- All icon-only controls need accessible labels, tooltips, and visible focus.
- Long paths, IDs, model names, endpoint labels, and JSON must wrap or truncate
  without horizontal page overflow.
- Mobile layout must keep navigation, conversation, composer, task controls,
  and inspector panels reachable without overlap.

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
and retry states, plus long Chinese/English labels and multipart attachments.
